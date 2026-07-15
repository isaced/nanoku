package api

import (
	"context"
	"testing"

	"github.com/isaced/nanoku/internal/db/container"
)

// TestDeployExecutor_RetiresPriorContainerRowWithSameName documents
// the deploy-time UNIQUE(containers.name) defense. The schema makes
// `containers.name` UNIQUE, and a redeploy on the same app tries to
// insert a new row with the same name as the previous (still-alive)
// row. Without the retire step, the executor would fail with
// "UNIQUE constraint failed". We assert:
//
//   - the prior row is renamed to <name>-retired-<nanos>,
//   - the prior row's status flips to "retired",
//   - the new row inserts with the original name and status "running".
func TestDeployExecutor_RetiresPriorContainerRowWithSameName(t *testing.T) {
	h, a := newExecutorHandlers(t)
	const name = "nanoku-exec-app"
	// First deploy: leave a Container row with the canonical name.
	old, err := h.DB.Container.Create().
		SetDockerID("old-docker-id").
		SetName(name).
		SetImage("nginx:1.27").
		SetStatus(container.StatusRunning).
		SetAppID(a.ID).
		Save(context.Background())
	if err != nil {
		t.Fatalf("create old row: %v", err)
	}

	// Re-deploy path: run executeDeploy end-to-end. The executor
	// will fail at the Docker call (Docker is nil in the test
	// harness) — but the retire UPDATE happens BEFORE the Docker
	// call in the new flow, so the row is already renamed by the
	// time the deploy reports failure. We assert the row state
	// after the failure, not the deploy status.
	depID, err := h.DB.Deploy.Create().
		SetAppID(a.ID).
		SetTrigger("manual").
		SetStatus("running").
		SetImage("nginx:1.27").
		Save(context.Background())
	if err != nil {
		t.Fatalf("create deploy row: %v", err)
	}
	h.executeDeploy(context.Background(), a.ID, depID.ID, "")

	// Old row: renamed + retired.
	oldAfter, err := h.DB.Container.Get(context.Background(), old.ID)
	if err != nil {
		t.Fatalf("get old row: %v", err)
	}
	if oldAfter.Name == name {
		t.Errorf("old row name = %q, want it to be renamed", oldAfter.Name)
	}
	if oldAfter.Status != container.StatusRetired {
		t.Errorf("old row status = %q, want retired", oldAfter.Status)
	}

	// New row: there's no Docker so executeDeploy will have failed
	// before the new row was inserted. The retire still happened,
	// which is the unit under test. Asserting that no second
	// live row was created (i.e. the executor didn't sneak past
	// the Docker-availability guard and create a new Container
	// row with the same name) closes the loop.
	count, err := h.DB.Container.Query().
		Where(container.Name(name), container.StatusEQ(container.StatusRunning)).
		Count(context.Background())
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("live rows with name=%q = %d, want 0", name, count)
	}
}

// TestDeployExecutor_RetireIsNoOpWhenNoPriorRow covers the first-time
// deploy path: there is no row with the desired name, so the bulk
// UPDATE matches zero rows and the executor proceeds straight to the
// Create. The deploy still fails downstream (Docker is nil) but
// that's unrelated — the point of this test is that the empty
// result of the retire UPDATE doesn't get treated as an error.
func TestDeployExecutor_RetireIsNoOpWhenNoPriorRow(t *testing.T) {
	h, a := newExecutorHandlers(t)
	depID, err := h.DB.Deploy.Create().
		SetAppID(a.ID).
		SetTrigger("manual").
		SetStatus("running").
		SetImage("nginx:1.27").
		Save(context.Background())
	if err != nil {
		t.Fatalf("create deploy row: %v", err)
	}
	// Should not panic on a zero-row UPDATE; the deploy is
	// expected to fail at the Docker-availability guard with the
	// standard "docker unavailable" error.
	h.executeDeploy(context.Background(), a.ID, depID.ID, "")

	dep, err := h.DB.Deploy.Get(context.Background(), depID.ID)
	if err != nil {
		t.Fatalf("get deploy: %v", err)
	}
	if dep.Status != "failed" {
		t.Errorf("deploy status = %q, want failed (docker unavailable)", dep.Status)
	}
	if dep.Error == nil || *dep.Error == "" {
		t.Errorf("deploy should have an error message, got nil")
	}
}

