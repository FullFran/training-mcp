package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type sample struct {
	Access string `json:"access"`
}

func TestVaultSecretsAreBoundToPersonAndName(t *testing.T) {
	dir := t.TempDir()
	v, _ := NewVault(dir, testKey(1))
	var got sample
	if ok, err := v.ReadSecret(1, "connector/7", &got); ok || err != nil {
		t.Fatalf("empty: %v %v", ok, err)
	}
	if err := v.WriteSecret(1, "connector/7", sample{Access: "tok-a"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := v.ReadSecret(1, "connector/7", &got); !ok || err != nil || got.Access != "tok-a" {
		t.Fatalf("round trip: %v %v %+v", ok, err, got)
	}
	// The other person, and another name, read nothing.
	if ok, err := v.ReadSecret(2, "connector/7", &got); ok || err != nil {
		t.Fatalf("other person: %v %v", ok, err)
	}
	if ok, err := v.ReadSecret(1, "connector/8", &got); ok || err != nil {
		t.Fatalf("other name: %v %v", ok, err)
	}
	// The ChatGPT credential is a different file.
	if c, err := v.Read(1); c != nil || err != nil {
		t.Fatalf("credential slot touched: %v %v", c, err)
	}

	// A file moved to another person's or another connector's name does not decrypt.
	src := v.secretPath(1, "connector/7")
	raw, _ := os.ReadFile(src)
	if err := os.WriteFile(v.secretPath(2, "connector/7"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, err := v.ReadSecret(2, "connector/7", &got); ok || err != ErrVaultUnreadable {
		t.Fatalf("moved to another person: %v %v", ok, err)
	}
	if err := os.WriteFile(v.secretPath(1, "connector/9"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if ok, err := v.ReadSecret(1, "connector/9", &got); ok || err != ErrVaultUnreadable {
		t.Fatalf("moved to another name: %v %v", ok, err)
	}
	info, err := os.Stat(src)
	if err != nil || info.Mode().Perm() != 0o600 || filepath.Dir(src) != dir {
		t.Fatalf("file mode %v, %v", info.Mode(), err)
	}
	if b, _ := os.ReadFile(src); strings.Contains(string(b), "tok-a") {
		t.Fatal("secret stored in clear")
	}
	if err := v.DeleteSecret(1, "connector/7"); err != nil {
		t.Fatal(err)
	}
	if ok, _ := v.ReadSecret(1, "connector/7", &got); ok {
		t.Fatal("deleted secret still read")
	}
	if err := v.DeleteSecret(1, "connector/7"); err != nil {
		t.Fatalf("second delete: %v", err)
	}
}
