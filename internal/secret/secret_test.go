package secret

import (
	"strings"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	s, err := NewFromPassphrase("this-is-a-test-passphrase-1234")
	if err != nil {
		t.Fatalf("NewFromPassphrase: %v", err)
	}
	cases := []string{
		"hunter2",
		"a",
		strings.Repeat("x", 4096),
		"unicode 中文 🔐 — also encrypted",
	}
	for _, in := range cases {
		enc, err := s.EncryptString(in)
		if err != nil {
			t.Fatalf("encrypt %q: %v", in, err)
		}
		if !IsEncrypted(enc) {
			t.Errorf("encrypted value missing enc: prefix")
		}
		out, err := s.DecryptString(enc)
		if err != nil {
			t.Fatalf("decrypt: %v", err)
		}
		if out != in {
			t.Errorf("round-trip mismatch: got %q want %q", out, in)
		}
	}
}

func TestDecryptAcceptsLegacyPlaintext(t *testing.T) {
	s, _ := NewFromPassphrase("another-passphrase-xyz789")
	out, err := s.DecryptString("legacy-plaintext-row")
	if err != nil {
		t.Fatalf("legacy decrypt should not error: %v", err)
	}
	if out != "legacy-plaintext-row" {
		t.Errorf("legacy passthrough failed: got %q", out)
	}
}

func TestEncryptEmptyRejected(t *testing.T) {
	s, _ := NewFromPassphrase("valid-passphrase-abc123")
	if _, err := s.EncryptString(""); err == nil {
		t.Fatal("EncryptString(\"\") should error, got nil")
	}
}

func TestEncryptOptional(t *testing.T) {
	s, _ := NewFromPassphrase("valid-passphrase-abc123")
	if p, ok, err := s.EncryptOptional(""); err != nil || ok || p != nil {
		t.Errorf("empty: got (%v,%v,%v) want (nil,false,nil)", p, ok, err)
	}
	enc, ok, err := s.EncryptOptional("real-secret")
	if err != nil || !ok || enc == nil {
		t.Fatalf("non-empty: got (%v,%v,%v) want (non-nil,true,nil)", enc, ok, err)
	}
	if !IsEncrypted(*enc) {
		t.Errorf("EncryptedOptional produced unencrypted output: %q", *enc)
	}
}

func TestWrongKeyFailsToDecrypt(t *testing.T) {
	a, _ := NewFromPassphrase("passphrase-A-1234567890")
	b, _ := NewFromPassphrase("passphrase-B-0987654321")
	enc, err := a.EncryptString("payload")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := b.DecryptString(enc); err == nil {
		t.Fatal("DecryptString with wrong key should fail, got nil")
	}
}

func TestCorruptedCiphertextFails(t *testing.T) {
	s, _ := NewFromPassphrase("valid-passphrase-abc123")
	enc, _ := s.EncryptString("payload")
	// Flip one character inside the base64 payload.
	idx := len(encryptedPrefix) + 4
	bad := enc[:idx] + flipChar(string(enc[idx])) + enc[idx+1:]
	if _, err := s.DecryptString(bad); err == nil {
		t.Fatal("corrupted ciphertext should fail to decrypt")
	}
}

func TestShortPassphraseRejected(t *testing.T) {
	if _, err := NewFromPassphrase("short"); err == nil {
		t.Fatal("short passphrase should be rejected")
	}
}

func TestLoadFromEnvMissing(t *testing.T) {
	t.Setenv("NANOKU_TEST_SECRET_KEY", "")
	if _, err := LoadFromEnv("NANOKU_TEST_SECRET_KEY_MISSING"); err == nil {
		t.Fatal("LoadFromEnv on missing var should error")
	}
}

func TestLoadFromEnvPresent(t *testing.T) {
	t.Setenv("NANOKU_TEST_SECRET_KEY_LOAD", "an-environment-supplied-passphrase-1234567890")
	s, err := LoadFromEnv("NANOKU_TEST_SECRET_KEY_LOAD")
	if err != nil {
		t.Fatalf("LoadFromEnv: %v", err)
	}
	enc, err := s.EncryptString("env-supplied")
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	out, err := s.DecryptString(enc)
	if err != nil || out != "env-supplied" {
		t.Errorf("round-trip via env-loaded key: got (%q,%v)", out, err)
	}
}

func TestIsEncrypted(t *testing.T) {
	if IsEncrypted("") || IsEncrypted("plaintext") || IsEncrypted("enc") {
		t.Error("IsEncrypted should reject non-enc: prefixed values")
	}
	if !IsEncrypted("enc:abcd") {
		t.Error("IsEncrypted should accept enc: prefixed value")
	}
}

func flipChar(s string) string {
	if s == "A" {
		return "B"
	}
	return "A"
}