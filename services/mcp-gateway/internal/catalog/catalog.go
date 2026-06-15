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

	"aireos.local/services/mcp-gateway/internal/mcp"
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

// ErrNotImplemented is returned by the T-037 stub Invoke for a known tool whose
// governed execution is not yet wired (lands in T-039).
var ErrNotImplemented = errors.New("tool is not yet executable (governed execution wired in T-039)")

// ErrUnknownTool is returned for a tools/call against an unregistered tool.
var ErrUnknownTool = errors.New("unknown tool")

// Service satisfies mcp.Catalog for the wire handler. At v0.3.0 (T-037) List is
// real and Invoke is a stub: it confirms the tool exists and its arguments resolve,
// then returns ErrNotImplemented. T-039 replaces this with the governed orchestrator
// (policy → approval → connector lease → broker exec → audit), which holds the
// Registry but keeps it pure.
type Service struct {
	registry *Registry
}

// New builds the gateway tool service over the v0.3.0 registry.
func New() *Service { return &Service{registry: NewRegistry()} }

// List implements mcp.Catalog.
func (s *Service) List() []mcp.Tool { return s.registry.List() }

// Invoke implements mcp.Catalog. The ctx (which carries the agent capability) is
// unused until T-039's governed path; arguments are validated now so a malformed
// call fails clearly even before execution exists.
func (s *Service) Invoke(_ context.Context, name string, args json.RawMessage) (mcp.ToolResult, error) {
	def, ok := s.registry.Lookup(name)
	if !ok {
		return mcp.ToolResult{}, fmt.Errorf("%w: %q", ErrUnknownTool, name)
	}
	if _, err := def.Target(args); err != nil {
		return mcp.ToolResult{}, err
	}
	return mcp.ToolResult{}, ErrNotImplemented
}
