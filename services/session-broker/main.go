// Command session-broker brokers live remote sessions (ADR-0043). It serves
// over stdlib http.Server with HTTP/2: TLS gives HTTP/2 automatically in
// production; for cleartext dev/CI it is enabled via h2c so the broker
// demonstrably speaks HTTP/2 (ADR-0043 §Verification item 5). The WebSocket
// channel uses github.com/coder/websocket (the maintained successor to the
// nhooyr.io/websocket named in ADR-0043).
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"aireos.local/packages/go/auth"
	mw "aireos.local/packages/go/middleware"
	"aireos.local/services/session-broker/internal/server"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("session-broker: %v", err)
	}
}

func run() error {
	v, err := capabilityVerifier()
	if err != nil {
		return err
	}
	// h2c lets the cleartext dev/CI listener speak HTTP/2; under TLS, http.Server
	// negotiates HTTP/2 on its own and h2c is a transparent pass-through.
	h := h2c.NewHandler(server.New(v), &http2.Server{})
	srv := &http.Server{
		Addr:              firstNonEmpty(os.Getenv("BROKER_ADDR"), ":8082"),
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("session-broker listening on %s", srv.Addr)
	return srv.ListenAndServe()
}

// capabilityVerifier mirrors the other services: real PASETO verifier (audience
// "session-broker") when AIREOS_PASETO_KEY is set, else dev opt-in, else fail loud.
func capabilityVerifier() (mw.Verifier, error) {
	if keyHex := os.Getenv("AIREOS_PASETO_KEY"); keyHex != "" {
		key, err := auth.NewKeyFromHex(keyHex)
		if err != nil {
			return nil, err
		}
		issuer := os.Getenv("AIREOS_CLUSTER_ID")
		return auth.AsMiddlewareVerifier(auth.NewVerifier(key, issuer, "session-broker")), nil
	}
	if os.Getenv("BROKER_DEV_INSECURE_AUTH") == "1" {
		log.Print("WARNING: BROKER_DEV_INSECURE_AUTH=1 — using dev allow-all capability verifier; NOT for production")
		return devAllowAll{}, nil
	}
	return nil, errors.New("no capability verifier configured: set AIREOS_PASETO_KEY or BROKER_DEV_INSECURE_AUTH=1 for local dev")
}

type devAllowAll struct{}

func (devAllowAll) Verify(_ context.Context, _ string) (mw.Capability, error) {
	return mw.Capability{Subject: "dev", Edge: "agent-remote-session"}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
