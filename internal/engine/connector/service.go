package connector

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/fullfran/training-mcp/internal/engine"
	"github.com/fullfran/training-mcp/internal/training"
)

// Error is a refusal with a fixed code: the only text that reaches a browser.
type Error struct{ Code string }

func (e *Error) Error() string { return "connector: " + e.Code }

func fail(code string) error { return &Error{Code: code} }

// CodeOf maps any error of this package to its fixed code.
func CodeOf(err error) string {
	var e *Error
	switch {
	case errors.As(err, &e):
		return e.Code
	case errors.Is(err, ErrBlockedAddress):
		return "blocked_address"
	case errors.Is(err, ErrBadURL):
		return "bad_url"
	case errors.Is(err, training.ErrNotFound):
		return "not_found"
	case errors.Is(err, training.ErrConnectorExists):
		return "exists"
	case errors.Is(err, training.ErrTooManyConnectors):
		return "too_many"
	case errors.Is(err, ErrDiscovery):
		return "discovery_failed"
	case errors.Is(err, ErrRegistration):
		return "registration_failed"
	case errors.Is(err, ErrToken):
		return "token_failed"
	}
	return "unreachable"
}

// Store keeps connector records; every method names the person.
type Store interface {
	ListConnectors(ctx context.Context, userID int64) ([]training.Connector, error)
	Connector(ctx context.Context, userID, id int64) (training.Connector, error)
	ConnectorBySlug(ctx context.Context, userID int64, slug string) (training.Connector, error)
	CreateConnector(ctx context.Context, userID int64, c training.Connector) (training.Connector, error)
	SetConnectorState(ctx context.Context, userID, id int64, auth, state string) error
	DeleteConnector(ctx context.Context, userID, id int64) error
}

// Secrets is the encrypted per-person vault.
type Secrets interface {
	ReadSecret(userID int64, name string, value any) (bool, error)
	WriteSecret(userID int64, name string, value any) error
	DeleteSecret(userID int64, name string) error
}

// secret is everything about one connector that is not in its record: the
// static token, or the OAuth discovery, client and tokens.
type secret struct {
	Bearer    string     `json:"bearer,omitempty"`
	Discovery *Discovery `json:"discovery,omitempty"`
	Client    *Client    `json:"client,omitempty"`
	Tokens    *Tokens    `json:"tokens,omitempty"`
}

func secretName(id int64) string { return fmt.Sprintf("connector/%d", id) }

// View is a connector as the panel shows it. It holds no secret.
type View struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Label  string `json:"label"`
	URL    string `json:"url"`
	Auth   string `json:"auth"`
	State  string `json:"state"`
	Loaded *bool  `json:"loaded,omitempty"`
}

func viewOf(c training.Connector) View {
	return View{ID: c.ID, Name: c.Slug, Label: c.Label, URL: c.URL, Auth: c.Auth, State: c.State}
}

// Tool is one tool a connected server offers.
type Tool struct {
	Name        string `json:"name"`
	Model       string `json:"model"`
	Description string `json:"description,omitempty"`
}

// ConnectResult: connected now, or the URL the browser goes to to sign in.
type ConnectResult struct {
	State        string `json:"state"`
	AuthorizeURL string `json:"authorize_url,omitempty"`
}

type flow struct {
	userID    int64
	id        int64
	verifier  string
	discovery Discovery
	client    Client
	expires   time.Time
}

const (
	flowTTL          = 10 * time.Minute
	maxFlowsPerUser  = 3
	refreshMargin    = 60 * time.Second
	maxLabelRunes    = 40
	maxToolsBytes    = 1 << 20
	maxDescription   = 300
	toolsListTimeout = 20 * time.Second
)

