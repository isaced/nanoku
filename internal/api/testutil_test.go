package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/secret"
	"golang.org/x/crypto/bcrypt"
)

// newTestDB returns a real SQLite DB on disk (TempDir, removed by the
// runtime) with all migrations applied.
//
// Using a temp file instead of an in-memory DB is deliberate. The naive
// mode=memory DSN gives each pooled connection its own empty backing store,
// so a worker goroutine that forces the pool to open a second connection
// hits "no such table". mode=memory&cache=shared looks like a cheaper fix
// but has two real-world traps (verified against modernc.org/sqlite):
//
//  1. Process-level keying: the shared in-memory DB is keyed by the DSN's
//     filename and outlives any single *sql.DB, so tests reusing the same
//     name leak state into each other (UNIQUE-constraint failures, phantom
//     rows). Isolating per-test needs a t.Name()-derived, slash-sanitized
//     DSN.
//  2. Per-connection snapshots: shared cache only syncs visibility at commit,
//     so a read on a freshly-pooled connection can still miss a table just
//     created on another connection — turning a deterministic "no such table"
//     into a timing-dependent "no rows". Debugging that is strictly worse.
//
// A throwaway temp file sidesteps both: every connection sees the same
// committed state, and t.TempDir() gives free per-test isolation.
func newTestDB(t *testing.T) *db.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.db")
	d, err := db.OpenDB(path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := d.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return d
}

// newTestSealer returns a Sealer keyed off a fixed per-package passphrase.
// Handlers must always be constructed with a non-nil Secret in tests; the
// alternative — letting handlers no-op when Secret is nil — would silently
// mask regressions that bypass encryption in production.
func newTestSealer(t *testing.T) *secret.Sealer {
	t.Helper()
	s, err := secret.NewFromPassphrase("test-passphrase-do-not-use-in-prod-1234567890")
	if err != nil {
		t.Fatalf("test sealer: %v", err)
	}
	return s
}

// TestMain lowers bcryptCost for the whole test binary. Production callers
// in users.go run at cost 12; under -race each bcrypt op at cost 12 takes
// ~4s of instrumentation, and a single login flow can run a handful of
// them. MinCost keeps the same code paths (real bcrypt format, real
// CompareHashAndPassword, real hash storage) at a cost that takes ms
// instead of seconds, so the suite drops from ~90s to ~13s with race
// detection. The dummy hash in users.go tracks bcryptCost via
// sync.OnceValue, so it stays in sync with whatever cost is active here.
//
// Honor NANOKU_KEEP_BCRYPT_COST=1 to opt out and run at production cost —
// useful if you're investigating a regression that might depend on the
// cost factor itself.
func TestMain(m *testing.M) {
	if os.Getenv("NANOKU_KEEP_BCRYPT_COST") != "1" {
		bcryptCost = bcrypt.MinCost
	}
	os.Exit(m.Run())
}
