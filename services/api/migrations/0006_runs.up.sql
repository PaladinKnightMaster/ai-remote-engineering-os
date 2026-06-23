-- services/api runs schema, migration 0006 (ADR-0061; v0.5.0 "Governed Workstreams",
-- the Workstream Engine's execution layer = Runs). api owns the runs + run_steps
-- tables in the shared cockpit DB (ADR-0048), the same own-tables pattern the other
-- services use. An operator authors an ordered, governed Run of steps; the
-- internal/run store persists it (T-054). The sequencer that drives each step through
-- the gateway (T-055b), the reclaimable single-driver claim columns (T-056), and the
-- run.* lifecycle events (reusing api_outbox, T-058) land in later tickets. Standard
-- mutable service (only audit-service is forward-only, ADR-0044) — a destructive down
-- is permitted.

-- runs: one operator-authored governed Run (ADR-0061). id (UUID v7, time-ordered) is
-- the run:<id> audit-correlation anchor + the list cursor. operator is the launching
-- actor (the subject threaded to policy + the audit actor on every step).
CREATE TABLE runs (
    id          UUID        PRIMARY KEY,                 -- UUID v7
    operator    TEXT        NOT NULL,                    -- launching operator subject (the actor)
    status      TEXT        NOT NULL DEFAULT 'pending',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT runs_status_chk
        CHECK (status IN ('pending', 'running', 'completed', 'failed', 'canceled'))
);

-- The sequencer's claim scan (T-056) filters launched-but-unfinished runs; a partial
-- index keeps it cheap as terminal rows accumulate.
CREATE INDEX runs_active_idx ON runs (status) WHERE status IN ('pending', 'running');

-- run_steps: the ordered steps of a Run. command is BYTEA — operator-authored and
-- potentially secret-bearing (it may carry inline credentials), so it is sealed at
-- rest for F3 parity with the gateway's invocations.command (ADR-0059); the store
-- seals on write / opens on read, with a nil cipher as the plaintext dev/transition
-- fallback until T-057 provisions the key. resource_id is opaque text (no FK): a plan
-- may reference any resource id, validated at launch/dispatch (fail-closed), the same
-- posture the gateway uses. invocation_id links the step to its gateway invocation
-- once dispatched (T-055b), NULL until then.
CREATE TABLE run_steps (
    run_id        UUID        NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    step_index    INT         NOT NULL,                  -- 0-based execution order
    resource_id   TEXT        NOT NULL,
    command       BYTEA       NOT NULL,                  -- sealed at rest (F3)
    invocation_id UUID,                                  -- gateway invocation once dispatched; NULL until then
    status        TEXT        NOT NULL DEFAULT 'pending',
    outcome       TEXT        NOT NULL DEFAULT '',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (run_id, step_index),
    CONSTRAINT run_steps_status_chk
        CHECK (status IN ('pending', 'running', 'completed', 'failed', 'denied', 'canceled'))
);
