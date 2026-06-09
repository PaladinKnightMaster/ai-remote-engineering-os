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
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/packages/go/auth"
	mw "aireos.local/packages/go/middleware"
	"aireos.local/packages/go/migrate"
	"aireos.local/packages/go/outbox"
	"aireos.local/services/policy-service/internal/server"
	"aireos.local/services/policy-service/internal/store"
)

// Approval-timeout defaults (ADR-0054 §6: "~120s, fail-closed"). The TTL is
// written onto each pending row at decide time; the sweeper auto-denies past it.
const (
	defaultApprovalTTL = 120 * time.Second
	sweeperTick        = 10 * time.Second
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
	v, err := capabilityVerifier()
	if err != nil {
		return err
	}
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

	// The fail-closed timeout sweeper (ADR-0054 §6): every tick it auto-denies any
	// pending approval past its expiry, emitting approval.denied:timeout. Runs for
	// the life of the process alongside the relay.
	ttl := approvalTTL()
	go runSweeper(context.Background(), store.New(pool), sweeperTick)

	srv := &http.Server{
		Addr:              firstNonEmpty(os.Getenv("POLICY_ADDR"), ":8084"),
		Handler:           server.New(server.Config{Pool: pool, Verifier: v, TTL: ttl}),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("policy-service listening on %s (approval TTL %s)", srv.Addr, ttl)
	return srv.ListenAndServe()
}

// capabilityVerifier verifies api's service-service token (api mints
// IssueService("api","policy-service"), so the verified audience is
// "policy-service"). Mirrors the other services' real-PASETO-or-dev-opt-in shape.
func capabilityVerifier() (mw.Verifier, error) {
	if keyHex := os.Getenv("AIREOS_PASETO_KEY"); keyHex != "" {
		key, err := auth.NewKeyFromHex(keyHex)
		if err != nil {
			return nil, err
		}
		return auth.AsMiddlewareVerifier(auth.NewVerifier(key, os.Getenv("AIREOS_CLUSTER_ID"), "policy-service")), nil
	}
	if os.Getenv("POLICY_DEV_INSECURE_AUTH") == "1" {
		log.Print("WARNING: POLICY_DEV_INSECURE_AUTH=1 — dev allow-all capability verifier; NOT for production")
		return devAllowAll{}, nil
	}
	return nil, errors.New("no capability verifier configured: set AIREOS_PASETO_KEY or POLICY_DEV_INSECURE_AUTH=1 for local dev")
}

type devAllowAll struct{}

func (devAllowAll) Verify(_ context.Context, _ string) (mw.Capability, error) {
	return mw.Capability{Subject: "dev", Edge: "service-service"}, nil
}

// approvalTTL resolves the pending-approval timeout: POLICY_APPROVAL_TTL_SECONDS
// (positive int) overrides the ~120s default (ADR-0054 §6).
func approvalTTL() time.Duration {
	if s := os.Getenv("POLICY_APPROVAL_TTL_SECONDS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
		log.Printf("ignoring invalid POLICY_APPROVAL_TTL_SECONDS=%q", s)
	}
	return defaultApprovalTTL
}

// runSweeper ticks until ctx is canceled, fail-closing timed-out approvals.
func runSweeper(ctx context.Context, st *store.Store, tick time.Duration) {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := st.ExpireTimedOut(ctx, time.Now()); err != nil {
				log.Printf("policy sweeper: %v", err)
			} else if n > 0 {
				log.Printf("policy sweeper: auto-denied %d timed-out approval(s)", n)
			}
		}
	}
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
