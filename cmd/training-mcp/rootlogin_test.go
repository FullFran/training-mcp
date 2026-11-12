package main

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	rootClientID     = "web-client"
	rootClientSecret = "web-secret"
	rootResource     = "https://training.example.test/mcp"
	rootOwnerSubject = "user_owner"
)

// TestRootServerProcess is the server process of startRootServer: the real
// composition, run from this test binary so its TLS roots come from
// SSL_CERT_FILE, which a process reads only once. In a normal run it skips.
func TestRootServerProcess(t *testing.T) {
	addr := os.Getenv("TRAINING_TEST_ROOT_SERVER")
	if addr == "" {
		t.Skip("root server helper process only")
	}
	if err := run([]string{"--addr", addr}); err != nil {
		os.Stderr.WriteString("server: " + err.Error() + "\n")
		os.Exit(1)
	}
	os.Exit(0)
}

// rootServer is the real composition serving the app at the root behind the
// web login, with a fake provider standing in for the identity provider.
type rootServer struct {
	t           *testing.T
	base, token string
	key         *rsa.PrivateKey
	issuer      string
	logs        *lockedBuffer

	mu    sync.Mutex
	codes map[string]rootCode
}

type rootCode struct{ nonce, challenge string }

type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// startRootServer runs the server with the web login configured, plus env, and
// returns once /health answers. The database lives in TRAINING_DB_PATH when env
// names one, else in a temporary directory.
func startRootServer(t *testing.T, env map[string]string) *rootServer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	s := &rootServer{t: t, key: key, token: "test-" + rand.Text(), codes: map[string]rootCode{}, logs: &lockedBuffer{}}
	provider := httptest.NewTLSServer(http.HandlerFunc(s.serveProvider))
	t.Cleanup(provider.Close)
	s.issuer = provider.URL
	certFile := filepath.Join(t.TempDir(), "provider.pem")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: provider.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	vars := map[string]string{
		"MCP_BEARER_TOKEN": s.token, "MCP_AUTH_DISABLED": "",
		"MCP_OAUTH_ISSUER": s.issuer, "MCP_OAUTH_RESOURCE": rootResource, "MCP_OAUTH_SUBJECT": rootOwnerSubject, "MCP_OAUTH_PROVISION": "",
		"WEB_OAUTH_CLIENT_ID": rootClientID, "WEB_OAUTH_CLIENT_SECRET": rootClientSecret,
		"WEB_BASE_PATH": "", "TRAINING_COACH": "", "TRAINING_DB_PATH": filepath.Join(t.TempDir(), "training.db"),
		"SSL_CERT_FILE": certFile,
	}
	for k, v := range env {
		vars[k] = v
	}
	deadline := time.Now().Add(serveTimeout)
	for {
		addr := freeLoopbackAddr(t)
		vars["TRAINING_TEST_ROOT_SERVER"] = addr
		cmd := exec.Command(os.Args[0], "-test.run=^TestRootServerProcess$")
		cmd.Env = os.Environ()
		for k, v := range vars {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		attempt := &lockedBuffer{}
		cmd.Stderr = io.MultiWriter(attempt, s.logs)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		exited := make(chan struct{})
		go func() { cmd.Wait(); close(exited) }()
		s.base = "http://" + addr
		if s.waitHealthy(exited, deadline) {
			t.Cleanup(func() {
				cmd.Process.Signal(os.Interrupt)
				select {
				case <-exited:
				case <-time.After(serveTimeout):
					cmd.Process.Kill()
					t.Error("server did not stop")
				}
			})
			return s
		}
		cmd.Process.Kill()
		<-exited
		if !strings.Contains(attempt.String(), "address already in use") || time.Now().After(deadline) {
			t.Fatalf("server never served:\n%s", attempt.String())
		}
	}
}

