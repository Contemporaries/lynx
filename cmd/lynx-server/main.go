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
	"github.com/Contemporaries/lynx/internal/proto"
	"github.com/Contemporaries/lynx/internal/proxymux"
	"github.com/Contemporaries/lynx/internal/subscribe"
	"github.com/Contemporaries/lynx/internal/tlsutil"
	"github.com/Contemporaries/lynx/internal/transport"
	"github.com/Contemporaries/lynx/internal/version"
)

type server struct {
	cfg      *config.Server
	mu       sync.Mutex
	sessions map[string]int // fingerprint -> count
	byIP     map[string]int
	total    int
	flows    map[string]int // fingerprint -> active flows
}

func main() {
	configPath := flag.String("config", "/etc/lynx/server.json", "server JSON config")
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

	s := &server{
		cfg:      cfg,
		sessions: make(map[string]int),
		byIP:     make(map[string]int),
		flows:    make(map[string]int),
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

	go func() {
		log.Printf("direct TLS listening on %s", cfg.DirectListen)
		if err := transport.ServeDirect(ctx, cfg.DirectListen, directTLS, cfg.Security.HandshakeTimeout(), func(sess transport.Session) {
			s.handleSession(ctx, sess)
		}); err != nil && ctx.Err() == nil {
			log.Printf("direct server: %v", err)
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
		log.Printf("lynx-server %s origin http://%s (ws=%s subscribe=%s tokens=%d public=%s cdn=%s)",
			version.Version, cfg.WSListen, cfg.WSPath, cfg.SubscribePathPrefix, nTok, cfg.PublicBaseURL, cfg.CDNBaseURL)
		if nTok == 0 {
			log.Printf("WARNING: no subscribe_token configured; GET %s<token> will always 404. Set clients.*.subscribe_token in server.json", cfg.SubscribePathPrefix)
		}
		warnSubscribePortConflict(cfg)
		if err := transport.ServeWebSocket(ctx, cfg.WSListen, cfg.WSPath, innerTLS, func(sess transport.Session) {
			s.handleSession(ctx, sess)
		}, func(mux *http.ServeMux) {
			mux.HandleFunc("/_lynx/v1/version", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				_, _ = fmt.Fprintf(w, `{"service":"lynx-server","version":%q,"subscribe_tokens":%d}`+"\n", version.Version, nTok)
			})
			// Health (no token): distinguishes "route missing" from "bad token".
			mux.HandleFunc(strings.TrimSuffix(sub.Prefix(), "/"), sub.ServeHealth)
			mux.Handle(sub.Prefix(), sub)
		}); err != nil && ctx.Err() == nil {
			log.Printf("WebSocket server: %v", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Printf("shutting down")
}

func warnSubscribePortConflict(cfg *config.Server) {
	// public_base_url host:port must not collide with direct_listen (mTLS).
	pub := strings.TrimPrefix(strings.TrimPrefix(cfg.PublicBaseURL, "https://"), "http://")
	if !strings.Contains(pub, ":") {
		return
	}
	pubPort := pub[strings.LastIndex(pub, ":")+1:]
	_, directPort, err := net.SplitHostPort(cfg.DirectListen)
	if err != nil {
		// ":8443" form
		if strings.HasPrefix(cfg.DirectListen, ":") {
			directPort = strings.TrimPrefix(cfg.DirectListen, ":")
		} else {
			return
		}
	}
	if pubPort != "" && pubPort == directPort {
		log.Printf("WARNING: public_base_url port %s equals direct_listen %s — nginx subscribe cannot share the mTLS direct port; use https://host (443) for subscribe and :8443 for direct", pubPort, cfg.DirectListen)
	}
}

func (s *server) handleSession(parent context.Context, tr transport.Session) {
	defer tr.Close()
	fp := config.NormalizeFingerprint(tr.PeerCertificateSHA256())
	if fp == "" {
		log.Printf("%s peer has no client certificate", tr.Kind())
		return
	}
	device, ok := s.authorize(fp)
	if !ok {
		_ = tr.WriteControl(proto.FrameError, []byte("unauthorized client certificate"))
		log.Printf("rejected unauthorized client fingerprint=%s", fp[:16])
		return
	}

	srcIP := hostOnly(tr.RemoteAddr())
	if !s.acquireSession(fp, srcIP) {
		_ = tr.WriteControl(proto.FrameError, []byte("session limit exceeded"))
		log.Printf("rejected client %s: session limit", device)
		return
	}
	defer s.releaseSession(fp, srcIP)

	typ, payload, err := tr.ReadControl()
	if err != nil {
		log.Printf("%s read hello: %v", tr.Kind(), err)
		return
	}
	if typ != proto.FrameHello {
		log.Printf("%s expected hello, got 0x%x", tr.Kind(), typ)
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

	log.Printf("proxy client %s connected via %s", device, tr.Kind())
	err = proxymux.Serve(parent, tr, proxymux.ServerOptions{
		MaxFlows:          s.cfg.MaxProxyFlowsPerSession,
		MaxNewFlowsPerSec: s.cfg.Security.MaxNewFlowsPerSecond,
		IdleTimeout:       s.cfg.Security.FlowIdleTimeout(),
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
		log.Printf("proxy client %s disconnected: %v", device, err)
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
