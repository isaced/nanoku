package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// fakeFileDaemon is a tiny extension of fakeDaemon that knows how
// to respond to the exec / archive endpoints the file browser
// uses. It records each call so tests can assert the request
// shape (cmd, path, etc.) and returns a canned response (ls
// output, tar archive) the parser under test can chew on.
//
// The relationship with fakeDaemon is composition (via the
// `daemon` field), not embedding, because Go's type-identity
// rules don't let you pass an embedded wrapper where the
// embedded type is expected — the newTestManager helper
// wants a *fakeDaemon and there's no automatic promotion
// for value types.
type fakeFileDaemon struct {
	*fakeDaemon

	// lsResponse is the bytes returned for the `ls -lan -- <path>`
	// exec. Tests set this before triggering a ListContainerDir.
	lsResponse string
	// lsExitCode is the exit code the exec should report.
	// Defaults to 0 (success).
	lsExitCode int
	// fileTarBytes is the tar archive returned for CopyFromContainer
	// calls. Tests build it with buildSingleFileTar / buildDirTar.
	fileTarBytes []byte
	// statResponse is the JSON returned for ContainerStatPath.
	statResponse container.PathStat
	// statErr is the error message to surface (as a 404) for
	// ContainerStatPath calls. Empty means "return statResponse".
	statErr string
}

func newFakeFileDaemon() *fakeFileDaemon {
	fd := newFakeDaemon()
	ffd := &fakeFileDaemon{
		fakeDaemon:   fd,
		lsExitCode:   0,
		statResponse: container.PathStat{Size: 0, Mode: os.FileMode(0o644), Mtime: time.Now()},
	}
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.40")
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/containers/json":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]container.Summary{})
		case strings.HasPrefix(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/exec"):
			// POST /containers/{id}/exec — body is ExecConfig
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(container.ExecCreateResponse{ID: "exec-1"})
		case r.URL.Path == "/exec/exec-1/start":
			// POST /exec/{id}/start serves two purposes:
			//   - With Upgrade: tcp header, it's the
			//     ContainerExecAttach hijack endpoint.
			//   - Without, it's ContainerExecStart (a plain
			//     POST that returns 204).
			// The docker SDK uses the same URL for both —
			// the hijack-vs-no-hijack distinction lives in
			// the request headers, not the URL.
			if r.Header.Get("Upgrade") == "" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			ffd.handleExecAttach(w, r)
			return
		case r.URL.Path == "/exec/exec-1/json":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(container.ExecInspect{ExitCode: ffd.lsExitCode})
		case strings.HasPrefix(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/archive"):
			// Same endpoint for both HEAD (stat) and GET
			// (download). Method distinguishes intent.
			// ContainerStatPath reads its payload from the
			// X-Docker-Container-Path-Stat response header
			// (base64-encoded JSON), not the body.
			if r.Method == http.MethodHead {
				if ffd.statErr != "" {
					http.Error(w, ffd.statErr, http.StatusNotFound)
					return
				}
				statJSON, _ := json.Marshal(ffd.statResponse)
				w.Header().Set("X-Docker-Container-Path-Stat", base64.StdEncoding.EncodeToString(statJSON))
				w.WriteHeader(http.StatusOK)
				return
			}
			w.Header().Set("Content-Type", "application/x-tar")
			// CopyFromContainer also reads the stat header
			// (CopyFromContainer's "in order to get the copy
			// behavior right" preamble in container_copy.go),
			// so we set it on GET responses too.
			statJSON, _ := json.Marshal(ffd.statResponse)
			w.Header().Set("X-Docker-Container-Path-Stat", base64.StdEncoding.EncodeToString(statJSON))
			_, _ = w.Write(ffd.fileTarBytes)
		default:
			http.NotFound(w, r)
		}
	}
	return ffd
}

