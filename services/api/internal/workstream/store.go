package workstream

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"aireos.local/packages/go/outbox"
	"aireos.local/services/api/internal/grant"
)

// ErrNotFound is returned when no workstream has the given id.
var ErrNotFound = errors.New("workstream not found")

// charterEdge is the bracket edge for workstream lifecycle events — chartering (and
// pausing/retiring, T-074) is a human-agent authorization act (ADR-0063 §2: the ONE
// consent act, paired with the grant's own grant.issued).
const charterEdge = "human-agent"

// CommandSealer seals/opens the operator-authored template command at rest — the same
// contract as the run store's (F3 parity); a nil sealer is the plaintext dev/transition
// fallback, matching the run store's current wiring so both flip together when the key
// is provisioned.
type CommandSealer interface {
	Seal(plaintext string) ([]byte, error)
	Open(b []byte) (string, error)
}

// GrantCreator issues the charter's delegation grant INSIDE the charter transaction
// (WS-2 atomicity). Satisfied by *grant.Store (CreateInTx).
type GrantCreator interface {
	CreateInTx(ctx context.Context, tx pgx.Tx, in grant.Input) (grant.Grant, error)
}

// selectCols is the shared column list for Get/List (kept in one place so the scan
// order matches).
const selectCols = `id, principal, label, objective, grant_id, interval_seconds,
	next_fire_at, status, created_at, updated_at`

// Store persists workstreams + workstream_steps. It takes the pgx pool directly (the
// internal/run pattern) so the package stays extraction-ready.
type Store struct {
	pool   *pgxpool.Pool
	sealer CommandSealer
	grants GrantCreator
	writer outbox.Writer
}

// NewStore builds the workstream store. sealer may be nil (plaintext fallback — see
// CommandSealer). grants issues the charter's grant inside the charter tx (WS-2).
// outboxTable is the api outbox table (e.g. "api_outbox") into which Create enqueues
// workstream.chartered in the same transaction.
func NewStore(pool *pgxpool.Pool, sealer CommandSealer, grants GrantCreator, outboxTable string) *Store {
	return &Store{pool: pool, sealer: sealer, grants: grants, writer: outbox.Writer{Table: outboxTable}}
}

func (s *Store) seal(cmd string) ([]byte, error) {
	if s.sealer == nil {
		return []byte(cmd), nil
	}
	return s.sealer.Seal(cmd)
}

func (s *Store) open(b []byte) (string, error) {
	if s.sealer == nil {
		return string(b), nil
	}
	return s.sealer.Open(b)
}

