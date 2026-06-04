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
//	session-broker casts gc [--older-than-days N]  # retention sweep of closed-session cast files
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
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
	"aireos.local/services/session-broker/internal/connector"
	"aireos.local/services/session-broker/internal/server"
	"aireos.local/services/session-broker/internal/store"
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
	case "casts":
		return runCasts(dsn, args[1:])
	case "serve":
		return serve(dsn)
	default:
		return fmt.Errorf("unknown command %q (want serve|migrate|outbox|casts)", cmd)
	}
}

// runCasts handles `session-broker casts gc [--older-than-days N]` — the
// operator/cron retention sweep (ADR-0048 §6 cast files; the Negative note's
// "rotation policy"). It deletes the cast file for each closed session that
// ended more than N days ago, clears the row's cast_path, and enqueues a
// cast.pruned audit event (the running broker's relay delivers it). The original
// cast SHA256 stays on the row as the durable record of what was deleted.
func runCasts(dsn string, args []string) error {
	if len(args) == 0 || args[0] != "gc" {
		return errors.New("usage: session-broker casts gc [--older-than-days N]")
	}
	fs := flag.NewFlagSet("gc", flag.ContinueOnError)
	days := fs.Int("older-than-days", 90, "prune casts for sessions closed more than N days ago")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *days < 0 {
		return errors.New("--older-than-days must be >= 0")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	ctx := context.Background()
	st := store.New(pool)
	cutoff := time.Now().AddDate(0, 0, -*days)
	prunable, err := st.ListPrunableCasts(ctx, cutoff)
	if err != nil {
		return fmt.Errorf("list prunable casts: %w", err)
	}
	pruned := 0
	for _, c := range prunable {
		if rerr := os.Remove(c.CastPath); rerr != nil && !os.IsNotExist(rerr) {
			log.Printf("casts gc: remove %s: %v", c.CastPath, rerr)
			continue
		}
		if perr := st.PruneCast(ctx, c.SessionID, c.CastSHA); perr != nil {
			log.Printf("casts gc: prune %s: %v", c.SessionID, perr)
			continue
		}
		pruned++
	}
	fmt.Fprintf(os.Stderr, "session-broker: pruned %d cast file(s) for sessions closed > %d days ago\n", pruned, *days)
	return nil
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

	// The data plane no longer holds AIREOS_RESOURCE_SECRET_KEY (T-030/SEC-13): the
	// decrypted credential is leased from connector-service at connect time. Empty
	// CONNECTOR_URL is tolerated at boot — the channel handler answers 424 until it
	// is configured — so the broker can run (healthz, relay) before connector is up.
	connURL := os.Getenv("CONNECTOR_URL")
	if connURL == "" {
		log.Print("CONNECTOR_URL not set — session channels will answer 424 until configured")
	}
	connTok, err := connectorTokenSource()
	if err != nil {
		return err
	}
	leaser := &connector.Client{BaseURL: connURL, Token: connTok}

	castDir := firstNonEmpty(os.Getenv("AIREOS_CAST_DIR"), "/var/lib/aireos/sessions")
	if err := os.MkdirAll(castDir, 0o700); err != nil {
		return fmt.Errorf("create cast dir %s: %w", castDir, err)
	}

	handler := server.New(server.Config{
		Verifier: v,
		Store:    store.New(pool),
		Leaser:   leaser,
		CastDir:  castDir,
	})

	// h2c lets the cleartext dev/CI listener speak HTTP/2; under TLS, http.Server
	// negotiates HTTP/2 on its own and h2c is a transparent pass-through.
	h := h2c.NewHandler(handler, &http2.Server{})
	srv := &http.Server{
		Addr:              firstNonEmpty(os.Getenv("BROKER_ADDR"), ":8082"),
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("session-broker listening on %s", srv.Addr)
	return srv.ListenAndServe()
}

// connectorTokenSource returns the bearer provider for connector-service's
// credential-lease RPC: a service-service PASETO token (workload "session-broker",
// audience "connector-service") when AIREOS_PASETO_KEY is set (ADR-0046/0053),
// else BROKER_CONNECTOR_TOKEN (dev), else none (a dev connector with insecure
// auth).
func connectorTokenSource() (func(context.Context) (string, error), error) {
	if keyHex := os.Getenv("AIREOS_PASETO_KEY"); keyHex != "" {
		key, err := auth.NewKeyFromHex(keyHex)
		if err != nil {
			return nil, fmt.Errorf("invalid AIREOS_PASETO_KEY: %w", err)
		}
		signer := auth.NewSigner(key, os.Getenv("AIREOS_CLUSTER_ID"))
		return func(context.Context) (string, error) {
			return signer.IssueService("session-broker", "connector-service")
		}, nil
	}
	staticTok := os.Getenv("BROKER_CONNECTOR_TOKEN")
	return func(context.Context) (string, error) { return staticTok, nil }, nil
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

// capabilityVerifier mirrors the other services' real-PASETO-or-dev-opt-in
// shape, but verifies the OPERATOR audience "api", not "session-broker".
//
// The cockpit WS upgrade carries the operator's existing aireos_session cookie,
// which services/api mints with audience "api" (the only operator-token mint in
// the system). ADR-0048 §4 is explicit that this cookie "flows through … no new
// auth path"; the cockpit is one operator-facing trust domain on one origin. A
// "session-broker" audience here was unsatisfiable by any operator token (no
// code mints one) — it 401'd every real-PASETO WS upgrade, a gap masked by every
// prior test running with BROKER_DEV_INSECURE_AUTH=1 and surfaced live by T-020b.
// Ratified in ADR-0050 (cockpit WebSocket data-plane contract).
func capabilityVerifier() (mw.Verifier, error) {
	if keyHex := os.Getenv("AIREOS_PASETO_KEY"); keyHex != "" {
		key, err := auth.NewKeyFromHex(keyHex)
		if err != nil {
			return nil, err
		}
		issuer := os.Getenv("AIREOS_CLUSTER_ID")
		return auth.AsMiddlewareVerifier(auth.NewVerifier(key, issuer, "api")), nil
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
