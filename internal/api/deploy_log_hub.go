package api

import (
	"bytes"
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// deployLogLine is one captured line from a running deploy. Stored as a
// value type so the hub can copy under lock without a separate alloc.
//
// Key is the in-place replacement key used by structured progress (compose
// --progress json). When non-empty, this line replaces the previous line
// with the same Key in the history buffer and on the wire (SSE
// `line-replace` event) rather than appending - so a layer download tick
// updates a single row instead of scrolling. Empty Key = ordinary append.
type deployLogLine struct {
	at  time.Time
	msg string
	key string
}

// deployLogHub is a per-process pub/sub for in-flight deploy logs.
//
//   - executeDeploy pushes lines as the deploy progresses (one entry
//     per stdout line from docker pull, compose up, etc., plus our own
//     "→ pulling image", "✓ deploy success" annotations).
//   - GET /api/apps/{id}/deployments/{did}/logs/stream subscribes; the
//     hub first replays the buffered history, then streams live lines,
//     then closes the channel when the deploy reaches a terminal state.
//
// We bound the buffer per deploy (DEPLOY_LOG_BUFFER) so a runaway
// compose pull can't OOM the process while no one is watching. The
// hub is also self-cleaning: terminal deploys are removed after a
// short TTL so a long-running nanoku instance doesn't accumulate
// finished deploys indefinitely.
type deployLogHub struct {
	mu      sync.Mutex
	streams map[int]*deployLogStream // keyed by deployID
}

type deployLogStream struct {
	mu        sync.Mutex
	done      chan struct{}   // closed when deploy reaches terminal status
	history   []deployLogLine // replay buffer
	subs      []chan deployLogLine
	terminal  bool      // true once the deploy worker has reported success/failed
	expiresAt time.Time // when to GC the entry after terminal+no-subs
}

const (
	deployLogBufferMax  = 4000 // lines retained per deploy for replay
	deployLogHistoryTTL = 5 * time.Minute
	// Size of the per-subscriber channel. Slightly larger than the
	// caller-side SSE event loop expects to drain in one tick.
	deployLogSubBuffer = 256
)

func newDeployLogHub() *deployLogHub {
	return &deployLogHub{streams: make(map[int]*deployLogStream)}
}

// NewDeployLogHub is the public constructor used by main.go to wire the
// hub into Handlers.
func NewDeployLogHub() *deployLogHub { return newDeployLogHub() }

// publish appends a line to the deploy's buffer and pushes it to every
// live subscriber. Slow subscribers drop lines (we close their channel
// and they'll reconnect to recover via the history buffer) rather than
// backpressuring the deploy worker.
func (h *deployLogHub) publish(deployID int, msg string) {
	h.mu.Lock()
	s, ok := h.streams[deployID]
	if !ok {
		s = &deployLogStream{done: make(chan struct{})}
		h.streams[deployID] = s
	}
	h.mu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal {
		// Late publishers after terminal: don't accept, but still keep
		// the buffer. (In practice executeDeploy calls publish BEFORE
		// markTerminal in a single goroutine, so this is a safety net
		// for future refactors.)
		return
	}
	line := deployLogLine{at: time.Now().UTC(), msg: msg}
	if len(s.history) < deployLogBufferMax {
		s.history = append(s.history, line)
	} else {
		// Ring-buffer style: drop the oldest, append the newest. The
		// alternative (drop the new line) is worse — operators care
		// about what just happened, not what happened 4000 lines ago.
		s.history = append(s.history[1:], line)
	}
	live := s.subs
	// Slice may be mutated by unsubscribe; snapshot under lock.
	subs := make([]chan deployLogLine, len(live))
	copy(subs, live)
		for _, ch := range subs {
			select {
			case ch <- line:
			default:
				// Slow consumer - drop. They'll reconnect; the replay
				// buffer is bounded but recent, so the gap is small.
			}
		}
}

// publishReplace updates the last history line with the given key in-place
// (preserving its position) and notifies subscribers with a keyed line so
// the SSE layer emits a `line-replace` event instead of `line`. If no
// prior line has this key, it degrades to an append (same as publish with
// a key) so the first occurrence of a key still shows up.
//
// This is what makes compose download progress update a single row: each
// "Downloading NkB" tick for layer <sha> calls publishReplace with
// key=<sha>, so the operator sees one row that changes value rather than
// dozens of scrolling lines. History replay is correct too: the buffer
// holds the latest msg for each key at its original position.
func (h *deployLogHub) publishReplace(deployID int, key, msg string) {
	if key == "" {
		// No key => ordinary append. Delegate to publish for the
		// append + fan-out path (avoids duplicating it here).
		h.publish(deployID, msg)
		return
	}
	h.mu.Lock()
	s, ok := h.streams[deployID]
	if !ok {
		s = &deployLogStream{done: make(chan struct{})}
		h.streams[deployID] = s
	}
	h.mu.Unlock()

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.terminal {
		return
	}
	line := deployLogLine{at: time.Now().UTC(), msg: msg, key: key}
	// Scan backwards for an existing line with this key. Backwards is
	// correct: a key is most recently appended, and we want the latest
	// position (a key could in principle repeat after being evicted by
	// the ring buffer, but that's degenerate).
	found := false
	for i := len(s.history) - 1; i >= 0; i-- {
		if s.history[i].key == key {
			s.history[i].msg = msg
			s.history[i].at = line.at
			found = true
			break
		}
	}
	if !found {
		// First occurrence: append like a normal line (carrying the key
		// so future replaces can find it).
		if len(s.history) < deployLogBufferMax {
			s.history = append(s.history, line)
		} else {
			s.history = append(s.history[1:], line)
		}
	}
	live := s.subs
	subs := make([]chan deployLogLine, len(live))
	copy(subs, live)
	for _, ch := range subs {
		select {
		case ch <- line:
		default:
		}
	}
}

// markTerminal closes the done channel and sets an expiry. Subscribers
// in the SSE handler drain any remaining buffered lines and then exit.
func (h *deployLogHub) markTerminal(deployID int) {
	h.mu.Lock()
	s, ok := h.streams[deployID]
	if !ok {
		h.mu.Unlock()
		return
	}
	h.mu.Unlock()
	s.mu.Lock()
	s.terminal = true
	s.expiresAt = time.Now().Add(deployLogHistoryTTL)
	// done is closed exactly once; the deploy worker is the sole caller
	// of markTerminal, and we only close after setting terminal=true so
	// late publish() calls (which check s.terminal) don't race the
	// close.
	close(s.done)
	s.mu.Unlock()
	// Sweep terminal+idle streams in the background so we don't block
	// the deploy worker (which is calling this from a defer).
	go h.gc()
}

func (h *deployLogHub) gc() {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now()
	for id, s := range h.streams {
		s.mu.Lock()
		idle := s.terminal && len(s.subs) == 0 && now.After(s.expiresAt)
		s.mu.Unlock()
		if idle {
			delete(h.streams, id)
		}
	}
}

// subscribe returns: history (snapshot for replay), a live channel, and
// a done channel that closes when the deploy reaches a terminal state
// OR the caller's context is canceled (whichever first).
//
// The caller must call the returned unsubscribe func when done so the
// hub can GC terminal streams promptly.
func (h *deployLogHub) subscribe(ctx context.Context, deployID int) (history []deployLogLine, live <-chan deployLogLine, done <-chan struct{}, unsub func(), ok bool) {
	h.mu.Lock()
	s, exists := h.streams[deployID]
	if !exists {
		// Hub hasn't seen this deploy yet (e.g. deploy is queued or
		// pre-existed the boot). Create an empty entry so the
		// subscriber waits for the worker to publish.
		s = &deployLogStream{done: make(chan struct{})}
		h.streams[deployID] = s
	}
	h.mu.Unlock()

	ch := make(chan deployLogLine, deployLogSubBuffer)

	s.mu.Lock()
	// Copy history under the stream lock.
	histCopy := make([]deployLogLine, len(s.history))
	copy(histCopy, s.history)
	s.subs = append(s.subs, ch)
	subDone := s.done
	s.mu.Unlock()

	unsub = func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for i, sub := range s.subs {
			if sub == ch {
				s.subs = append(s.subs[:i], s.subs[i+1:]...)
				break
			}
		}
		// Don't close ch here — the consumer owns the close. The
		// hub's contract is "publisher won't block, slow subscribers
		// drop"; closing from the publisher side would race with
		// concurrent publish.
	}

	// Bridge ctx.Done to unsub so a client disconnect doesn't leak a
	// subscription. Use atomic.Bool to make the goroutine idempotent.
	var stopped atomic.Bool
	go func() {
		select {
		case <-ctx.Done():
			if stopped.CompareAndSwap(false, true) {
				unsub()
			}
		case <-subDone:
			if stopped.CompareAndSwap(false, true) {
				unsub()
			}
		}
	}()

	return histCopy, ch, subDone, unsub, true
}

// lineWriterToHub is an io.Writer that splits on '\n' and publishes each
// line into the hub. Used to tee the docker pull / compose up output
// straight into the live deploy log feed. A trailing partial line
// (no newline yet) is buffered and emitted on the next write.
type lineWriterToHub struct {
	hub      *deployLogHub
	deployID int
	buf      bytes.Buffer
}

func (w *lineWriterToHub) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		idx := bytes.IndexByte(w.buf.Bytes(), '\n')
		if idx < 0 {
			break
		}
		line := w.buf.Bytes()[:idx] // drop the trailing '\n' — SSE re-adds it
		w.buf.Next(idx + 1)
		w.hub.publish(w.deployID, string(line))
	}
	return len(p), nil
}

func (w *lineWriterToHub) flushPartial() {
	// Idempotent on empty buffer: a worker that calls flushPartial
	// in a defer can do so without checking whether any unterminated
	// line was actually pending. The size guard avoids a duplicate
	// publish when there's nothing buffered (which would surface
	// as a stray empty line in the deploy log).
	if w.buf.Len() == 0 {
		return
	}
	w.hub.publish(w.deployID, w.buf.String())
	w.buf.Reset()
}
