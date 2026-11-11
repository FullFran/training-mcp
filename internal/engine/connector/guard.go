// Package connector lets a person connect their own remote MCP servers to
// their coach: the records, the OAuth client, the guarded HTTP client every
// request to such a server goes through, and the loopback proxy the engine
// reaches those servers by.
//
// Every request to a connector leaves through a Guard: https only, and the
// address actually dialled, after DNS resolution and on every redirect, must be
// a public unicast address. The check runs when the connection is made, so a
// name that resolves to a public address at one moment and to a private one at
// the next (DNS rebinding) is still refused.
package connector

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"
)

var (
	// ErrBlockedAddress: the address is not a public unicast address.
	ErrBlockedAddress = errors.New("connector: address not allowed")
	// ErrBadURL: not an https URL a connector may have.
	ErrBadURL = errors.New("connector: URL not allowed")
	// ErrTooLarge: a response over its size limit.
	ErrTooLarge = errors.New("connector: response too large")
)

const (
	maxURLLen    = 2048
	maxRedirects = 3
)

// Guard builds HTTP clients that reach public https servers only. The zero
// value is the strict guard used in production.
type Guard struct {
	// Allow decides whether an address may be dialled. Nil means PublicAddr.
	// Tests replace it to reach their own fake servers.
	Allow func(netip.Addr) bool
	// RootCAs, when set, replaces the system roots (tests).
	RootCAs *x509.CertPool
}

// blockedPrefixes are every range that is not the public internet, or that
// embeds another address (NAT64, 6to4) which could then be private.
var blockedPrefixes = func() []netip.Prefix {
	var out []netip.Prefix
	for _, s := range []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.0.0.0/24", "192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16",
		"198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "224.0.0.0/4", "240.0.0.0/4",
		"::/128", "::1/128", "::ffff:0:0/96", "64:ff9b::/96", "64:ff9b:1::/48", "100::/64",
		"2001::/32", "2001:db8::/32", "2002::/16", "fc00::/7", "fe80::/10", "fec0::/10", "ff00::/8",
	} {
		out = append(out, netip.MustParsePrefix(s))
	}
	return out
}()

// PublicAddr reports whether an address is a public unicast address: not
// loopback, private, link-local (cloud metadata included), CGNAT, unique-local,
// multicast, reserved or documentation space.
func PublicAddr(a netip.Addr) bool {
	if !a.IsValid() || a.Zone() != "" {
		return false
	}
	a4 := a.Unmap()
	if a4 != a && !PublicAddr(a4) {
		return false
	}
	for _, p := range blockedPrefixes {
		if p.Contains(a) || p.Contains(a4) {
			return false
		}
	}
	return a.IsGlobalUnicast()
}

func (g *Guard) allowed(a netip.Addr) bool {
	if g.Allow != nil {
		return g.Allow(a.Unmap())
	}
	return PublicAddr(a)
}

// internalNames are names that never point at the public internet.
var internalSuffixes = []string{".localhost", ".local", ".internal", ".home.arpa", ".lan", ".intranet", ".corp"}

// CheckURL accepts an absolute https URL with a host name or a public IP, no
// user info, no fragment and a bounded length. It does not resolve the name;
// the dial does, and checks the address it gets.
func (g *Guard) CheckURL(raw string) (*url.URL, error) {
	if len(raw) > maxURLLen {
		return nil, ErrBadURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return nil, ErrBadURL
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return nil, ErrBadURL
	}
	if a, err := netip.ParseAddr(host); err == nil {
		if !g.allowed(a) {
			return nil, ErrBlockedAddress
		}
		return u, nil
	}
	host = strings.TrimSuffix(host, ".")
	if g.Allow == nil {
		if host == "localhost" || !strings.Contains(host, ".") {
			return nil, ErrBlockedAddress
		}
		for _, s := range internalSuffixes {
			if strings.HasSuffix(host, s) {
				return nil, ErrBlockedAddress
			}
		}
	}
	return u, nil
}

// Resolve checks a URL and every address its host resolves to now. The dial
// checks again, so this only refuses early with a clear answer.
func (g *Guard) Resolve(ctx context.Context, raw string) (*url.URL, error) {
	u, err := g.CheckURL(raw)
	if err != nil {
		return nil, err
	}
	if _, err := netip.ParseAddr(u.Hostname()); err == nil {
		return u, nil
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", u.Hostname())
	if err != nil || len(addrs) == 0 {
		return nil, ErrBadURL
	}
	for _, a := range addrs {
		if !g.allowed(a) {
			return nil, ErrBlockedAddress
		}
	}
	return u, nil
}

func (g *Guard) control(network, address string, _ syscall.RawConn) error {
	if network != "tcp4" && network != "tcp6" {
		return ErrBlockedAddress
	}
	ap, err := netip.ParseAddrPort(address)
	if err != nil || !g.allowed(ap.Addr()) {
		return ErrBlockedAddress
	}
	return nil
}

// Transport dials only allowed addresses, ignores proxy settings from the
// environment, and bounds the time to a response's headers.
func (g *Guard) Transport() *http.Transport {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second, Control: g.control}
	return &http.Transport{
		Proxy:                  nil,
		DialContext:            d.DialContext,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: g.RootCAs},
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  30 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
		IdleConnTimeout:        60 * time.Second,
		MaxIdleConnsPerHost:    4,
		ForceAttemptHTTP2:      true,
	}
}

// Client is a guarded client with a total timeout (zero: none, for streams).
// Every redirect is checked like the first URL, at most three of them.
func (g *Guard) Client(timeout time.Duration) *http.Client {
	return g.client(g.Transport(), timeout)
}

func (g *Guard) client(t http.RoundTripper, timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: t,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("connector: too many redirects")
			}
			if _, err := g.CheckURL(req.URL.String()); err != nil {
				return err
			}
			return nil
		},
	}
}

// readLimited reads at most limit bytes; more is ErrTooLarge.
func readLimited(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, ErrTooLarge
	}
	return b, nil
}
