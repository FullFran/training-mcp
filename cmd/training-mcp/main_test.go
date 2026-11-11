package main

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestHealthcheck(t *testing.T) {
	t.Run("200 ok returns 0 without database or secrets", func(t *testing.T) {
		// Ensure required env for full server startup is absent
		t.Setenv("MCP_BEARER_TOKEN", "")
		t.Setenv("TRAINING_DB_PATH", "")

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health" && r.Method == http.MethodGet {
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"status":"ok"}`))
				return
			}
			w.WriteHeader(http.StatusNotFound)
		}))
		defer srv.Close()

		if code := healthcheck(srv.Listener.Addr().String()); code != 0 {
			t.Fatalf("healthcheck returned %d, want 0", code)
		}
	})

	t.Run("non-200 returns 1 and prints one line to stderr", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()

		var errBuf bytes.Buffer
		code := healthcheckWithOutput(srv.Listener.Addr().String(), &errBuf)
		if code != 1 {
			t.Fatalf("healthcheck returned %d, want 1", code)
		}
		out := errBuf.String()
		if !strings.HasPrefix(out, "healthcheck: status 503") {
			t.Fatalf("unexpected stderr output: %q", out)
		}
		if lines := strings.Count(out, "\n"); lines != 1 {
			t.Fatalf("expected exactly 1 line on stderr, got %d: %q", lines, out)
		}
	})

	t.Run("nothing listening returns 1 and prints one line to stderr", func(t *testing.T) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := l.Addr().String()
		_ = l.Close()

		var errBuf bytes.Buffer
		code := healthcheckWithOutput(addr, &errBuf)
		if code != 1 {
			t.Fatalf("healthcheck returned %d, want 1", code)
		}
		out := errBuf.String()
		if !strings.HasPrefix(out, "healthcheck:") {
			t.Fatalf("unexpected stderr output: %q", out)
		}
		if lines := strings.Count(out, "\n"); lines != 1 {
			t.Fatalf("expected exactly 1 line on stderr, got %d: %q", lines, out)
		}
	})

	t.Run("invalid address returns 1 and prints one line to stderr", func(t *testing.T) {
		var errBuf bytes.Buffer
		code := healthcheckWithOutput("[invalid", &errBuf)
		if code != 1 {
			t.Fatalf("healthcheck returned %d, want 1", code)
		}
		out := errBuf.String()
		if !strings.HasPrefix(out, "healthcheck: invalid listen address") {
			t.Fatalf("unexpected stderr output: %q", out)
		}
		if lines := strings.Count(out, "\n"); lines != 1 {
			t.Fatalf("expected exactly 1 line on stderr, got %d: %q", lines, out)
		}
	})

	t.Run("host derivation resolves empty and 0.0.0.0 to 127.0.0.1", func(t *testing.T) {
		srv, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer srv.Close()
		_, port, err := net.SplitHostPort(srv.Addr().String())
		if err != nil {
			t.Fatal(err)
		}

		httpSrv := &http.Server{
			Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/health" && r.Method == http.MethodGet {
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(`{"status":"ok"}`))
					return
				}
				w.WriteHeader(http.StatusNotFound)
			}),
		}
		defer httpSrv.Close()
		go func() { _ = httpSrv.Serve(srv) }()

		if code := healthcheck(":" + port); code != 0 {
			t.Fatalf("healthcheck(:%s) returned %d, want 0", port, code)
		}
		if code := healthcheck("0.0.0.0:" + port); code != 0 {
			t.Fatalf("healthcheck(0.0.0.0:%s) returned %d, want 0", port, code)
		}
	})
}

func TestNormalStartupWithoutSubcommandUnchanged(t *testing.T) {
	t.Setenv("MCP_BEARER_TOKEN", "test-token")
	t.Setenv("TRAINING_DB_PATH", t.TempDir()+"/training.db")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := runWithContext(ctx, []string{"--addr", "127.0.0.1:0"}); err != nil {
		t.Fatalf("normal startup failed: %v", err)
	}
	if err := runWithContext(ctx, nil); err != nil {
		t.Fatalf("normal startup with nil args failed: %v", err)
	}
}

func TestHealthcheckCLIInvocation(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "training-mcp")
	buildCmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("go build failed: %v\n%s", err, out)
	}

	// 1. Inactive port -> exit code 1 and stderr message
	cmd := exec.Command(bin, "healthcheck", "--addr", "127.0.0.1:1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatal("healthcheck on inactive port should fail")
	}
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 1 {
		t.Fatalf("expected exit code 1, got %v", err)
	}
	if !strings.Contains(stderr.String(), "healthcheck:") {
		t.Fatalf("expected stderr output, got %q", stderr.String())
	}

	// 2. Active server returning 200 -> exit code 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" && r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	cmdOk := exec.Command(bin, "healthcheck", "--addr", srv.Listener.Addr().String())
	var stderrOk bytes.Buffer
	cmdOk.Stderr = &stderrOk
	if err := cmdOk.Run(); err != nil {
		t.Fatalf("healthcheck returned error: %v, stderr: %s", err, stderrOk.String())
	}
}

// TestWebLoginSettingsServeTheAppAtTheRoot boots the real composition with the
// web login configured and checks the root asks for a login at the provider.
func TestWebLoginSettingsServeTheAppAtTheRoot(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	t.Setenv("MCP_BEARER_TOKEN", "test-token")
	t.Setenv("MCP_AUTH_DISABLED", "")
	t.Setenv("WEB_BASE_PATH", "")
	t.Setenv("MCP_OAUTH_PROVISION", "")
	t.Setenv("TRAINING_DB_PATH", t.TempDir()+"/training.db")
	t.Setenv("MCP_OAUTH_ISSUER", "https://auth.example.test")
	t.Setenv("MCP_OAUTH_RESOURCE", "https://training.example.test/mcp")
	t.Setenv("MCP_OAUTH_SUBJECT", "user_owner")
	t.Setenv("WEB_OAUTH_CLIENT_ID", "web-client")
	t.Setenv("WEB_OAUTH_CLIENT_SECRET", "web-secret")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runWithContext(ctx, []string{"--addr", addr}) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("server: %v", err)
		}
	}()
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var resp *http.Response
	for i := 0; i < 100; i++ {
		if resp, err = client.Get("http://" + addr + "/"); err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
		t.Fatalf("GET /: status %d, location %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, err = client.Get("http://" + addr + "/login")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	to := resp.Header.Get("Location")
	if !strings.HasPrefix(to, "https://auth.example.test/oauth/authorize?") ||
		!strings.Contains(to, "redirect_uri=https%3A%2F%2Ftraining.example.test%2Fauth%2Fcallback") {
		t.Fatalf("login goes to %q", to)
	}
}
