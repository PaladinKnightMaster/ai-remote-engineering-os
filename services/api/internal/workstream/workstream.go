// Package workstream is services/api's chartered-Workstream store — the standing,
// operator-chartered objective that CREATES governed Runs on a schedule (ADR-0063;
// v0.7 "Chartered Workstreams"). A charter = objective + an IMMUTABLE plan template +
// a bounded scope + a fixed-interval schedule + bounds, consented to in ONE typed act:
// chartering auto-issues the Workstream's own delegation grant (grantee =
// workstream:<id>, 1:1) in the same transaction (WS-2) — the grant, not the charter,
// is the launch authority (no third authority model). The in-process ticker (T-073)
// fires due charters through the shared bounded-launch core; the existing sequencer
// drives the Runs.
//
// It is a sibling of internal/run and internal/grant (the extraction seam) and, by
// design, imports internal/grant: the charter and its grant are one lifecycle (they
// are created in one tx and the criteria treat the three packages as one extraction
// unit). The template's commands are sealed at rest via the same nil-safe
// CommandSealer contract as the run store (F3 parity).
//
// T-071 lands the schema + store (Create/Get/ListForOperator) + the charter routes.
// The ticker + windowed budget (T-073) and the lifecycle verbs (T-074) follow.
//
// T-087 (ADR-0065; v0.9.0 "Adaptive Workstreams") adds the additive `adaptive` charter
// kind (WS-5) — objective + bounds + schedule + a NAMED COMPOSER, zero template steps —
// whose auto-issued grant names the composer as grantee (R2), the one behavior change
// to Create; template charters are byte-behavior-preserved. It also adds the
// plan-request primitives (PLAN-1): the D1-ratified home for the inbound plan channel
// is THIS package (the charter is the owning entity), not a fifth sibling. No routes,
// no ticker branch, no audit events yet — T-088/T-089/T-090 wire them.
package workstream

import (
	"time"

	"aireos.local/services/api/internal/grant"
)

// Workstream lifecycle statuses (ADR-0063 §7). paused = ticks stop, in-flight Runs
// complete, the grant stays active (reversible). retired = terminal — the charter's
// grant is revoked and the existing cascade cancels its in-flight Runs.
const (
	StatusActive  = "active"
	StatusPaused  = "paused"
	StatusRetired = "retired"
)

// Workstream charter kinds (WS-5, ADR-0065 §4). template (the v0.7 shape, unchanged) =
// an IMMUTABLE plan instantiated verbatim per tick. adaptive (v0.9) = no template
// steps; each tick opens a plan request instead (PLAN-1) that the named Composer
// answers. Existing rows backfill 'template' (migration 0013); template semantics are
// structurally untouched.
const (
	KindTemplate = "template"
	KindAdaptive = "adaptive"
)

// Workstream is one charter: a standing objective whose immutable template is fired on
// a schedule with nobody present at launch (the third initiator, ADR-0063).
type Workstream struct {
	ID              string
	Principal       string // the chartering operator (owner; the principal on every fired Run)
	Label           string
	Objective       string // the chartered objective, reviewed at consent (GOV-8)
	GrantID         string // the charter's auto-issued delegation grant (1:1, WS-2)
	IntervalSeconds int
	NextFireAt      time.Time // durable schedule head (DUR-8); first fire = chartered_at + interval
	Status          string
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Steps           []Step // the immutable template, ordered by Index; populated by Get, not List
	Kind            string // KindTemplate | KindAdaptive (WS-5, ADR-0065 §4)
	Composer        string // the named composer agent subject; "" for a template charter
}

// Step is one ordered template step (instantiated verbatim per tick).
type Step struct {
	Index      int
	ResourceID string
	Command    string // operator-authored; sealed at rest, opened on read (F3 parity)
}

// StepInput is one step of a new charter's template.
type StepInput struct {
	ResourceID string
	Command    string
}

// Input is a new charter. Grant carries the charter's scope/TTL/bounds as the grant to
// auto-issue (ADR-0063 §2); the store overrides its Principal (= Input.Principal) and
// Agent (= "workstream:<id>" for a template charter, minted here; = Composer for an
// adaptive charter, R2) so a caller can never bind the charter to a foreign grantee.
// The handler validates the template against Grant's scope/bounds BEFORE Create (WS-2's
// 422 matrix); the store re-checks only well-formedness.
//
// Kind selects the charter shape (WS-5, ADR-0065 §4): "" defaults to KindTemplate
// (behavior-preserving for every pre-v0.9 caller). KindTemplate requires Composer == ""
// and len(Steps) >= 1; KindAdaptive requires Composer != "" and len(Steps) == 0 — Create
// rejects any other combination, and the DB CHECK (workstreams_composer_chk) backstops
// it structurally.
type Input struct {
	Principal       string
	Label           string
	Objective       string
	IntervalSeconds int
	Steps           []StepInput
	Grant           grant.Input
	Kind            string // "" | KindTemplate | KindAdaptive
	Composer        string // required for KindAdaptive; must be "" for KindTemplate
}

// PlanRequest lifecycle statuses (PLAN-1, ADR-0065 §5). open = discoverable via
// workstream.poll (T-090), awaiting an answer. submitted = plan.submit (T-090) created a
// `proposed` Run and set RunID. expired = the TTL passed unanswered (DUR-10) — the
// charter never wedges; the next tick opens a fresh request. closed = the revocation
// cascade re-aim (§7, T-089) closed it early (grant revoked/expired).
const (
	PlanRequestOpen      = "open"
	PlanRequestSubmitted = "submitted"
	PlanRequestExpired   = "expired"
	PlanRequestClosed    = "closed"
)

// PlanRequest is one inbound plan-request row (PLAN-1, ADR-0065 §5): an adaptive
// charter's tick opens this instead of instantiating a template; the named Composer
// discovers it (workstream.poll) and answers it (plan.submit), which creates a
// `proposed` Run (RUN-6) and marks the request submitted. At most one OPEN request
// exists per charter — a STRUCTURAL invariant (plan_requests_one_open_idx, C2), not
// application discipline.
type PlanRequest struct {
	ID           string
	WorkstreamID string
	LaunchKey    string // the tick's dedup carrier — mirrors the runs.launch_key convention
	Status       string
	RunID        string // "" until MarkPlanRequestSubmitted sets it
	ExpiresAt    time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// PlanRequestComposition is one open plan request as seen by the composer's
// workstream.poll (D1, ADR-0065 §5, T-090): the request plus the composition context
// the answering agent needs WITHOUT a second round trip — the objective (the charter's
// consented-to text, plaintext-safe per its own consent act) and the bounding grant's
// resource/verb scope + step bound + TTL. Derived ENTIRELY from the caller's own LIVE
// grants (PollOpenPlanRequests) — never a caller-supplied scope (SEC-32 containment).
type PlanRequestComposition struct {
	RequestID      string
	WorkstreamID   string
	Objective      string
	Resources      []string
	Verbs          []string
	MaxStepsPerRun int
	ExpiresAt      time.Time
}
