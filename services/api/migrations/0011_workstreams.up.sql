-- services/api workstreams schema, migration 0011 (ADR-0063; v0.7 "Chartered
-- Workstreams"). api owns the workstreams tables in the shared cockpit DB — the same
-- own-tables pattern as runs/run_steps and delegation_grants. A Workstream is a standing
-- operator-chartered objective whose IMMUTABLE plan template is fired on a fixed-interval
-- schedule by the in-process ticker (T-073); each tick creates a governed Run bounded by
-- the charter's own auto-issued delegation grant (grantee = workstream:<id>, 1:1, WS-2).
-- Standard mutable service (only audit-service is forward-only, ADR-0044) — a destructive
-- down is permitted.

-- workstreams: one charter (ADR-0063 §1). id (UUID v7, time-ordered) is the
-- workstream:<id> audit anchor + actor subject + the list cursor. principal is the
-- chartering operator (owner). grant_id binds the charter's auto-issued delegation grant
-- (1:1; the grant carries scope/TTL/bounds — they are NOT duplicated here). The schedule
-- is interval_seconds + a durable next_fire_at (restart-safe, DUR-8); first fire =
-- chartered_at + interval (no immediate fire). The template is immutable post-charter:
-- no UPDATE path exists in the store (WS-1) — only status + next_fire_at ever change.
CREATE TABLE workstreams (
    id               UUID        PRIMARY KEY,                  -- UUID v7
    principal        TEXT        NOT NULL,                     -- chartering operator (owner)
    label            TEXT        NOT NULL DEFAULT '',          -- operator-facing name (UI)
    objective        TEXT        NOT NULL,                     -- the chartered objective (reviewed at consent)
    grant_id         UUID        NOT NULL,                     -- the charter's auto-issued grant (1:1)
    interval_seconds INTEGER     NOT NULL CHECK (interval_seconds > 0),
    next_fire_at     TIMESTAMPTZ NOT NULL,                     -- durable schedule head (DUR-8)
    status           TEXT        NOT NULL DEFAULT 'active',
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT workstreams_status_chk CHECK (status IN ('active', 'paused', 'retired'))
);

-- workstream_steps: the immutable, ordered plan template (WS-1). command is sealed at
-- rest via the run-store CommandSealer (BYTEA; F3 parity — operator-authored command
-- material, same class as run_steps.command). Instantiated verbatim per tick (T-073).
CREATE TABLE workstream_steps (
    workstream_id UUID    NOT NULL REFERENCES workstreams (id) ON DELETE CASCADE,
    step_index    INTEGER NOT NULL CHECK (step_index >= 0),
    resource_id   TEXT    NOT NULL,
    command       BYTEA   NOT NULL,                            -- sealed at rest (F3 parity)
    PRIMARY KEY (workstream_id, step_index)
);

-- The operator's workstream list is principal-scoped (owner-only, like runs/grants).
CREATE INDEX workstreams_principal_idx ON workstreams (principal);

-- The ticker's claim scan (T-073) reads due active charters; the partial index keeps it
-- cheap as paused/retired rows accumulate.
CREATE INDEX workstreams_due_idx ON workstreams (next_fire_at) WHERE status = 'active';

-- WS-4 (ADR-0063 §3): the windowed step budget — an ADDITIVE, NULLABLE generalization of
-- the v0.6 cumulative step_budget. NULL = the v0.6 cumulative-forever semantics unchanged
-- (no existing grant changes meaning). When set, plan-vs-grant counts only planned steps
-- of the grant's Runs created within the last budget_window_seconds (rolling window,
-- strict inequality — enforced at T-073). Charter grants set it (default = the interval).
ALTER TABLE delegation_grants
    ADD COLUMN budget_window_seconds INTEGER CHECK (budget_window_seconds > 0);
