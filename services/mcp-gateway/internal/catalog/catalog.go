// Package catalog holds the gateway's typed tool catalog (ADR-0057). It keeps two
// concerns apart so the governed executor (T-039) can be added without polluting
// the tool definitions:
//
//   - Registry — pure data: the tool definitions (name, schema, edge, action) plus
//     List / Lookup / Target. No execution; no policy/connector/broker dependency.
//   - Service  — satisfies mcp.Catalog for the wire handler: List delegates to the
//     Registry; Invoke is a stub until T-039's orchestrator supplies the governed
//     path (policy → approval → connector lease → broker exec → audit). That
//     orchestrator will *hold* the Registry but keep it pure.
package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	mw "aireos.local/packages/go/middleware"
	"aireos.local/services/mcp-gateway/internal/mcp"
	"aireos.local/services/mcp-gateway/internal/store"
)

// ToolDef is the full tool definition (ADR-0057): the MCP wire surface
// (name/description/inputSchema) plus the governance metadata (edge/action) the
// T-039 orchestrator feeds to policy + connector. target decodes + validates the
// tool's typed arguments and resolves the audited target from them.
type ToolDef struct {
	Name        string
	Description string
	InputSchema json.RawMessage
	Edge        string // the capability edge the invocation traverses (ADR-0046)
	Action      string // the audited action (policy.Decide + the audit chain)

	target func(args json.RawMessage) (string, error)
}

// Tool returns the MCP wire view; the governance metadata stays internal.
func (d ToolDef) Tool() mcp.Tool {
	return mcp.Tool{Name: d.Name, Description: d.Description, InputSchema: d.InputSchema}
}

// Target resolves the audited target ("resource:<id>") from the invocation's raw
// arguments, validating them in the process.
func (d ToolDef) Target(args json.RawMessage) (string, error) { return d.target(args) }

// Registry is the immutable set of tool definitions. No execution, no
// downstream-service dependency — the durable artifact the orchestrator reads.
type Registry struct {
	defs map[string]ToolDef
	list []mcp.Tool // stable order for tools/list
}

// NewRegistry builds the v0.3.0 registry: exactly one tool, remote.exec (the WD-3
// wedge). The multi-tool catalog is v0.4.0.
func NewRegistry() *Registry {
	r := &Registry{defs: map[string]ToolDef{}}
	for _, d := range []ToolDef{remoteExec()} {
		r.defs[d.Name] = d
		r.list = append(r.list, d.Tool())
	}
	return r
}

// List returns the wire tools (tools/list).
func (r *Registry) List() []mcp.Tool { return r.list }

// Lookup returns a tool definition by name.
func (r *Registry) Lookup(name string) (ToolDef, bool) {
	d, ok := r.defs[name]
	return d, ok
}

// remote.exec — the one v0.3.0 tool. The description + per-property descriptions
// are the agent's interface: they tell the model what the tool does, that it is
// powerful, and that it may block for human approval or be denied.
func remoteExec() ToolDef {
	const inputSchema = `{
  "type": "object",
  "required": ["resourceId", "command"],
  "additionalProperties": false,
  "properties": {
    "resourceId": {
      "type": "string",
      "description": "The governed SSH resource to run the command on — a resource id from the operator's registry."
    },
    "command": {
      "type": "string",
      "description": "A single non-interactive shell command to execute on the resource (e.g. \"uname -a\"). Its captured stdout is returned."
    }
  }
}`
	return ToolDef{
		Name: "remote.exec",
		Description: "Run a single non-interactive command on a governed remote (SSH) resource. " +
			"The call is policy-checked and may require human operator approval before it runs — " +
			"it can block until approved, or be denied — and every invocation is audited. Powerful: " +
			"it executes an arbitrary command on a real host.",
		InputSchema: json.RawMessage(inputSchema),
		Edge:        "agent-remote-session",
		Action:      "remote.exec",
		target:      remoteExecTarget,
	}
}

// remoteExecArgs is the typed argument set for remote.exec.
type remoteExecArgs struct {
	ResourceID string `json:"resourceId"`
	Command    string `json:"command"`
}

func remoteExecTarget(raw json.RawMessage) (string, error) {
	var a remoteExecArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid remote.exec arguments: %w", err)
	}
	if a.ResourceID == "" || a.Command == "" {
		return "", errors.New("remote.exec requires resourceId and command")
	}
	return "resource:" + a.ResourceID, nil
}

// ErrUnknownTool is returned for a tools/call against an unregistered tool.
var ErrUnknownTool = errors.New("unknown tool")

// Store is the slice of the invocations store the orchestrator needs (ADR-0057).
type Store interface {
	Create(ctx context.Context, in store.CreateInput) (store.Invocation, bool, error)
	CountInFlight(ctx context.Context, agentSubject string) (int, error)
	SetStatus(ctx context.Context, id, status, decision string) error
	Finish(ctx context.Context, id, status, outcome string) error
	RequiresApproval(ctx context.Context, resourceID string) (bool, error)
}

// PolicyDecider consults policy-service per invocation (ADR-0054/0057).
type PolicyDecider interface {
	Decide(ctx context.Context, invocationID, subject, edge, target, action string, requiresApproval bool) (decision string, err error)
}

// BrokerExecer runs the governed non-interactive exec on the broker (ADR-0058).
type BrokerExecer interface {
	Exec(ctx context.Context, invocationID, resourceID, command, subject string) (stdout string, exitCode int, truncated bool, err error)
}

