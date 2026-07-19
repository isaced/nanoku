package api

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
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
	// statResponse is the JSON the fake returns for
	// ContainerStatPath (HEAD /containers/{id}/archive).
	// Tests can flip Mode to include os.ModeDir to make
	// the manager treat a path as a directory.
	statResponse container.PathStat
	// statErr is the body the fake writes to the stat
	// endpoint when set; the manager's HEAD 404 path
	// surfaces this as "stat: ... not found".
	statErr string
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
			// statErr short-circuits to a 404 so the
			// manager's "not found" path is exercised.
			// Tests use it to fake a deleted file
			// between stat and read.
			if fd.statErr != "" && r.Method == http.MethodHead {
				http.Error(w, fd.statErr, http.StatusNotFound)
				return
			}
			// statResponse lets tests flip Mode to
			// include os.ModeDir to make the manager
			// reject the path as a directory. Falls
			// back to a plain regular-file stat with
			// fileSize when unset.
			stat := fd.statResponse
			if stat.Size == 0 && stat.Mode == 0 {
				stat = container.PathStat{Size: fd.fileSize, Mode: 0o644, Mtime: time.Now()}
			}
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

// TestAppContainerFiles_ContainerMissing_Returns404 covers
// the case where the app's container exists in the DB but
// has been removed from docker (a "ghost" container —
// typical after a manual `docker rm`). ContainerIDByName
// returns "" + nil, the handler maps that to 404 with
// "container not found". This is the same surface as the
// unknown-container case from the UI's perspective.
func TestAppContainerFiles_ContainerMissing_Returns404(t *testing.T) {
	fd := newFakeFileAPIDaemon()
	defer fd.Close()
	h := newFileBrowserHandlers(t, fd)
	appID, cname := seedDockerApp(t, h, "blog", "nginx:1.27")
	// listReply intentionally empty — the fake returns
	// no matching containers, simulating a deleted
	// container.
	fd.listReply = nil

	req := httptest.NewRequest(http.MethodGet, "/api/apps/"+strconv.Itoa(appID)+"/containers/"+cname+"/files?path=/", nil)
	req.SetPathValue("id", strconv.Itoa(appID))
	req.SetPathValue("name", cname)
	w := httptest.NewRecorder()
	h.AppContainerFiles(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("Code = %d, want 404; body=%q", w.Code, w.Body.String())
	}
}

// TestAppContainerFiles_DockerUnavailable_Returns503 makes
// sure the handler short-circuits when h.Docker is nil.
// A Handlers with Docker=nil is the only safe way to wire
// up tests that don't exercise the docker surface; the
// file-browser endpoints must respect that and not panic
// trying to call into a nil manager.
func TestAppContainerFiles_DockerUnavailable_Returns503(t *testing.T) {
	h := &Handlers{DB: newTestDB(t), Docker: nil, Secret: newTestSealer(t)}
	appID, cname := seedDockerApp(t, h, "blog", "nginx:1.27")

	req := httptest.NewRequest(http.MethodGet, "/api/apps/"+strconv.Itoa(appID)+"/containers/"+cname+"/files?path=/", nil)
	req.SetPathValue("id", strconv.Itoa(appID))
	req.SetPathValue("name", cname)
	w := httptest.NewRecorder()
	h.AppContainerFiles(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Code = %d, want 503; body=%q", w.Code, w.Body.String())
	}
}

// TestAppContainerFile_MissingPath_Returns400 covers the
// "user clicked the file without entering a path" case
// (impossible from the UI but a valid curl probe). The
// handler must NOT silently fall back to "/" or to the
// last-known path; the contract is "path is required for
// the read endpoint".
func TestAppContainerFile_MissingPath_Returns400(t *testing.T) {
	fd := newFakeFileAPIDaemon()
	defer fd.Close()
	h := newFileBrowserHandlers(t, fd)
	appID, cname := seedDockerApp(t, h, "blog", "nginx:1.27")
	fd.listReply = []container.Summary{
		{ID: "c1", Names: []string{"/" + cname}, State: "running"},
	}

	req := httptest.NewRequest(http.MethodGet, "/api/apps/"+strconv.Itoa(appID)+"/containers/"+cname+"/file", nil)
	req.SetPathValue("id", strconv.Itoa(appID))
	req.SetPathValue("name", cname)
	w := httptest.NewRecorder()
	h.AppContainerFile(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("Code = %d, want 400; body=%q", w.Code, w.Body.String())
	}
}

