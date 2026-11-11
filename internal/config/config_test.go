package config

import (
	"strings"
	"testing"
)

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

// The secret web mount is gone. A leftover WEB_BASE_PATH, however malformed,
// must neither stop the server nor mount anything; it is only reported.
func TestLoadIgnoresWebBasePath(t *testing.T) {
	for _, tt := range []struct {
		name, env   string
		wantIgnored bool
	}{
		{name: "unset", env: "", wantIgnored: false},
		{name: "blank", env: "  ", wantIgnored: false},
		{name: "a former secret prefix", env: "/a1b2c3", wantIgnored: true},
		{name: "a value the old parser refused", env: "/app?x", wantIgnored: true},
		{name: "a bare slash", env: "/", wantIgnored: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MCP_BEARER_TOKEN", "secret")
			t.Setenv("WEB_BASE_PATH", tt.env)
			cfg, err := Load(":8080")
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.WebBasePathIgnored != tt.wantIgnored {
				t.Fatalf("WebBasePathIgnored = %v, want %v", cfg.WebBasePathIgnored, tt.wantIgnored)
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

func TestLoadReadsProvisioning(t *testing.T) {
	t.Setenv("MCP_BEARER_TOKEN", "x")
	t.Setenv("MCP_AUTH_DISABLED", "")
	set := func(provision string) {
		t.Setenv("MCP_OAUTH_ISSUER", "https://auth.example.test")
		t.Setenv("MCP_OAUTH_RESOURCE", "https://training.example.test/mcp")
		t.Setenv("MCP_OAUTH_SUBJECT", "user_owner")
		t.Setenv("MCP_OAUTH_PROVISION", provision)
	}
	set("")
	if cfg, err := Load(""); err != nil || cfg.OAuthProvision {
		t.Fatalf("default: provision = %v, err = %v", cfg.OAuthProvision, err)
	}
	set("1")
	if cfg, err := Load(""); err != nil || !cfg.OAuthProvision {
		t.Fatalf("on: provision = %v, err = %v", cfg.OAuthProvision, err)
	}
	// Who may hold an account must never depend on how a value was misspelled.
	set("true")
	if _, err := Load(""); err == nil {
		t.Fatal("accepted a provisioning value that is neither empty nor 1")
	}
	set("1")
	t.Setenv("MCP_OAUTH_ISSUER", "")
	t.Setenv("MCP_OAUTH_RESOURCE", "")
	t.Setenv("MCP_OAUTH_SUBJECT", "")
	if _, err := Load(""); err == nil {
		t.Fatal("accepted provisioning with OAuth off")
	}
}

func setWebLoginEnv(t *testing.T, id, secret string) {
	t.Helper()
	t.Setenv("MCP_BEARER_TOKEN", "x")
	t.Setenv("MCP_AUTH_DISABLED", "")
	t.Setenv("MCP_OAUTH_PROVISION", "")
	t.Setenv("MCP_OAUTH_ISSUER", "https://auth.example.test")
	t.Setenv("MCP_OAUTH_RESOURCE", "https://training.example.test/mcp")
	t.Setenv("MCP_OAUTH_SUBJECT", "user_owner")
	t.Setenv("WEB_OAUTH_CLIENT_ID", id)
	t.Setenv("WEB_OAUTH_CLIENT_SECRET", secret)
}

func TestLoadWebLoginDerivesItsURLFromTheResource(t *testing.T) {
	setWebLoginEnv(t, " client ", "secret")
	c, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if c.WebClientID != "client" || c.WebClientSecret != "secret" || c.WebPublicURL != "https://training.example.test" {
		t.Fatalf("config = %+v", c)
	}
}

func TestLoadWebLoginIsOffByDefault(t *testing.T) {
	setWebLoginEnv(t, "", "")
	c, err := Load("")
	if err != nil || c.WebClientID != "" || c.WebPublicURL != "" {
		t.Fatalf("config = %+v, err = %v", c, err)
	}
}

func TestLoadRefusesHalfAWebLogin(t *testing.T) {
	for name, env := range map[string][2]string{
		"no secret": {"client", ""},
		"no id":     {"", "secret"},
	} {
		t.Run(name, func(t *testing.T) {
			setWebLoginEnv(t, env[0], env[1])
			if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "WEB_OAUTH_CLIENT_ID and WEB_OAUTH_CLIENT_SECRET") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestLoadWebLoginNeedsTheMCPOAuthSettings(t *testing.T) {
	setWebLoginEnv(t, "client", "secret")
	t.Setenv("MCP_OAUTH_ISSUER", "")
	t.Setenv("MCP_OAUTH_RESOURCE", "")
	t.Setenv("MCP_OAUTH_SUBJECT", "")
	if _, err := Load(""); err == nil || !strings.Contains(err.Error(), "WEB_OAUTH_CLIENT_ID needs the MCP_OAUTH_* settings") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadNeverPrintsTheWebClientSecret(t *testing.T) {
	setWebLoginEnv(t, "", "do-not-print-me")
	if _, err := Load(""); err == nil || strings.Contains(err.Error(), "do-not-print-me") {
		t.Fatalf("err = %v", err)
	}
}

const testCoachKey = "AQIDBAUGBwgJCgsMDQ4PEBESExQVFhcYGRobHB0eHyA="

func TestLoadLeavesTheCoachOffByDefault(t *testing.T) {
	setWebLoginEnv(t, "client", "secret")
	t.Setenv("TRAINING_COACH", "")
	t.Setenv("TRAINING_COACH_KEY", testCoachKey)
	cfg, err := Load("")
	if err != nil || cfg.Coach.Enabled || cfg.Coach.Key != nil {
		t.Fatalf("coach = %+v, err %v", cfg.Coach, err)
	}
}

func TestLoadReadsTheCoach(t *testing.T) {
	setWebLoginEnv(t, "client", "secret")
	t.Setenv("TRAINING_DB_PATH", "/data/training.db")
	t.Setenv("TRAINING_COACH", "1")
	t.Setenv("TRAINING_COACH_KEY", testCoachKey)
	t.Setenv("TRAINING_COACH_MAX_PROCESSES", "")
	t.Setenv("TRAINING_COACH_IDLE_SECONDS", "")
	t.Setenv("TRAINING_COACH_NODE", "")
	t.Setenv("TRAINING_COACH_ENGINE", "")
	cfg, err := Load("127.0.0.1:9090")
	if err != nil {
		t.Fatal(err)
	}
	c := cfg.Coach
	if !c.Enabled || len(c.Key) != 32 || c.MaxProcesses != 2 || c.IdleSeconds != 300 || c.Node != "node" || c.Engine != "/app/engine/main.ts" {
		t.Fatalf("coach = %+v", c)
	}
	if c.DataDir != "/data/coach" || c.MCPURL != "http://127.0.0.1:9090/mcp" {
		t.Fatalf("dir %q url %q", c.DataDir, c.MCPURL)
	}
	t.Setenv("TRAINING_COACH_MAX_PROCESSES", "5")
	t.Setenv("TRAINING_COACH_IDLE_SECONDS", "60")
	cfg, err = Load(":8080")
	if err != nil || cfg.Coach.MaxProcesses != 5 || cfg.Coach.IdleSeconds != 60 || cfg.Coach.MCPURL != "http://127.0.0.1:8080/mcp" {
		t.Fatalf("coach = %+v, err %v", cfg.Coach, err)
	}
}

func TestLoadRefusesABrokenCoachConfiguration(t *testing.T) {
	for name, env := range map[string]map[string]string{
		"misspelled switch":  {"TRAINING_COACH": "yes"},
		"no key":             {"TRAINING_COACH_KEY": ""},
		"short key":          {"TRAINING_COACH_KEY": "c2hvcnQ="},
		"no web app":         {"WEB_OAUTH_CLIENT_ID": "", "WEB_OAUTH_CLIENT_SECRET": ""},
		"only the old mount": {"WEB_OAUTH_CLIENT_ID": "", "WEB_OAUTH_CLIENT_SECRET": "", "WEB_BASE_PATH": "app"},
		"zero processes":     {"TRAINING_COACH_MAX_PROCESSES": "0"},
		"bad idle":           {"TRAINING_COACH_IDLE_SECONDS": "soon"},
	} {
		t.Run(name, func(t *testing.T) {
			setWebLoginEnv(t, "client", "secret")
			t.Setenv("WEB_BASE_PATH", "")
			t.Setenv("TRAINING_COACH", "1")
			t.Setenv("TRAINING_COACH_KEY", testCoachKey)
			t.Setenv("TRAINING_COACH_MAX_PROCESSES", "")
			t.Setenv("TRAINING_COACH_IDLE_SECONDS", "")
			for k, v := range env {
				t.Setenv(k, v)
			}
			_, err := Load("")
			if err == nil {
				t.Fatal("accepted")
			}
			if strings.Contains(err.Error(), testCoachKey) {
				t.Fatal("error quotes the key")
			}
		})
	}
}
