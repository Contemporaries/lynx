package proxymux

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Contemporaries/lynx/internal/proto"
	"github.com/Contemporaries/lynx/internal/transport"
)

type DialFunc func(context.Context) (transport.Session, error)

type PoolOptions struct {
	Channels          int
	PingInterval      time.Duration
	PongTimeoutMisses int
}

type Pool struct {
	ctx      context.Context
	cancel   context.CancelFunc
	dial     DialFunc
	opts     PoolOptions
	mu       sync.Mutex
	sessions []*clientSession
	closed   bool
}

func NewPool(parent context.Context, channels int, dial DialFunc) (*Pool, error) {
	return NewPoolWithOptions(parent, PoolOptions{Channels: channels}, dial)
}

func NewPoolWithOptions(parent context.Context, opts PoolOptions, dial DialFunc) (*Pool, error) {
	if opts.Channels <= 0 {
		opts.Channels = 1
	}
	if opts.PingInterval <= 0 {
		opts.PingInterval = 20 * time.Second
	}
	if opts.PongTimeoutMisses <= 0 {
		opts.PongTimeoutMisses = 3
	}
	ctx, cancel := context.WithCancel(parent)
	p := &Pool{ctx: ctx, cancel: cancel, dial: dial, opts: opts}
	var firstErr error
	for i := 0; i < opts.Channels; i++ {
		s, err := p.connect(ctx)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		p.sessions = append(p.sessions, s)
	}
	if len(p.sessions) == 0 {
		cancel()
		return nil, fmt.Errorf("connect proxy transport: %w", firstErr)
	}
	go p.maintain()
	return p, nil
}

func (p *Pool) connect(ctx context.Context) (*clientSession, error) {
	tr, err := p.dial(ctx)
	if err != nil {
		return nil, err
	}
	hello, _ := json.Marshal(proto.Hello{Version: proto.Version, Mode: proto.ModeProxyMux})
	if err := tr.WriteControl(proto.FrameHello, hello); err != nil {
		_ = tr.Close()
		return nil, err
	}
	typ, payload, err := tr.ReadControl()
	if err != nil {
		_ = tr.Close()
		return nil, err
	}
	if typ == proto.FrameError {
		_ = tr.Close()
		return nil, fmt.Errorf("server rejected proxy session: %s", string(payload))
	}
	if typ != proto.FrameConfig {
		_ = tr.Close()
		return nil, fmt.Errorf("expected proxy config, got frame 0x%x", typ)
	}
	cfg, err := proto.ReadJSON[proto.ProxyConfig](payload)
	if err != nil {
		_ = tr.Close()
		return nil, fmt.Errorf("parse proxy config: %w", err)
	}
	if cfg.MaxFlows <= 0 {
		cfg.MaxFlows = 256
	}
	s := &clientSession{
		tr:                tr,
		maxFlows:          cfg.MaxFlows,
		flows:             make(map[uint32]*Conn),
		udps:              make(map[uint32]*UDPAssoc),
		dead:              make(chan struct{}),
		pingInterval:      p.opts.PingInterval,
		pongTimeoutMisses: p.opts.PongTimeoutMisses,
		lastPong:          time.Now(),
	}
	go s.readLoop()
	go s.pingLoop()
	return s, nil
}

func (p *Pool) maintain() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
		}
		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return
		}
		alive := p.sessions[:0]
		for _, s := range p.sessions {
			if !s.isDead() {
				alive = append(alive, s)
			}
		}
		p.sessions = alive
		missing := p.opts.Channels - len(p.sessions)
		p.mu.Unlock()
		for i := 0; i < missing; i++ {
			ctx, cancel := context.WithTimeout(p.ctx, 20*time.Second)
			s, err := p.connect(ctx)
			cancel()
			if err != nil {
				break
			}
			p.mu.Lock()
			if p.closed {
				p.mu.Unlock()
				_ = s.Close()
				return
			}
			p.sessions = append(p.sessions, s)
			p.mu.Unlock()
		}
	}
}

