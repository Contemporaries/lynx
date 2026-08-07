package config

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

type ClientAuth struct {
	CertificateSHA256 string `json:"certificate_sha256"`
	Enabled           bool   `json:"enabled"`
	SubscribeToken    string `json:"subscribe_token,omitempty"`
	CertFile          string `json:"cert_file,omitempty"`
	KeyFile           string `json:"key_file,omitempty"`
}

type Security struct {
	MaxSessionsPerCertificate int `json:"max_sessions_per_certificate"`
	MaxSessionsPerSourceIP    int `json:"max_sessions_per_source_ip"`
	MaxTotalSessions          int `json:"max_total_sessions"`
	MaxFlowsPerCertificate    int `json:"max_flows_per_certificate"`
	MaxNewFlowsPerSecond      int `json:"max_new_flows_per_second"`
	HandshakeTimeoutSeconds   int `json:"handshake_timeout_seconds"`
	SessionIdleTimeoutSeconds int `json:"session_idle_timeout_seconds"`
	FlowIdleTimeoutSeconds    int `json:"flow_idle_timeout_seconds"`
	// Subscribe requests per IP / token per minute.
	MaxSubscribePerIPPerMin    int `json:"max_subscribe_per_ip_per_min"`
	MaxSubscribePerTokenPerMin int `json:"max_subscribe_per_token_per_min"`
}

type Server struct {
	DirectListen            string                `json:"direct_listen"`
	WSListen                string                `json:"ws_listen"`
	WSPath                  string                `json:"ws_path"`
	// PublicBaseURL is the external HTTPS base for subscribe links (direct domain via nginx).
	PublicBaseURL string `json:"public_base_url"`
	// CDNBaseURL is the Cloudflare hostname base used to build ws_url in subscribe profiles.
	CDNBaseURL          string `json:"cdn_base_url"`
	SubscribePathPrefix string `json:"subscribe_path_prefix"`
	CertFile                string                `json:"cert_file"`
	KeyFile                 string                `json:"key_file"`
	ClientCAFile            string                `json:"client_ca_file"`
	Clients                 map[string]ClientAuth `json:"clients"`
	AllowPrivateNetworks    bool                  `json:"allow_private_networks"`
	MaxProxyFlowsPerSession int                   `json:"max_proxy_flows_per_session"`
	ProxyDialTimeoutSeconds int                   `json:"proxy_dial_timeout_seconds"`
	Security                Security              `json:"security"`
}

// Client is a single-file JSON config (sing-box style): TLS material is inline PEM.
type Client struct {
	Mode                 string `json:"mode,omitempty"`
	SubscribeURL         string `json:"subscribe_url,omitempty"`
	DirectAddr           string `json:"direct_addr,omitempty"`
	DirectServerName     string `json:"direct_server_name,omitempty"`
	WSURL                string `json:"ws_url,omitempty"`
	WSInnerServerName    string `json:"ws_inner_server_name,omitempty"`
	Certificate          string `json:"certificate,omitempty"`           // client cert PEM
	Key                  string `json:"key,omitempty"`                   // client key PEM
	CertificateAuthority string `json:"certificate_authority,omitempty"` // CA PEM
	CFAccessClientID     string `json:"cf_access_client_id,omitempty"`
	CFAccessClientSecret string `json:"cf_access_client_secret,omitempty"`
	SOCKSListen          string `json:"socks_listen,omitempty"`
	HTTPListen           string `json:"http_listen,omitempty"`
	ProxyChannels        int    `json:"proxy_channels,omitempty"`
	ProxyUsername        string `json:"proxy_username,omitempty"`
	ProxyPassword        string `json:"proxy_password,omitempty"`
	PingIntervalSeconds  int    `json:"ping_interval_seconds,omitempty"`
	PongTimeoutMisses    int    `json:"pong_timeout_misses,omitempty"`
	SubscribeRefreshSec  int    `json:"subscribe_refresh_seconds,omitempty"`
}

func (s Security) HandshakeTimeout() time.Duration {
	return time.Duration(s.HandshakeTimeoutSeconds) * time.Second
}

func (s Security) SessionIdleTimeout() time.Duration {
	return time.Duration(s.SessionIdleTimeoutSeconds) * time.Second
}

func (s Security) FlowIdleTimeout() time.Duration {
	return time.Duration(s.FlowIdleTimeoutSeconds) * time.Second
}

