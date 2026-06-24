package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/db/app"
)

const (
	testAppName    = "myapp"
	testToken      = "shhh-this-is-the-token"
	testImage      = "ghcr.io/isaced/myapp"
	testDeployTag  = "abc1234"
)

func newTestHandlers(t *testing.T, withToken bool) (*Handlers, *db.App) {
	t.Helper()
	d := newTestDB(t)
	ctx := context.Background()

	tok := ""
	if withToken {
		tok = testToken
	}
	a, err := d.App.Create().
		SetName(testAppName).
		SetImage(testImage + ":latest").
		SetPort(8080).
		SetTriggerToken(tok).
		Save(ctx)
	if err != nil {
		t.Fatalf("seed app: %v", err)
	}

	return &Handlers{
		DB:            d,
		DeployLock:    NewDeployLock(),
		CaddyfilePath: filepath.Join(t.TempDir(), "Caddyfile"),
	}, a
}

func postTrigger(t *testing.T, h *Handlers, appName string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/apps/"+appName+"/trigger", bytes.NewReader(body))
	req.SetPathValue("name", appName)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.Trigger(w, req)
	return w
}

func bearer(t *testing.T, secret string) string {
	t.Helper()
	return "Bearer " + secret
}

// --- Auth ----------------------------------------------------------------

