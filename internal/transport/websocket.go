package transport

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	wsGUID        = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	wsSubprotocol = "lynx.v1"
	maxWSMessage  = 2 << 20
)

type bufferedConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) { return c.r.Read(p) }

type wsConn struct {
	conn       net.Conn
	client     bool
	readMu     sync.Mutex
	writeMu    sync.Mutex
	readBuf    bytes.Reader
	fragment   bytes.Buffer
	fragmented bool
}

func newWSConn(conn net.Conn, client bool) net.Conn { return &wsConn{conn: conn, client: client} }

func (c *wsConn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	for c.readBuf.Len() == 0 {
		msg, err := c.readMessage()
		if err != nil {
			return 0, err
		}
		c.readBuf.Reset(msg)
	}
	return c.readBuf.Read(p)
}

func (c *wsConn) readMessage() ([]byte, error) {
	for {
		fin, opcode, payload, err := c.readFrame()
		if err != nil {
			return nil, err
		}
		switch opcode {
		case 0x8:
			return nil, io.EOF
		case 0x9:
			if err := c.writeFrame(0xA, payload); err != nil {
				return nil, err
			}
			continue
		case 0xA:
			continue
		case 0x2:
			if c.fragmented {
				return nil, fmt.Errorf("unexpected binary frame during fragmentation")
			}
			if fin {
				return payload, nil
			}
			c.fragment.Reset()
			c.fragment.Write(payload)
			c.fragmented = true
		case 0x0:
			if !c.fragmented {
				return nil, fmt.Errorf("unexpected continuation frame")
			}
			if c.fragment.Len()+len(payload) > maxWSMessage {
				return nil, fmt.Errorf("websocket message too large")
			}
			c.fragment.Write(payload)
			if fin {
				out := append([]byte(nil), c.fragment.Bytes()...)
				c.fragment.Reset()
				c.fragmented = false
				return out, nil
			}
		default:
			return nil, fmt.Errorf("unsupported websocket opcode 0x%x", opcode)
		}
	}
}

