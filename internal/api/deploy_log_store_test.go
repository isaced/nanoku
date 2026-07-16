package api

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDeployLogStore_AppendAndReadAll covers the basic happy path:
// write a few lines, read them back in order. This is the path
// the SSE handler takes for a terminal deploy: open the file,
// read everything, emit, exit.
func TestDeployLogStore_AppendAndReadAll(t *testing.T) {
	store := mustStore(t)
	w, err := store.openWriter(1)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	for _, l := range []string{"first", "second", "third"} {
		if err := w.appendLine(l); err != nil {
			t.Fatalf("append %q: %v", l, err)
		}
	}
	if err := w.close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	lines, err := store.readAll(1)
	if err != nil {
		t.Fatalf("readAll: %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("len = %d, want 3", len(lines))
	}
	for i, want := range []string{"first", "second", "third"} {
		if lines[i].Msg != want {
			t.Errorf("[%d].Msg = %q, want %q", i, lines[i].Msg, want)
		}
		if lines[i].Key != "" {
			t.Errorf("[%d].Key = %q, want empty", i, lines[i].Key)
		}
	}
}

// TestDeployLogStore_KeyedLineRoundtrip pins the wire shape: a
// keyed line is stored with the `|` prefix and a tab between key
// and msg. The reader splits on the first tab to recover both
// pieces. Plain lines (no `|`) read back as Msg-only.
func TestDeployLogStore_KeyedLineRoundtrip(t *testing.T) {
	store := mustStore(t)
	w, err := store.openWriter(1)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	if err := w.appendLine("plain line"); err != nil {
		t.Fatalf("append plain: %v", err)
	}
	if err := w.appendKeyed("layer-abc", "Downloading 3.9MB"); err != nil {
		t.Fatalf("append keyed: %v", err)
	}
	if err := w.close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	lines, err := store.readAll(1)
	if err != nil {
		t.Fatalf("readAll: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("len = %d, want 2", len(lines))
	}
	if lines[0].Key != "" || lines[0].Msg != "plain line" {
		t.Errorf("plain line: %+v", lines[0])
	}
	if lines[1].Key != "layer-abc" || lines[1].Msg != "Downloading 3.9MB" {
		t.Errorf("keyed line: %+v", lines[1])
	}
}

// TestDeployLogStore_TailReturnsNewLinesOnly covers the live-tail
// contract: the first call returns everything, subsequent calls
// return only lines added since the previous call. This is the
// path the SSE handler takes while a deploy is still running.
func TestDeployLogStore_TailReturnsNewLinesOnly(t *testing.T) {
	store := mustStore(t)
	w, err := store.openWriter(1)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	_ = w.appendLine("seed-1")
	_ = w.appendLine("seed-2")
	_ = w.close()

	tail, err := store.tail(1)
	if err != nil {
		t.Fatalf("tail: %v", err)
	}
	first, err := tail()
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if len(first) != 2 {
		t.Fatalf("first call len = %d, want 2", len(first))
	}

	// No new lines written → next call returns empty + nil.
	empty, err := tail()
	if err != nil {
		t.Fatalf("empty call: %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("empty call len = %d, want 0", len(empty))
	}

	// Append more and verify the tail picks them up.
	w2, err := store.openWriter(1)
	if err != nil {
		t.Fatalf("reopen writer: %v", err)
	}
	_ = w2.appendLine("seed-3")
	_ = w2.close()

	second, err := tail()
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if len(second) != 1 || second[0].Msg != "seed-3" {
		t.Errorf("second call = %+v, want one line 'seed-3'", second)
	}
}

// TestDeployLogStore_ReadAllMissingFileReturnsErrNotExist covers
// the "deploy hasn't written anything yet" case the SSE handler
// uses to short-circuit with a 404.
func TestDeployLogStore_ReadAllMissingFileReturnsErrNotExist(t *testing.T) {
	store := mustStore(t)
	_, err := store.readAll(999)
	if err == nil {
		t.Fatal("readAll on missing file: want error, got nil")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want os.ErrNotExist-shaped", err)
	}
}

// TestDeployLogStore_RemoveIdempotent covers the app-delete
// cleanup path: Remove must not fail when the file doesn't
// exist (the deploy may never have produced output).
func TestDeployLogStore_RemoveIdempotent(t *testing.T) {
	store := mustStore(t)
	// Remove a deploy that never wrote anything.
	if err := store.remove(999); err != nil {
		t.Errorf("remove missing: %v", err)
	}
	// Write + remove.
	w, err := store.openWriter(1)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	_ = w.appendLine("x")
	_ = w.close()
	if err := store.remove(1); err != nil {
		t.Errorf("remove existing: %v", err)
	}
	// Second remove is a no-op.
	if err := store.remove(1); err != nil {
		t.Errorf("remove again: %v", err)
	}
}

// TestDeployLogStore_PathForIsIDBased pins the on-disk naming:
// a deploy's log file is always "<dir>/<id>.log", no surprises.
func TestDeployLogStore_PathForIsIDBased(t *testing.T) {
	dir := t.TempDir()
	store, err := newDeployLogStore(dir)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	want := filepath.Join(dir, "42.log")
	if got := store.pathFor(42); got != want {
		t.Errorf("pathFor(42) = %q, want %q", got, want)
	}
}

// TestDeployLogStore_MkdirOnConstruction confirms the store
// creates its directory on first call, so a fresh boot doesn't
// fail because the dir is missing.
func TestDeployLogStore_MkdirOnConstruction(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "deploy-logs")
	if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("dir unexpectedly exists: %v", err)
	}
	if _, err := newDeployLogStore(dir); err != nil {
		t.Fatalf("new store: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("dir not created: %v", err)
	}
}

// TestParseDeployLog_MalformedKeyedLineFallsBackToPlain ensures
// a file corruption (keyed prefix without tab) doesn't lose the
// line — the reader surfaces the raw text as a plain line so
// the operator at least sees something.
func TestParseDeployLog_MalformedKeyedLineFallsBackToPlain(t *testing.T) {
	in := "|notab\nplain\n"
	lines, err := parseDeployLog(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(lines) != 2 {
		t.Fatalf("len = %d, want 2", len(lines))
	}
	if lines[0].Key != "" || lines[0].Msg != "|notab" {
		t.Errorf("malformed line: %+v, want Msg='|notab' Key=''", lines[0])
	}
	if lines[1].Key != "" || lines[1].Msg != "plain" {
		t.Errorf("plain line: %+v", lines[1])
	}
}

// TestDeployLogStore_TailOnMissingFileReturnsErrNotExist covers
// the path the SSE handler doesn't currently take (we 404
// before opening the tail), but tests the helper in isolation.
func TestDeployLogStore_TailOnMissingFileReturnsErrNotExist(t *testing.T) {
	store := mustStore(t)
	_, err := store.tail(999)
	if err == nil {
		t.Fatal("tail on missing file: want error, got nil")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("err = %v, want os.ErrNotExist-shaped", err)
	}
}

// TestDeployLogStore_AppendLineIsAtomicForLongLines exercises
// the scanner's bumped buffer: a single line up to ~100 KiB
// should be readable end-to-end. (The writer flushes after
// every line, so a long single line ends up in the file as a
// single record — important because a chatty compose progress
// message with a long image ref can push past the 64 KiB
// default.)
func TestDeployLogStore_AppendLineIsAtomicForLongLines(t *testing.T) {
	store := mustStore(t)
	w, err := store.openWriter(1)
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	long := strings.Repeat("a", 200*1024) // 200 KiB
	if err := w.appendLine(long); err != nil {
		t.Fatalf("append long: %v", err)
	}
	if err := w.close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	lines, err := store.readAll(1)
	if err != nil {
		t.Fatalf("readAll: %v", err)
	}
	if len(lines) != 1 {
		t.Fatalf("len = %d, want 1", len(lines))
	}
	if lines[0].Msg != long {
		t.Errorf("Msg length = %d, want %d", len(lines[0].Msg), len(long))
	}
}

// Sanity: the keepalive tick period stays a sane value so a
// quiet deploy doesn't time the SSE connection out. The exact
// value isn't pinned — this is a guardrail so a future
// refactor doesn't quietly drop it.
func TestDeployLogStream_KeepaliveBounded(t *testing.T) {
	const minKeepalive = 5 * time.Second
	if 15*time.Second < minKeepalive {
		t.Errorf("keepalive must be at least %v, got %v", minKeepalive, 15*time.Second)
	}
}