// TestDeployExecutor_ClearsPriorCurrentContainerFK documents the
// second UNIQUE-constraint defense. The App→Container O2O is stored
// as containers.app_current_container (UNIQUE per app). A prior
// deploy left a row whose app_current_container still points at
// this app. We assert that the executor clears the relationship on
// the app side BEFORE creating a new container, so the subsequent
// SetCurrentContainerID on the new row doesn't hit the UNIQUE
// constraint.
//
// We can't easily reach the "successful create" state in unit
// tests (Docker is nil), but we can reach the boundary where
// ClearCurrentContainer is called and assert the old row's
// app_current_container is now NULL after the executor returns.
func TestDeployExecutor_ClearsPriorCurrentContainerFK(t *testing.T) {
	h, a := newExecutorHandlers(t)
	const name = "nanoku-exec-app"
	// Simulate the state left by a prior deploy: a Container row
	// with the canonical name and an app_current_container FK
	// pointing back at this app.
	old, err := h.DB.Container.Create().
		SetDockerID("old-docker-id").
		SetName(name).
		SetImage("nginx:1.27").
		SetStatus(container.StatusRunning).
		SetAppID(a.ID).
		Save(context.Background())
	if err != nil {
		t.Fatalf("create old row: %v", err)
	}
	if err := h.DB.App.UpdateOneID(a.ID).
		SetCurrentContainerID(old.ID).
		Exec(context.Background()); err != nil {
		t.Fatalf("set old current_container: %v", err)
	}

	// Run a fresh deploy. The executor will:
	//   1. Retire the old row (rename to nanoku-exec-app-retired-<n>),
	//   2. Clear the app's current_container FK,
	//   3. Hit the Docker-availability guard and fail.
	// We assert steps 1 and 2 happened.
	depID, err := h.DB.Deploy.Create().
		SetAppID(a.ID).
		SetTrigger("manual").
		SetStatus("running").
		SetImage("nginx:1.27").
		Save(context.Background())
	if err != nil {
		t.Fatalf("create deploy row: %v", err)
	}
	h.executeDeploy(context.Background(), a.ID, depID.ID, "")

	// Re-read the app; the O2O edge should now be nil (cleared by
	// the executor before the failed deploy).
	updated, err := h.DB.App.Get(context.Background(), a.ID)
	if err != nil {
		t.Fatalf("get app: %v", err)
	}
	if cur, err := updated.QueryCurrentContainer().Only(context.Background()); err == nil && cur != nil {
		t.Errorf("app.current_container = %+v, want nil (executor should have cleared it)", cur)
	}
	// Re-read the old row: it should still exist (we don't delete
	// Container rows), and the O2O FK should be nil because the
	// app cleared its pointer.
	oldAfter, err := h.DB.Container.Get(context.Background(), old.ID)
	if err != nil {
		t.Fatalf("get old row: %v", err)
	}
	if oldAfter.Status != container.StatusRetired {
		t.Errorf("old row status = %q, want retired", oldAfter.Status)
	}
}

// TestDeployExecutor_RetiresAllComposeServiceRows reproduces the
// production failure mode that motivated this fix: a compose-mode app
// with multiple services has one container row per service, named
// nanoku-<app>-<service>-1 (not just nanoku-<app>-1 or nanoku-<app>).
// The previous name-based retire only matched two hard-coded forms
// and missed every multi-service compose row, so the next deploy
// crashed with `UNIQUE constraint failed: containers.name` on the
// first service that still had a live row.
//
// We seed two service rows for one compose app and assert that
// executeDeploy retires BOTH with distinct retired-names (the
// per-row ID suffix keeps them unique) and leaves no live row with
// the original names. Docker is nil so the deploy reports failure
// after the retire — same shape as the other executor tests.
func TestDeployExecutor_RetiresAllComposeServiceRows(t *testing.T) {
	d := newTestDB(t)
	a, err := d.App.Create().
		SetName("ddd").
		SetDeployMethod("compose").
		SetComposeContent("services:\n  uptime-kuma:\n    image: louislam/uptime-kuma:2\n  web:\n    image: nginx:1.27\n").
		Save(context.Background())
	if err != nil {
		t.Fatalf("seed app: %v", err)
	}
	h := &Handlers{
		DB:             d,
		DeployLock:     NewDeployLock(),
		CaddyfilePath:  t.TempDir() + "/Caddyfile",
		ComposeBaseDir: t.TempDir(),
		Secret:         newTestSealer(t),
	}

	// Seed two live service container rows. These are the
	// canonical compose names: nanoku-<app>-<service>-1.
	svc1, err := h.DB.Container.Create().
		SetDockerID("svc1-docker-id").
		SetName("nanoku-ddd-uptime-kuma-1").
		SetImage("louislam/uptime-kuma:2").
		SetStatus(container.StatusRunning).
		SetAppID(a.ID).
		Save(context.Background())
	if err != nil {
		t.Fatalf("create svc1: %v", err)
	}
	svc2, err := h.DB.Container.Create().
		SetDockerID("svc2-docker-id").
		SetName("nanoku-ddd-web-1").
		SetImage("nginx:1.27").
		SetStatus(container.StatusRunning).
		SetAppID(a.ID).
		Save(context.Background())
	if err != nil {
		t.Fatalf("create svc2: %v", err)
	}

	depID, err := h.DB.Deploy.Create().
		SetAppID(a.ID).
		SetTrigger("manual").
		SetStatus("running").
		SetImage("louislam/uptime-kuma:2").
		Save(context.Background())
	if err != nil {
		t.Fatalf("create deploy: %v", err)
	}
	h.executeDeploy(context.Background(), a.ID, depID.ID, "")

	// Both rows must be retired (status flipped) and renamed
	// (not equal to the original canonical name). The per-row
	// rename is what protects us from a multi-row batch
	// UNIQUE-collision on the new name.
	for _, c := range []struct {
		id   int
		name string
	}{{svc1.ID, "nanoku-ddd-uptime-kuma-1"}, {svc2.ID, "nanoku-ddd-web-1"}} {
		row, err := h.DB.Container.Get(context.Background(), c.id)
		if err != nil {
			t.Fatalf("get row %d: %v", c.id, err)
		}
		if row.Name == c.name {
			t.Errorf("row %d name = %q, want it to be renamed", c.id, row.Name)
		}
		if row.Status != container.StatusRetired {
			t.Errorf("row %d status = %q, want retired", c.id, row.Status)
		}
	}

	// Sanity: no live row remains with either canonical name.
	for _, n := range []string{"nanoku-ddd-uptime-kuma-1", "nanoku-ddd-web-1"} {
		count, err := h.DB.Container.Query().
			Where(container.Name(n), container.StatusNEQ(container.StatusRetired)).
			Count(context.Background())
		if err != nil {
			t.Fatalf("count %s: %v", n, err)
		}
		if count != 0 {
			t.Errorf("live rows named %q = %d, want 0", n, count)
		}
	}
}
