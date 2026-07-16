package api

// cleanup.go owns the background Janitor: a single ticker that
// drives a fixed set of periodic cleanup tasks. main.go constructs
// and starts it at boot and stops it on shutdown. Each task is a
// self-contained function with a known signature
// (func(context.Context) (CleanupResult, error)) so adding a new
// one is a single registration call.
//
// Why a Janitor instead of ad-hoc goroutines:
//
//   - One ticker for all tasks. A separate goroutine per task
//     means N timers all waking up at slightly different times
//     and each holding its own goroutine stack — wasteful and
//     harder to reason about under shutdown.
//   - Per-task panic isolation. A bug in one cleanup path (e.g. a
//     malformed log file name) must not crash the whole process.
//     The Janitor recovers, logs the panic, and continues ticking
//     the next interval.
//   - A unified status surface. Every task records its last-run
//     time / error / counts in a single map so an operator can
//     inspect health without log-grepping. Today nothing reads
//     this map (a /api/system/cleanup endpoint would), but the
//     accounting is free and useful for tests.
//   - Deterministic shutdown. main.go calls Stop(gracefulWindow)
//     after the HTTP server drains; the Janitor's loop exits
//     cleanly without orphan goroutines.
//
// Tasks (all are best-effort; a failure in one is logged and the
// rest keep ticking):
//
//   - purge-expired-sessions   delete session rows past their
//                              absolute expiry
//   - purge-stale-attempts     drop the in-memory per-IP login
//                              attempt window for IPs that haven't
//                              tried in the last minute
//   - prune-orphan-log-files   delete <id>.log files for deploy
//                              rows that no longer exist
//   - prune-old-log-files      delete log files for deploys older
//                              than KeepDeploysDays (deploy row
//                              itself is kept as history)
//   - prune-old-containers     delete Container rows in terminal
//                              states (exited/dead/retired) older
//                              than KeepDeploysDays that aren't
//                              the current container of any app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/db/app"
	"github.com/isaced/nanoku/internal/db/container"
	"github.com/isaced/nanoku/internal/db/deploy"
)

// CleanupResult is what a task returns so the Janitor can report
// what the last run did. Scanned / Pruned / Bytes are independent
// so a "log file" task that cares about disk can report Bytes
// while a "DB row" task reports just a row count.
type CleanupResult struct {
	// Scanned is the number of candidates the task considered
	// (files enumerated, rows matched by a query, etc).
	Scanned int
	// Pruned is the number of items actually removed. For tasks
	// that do a side effect other than deletion (e.g. session
	// purges), Pruned may be Scanned when every candidate was
	// removed.
	Pruned int
	// Bytes is the on-disk size reclaimed (0 for DB-only tasks).
	// A negative number is reserved for "freed" tasks that
	// generate data; we don't have any of those today.
	Bytes int64
}

// janitorTask is one registered cleanup job. The interval is the
// minimum gap between two runs; the Janitor checks LastRun on
// each master tick and runs any task whose interval has elapsed.
// A zero interval means "run once on Start, then never again" —
// useful for one-shot cleanups at boot.
type janitorTask struct {
	name     string
	interval time.Duration
	run      func(context.Context) (CleanupResult, error)
}

// CleanupConfig tunes the Janitor. Every field has a sensible
// default applied by NewJanitor; callers normally only override
// KeepDeploysDays. The per-task intervals are intentionally
// long (hours) because each task is cheap to run but we'd rather
// not thrash the DB on a busy minute.
type CleanupConfig struct {
	// KeepDeploysDays is the age threshold for pruning deploy
	// log files and old Container rows. Default 30.
	KeepDeploysDays int

	// PurgeExpiredSessionsEvery defaults to 1h. Zero disables.
	PurgeExpiredSessionsEvery time.Duration
	// PurgeStaleAttemptsEvery defaults to 5m. Zero disables.
	PurgeStaleAttemptsEvery time.Duration
	// PruneOrphanLogFilesEvery defaults to 1h. Zero disables.
	PruneOrphanLogFilesEvery time.Duration
	// PruneOldLogFilesEvery defaults to 6h. Zero disables.
	PruneOldLogFilesEvery time.Duration
	// PruneOldContainersEvery defaults to 24h. Zero disables.
	PruneOldContainersEvery time.Duration

	// TaskTimeout caps how long a single task can run. Default
	// 30s. The task gets its own context with this deadline so
	// a stuck query can't hold the Janitor past the next tick.
	TaskTimeout time.Duration

	// Now is injected for tests so we don't have to sleep to
	// observe retention behavior. Production callers leave it
	// nil and the Janitor uses time.Now.
	Now func() time.Time
}

