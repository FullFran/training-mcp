package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/fullfran/training-mcp/internal/config"
	"github.com/fullfran/training-mcp/internal/engine"
	"github.com/fullfran/training-mcp/internal/engine/connector"
	"github.com/fullfran/training-mcp/internal/httpsrv"
	"github.com/fullfran/training-mcp/internal/mcpsrv"
	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
	"github.com/fullfran/training-mcp/internal/websrv"
)

func address(args []string) string {
	fs := flag.NewFlagSet("training-mcp", flag.ContinueOnError)
	v := fs.String("addr", ":8080", "")
	_ = fs.Parse(args)
	return *v
}

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck(address(os.Args[2:])))
	}
	if err := run(os.Args[1:]); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

// healthcheck lets a distroless container check itself without a shell.
func healthcheck(listen string) int {
	return healthcheckWithOutput(listen, os.Stderr)
}

func healthcheckWithOutput(listen string, stderr io.Writer) int {
	if !strings.Contains(listen, ":") {
		listen = ":" + listen
	}
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck: invalid listen address")
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	c := &http.Client{Timeout: 3 * time.Second}
	resp, err := c.Get("http://" + net.JoinHostPort(host, port) + "/health")
	if err != nil {
		fmt.Fprintln(stderr, "healthcheck:", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(stderr, "healthcheck: status", resp.StatusCode)
		return 1
	}
	return 0
}
func run(args []string) error {
	return runWithSignals(args, os.Interrupt, syscall.SIGTERM)
}

func runWithSignals(args []string, signals ...os.Signal) error {
	ctx, stop := signal.NotifyContext(context.Background(), signals...)
	defer stop()
	return runWithContext(ctx, args)
}

func runWithContext(ctx context.Context, args []string) error {
	addr := address(args)
	cfg, err := config.Load(addr)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(cfg.DBPath), 0700); err != nil {
		return err
	}
	store, err := sqlitestore.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer store.Close()
	services := training.NewServices(store, time.Now)
	mcpServer := mcpsrv.New(services.ForRequest)
	logger := log.Default()
	httpsrv.WarningIfDisabled(cfg.AuthDisabled, logger)

	coach, engineTokens, connectors, proxy, err := startCoach(cfg.Coach, cfg.WebPublicURL, store, logger)
	if err != nil {
		return err
	}
	if coach != nil {
		// A coach proposal belongs to the stored conversation it was made in.
		mcpServer.SetCoachSessions(coach.SessionOf)
		// Engine processes must not outlive the server, even when it stops on error.
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			coach.Close(closeCtx)
		}()
	}

	if cfg.WebBasePathIgnored {
		// The value was a credential once; it is never logged.
		logger.Printf("WEB_BASE_PATH is set but ignored: the web app is only served at the root behind the login; remove it from the environment")
	}

	var oauth *httpsrv.OAuth
	if cfg.OAuthIssuer != "" {
		oauth = httpsrv.NewOAuth(httpsrv.OAuthConfig{
			Issuer: cfg.OAuthIssuer, Resource: cfg.OAuthResource, Resolve: services.ResolveUser(cfg.OAuthSubject, cfg.OAuthProvision), Logger: logger,
		})
		logger.Printf("oauth enabled for resource %s", cfg.OAuthResource)
		if cfg.OAuthProvision {
			logger.Printf("oauth gives every subject the provider vouches for an account")
		}
	}
	var root *httpsrv.RootWeb
	if cfg.WebClientID != "" {
		login := httpsrv.NewWebLogin(httpsrv.WebLoginConfig{
			Issuer: cfg.OAuthIssuer, ClientID: cfg.WebClientID, ClientSecret: cfg.WebClientSecret, PublicURL: cfg.WebPublicURL,
			Keys: oauth, Resolve: services.ResolveUser(cfg.OAuthSubject, cfg.OAuthProvision), Logger: logger,
		})
		// The root never serves /export.db: it holds every user's data.
		app, err := websrv.NewWithOptions(services.ForRequest, services.Snapshot, time.Now, "", websrv.Options{CSRFToken: login.CSRFToken, Apply: mcpServer.Apply, Coach: coachFor(coach), Connectors: connectorsFor(connectors)})
		if err != nil {
			return err
		}
		root = &httpsrv.RootWeb{Handler: app.Handler(), Login: login}
		logger.Printf("web UI served at %s/ behind the provider's login", cfg.WebPublicURL)
	}
	handler := httpsrv.LogProtocol(logger, httpsrv.RoutesWithEngine(mcpServer.Handler(), cfg.AuthDisabled, cfg.Token, oauth, root, engineTokens))
	if proxy != nil {
		// The engines' door to people's connectors: engine tokens on loopback only.
		outer := http.NewServeMux()
		outer.Handle(connector.ProxyPrefix, proxy)
		outer.Handle("/", handler)
		handler = outer
	}
	httpServer := &http.Server{Addr: cfg.Addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.ListenAndServe() }()
	select {
	case <-ctx.Done():
		// Open chat streams would hold Shutdown for its whole timeout; the engines
		// go first, which ends every stream.
		if coach != nil {
			closeCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			coach.Close(closeCtx)
			cancel()
		}
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		if err == http.ErrServerClosed {
			return nil
		}
		return err
	}
}

// startCoach builds the in-app coach when it is enabled. Disabled, it returns
// nils and nothing of the coach exists: no connectors and no proxy either.
func startCoach(c config.Coach, publicURL string, store *sqlitestore.Store, logger *log.Logger) (*engine.Manager, httpsrv.EngineTokens, *connector.Service, http.Handler, error) {
	if !c.Enabled {
		return nil, nil, nil, nil, nil
	}
	vault, err := engine.NewVault(filepath.Join(c.DataDir, "credentials"), c.Key)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	tokens := engine.NewTokens(nil)
	// The strict guard: public https servers only, checked at every dial.
	guard := &connector.Guard{}
	connectors := &connector.Service{Store: store, Secrets: vault, Guard: guard}
	if publicURL != "" {
		// Authorization servers send the person back to the app at the root.
		connectors.OAuth = &connector.OAuthClient{Guard: guard, RedirectURL: strings.TrimSuffix(publicURL, "/") + "/coach/connectors/callback", ClientName: "training-mcp coach"}
	}
	manager, err := engine.NewManager(engine.Settings{
		Vault: vault, Tokens: tokens, Command: engine.NodeCommand(c.Node, c.Engine),
		MCPURL: c.MCPURL, SystemPrompt: engine.SystemPrompt, Preferences: store, Conversations: store,
		MaxProcesses: c.MaxProcesses, Idle: time.Duration(c.IdleSeconds) * time.Second, Logger: logger,
		Connectors: connectors,
	})
	if err != nil {
		return nil, nil, nil, nil, err
	}
	connectors.Reload = manager.Reload
	connectors.Loaded = manager.Loaded
	proxy := &connector.Proxy{Service: connectors, Tokens: tokens, Authorize: manager.Authorize}
	logger.Printf("coach enabled: up to %d engine processes", c.MaxProcesses)
	return manager, tokens, connectors, proxy, nil
}

// connectorsFor keeps a nil service a nil interface.
func connectorsFor(s *connector.Service) websrv.Connectors {
	if s == nil {
		return nil
	}
	return s
}

// coachFor keeps a nil manager a nil interface, so the app sees no coach.
func coachFor(m *engine.Manager) websrv.Coach {
	if m == nil {
		return nil
	}
	return m
}
