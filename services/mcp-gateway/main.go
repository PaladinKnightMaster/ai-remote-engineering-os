// Command mcp-gateway is the Typed MCP Gateway (ADR-0056) — the first agent
// surface. T-035 (this slice) stands up the service spine: an MCP server over the
// synchronous streamable-HTTP subset (initialize / tools/list / tools/call), the
// `agent-tool` capability gate, and an ungated /healthz. The catalog lists zero
// tools; the remote.exec tool (T-037), the invocation lifecycle + policy/approval/
// egress (T-039/T-040), the invocations store + migrations, and the operator
// approval surface on services/api (ADR-0057) are later slices.
//
// Subcommands:
//
//	mcp-gateway serve   # default; serve the MCP HTTP surface (MCP_ADDR, default :8085)
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
	"aireos.local/services/mcp-gateway/internal/apiforward"
	"aireos.local/services/mcp-gateway/internal/brokerexec"
	"aireos.local/services/mcp-gateway/internal/catalog"
	"aireos.local/services/mcp-gateway/internal/cmdcrypt"
	"aireos.local/services/mcp-gateway/internal/mcp"
	"aireos.local/services/mcp-gateway/internal/policy"
	"aireos.local/services/mcp-gateway/internal/server"
	"aireos.local/services/mcp-gateway/internal/store"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

func main() {
	if err := run(os.Args[1:]); err != nil {
		log.Fatalf("mcp-gateway: %v", err)
	}
}

func run(args []string) error {
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case "serve":
		return serve()
	case "migrate":
		dsn := firstNonEmpty(os.Getenv("MCP_DATABASE_URL"), os.Getenv("DATABASE_URL"))
		if dsn == "" {
			return errors.New("MCP_DATABASE_URL (or DATABASE_URL) must be set")
		}
		return migrate.RunSubcommand(migrate.Runner{
			Service: "mcp-gateway",
			FS:      migrationsFS,
			Dir:     "migrations",
			DSN:     dsn,
		}, args[1:])
	default:
		return fmt.Errorf("unknown command %q (want serve|migrate)", cmd)
	}
}

