package appclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Contemporaries/lynx/internal/config"
	"github.com/Contemporaries/lynx/internal/flowlog"
	"github.com/Contemporaries/lynx/internal/localproxy"
	"github.com/Contemporaries/lynx/internal/logx"
	"github.com/Contemporaries/lynx/internal/proto"
	"github.com/Contemporaries/lynx/internal/proxymux"
	"github.com/Contemporaries/lynx/internal/tlsutil"
	"github.com/Contemporaries/lynx/internal/transport"
)

const autoProbeInterval = 15 * time.Second

// Runtime exposes live client state for the management API.
type Runtime struct {
	mu       sync.RWMutex
	cfg      *config.Client
	dialer   *pathDialer
	pool     *proxymux.Pool
	logger   *logx.Logger
	cfgPath  string
	started  time.Time
}

func (rt *Runtime) Status() map[string]any {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	path, via := "unknown", ""
	channels, healthy := 0, 0
	if rt.dialer != nil {
		path = rt.dialer.Current()
		via, _, _ = rt.dialer.endpointFields(path)
	}
	if rt.pool != nil {
		healthy = rt.pool.HealthyChannels()
	}
	if rt.cfg != nil {
		channels = rt.cfg.ProxyChannels
	}
	return map[string]any{
		"mode":             rt.cfg.Mode,
		"path":             path,
		"via":              via,
		"channels":         channels,
		"healthy_channels": healthy,
		"socks_listen":     rt.cfg.SOCKSListen,
		"http_listen":      rt.cfg.HTTPListen,
		"config_path":      rt.cfgPath,
	}
}

func (rt *Runtime) Reconnect() error {
	rt.mu.RLock()
	pool := rt.pool
	rt.mu.RUnlock()
	if pool == nil {
		return fmt.Errorf("pool not ready")
	}
	pool.ReconnectAll()
	return nil
}

func (rt *Runtime) SetLogLevel(level logx.Level) {
	rt.mu.RLock()
	l := rt.logger
	rt.mu.RUnlock()
	if l != nil {
		l.SetLevel(level)
	}
}

func (rt *Runtime) Logger() *logx.Logger {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.logger
}

func (rt *Runtime) Config() *config.Client {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.cfg
}

// Run loads cfgPath and serves local SOCKS5/HTTP until ctx is cancelled.
func Run(ctx context.Context, cfgPath string, logger *log.Logger) error {
	cfg, err := config.LoadClient(cfgPath)
	if err != nil {
		return err
	}
	_, err = RunConfig(ctx, cfg, logger, cfgPath, nil)
	return err
}

// RunOptions configures optional logx / runtime hooks.
type RunOptions struct {
	Logx    *logx.Logger
	Runtime **Runtime // optional out-param
}

// RunConfig starts the proxy client with an already-loaded config.
// configPath is accepted for API compatibility; auto mode never rewrites client.json.
func RunConfig(ctx context.Context, cfg *config.Client, logger *log.Logger, configPath string, opts *RunOptions) (*Runtime, error) {
	if logger == nil {
		logger = log.Default()
	}
	var lx *logx.Logger
	if opts != nil {
		lx = opts.Logx
	}
	if lx == nil {
		lx = logx.New(logx.ParseLevel(cfg.Log.Level))
	}
	std := log.New(logx.Writer{L: lx, Level: logx.LevelInfo}, "", 0)

	if !cfg.HasInlineCredentials() {
		return nil, fmt.Errorf("client config missing inline certificate/key/certificate_authority")
	}
	cert, err := tlsutil.LoadCertPEM(cfg.Certificate, cfg.Key)
	if err != nil {
		return nil, err
	}
	roots, err := tlsutil.LoadPoolPEM(cfg.CertificateAuthority)
	if err != nil {
		return nil, err
	}
	dialer := newPathDialer(cfg, cert, roots, lx)
	pool, err := proxymux.NewPoolWithOptions(ctx, proxymux.PoolOptions{
		Channels:          cfg.ProxyChannels,
		PingInterval:      time.Duration(cfg.PingIntervalSeconds) * time.Second,
		PongTimeoutMisses: cfg.PongTimeoutMisses,
	}, dialer.Dial)
	if err != nil {
		return nil, err
	}
	defer pool.Close()
	if strings.ToLower(cfg.Mode) == "auto" || cfg.Mode == "" {
		go dialer.StartProbe(ctx, pool)
	}

	rt := &Runtime{
		cfg:     cfg,
		dialer:  dialer,
		pool:    pool,
		logger:  lx,
		cfgPath: configPath,
		started: time.Now(),
	}
	if opts != nil && opts.Runtime != nil {
		*opts.Runtime = rt
	}

	var flowID atomic.Uint32
	lx.Info("proxy transport ready", "channels", fmt.Sprintf("%d/%d", pool.HealthyChannels(), cfg.ProxyChannels), "path", dialer.Current())

	open := func(c context.Context, address string) (net.Conn, error) {
		conn, err := pool.Open(c, address)
		path := dialer.Current()
		via, _, _ := dialer.endpointFields(path)
		id := flowID.Add(1)
		if err != nil {
			lx.Error("flow dial failed", "id", id, "target", address, "path", path, "via", via, "err", err)
			return nil, err
		}
		return flowlog.WrapTCP(conn, lx, id, address, path, via, ""), nil
	}
	associate := func(c context.Context) (localproxy.UDPRelay, error) {
		a, err := pool.AssociateUDP(c)
		path := dialer.Current()
		via, _, _ := dialer.endpointFields(path)
		id := flowID.Add(1)
		if err != nil {
			lx.Error("udp associate failed", "id", id, "path", path, "via", via, "err", err)
			return nil, err
		}
		return flowlog.WrapUDP(a, lx, id, path, via, ""), nil
	}

	err = localproxy.Serve(ctx, localproxy.Config{
		SOCKSListen:  cfg.SOCKSListen,
		HTTPListen:   cfg.HTTPListen,
		Username:     cfg.ProxyUsername,
		Password:     cfg.ProxyPassword,
		Open:         open,
		AssociateUDP: associate,
		Logger:       std,
	})
	return rt, err
}

type pathDialer struct {
	cfg    *config.Client
	cert   tls.Certificate
	roots  *x509.CertPool
	logger *logx.Logger

	mu      sync.Mutex
	current string // "" | "wss" | "direct"
}

func newPathDialer(cfg *config.Client, cert tls.Certificate, roots *x509.CertPool, logger *logx.Logger) *pathDialer {
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
	d.logger.Warn("auto: WSS failed, trying direct", "reason", reason, "err", werr)
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
		level := d.logger.Info
		if path == "direct" && prev == "wss" {
			level = d.logger.Warn
		}
		level("auto: path changed", "from", prev, "to", path, "reason", reason, "via", via)
	}
	d.logger.Info("transport", "path", path, "kind", kind, "via", via, sniKey, sniVal)
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
		d.logger.Info("auto: WSS recovered while on direct; reconnecting channels", "via", d.cfg.WSURL)
		pool.ReconnectAll()
	}
}
