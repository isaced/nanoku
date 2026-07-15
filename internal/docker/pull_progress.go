package docker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

// pullProgressEvent is the subset of the docker ImagePull JSON stream we
// care about. The engine emits one of these per state transition; the
// `id` field carries the layer SHA for per-layer events, and the empty
// id identifies image-level events (Pulling from, Pull complete, etc.).
type pullProgressEvent struct {
	Status string `json:"status"`
	ID     string `json:"id"`
	Error  string `json:"error"`
}

// progressWriter is an io.Writer adapter that sits between the engine's
// raw ImagePull JSON stream (passed to PullImage's WithPullStream) and a
// downstream line-oriented consumer. It decodes the JSON one line at a
// time and writes a single clean human-readable line per interesting
// state transition:
//
//	{"status":"Pulling fs layer","id":"<sha>"}    → "Pulling <short-sha>"
//	{"status":"Download complete","id":"<sha>"}    → "Layer <short-sha> downloaded"
//	{"status":"Extracting","id":"<sha>"}          → "Extracting <short-sha>"
//	{"status":"Pull complete", ...}                → "Pull complete"
//	{"error":"...","errorDetail":{...}}           → "pull error: <message>"
//
// Noisy events (Downloading with progressDetail, Verifying Checksum,
// Waiting, Digest, Already exists, …) are dropped — the operator gets a
// per-layer milestone trail and a final status, not 20+ lines of
// progress updates for a 50 MB layer. Per-byte progress is the UI's
// job if it wants it; the log feed is text-only.
//
// The output is line-oriented: each emit ends with '\n', so a downstream
// lineWriter (or any bufio.Scanner / split-by-newline consumer) can
// treat each entry as a complete log line.
type progressWriter struct {
	w   io.Writer
	buf []byte
}

func newProgressWriter(w io.Writer) *progressWriter {
	return &progressWriter{w: w}
}

func (p *progressWriter) Write(chunk []byte) (int, error) {
	p.buf = append(p.buf, chunk...)
	for {
		idx := bytes.IndexByte(p.buf, '\n')
		if idx < 0 {
			break
		}
		line := p.buf[:idx]
		p.buf = p.buf[idx+1:]
		var ev pullProgressEvent
		// A malformed line is dropped, not fatal. The engine rarely
		// emits non-JSON, and the alternative (returning an error) would
		// abort the whole pull for one bad line. The empty-line case
		// (engine writes "\n" between events) is also dropped, which
		// keeps the log clean of blank lines.
		if err := json.Unmarshal(line, &ev); err != nil {
			continue
		}
		if msg, ok := translatePullEvent(ev); ok {
			if _, err := io.WriteString(p.w, msg+"\n"); err != nil {
				return 0, err
			}
		}
	}
	return len(chunk), nil
}

func translatePullEvent(ev pullProgressEvent) (string, bool) {
	// Errors always surface verbatim — they are the most important
	// thing in a deploy log and the engine already formats them as a
	// short one-liner.
	if ev.Error != "" {
		return fmt.Sprintf("pull error: %s", ev.Error), true
	}
	switch ev.Status {
	case "Pulling fs layer":
		return "Pulling " + shortLayerID(ev.ID), true
	case "Download complete":
		return "Layer " + shortLayerID(ev.ID) + " downloaded", true
	case "Extracting":
		return "Extracting " + shortLayerID(ev.ID), true
	case "Pull complete":
		return "Pull complete", true
	}
	// Noisy events intentionally dropped:
	//   - "Waiting" / "Downloading" with progressDetail (fires per layer
	//     many times per second)
	//   - "Verifying Checksum" / "Digest" (transient, not actionable)
	//   - "Already exists" (cached, no work to report)
	return "", false
}

// shortLayerID trims a 64-char layer SHA to its 12-char prefix, which is
// enough to disambiguate layers in a log line and matches the convention
// docker uses in its own progress UI.
func shortLayerID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
