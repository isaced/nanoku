package api

import (
	"context"
	"errors"
	"time"

	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/db/user"
	"golang.org/x/crypto/bcrypt"
)

const bcryptCost = 12

var ErrInvalidCredentials = errors.New("invalid credentials")

// HashPassword returns a bcrypt hash suitable for storage in users.password_hash.
func HashPassword(plain string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(plain), bcryptCost)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// UserByUsername fetches a user row. Returns db.ErrNotFound if not present.
func UserByUsername(ctx context.Context, d *db.DB, username string) (*db.User, error) {
	u, err := d.User.Query().Where(user.Username(username)).Only(ctx)
	if err != nil {
		if db.IsNotFound(err) {
			return nil, db.ErrNotFound
		}
		return nil, err
	}
	return u, nil
}

// Authenticate verifies (username, password) against the users table.
// On success it returns the user and updates last_login_at.
// ErrInvalidCredentials is returned for both unknown user and bad password
// (caller must not distinguish, to avoid user enumeration).
func Authenticate(ctx context.Context, d *db.DB, username, password string) (*db.User, error) {
	u, err := UserByUsername(ctx, d, username)
	if err != nil {
		// still run a dummy bcrypt to keep timing similar for known/unknown users
		_ = bcrypt.CompareHashAndPassword(
			[]byte("$2a$12$abcdefghijklmnopqrstuvCqXcQqJZP9Vf3QvFnq6YhkQ3W2oYT6He"),
			[]byte(password),
		)
		return nil, ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)); err != nil {
		return nil, ErrInvalidCredentials
	}
	now := time.Now().UTC()
	if _, err := d.User.UpdateOneID(u.ID).SetLastLoginAt(now).Save(ctx); err != nil {
		return nil, err
	}
	u.LastLoginAt = &now
	return u, nil
}

// SeedFirstAdmin creates an initial admin user from the env-supplied
// username and password. Returns nil if a user already exists.
//
// Bootstrapping from env vars is a first-boot-only affordance: once any user
// row exists the env vars are ignored so a deployed instance is never locked
// to the env credentials. On a fresh DB with missing username or password we
// fail fast — otherwise nanoku would boot into a state where nobody can log
// in (no seeded admin, no way to create one short of editing the DB).
func SeedFirstAdmin(ctx context.Context, d *db.DB, username, password string) error {
	exists, err := d.User.Query().Limit(1).Exist(ctx)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if username == "" || password == "" {
		return errors.New("fresh database but NANOKU_ADMIN_USER/NANOKU_ADMIN_PASSWORD not set; cannot bootstrap admin (set them once, then unset after first login)")
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	_, err = d.User.Create().
		SetUsername(username).
		SetPasswordHash(hash).
		SetRole("admin").
		Save(ctx)
	return err
}
