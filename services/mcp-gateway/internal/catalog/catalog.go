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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	mw "aireos.local/packages/go/middleware"
	"aireos.local/services/mcp-gateway/internal/brokerfile"
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

	target   func(args json.RawMessage) (string, error)
	describe func(args json.RawMessage) (string, error)
	content  func(args json.RawMessage) ([]byte, error) // T-141: only remote.file.write carries a payload; nil for every other tool
}

// Tool returns the MCP wire view; the governance metadata stays internal.
func (d ToolDef) Tool() mcp.Tool {
	return mcp.Tool{Name: d.Name, Description: d.Description, InputSchema: d.InputSchema}
}

// Target resolves the audited target ("resource:<id>") from the invocation's raw
// arguments, validating them in the process.
func (d ToolDef) Target(args json.RawMessage) (string, error) { return d.target(args) }

// Describe renders the human-readable descriptor persisted as the invocation's Command
// (ADR-0059) from the invocation's raw arguments (ADR-0071 §D3). remote.exec's
// descriptor is the command verbatim; a future operation family (T-140b) whose payload
// is not itself reviewable (e.g. file bytes) renders a summary instead — informed
// approval becomes "the operator approves the intent and the digest, not the bytes."
func (d ToolDef) Describe(args json.RawMessage) (string, error) { return d.describe(args) }

// Content extracts a tool's payload bytes from its raw arguments (T-141, ADR-0071
// §D3/option-c): the ONLY caller-supplied field that must never be persisted. nil for
// every tool that carries no separate payload (remote.exec's payload IS its
// descriptor; remote.file.read has none).
func (d ToolDef) Content(args json.RawMessage) ([]byte, error) {
	if d.content == nil {
		return nil, nil
	}
	return d.content(args)
}

// Registry is the immutable set of tool definitions. No execution, no
// downstream-service dependency — the durable artifact the orchestrator reads.
type Registry struct {
	defs map[string]ToolDef
	list []mcp.Tool // stable order for tools/list
}

// NewRegistry builds the registry. v0.9 (ADR-0065, T-090) adds three control-plane
// tools on the run.launch pattern (workstream.poll / plan.submit / memory.read); v0.10
// (ADR-0066 §4, T-099) adds a sixth, context.search, on the SAME memory.read forward
// pattern; v0.14.0 (ADR-0071) adds remote.file.read (T-140b), remote.file.write
// (T-141) — the only tool besides remote.exec that mutates a target host (SEC-47) —
// then remote.file.list (T-142), the NINTH tool and the last of the file family:
// listing shares file.read's Operation verb rather than minting a new one (§D1).
func NewRegistry() *Registry {
	r := &Registry{defs: map[string]ToolDef{}}
	for _, d := range []ToolDef{remoteExec(), remoteFileRead(), remoteFileWrite(), remoteFileList(), runLaunch(), workstreamPoll(), planSubmit(), memoryRead(), contextSearch()} {
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
		describe:    remoteExecDescribe,
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

// remoteExecDescribe renders remote.exec's ADR-0071 §D3 descriptor: the command
// verbatim, byte-identical to what Invoke persisted before this ticket. Target already
// guarantees a present, non-empty command by the time Invoke reaches Describe, so this
// validation is defensive — it matters for a caller that invokes Describe directly
// (or a future tool whose Target and Describe diverge), not for today's dispatch order.
func remoteExecDescribe(raw json.RawMessage) (string, error) {
	var a remoteExecArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid remote.exec arguments: %w", err)
	}
	if a.Command == "" {
		return "", errors.New("remote.exec requires a command")
	}
	return a.Command, nil
}

// remoteFileReadTool is the T-140b file-family tool name (ADR-0071 §D1) — the
// first tool on the SAME agent-remote-session edge as remote.exec (§D1: same
// trust boundary, same credential, same host). Only the read verb ships this
// ticket; write/list are T-141/T-142.
const remoteFileReadTool = "remote.file.read"

// remoteFileReadDescriptorPrefix anchors the ADR-0071 §D3 descriptor. The
// invocation persists exactly ONE field for the approval payload (Command,
// F3-encrypted at rest) — there is no separate args column, and adding one is
// a migration this ticket may not make (C4). So the descriptor doubles as the
// operational parameter: describe renders "remote.file.read <path>" and
// performAndFinish strips this exact, self-controlled prefix back off to
// recover path when it dispatches post-approval. This is not a parse of
// untrusted input — the gateway is stripping a literal it itself wrote.
const remoteFileReadDescriptorPrefix = remoteFileReadTool + " "

// remote.file.read — read one file from a governed remote resource (ADR-0071
// §D1/§D4). Same edge as remote.exec: reading a file over the leased SSH
// credential crosses the identical trust boundary as running a command over it.
func remoteFileRead() ToolDef {
	const inputSchema = `{
  "type": "object",
  "required": ["resourceId", "path"],
  "additionalProperties": false,
  "properties": {
    "resourceId": {
      "type": "string",
      "description": "The governed SSH resource to read the file from — a resource id from the operator's registry."
    },
    "path": {
      "type": "string",
      "description": "The absolute path of the file to read on the resource."
    }
  }
}`
	return ToolDef{
		Name: remoteFileReadTool,
		Description: "Read one file from a governed remote (SSH) resource. " +
			"The call is policy-checked and may require human operator approval before it runs — " +
			"it can block until approved, or be denied — and every invocation is audited. Returns the " +
			"file's content and its sha256 digest. Bounded by a maximum transfer size (default 1 MiB); " +
			"a file over the cap is refused rather than truncated.",
		InputSchema: json.RawMessage(inputSchema),
		Edge:        "agent-remote-session",
		Action:      remoteFileReadTool,
		Operation:   "file.read",
		target:      remoteFileReadTarget,
		describe:    remoteFileReadDescribe,
	}
}

// remoteFileReadArgs is the typed argument set for remote.file.read.
type remoteFileReadArgs struct {
	ResourceID string `json:"resourceId"`
	Path       string `json:"path"`
}

func remoteFileReadTarget(raw json.RawMessage) (string, error) {
	var a remoteFileReadArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid remote.file.read arguments: %w", err)
	}
	if a.ResourceID == "" || a.Path == "" {
		return "", errors.New("remote.file.read requires resourceId and path")
	}
	return "resource:" + a.ResourceID, nil
}

// remoteFileReadDescribe renders remote.file.read's ADR-0071 §D3 descriptor:
// the tool name plus the path, e.g. "remote.file.read /etc/nginx/nginx.conf" —
// what the operator sees in the approval prompt, and (via
// remoteFileReadDescriptorPrefix) what performAndFinish recovers the path
// from post-approval.
func remoteFileReadDescribe(raw json.RawMessage) (string, error) {
	var a remoteFileReadArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid remote.file.read arguments: %w", err)
	}
	if a.Path == "" {
		return "", errors.New("remote.file.read requires a path")
	}
	return remoteFileReadDescriptorPrefix + a.Path, nil
}

