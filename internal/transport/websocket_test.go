package transport

import (
	"bytes"
	"io"
	"net"
	"testing"
)

func TestWSConnBidirectional(t *testing.T) {
	clientRaw, serverRaw := net.Pipe()
	client := newWSConn(clientRaw, true)
	server := newWSConn(serverRaw, false)
	defer client.Close()
	defer server.Close()

	payload := bytes.Repeat([]byte("lynx"), 20000)
	errCh := make(chan error, 1)
	go func() {
		_, err := client.Write(payload)
		errCh <- err
	}()
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(server, got); err != nil {
		t.Fatal(err)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("client to server mismatch")
	}

	go func() {
		_, err := server.Write(payload)
		errCh <- err
	}()
	got = make([]byte, len(payload))
	if _, err := io.ReadFull(client, got); err != nil {
		t.Fatal(err)
	}
	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatal("server to client mismatch")
	}
}
