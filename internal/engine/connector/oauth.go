package connector

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// OAuth errors carry no text from the remote side: a response body can quote
// anything, so it never reaches a log or a browser.
var (
	ErrDiscovery    = errors.New("connector: authorization discovery failed")
	ErrRegistration = errors.New("connector: client registration failed")
	ErrToken        = errors.New("connector: token request failed")
	ErrUnreachable  = errors.New("connector: server unreachable")
)

const (
	maxMetadataBytes = 256 << 10
	oauthTimeout     = 15 * time.Second
	// protocolVersion is the MCP revision the host speaks when it probes a server.
	protocolVersion = "2025-06-18"
)

// Discovery is what the host learned about a server's authorization: the
// resource indicator to ask tokens for (RFC 8707) and the authorization server's
// endpoints (RFC 8414). It is kept with the person's tokens in the vault.
type Discovery struct {
	Resource              string   `json:"resource"`
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	RegistrationEndpoint  string   `json:"registration_endpoint,omitempty"`
	Scopes                []string `json:"scopes,omitempty"`
}

// Client is the OAuth client the host registered for one person and one server.
type Client struct {
	ID     string `json:"client_id"`
	Secret string `json:"client_secret,omitempty"`
}

// Tokens are a person's tokens for one server.
type Tokens struct {
	Access  string `json:"access"`
	Refresh string `json:"refresh,omitempty"`
	// Expires is Unix milliseconds; zero when the server gave no lifetime.
	Expires int64 `json:"expires,omitempty"`
}

// OAuthClient speaks to authorization servers through a Guard.
type OAuthClient struct {
	Guard *Guard
	// RedirectURL is where authorization servers send the person back.
	RedirectURL string
	ClientName  string
	Now         func() time.Time
}

func (o *OAuthClient) http() *http.Client { return o.Guard.Client(oauthTimeout) }

func (o *OAuthClient) now() time.Time {
	if o.Now != nil {
		return o.Now()
	}
	return time.Now()
}

// Probe asks a server to initialize without credentials. It answers whether
// the server needs authorization and, if it says so, where its protected
// resource metadata is (RFC 9728 WWW-Authenticate resource_metadata).
func (o *OAuthClient) Probe(ctx context.Context, serverURL string) (needsAuth bool, resourceMetadata string, err error) {
	resp, err := postJSONRPC(ctx, o.http(), serverURL, "", initializeRequest)
	if err != nil {
		return false, "", ErrUnreachable
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized:
		return true, bearerParam(resp.Header.Values("WWW-Authenticate"), "resource_metadata"), nil
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return false, "", nil
	case resp.StatusCode == http.StatusForbidden:
		return true, bearerParam(resp.Header.Values("WWW-Authenticate"), "resource_metadata"), nil
	}
	return false, "", ErrUnreachable
}

var initializeRequest = map[string]any{
	"jsonrpc": "2.0", "id": 1, "method": "initialize",
	"params": map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "training-mcp", "version": "1"},
	},
}

func postJSONRPC(ctx context.Context, c *http.Client, target, bearer string, body any) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(string(b)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", protocolVersion)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	return c.Do(req)
}

// bearerParam finds a parameter of a Bearer challenge, quoted or not.
func bearerParam(challenges []string, name string) string {
	for _, ch := range challenges {
		rest, ok := cutFold(strings.TrimSpace(ch), "bearer")
		if !ok {
			continue
		}
		for _, part := range strings.Split(rest, ",") {
			k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
			if ok && strings.EqualFold(strings.TrimSpace(k), name) {
				return strings.Trim(strings.TrimSpace(v), `"`)
			}
		}
	}
	return ""
}

func cutFold(s, prefix string) (string, bool) {
	if len(s) < len(prefix) || !strings.EqualFold(s[:len(prefix)], prefix) {
		return "", false
	}
	return s[len(prefix):], true
}

func (o *OAuthClient) getJSON(ctx context.Context, target string, v any) error {
	if _, err := o.Guard.CheckURL(target); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := o.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return ErrDiscovery
	}
	b, err := readLimited(resp.Body, maxMetadataBytes)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