// parseFileReadDescriptor recovers the path from a persisted remote.file.read
// descriptor (T-141 Part 4, ADR-0071 §D3/§14c): strings.CutPrefix, not TrimPrefix —
// TrimPrefix silently returns the whole string when the prefix is absent, which would
// reinterpret a malformed descriptor as a path rather than refuse it. This is not a
// parse of untrusted caller input (the gateway is stripping a literal it itself wrote
// via remoteFileReadDescribe), but it is a governance path and this product's rule is
// fail loud, not fail silent — so a descriptor missing the prefix, or one that strips
// down to an empty path, is refused rather than passed through.
func parseFileReadDescriptor(command string) (string, error) {
	path, ok := strings.CutPrefix(command, remoteFileReadDescriptorPrefix)
	if !ok {
		return "", fmt.Errorf("descriptor missing the %q prefix", remoteFileReadDescriptorPrefix)
	}
	if path == "" {
		return "", errors.New("descriptor carries an empty path")
	}
	return path, nil
}

// remoteFileWriteTool is the T-141 file-family mutator (ADR-0071 §D1) — the ONLY
// member of the family that mutates a target host (SEC-47). Same edge as
// remote.file.read and remote.exec (§D1: same trust boundary).
const remoteFileWriteTool = "remote.file.write"

// remoteFileWriteDescriptorPrefix anchors the ADR-0071 §D3 descriptor. Per the T-141
// consult (§14c, option (c)): the invocation's persisted Command carries path, size,
// digest and mode — NEVER the bytes, which C4 forbids a column for and D3 forbids an
// operator from approving blind anyway. The bytes themselves are held in memory for
// the life of the blocking call (never persisted) and threaded straight to
// performAndFinish as a parameter.
const remoteFileWriteDescriptorPrefix = remoteFileWriteTool + " "

// remote.file.write — write bytes to one path on a governed remote resource
// (ADR-0071 §D1/§D3/§D4). Same edge as remote.exec/remote.file.read: writing over the
// leased SSH credential crosses the identical trust boundary.
func remoteFileWrite() ToolDef {
	const inputSchema = `{
  "type": "object",
  "required": ["resourceId", "path", "content"],
  "additionalProperties": false,
  "properties": {
    "resourceId": {
      "type": "string",
      "description": "The governed SSH resource to write the file to — a resource id from the operator's registry."
    },
    "path": {
      "type": "string",
      "description": "The absolute path to write on the resource."
    },
    "content": {
      "type": "string",
      "description": "Base64-encoded bytes to write to path. Bounded by a maximum transfer size (default 1 MiB)."
    },
    "mode": {
      "type": "integer",
      "description": "Optional POSIX permission bits for the file (e.g. 420 for 0644, decimal). Defaults to 0644."
    }
  }
}`
	return ToolDef{
		Name: remoteFileWriteTool,
		Description: "Write bytes to one path on a governed remote (SSH) resource. THIS MODIFIES A FILE " +
			"ON A REAL HOST — it can overwrite or create the target file. The call is policy-checked and " +
			"may require human operator approval before it runs — it can block until approved, or be " +
			"denied — and every invocation is audited. The operator approves the path, size and content " +
			"digest, not the raw bytes. Bounded by a maximum transfer size (default 1 MiB).",
		InputSchema: json.RawMessage(inputSchema),
		Edge:        "agent-remote-session",
		Action:      remoteFileWriteTool,
		Operation:   "file.write",
		target:      remoteFileWriteTarget,
		describe:    remoteFileWriteDescribe,
		content:     remoteFileWriteContent,
	}
}

// remoteFileWriteArgs is the typed argument set for remote.file.write. Content is
// []byte so encoding/json base64-decodes it for us on Unmarshal (and base64-encodes
// on Marshal) — the same convention the broker's own fileRequest uses (T-139b).
type remoteFileWriteArgs struct {
	ResourceID string  `json:"resourceId"`
	Path       string  `json:"path"`
	Content    []byte  `json:"content"`
	Mode       *uint32 `json:"mode,omitempty"`
}

// remoteFileWriteMode resolves the POSIX mode to persist/write: the caller's value if
// given, else the documented default 0644.
func remoteFileWriteMode(a remoteFileWriteArgs) uint32 {
	if a.Mode != nil {
		return *a.Mode
	}
	return 0o644
}

func remoteFileWriteTarget(raw json.RawMessage) (string, error) {
	var a remoteFileWriteArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid remote.file.write arguments: %w", err)
	}
	if a.ResourceID == "" || a.Path == "" {
		return "", errors.New("remote.file.write requires resourceId and path")
	}
	return "resource:" + a.ResourceID, nil
}

// remoteFileWriteDescribe renders remote.file.write's ADR-0071 §D3 descriptor:
// path, size, sha256 digest and mode — never the content. This is what the operator
// approves (the intent and the digest, not the bytes, per D3's explicit narrowing)
// and what performAndFinish parses back post-approval to learn WHERE and WITH WHAT
// PERMISSIONS to write, and against WHICH digest to verify the bytes it is handed
// (SEC-49).
func remoteFileWriteDescribe(raw json.RawMessage) (string, error) {
	var a remoteFileWriteArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid remote.file.write arguments: %w", err)
	}
	if a.Path == "" {
		return "", errors.New("remote.file.write requires a path")
	}
	sum := sha256.Sum256(a.Content)
	digest := hex.EncodeToString(sum[:])
	return fmt.Sprintf("%s%s (%d bytes, sha256:%s, mode %04o)",
		remoteFileWriteDescriptorPrefix, a.Path, len(a.Content), digest, remoteFileWriteMode(a)), nil
}

// remoteFileWriteContent extracts the payload bytes (ADR-0071 §14c option (c)): this is
// the ONLY value in the whole family that must never reach the invocations store — it
// is read here, from the caller's raw arguments, and handed straight to the governed
// core as a stepRequest field, never persisted.
func remoteFileWriteContent(raw json.RawMessage) ([]byte, error) {
	var a remoteFileWriteArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, fmt.Errorf("invalid remote.file.write arguments: %w", err)
	}
	return a.Content, nil
}

// parseFileWriteDescriptor recovers path, digest and mode from a persisted
// remote.file.write descriptor (T-141, ADR-0071 §D3/§14c). Fail-closed throughout
// (SEC-49): CutPrefix (not TrimPrefix, same reasoning as parseFileReadDescriptor)
// plus an explicit refusal at every stage — a descriptor that doesn't parse cleanly
// must never be treated as "no digest to check," it must refuse outright. This is the
// gateway parsing a literal it itself wrote via remoteFileWriteDescribe, not untrusted
// external input, but a governance-critical field silently defaulting on a parse miss
// is exactly the failure class SEC-49 exists to close.
func parseFileWriteDescriptor(command string) (path, digest string, mode uint32, err error) {
	rest, ok := strings.CutPrefix(command, remoteFileWriteDescriptorPrefix)
	if !ok {
		return "", "", 0, fmt.Errorf("descriptor missing the %q prefix", remoteFileWriteDescriptorPrefix)
	}
	idx := strings.LastIndex(rest, " (")
	if idx < 0 || !strings.HasSuffix(rest, ")") {
		return "", "", 0, errors.New("descriptor missing its (size/digest/mode) metadata block")
	}
	path = rest[:idx]
	if path == "" {
		return "", "", 0, errors.New("descriptor carries an empty path")
	}
	meta := rest[idx+2 : len(rest)-1] // strip the leading " (" and trailing ")"
	parts := strings.Split(meta, ", ")
	if len(parts) != 3 {
		return "", "", 0, fmt.Errorf("descriptor metadata has %d fields, want 3 (size, digest, mode)", len(parts))
	}
	digest, ok = strings.CutPrefix(parts[1], "sha256:")
	if !ok || len(digest) != 64 {
		return "", "", 0, errors.New("descriptor carries a malformed or absent sha256 digest")
	}
	modeField, ok := strings.CutPrefix(parts[2], "mode ")
	if !ok {
		return "", "", 0, errors.New("descriptor carries a malformed mode field")
	}
	m, merr := strconv.ParseUint(modeField, 8, 32)
	if merr != nil {
		return "", "", 0, fmt.Errorf("descriptor mode field does not parse as octal: %w", merr)
	}
	return path, digest, uint32(m), nil
}

