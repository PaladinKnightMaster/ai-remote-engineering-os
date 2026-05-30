// Command session-broker brokers live remote sessions (ADR-0043) and is the
// first transactional-outbox producer (ADR-0045). It serves over stdlib
// http.Server with HTTP/2: TLS gives HTTP/2 automatically in production; for
// cleartext dev/CI it is enabled via h2c so the broker demonstrably speaks
// HTTP/2 (ADR-0043 §Verification item 5). The WebSocket channel uses
// github.com/coder/websocket (the maintained successor to the nhooyr.io/websocket
// named in ADR-0043).
//
// Subcommands (uniform stateful-service surface, ADR-0044/0045):
//
//	session-broker serve                    # default; serve HTTP + start the in-process outbox relay
//	session-broker migrate up|version|force <v>
//	session-broker outbox relay             # run the outbox relay in the foreground (operator control)
package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"aireos.local/packages/go/auth"
	mw "aireos.local/packages/go/middleware"
	"aireos.local/packages/go/migrate"
	"aireos.local/packages/go/outbox"
	"aireos.local/services/session-broker/internal/server"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("session-broker: %v", err)
	}
}

func run(args []string) error {
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	}
	dsn := firstNonEmpty(os.Getenv("BROKER_DATABASE_URL"), os.Getenv("DATABASE_URL"))
	if dsn == "" {
		return errors.New("BROKER_DATABASE_URL (or DATABASE_URL) must be set")
	}
	switch cmd {
	case "migrate":
		return migrate.RunSubcommand(migrate.Runner{
			Service: "session-broker",
			FS:      migrationsFS,
			Dir:     "migrations",
			DSN:     dsn,
		}, args[1:])
	case "outbox":
		return runOutbox(dsn, args[1:])
	case "serve":
		return serve(dsn)
	default:
		return fmt.Errorf("unknown command %q (want serve|migrate|outbox)", cmd)
	}
}

// runOutbox handles `session-broker outbox relay` — the operator-run, foreground
// relay, the same shape as the in-process goroutine serve starts (ADR-0045).
func runOutbox(dsn string, args []string) error {
	if len(args) == 0 || args[0] != "relay" {
		return errors.New("usage: session-broker outbox relay")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()
	relay, err := newRelay(pool)
	if err != nil {
		return err
	}
	log.Print("session-broker: outbox relay (foreground)")
	return relay.Run(context.Background())
}

func serve(dsn string) error {
	v, err := capabilityVerifier()
	if err != nil {
		return err
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	// Start the in-process outbox relay (ADR-0045). Best-effort: if the audit
	// endpoint is not configured the relay is skipped and the broker still
	// serves — a degraded chain authority surfaces as growing outbox depth, not
	// a failed boot.
	if relay, err := newRelay(pool); err != nil {
		log.Printf("outbox relay disabled: %v", err)
	} else {
		go func() {
			if err := relay.Run(context.Background()); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("outbox relay stopped: %v", err)
			}
		}()
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

// newRelay builds the outbox relay's HTTP client to the audit-service chain
// authority. AIREOS_AUDIT_URL must be set or the relay is declined (returned
// error is logged, not fatal). The bearer is a service-service PASETO token when
// AIREOS_PASETO_KEY is set (ADR-0046); else BROKER_AUDIT_TOKEN (dev), else none
// (a dev audit-service with insecure auth).
func newRelay(pool *pgxpool.Pool) (*outbox.Relay, error) {
	auditURL := os.Getenv("AIREOS_AUDIT_URL")
	if auditURL == "" {
		return nil, errors.New("AIREOS_AUDIT_URL not set")
	}
	token, err := auditTokenSource()
	if err != nil {
		return nil, err
	}
	return &outbox.Relay{
		Pool:   pool,
		Client: &outbox.HTTPClient{BaseURL: auditURL, Token: token},
	}, nil
}

// auditTokenSource returns the bearer-token provider for audit-service ingest.
func auditTokenSource() (func(context.Context) (string, error), error) {
	if keyHex := os.Getenv("AIREOS_PASETO_KEY"); keyHex != "" {
		key, err := auth.NewKeyFromHex(keyHex)
		if err != nil {
			return nil, fmt.Errorf("invalid AIREOS_PASETO_KEY: %w", err)
		}
		signer := auth.NewSigner(key, os.Getenv("AIREOS_CLUSTER_ID"))
		// Mint a fresh short-lived service token per delivery batch. Cheap at P0
		// volume; a cached token is a later optimization.
		return func(context.Context) (string, error) {
			return signer.IssueService("session-broker", "audit-service")
		}, nil
	}
	staticTok := os.Getenv("BROKER_AUDIT_TOKEN")
	return func(context.Context) (string, error) { return staticTok, nil }, nil
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
