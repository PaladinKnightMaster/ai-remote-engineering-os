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
	"slices"
	"strings"
	"time"

	mw "aireos.local/packages/go/middleware"
	"aireos.local/services/mcp-gateway/internal/mcp"
	"aireos.local/services/mcp-gateway/internal/policy"
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
	Operation   string // the agent-token scope verb gating this tool (SEC-16; e.g. "exec")

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
		Operation:   "exec",
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

// Store is the slice of the invocations store the orchestrator needs (ADR-0057/0059).
type Store interface {
	Create(ctx context.Context, in store.CreateInput) (store.Invocation, bool, error)
	CountInFlight(ctx context.Context, agentSubject string) (int, error)
	SetStatus(ctx context.Context, id, status, decision string) error
	Finish(ctx context.Context, id, status, outcome string) error
	RequiresApproval(ctx context.Context, resourceID string) (bool, error)
	Get(ctx context.Context, id string) (store.Invocation, error)
	ClaimForExec(ctx context.Context, id string) (store.Invocation, bool, error)
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
	registry       *Registry
	store          Store
	policy         PolicyDecider
	broker         BrokerExecer
	maxInFlight    int
	waitTimeout    time.Duration // agent block-and-wait deadline for a parked invocation (ADR-0059)
	runWaitTimeout time.Duration // operator Run-step block-and-wait deadline (T-055b)
	pollInterval   time.Duration // how often the wait re-reads the invocation row
}

// Config wires the governed tool service. Zero values get safe defaults.
type Config struct {
	Store          Store
	Policy         PolicyDecider
	Broker         BrokerExecer
	MaxInFlight    int           // per-agent concurrency cap (PERF-3); ≤ 0 → 4
	WaitTimeout    time.Duration // agent block-and-wait deadline; ≤ 0 → 125s (just over policy's 120s TTL)
	RunWaitTimeout time.Duration // operator Run-step block-and-wait; ≤ 0 → 10m (Run approval window ≫ 125s)
	PollInterval   time.Duration // approval poll cadence; ≤ 0 → 1s
}

// New builds the governed tool service.
func New(cfg Config) *Service {
	if cfg.MaxInFlight <= 0 {
		cfg.MaxInFlight = 4
	}
	if cfg.WaitTimeout <= 0 {
		cfg.WaitTimeout = 125 * time.Second
	}
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = time.Second
	}
	if cfg.RunWaitTimeout <= 0 {
		cfg.RunWaitTimeout = 10 * time.Minute
	}
	return &Service{
		registry: NewRegistry(), store: cfg.Store, policy: cfg.Policy, broker: cfg.Broker,
		maxInFlight: cfg.MaxInFlight, waitTimeout: cfg.WaitTimeout, pollInterval: cfg.PollInterval,
		runWaitTimeout: cfg.RunWaitTimeout,
	}
}

// List implements mcp.Catalog.
func (s *Service) List() []mcp.Tool { return s.registry.List() }

