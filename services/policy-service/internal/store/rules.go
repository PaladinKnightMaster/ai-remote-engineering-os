package store

// rules.go is the policy_rules access path (ADR-0074, T-148): create/list/
// get/enable-disable/delete on an operator-authored rule over the
// (subject, edge, action, target) tuple. Since T-151c (AUD-40) the four
// mutating operations — create, enable, disable, delete — each chain a
// policy.rule:* event on policy_outbox in the SAME transaction as the
// mutation, the identical shape store.go's CreatePending/Resolve already
// establish: begin tx -> mutate -> s.writer.Enqueue(ctx, tx, ...) -> commit.
// Authoring, enabling, disabling or deleting a rule changes what the system
// will allow or deny (ADR-0073 §1, ADR-0012), so it belongs on the one
// signed ledger exactly like a decision does. ListRules/GetRule stay plain
// reads — no event, nothing changed.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"aireos.local/packages/go/outbox"
)

// Wildcard is the literal sentinel a rule column holds to mean "any value" —
// deliberately not NULL (ADR-0074; see 0002_policy_rules.up.sql's comment on
// why NULL-as-wildcard was rejected).
const Wildcard = "*"

// Rule outcomes. All three alias the store.go decision constants — DecisionDeny
// was added at T-150, when the evaluator this rule vocabulary feeds was
// actually wired into the decide path (the CHECK constraint has accepted
// "deny" since T-148, so no second migration was needed).
const (
	RuleOutcomeAllow            = DecisionAllow
	RuleOutcomeRequiresApproval = DecisionRequiresApproval
	RuleOutcomeDeny             = DecisionDeny
)

// ErrRuleNotFound is returned when a rule id has no matching row.
var ErrRuleNotFound = errors.New("policy rule not found")

// ErrDuplicateRule is returned when a create collides with the existing
// UNIQUE (subject, edge, action, target) tuple — one rule per exact match.
var ErrDuplicateRule = errors.New("a rule already exists for this (subject, edge, action, target) tuple")

// ErrInvalidRule is returned when the input fails Go-side validation before
// any query is issued — an empty field (use Wildcard, not "") or an outcome
// outside the three decision values.
type ErrInvalidRule struct{ Reason string }

func (e ErrInvalidRule) Error() string { return "invalid policy rule: " + e.Reason }

// Outbox outcomes for rule-authorship events (AUD-40; ADR-0073 §1, ADR-0012).
// Colon-suffixed house style, matching store.go's policy.decision:allow /
// approval.denied:operator.
const (
	OutcomeRuleCreated  = "policy.rule:created"
	OutcomeRuleEnabled  = "policy.rule:enabled"
	OutcomeRuleDisabled = "policy.rule:disabled"
	OutcomeRuleDeleted  = "policy.rule:deleted"
)

// ruleAnchor is a rule-mutation event's evidencePacketId. A rule is neither a
// session (policy/connector's session:<id>) nor an invocation (the gateway's
// invocation:<id>) — it is its own kind of audited object, so it gets its own
// anchor namespace.
func ruleAnchor(id string) string { return "rule:" + id }

// ruleTupleTarget packs the tuple components the outbox Event's fixed fields
// don't otherwise carry into ONE string on Target: subject, action, target,
// and the rule's own outcome (allow/requires_approval/deny — NOT this
// event's own policy.rule:* Outcome). Event.Edge already carries the tuple's
// edge component (Edge holds a capability-edge value everywhere else it's
// used, and a rule's edge column IS one), so it is not repeated here. A
// deleted rule's row is gone from policy_rules afterward, so for
// OutcomeRuleDeleted this composite is the ONLY place the tuple survives for
// offline verification (ADR-0073 §1) — which is why DeleteRule captures it
// via DELETE ... RETURNING in the same statement as the delete, not a
// preceding SELECT.
func ruleTupleTarget(r Rule) string {
	return fmt.Sprintf("subject=%s action=%s target=%s outcome=%s", r.Subject, r.Action, r.Target, r.Outcome)
}

