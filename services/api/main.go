// Command api is the operator-facing gateway (ADR-0043). It serves the resource
// registry (T-005b) gated by capability tokens; operator login (ADR-0046) lands
// in a later auth ticket.
//
// Subcommands:
//
//	api serve                       # default; run the HTTP server
//	api migrate up|version|force <v>
//
// Target-credential custody moved to connector-service at v0.2.0 (ADR-0053,
// T-030): provisioning is `connector-service credentials …`; api holds no
// resource secret key and never decrypts.
//
// (The shared migrate subcommand exposes up|version|force; wiring a `down`
// action for standard up/down services like api is a small follow-up on
// packages/go/migrate — the 0001 down.sql file already exists.)
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
	"aireos.local/services/api/internal/account"
	"aireos.local/services/api/internal/server"
	"aireos.local/services/api/internal/store"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("api: %v", err)
	}
}

func run(args []string) error {
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	}
	dsn := firstNonEmpty(os.Getenv("API_DATABASE_URL"), os.Getenv("DATABASE_URL"))
	if dsn == "" {
		return errors.New("API_DATABASE_URL (or DATABASE_URL) must be set")
	}
	switch cmd {
	case "migrate":
		return migrate.RunSubcommand(migrate.Runner{
			Service: "api",
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

func serve(dsn string) error {
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	v, signer, authn, err := buildAuth()
	if err != nil {
		return err
	}

	// Start the in-process outbox relay (ADR-0045 / SEC-9): api's auth +
	// control-plane evidence is drained from api_outbox to audit-service.
	// Best-effort — if AIREOS_AUDIT_URL is unset the relay is skipped and api
	// still serves; a degraded chain authority surfaces as growing outbox depth,
	// not a failed boot.
	if relay, rerr := newRelay(pool); rerr != nil {
		log.Printf("outbox relay disabled: %v", rerr)
	} else {
		go func() {
			if err := relay.Run(context.Background()); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("outbox relay stopped: %v", err)
			}
		}()
	}

	// Start the stuck-session reaper (T-032): the same in-process goroutine shape
	// as the relay. It closes sessions stranded pre-open — a 'pending' that no
	// broker attached to, or a 'pending_approval' whose approval was never resolved
	// (policy auto-denies but cannot write the api session row).
	go runReaper(context.Background(), store.New(pool))

	srv := &http.Server{
		Addr:              firstNonEmpty(os.Getenv("API_ADDR"), ":8080"),
		Handler:           server.New(store.New(pool), authn, signer, v, os.Getenv("AIREOS_AUDIT_URL"), os.Getenv("POLICY_URL"), server.WithGateway(os.Getenv("GATEWAY_URL")), server.WithAgentSigner(agentPlaneSigner())),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("api listening on %s", srv.Addr)
	return srv.ListenAndServe()
}

// buildAuth resolves the capability Verifier plus the Signer + operator
// Authenticator used by login (ADR-0046). With AIREOS_PASETO_KEY set: a real
// PASETO verifier (audience "api"), a Signer, and the env-provisioned bootstrap
// operator (login is disabled with a 503 if the operator env is absent). Else
// API_DEV_INSECURE_AUTH=1 gives the dev allow-all verifier (no signer/operator,
// so login is disabled). Else fail loud.
func buildAuth() (mw.Verifier, *auth.Signer, server.Authenticator, error) {
	if keyHex := os.Getenv("AIREOS_PASETO_KEY"); keyHex != "" {
		key, err := auth.NewKeyFromHex(keyHex)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("invalid AIREOS_PASETO_KEY: %w", err)
		}
		issuer := os.Getenv("AIREOS_CLUSTER_ID") // optional; empty skips the issuer rule
		v := auth.AsMiddlewareVerifier(auth.NewVerifier(key, issuer, "api"))
		signer := auth.NewSigner(key, issuer)
		var authn server.Authenticator
		if op, err := account.FromEnv(); err == nil {
			authn = op
		} else {
			log.Printf("operator login disabled: %v", err)
		}
		return v, signer, authn, nil
	}
	if os.Getenv("API_DEV_INSECURE_AUTH") == "1" {
		log.Print("WARNING: API_DEV_INSECURE_AUTH=1 — using dev allow-all capability verifier; NOT for production")
		return devAllowAll{}, nil, nil, nil
	}
	return nil, nil, nil, errors.New("no capability verifier configured: set AIREOS_PASETO_KEY (hex cluster key, ADR-0046) or API_DEV_INSECURE_AUTH=1 for local dev")
}

// agentPlaneSigner builds the asymmetric agent-plane signer (ADR-0060): api signs
// agent-tool tokens with this Ed25519 key so the gateway verifies them with the
// matching public key alone. nil when AIREOS_API_AGENT_SIGN_KEY is unset —
// IssueAgentToken then falls back to the symmetric signer (dev/transition). The
// issuer matches buildAuth's (AIREOS_CLUSTER_ID), so the gateway's verifier rule
// aligns.
func agentPlaneSigner() *auth.AsymmetricSigner {
	keyHex := os.Getenv("AIREOS_API_AGENT_SIGN_KEY")
	if keyHex == "" {
		return nil
	}
	sk, err := auth.NewAsymmetricSecretKeyFromHex(keyHex)
	if err != nil {
		log.Printf("invalid AIREOS_API_AGENT_SIGN_KEY: %v", err)
		return nil
	}
	return auth.NewAsymmetricSigner(sk, os.Getenv("AIREOS_CLUSTER_ID"))
}

type devAllowAll struct{}

func (devAllowAll) Verify(_ context.Context, _ string) (mw.Capability, error) {
	return mw.Capability{Subject: "dev", Edge: "human-agent"}, nil
}

// Stuck-session reaper defaults (T-032). A 'pending' session is reaped after it
// has sat unattached for pendingTTL; a 'pending_approval' after approvalTTL, which
// MUST exceed the ~120s policy approval timeout so the operator/policy resolves
// first. Active sessions are never reaped (left to the broker's idle timer).
const (
	defaultReapPendingTTL  = 30 * time.Minute
	defaultReapApprovalTTL = 5 * time.Minute
	reaperTick             = 2 * time.Minute
)

// runReaper periodically closes stuck pre-open sessions (T-032). Best-effort: an
// error logs and the loop continues — a stalled reaper surfaces as lingering
// pending rows, not a failed serve. Runs for the life of the process.
func runReaper(ctx context.Context, st *store.Store) {
	pendingTTL := reapDuration("API_REAP_PENDING_MINUTES", defaultReapPendingTTL)
	approvalTTL := reapDuration("API_REAP_APPROVAL_MINUTES", defaultReapApprovalTTL)
	t := time.NewTicker(reaperTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			now := time.Now()
			if n, err := st.ReapStale(ctx, now.Add(-pendingTTL), now.Add(-approvalTTL)); err != nil {
				log.Printf("session reaper: %v", err)
			} else if n > 0 {
				log.Printf("session reaper: closed %d stuck session(s)", n)
			}
		}
	}
}

// reapDuration reads a positive-integer minutes override from env, else the default.
func reapDuration(env string, def time.Duration) time.Duration {
	if s := os.Getenv(env); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return time.Duration(n) * time.Minute
		}
		log.Printf("ignoring invalid %s=%q", env, s)
	}
	return def
}