func (s *rootServer) waitHealthy(exited <-chan struct{}, deadline time.Time) bool {
	for time.Now().Before(deadline) {
		select {
		case <-exited:
			return false
		default:
		}
		if res, err := http.Get(s.base + "/health"); err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				// Another process may hold this port; only ours knows the token.
				return s.mcpAccepts()
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	return false
}

func (s *rootServer) mcpAccepts() bool {
	req, _ := http.NewRequest(http.MethodPost, s.base+"/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":0,"method":"tools/list","params":{}}`))
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	res.Body.Close()
	return res.StatusCode == http.StatusOK
}

func (s *rootServer) serveProvider(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/.well-known/jwks.json":
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "key-1",
			"n": base64.RawURLEncoding.EncodeToString(s.key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(s.key.E)).Bytes()),
		}}})
	case "/oauth/token":
		s.mu.Lock()
		code, ok := s.codes[r.PostFormValue("code")]
		delete(s.codes, r.PostFormValue("code"))
		s.mu.Unlock()
		id, secret, basic := r.BasicAuth()
		sum := sha256.Sum256([]byte(r.PostFormValue("code_verifier")))
		if !ok || !basic || id != rootClientID || secret != rootClientSecret || base64.RawURLEncoding.EncodeToString(sum[:]) != code.challenge {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
			"iss": s.issuer, "aud": rootClientID, "sub": rootOwnerSubject, "nonce": code.nonce,
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
		})
		token.Header["kid"] = "key-1"
		signed, err := token.SignedString(s.key)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"access_token": "opaque", "token_type": "Bearer", "id_token": signed})
	default:
		http.NotFound(w, r)
	}
}

var rootCSRFMeta = regexp.MustCompile(`name="csrf-token" content="([^"]+)"`)

// rootSession is the owner signed in through the provider, as a browser holds it.
type rootSession struct {
	s      *rootServer
	cookie *http.Cookie
	csrf   string
}

func noRedirects() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// login signs the owner in, as if they had passed the provider's own page.
func (s *rootServer) login() *rootSession {
	s.t.Helper()
	client := noRedirects()
	res, err := client.Get(s.base + "/login")
	if err != nil {
		s.t.Fatal(err)
	}
	res.Body.Close()
	authorize, err := url.Parse(res.Header.Get("Location"))
	if err != nil || !strings.HasPrefix(authorize.String(), s.issuer+"/oauth/authorize?") {
		s.t.Fatalf("/login went to %q", res.Header.Get("Location"))
	}
	var loginCookie *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == "__Host-training-login" {
			loginCookie = c
		}
	}
	if loginCookie == nil {
		s.t.Fatalf("/login set no login cookie: %v", res.Header["Set-Cookie"])
	}
	q := authorize.Query()
	code := rand.Text()
	s.mu.Lock()
	s.codes[code] = rootCode{nonce: q.Get("nonce"), challenge: q.Get("code_challenge")}
	s.mu.Unlock()
	req, _ := http.NewRequest(http.MethodGet, s.base+"/auth/callback?"+url.Values{"code": {code}, "state": {q.Get("state")}}.Encode(), nil)
	req.AddCookie(loginCookie)
	res, err = client.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	session := &rootSession{s: s}
	for _, c := range res.Cookies() {
		if c.Name == "__Host-training-session" {
			session.cookie = c
		}
	}
	if res.StatusCode != http.StatusSeeOther || session.cookie == nil {
		s.t.Fatalf("callback: status %d, body %s\n%s", res.StatusCode, body, s.logs.String())
	}
	code2, page := session.do(http.MethodGet, "/", "", "")
	m := rootCSRFMeta.FindStringSubmatch(page)
	if code2 != http.StatusOK || m == nil {
		s.t.Fatalf("no csrf token on the app page: status %d", code2)
	}
	session.csrf = m[1]
	return session
}

// request builds a request carrying this session, and its CSRF token on writes.
func (r *rootSession) request(method, path, contentType, body string) *http.Request {
	req, _ := http.NewRequest(method, r.s.base+path, strings.NewReader(body))
	req.AddCookie(r.cookie)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if r.csrf != "" && method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("X-CSRF-Token", r.csrf)
	}
	return req
}

func (r *rootSession) do(method, path, contentType, body string) (int, string) {
	r.s.t.Helper()
	res, err := noRedirects().Do(r.request(method, path, contentType, body))
	if err != nil {
		r.s.t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(data)
}

// mcp calls /mcp with the static bearer, as a non-web client does.
func (s *rootServer) mcp(body string) (int, string) {
	s.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, s.base+"/mcp", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+s.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		s.t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(data)
}
