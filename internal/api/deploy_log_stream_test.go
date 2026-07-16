package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestStreamDeployLog_TerminalReplayAndEnd covers the "user
// opened the panel after the deploy finished" path: the file
// already has the full log, the worker is no longer in flight,
// so the stream replays the file and exits. The end event
// prevents the EventSource auto-reconnect loop.
func TestStreamDeployLog_TerminalReplayAndEnd(t *testing.T) {
	h := &Handlers{
		DB:         newTestDB(t),
		DeployLogs: mustStore(t),
	}
	depID := seedDeployForLog(t, h, 1, "nginx:1.27")
	w := mustWriteLog(t, h, depID, []string{
		"-> deploy started (image=nginx:1.27)",
		"→ pull nginx:1.27",
		"→ pull nginx:1.27: ok",
	})
	_ = w

	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		streamDeployLog(context.Background(), rec, h, depID)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("streamDeployLog did not return for terminal deploy")
	}
	body := rec.Body.String()
	for _, want := range []string{
		"data: -> deploy started (image=nginx:1.27)",
		"data: → pull nginx:1.27",
		"data: → pull nginx:1.27: ok",
		"event: end",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\nbody:\n%s", want, body)
		}
	}
}

// TestStreamDeployLog_LiveTailSeesNewLines covers the "user
// opened the panel while the deploy is still running" path:
// the file is seeded with a few lines, the worker is marked
// in-flight, the stream dumps the seed then tails for new
// lines until the worker exits. We simulate "still writing"
// by adding the deployID to the inflight map and "done" by
// removing it after writing more lines.
func TestStreamDeployLog_LiveTailSeesNewLines(t *testing.T) {
	h := &Handlers{
		DB:         newTestDB(t),
		DeployLogs: mustStore(t),
	}
	depID := seedDeployForLog(t, h, 1, "nginx:1.27")
	mustWriteLog(t, h, depID, []string{
		"-> deploy started (image=nginx:1.27)",
		"→ pull nginx:1.27",
	})
	// Mark this deploy in-flight so the stream enters the live
	// tail loop instead of exiting immediately.
	h.inflightMu.Lock()
	if h.inflight == nil {
		h.inflight = make(map[int]int)
	}
	h.inflight[depID] = 1
	h.inflightMu.Unlock()
	defer func() {
		h.inflightMu.Lock()
		delete(h.inflight, depID)
		h.inflightMu.Unlock()
	}()

	rec := httptest.NewRecorder()
	streamCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		streamDeployLog(streamCtx, rec, h, depID)
		close(done)
	}()

	// Write a few more lines after a short delay so the live
	// tail picks them up. We bypass the SSE writer and write
	// directly to the log file via the store.
	go func() {
		// Give the stream a moment to consume the seed and
		// enter the tail loop.
		time.Sleep(150 * time.Millisecond)
		w, err := h.DeployLogs.openWriter(depID)
		if err != nil {
			return
		}
		_ = w.appendLine("→ pull nginx:1.27: ok")
		_ = w.close()
		// Worker "finishes" — remove from inflight. The next
		// 1s tick will see this, do one more read, and exit.
		time.Sleep(100 * time.Millisecond)
		h.inflightMu.Lock()
		delete(h.inflight, depID)
		h.inflightMu.Unlock()
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("streamDeployLog did not return after worker exit")
	}
	body := rec.Body.String()
	for _, want := range []string{
		"data: -> deploy started (image=nginx:1.27)",
		"data: → pull nginx:1.27",
		"data: → pull nginx:1.27: ok",
		"event: end",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\nbody:\n%s", want, body)
		}
	}
}

