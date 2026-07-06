// launch.go — the SHARED bounded-launch core (ADR-0063 §4; SEC-30a, T-072): plan-vs-grant
// validation (SEC-29) + the PERF-7 bounds + the idempotent create/re-attach, extracted
// behavior-preservingly from the gateway-forward handler (server.LaunchDelegatedRun) so
// its second caller — the T-073 workstream ticker — enforces IDENTICAL checks by
// construction. One validator, two callers; a second diverging launch path is a release
// blocker (the v0.7 criteria).
//
// The refusal REASON strings are wire contract: the gateway surfaces them to the agent
// ("run launch refused: <reason>") and the live e2e asserts their substrings — change one
// and the gate breaks by design.
package run

import (
	"context"
	"errors"
	"fmt"
	"log"
	"slices"
	"time"

	"aireos.local/services/api/internal/grant"
)

// MaxActiveRunsPerOperator caps an operator's concurrent active Runs (PERF-6; moved here
// from the server package at T-072 — the engine owns its own global safety ceiling). Both
// launch paths score against it: the operator's direct CreateRun, and — cap layering,
// PERF-7 — a delegated/scheduled Run, whose runs.operator is the grant's principal.
const MaxActiveRunsPerOperator = 10

// remoteExecVerb is the operation every Run step performs; plan-vs-grant checks it is
// within the grant's verbs (ADR-0062, SEC-29).
const remoteExecVerb = "exec"

// GrantSource is the slice of the grant store the launch core reads (and deny-audits)
// against. Satisfied by *grant.Store and by the server's grantStore interface.
type GrantSource interface {
	Get(ctx context.Context, id string) (grant.Grant, error)
	AuditLaunchDenied(ctx context.Context, agent, grantID string) error
}

// RefusalKind types a launch refusal so BOTH callers map it without string-matching
// (ADR-0063; the T-072 consult's load-bearing element).
type RefusalKind string

const (
	// RefusalOutOfGrant — the plan/agent is outside the grant (unknown grant, wrong
	// agent, verb/resource out of scope, oversize plan). HTTP 403; a ticker tick should
	// never see it (charters are validated at consent).
	RefusalOutOfGrant RefusalKind = "out_of_grant"
	// RefusalGrantDead — the grant is revoked or expired. HTTP 403 (unchanged wire
	// behavior); the ticker's SELF-RETIRE trigger (v0.7 criteria F1, T-073).
	RefusalGrantDead RefusalKind = "grant_dead"
	// RefusalBounds — a transient bound is hit (per-grant concurrency, the per-operator
	// ceiling, the step budget). HTTP 429; the ticker audits + re-fires next interval
	// (a bounds-refused tick never wedges or retires a charter).
	RefusalBounds RefusalKind = "bounds"
)

// Refusal is a typed, audited launch refusal. Reason is the wire-contract string (see
// the package comment).
type Refusal struct {
	Kind   RefusalKind
	Reason string
}

// validatePlanAgainstGrant returns nil when the plan is within the grant, else the typed
// refusal (SEC-29). Pure — unit-tested without HTTP/PG. Check order (and every reason
// string) is preserved verbatim from the pre-T-072 server-side validator.
func validatePlanAgainstGrant(g grant.Grant, agentSubject string, resourceIDs []string) *Refusal {
	switch {
	case g.Agent != agentSubject:
		return &Refusal{RefusalOutOfGrant, "the grant does not authorize this agent"}
	case g.Status != grant.StatusActive:
		return &Refusal{RefusalGrantDead, "the grant is not active"}
	case !time.Now().Before(g.ExpiresAt):
		return &Refusal{RefusalGrantDead, "the grant has expired"}
	case !slices.Contains(g.Verbs, remoteExecVerb):
		return &Refusal{RefusalOutOfGrant, "the grant does not authorize remote execution"}
	case len(resourceIDs) > g.MaxStepsPerRun:
		return &Refusal{RefusalOutOfGrant, "the plan exceeds the grant's maximum steps per run"}
	}
	for _, rid := range resourceIDs {
		if !slices.Contains(g.Resources, rid) {
			return &Refusal{RefusalOutOfGrant, "a step targets a resource outside the grant"}
		}
	}
	return nil
}

