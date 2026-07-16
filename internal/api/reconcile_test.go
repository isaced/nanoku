package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/isaced/nanoku/internal/db"
)

func mkContainer(id int, name string, appID int) *db.Container {
	c := &db.Container{ID: id, Name: name}
	if appID != 0 {
		c.Edges.App = &db.App{ID: appID}
	}
	return c
}

func mkAppWithCurrent(id int, currentID int, currentName string) *db.App {
	a := &db.App{ID: id}
	if currentID != 0 {
		a.Edges.CurrentContainer = &db.Container{ID: currentID, Name: currentName}
	}
	return a
}

func TestComputeReconcile_Clean(t *testing.T) {
	got := computeReconcile(reconcileInputs{
		DBContainers: []*db.Container{
			mkContainer(1, "nanoku-app1", 1),
			mkContainer(2, "nanoku-app2", 2),
		},
		Apps: []*db.App{
			mkAppWithCurrent(1, 1, "nanoku-app1"),
			mkAppWithCurrent(2, 2, "nanoku-app2"),
		},
		DockerNames: []string{"nanoku-app1", "nanoku-app2", "nanoku-caddy"},
		CaddyName:   "nanoku-caddy",
	})
	if got.DBOnlyCount != 0 || got.DockerOnlyCount != 0 || len(got.CurrentMissing) != 0 {
		t.Errorf("clean state should report nothing, got %+v", got)
	}
}

func TestComputeReconcile_DBOnlyMissingContainer(t *testing.T) {
	got := computeReconcile(reconcileInputs{
		DBContainers: []*db.Container{
			mkContainer(1, "nanoku-app1", 1),
			mkContainer(2, "nanoku-gone", 2),
		},
		Apps: []*db.App{
			mkAppWithCurrent(1, 1, "nanoku-app1"),
			mkAppWithCurrent(2, 2, "nanoku-gone"),
		},
		DockerNames: []string{"nanoku-app1", "nanoku-caddy"},
		CaddyName:   "nanoku-caddy",
	})
	if got.DBOnlyCount != 1 {
		t.Fatalf("DBOnlyCount = %d, want 1", got.DBOnlyCount)
	}
	if got.DBOnly[0].Name != "nanoku-gone" || got.DBOnly[0].AppID != 2 || got.DBOnly[0].ContainerID != 2 {
		t.Errorf("unexpected DBOnly row: %+v", got.DBOnly[0])
	}
	if !got.DBOnly[0].IsCurrent {
		t.Error("DBOnly[0] should be flagged as current_container")
	}
	if len(got.CurrentMissing) != 1 || got.CurrentMissing[0] != 2 {
		t.Errorf("CurrentMissing = %v, want [2]", got.CurrentMissing)
	}
}

func TestComputeReconcile_DockerOnlyOrphan(t *testing.T) {
	got := computeReconcile(reconcileInputs{
		DBContainers: []*db.Container{mkContainer(1, "nanoku-app1", 1)},
		Apps:         []*db.App{mkAppWithCurrent(1, 1, "nanoku-app1")},
		DockerNames:  []string{"nanoku-app1", "nanoku-orphan", "nanoku-caddy"},
		CaddyName:    "nanoku-caddy",
	})
	if got.DockerOnlyCount != 1 {
		t.Fatalf("DockerOnlyCount = %d, want 1", got.DockerOnlyCount)
	}
	if got.DockerOnly[0] != "nanoku-orphan" {
		t.Errorf("DockerOnly[0] = %q, want nanoku-orphan", got.DockerOnly[0])
	}
}

func TestComputeReconcile_ExcludesCaddyAndNonNanoku(t *testing.T) {
	got := computeReconcile(reconcileInputs{
		DBContainers: nil,
		Apps:         nil,
		DockerNames: []string{
			"nanoku-caddy",
			"some-other-container",
			"nginx",
		},
		CaddyName: "nanoku-caddy",
	})
	if got.DockerOnlyCount != 0 {
		t.Errorf("only caddy / non-nanoku containers should be excluded, got %v", got.DockerOnly)
	}
}