func (c *wsConn) readFrame() (bool, byte, []byte, error) {
	h := make([]byte, 2)
	if _, err := io.ReadFull(c.conn, h); err != nil {
		return false, 0, nil, err
	}
	fin := h[0]&0x80 != 0
	opcode := h[0] & 0x0f
	masked := h[1]&0x80 != 0
	if c.client && masked {
		return false, 0, nil, fmt.Errorf("masked server websocket frame")
	}
	if !c.client && !masked {
		return false, 0, nil, fmt.Errorf("unmasked client websocket frame")
	}
	length := uint64(h[1] & 0x7f)
	switch length {
	case 126:
		b := make([]byte, 2)
		if _, err := io.ReadFull(c.conn, b); err != nil {
			return false, 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(b))
	case 127:
		b := make([]byte, 8)
		if _, err := io.ReadFull(c.conn, b); err != nil {
			return false, 0, nil, err
		}
		length = binary.BigEndian.Uint64(b)
	}
	if length > maxWSMessage {
		return false, 0, nil, fmt.Errorf("websocket frame too large: %d", length)
	}
	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(c.conn, mask[:]); err != nil {
			return false, 0, nil, err
		}
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(c.conn, payload); err != nil {
		return false, 0, nil, err
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return fin, opcode, payload, nil
}

func (c *wsConn) Write(p []byte) (int, error) {
	if err := c.writeFrame(0x2, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *wsConn) writeFrame(opcode byte, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if len(payload) > maxWSMessage {
		return fmt.Errorf("websocket message too large: %d", len(payload))
	}
	var h bytes.Buffer
	h.WriteByte(0x80 | opcode)
	maskBit := byte(0)
	if c.client {
		maskBit = 0x80
	}
	switch {
	case len(payload) < 126:
		h.WriteByte(maskBit | byte(len(payload)))
	case len(payload) <= 65535:
		h.WriteByte(maskBit | 126)
		var b [2]byte
		binary.BigEndian.PutUint16(b[:], uint16(len(payload)))
		h.Write(b[:])
	default:
		h.WriteByte(maskBit | 127)
		var b [8]byte
		binary.BigEndian.PutUint64(b[:], uint64(len(payload)))
		h.Write(b[:])
	}
	data := payload
	if c.client {
		var mask [4]byte
		if _, err := rand.Read(mask[:]); err != nil {
			return err
		}
		h.Write(mask[:])
		data = append([]byte(nil), payload...)
		for i := range data {
			data[i] ^= mask[i%4]
		}
	}
	if err := writeAll(c.conn, h.Bytes()); err != nil {
		return err
	}
	return writeAll(c.conn, data)
}

func (c *wsConn) Close() error                       { return c.conn.Close() }
func (c *wsConn) LocalAddr() net.Addr                { return c.conn.LocalAddr() }
func (c *wsConn) RemoteAddr() net.Addr               { return c.conn.RemoteAddr() }
func (c *wsConn) SetDeadline(t time.Time) error      { return c.conn.SetDeadline(t) }
func (c *wsConn) SetReadDeadline(t time.Time) error  { return c.conn.SetReadDeadline(t) }
func (c *wsConn) SetWriteDeadline(t time.Time) error { return c.conn.SetWriteDeadline(t) }

func ServeWebSocket(ctx context.Context, addr, path string, innerTLS *tls.Config, handler func(Session), extra ...func(*http.ServeMux)) error {
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		if !headerHasToken(r.Header, "Connection", "upgrade") || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") || r.Header.Get("Sec-WebSocket-Version") != "13" {
			http.Error(w, "websocket upgrade required", http.StatusUpgradeRequired)
			return
		}
		if !headerHasToken(r.Header, "Sec-WebSocket-Protocol", wsSubprotocol) {
			http.Error(w, "subprotocol required", http.StatusBadRequest)
			return
		}
		key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
		if key == "" {
			http.Error(w, "missing websocket key", http.StatusBadRequest)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijacking unsupported", http.StatusInternalServerError)
			return
		}
		conn, rw, err := hj.Hijack()
		if err != nil {
			return
		}
		accept := websocketAccept(key)
		_, _ = fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\nSec-WebSocket-Protocol: %s\r\n\r\n", accept, wsSubprotocol)
		if err := rw.Flush(); err != nil {
			_ = conn.Close()
			return
		}
		bc := &bufferedConn{Conn: conn, r: rw.Reader}
		inner := tls.Server(newWSConn(bc, false), innerTLS.Clone())
		hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if err := inner.HandshakeContext(hctx); err != nil {
			_ = inner.Close()
			return
		}
		go handler(NewStreamSession("cloudflare-wss", inner, inner.ConnectionState()))
	})
	for _, reg := range extra {
		if reg != nil {
			reg(mux)
		}
	}
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	err := srv.ListenAndServe()
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func DialWebSocket(ctx context.Context, rawURL string, innerTLS *tls.Config, accessID, accessSecret string) (Session, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse WebSocket URL: %w", err)
	}
	if u.Scheme != "wss" {
		return nil, fmt.Errorf("WebSocket URL must use wss://")
	}
	hostPort := u.Host
	if !strings.Contains(hostPort, ":") {
		hostPort += ":443"
	}
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 20 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", hostPort)
	if err != nil {
		return nil, fmt.Errorf("dial Cloudflare: %w", err)
	}
	outer := tls.Client(raw, &tls.Config{MinVersion: tls.VersionTLS13, ServerName: u.Hostname()})
	if err := outer.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("outer TLS handshake: %w", err)
	}

	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		_ = outer.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(nonce[:])
	path := u.RequestURI()
	if path == "" {
		path = "/"
	}
	bw := bufio.NewWriter(outer)
	_, _ = fmt.Fprintf(bw, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Protocol: %s\r\n", path, u.Host, key, wsSubprotocol)
	if accessID != "" {
		_, _ = fmt.Fprintf(bw, "CF-Access-Client-Id: %s\r\n", accessID)
	}
	if accessSecret != "" {
		_, _ = fmt.Fprintf(bw, "CF-Access-Client-Secret: %s\r\n", accessSecret)
	}
	_, _ = fmt.Fprint(bw, "\r\n")
	if err := bw.Flush(); err != nil {
		_ = outer.Close()
		return nil, err
	}

	br := bufio.NewReader(outer)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		_ = outer.Close()
		return nil, fmt.Errorf("read WebSocket response: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		_ = outer.Close()
		return nil, fmt.Errorf("Cloudflare WebSocket returned %s", resp.Status)
	}
	if strings.TrimSpace(resp.Header.Get("Sec-WebSocket-Accept")) != websocketAccept(key) {
		_ = outer.Close()
		return nil, fmt.Errorf("invalid WebSocket accept value")
	}
	bc := &bufferedConn{Conn: outer, r: br}
	inner := tls.Client(newWSConn(bc, true), innerTLS.Clone())
	if err := inner.HandshakeContext(ctx); err != nil {
		_ = inner.Close()
		return nil, fmt.Errorf("inner TLS handshake: %w", err)
	}
	return NewStreamSession("cloudflare-wss", inner, inner.ConnectionState()), nil
}

func websocketAccept(key string) string {
	h := sha1.Sum([]byte(key + wsGUID))
	return base64.StdEncoding.EncodeToString(h[:])
}

func headerHasToken(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

func writeAll(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}
