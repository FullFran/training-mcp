package engine

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// TokenPrefix marks an engine bearer. It is not a JWT, so no OAuth verifier can
// mistake one for an access token.
const TokenPrefix = "tme_"

// Tokens are the bearers the host gives its own engine processes for /mcp. They
// are random, kept only as a SHA-256 hash in memory, tied to one person, and die
// with their process or at their expiry, whichever comes first.
type Tokens struct {
	mu   sync.Mutex
	live map[[32]byte]tokenEntry
	now  func() time.Time
}

type tokenEntry struct {
	userID  int64
	expires time.Time
}

func NewTokens(now func() time.Time) *Tokens {
	if now == nil {
		now = time.Now
	}
	return &Tokens{live: map[[32]byte]tokenEntry{}, now: now}
}

func (t *Tokens) Mint(userID int64, ttl time.Duration) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	token := TokenPrefix + base64.RawURLEncoding.EncodeToString(b)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.live[sha256.Sum256([]byte(token))] = tokenEntry{userID: userID, expires: t.now().Add(ttl)}
	return token
}

// Resolve finds the person a live token was minted for, by hash and never by
// comparing secrets.
func (t *Tokens) Resolve(token string) (int64, bool) {
	if !strings.HasPrefix(token, TokenPrefix) {
		return 0, false
	}
	h := sha256.Sum256([]byte(token))
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.live[h]
	if !ok {
		return 0, false
	}
	if !t.now().Before(e.expires) {
		delete(t.live, h)
		return 0, false
	}
	return e.userID, true
}

func (t *Tokens) Revoke(token string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.live, sha256.Sum256([]byte(token)))
}

// LooksLikeToken reports whether a bearer is meant as an engine token.
func LooksLikeToken(bearer string) bool { return strings.HasPrefix(bearer, TokenPrefix) }

// proxyHeaders are headers a reverse proxy adds. A proxy on the same host also
// connects from loopback, so a request that carries any of them is treated as
// coming from outside.
var proxyHeaders = []string{"Forwarded", "Via", "X-Real-Ip", "Cf-Connecting-Ip", "True-Client-Ip"}

// DirectLoopback reports whether a request came straight from this machine to
// the loopback address the engine dials: a loopback peer, a loopback Host and no
// proxy header. A proxy that adds no such header cannot be told apart; the docs
// say what that exposes.
func DirectLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return false
	}
	reqHost := r.Host
	if h, _, err := net.SplitHostPort(reqHost); err == nil {
		reqHost = h
	}
	if hip := net.ParseIP(strings.Trim(reqHost, "[]")); hip == nil || !hip.IsLoopback() {
		return false
	}
	for name := range r.Header {
		if strings.HasPrefix(http.CanonicalHeaderKey(name), "X-Forwarded-") {
			return false
		}
	}
	for _, name := range proxyHeaders {
		if _, ok := r.Header[name]; ok {
			return false
		}
	}
	return true
}
