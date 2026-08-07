package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"time"
)

func ServeDirect(ctx context.Context, addr string, tlsConf *tls.Config, handshakeTimeout time.Duration, handler func(Session)) error {
	if handshakeTimeout <= 0 {
		handshakeTimeout = 10 * time.Second
	}
	ln, err := tls.Listen("tcp", addr, tlsConf)
	if err != nil {
		return err
	}
	defer ln.Close()
	go func() { <-ctx.Done(); _ = ln.Close() }()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go func(c net.Conn) {
			tlsConn, ok := c.(*tls.Conn)
			if !ok {
				_ = c.Close()
				return
			}
			hctx, cancel := context.WithTimeout(ctx, handshakeTimeout)
			defer cancel()
			if err := tlsConn.HandshakeContext(hctx); err != nil {
				_ = c.Close()
				return
			}
			handler(NewStreamSession("direct-tls", tlsConn, tlsConn.ConnectionState()))
		}(conn)
	}
}

func DialDirect(ctx context.Context, addr string, tlsConf *tls.Config) (Session, error) {
	d := &net.Dialer{Timeout: 10 * time.Second, KeepAlive: 20 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("dial direct TCP: %w", err)
	}
	tlsConn := tls.Client(raw, tlsConf)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("direct TLS handshake: %w", err)
	}
	return NewStreamSession("direct-tls", tlsConn, tlsConn.ConnectionState()), nil
}
