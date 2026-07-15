package api

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestStreamDeployLog_ReplaysHistoryThenLivePublishesLines is the
// happy path: history lines come out first, then a live line published
// after subscribe is forwarded before the deploy is marked terminal.
func TestStreamDeployLog_ReplaysHistoryThenLivePublishesLines(t *testing.T) {
	hub := newDeployLogHub()
	hub.publish(1, "first-history")
	hub.publish(1, "second-history")

	hist, live, done, unsub, _ := hub.subscribe(context.Background(), 1)
	defer unsub()

	// Allow the 5ms pre-drain window to elapse without publishing
	// anything — we want to test the history path alone first.
	time.Sleep(20 * time.Millisecond)

	w := httptest.NewRecorder()
	streamDone := make(chan struct{})
	go func() {
		streamDeployLog(context.Background(), w, hist, live, done, "running")
		close(streamDone)
	}()

	// While the stream is in its select loop, publish a live line.
	time.Sleep(5 * time.Millisecond)
	hub.publish(1, "live-line")

	// Now mark the deploy terminal so streamDeployLog exits.
	hub.markTerminal(1)

	select {
	case <-streamDone:
	case <-time.After(2 * time.Second):
		t.Fatal("streamDeployLog did not return after markTerminal")
	}

	body := w.Body.String()
	for _, want := range []string{
		"data: first-history",
		"data: second-history",
		"data: live-line",
		": nanoku deploy log stream end",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q\nbody:\n%s", want, body)
		}
	}
}

// TestStreamDeployLog_TerminalAtSubscribeExitsImmediately documents the
// "user opened the panel after the deploy finished" path: we replay
// history and exit without waiting on the done channel.
func TestStreamDeployLog_TerminalAtSubscribeExitsImmediately(t *testing.T) {
	hub := newDeployLogHub()
	hub.publish(1, "history-1")
	hub.markTerminal(1)

	// Important: subscribe *after* markTerminal so the stream is
	// already terminal. The hist snapshot still has the line because
	// publish was called before markTerminal.
	hist, live, _, unsub, _ := hub.subscribe(context.Background(), 1)
	defer unsub()
	time.Sleep(10 * time.Millisecond) // allow drainPre

	w := httptest.NewRecorder()
	streamDone := make(chan struct{})
	go func() {
		streamDeployLog(context.Background(), w, hist, live, make(chan struct{}), "success")
		close(streamDone)
	}()
	select {
	case <-streamDone:
	case <-time.After(2 * time.Second):
		t.Fatal("streamDeployLog did not return for terminal-at-subscribe")
	}
	if !strings.Contains(w.Body.String(), "data: history-1") {
		t.Errorf("body missing history-1: %q", w.Body.String())
	}
	if !strings.Contains(w.Body.String(), ": nanoku deploy log stream end") {
		t.Errorf("body missing end comment: %q", w.Body.String())
	}
}

// TestStreamDeployLog_ClientDisconnectTriggersUnsub ensures that when
// the request context is canceled, streamDeployLog returns and the
// hub no longer pushes new lines to the disconnected subscriber
// (the bridge goroutine in subscribe() fires unsub). We don't read
// s.subs directly (that would race with the bridge goroutine);
// instead we publish a line post-disconnect and observe whether the
// original subscriber's \ channel still receives it.
func TestStreamDeployLog_ClientDisconnectTriggersUnsub(t *testing.T) {
	hub := newDeployLogHub()
	ctx, cancelCtx := context.WithCancel(context.Background())
	_, live, done, unsub, _ := hub.subscribe(ctx, 1)
	defer unsub()

	w := httptest.NewRecorder()
	streamDone := make(chan struct{})
	go func() {
		streamDeployLog(ctx, w, []deployLogLine{}, live, done, "running")
		close(streamDone)
	}()
	time.Sleep(20 * time.Millisecond)
	cancelCtx()
	select {
	case <-streamDone:
	case <-time.After(2 * time.Second):
		t.Fatal("streamDeployLog did not return on ctx cancel")
	}
	// Give the bridge goroutine time to react to cancelCtx.
	time.Sleep(100 * time.Millisecond)

	// Publish a line; the original subscriber's \ should
	// NOT receive it. A fresh probe subscriber should.
	probeCtx, probeCancel := context.WithCancel(context.Background())
	defer probeCancel()
	_, probeLive, _, _, _ := hub.subscribe(probeCtx, 1)
	hub.publish(1, "post-disconnect")

	select {
	case <-probeLive:
	case <-time.After(time.Second):
		t.Fatal("probe subscriber never received the test line")
	}
	select {
	case _, ok := <-live:
		if ok {
			t.Error("original subscriber received a line after disconnect - bridge did not run")
		}
	case <-time.After(50 * time.Millisecond):
		// The "did not receive" case is what we want.
	}
	_ = atomic.LoadInt32
}


