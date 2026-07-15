package docker

import (
	"strings"
	"testing"
)

// The JSON samples below are verbatim from a real `docker compose
// --progress json up` capture (compose v5.1.2). They exercise every
// event shape the parser must handle: top-level image pull, layer byte
// progress, layer milestones, container lifecycle, image-level error,
// and the separate fatal-error envelope.

func TestTranslateComposeEvent_LayerDownloadProgress(t *testing.T) {
	// A "Downloading" tick carries current/total/percent and should
	// produce a keyed replace line (so the same row updates in place
	// rather than scrolling a new line per tick).
	pct := int(98)
	ev := composeProgressEvent{
		ID:       "2dd7199cff98",
		Status:   "Working",
		Text:     "Downloading",
		ParentID: "Image alpine:3.21",
		Details:  "3.932MB",
		Current:  ptrInt64(3931516),
		Total:    ptrInt64(3994465),
		Percent:  &pct,
	}
	got := translateComposeEvent(ev)
	if got.Key != "2dd7199cff98" {
		t.Errorf("Key = %q, want 2dd7199cff98 (replace in place)", got.Key)
	}
	if !strings.Contains(got.Msg, "2dd7199cff98") || !strings.Contains(got.Msg, "Downloading") {
		t.Errorf("Msg = %q, want short-id + Downloading", got.Msg)
	}
	if !strings.Contains(got.Msg, "3.932MB") {
		t.Errorf("Msg = %q, want details 3.932MB", got.Msg)
	}
	if !strings.Contains(got.Msg, "[98%]") {
		t.Errorf("Msg %q should contain [98%%]", got.Msg)
	}
}

func TestTranslateComposeEvent_LayerMilestone(t *testing.T) {
	// "Pull complete" is a Done milestone with percent but no
	// current/total. It still carries the layer key so it replaces the
	// download row in place with the final state.
	ev := composeProgressEvent{
		ID:       "2dd7199cff98",
		Status:   "Done",
		Text:     "Pull complete",
		ParentID: "Image alpine:3.21",
		Details:  "0B",
		Percent:  ptrInt(100),
	}
	got := translateComposeEvent(ev)
	if got.Key != "2dd7199cff98" {
		t.Errorf("Key = %q, want layer id", got.Key)
	}
	if !strings.Contains(got.Msg, "Pull complete") {
		t.Errorf("Msg = %q, want Pull complete", got.Msg)
	}
	// Milestone without byte progress should not print a [0%] bracket.
	if strings.Contains(got.Msg, "[") {
		t.Errorf("Msg = %q, should not include percent bracket (no current/total)", got.Msg)
	}
}

func TestTranslateComposeEvent_TopLevelImage(t *testing.T) {
	// "Image alpine:3.21" Pulling is a top-level event (no parent_id);
	// the id itself is the human label, so the line is "Image alpine:3.21 Pulling".
	ev := composeProgressEvent{
		ID:     "Image alpine:3.21",
		Status: "Working",
		Text:   "Pulling",
	}
	got := translateComposeEvent(ev)
	if got.Key != "Image alpine:3.21" {
		t.Errorf("Key = %q, want Image alpine:3.21", got.Key)
	}
	if got.Msg != "Image alpine:3.21 Pulling" {
		t.Errorf("Msg = %q, want 'Image alpine:3.21 Pulling'", got.Msg)
	}
}

func TestTranslateComposeEvent_ContainerStarted(t *testing.T) {
	ev := composeProgressEvent{
		ID:     "Container app-web-1",
		Status: "Done",
		Text:   "Started",
	}
	got := translateComposeEvent(ev)
	if got.Key != "Container app-web-1" {
		t.Errorf("Key = %q, want Container app-web-1", got.Key)
	}
	if got.Msg != "Container app-web-1 Started" {
		t.Errorf("Msg = %q, want 'Container app-web-1 Started'", got.Msg)
	}
}

func TestTranslateComposeEvent_ImageLevelError(t *testing.T) {
	// An image-level error event surfaces the details as the message and
	// has NO key (append, don't replace) so the error is never collapsed
	// into the pulling row and lost.
	ev := composeProgressEvent{
		ID:      "Image alpine:3.21",
		Status:  "Error",
		Text:    "Error",
		Details: `Get "https://registry-1.docker.io/v2/": TLS handshake timeout`,
	}
	got := translateComposeEvent(ev)
	if got.Key != "" {
		t.Errorf("Key = %q, want empty (error must append, not replace)", got.Key)
	}
	if !strings.Contains(got.Msg, "TLS handshake timeout") {
		t.Errorf("Msg = %q, want the error details", got.Msg)
	}
	if !strings.Contains(got.Msg, "Image alpine:3.21") {
		t.Errorf("Msg = %q, want the image id prefix", got.Msg)
	}
}

