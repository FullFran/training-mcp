package httpsrv

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fullfran/training-mcp/internal/mcpsrv"
	"github.com/fullfran/training-mcp/internal/sqlitestore"
	"github.com/fullfran/training-mcp/internal/training"
	"github.com/fullfran/training-mcp/internal/websrv"
	"github.com/golang-jwt/jwt/v5"
)

const (
	testClientID     = "web-client"
	testClientSecret = "web-client-secret"
	testPublicURL    = "https://training.example.test"
	guestSubject     = "user_guest"
)

type issuedCode struct{ subject, nonce, challenge string }

// loginFixture runs the real routes, the web app at the root, the MCP handler and the
// database against a fake provider that behaves like Clerk's authorization server.
type loginFixture struct {
	t        *testing.T
	key      *rsa.PrivateKey
	provider *httptest.Server
	handler  http.Handler
	services *training.Services
	logs     *bytes.Buffer

	mu      sync.Mutex
	now     time.Time
	codes   map[string]issuedCode
	tweak   func(jwt.MapClaims)
	failing bool
}

func newLoginFixture(t *testing.T, provision bool) *loginFixture {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &loginFixture{t: t, key: key, logs: &bytes.Buffer{}, now: time.Now(), codes: map[string]issuedCode{}}
	f.provider = httptest.NewServer(http.HandlerFunc(f.serveProvider))
	t.Cleanup(f.provider.Close)

	store, err := sqlitestore.Open(filepath.Join(t.TempDir(), "training.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	f.services = training.NewServices(store, time.Now)
	logger := log.New(f.logs, "", 0)
	oauth := NewOAuth(OAuthConfig{
		Issuer: testIssuer, Resource: testResource, JWKSURL: f.provider.URL + "/.well-known/jwks.json",
		Logger: logger, Resolve: f.services.ResolveUser(testSubject, provision),
	})
	login := NewWebLogin(WebLoginConfig{
		Issuer: testIssuer, ClientID: testClientID, ClientSecret: testClientSecret, PublicURL: testPublicURL,
		AuthorizeURL: f.provider.URL + "/oauth/authorize", TokenURL: f.provider.URL + "/oauth/token",
		Keys: oauth, Resolve: f.services.ResolveUser(testSubject, provision), Now: f.clock, Logger: logger,
	})
	root, err := websrv.NewWithOptions(f.services.ForRequest, f.services.Snapshot, time.Now, "", websrv.Options{CSRFToken: login.CSRFToken})
	if err != nil {
		t.Fatal(err)
	}
	f.handler = RoutesWithLogin(mcpsrv.New(f.services.ForRequest).Handler(), false, staticToken, oauth,
		&RootWeb{Handler: root.Handler(), Login: login})
	return f
}

func (f *loginFixture) clock() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *loginFixture) advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

func (f *loginFixture) serveProvider(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/.well-known/jwks.json":
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "key-1",
			"n": base64.RawURLEncoding.EncodeToString(f.key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(f.key.E)).Bytes()),
		}}})
	case "/oauth/token":
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.failing {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		id, secret, ok := r.BasicAuth()
		code, issued := f.codes[r.PostFormValue("code")]
		delete(f.codes, r.PostFormValue("code"))
		sum := sha256.Sum256([]byte(r.PostFormValue("code_verifier")))
		switch {
		case r.Method != http.MethodPost || !ok || id != testClientID || secret != testClientSecret:
			http.Error(w, `{"error":"invalid_client"}`, http.StatusUnauthorized)
		case r.PostFormValue("grant_type") != "authorization_code" || !issued ||
			r.PostFormValue("redirect_uri") != testPublicURL+"/auth/callback" ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != code.challenge:
			http.Error(w, `{"error":"invalid_grant"}`, http.StatusBadRequest)
		default:
			claims := jwt.MapClaims{
				"iss": testIssuer, "aud": testClientID, "sub": code.subject, "nonce": code.nonce,
				"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
			}
			if f.tweak != nil {
				f.tweak(claims)
			}
			json.NewEncoder(w).Encode(map[string]any{
				"access_token": "opaque", "token_type": "Bearer", "id_token": f.sign(claims),
			})
		}
	default:
		http.NotFound(w, r)
	}
}

