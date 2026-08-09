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

// Run loads cfgPath and serves local SOCKS5/HTTP until ctx is cancelled.
func Run(ctx context.Context, cfgPath string, logger *log.Logger) error {
	cfg, err := config.LoadClient(cfgPath)
	if err != nil {
		return err
	}
	return RunConfig(ctx, cfg, logger, cfgPath)
}

// RunConfig starts the proxy client with an already-loaded config.
// If configPath is non-empty and mode is auto, a successful WSS dial persists mode=wss.
func RunConfig(ctx context.Context, cfg *config.Client, logger *log.Logger, configPath string) error {
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
	var persistMu sync.Mutex
	dial := func(dctx context.Context) (transport.Session, error) {
		return dialTransport(dctx, cfg, cert, roots, logger, configPath, &persistMu)
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

func dialTransport(ctx context.Context, cfg *config.Client, cert tls.Certificate, roots *x509.CertPool, logger *log.Logger, configPath string, persistMu *sync.Mutex) (transport.Session, error) {
	if logger == nil {
		logger = log.Default()
	}
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
		w, werr := tryWSS()
		if werr == nil {
			persistAutoToWSS(cfg, configPath, logger, persistMu)
			return w, nil
		}
		logger.Printf("Cloudflare WSS unavailable, falling back to direct TLS: %v", werr)
		d, derr := tryDirect()
		if derr != nil {
			return nil, fmt.Errorf("WSS failed: %v; direct failed: %w", werr, derr)
		}
		return d, nil
	}
}

func persistAutoToWSS(cfg *config.Client, configPath string, logger *log.Logger, persistMu *sync.Mutex) {
	if persistMu == nil {
		return
	}
	persistMu.Lock()
	defer persistMu.Unlock()
	if strings.ToLower(cfg.Mode) != "auto" {
		return
	}
	cfg.Mode = "wss"
	if configPath == "" {
		logger.Printf("auto: WSS ok, switched mode to wss (not persisted: no config path)")
		return
	}
	if err := config.WriteClient(configPath, cfg); err != nil {
		logger.Printf("auto: WSS ok, switched mode to wss; warn: could not persist %s: %v", configPath, err)
		return
	}
	logger.Printf("auto: WSS ok, switched mode to wss and wrote %s", configPath)
}
