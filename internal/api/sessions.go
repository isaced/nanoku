package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/isaced/nanoku/internal/db"
	"github.com/isaced/nanoku/internal/db/session"
)

const (
	sessionCookieName = "nanoku_session"
	sessionTTL        = 7 * 24 * time.Hour
	tokenBytes        = 32
)

// AuthedUser is the identity attached to an authenticated request.
type AuthedUser struct {
	ID       int
	Username string
	Role     string
}

type SessionStore struct {
	DB *db.DB

	// in-memory rate limiter for /api/login: sliding 1-min window, keyed by client IP.
	mu       sync.Mutex
	attempts map[string][]time.Time
}

func NewSessionStore(d *db.DB) *SessionStore {
	return &SessionStore{DB: d, attempts: make(map[string][]time.Time)}
}

func newToken() (raw string, hashHex string, err error) {
	b := make([]byte, tokenBytes)
	if _, err = rand.Read(b); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	sum := sha256.Sum256([]byte(raw))
	hashHex = hex.EncodeToString(sum[:])
	return raw, hashHex, nil
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// Create issues a new session for user and returns the raw token (cookie value).
func (s *SessionStore) Create(ctx context.Context, u *db.User) (string, error) {
	raw, hashHex, err := newToken()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	_, err = s.DB.Session.Create().
		SetTokenHash(hashHex).
		SetCreatedAt(now).
		SetExpiresAt(now.Add(sessionTTL)).
		SetLastSeen(now).
		SetUserID(u.ID).
		Save(ctx)
	if err != nil {
		return "", err
	}
	return raw, nil
}

// Lookup returns the user attached to a raw token if the session is valid.
// Expired sessions are deleted as a side effect.
func (s *SessionStore) Lookup(ctx context.Context, raw string) (*db.User, error) {
	if raw == "" {
		return nil, db.ErrNotFound
	}
	hash := hashToken(raw)
	sess, err := s.DB.Session.Query().
		Where(session.TokenHashEQ(hash)).
		WithUser().
		Only(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	if !now.Before(sess.ExpiresAt) {
		_ = s.DB.Session.DeleteOneID(sess.ID).Exec(ctx)
		return nil, db.ErrNotFound
	}
	u := sess.Edges.User
	if u == nil {
		return nil, db.ErrNotFound
	}
	// sliding renewal: extend expires_at and last_seen
	newExp := now.Add(sessionTTL)
	if _, err := s.DB.Session.UpdateOneID(sess.ID).
		SetExpiresAt(newExp).
		SetLastSeen(now).
		Save(ctx); err != nil {
		return nil, err
	}
	return u, nil
}

// Delete removes the session identified by raw token. Missing token is not an error.
func (s *SessionStore) Delete(ctx context.Context, raw string) error {
	if raw == "" {
		return nil
	}
	hash := hashToken(raw)
	_, err := s.DB.Session.Delete().Where(session.TokenHashEQ(hash)).Exec(ctx)
	if err != nil && !db.IsNotFound(err) && !errors.Is(err, db.ErrNotFound) {
		// ent returns &NotFoundError on zero rows; both are fine.
		return err
	}
	return nil
}

// PurgeExpired deletes sessions past their expiry. Safe to call periodically.
func (s *SessionStore) PurgeExpired(ctx context.Context) (int, error) {
	now := time.Now().UTC()
	n, err := s.DB.Session.Delete().Where(session.ExpiresAtLT(now)).Exec(ctx)
	return n, err
}

// AllowLogin returns false if the IP has exceeded the rate limit (5 / minute).
func (s *SessionStore) AllowLogin(ip string) bool {
	const limit = 5
	cutoff := time.Now().Add(-time.Minute)
	s.mu.Lock()
	defer s.mu.Unlock()
	log := s.attempts[ip]
	kept := log[:0]
	for _, t := range log {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= limit {
		s.attempts[ip] = kept
		return false
	}
	kept = append(kept, time.Now())
	s.attempts[ip] = kept
	return true
}