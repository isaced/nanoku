package api

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/isaced/nanoku/internal/db/app"
	"github.com/isaced/nanoku/internal/docker"
)

// fakeFileAPIDaemon is the API-test side of the docker engine
// mock. It responds to:
//
//   - /_ping
//   - /containers/json  (used by ListAppContainers for compose mode)
//   - POST /containers/{id}/exec
//   - POST /exec/{id}/start (the hijack endpoint for attach +
//     the no-hijack endpoint for start)
//   - GET /exec/{id}/json
//   - HEAD/GET /containers/{id}/archive?path=…
//
// Same wire shape as the docker package's fakeFileDaemon, but
// split out so the API tests don't pull in test-only internals
// from the docker package.
type fakeFileAPIDaemon struct {
	*httptest.Server

	// lsByPath is the canned `ls -lan` payload returned for a
	// given path. Tests seed it; an unknown path returns
	// exit code 1 with a "No such file" stderr.
	lsByPath  map[string]string
	listReply []container.Summary
	// fileBytes is the raw content the tar archive should
	// contain for the read path tests. Wrapped into a
	// single-file tar by the dispatch handler.
	fileBytes []byte
	// fileSize is reported in the X-Docker-Container-Path-Stat
	// header. Should match len(fileBytes).
	fileSize int64
	// calls lets tests assert on the request shape the
	// handler made (path, method, body).
	calls atomic.Int32
}

