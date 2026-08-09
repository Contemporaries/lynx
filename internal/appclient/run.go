package appclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/Contemporaries/lynx/internal/config"
	"github.com/Contemporaries/lynx/internal/localproxy"
	"github.com/Contemporaries/lynx/internal/proto"
	"github.com/Contemporaries/lynx/internal/proxymux"
	"github.com/Contemporaries/lynx/internal/tlsutil"
	"github.com/Contemporaries/lynx/internal/transport"
)

const autoProbeInterval = 15 * time.Second

// Run loads cfgPath and serves local SOCKS5/HTTP until ctx is cancelled.
func Run(ctx context.Context, cfgPath string, logger *log.Logger) error {
	cfg, err := config.LoadClient(cfgPath)
	if err != nil {
		return err
	}
	return RunConfig(ctx, cfg, logger, cfgPath)
}

// RunConfig starts the proxy client with an already-loaded config.
// configPath is accepted for API compatibility; auto mode never rewrites client.json.
func RunConfig(ctx context.Context, cfg *config.Client, logger *log.Logger, _ string) error {
	if logger == nil {
		logger = log.Default()
	}
	if !cfg.HasInlineCredentials() {
		return fmt.Errorf("client config missing inline certificate/key/certificate_authority")
	}
	cert, err := tlsutil.LoadCertPEM(cfg.Certificate, cfg.Key)
	if err != nil {
		return err
	}
	roots, err := tlsutil.LoadPoolPEM(cfg.CertificateAuthority)
	if err != nil {
		return err
	}
	dialer := newPathDialer(cfg, cert, roots, logger)
	pool, err := proxymux.NewPoolWithOptions(ctx, proxymux.PoolOptions{
		Channels:          cfg.ProxyChannels,
		PingInterval:      time.Duration(cfg.PingIntervalSeconds) * time.Second,
		PongTimeoutMisses: cfg.PongTimeoutMisses,
	}, dialer.Dial)
	if err != nil {
		return err
	}
	defer pool.Close()
	if strings.ToLower(cfg.Mode) == "auto" || cfg.Mode == "" {
		go dialer.StartProbe(ctx, pool)
	}
	logger.Printf("proxy transport ready: %d/%d encrypted channels path=%s", pool.HealthyChannels(), cfg.ProxyChannels, dialer.Current())
	return localproxy.Serve(ctx, localproxy.Config{
		SOCKSListen:  cfg.SOCKSListen,
		HTTPListen:   cfg.HTTPListen,
		Username:     cfg.ProxyUsername,
		Password:     cfg.ProxyPassword,
		Open:         pool.Open,
		AssociateUDP: func(c context.Context) (localproxy.UDPRelay, error) { return pool.AssociateUDP(c) },
		Logger:       logger,
	})
}

type pathDialer struct {
	cfg    *config.Client
	cert   tls.Certificate
	roots  *x509.CertPool
	logger *log.Logger

	mu      sync.Mutex
	current string // "" | "wss" | "direct"
}

func newPathDialer(cfg *config.Client, cert tls.Certificate, roots *x509.CertPool, logger *log.Logger) *pathDialer {
	return &pathDialer{cfg: cfg, cert: cert, roots: roots, logger: logger}
}

func (d *pathDialer) Current() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.current == "" {
		return "unknown"
	}
	return d.current
}

func (d *pathDialer) Dial(ctx context.Context) (transport.Session, error) {
	mode := strings.ToLower(d.cfg.Mode)
	if mode != "auto" && mode != "direct" && mode != "wss" {
		return nil, fmt.Errorf("unsupported mode %q", d.cfg.Mode)
	}
	switch mode {
	case "direct":
		sess, err := d.tryDirect(ctx)
		if err != nil {
			return nil, err
		}
		d.notePath("direct", sess, "")
		return sess, nil
	case "wss":
		sess, err := d.tryWSS(ctx)
		if err != nil {
			return nil, err
		}
		d.notePath("wss", sess, "")
		return sess, nil
	default:
		return d.dialAuto(ctx)
	}
}

