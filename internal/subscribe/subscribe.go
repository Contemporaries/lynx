package subscribe

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Contemporaries/lynx/internal/config"
	"github.com/Contemporaries/lynx/internal/version"
)

func logSubscribeReject(token, ip string) {
	shown := token
	if len(shown) > 8 {
		shown = shown[:8] + "…"
	}
	log.Printf("subscribe: unknown/disabled token=%s ip=%s", shown, ip)
}

const ProfileVersion = 1

// Profile is the JSON document returned by GET /_lynx/v1/subscribe/<token>.
type Profile struct {
	Version             int    `json:"version"`
	Device              string `json:"device"`
	Mode                string `json:"mode"`
	WSURL               string `json:"ws_url"`
	WSInnerServerName   string `json:"ws_inner_server_name"`
	CAPEM               string `json:"ca_pem"`
	CertPEM             string `json:"cert_pem"`
	KeyPEM              string `json:"key_pem"`
	ProxyChannels       int    `json:"proxy_channels"`
	CFAccessClientID    string `json:"cf_access_client_id,omitempty"`
	CFAccessClientSecret string `json:"cf_access_client_secret,omitempty"`
}

type ServerOptions struct {
	Clients           map[string]config.ClientAuth
	ClientCAFile      string
	CDNBaseURL        string // used to build ws_url (Cloudflare)
	PublicBaseURL     string // subscribe public base; fallback for ws_url if CDN unset
	WSPath            string
	WSInnerServerName string
	PathPrefix        string
	MaxPerIPPerMin    int
	MaxPerTokenPerMin int
}

type Handler struct {
	opts    ServerOptions
	limiter *windowLimiter
	byToken map[string]string // token -> device name
}

func NewHandler(opts ServerOptions) *Handler {
	if opts.PathPrefix == "" {
		opts.PathPrefix = "/_lynx/v1/subscribe/"
	}
	if !strings.HasSuffix(opts.PathPrefix, "/") {
		opts.PathPrefix += "/"
	}
	if opts.WSPath == "" {
		opts.WSPath = "/_lynx/v1/connect"
	}
	if opts.WSInnerServerName == "" {
		opts.WSInnerServerName = "lynx.internal"
	}
	if opts.MaxPerIPPerMin <= 0 {
		opts.MaxPerIPPerMin = 30
	}
	if opts.MaxPerTokenPerMin <= 0 {
		opts.MaxPerTokenPerMin = 10
	}
	h := &Handler{
		opts:    opts,
		limiter: newWindowLimiter(),
		byToken: make(map[string]string),
	}
	for name, auth := range opts.Clients {
		if tok := strings.TrimSpace(auth.SubscribeToken); tok != "" {
			h.byToken[tok] = name
		}
	}
	return h
}

func (h *Handler) Prefix() string { return h.opts.PathPrefix }

func (h *Handler) TokenCount() int { return len(h.byToken) }