func TestTrigger_RejectsMissingAuth(t *testing.T) {
	h, _ := newTestHandlers(t, true)
	body := []byte(`{"tag":"abc"}`)
	w := postTrigger(t, h, testAppName, body, nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestTrigger_RejectsNonBearerScheme(t *testing.T) {
	h, _ := newTestHandlers(t, true)
	body := []byte(`{"tag":"abc"}`)
	cases := []string{
		"Basic " + testToken,          // wrong scheme
		"Token " + testToken,          // wrong scheme
		testToken,                     // no scheme
		"Bearer",                      // scheme only, no token
		"Bearer ",                     // scheme + empty
		"bearer " + testToken,         // lowercase rejected (case-sensitive)
	}
	for _, h2 := range cases {
		t.Run(h2, func(t *testing.T) {
			w := postTrigger(t, h, testAppName, body, map[string]string{
				authHeader: h2,
			})
			if w.Code != http.StatusUnauthorized {
				t.Errorf("auth %q: status = %d, want 401", h2, w.Code)
			}
		})
	}
}

func TestTrigger_RejectsWrongToken(t *testing.T) {
	h, _ := newTestHandlers(t, true)
	body := []byte(`{"tag":"abc"}`)
	w := postTrigger(t, h, testAppName, body, map[string]string{
		authHeader: bearer(t, "wrong-token"),
	})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestTrigger_RejectsTamperedToken(t *testing.T) {
	h, _ := newTestHandlers(t, true)
	body := []byte(`{"tag":"abc"}`)
	// Same length as testToken, single-char diff — should still 401
	bad := testToken
	if bad[len(bad)-1] == 'n' {
		bad = bad[:len(bad)-1] + "x"
	} else {
		bad = bad[:len(bad)-1] + "n"
	}
	w := postTrigger(t, h, testAppName, body, map[string]string{
		authHeader: bearer(t, bad),
	})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (tampered token)", w.Code)
	}
}

func TestTrigger_RejectsUnknownApp(t *testing.T) {
	h, _ := newTestHandlers(t, true)
	body := []byte(`{"tag":"abc"}`)
	w := postTrigger(t, h, "nope", body, map[string]string{
		authHeader: bearer(t, testToken),
	})
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestTrigger_RejectsAppWithoutToken(t *testing.T) {
	h, _ := newTestHandlers(t, false)
	body := []byte(`{"tag":"abc"}`)
	w := postTrigger(t, h, testAppName, body, map[string]string{
		authHeader: bearer(t, testToken),
	})
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (token missing)", w.Code)
	}
}

// --- Input validation ----------------------------------------------------

func TestTrigger_RejectsInvalidTag(t *testing.T) {
	h, _ := newTestHandlers(t, true)
	cases := []string{
		"",
		" ",
		"contains space",
		"contains\nnewline",
		"contains:colon",
		"contains;semicolon",
		"$(rm -rf /)",
		"--flag",
	}
	for _, tag := range cases {
		t.Run(tag, func(t *testing.T) {
			body, _ := json.Marshal(triggerPayload{Tag: tag})
			w := postTrigger(t, h, testAppName, body, map[string]string{
				authHeader: bearer(t, testToken),
			})
			if w.Code != http.StatusBadRequest {
				t.Errorf("tag %q: status = %d, want 400", tag, w.Code)
			}
		})
	}
}

func TestTrigger_RejectsOversizedBody(t *testing.T) {
	h, _ := newTestHandlers(t, true)
	body := bytes.Repeat([]byte("a"), maxTriggerBody+1)
	w := postTrigger(t, h, testAppName, body, map[string]string{
		authHeader: bearer(t, testToken),
	})
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", w.Code)
	}
}

func TestTrigger_RejectsMalformedJSON(t *testing.T) {
	h, _ := newTestHandlers(t, true)
	body := []byte(`{not valid json`)
	w := postTrigger(t, h, testAppName, body, map[string]string{
		authHeader: bearer(t, testToken),
	})
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestTrigger_RejectsAppWithEmptyImage(t *testing.T) {
	h, _ := newTestHandlers(t, true)
	// Clear the image so resolveTriggerImage fails.
	_, _ = h.DB.App.Update().Where(app.NameEQ(testAppName)).ClearImage().Save(context.Background())

	body, _ := json.Marshal(triggerPayload{Tag: "v1"})
	w := postTrigger(t, h, testAppName, body, map[string]string{
		authHeader: bearer(t, testToken),
	})
	if w.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", w.Code)
	}
}

// --- Image assembly ------------------------------------------------------

func TestResolveTriggerImage_StripsExistingTag(t *testing.T) {
	// We don't need the handlers — we just need a fresh App entity we can
	// mutate. Build one directly.
	d := newDBClient(t)
	appEnt, err := d.App.Create().
		SetName(testAppName).
		SetImage(testImage).
		SetPort(80).
		SetTriggerToken(testToken).
		Save(context.Background())
	if err != nil {
		t.Fatalf("create app: %v", err)
	}

	cases := []struct {
		image string
		tag   string
		want  string
	}{
		{"ghcr.io/me/myapp:v1", "abc", "ghcr.io/me/myapp:abc"},
		{"ghcr.io/me/myapp", "abc", "ghcr.io/me/myapp:abc"},
		{"nginx", "1.27", "nginx:1.27"},
		{"registry.local:5000/app:v1", "abc", "registry.local:5000/app:abc"},
		{"registry.local:5000/app", "abc", "registry.local:5000/app:abc"},
	}
	for _, c := range cases {
		t.Run(c.image, func(t *testing.T) {
			appEnt.Image = &c.image
			got, err := resolveTriggerImage(appEnt, c.tag)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// --- Happy path & concurrency -------------------------------------------

func TestTrigger_AcceptedHappyPath(t *testing.T) {
	h, _ := newTestHandlers(t, true)
	body, _ := json.Marshal(triggerPayload{Tag: testDeployTag, CommitMessage: "fix: hello"})
	w := postTrigger(t, h, testAppName, body, map[string]string{
		authHeader: bearer(t, testToken),
	})
	if w.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", w.Code, w.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response body parse: %v", err)
	}
	if accepted, _ := resp["accepted"].(bool); !accepted {
		t.Errorf("expected accepted=true, got %v", resp)
	}
	if resp["tag"] != testDeployTag {
		t.Errorf("tag = %v, want %s", resp["tag"], testDeployTag)
	}
	if resp["image"] != testImage+":"+testDeployTag {
		t.Errorf("image = %v, want %s:%s", resp["image"], testImage, testDeployTag)
	}

	waitForDeployTerminal(t, h.DB, 1, []string{"success", "failed"})

	dep, err := h.DB.Deploy.Get(context.Background(), 1)
	if err != nil {
		t.Fatalf("read deploy: %v", err)
	}
	if string(dep.Status) != "failed" {
		t.Errorf("status = %s, want failed (Docker=nil)", dep.Status)
	}
	if dep.Error == nil || !strings.Contains(*dep.Error, "docker unavailable") {
		t.Errorf("error = %v, want docker unavailable", dep.Error)
	}
	if dep.CommitSha == nil || *dep.CommitSha != testDeployTag {
		t.Errorf("commit_sha = %v, want %s", dep.CommitSha, testDeployTag)
	}
	if string(dep.Trigger) != "trigger" {
		t.Errorf("trigger = %s, want trigger", dep.Trigger)
	}
}

func TestTrigger_BusyLockReturnsIgnored(t *testing.T) {
	h, _ := newTestHandlers(t, true)
	if !h.DeployLock.TryAcquire(1) {
		t.Fatal("setup: failed to acquire lock")
	}
	defer h.DeployLock.Release(1)

	body, _ := json.Marshal(triggerPayload{Tag: testDeployTag})
	w := postTrigger(t, h, testAppName, body, map[string]string{
		authHeader: bearer(t, testToken),
	})
	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", w.Code)
	}
	if !strings.Contains(w.Body.String(), "another deploy") {
		t.Errorf("body should mention another deploy in flight: %s", w.Body.String())
	}
}

// waitForDeployTerminal polls Deploy status until it's one of `want` or the
// deadline elapses. Goroutine-driven deploys need this because the response
// is 202 before the deploy actually finishes.
func waitForDeployTerminal(t *testing.T, dbConn *db.DB, id int, want []string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		d, err := dbConn.Deploy.Get(context.Background(), id)
		if err == nil {
			s := string(d.Status)
			for _, w := range want {
				if s == w {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("deploy %d never reached terminal status %v (last err=%v)", id, want, err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestVerifyToken(t *testing.T) {
	good := "topsecret"

	if !verifyToken(good, bearer(t, good)) {
		t.Error("good bearer should verify")
	}
	if verifyToken(good, "") {
		t.Error("empty header should fail")
	}
	if verifyToken(good, "Bearer") {
		t.Error("missing token should fail")
	}
	if verifyToken(good, "Bearer ") {
		t.Error("empty token should fail")
	}
	if verifyToken(good, "Basic "+good) {
		t.Error("Basic scheme should fail")
	}
	if verifyToken(good, "Bearer wrong-token-but-same-length-but-still-wrong") {
		// length differs to avoid the "length mismatch early-out" in
		// ConstantTimeCompare; just confirms any wrong token fails.
		t.Error("wrong token should fail")
	}
	if verifyToken("wrong", bearer(t, good)) {
		t.Error("expected-token mismatch should fail")
	}
}

// newDBClient returns a fresh DB handle for resolveTriggerImage tests, which
// need to set Image directly on a fresh entity.
func newDBClient(t *testing.T) *db.DB {
	t.Helper()
	return newTestDB(t)
}
