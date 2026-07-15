package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
)

// TestCreateAppContainer_PreEvictsStaleContainerWithSameName exercises
// the "previous deploy left a stub with this name" recovery path. We
// make the fake daemon report an existing container at the
// `inspect` endpoint, then assert:
//
//   - the inspect call was made (recovery was attempted),
//   - a stop + remove was issued for the stale ID,
//   - the subsequent create succeeded with a fresh ID.
//
// Without the recovery path, the second ContainerCreate would surface
// "name already in use" and the deploy would fail. With the path,
// the stale entry is evicted before create and the new container
// gets to start.
func TestCreateAppContainer_PreEvictsStaleContainerWithSameName(t *testing.T) {
	const staleID = "stale-id-9999"
	const freshID = "fresh-id-1111"
	var (
		inspected atomic.Int32
		stopped   atomic.Int32
		removed   atomic.Int32
		created   atomic.Int32
	)
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/networks" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]network.Summary{{Name: "nanoku-net"}})
		case r.URL.Path == "/containers/nanoku-blog/json" && r.Method == http.MethodGet:
			inspected.Add(1)
			// The inspect endpoint on the real engine returns a
			// full container JSON; we only need the ID for our
			// logic, and the daemon only reads .ID.
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(container.InspectResponse{
				ContainerJSONBase: &container.ContainerJSONBase{ID: staleID},
			})
		case strings.HasSuffix(r.URL.Path, "/stop") && r.Method == http.MethodPost:
			stopped.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "") && r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/containers/"+staleID):
			removed.Add(1)
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/containers/create" && r.Method == http.MethodPost:
			created.Add(1)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(container.CreateResponse{ID: freshID})
		case strings.HasSuffix(r.URL.Path, "/start") && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{NetworkName: "nanoku-net"})
	gotID, gotName, err := m.CreateAppContainer(context.Background(), "blog", "nginx:1.27", 80, nil, 0, nil)
	if err != nil {
		t.Fatalf("CreateAppContainer: %v", err)
	}
	if gotID != freshID {
		t.Errorf("id = %q, want %q", gotID, freshID)
	}
	if gotName != "nanoku-blog" {
		t.Errorf("name = %q, want nanoku-blog", gotName)
	}
	if inspected.Load() != 1 {
		t.Errorf("inspect calls = %d, want 1", inspected.Load())
	}
	if stopped.Load() != 1 {
		t.Errorf("stop calls = %d, want 1 (force-remove tears down a running container)", stopped.Load())
	}
	if removed.Load() != 1 {
		t.Errorf("remove calls = %d, want 1", removed.Load())
	}
	if created.Load() != 1 {
		t.Errorf("create calls = %d, want 1", created.Load())
	}
}

// TestCreateAppContainer_NotFoundOnInspectIsNoOp asserts the
// happy-path case where the desired name is free: the recovery
// helper should silently no-op on 404 and the create should
// succeed without a single stop/remove call.
func TestCreateAppContainer_NotFoundOnInspectIsNoOp(t *testing.T) {
	var stopped, removed atomic.Int32
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/networks" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]network.Summary{{Name: "nanoku-net"}})
		case r.URL.Path == "/containers/nanoku-blog/json" && r.Method == http.MethodGet:
			// 404 — the engine returns NotFound here. The
			// errdefs.IsNotFound branch in the helper treats
			// this as "nothing to evict".
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"No such container: nanoku-blog"}`))
		case strings.HasSuffix(r.URL.Path, "/stop") && r.Method == http.MethodPost:
			stopped.Add(1)
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/containers/"):
			removed.Add(1)
		case r.URL.Path == "/containers/create" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(container.CreateResponse{ID: "new-id"})
		case strings.HasSuffix(r.URL.Path, "/start") && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{NetworkName: "nanoku-net"})
	if _, _, err := m.CreateAppContainer(context.Background(), "blog", "nginx:1.27", 80, nil, 0, nil); err != nil {
		t.Fatalf("CreateAppContainer: %v", err)
	}
	if stopped.Load() != 0 {
		t.Errorf("stop calls = %d, want 0 (no stale container to evict)", stopped.Load())
	}
	if removed.Load() != 0 {
		t.Errorf("remove calls = %d, want 0", removed.Load())
	}
}

// TestCreateAppContainer_InspectErrorSurfaces covers the
// "we couldn't even tell whether the name is free" case. The
// recovery helper should surface the inspect error rather than
// silently fall through and let the create call fail with a
// generic "name already in use" — the operator needs to see the
// underlying cause (permission, daemon glitch, etc.).
func TestCreateAppContainer_InspectErrorSurfaces(t *testing.T) {
	fd := newFakeDaemon()
	defer fd.Close()
	fd.dispatch = func(w http.ResponseWriter, r *http.Request, _ string) {
		switch {
		case r.URL.Path == "/networks" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]network.Summary{{Name: "nanoku-net"}})
		case r.URL.Path == "/containers/nanoku-blog/json" && r.Method == http.MethodGet:
			// 500 — the engine is unreachable or refusing
			// inspect. The helper should propagate this rather
			// than pretend the name is free.
			w.WriteHeader(http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}
	m := newTestManager(t, fd, Config{NetworkName: "nanoku-net"})
	_, _, err := m.CreateAppContainer(context.Background(), "blog", "nginx:1.27", 80, nil, 0, nil)
	if err == nil {
		t.Fatal("expected error from inspect failure")
	}
	if !strings.Contains(err.Error(), "inspect") {
		t.Errorf("err = %v, want wrapped 'inspect'", err)
	}
}