// Invoke is the AGENT-plane entry (mcp.Catalog): it authorizes the agent's tools/call
// (capability + SEC-16 token scope), resolves the governance target from the typed
// args, then hands the resolved step to the shared governed core (runGovernedStep).
// The agent-token scope check stays here — it is the agent plane's authorization and
// has no analog on the operator Run engine plane (T-055b), which reuses the core.
func (s *Service) Invoke(ctx context.Context, name string, args json.RawMessage) (mcp.ToolResult, error) {
	def, ok := s.registry.Lookup(name)
	if !ok {
		return mcp.ToolResult{}, fmt.Errorf("%w: %q", ErrUnknownTool, name)
	}
	capb, ok := mw.CapabilityFromContext(ctx)
	if !ok || capb.Subject == "" {
		return mcp.ToolResult{}, errors.New("no agent capability on the invocation")
	}

	// Validate + resolve the governance target (resource:<id>) from the args.
	target, err := def.Target(args)
	if err != nil {
		return mcp.ToolResult{}, err
	}
	resourceID := strings.TrimPrefix(target, "resource:")

	// SEC-16: the invocation must stay within the agent token's scope — both the
	// resource and (when the token declares operations) the operation. An empty
	// Operations set is unrestricted (back-compat for resource-only scoped tokens).
	if capb.Scope.Resource != "" && capb.Scope.Resource != resourceID {
		return toolError("resource is outside the agent token's scope"), nil
	}
	if len(capb.Scope.Operations) > 0 && !slices.Contains(capb.Scope.Operations, def.Operation) {
		return toolError("operation is outside the agent token's scope"), nil
	}

	// The command is persisted on the invocation (ADR-0059) so the operator approves
	// the exact payload and any waiter execs what was approved. def.Target already
	// validated it is present.
	var execArgs remoteExecArgs
	_ = json.Unmarshal(args, &execArgs)

	// The idempotency key is read HERE (the agent's Idempotency-Key header, via ctx)
	// and passed in — the core never reads it from ctx, so it equally serves the Run
	// engine's deterministic per-step keys (T-055b).
	// The agent plane does not surface the invocation id in its tools/call result.
	_, res, err := s.runGovernedStep(ctx, stepRequest{
		Subject:        capb.Subject,
		Tool:           def.Name,
		ResourceID:     resourceID,
		Command:        execArgs.Command,
		Edge:           def.Edge,
		Action:         def.Action,
		Target:         target,
		BracketEdge:    "agent-tool", // the agent plane's bracket edge (RUN-5 default)
		IdempotencyKey: mcp.IdempotencyKey(ctx),
		WaitTimeout:    s.waitTimeout,
	})
	return res, err
}

// remoteExecTool is the wedge tool the operator Run engine drives (ADR-0061): a Run
// step is a sequence of this EXISTING tool, not a new one.
const remoteExecTool = "remote.exec"

// RunStep drives ONE operator-initiated governed step (ADR-0061 §2, T-055b) — the
// operator Run engine (services/api) calls this via the gateway's subject-pinned
// internal /v1/steps route. It reuses the SAME governed core as the agent path
// (runGovernedStep); the only differences are the initiator (the launching operator
// = the audit actor + policy subject), the bracket edge (human-agent, not agent-tool),
// the deterministic idempotency key, and the longer Run-approval wait window. The
// egress path is identical — the gateway still drives the broker with its own
// identity, so the v0.4.0 subject-pins (PIN-1/PIN-2) are untouched (SEC-25).
func (s *Service) RunStep(ctx context.Context, req mcp.StepRequest) (string, mcp.ToolResult, error) {
	def, ok := s.registry.Lookup(remoteExecTool)
	if !ok {
		return "", mcp.ToolResult{}, fmt.Errorf("%w: %q", ErrUnknownTool, remoteExecTool)
	}
	// Reuse the tool's own arg validation + target resolution (single-sources the
	// "resource:<id>" format and the non-empty resource/command checks).
	args, err := json.Marshal(remoteExecArgs{ResourceID: req.ResourceID, Command: req.Command})
	if err != nil {
		return "", mcp.ToolResult{}, fmt.Errorf("marshal step args: %w", err)
	}
	target, err := def.Target(args)
	if err != nil {
		return "", mcp.ToolResult{}, err
	}
	return s.runGovernedStep(ctx, stepRequest{
		Subject:        req.Subject,
		Tool:           def.Name,
		ResourceID:     req.ResourceID,
		Command:        req.Command,
		Edge:           def.Edge,
		Action:         def.Action,
		Target:         target,
		BracketEdge:    "human-agent", // operator-initiated step (RUN-5; ADR-0061 §4)
		IdempotencyKey: req.IdempotencyKey,
		WaitTimeout:    s.runWaitTimeout,
	})
}

var _ mcp.StepRunner = (*Service)(nil)