// Service is everything a person does with their connectors. Every method
// takes the person the request was authenticated as.
type Service struct {
	Store   Store
	Secrets Secrets
	Guard   *Guard
	// OAuth is nil when the app has no public URL to come back to: then only
	// servers with a static token, or none, can be connected.
	OAuth *OAuthClient
	// Reload ends the person's coach session after a change, so the next turn
	// loads the current connectors.
	Reload func(userID int64)
	// Loaded reports which servers the person's coach session loaded.
	Loaded func(userID int64) map[string]bool
	// CatalogEntries replaces the built-in catalog when set (tests).
	CatalogEntries []CatalogEntry
	Now            func() time.Time

	mu    sync.Mutex
	flows map[string]*flow
	locks map[string]*sync.Mutex
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) reload(userID int64) {
	if s.Reload != nil {
		s.Reload(userID)
	}
}

// List is the person's connectors, with whether their coach session loaded each.
func (s *Service) List(ctx context.Context, userID int64) ([]View, error) {
	cs, err := s.Store.ListConnectors(ctx, userID)
	if err != nil {
		return nil, err
	}
	var loaded map[string]bool
	if s.Loaded != nil {
		loaded = s.Loaded(userID)
	}
	out := make([]View, 0, len(cs))
	for _, c := range cs {
		v := viewOf(c)
		if l, ok := loaded[c.Slug]; ok && c.State == training.ConnectorConnected {
			v.Loaded = &l
		}
		out = append(out, v)
	}
	return out, nil
}

// slugFor makes the server name from the label, else from the host.
func slugFor(label string, u *url.URL) string {
	clean := func(s string) string {
		var b strings.Builder
		for _, r := range strings.ToLower(s) {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			}
		}
		out := b.String()
		if out != "" && out[0] >= '0' && out[0] <= '9' {
			out = "s" + out
		}
		if len(out) > 24 {
			out = out[:24]
		}
		return out
	}
	if s := clean(label); s != "" {
		return s
	}
	labels := strings.Split(u.Hostname(), ".")
	if len(labels) >= 2 {
		return clean(labels[len(labels)-2])
	}
	return clean(u.Hostname())
}

// Add records a new server for the person. A static token, when given, goes to
// the vault; the record never holds it.
func (s *Service) Add(ctx context.Context, userID int64, rawURL, label, token string) (View, error) {
	u, err := s.Guard.Resolve(ctx, strings.TrimSpace(rawURL))
	if err != nil {
		return View{}, err
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = u.Hostname()
	}
	if !utf8.ValidString(label) || utf8.RuneCountInString(label) > maxLabelRunes || strings.ContainsFunc(label, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return View{}, fail("bad_label")
	}
	slug := slugFor(label, u)
	if !engine.ValidServerName(slug) {
		return View{}, fail("bad_label")
	}
	token = strings.TrimSpace(token)
	if len(token) > 8192 || strings.ContainsAny(token, "\r\n") {
		return View{}, fail("bad_token")
	}
	auth := training.ConnectorAuthOAuth
	if token != "" {
		auth = training.ConnectorAuthBearer
	}
	c, err := s.Store.CreateConnector(ctx, userID, training.Connector{Slug: slug, Label: label, URL: u.String(), Auth: auth, State: training.ConnectorDisconnected})
	if err != nil {
		return View{}, err
	}
	if token != "" {
		if err := s.Secrets.WriteSecret(userID, secretName(c.ID), secret{Bearer: token}); err != nil {
			s.Store.DeleteConnector(ctx, userID, c.ID)
			return View{}, fail("vault")
		}
	}
	return viewOf(c), nil
}