func (d *pathDialer) dialAuto(ctx context.Context) (transport.Session, error) {
	w, werr := d.tryWSS(ctx)
	if werr == nil {
		d.notePath("wss", w, "")
		return w, nil
	}
	prev := d.Current()
	reason := "WSS unavailable"
	if prev == "wss" {
		reason = "WSS connection lost"
	}
	d.logger.Printf("auto: %s: %v", reason, werr)
	sess, derr := d.tryDirect(ctx)
	if derr != nil {
		return nil, fmt.Errorf("WSS failed: %v; direct failed: %w", werr, derr)
	}
	d.notePath("direct", sess, reason+": "+werr.Error())
	return sess, nil
}

func (d *pathDialer) tryDirect(ctx context.Context) (transport.Session, error) {
	if d.cfg.DirectAddr == "" || d.cfg.DirectServerName == "" {
		return nil, fmt.Errorf("direct endpoint is not configured")
	}
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return transport.DialDirect(dctx, d.cfg.DirectAddr, &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      d.roots,
		Certificates: []tls.Certificate{d.cert},
		ServerName:   d.cfg.DirectServerName,
		NextProtos:   []string{proto.ALPN},
	})
}

func (d *pathDialer) tryWSS(ctx context.Context) (transport.Session, error) {
	if d.cfg.WSURL == "" || d.cfg.WSInnerServerName == "" {
		return nil, fmt.Errorf("WebSocket endpoint is not configured")
	}
	wctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	return transport.DialWebSocket(wctx, d.cfg.WSURL, &tls.Config{
		MinVersion:   tls.VersionTLS13,
		RootCAs:      d.roots,
		Certificates: []tls.Certificate{d.cert},
		ServerName:   d.cfg.WSInnerServerName,
		NextProtos:   []string{proto.InnerALPN},
	}, d.cfg.CFAccessClientID, d.cfg.CFAccessClientSecret)
}

func (d *pathDialer) notePath(path string, sess transport.Session, changeReason string) {
	d.mu.Lock()
	prev := d.current
	d.current = path
	d.mu.Unlock()

	kind := ""
	if sess != nil {
		kind = sess.Kind()
	}
	via, sniKey, sniVal := d.endpointFields(path)
	if prev != "" && prev != path {
		reason := changeReason
		if reason == "" {
			if path == "wss" {
				reason = "WSS recovered"
			} else {
				reason = "switched"
			}
		}
		d.logger.Printf("auto: path changed %s → %s (%s) via=%s", prev, path, reason, via)
	}
	d.logger.Printf("transport: path=%s kind=%s via=%s %s=%s", path, kind, via, sniKey, sniVal)
}

func (d *pathDialer) endpointFields(path string) (via, sniKey, sniVal string) {
	if path == "wss" {
		return d.cfg.WSURL, "inner_sni", d.cfg.WSInnerServerName
	}
	return d.cfg.DirectAddr, "sni", d.cfg.DirectServerName
}

// StartProbe periodically tries WSS while the runtime path is direct, then forces pool reconnect.
func (d *pathDialer) StartProbe(ctx context.Context, pool *proxymux.Pool) {
	if d.cfg.WSURL == "" || d.cfg.WSInnerServerName == "" {
		return
	}
	if d.cfg.DirectAddr == "" || d.cfg.DirectServerName == "" {
		return
	}
	ticker := time.NewTicker(autoProbeInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if d.Current() != "direct" {
			continue
		}
		sess, err := d.tryWSS(ctx)
		if err != nil {
			continue
		}
		_ = sess.Close()
		d.logger.Printf("auto: WSS recovered while on direct; reconnecting channels via=%s", d.cfg.WSURL)
		pool.ReconnectAll()
	}
}
