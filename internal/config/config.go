package config

import (
	"net"
	"strconv"

	"errors"
	"flag"
	"github.com/fullfran/training-mcp/internal/engine"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

type Config struct {
	Addr, DBPath, Token string
	AuthDisabled        bool
	// WebBasePathIgnored reports a leftover WEB_BASE_PATH. The secret web mount
	// it used to name is gone: the app is only served at the root behind the
	// login, so the value is read for nothing but a startup notice.
	WebBasePathIgnored bool
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
	// Coach is the in-app coach. Off unless TRAINING_COACH=1.
	Coach Coach
}

// Coach configures the in-app coach (docs/coach.md). Disabled, nothing of it
// is started and its routes do not exist.
type Coach struct {
	Enabled bool
	// Key encrypts stored ChatGPT credentials. Never logged.
	Key []byte
	// DataDir holds the credential vault, next to the database.
	DataDir      string
	Node, Engine string
	// MCPURL is where engine processes reach /mcp, on loopback.
	MCPURL       string
	MaxProcesses int
	IdleSeconds  int
}

func Load(addr string) (Config, error) {
	if addr == "" {
		addr = ":8080"
	}
	// A deployment may still set it; refusing to start would turn a harmless
	// leftover into an outage.
	webBaseIgnored := strings.TrimSpace(os.Getenv("WEB_BASE_PATH")) != ""
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
	coach, err := loadCoach(addr, db, clientID != "")
	if err != nil {
		return Config{}, err
	}
	return Config{
		Coach:       coach,
		WebClientID: clientID, WebClientSecret: clientSecret, WebPublicURL: publicURL,
		Addr: addr, DBPath: db, Token: token, AuthDisabled: disabled, WebBasePathIgnored: webBaseIgnored,
		OAuthIssuer: issuer, OAuthResource: resource, OAuthSubject: subject, OAuthProvision: provision == "1",
	}, nil
}

func AddressFlag(fs *flag.FlagSet) *string { return fs.String("addr", ":8080", "HTTP listen address") }

func loadCoach(addr, db string, webOn bool) (Coach, error) {
	switch os.Getenv("TRAINING_COACH") {
	case "":
		return Coach{}, nil
	case "1":
	default:
		return Coach{}, errors.New("TRAINING_COACH must be 1 or empty")
	}
	// The chat lives in the web app; without one there is nowhere to use it.
	if !webOn {
		return Coach{}, errors.New("TRAINING_COACH needs the web app (WEB_OAUTH_CLIENT_ID)")
	}
	key, err := engine.ParseKey(strings.TrimSpace(os.Getenv("TRAINING_COACH_KEY")))
	if err != nil {
		return Coach{}, errors.New("TRAINING_COACH_KEY must be 32 bytes, base64 encoded")
	}
	maxProcs, err := positiveInt("TRAINING_COACH_MAX_PROCESSES", 2)
	if err != nil {
		return Coach{}, err
	}
	idle, err := positiveInt("TRAINING_COACH_IDLE_SECONDS", 300)
	if err != nil {
		return Coach{}, err
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return Coach{}, errors.New("TRAINING_COACH needs a listen address with a port")
	}
	return Coach{
		Enabled: true, Key: key, DataDir: filepath.Join(filepath.Dir(db), "coach"),
		Node: envOr("TRAINING_COACH_NODE", "node"), Engine: envOr("TRAINING_COACH_ENGINE", "/app/engine/main.ts"),
		MCPURL: "http://" + net.JoinHostPort("127.0.0.1", port) + "/mcp", MaxProcesses: maxProcs, IdleSeconds: idle,
	}, nil
}

func positiveInt(name string, def int) (int, error) {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, errors.New(name + " must be a positive whole number")
	}
	return n, nil
}

func envOr(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}
