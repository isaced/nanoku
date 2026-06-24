package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/db/user"
)

func seedUser(t *testing.T, d *db.DB, username, password string) *db.User {
	t.Helper()
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	u, err := d.User.Create().
		SetUsername(username).
		SetPasswordHash(hash).
		SetRole("admin").
		Save(context.Background())
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func TestAuthenticateAndSeed(t *testing.T) {
	d := newTestDB(t)

	if err := SeedFirstAdmin(context.Background(), d, "admin", "secret-123"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// second call must be a no-op
	if err := SeedFirstAdmin(context.Background(), d, "admin2", "another"); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	count, _ := d.User.Query().Count(context.Background())
	if count != 1 {
		t.Fatalf("user count = %d, want 1", count)
	}

	// empty creds: noop
	if err := SeedFirstAdmin(context.Background(), d, "", ""); err != nil {
		t.Fatalf("empty seed: %v", err)
	}
	count, _ = d.User.Query().Count(context.Background())
	if count != 1 {
		t.Fatalf("after empty seed, count = %d, want 1", count)
	}

	u, err := Authenticate(context.Background(), d, "admin", "secret-123")
	if err != nil {
		t.Fatalf("auth ok: %v", err)
	}
	if u.LastLoginAt == nil {
		t.Fatal("last_login_at not set")
	}
	if _, err := Authenticate(context.Background(), d, "admin", "wrong"); err != ErrInvalidCredentials {
		t.Fatalf("auth wrong pass = %v, want ErrInvalidCredentials", err)
	}
	if _, err := Authenticate(context.Background(), d, "nobody", "x"); err != ErrInvalidCredentials {
		t.Fatalf("auth unknown user = %v, want ErrInvalidCredentials", err)
	}
}

func TestSessionStore_RoundTrip(t *testing.T) {
	d := newTestDB(t)
	u := seedUser(t, d, "alice", "pass-1234")

	store := NewSessionStore(d)
	ctx := context.Background()
	token, err := store.Create(ctx, u)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if token == "" {
		t.Fatal("empty token")
	}

	got, err := store.Lookup(ctx, token)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got.ID != u.ID {
		t.Fatalf("user id = %d, want %d", got.ID, u.ID)
	}

	// sliding expiry: future-dated session is still valid
	now := time.Now().UTC()
	future, _ := d.Session.Query().First(ctx)
	if future == nil {
		t.Fatal("no session row")
	}
	d.Session.UpdateOneID(future.ID).SetExpiresAt(now.Add(time.Hour)).SetLastSeen(now).ExecX(ctx)
	got, err = store.Lookup(ctx, token)
	if err != nil {
		t.Fatalf("lookup after rewrite: %v", err)
	}

	// expired → not found + row deleted
	d.Session.UpdateOneID(future.ID).SetExpiresAt(now.Add(-time.Hour)).ExecX(ctx)
	if _, err := store.Lookup(ctx, token); err == nil {
		t.Fatal("expected error on expired lookup")
	}
	exists, _ := d.Session.Query().Exist(ctx)
	if exists {
		t.Fatal("expired session row was not deleted")
	}

	// Delete on unknown token is silent no-op
	if err := store.Delete(ctx, "not-a-real-token"); err != nil {
		t.Fatalf("delete unknown: %v", err)
	}
}

func TestSessionStore_RateLimit(t *testing.T) {
	d := newTestDB(t)
	store := NewSessionStore(d)
	for i := 0; i < 5; i++ {
		if !store.AllowLogin("1.2.3.4") {
			t.Fatalf("attempt %d should be allowed", i+1)
		}
	}
	if store.AllowLogin("1.2.3.4") {
		t.Fatal("6th attempt should be blocked")
	}
	// different IP is unaffected
	if !store.AllowLogin("5.6.7.8") {
		t.Fatal("different IP should be allowed")
	}
}

// TestSessionStore_RateLimit_NoAliasing is a regression guard for the
// `kept := log[:0]` bug. The earlier implementation reused the input slice's
// backing array, so the in-place append silently overwrote entries while the
// loop was still reading — aliasing damage accumulated across calls and the
// limit triggered one call earlier than it should have. We pre-seed 2 recent
// + 3 expired entries and expect exactly 3 allowed calls before rejection.
func TestSessionStore_RateLimit_NoAliasing(t *testing.T) {
	d := newTestDB(t)
	store := NewSessionStore(d)
	store.mu.Lock()
	old := time.Now().Add(-2 * time.Minute)
	recent := time.Now().Add(-10 * time.Second)
	store.attempts["9.9.9.9"] = []time.Time{old, recent, old, recent, old}
	store.mu.Unlock()

	// 2 recents survive the cutoff. After call 1, stored=3; call 2, stored=4;
	// call 3, stored=5; call 4 has 5 recents inside the window → reject.
	for i := 1; i <= 3; i++ {
		if !store.AllowLogin("9.9.9.9") {
			t.Fatalf("call %d should be allowed", i)
		}
	}
	if store.AllowLogin("9.9.9.9") {
		t.Fatal("4th call should be blocked (5 recents within window)")
	}
}

// TestSessionStore_PurgeStaleAttempts verifies that an IP whose attempt
// log has fully expired (no entries within the last minute) is removed
// from the attempts map, so an attacker walking source IPs can't
// accumulate unbounded map entries.
func TestSessionStore_PurgeStaleAttempts(t *testing.T) {
	d := newTestDB(t)
	store := NewSessionStore(d)
	store.mu.Lock()
	store.attempts["stale.ip"] = []time.Time{time.Now().Add(-5 * time.Minute)}
	store.attempts["fresh.ip"] = []time.Time{time.Now()}
	store.mu.Unlock()

	store.PurgeStaleAttempts()

	store.mu.Lock()
	defer store.mu.Unlock()
	if _, ok := store.attempts["stale.ip"]; ok {
		t.Error("stale.ip should have been purged")
	}
	if _, ok := store.attempts["fresh.ip"]; !ok {
		t.Error("fresh.ip should still be present")
	}
}

// doJSON performs an HTTP request through the supplied handler and returns
// the response recorder.
func doJSON(h http.Handler, method, path string, body any, cookie *http.Cookie) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	r := httptest.NewRequest(method, path, &buf)
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestLoginFlow_HappyPath(t *testing.T) {
	d := newTestDB(t)
	seedUser(t, d, "admin", "pass-1234")
	store := NewSessionStore(d)
	h := Handlers{DB: d, Sessions: store}
	login := http.HandlerFunc(h.Login)
	me := SessionAuth(store)(http.HandlerFunc(h.Me))
	logout := http.HandlerFunc(h.Logout)

	// unauthenticated /api/me → 401
	if w := doJSON(me, "GET", "/api/me", nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("unauthed me status = %d, want 401", w.Code)
	}

	// wrong password
	if w := doJSON(login, "POST", "/api/login", LoginRequest{Username: "admin", Password: "wrong"}, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong pass status = %d, want 401", w.Code)
	}

	// good login → 200 + cookie set
	w := doJSON(login, "POST", "/api/login", LoginRequest{Username: "admin", Password: "pass-1234"}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200, body=%s", w.Code, w.Body.String())
	}
	var resp LoginResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("login response decode: %v", err)
	}
	if resp.Username != "admin" {
		t.Fatalf("username = %q, want admin", resp.Username)
	}
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("session cookie not set")
	}
	if !cookie.HttpOnly {
		t.Error("cookie not HttpOnly")
	}
	if cookie.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict", cookie.SameSite)
	}
	if cookie.Secure {
		t.Error("Secure should be false on plaintext test request")
	}

	// /api/me with the cookie → 200
	w = doJSON(me, "GET", "/api/me", nil, cookie)
	if w.Code != http.StatusOK {
		t.Fatalf("authed me status = %d, want 200", w.Code)
	}

	// logout → 204 + clear cookie
	w = doJSON(logout, "POST", "/api/logout", nil, cookie)
	if w.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", w.Code)
	}
	cleared := false
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("logout did not clear session cookie")
	}

	// the deleted session row is gone
	if _, err := store.Lookup(context.Background(), cookie.Value); err == nil {
		t.Fatal("lookup after logout should fail")
	}
}

