package proxymux

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/Contemporaries/lynx/internal/proto"
	"github.com/Contemporaries/lynx/internal/transport"
)

// FlowLogger is an optional traffic logger (typically *logx.Logger).
type FlowLogger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

type ServerOptions struct {
	MaxFlows          int
	MaxNewFlowsPerSec int
	Dial              func(context.Context, string) (net.Conn, error)
	// DialUDP creates the server-side PacketConn for a UDP association.
	// If nil, ListenPacket("udp", ":0") is used.
	DialUDP func(context.Context) (net.PacketConn, error)
	// CheckUDPDestination validates each UDP target before WriteTo.
	// If nil, all destinations are allowed.
	CheckUDPDestination func(context.Context, string) error
	IdleTimeout         time.Duration
	OnFlowOpen          func() bool // optional gate; return false to reject
	OnFlowClose         func()
	Logger              FlowLogger // optional traffic logs
	Device              string     // device name for log fields
	Path                string     // "wss" | "direct"
}

type serverFlow struct {
	id         uint32
	kind       string // "tcp" or "udp"
	target     string
	conn       net.Conn
	pc         net.PacketConn
	cancel     context.CancelFunc
	start      time.Time
	up         int64
	down       int64
	packetsUp  int64
	packetsDown int64
	mu         sync.Mutex
}

type serverMux struct {
	ctx     context.Context
	tr      transport.Session
	opts    ServerOptions
	mu      sync.Mutex
	flows   map[uint32]*serverFlow
	closed  bool
	limiter *tokenBucket
}

func Serve(ctx context.Context, tr transport.Session, opts ServerOptions) error {
	if opts.MaxFlows <= 0 {
		opts.MaxFlows = 256
	}
	if opts.Dial == nil {
		return fmt.Errorf("proxy dial function is required")
	}
	if opts.DialUDP == nil {
		opts.DialUDP = func(context.Context) (net.PacketConn, error) {
			return net.ListenPacket("udp", ":0")
		}
	}
	if opts.IdleTimeout <= 0 {
		opts.IdleTimeout = 10 * time.Minute
	}
	m := &serverMux{ctx: ctx, tr: tr, opts: opts, flows: make(map[uint32]*serverFlow)}
	if opts.MaxNewFlowsPerSec > 0 {
		m.limiter = newTokenBucket(opts.MaxNewFlowsPerSec)
	}
	cfg, _ := json.Marshal(proto.ProxyConfig{MaxFlows: opts.MaxFlows})
	if err := tr.WriteControl(proto.FrameConfig, cfg); err != nil {
		return err
	}
	defer m.closeAll()
	for {
		typ, payload, err := tr.ReadControl()
		if err != nil {
			return err
		}
		switch typ {
		case proto.FrameProxyOpen:
			req, err := proto.ReadJSON[proto.ProxyOpen](payload)
			if err != nil {
				return err
			}
			m.open(req)
		case proto.FrameProxyData:
			id, data, err := proto.DecodeFlowPayload(payload)
			if err != nil {
				return err
			}
			if err := m.writeFlow(id, data); err != nil {
				m.closeFlow(id, true)
			}
		case proto.FrameProxyDatagram:
			id, addr, data, err := proto.DecodeDatagram(payload)
			if err != nil {
				return err
			}
			m.writeDatagram(id, addr, data)
		case proto.FrameProxyEOF:
			id, err := proto.DecodeFlowID(payload)
			if err != nil {
				return err
			}
			m.closeWrite(id)
		case proto.FrameProxyClose:
			id, err := proto.DecodeFlowID(payload)
			if err != nil {
				return err
			}
			m.closeFlow(id, false)
		case proto.FramePing:
			if err := tr.WriteControl(proto.FramePong, payload); err != nil {
				return err
			}
		case proto.FramePong:
			continue
		default:
			return fmt.Errorf("unexpected proxy frame 0x%x", typ)
		}
	}
}

func (m *serverMux) open(req proto.ProxyOpen) {
	switch req.Network {
	case "tcp":
		m.openTCP(req)
	case "udp":
		m.openUDP(req)
	default:
		m.writeResult(proto.ProxyResult{FlowID: req.FlowID, Error: "invalid proxy request"})
	}
}