// remoteFileListTool is the T-142 file-family tool name (ADR-0071 §D1) — the
// THIRD and last member of the family, same edge as remote.exec/remote.file.read/
// remote.file.write (§D1: same trust boundary). Listing is a read of the
// filesystem, so it shares remote.file.read's "file.read" Operation verb
// rather than minting a third checkbox no threat model distinguishes (§D1) —
// a token scoped ["file.read"] can both read and list, but still cannot write
// (SEC-50).
const remoteFileListTool = "remote.file.list"

// remoteFileListDescriptorPrefix anchors the ADR-0071 §D3 descriptor, same
// convention as remote.file.read's prefix: the invocation persists exactly ONE
// field (Command) for the approval payload, so the descriptor doubles as the
// operational parameter. The per-tool prefix is load-bearing (§14e): it is one
// of two independent controls against tool confusion, so it is never collapsed
// into a shared format with remote.file.read's or remote.file.write's prefix.
const remoteFileListDescriptorPrefix = remoteFileListTool + " "

// remote.file.list — list one directory's immediate entries on a governed
// remote (SSH) resource (ADR-0071 §D1/§D4/§D5). Same edge as the rest of the
// file family: listing over the leased SSH credential crosses the identical
// trust boundary as reading or writing a file over it.
func remoteFileList() ToolDef {
	const inputSchema = `{
  "type": "object",
  "required": ["resourceId", "path"],
  "additionalProperties": false,
  "properties": {
    "resourceId": {
      "type": "string",
      "description": "The governed SSH resource to list the directory on — a resource id from the operator's registry."
    },
    "path": {
      "type": "string",
      "description": "The absolute path of the directory to list on the resource."
    }
  }
}`
	return ToolDef{
		Name: remoteFileListTool,
		Description: "List one directory's immediate entries on a governed remote (SSH) resource. " +
			"The call is policy-checked and may require human operator approval before it runs — " +
			"it can block until approved, or be denied — and every invocation is audited. Returns each " +
			"entry's name, size, permission mode, modification time and whether it is a directory. " +
			"Bounded by a maximum entry count (default 1000); a directory over the cap is refused " +
			"rather than truncated.",
		InputSchema: json.RawMessage(inputSchema),
		Edge:        "agent-remote-session",
		Action:      remoteFileListTool,
		Operation:   "file.read", // ADR-0071 §D1: listing is a read, deliberately no separate verb
		target:      remoteFileListTarget,
		describe:    remoteFileListDescribe,
	}
}

// remoteFileListArgs is the typed argument set for remote.file.list.
type remoteFileListArgs struct {
	ResourceID string `json:"resourceId"`
	Path       string `json:"path"`
}

func remoteFileListTarget(raw json.RawMessage) (string, error) {
	var a remoteFileListArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid remote.file.list arguments: %w", err)
	}
	if a.ResourceID == "" || a.Path == "" {
		return "", errors.New("remote.file.list requires resourceId and path")
	}
	return "resource:" + a.ResourceID, nil
}

// remoteFileListDescribe renders remote.file.list's ADR-0071 §D3 descriptor:
// the tool name plus the path, e.g. "remote.file.list /etc/nginx" — what the
// operator sees in the approval prompt, and (via remoteFileListDescriptorPrefix)
// what performAndFinish recovers the path from post-approval.
func remoteFileListDescribe(raw json.RawMessage) (string, error) {
	var a remoteFileListArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return "", fmt.Errorf("invalid remote.file.list arguments: %w", err)
	}
	if a.Path == "" {
		return "", errors.New("remote.file.list requires a path")
	}
	return remoteFileListDescriptorPrefix + a.Path, nil
}

// parseFileListDescriptor recovers the path from a persisted remote.file.list
// descriptor (ADR-0071 §D3, mirrors parseFileReadDescriptor exactly): CutPrefix,
// not TrimPrefix — TrimPrefix silently returns the whole string when the prefix
// is absent, which would reinterpret a malformed descriptor as a path rather
// than refuse it. Fail loud, not fail silent: a descriptor missing the prefix,
// or one that strips down to an empty path, is refused rather than passed
// through.
func parseFileListDescriptor(command string) (string, error) {
	path, ok := strings.CutPrefix(command, remoteFileListDescriptorPrefix)
	if !ok {
		return "", fmt.Errorf("descriptor missing the %q prefix", remoteFileListDescriptorPrefix)
	}
	if path == "" {
		return "", errors.New("descriptor carries an empty path")
	}
	return path, nil
}

// runLaunchTool is the v0.6 control-plane tool name (ADR-0062, T-065b).
const runLaunchTool = "run.launch"

// run.launch — an agent launches a governed multi-step Run bounded by a delegation grant an
// operator issued to it (ADR-0062, T-065b). Unlike remote.exec it resolves NO target and
// mints NO egress: the gateway forwards the plan to services/api, which validates it against
// the grant and creates the Run. So it carries no Edge/Action/Operation and no target
// resolver; Invoke dispatches it BEFORE the remote.exec target + SEC-16 path.
func runLaunch() ToolDef {
	const inputSchema = `{
  "type": "object",
  "required": ["grantId", "steps"],
  "additionalProperties": false,
  "properties": {
    "grantId": {
      "type": "string",
      "description": "The id of a delegation grant an operator issued to you (bounds the resources, verbs, and step count you may launch)."
    },
    "steps": {
      "type": "array",
      "minItems": 1,
      "description": "The ordered plan: each step runs one non-interactive command on a governed resource within the grant.",
      "items": {
        "type": "object",
        "required": ["resourceId", "command"],
        "additionalProperties": false,
        "properties": {
          "resourceId": { "type": "string" },
          "command": { "type": "string" }
        }
      }
    }
  }
}`
	return ToolDef{
		Name: runLaunchTool,
		Description: "Launch a governed, multi-step Run bounded by a delegation grant an operator issued to you. " +
			"The plan is validated against your grant (resources, verbs, step count) before anything runs; " +
			"ungated steps run unattended and flagged steps park for human approval — every step is audited. " +
			"Returns the new run's id. It executes nothing itself; it hands the plan to the governed engine.",
		InputSchema: json.RawMessage(inputSchema),
	}
}