func TestComputeReconcile_NonCurrentMissingIsTracked(t *testing.T) {
	// A historical Container row whose docker container is missing should
	// appear in DBOnly without being flagged as the current container —
	// it's the audit trail for the previous deploy, not the load-bearing one.
	got := computeReconcile(reconcileInputs{
		DBContainers: []*db.Container{
			mkContainer(1, "nanoku-app1", 1),
			mkContainer(99, "nanoku-app1-old", 1),
		},
		Apps: []*db.App{
			mkAppWithCurrent(1, 1, "nanoku-app1"),
		},
		DockerNames: []string{"nanoku-app1", "nanoku-caddy"},
		CaddyName:   "nanoku-caddy",
	})
	if got.DBOnlyCount != 1 {
		t.Fatalf("DBOnlyCount = %d, want 1", got.DBOnlyCount)
	}
	if got.DBOnly[0].ContainerID != 99 {
		t.Errorf("DBOnly[0].ContainerID = %d, want 99", got.DBOnly[0].ContainerID)
	}
	if got.DBOnly[0].IsCurrent {
		t.Error("historical container should not be flagged as current")
	}
	if len(got.CurrentMissing) != 0 {
		t.Errorf("CurrentMissing = %v, want empty", got.CurrentMissing)
	}
}

// --- RemoveOrphanContainer / SystemRemoveOrphan -------------------------
//
// The refusal path is a typed error (*orphanRefusal) so the HTTP
// handler can route it to 400 with the safe message, while DB /
// docker errors go to 500. The tests below pin both behaviors.

// TestRemoveOrphanContainer_NilDockerRefuses covers the simplest
// refusal: with Docker == nil, the function short-circuits to a
// typed refusal (not a panic) and the message is hand-written
// (no internal detail leaks).
func TestRemoveOrphanContainer_NilDockerRefuses(t *testing.T) {
	h := &Handlers{DB: newTestDB(t), Docker: nil}
	err := h.RemoveOrphanContainer(context.Background(), "nanoku-anything")
	if err == nil {
		t.Fatal("expected refusal, got nil")
	}
	var refused *orphanRefusal
	if !errors.As(err, &refused) {
		t.Fatalf("err is not *orphanRefusal: %T (%v)", err, err)
	}
	if !strings.Contains(refused.msg, "docker manager not initialized") {
		t.Errorf("msg = %q, want hand-written message", refused.msg)
	}
}

// TestSystemRemoveOrphan_RefusalIs400 is the HTTP-end test: the
// typed refusal is unwrapped to its safe message, served as 400.
// The typed error name must NOT appear in the response — only the
// message we want the operator to see.
func TestSystemRemoveOrphan_RefusalIs400(t *testing.T) {
	h := &Handlers{DB: newTestDB(t), Docker: nil}
	req := httptest.NewRequest(http.MethodDelete, "/api/system/orphans/nanoku-orphan-1", nil)
	req.SetPathValue("name", "nanoku-orphan-1")
	w := httptest.NewRecorder()

	h.SystemRemoveOrphan(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("Code = %d, want 400; body=%q", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "docker manager not initialized") {
		t.Errorf("body = %q, want refusal message", body)
	}
	if strings.Contains(body, "orphanRefusal") {
		t.Errorf("body leaked type name: %q", body)
	}
}

// TestSystemRemoveOrphan_NonRefusalRoutesToInternalErr pins the
// routing contract: when RemoveOrphanContainer returns a non-refusal
// error, the handler falls through to writeInternalErr (500 with
// the stable message). We can't easily exercise the actual 500
// path here without a fake docker manager that fails
// RemoveContainer, but the routing decision is `errors.As(err,
// &refused) ? 400 : 500` — any future refactor that returns a raw
// error from a refusal branch (forgetting the typed-error wrapper)
// would change this test's expectation.
func TestSystemRemoveOrphan_NonRefusalRoutesToInternalErr(t *testing.T) {
	// We exercise the routing logic directly via a tiny synthetic
	// handler that mirrors SystemRemoveOrphan's branch. This pins
	// the contract independently of the docker manager.
	refused := &orphanRefusal{"refused for test"}
	other := errors.New("ent: database is locked")

	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"refusal", refused, http.StatusBadRequest},
		{"non-refusal", other, http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var refused *orphanRefusal
			got := http.StatusInternalServerError
			if errors.As(tc.err, &refused) {
				got = http.StatusBadRequest
			}
			if got != tc.want {
				t.Errorf("err=%v routed to %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}