func (f *loginFixture) sign(claims jwt.MapClaims) string {
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "key-1"
	signed, err := token.SignedString(f.key)
	if err != nil {
		f.t.Fatal(err)
	}
	return signed
}

func (f *loginFixture) do(method, path string, form url.Values, cookies []*http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	f.t.Helper()
	var body *strings.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	} else {
		body = strings.NewReader("")
	}
	r := httptest.NewRequest(method, path, body)
	if form != nil {
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, c := range cookies {
		r.AddCookie(c)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	return w
}

// startLogin follows /login and returns the provider redirect and the cookie that
// binds the login to this browser.
func (f *loginFixture) startLogin() (*url.URL, *http.Cookie) {
	f.t.Helper()
	w := f.do(http.MethodGet, "/login", nil, nil, nil)
	if w.Code != http.StatusSeeOther {
		f.t.Fatalf("/login: status %d", w.Code)
	}
	to, err := url.Parse(w.Header().Get("Location"))
	if err != nil {
		f.t.Fatal(err)
	}
	cookie := findCookie(w, loginCookieName)
	if cookie == nil {
		f.t.Fatalf("/login set no login cookie: %v", w.Header()["Set-Cookie"])
	}
	return to, cookie
}

// issue makes the fake provider accept one code for subject, as if they had
// signed in on its page.
func (f *loginFixture) issue(subject string, authorize *url.URL) string {
	q := authorize.Query()
	code := randomTestString(f.t)
	f.mu.Lock()
	f.codes[code] = issuedCode{subject: subject, nonce: q.Get("nonce"), challenge: q.Get("code_challenge")}
	f.mu.Unlock()
	return code
}

func (f *loginFixture) callback(code, state string, loginCookie *http.Cookie) *httptest.ResponseRecorder {
	f.t.Helper()
	var cookies []*http.Cookie
	if loginCookie != nil {
		cookies = append(cookies, loginCookie)
	}
	return f.do(http.MethodGet, "/auth/callback?"+url.Values{"code": {code}, "state": {state}}.Encode(), nil, cookies, nil)
}

// login runs the whole flow and returns the session cookie.
func (f *loginFixture) login(subject string) *http.Cookie {
	f.t.Helper()
	authorize, loginCookie := f.startLogin()
	w := f.callback(f.issue(subject, authorize), authorize.Query().Get("state"), loginCookie)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/" {
		f.t.Fatalf("callback: status %d, location %q, body %s", w.Code, w.Header().Get("Location"), w.Body)
	}
	session := findCookie(w, sessionCookieName)
	if session == nil {
		f.t.Fatalf("callback set no session cookie")
	}
	return session
}

var csrfMeta = regexp.MustCompile(`name="csrf-token" content="([^"]+)"`)

// csrf reads the token the app page hands its scripts.
func (f *loginFixture) csrf(session *http.Cookie) string {
	f.t.Helper()
	w := f.do(http.MethodGet, "/", nil, []*http.Cookie{session}, nil)
	m := csrfMeta.FindStringSubmatch(w.Body.String())
	if w.Code != http.StatusOK || m == nil {
		f.t.Fatalf("no csrf token on the app page: status %d", w.Code)
	}
	return m[1]
}

func (f *loginFixture) addSet(session *http.Cookie, headers map[string]string, form url.Values) *httptest.ResponseRecorder {
	f.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	form.Set("exercise", "press secreto")
	form.Set("weight_kg", "80")
	form.Set("reps", "8")
	form.Set("rpe", "8")
	h := map[string]string{"HX-Request": "true"}
	for k, v := range headers {
		h[k] = v
	}
	var cookies []*http.Cookie
	if session != nil {
		cookies = append(cookies, session)
	}
	return f.do(http.MethodPost, "/sets", form, cookies, h)
}