// workstreamPollTool is the v0.9 plan-discovery control-plane tool name (ADR-0065 §5,
// T-090).
const workstreamPollTool = "workstream.poll"

// workstream.poll — the composer discovers the open plan requests its charters have
// published (ADR-0065 §5). Like run.launch it resolves NO target and mints NO egress:
// the gateway forwards {agent, optional workstream filter} to services/api, which
// derives the readable set from the caller's own LIVE grants — so it carries no
// Edge/Action/Operation and no target resolver; Invoke dispatches it BEFORE the
// remote.exec target + SEC-16 path, exactly like run.launch. It rides the existing
// agent-tool transport edge (ADR-0065 §2) — no per-tool edge metadata of its own.
func workstreamPoll() ToolDef {
	const inputSchema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "workstreamId": {
      "type": "string",
      "description": "Optional: narrow to one of your charters. Omit to see open plan requests across every charter you compose for."
    }
  }
}`
	return ToolDef{
		Name: workstreamPollTool,
		Description: "Discover the plan requests your charters have open — each adaptive Workstream " +
			"you compose for publishes one when its schedule ticks, with the objective and the " +
			"resource/verb bounds you must plan within. Answer one with plan.submit.",
		InputSchema: json.RawMessage(inputSchema),
	}
}

// planSubmitTool is the v0.9 plan-answer control-plane tool name (ADR-0065 §6, T-090).
const planSubmitTool = "plan.submit"

// plan.submit — the composer answers an open plan request with an ordered plan
// (ADR-0065 §6). Like run.launch it resolves NO target and mints NO egress: the
// gateway forwards {agent, requestId, plan} to services/api, which validates the
// impersonation pin + plan ⊆ grant (SEC-29) and creates a `proposed` Run parked for
// human review — no step ever runs unattended from this call. No Edge/Action/Operation,
// no target resolver; dispatched BEFORE the remote.exec path.
func planSubmit() ToolDef {
	const inputSchema = `{
  "type": "object",
  "required": ["requestId", "steps"],
  "additionalProperties": false,
  "properties": {
    "requestId": {
      "type": "string",
      "description": "The plan request id from workstream.poll you are answering."
    },
    "steps": {
      "type": "array",
      "minItems": 1,
      "description": "The ordered plan: each step runs one non-interactive command on a resource within your charter's grant.",
      "items": {
        "type": "object",
        "required": ["resourceId", "command"],
        "additionalProperties": false,
        "properties": {
          "resourceId": { "type": "string" },
          "command": { "type": "string" }
        }
      }
    }
  }
}`
	return ToolDef{
		Name: planSubmitTool,
		Description: "Answer an open plan request with an ordered plan — it parks for human review " +
			"before anything runs; nothing you submit here executes unattended.",
		InputSchema: json.RawMessage(inputSchema),
	}
}

// memoryReadTool is the v0.9 agent-memory-read control-plane tool name (ADR-0065 §3,
// T-090) — the ONE path the reserved agent-memory edge is enforced on (SEC-32).
const memoryReadTool = "memory.read"

// memory.read — the ONE lit agent-memory path (ADR-0065 §2/§3, SEC-32): unlike its two
// control-plane siblings it DOES carry a governance Edge (the reserved seventh edge,
// documenting the enforcement even though this control-plane dispatch, like run.launch,
// bypasses the target-resolution/policy path that would otherwise consult it). No
// target resolver, no egress minted: the gateway forwards {agent, optional workstream
// filter} to services/api, which derives the readable set from the caller's own LIVE
// grants and chains one memory.read event per call.
func memoryRead() ToolDef {
	const inputSchema = `{
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "workstreamId": {
      "type": "string",
      "description": "Optional: narrow the read to one of your charters. Omit to read everything your grants entitle you to."
    }
  }
}`
	return ToolDef{
		Name: memoryReadTool,
		Description: "Read the scoped memory your grant entitles you to — accrued evidence pointers " +
			"from past runs, plus any operator-authored content explicitly shared with you. Every " +
			"read is audited.",
		InputSchema: json.RawMessage(inputSchema),
		Edge:        "agent-memory",
	}
}

// contextSearchTool is the v0.10 agent-workspace-read control-plane tool name
// (ADR-0066 §4, T-099) — the ONE path the reserved agent-workspace edge is enforced on
// (SEC-33).
const contextSearchTool = "context.search"

// context.search — the composer searches the project context (docs/runbooks/repo) its
// charter's grant entitles it to (ADR-0066 §4). Like memory.read it DOES carry a
// governance Edge (the reserved agent-workspace edge, documenting the enforcement even
// though this control-plane dispatch bypasses the target-resolution/policy path that
// would otherwise consult it). No target resolver, no egress minted: the gateway
// forwards {agent, query, optional workstream filter} to services/api, which derives
// the readable set from the caller's own LIVE grants and chains one context.read event
// per call.
func contextSearch() ToolDef {
	const inputSchema = `{
  "type": "object",
  "required": ["query"],
  "additionalProperties": false,
  "properties": {
    "query": {
      "type": "string",
      "description": "The search terms to look up in the project context (docs, runbooks, repo) your charter's grant entitles you to."
    },
    "workstreamId": {
      "type": "string",
      "description": "Optional: narrow the search to one of your charters. Omit to search everything your grants entitle you to."
    }
  }
}`
	return ToolDef{
		Name: contextSearchTool,
		Description: "Search the project context (docs/runbooks/repo) your charter's grant entitles you to. " +
			"Returns ranked text chunks with their source file and location. Every search is audited.",
		InputSchema: json.RawMessage(inputSchema),
		Edge:        "agent-workspace",
	}
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

// BrokerFiler runs the governed remote.file.{read,write,list} legs on the broker
// (ADR-0071 §D4). Kept SEPARATE from BrokerExecer — the T-138 seam constraint holds:
// BrokerExecer is not renamed or widened for this second egress family. Write is
// T-141: performAndFinish calls it ONLY after SEC-49's digest check has already
// passed, so it never needs to re-verify — it trusts its caller within this package.
// List is T-142: it returns brokerfile.Entry (this package's mirror of the broker's
// sshpty.FileEntry — see Finding 2 in the T-142 brief), not a type this package
// defines itself, since brokerfile is the only place that already owns the wire
// contract with a golden-JSON fixture test guarding it.
type BrokerFiler interface {
	Read(ctx context.Context, invocationID, resourceID, path, subject string) (content []byte, sha256sum string, err error)
	Write(ctx context.Context, invocationID, resourceID, path string, content []byte, mode uint32, subject string) (sha256sum string, err error)
	List(ctx context.Context, invocationID, resourceID, path, subject string) (entries []brokerfile.Entry, err error)
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
	brokerFile     BrokerFiler // remote.file.read's broker leg (ADR-0071); nil → tool error, mirrors Forwarder et al.
	maxInFlight    int
	waitTimeout    time.Duration       // agent block-and-wait deadline for a parked invocation (ADR-0059)
	runWaitTimeout time.Duration       // operator Run-step block-and-wait deadline (T-055b)
	pollInterval   time.Duration       // how often the wait re-reads the invocation row
	forwarder      mcp.LaunchForwarder // run.launch forward to services/api (ADR-0062, T-065b); nil → run.launch is unavailable
	// The three v0.9 forwards (ADR-0065, T-090): each nil independently answers a tool
	// error for its own tool, mirroring forwarder above.
	pollForwarder   mcp.PollForwarder
	submitForwarder mcp.SubmitForwarder
	readForwarder   mcp.ReadForwarder
	// searchForwarder is the v0.10 context.search forward (ADR-0066 §4, T-099), nil-able
	// like its three siblings above.
	searchForwarder mcp.ContextSearchForwarder
}

// Config wires the governed tool service. Zero values get safe defaults.
type Config struct {
	Store          Store
	Policy         PolicyDecider
	Broker         BrokerExecer
	BrokerFile     BrokerFiler         // remote.file.read's broker leg (ADR-0071); nil → remote.file.read answers a tool error
	MaxInFlight    int                 // per-agent concurrency cap (PERF-3); ≤ 0 → 4
	WaitTimeout    time.Duration       // agent block-and-wait deadline; ≤ 0 → 125s (just over policy's 120s TTL)
	RunWaitTimeout time.Duration       // operator Run-step block-and-wait; ≤ 0 → 10m (Run approval window ≫ 125s)
	PollInterval   time.Duration       // approval poll cadence; ≤ 0 → 1s
	Forwarder      mcp.LaunchForwarder // run.launch forward to services/api (ADR-0062, T-065b); nil → run.launch answers a tool error
	// The three v0.9 forwards (ADR-0065, T-090), each independently nil-able.
	PollForwarder   mcp.PollForwarder   // workstream.poll forward; nil → tool error
	SubmitForwarder mcp.SubmitForwarder // plan.submit forward; nil → tool error
	ReadForwarder   mcp.ReadForwarder   // memory.read forward; nil → tool error
	// SearchForwarder is the v0.10 context.search forward (ADR-0066 §4, T-099); nil →
	// tool error.
	SearchForwarder mcp.ContextSearchForwarder
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
		registry: NewRegistry(), store: cfg.Store, policy: cfg.Policy, broker: cfg.Broker, brokerFile: cfg.BrokerFile,
		maxInFlight: cfg.MaxInFlight, waitTimeout: cfg.WaitTimeout, pollInterval: cfg.PollInterval,
		runWaitTimeout: cfg.RunWaitTimeout, forwarder: cfg.Forwarder,
		pollForwarder: cfg.PollForwarder, submitForwarder: cfg.SubmitForwarder, readForwarder: cfg.ReadForwarder,
		searchForwarder: cfg.SearchForwarder,
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

	// FIVE CONTROL-PLANE tools (run.launch, ADR-0062 T-065b; workstream.poll/plan.submit/
	// memory.read, ADR-0065 T-090; context.search, ADR-0066 T-099): none resolve a
	// resource target, so all are dispatched BEFORE the remote.exec target resolution +
	// the SEC-16 scope check (which would otherwise reject them). Their authority is the
	// delegation grant / api's own derivation — never the agent token's resource/operation
	// scope.
	switch name {
	case runLaunchTool:
		return s.launchRun(ctx, capb.Subject, args)
	case workstreamPollTool:
		return s.pollWorkstream(ctx, capb.Subject, args)
	case planSubmitTool:
		return s.submitPlan(ctx, capb.Subject, args)
	case memoryReadTool:
		return s.readMemory(ctx, capb.Subject, args)
	case contextSearchTool:
		return s.searchContext(ctx, capb.Subject, args)
	}

	// Validate + resolve the governance target (resource:<id>) from the args.
	target, err := def.Target(args)
	if err != nil {
		return mcp.ToolResult{}, err
	}
	resourceID := strings.TrimPrefix(target, "resource:")

	// SEC-16: the invocation must stay within the agent token's scope — both the
	// resource and the operation.
	if capb.Scope.Resource != "" && capb.Scope.Resource != resourceID {
		return toolError("resource is outside the agent token's scope"), nil
	}
	// ADR-0071 §D7: an empty Operations list is unrestricted ONLY for "exec" —
	// back-compat for resource-only scoped tokens minted before SEC-16, which
	// predates every operation but exec. Every other verb, including
	// remote.file.read's "file.read", requires its verb to be explicitly present;
	// an empty list is a REFUSAL, not a pass. Without this, the moment a second
	// operation verb existed, every pre-existing (and every future
	// operations-unaware) agent token would silently acquire it. When the token
	// DOES declare operations, the check is identical for every verb including
	// exec — this branch only ever changes behavior for the empty-list case.
	if len(capb.Scope.Operations) > 0 {
		if !slices.Contains(capb.Scope.Operations, def.Operation) {
			return toolError("operation is outside the agent token's scope"), nil
		}
	} else if def.Operation != "exec" {
		return toolError("operation is outside the agent token's scope"), nil
	}

	// The descriptor is persisted on the invocation as Command (ADR-0059/ADR-0071 §D3)
	// so the operator approves the exact payload and any waiter runs what was approved.
	// remote.exec's descriptor is the command verbatim; a future operation family
	// renders a human-readable summary instead. A descriptor failure is a tool-level
	// refusal (the agent's call was malformed), not a Go error and not a panic.
	command, descErr, isDescErr := describeCommand(def, args)
	if isDescErr {
		return descErr, nil
	}

	// T-141 (ADR-0071 §14c, option (c)): the payload bytes (remote.file.write only —
	// nil for every other tool) are extracted HERE, from the caller's raw arguments,
	// and threaded through stepRequest as a plain parameter for the life of this
	// blocking call. They are never persisted — Create below writes only `command`,
	// the descriptor, never the content.
	content, contentErr := def.Content(args)
	if contentErr != nil {
		return toolError(contentErr.Error()), nil
	}

	// The idempotency key is read HERE (the agent's Idempotency-Key header, via ctx)
	// and passed in — the core never reads it from ctx, so it equally serves the Run
	// engine's deterministic per-step keys (T-055b).
	// The agent plane does not surface the invocation id in its tools/call result.
	_, res, err := s.runGovernedStep(ctx, stepRequest{
		Subject:        capb.Subject,
		Tool:           def.Name,
		ResourceID:     resourceID,
		Command:        command,
		Content:        content,
		Edge:           def.Edge,
		Action:         def.Action,
		Target:         target,
		BracketEdge:    "agent-tool", // the agent plane's bracket edge (RUN-5 default)
		IdempotencyKey: mcp.IdempotencyKey(ctx),
		WaitTimeout:    s.waitTimeout,
	})
	return res, err
}

// describeCommand renders a tool's ADR-0071 §D3 descriptor and maps a rendering
// failure to a tool-level refusal — never a bubbled Go error, never a panic. It is kept
// as its own function (rather than inlined into Invoke) so that mapping is directly
// testable without the agent-capability context Invoke otherwise requires: today
// def.Target already guarantees valid args by the time Invoke reaches this call, so the
// error branch is unreachable through remote.exec's real dispatch, but it is the
// contract every future operation family (T-140b) depends on the moment its Target and
// Describe validation diverge.
func describeCommand(def ToolDef, args json.RawMessage) (string, mcp.ToolResult, bool) {
	command, err := def.Describe(args)
	if err != nil {
		return "", toolError(err.Error()), true
	}
	return command, mcp.ToolResult{}, false
}

// launchRun handles the run.launch control-plane tool (ADR-0062, T-065b): it parses the
// grant id + plan, forwards {agent, grant, plan, idempotency key} to services/api (which
// validates plan-vs-grant and creates the Run), and returns the new run id to the agent. The
// gateway mints no egress and creates no Run here — api is the authority. An api refusal
// (out-of-grant, N1) is a tool error; an infrastructure failure is a Go error (fail-closed).
func (s *Service) launchRun(ctx context.Context, agentSubject string, args json.RawMessage) (mcp.ToolResult, error) {
	if s.forwarder == nil {
		return toolError("run launching is not available on this gateway"), nil
	}
	var a struct {
		GrantID string `json:"grantId"`
		Steps   []struct {
			ResourceID string `json:"resourceId"`
			Command    string `json:"command"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return toolError("invalid run.launch arguments: " + err.Error()), nil
	}
	if a.GrantID == "" || len(a.Steps) == 0 {
		return toolError("run.launch requires grantId and at least one step"), nil
	}
	steps := make([]mcp.LaunchStep, len(a.Steps))
	for i, st := range a.Steps {
		if st.ResourceID == "" || st.Command == "" {
			return toolError("each run.launch step requires resourceId and command"), nil
		}
		steps[i] = mcp.LaunchStep{ResourceID: st.ResourceID, Command: st.Command}
	}
	runID, status, refusal, err := s.forwarder.Launch(ctx, agentSubject, a.GrantID, mcp.IdempotencyKey(ctx), steps)
	if err != nil {
		return mcp.ToolResult{}, fmt.Errorf("forward run launch: %w", err)
	}
	if refusal != "" {
		return toolError("run launch refused: " + refusal), nil
	}
	return mcp.ToolResult{Content: []mcp.Content{{Type: "text", Text: fmt.Sprintf("launched run %s (%s)", runID, status)}}}, nil
}

