package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/db/volume"
	"github.com/isaced/nanoku/internal/docker"
)

func strPtr(s string) *string { return &s }
func boolPtr(b bool) *bool    { return &b }

func TestValidateVolumeMount(t *testing.T) {
	cases := []struct {
		name    string
		in      VolumeInput
		wantErr string
	}{
		{
			name: "volume empty source ok",
			in:   VolumeInput{Type: strPtr("volume"), Target: strPtr("/data")},
		},
		{
			name: "volume with name ok",
			in:   VolumeInput{Type: strPtr("volume"), Source: strPtr("my-vol"), Target: strPtr("/data")},
		},
		{
			name: "bind requires absolute source",
			in:   VolumeInput{Type: strPtr("bind"), Source: strPtr("relative/path"), Target: strPtr("/data")},
			wantErr: "absolute host path",
		},
		{
			name: "bind requires source",
			in:   VolumeInput{Type: strPtr("bind"), Target: strPtr("/data")},
			wantErr: "required for type=bind",
		},
		{
			name: "target must be absolute",
			in:   VolumeInput{Type: strPtr("volume"), Target: strPtr("relative")},
			wantErr: "absolute path",
		},
		{
			name: "invalid volume name",
			in:   VolumeInput{Type: strPtr("volume"), Source: strPtr("UPPER"), Target: strPtr("/data")},
			wantErr: "not a valid docker volume name",
		},
		{
			name: "type required",
			in:   VolumeInput{Target: strPtr("/data")},
			wantErr: "type is required",
		},
		{
			name: "unknown type",
			in:   VolumeInput{Type: strPtr("tmpfs"), Target: strPtr("/data")},
			wantErr: "must be 'volume' or 'bind'",
		},
		{
			name: "readOnly passthrough",
			in:   VolumeInput{Type: strPtr("volume"), Target: strPtr("/data"), ReadOnly: boolPtr(true)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateVolumeMount(tc.in, 0)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("want error containing %q, got nil", tc.wantErr)
			}
			if !contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %q, want substring %q", err.Error(), tc.wantErr)
			}
		})
	}
}

func TestValidateVolumeSet(t *testing.T) {
	mk := func(target string) docker.VolumeMount {
		return docker.VolumeMount{Type: "volume", Target: target}
	}
	if err := validateVolumeSet(nil); err != nil {
		t.Fatalf("nil set: %v", err)
	}
	if err := validateVolumeSet([]docker.VolumeMount{mk("/a"), mk("/b")}); err != nil {
		t.Fatalf("unique: %v", err)
	}
	err := validateVolumeSet([]docker.VolumeMount{mk("/a"), mk("/b"), mk("/a")})
	if err == nil || !contains(err.Error(), "duplicates") {
		t.Fatalf("want dup error, got %v", err)
	}
}

func contains(s, sub string) bool {
	return bytes.Contains([]byte(s), []byte(sub))
}

func TestReplaceAppVolumes_AutoNamesAndPersists(t *testing.T) {
	d := newTestDB(t)
	a := createTestApp(t, d, "blog", "docker", "nginx:1.27", 80, false)

	body, _ := json.Marshal([]VolumeInput{
		{Type: strPtr("volume"), Target: strPtr("/data")},
		{Type: strPtr("volume"), Source: strPtr("user-vol"), Target: strPtr("/var")},
		{Type: strPtr("bind"), Source: strPtr("/srv/cache"), Target: strPtr("/tmp/cache"), ReadOnly: boolPtr(true)},
	})
	r := httptest.NewRequest(http.MethodPut, "/api/apps/"+strconv.Itoa(a.ID)+"/volumes", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	(&Handlers{DB: d}).ReplaceAppVolumes(w, r)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}

	ctx := t.Context()
	vols, err := d.Volume.Query().Order(volume.ByID()).All(ctx)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(vols) != 3 {
		t.Fatalf("volume count = %d, want 3", len(vols))
	}
	if vols[0].Source == nil || *vols[0].Source != "nanoku-blog-vol-0" {
		t.Errorf("volumes[0].source = %v, want nanoku-blog-vol-0", vols[0].Source)
	}
	if vols[1].Source == nil || *vols[1].Source != "user-vol" {
		t.Errorf("volumes[1].source = %v, want user-vol", vols[1].Source)
	}
	if vols[2].Source == nil || *vols[2].Source != "/srv/cache" {
		t.Errorf("volumes[2].source = %v, want /srv/cache", vols[2].Source)
	}
	if !vols[2].ReadOnly {
		t.Errorf("volumes[2] read_only should be true")
	}
}