// TestStreamDeployLog_KeyedLineEmitsLineReplaceEvent pins the
// wire shape: a keyed line (| prefix in the file) is emitted as
// `event: line-replace` with data = "key\tmsg"; a plain line is
// emitted as `event: line`. The frontend dedupes by key.
func TestStreamDeployLog_KeyedLineEmitsLineReplaceEvent(t *testing.T) {
	h := &Handlers{
		DB:         newTestDB(t),
		DeployLogs: mustStore(t),
	}
	depID := seedDeployForLog(t, h, 1, "nginx:1.27")
	// Write one plain line and one keyed line.
	w, err := h.DeployLogs.openWriter(depID)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	if err := w.appendLine("compose up started"); err != nil {
		t.Fatalf("append plain: %v", err)
	}
	if err := w.appendKeyed("layer-abc", "abc Pull complete"); err != nil {
		t.Fatalf("append keyed: %v", err)
	}
	if err := w.close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		streamDeployLog(context.Background(), rec, h, depID)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("streamDeployLog did not return")
	}
	body := rec.Body.String()
	if !strings.Contains(body, "event: line\ndata: compose up started") {
		t.Errorf("body missing plain `line` event:\n%s", body)
	}
	if !strings.Contains(body, "event: line-replace\ndata: layer-abc\tabc Pull complete") {
		t.Errorf("body missing keyed `line-replace` event:\n%s", body)
	}
	if !strings.Contains(body, "event: end") {
		t.Errorf("body missing terminal `end` event:\n%s", body)
	}
}

// TestStreamDeployLog_ContextCancelStopsTail ensures the live
// tail loop respects the request context: if the client
// disconnects, the stream returns and the deploy worker can
// proceed unblocked.
func TestStreamDeployLog_ContextCancelStopsTail(t *testing.T) {
	h := &Handlers{
		DB:         newTestDB(t),
		DeployLogs: mustStore(t),
	}
	depID := seedDeployForLog(t, h, 1, "nginx:1.27")
	if err := mustOpenWriter(t, h, depID).appendLine("seed"); err != nil {
		t.Fatalf("append: %v", err)
	}
	h.inflightMu.Lock()
	if h.inflight == nil {
		h.inflight = make(map[int]int)
	}
	h.inflight[depID] = 1
	h.inflightMu.Unlock()
	defer func() {
		h.inflightMu.Lock()
		delete(h.inflight, depID)
		h.inflightMu.Unlock()
	}()

	rec := httptest.NewRecorder()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		streamDeployLog(ctx, rec, h, depID)
		close(done)
	}()
	time.Sleep(100 * time.Millisecond) // let it enter the tail loop
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("streamDeployLog did not return on ctx cancel")
	}
}

// TestIsTerminalStatus pins the terminal set so a future status
// rename doesn't silently change stream behavior.
func TestIsTerminalStatus(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"running", false},
		{"pending", false},
		{"success", true},
		{"failed", true},
		{"", false},
		{"SUCCESS", false}, // case-sensitive: matches the ent enum
	}
	for _, c := range cases {
		if got := isTerminalStatus(c.in); got != c.want {
			t.Errorf("isTerminalStatus(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// mustStore returns a deployLogStore rooted at t.TempDir().
func mustStore(t *testing.T) *deployLogStore {
	t.Helper()
	s, err := newDeployLogStore(t.TempDir())
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return s
}

// seedDeployForLog creates an App + Deploy row and returns the
// deployID. The app is created from scratch (we don't share
// across tests because each test has its own DB), so callers
// don't need to seed an app first.
func seedDeployForLog(t *testing.T, h *Handlers, appID int, image string) int {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	a, err := h.DB.App.Create().
		SetName("log-test-app").
		SetImage(image).
		SetPort(80).
		SetDeployMethod("docker").
		Save(ctx)
	if err != nil {
		t.Fatalf("seed app: %v", err)
	}
	d, err := h.DB.Deploy.Create().
		SetAppID(a.ID).
		SetTrigger("manual").
		SetStatus("success").
		SetStartedAt(now).
		SetFinishedAt(now).
		SetImage(image).
		Save(ctx)
	if err != nil {
		t.Fatalf("seed deploy: %v", err)
	}
	return d.ID
}

// mustOpenWriter opens a writer for a deploy's log file. Used
// by tests that need to seed lines without going through a
// real deploy worker.
func mustOpenWriter(t *testing.T, h *Handlers, deployID int) *deployLogWriter {
	t.Helper()
	w, err := h.DeployLogs.openWriter(deployID)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	return w
}

// mustWriteLog seeds a deploy's log file with the given lines
// (one appendLine per entry, in order) and returns the writer
// (caller is responsible for closing it).
func mustWriteLog(t *testing.T, h *Handlers, deployID int, lines []string) *deployLogWriter {
	t.Helper()
	w := mustOpenWriter(t, h, deployID)
	for _, l := range lines {
		if err := w.appendLine(l); err != nil {
			t.Fatalf("append %q: %v", l, err)
		}
	}
	if err := w.close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return w
}
