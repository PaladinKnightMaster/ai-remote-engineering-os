-- services/api adaptive-workstream schema, migration 0013 (ADR-0065; v0.9.0 "Adaptive
-- Workstreams"). Four additive deltas (T-087), discharging ADR-0064 §Deferred's fused
-- agent-memory/memory-execution trigger together with ADR-0063 §Deferred's
-- agent-composed-plans item. Standard mutable service (only audit-service is
-- forward-only, ADR-0044) — a destructive down is permitted.

-- (1) MEM-6 schema half: the share flip's column (ADR-0065 §3). Default false —
-- operator-authored content is invisible to the agent plane until explicitly shared
-- (the audited memory.shared/memory.unshared pair, T-091). Accrued reference rows
-- carry no content to share; the column applies uniformly (harmless default there).
ALTER TABLE memories ADD COLUMN agent_visible BOOLEAN NOT NULL DEFAULT false;

-- (2) WS-5: the additive adaptive charter kind (ADR-0065 §4). Existing rows backfill
-- 'template' (DEFAULT), so v0.7/v0.8 charters are byte-behavior-preserved. An adaptive
-- charter names its composer at consent (R2: the auto-issued grant's grantee); a
-- template charter carries none — the CHECK below makes composer-presence the kind
-- discriminant's structural mirror (adaptive requires a composer; template requires
-- none), not a convention the store has to uphold alone.
ALTER TABLE workstreams ADD COLUMN kind TEXT NOT NULL DEFAULT 'template';
ALTER TABLE workstreams ADD CONSTRAINT workstreams_kind_chk CHECK (kind IN ('template', 'adaptive'));
ALTER TABLE workstreams ADD COLUMN composer TEXT NOT NULL DEFAULT '';
ALTER TABLE workstreams ADD CONSTRAINT workstreams_composer_chk
    CHECK (kind = 'template' OR composer <> '');

-- (3) PLAN-1: the inbound plan-request channel (ADR-0065 §5). id is UUID v7, minted in
-- Go like every other store (never a DB default — the house convention). launch_key is
-- the tick's dedup carrier (mirrors runs.launch_key); UNIQUE makes a same-tick retry a
-- no-op re-attach, never a duplicate row. run_id is set only on submit (plan.submit,
-- T-090 → the `proposed` Run, RUN-6), NULL until then. expires_at is the TTL a request
-- opens with (charter-interval-derived, C2); ExpirePlanRequestsDue sweeps past-due open
-- rows so an unanswered request never wedges the charter.
CREATE TABLE plan_requests (
    id            UUID        PRIMARY KEY,                  -- UUID v7
    workstream_id UUID        NOT NULL REFERENCES workstreams (id) ON DELETE CASCADE,
    launch_key    TEXT        NOT NULL UNIQUE,               -- the tick dedup carrier
    status        TEXT        NOT NULL DEFAULT 'open',
    run_id        UUID,                                      -- set on submit; NULL until then
    expires_at    TIMESTAMPTZ NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT plan_requests_status_chk CHECK (status IN ('open', 'submitted', 'expired', 'closed'))
);

-- At most ONE open request per charter — STRUCTURAL (C2), not application discipline: a
-- second OpenPlanRequest attempt for an already-open charter cannot insert, full stop.
CREATE UNIQUE INDEX plan_requests_one_open_idx ON plan_requests (workstream_id) WHERE status = 'open';

-- The expiry sweep's scan (ExpirePlanRequestsDue, DUR-10) — the workstreams_due_idx
-- partial-index precedent: cheap as submitted/expired/closed rows accumulate.
CREATE INDEX plan_requests_expiry_idx ON plan_requests (expires_at) WHERE status = 'open';

-- (4) RUN-6 (F-a): admit the pre-'pending' `proposed` Run status — a submitted plan
-- parks here before any step executes (the consent successor, ADR-0065 §6). Postgres
-- has no ALTER CHECK, so the constraint is dropped and recreated under the SAME name —
-- every existing reference to runs_status_chk keeps naming one constraint.
ALTER TABLE runs DROP CONSTRAINT runs_status_chk;
ALTER TABLE runs ADD CONSTRAINT runs_status_chk
    CHECK (status IN ('proposed', 'pending', 'running', 'completed', 'failed', 'canceled'));