func (p *Pool) pickSession() *clientSession {
	p.mu.Lock()
	defer p.mu.Unlock()
	var chosen *clientSession
	for _, s := range p.sessions {
		if s.isDead() || s.active.Load() >= int64(s.maxFlows) {
			continue
		}
		if chosen == nil || s.active.Load() < chosen.active.Load() {
			chosen = s
		}
	}
	return chosen
}

func (p *Pool) Open(ctx context.Context, address string) (net.Conn, error) {
	chosen := p.pickSession()
	if chosen == nil {
		return nil, errors.New("no healthy proxy transport is available")
	}
	return chosen.open(ctx, address)
}

// AssociateUDP opens a SOCKS5-style UDP association over the mux.
func (p *Pool) AssociateUDP(ctx context.Context) (*UDPAssoc, error) {
	chosen := p.pickSession()
	if chosen == nil {
		return nil, errors.New("no healthy proxy transport is available")
	}
	return chosen.associateUDP(ctx)
}

func (p *Pool) HealthyChannels() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, s := range p.sessions {
		if !s.isDead() {
			n++
		}
	}
	return n
}

// ReconnectAll closes all sessions without shutting down the pool so maintain() can refill them.
func (p *Pool) ReconnectAll() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	sessions := append([]*clientSession(nil), p.sessions...)
	p.sessions = nil
	p.mu.Unlock()
	for _, s := range sessions {
		_ = s.Close()
	}
}

func (p *Pool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	sessions := append([]*clientSession(nil), p.sessions...)
	p.mu.Unlock()
	p.cancel()
	for _, s := range sessions {
		_ = s.Close()
	}
	return nil
}

type clientSession struct {
	tr                transport.Session
	maxFlows          int
	nextID            atomic.Uint32
	active            atomic.Int64
	mu                sync.Mutex
	flows             map[uint32]*Conn
	udps              map[uint32]*UDPAssoc
	dead              chan struct{}
	deadOnce          sync.Once
	pingInterval      time.Duration
	pongTimeoutMisses int
	pongMu            sync.Mutex
	lastPong          time.Time
	missedPongs       int
}

func (s *clientSession) allocID() (uint32, error) {
	id := s.nextID.Add(1)
	if id == 0 {
		id = s.nextID.Add(1)
	}
	return id, nil
}

func (s *clientSession) open(ctx context.Context, address string) (*Conn, error) {
	if s.isDead() {
		return nil, io.ErrClosedPipe
	}
	id, _ := s.allocID()
	c := &Conn{
		session:    s,
		id:         id,
		opened:     make(chan proto.ProxyResult, 1),
		recv:       make(chan []byte, 64),
		done:       make(chan struct{}),
		remoteDone: make(chan struct{}),
	}
	s.mu.Lock()
	if len(s.flows)+len(s.udps) >= s.maxFlows {
		s.mu.Unlock()
		return nil, errors.New("proxy session flow limit reached")
	}
	s.flows[id] = c
	s.active.Add(1)
	s.mu.Unlock()
	openPayload, _ := json.Marshal(proto.ProxyOpen{FlowID: id, Network: "tcp", Address: address})
	if err := s.tr.WriteControl(proto.FrameProxyOpen, openPayload); err != nil {
		s.removeTCP(id, err)
		return nil, err
	}
	select {
	case result := <-c.opened:
		if !result.OK {
			s.removeTCP(id, errors.New(result.Error))
			return nil, errors.New(result.Error)
		}
		return c, nil
	case <-ctx.Done():
		_ = s.tr.WriteControl(proto.FrameProxyClose, proto.EncodeFlowID(id))
		s.removeTCP(id, ctx.Err())
		return nil, ctx.Err()
	case <-s.dead:
		return nil, io.ErrClosedPipe
	}
}

