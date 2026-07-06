// Package grant is services/api's delegation-grant store — the operator-issued,
// scoped, revocable authority that lets an agent launch governed Runs bounded by it
// (ADR-0062; v0.6 "Agent-initiated Runs"). An operator issues a grant (this package +
// the /v1/grants routes); the agent later launches a Run naming the grant, and api
// validates the plan against it (T-065). The grant is a SERVER-SIDE record, never a
// bearer token the agent holds — so the agent mints nothing (SEC-26) and revocation is
// instant (status flip, T-063; cascade to in-flight Runs, T-066).
//
// It is deliberately self-contained — a sibling of internal/run (the extraction seam):
// it depends only on the pgx pool + the outbox writer, never on api's other internals.
// Unlike the Run store it has NO command sealer — a grant carries no command (no
// secret-class material), so F3 does not apply.
//
// T-063 lands the schema + store (Create/Get/List/Revoke) + the issuance routes. The
// plan-vs-grant validation (T-065) and the revoke→cancel-in-flight-runs cascade (T-066)
// follow.
package grant

import "time"

// Grant lifecycle statuses (ADR-0062). "expired" is COMPUTED (now > ExpiresAt), not a
// stored status — plan-vs-grant (T-065) checks expiry live, so no sweeper is needed.
const (
	StatusActive  = "active"
	StatusRevoked = "revoked"
)

// Grant is an operator-issued delegation: agent Agent may launch governed Runs targeting
// Resources with Verbs, until ExpiresAt or revocation, within the bounds (ADR-0062).
type Grant struct {
	ID                string
	Principal         string    // the delegating operator (the lent authority + owner)
	Agent             string    // the grantee agent subject
	Label             string    // operator-facing name
	Resources         []string  // granted resource ids
	Verbs             []string  // granted operations (e.g. "exec")
	ExpiresAt         time.Time // TTL boundary; expiry is computed at plan-vs-grant
	MaxStepsPerRun    int
	MaxConcurrentRuns int
	StepBudget        int // total steps across all the grant's Runs (cap)
	// BudgetWindowSeconds windows the step budget (WS-4, ADR-0063 §3): when set (> 0),
	// StepBudget counts only planned steps of Runs created within the last window
	// (rolling, strict — enforced at T-073); 0 = NULL = the v0.6 cumulative-forever
	// semantics unchanged. Charter grants set it; agent grants may leave it 0.
	BudgetWindowSeconds int
	Status              string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// Input is a new grant (the issuing operator is the Principal; ExpiresAt is resolved by
// the handler from a TTL). BudgetWindowSeconds 0 = unwindowed (the v0.6 semantics).
type Input struct {
	Principal           string
	Agent               string
	Label               string
	Resources           []string
	Verbs               []string
	ExpiresAt           time.Time
	MaxStepsPerRun      int
	MaxConcurrentRuns   int
	StepBudget          int
	BudgetWindowSeconds int
}