// TestAppContainerFile_Download_TooLarge_StillStreams is
// the download-side symmetry of the size cap: the JSON
// text-view path refuses files > 1 MiB, but the
// ?download=1 path streams them anyway. The Content-Length
// header must reflect the on-disk size (so the browser
// shows a real progress bar), and the body must include
// the file content end-to-end.
func TestAppContainerFile_Download_TooLarge_StillStreams(t *testing.T) {
	fd := newFakeFileAPIDaemon()
	defer fd.Close()
	h := newFileBrowserHandlers(t, fd)
	appID, cname := seedDockerApp(t, h, "blog", "nginx:1.27")
	fd.listReply = []container.Summary{
		{ID: "c1", Names: []string{"/" + cname}, State: "running"},
	}

	// 2 MiB file. The text-view path would 413; the
	// download path should stream it.
	big := bytes.Repeat([]byte("X"), 2<<20)
	fd.fileBytes = big
	fd.fileSize = int64(len(big))

	req := httptest.NewRequest(http.MethodGet, "/api/apps/"+strconv.Itoa(appID)+"/containers/"+cname+"/file?path=/big&download=1", nil)
	req.SetPathValue("id", strconv.Itoa(appID))
	req.SetPathValue("name", cname)
	w := httptest.NewRecorder()
	h.AppContainerFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Code = %d, want 200; body len=%d", w.Code, w.Body.Len())
	}
	if w.Header().Get("Content-Type") != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", w.Header().Get("Content-Type"))
	}
	cl := w.Header().Get("Content-Length")
	if cl == "" {
		t.Errorf("Content-Length missing")
	} else if cl != strconv.FormatInt(int64(len(big)), 10) {
		t.Errorf("Content-Length = %q, want %d", cl, len(big))
	}
}

