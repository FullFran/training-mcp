package connector

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func oauthFor(f *fakeRemote) *OAuthClient {
	return &OAuthClient{Guard: testGuard(f.srv), RedirectURL: "https://app.example.test/coach/connectors/callback", ClientName: "training-mcp"}
}

func TestOAuthFullFlowWithPKCEAndRotatingRefresh(t *testing.T) {
	f := newFakeRemote(t)
	o := oauthFor(f)
	ctx := context.Background()

	needs, prm, err := o.Probe(ctx, f.URL())
	if err != nil || !needs || prm != f.srv.URL+"/.well-known/oauth-protected-resource/mcp" {
		t.Fatalf("probe = %v %q %v", needs, prm, err)
	}
	d, err := o.Discover(ctx, f.URL(), prm)
	if err != nil {
		t.Fatal(err)
	}
	if d.Resource != f.URL() || d.TokenEndpoint != f.srv.URL+"/token" || d.Issuer != f.srv.URL {
		t.Fatalf("discovery = %+v", d)
	}
	c, err := o.Register(ctx, d)
	if err != nil || c.ID == "" {
		t.Fatalf("register = %+v %v", c, err)
	}
	verifier, challenge := PKCE()
	code, state := f.approve(o.AuthorizeURL(d, c, "st-1", challenge))
	if state != "st-1" {
		t.Fatalf("state = %q", state)
	}
	// A wrong verifier is refused, and the refusal text does not leak.
	if _, err := o.Exchange(ctx, d, c, code, "wrong"); !errors.Is(err, ErrToken) || strings.Contains(err.Error(), "leak") {
		t.Fatalf("wrong verifier = %v", err)
	}
	code, _ = f.approve(o.AuthorizeURL(d, c, "st-2", challenge))
	tok, err := o.Exchange(ctx, d, c, code, verifier)
	if err != nil || tok.Access == "" || tok.Refresh == "" || tok.Expires == 0 {
		t.Fatalf("exchange = %+v %v", tok, err)
	}
	next, err := o.Refresh(ctx, d, c, tok.Refresh)
	if err != nil || next.Access == tok.Access || next.Refresh == tok.Refresh {
		t.Fatalf("refresh = %+v %v", next, err)
	}
	// The old refresh token was single use.
	if _, err := o.Refresh(ctx, d, c, tok.Refresh); !errors.Is(err, ErrToken) {
		t.Fatalf("reused refresh = %v", err)
	}
}

func TestDiscoveryRefusals(t *testing.T) {
	ctx := context.Background()
	cases := map[string]func(*fakeRemote){
		"issuer mismatch":         func(f *fakeRemote) { f.IssuerOverride = "https://evil.example.test" },
		"no PKCE S256":            func(f *fakeRemote) { f.NoS256 = true },
		"token endpoint internal": func(f *fakeRemote) { f.TokenEndpointOverride = "https://169.254.169.254/token" },
		"token endpoint http":     func(f *fakeRemote) { f.TokenEndpointOverride = "http://example.test/token" },
	}
	for name, setup := range cases {
		f := newFakeRemote(t)
		setup(f)
		if _, err := oauthFor(f).Discover(ctx, f.URL(), ""); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Resource metadata the server points at must pass the guard too.
	f := newFakeRemote(t)
	if _, err := oauthFor(f).Discover(ctx, f.URL(), "https://10.0.0.1/.well-known/oauth-protected-resource"); err == nil {
		t.Error("internal resource metadata URL followed")
	}
}

func TestProbeOpenServer(t *testing.T) {
	f := newFakeRemote(t)
	f.Open = true
	needs, _, err := oauthFor(f).Probe(context.Background(), f.URL())
	if err != nil || needs {
		t.Fatalf("open server: %v %v", needs, err)
	}
}

func TestBearerParam(t *testing.T) {
	got := bearerParam([]string{`Basic realm="x"`, `Bearer error="invalid_token", resource_metadata="https://a.test/.well-known/oauth-protected-resource"`}, "resource_metadata")
	if got != "https://a.test/.well-known/oauth-protected-resource" {
		t.Fatalf("got %q", got)
	}
}