// Connect connects a server: with its static token, with no credential when
// it needs none, or by starting an OAuth sign-in whose URL the browser opens.
func (s *Service) Connect(ctx context.Context, userID, id int64) (ConnectResult, error) {
	c, err := s.Store.Connector(ctx, userID, id)
	if err != nil {
		return ConnectResult{}, err
	}
	var sec secret
	if _, err := s.Secrets.ReadSecret(userID, secretName(id), &sec); err != nil {
		return ConnectResult{}, fail("vault")
	}
	if sec.Bearer != "" {
		ok, err := s.initialize(ctx, c.URL, sec.Bearer)
		if err != nil {
			return ConnectResult{}, err
		}
		if !ok {
			return ConnectResult{}, fail("bad_token")
		}
		return s.connected(ctx, userID, c, training.ConnectorAuthBearer)
	}
	o := s.OAuth
	if o == nil {
		o = &OAuthClient{Guard: s.Guard}
	}
	needsAuth, prm, err := o.Probe(ctx, c.URL)
	if err != nil {
		return ConnectResult{}, err
	}
	if !needsAuth {
		return s.connected(ctx, userID, c, training.ConnectorAuthNone)
	}
	if s.OAuth == nil {
		return ConnectResult{}, fail("oauth_unavailable")
	}
	d, err := s.OAuth.Discover(ctx, c.URL, prm)
	if err != nil {
		return ConnectResult{}, err
	}
	// A client registered earlier with the same authorization server is reused.
	client := Client{}
	if sec.Client != nil && sec.Discovery != nil && sec.Discovery.Issuer == d.Issuer {
		client = *sec.Client
	} else if client, err = s.OAuth.Register(ctx, d); err != nil {
		return ConnectResult{}, err
	}
	sec.Discovery, sec.Client = &d, &client
	if err := s.Secrets.WriteSecret(userID, secretName(id), sec); err != nil {
		return ConnectResult{}, fail("vault")
	}
	verifier, challenge := PKCE()
	state := randomToken(32)
	s.mu.Lock()
	if s.flows == nil {
		s.flows = map[string]*flow{}
	}
	s.pruneFlows(userID)
	s.flows[state] = &flow{userID: userID, id: id, verifier: verifier, discovery: d, client: client, expires: s.now().Add(flowTTL)}
	s.mu.Unlock()
	return ConnectResult{State: "authorize", AuthorizeURL: s.OAuth.AuthorizeURL(d, client, state, challenge)}, nil
}

// pruneFlows drops expired sign-ins and keeps the person's newest few.
func (s *Service) pruneFlows(userID int64) {
	now := s.now()
	var mine []string
	for k, f := range s.flows {
		if !now.Before(f.expires) {
			delete(s.flows, k)
		} else if f.userID == userID {
			mine = append(mine, k)
		}
	}
	for len(mine) >= maxFlowsPerUser {
		oldest := 0
		for i, k := range mine {
			if s.flows[k].expires.Before(s.flows[mine[oldest]].expires) {
				oldest = i
			}
		}
		delete(s.flows, mine[oldest])
		mine = append(mine[:oldest], mine[oldest+1:]...)
	}
}

func (s *Service) connected(ctx context.Context, userID int64, c training.Connector, auth string) (ConnectResult, error) {
	if err := s.Store.SetConnectorState(ctx, userID, c.ID, auth, training.ConnectorConnected); err != nil {
		return ConnectResult{}, err
	}
	s.reload(userID)
	return ConnectResult{State: training.ConnectorConnected}, nil
}

// Callback finishes a sign-in. The state is single use, expires, and only
// the person who started the sign-in can finish it.
func (s *Service) Callback(ctx context.Context, userID int64, state, code string) (int64, error) {
	if state == "" || code == "" || len(code) > 4096 {
		return 0, fail("state_invalid")
	}
	s.mu.Lock()
	f, ok := s.flows[state]
	if ok && f.userID != userID {
		// Someone else's sign-in: refused and left for its owner.
		ok = false
		f = nil
	} else if ok {
		delete(s.flows, state)
	}
	s.mu.Unlock()
	if !ok || !s.now().Before(f.expires) {
		return 0, fail("state_invalid")
	}
	c, err := s.Store.Connector(ctx, userID, f.id)
	if err != nil {
		return 0, err
	}
	unlock := s.lock(userID, f.id)
	defer unlock()
	tokens, err := s.OAuth.Exchange(ctx, f.discovery, f.client, code, f.verifier)
	if err != nil {
		return 0, err
	}
	sec := secret{Discovery: &f.discovery, Client: &f.client, Tokens: &tokens}
	if err := s.Secrets.WriteSecret(userID, secretName(f.id), sec); err != nil {
		return 0, fail("vault")
	}
	if _, err := s.connected(ctx, userID, c, training.ConnectorAuthOAuth); err != nil {
		return 0, err
	}
	return f.id, nil
}

