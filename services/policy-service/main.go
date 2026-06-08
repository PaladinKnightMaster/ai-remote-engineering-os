// Command policy-service is the approval/policy decision point (ADR-0054). At
// v0.2.0 it returns allow | deny | requires_approval for an (actor, edge, target,
// action) tuple and brokers the human approve/deny round-trip when the answer is
// requires_approval — every decision audit-chained (ADR-0045/0049). It is the
// "approval" leg of the CLAUDE.md doctrine "no AI autonomy before audit + policy +
// approval primitives exist".
//
// T-031a-i (this slice) stands up the service spine: the schema
// (pending_approvals + policy_outbox), the HTTP surface (ungated /healthz), and
// the in-process audit relay. The gated decide/resolve RPCs + the audited store +
// the fail-closed timeout sweeper are T-031a-ii; the services/api enforcement seam
// (consult at session-create) is T-031b; the <ApprovalPrompt> UI is T-031c.
//
// Subcommands (uniform stateful-service surface, ADR-0044/0045):
//
//	policy-service serve                    # default; serve HTTP + start the in-process outbox relay
//	policy-service migrate up|version|force <v>
//	policy-service outbox relay             # run the outbox relay in the foreground
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

	"aireos.local/packages/go/auth"
	"aireos.local/packages/go/migrate"
	"aireos.local/packages/go/outbox"
	"aireos.local/services/policy-service/internal/server"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("policy-service: %v", err)
	}
}

func run(args []string) error {
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	}
	dsn := firstNonEmpty(os.Getenv("POLICY_DATABASE_URL"), os.Getenv("DATABASE_URL"))
	if dsn == "" {
		return errors.New("POLICY_DATABASE_URL (or DATABASE_URL) must be set")
	}
	switch cmd {
	case "serve":
		return serve(dsn)
	case "migrate":
		return migrate.RunSubcommand(migrate.Runner{
			Service: "policy-service",
			FS:      migrationsFS,
			Dir:     "migrations",
			DSN:     dsn,
		}, args[1:])
	case "outbox":
		return runOutbox(dsn, args[1:])
	default:
		return fmt.Errorf("unknown command %q (want serve|migrate|outbox)", cmd)
	}
}

// serve runs the HTTP surface (ungated /healthz at T-031a-i; the gated
// decide/resolve RPCs land in T-031a-ii) and starts the in-process outbox relay
// (ADR-0045). Best-effort relay: if AIREOS_AUDIT_URL is unset the relay is skipped
// and policy-service still serves — a degraded chain authority surfaces as growing
// outbox depth, not a failed boot.
func serve(dsn string) error {
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	if relay, err := newRelay(pool); err != nil {
		log.Printf("outbox relay disabled: %v", err)
	} else {
		go func() {
			if err := relay.Run(context.Background()); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("outbox relay stopped: %v", err)
			}
		}()
	}

	srv := &http.Server{
		Addr:              firstNonEmpty(os.Getenv("POLICY_ADDR"), ":8084"),
		Handler:           server.New(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("policy-service listening on %s", srv.Addr)
	return srv.ListenAndServe()
}

// runOutbox handles `policy-service outbox relay` — the operator-run, foreground
// relay, the same shape as the goroutine serve starts (ADR-0045).
func runOutbox(dsn string, args []string) error {
	if len(args) == 0 || args[0] != "relay" {
		return errors.New("usage: policy-service outbox relay")
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
	log.Print("policy-service: outbox relay (foreground)")
	return relay.Run(context.Background())
}

// newRelay builds the outbox relay draining policy_outbox into audit-service.
// AIREOS_AUDIT_URL must be set or the relay is declined (logged, not fatal). The
// bearer is a service-service PASETO token when AIREOS_PASETO_KEY is set
// (ADR-0046); else POLICY_AUDIT_TOKEN (dev), else none.
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
		Pool:    pool,
		Client:  &outbox.HTTPClient{BaseURL: auditURL, Token: token},
		Table:   "policy_outbox",
		Channel: "policy_outbox_new",
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
		return func(context.Context) (string, error) {
			return signer.IssueService("policy-service", "audit-service")
		}, nil
	}
	staticTok := os.Getenv("POLICY_AUDIT_TOKEN")
	return func(context.Context) (string, error) { return staticTok, nil }, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