func (f *loginFixture) ownerSetCount() int {
	f.t.Helper()
	sessions, err := f.services.ForUser(training.OwnerUserID).ListSessions(context.Background(), training.ListFilter{Limit: 100})
	if err != nil {
		f.t.Fatal(err)
	}
	n := 0
	for _, s := range sessions {
		n += s.SetCount
	}
	return n
}

func findCookie(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func randomTestString(t *testing.T) string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func TestWebRootRequiresLogin(t *testing.T) {
	f := newLoginFixture(t, false)
	for _, path := range []string{"/", "/history", "/plans", "/s/1", "/exercise-info?name=x", "/nope"} {
		w := f.do(http.MethodGet, path, nil, nil, nil)
		if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
			t.Errorf("GET %s: status %d, location %q", path, w.Code, w.Header().Get("Location"))
		}
	}
	// A partial request cannot follow a redirect into a login page, so it is told
	// where to go instead.
	w := f.do(http.MethodGet, "/history", nil, nil, map[string]string{"HX-Request": "true"})
	if w.Code != http.StatusUnauthorized || w.Header().Get("HX-Redirect") != "/login" {
		t.Errorf("htmx GET: status %d, HX-Redirect %q", w.Code, w.Header().Get("HX-Redirect"))
	}
	if w := f.addSet(nil, nil, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous POST /sets: status %d", w.Code)
	}
	if n := f.ownerSetCount(); n != 0 {
		t.Fatalf("an anonymous write landed: %d sets", n)
	}
}

func TestLoginStartsAnAuthorizationCodeFlowWithPKCE(t *testing.T) {
	f := newLoginFixture(t, false)
	authorize, cookie := f.startLogin()
	q := authorize.Query()
	if got := authorize.Scheme + "://" + authorize.Host + authorize.Path; got != f.provider.URL+"/oauth/authorize" {
		t.Fatalf("authorize endpoint = %s", got)
	}
	want := map[string]string{
		"response_type": "code", "client_id": testClientID, "redirect_uri": testPublicURL + "/auth/callback",
		"scope": "openid", "code_challenge_method": "S256",
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	for _, k := range []string{"state", "nonce", "code_challenge"} {
		if len(q.Get(k)) < 43 {
			t.Errorf("%s is too short to be unguessable: %q", k, q.Get(k))
		}
	}
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
		t.Fatalf("login cookie flags: %+v", cookie)
	}
	// Two logins never share their secrets.
	again, _ := f.startLogin()
	if again.Query().Get("state") == q.Get("state") || again.Query().Get("nonce") == q.Get("nonce") {
		t.Fatal("state or nonce repeated across logins")
	}
}

func TestLoginGivesASecureSessionCookie(t *testing.T) {
	f := newLoginFixture(t, false)
	session := f.login(testSubject)
	if !session.HttpOnly || !session.Secure || session.SameSite != http.SameSiteLaxMode || session.Path != "/" || session.Domain != "" {
		t.Fatalf("session cookie flags: %+v", session)
	}
	if !strings.HasPrefix(session.Name, "__Host-") {
		t.Fatalf("session cookie %q is not host-only", session.Name)
	}
	if session.MaxAge <= 0 || session.MaxAge > int((7*24*time.Hour).Seconds()) {
		t.Fatalf("session max-age = %d", session.MaxAge)
	}
	w := f.do(http.MethodGet, "/", nil, []*http.Cookie{session}, nil)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `id="coach-log"`) {
		t.Fatalf("app page: status %d", w.Code)
	}
}

