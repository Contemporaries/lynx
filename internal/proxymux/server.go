package proxymux

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/Contemporaries/lynx/internal/proto"
	"github.com/Contemporaries/lynx/internal/transport"
)

type ServerOptions struct {
	MaxFlows           int
	MaxNewFlowsPerSec  int
	Dial               func(context.Context, string) (net.Conn, error)
	OnFlowOpen         func() bool // optional gate; return false to reject
	OnFlowClose        func()
}

type serverFlow struct {
	id     uint32
	conn   net.Conn
	cancel context.CancelFunc
}

type serverMux struct {
	ctx      context.Context
	tr       transport.Session
	opts     ServerOptions
	mu       sync.Mutex
	flows    map[uint32]*serverFlow
	closed   bool
	limiter  *tokenBucket
}

func Serve(ctx context.Context, tr transport.Session, opts ServerOptions) error {
	if opts.MaxFlows <= 0 {
		opts.MaxFlows = 256
	}
	if opts.Dial == nil {
		return fmt.Errorf("proxy dial function is required")
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
	result := proto.ProxyResult{FlowID: req.FlowID}
	if req.FlowID == 0 || req.Network != "tcp" || req.Address == "" {
		result.Error = "invalid proxy request"
		m.writeResult(result)
		return
	}
	if m.limiter != nil && !m.limiter.allow() {
		result.Error = "proxy flow rate limited"
		m.writeResult(result)
		return
	}
	if m.opts.OnFlowOpen != nil && !m.opts.OnFlowOpen() {
		result.Error = "proxy flow limit reached"
		m.writeResult(result)
		return
	}
	m.mu.Lock()
	if m.closed || len(m.flows) >= m.opts.MaxFlows || m.flows[req.FlowID] != nil {
		m.mu.Unlock()
		if m.opts.OnFlowClose != nil {
			m.opts.OnFlowClose()
		}
		result.Error = "proxy flow limit reached or duplicate flow id"
		m.writeResult(result)
		return
	}
	flowCtx, cancel := context.WithCancel(m.ctx)
	flow := &serverFlow{id: req.FlowID, cancel: cancel}
	m.flows[req.FlowID] = flow
	m.mu.Unlock()

	go func() {
		conn, err := m.opts.Dial(flowCtx, req.Address)
		if err != nil {
			m.removePending(req.FlowID)
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
		m.remoteToClient(flow)
	}()
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
		data = data[n:]
	}
	return nil
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
	if notify {
		_ = m.tr.WriteControl(proto.FrameProxyClose, proto.EncodeFlowID(id))
	}
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