func serve() error {
	v, err := capabilityVerifier()
	if err != nil {
		return err
	}
	dsn := firstNonEmpty(os.Getenv("MCP_DATABASE_URL"), os.Getenv("DATABASE_URL"))
	if dsn == "" {
		return errors.New("MCP_DATABASE_URL (or DATABASE_URL) must be set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	// In-process outbox relay (ADR-0045): drains tool.invoked/tool.result to
	// audit-service. Best-effort — if AIREOS_AUDIT_URL is unset the relay is
	// skipped and the gateway still serves (degraded chain = growing outbox depth).
	if relay, err := newRelay(pool); err != nil {
		log.Printf("outbox relay disabled: %v", err)
	} else {
		go func() {
			if err := relay.Run(context.Background()); err != nil && !errors.Is(err, context.Canceled) {
				log.Printf("outbox relay stopped: %v", err)
			}
		}()
	}

	signer := serviceSigner()
	cmdCipher, err := commandCipher()
	if err != nil {
		return err
	}
	gw := store.New(pool, cmdCipher)
	pol := policy.New(os.Getenv("POLICY_URL"), signer)
	// run.launch forward to services/api (ADR-0062, T-065b): the gateway forwards an
	// agent-initiated Run launch; api validates plan-vs-grant and creates the Run. Signed with
	// the gateway's OWN asymmetric service token (api's route-scoped verifier accepts it). Unset
	// API_URL → nil → run.launch answers a tool error (agent-initiated Runs disabled).
	var forwarder mcp.LaunchForwarder
	if apiURL := os.Getenv("API_URL"); apiURL != "" {
		forwarder = apiforward.New(apiURL, signer)
	}
	cat := catalog.New(catalog.Config{
		Store:          gw,
		Policy:         pol,
		Broker:         brokerexec.New(os.Getenv("BROKER_URL"), signer),
		MaxInFlight:    maxInFlight(),
		WaitTimeout:    durationEnvSeconds("MCP_APPROVAL_WAIT_SECONDS", 125*time.Second),
		RunWaitTimeout: durationEnvSeconds("MCP_RUN_WAIT_SECONDS", 600*time.Second),
		Forwarder:      forwarder,
	})
	// Operator-plane resolver (ADR-0057 §4): the internal resolve/list routes, gated
	// service-service, that services/api proxies the operator's approve/deny to.
	resolver := catalog.NewResolver(gw, pol)

	// Invocation reaper (ADR-0059): a parked invocation whose agent disconnected
	// mid-wait would otherwise leak its concurrency slot and retain its command
	// forever. Finalize stale pending_approval rows → failed (the second-line
	// backstop; a connected agent's own block-and-wait deadline finalizes first).
	go reapLoop(context.Background(), gw, durationEnvSeconds("MCP_APPROVAL_REAP_TTL_SECONDS", 300*time.Second))

	srv := &http.Server{
		Addr:              firstNonEmpty(os.Getenv("MCP_ADDR"), ":8085"),
		Handler:           server.New(server.Config{Verifier: v, Catalog: cat, Resolver: resolver, StepRunner: cat}),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: a governed tools/call may block on synchronous human
		// approval (ADR-0054/0057 block-and-wait) once T-040 wires it.
	}
	log.Printf("mcp-gateway listening on %s (catalog: remote.exec, governed)", srv.Addr)
	return srv.ListenAndServe()
}

// serviceSigner builds the gateway's OWN asymmetric signer (ADR-0060): it signs
// every outbound service-service token (→ policy-service, session-broker,
// audit-service) with the gateway's Ed25519 key, so those peers verify it with the
// gateway's PUBLIC key alone — the gateway holds NO symmetric cluster key (SEC-20).
// nil when AIREOS_GATEWAY_SIGN_KEY is unset (dev) — the clients then send no bearer.
func serviceSigner() *auth.AsymmetricSigner {
	keyHex := os.Getenv("AIREOS_GATEWAY_SIGN_KEY")
	if keyHex == "" {
		return nil
	}
	sk, err := auth.NewAsymmetricSecretKeyFromHex(keyHex)
	if err != nil {
		log.Printf("invalid AIREOS_GATEWAY_SIGN_KEY: %v", err)
		return nil
	}
	return auth.NewAsymmetricSigner(sk, os.Getenv("AIREOS_CLUSTER_ID"))
}

// commandCipher builds the F3 command-at-rest cipher (ADR-0059 extension) from
// AIREOS_GATEWAY_COMMAND_KEY (hex, 32 bytes / AES-256), kept distinct from the
// gateway's sign / PASETO keys (authority separation). Unset → a nil cipher =
// plaintext at rest (dev/transition; the key is provisioned at install, OI-11).
func commandCipher() (*cmdcrypt.Cipher, error) {
	keyHex := os.Getenv("AIREOS_GATEWAY_COMMAND_KEY")
	if keyHex == "" {
		log.Print("AIREOS_GATEWAY_COMMAND_KEY not set — invocation commands stored in plaintext (dev/transition)")
		return nil, nil
	}
	if keyHex == os.Getenv("AIREOS_GATEWAY_SIGN_KEY") || keyHex == os.Getenv("AIREOS_PASETO_KEY") {
		return nil, errors.New("AIREOS_GATEWAY_COMMAND_KEY must differ from the gateway sign / PASETO keys (authority separation)")
	}
	return cmdcrypt.NewFromHex(keyHex)
}

// newRelay builds the outbox relay draining gateway_outbox into audit-service.
func newRelay(pool *pgxpool.Pool) (*outbox.Relay, error) {
	auditURL := os.Getenv("AIREOS_AUDIT_URL")
	if auditURL == "" {
		return nil, errors.New("AIREOS_AUDIT_URL not set")
	}
	tok, err := auditTokenSource()
	if err != nil {
		return nil, err
	}
	return &outbox.Relay{
		Pool:    pool,
		Client:  &outbox.HTTPClient{BaseURL: auditURL, Token: tok},
		Table:   store.OutboxTable,
		Channel: store.OutboxChannel,
	}, nil
}

func auditTokenSource() (func(context.Context) (string, error), error) {
	if keyHex := os.Getenv("AIREOS_GATEWAY_SIGN_KEY"); keyHex != "" {
		sk, err := auth.NewAsymmetricSecretKeyFromHex(keyHex)
		if err != nil {
			return nil, fmt.Errorf("invalid AIREOS_GATEWAY_SIGN_KEY: %w", err)
		}
		signer := auth.NewAsymmetricSigner(sk, os.Getenv("AIREOS_CLUSTER_ID"))
		return func(context.Context) (string, error) {
			return signer.IssueService("mcp-gateway", "audit-service")
		}, nil
	}
	staticTok := os.Getenv("MCP_AUDIT_TOKEN")
	return func(context.Context) (string, error) { return staticTok, nil }, nil
}

// reapLoop runs the invocation reaper on a fixed tick (ADR-0059). ttl is the parked
// age past which a never-resolved invocation is finalized — set well beyond the
// block-and-wait deadline + policy TTL so it only catches orphans, not live waits.
func reapLoop(ctx context.Context, st *store.Store, ttl time.Duration) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := st.ReapStale(ctx, ttl)
			if err != nil {
				log.Printf("invocation reaper: %v", err)
				continue
			}
			if n > 0 {
				log.Printf("invocation reaper: finalized %d stale parked invocation(s)", n)
			}
		}
	}
}

