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
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"aireos.local/packages/go/auth"
	mw "aireos.local/packages/go/middleware"
	"aireos.local/services/mcp-gateway/internal/catalog"
	"aireos.local/services/mcp-gateway/internal/server"
)

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
	default:
		return fmt.Errorf("unknown command %q (want serve)", cmd)
	}
}

func serve() error {
	v, err := capabilityVerifier()
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              firstNonEmpty(os.Getenv("MCP_ADDR"), ":8085"),
		Handler:           server.New(server.Config{Verifier: v, Catalog: catalog.New()}),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: a governed tools/call may block on synchronous human
		// approval (ADR-0054/0057 block-and-wait, ~120s) once T-039 wires it.
	}
	log.Printf("mcp-gateway listening on %s (catalog: remote.exec)", srv.Addr)
	return srv.ListenAndServe()
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
