package store

// rules.go is the policy_rules access path (ADR-0074, T-148): create/list/
// get/enable-disable/delete on an operator-authored rule over the
// (subject, edge, action, target) tuple. STORE ONLY — no evaluator (T-149), no
// HTTP routes (T-151), no audit events (T-150). Rules produce no outbox writes
// yet, so unlike store.go's methods these are plain (non-transactional) calls.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
// insert path single-purpose).
type CreateRuleInput struct {
	Label   string
	Subject string
	Edge    string
	Action  string
	Target  string
	Outcome string
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

// CreateRule validates in Go, then inserts a new rule. ErrInvalidRule on a bad
// field before any query; ErrDuplicateRule if the (subject, edge, action,
// target) tuple already has a rule.
func (s *Store) CreateRule(ctx context.Context, in CreateRuleInput) (Rule, error) {
	if err := validateRuleInput(in.Subject, in.Edge, in.Action, in.Target, in.Outcome); err != nil {
		return Rule{}, err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Rule{}, fmt.Errorf("mint id: %w", err)
	}
	var r Rule
	err = scanRule(s.pool.QueryRow(ctx, `
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

// SetRuleEnabled flips a rule's enabled flag and bumps updated_at.
// ErrRuleNotFound if the id doesn't exist.
func (s *Store) SetRuleEnabled(ctx context.Context, id string, enabled bool) (Rule, error) {
	var r Rule
	err := scanRule(s.pool.QueryRow(ctx, `
		UPDATE policy_rules SET enabled = $2, updated_at = now()
		 WHERE id = $1
		RETURNING `+ruleCols, id, enabled), &r)
	if errors.Is(err, pgx.ErrNoRows) || isInvalidTextRepresentation(err) {
		return Rule{}, ErrRuleNotFound
	}
	if err != nil {
		return Rule{}, fmt.Errorf("set rule enabled: %w", err)
	}
	return r, nil
}

// DeleteRule removes a rule by id. ErrRuleNotFound if the id doesn't exist —
// including a malformed (non-UUID) id, which definitionally matches no row.
func (s *Store) DeleteRule(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM policy_rules WHERE id = $1`, id)
	if isInvalidTextRepresentation(err) {
		return ErrRuleNotFound
	}
	if err != nil {
		return fmt.Errorf("delete rule: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrRuleNotFound
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
