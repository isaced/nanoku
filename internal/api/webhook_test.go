package api

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"modernc.org/sqlite"

	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/db/app"
	"github.com/isaced/nanoku/internal/db/enttest"
)

var registerOnce sync.Once

func TestMain(m *testing.M) {
	registerOnce.Do(func() {
		sql.Register("sqlite3", &sqlite.Driver{})
	})
	os.Exit(m.Run())
}

const (
	testAppName     = "myapp"
	testWebhookKey  = "shhh-this-is-the-secret"
	testImageRepo   = "ghcr.io/isaced/myapp"
	testDeployTag   = "abc1234"
)

func newTestHandlers(t *testing.T, withSecret bool) (*Handlers, *db.App) {
	t.Helper()
	client := enttest.Open(t, "sqlite3", "file:webhook_test?mode=memory&_fk=1&_pragma=foreign_keys(1)")
	ctx := context.Background()

	secret := ""
	if withSecret {
		secret = testWebhookKey
	}
	a, err := client.App.Create().
		SetName(testAppName).
		SetImage("nginx:1.27").
		SetPort(8080).
		SetImageRepo(testImageRepo).
		SetWebhookSecret(secret).
		Save(ctx)
	if err != nil {
		t.Fatalf("seed app: %v", err)
	}

	return &Handlers{
		DB:            &db.DB{Client: client},
		DeployLock:    NewDeployLock(),
		CaddyfilePath: filepath.Join(t.TempDir(), "Caddyfile"),
	}, a
}