func newFakeFileAPIDaemon() *fakeFileAPIDaemon {
	fd := &fakeFileAPIDaemon{
		lsByPath: map[string]string{},
	}
	fd.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fd.calls.Add(1)
		switch {
		case strings.HasSuffix(r.URL.Path, "/_ping"):
			w.Header().Set("API-Version", "1.40")
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(fd.listReply)
		case strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/exec"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(container.ExecCreateResponse{ID: "exec-1"})
		case strings.HasSuffix(r.URL.Path, "/exec/exec-1/start"):
			if r.Header.Get("Upgrade") == "" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			// Hijack + write a 101 + the ls payload for
			// whatever path the request asked for.
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
			// We don't know which path the request asked
			// for here without re-parsing the start
			// request body, so we look it up by a
			// convention: the most-recently-set ls
			// response wins. The tests only ask for one
			// path per scenario, so this is fine.
			_, ok = fd.lsByPath["__last__"]
			_ = ok
			var payload string
			for _, p := range fd.lsByPath {
				payload = p
				break
			}
			var frame bytes.Buffer
			stdcopy.NewStdWriter(&frame, stdcopy.Stdout).Write([]byte(payload))
			_, _ = bufrw.Write(frame.Bytes())
			_ = bufrw.Flush()
		case strings.HasSuffix(r.URL.Path, "/exec/exec-1/json"):
			w.Header().Set("Content-Type", "application/json")
			// Non-zero if the test set an empty payload
			// (caller signals "not found" by clearing
			// the lsByPath entry).
			hasPayload := false
			for range fd.lsByPath {
				hasPayload = true
			}
			code := 0
			if !hasPayload {
				code = 1
			}
			_ = json.NewEncoder(w).Encode(container.ExecInspect{ExitCode: code})
		case strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/archive"):
			stat := container.PathStat{Size: fd.fileSize, Mode: 0o644, Mtime: time.Now()}
			statJSON, _ := json.Marshal(stat)
			w.Header().Set("X-Docker-Container-Path-Stat", base64.StdEncoding.EncodeToString(statJSON))
			if r.Method == http.MethodHead {
				w.WriteHeader(http.StatusOK)
				return
			}
			w.Header().Set("Content-Type", "application/x-tar")
			// Build a one-file tar from fileBytes.
			var buf bytes.Buffer
			tw := tar.NewWriter(&buf)
			_ = tw.WriteHeader(&tar.Header{
				Name:    "hello.txt",
				Mode:    0o644,
				Size:    fd.fileSize,
				ModTime: time.Now(),
			})
			_, _ = tw.Write(fd.fileBytes)
			_ = tw.Close()
			_, _ = w.Write(buf.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	return fd
}

// newFileBrowserHandlers builds a Handlers with the fake
// daemon wired in. Same shape as newLogsHandlers but
// installs Docker so the file handlers don't bail with 503.
func newFileBrowserHandlers(t *testing.T, fd *fakeFileAPIDaemon) *Handlers {
	t.Helper()
	cli, err := client.NewClientWithOpts(
		client.WithHost("tcp://"+strings.TrimPrefix(fd.URL, "http://")),
		client.WithHTTPClient(fd.Client()),
		client.WithVersion("1.40"),
	)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	return &Handlers{
		DB:              newTestDB(t),
		Docker:          docker.NewManagerWithClient(docker.Config{ContainerName: "nanoku-caddy"}, cli),
		SkipCaddyReload: true,
		Secret:          newTestSealer(t),
	}
}

// seedDockerApp creates an app with a single current_container
// named "nanoku-<name>". The returned id can be used with
// the file-browser endpoints.
func seedDockerApp(t *testing.T, h *Handlers, name, image string) (appID int, containerName string) {
	t.Helper()
	ctx := context.Background()
	a, err := h.DB.App.Create().
		SetName(name).
		SetImage(image).
		SetPort(80).
		SetDeployMethod("docker").
		Save(ctx)
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	now := time.Now().UTC()
	cont, err := h.DB.Container.Create().
		SetDockerID("docker-id-" + name).
		SetName("nanoku-" + name).
		SetImage(image).
		SetStatus("running").
		SetStartedAt(now).
		SetAppID(a.ID).
		Save(ctx)
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	if err := h.DB.App.UpdateOneID(a.ID).SetCurrentContainerID(cont.ID).Exec(ctx); err != nil {
		t.Fatalf("set current container: %v", err)
	}
	return a.ID, cont.Name
}

// TestAppContainerFiles_DockerMode_HappyPath exercises the
// most common case: a docker-mode app with a single
// current_container; the request asks for that container by
// name; the handler resolves it via the DB and returns the
// ls payload.
func TestAppContainerFiles_DockerMode_HappyPath(t *testing.T) {
	fd := newFakeFileAPIDaemon()
	defer fd.Close()
	h := newFileBrowserHandlers(t, fd)
	appID, cname := seedDockerApp(t, h, "blog", "nginx:1.27")
	fd.listReply = []container.Summary{
		{ID: "c1", Names: []string{"/" + cname}, State: "running"},
	}

	fd.lsByPath["__last__"] = "total 4\n-rw-r--r-- 1 0 0 12 Jan  1 00:00 hello.txt\n"

	url := "/api/apps/" + strconv.Itoa(appID) + "/containers/" + cname + "/files?path=/app"
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.SetPathValue("id", strconv.Itoa(appID))
	req.SetPathValue("name", cname)
	w := httptest.NewRecorder()
	h.AppContainerFiles(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Code = %d, want 200; body=%q", w.Code, w.Body.String())
	}
	var got struct {
		Path    string            `json:"path"`
		Entries []docker.FileInfo `json:"entries"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Path != "/app" {
		t.Errorf("path = %q, want /app", got.Path)
	}
	if len(got.Entries) != 1 || got.Entries[0].Name != "hello.txt" {
		t.Errorf("entries = %+v, want one 'hello.txt'", got.Entries)
	}
}

// TestAppContainerFiles_DockerMode_WrongContainer is the
// security check: asking for a container that isn't the
// app's current_container must look like a 404, not a
// permission error, so the response surface gives nothing
// away about which containers exist.
func TestAppContainerFiles_DockerMode_WrongContainer(t *testing.T) {
	fd := newFakeFileAPIDaemon()
	defer fd.Close()
	h := newFileBrowserHandlers(t, fd)
	appID, _ := seedDockerApp(t, h, "blog", "nginx:1.27")

	req := httptest.NewRequest(http.MethodGet, "/api/apps/"+strconv.Itoa(appID)+"/containers/nanoku-other/files?path=/app", nil)
	req.SetPathValue("id", strconv.Itoa(appID))
	req.SetPathValue("name", "nanoku-other")
	w := httptest.NewRecorder()
	h.AppContainerFiles(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("Code = %d, want 404", w.Code)
	}
}

// TestAppContainerFiles_PathTraversal_Rejected makes sure
// the validator catches ".." before any docker call goes
// out the door.
func TestAppContainerFiles_PathTraversal_Rejected(t *testing.T) {
	fd := newFakeFileAPIDaemon()
	defer fd.Close()
	h := newFileBrowserHandlers(t, fd)
	appID, cname := seedDockerApp(t, h, "blog", "nginx:1.27")

	req := httptest.NewRequest(http.MethodGet, "/api/apps/"+strconv.Itoa(appID)+"/containers/"+cname+"/files?path=/app/../../etc", nil)
	req.SetPathValue("id", strconv.Itoa(appID))
	req.SetPathValue("name", cname)
	w := httptest.NewRecorder()
	h.AppContainerFiles(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("Code = %d, want 400", w.Code)
	}
	if fd.calls.Load() != 0 {
		t.Errorf("docker daemon was hit %d times, want 0 (validation should short-circuit)", fd.calls.Load())
	}
}

// TestAppContainerFiles_RelativePath_Rejected covers the
// other half of the path invariant: relative paths are
// rejected the same way.
func TestAppContainerFiles_RelativePath_Rejected(t *testing.T) {
	fd := newFakeFileAPIDaemon()
	defer fd.Close()
	h := newFileBrowserHandlers(t, fd)
	appID, cname := seedDockerApp(t, h, "blog", "nginx:1.27")

	req := httptest.NewRequest(http.MethodGet, "/api/apps/"+strconv.Itoa(appID)+"/containers/"+cname+"/files?path=app", nil)
	req.SetPathValue("id", strconv.Itoa(appID))
	req.SetPathValue("name", cname)
	w := httptest.NewRecorder()
	h.AppContainerFiles(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("Code = %d, want 400", w.Code)
	}
}

// TestAppContainerFile_DockerMode_HappyPath is the text-view
// round-trip: the fake serves a 12-byte file; the handler
// returns JSON with the bytes base64-encoded.
func TestAppContainerFile_DockerMode_HappyPath(t *testing.T) {
	fd := newFakeFileAPIDaemon()
	defer fd.Close()
	h := newFileBrowserHandlers(t, fd)
	appID, cname := seedDockerApp(t, h, "blog", "nginx:1.27")
	fd.listReply = []container.Summary{
		{ID: "c1", Names: []string{"/" + cname}, State: "running"},
	}

	want := []byte("hello world\n")
	fd.fileBytes = want
	fd.fileSize = int64(len(want))

	req := httptest.NewRequest(http.MethodGet, "/api/apps/"+strconv.Itoa(appID)+"/containers/"+cname+"/file?path=/app/hello.txt", nil)
	req.SetPathValue("id", strconv.Itoa(appID))
	req.SetPathValue("name", cname)
	w := httptest.NewRecorder()
	h.AppContainerFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Code = %d, want 200; body=%q", w.Code, w.Body.String())
	}
	var got struct {
		Path     string `json:"path"`
		Size     int64  `json:"size"`
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Encoding != "base64" {
		t.Errorf("encoding = %q, want base64", got.Encoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(got.Content)
	if err != nil {
		t.Fatalf("decode content: %v", err)
	}
	if !bytes.Equal(decoded, want) {
		t.Errorf("content = %q, want %q", decoded, want)
	}
}

// TestAppContainerFile_DockerMode_TooLarge covers the
// 1 MiB cap on the text-view path. The fake reports
// a 2 MiB stat; the handler must return 413.
func TestAppContainerFile_DockerMode_TooLarge(t *testing.T) {
	fd := newFakeFileAPIDaemon()
	defer fd.Close()
	h := newFileBrowserHandlers(t, fd)
	appID, cname := seedDockerApp(t, h, "blog", "nginx:1.27")
	fd.listReply = []container.Summary{
		{ID: "c1", Names: []string{"/" + cname}, State: "running"},
	}

	fd.fileSize = 2 << 20 // 2 MiB
	// fileBytes can be empty for the size-cap path; the
	// manager short-circuits on stat before reading the
	// tar body.

	req := httptest.NewRequest(http.MethodGet, "/api/apps/"+strconv.Itoa(appID)+"/containers/"+cname+"/file?path=/big", nil)
	req.SetPathValue("id", strconv.Itoa(appID))
	req.SetPathValue("name", cname)
	w := httptest.NewRecorder()
	h.AppContainerFile(w, req)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("Code = %d, want 413; body=%q", w.Code, w.Body.String())
	}
}

// TestAppContainerFile_DockerMode_Download_HappyPath flips
// the download switch and checks we get back an
// octet-stream with a Content-Disposition and the
// original bytes.
func TestAppContainerFile_DockerMode_Download_HappyPath(t *testing.T) {
	fd := newFakeFileAPIDaemon()
	defer fd.Close()
	h := newFileBrowserHandlers(t, fd)
	appID, cname := seedDockerApp(t, h, "blog", "nginx:1.27")
	fd.listReply = []container.Summary{
		{ID: "c1", Names: []string{"/" + cname}, State: "running"},
	}

	want := []byte("binary garbage \x00\x01\x02")
	fd.fileBytes = want
	fd.fileSize = int64(len(want))

	req := httptest.NewRequest(http.MethodGet, "/api/apps/"+strconv.Itoa(appID)+"/containers/"+cname+"/file?path=/app/binary&download=1", nil)
	req.SetPathValue("id", strconv.Itoa(appID))
	req.SetPathValue("name", cname)
	w := httptest.NewRecorder()
	h.AppContainerFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Code = %d, want 200; body=%q", w.Code, w.Body.String())
	}
	ct := w.Header().Get("Content-Type")
	if ct != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", ct)
	}
	cd := w.Header().Get("Content-Disposition")
	if !strings.Contains(cd, "attachment") {
		t.Errorf("Content-Disposition = %q, want it to include 'attachment'", cd)
	}
	// The download path pulls the same tar archive; the
	// real body is the file content from inside the tar.
	// The fake puts want into the tar so w.Body should
	// equal want (after our tar extraction).
	if !bytes.Contains(w.Body.Bytes(), want) {
		t.Errorf("body doesn't contain expected bytes")
	}
}

// TestAppContainerFile_DockerMode_MissingApp makes sure
// asking for a non-existent app id is a clean 404
// (not "container not found" — the order of checks
// should fail at the app lookup).
func TestAppContainerFile_DockerMode_MissingApp(t *testing.T) {
	fd := newFakeFileAPIDaemon()
	defer fd.Close()
	h := newFileBrowserHandlers(t, fd)

	req := httptest.NewRequest(http.MethodGet, "/api/apps/9999/containers/nanoku-foo/file?path=/app", nil)
	req.SetPathValue("id", "9999")
	req.SetPathValue("name", "nanoku-foo")
	w := httptest.NewRecorder()
	h.AppContainerFile(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("Code = %d, want 404", w.Code)
	}
}

// TestAppContainerFiles_ComposeMode_VerifiesMembership
// covers the compose side of the ownership check: the
// app is compose, the fake lists two containers in the
// project, and the handler accepts a name that's in the
// list and rejects one that isn't.
func TestAppContainerFiles_ComposeMode_VerifiesMembership(t *testing.T) {
	fd := newFakeFileAPIDaemon()
	defer fd.Close()
	h := newFileBrowserHandlers(t, fd)

	ctx := context.Background()
	a, err := h.DB.App.Create().
		SetName("blog").
		SetImage("nginx:1.27").
		SetPort(80).
		SetDeployMethod("compose").
		Save(ctx)
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	// The fake ListAppContainers reply below is keyed by
	// the project's filter (com.docker.compose.project=…)
	// — we don't need to populate it here, just make the
	// handler look at the right project name.
	fd.listReply = []container.Summary{
		{ID: "c1", Names: []string{"/nanoku-blog-web"}, State: "running"},
		{ID: "c2", Names: []string{"/nanoku-blog-db"}, State: "running"},
	}

	// In-list container → 200 (with whatever ls payload the
	// fake provides).
	fd.lsByPath["__last__"] = "total 0\n"
	req := httptest.NewRequest(http.MethodGet, "/api/apps/"+strconv.Itoa(a.ID)+"/containers/nanoku-blog-web/files?path=/app", nil)
	req.SetPathValue("id", strconv.Itoa(a.ID))
	req.SetPathValue("name", "nanoku-blog-web")
	w := httptest.NewRecorder()
	h.AppContainerFiles(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("in-list: Code = %d, want 200; body=%q", w.Code, w.Body.String())
	}

	// Out-of-list container → 404.
	req = httptest.NewRequest(http.MethodGet, "/api/apps/"+strconv.Itoa(a.ID)+"/containers/nanoku-other/files?path=/app", nil)
	req.SetPathValue("id", strconv.Itoa(a.ID))
	req.SetPathValue("name", "nanoku-other")
	w = httptest.NewRecorder()
	h.AppContainerFiles(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("out-of-list: Code = %d, want 404", w.Code)
	}
}

// silenceUnusedImport keeps the `app` package referenced if
// the docker-mode test path is later removed. The import is
// currently only used by the indirect path (QueryCurrentContainer
// walks the app edge), so this is belt-and-suspenders.
var _ = app.HasCurrentContainer
