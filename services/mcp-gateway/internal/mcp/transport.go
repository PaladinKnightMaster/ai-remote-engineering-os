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
	"time"
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
	ID    string `json:"id"`
	Agent string `json:"agent"`
	// Principal is the delegating operator when an agent drives this step under a grant
	// (ADR-0062, FE-18); "" for an operator-direct Run step or an agent-tool invocation.
	Principal  string `json:"principal,omitempty"`
	ResourceID string `json:"resourceId"`
	Tool       string `json:"tool"`
	Command    string `json:"command"`
	Status     string `json:"status"`
	// IdempotencyKey is the parked step's dedup key (e.g. "run:<id>:step:<n>" for a
	// governed Run step). Opaque to the gateway; services/api parses it for the
	// operator's FE-16 Run context. Empty/absent for an agent-tool invocation.
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
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

// StepRequest is the operator Run engine's input to ONE governed step (ADR-0061,
// T-055b): the launching operator + one plan step. The gateway's RunStep fills the
// fixed tool + edges; the Run engine never holds agent-token machinery.
type StepRequest struct {
	Subject        string `json:"subject"`             // the ACTOR (audit actor + policy subject): operator for a direct Run, agent for a delegated Run
	Principal      string `json:"principal,omitempty"` // the PRINCIPAL (ADR-0062 §4): the delegating operator when an agent drives under a grant; "" for a direct operator Run
	ResourceID     string `json:"resourceId"`          // target resource
	Command        string `json:"command"`             // the command to run
	IdempotencyKey string `json:"idempotencyKey"`      // deterministic per step (run:<id>:step:<index>)
}

// StepRunner is the operator Run-engine seam (ADR-0061 §2): services/api's Run
// sequencer drives one governed step here via the gateway's subject-pinned internal
// /v1/steps route. The implementation (catalog.Service.RunStep, reusing the agent
// path's governed core) runs off the agent plane. nil → /v1/steps is not mounted.
type StepRunner interface {
	// RunStep returns the gateway invocation id (empty for a refusal that created no
	// invocation) so the Run engine can record the run→invocation correlation (AUD-15).
	RunStep(ctx context.Context, req StepRequest) (invocationID string, result ToolResult, err error)
}

// LaunchStep is one step of an agent-initiated Run launch (ADR-0062, T-065b).
type LaunchStep struct {
	ResourceID string
	Command    string
}

// LaunchForwarder forwards an agent's grant-bounded Run launch to services/api (ADR-0062
// §3): the gateway does NOT create Runs — it forwards {agent, grant, plan} to api, which
// validates plan-vs-grant and creates the Run. It returns the new run id + status; a
// non-empty refusal means api refused the plan (out-of-grant, N1) — a tool error for the
// agent, not a Go error; err is an infrastructure failure (fail-closed). nil → run.launch
// is not configured.
type LaunchForwarder interface {
	Launch(ctx context.Context, agentSubject, grantID, idempotencyKey string, steps []LaunchStep) (runID, status, refusal string, err error)
}

// PlanRequestView is one open plan request the composer's workstream.poll call sees
// (ADR-0065 §5, T-090): composition context — objective + the bounding grant's
// resource/verb scope + step bound + the request's TTL — everything the agent needs to
// compose a plan without a second round trip.
type PlanRequestView struct {
	RequestID      string    `json:"requestId"`
	WorkstreamID   string    `json:"workstreamId"`
	Objective      string    `json:"objective"`
	Resources      []string  `json:"resources"`
	Verbs          []string  `json:"verbs"`
	MaxStepsPerRun int       `json:"maxStepsPerRun"`
	ExpiresAt      time.Time `json:"expiresAt"`
}

// PollForwarder forwards workstream.poll to services/api (ADR-0065 §5, T-090): the
// keyless gateway relays {agent, optional workstream filter} to api's subject-pinned
// internal route, which derives the caller's own open plan requests from its LIVE
// grants only — never a foreign charter (SEC-32 containment). A workstreamID outside
// the caller's own scope answers an empty slice, never an error (no oracle). nil →
// workstream.poll answers a tool error.
type PollForwarder interface {
	Poll(ctx context.Context, agentSubject, workstreamID string) ([]PlanRequestView, error)
}

// SubmitForwarder forwards plan.submit to services/api (ADR-0065 §6, T-090): the
// keyless gateway relays {agent, requestId, plan} to api's subject-pinned internal
// route, which validates the impersonation pin + plan ⊆ grant (SEC-29, the shared
// LaunchBounded core) and creates a `proposed` Run. A non-empty refusal is api's
// refusal (a tool error for the agent — an unknown request and a foreign agent's
// request read IDENTICALLY, no oracle, §6); err is an infrastructure failure
// (fail-closed). nil → plan.submit answers a tool error.
type SubmitForwarder interface {
	Submit(ctx context.Context, agentSubject, requestID string, steps []LaunchStep) (runID, status, refusal string, err error)
}

// MemoryRowView is one memory row on the agent-plane read (ADR-0065 §3, T-090):
// content-free for an accrued reference row; opened content for a shared authored row
// (an unshared authored row or a redacted row never reaches this view — SEC-32).
type MemoryRowView struct {
	ID          string    `json:"id"`
	ScopeType   string    `json:"scopeType"`
	ScopeID     string    `json:"scopeId"`
	Key         string    `json:"key,omitempty"`
	Ref         string    `json:"ref,omitempty"`
	Content     string    `json:"content,omitempty"`
	SourceTrace string    `json:"sourceTrace,omitempty"`
	Confidence  float32   `json:"confidence"`
	CreatedAt   time.Time `json:"createdAt"`
}

// ReadForwarder forwards memory.read to services/api (ADR-0065 §3, T-090): the keyless
// gateway relays {agent, optional workstream filter} to api's subject-pinned internal
// route, which derives the caller's readable set from its own LIVE grants (SEC-32
// containment BY CONSTRUCTION) and chains ONE memory.read event per call (R4). err is
// an infrastructure failure (fail-closed); a revoked/expired grant or an out-of-scope
// filter answers an EMPTY slice, never an error (no oracle). nil → memory.read answers
// a tool error.
type ReadForwarder interface {
	Read(ctx context.Context, agentSubject, workstreamID string) ([]MemoryRowView, error)
}

// ContextChunkView is one ranked context chunk on the agent-plane read (ADR-0066 §4,
// T-099): content-bearing — the composer needs the chunk text + its provenance
// (file/locator) to ground a plan, exactly as MemoryRowView carries opened content for
// the composer over a different corpus.
type ContextChunkView struct {
	ChunkID  string `json:"chunkId"`
	SourceID string `json:"sourceId"`
	FilePath string `json:"filePath"`
	Locator  string `json:"locator"`
	Content  string `json:"content"`
}

// ContextSearchForwarder forwards context.search to services/api (ADR-0066 §4, T-099):
// the keyless gateway relays {agent, query, optional workstream filter} to api's
// subject-pinned internal route, which derives the caller's readable context sources
// entirely from its own LIVE grants (SEC-33 containment BY CONSTRUCTION) and chains ONE
// context.read event per call. err is an infrastructure failure (fail-closed); a
// revoked/expired grant, an out-of-scope filter, or simply no matches all answer an
// EMPTY slice, never an error (no oracle) — the memory.read Read precedent. nil →
// context.search answers a tool error.
type ContextSearchForwarder interface {
	Search(ctx context.Context, agentSubject, query, workstreamID string) ([]ContextChunkView, error)
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
