package proxymux

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/Contemporaries/lynx/internal/proto"
	"github.com/Contemporaries/lynx/internal/transport"
)

func TestProxyMuxRoundTrip(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				_, _ = io.Copy(conn, conn)
			}(c)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dial := func(context.Context) (transport.Session, error) {
		client, server := net.Pipe()
		clientSession := transport.NewStreamSession("test-client", client, tls.ConnectionState{})
		serverSession := transport.NewStreamSession("test-server", server, tls.ConnectionState{})
		go func() {
			typ, payload, err := serverSession.ReadControl()
			if err != nil || typ != proto.FrameHello {
				_ = serverSession.Close()
				return
			}
			hello, err := proto.ReadJSON[proto.Hello](payload)
			if err != nil || hello.Mode != proto.ModeProxyMux {
				_ = serverSession.Close()
				return
			}
			_ = Serve(ctx, serverSession, ServerOptions{
				MaxFlows: 32,
				Dial: func(ctx context.Context, address string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "tcp", address)
				},
			})
		}()
		return clientSession, nil
	}
	pool, err := NewPool(ctx, 1, dial)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			openCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			conn, err := pool.Open(openCtx, echo.Addr().String())
			if err != nil {
				t.Errorf("open: %v", err)
				return
			}
			defer conn.Close()
			payload, _ := json.Marshal(map[string]int{"flow": i})
			if _, err := conn.Write(payload); err != nil {
				t.Errorf("write: %v", err)
				return
			}
			got := make([]byte, len(payload))
			if _, err := io.ReadFull(conn, got); err != nil {
				t.Errorf("read: %v", err)
				return
			}
			if string(got) != string(payload) {
				t.Errorf("got %q want %q", got, payload)
			}
		}(i)
	}
	wg.Wait()
}
