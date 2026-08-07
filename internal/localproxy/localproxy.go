package localproxy

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type OpenFunc func(context.Context, string) (net.Conn, error)

// UDPRelay is a multiplexed UDP association used by SOCKS5 UDP ASSOCIATE.
type UDPRelay interface {
	WriteTo(p []byte, addr string) (int, error)
	ReadFrom(p []byte) (n int, addr string, err error)
	Close() error
}

type AssociateUDPFunc func(context.Context) (UDPRelay, error)

type Config struct {
	SOCKSListen  string
	HTTPListen   string
	Username     string
	Password     string
	Open         OpenFunc
	AssociateUDP AssociateUDPFunc // optional; if nil, ASSOCIATE returns command not supported
	Logger       *log.Logger
}

func Serve(ctx context.Context, cfg Config) error {
	if cfg.Open == nil {
		return errors.New("proxy open function is required")
	}
	if cfg.Logger == nil {
		cfg.Logger = log.Default()
	}
	var listeners []net.Listener
	for kind, addr := range map[string]string{"SOCKS5": cfg.SOCKSListen, "HTTP": cfg.HTTPListen} {
		if strings.TrimSpace(addr) == "" {
			continue
		}
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			for _, opened := range listeners {
				_ = opened.Close()
			}
			return fmt.Errorf("listen %s proxy on %s: %w", kind, addr, err)
		}
		listeners = append(listeners, ln)
		cfg.Logger.Printf("%s proxy listening on %s", kind, ln.Addr())
		if kind == "SOCKS5" {
			go acceptLoop(ctx, ln, cfg.Logger, func(c net.Conn) { handleSOCKS(ctx, c, cfg) })
		} else {
			go acceptLoop(ctx, ln, cfg.Logger, func(c net.Conn) { handleHTTP(ctx, c, cfg) })
		}
	}
	if len(listeners) == 0 {
		return errors.New("at least one local proxy listener must be configured")
	}
	<-ctx.Done()
	for _, ln := range listeners {
		_ = ln.Close()
	}
	return nil
}

func acceptLoop(ctx context.Context, ln net.Listener, logger *log.Logger, handler func(net.Conn)) {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() == nil {
				logger.Printf("accept %s: %v", ln.Addr(), err)
			}
			return
		}
		go handler(conn)
	}
}

func handleSOCKS(parent context.Context, conn net.Conn, cfg Config) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	br := bufio.NewReader(conn)
	method, err := socksNegotiate(br, conn, cfg.Username != "")
	if err != nil {
		return
	}
	if method == 0x02 {
		if err := socksAuthenticate(br, conn, cfg.Username, cfg.Password); err != nil {
			return
		}
	}
	cmd, target, err := socksReadRequest(br)
	if err != nil {
		_ = writeSOCKSReply(conn, 0x07, nil)
		return
	}
	switch cmd {
	case 0x01: // CONNECT
		ctx, cancel := context.WithTimeout(parent, 20*time.Second)
		remote, err := cfg.Open(ctx, target)
		cancel()
		if err != nil {
			_ = writeSOCKSReply(conn, 0x05, nil)
			return
		}
		defer remote.Close()
		if err := writeSOCKSReply(conn, 0x00, nil); err != nil {
			return
		}
		_ = conn.SetDeadline(time.Time{})
		proxyBidirectional(conn, br, remote)
	case 0x03: // UDP ASSOCIATE
		if cfg.AssociateUDP == nil {
			_ = writeSOCKSReply(conn, 0x07, nil)
			return
		}
		handleSOCKSAssociate(parent, conn, br, cfg)
	default:
		_ = writeSOCKSReply(conn, 0x07, nil)
	}
}

