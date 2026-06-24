// Package secret provides AES-256-GCM authenticated encryption for sensitive
// values stored in SQLite (registry passwords, trigger tokens, env var
// values). The cipher key is derived from NANOKU_SECRET_KEY at startup; the
// process refuses to boot without it.
//
// Wire format for ciphertext values: "enc:" + base64(nonce || ct || tag).
// The "enc:" prefix lets DecryptString distinguish freshly encrypted rows
// from legacy plaintext rows left by older builds, so a one-shot startup
// migration can re-encrypt in place without breaking either side.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	// encryptedPrefix is the on-disk marker for AES-GCM-encrypted values.
	// Plaintext rows (pre-encryption builds) lack this prefix; the migration
	// routine in MigrateDatabase rewrites them to encrypted form.
	encryptedPrefix = "enc:"

	// gcmNonceSize is the standard 96-bit nonce for AES-GCM. Anything other
	// than 12 bytes would mean either a corrupted row or an external writer;
	// either way, refuse to decrypt.
	gcmNonceSize = 12

	// minKeyMaterial is the lower bound on the NANOKU_SECRET_KEY input after
	// trimming. We don't enforce a fixed length because the user can pass
	// any passphrase; the bytes are SHA-256'd down to a 32-byte AES key.
	// 16 chars of minimum entropy keeps the wrap path from being a footgun
	// ("hunter2"-style typos).
	minKeyMaterial = 16
)

// Sealer wraps an authenticated AES-GCM cipher. Methods are safe for
// concurrent use — cipher.AEAD is goroutine-safe for Seal/Open.
type Sealer struct {
	gcm cipher.AEAD
}

// LoadFromEnv reads the secret key from the named env var, derives a 32-byte
// AES key, and returns a ready Sealer. Returns an error if the variable is
// unset, too short, or the cipher can't be constructed.
func LoadFromEnv(envVar string) (*Sealer, error) {
	raw, ok := os.LookupEnv(envVar)
	if !ok || strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("%s is required (generate with: openssl rand -base64 32)", envVar)
	}
	return NewFromPassphrase(strings.TrimSpace(raw))
}

// NewFromPassphrase derives a 32-byte AES key from any-length input by
// hashing with SHA-256. Accepting a passphrase (not just raw 32 bytes) makes
// ops slightly more forgiving without weakening the cipher materially:
// SHA-256(passphrase) is the key. The input length is still bounded below
// to keep empty / typo values from looking like valid keys.
func NewFromPassphrase(passphrase string) (*Sealer, error) {
	if len(passphrase) < minKeyMaterial {
		return nil, fmt.Errorf("secret key must be at least %d characters", minKeyMaterial)
	}
	sum := sha256.Sum256([]byte(passphrase))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, fmt.Errorf("aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	return &Sealer{gcm: gcm}, nil
}

// EncryptString seals plaintext with a fresh random nonce and returns the
// canonical "enc:<base64>" form. Empty plaintext is rejected — callers
// should not bother encrypting empty values, and silently returning "" would
// let a stray ClearRegistry path leak as if it had encrypted a password.
func (s *Sealer) EncryptString(plaintext string) (string, error) {
	if plaintext == "" {
		return "", errors.New("EncryptString: empty plaintext")
	}
	nonce := make([]byte, gcmNonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("read nonce: %w", err)
	}
	sealed := s.gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return encryptedPrefix + base64.StdEncoding.EncodeToString(sealed), nil
}

// DecryptString returns the plaintext for an "enc:<base64>" value, or
// (plaintext, plaintext) when the input lacks the encrypted prefix.
//
// The plaintext fallback exists for one reason only: the startup migration
// in MigrateDatabase reads rows that may still be in the legacy form and
// needs to round-trip them without crashing. New code paths should never
// observe a plaintext row — by the time handlers are serving traffic, the
// migration has finished.
func (s *Sealer) DecryptString(value string) (string, error) {
	if !strings.HasPrefix(value, encryptedPrefix) {
		return value, nil
	}
	raw, err := base64.StdEncoding.DecodeString(value[len(encryptedPrefix):])
	if err != nil {
		return "", fmt.Errorf("decode base64: %w", err)
	}
	if len(raw) < gcmNonceSize+s.gcm.Overhead() {
		return "", fmt.Errorf("ciphertext too short: %d bytes", len(raw))
	}
	nonce, ct := raw[:gcmNonceSize], raw[gcmNonceSize:]
	plain, err := s.gcm.Open(nil, nonce, ct, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt: %w", err)
	}
	return string(plain), nil
}

// IsEncrypted reports whether value carries the on-disk encrypted marker.
// Used by the startup migration to find plaintext rows to rewrite.
func IsEncrypted(value string) bool {
	return strings.HasPrefix(value, encryptedPrefix)
}

// EncryptOptional is a convenience for handler code: encrypts value when
// non-empty, returns nil without touching the DB when empty. The bool
// returned indicates whether there was something to encrypt, so callers can
// distinguish "skip the column update" from "set the column to empty".
//
// Encryption errors are returned verbatim — there's no safe fallback when
// AES-GCM itself fails, and surfacing it lets the handler 500 the request
// rather than silently storing plaintext.
func (s *Sealer) EncryptOptional(value string) (*string, bool, error) {
	if value == "" {
		return nil, false, nil
	}
	enc, err := s.EncryptString(value)
	if err != nil {
		return nil, false, err
	}
	return &enc, true, nil
}