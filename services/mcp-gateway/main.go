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
	"aireos.local/services/mcp-gateway/internal/brokerexec"
	"aireos.local/services/mcp-gateway/internal/catalog"
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
	cat := catalog.New(
		store.New(pool),
		policy.New(os.Getenv("POLICY_URL"), signer),
		brokerexec.New(os.Getenv("BROKER_URL"), signer),
		maxInFlight(),
	)
	srv := &http.Server{
		Addr:              firstNonEmpty(os.Getenv("MCP_ADDR"), ":8085"),
		Handler:           server.New(server.Config{Verifier: v, Catalog: cat}),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: a governed tools/call may block on synchronous human
		// approval (ADR-0054/0057 block-and-wait) once T-040 wires it.
	}
	log.Printf("mcp-gateway listening on %s (catalog: remote.exec, governed)", srv.Addr)
	return srv.ListenAndServe()
}

// serviceSigner builds the cluster signer the gateway uses to mint its
// service-service tokens (→ policy-service, session-broker, audit-service). nil
// when AIREOS_PASETO_KEY is unset (dev) — the clients then send no bearer.
func serviceSigner() *auth.Signer {
	keyHex := os.Getenv("AIREOS_PASETO_KEY")
	if keyHex == "" {
		return nil
	}
	key, err := auth.NewKeyFromHex(keyHex)
	if err != nil {
		log.Printf("invalid AIREOS_PASETO_KEY: %v", err)
		return nil
	}
	return auth.NewSigner(key, os.Getenv("AIREOS_CLUSTER_ID"))
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
	if keyHex := os.Getenv("AIREOS_PASETO_KEY"); keyHex != "" {
		key, err := auth.NewKeyFromHex(keyHex)
		if err != nil {
			return nil, fmt.Errorf("invalid AIREOS_PASETO_KEY: %w", err)
		}
		signer := auth.NewSigner(key, os.Getenv("AIREOS_CLUSTER_ID"))
		return func(context.Context) (string, error) {
			return signer.IssueService("mcp-gateway", "audit-service")
		}, nil
	}
	staticTok := os.Getenv("MCP_AUDIT_TOKEN")
	return func(context.Context) (string, error) { return staticTok, nil }, nil
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

// capabilityVerifier builds the agent-token verifier (ADR-0046): tokens must be
// minted for audience "mcp-gateway" under the cluster key. Mirrors the other
// services' real-PASETO-or-dev-opt-in shape.
func capabilityVerifier() (mw.Verifier, error) {
	if keyHex := os.Getenv("AIREOS_PASETO_KEY"); keyHex != "" {
		key, err := auth.NewKeyFromHex(keyHex)
		if err != nil {
			return nil, err
		}
		return auth.AsMiddlewareVerifier(auth.NewVerifier(key, os.Getenv("AIREOS_CLUSTER_ID"), "mcp-gateway")), nil
	}
	if os.Getenv("MCP_GATEWAY_DEV_INSECURE_AUTH") == "1" {
		log.Print("WARNING: MCP_GATEWAY_DEV_INSECURE_AUTH=1 — dev allow-all verifier; NOT for production")
		return devAllowAll{}, nil
	}
	return nil, errors.New("no capability verifier configured: set AIREOS_PASETO_KEY or MCP_GATEWAY_DEV_INSECURE_AUTH=1 for local dev")
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
