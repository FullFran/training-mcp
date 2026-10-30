package config

import "testing"

func TestLoadDefaultsAndRequiresToken(t *testing.T) {
	t.Setenv("MCP_BEARER_TOKEN", "secret")
	t.Setenv("MCP_AUTH_DISABLED", "")
	c, err := Load("")
	if err != nil || c.Addr != ":8080" || c.Token != "secret" {
		t.Fatalf("config=%#v err=%v", c, err)
	}
	t.Setenv("MCP_BEARER_TOKEN", "")
	if _, err := Load(""); err == nil {
		t.Fatal("missing token should fail")
	}
}

func TestLoadNormalizesWebBasePath(t *testing.T) {
	for _, tt := range []struct {
		name, env, want string
		wantErr         bool
	}{
		{name: "unset disables the web UI", env: "", want: ""},
		{name: "bare slashes disable it too", env: "/", want: ""},
		{name: "adds the leading slash", env: "app", want: "/app"},
		{name: "strips the trailing slash", env: "/app/", want: "/app"},
		{name: "keeps a normalized value", env: "/a1b2c3", want: "/a1b2c3"},
		{name: "rejects a query separator", env: "/app?x", wantErr: true},
		{name: "rejects a fragment separator", env: "/app#x", wantErr: true},
		{name: "rejects spaces", env: "/my app", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MCP_BEARER_TOKEN", "secret")
			t.Setenv("WEB_BASE_PATH", tt.env)
			cfg, err := Load(":8080")
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Load() error = nil, want an error for %q", tt.env)
				}
				return
			}
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.WebBasePath != tt.want {
				t.Fatalf("WebBasePath = %q, want %q", cfg.WebBasePath, tt.want)
			}
		})
	}
}

func TestLoadRequiresOAuthSettingsTogether(t *testing.T) {
	t.Setenv("MCP_BEARER_TOKEN", "x")
	t.Setenv("MCP_AUTH_DISABLED", "")
	full := map[string]string{
		"MCP_OAUTH_ISSUER":   "https://auth.example.test",
		"MCP_OAUTH_RESOURCE": "https://training.example.test/mcp",
		"MCP_OAUTH_SUBJECT":  "user_owner",
	}
	for missing := range full {
		for k, v := range full {
			if k == missing {
				v = ""
			}
			t.Setenv(k, v)
		}
		if _, err := Load(":0"); err == nil {
			t.Errorf("accepted a configuration without %s", missing)
		}
	}
	for k, v := range full {
		t.Setenv(k, v)
	}
	cfg, err := Load(":0")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OAuthIssuer != full["MCP_OAUTH_ISSUER"] || cfg.OAuthResource != full["MCP_OAUTH_RESOURCE"] || cfg.OAuthSubject != full["MCP_OAUTH_SUBJECT"] {
		t.Fatalf("cfg = %+v", cfg)
	}
	t.Setenv("MCP_OAUTH_ISSUER", "http://auth.example.test")
	if _, err := Load(":0"); err == nil {
		t.Error("accepted a plain http issuer")
	}
}

func TestLoadLeavesOAuthOffByDefault(t *testing.T) {
	t.Setenv("MCP_BEARER_TOKEN", "x")
	t.Setenv("MCP_OAUTH_ISSUER", "")
	t.Setenv("MCP_OAUTH_RESOURCE", "")
	t.Setenv("MCP_OAUTH_SUBJECT", "")
	cfg, err := Load(":0")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OAuthIssuer != "" {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadRefusesOAuthWithAuthDisabled(t *testing.T) {
	t.Setenv("MCP_AUTH_DISABLED", "1")
	t.Setenv("MCP_BEARER_TOKEN", "")
	t.Setenv("MCP_OAUTH_ISSUER", "https://auth.example.test")
	t.Setenv("MCP_OAUTH_RESOURCE", "https://training.example.test/mcp")
	t.Setenv("MCP_OAUTH_SUBJECT", "user_owner")
	if _, err := Load(":0"); err == nil {
		t.Fatal("accepted OAuth settings on a server with authentication disabled")
	}
}

func TestLoadRejectsAResourceWithoutAHostOrPath(t *testing.T) {
	t.Setenv("MCP_BEARER_TOKEN", "x")
	t.Setenv("MCP_OAUTH_ISSUER", "https://auth.example.test")
	t.Setenv("MCP_OAUTH_SUBJECT", "user_owner")
	for _, resource := range []string{"https://", "https://x y/mcp", "https://training.example.test", "https://training.example.test/other"} {
		t.Setenv("MCP_OAUTH_RESOURCE", resource)
		if _, err := Load(":0"); err == nil {
			t.Errorf("accepted resource %q", resource)
		}
	}
}

func TestLoadTrimsOAuthValues(t *testing.T) {
	t.Setenv("MCP_BEARER_TOKEN", "x")
	t.Setenv("MCP_OAUTH_ISSUER", " https://auth.example.test ")
	t.Setenv("MCP_OAUTH_RESOURCE", "https://training.example.test/mcp\n")
	t.Setenv("MCP_OAUTH_SUBJECT", " user_owner ")
	cfg, err := Load(":0")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OAuthIssuer != "https://auth.example.test" || cfg.OAuthResource != "https://training.example.test/mcp" || cfg.OAuthSubject != "user_owner" {
		t.Fatalf("cfg = %+v", cfg)
	}
}
