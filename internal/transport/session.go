package transport

type Session interface {
	Kind() string
	ReadControl() (byte, []byte, error)
	WriteControl(typ byte, payload []byte) error
	PeerCommonName() string
	PeerCertificateSHA256() string
	RemoteAddr() string
	Close() error
}