func signBody(t *testing.T, secret string, body []byte) string {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func postWebhook(t *testing.T, h *Handlers, appName string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/webhook/"+appName, bytes.NewReader(body))
	req.SetPathValue("name", appName)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.Webhook(w, req)
	return w
}

func TestWebhook_RejectsMissingSignature(t *testing.T) {
	h, _ := newTestHandlers(t, true)
	body := []byte(`{"tag":"abc"}`)
	w := postWebhook(t, h, testAppName, body, nil)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestWebhook_RejectsBadSignature(t *testing.T) {
	h, _ := newTestHandlers(t, true)
	body := []byte(`{"tag":"abc"}`)
	w := postWebhook(t, h, testAppName, body, map[string]string{
		githubSignatureHeader: "sha256=deadbeef",
	})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestWebhook_RejectsWrongSecret(t *testing.T) {
	h, _ := newTestHandlers(t, true)
	body := []byte(`{"tag":"abc"}`)
	w := postWebhook(t, h, testAppName, body, map[string]string{
		githubSignatureHeader: signBody(t, "wrong-secret", body),
	})
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestWebhook_RejectsUnknownApp(t *testing.T) {
	h, _ := newTestHandlers(t, true)
	body := []byte(`{"tag":"abc"}`)
	w := postWebhook(t, h, "nope", body, map[string]string{
		githubSignatureHeader: signBody(t, testWebhookKey, body),
	})
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestWebhook_RejectsAppWithoutImageRepo(t *testing.T) {
	h, _ := newTestHandlers(t, true)
	ctx := context.Background()
	_, _ = h.DB.App.Update().Where(app.NameEQ(testAppName)).ClearImageRepo().Save(ctx)
	body := []byte(`{"tag":"abc"}`)
	w := postWebhook(t, h, testAppName, body, map[string]string{
		githubSignatureHeader: signBody(t, testWebhookKey, body),
	})
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (image_repo missing)", w.Code)
	}
}

func TestWebhook_RejectsAppWithoutSecret(t *testing.T) {
	h, _ := newTestHandlers(t, false)
	body := []byte(`{"tag":"abc"}`)
	w := postWebhook(t, h, testAppName, body, map[string]string{
		githubSignatureHeader: signBody(t, testWebhookKey, body),
	})
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 (secret missing)", w.Code)
	}
}

func TestWebhook_RejectsInvalidTag(t *testing.T) {
	h, _ := newTestHandlers(t, true)
	cases := []string{
		"",
		" ",
		"contains space",
		"contains\nnewline",
		"contains:colon",
		"contains;semicolon",
	}
	for _, tag := range cases {
		t.Run(tag, func(t *testing.T) {
			body, _ := json.Marshal(webhookPayload{Tag: tag})
			w := postWebhook(t, h, testAppName, body, map[string]string{
				githubSignatureHeader: signBody(t, testWebhookKey, body),
			})
			if w.Code != http.StatusBadRequest {
				t.Errorf("tag %q: status = %d, want 400", tag, w.Code)
			}
		})
	}
}

func TestWebhook_RejectsOversizedBody(t *testing.T) {
	h, _ := newTestHandlers(t, true)
	body := bytes.Repeat([]byte("a"), maxWebhookBody+1)
	w := postWebhook(t, h, testAppName, body, map[string]string{
		githubSignatureHeader: signBody(t, testWebhookKey, body),
	})
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", w.Code)
	}
}

func TestWebhook_IgnoresNonPushEvent(t *testing.T) {
	h, _ := newTestHandlers(t, true)
	body := []byte(`{"tag":"abc"}`)
	w := postWebhook(t, h, testAppName, body, map[string]string{
		githubSignatureHeader: signBody(t, testWebhookKey, body),
		githubEventHeader:     "ping",
	})
	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", w.Code)
	}
	if !strings.Contains(w.Body.String(), "ignored") {
		t.Errorf("body should mention ignored: %s", w.Body.String())
	}
	if h.DeployLock.IsBusy(1) {
		t.Error("non-push event must not acquire deploy lock")
	}
}

func TestWebhook_AcceptedHappyPath(t *testing.T) {
	h, _ := newTestHandlers(t, true)
	body, _ := json.Marshal(webhookPayload{Tag: testDeployTag, CommitMessage: "fix: hello"})
	w := postWebhook(t, h, testAppName, body, map[string]string{
		githubSignatureHeader: signBody(t, testWebhookKey, body),
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

	// Lock should be held until the goroutine releases it (Docker=nil will
	// quickly mark the deploy failed and release).
	if !h.DeployLock.IsBusy(1) {
		// Could already be released if goroutine was extremely fast.
		t.Log("lock already released; goroutine finished during request")
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
	if dep.Trigger != "webhook" {
		t.Errorf("trigger = %s, want webhook", dep.Trigger)
	}
}

func TestWebhook_BusyLockReturnsIgnored(t *testing.T) {
	h, _ := newTestHandlers(t, true)
	if !h.DeployLock.TryAcquire(1) {
		t.Fatal("setup: failed to acquire lock")
	}
	defer h.DeployLock.Release(1)

	body, _ := json.Marshal(webhookPayload{Tag: testDeployTag})
	w := postWebhook(t, h, testAppName, body, map[string]string{
		githubSignatureHeader: signBody(t, testWebhookKey, body),
	})
	if w.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", w.Code)
	}
	if !strings.Contains(w.Body.String(), "another deploy") {
		t.Errorf("body should mention another deploy in flight: %s", w.Body.String())
	}
}

// waitForDeployTerminal polls Deploy status until it's one of `want` or the
// deadline elapses. Goroutine-driven deploys need this because the
// response is 202 before the deploy actually finishes.
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

func TestVerifySignature(t *testing.T) {
	secret := "topsecret"
	body := []byte(`{"tag":"abc"}`)

	good := signBody(t, secret, body)
	if !verifySignature(secret, body, good) {
		t.Error("good signature should verify")
	}

	if verifySignature(secret, body, "") {
		t.Error("empty header should fail")
	}
	if verifySignature(secret, body, "sha1="+good[7:]) {
		t.Error("sha1 prefix should fail")
	}
	if verifySignature(secret, body, good[:len(good)-2]+"00") {
		t.Error("tampered hex should fail")
	}
	if verifySignature("wrong", body, good) {
		t.Error("wrong secret should fail")
	}
	if verifySignature(secret, []byte(`{"tag":"xyz"}`), good) {
		t.Error("body mismatch should fail")
	}
}
