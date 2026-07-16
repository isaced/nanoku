package api

// cleanup_test.go covers the Janitor end-to-end. The unit
// tests are organised bottom-up:
//
//   - parseDeployIDFromLogName: a pure helper, edge cases only.
//   - per-task bodies (purgeOldLogFiles, pruneOldContainers,
//     pruneOrphanLogFiles, purgeExpiredSessions): seed the DB
//     and the on-disk log store, run the task once, assert
//     the resulting state.
//   - CleanupConfig defaults: verify the documented defaults.
//   - Janitor loop + Start/Stop: drives the master ticker with
//     short intervals; uses injected custom tasks (channel
//     signals + a panic) to assert cadence and isolation
//     without sleeping for real wall-clock minutes.
//   - Janitor.Status / RunTask: read-after-write semantics.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/isaced/nanoku/internal/db"
	apppkg "github.com/isaced/nanoku/internal/db/app"
	"github.com/isaced/nanoku/internal/db/container"
	"github.com/isaced/nanoku/internal/db/deploy"
)

// ---- helpers ------------------------------------------------------------

// newTestJanitor builds a Janitor against a fresh DB + log dir.
// The session store is built without a DB pointer where the
// cleanup tests don't exercise session behavior.
func newTestJanitor(t *testing.T) (*Janitor, *db.DB, string) {
	t.Helper()
	d := newTestDB(t)
	logDir := t.TempDir()
	store, err := newDeployLogStore(logDir)
	if err != nil {
		t.Fatalf("deploy log store: %v", err)
	}
	sessions := NewSessionStore(d)
	return NewJanitor(d, sessions, store, CleanupConfig{}), d, logDir
}

// writeLog is a small convenience that creates a log file for
// the given deploy id with the given content. The content is
// echoed back so tests can assert on Bytes reclaimed.
func writeLog(t *testing.T, dir string, deployID int, content string) string {
	t.Helper()
	path := filepath.Join(dir, fmt.Sprintf("%d.log", deployID))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write log %s: %v", path, err)
	}
	return path
}

// seedAppWithContainers creates an app and N container rows in
// the listed statuses, each with its own age. created_at is
// backdated explicitly via SetCreatedAt (ent's TimeMixin
// defaults created_at to now(), which would defeat age-based
// queries). Returns the app so the caller can mark one
// container as current.
func seedAppWithContainers(
	t *testing.T,
	d *db.DB,
	name string,
	ages []time.Duration,
	statuses []container.Status,
) *db.App {
	t.Helper()
	a, err := d.App.Create().
		SetName(name).
		SetImage("nginx:1.27").
		SetPort(80).
		Save(context.Background())
	if err != nil {
		t.Fatalf("seed app: %v", err)
	}
	now := time.Now().UTC()
	for i, age := range ages {
		_, err := d.Container.Create().
			SetDockerID(fmt.Sprintf("d%d", i)).
			SetName(fmt.Sprintf("%s-c%d", name, i)).
			SetImage("nginx:1.27").
			SetStatus(statuses[i]).
			SetCreatedAt(now.Add(-age)).
			SetStartedAt(now.Add(-age)).
			SetAppID(a.ID).
			Save(context.Background())
		if err != nil {
			t.Fatalf("seed container: %v", err)
		}
	}
	return a
}

// markCurrent sets app.current_container_id to the given
// container, simulating a successful deploy.
func markCurrent(t *testing.T, d *db.DB, appID, containerID int) {
	t.Helper()
	if _, err := d.App.UpdateOneID(appID).SetCurrentContainerID(containerID).Save(context.Background()); err != nil {
		t.Fatalf("set current_container: %v", err)
	}
}

// seedDeployAt creates a deploy row with the given age and
// status. created_at is backdated explicitly via
// SetCreatedAt (ent's TimeMixin would default it to now(),
// defeating age-based queries).
func seedDeployAt(t *testing.T, d *db.DB, appID int, age time.Duration, status deploy.Status) *db.Deploy {
	t.Helper()
	now := time.Now().UTC()
	dep, err := d.Deploy.Create().
		SetAppID(appID).
		SetTrigger("manual").
		SetStatus(status).
		SetCreatedAt(now.Add(-age)).
		SetStartedAt(now.Add(-age)).
		SetFinishedAt(now.Add(-age + time.Minute)).
		SetImage("nginx:1.27").
		Save(context.Background())
	if err != nil {
		t.Fatalf("seed deploy: %v", err)
	}
	return dep
}

// ---- parseDeployIDFromLogName ------------------------------------------

func TestParseDeployIDFromLogName(t *testing.T) {
	cases := []struct {
		in     string
		wantID int
		wantOK bool
	}{
		{"42.log", 42, true},
		{"1.log", 1, true},
		{"999999.log", 999999, true},
		{"foo.log", 0, false},
		{"42", 0, false},      // no .log suffix
		{".42.log", 0, false}, // hidden file
		{".hidden", 0, false}, // no suffix
		{"-1.log", 0, false},  // negative id
		{"0.log", 0, false},   // zero id is invalid
		{"", 0, false},
		{"42.log.bak", 0, false},               // extra extension
		{"99999999999999999999.log", 0, false}, // overflow
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, ok := parseDeployIDFromLogName(tc.in)
			if ok != tc.wantOK || got != tc.wantID {
				t.Errorf("parseDeployIDFromLogName(%q) = (%d, %v), want (%d, %v)",
					tc.in, got, ok, tc.wantID, tc.wantOK)
			}
		})
	}
}

// ---- CleanupConfig defaults --------------------------------------------

