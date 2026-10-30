package httpsrv

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testIssuer   = "https://auth.example.test"
	testResource = "https://training.example.test/mcp"
	testSubject  = "user_owner"
)

type oauthFixture struct {
	t        *testing.T
	key      *rsa.PrivateKey
	kid      string
	jwks     *httptest.Server
	requests atomic.Int32
	failing  atomic.Bool
	handler  http.Handler
	logs     *bytes.Buffer
	reached  *atomic.Bool
}

func newOAuthFixture(t *testing.T) *oauthFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &oauthFixture{t: t, key: key, kid: "key-1", logs: &bytes.Buffer{}, reached: &atomic.Bool{}}
	f.jwks = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.requests.Add(1)
		if f.failing.Load() {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "use": "sig", "alg": "RS256", "kid": f.kid,
			"n": base64.RawURLEncoding.EncodeToString(f.key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(f.key.E)).Bytes()),
		}}})
	}))
	t.Cleanup(f.jwks.Close)
	oauth := NewOAuth(OAuthConfig{
		Issuer: testIssuer, Resource: testResource, Subject: testSubject,
		JWKSURL: f.jwks.URL, Logger: log.New(f.logs, "", 0),
	})
	mcp := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.reached.Store(true)
		w.WriteHeader(http.StatusOK)
	})
	web := &WebUI{Handler: mcp, BasePath: "/app"}
	f.handler = RoutesWithOAuth(mcp, false, "static-token", web, oauth)
	return f
}

func goodClaims() jwt.MapClaims {
	now := time.Now()
	return jwt.MapClaims{
		"iss": testIssuer, "aud": testResource, "sub": testSubject,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}
}

func (f *oauthFixture) sign(claims jwt.MapClaims) string {
	f.t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = f.kid
	signed, err := token.SignedString(f.key)
	if err != nil {
		f.t.Fatal(err)
	}
	return signed
}

func (f *oauthFixture) post(path, token string) *httptest.ResponseRecorder {
	f.t.Helper()
	f.reached.Store(false)
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader("{}"))
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

func TestOAuthAcceptsAValidTokenForTheOwner(t *testing.T) {
	f := newOAuthFixture(t)
	if got := f.post("/mcp", f.sign(goodClaims())); got.Code != http.StatusOK || !f.reached.Load() {
		t.Fatalf("status = %d, reached = %v, logs = %s", got.Code, f.reached.Load(), f.logs)
	}
}

func TestOAuthAcceptsAnAudienceList(t *testing.T) {
	f := newOAuthFixture(t)
	claims := goodClaims()
	claims["aud"] = []string{"something-else", testResource}
	if got := f.post("/mcp", f.sign(claims)); got.Code != http.StatusOK {
		t.Fatalf("status = %d, logs = %s", got.Code, f.logs)
	}
}

func TestOAuthRejectsEveryOtherToken(t *testing.T) {
	stranger, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	with := func(key string, value any) func(*oauthFixture) string {
		return func(f *oauthFixture) string {
			claims := goodClaims()
			claims[key] = value
			return f.sign(claims)
		}
	}
	without := func(key string) func(*oauthFixture) string {
		return func(f *oauthFixture) string {
			claims := goodClaims()
			delete(claims, key)
			return f.sign(claims)
		}
	}
	cases := map[string]func(*oauthFixture) string{
		"garbage":        func(*oauthFixture) string { return "not-a-jwt" },
		"expired":        with("exp", time.Now().Add(-time.Hour).Unix()),
		"not yet valid":  with("nbf", time.Now().Add(time.Hour).Unix()),
		"wrong issuer":   with("iss", "https://evil.example.test"),
		"wrong audience": with("aud", "https://other.example.test/mcp"),
		"another user":   with("sub", "user_stranger"),
		"no expiry":      without("exp"),
		"no audience":    without("aud"),
		"no issuer":      without("iss"),
		"no subject":     without("sub"),
		"signed by a stranger with our key id": func(f *oauthFixture) string {
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, goodClaims())
			token.Header["kid"] = f.kid
			signed, err := token.SignedString(stranger)
			if err != nil {
				t.Fatal(err)
			}
			return signed
		},
		"alg none": func(*oauthFixture) string {
			signed, err := jwt.NewWithClaims(jwt.SigningMethodNone, goodClaims()).SignedString(jwt.UnsafeAllowNoneSignatureType)
			if err != nil {
				t.Fatal(err)
			}
			return signed
		},
		"HS256 keyed with the public key": func(f *oauthFixture) string {
			token := jwt.NewWithClaims(jwt.SigningMethodHS256, goodClaims())
			token.Header["kid"] = f.kid
			signed, err := token.SignedString(f.key.N.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			return signed
		},
		"tampered payload": func(f *oauthFixture) string {
			parts := strings.Split(f.sign(goodClaims()), ".")
			forged, err := json.Marshal(func() jwt.MapClaims { c := goodClaims(); c["sub"] = "user_stranger"; return c }())
			if err != nil {
				t.Fatal(err)
			}
			parts[1] = base64.RawURLEncoding.EncodeToString(forged)
			return strings.Join(parts, ".")
		},
	}
	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			f := newOAuthFixture(t)
			got := f.post("/mcp", token(f))
			if got.Code != http.StatusUnauthorized || f.reached.Load() {
				t.Fatalf("status = %d, reached = %v", got.Code, f.reached.Load())
			}
		})
	}
}

