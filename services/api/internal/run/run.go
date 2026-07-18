// Package run is services/api's Run engine — the execution layer beneath the
// chartered Workstream module (ADR-0061; v0.5.0 "Governed Workstreams"). An operator
// authors an ordered, governed Run of steps; this package owns the durable plan
// (runs + run_steps) and, in later tickets, the sequencer that drives each step
// through the gateway's governed invocation path.
//
// It is deliberately self-contained — the v0.6 extraction seam (RUN-1): it depends
// only on the pgx pool and small interfaces, never on api's other internals, so the
// Run engine can be lifted into its own service when agent-driven Runs arrive.
//
// T-054 lands the schema + store skeleton (Create/Get/List). The sequencer + the
// gateway POST /v1/steps route (T-055b), the reclaimable claim (T-056), and the
// run.* audit events (T-058) follow.
package run

import "time"

// Run lifecycle statuses (ADR-0061). StatusProposed (RUN-6, ADR-0065 §6; migration
// 0013, F-a) is a PRE-'pending' park: LaunchBoundedProposed (T-088; T-090's plan.submit
// forward is its production caller) creates a `proposed` Run for a submitted adaptive
// plan, visible to the operator for informed full-plan review before any step executes
// — the consent successor invariant. It stayed deliberately INERT through T-087 (no
// production insert path existed yet); T-088 lands the ONLY production insert path
// (LaunchBoundedProposed) plus its two resolutions — ApproveProposed (→pending, chains
// the deferred run.launched) and DenyProposed (→canceled + the distinct run.denied,
// beside the standard terminal event + memory accrual) — while every OTHER existing
// status consumer's positive-list predicate still excludes 'proposed' by construction
// (proven by this package's integration tests): NextClaimable/ClaimRun never claim it,
// CancelRun's fromStatuses (pending, running) never matches it.
const (
	StatusProposed  = "proposed"  // a submitted adaptive plan, parked for informed review (RUN-6)
	StatusPending   = "pending"   // authored, not yet driven
	StatusRunning   = "running"   // claimed and being driven by the sequencer
	StatusCompleted = "completed" // every step completed
	StatusFailed    = "failed"    // a step failed/denied/timed out (stop-on-first-failure)
	StatusCanceled  = "canceled"  // operator-canceled mid-flight
)

// Step lifecycle statuses (ADR-0061).
const (
	StepPending   = "pending"
	StepRunning   = "running"
	StepCompleted = "completed"
	StepFailed    = "failed"
	StepDenied    = "denied"
	StepCanceled  = "canceled"
)

// Run is an operator-authored, ordered, governed execution of steps — the `run` node
// of the chartered Workstream lifecycle (ADR-0061).
type Run struct {
	ID        string
	Operator  string // the owner/principal: the launching operator (direct) or the delegating operator (delegated, T-065)
	Actor     string // the AGENT that drove a delegated Run (ADR-0062, T-064); "" for an operator-direct Run (actor = Operator)
	GrantID   string // the bounding delegation grant (ADR-0062, T-064); "" for an operator-direct Run
	Status    string
	CreatedAt time.Time
	UpdatedAt time.Time
	Steps     []Step // ordered by Index; populated by Get, not List
}

// Step is one ordered governed step of a Run (a single remote.exec).
type Step struct {
	Index        int
	ResourceID   string
	Command      string // operator-authored; sealed at rest, decrypted on read
	InvocationID string // the gateway invocation once dispatched; "" until then
	Status       string
	Outcome      string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// Input is a new Run: the owner/principal operator + the ordered plan. For a delegated
// Run (T-065) Actor is the agent and GrantID the bounding grant; both "" for an
// operator-direct Run (the run.* audit actor then = Operator — behavior-preserving).
type Input struct {
	Operator  string
	Actor     string
	GrantID   string
	LaunchKey string // delegated-launch idempotency key (ADR-0062, T-065a); "" disables dedup (operator-direct)
	Steps     []StepInput
	// Disposition is the Run's starting status (RUN-6, ADR-0065 §6/D1, T-088): "" (the
	// zero value) = StatusPending, today's behavior byte-identical for every existing
	// caller (insert 'pending' + chain run.launched in the SAME transaction, AUD-9).
	// StatusProposed inserts 'proposed' and chains NO run.* event at creation — the
	// submission moment's event is plan.submitted (T-090), not this one; the deferred
	// run.launched fires later, at ApproveProposed. No other value is valid.
	Disposition string
}

// StepInput is one step of a new plan.
type StepInput struct {
	ResourceID string
	Command    string
}