func TestCallbackRefusesEverythingButAGenuineLogin(t *testing.T) {
	cases := map[string]func(f *loginFixture) *httptest.ResponseRecorder{
		"no login cookie": func(f *loginFixture) *httptest.ResponseRecorder {
			a, _ := f.startLogin()
			return f.callback(f.issue(testSubject, a), a.Query().Get("state"), nil)
		},
		"state from another login": func(f *loginFixture) *httptest.ResponseRecorder {
			a, _ := f.startLogin()
			_, other := f.startLogin()
			return f.callback(f.issue(testSubject, a), a.Query().Get("state"), other)
		},
		"forged state": func(f *loginFixture) *httptest.ResponseRecorder {
			a, c := f.startLogin()
			return f.callback(f.issue(testSubject, a), "forged", c)
		},
		"unknown code": func(f *loginFixture) *httptest.ResponseRecorder {
			a, c := f.startLogin()
			return f.callback("never-issued", a.Query().Get("state"), c)
		},
		"provider error": func(f *loginFixture) *httptest.ResponseRecorder {
			a, c := f.startLogin()
			return f.do(http.MethodGet, "/auth/callback?error=access_denied&state="+url.QueryEscape(a.Query().Get("state")), nil, []*http.Cookie{c}, nil)
		},
		"wrong issuer parameter": func(f *loginFixture) *httptest.ResponseRecorder {
			a, c := f.startLogin()
			q := url.Values{"code": {f.issue(testSubject, a)}, "state": {a.Query().Get("state")}, "iss": {"https://evil.example"}}
			return f.do(http.MethodGet, "/auth/callback?"+q.Encode(), nil, []*http.Cookie{c}, nil)
		},
		"provider down": func(f *loginFixture) *httptest.ResponseRecorder {
			f.mu.Lock()
			f.failing = true
			f.mu.Unlock()
			return f.genuineCallback()
		},
		"wrong nonce":     tweaked(func(c jwt.MapClaims) { c["nonce"] = "other" }),
		"no nonce":        tweaked(func(c jwt.MapClaims) { delete(c, "nonce") }),
		"wrong audience":  tweaked(func(c jwt.MapClaims) { c["aud"] = testResource }),
		"wrong issuer":    tweaked(func(c jwt.MapClaims) { c["iss"] = "https://evil.example" }),
		"expired":         tweaked(func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Hour).Unix() }),
		"no subject":      tweaked(func(c jwt.MapClaims) { c["sub"] = "" }),
		"no account":      tweaked(func(c jwt.MapClaims) { c["sub"] = "user_stranger" }),
		"code used twice": func(f *loginFixture) *httptest.ResponseRecorder { return f.replayedCode() },
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			f := newLoginFixture(t, false)
			w := run(f)
			if w.Code < 400 || findCookie(w, sessionCookieName) != nil && findCookie(w, sessionCookieName).MaxAge >= 0 {
				t.Fatalf("status %d, cookies %v", w.Code, w.Header()["Set-Cookie"])
			}
			if strings.Contains(w.Body.String(), "id_token") || strings.Contains(f.logs.String(), testClientSecret) {
				t.Fatal("a credential leaked into the response or the log")
			}
		})
	}
}

func tweaked(change func(jwt.MapClaims)) func(*loginFixture) *httptest.ResponseRecorder {
	return func(f *loginFixture) *httptest.ResponseRecorder {
		f.setTweak(change)
		return f.genuineCallback()
	}
}

func (f *loginFixture) setTweak(change func(jwt.MapClaims)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tweak = change
}

func (f *loginFixture) genuineCallback() *httptest.ResponseRecorder {
	a, c := f.startLogin()
	return f.callback(f.issue(testSubject, a), a.Query().Get("state"), c)
}

func (f *loginFixture) replayedCode() *httptest.ResponseRecorder {
	a, c := f.startLogin()
	code := f.issue(testSubject, a)
	if w := f.callback(code, a.Query().Get("state"), c); w.Code != http.StatusSeeOther {
		f.t.Fatalf("first use: status %d", w.Code)
	}
	return f.callback(code, a.Query().Get("state"), c)
}

func TestAccountlessSubjectIsForbiddenNotSentBackToLogin(t *testing.T) {
	f := newLoginFixture(t, false)
	f.setTweak(func(c jwt.MapClaims) { c["sub"] = "user_stranger" })
	w := f.genuineCallback()
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
}

