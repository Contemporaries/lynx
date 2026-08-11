package mgmt_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Contemporaries/lynx/internal/config"
	"github.com/Contemporaries/lynx/internal/logx"
	"github.com/Contemporaries/lynx/internal/mgmt"
)

func TestMgmtHealthAndAuth(t *testing.T) {
	lx := logx.New(logx.LevelInfo)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "client.json")
	cfg := &config.Client{
		Mode:                 "direct",
		DirectAddr:           "127.0.0.1:8443",
		DirectServerName:     "lynx.internal",
		Certificate:          "-----BEGIN CERTIFICATE-----\nA\n-----END CERTIFICATE-----",
		Key:                  "-----BEGIN PRIVATE KEY-----\nB\n-----END PRIVATE KEY-----",
		CertificateAuthority: "-----BEGIN CERTIFICATE-----\nC\n-----END CERTIFICATE-----",
		SOCKSListen:          "127.0.0.1:0",
		HTTPListen:           "",
		Log:                  config.LogConfig{Level: "info"},
		Mgmt:                 config.MgmtConfig{Secret: "test-secret"},
	}
	cfg, err := config.NormalizeClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.WriteClient(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &mgmt.FileConfigStore{PathName: cfgPath, Role: mgmt.RoleClient, Client: cfg, Logger: lx}
	errCh := make(chan error, 1)
	go func() {
		errCh <- mgmt.ListenAndServe(ctx, mgmt.Options{
			Listen:     addr,
			Secret:     "test-secret",
			CORSOrigin: "*",
			Role:       mgmt.RoleClient,
			Logger:     lx,
			StartedAt:  time.Now(),
			Status:     func() map[string]any { return map[string]any{"ok": true} },
			Config:     store,
		})
	}()
	time.Sleep(100 * time.Millisecond)

	resp, err := http.Get("http://" + addr + "/api/v1/health")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("health status %d", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/api/v1/status", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("expected 401 without auth, got %d", resp.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodGet, "http://"+addr+"/api/v1/config", nil)
	req.Header.Set("Authorization", "Bearer test-secret")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("config status %d: %s", resp.StatusCode, b)
	}
	var view map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	if view["key"] != "***" {
		t.Fatalf("key not redacted: %v", view["key"])
	}

	cancel()
	select {
	case <-errCh:
	case <-time.After(2 * time.Second):
	}
	_ = os.Remove(cfgPath)
}
