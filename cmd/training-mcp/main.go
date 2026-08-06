package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/fullfran/training-mcp/internal/config"
	"github.com/fullfran/training-mcp/internal/httpsrv"
	"github.com/fullfran/training-mcp/internal/mcpsrv"
	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
)

func address(args []string) string {
	fs := flag.NewFlagSet("training-mcp", flag.ContinueOnError)
	v := fs.String("addr", ":8080", "")
	_ = fs.Parse(args)
	return *v
}
func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Print(err)
		os.Exit(1)
	}
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
	service := training.NewService(store, time.Now)
	mcpServer := mcpsrv.New(service)
	logger := log.Default()
	httpsrv.WarningIfDisabled(cfg.AuthDisabled, logger)
	httpServer := &http.Server{Addr: cfg.Addr, Handler: httpsrv.Routes(mcpServer.Handler(), cfg.AuthDisabled, cfg.Token), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
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
