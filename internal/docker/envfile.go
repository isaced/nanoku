package docker

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

// envKeyRe matches POSIX env var names (key=value files used by `docker run
// --env-file`). Disallows keys starting with `-` to prevent them from being
// parsed as docker flags when passed via argv.
var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidateEnvVar checks an env var pair for safe inclusion in a docker
// `--env-file`. Returns an error if the key would be misinterpreted as a
// flag, or the value contains line separators that would split the file.
func ValidateEnvVar(key, value string) error {
	if !envKeyRe.MatchString(key) {
		return fmt.Errorf("invalid env var key %q (must match %s)", key, envKeyRe)
	}
	if strings.ContainsAny(value, "\n\r\x00") {
		return fmt.Errorf("env var %q contains newline or NUL", key)
	}
	return nil
}

// WriteEnvFile writes env vars as KEY=VALUE lines to a new temp file and
// returns its path plus a cleanup func. Caller must call cleanup after the
// docker command finishes.
func WriteEnvFile(env []string) (path string, cleanup func(), err error) {
	f, err := os.CreateTemp("", "nanoku-env-*.env")
	if err != nil {
		return "", func() {}, fmt.Errorf("create env file: %w", err)
	}
	path = f.Name()
	cleanup = func() { _ = os.Remove(path) }

	for _, kv := range env {
		if _, werr := f.WriteString(kv + "\n"); werr != nil {
			f.Close()
			cleanup()
			return "", func() {}, fmt.Errorf("write env file: %w", werr)
		}
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("close env file: %w", err)
	}
	return path, cleanup, nil
}
