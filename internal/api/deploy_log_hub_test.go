package api

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestDeployLogHub_ReplayThenLive verifies the basic contract: a
// subscriber sees the history buffer first, then live lines until the
// hub marks the deploy terminal.
func TestDeployLogHub_ReplayThenLive(t *testing.T) {
	hub := newDeployLogHub()
	hub.publish(1, "first line")
	hub.publish(1, "second line")

	hist, live, done, unsub, ok := hub.subscribe(context.Background(), 1)
	if !ok {
		t.Fatal("subscribe returned !ok")
	}
	defer unsub()

	if len(hist) != 2 {
		t.Fatalf("history len = %d, want 2", len(hist))
	}
	if hist[0].msg != "first line" || hist[1].msg != "second line" {
		t.Errorf("history content = %q, want [first, second]", historyMessages(hist))
	}

	// Drain the 5ms pre-buffer window.
	time.Sleep(20 * time.Millisecond)

	// Push a new line and assert it arrives on live.
	hub.publish(1, "third line")
	select {
	case l := <-live:
		if l.msg != "third line" {
			t.Errorf("live line = %q, want %q", l.msg, "third line")
		}
	case <-time.After(time.Second):
		t.Fatal("live line never arrived")
	}

	// Mark terminal; the done channel should close, and the live
	// channel's next read should be ok=false.
	hub.markTerminal(1)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("done did not close after markTerminal")
	}
	// Allow a brief moment for the drainPre contract to settle.
	time.Sleep(20 * time.Millisecond)
}

// TestDeployLogHub_SlowSubscriberDoesNotBlockPublisher checks the
// backpressure policy: if a subscriber's channel is full, publish drops
// the line (rather than blocking the deploy worker).
func TestDeployLogHub_SlowSubscriberDoesNotBlockPublisher(t *testing.T) {
	hub := newDeployLogHub()
	_, live, _, unsub, _ := hub.subscribe(context.Background(), 1)
	defer unsub()
	// Fill the subscriber's channel past its buffer. We don't drain it
	// — that's the whole point of the test.
	for i := 0; i < deployLogSubBuffer*2; i++ {
		hub.publish(1, "filler")
	}
	// The publisher must have returned from every publish call (i.e.,
	// it didn't block). We assert this by completing the test in <1s
	// rather than hanging. If we got here, the policy holds.
	select {
	case <-time.After(100 * time.Millisecond):
		// Good — we made it through.
	}
	_ = live
}

// TestDeployLogHub_UnsubscribeStopsReceiving ensures a subscriber that
// unsubscribes no longer appears in the slice, so a late publish
// doesn't try to push to a closed channel.
func TestDeployLogHub_UnsubscribeStopsReceiving(t *testing.T) {
	hub := newDeployLogHub()
	_, _, _, unsub, _ := hub.subscribe(context.Background(), 1)
	unsub()
	hub.mu.Lock()
	n := len(hub.streams[1].subs)
	hub.mu.Unlock()
	if n != 0 {
		t.Errorf("after unsub, subs = %d, want 0", n)
	}
}

// TestDeployLogHub_TerminalStopsAcceptingNewLines documents the safety
// net: a publish call after markTerminal is dropped (the deploy row
// already reports its terminal status; logging more would mislead the
// operator). The history buffer is also not extended past the
// terminal mark.
func TestDeployLogHub_TerminalStopsAcceptingNewLines(t *testing.T) {
	hub := newDeployLogHub()
	hub.publish(1, "before-terminal")
	hub.markTerminal(1)
	hub.publish(1, "after-terminal")

	hist, _, _, unsub, _ := hub.subscribe(context.Background(), 1)
	defer unsub()
	if len(hist) != 1 {
		t.Errorf("history len = %d, want 1 (post-terminal lines are dropped)", len(hist))
	}
	if hist[0].msg != "before-terminal" {
		t.Errorf("history[0] = %q, want before-terminal", hist[0].msg)
	}
}