func TestForgedExpiredAndLoggedOutSessionsOpenNothing(t *testing.T) {
	f := newLoginFixture(t, false)
	session := f.login(testSubject)
	token := f.csrf(session)

	forged := &http.Cookie{Name: sessionCookieName, Value: randomTestString(t) + randomTestString(t)}
	if w := f.do(http.MethodGet, "/", nil, []*http.Cookie{forged}, nil); w.Code != http.StatusSeeOther {
		t.Errorf("forged cookie: status %d", w.Code)
	}
	if w := f.addSet(forged, map[string]string{csrfHeader: token}, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("forged cookie write: status %d", w.Code)
	}
	empty := &http.Cookie{Name: sessionCookieName, Value: ""}
	if w := f.do(http.MethodGet, "/", nil, []*http.Cookie{empty}, nil); w.Code != http.StatusSeeOther {
		t.Errorf("empty cookie: status %d", w.Code)
	}

	// Logging out ends the session on the server, not just in this browser.
	w := f.do(http.MethodPost, "/logout", url.Values{csrfField: {token}}, []*http.Cookie{session}, nil)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/logged-out" {
		t.Fatalf("logout: status %d, location %q", w.Code, w.Header().Get("Location"))
	}
	if c := findCookie(w, sessionCookieName); c == nil || c.MaxAge >= 0 {
		t.Errorf("logout did not clear the cookie: %v", w.Header()["Set-Cookie"])
	}
	if got := w.Header().Get("Clear-Site-Data"); !strings.Contains(got, `"cache"`) || !strings.Contains(got, `"storage"`) {
		t.Errorf("Clear-Site-Data = %q", got)
	}
	if w := f.do(http.MethodGet, "/", nil, []*http.Cookie{session}, nil); w.Code != http.StatusSeeOther {
		t.Errorf("cookie after logout: status %d", w.Code)
	}

	// A session ends after seven days whatever the cookie says.
	late := f.login(testSubject)
	f.advance(7*24*time.Hour + time.Second)
	if w := f.do(http.MethodGet, "/", nil, []*http.Cookie{late}, nil); w.Code != http.StatusSeeOther {
		t.Errorf("expired session: status %d", w.Code)
	}
}

func TestEveryStateChangingRequestNeedsTheSessionsCSRFToken(t *testing.T) {
	f := newLoginFixture(t, true)
	owner := f.login(testSubject)
	token := f.csrf(owner)
	guest := f.login(guestSubject)
	guestToken := f.csrf(guest)

	refused := map[string]*httptest.ResponseRecorder{
		"no token":                 f.addSet(owner, nil, nil),
		"wrong token":              f.addSet(owner, map[string]string{csrfHeader: "wrong"}, nil),
		"another session's token":  f.addSet(owner, map[string]string{csrfHeader: guestToken}, nil),
		"token in the query":       f.do(http.MethodPost, "/sets?"+csrfField+"="+token, url.Values{"exercise": {"x"}}, []*http.Cookie{owner}, nil),
		"logout without the token": f.do(http.MethodPost, "/logout", nil, []*http.Cookie{owner}, nil),
		"plan form without token":  f.do(http.MethodPost, "/plan", url.Values{"plan_id": {"1"}}, []*http.Cookie{owner}, nil),
		"delete without token":     f.do(http.MethodPost, "/sets/1/delete", url.Values{}, []*http.Cookie{owner}, nil),
		"plans without token":      f.do(http.MethodPost, "/plans/create", url.Values{"name": {"x"}}, []*http.Cookie{owner}, nil),
		"other method no token":    f.do(http.MethodPut, "/sets", url.Values{}, []*http.Cookie{owner}, nil),
	}
	for name, w := range refused {
		if w.Code != http.StatusForbidden {
			t.Errorf("%s: status %d", name, w.Code)
		}
	}
	if n := f.ownerSetCount(); n != 0 {
		t.Fatalf("a request without the token wrote %d sets", n)
	}
	if w := f.addSet(owner, map[string]string{csrfHeader: token}, nil); w.Code != http.StatusOK {
		t.Fatalf("header token: status %d", w.Code)
	}
	if w := f.addSet(owner, map[string]string{"HX-Request": ""}, url.Values{csrfField: {token}}); w.Code != http.StatusSeeOther {
		t.Fatalf("form token: status %d", w.Code)
	}
	if n := f.ownerSetCount(); n != 2 {
		t.Fatalf("owner has %d sets, want 2", n)
	}
	if guestToken == token {
		t.Fatal("two sessions share a token")
	}
}

