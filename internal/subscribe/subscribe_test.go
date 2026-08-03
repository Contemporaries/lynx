package subscribe

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Contemporaries/lynx/internal/config"
)

func TestHandlerServesProfile(t *testing.T) {
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.crt")
	cert := filepath.Join(dir, "dev.crt")
	key := filepath.Join(dir, "dev.key")
	_ = os.WriteFile(ca, []byte("CA"), 0o600)
	_ = os.WriteFile(cert, []byte("CERT"), 0o600)
	_ = os.WriteFile(key, []byte("KEY"), 0o600)

	h := NewHandler(ServerOptions{
		Clients: map[string]config.ClientAuth{
			"phone": {
				CertificateSHA256: "abc",
				Enabled:           true,
				SubscribeToken:    "tok123",
				CertFile:          cert,
				KeyFile:           key,
			},
		},
		ClientCAFile:  ca,
		PublicBaseURL: "https://direct.example.com",
		CDNBaseURL:    "https://cdn.example.com",
		WSPath:        "/_lynx/v1/connect",
	})
	req := httptest.NewRequest(http.MethodGet, "/_lynx/v1/subscribe/tok123", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var p Profile
	if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Device != "phone" || p.WSURL != "wss://cdn.example.com/_lynx/v1/connect" {
		t.Fatalf("%+v", p)
	}
	if p.CAPEM != "CA" || p.CertPEM != "CERT" || p.KeyPEM != "KEY" {
		t.Fatalf("pem mismatch %+v", p)
	}
}

func TestHandlerUnknownToken(t *testing.T) {
	h := NewHandler(ServerOptions{Clients: map[string]config.ClientAuth{}})
	req := httptest.NewRequest(http.MethodGet, "/_lynx/v1/subscribe/nope", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestHandlerMissingToken(t *testing.T) {
	h := NewHandler(ServerOptions{Clients: map[string]config.ClientAuth{}})
	req := httptest.NewRequest(http.MethodGet, "/_lynx/v1/subscribe/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rr.Code)
	}
}

func TestServeHealth(t *testing.T) {
	h := NewHandler(ServerOptions{
		Clients: map[string]config.ClientAuth{
			"phone": {CertificateSHA256: "abc", Enabled: true, SubscribeToken: "tok"},
		},
	})
	req := httptest.NewRequest(http.MethodGet, "/_lynx/v1/subscribe", nil)
	rr := httptest.NewRecorder()
	h.ServeHealth(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d", rr.Code)
	}
	if h.TokenCount() != 1 {
		t.Fatalf("tokens=%d", h.TokenCount())
	}
}