// stepRequest is the resolved input to the governed core — a single governed step,
// independent of which plane initiated it. The agent path (Invoke) builds it from the
// agent capability + ToolDef + args; the operator Run engine builds it from the
// launching operator + a plan step (T-055b). Keeping the core plane-agnostic is the
// seam that lets the Run engine reuse the whole governed pipeline without the
// agent-token machinery.
type stepRequest struct {
	Subject        string        // the initiator (agent now; operator at T-055b)
	Tool           string        // the tool name (def.Name)
	ResourceID     string        // target resource
	Command        string        // the command to persist + execute
	Edge           string        // capability edge for policy.Decide (def.Edge)
	Action         string        // audited action for policy.Decide (def.Action)
	Target         string        // "resource:<id>" for policy.Decide
	BracketEdge    string        // tool.invoked/result audit edge (agent-tool | human-agent, RUN-5)
	IdempotencyKey string        // resolved by the CALLER (never read from ctx here)
	WaitTimeout    time.Duration // block-and-wait deadline for a parked step
}

// runGovernedStep is the shared, plane-agnostic governed pipeline (ADR-0057/0059):
// per-initiator concurrency cap (PERF-3) → create the invocation with the persisted
// command (idempotency, PERF-2) → policy decide → allow: broker exec → tool.result;
// requires_approval: park and BLOCK until the operator resolves it (block-and-wait)
// → grant: exec; deny/timeout: typed error. Fail-closed (SEC-19): any downstream
// failure marks the invocation failed and refuses egress.
//
// Governance refusals the caller should handle (concurrency/denied/timeout/dup)
// return an MCP tool-error result (isError); protocol/infra failures return a Go
// error. The SEC-16 agent-token scope check is NOT here — it is the agent wrapper's
// concern (Invoke); this core serves both the agent and the operator Run engine.
// It returns the invocation id (empty for refusals that never created one — a
// concurrency cap or unknown resource) so the operator Run engine can record the
// run→invocation correlation (AUD-15, T-058a); the agent path discards it.
func (s *Service) runGovernedStep(ctx context.Context, req stepRequest) (string, mcp.ToolResult, error) {
	// PERF-3: per-initiator concurrency cap.
	inFlight, err := s.store.CountInFlight(ctx, req.Subject)
	if err != nil {
		return "", mcp.ToolResult{}, fmt.Errorf("concurrency check: %w", err)
	}
	if inFlight >= s.maxInFlight {
		return "", toolError("too many concurrent invocations for this agent; retry shortly"), nil
	}

	// requires_approval from the resource label (ADR-0057).
	requiresApproval, err := s.store.RequiresApproval(ctx, req.ResourceID)
	if err != nil {
		return "", toolError("unknown or unavailable resource"), nil
	}

	// Create the invocation (idempotency: PERF-2).
	inv, created, err := s.store.Create(ctx, store.CreateInput{
		AgentSubject: req.Subject, ResourceID: req.ResourceID, Tool: req.Tool,
		Command: req.Command, IdempotencyKey: req.IdempotencyKey, Edge: req.BracketEdge,
	})
	if err != nil {
		return "", mcp.ToolResult{}, fmt.Errorf("create invocation: %w", err)
	}

	if !created {
		// Idempotent hit (PERF-2). A terminal original cannot be replayed (the stdout
		// is not stored, ADR-0058); a still-live one RE-ATTACHES to the wait so a
		// dropped long-poll that the caller retries resumes rather than erroring.
		if isTerminal(inv.Status) {
			return inv.ID, toolError(fmt.Sprintf("duplicate invocation (idempotent); original %s is %s", inv.ID, inv.Status)), nil
		}
		res, werr := s.waitForApproval(ctx, inv.ID, req.Subject, req.WaitTimeout)
		return inv.ID, res, werr
	}

	// Policy decision (fail-closed: an error → mark failed, refuse egress, SEC-19).
	decision, err := s.policy.Decide(ctx, inv.ID, req.Subject, req.Edge, req.Target, req.Action, requiresApproval)
	if err != nil {
		_ = s.store.Finish(ctx, inv.ID, "failed", "tool.result:error")
		return inv.ID, mcp.ToolResult{}, fmt.Errorf("policy decision unavailable: %w", err)
	}
	if decision == PolicyAllow {
		_ = s.store.SetStatus(ctx, inv.ID, "running", decision)
		res, eerr := s.execAndFinish(ctx, inv.ID, req.ResourceID, req.Command, req.Subject)
		return inv.ID, res, eerr
	}

	// requires_approval → park, then block until the operator resolves it (T-040).
	_ = s.store.SetStatus(ctx, inv.ID, "pending_approval", decision)
	res, werr := s.waitForApproval(ctx, inv.ID, req.Subject, req.WaitTimeout)
	return inv.ID, res, werr
}

