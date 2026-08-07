package transport

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"net"
	"sync"
	"time"

	"github.com/Contemporaries/lynx/internal/proto"
)

type StreamSession struct {
	kind        string
	conn        net.Conn
	framer      *proto.Framer
	cn          string
	fingerprint string
	remoteAddr  string
	writeMu     sync.Mutex
}

func NewStreamSession(kind string, conn net.Conn, state tls.ConnectionState) *StreamSession {
	cn := ""
	fp := ""
	if len(state.PeerCertificates) > 0 {
		cn = state.PeerCertificates[0].Subject.CommonName
		sum := sha256.Sum256(state.PeerCertificates[0].Raw)
		fp = hex.EncodeToString(sum[:])
	}
	return &StreamSession{
		kind:        kind,
		conn:        conn,
		framer:      &proto.Framer{RW: conn},
		cn:          cn,
		fingerprint: fp,
		remoteAddr:  conn.RemoteAddr().String(),
	}
}

func (s *StreamSession) Kind() string                       { return s.kind }
func (s *StreamSession) PeerCommonName() string             { return s.cn }
func (s *StreamSession) PeerCertificateSHA256() string      { return s.fingerprint }
func (s *StreamSession) RemoteAddr() string                 { return s.remoteAddr }
func (s *StreamSession) Close() error                       { return s.conn.Close() }
func (s *StreamSession) SetReadDeadline(t time.Time) error  { return s.conn.SetReadDeadline(t) }
func (s *StreamSession) SetWriteDeadline(t time.Time) error { return s.conn.SetWriteDeadline(t) }
func (s *StreamSession) ReadControl() (byte, []byte, error) { return s.framer.ReadFrame() }
func (s *StreamSession) WriteControl(typ byte, payload []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	err := s.framer.WriteFrame(typ, payload)
	_ = s.conn.SetWriteDeadline(time.Time{})
	return err
}

func CertificateSHA256(cert *x509.Certificate) string {
	if cert == nil {
		return ""
	}
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}