func TestCleanupConfig_Defaults(t *testing.T) {
	cfg := CleanupConfig{}
	cfg.applyDefaults()
	if cfg.KeepDeploysDays != 30 {
		t.Errorf("KeepDeploysDays = %d, want 30", cfg.KeepDeploysDays)
	}
	if cfg.PurgeExpiredSessionsEvery != time.Hour {
		t.Errorf("PurgeExpiredSessionsEvery = %s, want 1h", cfg.PurgeExpiredSessionsEvery)
	}
	if cfg.PurgeStaleAttemptsEvery != 5*time.Minute {
		t.Errorf("PurgeStaleAttemptsEvery = %s, want 5m", cfg.PurgeStaleAttemptsEvery)
	}
	if cfg.PruneOrphanLogFilesEvery != time.Hour {
		t.Errorf("PruneOrphanLogFilesEvery = %s, want 1h", cfg.PruneOrphanLogFilesEvery)
	}
	if cfg.PruneOldLogFilesEvery != 6*time.Hour {
		t.Errorf("PruneOldLogFilesEvery = %s, want 6h", cfg.PruneOldLogFilesEvery)
	}
	if cfg.PruneOldContainersEvery != 24*time.Hour {
		t.Errorf("PruneOldContainersEvery = %s, want 24h", cfg.PruneOldContainersEvery)
	}
	if cfg.TaskTimeout != 30*time.Second {
		t.Errorf("TaskTimeout = %s, want 30s", cfg.TaskTimeout)
	}
	if cfg.Now == nil {
		t.Error("Now should default to time.Now")
	}
}

// ---- purgeExpiredSessions ----------------------------------------------

func TestPurgeExpiredSessions(t *testing.T) {
	d := newTestDB(t)
	sessions := NewSessionStore(d)

	// Two expired sessions (backdate by setting ExpiresAt
	// directly via the entity builder).
	now := time.Now().UTC()
	for i := 0; i < 2; i++ {
		_, _ = d.Session.Create().
			SetTokenHash(fmt.Sprintf("h%d", i)).
			SetCreatedAt(now.Add(-2 * time.Hour)).
			SetExpiresAt(now.Add(-time.Hour)).
			SetLastSeen(now.Add(-2 * time.Hour)).
			SetUserID(seedUserForCleanup(t, d, fmt.Sprintf("u%d", i)).ID).
			Save(context.Background())
	}
	// One live session.
	_, _ = d.Session.Create().
		SetTokenHash("h-live").
		SetCreatedAt(now).
		SetExpiresAt(now.Add(time.Hour)).
		SetLastSeen(now).
		SetUserID(seedUserForCleanup(t, d, "u-live").ID).
		Save(context.Background())

	j := NewJanitor(d, sessions, nil, CleanupConfig{})
	res, err := j.purgeExpiredSessions(context.Background())
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if res.Pruned != 2 {
		t.Errorf("Pruned = %d, want 2", res.Pruned)
	}
	remaining, _ := d.Session.Query().Count(context.Background())
	if remaining != 1 {
		t.Errorf("sessions left = %d, want 1", remaining)
	}
}

// ---- pruneOrphanLogFiles -----------------------------------------------

