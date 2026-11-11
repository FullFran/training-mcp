package main

import (
	"bytes"
	"context"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
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
	for _, sig := range []os.Signal{os.Interrupt, syscall.SIGTERM} {
		t.Run(sig.String(), func(t *testing.T) {
			t.Setenv("MCP_BEARER_TOKEN", "test-token")
			t.Setenv("TRAINING_DB_PATH", t.TempDir()+"/training.db")
			// Nothing tells the test when the server starts listening for the
			// signal, so it is sent until the server stops. Catching it here as
			// well keeps one sent too early from killing the test binary.
			caught := make(chan os.Signal, 1)
			signal.Notify(caught, sig)
			defer signal.Stop(caught)
			done := make(chan error, 1)
			go func() { done <- runWithSignals([]string{"--addr", "127.0.0.1:0"}, sig) }()
			process, err := os.FindProcess(os.Getpid())
			if err != nil {
				t.Fatal(err)
			}
			again := time.NewTicker(50 * time.Millisecond)
			defer again.Stop()
			timeout := time.After(serveTimeout)
			for {
				if err := process.Signal(sig); err != nil {
					t.Fatal(err)
				}
				select {
				case err := <-done:
					if err != nil {
						t.Fatalf("signal shutdown: %v", err)
					}
					return
				case <-again.C:
				case <-timeout:
					t.Fatal("signal did not stop server")
				}
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
	t.Setenv("MCP_OAUTH_PROVISION", "")
	t.Setenv("TRAINING_DB_PATH", t.TempDir()+"/training.db")
	t.Setenv("MCP_OAUTH_ISSUER", "https://auth.example.test")
	t.Setenv("MCP_OAUTH_RESOURCE", "https://training.example.test/mcp")
	t.Setenv("MCP_OAUTH_SUBJECT", "user_owner")
	t.Setenv("WEB_OAUTH_CLIENT_ID", "web-client")
	t.Setenv("WEB_OAUTH_CLIENT_SECRET", "web-secret")
	base, _ := startServer(t)
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
		t.Fatalf("GET /: status %d, location %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, err = client.Get(base + "/login")
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

// TestRootAcceptsAgentProposals boots the real composition and walks one agent
// write from pending to applied through the review page, signed in at the root.
func TestRootAcceptsAgentProposals(t *testing.T) {
	srv := startRootServer(t, nil)
	owner := srv.login()
	form := "application/x-www-form-urlencoded"
	if code, _ := owner.do("POST", "/review/accept-all", form, "on=0"); code != http.StatusSeeOther {
		t.Fatalf("turning accept-all off: %d", code)
	}
	code, body := srv.mcp(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"create_plan","arguments":{"name":"empuje","items":[{"exercise":"banca","target_sets":3}]}}}`)
	if code != http.StatusOK || !strings.Contains(body, `\"status\":\"pending\"`) {
		t.Fatalf("create_plan: %d %s", code, body)
	}
	match := regexp.MustCompile(`\\"proposal_id\\":(\d+)`).FindStringSubmatch(body)
	if match == nil {
		t.Fatalf("no proposal id in %s", body)
	}
	if code, body := owner.do("POST", "/review/"+match[1]+"/accept", form, "remember=once"); code != http.StatusSeeOther {
		t.Fatalf("accept: %d %s", code, body)
	}
	_, body = srv.mcp(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"list_plans","arguments":{}}}`)
	if !strings.Contains(body, "empuje") {
		t.Fatalf("the accepted plan is missing: %s", body)
	}
}

const formerSecretPrefix = "/s3cr3t-path-for-tests"

// A leftover WEB_BASE_PATH starts the server, says it is ignored without
// quoting it, and mounts nothing: its prefix is a missing path, bearer or not.
func TestWebBasePathIsIgnored(t *testing.T) {
	logs := &lockedBuffer{}
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	t.Setenv("WEB_BASE_PATH", formerSecretPrefix)
	t.Setenv("MCP_OAUTH_ISSUER", "")
	t.Setenv("MCP_OAUTH_RESOURCE", "")
	t.Setenv("MCP_OAUTH_SUBJECT", "")
	t.Setenv("MCP_OAUTH_PROVISION", "")
	t.Setenv("WEB_OAUTH_CLIENT_ID", "")
	t.Setenv("WEB_OAUTH_CLIENT_SECRET", "")
	t.Setenv("TRAINING_COACH", "")
	t.Setenv("TRAINING_DB_PATH", t.TempDir()+"/training.db")
	base, token := startServer(t)
	for _, path := range []string{formerSecretPrefix, formerSecretPrefix + "/", formerSecretPrefix + "/export.db", formerSecretPrefix + "/history", "/"} {
		req, _ := http.NewRequest(http.MethodGet, base+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s with the bearer: status %d", path, res.StatusCode)
		}
	}
	got := logs.String()
	if !strings.Contains(got, "WEB_BASE_PATH is set but ignored") || strings.Contains(got, formerSecretPrefix[1:]) {
		t.Fatalf("startup log:\n%s", got)
	}
}

// With the login on, the former prefix is one more unknown path at the root:
// the static bearer is sent to sign in, and a signed-in owner finds nothing.
func TestWebBasePathIsIgnoredBesideTheLogin(t *testing.T) {
	srv := startRootServer(t, map[string]string{"WEB_BASE_PATH": formerSecretPrefix})
	owner := srv.login()
	for _, path := range []string{formerSecretPrefix + "/", formerSecretPrefix + "/export.db", "/export.db"} {
		req, _ := http.NewRequest(http.MethodGet, srv.base+path, nil)
		req.Header.Set("Authorization", "Bearer "+srv.token)
		res, err := noRedirects().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/login" {
			t.Errorf("GET %s with the bearer: status %d, location %q", path, res.StatusCode, res.Header.Get("Location"))
		}
		if code, _ := owner.do(http.MethodGet, path, "", ""); code != http.StatusNotFound {
			t.Errorf("GET %s signed in: status %d", path, code)
		}
	}
	if !strings.Contains(srv.logs.String(), "WEB_BASE_PATH is set but ignored") {
		t.Fatalf("startup log:\n%s", srv.logs.String())
	}
}
