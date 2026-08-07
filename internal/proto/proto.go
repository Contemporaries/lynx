package proto

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

const (
	Version   = 1
	ALPN      = "lynx/1"
	InnerALPN = "lynx-inner/1"
)

const (
	FrameHello       byte = 0x01
	FrameConfig      byte = 0x02
	FramePing        byte = 0x20
	FramePong        byte = 0x21
	FrameProxyOpen     byte = 0x30
	FrameProxyResult   byte = 0x31
	FrameProxyData     byte = 0x32
	FrameProxyEOF      byte = 0x33
	FrameProxyClose    byte = 0x34
	FrameProxyDatagram byte = 0x35
	FrameError         byte = 0x7f
)

const MaxFrame = 1 << 20

const ModeProxyMux = "proxy_mux"

type Hello struct {
	Version int    `json:"version"`
	Mode    string `json:"mode,omitempty"`
}

type ProxyConfig struct {
	MaxFlows int `json:"max_flows"`
}

type ProxyOpen struct {
	FlowID  uint32 `json:"flow_id"`
	Network string `json:"network"`
	Address string `json:"address"`
}

type ProxyResult struct {
	FlowID uint32 `json:"flow_id"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

type Framer struct {
	RW io.ReadWriter
}

func (f *Framer) WriteFrame(typ byte, payload []byte) error {
	if len(payload) > MaxFrame {
		return fmt.Errorf("frame too large: %d", len(payload))
	}
	h := make([]byte, 5)
	h[0] = typ
	binary.BigEndian.PutUint32(h[1:], uint32(len(payload)))
	if err := writeFull(f.RW, h); err != nil {
		return err
	}
	return writeFull(f.RW, payload)
}

func (f *Framer) ReadFrame() (byte, []byte, error) {
	h := make([]byte, 5)
	if _, err := io.ReadFull(f.RW, h); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(h[1:])
	if n > MaxFrame {
		return 0, nil, fmt.Errorf("frame too large: %d", n)
	}
	p := make([]byte, int(n))
	if _, err := io.ReadFull(f.RW, p); err != nil {
		return 0, nil, err
	}
	return h[0], p, nil
}

func WriteJSON(f *Framer, typ byte, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return f.WriteFrame(typ, b)
}

func ReadJSON[T any](payload []byte) (T, error) {
	var v T
	err := json.Unmarshal(payload, &v)
	return v, err
}

func EncodeFlowPayload(flowID uint32, data []byte) []byte {
	out := make([]byte, 4+len(data))
	binary.BigEndian.PutUint32(out[:4], flowID)
	copy(out[4:], data)
	return out
}

func DecodeFlowPayload(payload []byte) (uint32, []byte, error) {
	if len(payload) < 4 {
		return 0, nil, fmt.Errorf("flow payload is too short")
	}
	return binary.BigEndian.Uint32(payload[:4]), payload[4:], nil
}

func EncodeFlowID(flowID uint32) []byte {
	out := make([]byte, 4)
	binary.BigEndian.PutUint32(out, flowID)
	return out
}

func DecodeFlowID(payload []byte) (uint32, error) {
	if len(payload) != 4 {
		return 0, fmt.Errorf("invalid flow id payload length %d", len(payload))
	}
	return binary.BigEndian.Uint32(payload), nil
}

// EncodeDatagram encodes flow_id(4) + addr_len(2 BE) + addr + data.
func EncodeDatagram(flowID uint32, addr string, data []byte) ([]byte, error) {
	if len(addr) > 65535 {
		return nil, fmt.Errorf("datagram address too long")
	}
	total := 4 + 2 + len(addr) + len(data)
	if total > MaxFrame {
		return nil, fmt.Errorf("datagram frame too large: %d", total)
	}
	out := make([]byte, total)
	binary.BigEndian.PutUint32(out[:4], flowID)
	binary.BigEndian.PutUint16(out[4:6], uint16(len(addr)))
	copy(out[6:6+len(addr)], addr)
	copy(out[6+len(addr):], data)
	return out, nil
}

func DecodeDatagram(payload []byte) (flowID uint32, addr string, data []byte, err error) {
	if len(payload) < 6 {
		return 0, "", nil, fmt.Errorf("datagram payload is too short")
	}
	flowID = binary.BigEndian.Uint32(payload[:4])
	addrLen := int(binary.BigEndian.Uint16(payload[4:6]))
	if len(payload) < 6+addrLen {
		return 0, "", nil, fmt.Errorf("datagram address truncated")
	}
	addr = string(payload[6 : 6+addrLen])
	data = payload[6+addrLen:]
	return flowID, addr, data, nil
}

func writeFull(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, err := w.Write(b)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}
