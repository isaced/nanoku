package config

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Listen           string
	DBPath           string
	CaddyfilePath    string
	CaddyMode        string // v1 only: "managed"
	CaddyImage       string
	CaddyContainer   string
	CaddyVolumeName  string
	CaddyNetworkName string
	ACMEEmail        string
	AdminUser        string
	AdminPassword    string
	DockerHost       string
	SkipCaddyReload  bool
	SelfContainer    string // nanoku's own container name (for log viewing); empty if not containerized
	ComposeBaseDir   string // where nanoku stores generated docker-compose.yml files
	// TrustProxy makes clientIP honor X-Forwarded-For (leftmost hop).
	// Only enable when nanoku sits behind a reverse proxy that sanitizes
	// the header; otherwise an attacker can spoof IPs to bypass the
	// /api/login rate limit.
	TrustProxy bool
}

func Load() (*Config, error) {
	c := &Config{
	Listen:           getEnv("NANOKU_LISTEN", ":8080"),
	DBPath:           getEnv("NANOKU_DB", "./nanoku.db"),
	CaddyfilePath:    getEnv("NANOKU_CADDYFILE", "./Caddyfile"),
	CaddyMode:        getEnv("NANOKU_CADDY_MODE", "managed"),
	CaddyImage:       getEnv("NANOKU_CADDY_IMAGE", "caddy:2"),
	CaddyContainer:   getEnv("NANOKU_CADDY_CONTAINER", "nanoku-caddy"),
	CaddyVolumeName:  getEnv("NANOKU_CADDY_VOLUME", "nanoku-caddy-data"),
	CaddyNetworkName: getEnv("NANOKU_CADDY_NETWORK", "nanoku-net"),
	ACMEEmail:        os.Getenv("NANOKU_ACME_EMAIL"),
	AdminUser:        getEnv("NANOKU_ADMIN_USER", "admin"),
	AdminPassword:    os.Getenv("NANOKU_ADMIN_PASSWORD"),
	DockerHost:       getEnv("DOCKER_HOST", "unix:///var/run/docker.sock"),
	SkipCaddyReload:  false,
	SelfContainer:    os.Getenv("NANOKU_SELF_CONTAINER"),
	ComposeBaseDir:   getEnv("NANOKU_COMPOSE_DIR", "./composes"),
	TrustProxy:       parseBool("NANOKU_TRUST_PROXY"),
}

	flag.StringVar(&c.Listen, "listen", c.Listen, "admin HTTP listen address")
	flag.StringVar(&c.DBPath, "db", c.DBPath, "SQLite database file path")
	flag.StringVar(&c.CaddyfilePath, "caddyfile", c.CaddyfilePath, "generated Caddyfile path (host filesystem)")
	flag.BoolVar(&c.SkipCaddyReload, "skip-caddy-reload", false, "skip Docker Caddy container management (write Caddyfile only)")
	flag.Parse()

	if c.CaddyMode != "managed" {
		return nil, fmt.Errorf("unsupported CADDY_MODE %q (v1 only supports 'managed')", c.CaddyMode)
	}

	return c, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// parseBool returns true when the named env var is set to a truthy value
// (1 / true / yes / on, case-insensitive). Defaults to false.
func parseBool(key string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}