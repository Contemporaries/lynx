package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Contemporaries/lynx/internal/config"
	"github.com/Contemporaries/lynx/internal/logx"
	"github.com/Contemporaries/lynx/internal/mgmt"
	"github.com/Contemporaries/lynx/internal/proto"
	"github.com/Contemporaries/lynx/internal/proxymux"
	"github.com/Contemporaries/lynx/internal/subscribe"
	"github.com/Contemporaries/lynx/internal/tlsutil"
	"github.com/Contemporaries/lynx/internal/transport"
	"github.com/Contemporaries/lynx/internal/upgrade"
	"github.com/Contemporaries/lynx/internal/version"
)

type server struct {
	cfg      *config.Server
	lx       *logx.Logger
	mu       sync.Mutex
	sessions map[string]int // fingerprint -> count
	byIP     map[string]int
	total    int
	flows    map[string]int // fingerprint -> active flows
	started  time.Time
}

func main() {
	configPath := flag.String("config", "/etc/lynx/server.json", "server JSON config")
	logLevel := flag.String("log-level", "", "log level override: debug|info|warn|error")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version.Version)
		return
	}

	cfg, err := config.LoadServer(*configPath)
	if err != nil {
		log.Fatal(err)
	}

	level := logx.ParseLevel(cfg.Log.Level)
	if *logLevel != "" {
		level = logx.ParseLevel(*logLevel)
	}
	lx := logx.New(level)
	started := time.Now()

	s := &server{
		cfg:      cfg,
		lx:       lx,
		sessions: make(map[string]int),
		byIP:     make(map[string]int),
		flows:    make(map[string]int),
		started:  started,
	}

	cert, err := tlsutil.LoadCert(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		log.Fatal(err)
	}
	clientCAs, err := tlsutil.LoadPool(cfg.ClientCAFile)
	if err != nil {
		log.Fatal(err)
	}

	directTLS := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
		NextProtos:   []string{proto.ALPN},
	}
	innerTLS := &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
		NextProtos:   []string{proto.InnerALPN},
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store := &mgmt.FileConfigStore{PathName: *configPath, Role: mgmt.RoleServer, Server: cfg, Logger: lx}
	svc := &serverService{stop: stop, cfgPath: *configPath, lx: lx, srv: s}
	if cfg.Mgmt.Listen != "" {
		go func() {
			err := mgmt.ListenAndServe(ctx, mgmt.Options{
				Listen:       cfg.Mgmt.Listen,
				Secret:       cfg.Mgmt.Secret,
				CORSOrigin:   cfg.Mgmt.CORSOrigin,
				AllowUpgrade: cfg.Mgmt.AllowUpgrade,
				ApplyRestart: cfg.Mgmt.ApplyRestart,
				Role:         mgmt.RoleServer,
				Unit:         "lynx-server",
				Binary:       "lynx-server",
				Logger:       lx,
				StartedAt:    started,
				Status:       s.status,
				Config:       store,
				Service:      svc,
			})
			if err != nil && ctx.Err() == nil {
				lx.Error("mgmt server stopped", "err", err)
			}
		}()
	}

	go func() {
		lx.Info("direct TLS listening", "addr", cfg.DirectListen)
		if err := transport.ServeDirect(ctx, cfg.DirectListen, directTLS, cfg.Security.HandshakeTimeout(), func(sess transport.Session) {
			s.handleSession(ctx, sess)
		}); err != nil && ctx.Err() == nil {
			lx.Error("direct server", "err", err)
			stop()
		}
	}()

	go func() {
		sub := subscribe.NewHandler(subscribe.ServerOptions{
			Clients:           cfg.Clients,
			ClientCAFile:      cfg.ClientCAFile,
			CDNBaseURL:        cfg.CDNBaseURL,
			PublicBaseURL:     cfg.PublicBaseURL,
			WSPath:            cfg.WSPath,
			WSInnerServerName: "lynx.internal",
			PathPrefix:        cfg.SubscribePathPrefix,
			MaxPerIPPerMin:    cfg.Security.MaxSubscribePerIPPerMin,
			MaxPerTokenPerMin: cfg.Security.MaxSubscribePerTokenPerMin,
		})
		nTok := sub.TokenCount()
		lx.Info("lynx-server origin",
			"version", version.Version,
			"listen", cfg.WSListen,
			"ws", cfg.WSPath,
			"subscribe", cfg.SubscribePathPrefix,
			"tokens", nTok,
			"public", cfg.PublicBaseURL,
			"cdn", cfg.CDNBaseURL,
		)
		if nTok == 0 {
			lx.Warn("no subscribe_token configured; subscribe GET will always 404", "prefix", cfg.SubscribePathPrefix)
		}
		warnSubscribePortConflict(cfg, lx)
		if err := transport.ServeWebSocket(ctx, cfg.WSListen, cfg.WSPath, innerTLS, func(sess transport.Session) {
			s.handleSession(ctx, sess)
		}, func(mux *http.ServeMux) {
			mux.HandleFunc("/_lynx/v1/version", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				_, _ = fmt.Fprintf(w, `{"service":"lynx-server","version":%q,"subscribe_tokens":%d}`+"\n", version.Version, nTok)
			})
			mux.HandleFunc(strings.TrimSuffix(sub.Prefix(), "/"), sub.ServeHealth)
			mux.Handle(sub.Prefix(), sub)
		}); err != nil && ctx.Err() == nil {
			lx.Error("WebSocket server", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	lx.Info("shutting down")
}

type serverService struct {
	stop    context.CancelFunc
	cfgPath string
	lx      *logx.Logger
	srv     *server
}

func (c *serverService) Restart() error {
	if err := upgrade.RestartService("lynx-server"); err == nil {
		return nil
	}
	return mgmt.SelfRestart(c.stop)
}

func (c *serverService) Reload() ([]string, bool, error) {
	cfg, err := config.LoadServer(c.cfgPath)
	if err != nil {
		return nil, false, err
	}
	applied := []string{}
	if c.lx != nil && cfg.Log.Level != "" {
		c.lx.SetLevel(logx.ParseLevel(cfg.Log.Level))
		applied = append(applied, "log.level")
	}
	return applied, true, nil
}

func (c *serverService) Reconnect() error         { return fmt.Errorf("not supported on server") }
func (c *serverService) SubscribeRefresh() error { return fmt.Errorf("not supported on server") }

func (s *server) status() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	activeFlows := 0
	for _, n := range s.flows {
		activeFlows += n
	}
	return map[string]any{
		"direct_listen":   s.cfg.DirectListen,
		"ws_listen":       s.cfg.WSListen,
		"ws_path":         s.cfg.WSPath,
		"sessions":        s.total,
		"active_flows":    activeFlows,
		"clients_enabled": countEnabled(s.cfg.Clients),
	}
}

