package caddy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteAtomic_CreatesDirAndFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "nested", "Caddyfile")

	if err := WriteAtomic(target, "hello\n"); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello\n" {
		t.Errorf("content = %q, want %q", got, "hello\n")
	}
}

func TestWriteAtomic_OverwritesExisting(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "Caddyfile")
	if err := os.WriteFile(target, []byte("old\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := WriteAtomic(target, "new\n"); err != nil {
		t.Fatalf("WriteAtomic: %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != "new\n" {
		t.Errorf("content = %q, want %q", got, "new\n")
	}
}

func TestWriteAtomic_CleansUpTempOnError(t *testing.T) {
	dir := t.TempDir()
	// Make the target path a directory so the final rename fails.
	target := filepath.Join(dir, "Caddyfile")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatalf("setup: %v", err)
	}

	err := WriteAtomic(target, "x")
	if err == nil {
		t.Fatalf("expected error renaming onto a directory")
	}

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".Caddyfile.") && strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}
