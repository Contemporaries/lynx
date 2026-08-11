package flowlog

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Logger is the subset of logx used for traffic summaries.
type Logger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// TCP wraps a net.Conn and emits open/close traffic logs.
type TCP struct {
	net.Conn
	log      Logger
	id       uint32
	target   string
	path     string
	via      string
	device   string
	start    time.Time
	up       atomic.Int64
	down     atomic.Int64
	once     sync.Once
	openLogged bool
}

func WrapTCP(conn net.Conn, log Logger, id uint32, target, path, via, device string) *TCP {
	c := &TCP{
		Conn:   conn,
		log:    log,
		id:     id,
		target: target,
		path:   path,
		via:    via,
		device: device,
		start:  time.Now(),
	}
	if log != nil {
		args := []any{"id", id, "net", "tcp", "target", target}
		if path != "" {
			args = append(args, "path", path)
		}
		if via != "" {
			args = append(args, "via", via)
		}
		if device != "" {
			args = append(args, "device", device)
		}
		log.Info("flow open", args...)
		c.openLogged = true
	}
	return c
}

func (c *TCP) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.down.Add(int64(n))
	}
	return n, err
}

func (c *TCP) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.up.Add(int64(n))
	}
	return n, err
}

func (c *TCP) Close() error {
	err := c.Conn.Close()
	c.finish(err)
	return err
}

func (c *TCP) finish(closeErr error) {
	c.once.Do(func() {
		if c.log == nil || !c.openLogged {
			return
		}
		ok := closeErr == nil || closeErr == io.EOF
		args := []any{
			"id", c.id,
			"target", c.target,
			"ok", ok,
			"up", c.up.Load(),
			"down", c.down.Load(),
			"duration", time.Since(c.start).Round(time.Millisecond).String(),
		}
		if c.path != "" {
			args = append(args, "path", c.path)
		}
		if c.via != "" {
			args = append(args, "via", c.via)
		}
		if c.device != "" {
			args = append(args, "device", c.device)
		}
		c.log.Info("flow close", args...)
	})
}

// UDPRelay is the localproxy UDP association surface.
type UDPRelay interface {
	WriteTo(p []byte, addr string) (int, error)
	ReadFrom(p []byte) (n int, addr string, err error)
	Close() error
}

// UDP wraps a UDP association and emits open/close summaries.
type UDP struct {
	inner        UDPRelay
	log          Logger
	id           uint32
	path         string
	via          string
	device       string
	start        time.Time
	up           atomic.Int64
	down         atomic.Int64
	packetsUp    atomic.Int64
	packetsDown  atomic.Int64
	once         sync.Once
}

func WrapUDP(inner UDPRelay, log Logger, id uint32, path, via, device string) *UDP {
	u := &UDP{
		inner:  inner,
		log:    log,
		id:     id,
		path:   path,
		via:    via,
		device: device,
		start:  time.Now(),
	}
	if log != nil {
		args := []any{"id", id, "net", "udp"}
		if path != "" {
			args = append(args, "path", path)
		}
		if via != "" {
			args = append(args, "via", via)
		}
		if device != "" {
			args = append(args, "device", device)
		}
		log.Info("udp associate open", args...)
	}
	return u
}

func (u *UDP) WriteTo(p []byte, addr string) (int, error) {
	n, err := u.inner.WriteTo(p, addr)
	if n > 0 {
		u.up.Add(int64(n))
		u.packetsUp.Add(1)
	}
	if u.log != nil {
		u.log.Debug("udp packet up", "id", u.id, "dst", addr, "bytes", n)
	}
	return n, err
}

func (u *UDP) ReadFrom(p []byte) (int, string, error) {
	n, addr, err := u.inner.ReadFrom(p)
	if n > 0 {
		u.down.Add(int64(n))
		u.packetsDown.Add(1)
	}
	if u.log != nil && n > 0 {
		u.log.Debug("udp packet down", "id", u.id, "src", addr, "bytes", n)
	}
	return n, addr, err
}

func (u *UDP) Close() error {
	err := u.inner.Close()
	u.once.Do(func() {
		if u.log == nil {
			return
		}
		args := []any{
			"id", u.id,
			"packets_up", u.packetsUp.Load(),
			"packets_down", u.packetsDown.Load(),
			"bytes_up", u.up.Load(),
			"bytes_down", u.down.Load(),
			"duration", time.Since(u.start).Round(time.Millisecond).String(),
		}
		if u.path != "" {
			args = append(args, "path", u.path)
		}
		if u.via != "" {
			args = append(args, "via", u.via)
		}
		if u.device != "" {
			args = append(args, "device", u.device)
		}
		u.log.Info("udp associate close", args...)
	})
	return err
}

// CountingConn tracks bytes without logging (used on server when logging at dial site).
type CountingConn struct {
	net.Conn
	Up   atomic.Int64
	Down atomic.Int64
}

func (c *CountingConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 {
		c.Down.Add(int64(n))
	}
	return n, err
}

func (c *CountingConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 {
		c.Up.Add(int64(n))
	}
	return n, err
}
