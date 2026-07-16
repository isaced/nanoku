package config

// config_test.go covers the env-var parsing helpers. The Load
// function itself isn't tested in isolation because it pulls in
// every NANOKU_* env var and is best exercised by an
// integration test (the install.sh flow covers the realistic
// combination). The helpers below are pure functions and
// benefit from focused unit tests.

import (
	"os"
	"testing"
)

func TestParseBool(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"0", false},
		{"1", true},
		{"true", true},
		{"TRUE", true},
		{"True", true},
		{"yes", true},
		{"YES", true},
		{"on", true},
		{"off", false},
		{"garbage", false},
		{"  true  ", true},
		{"\t1\n", true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			key := "TEST_PARSE_BOOL"
			if tc.in == "" {
				_ = os.Unsetenv(key)
			} else {
				t.Setenv(key, tc.in)
			}
			if got := parseBool(key); got != tc.want {
				t.Errorf("parseBool(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestParseInt_DefaultsWhenMissingOrInvalid(t *testing.T) {
	cases := []struct {
		name     string
		set      bool
		value    string
		fallback int
		want     int
	}{
		{"unset", false, "", 30, 30},
		{"blank", true, "", 30, 30},
		{"whitespace", true, "   ", 30, 30},
		{"valid", true, "60", 30, 60},
		{"valid-padded", true, "  60  ", 30, 60},
		{"negative-falls-back", true, "-1", 30, 30},
		{"zero-falls-back", true, "0", 30, 30},
		{"garbage-falls-back", true, "abc", 30, 30},
		{"overflow-falls-back", true, "99999999999999999999", 30, 30},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key := "TEST_PARSE_INT"
			if !tc.set {
				_ = os.Unsetenv(key)
			} else {
				t.Setenv(key, tc.value)
			}
			if got := parseInt(key, tc.fallback); got != tc.want {
				t.Errorf("parseInt(%q, %d) = %d, want %d", tc.value, tc.fallback, got, tc.want)
			}
		})
	}
}

func TestGetEnv(t *testing.T) {
	t.Run("unset returns fallback", func(t *testing.T) {
		_ = os.Unsetenv("TEST_GETENV")
		if got := getEnv("TEST_GETENV", "fb"); got != "fb" {
			t.Errorf("got %q, want fallback", got)
		}
	})
	t.Run("blank returns fallback", func(t *testing.T) {
		t.Setenv("TEST_GETENV", "")
		if got := getEnv("TEST_GETENV", "fb"); got != "fb" {
			t.Errorf("got %q, want fallback", got)
		}
	})
	t.Run("set returns value", func(t *testing.T) {
		t.Setenv("TEST_GETENV", "real")
		if got := getEnv("TEST_GETENV", "fb"); got != "real" {
			t.Errorf("got %q, want %q", got, "real")
		}
	})
}
