package docker

import (
	"os"
	"strings"
	"testing"
)

func TestValidateEnvVar(t *testing.T) {
	tests := []struct {
		name, key, value string
		wantErr          bool
	}{
		{"valid", "FOO", "bar", false},
		{"valid with =", "FOO", "bar=baz", false},
		{"valid with space", "FOO", "hello world", false},
		{"valid with quote", "FOO", `say "hi"`, false},
		{"valid underscore prefix", "_FOO", "x", false},
		{"valid digit body", "F00_BAR", "x", false},
		{"empty key", "", "x", true},
		{"key starts with dash", "-foo", "x", true},
		{"key starts with --", "--privileged", "x", true},
		{"key with equals", "FOO=BAR", "x", true},
		{"key with space", "FOO BAR", "x", true},
		{"key with dot", "FOO.BAR", "x", true},
		{"key starts with digit", "1FOO", "x", true},
		{"value with newline", "FOO", "line1\nline2", true},
		{"value with CR", "FOO", "line1\rline2", true},
		{"value with NUL", "FOO", "before\x00after", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateEnvVar(tc.key, tc.value)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateEnvVar(%q, %q) err = %v, wantErr = %v", tc.key, tc.value, err, tc.wantErr)
			}
		})
	}
}

func TestWriteEnvFile(t *testing.T) {
	path, cleanup, err := WriteEnvFile([]string{
		"FOO=bar",
		"BAZ=hello world",
		"EMPTY=",
	})
	if err != nil {
		t.Fatalf("WriteEnvFile: %v", err)
	}
	defer cleanup()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("env file missing: %v", err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	want := "FOO=bar\nBAZ=hello world\nEMPTY=\n"
	if string(body) != want {
		t.Errorf("content = %q, want %q", body, want)
	}

	cleanup()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("cleanup did not remove file (stat err = %v)", err)
	}
}

func TestWriteEnvFile_PermissionRestricted(t *testing.T) {
	// The temp file must not be world-readable since it may contain secrets
	// (registry passwords proxied via env vars are the common case).
	path, cleanup, err := WriteEnvFile([]string{"SECRET=x"})
	if err != nil {
		t.Fatalf("WriteEnvFile: %v", err)
	}
	defer cleanup()

	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	mode := st.Mode().Perm()
	if mode&0o077 != 0 {
		t.Errorf("env file mode = %o, want no group/other bits (secrets)", mode)
	}
}

func TestWriteEnvFile_Empty(t *testing.T) {
	path, cleanup, err := WriteEnvFile(nil)
	if err != nil {
		t.Fatalf("WriteEnvFile(nil): %v", err)
	}
	defer cleanup()
	body, _ := os.ReadFile(path)
	if len(body) != 0 {
		t.Errorf("empty env file should be empty bytes, got %q", body)
	}
}

func TestValidateEnvVar_RejectsFlagShapedKey(t *testing.T) {
	// A key like "--privileged" would, if passed via -e on argv, be parsed by
	// docker CLI as a flag rather than an env var. The validator must block
	// this even though the resulting "-e" pair would have appeared well-formed.
	for _, key := range []string{"--privileged", "-v", "-e", "--label=foo"} {
		if err := ValidateEnvVar(key, "x"); err == nil {
			t.Errorf("ValidateEnvVar(%q) should fail", key)
		}
	}
}

func TestValidateEnvVar_DoesNotCrashOnEmpty(t *testing.T) {
	// Defensive: empty value is allowed (e.g. PORT=).
	if err := ValidateEnvVar("PORT", ""); err != nil {
		t.Errorf("ValidateEnvVar(PORT, \"\") err = %v, want nil", err)
	}
	// And the strings.ContainsAny branch must not panic on the empty case.
	if err := ValidateEnvVar("X", ""); !strings.Contains("", "\n") {
		_ = err
	}
}