func (m *serverMux) reserveFlow(req proto.ProxyOpen, kind string) (*serverFlow, string) {
	if req.FlowID == 0 {
		return nil, "invalid proxy request"
	}
	if m.limiter != nil && !m.limiter.allow() {
		return nil, "proxy flow rate limited"
	}
	if m.opts.OnFlowOpen != nil && !m.opts.OnFlowOpen() {
		return nil, "proxy flow limit reached"
	}
	m.mu.Lock()
	if m.closed || len(m.flows) >= m.opts.MaxFlows || m.flows[req.FlowID] != nil {
		m.mu.Unlock()
		if m.opts.OnFlowClose != nil {
			m.opts.OnFlowClose()
		}
		return nil, "proxy flow limit reached or duplicate flow id"
	}
	_, cancel := context.WithCancel(m.ctx)
	flow := &serverFlow{id: req.FlowID, kind: kind, cancel: cancel}
	m.flows[req.FlowID] = flow
	m.mu.Unlock()
	return flow, ""
}

func (m *serverMux) openTCP(req proto.ProxyOpen) {
	result := proto.ProxyResult{FlowID: req.FlowID}
	if req.Address == "" {
		result.Error = "invalid proxy request"
		m.writeResult(result)
		return
	}
	flow, errMsg := m.reserveFlow(req, "tcp")
	if flow == nil {
		m.logFlowDenied(req.FlowID, "tcp", req.Address, errMsg)
		result.Error = errMsg
		m.writeResult(result)
		return
	}
	flow.target = req.Address
	flow.start = time.Now()

	go func() {
		conn, err := m.opts.Dial(m.ctx, req.Address)
		if err != nil {
			m.removePending(req.FlowID)
			m.logDialFailed(req.FlowID, req.Address, err)
			result.Error = "target connection failed"
			m.writeResult(result)
			return
		}
		m.mu.Lock()
		current := m.flows[req.FlowID]
		if current != flow || m.closed {
			m.mu.Unlock()
			_ = conn.Close()
			return
		}
		flow.conn = conn
		m.mu.Unlock()
		result.OK = true
		m.writeResult(result)
		m.logFlowOpen(flow)
		m.remoteToClient(flow)
	}()
}

func (m *serverMux) openUDP(req proto.ProxyOpen) {
	result := proto.ProxyResult{FlowID: req.FlowID}
	flow, errMsg := m.reserveFlow(req, "udp")
	if flow == nil {
		m.logFlowDenied(req.FlowID, "udp", "", errMsg)
		result.Error = errMsg
		m.writeResult(result)
		return
	}
	flow.start = time.Now()

	go func() {
		pc, err := m.opts.DialUDP(m.ctx)
		if err != nil {
			m.removePending(req.FlowID)
			if m.opts.Logger != nil {
				m.opts.Logger.Error("udp associate failed", "id", req.FlowID, "device", m.opts.Device, "err", err)
			}
			result.Error = "udp associate failed"
			m.writeResult(result)
			return
		}
		m.mu.Lock()
		current := m.flows[req.FlowID]
		if current != flow || m.closed {
			m.mu.Unlock()
			_ = pc.Close()
			return
		}
		flow.pc = pc
		m.mu.Unlock()
		result.OK = true
		m.writeResult(result)
		m.logFlowOpen(flow)
		m.udpRemoteToClient(flow)
	}()
}

func (m *serverMux) logFlowOpen(flow *serverFlow) {
	if m.opts.Logger == nil {
		return
	}
	if flow.kind == "udp" {
		args := []any{"id", flow.id, "net", "udp"}
		if m.opts.Path != "" {
			args = append(args, "path", m.opts.Path)
		}
		if m.opts.Device != "" {
			args = append(args, "device", m.opts.Device)
		}
		m.opts.Logger.Info("udp associate open", args...)
		return
	}
	args := []any{"id", flow.id, "net", "tcp", "target", flow.target}
	if m.opts.Path != "" {
		args = append(args, "path", m.opts.Path)
	}
	if m.opts.Device != "" {
		args = append(args, "device", m.opts.Device)
	}
	m.opts.Logger.Info("flow open", args...)
}