// Create charters a Workstream: ONE transaction inserts the workstream + its immutable
// template steps (sealed) + the charter's auto-issued delegation grant (grantee =
// workstream:<id>, overridden here so a caller can never bind a foreign grantee) and
// enqueues workstream.chartered — the grant's own CreateInTx enqueues grant.issued in
// the SAME tx (WS-2: the one consent act is atomically audited as both events, or
// nothing exists). First fire = now + interval (no immediate fire, ADR-0063 §5).
func (s *Store) Create(ctx context.Context, in Input) (Workstream, error) {
	if in.Principal == "" || in.Objective == "" {
		return Workstream{}, errors.New("workstream requires a principal and an objective")
	}
	if in.IntervalSeconds <= 0 {
		return Workstream{}, errors.New("workstream interval must be positive")
	}
	if len(in.Steps) == 0 {
		return Workstream{}, errors.New("workstream requires at least one template step")
	}
	for i, st := range in.Steps {
		if st.ResourceID == "" || st.Command == "" {
			return Workstream{}, fmt.Errorf("template step %d requires resourceId and command", i)
		}
	}
	if s.grants == nil {
		return Workstream{}, errors.New("workstream store has no grant creator")
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Workstream{}, fmt.Errorf("mint workstream id: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Workstream{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op once committed

	// The charter's grant, in the SAME tx (WS-2). Grantee/principal are bound here —
	// never caller-supplied (the SEC-26 posture: the grant is server-side authority).
	gin := in.Grant
	gin.Principal = in.Principal
	gin.Agent = "workstream:" + id.String()
	g, err := s.grants.CreateInTx(ctx, tx, gin)
	if err != nil {
		return Workstream{}, fmt.Errorf("issue charter grant: %w", err)
	}

	var nextFireAt time.Time
	var createdAt, updatedAt time.Time
	if err := tx.QueryRow(ctx, `
		INSERT INTO workstreams (id, principal, label, objective, grant_id, interval_seconds, next_fire_at)
		VALUES ($1, $2, $3, $4, $5, $6::int, now() + $6::int * interval '1 second')
		RETURNING next_fire_at, created_at, updated_at`,
		id.String(), in.Principal, in.Label, in.Objective, g.ID, in.IntervalSeconds).
		Scan(&nextFireAt, &createdAt, &updatedAt); err != nil {
		return Workstream{}, fmt.Errorf("insert workstream: %w", err)
	}

	steps := make([]Step, len(in.Steps))
	for i, st := range in.Steps {
		sealed, err := s.seal(st.Command)
		if err != nil {
			return Workstream{}, fmt.Errorf("seal template step %d: %w", i, err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO workstream_steps (workstream_id, step_index, resource_id, command)
			VALUES ($1, $2, $3, $4)`,
			id.String(), i, st.ResourceID, sealed); err != nil {
			return Workstream{}, fmt.Errorf("insert template step %d: %w", i, err)
		}
		steps[i] = Step{Index: i, ResourceID: st.ResourceID, Command: st.Command}
	}

	// workstream.chartered in the SAME transaction — content-free (the objective and the
	// template stay off the chain, ADR-0049); the workstream:<id> anchor pairs with the
	// grant's grant.issued from CreateInTx above (AUD-18).
	if _, err := s.writer.Enqueue(ctx, tx, outbox.Event{
		Timestamp: time.Now(), Actor: in.Principal, Edge: charterEdge,
		Target: "workstream:" + id.String(), Outcome: "workstream.chartered",
		EvidencePacketID: "workstream:" + id.String(),
	}); err != nil {
		return Workstream{}, fmt.Errorf("enqueue workstream.chartered: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return Workstream{}, fmt.Errorf("commit workstream: %w", err)
	}
	return Workstream{
		ID: id.String(), Principal: in.Principal, Label: in.Label, Objective: in.Objective,
		GrantID: g.ID, IntervalSeconds: in.IntervalSeconds, NextFireAt: nextFireAt,
		Status: StatusActive, CreatedAt: createdAt, UpdatedAt: updatedAt, Steps: steps,
	}, nil
}

// Get returns a workstream with its template (opened), or ErrNotFound.
func (s *Store) Get(ctx context.Context, id string) (Workstream, error) {
	var w Workstream
	err := s.pool.QueryRow(ctx, `SELECT `+selectCols+` FROM workstreams WHERE id = $1`, id).
		Scan(&w.ID, &w.Principal, &w.Label, &w.Objective, &w.GrantID, &w.IntervalSeconds,
			&w.NextFireAt, &w.Status, &w.CreatedAt, &w.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Workstream{}, ErrNotFound
	}
	if err != nil {
		return Workstream{}, fmt.Errorf("get workstream: %w", err)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT step_index, resource_id, command FROM workstream_steps
		 WHERE workstream_id = $1 ORDER BY step_index ASC`, id)
	if err != nil {
		return Workstream{}, fmt.Errorf("get template: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var st Step
		var sealed []byte
		if err := rows.Scan(&st.Index, &st.ResourceID, &sealed); err != nil {
			return Workstream{}, err
		}
		if st.Command, err = s.open(sealed); err != nil {
			return Workstream{}, fmt.Errorf("open template step %d: %w", st.Index, err)
		}
		w.Steps = append(w.Steps, st)
	}
	return w, rows.Err()
}

// ListForOperator returns up to limit of the principal's workstreams in id (≈ creation)
// order with id > after (after="" starts at the head), WITHOUT templates (the runs-list
// pattern). Owner-scoped. The cursor is the last id, or "".
func (s *Store) ListForOperator(ctx context.Context, principal, after string, limit int) ([]Workstream, string, error) {
	const cols = `SELECT ` + selectCols + ` FROM workstreams WHERE principal = $1`
	var (
		rows pgx.Rows
		err  error
	)
	if after == "" {
		rows, err = s.pool.Query(ctx, cols+` ORDER BY id ASC LIMIT $2`, principal, limit)
	} else {
		rows, err = s.pool.Query(ctx, cols+` AND id > $2 ORDER BY id ASC LIMIT $3`, principal, after, limit)
	}
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []Workstream
	var last string
	for rows.Next() {
		var w Workstream
		if err := rows.Scan(&w.ID, &w.Principal, &w.Label, &w.Objective, &w.GrantID, &w.IntervalSeconds,
			&w.NextFireAt, &w.Status, &w.CreatedAt, &w.UpdatedAt); err != nil {
			return nil, "", err
		}
		out = append(out, w)
		last = w.ID
	}
	return out, last, rows.Err()
}