func (c *CleanupConfig) applyDefaults() {
	if c.KeepDeploysDays <= 0 {
		c.KeepDeploysDays = 30
	}
	if c.PurgeExpiredSessionsEvery == 0 {
		c.PurgeExpiredSessionsEvery = time.Hour
	}
	if c.PurgeStaleAttemptsEvery == 0 {
		c.PurgeStaleAttemptsEvery = 5 * time.Minute
	}
	if c.PruneOrphanLogFilesEvery == 0 {
		c.PruneOrphanLogFilesEvery = time.Hour
	}
	if c.PruneOldLogFilesEvery == 0 {
		c.PruneOldLogFilesEvery = 6 * time.Hour
	}
	if c.PruneOldContainersEvery == 0 {
		c.PruneOldContainersEvery = 24 * time.Hour
	}
	if c.TaskTimeout == 0 {
		c.TaskTimeout = 30 * time.Second
	}
	if c.Now == nil {
		c.Now = time.Now
	}
}

// Janitor runs the registered tasks on a single master ticker.
// Construct with NewJanitor, start with Start, stop with Stop.
// The struct is safe for concurrent access — status reads can
// happen while tasks are mid-run.
type Janitor struct {
	db       *db.DB
	sessions *SessionStore
	logs     *deployLogStore
	cfg      CleanupConfig

	// customTasks is appended after the built-in tasks so tests
	// can inject their own. NewJanitor deduplicates by name.
	customTasks []janitorTask

	mu     sync.Mutex
	status map[string]taskStatus

	cancel context.CancelFunc
	done   chan struct{}
}

// taskStatus is the public status of one task. Read via Janitor.Status.
type taskStatus struct {
	LastRun    time.Time     `json:"lastRun"`
	NextRun    time.Time     `json:"nextRun"`
	LastResult CleanupResult `json:"lastResult"`
	LastErr    string        `json:"lastErr,omitempty"`
	LastErrAt  time.Time     `json:"lastErrAt,omitempty"`
	RunCount   int           `json:"runCount"`
	ErrCount   int           `json:"errCount"`
}

// NewJanitor builds a Janitor with the default task set. db,
// sessions, and logs are required; pass nil for logs to disable
// the deploy-log-file tasks. cfg is copied so later mutations
// don't change Janitor behavior.
func NewJanitor(db *db.DB, sessions *SessionStore, logs *deployLogStore, cfg CleanupConfig) *Janitor {
	cfg.applyDefaults()
	return &Janitor{
		db:       db,
		sessions: sessions,
		logs:     logs,
		cfg:      cfg,
		status:   make(map[string]taskStatus),
	}
}

// WithTask registers an additional task. Tests use this to inject
// deterministic tasks (channel-signaling, panic-injecting, etc).
// Built-in task names are reserved; re-using one is a no-op
// recorded as a panic in the test, not at runtime.
func (j *Janitor) WithTask(name string, interval time.Duration, run func(context.Context) (CleanupResult, error)) *Janitor {
	j.customTasks = append(j.customTasks, janitorTask{
		name:     name,
		interval: interval,
		run:      run,
	})
	return j
}