// pollWorkstream handles the workstream.poll control-plane tool (ADR-0065 §5, T-090):
// it forwards {agent, optional workstream filter} to services/api, which derives the
// caller's own open plan requests from its LIVE grants (SEC-32 containment). Like
// run.launch it mints no egress and stays OUTSIDE the maxInFlight cap (dispatched here,
// never reaching runGovernedStep where CountInFlight lives) — PERF-10(5) makes
// memory.read alone participate, below. The composition-context list is returned as one
// JSON text block (MCP has no structured-content type on this wire; the agent's
// interface is the tool description, not the payload shape).
func (s *Service) pollWorkstream(ctx context.Context, agentSubject string, args json.RawMessage) (mcp.ToolResult, error) {
	if s.pollForwarder == nil {
		return toolError("plan polling is not available on this gateway"), nil
	}
	var a struct {
		WorkstreamID string `json:"workstreamId"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return toolError("invalid workstream.poll arguments: " + err.Error()), nil
	}
	reqs, err := s.pollForwarder.Poll(ctx, agentSubject, a.WorkstreamID)
	if err != nil {
		return mcp.ToolResult{}, fmt.Errorf("forward workstream poll: %w", err)
	}
	body, merr := json.Marshal(struct {
		Requests []mcp.PlanRequestView `json:"requests"`
	}{reqs})
	if merr != nil {
		return mcp.ToolResult{}, fmt.Errorf("marshal poll result: %w", merr)
	}
	return mcp.ToolResult{Content: []mcp.Content{{Type: "text", Text: string(body)}}}, nil
}

// submitPlan handles the plan.submit control-plane tool (ADR-0065 §6, T-090): it
// forwards {agent, requestId, plan} to services/api, which validates the impersonation
// pin + plan ⊆ grant (SEC-29, the shared LaunchBounded core) and creates a `proposed`
// Run. A non-empty refusal is api's refusal (a tool error, the run.launch mapping — an
// unknown request and a foreign agent's request read identically, no oracle). Stays
// OUTSIDE the maxInFlight cap, like run.launch/workstream.poll (D5/R1 — only
// memory.read participates).
func (s *Service) submitPlan(ctx context.Context, agentSubject string, args json.RawMessage) (mcp.ToolResult, error) {
	if s.submitForwarder == nil {
		return toolError("plan submission is not available on this gateway"), nil
	}
	var a struct {
		RequestID string `json:"requestId"`
		Steps     []struct {
			ResourceID string `json:"resourceId"`
			Command    string `json:"command"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return toolError("invalid plan.submit arguments: " + err.Error()), nil
	}
	if a.RequestID == "" || len(a.Steps) == 0 {
		return toolError("plan.submit requires requestId and at least one step"), nil
	}
	steps := make([]mcp.LaunchStep, len(a.Steps))
	for i, st := range a.Steps {
		if st.ResourceID == "" || st.Command == "" {
			return toolError("each plan.submit step requires resourceId and command"), nil
		}
		steps[i] = mcp.LaunchStep{ResourceID: st.ResourceID, Command: st.Command}
	}
	runID, status, refusal, err := s.submitForwarder.Submit(ctx, agentSubject, a.RequestID, steps)
	if err != nil {
		return mcp.ToolResult{}, fmt.Errorf("forward plan submit: %w", err)
	}
	if refusal != "" {
		return toolError("plan submit refused: " + refusal), nil
	}
	return mcp.ToolResult{Content: []mcp.Content{{Type: "text", Text: fmt.Sprintf("submitted plan; run %s (%s)", runID, status)}}}, nil
}

