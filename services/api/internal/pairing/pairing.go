// Package pairing is services/api's one-time QR-pairing request store + revocable
// paired-session store (ADR-0068 §D4-§D6; v0.12 "Access & Onboarding", E-02). An
// authenticated desktop operator requests a pairing (Store.Create); a phone redeems
// the one-time secret (Store.Redeem) for a durable, revocable session narrowed to
// the approvals@1 scope. The paired session is the first revocable operator session
// in the product — ADR-0068's declared ADR-0046 divergence: api checks the
// paired_sessions row's liveness on every request rather than trusting the token's
// own stateless expiry alone.
//
// It is deliberately self-contained — the internal/grant precedent (ADR-0062): it
// depends only on the pgx pool + the outbox writer, never on api's other internals.
//
// T-113 (this package) is the data layer ONLY: migration 0016, the store, the
// single-use redeem transaction, and the approvals@1 scope definition below. It
// adds NO route, NO handler, and chains NO audit event. T-114 owns the four
// /v1/pairing-*/paired-sessions* routes and handlers, the per-request liveness
// enforcement, approvals@1 route-level enforcement, and the three pairing.* audit
// events (pairing.requested / pairing.redeemed / pairing.revoked) — added to the
// XxxInTx methods below without restructuring them (the T-058a→b lesson: an
// already-open transaction is a seam a later ticket can hook, not something it
// should have to recreate).
package pairing

import "time"

// ApprovalsScopeV1 is the sole paired-session scope (ADR-0068 §D5, SEC-36). Its
// membership is defined HERE — in exactly one place in services/api, never in the
// frontend, the database, or a migration (a CHECK constraint on
// paired_sessions.scope would put membership policy in the schema, which is
// precisely what SEC-36 forbids). Widening this scope is a criteria change and
// therefore requires a new ADR — do not extend ApprovalsV1Permit without one.
const ApprovalsScopeV1 = "approvals@1"

// ApprovalsV1Permit enumerates, verbatim from ADR-0068 §D5 and release criteria
// SEC-36, what approvals@1 permits: read pending approvals/invocations,
// approve/deny, read the audit surface, read its own session. T-114's enforcement
// layer is the consumer that matters; this slice is the documented, testable
// source of truth it reads from.
var ApprovalsV1Permit = []string{
	"read-pending-approvals",
	"read-pending-invocations",
	"approve",
	"deny",
	"read-audit",
	"read-own-session",
}

// ApprovalsV1Deny enumerates, verbatim from ADR-0068 §D5, the actions approvals@1
// denies (SEC-35's named refusals): launching Runs, issuing or revoking grants,
// registering or removing context sources, minting agent tokens, opening terminal
// sessions, and creating further pairings. This slice documents the denial set for
// tests and readers; T-114 enforces it by never wiring these actions into the
// approvals@1-gated route set, so a live paired session cannot reach them
// regardless of this slice's contents (no oracle from consulting it at runtime).
var ApprovalsV1Deny = []string{
	"launch-run",
	"issue-grant",
	"revoke-grant",
	"register-context-source",
	"remove-context-source",
	"mint-agent-token",
	"open-terminal-session",
	"create-pairing",
}

// Request is a one-time pairing request (ADR-0068 §D6) — the 120-second window a QR
// code stays valid for. Only its SHA-256 digest is ever persisted; the plaintext
// secret exists solely in Store.Create's return value, never logged or stored.
type Request struct {
	ID         string
	Operator   string // the requesting desktop operator
	Label      string // set by the desk at request time (T-113's consult, §6)
	ExpiresAt  time.Time
	ConsumedAt *time.Time // nil = unconsumed
	Attempts   int        // failed-redeem counter
	CreatedAt  time.Time
}

// CreateInput is the desk-side ask to start a pairing. Label is chosen by the
// REQUESTING operator, never by the redeeming device — a hostile redeemer must not
// get to name how their session appears in the operator's revoke list (T-113's
// consult, §6).
type CreateInput struct {
	Operator string
	Label    string
}

// Session is a durable, revocable paired operator session (ADR-0068 §D4) — the
// product's first revocable session. Liveness is COMPUTED (RevokedAt == nil &&
// ExpiresAt.After(now)), never a stored status; migration 0008's ruling for
// delegation_grants applies here too ("expired is COMPUTED ... so no sweeper is
// needed"), and here it is also what AUD-33 requires (no pairing.expired event, no
// expiry-sweep event may exist).
type Session struct {
	ID        string // the minted token's jti (PERF-12's single indexed PK lookup)
	Operator  string // the subject the session acts as
	Label     string // carried over from the request at redeem
	Scope     string // ApprovalsScopeV1, always
	ExpiresAt time.Time
	RevokedAt *time.Time // nil = live
	CreatedAt time.Time
}
