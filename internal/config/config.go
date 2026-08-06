package config

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
)

type Config struct {
	Addr, DBPath, Token string
	AuthDisabled        bool
}

func Load(addr string) (Config, error) {
	if addr == "" {
		addr = ":8080"
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
	return Config{Addr: addr, DBPath: db, Token: token, AuthDisabled: disabled}, nil
}
func AddressFlag(fs *flag.FlagSet) *string { return fs.String("addr", ":8080", "HTTP listen address") }
