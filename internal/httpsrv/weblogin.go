package httpsrv

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fullfran/training-mcp/internal/training"
)

const (
	// The __Host- prefix makes the browser refuse these cookies unless they are
	// Secure, host-only and on /, so no sibling subdomain can plant or read them.
	sessionCookieName = "__Host-training-session"
	loginCookieName   = "__Host-training-login"
	csrfHeader        = "X-CSRF-Token"
	csrfField         = "csrf_token"

	// sessionTTL matches the provider's own fixed session on the free plan, so
	// signing in here never outlives signing in there by much.
	sessionTTL = 7 * 24 * time.Hour
	// loginTTL bounds how long a started login can be finished.
	loginTTL = 10 * time.Minute
	// maxSessions bounds memory. Sessions are only made by people the provider
	// let in, so reaching it means something is wrong, not a busy day.
	maxSessions = 10000
)

// WebLoginConfig describes the provider's OAuth application for the web app. It
// trusts the same issuer and admits the same accounts as /mcp.
type WebLoginConfig struct {
	Issuer, ClientID, ClientSecret string
	// PublicURL is the origin the browser uses; the callback lives under it.
	PublicURL string
	// AuthorizeURL and TokenURL default to the provider's standard paths.
	AuthorizeURL, TokenURL string
	// Keys verifies ID tokens with the provider's signing keys.
	Keys    *OAuth
	Resolve training.UserResolver
	Now     func() time.Time
	Logger  *log.Logger
}

type webSession struct {
	userID int64
	csrf   string
	// partition names this session's offline cache in the service worker.
	partition string
	expires   time.Time
}

// WebLogin signs people into the web app with the provider and keeps them signed
// in with a cookie. Sessions live in memory: a restart signs everyone out, and
// signing in again is one round trip while the provider's session lasts.
type WebLogin struct {
	cfg    WebLoginConfig
	client *http.Client

	mu       sync.Mutex
	sessions map[string]*webSession
}

func NewWebLogin(cfg WebLoginConfig) *WebLogin {
	issuer := strings.TrimRight(cfg.Issuer, "/")
	if cfg.AuthorizeURL == "" {
		cfg.AuthorizeURL = issuer + "/oauth/authorize"
	}
	if cfg.TokenURL == "" {
		cfg.TokenURL = issuer + "/oauth/token"
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	cfg.PublicURL = strings.TrimRight(cfg.PublicURL, "/")
	return &WebLogin{
		cfg: cfg,
		client: &http.Client{
			Timeout:       10 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		sessions: map[string]*webSession{},
	}
}

// RootWeb is the app served at the root behind the login.
type RootWeb struct {
	Handler http.Handler
	Login   *WebLogin
}

type csrfKey struct{}

// CSRFToken is the token the request's session must send back with every write,
// for the pages to embed. It is empty outside a session.
func (l *WebLogin) CSRFToken(r *http.Request) string {
	token, _ := r.Context().Value(csrfKey{}).(string)
	return token
}

// Wrap serves the login's own routes and puts everything else behind a session.
// The app's shell files hold no one's data, so they stay public: the service
// worker and the manifest must load before anyone signs in.
func (l *WebLogin) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch path := r.URL.Path; {
		case path == "/login" && isRead(r):
			l.start(w, r)
		case path == "/auth/callback" && isRead(r):
			l.callback(w, r)
		case path == "/logged-out" && isRead(r):
			l.loggedOut(w)
		case path == "/sw.js" || path == "/manifest.webmanifest" || strings.HasPrefix(path, "/static/"):
			next.ServeHTTP(w, r)
		default:
			l.guard(w, r, next)
		}
	})
}

func isRead(r *http.Request) bool { return r.Method == http.MethodGet || r.Method == http.MethodHead }