func TestEachSignedInUserSeesOnlyTheirOwnData(t *testing.T) {
	f := newLoginFixture(t, true)
	owner := f.login(testSubject)
	if w := f.addSet(owner, map[string]string{csrfHeader: f.csrf(owner)}, nil); w.Code != http.StatusOK {
		t.Fatalf("owner write: status %d", w.Code)
	}
	sessions, err := f.services.ForUser(training.OwnerUserID).ListSessions(context.Background(), training.ListFilter{Limit: 1})
	if err != nil || len(sessions) != 1 {
		t.Fatalf("owner sessions: %v %v", sessions, err)
	}
	guest := f.login(guestSubject)
	for _, path := range []string{"/", "/history", "/plans", "/exercise-info?name=press+secreto"} {
		w := f.do(http.MethodGet, path, nil, []*http.Cookie{guest}, nil)
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "press secreto") {
			t.Errorf("guest %s: status %d, sees owner data=%v", path, w.Code, strings.Contains(w.Body.String(), "press secreto"))
		}
	}
	if w := f.do(http.MethodGet, fmt.Sprintf("/s/%d", sessions[0].ID), nil, []*http.Cookie{guest}, nil); w.Code != http.StatusNotFound {
		t.Errorf("guest opening the owner's session: status %d", w.Code)
	}
	if w := f.do(http.MethodGet, "/", nil, []*http.Cookie{owner}, nil); !strings.Contains(w.Body.String(), "press secreto") {
		t.Error("the owner lost sight of their own set")
	}
}

func TestTheRootNeverServesTheWholeDatabase(t *testing.T) {
	f := newLoginFixture(t, false)
	owner := f.login(testSubject)
	if w := f.do(http.MethodGet, "/export.db", nil, []*http.Cookie{owner}, nil); w.Code != http.StatusNotFound {
		t.Fatalf("export at the root: status %d", w.Code)
	}
	if w := f.do(http.MethodGet, "/history", nil, []*http.Cookie{owner}, nil); strings.Contains(w.Body.String(), "export.db") {
		t.Fatal("history links to a backup the root does not serve")
	}
}

func TestSignedInResponsesNameTheirOfflineCachePartition(t *testing.T) {
	f := newLoginFixture(t, true)
	owner, guest := f.login(testSubject), f.login(guestSubject)
	partition := func(c *http.Cookie, path string) string {
		w := f.do(http.MethodGet, path, nil, []*http.Cookie{c}, nil)
		if w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s Cache-Control = %q", path, w.Header().Get("Cache-Control"))
		}
		return w.Header().Get("X-Cache-Partition")
	}
	a, b := partition(owner, "/"), partition(guest, "/")
	if a == "" || b == "" || a == b {
		t.Fatalf("partitions %q and %q", a, b)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_]+-\d+$`).MatchString(a) {
		t.Fatalf("partition %q is not name-expiry", a)
	}
	if again := partition(owner, "/history"); again != a {
		t.Fatalf("one session changed partition: %q then %q", a, again)
	}
	// Anything served without a session carries no partition, so it is never
	// filed under anyone's data.
	for _, path := range []string{"/sw.js", "/manifest.webmanifest", "/static/app.js", "/logged-out", "/login"} {
		if w := f.do(http.MethodGet, path, nil, nil, nil); w.Header().Get("X-Cache-Partition") != "" {
			t.Errorf("%s has a partition", path)
		}
	}
	if w := f.do(http.MethodGet, "/logged-out", nil, nil, nil); w.Code != http.StatusOK || w.Header().Get("X-Cache-Clear") != "1" {
		t.Fatalf("logged-out page: status %d, X-Cache-Clear %q", w.Code, w.Header().Get("X-Cache-Clear"))
	}
}

func TestPublicAppFilesNeedNoSession(t *testing.T) {
	f := newLoginFixture(t, false)
	for _, path := range []string{"/sw.js", "/manifest.webmanifest", "/static/app.css"} {
		if w := f.do(http.MethodGet, path, nil, nil, nil); w.Code != http.StatusOK {
			t.Errorf("%s: status %d", path, w.Code)
		}
	}
}

func TestExistingRoutesBehaveAsBeforeAlongsideTheLogin(t *testing.T) {
	f := newLoginFixture(t, false)
	session := f.login(testSubject)

	if w := f.do(http.MethodGet, "/health", nil, nil, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ok"`) {
		t.Errorf("/health: status %d", w.Code)
	}
	if w := f.do(http.MethodGet, "/.well-known/oauth-protected-resource/mcp", nil, nil, nil); w.Code != http.StatusOK {
		t.Errorf("metadata: status %d", w.Code)
	}
	w := f.do(http.MethodPost, "/mcp", nil, nil, nil)
	if w.Code != http.StatusUnauthorized || !strings.Contains(w.Header().Get("WWW-Authenticate"), "resource_metadata") {
		t.Errorf("anonymous /mcp: status %d", w.Code)
	}
	// A browser session is not an MCP credential.
	if w := f.do(http.MethodPost, "/mcp", nil, []*http.Cookie{session}, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("/mcp with a session cookie: status %d", w.Code)
	}
	if got := listPlans(t, f.handler, staticToken); !strings.Contains(got, "structuredContent") {
		t.Errorf("static token on /mcp: %s", got)
	}
}

