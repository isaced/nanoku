package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/isaced/nanoku/internal/db"
)

// composeProjectName / networkAliasForService are the two
// pure-name helpers behind the upstream formula. They're easy
// to break in a rename refactor and the bug would surface as
// "502 Bad Gateway" from Caddy, so dedicated tests are worth
// the small file.
func TestComposeProjectName(t *testing.T) {
	if got := composeProjectName("blog"); got != "nanoku-blog" {
		t.Errorf("composeProjectName(blog) = %q, want nanoku-blog", got)
	}
}

func TestNetworkAliasForService(t *testing.T) {
	// The alias has no -1 suffix; the alias is independent
	// of the container replica index, so multi-replica
	// stacks resolve to the same string and Docker DNS
	// round-robins across all containers carrying the
	// alias.
	if got := networkAliasForService("kuma", "uptime-kuma"); got != "nanoku-kuma-uptime-kuma" {
		t.Errorf("got %q, want nanoku-kuma-uptime-kuma", got)
	}
}

// composeFilePath / writeComposeFile are pure filesystem ops
// against a temp directory; tested in isolation so a refactor
// (e.g. switching the layout) is fast to validate.
func TestComposeFilePath(t *testing.T) {
	h := &Handlers{ComposeBaseDir: t.TempDir()}
	got := h.composeFilePath("blog")
	want := filepath.Join(h.ComposeBaseDir, "blog", "docker-compose.yml")
	if got != want {
		t.Errorf("composeFilePath = %q, want %q", got, want)
	}
}

func TestWriteComposeFile_AtomicAndCreatesParents(t *testing.T) {
	base := t.TempDir()
	h := &Handlers{ComposeBaseDir: base}

	path, err := h.writeComposeFile("blog", "services:\n  web:\n    image: nginx:1.27\n")
	if err != nil {
		t.Fatalf("writeComposeFile: %v", err)
	}
	if path != filepath.Join(base, "blog", "docker-compose.yml") {
		t.Errorf("path = %q, want under base", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(got), "image: nginx:1.27") {
		t.Errorf("file content missing expected service; got %q", got)
	}
}

func TestWriteComposeFile_OverwriteExisting(t *testing.T) {
	h := &Handlers{ComposeBaseDir: t.TempDir()}
	if _, err := h.writeComposeFile("blog", "v1"); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if _, err := h.writeComposeFile("blog", "v2"); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	got, _ := os.ReadFile(h.composeFilePath("blog"))
	if string(got) != "v2" {
		t.Errorf("got %q, want v2", got)
	}
}

// appImage / appPort are the nil-safe accessors. They live in
// compose_helpers.go because the same nil-safety pattern is
// used there; the boundary is "helpers that don't touch the
// network" rather than "compose-specific". The two cases worth
// pinning: nil pointer and empty string.
func TestAppImageAndPort(t *testing.T) {
	if got := appImage(&db.App{}); got != "" {
		t.Errorf("nil image should give empty, got %q", got)
	}
	img := "nginx:1.27"
	if got := appImage(&db.App{Image: &img}); got != img {
		t.Errorf("got %q, want %q", got, img)
	}
	if got := appPort(&db.App{}); got != 0 {
		t.Errorf("zero value port should be 0, got %d", got)
	}
	if got := appPort(&db.App{Port: 80}); got != 80 {
		t.Errorf("got %d, want 80", got)
	}
}

// loadMounts is the one piece in compose_helpers.go that
// touches the DB. The integration through the QueryVolumes edge
// is already covered indirectly by deploy_executor_test.go, so
// this test just pins the no-mounts / one-mount shape and the
// ordering contract.
func TestLoadMounts_EmptyAndOrdered(t *testing.T) {
	d := newTestDB(t)
	a, err := d.App.Create().
		SetName("blog").
		SetDeployMethod("docker").
		SetImage("nginx:1.27").
		SetPort(80).
		Save(context.Background())
	if err != nil {
		t.Fatalf("seed app: %v", err)
	}

	// Empty case: no rows, no error.
	mounts, err := loadMounts(context.Background(), a)
	if err != nil {
		t.Fatalf("loadMounts empty: %v", err)
	}
	if len(mounts) != 0 {
		t.Errorf("got %d mounts, want 0", len(mounts))
	}

	// One mount: verify shape carries through (Source nil → "",
	// ReadOnly preserved).
	src := "my-vol"
	if _, err := d.Volume.Create().
		SetAppID(a.ID).
		SetType("volume").
		SetSource(src).
		SetTarget("/data").
		SetReadOnly(true).
		Save(context.Background()); err != nil {
		t.Fatalf("seed volume: %v", err)
	}
	mounts, err = loadMounts(context.Background(), a)
	if err != nil {
		t.Fatalf("loadMounts populated: %v", err)
	}
	if len(mounts) != 1 {
		t.Fatalf("got %d mounts, want 1", len(mounts))
	}
	got := mounts[0]
	if got.Type != "volume" || got.Source != "my-vol" || got.Target != "/data" || !got.ReadOnly {
		t.Errorf("mount shape = %+v, want volume/my-vol//data/true", got)
	}
}

// networkAliasForApp is the docker-mode sibling of the
// compose-mode networkAliasForService. Same shape, no service
// suffix, used by sites linked to a docker-mode app.
func TestNetworkAliasForApp(t *testing.T) {
	if got := networkAliasForApp("blog"); got != "nanoku-blog" {
		t.Errorf("got %q, want nanoku-blog", got)
	}
}

// resolveComposeFile chooses between user-provided path and the
// generated path. The two cases worth pinning: the user set
// compose_path (their value wins), and they didn't (we fall
// back to the generated path under ComposeBaseDir).
func TestResolveComposeFile(t *testing.T) {
	base := t.TempDir()
	h := &Handlers{ComposeBaseDir: base}

	a := &db.App{Name: "kuma"}
	project, path := h.resolveComposeFile(a)
	if project != "nanoku-kuma" {
		t.Errorf("project = %q, want nanoku-kuma", project)
	}
	if path != filepath.Join(base, "kuma", "docker-compose.yml") {
		t.Errorf("path = %q, want generated", path)
	}

	userPath := "/etc/nanoku/kuma.yml"
	a = &db.App{Name: "kuma", ComposePath: &userPath}
	project, path = h.resolveComposeFile(a)
	if path != userPath {
		t.Errorf("path = %q, want user-provided", path)
	}
	if project != "nanoku-kuma" {
		t.Errorf("project = %q, want nanoku-kuma", project)
	}
}