func (l *WebLogin) guard(w http.ResponseWriter, r *http.Request, next http.Handler) {
	id, session := l.session(r)
	if session == nil {
		if isRead(r) && r.Header.Get("HX-Request") == "" {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		// A partial request or a write cannot follow a redirect into a login page.
		w.Header().Set("HX-Redirect", "/login")
		http.Error(w, "sign in first", http.StatusUnauthorized)
		return
	}
	if !isRead(r) && !csrfMatches(r, session.csrf) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	h := w.Header()
	// Signed-in pages are never kept by the browser's own cache, which knows
	// nothing of who is signed in; the service worker keeps them per session.
	h.Set("Cache-Control", "no-store")
	h.Set("X-Cache-Partition", session.partition+"-"+strconv.FormatInt(session.expires.Unix(), 10))
	if r.URL.Path == "/logout" && r.Method == http.MethodPost {
		l.logout(w, r, id)
		return
	}
	ctx := training.WithUser(r.Context(), session.userID)
	ctx = context.WithValue(ctx, csrfKey{}, session.csrf)
	next.ServeHTTP(w, r.WithContext(ctx))
}

// csrfMatches accepts the token from the header htmx sends or from the form
// field plain forms send, never from the URL, where it would leak into logs.
func csrfMatches(r *http.Request, want string) bool {
	got := r.Header.Get(csrfHeader)
	if got == "" {
		got = r.PostFormValue(csrfField)
	}
	return got != "" && len(got) == len(want) && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

func (l *WebLogin) session(r *http.Request) (string, *webSession) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		return "", nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	s, ok := l.sessions[c.Value]
	if !ok {
		return "", nil
	}
	if !l.cfg.Now().Before(s.expires) {
		delete(l.sessions, c.Value)
		return "", nil
	}
	return c.Value, s
}

// start sends the browser to the provider. state, nonce and the PKCE verifier
// stay in a cookie only this browser holds, so a login cannot be finished in
// another browser or replayed into this one.
func (l *WebLogin) start(w http.ResponseWriter, r *http.Request) {
	state, nonce, verifier := randomToken(), randomToken(), randomToken()
	challenge := sha256.Sum256([]byte(verifier))
	http.SetCookie(w, &http.Cookie{
		Name: loginCookieName, Value: state + "." + nonce + "." + verifier, Path: "/",
		MaxAge: int(loginTTL.Seconds()), HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {l.cfg.ClientID},
		"redirect_uri":          {l.redirectURI()},
		"scope":                 {"openid"},
		"state":                 {state},
		"nonce":                 {nonce},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(challenge[:])},
		"code_challenge_method": {"S256"},
	}
	http.Redirect(w, r, l.cfg.AuthorizeURL+"?"+q.Encode(), http.StatusSeeOther)
}

func (l *WebLogin) redirectURI() string { return l.cfg.PublicURL + "/auth/callback" }

func (l *WebLogin) callback(w http.ResponseWriter, r *http.Request) {
	// A login cookie is good for one attempt, whatever the outcome.
	http.SetCookie(w, &http.Cookie{Name: loginCookieName, Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	q := r.URL.Query()
	c, err := r.Cookie(loginCookieName)
	parts := []string{}
	if err == nil {
		parts = strings.Split(c.Value, ".")
	}
	if len(parts) != 3 || !equal(parts[0], q.Get("state")) {
		l.page(w, http.StatusBadRequest, "No se pudo entrar", "El inicio de sesión caducó o no empezó en este navegador.")
		return
	}
	nonce, verifier := parts[1], parts[2]
	if e := q.Get("error"); e != "" {
		l.cfg.Logger.Printf("web login refused by the provider: %s", clip(e))
		l.page(w, http.StatusUnauthorized, "No se pudo entrar", "El inicio de sesión no se completó.")
		return
	}
	// RFC 9207: a response naming another issuer came from a mix-up, not from us.
	if iss := q.Get("iss"); iss != "" && strings.TrimRight(iss, "/") != strings.TrimRight(l.cfg.Issuer, "/") {
		l.page(w, http.StatusBadRequest, "No se pudo entrar", "La respuesta no viene del proveedor esperado.")
		return
	}
	code := q.Get("code")
	if code == "" {
		l.page(w, http.StatusBadRequest, "No se pudo entrar", "Falta el código de inicio de sesión.")
		return
	}
	idToken, err := l.exchange(r.Context(), code, verifier)
	if err != nil {
		l.cfg.Logger.Printf("web login code exchange failed: %v", err)
		l.page(w, http.StatusBadGateway, "No se pudo entrar", "El proveedor de identidad no respondió bien. Prueba otra vez.")
		return
	}
	subject, err := l.cfg.Keys.verifyIDToken(idToken, l.cfg.ClientID, nonce)
	if err != nil {
		l.cfg.Logger.Printf("web login id token rejected: %v", err)
		l.page(w, http.StatusUnauthorized, "No se pudo entrar", "La identidad recibida no es válida.")
		return
	}
	userID, known, err := l.cfg.Resolve(r.Context(), subject)
	if err != nil {
		l.cfg.Logger.Printf("resolving a web user failed: %v", err)
		l.page(w, http.StatusServiceUnavailable, "No disponible", "No se pudo comprobar la cuenta. Prueba otra vez.")
		return
	}
	if !known {
		l.cfg.Logger.Printf("web login: subject %q has no account", clip(subject))
		l.page(w, http.StatusForbidden, "Sin acceso", "Esta cuenta no tiene acceso a la aplicación.")
		return
	}
	id, ok := l.newSession(userID)
	if !ok {
		l.cfg.Logger.Printf("web login: %d sessions open, refusing another", maxSessions)
		l.page(w, http.StatusServiceUnavailable, "No disponible", "Hay demasiadas sesiones abiertas.")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: id, Path: "/", MaxAge: int(sessionTTL.Seconds()),
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (l *WebLogin) newSession(userID int64) (string, bool) {
	now := l.cfg.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.sessions) >= maxSessions {
		for id, s := range l.sessions {
			if !now.Before(s.expires) {
				delete(l.sessions, id)
			}
		}
		if len(l.sessions) >= maxSessions {
			return "", false
		}
	}
	id := randomToken()
	l.sessions[id] = &webSession{
		userID: userID, csrf: randomToken(), partition: strings.ReplaceAll(randomToken()[:22], "-", "_"),
		expires: now.Add(sessionTTL),
	}
	return id, true
}

// exchange trades the code for the provider's ID token, proving this is the
// registered client and the browser that started the login.
func (l *WebLogin) exchange(ctx context.Context, code, verifier string) (string, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {l.redirectURI()},
		"code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, l.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(url.QueryEscape(l.cfg.ClientID), url.QueryEscape(l.cfg.ClientSecret))
	resp, err := l.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("token endpoint status %d", resp.StatusCode)
	}
	var body struct {
		IDToken string `json:"id_token"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", err
	}
	if body.IDToken == "" {
		return "", errors.New("token response has no id_token")
	}
	return body.IDToken, nil
}

// logout ends the session on the server and asks the browser to drop every
// copy of the app's pages and storage, so the next person on this device starts
// with nothing.
func (l *WebLogin) logout(w http.ResponseWriter, r *http.Request, id string) {
	l.mu.Lock()
	delete(l.sessions, id)
	l.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: sessionCookieName, Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
	w.Header().Del("X-Cache-Partition")
	w.Header().Set("Clear-Site-Data", `"cache", "storage"`)
	if r.Header.Get("HX-Request") != "" {
		w.Header().Set("HX-Redirect", "/logged-out")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/logged-out", http.StatusSeeOther)
}

// loggedOut is a page of its own rather than a redirect to /login, which would
// sign the same person straight back in while their provider session lasts.
// X-Cache-Clear tells the service worker to drop the signed-in pages, for
// browsers that ignore Clear-Site-Data.
func (l *WebLogin) loggedOut(w http.ResponseWriter) {
	w.Header().Set("X-Cache-Clear", "1")
	l.page(w, http.StatusOK, "Sesión cerrada", "Has cerrado sesión en este dispositivo.")
}

var pageTemplate = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="es">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<link rel="stylesheet" href="/static/app.css">
</head>
<body>
<main class="card">
<h1>{{.Title}}</h1>
<p>{{.Message}}</p>
<p><a href="/login">Entrar</a></p>
</main>
</body>
</html>
`))

func (l *WebLogin) page(w http.ResponseWriter, status int, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = pageTemplate.Execute(w, struct{ Title, Message string }{title, message})
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func equal(a, b string) bool {
	return a != "" && len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