// Start launches the background loop in a new goroutine.
//
// The one-shot startup pass runs synchronously inside Start
// (so a caller that has just deployed an upgrade doesn't have
// to wait the first interval before existing junk is cleaned
// up), and the master loop runs in the goroutine. Stop must
// be called for clean shutdown; it blocks until the master
// loop has exited (or the graceful timeout fires).
//
// The synchronous one-shot is bounded by len(tasks)*TaskTimeout.
// In normal operation each task finishes in milliseconds; only
// pathological cases (a stuck docker call, an enormous log dir)
// would push the wall time anywhere near that bound. Tests
// rely on Start being synchronous to read Status() immediately
// after.
func (j *Janitor) Start(parent context.Context) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	j.cancel = cancel
	j.done = make(chan struct{})

	tasks := j.tasks()
	if len(tasks) == 0 {
		close(j.done)
		return
	}

	// One-shot pass. Done synchronously so callers (and tests)
	// can observe the initial state via Status() the moment Start
	// returns. A task panic is recovered inside runTask, so Start
	// itself never panics.
	for _, t := range tasks {
		if ctx.Err() != nil {
			break
		}
		j.runTask(ctx, t)
	}

	go func() {
		defer close(j.done)
		j.loop(ctx, tasks)
	}()
}

// Stop cancels the loop and waits up to gracefulTimeout for the
// goroutine to exit. If a task is mid-run when Stop is called,
// it'll be allowed to finish if it can do so within the window;
// otherwise the goroutine is abandoned and the next Start
// (e.g. after a restart) takes over. Stop is a no-op when Start
// was never called.
func (j *Janitor) Stop(gracefulTimeout time.Duration) {
	if j.cancel == nil {
		return
	}
	j.cancel()
	if j.done == nil {
		return
	}
	if gracefulTimeout <= 0 {
		<-j.done
		return
	}
	select {
	case <-j.done:
	case <-time.After(gracefulTimeout):
		log.Printf("janitor: graceful stop timed out after %s", gracefulTimeout)
	}
}

// Status returns a snapshot of every task's last-run info. The
// returned map is a copy — callers may mutate it freely.
func (j *Janitor) Status() map[string]taskStatus {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make(map[string]taskStatus, len(j.status))
	for k, v := range j.status {
		out[k] = v
	}
	return out
}

// RunTask executes a single named task once, blocking until it
// returns. Returns ErrTaskNotFound for an unknown name, the
// task's own error otherwise. Tests use this to exercise the
// task bodies without spinning up the master loop.
func (j *Janitor) RunTask(ctx context.Context, name string) (CleanupResult, error) {
	for _, t := range j.tasks() {
		if t.name == name {
			return j.runTask(ctx, t)
		}
	}
	return CleanupResult{}, fmt.Errorf("janitor: %w: %s", ErrTaskNotFound, name)
}

// ErrTaskNotFound is returned by RunTask for an unregistered name.
var ErrTaskNotFound = errors.New("task not found")

func (j *Janitor) tasks() []janitorTask {
	all := j.builtinTasks()
	seen := make(map[string]struct{}, len(all))
	for _, t := range all {
		seen[t.name] = struct{}{}
	}
	for _, t := range j.customTasks {
		if _, dup := seen[t.name]; dup {
			// Built-in name collision is a programming error,
			// not something to silently ignore. Surface it in
			// logs; tests can read the status map to assert.
			log.Printf("janitor: custom task %q shadows a built-in; ignoring", t.name)
			continue
		}
		seen[t.name] = struct{}{}
		all = append(all, t)
	}
	return all
}

func (j *Janitor) builtinTasks() []janitorTask {
	return []janitorTask{
		{
			name:     "purge-expired-sessions",
			interval: j.cfg.PurgeExpiredSessionsEvery,
			run:      j.purgeExpiredSessions,
		},
		{
			name:     "purge-stale-attempts",
			interval: j.cfg.PurgeStaleAttemptsEvery,
			run:      j.purgeStaleAttempts,
		},
		{
			name:     "prune-orphan-log-files",
			interval: j.cfg.PruneOrphanLogFilesEvery,
			run:      j.pruneOrphanLogFiles,
		},
		{
			name:     "prune-old-log-files",
			interval: j.cfg.PruneOldLogFilesEvery,
			run:      j.pruneOldLogFiles,
		},
		{
			name:     "prune-old-containers",
			interval: j.cfg.PruneOldContainersEvery,
			run:      j.pruneOldContainers,
		},
	}
}