// ServeHealth answers GET /_lynx/v1/subscribe (no trailing token) for local diagnostics.
func (h *Handler) ServeHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"service": "lynx-subscribe",
		"ok":      true,
		"version": version.Version,
		"tokens":  h.TokenCount(),
		"prefix":  h.opts.PathPrefix,
	})
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token := strings.TrimPrefix(r.URL.Path, h.opts.PathPrefix)
	token = strings.Trim(token, "/")
	if token == "" || strings.Contains(token, "/") {
		// Empty path under prefix: tell operator the route exists.
		http.Error(w, "missing subscribe token; use GET "+h.opts.PathPrefix+"<token>", http.StatusBadRequest)
		return
	}
	ip := clientIP(r)
	if !h.limiter.allow("ip:"+ip, h.opts.MaxPerIPPerMin) || !h.limiter.allow("tok:"+token, h.opts.MaxPerTokenPerMin) {
		http.Error(w, "too many requests", http.StatusTooManyRequests)
		return
	}
	name, ok := h.byToken[token]
	if !ok {
		// Opaque 404 (do not reveal whether token exists); log for operators.
		logSubscribeReject(token, ip)
		http.NotFound(w, r)
		return
	}
	auth := h.opts.Clients[name]
	if !auth.Enabled {
		http.NotFound(w, r)
		return
	}
	if auth.CertFile == "" || auth.KeyFile == "" || h.opts.ClientCAFile == "" {
		http.Error(w, "subscription not configured", http.StatusInternalServerError)
		return
	}
	caPEM, err := os.ReadFile(h.opts.ClientCAFile)
	if err != nil {
		http.Error(w, "subscription unavailable", http.StatusInternalServerError)
		return
	}
	certPEM, err := os.ReadFile(auth.CertFile)
	if err != nil {
		http.Error(w, "subscription unavailable", http.StatusInternalServerError)
		return
	}
	keyPEM, err := os.ReadFile(auth.KeyFile)
	if err != nil {
		http.Error(w, "subscription unavailable", http.StatusInternalServerError)
		return
	}
	base := strings.TrimRight(h.opts.CDNBaseURL, "/")
	if base == "" {
		base = strings.TrimRight(h.opts.PublicBaseURL, "/")
	}
	if base == "" {
		base = "https://" + r.Host
	}
	wsURL := "wss://" + strings.TrimPrefix(strings.TrimPrefix(base, "https://"), "http://") + h.opts.WSPath
	profile := Profile{
		Version:           ProfileVersion,
		Device:            name,
		Mode:              "wss",
		WSURL:             wsURL,
		WSInnerServerName: h.opts.WSInnerServerName,
		CAPEM:             string(caPEM),
		CertPEM:           string(certPEM),
		KeyPEM:            string(keyPEM),
		ProxyChannels:     3,
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	_ = json.NewEncoder(w).Encode(profile)
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("CF-Connecting-IP"); xff != "" {
		return strings.TrimSpace(xff)
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type windowLimiter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

func newWindowLimiter() *windowLimiter {
	return &windowLimiter{hits: make(map[string][]time.Time)}
}

func (l *windowLimiter) allow(key string, maxPerMin int) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-time.Minute)
	arr := l.hits[key]
	kept := arr[:0]
	for _, t := range arr {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= maxPerMin {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	return true
}

// Fetch downloads a subscription profile over HTTPS.
func Fetch(ctx context.Context, rawURL string) (*Profile, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, fmt.Errorf("empty subscribe url")
	}
	if !strings.HasPrefix(rawURL, "https://") {
		return nil, fmt.Errorf("subscribe url must use https://")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	client := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("subscribe fetch failed: HTTP %s", resp.Status)
	}
	var p Profile
	if err := json.Unmarshal(body, &p); err != nil {
		return nil, fmt.Errorf("parse subscribe profile: %w", err)
	}
	if p.Version != ProfileVersion {
		return nil, fmt.Errorf("unsupported subscribe profile version %d", p.Version)
	}
	if p.WSURL == "" || p.CAPEM == "" || p.CertPEM == "" || p.KeyPEM == "" {
		return nil, fmt.Errorf("subscribe profile missing required fields")
	}
	if p.WSInnerServerName == "" {
		p.WSInnerServerName = "lynx.internal"
	}
	if p.Mode == "" {
		p.Mode = "wss"
	}
	if p.ProxyChannels <= 0 {
		p.ProxyChannels = 3
	}
	return &p, nil
}

// ApplyProfile builds a single-file client config with inline PEMs (no disk writes).
func ApplyProfile(p *Profile, socksListen, httpListen string, keepSubscribeURL string) (*config.Client, error) {
	cfg := &config.Client{
		Mode:                 p.Mode,
		SubscribeURL:         keepSubscribeURL,
		WSURL:                p.WSURL,
		WSInnerServerName:    p.WSInnerServerName,
		Certificate:          p.CertPEM,
		Key:                  p.KeyPEM,
		CertificateAuthority: p.CAPEM,
		CFAccessClientID:     p.CFAccessClientID,
		CFAccessClientSecret: p.CFAccessClientSecret,
		SOCKSListen:          socksListen,
		HTTPListen:           httpListen,
		ProxyChannels:        p.ProxyChannels,
		PingIntervalSeconds:  20,
		PongTimeoutMisses:    3,
	}
	if cfg.SOCKSListen == "" {
		cfg.SOCKSListen = "127.0.0.1:1080"
	}
	if cfg.HTTPListen == "" {
		cfg.HTTPListen = "127.0.0.1:8080"
	}
	return config.NormalizeClient(cfg)
}

// FetchAndApply downloads a profile and returns an inline client config.
func FetchAndApply(ctx context.Context, subscribeURL, socksListen, httpListen string) (*config.Client, *Profile, error) {
	p, err := Fetch(ctx, subscribeURL)
	if err != nil {
		return nil, nil, err
	}
	cfg, err := ApplyProfile(p, socksListen, httpListen, subscribeURL)
	if err != nil {
		return nil, p, err
	}
	return cfg, p, nil
}