func TestChangePassword(t *testing.T) {
	d := newTestDB(t)
	seedUser(t, d, "admin", "oldpass-1234")
	store := NewSessionStore(d)
	h := Handlers{DB: d, Sessions: store}
	login := http.HandlerFunc(h.Login)
	changePw := SessionAuth(store)(http.HandlerFunc(h.ChangePassword))

	// login to get authed context
	w := doJSON(login, "POST", "/api/login", LoginRequest{Username: "admin", Password: "oldpass-1234"}, nil)
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookieName {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no session cookie")
	}

	// weak new password rejected
	w = doJSON(changePw, "POST", "/api/me/password", map[string]string{
		"oldPassword": "oldpass-1234", "newPassword": "short",
	}, cookie)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("weak new pass status = %d, want 400", w.Code)
	}

	// wrong old password rejected
	w = doJSON(changePw, "POST", "/api/me/password", map[string]string{
		"oldPassword": "WRONG", "newPassword": "newpass-1234",
	}, cookie)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong old pass status = %d, want 401", w.Code)
	}

	// happy path
	w = doJSON(changePw, "POST", "/api/me/password", map[string]string{
		"oldPassword": "oldpass-1234", "newPassword": "newpass-1234",
	}, cookie)
	if w.Code != http.StatusNoContent {
		t.Fatalf("change status = %d, want 204, body=%s", w.Code, w.Body.String())
	}

	// verify hash changed in DB
	u, _ := d.User.Query().Where(user.Username("admin")).Only(context.Background())
	if u.PasswordHash == "" {
		t.Fatal("hash cleared")
	}
	// old pass no longer works
	if _, err := Authenticate(context.Background(), d, "admin", "oldpass-1234"); err != ErrInvalidCredentials {
		t.Fatalf("auth with old pass = %v, want ErrInvalidCredentials", err)
	}
	// new pass works
	if _, err := Authenticate(context.Background(), d, "admin", "newpass-1234"); err != nil {
		t.Fatalf("auth with new pass: %v", err)
	}

	// old session was deleted by change-password
	if _, err := store.Lookup(context.Background(), cookie.Value); err == nil {
		t.Fatal("old session should be invalid after password change")
	}
}

func TestSessionAuth_ClearsStaleCookie(t *testing.T) {
	d := newTestDB(t)
	seedUser(t, d, "admin", "pass-1234")
	store := NewSessionStore(d)

	protected := SessionAuth(store)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := UserFromContext(r.Context())
		if u == nil {
			t.Fatal("user missing in context")
		}
		w.WriteHeader(http.StatusOK)
	}))

	stale := &http.Cookie{Name: sessionCookieName, Value: "definitely-not-a-real-token"}
	w := doJSON(protected, "GET", "/api/sites", nil, stale)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("stale token status = %d, want 401", w.Code)
	}
	cleared := false
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookieName && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("stale cookie should have been cleared on 401")
	}
}