func handleSOCKSAssociate(parent context.Context, conn net.Conn, _ *bufio.Reader, cfg Config) {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	assocCtx, assocCancel := context.WithTimeout(ctx, 20*time.Second)
	relay, err := cfg.AssociateUDP(assocCtx)
	assocCancel()
	if err != nil {
		_ = writeSOCKSReply(conn, 0x05, nil)
		return
	}
	defer relay.Close()

	udpHost := "127.0.0.1"
	if la, ok := conn.LocalAddr().(*net.TCPAddr); ok && la.IP != nil && !la.IP.IsUnspecified() {
		udpHost = la.IP.String()
	}
	pc, err := net.ListenPacket("udp", net.JoinHostPort(udpHost, "0"))
	if err != nil {
		_ = writeSOCKSReply(conn, 0x01, nil)
		return
	}
	defer pc.Close()

	if err := writeSOCKSReply(conn, 0x00, pc.LocalAddr()); err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})

	clientIP := ""
	if ra, ok := conn.RemoteAddr().(*net.TCPAddr); ok && ra.IP != nil {
		clientIP = ra.IP.String()
	}

	var peerMu sync.Mutex
	var lastPeer net.Addr

	go func() {
		buf := make([]byte, 64*1024)
		for {
			n, raddr, err := pc.ReadFrom(buf)
			if err != nil {
				cancel()
				return
			}
			if clientIP != "" {
				if ua, ok := raddr.(*net.UDPAddr); ok && ua.IP != nil && ua.IP.String() != clientIP {
					continue
				}
			}
			peerMu.Lock()
			lastPeer = raddr
			peerMu.Unlock()
			dst, payload, err := parseSOCKSUDP(buf[:n])
			if err != nil {
				continue
			}
			if _, err := relay.WriteTo(payload, dst); err != nil {
				cancel()
				return
			}
		}
	}()

	go func() {
		buf := make([]byte, 64*1024)
		for {
			n, addr, err := relay.ReadFrom(buf)
			if err != nil {
				cancel()
				return
			}
			pkt, err := encodeSOCKSUDP(addr, buf[:n])
			if err != nil {
				continue
			}
			peerMu.Lock()
			peer := lastPeer
			peerMu.Unlock()
			if peer == nil {
				continue
			}
			_ = pc.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err := pc.WriteTo(pkt, peer); err != nil {
				cancel()
				return
			}
		}
	}()

	// Keep ASSOCIATION alive until the TCP control connection drops.
	tmp := make([]byte, 1)
	_, _ = conn.Read(tmp)
	cancel()
	_ = pc.Close()
	_ = relay.Close()
}

func socksNegotiate(br *bufio.Reader, w io.Writer, auth bool) (byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(br, header); err != nil {
		return 0, err
	}
	if header[0] != 0x05 || header[1] == 0 {
		return 0, errors.New("invalid SOCKS5 greeting")
	}
	methods := make([]byte, int(header[1]))
	if _, err := io.ReadFull(br, methods); err != nil {
		return 0, err
	}
	wanted := byte(0x00)
	if auth {
		wanted = 0x02
	}
	selected := byte(0xff)
	for _, m := range methods {
		if m == wanted {
			selected = wanted
			break
		}
	}
	if _, err := w.Write([]byte{0x05, selected}); err != nil {
		return 0, err
	}
	if selected == 0xff {
		return 0, errors.New("no acceptable SOCKS5 authentication method")
	}
	return selected, nil
}

func socksAuthenticate(br *bufio.Reader, w io.Writer, username, password string) error {
	header := make([]byte, 2)
	if _, err := io.ReadFull(br, header); err != nil {
		return err
	}
	if header[0] != 0x01 {
		return errors.New("invalid SOCKS5 username/password version")
	}
	user := make([]byte, int(header[1]))
	if _, err := io.ReadFull(br, user); err != nil {
		return err
	}
	plen, err := br.ReadByte()
	if err != nil {
		return err
	}
	pass := make([]byte, int(plen))
	if _, err := io.ReadFull(br, pass); err != nil {
		return err
	}
	ok := secureEqual(string(user), username) && secureEqual(string(pass), password)
	status := byte(0x01)
	if ok {
		status = 0x00
	}
	_, _ = w.Write([]byte{0x01, status})
	if !ok {
		return errors.New("SOCKS5 authentication failed")
	}
	return nil
}