func (s *clientSession) associateUDP(ctx context.Context) (*UDPAssoc, error) {
	if s.isDead() {
		return nil, io.ErrClosedPipe
	}
	id, _ := s.allocID()
	a := &UDPAssoc{
		session: s,
		id:      id,
		opened:  make(chan proto.ProxyResult, 1),
		recv:    make(chan udpPacket, 64),
		done:    make(chan struct{}),
	}
	s.mu.Lock()
	if len(s.flows)+len(s.udps) >= s.maxFlows {
		s.mu.Unlock()
		return nil, errors.New("proxy session flow limit reached")
	}
	s.udps[id] = a
	s.active.Add(1)
	s.mu.Unlock()
	openPayload, _ := json.Marshal(proto.ProxyOpen{FlowID: id, Network: "udp"})
	if err := s.tr.WriteControl(proto.FrameProxyOpen, openPayload); err != nil {
		s.removeUDP(id, err)
		return nil, err
	}
	select {
	case result := <-a.opened:
		if !result.OK {
			err := errors.New(result.Error)
			if result.Error == "" {
				err = errors.New("udp associate rejected")
			}
			s.removeUDP(id, err)
			return nil, err
		}
		return a, nil
	case <-ctx.Done():
		_ = s.tr.WriteControl(proto.FrameProxyClose, proto.EncodeFlowID(id))
		s.removeUDP(id, ctx.Err())
		return nil, ctx.Err()
	case <-s.dead:
		return nil, io.ErrClosedPipe
	}
}

func (s *clientSession) readLoop() {
	var terminal error
	defer func() { s.failAll(terminal) }()
	for {
		typ, payload, err := s.tr.ReadControl()
		if err != nil {
			terminal = err
			return
		}
		switch typ {
		case proto.FrameProxyResult:
			result, err := proto.ReadJSON[proto.ProxyResult](payload)
			if err != nil {
				terminal = err
				return
			}
			if c := s.getTCP(result.FlowID); c != nil {
				select {
				case c.opened <- result:
				default:
				}
			} else if a := s.getUDP(result.FlowID); a != nil {
				select {
				case a.opened <- result:
				default:
				}
			}
		case proto.FrameProxyData:
			id, data, err := proto.DecodeFlowPayload(payload)
			if err != nil {
				terminal = err
				return
			}
			if c := s.getTCP(id); c != nil {
				copyData := append([]byte(nil), data...)
				select {
				case c.recv <- copyData:
				case <-c.done:
				default:
					s.removeTCP(id, errors.New("proxy flow receive buffer full"))
					_ = s.tr.WriteControl(proto.FrameProxyClose, proto.EncodeFlowID(id))
				}
			}
		case proto.FrameProxyDatagram:
			id, addr, data, err := proto.DecodeDatagram(payload)
			if err != nil {
				terminal = err
				return
			}
			if a := s.getUDP(id); a != nil {
				pkt := udpPacket{addr: addr, data: append([]byte(nil), data...)}
				select {
				case a.recv <- pkt:
				case <-a.done:
				default:
					s.removeUDP(id, errors.New("udp association receive buffer full"))
					_ = s.tr.WriteControl(proto.FrameProxyClose, proto.EncodeFlowID(id))
				}
			}
		case proto.FrameProxyEOF:
			id, err := proto.DecodeFlowID(payload)
			if err != nil {
				terminal = err
				return
			}
			if c := s.getTCP(id); c != nil {
				c.remoteEOF()
			}
		case proto.FrameProxyClose:
			id, err := proto.DecodeFlowID(payload)
			if err != nil {
				terminal = err
				return
			}
			if s.getTCP(id) != nil {
				s.removeTCP(id, io.EOF)
			} else {
				s.removeUDP(id, io.EOF)
			}
		case proto.FramePing:
			if err := s.tr.WriteControl(proto.FramePong, payload); err != nil {
				terminal = err
				return
			}
		case proto.FramePong:
			s.notePong()
		case proto.FrameError:
			terminal = fmt.Errorf("proxy server error: %s", string(payload))
			return
		default:
			terminal = fmt.Errorf("unexpected proxy frame 0x%x", typ)
			return
		}
	}
}

