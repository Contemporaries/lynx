package appclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Contemporaries/lynx/internal/config"
	"github.com/Contemporaries/lynx/internal/localproxy"
	"github.com/Contemporaries/lynx/internal/proto"
	"github.com/Contemporaries/lynx/internal/proxymux"
	"github.com/Contemporaries/lynx/internal/tlsutil"
	"github.com/Contemporaries/lynx/internal/transport"
)

// Run loads cfgPath and serves local SOCKS5/HTTP until ctx is cancelled.
func Run(ctx context.Context, cfgPath string, logger *log.Logger) error {
	cfg, err := config.LoadClient(cfgPath)
	if err != nil {
		return err
	}
	return RunConfig(ctx, cfg, logger)
}

// RunConfig starts the proxy client with an already-loaded config.
func RunConfig(ctx context.Context, cfg *config.Client, logger *log.Logger) error {
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
	dial := func(dctx context.Context) (transport.Session, error) {
		return dialTransport(dctx, cfg, cert, roots)
	}
	pool, err := proxymux.NewPoolWithOptions(ctx, proxymux.PoolOptions{
		Channels:          cfg.ProxyChannels,
		PingInterval:      time.Duration(cfg.PingIntervalSeconds) * time.Second,
		PongTimeoutMisses: cfg.PongTimeoutMisses,
	}, dial)
	if err != nil {
		return err
	}
	defer pool.Close()
	logger.Printf("proxy transport ready: %d/%d encrypted channels", pool.HealthyChannels(), cfg.ProxyChannels)
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

func dialTransport(ctx context.Context, cfg *config.Client, cert tls.Certificate, roots *x509.CertPool) (transport.Session, error) {
	mode := strings.ToLower(cfg.Mode)
	if mode != "auto" && mode != "direct" && mode != "wss" {
		return nil, fmt.Errorf("unsupported mode %q", cfg.Mode)
	}
	tryDirect := func() (transport.Session, error) {
		if cfg.DirectAddr == "" || cfg.DirectServerName == "" {
			return nil, fmt.Errorf("direct endpoint is not configured")
		}
		dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		return transport.DialDirect(dctx, cfg.DirectAddr, &tls.Config{
			MinVersion:   tls.VersionTLS13,
			RootCAs:      roots,
			Certificates: []tls.Certificate{cert},
			ServerName:   cfg.DirectServerName,
			NextProtos:   []string{proto.ALPN},
		})
	}
	tryWSS := func() (transport.Session, error) {
		if cfg.WSURL == "" || cfg.WSInnerServerName == "" {
			return nil, fmt.Errorf("WebSocket endpoint is not configured")
		}
		wctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		return transport.DialWebSocket(wctx, cfg.WSURL, &tls.Config{
			MinVersion:   tls.VersionTLS13,
			RootCAs:      roots,
			Certificates: []tls.Certificate{cert},
			ServerName:   cfg.WSInnerServerName,
			NextProtos:   []string{proto.InnerALPN},
		}, cfg.CFAccessClientID, cfg.CFAccessClientSecret)
	}
	switch mode {
	case "direct":
		return tryDirect()
	case "wss":
		return tryWSS()
	default:
		d, derr := tryDirect()
		if derr == nil {
			return d, nil
		}
		log.Printf("direct TLS unavailable, falling back to Cloudflare WSS: %v", derr)
		w, werr := tryWSS()
		if werr != nil {
			return nil, fmt.Errorf("direct failed: %v; WSS failed: %w", derr, werr)
		}
		return w, nil
	}
}
