package main

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"
)

func TestAddressDefaultAndOverride(t *testing.T) {
	if got := address(nil); got != ":8080" {
		t.Fatalf("default=%q", got)
	}
	if got := address([]string{"--addr", "127.0.0.1:9090"}); got != "127.0.0.1:9090" {
		t.Fatalf("override=%q", got)
	}
}

func TestRunReturnsStartupFailure(t *testing.T) {
	t.Setenv("MCP_BEARER_TOKEN", "test-token")
	t.Setenv("TRAINING_DB_PATH", t.TempDir()+"/training.db")
	if err := run([]string{"--addr", "[invalid"}); err == nil {
		t.Fatal("invalid listen address should fail startup")
	}
}

func TestRunWithContextDrainsOnCancellation(t *testing.T) {
	t.Setenv("MCP_BEARER_TOKEN", "test-token")
	t.Setenv("TRAINING_DB_PATH", t.TempDir()+"/training.db")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runWithContext(ctx, []string{"--addr", "127.0.0.1:0"}); err != nil {
		t.Fatalf("canceled server returned %v", err)
	}
}

func TestRunHandlesInterruptAndTerminateSignalsAtCompositionSeam(t *testing.T) {
	for _, signal := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(signal.String(), func(t *testing.T) {
			t.Setenv("MCP_BEARER_TOKEN", "test-token")
			t.Setenv("TRAINING_DB_PATH", t.TempDir()+"/training.db")
			done := make(chan error, 1)
			go func() { done <- runWithSignals([]string{"--addr", "127.0.0.1:0"}, signal) }()
			time.Sleep(20 * time.Millisecond)
			process, err := os.FindProcess(os.Getpid())
			if err != nil {
				t.Fatal(err)
			}
			if err := process.Signal(signal); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("signal shutdown: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("signal did not stop server")
			}
		})
	}
}
