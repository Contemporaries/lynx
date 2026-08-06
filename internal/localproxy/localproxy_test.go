package localproxy

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func echoOpen(context.Context, string) (net.Conn, error) {
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		_, _ = io.Copy(server, server)
	}()
	return client, nil
}

func TestSOCKS5Connect(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handleSOCKS(ctx, server, Config{Open: echoOpen})
	_ = client.SetDeadline(time.Now().Add(2 * time.Second))
	_, _ = client.Write([]byte{0x05, 0x01, 0x00})
	resp := make([]byte, 2)
	if _, err := io.ReadFull(client, resp); err != nil || resp[1] != 0x00 {
		t.Fatalf("negotiate: %v %v", resp, err)
	}
	host := "example.com"
	req := append([]byte{0x05, 0x01, 0x00, 0x03, byte(len(host))}, []byte(host)...)
	req = append(req, 0x01, 0xbb)
	_, _ = client.Write(req)
	reply := make([]byte, 10)
	if _, err := io.ReadFull(client, reply); err != nil || reply[1] != 0x00 {
		t.Fatalf("connect: %v %v", reply, err)
	}
	_, _ = client.Write([]byte("hello"))
	got := make([]byte, 5)
	if _, err := io.ReadFull(client, got); err != nil || string(got) != "hello" {
		t.Fatalf("echo: %q %v", got, err)
	}
}

type memUDPRelay struct {
	in  chan struct {
		addr string
		data []byte
	}
	closed chan struct{}
}

func (m *memUDPRelay) WriteTo(p []byte, addr string) (int, error) {
	select {
	case <-m.closed:
		return 0, io.ErrClosedPipe
	case m.in <- struct {
		addr string
		data []byte
	}{addr: addr, data: append([]byte(nil), p...)}:
		return len(p), nil
	}
}

func (m *memUDPRelay) ReadFrom(p []byte) (int, string, error) {
	select {
	case <-m.closed:
		return 0, "", io.EOF
	case pkt := <-m.in:
		// Echo back as if from target.
		n := copy(p, pkt.data)
		return n, pkt.addr, nil
	}
}

func (m *memUDPRelay) Close() error {
	select {
	case <-m.closed:
	default:
		close(m.closed)
	}
	return nil
}

func TestSOCKS5UDPAssociate(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	relay := &memUDPRelay{
		in: make(chan struct {
			addr string
			data []byte
		}, 8),
		closed: make(chan struct{}),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		handleSOCKS(ctx, c, Config{
			Open: echoOpen,
			AssociateUDP: func(context.Context) (UDPRelay, error) {
				return relay, nil
			},
		})
	}()

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(3 * time.Second))

	_, _ = client.Write([]byte{0x05, 0x01, 0x00})
	resp := make([]byte, 2)
	if _, err := io.ReadFull(client, resp); err != nil || resp[1] != 0x00 {
		t.Fatalf("negotiate: %v %v", resp, err)
	}
	// ASSOCIATE with 0.0.0.0:0
	_, _ = client.Write([]byte{0x05, 0x03, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
	reply := make([]byte, 10)
	if _, err := io.ReadFull(client, reply); err != nil || reply[1] != 0x00 {
		t.Fatalf("associate: %v %v", reply, err)
	}
	port := int(reply[8])<<8 | int(reply[9])
	if port == 0 {
		t.Fatal("expected non-zero BND port")
	}
	udpAddr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}
	uc, err := net.DialUDP("udp", nil, udpAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer uc.Close()

	// SOCKS UDP header to 8.8.8.8:53 + payload
	pkt := []byte{0, 0, 0, 0x01, 8, 8, 8, 8, 0, 53, 'p', 'i', 'n', 'g'}
	if _, err := uc.Write(pkt); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, 64)
	_ = uc.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, err := uc.Read(got)
	if err != nil {
		t.Fatal(err)
	}
	dst, payload, err := parseSOCKSUDP(got[:n])
	if err != nil {
		t.Fatal(err)
	}
	if dst != "8.8.8.8:53" || string(payload) != "ping" {
		t.Fatalf("dst=%q payload=%q", dst, payload)
	}
}

func TestParseEncodeSOCKSUDP(t *testing.T) {
	raw, err := encodeSOCKSUDP("1.2.3.4:53", []byte("ab"))
	if err != nil {
		t.Fatal(err)
	}
	dst, payload, err := parseSOCKSUDP(raw)
	if err != nil {
		t.Fatal(err)
	}
	if dst != "1.2.3.4:53" || string(payload) != "ab" {
		t.Fatalf("dst=%q payload=%q", dst, payload)
	}
}

func TestHTTPConnectWithAuth(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go handleHTTP(ctx, server, Config{Open: echoOpen, Username: "u", Password: "p"})
	_ = client.SetDeadline(time.Now().Add(2 * time.Second))
	auth := base64.StdEncoding.EncodeToString([]byte("u:p"))
	_, _ = io.WriteString(client, "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\nProxy-Authorization: Basic "+auth+"\r\n\r\n")
	br := bufio.NewReader(client)
	status, err := br.ReadString('\n')
	if err != nil || !strings.Contains(status, "200") {
		t.Fatalf("status=%q err=%v", status, err)
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			break
		}
	}
	_, _ = client.Write([]byte("world"))
	got := make([]byte, 5)
	if _, err := io.ReadFull(br, got); err != nil || string(got) != "world" {
		t.Fatalf("echo: %q %v", got, err)
	}
}
