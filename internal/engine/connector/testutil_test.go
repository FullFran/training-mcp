package connector

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func poolOf(servers ...*httptest.Server) *x509.CertPool {
	pool := x509.NewCertPool()
	for _, s := range servers {
		pool.AddCert(s.Certificate())
	}
	return pool
}

// testGuard reaches the given test servers on loopback and nothing else.
func testGuard(servers ...*httptest.Server) *Guard {
	return &Guard{RootCAs: poolOf(servers...), Allow: func(a netip.Addr) bool { return a.IsLoopback() }}
}

// fakeRemote is a remote MCP server and its OAuth authorization server in one:
// protected resource metadata, authorization server metadata, dynamic client
// registration, a token endpoint that checks PKCE and rotates refresh tokens,
// and a Streamable HTTP MCP endpoint at /mcp that needs a live access token.
type fakeRemote struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	clients  map[string]string // client id -> redirect uri
	codes    map[string]fakeCode
	access   map[string]bool
	refresh  map[string]bool
	n        int
	seenAuth []string
	calls    []string // tools/call names
	lastHdr  http.Header
	// Static, when set, is the one bearer /mcp accepts (no OAuth).
	Static string
	// Open: /mcp needs no token at all.
	Open bool
	// NoS256 leaves PKCE S256 out of the metadata.
	NoS256 bool
	// IssuerOverride replaces the issuer in the metadata.
	IssuerOverride string
	// TokenEndpointOverride replaces the token endpoint in the metadata.
	TokenEndpointOverride string
	// ExpiresIn is the access token lifetime the token endpoint gives.
	ExpiresIn int
	// RejectAccess makes /mcp refuse every token once (to test refresh on 401).
	RejectAccess int
}

type fakeCode struct {
	client, challenge, redirect, resource string
}

func newFakeRemote(t *testing.T) *fakeRemote {
	f := &fakeRemote{t: t, clients: map[string]string{}, codes: map[string]fakeCode{}, access: map[string]bool{}, refresh: map[string]bool{}, ExpiresIn: 3600}
	f.srv = httptest.NewTLSServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeRemote) URL() string { return f.srv.URL + "/mcp" }

func (f *fakeRemote) next(prefix string) string {
	f.n++
	return fmt.Sprintf("%s-%d", prefix, f.n)
}

func (f *fakeRemote) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	base := f.srv.URL
	switch r.URL.Path {
	case "/.well-known/oauth-protected-resource/mcp":
		json.NewEncoder(w).Encode(map[string]any{"resource": base + "/mcp", "authorization_servers": []string{base}, "scopes_supported": []string{"read"}})
	case "/.well-known/oauth-authorization-server":
		issuer := base
		if f.IssuerOverride != "" {
			issuer = f.IssuerOverride
		}
		token := base + "/token"
		if f.TokenEndpointOverride != "" {
			token = f.TokenEndpointOverride
		}
		methods := []string{"S256"}
		if f.NoS256 {
			methods = []string{"plain"}
		}
		json.NewEncoder(w).Encode(map[string]any{
			"issuer": issuer, "authorization_endpoint": base + "/authorize", "token_endpoint": token,
			"registration_endpoint": base + "/register", "code_challenge_methods_supported": methods,
		})
	case "/register":
		var body struct {
			RedirectURIs []string `json:"redirect_uris"`
			AuthMethod   string   `json:"token_endpoint_auth_method"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || len(body.RedirectURIs) != 1 || body.AuthMethod != "none" {
			http.Error(w, "bad registration", http.StatusBadRequest)
			return
		}
		id := f.next("client")
		f.clients[id] = body.RedirectURIs[0]
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"client_id": id})
	case "/token":
		f.token(w, r)
	case "/mcp":
		f.mcp(w, r)
	default:
		http.NotFound(w, r)
	}
}

// approve plays the person's browser at the authorization endpoint: it returns
// the code and state the server would redirect back with.
func (f *fakeRemote) approve(authorizeURL string) (code, state string) {
	f.t.Helper()
	u, err := url.Parse(authorizeURL)
	if err != nil || !strings.HasPrefix(authorizeURL, f.srv.URL+"/authorize") {
		f.t.Fatalf("authorize URL %q", authorizeURL)
	}
	q := u.Query()
	f.mu.Lock()
	defer f.mu.Unlock()
	redirect, ok := f.clients[q.Get("client_id")]
	if !ok || redirect != q.Get("redirect_uri") || q.Get("code_challenge_method") != "S256" || q.Get("response_type") != "code" || q.Get("resource") != f.srv.URL+"/mcp" {
		f.t.Fatalf("authorize query %v", q)
	}
	code = f.next("code")
	f.codes[code] = fakeCode{client: q.Get("client_id"), challenge: q.Get("code_challenge"), redirect: redirect, resource: q.Get("resource")}
	return code, q.Get("state")
}

func (f *fakeRemote) issue(w http.ResponseWriter) {
	access, refresh := f.next("access"), f.next("refresh")
	f.access[access] = true
	f.refresh[refresh] = true
	json.NewEncoder(w).Encode(map[string]any{"access_token": access, "token_type": "Bearer", "expires_in": f.ExpiresIn, "refresh_token": refresh})
}

func (f *fakeRemote) token(w http.ResponseWriter, r *http.Request) {
	if r.ParseForm() != nil {
		http.Error(w, "bad", http.StatusBadRequest)
		return
	}
	if r.PostForm.Get("resource") != f.srv.URL+"/mcp" {
		http.Error(w, `{"error":"invalid_target"}`, http.StatusBadRequest)
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		c, ok := f.codes[r.PostForm.Get("code")]
		delete(f.codes, r.PostForm.Get("code"))
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !ok || c.client != r.PostForm.Get("client_id") || c.redirect != r.PostForm.Get("redirect_uri") || base64.RawURLEncoding.EncodeToString(sum[:]) != c.challenge {
			http.Error(w, `{"error":"invalid_grant","error_description":"leak me"}`, http.StatusBadRequest)
			return
		}
		f.issue(w)
	case "refresh_token":
		rt := r.PostForm.Get("refresh_token")
		if !f.refresh[rt] {
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
			return
		}
		delete(f.refresh, rt) // single use
		f.issue(w)
	default:
		http.Error(w, "bad grant", http.StatusBadRequest)
	}
}

func (f *fakeRemote) mcp(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	f.seenAuth = append(f.seenAuth, auth)
	f.lastHdr = r.Header.Clone()
	token := strings.TrimPrefix(auth, "Bearer ")
	ok := f.Open || (f.Static != "" && token == f.Static) || f.access[token]
	if ok && f.RejectAccess > 0 {
		f.RejectAccess--
		ok = false
	}
	if !ok {
		w.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+f.srv.URL+`/.well-known/oauth-protected-resource/mcp"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var msg struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params struct {
			Name string `json:"name"`
		} `json:"params"`
	}
	json.Unmarshal(body, &msg)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Mcp-Session-Id", "sess-1")
	switch msg.Method {
	case "initialize":
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2025-06-18","capabilities":{"tools":{}},"serverInfo":{"name":"fake","version":"1"}}}`, msg.ID)
	case "notifications/initialized":
		w.WriteHeader(http.StatusAccepted)
	case "tools/list":
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"search","description":"Search pages"},{"name":"create-page","description":"Create a page"}]}}`, msg.ID)
	case "tools/call":
		f.calls = append(f.calls, msg.Params.Name)
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"ok"}]}}`, msg.ID)
	default:
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"error":{"code":-32601,"message":"no"}}`, msg.ID)
	}
}