func TestReplaceAppVolumes_ReplacesAndNotAppends(t *testing.T) {
	d := newTestDB(t)
	a := createTestApp(t, d, "blog", "docker", "nginx:1.27", 80, false)

	body, _ := json.Marshal([]VolumeInput{
		{Type: strPtr("volume"), Target: strPtr("/data")},
	})
	doPut := func(b []byte) {
		r := httptest.NewRequest(http.MethodPut, "/api/apps/"+strconv.Itoa(a.ID)+"/volumes", bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		(&Handlers{DB: d}).ReplaceAppVolumes(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
		}
	}
	doPut(body)

	body2, _ := json.Marshal([]VolumeInput{
		{Type: strPtr("volume"), Target: strPtr("/v1")},
		{Type: strPtr("volume"), Target: strPtr("/v2")},
	})
	doPut(body2)

	count, _ := d.Volume.Query().Count(t.Context())
	if count != 2 {
		t.Fatalf("after replace, count = %d, want 2", count)
	}
}

func TestReplaceAppVolumes_RejectsBadInput(t *testing.T) {
	d := newTestDB(t)
	a := createTestApp(t, d, "blog", "docker", "nginx:1.27", 80, false)

	cases := []struct {
		name string
		body []VolumeInput
		want string
	}{
		{
			name: "duplicate target",
			body: []VolumeInput{
				{Type: strPtr("volume"), Target: strPtr("/dup")},
				{Type: strPtr("volume"), Target: strPtr("/dup")},
			},
			want: "duplicates",
		},
		{
			name: "relative target",
			body: []VolumeInput{
				{Type: strPtr("volume"), Target: strPtr("nope")},
			},
			want: "absolute path",
		},
		{
			name: "bind without source",
			body: []VolumeInput{
				{Type: strPtr("bind"), Target: strPtr("/x")},
			},
			want: "required for type=bind",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _ := json.Marshal(tc.body)
			r := httptest.NewRequest(http.MethodPut, "/api/apps/"+strconv.Itoa(a.ID)+"/volumes", bytes.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			(&Handlers{DB: d}).ReplaceAppVolumes(w, r)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", w.Code, w.Body.String())
			}
			if !contains(w.Body.String(), tc.want) {
				t.Fatalf("body = %s, want substring %q", w.Body.String(), tc.want)
			}
		})
	}
}

func TestListAppVolumes(t *testing.T) {
	d := newTestDB(t)
	a := createTestApp(t, d, "blog", "docker", "nginx:1.27", 80, false)

	// seed via direct DB so we don't depend on PUT semantics
	ctx := t.Context()
	if _, err := d.Volume.Create().
		SetType(volume.TypeVolume).
		SetSource("nanoku-blog-vol-0").
		SetTarget("/data").
		SetAppID(a.ID).
		Save(ctx); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := d.Volume.Create().
		SetType(volume.TypeBind).
		SetSource("/host").
		SetTarget("/cont").
		SetAppID(a.ID).
		Save(ctx); err != nil {
		t.Fatalf("create: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/api/apps/"+strconv.Itoa(a.ID)+"/volumes", nil)
	w := httptest.NewRecorder()
	(&Handlers{DB: d}).ListAppVolumes(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", w.Code, w.Body.String())
	}
	var out []VolumeDTO
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("len = %d, want 2", len(out))
	}
	if out[0].Type != "volume" || out[0].Source != "nanoku-blog-vol-0" {
		t.Errorf("vol[0] = %+v", out[0])
	}
	if out[1].Type != "bind" || out[1].Source != "/host" {
		t.Errorf("vol[1] = %+v", out[1])
	}
}

// silence unused import if a test is removed
var _ = context.Background
var _ db.DB

// createTestApp inserts a minimal App row for tests.
func createTestApp(t *testing.T, d *db.DB, name, deployMethod, image string, port int, withToken bool) *db.App {
	t.Helper()
	create := d.App.Create().
		SetName(name).
		SetDeployMethod(deployMethod)
	if deployMethod == "docker" {
		create.SetImage(image)
		create.SetPort(port)
	}
	if withToken {
		create.SetTriggerToken("test-token-" + name)
	}
	a, err := create.Save(context.Background())
	if err != nil {
		t.Fatalf("createTestApp(%q): %v", name, err)
	}
	return a
}