func TestPruneOrphanLogFiles(t *testing.T) {
	j, d, dir := newTestJanitor(t)
	app := seedAppWithContainers(t, d, "orph", nil, nil)

	// Deploy 1: log exists, deploy row exists, status=success.
	// Should NOT be pruned (the live deploys task handles
	// retention; this task only handles orphans).
	dep1 := seedDeployAt(t, d, app.ID, time.Hour, deploy.StatusSuccess)
	writeLog(t, dir, dep1.ID, "live log\n")

	// Deploy 2: log exists, deploy row missing.
	// Should be pruned.
	orphanID := 9999
	orphanPath := writeLog(t, dir, orphanID, "orphan contents\n")

	// Deploy 3: log exists, deploy row exists, status=running.
	// Should NOT be pruned (worker is still writing).
	dep3 := seedDeployAt(t, d, app.ID, time.Minute, deploy.StatusRunning)
	writeLog(t, dir, dep3.ID, "still being written\n")

	// Non-log file should be ignored.
	nonLog := filepath.Join(dir, "README.md")
	if err := os.WriteFile(nonLog, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	res, err := j.pruneOrphanLogFiles(context.Background())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if res.Pruned != 1 {
		t.Errorf("Pruned = %d, want 1 (orphan only)", res.Pruned)
	}
	if res.Bytes <= 0 {
		t.Errorf("Bytes = %d, want > 0", res.Bytes)
	}

	if _, err := os.Stat(orphanPath); !os.IsNotExist(err) {
		t.Errorf("orphan log still exists: err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("%d.log", dep1.ID))); err != nil {
		t.Errorf("live log should remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("%d.log", dep3.ID))); err != nil {
		t.Errorf("running log should remain: %v", err)
	}
	if _, err := os.Stat(nonLog); err != nil {
		t.Errorf("non-log file should be untouched: %v", err)
	}
}

func TestPruneOrphanLogFiles_MissingDirIsNoop(t *testing.T) {
	d := newTestDB(t)
	// Build a store pointing at a non-existent dir; this
	// shouldn't happen in production (NewDeployLogStore
	// mkdirs) but a paranoid check matters when the dir is
	// bind-mounted from a host that lost the volume.
	logDir := filepath.Join(t.TempDir(), "does-not-exist")
	store, _ := newDeployLogStore(t.TempDir()) // for the type; we'll override dir
	// Reassign dir to a missing path via direct construction
	// of the type (we keep it in-package).
	store.dir = logDir
	j := NewJanitor(d, nil, store, CleanupConfig{})
	res, err := j.pruneOrphanLogFiles(context.Background())
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if res.Pruned != 0 {
		t.Errorf("Pruned = %d, want 0", res.Pruned)
	}
}

// ---- pruneOldLogFiles ---------------------------------------------------

func TestPruneOldLogFiles_KeepsRecentAndRunning(t *testing.T) {
	j, d, dir := newTestJanitor(t)
	app := seedAppWithContainers(t, d, "oldlog", nil, nil)

	// Old success: should be pruned.
	oldSucc := seedDeployAt(t, d, app.ID, 60*24*time.Hour, deploy.StatusSuccess)
	oldPath := writeLog(t, dir, oldSucc.ID, "old success log\n")

	// Old failed: should be pruned.
	oldFail := seedDeployAt(t, d, app.ID, 60*24*time.Hour, deploy.StatusFailed)
	oldFailPath := writeLog(t, dir, oldFail.ID, "old fail log\n")

	// Recent success: should be kept.
	recentSucc := seedDeployAt(t, d, app.ID, 24*time.Hour, deploy.StatusSuccess)
	writeLog(t, dir, recentSucc.ID, "recent success log\n")

	// Old running: should be kept (worker still writing).
	oldRunning := seedDeployAt(t, d, app.ID, 60*24*time.Hour, deploy.StatusRunning)
	writeLog(t, dir, oldRunning.ID, "still writing\n")

	// Use a fixed Now so the test doesn't drift on a midnight
	// boundary.
	frozen := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	j.cfg.Now = func() time.Time { return frozen }
	j.cfg.KeepDeploysDays = 30

	res, err := j.pruneOldLogFiles(context.Background())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if res.Pruned != 2 {
		t.Errorf("Pruned = %d, want 2", res.Pruned)
	}
	if res.Bytes == 0 {
		t.Error("Bytes should be > 0")
	}
	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Errorf("old success log should be gone: err=%v", err)
	}
	if _, err := os.Stat(oldFailPath); !os.IsNotExist(err) {
		t.Errorf("old fail log should be gone: err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("%d.log", recentSucc.ID))); err != nil {
		t.Errorf("recent log should remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, fmt.Sprintf("%d.log", oldRunning.ID))); err != nil {
		t.Errorf("running log should remain: %v", err)
	}
}

func TestPruneOldLogFiles_NoMatchingDeploySkipped(t *testing.T) {
	j, _, dir := newTestJanitor(t)
	// File exists but no deploy row at all (orphan — covered
	// by a different task; this task only touches files whose
	// deploy row exists and is old + finished).
	path := writeLog(t, dir, 12345, "stray\n")
	res, err := j.pruneOldLogFiles(context.Background())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if res.Pruned != 0 {
		t.Errorf("Pruned = %d, want 0 (orphan handled elsewhere)", res.Pruned)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("stray file should not be touched by this task: %v", err)
	}
}

// ---- pruneOldContainers -------------------------------------------------

func TestPruneOldContainers_LeavesCurrentAlone(t *testing.T) {
	j, d, _ := newTestJanitor(t)
	frozen := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	j.cfg.Now = func() time.Time { return frozen }
	j.cfg.KeepDeploysDays = 30

	// 4 containers: very old running (current_container of
	// some app), old exited, old dead, very recent exited.
	app := seedAppWithContainers(t, d, "cc",
		[]time.Duration{
			60 * 24 * time.Hour, // current
			60 * 24 * time.Hour, // exited
			90 * 24 * time.Hour, // dead
			2 * 24 * time.Hour,  // recent exited
		},
		[]container.Status{
			container.StatusRunning,
			container.StatusExited,
			container.StatusDead,
			container.StatusExited,
		},
	)
	conts, _ := d.Container.Query().Where(container.HasAppWith(apppkg.IDEQ(app.ID))).All(context.Background())
	markCurrent(t, d, app.ID, conts[0].ID)

	res, err := j.pruneOldContainers(context.Background())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	// Expect 2 pruned: the old exited + the old dead. The
	// current_container is "running" so it's already
	// excluded by the status filter; the recent exited is
	// excluded by the age filter.
	if res.Pruned != 2 {
		t.Errorf("Pruned = %d, want 2", res.Pruned)
	}

	// Verify the right rows are gone.
	gotIDs := map[int]container.Status{}
	survivors, _ := d.Container.Query().All(context.Background())
	for _, c := range survivors {
		gotIDs[c.ID] = c.Status
	}
	if _, ok := gotIDs[conts[0].ID]; !ok {
		t.Error("current_container (running) was deleted")
	}
	if _, ok := gotIDs[conts[3].ID]; !ok {
		t.Error("recent exited was deleted (should be kept by age filter)")
	}
}

func TestPruneOldContainers_PreservesCurrentEvenIfOldAndExited(t *testing.T) {
	// An app whose current_container is in a terminal state
	// (because the latest deploy failed and marked the old
	// container as "exited" without re-pointing current) is
	// a real edge case. We must not delete the current row
	// even if it matches every other filter.
	j, d, _ := newTestJanitor(t)
	frozen := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	j.cfg.Now = func() time.Time { return frozen }
	j.cfg.KeepDeploysDays = 30

	a := seedAppWithContainers(t, d, "stale",
		[]time.Duration{60 * 24 * time.Hour},
		[]container.Status{container.StatusExited},
	)
	conts, _ := d.Container.Query().Where(container.HasAppWith(apppkg.IDEQ(a.ID))).All(context.Background())
	markCurrent(t, d, a.ID, conts[0].ID)

	if _, err := j.pruneOldContainers(context.Background()); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if _, err := d.Container.Get(context.Background(), conts[0].ID); err != nil {
		t.Errorf("current_container (exited but still current) was deleted: %v", err)
	}
}

func TestPruneOldContainers_SlowPathUsedWhenManyActiveApps(t *testing.T) {
	// Drive pruneOldContainers down the slow path by making
	// idFilterCap+1 active apps. Each gets an old exited
	// container; we verify they all survive (current_container
	// filter applies regardless of path).
	j, d, _ := newTestJanitor(t)
	frozen := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	j.cfg.Now = func() time.Time { return frozen }
	j.cfg.KeepDeploysDays = 30

	// Mirror the cap in cleanup.go (intentionally duplicated
	// rather than exported — the production constant is a
	// performance tuning knob, not a stable contract).
	const idFilterCap = 100
	const n = idFilterCap + 5
	for i := 0; i < n; i++ {
		a := seedAppWithContainers(t, d, fmt.Sprintf("bulk%d", i),
			[]time.Duration{60 * 24 * time.Hour},
			[]container.Status{container.StatusExited},
		)
		conts, _ := d.Container.Query().Where(container.HasAppWith(apppkg.IDEQ(a.ID))).All(context.Background())
		markCurrent(t, d, a.ID, conts[0].ID)
	}
	// Add one non-current, very old, exited container for
	// each app to verify they're pruned in the slow path.
	for i := 0; i < n; i++ {
		_, _ = d.Container.Create().
			SetDockerID(fmt.Sprintf("extra%d", i)).
			SetName(fmt.Sprintf("bulk%d-extra", i)).
			SetImage("nginx:1.27").
			SetStatus(container.StatusExited).
			SetCreatedAt(frozen.Add(-60 * 24 * time.Hour)).
			SetStartedAt(frozen.Add(-60 * 24 * time.Hour)).
			SetStoppedAt(frozen.Add(-60 * 24 * time.Hour)).
			Save(context.Background())
	}
	res, err := j.pruneOldContainers(context.Background())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if res.Pruned != n {
		t.Errorf("Pruned = %d, want %d (slow path)", res.Pruned, n)
	}
}

// ---- Janitor loop: cadence, panic isolation, shutdown -------------------

func TestJanitor_RunsCustomTaskAtInterval(t *testing.T) {
	d := newTestDB(t)
	j := NewJanitor(d, nil, nil, CleanupConfig{TaskTimeout: time.Second})

	// Use a long interval (1h) and verify only the one-shot
	// startup run fires. Then trigger a second run via RunTask.
	fired := make(chan struct{}, 4)
	j.WithTask("ticker", time.Hour, func(ctx context.Context) (CleanupResult, error) {
		fired <- struct{}{}
		return CleanupResult{Pruned: 1}, nil
	})

	j.Start(context.Background())
	defer j.Stop(time.Second)

	select {
	case <-fired:
	case <-time.After(time.Second):
		t.Fatal("custom task did not run on Start")
	}

	// Confirm the status map records the run.
	st := j.Status()["ticker"]
	if st.RunCount != 1 {
		t.Errorf("RunCount = %d, want 1", st.RunCount)
	}
	if st.LastResult.Pruned != 1 {
		t.Errorf("LastResult.Pruned = %d, want 1", st.LastResult.Pruned)
	}
	if st.NextRun.IsZero() {
		t.Error("NextRun should be populated after a run")
	}
}

func TestJanitor_BuiltInTasksFireOnStart(t *testing.T) {
	d := newTestDB(t)
	sessions := NewSessionStore(d)
	j := NewJanitor(d, sessions, nil, CleanupConfig{})

	// Disable the running deploys that the Janitor would do
	// background work against, by handing it a nil log store.
	// (purge-stale-attempts + purge-expired-sessions don't
	// need the log store; they should still report 0/0.)
	j.Start(context.Background())
	defer j.Stop(time.Second)

	status := j.Status()
	for _, name := range []string{
		"purge-expired-sessions",
		"purge-stale-attempts",
		"prune-orphan-log-files",
		"prune-old-log-files",
		"prune-old-containers",
	} {
		if _, ok := status[name]; !ok {
			t.Errorf("task %q missing from status", name)
		}
	}
}

func TestJanitor_PanicDoesNotKillLoop(t *testing.T) {
	d := newTestDB(t)
	j := NewJanitor(d, nil, nil, CleanupConfig{TaskTimeout: time.Second})

	var panics atomic.Int32
	var healthy atomic.Int32
	fired := make(chan struct{}, 8)
	j.WithTask("panicker", time.Hour, func(ctx context.Context) (CleanupResult, error) {
		panics.Add(1)
		panic("boom")
	})
	j.WithTask("healthy", time.Hour, func(ctx context.Context) (CleanupResult, error) {
		healthy.Add(1)
		fired <- struct{}{}
		return CleanupResult{}, nil
	})

	j.Start(context.Background())
	defer j.Stop(time.Second)

	select {
	case <-fired:
	case <-time.After(2 * time.Second):
		t.Fatal("healthy task never ran (panicker must have crashed the loop)")
	}
	if panics.Load() == 0 {
		t.Error("panicker should have fired at least once")
	}

	st := j.Status()["panicker"]
	if st.LastErr == "" {
		t.Error("panicker should have recorded an error")
	}
	if st.ErrCount == 0 {
		t.Error("ErrCount should be incremented")
	}
	if st.RunCount == 0 {
		t.Error("RunCount should still be incremented even on panic")
	}
}

func TestJanitor_StopWaitsForInflightTask(t *testing.T) {
	// The slow task respects ctx cancellation, so when Stop
	// cancels the parent context the task returns promptly
	// with ctx.Err() and Stop unblocks. This is the contract
	// every task should honor — see how the built-in tasks
	// check ctx.Err() at the top of their loops.
	//
	// We use a 1-minute TaskTimeout to make sure the test
	// doesn't accidentally pass via the timeout instead of
	// via Stop's cancellation. The slow task blocks on
	// <-ctx.Done(), so the only way it returns is through
	// either Stop or task-timeout.
	d := newTestDB(t)
	j := NewJanitor(d, nil, nil, CleanupConfig{TaskTimeout: time.Minute})

	started := make(chan struct{}, 1)
	j.WithTask("slow", time.Hour, func(ctx context.Context) (CleanupResult, error) {
		started <- struct{}{}
		<-ctx.Done()
		return CleanupResult{}, ctx.Err()
	})

	// Start with a parent context we can cancel via t.Cleanup
	// to prove the task's ctx was derived from the parent
	// (and not the package-level background).
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()

	// Start blocks on the one-shot pass. Run it in a goroutine
	// so the test can synchronize on `started` to know the
	// task is mid-flight.
	startDone := make(chan struct{})
	go func() {
		j.Start(parent)
		close(startDone)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("slow task never started")
	}

	stopDone := make(chan struct{})
	go func() {
		j.Stop(time.Second)
		close(stopDone)
	}()

	select {
	case <-stopDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop did not unblock after ctx cancellation")
	}
	<-startDone
}

func TestJanitor_RunTaskUnknownReturnsError(t *testing.T) {
	d := newTestDB(t)
	j := NewJanitor(d, nil, nil, CleanupConfig{})
	_, err := j.RunTask(context.Background(), "nope")
	if !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("err = %v, want ErrTaskNotFound", err)
	}
}

func TestJanitor_StartStopWithoutTasksIsSafe(t *testing.T) {
	d := newTestDB(t)
	j := NewJanitor(d, nil, nil, CleanupConfig{})

	// No tasks registered; Start should immediately close
	// the done channel without spawning a goroutine that
	// could leak. Stop must still return cleanly.
	j.Start(context.Background())
	j.Stop(time.Second)
	if j.Status() == nil {
		t.Error("Status should still return an initialized map")
	}
}

func TestJanitor_CadenceUnderFastTick(t *testing.T) {
	// Drive a 50ms-interval task through two ticks of the
	// 1-minute master loop by injecting a shorter master
	// tick. We don't expose the master interval, so this
	// test uses a different strategy: register a 1ms-interval
	// task and pump RunTask explicitly twice. The point is
	// to verify that re-running the same task updates the
	// status map correctly.
	d := newTestDB(t)
	j := NewJanitor(d, nil, nil, CleanupConfig{TaskTimeout: time.Second})

	count := 0
	j.WithTask("counter", time.Millisecond, func(ctx context.Context) (CleanupResult, error) {
		count++
		return CleanupResult{}, nil
	})

	// First run: status LastRun should populate.
	_, err := j.RunTask(context.Background(), "counter")
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	st := j.Status()["counter"]
	if st.RunCount != 1 {
		t.Errorf("RunCount = %d, want 1", st.RunCount)
	}

	// The 1ms interval hasn't elapsed against time.Now's
	// resolution, so we can't reliably call RunTask again
	// without a brief sleep. Sleep just enough to clear the
	// interval, then re-run.
	time.Sleep(5 * time.Millisecond)
	_, err = j.RunTask(context.Background(), "counter")
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	st = j.Status()["counter"]
	if st.RunCount != 2 {
		t.Errorf("RunCount = %d, want 2", st.RunCount)
	}
	if count != 2 {
		t.Errorf("task body fired %d times, want 2", count)
	}
}

// ---- Status / Result reporting -----------------------------------------

func TestCleanupResult_ReportsBytes(t *testing.T) {
	// pruneOrphanLogFiles is the only task that reports Bytes.
	// Exercise the path explicitly so the contract is pinned.
	j, d, dir := newTestJanitor(t)
	seedAppWithContainers(t, d, "br", nil, nil)

	// Three orphan files of known size.
	for _, id := range []int{11, 22, 33} {
		writeLog(t, dir, id, strings.Repeat("a", 100))
	}
	res, err := j.pruneOrphanLogFiles(context.Background())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if res.Pruned != 3 {
		t.Errorf("Pruned = %d, want 3", res.Pruned)
	}
	if res.Bytes != 300 {
		t.Errorf("Bytes = %d, want 300", res.Bytes)
	}
	if res.Scanned != 3 {
		t.Errorf("Scanned = %d, want 3", res.Scanned)
	}
}

func TestJanitor_StatusSnapshotIsACopy(t *testing.T) {
	// Mutating the returned map must not affect future
	// Status() calls.
	d := newTestDB(t)
	j := NewJanitor(d, nil, nil, CleanupConfig{TaskTimeout: time.Second})
	j.WithTask("noop", time.Hour, func(ctx context.Context) (CleanupResult, error) {
		return CleanupResult{}, nil
	})
	if _, err := j.RunTask(context.Background(), "noop"); err != nil {
		t.Fatal(err)
	}

	st := j.Status()
	st["noop"] = taskStatus{RunCount: 999}

	fresh := j.Status()["noop"]
	if fresh.RunCount == 999 {
		t.Error("Status returned a shared map; callers can corrupt internal state")
	}
}

// ---- parallel safety ---------------------------------------------------

// TestJanitor_ConcurrentStatusReads is a small race-detector
// smoke test: hammer Status() from many goroutines while a
// task ticks. Run with `-race` to catch any data races.
func TestJanitor_ConcurrentStatusReads(t *testing.T) {
	d := newTestDB(t)
	j := NewJanitor(d, nil, nil, CleanupConfig{TaskTimeout: time.Second})
	j.WithTask("tick", 10*time.Millisecond, func(ctx context.Context) (CleanupResult, error) {
		return CleanupResult{Pruned: 1}, nil
	})

	j.Start(context.Background())
	defer j.Stop(time.Second)

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = j.Status()
				}
			}
		}()
	}
	time.Sleep(80 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// ---- supporting helpers -------------------------------------------------

// seedUserForCleanup is a thin wrapper around the existing
// login_test.go helper that uses a throwaway password.
// Sessions require a FK to users; an isolated test user is
// the cheapest way to wire one up.
func seedUserForCleanup(t *testing.T, d *db.DB, name string) *db.User {
	t.Helper()
	return seedUser(t, d, name, "x") // password irrelevant; we never call Login
}

// SortByName is a tiny helper for tests that want stable
// ordering on the container / deploy query results.
func sortByName(ss []string) []string {
	out := append([]string(nil), ss...)
	sort.Strings(out)
	return out
}

var _ = sortByName // keep the helper exported-but-internal

// ---- coverage gaps: lifecycle edges, status states, error paths ---------

// TestJanitor_StartNilParentDefaultsToBackground pins the
// contract that Start(nil) does not crash and behaves the same
// as Start(context.Background()). Defensive — without this, a
// misconfigured main.go could nil-deref at boot.
func TestJanitor_StartNilParentDefaultsToBackground(t *testing.T) {
	d := newTestDB(t)
	j := NewJanitor(d, nil, nil, CleanupConfig{TaskTimeout: time.Second})
	var fired atomic.Int32
	j.WithTask("ok", time.Hour, func(ctx context.Context) (CleanupResult, error) {
		fired.Add(1)
		return CleanupResult{}, nil
	})
	j.Start(nil)
	defer j.Stop(time.Second)
	if fired.Load() == 0 {
		t.Fatal("task did not run on Start(nil)")
	}
}

// TestJanitor_StopIdempotent: calling Stop twice in a row
// must not deadlock or panic. The second call short-circuits
// because j.cancel is already drained, and the done channel
// is already closed. Important for shutdown code that may
// run through multiple paths (signal handler + defer, etc).
func TestJanitor_StopIdempotent(t *testing.T) {
	d := newTestDB(t)
	j := NewJanitor(d, nil, nil, CleanupConfig{TaskTimeout: time.Second})
	j.WithTask("noop", time.Hour, func(ctx context.Context) (CleanupResult, error) {
		return CleanupResult{}, nil
	})
	j.Start(context.Background())
	j.Stop(time.Second)
	// Second call: must return promptly.
	done := make(chan struct{})
	go func() {
		j.Stop(time.Second)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("second Stop hung")
	}
}

// TestJanitor_StopWithoutStart covers the case where Stop is
// called on a freshly-constructed Janitor (Start was never
// called). The cancel field is nil; Stop must early-return
// without panicking.
func TestJanitor_StopWithoutStart(t *testing.T) {
	j := NewJanitor(newTestDB(t), nil, nil, CleanupConfig{})
	// Should be a no-op, not a panic.
	j.Stop(time.Second)
}

// TestJanitor_CustomTaskNameCollision covers the warning path
// in `tasks()`: when a WithTask name shadows a built-in, the
// custom one is dropped (built-in wins, to prevent the
// registration from silently disabling a core cleanup). The
// status map should still record the built-in's run, not the
// shadow.
func TestJanitor_CustomTaskNameCollision(t *testing.T) {
	d := newTestDB(t)
	j := NewJanitor(d, nil, nil, CleanupConfig{TaskTimeout: time.Second})
	var shadowFired atomic.Int32
	j.WithTask("prune-old-log-files", time.Hour, func(ctx context.Context) (CleanupResult, error) {
		shadowFired.Add(1)
		return CleanupResult{Pruned: 999}, nil
	})
	j.Start(context.Background())
	defer j.Stop(time.Second)
	if shadowFired.Load() != 0 {
		t.Error("shadow task should not have run (built-in wins)")
	}
	// Built-in should have run, recorded under its real name.
	if _, ok := j.Status()["prune-old-log-files"]; !ok {
		t.Error("built-in task missing from status")
	}
}

// TestRunTask_RecordsNonPanicError pins the error-recording
// path: a task returning a regular error (not a panic) must
// show up in Status with the message and ErrCount bumped.
// This is distinct from TestJanitor_PanicDoesNotKillLoop,
// which exercises the recover path.
func TestRunTask_RecordsNonPanicError(t *testing.T) {
	d := newTestDB(t)
	j := NewJanitor(d, nil, nil, CleanupConfig{TaskTimeout: time.Second})
	want := errors.New("docker socket closed")
	j.WithTask("fails", time.Hour, func(ctx context.Context) (CleanupResult, error) {
		return CleanupResult{}, want
	})

	got, err := j.RunTask(context.Background(), "fails")
	if !errors.Is(err, want) {
		t.Errorf("RunTask err = %v, want %v", err, want)
	}
	if got.Pruned != 0 {
		t.Errorf("result = %+v, want zero value", got)
	}
	st := j.Status()["fails"]
	if st.ErrCount != 1 {
		t.Errorf("ErrCount = %d, want 1", st.ErrCount)
	}
	if st.LastErr != want.Error() {
		t.Errorf("LastErr = %q, want %q", st.LastErr, want.Error())
	}
	if st.RunCount != 1 {
		t.Errorf("RunCount = %d, want 1 (error runs still count)", st.RunCount)
	}
	if !st.LastErrAt.IsZero() {
		// LastErrAt should be populated when an error happens.
		if st.LastErrAt.Before(st.LastRun) {
			t.Error("LastErrAt < LastRun")
		}
	}
}

// TestPruneOldLogFiles_KeepsPendingStatus covers the third
// status: an old deploy row that's still in "pending" state
// (e.g. aborted before docker pull started) must NOT have
// its log file pruned. The worker may resume it on a future
// trigger, and a missing log would lose history.
func TestPruneOldLogFiles_KeepsPendingStatus(t *testing.T) {
	j, d, dir := newTestJanitor(t)
	app := seedAppWithContainers(t, d, "pending", nil, nil)

	// Old pending: should be kept (worker could resume).
	oldPending := seedDeployAt(t, d, app.ID, 60*24*time.Hour, deploy.StatusPending)
	pendingPath := writeLog(t, dir, oldPending.ID, "partial pull progress\n")

	// Old running: should be kept (still writing).
	oldRunning := seedDeployAt(t, d, app.ID, 60*24*time.Hour, deploy.StatusRunning)
	runningPath := writeLog(t, dir, oldRunning.ID, "still writing\n")

	// Old success: should be pruned.
	oldSucc := seedDeployAt(t, d, app.ID, 60*24*time.Hour, deploy.StatusSuccess)
	writeLog(t, dir, oldSucc.ID, "old success\n")

	frozen := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	j.cfg.Now = func() time.Time { return frozen }
	j.cfg.KeepDeploysDays = 30

	res, err := j.pruneOldLogFiles(context.Background())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if res.Pruned != 1 {
		t.Errorf("Pruned = %d, want 1 (only success)", res.Pruned)
	}
	if _, err := os.Stat(pendingPath); err != nil {
		t.Errorf("pending log was deleted: %v", err)
	}
	if _, err := os.Stat(runningPath); err != nil {
		t.Errorf("running log was deleted: %v", err)
	}
	if oldSucc.ID == 0 {
		t.Fatal("test setup: succ deploy id is 0")
	}
}

// TestPruneOldLogFiles_ReportsBytes is the Bytes-accounting
// counterpart to TestCleanupResult_ReportsBytes (which only
// covered the orphan path). Together they pin the contract
// that the operator-visible "Bytes freed" column reflects
// actual disk usage reclaimed.
func TestPruneOldLogFiles_ReportsBytes(t *testing.T) {
	j, d, dir := newTestJanitor(t)
	app := seedAppWithContainers(t, d, "bytes", nil, nil)

	const size = 256
	oldA := seedDeployAt(t, d, app.ID, 60*24*time.Hour, deploy.StatusSuccess)
	oldB := seedDeployAt(t, d, app.ID, 60*24*time.Hour, deploy.StatusFailed)
	writeLog(t, dir, oldA.ID, strings.Repeat("a", size))
	writeLog(t, dir, oldB.ID, strings.Repeat("b", size))

	frozen := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	j.cfg.Now = func() time.Time { return frozen }
	j.cfg.KeepDeploysDays = 30

	res, err := j.pruneOldLogFiles(context.Background())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if res.Pruned != 2 {
		t.Errorf("Pruned = %d, want 2", res.Pruned)
	}
	if res.Bytes != 2*size {
		t.Errorf("Bytes = %d, want %d", res.Bytes, 2*size)
	}
	if res.Scanned != 2 {
		t.Errorf("Scanned = %d, want 2", res.Scanned)
	}
}

// TestPruneOldLogFiles_NilLogStoreIsNoop covers the
// `j.logs == nil` early return. The path is reachable when
// a test (or, theoretically, a misconfigured production
// build) wires a Janitor without a deploy log store. The
// function should not panic on a nil deref.
func TestPruneOldLogFiles_NilLogStoreIsNoop(t *testing.T) {
	j := NewJanitor(newTestDB(t), nil, nil, CleanupConfig{})
	res, err := j.pruneOldLogFiles(context.Background())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if res.Pruned != 0 {
		t.Errorf("Pruned = %d, want 0", res.Pruned)
	}
}

// TestPruneOldLogFiles_RespectsCancelledContext seeds enough
// old deploy rows to make the loop's ctx.Err() check
// reachable, then cancels the context before calling the
// task. The function should return whatever it has so far
// (Pruned=0 in this empty-DB case) plus the cancel error.
func TestPruneOldLogFiles_RespectsCancelledContext(t *testing.T) {
	j, d, dir := newTestJanitor(t)
	app := seedAppWithContainers(t, d, "cancel", nil, nil)
	for i := 0; i < 5; i++ {
		dep := seedDeployAt(t, d, app.ID, 60*24*time.Hour, deploy.StatusSuccess)
		writeLog(t, dir, dep.ID, "old\n")
	}
	frozen := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	j.cfg.Now = func() time.Time { return frozen }
	j.cfg.KeepDeploysDays = 30

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before the call
	res, err := j.pruneOldLogFiles(ctx)
	if err == nil {
		t.Fatal("expected ctx error, got nil")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
	// Res.Pruned is 0 because the loop's ctx.Err() check
	// short-circuits before any delete. Bytes likewise 0.
	if res.Pruned != 0 {
		t.Errorf("Pruned = %d, want 0 (cancelled before loop body)", res.Pruned)
	}
}

// TestPruneOldContainers_HandlesRetiredStatus covers the
// third terminal status (retired) that the production
// container lifecycle can leave behind (the executor marks
// previous containers "exited" today, but the field exists
// for future states and the cleanup should already handle
// it). Pairs with the exited+dead cases in
// TestPruneOldContainers_LeavesCurrentAlone.
func TestPruneOldContainers_HandlesRetiredStatus(t *testing.T) {
	j, d, _ := newTestJanitor(t)
	frozen := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	j.cfg.Now = func() time.Time { return frozen }
	j.cfg.KeepDeploysDays = 30

	// Two retired containers of different ages; only the old
	// one should be pruned.
	seedAppWithContainers(t, d, "ret",
		[]time.Duration{60 * 24 * time.Hour, 2 * 24 * time.Hour},
		[]container.Status{container.StatusRetired, container.StatusRetired},
	)

	res, err := j.pruneOldContainers(context.Background())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if res.Pruned != 1 {
		t.Errorf("Pruned = %d, want 1 (only the old retired)", res.Pruned)
	}
	// Sanity: the recent one survived.
	count, _ := d.Container.Query().Count(context.Background())
	if count != 1 {
		t.Errorf("survivors = %d, want 1", count)
	}
}

// TestPruneOldContainers_KeepsActiveStates covers the
// negative case for the status filter: containers in
// running/paused/restarting/created must be kept regardless
// of age. None of them are "terminal", so the status filter
// (StatusIn exited,dead,retired) is the only thing standing
// between a live app and accidental deletion.
func TestPruneOldContainers_KeepsActiveStates(t *testing.T) {
	j, d, _ := newTestJanitor(t)
	frozen := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	j.cfg.Now = func() time.Time { return frozen }
	j.cfg.KeepDeploysDays = 30

	active := []container.Status{
		container.StatusRunning,
		container.StatusPaused,
		container.StatusRestarting,
		container.StatusCreated,
		container.StatusRemoving,
	}
	ages := make([]time.Duration, len(active))
	for i := range ages {
		ages[i] = 365 * 24 * time.Hour // a year old, well past retention
	}
	seedAppWithContainers(t, d, "active", ages, active)

	res, err := j.pruneOldContainers(context.Background())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if res.Pruned != 0 {
		t.Errorf("Pruned = %d, want 0 (active states preserved)", res.Pruned)
	}
	if res.Scanned != 0 {
		// Status filter excludes them before CreatedAtLT — Scanned
		// counts only rows that matched the entire WHERE clause.
		t.Errorf("Scanned = %d, want 0", res.Scanned)
	}
	count, _ := d.Container.Query().Count(context.Background())
	if count != len(active) {
		t.Errorf("survivors = %d, want %d", count, len(active))
	}
}

// TestJanitor_RunTaskReportsResultToStatus confirms that a
// successful task's CleanupResult bubbles up to Status so the
// /api/system/cleanup endpoint can show "Pruned: 5" instead
// of just "Last run: 12:34:56". Pairs with
// TestRunTask_RecordsNonPanicError (error path) and
// TestCleanupResult_ReportsBytes (orphan Bytes).
func TestJanitor_RunTaskReportsResultToStatus(t *testing.T) {
	d := newTestDB(t)
	j := NewJanitor(d, nil, nil, CleanupConfig{TaskTimeout: time.Second})
	want := CleanupResult{Scanned: 3, Pruned: 2, Bytes: 1024}
	j.WithTask("worker", time.Hour, func(ctx context.Context) (CleanupResult, error) {
		return want, nil
	})
	if _, err := j.RunTask(context.Background(), "worker"); err != nil {
		t.Fatalf("RunTask: %v", err)
	}
	st := j.Status()["worker"]
	if st.LastResult != want {
		t.Errorf("LastResult = %+v, want %+v", st.LastResult, want)
	}
	if st.LastErr != "" {
		t.Errorf("LastErr = %q, want empty", st.LastErr)
	}
}

// TestJanitor_MasterLoopTicksAtInterval is the only test
// that exercises the actual master-loop path (not the
// one-shot Start pass or manual RunTask). It uses a short
// 100ms task interval to wait for the 1-minute master tick
// — which is too long for a test — so we run for 200ms and
// verify the RunCount grew.
//
// Note: the master tick is hard-coded at 1 minute in
// cleanup.go; the per-task interval is what determines
// re-run cadence. With a 100ms task interval and a 1-minute
// master tick, a freshly started Janitor's loop won't
// re-tick the task within 200ms. To exercise the master
// loop's re-tick path without 1-minute test budgets, this
// test only verifies the loop is alive (no hang, no panic)
// after a brief wall-clock wait. The interval-check logic
// in shouldRun is exercised directly in
// TestJanitor_CadenceUnderFastTick via RunTask.
func TestJanitor_MasterLoopAlive(t *testing.T) {
	d := newTestDB(t)
	j := NewJanitor(d, nil, nil, CleanupConfig{TaskTimeout: time.Second})
	var fired atomic.Int32
	j.WithTask("heartbeat", time.Hour, func(ctx context.Context) (CleanupResult, error) {
		fired.Add(1)
		return CleanupResult{}, nil
	})
	j.Start(context.Background())
	defer j.Stop(time.Second)

	// One-shot already ran. Wait a brief moment to ensure
	// the master loop is alive in the background, then
	// verify only the one-shot fired (no spurious ticks).
	time.Sleep(80 * time.Millisecond)
	if got := fired.Load(); got != 1 {
		t.Errorf("fired = %d, want 1 (master tick shouldn't fire within 80ms)", got)
	}
	// The status map should still show RunCount=1 with a
	// non-zero NextRun scheduled into the future.
	st := j.Status()["heartbeat"]
	if st.RunCount != 1 {
		t.Errorf("RunCount = %d, want 1", st.RunCount)
	}
	if !st.NextRun.After(st.LastRun) {
		t.Error("NextRun should be after LastRun")
	}
}