// Service is the governed orchestrator: it satisfies mcp.Catalog, turning an
// agent's tools/call into a persisted, policy-decided, audited invocation that
// drives the broker exec (ADR-0057). List delegates to the pure Registry; Invoke
// is the spine.
type Service struct {
	registry    *Registry
	store       Store
	policy      PolicyDecider
	broker      BrokerExecer
	maxInFlight int
}

// New builds the governed tool service. maxInFlight ≤ 0 defaults to 4 (PERF-3).
func New(st Store, pol PolicyDecider, brk BrokerExecer, maxInFlight int) *Service {
	if maxInFlight <= 0 {
		maxInFlight = 4
	}
	return &Service{registry: NewRegistry(), store: st, policy: pol, broker: brk, maxInFlight: maxInFlight}
}

// List implements mcp.Catalog.
func (s *Service) List() []mcp.Tool { return s.registry.List() }

// Invoke runs the governed pipeline (ADR-0057): scope check (SEC-16) → per-agent
// concurrency cap (PERF-3) → create invocation (idempotency, PERF-2) → policy
// decide → allow: broker exec → tool.result; requires_approval: park (the
// block-and-wait + operator resolve is T-040). Fail-closed (SEC-19): any
// downstream failure marks the invocation failed and refuses egress.
//
// Governance refusals the agent should handle (scope/concurrency/approval/dup)
// return an MCP tool-error result (isError); protocol/infra failures return a Go
// error (a JSON-RPC error).
func (s *Service) Invoke(ctx context.Context, name string, args json.RawMessage) (mcp.ToolResult, error) {
	def, ok := s.registry.Lookup(name)
	if !ok {
		return mcp.ToolResult{}, fmt.Errorf("%w: %q", ErrUnknownTool, name)
	}
	capb, ok := mw.CapabilityFromContext(ctx)
	if !ok || capb.Subject == "" {
		return mcp.ToolResult{}, errors.New("no agent capability on the invocation")
	}
	agent := capb.Subject

	// Validate + resolve the governance target (resource:<id>) from the args.
	target, err := def.Target(args)
	if err != nil {
		return mcp.ToolResult{}, err
	}
	resourceID := strings.TrimPrefix(target, "resource:")

	// SEC-16: the invocation must stay within the agent token's scope.
	if capb.Scope.Resource != "" && capb.Scope.Resource != resourceID {
		return toolError("resource is outside the agent token's scope"), nil
	}

	// PERF-3: per-agent concurrency cap.
	inFlight, err := s.store.CountInFlight(ctx, agent)
	if err != nil {
		return mcp.ToolResult{}, fmt.Errorf("concurrency check: %w", err)
	}
	if inFlight >= s.maxInFlight {
		return toolError("too many concurrent invocations for this agent; retry shortly"), nil
	}

	// requires_approval from the resource label (ADR-0057).
	requiresApproval, err := s.store.RequiresApproval(ctx, resourceID)
	if err != nil {
		return toolError("unknown or unavailable resource"), nil
	}

	// Create the invocation (idempotency: PERF-2 — a retry never re-executes).
	inv, created, err := s.store.Create(ctx, store.CreateInput{
		AgentSubject: agent, ResourceID: resourceID, Tool: def.Name, IdempotencyKey: mcp.IdempotencyKey(ctx),
	})
	if err != nil {
		return mcp.ToolResult{}, fmt.Errorf("create invocation: %w", err)
	}
	if !created {
		return toolError(fmt.Sprintf("duplicate invocation (idempotent); original %s is %s", inv.ID, inv.Status)), nil
	}

	// Policy decision (fail-closed: an error → mark failed, refuse egress, SEC-19).
	decision, err := s.policy.Decide(ctx, inv.ID, agent, def.Edge, target, def.Action, requiresApproval)
	if err != nil {
		_ = s.store.Finish(ctx, inv.ID, "failed", "tool.result:error")
		return mcp.ToolResult{}, fmt.Errorf("policy decision unavailable: %w", err)
	}
	if decision != PolicyAllow {
		// Parked pending approval; the operator resolve + block-and-wait is T-040.
		_ = s.store.SetStatus(ctx, inv.ID, "pending_approval", decision)
		return toolError("approval required — resolve via the operator console (interactive approval lands in T-040)"), nil
	}

	// Allow → governed egress via the broker (the sole SSH egress, ADR-0058).
	_ = s.store.SetStatus(ctx, inv.ID, "running", decision)
	var execArgs struct {
		Command string `json:"command"`
	}
	_ = json.Unmarshal(args, &execArgs) // command is non-empty: def.Target already validated it
	stdout, exitCode, truncated, err := s.broker.Exec(ctx, inv.ID, resourceID, execArgs.Command, agent)
	if err != nil {
		_ = s.store.Finish(ctx, inv.ID, "failed", "tool.result:error")
		return mcp.ToolResult{}, fmt.Errorf("exec failed: %w", err)
	}
	isErr := exitCode != 0
	outcome := "tool.result"
	if isErr {
		outcome = "tool.result:error"
	}
	_ = s.store.Finish(ctx, inv.ID, "completed", outcome) // it ran; non-zero exit is a tool error, not a transport failure
	text := stdout
	if truncated {
		text += "\n[output truncated]"
	}
	return mcp.ToolResult{Content: []mcp.Content{{Type: "text", Text: text}}, IsError: isErr}, nil
}

// PolicyAllow is the decision string for an unflagged (or pre-approved) action.
const PolicyAllow = "allow"

// toolError is an MCP tool-level error result (a 200 JSON-RPC result with isError)
// for governance refusals the agent should handle.
func toolError(msg string) mcp.ToolResult {
	return mcp.ToolResult{Content: []mcp.Content{{Type: "text", Text: msg}}, IsError: true}
}
