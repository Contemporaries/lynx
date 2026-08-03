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
