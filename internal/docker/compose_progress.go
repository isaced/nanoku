package docker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// composeProgressEvent is the per-line JSON object emitted by
// `docker compose --progress json` (compose v5.1.2 schema, captured from a
// real pull). One object per line on stderr. Field sets vary by event type:
//
//   - top-level image/network/container events: {id, status, text}
//   - layer milestone (Pulling fs layer / Verifying Checksum): +parent_id, +details
//   - layer byte progress (Downloading / Extracting): +current, +total, +percent
//   - layer completion (Download complete / Pull complete): +details, +percent
//   - image-level error: {id, status:"Error", text:"Error", details:"<msg>"}
//
// The fatal terminal error is a SEPARATE shape: {error:true, message:"..."}.
type composeProgressEvent struct {
	ID       string `json:"id"`
	Status   string `json:"status"`             // "Working" | "Done" | "Error"
	Text     string `json:"text"`               // "Pulling"/"Downloading"/"Pull complete"/"Creating"/"Started"...
	ParentID string `json:"parent_id,omitempty"`
	Details  string `json:"details,omitempty"`  // "3.994MB" / "0B" / error text
	Current  *int64 `json:"current,omitempty"`  // bytes so far (Downloading/Extracting only)
	Total    *int64 `json:"total,omitempty"`    // total bytes
	Percent  *int   `json:"percent,omitempty"`  // 0-100
}

// composeFatalError is the terminal object compose emits on hard failure,
// e.g. a registry TLS timeout. It is distinct from the per-event envelope:
// {error:true, message:"..."}. We treat it as a normal append line (no key).
type composeFatalError struct {
	Error   bool   `json:"error"`
	Message string `json:"message"`
}

// composeLine is one translated output line. When Key != "" the consumer
// should REPLACE the previous line with the same Key in-place rather than
// append; this is what makes per-layer download progress update a single
// row instead of scrolling a new line per tick.
type composeLine struct {
	Msg string
	Key string // "" => append; non-empty => replace same-key line
}

// translateComposeEvent maps a structured compose progress event to a
// human-readable log line plus a replacement key.
//
// Replacement strategy: every event for the same entity ID (layer SHA,
// "Image <ref>", "Container <name>", "Network <name>") shares a Key, so
// successive states for that entity overwrite the same log row. A 50 MB
// layer thus occupies exactly one row that updates from "Pulling fs layer"
// -> "Downloading 3.9MB [98%]" -> "Pull complete", instead of dozens of
// scrolling lines. Fatal errors and other non-entity messages have Key=""
// and append like ordinary log lines.
func translateComposeEvent(ev composeProgressEvent) composeLine {
	// Image-level error: surface verbatim, append (no key so it isn't
	// collapsed into the pulling row and lost).
	if ev.Status == "Error" || ev.Text == "Error" {
		msg := ev.Text
		if ev.Details != "" {
			msg = ev.Details
		}
		return composeLine{Msg: fmt.Sprintf("%s: %s", ev.ID, msg)}
	}

	key := ev.ID
	// Layer events (have a parent_id) show the short layer id + state +
	// details/percent. Top-level entity events (Image/Container/Network)
	// show "<id> <text>".
	if ev.ParentID != "" {
		msg := fmt.Sprintf("%s %s", shortLayerID(ev.ID), ev.Text)
		if ev.Details != "" {
			msg += " " + ev.Details
		}
		// Show the percent bracket only while bytes are actively
		// moving (current/total present): a Downloading/Extracting
		// tick. Completion milestones (Download complete, Pull
		// complete) carry percent:100 but no current/total - showing
		// "[100%]" next to "Pull complete" is redundant noise.
		if ev.Current != nil && ev.Percent != nil && *ev.Percent > 0 {
			msg += fmt.Sprintf(" [%d%%]", *ev.Percent)
		}
		return composeLine{Msg: msg, Key: key}
	}
	return composeLine{Msg: fmt.Sprintf("%s %s", ev.ID, ev.Text), Key: key}
}

// composeProgressWriter is an io.Writer that decodes the compose
// `--progress json` stream (one JSON object per line on stderr) and emits
// translated composeLine values via the emit callback. It is the compose
// analogue of progressWriter (which handles the Engine ImagePull JSON
// stream for docker-mode pulls).
//
// Malformed lines (rare; the engine sometimes writes a blank line between
// events) are dropped, not fatal. A fatal {error:true} object is emitted
// as an append line and the stream continues - the CLI's own exit code is
// the source of truth for failure; this writer just surfaces the message.
type composeProgressWriter struct {
	emit func(composeLine)
	buf  []byte
}

func newComposeProgressWriter(emit func(composeLine)) *composeProgressWriter {
	return &composeProgressWriter{emit: emit}
}

func (p *composeProgressWriter) Write(chunk []byte) (int, error) {
	p.buf = append(p.buf, chunk...)
	for {
		idx := bytes.IndexByte(p.buf, '\n')
		if idx < 0 {
			break
		}
		line := p.buf[:idx]
		p.buf = p.buf[idx+1:]
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}
		// Try the fatal-error shape first: it has a top-level "error"
		// bool. The per-event envelope never has that field, so this
		// disambiguates without a full double-decode.
		var fatal composeFatalError
		if err := json.Unmarshal(trimmed, &fatal); err == nil && fatal.Error {
			p.emit(composeLine{Msg: "compose: " + fatal.Message})
			continue
		}
		var ev composeProgressEvent
		if err := json.Unmarshal(trimmed, &ev); err != nil {
			// Not JSON we recognize: surface the raw text so the
			// operator still sees it (e.g. a compose warning printed
			// outside the progress protocol).
			p.emit(composeLine{Msg: strings.TrimSpace(string(trimmed))})
			continue
		}
		p.emit(translateComposeEvent(ev))
	}
	return len(chunk), nil
}

// Ensure the writer satisfies io.Writer at compile time.
var _ io.Writer = (*composeProgressWriter)(nil)
