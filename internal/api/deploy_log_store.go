package api

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// deploy_log_store.go manages per-deploy log files on disk. The file
// is the single source of truth: every line the deploy worker
// produces is appended, and the SSE handler tail-reads on demand.
//
// Why files instead of an in-memory hub:
//
//   - Operators care about deploy history. An in-memory hub with a
//     5-minute TTL is a UX bug — every "open the deploys tab to
//     check what happened an hour ago" comes back empty.
//   - Disk is cheap; deploy logs are small. A long compose pull
//     tops out at a few hundred KB, and we keep one file per
//     deploy for the lifetime of the deploy row.
//   - A reboot no longer wipes history. The hub lived in process
//     memory; the file persists across restarts.
//
// File format:
//
//	<line>\n                  — a normal log line (verbatim msg)
//	|<key>\t<msg>\n           — a keyed "in-place replace" line
//	                            (same wire shape as the SSE
//	                            `line-replace` event's `data`
//	                            field, with a leading `|` so the
//	                            reader can tell them apart without
//	                            any JSON parsing)
//
// The `|` prefix for keyed lines is unambiguous in practice: a
// docker pull progress message or a compose up log line never
// starts with a literal `|`. We don't bother with a length-prefix
// or escape encoding because both key and msg come from a trusted
// source (compose JSON events / our own annotations) and never
// contain newlines.
//
// Atomicity: writes go through O_APPEND, which POSIX guarantees
// are atomic for buffers up to PIPE_BUF (typically 4 KiB on
// Linux). A single deploy log line is well under that, so a
// deploy worker that crashes mid-write leaves the file with a
// clean line boundary — readers never see a torn line.

const (
	// deployLogKindPrefix is the byte that marks a keyed
	// ("replaceable") line. Everything else is a plain line.
	deployLogKindPrefix = '|'
)

// deployLogStore owns the directory that holds per-deploy log
// files. One file per deploy, named "<deployID>.log". The store
// is concurrency-safe: writers from the deploy worker and
// readers from the SSE handler can use the same store
// simultaneously (the OS handles file-level locking via
// O_APPEND for writes and the readers open their own FD for
// reads).
type deployLogStore struct {
	dir string
}

// newDeployLogStore returns a store rooted at dir. The directory
// is created (mkdir -p) on first call so a fresh boot never
// fails because the dir is missing. main.go calls the exported
// form (NewDeployLogStore) at boot; tests use the unexported
// form via t.TempDir() to keep the package self-contained.
func newDeployLogStore(dir string) (*deployLogStore, error) {
	if dir == "" {
		return nil, fmt.Errorf("deploy log dir must not be empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create deploy log dir: %w", err)
	}
	return &deployLogStore{dir: dir}, nil
}

// NewDeployLogStore is the public constructor used by main.go.
func NewDeployLogStore(dir string) (*deployLogStore, error) { return newDeployLogStore(dir) }

// pathFor returns the file path for a given deploy. Pure helper;
// used by both the writer and the reader.
func (s *deployLogStore) pathFor(deployID int) string {
	return filepath.Join(s.dir, strconv.Itoa(deployID)+".log")
}

// OpenWriter returns a writer for the deploy's log file. The
// caller (the deploy worker) is expected to call Close when the
// deploy reaches a terminal state. Two writers for the same
// deployID would race on append, but in practice only the
// worker writes — readers use ReadAll.
//
// The writer is buffered so a chatty compose progress stream
// doesn't issue one write(2) per line. Flush is called after
// every Append so a crash loses at most one buffered line, not
// the whole burst (the OS still has the previous lines).
type deployLogWriter struct {
	f  *os.File
	bw *bufio.Writer
	mu sync.Mutex
}

func (s *deployLogStore) openWriter(deployID int) (*deployLogWriter, error) {
	f, err := os.OpenFile(s.pathFor(deployID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open deploy log file: %w", err)
	}
	return &deployLogWriter{f: f, bw: bufio.NewWriter(f)}, nil
}

// AppendLine writes a normal log line. The msg is written as-is
// (no escaping needed; we control the writer).
func (w *deployLogWriter) appendLine(msg string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.bw.WriteString(msg); err != nil {
		return err
	}
	if err := w.bw.WriteByte('\n'); err != nil {
		return err
	}
	return w.bw.Flush()
}

// AppendKeyed writes a replaceable (keyed) line. The key is
// expected to be a stable identifier (e.g. a docker image layer
// SHA) and msg is the human-readable progress text. The reader
// (and the SSE layer) treat a new occurrence of the same key as
// an in-place update of the existing row.
func (w *deployLogWriter) appendKeyed(key, msg string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.bw.WriteByte(deployLogKindPrefix); err != nil {
		return err
	}
	if _, err := w.bw.WriteString(key); err != nil {
		return err
	}
	if err := w.bw.WriteByte('\t'); err != nil {
		return err
	}
	if _, err := w.bw.WriteString(msg); err != nil {
		return err
	}
	if err := w.bw.WriteByte('\n'); err != nil {
		return err
	}
	return w.bw.Flush()
}

// Close flushes any buffered bytes and closes the file. Safe to
// call multiple times (idempotent) so a defer in the deploy
// worker can call it without coordinating with a Close on the
// error path.
func (w *deployLogWriter) close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.bw.Flush()
	if cerr := w.f.Close(); cerr != nil && err == nil {
		err = cerr
	}
	w.f = nil
	return err
}