func (m *serverMux) logFlowClose(flow *serverFlow) {
	if m.opts.Logger == nil || flow == nil {
		return
	}
	flow.mu.Lock()
	up, down := flow.up, flow.down
	pu, pd := flow.packetsUp, flow.packetsDown
	flow.mu.Unlock()
	dur := time.Since(flow.start).Round(time.Millisecond).String()
	if flow.kind == "udp" {
		args := []any{
			"id", flow.id,
			"packets_up", pu,
			"packets_down", pd,
			"bytes_up", up,
			"bytes_down", down,
			"duration", dur,
		}
		if m.opts.Path != "" {
			args = append(args, "path", m.opts.Path)
		}
		if m.opts.Device != "" {
			args = append(args, "device", m.opts.Device)
		}
		m.opts.Logger.Info("udp associate close", args...)
		return
	}
	args := []any{
		"id", flow.id,
		"target", flow.target,
		"ok", true,
		"up", up,
		"down", down,
		"duration", dur,
	}
	if m.opts.Path != "" {
		args = append(args, "path", m.opts.Path)
	}
	if m.opts.Device != "" {
		args = append(args, "device", m.opts.Device)
	}
	m.opts.Logger.Info("flow close", args...)
}

func (m *serverMux) logFlowDenied(id uint32, netw, target, reason string) {
	if m.opts.Logger == nil {
		return
	}
	r := reason
	switch {
	case strings.Contains(reason, "rate limited"):
		r = "rate_limited"
	case strings.Contains(reason, "limit"):
		r = "flow_limit"
	}
	args := []any{"id", id, "net", netw, "reason", r}
	if target != "" {
		args = append(args, "target", target)
	}
	if m.opts.Device != "" {
		args = append(args, "device", m.opts.Device)
	}
	m.opts.Logger.Warn("flow denied", args...)
}

func (m *serverMux) logDialFailed(id uint32, target string, err error) {
	if m.opts.Logger == nil {
		return
	}
	msg := err.Error()
	if strings.Contains(msg, "not allowed") {
		m.opts.Logger.Warn("flow denied", "id", id, "target", target, "reason", "private_network", "device", m.opts.Device)
		return
	}
	m.opts.Logger.Error("flow dial failed", "id", id, "target", target, "device", m.opts.Device, "err", err)
}

func (m *serverMux) writeResult(result proto.ProxyResult) {
	payload, _ := json.Marshal(result)
	_ = m.tr.WriteControl(proto.FrameProxyResult, payload)
}

func (m *serverMux) remoteToClient(flow *serverFlow) {
	buf := make([]byte, 64*1024)
	for {
		n, err := flow.conn.Read(buf)
		if n > 0 {
			flow.mu.Lock()
			flow.down += int64(n)
			flow.mu.Unlock()
			if werr := m.tr.WriteControl(proto.FrameProxyData, proto.EncodeFlowPayload(flow.id, buf[:n])); werr != nil {
				m.closeFlow(flow.id, false)
				return
			}
		}
		if err != nil {
			if err == io.EOF {
				_ = m.tr.WriteControl(proto.FrameProxyEOF, proto.EncodeFlowID(flow.id))
			} else {
				_ = m.tr.WriteControl(proto.FrameProxyClose, proto.EncodeFlowID(flow.id))
			}
			m.closeFlow(flow.id, false)
			return
		}
	}
}

func (m *serverMux) udpRemoteToClient(flow *serverFlow) {
	buf := make([]byte, 64*1024)
	for {
		_ = flow.pc.SetReadDeadline(time.Now().Add(m.opts.IdleTimeout))
		n, raddr, err := flow.pc.ReadFrom(buf)
		if n > 0 && raddr != nil {
			flow.mu.Lock()
			flow.down += int64(n)
			flow.packetsDown++
			flow.mu.Unlock()
			payload, encErr := proto.EncodeDatagram(flow.id, raddr.String(), buf[:n])
			if encErr != nil {
				continue
			}
			if werr := m.tr.WriteControl(proto.FrameProxyDatagram, payload); werr != nil {
				m.closeFlow(flow.id, false)
				return
			}
		}
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				m.closeFlow(flow.id, true)
				return
			}
			_ = m.tr.WriteControl(proto.FrameProxyClose, proto.EncodeFlowID(flow.id))
			m.closeFlow(flow.id, false)
			return
		}
	}
}

