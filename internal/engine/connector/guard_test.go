package connector

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestPublicAddrRefusesEveryInternalRange(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "127.1.2.3", "::1", "0.0.0.0", "::",
		"10.0.0.1", "172.16.0.1", "172.31.255.255", "192.168.1.1",
		"169.254.169.254", "169.254.0.1", "fe80::1",
		"100.64.0.1", "100.100.100.200", "100.127.255.254",
		"fc00::1", "fd00:ec2::254",
		"::ffff:127.0.0.1", "::ffff:10.0.0.1", "::ffff:169.254.169.254",
		"64:ff9b::a00:1", "2002:a00:1::1",
		"224.0.0.1", "ff02::1", "255.255.255.255", "240.0.0.1",
		"192.0.0.1", "198.18.0.1", "192.0.2.1", "2001:db8::1",
	}
	for _, s := range blocked {
		if PublicAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s is allowed", s)
		}
	}
	for _, s := range []string{"1.1.1.1", "8.8.8.8", "104.18.0.1", "2606:4700::1111", "100.63.255.255", "172.32.0.1"} {
		if !PublicAddr(netip.MustParseAddr(s)) {
			t.Errorf("%s is refused", s)
		}
	}
}

func TestCheckURL(t *testing.T) {
	g := &Guard{}
	for _, raw := range []string{
		"http://example.com/mcp", "ftp://example.com", "https://user:pw@example.com/mcp", "https:///mcp",
		"https://127.0.0.1/mcp", "https://[::1]/mcp", "https://169.254.169.254/latest", "https://10.0.0.1/mcp",
		"https://example.com/" + strings.Repeat("a", 2100), "example.com/mcp", "https://example.com/mcp#frag",
		"https://localhost/mcp", "https://foo.localhost/mcp", "https://metadata.google.internal/x",
	} {
		if _, err := g.CheckURL(raw); err == nil {
			t.Errorf("%q accepted", raw)
		}
	}
	for _, raw := range []string{"https://mcp.notion.com/mcp", "https://example.com:8443/a/b?x=1"} {
		if _, err := g.CheckURL(raw); err != nil {
			t.Errorf("%q refused: %v", raw, err)
		}
	}
}

func TestStrictClientNeverReachesLoopback(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "secret") }))
	defer srv.Close()
	// The strict guard trusts the test server's certificate, so only the
	// address check can stop it.
	g := &Guard{RootCAs: poolOf(srv)}
	port := srv.URL[strings.LastIndex(srv.URL, ":")+1:]
	for _, target := range []string{srv.URL, "https://localhost:" + port} {
		req, _ := http.NewRequest(http.MethodGet, target, nil)
		resp, err := g.Client(5 * time.Second).Do(req)
		if err == nil {
			resp.Body.Close()
			t.Fatalf("%s reached", target)
		}
		if !errors.Is(err, ErrBlockedAddress) {
			t.Fatalf("%s: %v, want ErrBlockedAddress", target, err)
		}
	}
}

func TestRedirectsAreCheckedAgain(t *testing.T) {
	inner := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "inner") }))
	defer inner.Close()
	var target string
	outer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target, http.StatusFound) }))
	defer outer.Close()
	// Only the outer server's address counts as public here.
	outerAddr := netip.MustParseAddrPort(strings.TrimPrefix(outer.URL, "https://"))
	g := &Guard{RootCAs: poolOf(outer, inner), Allow: func(a netip.Addr) bool { return a == outerAddr.Addr() }}
	innerPort := inner.URL[strings.LastIndex(inner.URL, ":")+1:]
	cases := map[string]string{
		"to plain http":      "http://" + strings.TrimPrefix(inner.URL, "https://"),
		"to a blocked addr":  "https://[::1]:" + innerPort,
		"to a metadata addr": "https://169.254.169.254/latest/meta-data",
	}
	for name, to := range cases {
		target = to
		resp, err := g.Client(5 * time.Second).Get(outer.URL)
		if err == nil {
			resp.Body.Close()
			t.Errorf("%s: followed", name)
		}
	}
	// Too many hops.
	target = outer.URL
	if resp, err := g.Client(5 * time.Second).Get(outer.URL); err == nil {
		resp.Body.Close()
		t.Error("redirect loop followed")
	}
}

func TestEnvironmentProxyIsIgnored(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	g := &Guard{}
	if g.Transport().Proxy != nil {
		t.Fatal("the guarded transport uses a proxy")
	}
}

func TestReadLimited(t *testing.T) {
	if _, err := readLimited(strings.NewReader(strings.Repeat("a", 11)), 10); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over the limit: %v", err)
	}
	if b, err := readLimited(strings.NewReader("abc"), 10); err != nil || string(b) != "abc" {
		t.Fatalf("under the limit: %q %v", b, err)
	}
	_ = context.Background
}