// wellKnown inserts a well-known segment between a URL's host and its path
// (RFC 8414 section 3.1, RFC 9728 section 3.1).
func wellKnown(base *url.URL, suffix string) string {
	u := url.URL{Scheme: base.Scheme, Host: base.Host, Path: "/.well-known/" + suffix + strings.TrimSuffix(base.Path, "/")}
	return u.String()
}

func origin(u *url.URL) string { return u.Scheme + "://" + u.Host }

// Discover finds the server's authorization server and its endpoints: from
// the protected resource metadata the server pointed at, else from the
// well-known locations of the server's URL, else (older servers) the server's
// own origin as the authorization server. Every URL it follows passes the Guard.
func (o *OAuthClient) Discover(ctx context.Context, serverURL, resourceMetadata string) (Discovery, error) {
	server, err := o.Guard.CheckURL(serverURL)
	if err != nil {
		return Discovery{}, err
	}
	var prm struct {
		Resource             string   `json:"resource"`
		AuthorizationServers []string `json:"authorization_servers"`
		ScopesSupported      []string `json:"scopes_supported"`
	}
	candidates := []string{wellKnown(server, "oauth-protected-resource"), origin(server) + "/.well-known/oauth-protected-resource"}
	if resourceMetadata != "" {
		candidates = append([]string{resourceMetadata}, candidates...)
	}
	found := false
	for _, c := range candidates {
		err := o.getJSON(ctx, c, &prm)
		if err == nil && len(prm.AuthorizationServers) > 0 {
			found = true
			break
		}
		if errors.Is(err, ErrBlockedAddress) || errors.Is(err, ErrBadURL) {
			return Discovery{}, err
		}
	}
	d := Discovery{Resource: canonicalResource(server)}
	issuer := origin(server)
	if found {
		// The metadata must be about this server (RFC 9728 section 3.3).
		res, err := url.Parse(prm.Resource)
		if err != nil || origin(res) != origin(server) {
			return Discovery{}, ErrDiscovery
		}
		d.Resource = prm.Resource
		d.Scopes = prm.ScopesSupported
		issuer = prm.AuthorizationServers[0]
	}
	as, err := o.Guard.CheckURL(issuer)
	if err != nil {
		return Discovery{}, err
	}
	var meta struct {
		Issuer                        string   `json:"issuer"`
		AuthorizationEndpoint         string   `json:"authorization_endpoint"`
		TokenEndpoint                 string   `json:"token_endpoint"`
		RegistrationEndpoint          string   `json:"registration_endpoint"`
		CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported"`
		ScopesSupported               []string `json:"scopes_supported"`
	}
	metaFound := false
	for _, c := range []string{wellKnown(as, "oauth-authorization-server"), wellKnown(as, "openid-configuration"), strings.TrimSuffix(as.String(), "/") + "/.well-known/openid-configuration"} {
		if err := o.getJSON(ctx, c, &meta); err == nil && meta.Issuer != "" {
			metaFound = true
			break
		}
	}
	if !metaFound || strings.TrimSuffix(meta.Issuer, "/") != strings.TrimSuffix(as.String(), "/") {
		return Discovery{}, ErrDiscovery
	}
	// PKCE with S256 is required; a server that does not say it supports it is refused.
	s256 := false
	for _, m := range meta.CodeChallengeMethodsSupported {
		s256 = s256 || m == "S256"
	}
	if !s256 {
		return Discovery{}, ErrDiscovery
	}
	for _, e := range []string{meta.AuthorizationEndpoint, meta.TokenEndpoint} {
		if _, err := o.Guard.CheckURL(e); err != nil {
			return Discovery{}, err
		}
	}
	if meta.RegistrationEndpoint != "" {
		if _, err := o.Guard.CheckURL(meta.RegistrationEndpoint); err != nil {
			return Discovery{}, err
		}
	}
	d.Issuer = meta.Issuer
	d.AuthorizationEndpoint, d.TokenEndpoint, d.RegistrationEndpoint = meta.AuthorizationEndpoint, meta.TokenEndpoint, meta.RegistrationEndpoint
	if len(d.Scopes) == 0 {
		d.Scopes = meta.ScopesSupported
	}
	return d, nil
}

