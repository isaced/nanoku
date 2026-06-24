package api

import (
	"context"
	"strings"
	"testing"

	"github.com/isaced/nanoku/internal/db"
)

func newUsersDB(t *testing.T) *db.DB {
	t.Helper()
	return newTestDB(t)
}

// TestSeedFirstAdmin_FreshDBMissingCreds is the regression guard: a fresh DB
// with no NANOKU_ADMIN_PASSWORD must not boot into an unloginable state. The
// call returns an error that main.go turns into a fatal exit.
func TestSeedFirstAdmin_FreshDBMissingCreds(t *testing.T) {
	cases := []struct {
		name     string
		username string
		password string
	}{
		{"both empty", "", ""},
		{"password empty", "admin", ""},
		{"username empty", "", "secret"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := newUsersDB(t)
			err := SeedFirstAdmin(context.Background(), d, tc.username, tc.password)
			if err == nil {
				t.Fatal("expected error on fresh DB with missing creds, got nil")
			}
			if !strings.Contains(err.Error(), "NANOKU_ADMIN") {
				t.Errorf("error %q should mention NANOKU_ADMIN_* env vars", err)
			}
			// Confirm nothing was seeded.
			n, _ := d.User.Query().Count(context.Background())
			if n != 0 {
				t.Errorf("no user should have been seeded, found %d", n)
			}
		})
	}
}

// TestSeedFirstAdmin_FreshDBSeedsAdmin confirms the happy path still works.
func TestSeedFirstAdmin_FreshDBSeedsAdmin(t *testing.T) {
	d := newUsersDB(t)
	if err := SeedFirstAdmin(context.Background(), d, "admin", "supersecret"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	u, err := d.User.Query().Only(context.Background())
	if err != nil {
		t.Fatalf("query user: %v", err)
	}
	if u.Username != "admin" || u.Role != "admin" {
		t.Errorf("seeded user = %+v, want admin/admin", u)
	}
	if u.PasswordHash == "" || u.PasswordHash == "supersecret" {
		t.Errorf("password not hashed: %q", u.PasswordHash)
	}
}

// TestSeedFirstAdmin_NoopOnceUserExists confirms env vars are ignored on
// subsequent boots — a deployed instance is never re-seeded / overwritten.
func TestSeedFirstAdmin_NoopOnceUserExists(t *testing.T) {
	d := newUsersDB(t)
	// First boot seeds the real admin.
	if err := SeedFirstAdmin(context.Background(), d, "realadmin", "realpass"); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	// Second boot with different (or empty) env vars must not touch the row,
	// even though the password env is now empty.
	if err := SeedFirstAdmin(context.Background(), d, "", ""); err != nil {
		t.Fatalf("second boot should be a no-op, got: %v", err)
	}
	u, err := d.User.Query().Only(context.Background())
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if u.Username != "realadmin" {
		t.Errorf("user overwritten to %q, want realadmin", u.Username)
	}
}

// TestSeedFirstAdmin_DBErrorPropagates confirms a DB failure surfaces
// (here via a cancelled context, so the Exist query errors out).
func TestSeedFirstAdmin_DBErrorPropagates(t *testing.T) {
	d := newUsersDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := SeedFirstAdmin(ctx, d, "admin", "x")
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}
