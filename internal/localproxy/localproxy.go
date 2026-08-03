package localproxy

import (
	"bufio"
	"context"
	"crypto/subtle"
	"encoding/base64"
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

type Config struct {
	SOCKSListen string
	HTTPListen  string
	Username    string
	Password    string
	Open        OpenFunc
	Logger      *log.Logger
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
	target, err := socksReadConnect(br)
	if err != nil {
		_ = writeSOCKSReply(conn, 0x07)
		return
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	remote, err := cfg.Open(ctx, target)
	cancel()
	if err != nil {
		_ = writeSOCKSReply(conn, 0x05)
		return
	}
	defer remote.Close()
	if err := writeSOCKSReply(conn, 0x00); err != nil {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	proxyBidirectional(conn, br, remote)
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

func socksReadConnect(br *bufio.Reader) (string, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(br, header); err != nil {
		return "", err
	}
	if header[0] != 0x05 || header[1] != 0x01 || header[2] != 0x00 {
		return "", errors.New("only SOCKS5 CONNECT is supported")
	}
	var host string
	switch header[3] {
	case 0x01:
		b := make([]byte, 4)
		if _, err := io.ReadFull(br, b); err != nil {
			return "", err
		}
		host = net.IP(b).String()
	case 0x03:
		n, err := br.ReadByte()
		if err != nil || n == 0 {
			return "", errors.New("invalid SOCKS5 domain")
		}
		b := make([]byte, int(n))
		if _, err := io.ReadFull(br, b); err != nil {
			return "", err
		}
		host = string(b)
	case 0x04:
		b := make([]byte, 16)
		if _, err := io.ReadFull(br, b); err != nil {
			return "", err
		}
		host = net.IP(b).String()
	default:
		return "", errors.New("unsupported SOCKS5 address type")
	}
	portBytes := make([]byte, 2)
	if _, err := io.ReadFull(br, portBytes); err != nil {
		return "", err
	}
	port := int(portBytes[0])<<8 | int(portBytes[1])
	if port <= 0 {
		return "", errors.New("invalid SOCKS5 port")
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func writeSOCKSReply(w io.Writer, code byte) error {
	_, err := w.Write([]byte{0x05, code, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	return err
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