func socksReadRequest(br *bufio.Reader) (cmd byte, target string, err error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(br, header); err != nil {
		return 0, "", err
	}
	if header[0] != 0x05 || header[2] != 0x00 {
		return 0, "", errors.New("invalid SOCKS5 request")
	}
	cmd = header[1]
	host, port, err := socksReadAddress(br, header[3])
	if err != nil {
		return 0, "", err
	}
	if cmd == 0x01 && port <= 0 {
		return 0, "", errors.New("invalid SOCKS5 port")
	}
	if port < 0 {
		port = 0
	}
	return cmd, net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func socksReadAddress(br *bufio.Reader, atyp byte) (host string, port int, err error) {
	switch atyp {
	case 0x01:
		b := make([]byte, 4)
		if _, err := io.ReadFull(br, b); err != nil {
			return "", 0, err
		}
		host = net.IP(b).String()
	case 0x03:
		n, err := br.ReadByte()
		if err != nil || n == 0 {
			return "", 0, errors.New("invalid SOCKS5 domain")
		}
		b := make([]byte, int(n))
		if _, err := io.ReadFull(br, b); err != nil {
			return "", 0, err
		}
		host = string(b)
	case 0x04:
		b := make([]byte, 16)
		if _, err := io.ReadFull(br, b); err != nil {
			return "", 0, err
		}
		host = net.IP(b).String()
	default:
		return "", 0, errors.New("unsupported SOCKS5 address type")
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(br, portBytes); err != nil {
		return "", 0, err
	}
	port = int(portBytes[0])<<8 | int(portBytes[1])
	return host, port, nil
}

func writeSOCKSReply(w io.Writer, code byte, bind net.Addr) error {
	ip := net.IPv4zero
	port := 0
	atyp := byte(0x01)
	if bind != nil {
		switch a := bind.(type) {
		case *net.UDPAddr:
			if a.IP != nil {
				if v4 := a.IP.To4(); v4 != nil {
					ip = v4
					atyp = 0x01
				} else {
					ip = a.IP.To16()
					atyp = 0x04
				}
			}
			port = a.Port
		case *net.TCPAddr:
			if a.IP != nil {
				if v4 := a.IP.To4(); v4 != nil {
					ip = v4
					atyp = 0x01
				} else {
					ip = a.IP.To16()
					atyp = 0x04
				}
			}
			port = a.Port
		}
	}
	var reply []byte
	if atyp == 0x04 {
		reply = make([]byte, 4+16+2)
		reply[0], reply[1], reply[2], reply[3] = 0x05, code, 0x00, 0x04
		copy(reply[4:20], ip)
		binary.BigEndian.PutUint16(reply[20:22], uint16(port))
	} else {
		reply = make([]byte, 10)
		reply[0], reply[1], reply[2], reply[3] = 0x05, code, 0x00, 0x01
		copy(reply[4:8], ip.To4())
		binary.BigEndian.PutUint16(reply[8:10], uint16(port))
	}
	_, err := w.Write(reply)
	return err
}

func parseSOCKSUDP(pkt []byte) (dst string, payload []byte, err error) {
	if len(pkt) < 4 {
		return "", nil, errors.New("udp packet too short")
	}
	if pkt[0] != 0 || pkt[1] != 0 {
		return "", nil, errors.New("invalid socks udp rsv")
	}
	if pkt[2] != 0 {
		return "", nil, errors.New("socks udp fragmentation not supported")
	}
	atyp := pkt[3]
	rest := pkt[4:]
	var host string
	var port int
	switch atyp {
	case 0x01:
		if len(rest) < 6 {
			return "", nil, errors.New("udp ipv4 truncated")
		}
		host = net.IP(rest[:4]).String()
		port = int(rest[4])<<8 | int(rest[5])
		payload = rest[6:]
	case 0x03:
		if len(rest) < 1 {
			return "", nil, errors.New("udp domain truncated")
		}
		n := int(rest[0])
		if n == 0 || len(rest) < 1+n+2 {
			return "", nil, errors.New("udp domain truncated")
		}
		host = string(rest[1 : 1+n])
		port = int(rest[1+n])<<8 | int(rest[1+n+1])
		payload = rest[1+n+2:]
	case 0x04:
		if len(rest) < 18 {
			return "", nil, errors.New("udp ipv6 truncated")
		}
		host = net.IP(rest[:16]).String()
		port = int(rest[16])<<8 | int(rest[17])
		payload = rest[18:]
	default:
		return "", nil, errors.New("unsupported socks udp atyp")
	}
	if port <= 0 {
		return "", nil, errors.New("invalid socks udp port")
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), payload, nil
}

func encodeSOCKSUDP(addr string, payload []byte) ([]byte, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port < 0 || port > 65535 {
		return nil, errors.New("invalid port")
	}
	host = strings.Trim(host, "[]")
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			out := make([]byte, 10+len(payload))
			out[3] = 0x01
			copy(out[4:8], v4)
			binary.BigEndian.PutUint16(out[8:10], uint16(port))
			copy(out[10:], payload)
			return out, nil
		}
		v6 := ip.To16()
		out := make([]byte, 22+len(payload))
		out[3] = 0x04
		copy(out[4:20], v6)
		binary.BigEndian.PutUint16(out[20:22], uint16(port))
		copy(out[22:], payload)
		return out, nil
	}
	if len(host) > 255 {
		return nil, errors.New("domain too long")
	}
	out := make([]byte, 7+len(host)+len(payload))
	out[3] = 0x03
	out[4] = byte(len(host))
	copy(out[5:5+len(host)], host)
	binary.BigEndian.PutUint16(out[5+len(host):7+len(host)], uint16(port))
	copy(out[7+len(host):], payload)
	return out, nil
}

