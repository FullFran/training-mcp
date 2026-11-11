package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"
)

// serveTimeout bounds how long the real composition may take to start serving
// and to stop. CI runs with -race next to every other package, where startup
// takes seconds, so this only has to catch a server that never serves.
const serveTimeout = 30 * time.Second

// startServer runs the real composition with the environment the test already
// set and returns its base URL and bearer token once that server answers.
//
// A port found by listening on :0 and closing is only free at that instant:
// under go test ./... other packages open servers and connections too, and one
// can take the port before this server binds it. So the token is unique to
// this test, readiness means /mcp accepted it (no other server would), and a
// port lost to someone else is replaced by a new one.
func startServer(t *testing.T) (base, token string) {
	t.Helper()
	token = "test-" + rand.Text()
	t.Setenv("MCP_BEARER_TOKEN", token)
	// With auth disabled any server would accept the token and prove nothing.
	t.Setenv("MCP_AUTH_DISABLED", "")
	deadline := time.Now().Add(serveTimeout)
	for {
		addr := freeLoopbackAddr(t)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- runWithContext(ctx, []string{"--addr", addr}) }()
		base = "http://" + addr
		err := waitServing(base, token, done, deadline)
		if err == nil {
			t.Cleanup(func() {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Errorf("server: %v", err)
					}
				case <-time.After(serveTimeout):
					t.Error("server did not stop")
				}
			})
			return base, token
		}
		cancel()
		if !errors.Is(err, syscall.EADDRINUSE) || time.Now().After(deadline) {
			t.Fatalf("server never served: %v", err)
		}
	}
}

func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

// waitServing returns nil once /mcp at base accepts token, the server's error
// if it stops first, or the last failure when the deadline passes.
func waitServing(base, token string, done <-chan error, deadline time.Time) error {
	const probe = `{"jsonrpc":"2.0","id":0,"method":"tools/list","params":{}}`
	last := errors.New("not probed")
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			if err == nil {
				err = errors.New("server stopped before serving")
			}
			return err
		default:
		}
		req, _ := http.NewRequest(http.MethodPost, base+"/mcp", strings.NewReader(probe))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		res, err := http.DefaultClient.Do(req)
		if err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return nil
			}
			err = fmt.Errorf("/mcp answered %d", res.StatusCode)
		}
		last = err
		time.Sleep(20 * time.Millisecond)
	}
	return fmt.Errorf("not serving after %v: %w", serveTimeout, last)
}