// ruleEvent builds the outbox.Event for a rule mutation. actor is the
// operator who performed the mutation (T-151c: one uniform "actor" concept
// across create/enable/disable/delete, replacing the create-only, never-
// consumed "authored_by" T-151b introduced).
func ruleEvent(actor string, r Rule, outcome string) outbox.Event {
	return outbox.Event{
		Timestamp: time.Now(), Actor: actor, Edge: r.Edge,
		Target: ruleTupleTarget(r), Outcome: outcome,
		EvidencePacketID: ruleAnchor(r.ID),
	}
}

const ruleCols = `id, label, subject, edge, action, target, outcome, enabled, created_at, updated_at`

// Rule is a policy_rules row.
type Rule struct {
	ID        string
	Label     string
	Subject   string
	Edge      string
	Action    string
	Target    string
	Outcome   string
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreateRuleInput is the create-rule payload. Enabled defaults to true —
// callers wanting a disabled rule call SetRuleEnabled after create (keeps the
// insert path single-purpose). Actor is the operator who authored the rule
// (AUD-40, T-151c) — chained on policy.rule:created, never validated
// non-empty here (that belongs at the HTTP boundary, rules_handler.go); the
// store stays usable from evaluator/fixture tests that don't care about
// attribution.
type CreateRuleInput struct {
	Label   string
	Subject string
	Edge    string
	Action  string
	Target  string
	Outcome string
	Actor   string
}

func validateRuleInput(subject, edge, action, target, outcome string) error {
	if subject == "" || edge == "" || action == "" || target == "" {
		return ErrInvalidRule{Reason: "subject, edge, action, target must not be empty — use \"" + Wildcard + "\" for wildcard, not \"\""}
	}
	switch outcome {
	case RuleOutcomeAllow, RuleOutcomeRequiresApproval, RuleOutcomeDeny:
	default:
		return ErrInvalidRule{Reason: fmt.Sprintf("outcome %q is not one of allow, requires_approval, deny", outcome)}
	}
	return nil
}

// CreateRule validates in Go, then inserts a new rule and chains
// policy.rule:created in the same transaction (AUD-40). ErrInvalidRule on a
// bad field before any query (and before any transaction is opened);
// ErrDuplicateRule if the (subject, edge, action, target) tuple already has
// a rule — the insert's own conflict, so nothing is enqueued.
func (s *Store) CreateRule(ctx context.Context, in CreateRuleInput) (Rule, error) {
	if err := validateRuleInput(in.Subject, in.Edge, in.Action, in.Target, in.Outcome); err != nil {
		return Rule{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Rule{}, fmt.Errorf("mint id: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Rule{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	var r Rule
	err = scanRule(tx.QueryRow(ctx, `
		INSERT INTO policy_rules (id, label, subject, edge, action, target, outcome)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+ruleCols,
		id.String(), in.Label, in.Subject, in.Edge, in.Action, in.Target, in.Outcome), &r)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Rule{}, ErrDuplicateRule
		}
		return Rule{}, fmt.Errorf("insert rule: %w", err)
	}
	if _, err := s.writer.Enqueue(ctx, tx, ruleEvent(in.Actor, r, OutcomeRuleCreated)); err != nil {
		return Rule{}, fmt.Errorf("enqueue rule created: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Rule{}, fmt.Errorf("commit rule created: %w", err)
	}
	return r, nil
}

// ListRules returns every rule, newest first. No enabled/disabled filter —
// callers wanting only live rules apply that themselves (the evaluator, T-149,
// uses policy_rules_enabled_idx directly instead of this method).
func (s *Store) ListRules(ctx context.Context) ([]Rule, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+ruleCols+` FROM policy_rules ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list rules: %w", err)
	}
	defer rows.Close()
	var out []Rule
	for rows.Next() {
		var r Rule
		if err := scanRule(rows, &r); err != nil {
			return nil, fmt.Errorf("scan rule: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// GetRule returns one rule by id, or ErrRuleNotFound.
func (s *Store) GetRule(ctx context.Context, id string) (Rule, error) {
	var r Rule
	err := scanRule(s.pool.QueryRow(ctx, `SELECT `+ruleCols+` FROM policy_rules WHERE id = $1`, id), &r)
	if errors.Is(err, pgx.ErrNoRows) || isInvalidTextRepresentation(err) {
		return Rule{}, ErrRuleNotFound
	}
	if err != nil {
		return Rule{}, fmt.Errorf("get rule: %w", err)
	}
	return r, nil
}

// SetRuleEnabled flips a rule's enabled flag, bumps updated_at, and chains
// policy.rule:enabled or policy.rule:disabled in the same transaction
// (AUD-40). ErrRuleNotFound if the id doesn't exist — including a malformed
// one — and in that case NOTHING is enqueued (the transaction rolls back).
func (s *Store) SetRuleEnabled(ctx context.Context, id string, enabled bool, actor string) (Rule, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Rule{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var r Rule
	err = scanRule(tx.QueryRow(ctx, `
		UPDATE policy_rules SET enabled = $2, updated_at = now()
		 WHERE id = $1
		RETURNING `+ruleCols, id, enabled), &r)
	if errors.Is(err, pgx.ErrNoRows) || isInvalidTextRepresentation(err) {
		return Rule{}, ErrRuleNotFound
	}
	if err != nil {
		return Rule{}, fmt.Errorf("set rule enabled: %w", err)
	}
	outcome := OutcomeRuleDisabled
	if enabled {
		outcome = OutcomeRuleEnabled
	}
	if _, err := s.writer.Enqueue(ctx, tx, ruleEvent(actor, r, outcome)); err != nil {
		return Rule{}, fmt.Errorf("enqueue rule %s: %w", outcome, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Rule{}, fmt.Errorf("commit rule enabled: %w", err)
	}
	return r, nil
}

// DeleteRule removes a rule by id and chains policy.rule:deleted in the same
// transaction (AUD-40). ErrRuleNotFound if the id doesn't exist — including a
// malformed (non-UUID) id, which definitionally matches no row — and in
// that case NOTHING is enqueued. Uses DELETE ... RETURNING rather than a
// preceding SELECT + DELETE: the row's tuple is captured atomically at the
// moment it stops existing, which is the only way ruleTupleTarget has
// anything to read (the row is gone from policy_rules right after).
func (s *Store) DeleteRule(ctx context.Context, id, actor string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var r Rule
	err = scanRule(tx.QueryRow(ctx, `DELETE FROM policy_rules WHERE id = $1 RETURNING `+ruleCols, id), &r)
	if errors.Is(err, pgx.ErrNoRows) || isInvalidTextRepresentation(err) {
		return ErrRuleNotFound
	}
	if err != nil {
		return fmt.Errorf("delete rule: %w", err)
	}
	if _, err := s.writer.Enqueue(ctx, tx, ruleEvent(actor, r, OutcomeRuleDeleted)); err != nil {
		return fmt.Errorf("enqueue rule deleted: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit rule deleted: %w", err)
	}
	return nil
}

// isInvalidTextRepresentation reports whether err is Postgres SQLSTATE 22P02
// ("invalid text representation") — what a non-UUID string against a uuid
// column raises. CON-7: a malformed rule id must not surface as a 500, and a
// value Postgres cannot even parse as a uuid definitionally matches no row,
// so it maps to the same ErrRuleNotFound as a well-formed-but-absent id
// (the CreateRule/23505->ErrDuplicateRule precedent above for mapping a
// specific pg error code to a typed store error).
func isInvalidTextRepresentation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "22P02"
}

func scanRule(row scanner, r *Rule) error {
	return row.Scan(&r.ID, &r.Label, &r.Subject, &r.Edge, &r.Action, &r.Target,
		&r.Outcome, &r.Enabled, &r.CreatedAt, &r.UpdatedAt)
}
