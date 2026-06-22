package docker

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLogin_MissingUsername(t *testing.T) {
	m := &Manager{loginBinary: "/nonexistent"}
	err := m.Login(t.Context(), "ghcr.io", "", "secret")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "registry_username") {
		t.Errorf("error %q should mention registry_username", err)
	}
}

func TestLogin_HappyPath_PassesPasswordViaStdin(t *testing.T) {
	bin := fakeBin(t, `cat - > /dev/null
exit 0`)
	m := &Manager{loginBinary: bin}
	if err := m.Login(t.Context(), "ghcr.io", "alice", "s3cret"); err != nil {
		t.Fatalf("Login: %v", err)
	}
}

func TestLogin_FoldsStderrIntoError(t *testing.T) {
	bin := fakeBin(t, `echo 'unauthorized: bad password' 1>&2
exit 1`)
	m := &Manager{loginBinary: bin}
	err := m.Login(t.Context(), "ghcr.io", "alice", "wrong")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("error %q should contain stderr text", err)
	}
}

func TestLogin_NoRegistryArg(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "argv.log")
	bin := fakeBin(t, `printf '%s\n' "$@" > "$DOCKER_FAKE_ARGV"
exit 0`)
	t.Setenv("DOCKER_FAKE_ARGV", logPath)
	m := &Manager{loginBinary: bin}
	if err := m.Login(t.Context(), "", "alice", "secret"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	got := readLines(t, logPath)
	if containsToken(got, "ghcr.io") || containsToken(got, "docker.io") {
		t.Errorf("registry should be omitted when empty, got %v", got)
	}
	if !containsToken(got, "-u") || !containsToken(got, "alice") {
		t.Errorf("missing -u alice in argv: %v", got)
	}
}

func TestLogin_WithRegistry_AddsRegistryArg(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "argv.log")
	bin := fakeBin(t, `printf '%s\n' "$@" > "$DOCKER_FAKE_ARGV"
exit 0`)
	t.Setenv("DOCKER_FAKE_ARGV", logPath)
	m := &Manager{loginBinary: bin}
	if err := m.Login(t.Context(), "registry.example.com", "alice", "secret"); err != nil {
		t.Fatalf("Login: %v", err)
	}
	got := readLines(t, logPath)
	if !containsToken(got, "registry.example.com") {
		t.Errorf("missing registry in argv: %v", got)
	}
}

func TestLogout_HappyPath(t *testing.T) {
	bin := fakeBin(t, `exit 0`)
	m := &Manager{loginBinary: bin}
	if err := m.Logout(t.Context(), "ghcr.io"); err != nil {
		t.Fatalf("Logout: %v", err)
	}
}

func TestLogout_FoldsStderrIntoError(t *testing.T) {
	bin := fakeBin(t, `echo 'not logged in' 1>&2
exit 1`)
	m := &Manager{loginBinary: bin}
	err := m.Logout(t.Context(), "ghcr.io")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("error %q should contain stderr text", err)
	}
}

func TestLogout_NoRegistryArg(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "argv.log")
	bin := fakeBin(t, `printf '%s\n' "$@" > "$DOCKER_FAKE_ARGV"
exit 0`)
	t.Setenv("DOCKER_FAKE_ARGV", logPath)
	m := &Manager{loginBinary: bin}
	if err := m.Logout(t.Context(), ""); err != nil {
		t.Fatalf("Logout: %v", err)
	}
	got := readLines(t, logPath)
	if !containsToken(got, "logout") {
		t.Errorf("expected logout subcommand in argv: %v", got)
	}
}

func TestWithRegistry_NoCreds_SkipsLogin(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "argv.log")
	bin := fakeBin(t, `printf '%s\n' "$@" > "$DOCKER_FAKE_ARGV"
exit 0`)
	t.Setenv("DOCKER_FAKE_ARGV", logPath)
	m := &Manager{loginBinary: bin}

	fnCalled := false
	fn := func() error { fnCalled = true; return nil }
	if err := m.WithRegistry(t.Context(), "ghcr.io", "", "", fn); err != nil {
		t.Fatalf("WithRegistry: %v", err)
	}
	if !fnCalled {
		t.Error("fn should have been called")
	}
	if _, err := os.Stat(logPath); err == nil {
		t.Errorf("login should not have been called; argv log exists")
	}
}

func TestWithRegistry_WithCreds_LogsInAndOut(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "argv.log")
	bin := fakeBin(t, `printf '%s\n' "$@" >> "$DOCKER_FAKE_ARGV"
exit 0`)
	t.Setenv("DOCKER_FAKE_ARGV", logPath)
	m := &Manager{loginBinary: bin}

	fnCalled := false
	fn := func() error { fnCalled = true; return nil }
	if err := m.WithRegistry(t.Context(), "ghcr.io", "alice", "secret", fn); err != nil {
		t.Fatalf("WithRegistry: %v", err)
	}
	if !fnCalled {
		t.Error("fn should have been called")
	}
	got := readLines(t, logPath)
	if !containsToken(got, "login") {
		t.Errorf("expected login invocation: %v", got)
	}
	if !containsToken(got, "logout") {
		t.Errorf("expected logout after fn: %v", got)
	}
}

func TestWithRegistry_DefaultRegistry_SkipsLogout(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "argv.log")
	bin := fakeBin(t, `printf '%s\n' "$@" >> "$DOCKER_FAKE_ARGV"
exit 0`)
	t.Setenv("DOCKER_FAKE_ARGV", logPath)
	m := &Manager{loginBinary: bin}

	if err := m.WithRegistry(t.Context(), "", "alice", "secret", func() error { return nil }); err != nil {
		t.Fatalf("WithRegistry: %v", err)
	}
	got := readLines(t, logPath)
	if !containsToken(got, "login") {
		t.Errorf("expected login: %v", got)
	}
	if containsToken(got, "logout") {
		t.Errorf("logout must be skipped when registry is empty (docker hub): %v", got)
	}
}

func TestWithRegistry_LoginFails_SkipsFn(t *testing.T) {
	bin := fakeBin(t, `echo 'unauthorized' 1>&2
exit 1`)
	m := &Manager{loginBinary: bin}

	fnCalled := false
	want := errors.New("login failed")
	fn := func() error {
		fnCalled = true
		return want
	}
	err := m.WithRegistry(t.Context(), "ghcr.io", "alice", "wrong", fn)
	if fnCalled {
		t.Error("fn should not have been called when login fails")
	}
	if err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("expected login error, got %v", err)
	}
}

func TestWithRegistry_FnError_DoesNotSuppress(t *testing.T) {
	bin := fakeBin(t, `exit 0`)
	m := &Manager{loginBinary: bin}

	want := errors.New("deploy boom")
	err := m.WithRegistry(t.Context(), "ghcr.io", "alice", "secret", func() error { return want })
	if !errors.Is(err, want) {
		t.Errorf("got err %v, want wraps %v", err, want)
	}
}