func TestOAuthNeverLogsTheToken(t *testing.T) {
	f := newOAuthFixture(t)
	claims := goodClaims()
	claims["sub"] = "user_stranger"
	token := f.sign(claims)
	f.post("/mcp", token)
	if strings.Contains(f.logs.String(), token) || strings.Contains(f.logs.String(), strings.Split(token, ".")[2]) {
		t.Fatalf("logs contain the token: %s", f.logs)
	}
	if !strings.Contains(f.logs.String(), "rejected") {
		t.Fatalf("a rejection left no trace to diagnose: %q", f.logs)
	}
}

func TestStaticTokenStillWorksAlongsideOAuth(t *testing.T) {
	f := newOAuthFixture(t)
	if got := f.post("/mcp", "static-token"); got.Code != http.StatusOK || !f.reached.Load() {
		t.Fatalf("status = %d", got.Code)
	}
	if got := f.requests.Load(); got != 0 {
		t.Fatalf("the static token triggered %d key fetches", got)
	}
}

func TestUnauthenticatedRequestPointsAtTheMetadata(t *testing.T) {
	f := newOAuthFixture(t)
	got := f.post("/mcp", "")
	if got.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", got.Code)
	}
	want := `resource_metadata="https://training.example.test/.well-known/oauth-protected-resource/mcp"`
	if h := got.Header().Get("WWW-Authenticate"); !strings.HasPrefix(h, "Bearer ") || !strings.Contains(h, want) {
		t.Fatalf("WWW-Authenticate = %q", h)
	}
}

func TestProtectedResourceMetadataIsPublic(t *testing.T) {
	f := newOAuthFixture(t)
	for _, path := range []string{"/.well-known/oauth-protected-resource/mcp", "/.well-known/oauth-protected-resource"} {
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d", path, w.Code)
		}
		var doc struct {
			Resource             string   `json:"resource"`
			AuthorizationServers []string `json:"authorization_servers"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		if doc.Resource != testResource || len(doc.AuthorizationServers) != 1 || doc.AuthorizationServers[0] != testIssuer {
			t.Fatalf("%s doc = %#v", path, doc)
		}
	}
}

func TestOAuthTokensDoNotOpenTheWebApp(t *testing.T) {
	f := newOAuthFixture(t)
	if got := f.post("/app", f.sign(goodClaims())); got.Code != http.StatusUnauthorized || f.reached.Load() {
		t.Fatalf("status = %d, reached = %v", got.Code, f.reached.Load())
	}
	if got := f.post("/app", "static-token"); got.Code != http.StatusOK {
		t.Fatalf("static token on the web app: status = %d", got.Code)
	}
}

func TestOAuthOffBehavesAsBefore(t *testing.T) {
	mcp := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := Routes(mcp, false, "static-token", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{}")))
	if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") != "Bearer" {
		t.Fatalf("status = %d, header = %q", w.Code, w.Header().Get("WWW-Authenticate"))
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/.well-known/oauth-protected-resource/mcp", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("metadata status with OAuth off = %d", w.Code)
	}
}

func TestOAuthFetchesNewKeysOnRotationAndCachesOtherwise(t *testing.T) {
	f := newOAuthFixture(t)
	f.post("/mcp", f.sign(goodClaims()))
	f.post("/mcp", f.sign(goodClaims()))
	if got := f.requests.Load(); got != 1 {
		t.Fatalf("key fetches = %d, want 1", got)
	}
	rotated, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f.key, f.kid = rotated, "key-2"
	if got := f.post("/mcp", f.sign(goodClaims())); got.Code != http.StatusOK {
		t.Fatalf("rotated key rejected: status = %d, logs = %s", got.Code, f.logs)
	}
}

func TestOAuthFailsClosedWhenKeysAreUnavailable(t *testing.T) {
	f := newOAuthFixture(t)
	f.failing.Store(true)
	if got := f.post("/mcp", f.sign(goodClaims())); got.Code != http.StatusUnauthorized || f.reached.Load() {
		t.Fatalf("status = %d, reached = %v", got.Code, f.reached.Load())
	}
}

func TestUnknownKeyIDsCannotHammerTheKeyEndpoint(t *testing.T) {
	f := newOAuthFixture(t)
	f.post("/mcp", f.sign(goodClaims()))
	before := f.requests.Load()
	for range 20 {
		token := jwt.NewWithClaims(jwt.SigningMethodRS256, goodClaims())
		token.Header["kid"] = "attacker-chosen"
		signed, err := token.SignedString(f.key)
		if err != nil {
			t.Fatal(err)
		}
		if got := f.post("/mcp", signed); got.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d", got.Code)
		}
	}
	if got := f.requests.Load() - before; got > 1 {
		t.Fatalf("20 unknown key ids caused %d fetches", got)
	}
}