func (s *clientSession) notePong() {
	s.pongMu.Lock()
	s.lastPong = time.Now()
	s.missedPongs = 0
	s.pongMu.Unlock()
}

func (s *clientSession) pingLoop() {
	ticker := time.NewTicker(s.pingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.dead:
			return
		case now := <-ticker.C:
			s.pongMu.Lock()
			missed := s.missedPongs
			last := s.lastPong
			s.pongMu.Unlock()
			if missed >= s.pongTimeoutMisses || now.Sub(last) > s.pingInterval*time.Duration(s.pongTimeoutMisses) {
				s.failAll(errors.New("proxy session pong timeout"))
				return
			}
			if err := s.tr.WriteControl(proto.FramePing, []byte(now.UTC().Format(time.RFC3339Nano))); err != nil {
				s.failAll(err)
				return
			}
			s.pongMu.Lock()
			s.missedPongs++
			s.pongMu.Unlock()
		}
	}
}

func (s *clientSession) getTCP(id uint32) *Conn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flows[id]
}

func (s *clientSession) getUDP(id uint32) *UDPAssoc {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.udps[id]
}

func (s *clientSession) removeTCP(id uint32, err error) {
	s.mu.Lock()
	c := s.flows[id]
	if c != nil {
		delete(s.flows, id)
		s.active.Add(-1)
	}
	s.mu.Unlock()
	if c != nil {
		c.finish(err)
	}
}

func (s *clientSession) removeUDP(id uint32, err error) {
	s.mu.Lock()
	a := s.udps[id]
	if a != nil {
		delete(s.udps, id)
		s.active.Add(-1)
	}
	s.mu.Unlock()
	if a != nil {
		a.finish(err)
	}
}

func (s *clientSession) failAll(err error) {
	s.deadOnce.Do(func() {
		close(s.dead)
		_ = s.tr.Close()
		s.mu.Lock()
		flows := s.flows
		udps := s.udps
		s.flows = make(map[uint32]*Conn)
		s.udps = make(map[uint32]*UDPAssoc)
		s.active.Store(0)
		s.mu.Unlock()
		for _, c := range flows {
			c.finish(err)
		}
		for _, a := range udps {
			a.finish(err)
		}
	})
}

func (s *clientSession) isDead() bool {
	select {
	case <-s.dead:
		return true
	default:
		return false
	}
}

func (s *clientSession) Close() error {
	s.failAll(io.ErrClosedPipe)
	return nil
}

type Conn struct {
	session    *clientSession
	id         uint32
	opened     chan proto.ProxyResult
	recv       chan []byte
	done       chan struct{}
	remoteDone chan struct{}

	readMu  sync.Mutex
	readBuf []byte

	writeMu sync.Mutex
	closed  bool
	eofSent bool

	stateMu    sync.Mutex
	err        error
	remoteOnce sync.Once
	doneOnce   sync.Once
}

func (c *Conn) Read(p []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	if len(c.readBuf) > 0 {
		n := copy(p, c.readBuf)
		c.readBuf = c.readBuf[n:]
		return n, nil
	}
	for {
		select {
		case data := <-c.recv:
			if len(data) == 0 {
				continue
			}
			n := copy(p, data)
			if n < len(data) {
				c.readBuf = data[n:]
			}
			return n, nil
		case <-c.remoteDone:
			return 0, io.EOF
		case <-c.done:
			return 0, c.terminalError()
		}
	}
}

func (c *Conn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closed || c.eofSent {
		return 0, io.ErrClosedPipe
	}
	const chunkSize = 64 * 1024
	written := 0
	for len(p) > 0 {
		n := len(p)
		if n > chunkSize {
			n = chunkSize
		}
		if err := c.session.tr.WriteControl(proto.FrameProxyData, proto.EncodeFlowPayload(c.id, p[:n])); err != nil {
			c.session.removeTCP(c.id, err)
			return written, err
		}
		written += n
		p = p[n:]
	}
	return written, nil
}