// waitForApproval blocks the agent's tools/call on a parked invocation until the
// operator resolves it (ADR-0057/0059 block-and-wait): grant (decision=allow) →
// atomically claim + exec; deny/failed → typed error; the gateway's own deadline →
// finalize failed (policy's TTL timeout runs in parallel). An agent disconnect (ctx)
// leaves the row parked for the reaper or an idempotent re-attach. It polls the row —
// a short query per tick, never a held transaction.
func (s *Service) waitForApproval(ctx context.Context, id, agent string, timeout time.Duration) (mcp.ToolResult, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(s.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return mcp.ToolResult{}, ctx.Err()
		case <-deadline.C:
			// Finalize on a detached context so the agent's cancellation can't strand
			// the row; the reaper is the second-line backstop (ADR-0059).
			fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			_ = s.store.Finish(fctx, id, "failed", "tool.result:error")
			cancel()
			return toolError("approval timed out before an operator responded"), nil
		case <-ticker.C:
			inv, err := s.store.Get(ctx, id)
			if err != nil {
				return mcp.ToolResult{}, fmt.Errorf("poll invocation: %w", err)
			}
			switch inv.Status {
			case "denied":
				return toolError("the operator denied this invocation"), nil
			case "failed":
				return toolError("invocation failed before it could run"), nil
			case "completed":
				return toolError("invocation already completed by a concurrent call"), nil
			case "pending_approval":
				if inv.Decision == PolicyAllow {
					claimed, won, cerr := s.store.ClaimForExec(ctx, id)
					if cerr != nil {
						return mcp.ToolResult{}, fmt.Errorf("claim invocation: %w", cerr)
					}
					if won {
						return s.execAndFinish(ctx, id, claimed.ResourceID, claimed.Command, agent)
					}
				}
			}
		}
	}
}

// execAndFinish runs the governed command on the broker (the sole SSH egress,
// ADR-0058) and records the terminal outcome. The caller has already moved the
// invocation to running (allow-now via SetStatus, post-approval via ClaimForExec).
func (s *Service) execAndFinish(ctx context.Context, id, resourceID, command, agent string) (mcp.ToolResult, error) {
	stdout, exitCode, truncated, err := s.broker.Exec(ctx, id, resourceID, command, agent)
	if err != nil {
		_ = s.store.Finish(ctx, id, "failed", "tool.result:error")
		return mcp.ToolResult{}, fmt.Errorf("exec failed: %w", err)
	}
	isErr := exitCode != 0
	outcome := "tool.result"
	if isErr {
		outcome = "tool.result:error"
	}
	_ = s.store.Finish(ctx, id, "completed", outcome) // it ran; non-zero exit is a tool error, not a transport failure
	text := stdout
	if truncated {
		text += "\n[output truncated]"
	}
	return mcp.ToolResult{Content: []mcp.Content{{Type: "text", Text: text}}, IsError: isErr}, nil
}

// isTerminal reports whether an invocation has reached a final status.
func isTerminal(status string) bool {
	switch status {
	case "completed", "denied", "failed":
		return true
	}
	return false
}

// PolicyAllow is the decision string for an unflagged (or pre-approved) action.
const PolicyAllow = "allow"