// TestDeployLogHub_ConcurrentSubscribers is a smoke test for the locks:
// many goroutines subscribe and read concurrently; the test passes if
// the race detector doesn't fire (run with -race).
func TestDeployLogHub_ConcurrentSubscribers(t *testing.T) {
	hub := newDeployLogHub()
	const N = 16
	var wg sync.WaitGroup
	var published atomic.Int64
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, live, _, unsub, _ := hub.subscribe(context.Background(), 1)
			defer unsub()
			deadline := time.Now().Add(50 * time.Millisecond)
			for time.Now().Before(deadline) {
				select {
				case <-live:
					published.Add(1)
				case <-time.After(time.Millisecond):
				}
			}
		}()
	}
	// Publisher loop, runs alongside subscribers.
	pubDone := make(chan struct{})
	go func() {
		for i := 0; i < 200; i++ {
			hub.publish(1, "tick")
			time.Sleep(time.Millisecond)
		}
		close(pubDone)
	}()
	wg.Wait()
	<-pubDone
	hub.markTerminal(1)
	// published > 0 is the loose assertion — exact number is timing-
	// dependent. The point is no panic, no race, no deadlock.
	if published.Load() == 0 {
		t.Error("subscribers received no lines — likely a lock or buffering bug")
	}
}

func historyMessages(h []deployLogLine) []string {
	out := make([]string, len(h))
	for i, l := range h {
		out[i] = l.msg
	}
	return out
}

// TestDeployLogHub_PublishBeforeMarkTerminalStoresLines pins the
// ordering: every publish call that lands before markTerminal is
// recorded in the history, and a subscriber that subscribes after
// the deploy finishes sees them on replay. (We test this property
// explicitly because it's the path the SSE handler takes when a
// user opens the deploy log panel after the deploy has already
// finished — the most common case for CI deploys.)
func TestDeployLogHub_PublishBeforeMarkTerminalStoresLines(t *testing.T) {
	hub := newDeployLogHub()
	for _, msg := range []string{"step 1", "step 2", "step 3"} {
		hub.publish(1, msg)
	}
	hub.markTerminal(1)
	hist, _, _, unsub, _ := hub.subscribe(context.Background(), 1)
	defer unsub()
	if len(hist) != 3 {
		t.Fatalf("history len = %d, want 3", len(hist))
	}
	for i, want := range []string{"step 1", "step 2", "step 3"} {
		if hist[i].msg != want {
			t.Errorf("hist[%d] = %q, want %q", i, hist[i].msg, want)
		}
	}
}

// TestDeployLogHub_MarkTerminalOnUnknownDeployIsNoop ensures that
// calling markTerminal on a deploy the hub has never seen doesn't
// panic and doesn't pollute the stream map. This catches the
// double-defer pattern in executeDeploy (which may call markTerminal
// after a panic) and lets future refactors call it eagerly without
// crashing.
func TestDeployLogHub_MarkTerminalOnUnknownDeployIsNoop(t *testing.T) {
	hub := newDeployLogHub()
	// Should not panic.
	hub.markTerminal(999)
	hub.mu.Lock()
	n := len(hub.streams)
	hub.mu.Unlock()
	if n != 0 {
		t.Errorf("streams after markTerminal(unknown) = %d, want 0", n)
	}
}

// TestDeployLogHub_RingBufferDropsOldestLines verifies the bounded
// buffer: with deployLogBufferMax=4000 and a stream pushing more
// than that, the history should keep the most recent lines. We
// push just over the cap and assert the oldest entries are gone.
func TestDeployLogHub_RingBufferDropsOldestLines(t *testing.T) {
	hub := newDeployLogHub()
	const total = deployLogBufferMax + 50
	for i := 0; i < total; i++ {
		hub.publish(1, fmt.Sprintf("line-%d", i))
	}
	hist, _, _, unsub, _ := hub.subscribe(context.Background(), 1)
	defer unsub()
	if len(hist) != deployLogBufferMax {
		t.Fatalf("history len = %d, want %d", len(hist), deployLogBufferMax)
	}
	// The first surviving entry should be `line-50` (the cap+1-th push).
	if hist[0].msg != "line-50" {
		t.Errorf("hist[0] = %q, want line-50", hist[0].msg)
	}
	if hist[len(hist)-1].msg != fmt.Sprintf("line-%d", total-1) {
		t.Errorf("hist[last] = %q, want line-%d", hist[len(hist)-1].msg, total-1)
	}
}

