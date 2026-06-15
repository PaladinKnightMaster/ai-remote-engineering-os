// Package mcp holds the gateway's protocol-edge types + the governance seam
// (ADR-0056). The MCP wire format is a thin, replaceable edge: the HTTP/JSON-RPC
// handler (internal/server) depends only on the Catalog interface here, so the
// governance core stays protocol-agnostic and an SDK could later replace the
// hand-rolled wire behind this boundary without touching the core.
package mcp

import (
	"context"
	"encoding/json"
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
