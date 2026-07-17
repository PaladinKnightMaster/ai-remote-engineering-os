// Package memory is services/api's Persistent Scoped Memory store — the fourth
// sibling seam after internal/run, internal/grant, and internal/workstream (ADR-0064;
// v0.8.0 "Persistent Scoped Memory"). Memory is the mutable, curated, forgettable
// projection standing beside the immutable audit chain: the chain cannot redact,
// expire, or permission-scope what it records — by design — and governed operation
// still demands governed forgetting. That capability lives here.
//
// Two content classes (ADR-0064 §2). The core path is accrual by reference: a
// terminal Run writes a content-free evidence pointer (run/step/invocation ids +
// evidence-packet linkage) in the SAME transaction as its terminal run.* event — the
// literal promotion of the v0.7 EVD-1 read-time assembly into a durable record. The
// secondary path is operator-authored free text (content set), sealed at rest via the
// same nil-safe CommandSealer contract the Run and Workstream stores use, and readable
// only by its owning operator.
//
// It is deliberately self-contained (the internal/run / internal/workstream extraction
// seam): internal/run depends on this package only through the primitive-typed
// MemoryAccruer interface it declares locally — internal/run never imports
// internal/memory (ADR-0064's "the engine must not depend on the P2 module").
//
// T-079 lands the schema (migration 0012) + the store (AccrueRunTerminal/List/Get) +
// the accrual hook on the Run engine's shared terminal flip. The /v1/memories* routes
// (T-080), the forgetting verbs + expiry sweep (T-081), and the UI (T-082) follow.
package memory

import "time"

// Memory scope types (ADR-0064 §6, ratified R3): the bounded scope set the entities
// that exist at v0.8 — {workstream, run, operator}. Further scopes (org/team/project/
// ticket/skill) defer with their owning modules.
const (
	ScopeWorkstream = "workstream"
	ScopeRun        = "run"
	ScopeOperator   = "operator"
)

// Memory is one row: an accrued content-free pointer or an operator-authored note
// (ADR-0064 §1). Content is nil for accrual (the reference-based core, R1); set only
// for authored free text, sealed at rest and opened on read (F3 parity). No
// update-in-place exists in the store — correction is redact + write.
type Memory struct {
	ID              string
	ScopeType       string
	ScopeID         string
	Key             string
	Ref             string // the content-free evidence pointer — never command text or stdout
	Content         string // "" when the row carries no content (accrual); opened on read
	HasContent      bool   // true when content is non-NULL (distinguishes "" content from no content)
	SourceTrace     string
	Confidence      float32
	Pinned          bool
	ExpiresAt       *time.Time
	RedactedAt      *time.Time
	PermissionScope string // the owning operator (owner-scoped reads)
	CreatedAt       time.Time
	UpdatedAt       time.Time
	// AgentVisible is the share flip (MEM-6, ADR-0065 §3; migration 0013): default
	// false — an operator-authored row is invisible to the agent-plane read (memory.read,
	// T-090) until an operator explicitly flips it via memory.shared (T-091). This
	// ticket (T-087) lands the column + this field ONLY — no verb, no route, no
	// agent-plane read exists yet.
	AgentVisible bool
}

// CreateInput is the operator-authored write's input (ADR-0064 §2 secondary path;
// T-080, POST /v1/memories). Owner is bound by the server handler from the caller's
// verified capability — never accepted from the request body (SEC-31/GOV-7). A nil
// Confidence defers to the schema's 1.0 default (recorded, never computed — an L3
// concern). No scope-existence check is performed here (GOV-7): reads are
// permission_scope-filtered, so a foreign scope_id simply returns no rows.
type CreateInput struct {
	Owner       string
	ScopeType   string
	ScopeID     string
	Key         string
	Content     string
	SourceTrace string
	Confidence  *float32
	ExpiresAt   *time.Time
}