func (m *serverMux) writeFlow(id uint32, data []byte) error {
	m.mu.Lock()
	flow := m.flows[id]
	m.mu.Unlock()
	if flow == nil || flow.conn == nil {
		return fmt.Errorf("unknown or unopened flow %d", id)
	}
	for len(data) > 0 {
		n, err := flow.conn.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		flow.mu.Lock()
		flow.up += int64(n)
		flow.mu.Unlock()
		data = data[n:]
	}
	return nil
}

func (m *serverMux) writeDatagram(id uint32, addr string, data []byte) {
	m.mu.Lock()
	flow := m.flows[id]
	m.mu.Unlock()
	if flow == nil || flow.pc == nil || flow.kind != "udp" {
		return
	}
	if m.opts.CheckUDPDestination != nil {
		if err := m.opts.CheckUDPDestination(m.ctx, addr); err != nil {
			if m.opts.Logger != nil {
				m.opts.Logger.Warn("flow denied", "id", id, "target", addr, "reason", "private_network", "device", m.opts.Device)
				m.opts.Logger.Debug("udp packet dropped", "id", id, "dst", addr, "err", err)
			}
			return
		}
	}
	raddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return
	}
	_ = flow.pc.SetWriteDeadline(time.Now().Add(15 * time.Second))
	n, _ := flow.pc.WriteTo(data, raddr)
	if n > 0 {
		flow.mu.Lock()
		flow.up += int64(n)
		flow.packetsUp++
		flow.mu.Unlock()
	}
	if m.opts.Logger != nil {
		m.opts.Logger.Debug("udp packet up", "id", id, "dst", addr, "bytes", n)
	}
}

func (m *serverMux) closeWrite(id uint32) {
	m.mu.Lock()
	flow := m.flows[id]
	m.mu.Unlock()
	if flow == nil || flow.conn == nil {
		return
	}
	if tcp, ok := flow.conn.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
	}
}

func (m *serverMux) removePending(id uint32) {
	m.mu.Lock()
	flow := m.flows[id]
	if flow != nil {
		delete(m.flows, id)
	}
	m.mu.Unlock()
	if flow != nil {
		flow.cancel()
		if m.opts.OnFlowClose != nil {
			m.opts.OnFlowClose()
		}
	}
}

func (m *serverMux) closeFlow(id uint32, notify bool) {
	m.mu.Lock()
	flow := m.flows[id]
	if flow != nil {
		delete(m.flows, id)
	}
	m.mu.Unlock()
	if flow == nil {
		return
	}
	flow.cancel()
	if flow.conn != nil {
		_ = flow.conn.Close()
	}
	if flow.pc != nil {
		_ = flow.pc.Close()
	}
	if notify {
		_ = m.tr.WriteControl(proto.FrameProxyClose, proto.EncodeFlowID(id))
	}
	m.logFlowClose(flow)
	if m.opts.OnFlowClose != nil {
		m.opts.OnFlowClose()
	}
}

func (m *serverMux) closeAll() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	flows := m.flows
	m.flows = make(map[uint32]*serverFlow)
	m.mu.Unlock()
	for _, flow := range flows {
		flow.cancel()
		if flow.conn != nil {
			_ = flow.conn.Close()
		}
		if flow.pc != nil {
			_ = flow.pc.Close()
		}
		m.logFlowClose(flow)
		if m.opts.OnFlowClose != nil {
			m.opts.OnFlowClose()
		}
	}
}

type tokenBucket struct {
	mu     sync.Mutex
	rate   int
	tokens float64
	last   time.Time
}

func newTokenBucket(rate int) *tokenBucket {
	return &tokenBucket{rate: rate, tokens: float64(rate), last: time.Now()}
}

func (b *tokenBucket) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	elapsed := now.Sub(b.last).Seconds()
	b.last = now
	b.tokens += elapsed * float64(b.rate)
	if b.tokens > float64(b.rate) {
		b.tokens = float64(b.rate)
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
