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
