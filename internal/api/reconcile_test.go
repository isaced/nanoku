package api

import (
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