// loop is the master tick. The one-shot startup pass is
// performed synchronously in Start, so by the time loop runs
// every task already has a non-zero LastRun. We use a 1-minute
// tick so multiple tasks with the same interval don't drift
// apart, and so a slow task doesn't multiply by N (each task
// gets one chance per minute, not per interval).
func (j *Janitor) loop(ctx context.Context, tasks []janitorTask) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			for _, t := range tasks {
				if ctx.Err() != nil {
					return
				}
				if j.shouldRun(t, now) {
					j.runTask(ctx, t)
				}
			}
		}
	}
}

func (j *Janitor) shouldRun(t janitorTask, now time.Time) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	s := j.status[t.name]
	// LastRun is always set by the time loop runs because the
	// one-shot pass in Start fills it in synchronously. If a
	// future refactor moves that, the zero-check is the
	// safety net: skip rather than run twice in a row.
	if s.LastRun.IsZero() {
		return false
	}
	return now.Sub(s.LastRun) >= t.interval
}

func (j *Janitor) runTask(ctx context.Context, t janitorTask) (CleanupResult, error) {
	timeout := j.cfg.TaskTimeout
	taskCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := j.cfg.Now()
	var result CleanupResult
	var err error

	func() {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("panic: %v", r)
			}
		}()
		result, err = t.run(taskCtx)
	}()
	end := j.cfg.Now()

	j.mu.Lock()
	defer j.mu.Unlock()
	s := j.status[t.name]
	s.LastRun = start
	s.RunCount++
	s.NextRun = start.Add(t.interval)
	if err != nil {
		s.LastErr = err.Error()
		s.LastErrAt = end
		s.ErrCount++
		log.Printf("janitor: task %q failed: %v", t.name, err)
	} else {
		s.LastResult = result
		s.LastErr = ""
	}
	j.status[t.name] = s
	return result, err
}

// ---- task bodies --------------------------------------------------------
//
// Each task is a method on Janitor so it has access to the DB /
// log store without closure plumbing. The bodies are pure: they
// take a context and return a CleanupResult, with no shared
// state other than what they read from j.

func (j *Janitor) purgeExpiredSessions(ctx context.Context) (CleanupResult, error) {
	if j.sessions == nil {
		return CleanupResult{}, nil
	}
	n, err := j.sessions.PurgeExpired(ctx)
	if err != nil {
		return CleanupResult{}, err
	}
	return CleanupResult{Pruned: n}, nil
}

func (j *Janitor) purgeStaleAttempts(_ context.Context) (CleanupResult, error) {
	if j.sessions == nil {
		return CleanupResult{}, nil
	}
	j.sessions.PurgeStaleAttempts()
	// PurgeStaleAttempts doesn't return a count, but we can
	// report the map's pre/post size delta for observability.
	// (Skipped: reading the map here would race with Login
	// holding the mutex. Operators get the same signal from
	// the next status snapshot's LastRun timestamp.)
	return CleanupResult{}, nil
}

// pruneOrphanLogFiles deletes <id>.log files whose deploy row
// no longer exists. This catches two leak classes:
//
//   - App deletion: DeleteApp removes the App row but doesn't
//     touch the per-deploy log files (deleting them is the
//     deploy's owner, not the app's). The Deploy row cascades
//     to app_id=NULL but stays around, so this task only fires
//     after the row itself is removed (rare in practice — only
//     manual SQL cleanup).
//   - Test runs and aborted boots that wrote a file but never
//     committed the Deploy row.
//
// We also skip files whose deploy is in pending/running so we
// never delete a log out from under a live deploy worker.
func (j *Janitor) pruneOrphanLogFiles(ctx context.Context) (CleanupResult, error) {
	if j.logs == nil {
		return CleanupResult{}, nil
	}
	entries, err := os.ReadDir(j.logs.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return CleanupResult{}, nil
		}
		return CleanupResult{}, fmt.Errorf("read log dir: %w", err)
	}

	var scanned, pruned int
	var bytesFreed int64
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return CleanupResult{Scanned: scanned, Pruned: pruned, Bytes: bytesFreed}, err
		}
		if e.IsDir() {
			continue
		}
		id, ok := parseDeployIDFromLogName(e.Name())
		if !ok {
			continue
		}
		scanned++
		exists, status, err := j.deployExists(ctx, id)
		if err != nil {
			return CleanupResult{Scanned: scanned, Pruned: pruned, Bytes: bytesFreed}, err
		}
		// Skip live deploys — the worker is still writing to
		// this file. Removing it would race with appendLine.
		if exists && (status == "pending" || status == "running") {
			continue
		}
		// Skip current deploys even when not in pending/running:
		// a "success" deploy with a fresh log is exactly what
		// the operator wants to see. pruneOldLogFiles handles
		// retention; this task only handles the orphan case.
		if exists {
			continue
		}
		full := filepath.Join(j.logs.dir, e.Name())
		info, statErr := os.Stat(full)
		if statErr == nil {
			bytesFreed += info.Size()
		}
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			log.Printf("janitor: remove orphan log %s: %v", full, err)
			continue
		}
		pruned++
	}
	return CleanupResult{Scanned: scanned, Pruned: pruned, Bytes: bytesFreed}, nil
}