func LoadServer(path string) (*Server, error) {
	var c Server
	if err := load(path, &c); err != nil {
		return nil, err
	}
	if c.DirectListen == "" {
		c.DirectListen = "127.0.0.1:8443"
	}
	if c.WSListen == "" {
		c.WSListen = "127.0.0.1:8080"
	}
	if c.WSPath == "" {
		c.WSPath = "/_lynx/v1/connect"
	}
	if c.SubscribePathPrefix == "" {
		c.SubscribePathPrefix = "/_lynx/v1/subscribe/"
	}
	if !strings.HasSuffix(c.SubscribePathPrefix, "/") {
		c.SubscribePathPrefix += "/"
	}
	c.PublicBaseURL = strings.TrimRight(strings.TrimSpace(c.PublicBaseURL), "/")
	c.CDNBaseURL = strings.TrimRight(strings.TrimSpace(c.CDNBaseURL), "/")
	if c.CDNBaseURL == "" && c.PublicBaseURL != "" {
		// Backward compatible: older configs only had public_base_url (often the CDN host).
		c.CDNBaseURL = c.PublicBaseURL
	}
	if len(c.Clients) == 0 {
		return nil, fmt.Errorf("clients mapping is empty")
	}
	for name, auth := range c.Clients {
		fp := NormalizeFingerprint(auth.CertificateSHA256)
		if fp == "" {
			return nil, fmt.Errorf("client %q missing certificate_sha256", name)
		}
		auth.CertificateSHA256 = fp
		auth.SubscribeToken = strings.TrimSpace(auth.SubscribeToken)
		c.Clients[name] = auth
	}
	if c.MaxProxyFlowsPerSession <= 0 {
		c.MaxProxyFlowsPerSession = 256
	}
	if c.ProxyDialTimeoutSeconds <= 0 {
		c.ProxyDialTimeoutSeconds = 15
	}
	if c.Security.MaxSessionsPerCertificate <= 0 {
		c.Security.MaxSessionsPerCertificate = 4
	}
	if c.Security.MaxSessionsPerSourceIP <= 0 {
		c.Security.MaxSessionsPerSourceIP = 8
	}
	if c.Security.MaxTotalSessions <= 0 {
		c.Security.MaxTotalSessions = 256
	}
	if c.Security.MaxFlowsPerCertificate <= 0 {
		c.Security.MaxFlowsPerCertificate = 512
	}
	if c.Security.MaxNewFlowsPerSecond <= 0 {
		c.Security.MaxNewFlowsPerSecond = 50
	}
	if c.Security.HandshakeTimeoutSeconds <= 0 {
		c.Security.HandshakeTimeoutSeconds = 10
	}
	if c.Security.SessionIdleTimeoutSeconds <= 0 {
		c.Security.SessionIdleTimeoutSeconds = 300
	}
	if c.Security.FlowIdleTimeoutSeconds <= 0 {
		c.Security.FlowIdleTimeoutSeconds = 600
	}
	if c.Security.MaxSubscribePerIPPerMin <= 0 {
		c.Security.MaxSubscribePerIPPerMin = 30
	}
	if c.Security.MaxSubscribePerTokenPerMin <= 0 {
		c.Security.MaxSubscribePerTokenPerMin = 10
	}
	return &c, nil
}

func LoadClient(path string) (*Client, error) {
	var c Client
	if err := load(path, &c); err != nil {
		return nil, err
	}
	return NormalizeClient(&c)
}

func (c *Client) HasInlineCredentials() bool {
	return strings.TrimSpace(c.Certificate) != "" &&
		strings.TrimSpace(c.Key) != "" &&
		strings.TrimSpace(c.CertificateAuthority) != ""
}

func NormalizeClient(c *Client) (*Client, error) {
	if c.Mode == "" {
		c.Mode = "auto"
	}
	if c.SOCKSListen == "" {
		c.SOCKSListen = "127.0.0.1:1080"
	}
	if c.HTTPListen == "" {
		c.HTTPListen = "127.0.0.1:8080"
	}
	if c.ProxyChannels <= 0 {
		c.ProxyChannels = 3
	}
	if c.ProxyChannels > 8 {
		return nil, fmt.Errorf("proxy_channels cannot exceed 8")
	}
	if c.PingIntervalSeconds <= 0 {
		c.PingIntervalSeconds = 20
	}
	if c.PongTimeoutMisses <= 0 {
		c.PongTimeoutMisses = 3
	}
	if (c.ProxyUsername == "") != (c.ProxyPassword == "") {
		return nil, fmt.Errorf("proxy_username and proxy_password must be configured together")
	}
	if strings.TrimSpace(c.SubscribeURL) == "" && !c.HasInlineCredentials() {
		return nil, fmt.Errorf("subscribe_url or inline certificate/key/certificate_authority is required")
	}
	if c.HasInlineCredentials() && strings.TrimSpace(c.WSInnerServerName) == "" {
		c.WSInnerServerName = "lynx.internal"
	}
	for _, addr := range []string{c.SOCKSListen, c.HTTPListen} {
		if strings.TrimSpace(addr) == "" {
			continue
		}
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("invalid proxy listen address %q: %w", addr, err)
		}
		if host != "127.0.0.1" && host != "::1" && host != "localhost" && c.ProxyUsername == "" {
			return nil, fmt.Errorf("proxy authentication is required when listening outside localhost")
		}
	}
	return c, nil
}

// WriteClient writes cfg as pretty JSON to path (mode 0600).
func WriteClient(path string, cfg *Client) error {
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o600)
}

func NormalizeFingerprint(fp string) string {
	fp = strings.TrimSpace(strings.ToLower(fp))
	fp = strings.ReplaceAll(fp, ":", "")
	fp = strings.ReplaceAll(fp, " ", "")
	return fp
}

func load(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	return nil
}