// canonicalResource is the server URL without query or fragment (RFC 8707).
func canonicalResource(u *url.URL) string {
	c := url.URL{Scheme: u.Scheme, Host: strings.ToLower(u.Host), Path: u.Path}
	return c.String()
}

// Register registers a public client with dynamic client registration (RFC 7591).
func (o *OAuthClient) Register(ctx context.Context, d Discovery) (Client, error) {
	if d.RegistrationEndpoint == "" {
		return Client{}, ErrRegistration
	}
	body, _ := json.Marshal(map[string]any{
		"client_name":                o.ClientName,
		"redirect_uris":              []string{o.RedirectURL},
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.RegistrationEndpoint, strings.NewReader(string(body)))
	if err != nil {
		return Client{}, ErrRegistration
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := o.http().Do(req)
	if err != nil {
		return Client{}, ErrRegistration
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return Client{}, ErrRegistration
	}
	b, err := readLimited(resp.Body, maxMetadataBytes)
	if err != nil {
		return Client{}, ErrRegistration
	}
	var c Client
	if json.Unmarshal(b, &c) != nil || c.ID == "" || len(c.ID) > 1024 || len(c.Secret) > 4096 {
		return Client{}, ErrRegistration
	}
	return c, nil
}

// PKCE makes a verifier and its S256 challenge (RFC 7636).
func PKCE() (verifier, challenge string) {
	verifier = randomToken(32)
	sum := sha256.Sum256([]byte(verifier))
	return verifier, base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// AuthorizeURL is where the person's browser goes to approve the client.
func (o *OAuthClient) AuthorizeURL(d Discovery, c Client, state, challenge string) string {
	u, _ := url.Parse(d.AuthorizationEndpoint)
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", c.ID)
	q.Set("redirect_uri", o.RedirectURL)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("resource", d.Resource)
	if len(d.Scopes) > 0 {
		q.Set("scope", strings.Join(d.Scopes, " "))
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// Exchange trades an authorization code for tokens.
func (o *OAuthClient) Exchange(ctx context.Context, d Discovery, c Client, code, verifier string) (Tokens, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {o.RedirectURL}, "code_verifier": {verifier}}
	return o.token(ctx, d, c, form, "")
}

// Refresh trades a refresh token for new tokens. A server that does not rotate
// the refresh token keeps the old one valid.
func (o *OAuthClient) Refresh(ctx context.Context, d Discovery, c Client, refresh string) (Tokens, error) {
	if refresh == "" {
		return Tokens{}, ErrToken
	}
	return o.token(ctx, d, c, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}}, refresh)
}

func (o *OAuthClient) token(ctx context.Context, d Discovery, c Client, form url.Values, oldRefresh string) (Tokens, error) {
	if _, err := o.Guard.CheckURL(d.TokenEndpoint); err != nil {
		return Tokens{}, err
	}
	form.Set("client_id", c.ID)
	form.Set("resource", d.Resource)
	if c.Secret != "" {
		form.Set("client_secret", c.Secret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.TokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return Tokens{}, ErrToken
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := o.http().Do(req)
	if err != nil {
		return Tokens{}, ErrToken
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Tokens{}, ErrToken
	}
	b, err := readLimited(resp.Body, maxMetadataBytes)
	if err != nil {
		return Tokens{}, ErrToken
	}
	var t struct {
		AccessToken  string `json:"access_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		RefreshToken string `json:"refresh_token"`
	}
	if json.Unmarshal(b, &t) != nil || t.AccessToken == "" || len(t.AccessToken) > 16384 || len(t.RefreshToken) > 16384 || !strings.EqualFold(t.TokenType, "bearer") {
		return Tokens{}, ErrToken
	}
	out := Tokens{Access: t.AccessToken, Refresh: t.RefreshToken}
	if out.Refresh == "" {
		out.Refresh = oldRefresh
	}
	if t.ExpiresIn > 0 {
		out.Expires = o.now().Add(time.Duration(t.ExpiresIn) * time.Second).UnixMilli()
	}
	return out, nil
}
