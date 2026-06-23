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

// Run lifecycle statuses (ADR-0061).
const (
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
	Operator  string // the launching operator subject (the audit actor)
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

// Input is a new Run: the launching operator + the ordered plan.
type Input struct {
	Operator string
	Steps    []StepInput
}

// StepInput is one step of a new plan.
type StepInput struct {
	ResourceID string
	Command    string
}