// The secret mount is gone: its prefix is an ordinary unknown path at the root,
// and the static token opens no part of the web app.
func TestTheFormerSecretPrefixServesNothing(t *testing.T) {
	f := newLoginFixture(t, false)
	session := f.login(testSubject)
	csrf := f.csrf(session)
	bearer := map[string]string{"Authorization": "Bearer " + staticToken}
	for _, path := range []string{"/app/", "/app/export.db", "/app/history"} {
		if w := f.do(http.MethodGet, path, nil, nil, bearer); w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
			t.Errorf("%s with the static token: status %d, location %q", path, w.Code, w.Header().Get("Location"))
		}
		if w := f.do(http.MethodGet, path, nil, []*http.Cookie{session}, nil); w.Code != http.StatusNotFound {
			t.Errorf("%s signed in: status %d", path, w.Code)
		}
	}
	if w := f.do(http.MethodPost, "/app/sets", setFormValues(), nil, mergeHeaders(bearer, map[string]string{"HX-Request": "true"})); w.Code != http.StatusUnauthorized {
		t.Errorf("write under the prefix with the static token: status %d", w.Code)
	}
	if w := f.do(http.MethodPost, "/app/sets", setFormValues(), []*http.Cookie{session}, map[string]string{"HX-Request": "true", "X-CSRF-Token": csrf}); w.Code != http.StatusNotFound {
		t.Errorf("write under the prefix signed in: status %d", w.Code)
	}
	if n := f.ownerSetCount(); n != 0 {
		t.Errorf("writes under the prefix landed %d sets for the owner", n)
	}
}

func setFormValues() url.Values {
	return url.Values{"exercise": {"banca"}, "weight_kg": {"60"}, "reps": {"5"}, "rpe": {"8"}}
}

func mergeHeaders(a, b map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// Clients probe these for OAuth and MCP discovery; a login page is no answer.
func TestDiscoveryPathsStayMissingBesideTheLogin(t *testing.T) {
	f := newLoginFixture(t, false)
	for _, path := range []string{"/.well-known/oauth-authorization-server", "/.well-known/openid-configuration", "/mcp/", "/mcp/x"} {
		if w := f.do(http.MethodGet, path, nil, nil, nil); w.Code != http.StatusNotFound {
			t.Errorf("%s: status %d", path, w.Code)
		}
	}
}

func TestRoutesWithoutTheLoginKeepUnknownPathsMissing(t *testing.T) {
	handler := Routes(http.NotFoundHandler(), false, staticToken)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Code != http.StatusNotFound {
		t.Fatalf("root without the login: status %d", w.Code)
	}
}