// pruneOldLogFiles deletes the per-deploy log file for any
// deploy row older than KeepDeploysDays. The deploy row itself
// is kept as history — ListAppDeploys shows the metadata, the
// log stream endpoint returns 404 for the pruned log. This
// gives operators long-term auditability without unbounded
// disk growth.
func (j *Janitor) pruneOldLogFiles(ctx context.Context) (CleanupResult, error) {
	if j.logs == nil {
		return CleanupResult{}, nil
	}
	cutoff := j.cfg.Now().Add(-time.Duration(j.cfg.KeepDeploysDays) * 24 * time.Hour)

	deps, err := j.db.Deploy.Query().
		Where(
			deploy.CreatedAtLT(cutoff),
			deploy.StatusIn(deploy.StatusSuccess, deploy.StatusFailed),
		).
		All(ctx)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("query old deploys: %w", err)
	}

	var scanned, pruned int
	var bytesFreed int64
	for _, d := range deps {
		if err := ctx.Err(); err != nil {
			return CleanupResult{Scanned: scanned, Pruned: pruned, Bytes: bytesFreed}, err
		}
		scanned++
		full := j.logs.pathFor(d.ID)
		info, statErr := os.Stat(full)
		if statErr != nil {
			if os.IsNotExist(statErr) {
				continue // already gone
			}
			log.Printf("janitor: stat %s: %v", full, statErr)
			continue
		}
		bytesFreed += info.Size()
		if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
			log.Printf("janitor: remove old log %s: %v", full, err)
			continue
		}
		pruned++
	}
	return CleanupResult{Scanned: scanned, Pruned: pruned, Bytes: bytesFreed}, nil
}

// pruneOldContainers deletes Container rows that are clearly
// retired: terminal status (exited/dead/retired) and older than
// the retention window, and not currently the live container
// for any app. We use created_at (not stopped_at) because
// stopped_at can be NULL for some edge cases (manual row
// creation, crashed daemon). Deletion is safe because:
//
//   - Container.deploy_id FK is ON DELETE SET NULL, so the
//     linked Deploy row is left alone (its deploy_id becomes
//     NULL, which ListAppDeploys handles fine).
//   - App.current_container FK is also ON DELETE SET NULL, but
//     the not-current_container filter guarantees we never
//     delete a container any app is pointing at.
func (j *Janitor) pruneOldContainers(ctx context.Context) (CleanupResult, error) {
	if j.db == nil {
		return CleanupResult{}, nil
	}
	cutoff := j.cfg.Now().Add(-time.Duration(j.cfg.KeepDeploysDays) * 24 * time.Hour)

	// Two passes:
	//   pass 1: collect the IDs of containers currently linked
	//           as an app's current_container. We load these
	//           once instead of doing a per-row subquery.
	//   pass 2: delete-by-IDs in a single statement.
	//
	// The two-pass shape keeps the query plan small even when
	// the container table grows: the subquery version scales
	// as O(N*M); the explicit list is O(N+M).
	currentIDs, err := j.currentContainerIDs(ctx)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("list current containers: %w", err)
	}

	query := j.db.Container.Query().
		Where(
			container.StatusIn(container.StatusExited, container.StatusDead, container.StatusRetired),
			container.CreatedAtLT(cutoff),
		)
	if len(currentIDs) > 0 {
		// container.IDNotIn takes a variadic int list, which
		// becomes a long SQL IN (...) clause. The query planner
		// tolerates a handful of values; a 1000-element list
		// is a footgun. Realistic installs have at most a few
		// dozen active apps, so 100 is a comfortable cap that
		// keeps the happy path fast. Above the cap, fall back
		// to a per-row filter in Go.
		const idFilterCap = 100
		if len(currentIDs) <= idFilterCap {
			args := make([]int, 0, len(currentIDs))
			for id := range currentIDs {
				args = append(args, id)
			}
			query = query.Where(container.IDNotIn(args...))
		} else {
			return j.pruneOldContainersSlow(ctx, cutoff, currentIDs)
		}
	}
	conts, err := query.All(ctx)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("query old containers: %w", err)
	}
	return j.deleteContainers(ctx, conts)
}

