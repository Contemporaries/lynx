package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestMergeClientPreservesSecrets(t *testing.T) {
	base := &Client{
		Mode:                 "auto",
		Certificate:          "CERT",
		Key:                  "KEY",
		CertificateAuthority: "CA",
		SOCKSListen:          "127.0.0.1:1080",
		HTTPListen:           "127.0.0.1:8080",
		Mgmt:                 MgmtConfig{Secret: "mgmt-secret", Listen: "127.0.0.1:9091"},
	}
	patch := []byte(`{"mode":"direct","key":"***","mgmt":{"secret":"***","allow_upgrade":true}}`)
	merged, err := MergeClientJSON(base, patch, false)
	if err != nil {
		t.Fatal(err)
	}
	if merged.Mode != "direct" {
		t.Fatalf("mode=%s", merged.Mode)
	}
	if merged.Key != "KEY" {
		t.Fatalf("key overwritten: %q", merged.Key)
	}
	if merged.Mgmt.Secret != "mgmt-secret" {
		t.Fatalf("mgmt secret overwritten: %q", merged.Mgmt.Secret)
	}
	if !merged.Mgmt.AllowUpgrade {
		t.Fatal("allow_upgrade not applied")
	}
}

func TestWriteAndRedactClient(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.json")
	cfg := &Client{
		Mode:                 "wss",
		Certificate:          "CERT",
		Key:                  "KEY",
		CertificateAuthority: "CA",
		WSURL:                "wss://cdn.example/_lynx/v1/connect",
		WSInnerServerName:    "lynx.internal",
		SOCKSListen:          "127.0.0.1:1080",
		HTTPListen:           "127.0.0.1:8080",
	}
	cfg, err := NormalizeClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteClient(path, cfg); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	view := RedactClient(cfg)
	if view["key"] != "***" {
		t.Fatalf("redact key=%v", view["key"])
	}
}