// TestAppContainerFile_Download_Directory_Returns400
// guards the "user clicked a directory and added
// &download=1" case. We must 400 instead of streaming
// a tar archive the user can't make sense of.
func TestAppContainerFile_Download_Directory_Returns400(t *testing.T) {
	fd := newFakeFileAPIDaemon()
	defer fd.Close()
	h := newFileBrowserHandlers(t, fd)
	appID, cname := seedDockerApp(t, h, "blog", "nginx:1.27")
	fd.listReply = []container.Summary{
		{ID: "c1", Names: []string{"/" + cname}, State: "running"},
	}

	// Stat says this path is a directory. The download
	// branch's pre-flight check catches it.
	fd.statResponse = container.PathStat{
		Size:  4096,
		Mode:  os.FileMode(0o755) | os.ModeDir,
		Mtime: time.Now(),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/apps/"+strconv.Itoa(appID)+"/containers/"+cname+"/file?path=/app&download=1", nil)
	req.SetPathValue("id", strconv.Itoa(appID))
	req.SetPathValue("name", cname)
	w := httptest.NewRecorder()
	h.AppContainerFile(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("Code = %d, want 400; body=%q", w.Code, w.Body.String())
	}
}

// TestAppContainerFile_Download_MissingFile_Returns404
// covers the "download endpoint with a bad path" case.
// The pre-flight stat is the same as the text-view
// path; 404 is the right answer in both branches.
func TestAppContainerFile_Download_MissingFile_Returns404(t *testing.T) {
	fd := newFakeFileAPIDaemon()
	defer fd.Close()
	h := newFileBrowserHandlers(t, fd)
	appID, cname := seedDockerApp(t, h, "blog", "nginx:1.27")
	fd.listReply = []container.Summary{
		{ID: "c1", Names: []string{"/" + cname}, State: "running"},
	}

	// The fake's statErr is set; the HEAD /archive
	// branch will return a 404, which the manager maps
	// to "stat: ... not found".
	fd.statErr = "No such file: /missing"

	req := httptest.NewRequest(http.MethodGet, "/api/apps/"+strconv.Itoa(appID)+"/containers/"+cname+"/file?path=/missing&download=1", nil)
	req.SetPathValue("id", strconv.Itoa(appID))
	req.SetPathValue("name", cname)
	w := httptest.NewRecorder()
	h.AppContainerFile(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("Code = %d, want 404; body=%q", w.Code, w.Body.String())
	}
}

// TestValidateContainerPath covers the table of inputs
// the helper accepts and refuses. The handler-level
// integration tests already exercise the happy path;
// this is the unit-level invariant we want to keep
// honest if the rules ever change.
func TestValidateContainerPath(t *testing.T) {
	cases := []struct {
		path    string
		wantErr bool
	}{
		{"/", false},
		{"/app", false},
		{"/app/sub/deep", false},
		{"relative", true},
		{"app/relative", true},
		{"/app/../etc", true},
		{"/app/..", true},
		{"/../etc", true},
		{"/app/./sub", false}, // current dir is fine
	}
	for _, tc := range cases {
		err := validateContainerPath(tc.path)
		if (err != nil) != tc.wantErr {
			t.Errorf("validateContainerPath(%q) err = %v, wantErr = %v", tc.path, err, tc.wantErr)
		}
	}
}

// TestEscapeContentDispositionFilename covers the
// RFC 6266 surface area for the value-only form of
// Content-Disposition's filename parameter. The
// header is sensitive to embedded CR/LF (header
// injection) and unmatched quotes (parsing), so we
// strip those and the rest of the printable-but-
// problematic punctuation. Anything that survives
// the filter is plain ASCII text, safe to drop
// between the surrounding quotes.
func TestEscapeContentDispositionFilename(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"hello.txt", "hello.txt"},
		{"my file.yaml", "my file.yaml"},
		// Hostile inputs get sanitized.
		{`a"b`, "a_b"},
		{`a;b`, "a_b"},
		{"a\\b", "a_b"},
		{"a\nb", "a_b"},
		{"a\rb", "a_b"},
		{"a\x00b", "a_b"},
		{"a\x7fb", "a_b"},
		{"normal-中文.log", "normal-中文.log"},
		// Empty / all-hostile fall back to "download".
		{"", "download"},
		{"\"\n\r", "___"},
	}
	for _, tc := range cases {
		got := escapeContentDispositionFilename(tc.in)
		if got != tc.want {
			t.Errorf("escapeContentDispositionFilename(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestAppContainerFiles_DockerUnavailable_ComposeMode_Returns503
// is the bug Copilot flagged: when h.Docker is nil AND
// the app is compose mode, the previous code reached
// into docker.ListAppContainers via containerBelongsToApp
// and returned "container not found" 404. The fix moves
// the nil check into resolveAppContainer so the user
// gets the actual reason: 503 "docker unavailable".
func TestAppContainerFiles_DockerUnavailable_ComposeMode_Returns503(t *testing.T) {
	h := &Handlers{DB: newTestDB(t), Docker: nil, Secret: newTestSealer(t)}
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

	req := httptest.NewRequest(http.MethodGet, "/api/apps/"+strconv.Itoa(a.ID)+"/containers/nanoku-blog-web/files?path=/", nil)
	req.SetPathValue("id", strconv.Itoa(a.ID))
	req.SetPathValue("name", "nanoku-blog-web")
	w := httptest.NewRecorder()
	h.AppContainerFiles(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Code = %d, want 503; body=%q", w.Code, w.Body.String())
	}
}

// TestAppContainerFile_Download_StreamsWithoutBuffering
// confirms the streaming behavior: the body bytes flow
// through io.Copy, not via a single big buffer. We
// assert the response body contains the tarred file's
// raw bytes (after our tar extraction) so a wire-level
// reader gets the same content the in-memory path
// would have. The crucial property — "we never read
// the whole file before writing the first byte" — is
// exercised by the docker package's TestStreamContainerFile
// suite; this test just guards the handler-level
// integration.
func TestAppContainerFile_Download_StreamsWithoutBuffering(t *testing.T) {
	fd := newFakeFileAPIDaemon()
	defer fd.Close()
	h := newFileBrowserHandlers(t, fd)
	appID, cname := seedDockerApp(t, h, "blog", "nginx:1.27")
	fd.listReply = []container.Summary{
		{ID: "c1", Names: []string{"/" + cname}, State: "running"},
	}

	// 256 KiB payload — big enough that a naive "read
	// everything then write" approach would noticeably
	// inflate the goroutine's working set, but small
	// enough that the fake's tar wrapping stays cheap.
	const size = 256 << 10
	want := bytes.Repeat([]byte("X"), size)
	fd.fileBytes = want
	fd.fileSize = int64(size)

	req := httptest.NewRequest(http.MethodGet, "/api/apps/"+strconv.Itoa(appID)+"/containers/"+cname+"/file?path=/data/big&download=1", nil)
	req.SetPathValue("id", strconv.Itoa(appID))
	req.SetPathValue("name", cname)
	w := httptest.NewRecorder()
	h.AppContainerFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Code = %d, want 200; body len=%d", w.Code, w.Body.Len())
	}
	if !bytes.Contains(w.Body.Bytes(), want) {
		t.Errorf("body does not contain expected bytes (got %d bytes)", w.Body.Len())
	}
	if cl := w.Header().Get("Content-Length"); cl != strconv.Itoa(size) {
		t.Errorf("Content-Length = %q, want %d", cl, size)
	}
}

// TestAppContainerFile_Download_FilenameEscaped covers
// the Content-Disposition header-injection guard. A
// filename with embedded quotes or control chars must
// be sanitized so it can't break out of the surrounding
// quotes or inject extra header lines. We don't have a
// way to get an actual container path with a hostile
// name (validateContainerPath only blocks `..`, not
// `;` or `"`), so we drive the helper directly and
// confirm the handler applies the same escaper.
func TestAppContainerFile_Download_FilenameEscaped(t *testing.T) {
	fd := newFakeFileAPIDaemon()
	defer fd.Close()
	h := newFileBrowserHandlers(t, fd)
	appID, cname := seedDockerApp(t, h, "blog", "nginx:1.27")
	fd.listReply = []container.Summary{
		{ID: "c1", Names: []string{"/" + cname}, State: "running"},
	}

	fd.fileBytes = []byte("data")
	fd.fileSize = 4

	// Path with a quote in the basename. validateContainerPath
	// only blocks "..", so this is a legal request.
	// The download handler must escape the basename
	// before inserting it into Content-Disposition.
	req := httptest.NewRequest(http.MethodGet, "/api/apps/"+strconv.Itoa(appID)+"/containers/"+cname+"/file?path=/etc/my%22file&download=1", nil)
	req.SetPathValue("id", strconv.Itoa(appID))
	req.SetPathValue("name", cname)
	w := httptest.NewRecorder()
	h.AppContainerFile(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Code = %d, want 200; body=%q", w.Code, w.Body.String())
	}
	cd := w.Header().Get("Content-Disposition")
	// The header value must be a single line and must
	// not contain an unescaped quote. Our escaper
	// replaces `"` with `_`, so the basename in the
	// header is the sanitized form.
	if strings.Contains(cd, "\n") || strings.Contains(cd, "\r") {
		t.Errorf("Content-Disposition contains CR/LF: %q", cd)
	}
	if !strings.Contains(cd, `filename="my_file"`) {
		t.Errorf("Content-Disposition = %q, want it to include the escaped filename", cd)
	}
}