// readMemory handles the memory.read control-plane tool (ADR-0065 §3, T-090): the ONE
// lit agent-memory path (SEC-32). PERF-10(5)/D5/R1: reads have no grant-budget bound,
// so the gateway's existing per-agent maxInFlight cap (PERF-3) is their only brake — the
// SAME counter run.launch/workstream.poll/plan.submit bypass entirely (they are
// dispatched before runGovernedStep, the only other place CountInFlight is consulted).
// memory.read alone checks it here, reusing the counter as-is (no new limiter).
func (s *Service) readMemory(ctx context.Context, agentSubject string, args json.RawMessage) (mcp.ToolResult, error) {
	if s.readForwarder == nil {
		return toolError("memory reading is not available on this gateway"), nil
	}
	inFlight, err := s.store.CountInFlight(ctx, agentSubject)
	if err != nil {
		return mcp.ToolResult{}, fmt.Errorf("concurrency check: %w", err)
	}
	if inFlight >= s.maxInFlight {
		return toolError("too many concurrent invocations for this agent; retry shortly"), nil
	}
	var a struct {
		WorkstreamID string `json:"workstreamId"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return toolError("invalid memory.read arguments: " + err.Error()), nil
	}
	items, err := s.readForwarder.Read(ctx, agentSubject, a.WorkstreamID)
	if err != nil {
		return mcp.ToolResult{}, fmt.Errorf("forward memory read: %w", err)
	}
	body, merr := json.Marshal(struct {
		Items []mcp.MemoryRowView `json:"items"`
	}{items})
	if merr != nil {
		return mcp.ToolResult{}, fmt.Errorf("marshal read result: %w", merr)
	}
	return mcp.ToolResult{Content: []mcp.Content{{Type: "text", Text: string(body)}}}, nil
}

// searchContext handles the context.search control-plane tool (ADR-0066 §4, T-099): the
// ONE lit agent-workspace path (SEC-33). Like memory.read it is a READ with no
// grant-budget bound, so it participates in the SAME per-agent maxInFlight cap
// (PERF-3/PERF-10(5)) — the memory.read precedent extended to a second read verb.
func (s *Service) searchContext(ctx context.Context, agentSubject string, args json.RawMessage) (mcp.ToolResult, error) {
	if s.searchForwarder == nil {
		return toolError("context search is not available on this gateway"), nil
	}
	inFlight, err := s.store.CountInFlight(ctx, agentSubject)
	if err != nil {
		return mcp.ToolResult{}, fmt.Errorf("concurrency check: %w", err)
	}
	if inFlight >= s.maxInFlight {
		return toolError("too many concurrent invocations for this agent; retry shortly"), nil
	}
	var a struct {
		Query        string `json:"query"`
		WorkstreamID string `json:"workstreamId"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return toolError("invalid context.search arguments: " + err.Error()), nil
	}
	if a.Query == "" {
		return toolError("context.search requires a query"), nil
	}
	chunks, err := s.searchForwarder.Search(ctx, agentSubject, a.Query, a.WorkstreamID)
	if err != nil {
		return mcp.ToolResult{}, fmt.Errorf("forward context search: %w", err)
	}
	body, merr := json.Marshal(struct {
		Chunks []mcp.ContextChunkView `json:"chunks"`
	}{chunks})
	if merr != nil {
		return mcp.ToolResult{}, fmt.Errorf("marshal search result: %w", merr)
	}
	return mcp.ToolResult{Content: []mcp.Content{{Type: "text", Text: string(body)}}}, nil
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
		Principal:      req.Principal, // the delegating operator for a delegated Run (ADR-0062, T-064); "" for a direct operator Run
		Tool:           def.Name,
		ResourceID:     req.ResourceID,
		Command:        req.Command,
		Edge:           def.Edge,
		Action:         def.Action,
		Target:         target,
		BracketEdge:    delegatedBracketEdge(req.Principal),
		IdempotencyKey: req.IdempotencyKey,
		WaitTimeout:    s.runWaitTimeout,
	})
}