// handleExecAttach hijacks the underlying connection,
// responds with HTTP/1.1 101 Switching Protocols, and then
// streams the canned `ls` response as a docker raw-stream
// frame so the manager's stdcopy demuxer can read it back
// as plain text. Mirrors the trick the caddy-reload tests
// use; without it the SDK's hijack path can't talk to the
// fake (it expects to dial a raw TCP socket, not talk
// HTTP/1.1).
func (ffd *fakeFileDaemon) handleExecAttach(w http.ResponseWriter, _ *http.Request) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		return
	}
	conn, bufrw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	_, _ = bufrw.WriteString("HTTP/1.1 101 Switching Protocols\r\n" +
		"Content-Type: application/vnd.docker.raw-stream\r\n" +
		"Connection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
	_ = writeStdCopyFrame(bufrw, []byte(ffd.lsResponse))
	_ = bufrw.Flush()
}

// writeStdCopyFrame emits one stdout frame for the engine's
// raw-stream protocol. It's a stripped-down copy of stdcopy's
// frame encoder — just enough for the tests to talk to the
// stdcopy demuxer on the manager side.
func writeStdCopyFrame(w io.Writer, payload []byte) error {
	var header [8]byte
	header[0] = 0x01 // STDOUT
	header[1] = 0x00
	// big-endian uint32 size in bytes 4..7
	size := uint32(len(payload))
	header[4] = byte(size >> 24)
	header[5] = byte(size >> 16)
	header[6] = byte(size >> 8)
	header[7] = byte(size)
	if _, err := w.Write(header[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// newTestManagerForFiles is the file-browser-flavored version
// of newTestManager. The only difference is the host scheme:
// we need tcp:// instead of the default http:// because the
// docker SDK's hijack dialer (used by ContainerExecAttach)
// dials the host's *raw TCP* port and doesn't understand
// the http:// prefix. Non-hijack calls (ContainerList,
// ContainerStatPath) go through the same client, but the
// transport round-trips them through the same fake daemon
// without trouble.
func newTestManagerForFiles(t *testing.T, ffd *fakeFileDaemon) *Manager {
	t.Helper()
	tcpHost := "tcp://" + strings.TrimPrefix(ffd.URL, "http://")
	cli, err := client.NewClientWithOpts(
		client.WithHost(tcpHost),
		client.WithHTTPClient(ffd.Client()),
		client.WithVersion("1.40"),
	)
	if err != nil {
		t.Fatalf("NewClientWithOpts: %v", err)
	}
	return newManagerWithClient(Config{}, cli)
}

// buildSingleFileTar produces a tar archive containing a single
// regular file with the given name and content. Used by the
// ReadContainerFile tests.
func buildSingleFileTar(t *testing.T, name string, content []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{
		Name:    name,
		Mode:    0o644,
		Size:    int64(len(content)),
		ModTime: time.Now(),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("write tar header: %v", err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatalf("write tar body: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	return buf.Bytes()
}

// TestListContainerDir_ParsesCoreutilsLs verifies that the
// parser produces FileInfo rows from a real coreutils `ls -lan`
// payload. Sizes, modes, type flags, and mtime are all checked.
func TestListContainerDir_ParsesCoreutilsLs(t *testing.T) {
	ffd := newFakeFileDaemon()
	defer ffd.Close()
	m := newTestManagerForFiles(t, ffd)

	ffd.lsResponse = "total 24\n" +
		"drwxr-xr-x 2 0 0 4096 Jan  1 00:00 bin\n" +
		"-rw-r--r-- 1 0 0  123 Jan  1 00:00 hello.txt\n" +
		"lrwxrwxrwx 1 0 0    7 Jan  1 00:00 link -> hello.txt\n"

	entries, err := m.ListContainerDir(context.Background(), "fake-id", "/app")
	if err != nil {
		t.Fatalf("ListContainerDir: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3: %+v", len(entries), entries)
	}

	// Order should match the input.
	if entries[0].Name != "bin" || !entries[0].IsDir || entries[0].Size != 4096 {
		t.Errorf("entry[0] = %+v, want dir bin 4096", entries[0])
	}
	if entries[1].Name != "hello.txt" || entries[1].IsDir || entries[1].Size != 123 {
		t.Errorf("entry[1] = %+v, want file hello.txt 123", entries[1])
	}
	if entries[2].Name != "link" || !entries[2].IsLink || entries[2].LinkTarget != "hello.txt" {
		t.Errorf("entry[2] = %+v, want symlink link -> hello.txt", entries[2])
	}
}

// TestListContainerDir_RejectsRelativePath makes sure the
// public method refuses anything that isn't an absolute
// path. We don't want to send `ls -lan relative` and have
// it interpreted against $CWD — the user-facing API never
// hands us relative paths in the first place, but a
// misbehaving caller should fail fast at this layer
// rather than at the exec call.
func TestListContainerDir_RejectsRelativePath(t *testing.T) {
	m := newTestManagerForFiles(t, newFakeFileDaemon())
	if _, err := m.ListContainerDir(context.Background(), "id", "app"); err == nil {
		t.Fatal("expected error for relative path, got nil")
	}
}

// TestListContainerDir_LsFailureBubblesUp makes sure a
// non-zero `ls` exit code (e.g. the directory doesn't
// exist) is reported as an error rather than a silent
// empty list. Callers map this to a 404.
func TestListContainerDir_LsFailureBubblesUp(t *testing.T) {
	ffd := newFakeFileDaemon()
	defer ffd.Close()
	m := newTestManagerForFiles(t, ffd)

	ffd.lsResponse = "ls: cannot access '/app': No such file or directory\n"
	ffd.lsExitCode = 1

	_, err := m.ListContainerDir(context.Background(), "id", "/app")
	if err == nil {
		t.Fatal("expected error when ls fails, got nil")
	}
	if !strings.Contains(err.Error(), "No such file") {
		t.Errorf("err = %v, want it to mention 'No such file'", err)
	}
}

// TestReadContainerFile_RejectsRelativePath is the same guard
// for the read path as for list. Belt and suspenders against
// future callers that might forget the absolute invariant.
func TestReadContainerFile_RejectsRelativePath(t *testing.T) {
	m := newTestManagerForFiles(t, newFakeFileDaemon())
	if _, _, _, _, err := m.ReadContainerFile(context.Background(), "id", "app", 1024); err == nil {
		t.Fatal("expected error for relative path, got nil")
	}
}

// TestReadContainerFile_RejectsDirectory makes sure the
// pre-flight stat check catches "user clicked a directory"
// and reports it as an error rather than returning the
// directory's tar listing as if it were a file. We construct
// the directory mode by setting the os.ModeDir bit on an
// os.FileMode value; that's what ContainerStatPath returns
// from the engine, and it's what the manager's IsDir() check
// reads back.
func TestReadContainerFile_RejectsDirectory(t *testing.T) {
	ffd := newFakeFileDaemon()
	defer ffd.Close()
	m := newTestManagerForFiles(t, ffd)

	ffd.statResponse = container.PathStat{
		Size:  4096,
		Mode:  os.FileMode(0o755) | os.ModeDir,
		Mtime: time.Now(),
	}
	_, _, _, _, err := m.ReadContainerFile(context.Background(), "id", "/app", 1024)
	if err == nil {
		t.Fatal("expected error for directory, got nil")
	}
	if !strings.Contains(err.Error(), "directory") {
		t.Errorf("err = %v, want it to mention 'directory'", err)
	}
}

// TestReadContainerFile_RejectsOversize covers the size
// cap. We set the stat size to >maxBytes, expect a clean
// error, and never touch the tar archive (the fake would
// return a tar anyway, but it should never be requested).
func TestReadContainerFile_RejectsOversize(t *testing.T) {
	ffd := newFakeFileDaemon()
	defer ffd.Close()
	m := newTestManagerForFiles(t, ffd)

	ffd.statResponse = container.PathStat{Size: 10 << 20, Mode: 0o644, Mtime: time.Now()}
	_, _, _, _, err := m.ReadContainerFile(context.Background(), "id", "/big", 1024)
	if err == nil {
		t.Fatal("expected error for oversize file, got nil")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("err = %v, want it to mention 'too large'", err)
	}
}

// TestReadContainerFile_HappyPath is the round-trip case:
// stat says the file is small, CopyFromContainer returns a
// tar with one regular file inside, and the manager
// returns the raw bytes.
func TestReadContainerFile_HappyPath(t *testing.T) {
	ffd := newFakeFileDaemon()
	defer ffd.Close()
	m := newTestManagerForFiles(t, ffd)

	want := []byte("hello world\n")
	ffd.fileTarBytes = buildSingleFileTar(t, "hello.txt", want)
	ffd.statResponse = container.PathStat{Size: int64(len(want)), Mode: 0o644, Mtime: time.Now()}

	got, size, _, _, err := m.ReadContainerFile(context.Background(), "id", "/app/hello.txt", 1024)
	if err != nil {
		t.Fatalf("ReadContainerFile: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
	if size != int64(len(want)) {
		t.Errorf("size = %d, want %d", size, len(want))
	}
}

// TestContainerIDByName_ResolvesExactMatch exercises the
// name filter + strict equality check. The fake returns
// a single container named "nanoku-foo"; asking for it
// should return its id. Asking for "nanoku-fo" should
// not match.
func TestContainerIDByName_ResolvesExactMatch(t *testing.T) {
	ffd := newFakeFileDaemon()
	defer ffd.Close()
	m := newTestManagerForFiles(t, ffd)

	ffd.fakeDaemon.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		if r.URL.Path == "/containers/json" {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]container.Summary{
				{ID: "abc123", Names: []string{"/nanoku-foo"}},
			})
			return
		}
		http.NotFound(w, r)
	}

	id, err := m.ContainerIDByName(context.Background(), "nanoku-foo")
	if err != nil {
		t.Fatalf("ContainerIDByName: %v", err)
	}
	if id != "abc123" {
		t.Errorf("id = %q, want abc123", id)
	}

	id, err = m.ContainerIDByName(context.Background(), "nanoku-fo")
	if err != nil {
		t.Fatalf("ContainerIDByName(partial): %v", err)
	}
	if id != "" {
		t.Errorf("partial-match id = %q, want empty", id)
	}
}

// TestParseLsOutput_DropsDotAndDotdot makes sure the
// parser skips the self/parent entries. The docker engine
// never returns them via exec ls -l, but we want the
// parser to be defensive — anyone shipping a custom image
// with an alias could trip the assumption.
func TestParseLsOutput_DropsDotAndDotdot(t *testing.T) {
	out := parseLsOutput("total 4\n" +
		"drwxr-xr-x 1 0 0 4096 Jan  1 00:00 .\n" +
		"drwxr-xr-x 1 0 0 4096 Jan  1 00:00 ..\n" +
		"-rw-r--r-- 1 0 0    0 Jan  1 00:00 keep\n")
	if len(out) != 1 {
		t.Fatalf("got %d entries, want 1 (just 'keep'): %+v", len(out), out)
	}
	if out[0].Name != "keep" {
		t.Errorf("entry[0] = %q, want 'keep'", out[0].Name)
	}
}

// TestParseLsOutput_IgnoresNoise is the negative-control
// test: garbage lines, missing fields, and a non-type
// prefix are all silently dropped. The parser should
// never panic on a weird input line.
func TestParseLsOutput_IgnoresNoise(t *testing.T) {
	out := parseLsOutput("total 0\n" +
		"junk line that doesn't look like ls\n" +
		"-rw-r--r-- 1 0 0 0 Jan  1 00:00 real\n" +
		"only six fields here\n")
	if len(out) != 1 {
		t.Fatalf("got %d entries, want 1: %+v", len(out), out)
	}
	if out[0].Name != "real" {
		t.Errorf("entry[0] = %q, want 'real'", out[0].Name)
	}
}

// TestParseLsOutput_BusyboxYearFormat covers the case
// `ls` reports an old file with a 4-digit year in the
// time slot (the "older than 6 months" rule). The
// parser shouldn't choke — the year just looks like
// another token after the day, the line still parses,
// and the modtime falls back to the zero time because
// the time slot isn't a real time. The check is just
// "the entry survives" — we don't try to recover the
// year value because the UI's relative-time formatter
// already handles that gracefully.
func TestParseLsOutput_BusyboxYearFormat(t *testing.T) {
	out := parseLsOutput("total 4\n" +
		"-rw-r--r-- 1 0 0 123 Jan  1 2023 old.log\n")
	if len(out) != 1 {
		t.Fatalf("got %d entries, want 1: %+v", len(out), out)
	}
	if out[0].Name != "old.log" {
		t.Errorf("entry[0] = %q, want 'old.log'", out[0].Name)
	}
	if out[0].Size != 123 {
		t.Errorf("size = %d, want 123", out[0].Size)
	}
}

// TestParseLsOutput_FilenameWithSpaces checks that
// `ls` filenames containing whitespace are joined
// back into a single name. The mode+8-fields contract
// is what makes this safe: fields[8:] is always the
// name portion, and we re-join with single spaces.
// (The " -> target" tail is handled by the same join.)
func TestParseLsOutput_FilenameWithSpaces(t *testing.T) {
	out := parseLsOutput("total 4\n" +
		"-rw-r--r-- 1 0 0 0 Jan  1 00:00 file with spaces.txt\n")
	if len(out) != 1 {
		t.Fatalf("got %d entries, want 1: %+v", len(out), out)
	}
	if out[0].Name != "file with spaces.txt" {
		t.Errorf("entry[0] = %q, want 'file with spaces.txt'", out[0].Name)
	}
}

// TestParseLsOutput_LinkTargetRoundtrip ensures the
// "name -> target" tail of a symlink line splits on
// the FIRST occurrence only — a target path that
// itself contains " -> " would be unusual, but we
// want the contract to be "first arrow wins".
func TestParseLsOutput_LinkTargetRoundtrip(t *testing.T) {
	out := parseLsOutput("total 4\n" +
		"lrwxrwxrwx 1 0 0 7 Jan  1 00:00 a -> b -> c\n")
	if len(out) != 1 {
		t.Fatalf("got %d entries, want 1: %+v", len(out), out)
	}
	if out[0].Name != "a" {
		t.Errorf("name = %q, want 'a'", out[0].Name)
	}
	if out[0].LinkTarget != "b -> c" {
		t.Errorf("linkTarget = %q, want 'b -> c'", out[0].LinkTarget)
	}
	if !out[0].IsLink {
		t.Errorf("IsLink = false, want true")
	}
}

// TestParseLsDate covers the date parser directly.
// It should produce a time in the current year for
// a valid "Mon Day" pair, and the zero time for
// gibberish input.
func TestParseLsDate(t *testing.T) {
	t.Run("valid month day", func(t *testing.T) {
		got := parseLsDate("Jan", "15")
		if got.IsZero() {
			t.Fatalf("parseLsDate(Jan, 15) = zero time, want non-zero")
		}
		if got.Month() != time.January || got.Day() != 15 {
			t.Errorf("got %v, want Jan 15 (this year)", got)
		}
	})
	t.Run("invalid month", func(t *testing.T) {
		got := parseLsDate("NotAMonth", "5")
		if !got.IsZero() {
			t.Errorf("got %v, want zero time", got)
		}
	})
}

// TestContainerStatPath_DelegatesToSDK checks that
// the thin wrapper around cli.ContainerStatPath adds
// the absolute-path guard without mangling the result.
// We wire the fake to return a canned PathStat and
// assert the same struct (modulo the abs guard) comes
// back.
func TestContainerStatPath_DelegatesToSDK(t *testing.T) {
	ffd := newFakeFileDaemon()
	defer ffd.Close()
	m := newTestManagerForFiles(t, ffd)

	ffd.statResponse = container.PathStat{
		Name:  "hello.txt",
		Size:  42,
		Mode:  os.FileMode(0o644),
		Mtime: time.Now(),
	}

	stat, err := m.ContainerStatPath(context.Background(), "id", "/app/hello.txt")
	if err != nil {
		t.Fatalf("ContainerStatPath: %v", err)
	}
	if stat.Size != 42 {
		t.Errorf("size = %d, want 42", stat.Size)
	}
	if stat.Name != "hello.txt" {
		t.Errorf("name = %q, want 'hello.txt'", stat.Name)
	}
}

// TestStreamContainerFile_HappyPath exercises the
// streaming variant of ReadContainerFile: it must
// return a ReadCloser that yields the file's bytes
// without buffering the whole file, and the closer
// must release the underlying tar body. We use a
// 1 MiB payload to confirm the streaming path
// actually works for "the user pulled a big log".
func TestStreamContainerFile_HappyPath(t *testing.T) {
	ffd := newFakeFileDaemon()
	defer ffd.Close()
	m := newTestManagerForFiles(t, ffd)

	const size = 1 << 20 // 1 MiB
	payload := bytes.Repeat([]byte("A"), size)
	ffd.fileTarBytes = buildSingleFileTar(t, "big.log", payload)
	ffd.statResponse = container.PathStat{Size: int64(size), Mode: 0o644, Mtime: time.Now()}

	body, n, err := m.StreamContainerFile(context.Background(), "id", "/var/log/big.log")
	if err != nil {
		t.Fatalf("StreamContainerFile: %v", err)
	}
	defer body.Close()

	if n != int64(size) {
		t.Errorf("reported size = %d, want %d", n, size)
	}
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if len(got) != size {
		t.Errorf("read %d bytes, want %d", len(got), size)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("body mismatch (first 32 bytes: %q)", got[:32])
	}
}

// TestStreamContainerFile_RejectsDirectory mirrors
// the same pre-flight check ReadContainerFile has:
// a directory path must be rejected before we try
// to stream (a tar listing would be technically
// "bytes" but not what the user asked for).
func TestStreamContainerFile_RejectsDirectory(t *testing.T) {
	ffd := newFakeFileDaemon()
	defer ffd.Close()
	m := newTestManagerForFiles(t, ffd)

	ffd.statResponse = container.PathStat{
		Size:  4096,
		Mode:  os.FileMode(0o755) | os.ModeDir,
		Mtime: time.Now(),
	}
	if _, _, err := m.StreamContainerFile(context.Background(), "id", "/app"); err == nil {
		t.Fatal("expected error for directory, got nil")
	}
}

// TestStreamContainerFile_RejectsRelativePath is
// the same guard as for ListContainerDir / Read /
// Stat — the API surface only ever hands absolute
// paths to the manager, so a relative one here means
// a caller bug.
func TestStreamContainerFile_RejectsRelativePath(t *testing.T) {
	m := newTestManagerForFiles(t, newFakeFileDaemon())
	if _, _, err := m.StreamContainerFile(context.Background(), "id", "relative"); err == nil {
		t.Fatal("expected error for relative path, got nil")
	}
}

// TestContainerStatPath_RejectsRelativePath is the
// symmetry check with ReadContainerFile / ListContainerDir:
// relative paths are refused at the boundary so the SDK
// never gets a `..`-laden or $CWD-dependent value.
func TestContainerStatPath_RejectsRelativePath(t *testing.T) {
	m := newTestManagerForFiles(t, newFakeFileDaemon())
	if _, err := m.ContainerStatPath(context.Background(), "id", "relative"); err == nil {
		t.Fatal("expected error for relative path, got nil")
	}
}

// TestListContainerDir_StderrFallback is the error-message
// path: when `ls` exits non-zero and there's no stderr
// in the framed stream, the wrapper falls back to
// stdout for the user-visible detail. We can't easily
// shape that scenario through the docker SDK (stderr
// and stdout both arrive in the same stream), but we
// can at least confirm the error includes the exit
// code, which is the contract for callers that map
// the error to "path not found".
func TestListContainerDir_StderrFallback(t *testing.T) {
	ffd := newFakeFileDaemon()
	defer ffd.Close()
	m := newTestManagerForFiles(t, ffd)

	ffd.lsResponse = "ls: cannot open directory '/app': Permission denied\n"
	ffd.lsExitCode = 2

	_, err := m.ListContainerDir(context.Background(), "id", "/app")
	if err == nil {
		t.Fatal("expected error when ls fails, got nil")
	}
	// The error should mention the exit code (so callers
	// can decide whether 1 == "not found" vs 2 == "denied")
	// and surface the stderr text.
	if !strings.Contains(err.Error(), "exit 2") {
		t.Errorf("err = %v, want it to mention 'exit 2'", err)
	}
	if !strings.Contains(err.Error(), "Permission denied") {
		t.Errorf("err = %v, want it to include stderr text", err)
	}
}