// lock serializes token use and refresh per person and connector: a rotating
// refresh token is never spent twice.
func (s *Service) lock(userID, id int64) func() {
	key := fmt.Sprintf("%d/%d", userID, id)
	s.mu.Lock()
	if s.locks == nil {
		s.locks = map[string]*sync.Mutex{}
	}
	l, ok := s.locks[key]
	if !ok {
		l = &sync.Mutex{}
		s.locks[key] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// Disconnect forgets the person's OAuth tokens for a server and keeps its
// record, its OAuth client registration and a static token, so Connect works
// again without retyping it; Remove deletes them all.
func (s *Service) Disconnect(ctx context.Context, userID, id int64) error {
	c, err := s.Store.Connector(ctx, userID, id)
	if err != nil {
		return err
	}
	unlock := s.lock(userID, id)
	defer unlock()
	var sec secret
	if _, err := s.Secrets.ReadSecret(userID, secretName(id), &sec); err == nil && sec.Bearer == "" {
		// The OAuth client registration is kept; the tokens are gone.
		sec.Tokens = nil
		if err := s.Secrets.WriteSecret(userID, secretName(id), sec); err != nil {
			return fail("vault")
		}
	}
	if err := s.Store.SetConnectorState(ctx, userID, id, c.Auth, training.ConnectorDisconnected); err != nil {
		return err
	}
	s.reload(userID)
	return nil
}

// Remove deletes a server, its record and every secret of it.
func (s *Service) Remove(ctx context.Context, userID, id int64) error {
	if _, err := s.Store.Connector(ctx, userID, id); err != nil {
		return err
	}
	unlock := s.lock(userID, id)
	defer unlock()
	if err := s.Secrets.DeleteSecret(userID, secretName(id)); err != nil {
		return fail("vault")
	}
	if err := s.Store.DeleteConnector(ctx, userID, id); err != nil {
		return err
	}
	s.reload(userID)
	return nil
}

// ForEngine lists the person's connected servers for a new coach session.
func (s *Service) ForEngine(userID int64) []engine.Server {
	cs, err := s.Store.ListConnectors(context.Background(), userID)
	if err != nil {
		return nil
	}
	var out []engine.Server
	for _, c := range cs {
		if c.State == training.ConnectorConnected {
			out = append(out, engine.Server{Name: c.Slug, Label: c.Label})
		}
	}
	return out
}

// target is where a connected server is and the Authorization it gets, with
// an OAuth token refreshed first when it is about to expire (or when force).
func (s *Service) target(ctx context.Context, userID int64, slug string, force bool) (training.Connector, string, error) {
	c, err := s.Store.ConnectorBySlug(ctx, userID, slug)
	if err != nil {
		return c, "", err
	}
	if c.State != training.ConnectorConnected {
		return c, "", training.ErrNotFound
	}
	if c.Auth == training.ConnectorAuthNone {
		return c, "", nil
	}
	unlock := s.lock(userID, c.ID)
	defer unlock()
	var sec secret
	if ok, err := s.Secrets.ReadSecret(userID, secretName(c.ID), &sec); err != nil || !ok {
		return c, "", fail("vault")
	}
	if sec.Bearer != "" {
		return c, "Bearer " + sec.Bearer, nil
	}
	if sec.Tokens == nil || sec.Discovery == nil || sec.Client == nil || s.OAuth == nil {
		return c, "", fail("token_failed")
	}
	expiring := sec.Tokens.Expires != 0 && time.UnixMilli(sec.Tokens.Expires).Sub(s.now()) < refreshMargin
	if force || expiring {
		next, err := s.OAuth.Refresh(ctx, *sec.Discovery, *sec.Client, sec.Tokens.Refresh)
		if err != nil {
			// The person signs in again; the stale tokens are dropped.
			sec.Tokens = nil
			s.Secrets.WriteSecret(userID, secretName(c.ID), sec)
			s.Store.SetConnectorState(ctx, userID, c.ID, c.Auth, training.ConnectorDisconnected)
			return c, "", err
		}
		sec.Tokens = &next
		if err := s.Secrets.WriteSecret(userID, secretName(c.ID), sec); err != nil {
			return c, "", fail("vault")
		}
	}
	return c, "Bearer " + sec.Tokens.Access, nil
}

// initialize reports whether a server accepts an initialize with this bearer.
func (s *Service) initialize(ctx context.Context, serverURL, bearer string) (bool, error) {
	resp, err := postJSONRPC(ctx, s.Guard.Client(toolsListTimeout), serverURL, bearer, initializeRequest)
	if err != nil {
		return false, ErrUnreachable
	}
	resp.Body.Close()
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return true, nil
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return false, nil
	}
	return false, ErrUnreachable
}

// Tools lists what a connected server offers, as the person and as the coach
// sees the names.
func (s *Service) Tools(ctx context.Context, userID, id int64) ([]Tool, error) {
	c, err := s.Store.Connector(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	tools, err := s.listTools(ctx, userID, c.Slug, false)
	var e *Error
	if errors.As(err, &e) && e.Code == "unauthorized" && c.Auth == training.ConnectorAuthOAuth {
		tools, err = s.listTools(ctx, userID, c.Slug, true)
	}
	return tools, err
}

func (s *Service) listTools(ctx context.Context, userID int64, slug string, force bool) ([]Tool, error) {
	c, auth, err := s.target(ctx, userID, slug, force)
	if err != nil {
		return nil, err
	}
	client := s.Guard.Client(toolsListTimeout)
	bearer := strings.TrimPrefix(auth, "Bearer ")
	call := func(sessionID string, body map[string]any) (*http.Response, error) {
		b, _ := json.Marshal(body)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("MCP-Protocol-Version", protocolVersion)
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if sessionID != "" {
			req.Header.Set("Mcp-Session-Id", sessionID)
		}
		return client.Do(req)
	}
	resp, err := call("", initializeRequest)
	if err != nil {
		return nil, ErrUnreachable
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fail("unauthorized")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, ErrUnreachable
	}
	session := resp.Header.Get("Mcp-Session-Id")
	if r, err := call(session, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}); err == nil {
		r.Body.Close()
	}
	resp, err = call(session, map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	if err != nil {
		return nil, ErrUnreachable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, ErrUnreachable
	}
	body, err := readLimited(resp.Body, maxToolsBytes)
	if err != nil {
		return nil, ErrUnreachable
	}
	var out struct {
		Result struct {
			Tools []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rpcPayload(resp.Header.Get("Content-Type"), body), &out); err != nil {
		return nil, ErrUnreachable
	}
	tools := []Tool{}
	for _, t := range out.Result.Tools {
		if t.Name == "" || len(t.Name) > 128 || !utf8.ValidString(t.Name) {
			continue
		}
		desc := t.Description
		if !utf8.ValidString(desc) {
			desc = ""
		}
		if r := []rune(desc); len(r) > maxDescription {
			desc = string(r[:maxDescription]) + "…"
		}
		tools = append(tools, Tool{Name: t.Name, Model: PiToolName(slug, t.Name), Description: desc})
	}
	return tools, nil
}

// rpcPayload is the JSON-RPC message of a response: the body itself, or the
// last data event of a server-sent event stream.
func rpcPayload(contentType string, body []byte) []byte {
	if !strings.HasPrefix(strings.ToLower(contentType), "text/event-stream") {
		return body
	}
	var data, last []byte
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 0, 64<<10), maxToolsBytes)
	for sc.Scan() {
		line := sc.Bytes()
		switch {
		case bytes.HasPrefix(line, []byte("data:")):
			data = append(data, bytes.TrimSpace(line[5:])...)
		case len(line) == 0 && len(data) > 0:
			last, data = data, nil
		}
	}
	if len(data) > 0 {
		last = data
	}
	return last
}
