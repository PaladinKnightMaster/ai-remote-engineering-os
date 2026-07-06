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
// Agent (= "workstream:<id>", minted here) so a caller can never bind the charter to a
// foreign grantee. The handler validates the template against Grant's scope/bounds
// BEFORE Create (WS-2's 422 matrix); the store re-checks only well-formedness.
type Input struct {
	Principal       string
	Label           string
	Objective       string
	IntervalSeconds int
	Steps           []StepInput
	Grant           grant.Input
}