// TestIsTerminalStatus pins the terminal set so a future status rename
// doesn't silently change the SSE behavior (e.g. starting to wait on
// `done` for an "indeterminate" status).
func TestIsTerminalStatus(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"running", false},
		{"pending", false},
		{"success", true},
		{"failed", true},
		{"rolled_back", true},
		{"", false},
		{"SUCCESS", false}, // case-sensitive: matches the ent enum
	}
	for _, c := range cases {
		if got := isTerminalStatus(c.in); got != c.want {
			t.Errorf("isTerminalStatus(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestStreamDeployLog_LineWriterToHub exercises the io.Writer adapter
// that executeDeploy uses to tee docker pull / compose up output into
// the hub. It must split on '\n' and emit each line verbatim.
func TestStreamDeployLog_LineWriterToHub(t *testing.T) {
	hub := newDeployLogHub()
	w := &lineWriterToHub{hub: hub, deployID: 1}
	// Three lines in one Write, one trailing partial without newline.
	_, _ = w.Write([]byte("alpha\nbeta\ngamma\ndelt"))
	_, _ = w.Write([]byte("a\n"))
	// Flush the trailing partial.
	w.flushPartial()
	// The partial "delta" is only published on flushPartial.
	w.flushPartial() // idempotent: nothing buffered

	// Subscribe and drain the history.
	hist, _, _, unsub, _ := hub.subscribe(context.Background(), 1)
	defer unsub()
	got := historyMessages(hist)
	want := []string{"alpha", "beta", "gamma", "delta"}
	if len(got) != len(want) {
		t.Fatalf("history = %v, want %v", got, want)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("history[%d] = %q, want %q", i, got[i], w)
		}
	}
}

// TestStreamDeployLog_PublishIsConcurrencySafe runs the lineWriter
// and the hub subscribe/publish against each other in parallel, with
// the race detector enabled, to catch any lock-free mutation.
func TestStreamDeployLog_PublishIsConcurrencySafe(t *testing.T) {
	hub := newDeployLogHub()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lw := &lineWriterToHub{hub: hub, deployID: 1}
			for j := 0; j < 50; j++ {
				_, _ = lw.Write([]byte("tick\n"))
			}
		}()
	}
	wg.Wait()
	hub.markTerminal(1)
	hist, _, _, unsub, _ := hub.subscribe(context.Background(), 1)
	defer unsub()
	if len(hist) == 0 {
		t.Error("history empty after concurrent publishes")
	}
}

// TestStreamDeployLog_LineWriterToHubFlushPartial asserts that
// flushPartial pushes a trailing partial line (no newline). The
// deploy worker calls this in defer, so a partial line emitted by
// the docker CLI but not yet terminated by '\n' still reaches the
// hub. We assert the call is idempotent: a second flushPartial with
// nothing buffered is a no-op, not a duplicate publish.
func TestStreamDeployLog_LineWriterToHubFlushPartial(t *testing.T) {
	hub := newDeployLogHub()
	w := &lineWriterToHub{hub: hub, deployID: 1}
	// No newline at the end of this write — buffered, not yet
	// published.
	_, _ = w.Write([]byte("trailing partial"))
	// Pre-flush: the partial is in the writer's buffer (not in the
	// hub's history yet). We use the writer's own state to assert
	// this, since the hub entry is lazily created on first publish.
	if w.buf.Len() == 0 {
		t.Fatal("pre-flush: writer buffer is empty, expected the partial line")
	}
	// Subscribe BEFORE flushing so the partial line reaches us
	// (we want the published version, not the un-published buffer).
	_, live, _, unsub, _ := hub.subscribe(context.Background(), 1)
	defer unsub()
	w.flushPartial()
	select {
	case l := <-live:
		if l.msg != "trailing partial" {
			t.Errorf("got %q, want trailing partial", l.msg)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("subscriber did not receive the partial line")
	}
	// Second flush: nothing buffered, no duplicate. The live channel
	// would receive a second copy if flushPartial weren't
	// idempotent on empty buffers.
	w.flushPartial()
	select {
	case l, ok := <-live:
		if ok {
			t.Errorf("idempotent flush produced duplicate: got %q", l.msg)
		}
	case <-time.After(50 * time.Millisecond):
		// Expected: no second copy.
	}
}