// toolError is an MCP tool-level error result (a 200 JSON-RPC result with isError)
// for governance refusals the agent should handle.
func toolError(msg string) mcp.ToolResult {
	return mcp.ToolResult{Content: []mcp.Content{{Type: "text", Text: msg}}, IsError: true}
}

// --- operator plane: the invocation resolver (ADR-0057 §4 / ADR-0059) ------------
// The agent-facing Invoke (above) parks an invocation; the operator resolves it
// here, off the agent plane. The gateway OWNS the reconcile (api is a thin proxy,
// T-040b): it consults policy-service (the authority), then grants (decision→allow,
// the waiter claims + execs) or denies (terminal) — and reconciles a policy timeout.

// ResolverStore is the slice of the store the resolver needs.
type ResolverStore interface {
	Get(ctx context.Context, id string) (store.Invocation, error)
	Grant(ctx context.Context, id string) (bool, error)
	FinalizeIfPending(ctx context.Context, id, status, outcome string) (bool, error)
	ListPending(ctx context.Context) ([]store.Invocation, error)
}

// PolicyResolver records the operator's decision with policy-service (ADR-0054
// resolve). policy.ErrNotPending signals the approval already lapsed (timeout).
type PolicyResolver interface {
	Resolve(ctx context.Context, invocationID, decision, resolvedBy string) error
}

// Resolver implements mcp.ResolverPort: the operator pending-list + resolve surface.
type Resolver struct {
	store  ResolverStore
	policy PolicyResolver
}

// NewResolver builds the operator-plane resolver.
func NewResolver(st ResolverStore, pol PolicyResolver) *Resolver {
	return &Resolver{store: st, policy: pol}
}

// ListPending returns the parked invocations for the operator console, including the
// persisted command so the approval is informed (ADR-0059).
func (r *Resolver) ListPending(ctx context.Context) ([]mcp.InvocationView, error) {
	invs, err := r.store.ListPending(ctx)
	if err != nil {
		return nil, err
	}
	views := make([]mcp.InvocationView, 0, len(invs))
	for _, inv := range invs {
		views = append(views, mcp.InvocationView{
			ID: inv.ID, Agent: inv.AgentSubject, ResourceID: inv.ResourceID,
			Tool: inv.Tool, Command: inv.Command, Status: inv.Status,
		})
	}
	return views, nil
}

// Resolve applies the operator's decision ("granted" | "denied"). It pre-checks the
// invocation is still parked (404/409), records the decision with policy (the
// authority), then transitions the row: grant → decision=allow (the waiter claims +
// execs); deny → terminal. A policy 409 (the fail-closed sweeper auto-denied on
// timeout before the operator acted) reconciles the row to failed and reports
// ErrApprovalNotPending. A policy outage is returned (the row stays parked).
func (r *Resolver) Resolve(ctx context.Context, id, decision, resolvedBy string) error {
	inv, err := r.store.Get(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return mcp.ErrInvocationNotFound
	}
	if err != nil {
		return err
	}
	if inv.Status != "pending_approval" {
		return mcp.ErrApprovalNotPending
	}

	if perr := r.policy.Resolve(ctx, id, decision, resolvedBy); perr != nil {
		if errors.Is(perr, policy.ErrNotPending) {
			_, _ = r.store.FinalizeIfPending(ctx, id, "failed", "tool.result:error")
			return mcp.ErrApprovalNotPending
		}
		return perr
	}

	if decision == mcp.DecisionGranted {
		ok, err := r.store.Grant(ctx, id)
		if err != nil {
			return err
		}
		if !ok {
			return mcp.ErrApprovalNotPending
		}
		return nil
	}
	// denied
	ok, err := r.store.FinalizeIfPending(ctx, id, "denied", "tool.result:error")
	if err != nil {
		return err
	}
	if !ok {
		return mcp.ErrApprovalNotPending
	}
	return nil
}

var _ mcp.ResolverPort = (*Resolver)(nil)
