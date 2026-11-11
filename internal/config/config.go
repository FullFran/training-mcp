package config

import (
	"errors"
	"flag"
	"net/url"
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
	// OAuth settings are all set or all empty; empty leaves OAuth off.
	OAuthIssuer, OAuthResource, OAuthSubject string
	// OAuthProvision gives any subject the provider vouches for an account of its
	// own. Off, only the owner and existing accounts get in.
	OAuthProvision bool
	// WebClientID and WebClientSecret are the provider's OAuth application for
	// the web login. Set, the app is served at the root of WebPublicURL behind
	// that login; empty, the root serves nothing.
	WebClientID, WebClientSecret string
	// WebPublicURL is the origin of MCP_OAUTH_RESOURCE, where the login returns.
	WebPublicURL string
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
	issuer := strings.TrimSpace(os.Getenv("MCP_OAUTH_ISSUER"))
	resource := strings.TrimSpace(os.Getenv("MCP_OAUTH_RESOURCE"))
	subject := strings.TrimSpace(os.Getenv("MCP_OAUTH_SUBJECT"))
	provision := os.Getenv("MCP_OAUTH_PROVISION")
	// Who may hold an account must never depend on how a value was misspelled.
	if provision != "" && provision != "1" {
		return Config{}, errors.New("MCP_OAUTH_PROVISION must be 1 or empty")
	}
	if provision != "" && issuer == "" {
		return Config{}, errors.New("MCP_OAUTH_PROVISION needs the MCP_OAUTH_* settings")
	}
	if issuer != "" || resource != "" || subject != "" {
		if disabled {
			// The bypass would leave /mcp open while the operator believes tokens
			// are being checked.
			return Config{}, errors.New("MCP_AUTH_DISABLED=1 cannot be combined with MCP_OAUTH_* settings")
		}
		// Half a configuration must stop the server: quietly running without one of
		// these would either lock the owner out or stop checking who is calling.
		if issuer == "" || resource == "" || subject == "" {
			return Config{}, errors.New("MCP_OAUTH_ISSUER, MCP_OAUTH_RESOURCE and MCP_OAUTH_SUBJECT must be set together")
		}
		if !strings.HasPrefix(issuer, "https://") || !strings.HasPrefix(resource, "https://") {
			return Config{}, errors.New("MCP_OAUTH_ISSUER and MCP_OAUTH_RESOURCE must be https URLs")
		}
		// Only /mcp is served, so any other resource would advertise metadata at a
		// path that does not exist.
		if u, err := url.Parse(resource); err != nil || u.Host == "" || u.Path != "/mcp" {
			return Config{}, errors.New("MCP_OAUTH_RESOURCE must be this server's public /mcp URL")
		}
	}
	clientID := strings.TrimSpace(os.Getenv("WEB_OAUTH_CLIENT_ID"))
	clientSecret := strings.TrimSpace(os.Getenv("WEB_OAUTH_CLIENT_SECRET"))
	var publicURL string
	if clientID != "" || clientSecret != "" {
		// Half a login would either never start or leave the client unable to prove
		// who it is to the provider.
		if clientID == "" || clientSecret == "" {
			return Config{}, errors.New("WEB_OAUTH_CLIENT_ID and WEB_OAUTH_CLIENT_SECRET must be set together")
		}
		// The login trusts the same provider as /mcp and admits the same accounts.
		if issuer == "" {
			return Config{}, errors.New("WEB_OAUTH_CLIENT_ID needs the MCP_OAUTH_* settings")
		}
		u, _ := url.Parse(resource)
		publicURL = u.Scheme + "://" + u.Host
	}
	return Config{
		WebClientID: clientID, WebClientSecret: clientSecret, WebPublicURL: publicURL,
		Addr: addr, DBPath: db, Token: token, AuthDisabled: disabled, WebBasePath: webBase,
		OAuthIssuer: issuer, OAuthResource: resource, OAuthSubject: subject, OAuthProvision: provision == "1",
	}, nil
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