// pruneOldContainersSlow is the fallback for installs with
// many active apps. Loads the candidates and filters
// current_container in Go, then deletes. The slow path is
// still O(N+M) just with an extra round-trip; the constant
// factor dominates only when N (total retired containers) is
// large.
func (j *Janitor) pruneOldContainersSlow(ctx context.Context, cutoff time.Time, currentIDs map[int]struct{}) (CleanupResult, error) {
	conts, err := j.db.Container.Query().
		Where(
			container.StatusIn(container.StatusExited, container.StatusDead, container.StatusRetired),
			container.CreatedAtLT(cutoff),
		).
		All(ctx)
	if err != nil {
		return CleanupResult{}, fmt.Errorf("query old containers (slow): %w", err)
	}
	filtered := conts[:0]
	for _, c := range conts {
		if _, isCurrent := currentIDs[c.ID]; isCurrent {
			continue
		}
		filtered = append(filtered, c)
	}
	return j.deleteContainers(ctx, filtered)
}

func (j *Janitor) deleteContainers(ctx context.Context, conts []*db.Container) (CleanupResult, error) {
	scanned := len(conts)
	pruned := 0
	for _, c := range conts {
		if err := ctx.Err(); err != nil {
			return CleanupResult{Scanned: scanned, Pruned: pruned}, err
		}
		if err := j.db.Container.DeleteOneID(c.ID).Exec(ctx); err != nil {
			log.Printf("janitor: delete container %d (%s): %v", c.ID, c.Name, err)
			continue
		}
		pruned++
	}
	return CleanupResult{Scanned: scanned, Pruned: pruned}, nil
}

func (j *Janitor) currentContainerIDs(ctx context.Context) (map[int]struct{}, error) {
	apps, err := j.db.App.Query().
		Where(app.HasCurrentContainer()).
		WithCurrentContainer().
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[int]struct{}, len(apps))
	for _, a := range apps {
		if a.Edges.CurrentContainer != nil {
			out[a.Edges.CurrentContainer.ID] = struct{}{}
		}
	}
	return out, nil
}

// deployExists returns (exists, status, err). "exists" is false
// when the row is missing; in that case status is "". Used by
// the orphan-log-file task to distinguish "deploy never existed"
// from "deploy is in progress".
func (j *Janitor) deployExists(ctx context.Context, id int) (bool, string, error) {
	d, err := j.db.Deploy.Get(ctx, id)
	if err != nil {
		if isNotFound(err) {
			return false, "", nil
		}
		return false, "", err
	}
	return true, string(d.Status), nil
}

// parseDeployIDFromLogName returns the deploy id from a file
// name like "42.log". Anything that doesn't fit the convention
// (extra extensions, hidden files, weird suffixes) is ignored
// so we never delete a non-log file by accident.
func parseDeployIDFromLogName(name string) (int, bool) {
	if name == "" || strings.HasPrefix(name, ".") {
		return 0, false
	}
	stem := strings.TrimSuffix(name, ".log")
	if stem == name {
		return 0, false
	}
	id, err := strconv.Atoi(stem)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
