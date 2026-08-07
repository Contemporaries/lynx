package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeFingerprint(t *testing.T) {
	got := NormalizeFingerprint("AB:CD:Ef:12")
	if got != "abcdef12" {
		t.Fatalf("got %q", got)
	}
}

func TestNormalizeClientSubscribeOrInline(t *testing.T) {
	if _, err := NormalizeClient(&Client{}); err == nil {
		t.Fatal("expected error")
	}
	c, err := NormalizeClient(&Client{SubscribeURL: "https://example.com/s/t"})
	if err != nil {
		t.Fatal(err)
	}
	if c.SOCKSListen == "" {
		t.Fatal("defaults")
	}
	c2, err := NormalizeClient(&Client{
		Certificate: "c", Key: "k", CertificateAuthority: "a",
		WSURL: "wss://x/_lynx/v1/connect",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !c2.HasInlineCredentials() || c2.WSInnerServerName != "lynx.internal" {
		t.Fatalf("%+v", c2)
	}
}

func TestLoadServerFingerprintClients(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "server.json")
	content := `{
  "clients": {
    "laptop": {
      "certificate_sha256": "AA:BB:CC",
      "enabled": true
    }
  }
}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadServer(path)
	if err != nil {
		t.Fatal(err)
	}
	auth := cfg.Clients["laptop"]
	if auth.CertificateSHA256 != "aabbcc" || !auth.Enabled {
		t.Fatalf("%+v", auth)
	}
}