// durationEnvSeconds reads a positive-integer-seconds env var, falling back to def.
func durationEnvSeconds(name string, def time.Duration) time.Duration {
	if s := os.Getenv(name); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
		log.Printf("ignoring invalid %s=%q", name, s)
	}
	return def
}

// maxInFlight resolves the per-agent concurrency cap (PERF-3) from
// MCP_MAX_INFLIGHT_PER_AGENT (positive int), default 4.
func maxInFlight() int {
	if s := os.Getenv("MCP_MAX_INFLIGHT_PER_AGENT"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			return n
		}
		log.Printf("ignoring invalid MCP_MAX_INFLIGHT_PER_AGENT=%q", s)
	}
	return 4
}

// capabilityVerifier builds the agent-token verifier (ADR-0046/0060). The gateway
// holds NO symmetric cluster key (SEC-20): all inbound — the agent-tool token on
// /mcp and api's service-service token on the internal plane — is minted by api on
// the asymmetric agent plane and verified here with api's PUBLIC key alone (audience
// "mcp-gateway"). The dual-mode (symmetric) inbound from T-047 collapses away once
// every inbound caller is asymmetric (T-048).
func capabilityVerifier() (mw.Verifier, error) {
	if pubHex := os.Getenv("AIREOS_API_AGENT_PUBLIC_KEY"); pubHex != "" {
		pub, err := auth.NewAsymmetricPublicKeyFromHex(pubHex)
		if err != nil {
			return nil, fmt.Errorf("invalid AIREOS_API_AGENT_PUBLIC_KEY: %w", err)
		}
		issuer := os.Getenv("AIREOS_CLUSTER_ID")
		return auth.AsMiddlewareVerifier(auth.NewAsymmetricVerifier(pub, issuer, "mcp-gateway")), nil
	}
	if os.Getenv("MCP_GATEWAY_DEV_INSECURE_AUTH") == "1" {
		log.Print("WARNING: MCP_GATEWAY_DEV_INSECURE_AUTH=1 — dev allow-all verifier; NOT for production")
		return devAllowAll{}, nil
	}
	return nil, errors.New("no capability verifier configured: set AIREOS_API_AGENT_PUBLIC_KEY (ADR-0060) or MCP_GATEWAY_DEV_INSECURE_AUTH=1 for local dev")
}

type devAllowAll struct{}

func (devAllowAll) Verify(_ context.Context, _ string) (mw.Capability, error) {
	return mw.Capability{Subject: "dev", Edge: "agent-tool"}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