// runOutbox handles `api outbox relay` — the operator-run foreground relay, the
// same shape as the in-process goroutine serve starts (ADR-0045).
func runOutbox(dsn string, args []string) error {
	if len(args) == 0 || args[0] != "relay" {
		return errors.New("usage: api outbox relay")
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
	log.Print("api: outbox relay (foreground)")
	return relay.Run(context.Background())
}

// newRelay builds api's outbox relay → audit-service. AIREOS_AUDIT_URL must be
// set or the relay is declined (logged, not fatal). The relay drains api's own
// api_outbox table/channel (ADR-0048 shared DB; the broker owns "outbox").
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
		Table:   store.OutboxTable,
		Channel: store.OutboxChannel,
	}, nil
}

// auditTokenSource returns the bearer provider for audit-service ingest: a
// service-service PASETO token (workload "api", audience "audit-service") when
// AIREOS_PASETO_KEY is set (ADR-0046), else API_AUDIT_TOKEN (dev), else none.
func auditTokenSource() (func(context.Context) (string, error), error) {
	if keyHex := os.Getenv("AIREOS_PASETO_KEY"); keyHex != "" {
		key, err := auth.NewKeyFromHex(keyHex)
		if err != nil {
			return nil, fmt.Errorf("invalid AIREOS_PASETO_KEY: %w", err)
		}
		signer := auth.NewSigner(key, os.Getenv("AIREOS_CLUSTER_ID"))
		return func(context.Context) (string, error) {
			return signer.IssueService("api", "audit-service")
		}, nil
	}
	staticTok := os.Getenv("API_AUDIT_TOKEN")
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