func handleHTTP(parent context.Context, conn net.Conn, cfg Config) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	br := bufio.NewReader(conn)
	req, err := http.ReadRequest(br)
	if err != nil {
		writeHTTPError(conn, http.StatusBadRequest, "Bad proxy request")
		return
	}
	if req.Body != nil {
		defer req.Body.Close()
	}
	if !httpAuthorized(req, cfg.Username, cfg.Password) {
		_, _ = io.WriteString(conn, "HTTP/1.1 407 Proxy Authentication Required\r\nProxy-Authenticate: Basic realm=\"Lynx\"\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		return
	}
	target, err := httpTarget(req)
	if err != nil {
		writeHTTPError(conn, http.StatusBadRequest, "Invalid target")
		return
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	remote, err := cfg.Open(ctx, target)
	cancel()
	if err != nil {
		writeHTTPError(conn, http.StatusBadGateway, "Target connection failed")
		return
	}
	defer remote.Close()
	_ = conn.SetDeadline(time.Time{})

	if req.Method == http.MethodConnect {
		_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\nProxy-Agent: Lynx\r\n\r\n")
		proxyBidirectional(conn, br, remote)
		return
	}

	req.Header.Del("Proxy-Authorization")
	req.Header.Del("Proxy-Connection")
	req.Header.Set("Connection", "close")
	if req.URL == nil {
		req.URL = &url.URL{}
	}
	req.URL.Scheme = ""
	req.URL.Host = ""
	req.RequestURI = ""
	if err := req.Write(remote); err != nil {
		return
	}
	_, _ = io.Copy(conn, remote)
}

func httpAuthorized(req *http.Request, username, password string) bool {
	if username == "" {
		return true
	}
	raw := req.Header.Get("Proxy-Authorization")
	if raw == "" {
		return false
	}
	parts := strings.SplitN(raw, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Basic") {
		return false
	}
	decoded, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return false
	}
	pair := strings.SplitN(string(decoded), ":", 2)
	return len(pair) == 2 && secureEqual(pair[0], username) && secureEqual(pair[1], password)
}

func httpTarget(req *http.Request) (string, error) {
	host := req.Host
	if req.URL != nil && req.URL.Host != "" {
		host = req.URL.Host
	}
	if host == "" {
		return "", errors.New("missing host")
	}
	if _, _, err := net.SplitHostPort(host); err == nil {
		return host, nil
	}
	if strings.Contains(host, ":") && net.ParseIP(strings.Trim(host, "[]")) != nil {
		if req.Method == http.MethodConnect {
			return net.JoinHostPort(strings.Trim(host, "[]"), "443"), nil
		}
		return net.JoinHostPort(strings.Trim(host, "[]"), "80"), nil
	}
	port := "80"
	if req.Method == http.MethodConnect {
		port = "443"
	}
	return net.JoinHostPort(host, port), nil
}

func writeHTTPError(w io.Writer, status int, message string) {
	body := message + "\n"
	_, _ = fmt.Fprintf(w, "HTTP/1.1 %d %s\r\nContent-Type: text/plain\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s", status, http.StatusText(status), len(body), body)
}

func proxyBidirectional(local net.Conn, localReader io.Reader, remote net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(remote, localReader)
		if cw, ok := remote.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(local, remote)
		if tcp, ok := local.(*net.TCPConn); ok {
			_ = tcp.CloseWrite()
		}
	}()
	wg.Wait()
}

func secureEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