func (c *Conn) CloseWrite() error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if c.closed || c.eofSent {
		return nil
	}
	c.eofSent = true
	return c.session.tr.WriteControl(proto.FrameProxyEOF, proto.EncodeFlowID(c.id))
}

func (c *Conn) Close() error {
	c.writeMu.Lock()
	already := c.closed
	c.closed = true
	c.writeMu.Unlock()
	if already {
		return nil
	}
	_ = c.session.tr.WriteControl(proto.FrameProxyClose, proto.EncodeFlowID(c.id))
	c.session.removeTCP(c.id, io.EOF)
	return nil
}

func (c *Conn) remoteEOF() {
	c.remoteOnce.Do(func() { close(c.remoteDone) })
}

func (c *Conn) finish(err error) {
	c.doneOnce.Do(func() {
		if err == nil {
			err = io.EOF
		}
		c.stateMu.Lock()
		c.err = err
		c.stateMu.Unlock()
		close(c.done)
	})
}

func (c *Conn) terminalError() error {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()
	if c.err == nil {
		return io.EOF
	}
	return c.err
}

func (c *Conn) LocalAddr() net.Addr              { return proxyAddr("lynx-local") }
func (c *Conn) RemoteAddr() net.Addr             { return proxyAddr("lynx-remote") }
func (c *Conn) SetDeadline(time.Time) error      { return nil }
func (c *Conn) SetReadDeadline(time.Time) error  { return nil }
func (c *Conn) SetWriteDeadline(time.Time) error { return nil }

type udpPacket struct {
	addr string
	data []byte
}

// UDPAssoc is a multiplexed UDP association (SOCKS5 UDP ASSOCIATE).
type UDPAssoc struct {
	session *clientSession
	id      uint32
	opened  chan proto.ProxyResult
	recv    chan udpPacket
	done    chan struct{}

	writeMu sync.Mutex
	closed  bool

	stateMu  sync.Mutex
	err      error
	doneOnce sync.Once
}

func (a *UDPAssoc) WriteTo(p []byte, addr string) (int, error) {
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	if a.closed {
		return 0, io.ErrClosedPipe
	}
	payload, err := proto.EncodeDatagram(a.id, addr, p)
	if err != nil {
		return 0, err
	}
	if err := a.session.tr.WriteControl(proto.FrameProxyDatagram, payload); err != nil {
		a.session.removeUDP(a.id, err)
		return 0, err
	}
	return len(p), nil
}

func (a *UDPAssoc) ReadFrom(p []byte) (n int, addr string, err error) {
	select {
	case pkt := <-a.recv:
		n = copy(p, pkt.data)
		return n, pkt.addr, nil
	case <-a.done:
		return 0, "", a.terminalError()
	}
}

func (a *UDPAssoc) Close() error {
	a.writeMu.Lock()
	already := a.closed
	a.closed = true
	a.writeMu.Unlock()
	if already {
		return nil
	}
	_ = a.session.tr.WriteControl(proto.FrameProxyClose, proto.EncodeFlowID(a.id))
	a.session.removeUDP(a.id, io.EOF)
	return nil
}

func (a *UDPAssoc) finish(err error) {
	a.doneOnce.Do(func() {
		if err == nil {
			err = io.EOF
		}
		a.stateMu.Lock()
		a.err = err
		a.stateMu.Unlock()
		close(a.done)
	})
}

func (a *UDPAssoc) terminalError() error {
	a.stateMu.Lock()
	defer a.stateMu.Unlock()
	if a.err == nil {
		return io.EOF
	}
	return a.err
}

type proxyAddr string

func (a proxyAddr) Network() string { return "lynx" }
func (a proxyAddr) String() string  { return string(a) }
