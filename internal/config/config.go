package config

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Addr, DBPath, Token string
	AuthDisabled        bool
	// WebBasePath mounts the PWA under a secret, unguessable prefix. Empty
	// disables the web UI entirely, so it is never exposed by accident.
	WebBasePath string
}

func Load(addr string) (Config, error) {
	if addr == "" {
		addr = ":8080"
	}
	webBase, err := normalizeBasePath(os.Getenv("WEB_BASE_PATH"))
	if err != nil {
		return Config{}, err
	}
	db := os.Getenv("TRAINING_DB_PATH")
	if db == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return Config{}, err
		}
		db = filepath.Join(home, ".local", "share", "training-mcp", "training.db")
	}
	disabled := os.Getenv("MCP_AUTH_DISABLED") == "1"
	token := os.Getenv("MCP_BEARER_TOKEN")
	if token == "" && !disabled {
		return Config{}, errors.New("MCP_BEARER_TOKEN is required unless MCP_AUTH_DISABLED=1")
	}
	return Config{Addr: addr, DBPath: db, Token: token, AuthDisabled: disabled, WebBasePath: webBase}, nil
}

// normalizeBasePath accepts "", "app", "/app" or "/app/" and returns "" or
// "/app". A single "/" is rejected: the PWA must live under a secret prefix,
// not at the root where it would be reachable by guessing.
func normalizeBasePath(v string) (string, error) {
	v = strings.Trim(strings.TrimSpace(v), "/")
	if v == "" {
		return "", nil
	}
	if strings.ContainsAny(v, " ?#") {
		return "", errors.New("WEB_BASE_PATH must not contain spaces, '?' or '#'")
	}
	return "/" + v, nil
}
func AddressFlag(fs *flag.FlagSet) *string { return fs.String("addr", ":8080", "HTTP listen address") }
