package server_test

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	mw "aireos.local/packages/go/middleware"
	"aireos.local/services/session-broker/internal/server"
)

type allowAll struct{}

func (allowAll) Verify(context.Context, string) (mw.Capability, error) {
	return mw.Capability{Subject: "tester", Edge: "agent-remote-session"}, nil
}

func wsURL(httpURL, path string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http") + path
}

// A capability-bearing client can open the channel and the broker echoes.
func TestWebSocketEchoGated(t *testing.T) {
	srv := httptest.NewServer(server.New(allowAll{}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, _, err := websocket.Dial(ctx, wsURL(srv.URL, "/v1/sessions/abc/channel"), &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer test"}},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.CloseNow()

	if err := c.Write(ctx, websocket.MessageText, []byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	typ, msg, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if typ != websocket.MessageText || string(msg) != "ping" {
		t.Fatalf("echo mismatch: type=%v msg=%q", typ, msg)
	}
	c.Close(websocket.StatusNormalClosure, "")
}

// The channel upgrade is gated: no bearer → 401, no upgrade.
func TestWebSocketRequiresCapability(t *testing.T) {
	srv := httptest.NewServer(server.New(allowAll{}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, resp, err := websocket.Dial(ctx, wsURL(srv.URL, "/v1/sessions/abc/channel"), nil)
	if err == nil {
		t.Fatal("expected dial to fail without a capability token")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %v", resp)
	}
}

// /healthz is served over HTTP/2 (cleartext via h2c), per ADR-0043 §Verification.
func TestHTTP2Healthz(t *testing.T) {
	h := h2c.NewHandler(server.New(allowAll{}), &http2.Server{})
	srv := httptest.NewServer(h)
	defer srv.Close()

	client := &http.Client{Transport: &http2.Transport{
		AllowHTTP: true,
		DialTLSContext: func(_ context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
			return net.Dial(network, addr)
		},
	}}
	resp, err := client.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.ProtoMajor != 2 {
		t.Fatalf("want HTTP/2 (ProtoMajor 2), got %d", resp.ProtoMajor)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}