// LaunchBounded launches a grant-bounded Run through the ONE shared enforcement path
// (SEC-30a): fetch + plan-vs-grant (SEC-29) → early re-attach → PERF-7 bounds →
// idempotent create. Every refusal is deny-audited (run.launch.denied) here, so both
// callers inherit the discipline. Returns exactly one of: a Run (reattached=true when an
// identical prior launch was found), a *Refusal, or an error (infra — the caller answers
// 500 / logs and retries next tick).
//
// Ordering (the T-072 audit's F3): the launch-key re-attach sits AFTER plan-vs-grant —
// a retry under a dead grant still refuses (and self-retires a charter) — but BEFORE the
// bounds, so a retry re-attaches to the Run it already created even when that Run
// exhausted the budget (the ratified early-re-attach delta; the unique
// (grant_id, launch_key) index stays the backstop for concurrent same-key races).
func (s *Store) LaunchBounded(ctx context.Context, grants GrantSource, agentSubject, grantID, launchKey string, steps []StepInput) (Run, bool, *Refusal, error) {
	g, err := grants.Get(ctx, grantID)
	if errors.Is(err, grant.ErrNotFound) {
		return Run{}, false, denyAudited(ctx, grants, agentSubject, grantID,
			&Refusal{RefusalOutOfGrant, "no such grant for this agent"}), nil
	}
	if err != nil {
		return Run{}, false, nil, fmt.Errorf("load grant: %w", err)
	}

	resourceIDs := make([]string, 0, len(steps))
	for _, st := range steps {
		resourceIDs = append(resourceIDs, st.ResourceID)
	}
	if r := validatePlanAgainstGrant(g, agentSubject, resourceIDs); r != nil {
		return Run{}, false, denyAudited(ctx, grants, agentSubject, grantID, r), nil
	}

	// Early re-attach (idempotency fast path).
	if launchKey != "" {
		existing, err := s.GetByLaunchKey(ctx, g.ID, launchKey)
		if err == nil {
			return existing, true, nil, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return Run{}, false, nil, err
		}
	}

	// PERF-7 bounds — non-atomic soft ceilings (the documented v0.6 semantics).
	active, err := s.CountActiveForGrant(ctx, g.ID)
	if err != nil {
		return Run{}, false, nil, err
	}
	if active >= g.MaxConcurrentRuns {
		return Run{}, false, denyAudited(ctx, grants, agentSubject, grantID,
			&Refusal{RefusalBounds, "the grant's concurrent-run limit is reached"}), nil
	}
	// Cap layering (PERF-7): the Run's operator is the principal, so it also counts
	// against the global per-operator safety ceiling.
	opActive, err := s.CountActiveForOperator(ctx, g.Principal)
	if err != nil {
		return Run{}, false, nil, err
	}
	if opActive >= MaxActiveRunsPerOperator {
		return Run{}, false, denyAudited(ctx, grants, agentSubject, grantID,
			&Refusal{RefusalBounds, "too many active runs for this operator"}), nil
	}
	consumed, err := s.CountStepsForGrant(ctx, g.ID)
	if err != nil {
		return Run{}, false, nil, err
	}
	if consumed+len(steps) > g.StepBudget {
		return Run{}, false, denyAudited(ctx, grants, agentSubject, grantID,
			&Refusal{RefusalBounds, "the grant's step budget is exhausted"}), nil
	}

	created, err := s.Create(ctx, Input{
		Operator: g.Principal, Actor: agentSubject, GrantID: g.ID,
		LaunchKey: launchKey, Steps: steps,
	})
	if errors.Is(err, ErrDuplicateLaunch) {
		// A concurrent same-key launch won the unique index — re-attach (the race
		// backstop behind the early fast path).
		existing, gerr := s.GetByLaunchKey(ctx, g.ID, launchKey)
		if gerr != nil {
			return Run{}, false, nil, gerr
		}
		return existing, true, nil, nil
	}
	if err != nil {
		return Run{}, false, nil, err
	}
	return created, false, nil, nil
}

// denyAudited chains run.launch.denied for a refusal (N1: denied + audited, no egress)
// and passes the refusal through. Best-effort — the launch is refused regardless; a
// failed enqueue surfaces in the log, matching the pre-T-072 handler behavior.
func denyAudited(ctx context.Context, grants GrantSource, agent, grantID string, r *Refusal) *Refusal {
	if err := grants.AuditLaunchDenied(ctx, agent, grantID); err != nil {
		log.Printf("audit run.launch.denied (grant %s): %v", grantID, err)
	}
	return r
}