// delegatedBracketEdge picks a Run step's bracket audit edge (ADR-0062 §4): a DELEGATED step
// (a Principal is set — an agent drives under a grant) is bracketed `agent-tool` (the actor is
// an agent invoking a tool, with the principal recorded on the row); a direct OPERATOR step
// (no Principal) stays `human-agent`. Not `agent-agent` — there is no second agent (the
// permission model's edge set is unchanged).
func delegatedBracketEdge(principal string) string {
	if principal != "" {
		return "agent-tool"
	}
	return "human-agent"
}

var _ mcp.StepRunner = (*Service)(nil)

// stepRequest is the resolved input to the governed core — a single governed step,
// independent of which plane initiated it. The agent path (Invoke) builds it from the
// agent capability + ToolDef + args; the operator Run engine builds it from the
// launching operator + a plan step (T-055b). Keeping the core plane-agnostic is the
// seam that lets the Run engine reuse the whole governed pipeline without the
// agent-token machinery.
type stepRequest struct {
	Subject        string        // the ACTOR (agent for a delegated Run / a direct tools/call; operator for a direct Run) — audit actor + policy subject
	Principal      string        // the PRINCIPAL (ADR-0062 §4): the delegating operator when an agent drives under a grant; "" for a direct operator Run or a direct agent call
	Tool           string        // the tool name (def.Name)
	ResourceID     string        // target resource
	Command        string        // the command / descriptor to persist + execute
	Content        []byte        // T-141: remote.file.write's payload bytes; NEVER persisted (ADR-0071 §14c option (c)); nil for every other tool
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
		Principal: req.Principal, // the delegating operator (ADR-0062, T-064); "" for non-delegated
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
		res, werr := s.waitForApproval(ctx, inv.ID, req.Subject, req.WaitTimeout, req.Content)
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
		// The dispatch tool comes from the PERSISTED row (inv.Tool), not req.Tool: on an
		// idempotent conflict inv is the ORIGINAL invocation (store.go's ON CONFLICT is
		// keyed on agent_subject+idempotency_key, not tool), so a caller-supplied tool
		// could otherwise diverge from what was actually persisted and approved
		// (SEC-49 — approval binds to persisted state, never to what the caller says
		// afterwards). On the freshly-created path inv.Tool always equals req.Tool
		// anyway (store.Create persists req.Tool verbatim), so this is a no-op rename
		// of the source, not a behavior change, for the only path that reaches here
		// with created=true.
		res, eerr := s.performAndFinish(ctx, inv.ID, req.ResourceID, req.Command, req.Subject, inv.Tool, req.Content)
		return inv.ID, res, eerr
	}

	// requires_approval → park, then block until the operator resolves it (T-040).
	_ = s.store.SetStatus(ctx, inv.ID, "pending_approval", decision)
	res, werr := s.waitForApproval(ctx, inv.ID, req.Subject, req.WaitTimeout, req.Content)
	return inv.ID, res, werr
}

// waitForApproval blocks the agent's tools/call on a parked invocation until the
// operator resolves it (ADR-0057/0059 block-and-wait): grant (decision=allow) →
// atomically claim + exec; deny/failed → typed error; the gateway's own deadline →
// finalize failed (policy's TTL timeout runs in parallel). An agent disconnect (ctx)
// leaves the row parked for the reaper or an idempotent re-attach. It polls the row —
// a short query per tick, never a held transaction.
func (s *Service) waitForApproval(ctx context.Context, id, agent string, timeout time.Duration, content []byte) (mcp.ToolResult, error) {
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
						// content is THIS CALL's own bytes (T-141, ADR-0071 §14c) — never
						// the original caller's, since those were never persisted and this
						// may be a different goroutine entirely (idempotent reattach).
						// SEC-49 is what makes that safe: performAndFinish verifies these
						// bytes against the persisted descriptor's digest before writing
						// anything, regardless of whose call supplied them.
						return s.performAndFinish(ctx, id, claimed.ResourceID, claimed.Command, agent, claimed.Tool, content)
					}
				}
			}
		}
	}
}