// deployLogReadLine is one parsed line. Exactly one of key or
// (msg alone) is meaningful; if Key is set, the line was a
// replaceable progress line.
type deployLogReadLine struct {
	Msg string
	Key string // empty for plain lines
}

// ReadAll returns every line in the deploy's log file in order.
// Returns os.ErrNotExist when the deploy hasn't written any
// lines yet (file not yet created) — callers should treat that
// as "no log available" and either 404 or wait, depending on
// whether the deploy is in flight.
func (s *deployLogStore) readAll(deployID int) ([]deployLogReadLine, error) {
	f, err := os.Open(s.pathFor(deployID))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return parseDeployLog(f)
}

// Tail returns a function that, when called, returns the lines
// appended to the file since the previous call. The first call
// returns the entire file; subsequent calls return only new
// lines. EOF is signalled by a nil line slice with a nil error
// (we've caught up to the writer).
//
// Implementation: we open the file once and stat the inode on
// every poll. If the size hasn't changed, return nil. If the
// size grew, seek to the previous size and read forward. The
// 1s poll loop in the SSE handler calls this; a higher poll
// rate would need fsnotify (intentionally avoided as a
// dependency for now).
//
// Returns os.ErrNotExist when the file doesn't exist yet
// (deploy hasn't started writing) — the caller should treat
// that the same as EOF and retry on the next tick.
func (s *deployLogStore) tail(deployID int) (func() ([]deployLogReadLine, error), error) {
	f, err := os.Open(s.pathFor(deployID))
	if err != nil {
		return nil, err
	}
	// Start at offset 0 so the first call replays the full file.
	// For a "live only" tail we'd start at the current end, but
	// the SSE handler always wants the full history.
	offset := int64(0)
	return func() ([]deployLogReadLine, error) {
		info, err := f.Stat()
		if err != nil {
			return nil, err
		}
		size := info.Size()
		if size == offset {
			return nil, nil
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return nil, err
		}
		lines, err := parseDeployLog(f)
		if err != nil {
			return nil, err
		}
		// Advance offset to the new end. We rely on the file being
		// append-only; a truncation would reset our cursor and we'd
		// double-replay lines. The deploy worker never truncates.
		offset = size
		return lines, nil
	}, nil
}

// Remove deletes the deploy's log file. Returns nil if the file
// doesn't exist (idempotent — the file may never have been
// created if the deploy produced no output). Called when the
// app is deleted so the on-disk state matches the DB.
func (s *deployLogStore) remove(deployID int) error {
	err := os.Remove(s.pathFor(deployID))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// parseDeployLog reads newline-delimited lines from r and
// returns them as parsed entries. The reader is consumed until
// io.EOF or the first read error. A trailing partial line
// (no newline) is preserved as the last entry — the writer
// uses O_APPEND so a line is either fully present or not
// present at all, except for the very last one being written
// at the time of the read.
func parseDeployLog(r io.Reader) ([]deployLogReadLine, error) {
	var out []deployLogReadLine
	scanner := bufio.NewScanner(r)
	// Allow long lines (compose progress messages with embedded
	// JSON can exceed the default 64 KiB scanner cap on busy
	// registries). 1 MiB is a comfortable upper bound for a
	// single line; anything bigger is almost certainly a
	// corrupt / runaway writer.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		text := scanner.Text()
		if text == "" {
			continue
		}
		if text[0] == deployLogKindPrefix {
			rest := text[1:]
			tab := strings.IndexByte(rest, '\t')
			if tab < 0 {
				// Malformed: prefix without tab. Surface as a
				// plain line so the operator at least sees the
				// raw text rather than losing it.
				out = append(out, deployLogReadLine{Msg: text})
				continue
			}
			out = append(out, deployLogReadLine{
				Key: rest[:tab],
				Msg: rest[tab+1:],
			})
			continue
		}
		out = append(out, deployLogReadLine{Msg: text})
	}
	if err := scanner.Err(); err != nil {
		return out, err
	}
	return out, nil
}