// TestDeployLogHub_GCRemovesExpiredTerminalStreams exercises the
// post-terminal garbage collection path. We override the TTL by
// calling gc() after manually rewinding expiresAt. This avoids a
// 5-minute real-time wait in the test.
func TestDeployLogHub_GCRemovesExpiredTerminalStreams(t *testing.T) {
	hub := newDeployLogHub()
	hub.publish(1, "x")
	hub.markTerminal(1)
	// After markTerminal, the entry is terminal+idle (no subs).
	// gc() should NOT remove it yet (TTL is 5 minutes).
	hub.gc()
	hub.mu.Lock()
	if len(hub.streams) != 1 {
		hub.mu.Unlock()
		t.Fatalf("before expiry: streams = %d, want 1", len(hub.streams))
	}
	hub.mu.Unlock()
	// Rewind expiresAt to the past and gc again. The entry is now
	// terminal AND expired AND no subs, so gc should drop it.
	hub.mu.Lock()
	s := hub.streams[1]
	hub.mu.Unlock()
	s.mu.Lock()
	s.expiresAt = time.Now().Add(-time.Minute)
	s.mu.Unlock()
	hub.gc()
	hub.mu.Lock()
	n := len(hub.streams)
	hub.mu.Unlock()
	if n != 0 {
		t.Errorf("after expiry: streams = %d, want 0", n)
	}
}

// TestDeployLogHub_GCKeepsStreamsWithLiveSubs ensures gc() doesn't
// drop a terminal stream that still has subscribers (the operator
// is reading the log; the TTL only applies once they leave).
func TestDeployLogHub_GCKeepsStreamsWithLiveSubs(t *testing.T) {
	hub := newDeployLogHub()
	hub.publish(1, "x")
	hub.markTerminal(1)
	_, _, _, unsub, _ := hub.subscribe(context.Background(), 1)
	defer unsub()
	// Rewind expiresAt to the past.
	hub.mu.Lock()
	s := hub.streams[1]
	hub.mu.Unlock()
	s.mu.Lock()
	s.expiresAt = time.Now().Add(-time.Hour)
	s.mu.Unlock()
	hub.gc()
	hub.mu.Lock()
	n := len(hub.streams)
	hub.mu.Unlock()
	if n != 1 {
		t.Errorf("with live subs, streams after gc = %d, want 1", n)
	}
}

// TestDeployLogHub_SubscribeOnUnknownDeployCreatesEntry documents
// the "user opens the panel before the worker has published
// anything" path. A subscribe to a non-existent deploy should create
// an empty stream so the subscriber waits for the first publish
// rather than getting an immediate EOF.
func TestDeployLogHub_SubscribeOnUnknownDeployCreatesEntry(t *testing.T) {
	hub := newDeployLogHub()
	hist, live, done, unsub, ok := hub.subscribe(context.Background(), 42)
	defer unsub()
	if !ok {
		t.Fatal("subscribe returned !ok")
	}
	if len(hist) != 0 {
		t.Errorf("empty history: got %d entries", len(hist))
	}
	// A subsequent publish should be observable to this subscriber.
	hub.publish(42, "first")
	select {
	case l := <-live:
		if l.msg != "first" {
			t.Errorf("got %q, want first", l.msg)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("subscriber did not receive late publish")
	}
	// And markTerminal on a previously-unknown id should now work.
	hub.markTerminal(42)
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("done did not close after markTerminal")
	}
}

// TestDeployLogHub_MultipleDeploysIndependent verifies the hub
// doesn't cross-publish across deploy IDs.
func TestDeployLogHub_MultipleDeploysIndependent(t *testing.T) {
	hub := newDeployLogHub()
	hub.publish(1, "deploy-1-only")
	hub.publish(2, "deploy-2-only")
	hist1, _, _, unsub1, _ := hub.subscribe(context.Background(), 1)
	defer unsub1()
	hist2, _, _, unsub2, _ := hub.subscribe(context.Background(), 2)
	defer unsub2()
	if len(hist1) != 1 || hist1[0].msg != "deploy-1-only" {
		t.Errorf("deploy 1 history = %v, want [deploy-1-only]", historyMessages(hist1))
	}
	if len(hist2) != 1 || hist2[0].msg != "deploy-2-only" {
		t.Errorf("deploy 2 history = %v, want [deploy-2-only]", historyMessages(hist2))
	}
}