// performAndFinish is the seam's terminal step (ADR-0071 §D2): it dispatches on the
// invocation's TOOL to the operation that actually crosses the trust boundary, and
// records the terminal outcome. The caller has already moved the invocation to
// running (allow-now via SetStatus, post-approval via ClaimForExec). remote.exec's
// case is unchanged byte-for-byte from the prior execAndFinish; remote.file.read
// (T-140b, ADR-0071 §D4) and remote.file.write (T-141) dispatch to BrokerFiler
// instead of BrokerExecer. content is remote.file.write's payload ONLY — every other
// case ignores it. The default branch is the whole point of this seam: an
// unrecognized tool fails closed (SEC-19) rather than falling through to exec, so a
// later tool addition (T-142+) cannot silently reuse exec's egress by omission.
func (s *Service) performAndFinish(ctx context.Context, id, resourceID, command, agent, tool string, content []byte) (mcp.ToolResult, error) {
	switch tool {
	case remoteExecTool:
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
	case remoteFileReadTool:
		// The nil-able pattern (Forwarder/PollForwarder/etc.): an unwired broker
		// file client answers a tool error, never a panic.
		if s.brokerFile == nil {
			_ = s.store.Finish(ctx, id, "failed", "tool.result:error")
			return toolError("file operations are not available on this gateway"), nil
		}
		// command IS the persisted ADR-0071 §D3 descriptor (there is no separate
		// args column, C4); recover the path the agent asked to read — fail closed
		// (T-141 Part 4) rather than silently reinterpreting a malformed descriptor.
		path, perr := parseFileReadDescriptor(command)
		if perr != nil {
			_ = s.store.Finish(ctx, id, "failed", "tool.result:error")
			return toolError("malformed file read descriptor, refusing: " + perr.Error()), nil
		}
		readContent, sha, err := s.brokerFile.Read(ctx, id, resourceID, path, agent)
		if err != nil {
			_ = s.store.Finish(ctx, id, "failed", "tool.result:error")
			return mcp.ToolResult{}, fmt.Errorf("file read failed: %w", err)
		}
		_ = s.store.Finish(ctx, id, "completed", "tool.result")
		return mcp.ToolResult{Content: []mcp.Content{{Type: "text", Text: string(readContent) + "\n[sha256:" + sha + "]"}}}, nil
	case remoteFileWriteTool:
		// The nil-able pattern, same as remote.file.read above.
		if s.brokerFile == nil {
			_ = s.store.Finish(ctx, id, "failed", "tool.result:error")
			return toolError("file operations are not available on this gateway"), nil
		}
		// command IS the persisted ADR-0071 §D3 descriptor (path, size, digest, mode
		// — never the bytes, per the T-141 consult's option (c)). Fail closed on a
		// descriptor that doesn't parse cleanly (T-141 Part 4 / SEC-49): it must
		// never proceed as if there were no digest to check.
		path, digest, mode, perr := parseFileWriteDescriptor(command)
		if perr != nil {
			_ = s.store.Finish(ctx, id, "failed", "tool.result:error")
			return toolError("malformed file write descriptor, refusing to write: " + perr.Error()), nil
		}
		// SEC-49, the load-bearing check (ADR-0071 §14c): the approved descriptor
		// carries the digest of the bytes the operator approved; recompute the
		// digest over the bytes actually about to be written and refuse on ANY
		// mismatch, BEFORE calling the broker. This is what makes it safe that
		// content is never persisted and never re-verified against anything but
		// this digest — a swapped payload (the T-138 tool-confusion shape) is
		// refused cryptographically, not by convention.
		sum := sha256.Sum256(content)
		actual := hex.EncodeToString(sum[:])
		if actual != digest {
			_ = s.store.Finish(ctx, id, "failed", "tool.result:error")
			return toolError(fmt.Sprintf(
				"content digest sha256:%s does not match the approved descriptor's sha256:%s; refusing to write (SEC-49)",
				actual, digest)), nil
		}
		sha, err := s.brokerFile.Write(ctx, id, resourceID, path, content, mode, agent)
		if err != nil {
			_ = s.store.Finish(ctx, id, "failed", "tool.result:error")
			return mcp.ToolResult{}, fmt.Errorf("file write failed: %w", err)
		}
		_ = s.store.Finish(ctx, id, "completed", "tool.result")
		return mcp.ToolResult{Content: []mcp.Content{{Type: "text",
			Text: fmt.Sprintf("wrote %d bytes to %s [sha256:%s]", len(content), path, sha)}}}, nil
	case remoteFileListTool:
		// The nil-able pattern, same as remote.file.read/write above.
		if s.brokerFile == nil {
			_ = s.store.Finish(ctx, id, "failed", "tool.result:error")
			return toolError("file operations are not available on this gateway"), nil
		}
		// command IS the persisted ADR-0071 §D3 descriptor; recover the directory
		// path the agent asked to list — fail closed on a malformed descriptor,
		// same as read/write.
		path, perr := parseFileListDescriptor(command)
		if perr != nil {
			_ = s.store.Finish(ctx, id, "failed", "tool.result:error")
			return toolError("malformed file list descriptor, refusing: " + perr.Error()), nil
		}
		entries, err := s.brokerFile.List(ctx, id, resourceID, path, agent)
		if err != nil {
			// PERF-18: the broker's error (including its 413 over-cap refusal, T-139b)
			// becomes a Go error here, exactly like read/write's client failures — NEVER
			// an empty or partial listing. A refusal must be distinguishable from "this
			// directory has zero entries."
			_ = s.store.Finish(ctx, id, "failed", "tool.result:error")
			return mcp.ToolResult{}, fmt.Errorf("file list failed: %w", err)
		}
		_ = s.store.Finish(ctx, id, "completed", "tool.result")
		return mcp.ToolResult{Content: []mcp.Content{{Type: "text", Text: renderFileEntries(path, entries)}}}, nil
	default:
		// Fail closed (SEC-19): no operation is implemented for this tool at the
		// terminal step, so nothing runs and the invocation cannot be left parked in
		// "running".
		_ = s.store.Finish(ctx, id, "failed", "tool.result:error")
		return mcp.ToolResult{}, fmt.Errorf("no governed operation for tool %q", tool)
	}
}

// renderFileEntries renders remote.file.list's result as readable text for the
// agent (ADR-0071 §D1): one line per entry, "name\tsize\tmode\tmodTime\tkind".
// Mode is masked to its permission bits (& 0o777) since brokerfile.Entry.Mode
// carries the broker's full os.FileMode bits, of which the agent only needs the
// POSIX permissions. An empty directory renders an explicit marker rather than
// an empty string, so the agent (and a human reading the audit trail) can tell
// "listed, zero entries" apart from "no text came back."
func renderFileEntries(path string, entries []brokerfile.Entry) string {
	if len(entries) == 0 {
		return path + ": (empty directory)"
	}
	lines := make([]string, 0, len(entries)+1)
	lines = append(lines, path+":")
	for _, e := range entries {
		kind := "file"
		if e.IsDir {
			kind = "dir"
		}
		lines = append(lines, fmt.Sprintf("%s\t%d\t%04o\t%s\t%s",
			e.Name, e.Size, e.Mode&0o777, e.ModTime.Format(time.RFC3339), kind))
	}
	return strings.Join(lines, "\n")
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
			ID: inv.ID, Agent: inv.AgentSubject, Principal: inv.Principal, ResourceID: inv.ResourceID,
			Tool: inv.Tool, Command: inv.Command, Status: inv.Status,
			IdempotencyKey: inv.IdempotencyKey, // api parses run:<id>:step:<n> for FE-16 context
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