func TestComposeProgressWriter_ParsesRealStream(t *testing.T) {
	// Feed a sequence of real JSON lines (layer pull -> download ticks
	// -> complete -> container start -> fatal error) and assert the
	// emitted composeLines have the right keys and that download ticks
	// for the same layer share a key (so the consumer can replace).
	raw := strings.Join([]string{
		`{"id":"Image alpine:3.21","status":"Working","text":"Pulling"}`,
		`{"id":"2dd7199cff98","parent_id":"Image alpine:3.21","status":"Working","text":"Pulling fs layer","details":"0B"}`,
		`{"id":"2dd7199cff98","parent_id":"Image alpine:3.21","status":"Working","text":"Downloading","details":"48.51kB","current":48508,"total":3994465,"percent":1}`,
		`{"id":"2dd7199cff98","parent_id":"Image alpine:3.21","status":"Working","text":"Downloading","details":"3.932MB","current":3931516,"total":3994465,"percent":98}`,
		`{"id":"2dd7199cff98","parent_id":"Image alpine:3.21","status":"Done","text":"Pull complete","details":"0B","percent":100}`,
		`{"id":"Image alpine:3.21","status":"Done","text":"Pulled"}`,
		`{"id":"Container app-tiny-1","status":"Done","text":"Started"}`,
		`{"error":true,"message":"Error response from daemon: boom"}`,
		``, // blank line should be dropped, not fatal
	}, "\n") + "\n"

	var got []composeLine
	w := newComposeProgressWriter(func(cl composeLine) { got = append(got, cl) })
	if _, err := w.Write([]byte(raw)); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if len(got) != 8 {
		t.Fatalf("got %d lines, want 8: %+v", len(got), got)
	}
	// First: image pulling (key = image id).
	if got[0].Key != "Image alpine:3.21" || !strings.Contains(got[0].Msg, "Pulling") {
		t.Errorf("line 0 = %+v, want image Pulling", got[0])
	}
	// Download ticks share the layer key (replace semantics).
	if got[2].Key != "2dd7199cff98" || got[3].Key != "2dd7199cff98" {
		t.Errorf("download ticks should share layer key: got %+v / %+v", got[2], got[3])
	}
	if !strings.Contains(got[2].Msg, "48.51kB") || !strings.Contains(got[3].Msg, "3.932MB") {
		t.Errorf("download tick msgs = %q / %q", got[2].Msg, got[3].Msg)
	}
	// Fatal error appends with no key and carries the message.
	last := got[7]
	if last.Key != "" {
		t.Errorf("fatal error Key = %q, want empty (append)", last.Key)
	}
	if !strings.Contains(last.Msg, "boom") {
		t.Errorf("fatal error Msg = %q, want 'boom'", last.Msg)
	}
}

func TestComposeProgressWriter_ChunkedWrites(t *testing.T) {
	// A single JSON object split across two Write calls must still
	// decode once the newline arrives (the writer buffers partial
	// lines, mirroring progressWriter's chunk handling).
	var got []composeLine
	w := newComposeProgressWriter(func(cl composeLine) { got = append(got, cl) })
	full := `{"id":"abc","status":"Working","text":"Pulling"}` + "\n"
	half := len(full) / 2
	if _, err := w.Write([]byte(full[:half])); err != nil {
		t.Fatalf("Write 1: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("after partial write, got %d lines, want 0", len(got))
	}
	if _, err := w.Write([]byte(full[half:])); err != nil {
		t.Fatalf("Write 2: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("after full write, got %d lines, want 1", len(got))
	}
	if got[0].Key != "abc" {
		t.Errorf("Key = %q, want abc", got[0].Key)
	}
}

func TestComposeProgressWriter_NonJSONDroppedOrSurfaced(t *testing.T) {
	// A line that isn't valid JSON for either envelope is surfaced as
	// raw text (append, no key) so the operator still sees it - e.g. a
	// compose warning printed outside the progress protocol.
	var got []composeLine
	w := newComposeProgressWriter(func(cl composeLine) { got = append(got, cl) })
	if _, err := w.Write([]byte("not json at all\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d lines, want 1", len(got))
	}
	if got[0].Key != "" {
		t.Errorf("Key = %q, want empty (raw text append)", got[0].Key)
	}
	if got[0].Msg != "not json at all" {
		t.Errorf("Msg = %q, want raw text", got[0].Msg)
	}
}

func ptrInt64(v int64) *int64 { return &v }
func ptrInt(v int) *int       { return &v }
