package config

import (
	"errors"
	"flag"
	"fmt"
	"os"
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
	}

	flag.StringVar(&c.Listen, "listen", c.Listen, "admin HTTP listen address")
	flag.StringVar(&c.DBPath, "db", c.DBPath, "SQLite database file path")
	flag.StringVar(&c.CaddyfilePath, "caddyfile", c.CaddyfilePath, "generated Caddyfile path (host filesystem)")
	flag.BoolVar(&c.SkipCaddyReload, "skip-caddy-reload", false, "skip Docker Caddy container management (write Caddyfile only)")
	flag.Parse()

	if c.AdminPassword == "" {
		return nil, errors.New("NANOKU_ADMIN_PASSWORD env var is required")
	}
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