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

	var web *httpsrv.WebUI
	if cfg.WebBasePath != "" {
		webServer, err := websrv.NewWithOptions(services.ForRequest, services.Snapshot, time.Now, cfg.WebBasePath, websrv.Options{Export: true, Apply: mcpServer.Apply})
		if err != nil {
			return err
		}
		web = &httpsrv.WebUI{Handler: webServer.Handler(), BasePath: webServer.BasePath()}
		logger.Printf("web UI mounted under a secret base path (%d chars)", len(cfg.WebBasePath))
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
		app, err := websrv.NewWithOptions(services.ForRequest, services.Snapshot, time.Now, "", websrv.Options{CSRFToken: login.CSRFToken, Apply: mcpServer.Apply})
		if err != nil {
			return err
		}
		root = &httpsrv.RootWeb{Handler: app.Handler(), Login: login}
		logger.Printf("web UI served at %s/ behind the provider's login", cfg.WebPublicURL)
	}
	handler := httpsrv.LogProtocol(logger, httpsrv.RoutesWithLogin(mcpServer.Handler(), cfg.AuthDisabled, cfg.Token, web, oauth, root))
	httpServer := &http.Server{Addr: cfg.Addr, Handler: handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- httpServer.ListenAndServe() }()
	select {
	case <-ctx.Done():
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