func countEnabled(clients map[string]config.ClientAuth) int {
	n := 0
	for _, c := range clients {
		if c.Enabled {
			n++
		}
	}
	return n
}

func warnSubscribePortConflict(cfg *config.Server, lx *logx.Logger) {
	pub := strings.TrimPrefix(strings.TrimPrefix(cfg.PublicBaseURL, "https://"), "http://")
	if !strings.Contains(pub, ":") {
		return
	}
	pubPort := pub[strings.LastIndex(pub, ":")+1:]
	_, directPort, err := net.SplitHostPort(cfg.DirectListen)
	if err != nil {
		if strings.HasPrefix(cfg.DirectListen, ":") {
			directPort = strings.TrimPrefix(cfg.DirectListen, ":")
		} else {
			return
		}
	}
	if pubPort != "" && pubPort == directPort {
		lx.Warn("public_base_url port equals direct_listen — nginx subscribe cannot share the mTLS direct port",
			"public_port", pubPort, "direct_listen", cfg.DirectListen)
	}
}

func (s *server) handleSession(parent context.Context, tr transport.Session) {
	defer tr.Close()
	fp := config.NormalizeFingerprint(tr.PeerCertificateSHA256())
	if fp == "" {
		s.lx.Warn("peer has no client certificate", "kind", tr.Kind())
		return
	}
	device, ok := s.authorize(fp)
	if !ok {
		_ = tr.WriteControl(proto.FrameError, []byte("unauthorized client certificate"))
		s.lx.Warn("rejected unauthorized client", "fingerprint", fp[:16])
		return
	}

	srcIP := hostOnly(tr.RemoteAddr())
	if !s.acquireSession(fp, srcIP) {
		_ = tr.WriteControl(proto.FrameError, []byte("session limit exceeded"))
		s.lx.Warn("rejected client: session limit", "device", device)
		return
	}
	defer s.releaseSession(fp, srcIP)

	typ, payload, err := tr.ReadControl()
	if err != nil {
		s.lx.Debug("read hello failed", "kind", tr.Kind(), "err", err)
		return
	}
	if typ != proto.FrameHello {
		s.lx.Warn("expected hello", "kind", tr.Kind(), "frame", fmt.Sprintf("0x%x", typ))
		return
	}
	hello, err := proto.ReadJSON[proto.Hello](payload)
	if err != nil || hello.Version != proto.Version {
		_ = tr.WriteControl(proto.FrameError, []byte("unsupported protocol version"))
		return
	}
	if hello.Mode != proto.ModeProxyMux {
		_ = tr.WriteControl(proto.FrameError, []byte("only proxy_mux mode is supported"))
		return
	}

	s.lx.Info("proxy client connected", "device", device, "path", tr.Kind())
	err = proxymux.Serve(parent, tr, proxymux.ServerOptions{
		MaxFlows:          s.cfg.MaxProxyFlowsPerSession,
		MaxNewFlowsPerSec: s.cfg.Security.MaxNewFlowsPerSecond,
		IdleTimeout:       s.cfg.Security.FlowIdleTimeout(),
		Logger:            s.lx,
		Device:            device,
		Path:              tr.Kind(),
		OnFlowOpen: func() bool {
			return s.acquireFlow(fp)
		},
		OnFlowClose: func() {
			s.releaseFlow(fp)
		},
		Dial: func(ctx context.Context, address string) (net.Conn, error) {
			return dialProxyTarget(ctx, address, s.cfg.AllowPrivateNetworks, time.Duration(s.cfg.ProxyDialTimeoutSeconds)*time.Second)
		},
		CheckUDPDestination: func(ctx context.Context, address string) error {
			return checkUDPDestination(ctx, address, s.cfg.AllowPrivateNetworks, time.Duration(s.cfg.ProxyDialTimeoutSeconds)*time.Second)
		},
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		s.lx.Info("proxy client disconnected", "device", device, "err", err)
	} else {
		s.lx.Info("proxy client disconnected", "device", device)
	}
}

func (s *server) authorize(fp string) (string, bool) {
	for name, auth := range s.cfg.Clients {
		if !auth.Enabled {
			continue
		}
		if auth.CertificateSHA256 == fp {
			return name, true
		}
	}
	return "", false
}

func (s *server) acquireSession(fp, srcIP string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	sec := s.cfg.Security
	if s.total >= sec.MaxTotalSessions {
		return false
	}
	if s.sessions[fp] >= sec.MaxSessionsPerCertificate {
		return false
	}
	if srcIP != "" && s.byIP[srcIP] >= sec.MaxSessionsPerSourceIP {
		return false
	}
	s.total++
	s.sessions[fp]++
	if srcIP != "" {
		s.byIP[srcIP]++
	}
	return true
}

func (s *server) releaseSession(fp, srcIP string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.total--
	s.sessions[fp]--
	if s.sessions[fp] <= 0 {
		delete(s.sessions, fp)
	}
	if srcIP != "" {
		s.byIP[srcIP]--
		if s.byIP[srcIP] <= 0 {
			delete(s.byIP, srcIP)
		}
	}
}

func (s *server) acquireFlow(fp string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.flows[fp] >= s.cfg.Security.MaxFlowsPerCertificate {
		return false
	}
	s.flows[fp]++
	return true
}

func (s *server) releaseFlow(fp string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flows[fp]--
	if s.flows[fp] <= 0 {
		delete(s.flows, fp)
	}
}

func dialProxyTarget(ctx context.Context, address string, allowPrivate bool, timeout time.Duration) (net.Conn, error) {
	ips, port, err := resolveProxyIPs(ctx, address, allowPrivate, timeout)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	var lastErr error
	for _, ip := range ips {
		conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no allowed target address")
	}
	return nil, lastErr
}

func checkUDPDestination(ctx context.Context, address string, allowPrivate bool, timeout time.Duration) error {
	_, _, err := resolveProxyIPs(ctx, address, allowPrivate, timeout)
	return err
}

func resolveProxyIPs(ctx context.Context, address string, allowPrivate bool, timeout time.Duration) ([]net.IP, string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, "", fmt.Errorf("invalid target address: %w", err)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return nil, "", fmt.Errorf("invalid target port")
	}
	host = strings.TrimSpace(strings.Trim(host, "[]"))
	if host == "" {
		return nil, "", fmt.Errorf("empty target host")
	}
	lookupCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var ips []net.IP
	if parsed := net.ParseIP(host); parsed != nil {
		ips = []net.IP{parsed}
	} else {
		resolved, err := net.DefaultResolver.LookupIPAddr(lookupCtx, host)
		if err != nil {
			return nil, "", fmt.Errorf("resolve target: %w", err)
		}
		for _, item := range resolved {
			ips = append(ips, item.IP)
		}
	}
	if len(ips) == 0 {
		return nil, "", fmt.Errorf("target has no IP address")
	}
	var allowed []net.IP
	var lastErr error
	for _, ip := range ips {
		if !allowPrivate && forbiddenProxyIP(ip) {
			lastErr = fmt.Errorf("target address is not allowed")
			continue
		}
		allowed = append(allowed, ip)
	}
	if len(allowed) == 0 {
		if lastErr == nil {
			lastErr = fmt.Errorf("no allowed target address")
		}
		return nil, "", lastErr
	}
	return allowed, port, nil
}

func forbiddenProxyIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate()
}

func hostOnly(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}
