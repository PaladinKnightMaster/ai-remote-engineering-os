// Package mcp holds the gateway's protocol-edge types + the governance seam
// (ADR-0056). The MCP wire format is a thin, replaceable edge: the HTTP/JSON-RPC
// handler (internal/server) depends only on the Catalog interface here, so the
// governance core stays protocol-agnostic and an SDK could later replace the
// hand-rolled wire behind this boundary without touching the core.
package mcp

import (
	"context"
	"encoding/json"
	"errors"
)

// Protocol identity advertised in the MCP `initialize` response. ProtocolVersion
// is pinned (ADR-0056 §2) — a client negotiates against it.
const (
	ProtocolVersion = "2025-06-18"
	ServerName      = "aireos-mcp-gateway"
	ServerVersion   = "0.3.0-dev"
)

// Tool is a typed catalog entry surfaced to agents via tools/list (ADR-0057). The
// remote.exec tool lands in T-037; the skeleton catalog lists none.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

// Content is one piece of a tool result (MCP content block).
type Content struct {
	Type string `json:"type"` // "text"
	Text string `json:"text,omitempty"`
}

// ToolResult is the outcome of a tools/call. IsError marks a tool-level failure
// (distinct from a JSON-RPC protocol error).
type ToolResult struct {
	Content []Content `json:"content"`
	IsError bool      `json:"isError,omitempty"`
}

// Catalog is the protocol-agnostic governance seam. The wire handler calls List
// for tools/list and Invoke for tools/call; the governed implementation (T-039:
// policy → approval → connector lease → broker exec → audit) lives behind it.
type Catalog interface {
	List() []Tool
	Invoke(ctx context.Context, name string, args json.RawMessage) (ToolResult, error)
}

// InvocationView is the operator's view of a parked invocation (ADR-0057/0059) — the
// internal pending-list payload services/api proxies to the operator console. It
// carries the persisted Command so the operator gives informed consent.
type InvocationView struct {
	ID         string `json:"id"`
	Agent      string `json:"agent"`
	ResourceID string `json:"resourceId"`
	Tool       string `json:"tool"`
	Command    string `json:"command"`
	Status     string `json:"status"`
}

// ResolverPort is the operator-plane seam (ADR-0057 §4): the internal HTTP handler
// (service-service, proxied by api) lists parked invocations and resolves one. The
// governed implementation (catalog.Resolver: policy.Resolve → grant/deny + the
// fail-closed reconcile) lives behind it, off the agent plane.
type ResolverPort interface {
	ListPending(ctx context.Context) ([]InvocationView, error)
	// Resolve applies the operator's decision ("granted" | "denied"). It returns
	// ErrInvocationNotFound for an unknown id and ErrApprovalNotPending when the
	// invocation is no longer awaiting approval (already resolved, or policy timed
	// it out); any other error is an infrastructure failure.
	Resolve(ctx context.Context, id, decision, resolvedBy string) error
}

// Operator-plane sentinels the resolve handler maps to HTTP status (404 / 409).
var (
	ErrInvocationNotFound = errors.New("invocation not found")
	ErrApprovalNotPending = errors.New("invocation is not awaiting approval")
)

// Resolve decision vocabulary (ADR-0054 policy verbs). services/api maps the operator
// verbs approve/deny → these before proxying to the gateway's internal resolve route.
const (
	DecisionGranted = "granted"
	DecisionDenied  = "denied"
)

// ctxKey is the private type for this package's context values.
type ctxKey int

const idempotencyKeyCtx ctxKey = iota

// WithIdempotencyKey stashes the client's Idempotency-Key (the server reads the
// HTTP header) so the governed Invoke can dedup retries (PERF-2) without widening
// the Catalog interface.
func WithIdempotencyKey(ctx context.Context, key string) context.Context {
	return context.WithValue(ctx, idempotencyKeyCtx, key)
}

// IdempotencyKey returns the stashed key, or "" if none.
func IdempotencyKey(ctx context.Context) string {
	k, _ := ctx.Value(idempotencyKeyCtx).(string)
	return k
}

// InitializeResult is the MCP `initialize` response: the pinned protocol version,
// the tools capability, and server identity.
func InitializeResult() map[string]any {
	return map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": ServerName, "version": ServerVersion},
	}
}
