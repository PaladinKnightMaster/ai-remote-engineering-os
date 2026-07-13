-- services/api memories schema, migration 0012 (ADR-0064; v0.8.0 "Persistent Scoped
-- Memory"). api owns the memories table in the shared cockpit DB — the same own-tables
-- pattern as runs/run_steps, delegation_grants, and workstreams/workstream_steps. Memory
-- is the mutable, forgettable projection beside the immutable audit chain (ADR-0049):
-- the chain cannot redact, expire, or permission-scope what it records; memory can.
-- Standard mutable service (only audit-service is forward-only, ADR-0044) — a destructive
-- down is permitted.

-- memories: one row per accrued or authored memory (ADR-0064 §1). id (UUID v7,
-- time-ordered) is the memory:<id> audit anchor. scope_type/scope_id place the row in
-- the bounded scope set {workstream, run, operator} (R3). key is an operator-facing
-- name (""  for accrual — content-free pointers carry no naming). ref is the
-- content-free evidence pointer (run/step/invocation ids + evidence-packet linkage) —
-- NEVER command text or stdout bytes. content is NULLABLE: NULL for accrued rows (the
-- reference-based core, R1); set only for operator-authored free text, sealed at rest
-- via the same nil-safe CommandSealer the Run and Workstream stores use (F3 parity).
-- source_trace records what produced the row ("run:<id>:completed" / "operator").
-- confidence is recorded, never computed (an L3 concern, deferred). pinned exempts a
-- row from the expiry sweep (T-081). expires_at/redacted_at are the two forgetting
-- flips (T-081): redact erases content and tombstones the row once; expire tombstones
-- unpinned rows past expires_at. permission_scope is the owning operator — every read
-- is owner-scoped (GOV-7, the single-bootstrap-operator posture). No update-in-place of
-- content exists in the store: correction = redact + write (ADR-0064 §1).
CREATE TABLE memories (
    id                UUID        PRIMARY KEY,                  -- UUID v7
    scope_type        TEXT        NOT NULL,
    scope_id          TEXT        NOT NULL,
    key               TEXT        NOT NULL DEFAULT '',          -- operator-facing name
    ref               TEXT        NOT NULL DEFAULT '',          -- content-free evidence pointer
    content           BYTEA,                                    -- NULL for accrual; sealed at rest when set
    source_trace      TEXT        NOT NULL DEFAULT '',          -- what produced the row
    confidence        REAL        NOT NULL DEFAULT 1.0,         -- recorded, never computed
    pinned            BOOLEAN     NOT NULL DEFAULT false,
    expires_at        TIMESTAMPTZ,
    redacted_at       TIMESTAMPTZ,                               -- the tombstone flip (T-081)
    permission_scope  TEXT        NOT NULL,                     -- the owning operator
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT memories_scope_type_chk CHECK (scope_type IN ('workstream', 'run', 'operator'))
);

-- The scope read (GET /v1/memories?scope_type=&scope_id=, T-080) is the primary query
-- shape.
CREATE INDEX memories_scope_idx ON memories (scope_type, scope_id);

-- The operator memory view (owner-scoped list, T-080/082) is principal-scoped, like
-- runs/workstreams.
CREATE INDEX memories_owner_idx ON memories (permission_scope);

-- The expiry sweep's scan (T-081, DUR-9): only unpinned, not-yet-redacted rows with a
-- set expires_at are candidates — the partial index keeps the scan cheap as pinned and
-- already-tombstoned rows accumulate.
CREATE INDEX memories_expiry_idx ON memories (expires_at)
    WHERE expires_at IS NOT NULL AND redacted_at IS NULL AND NOT pinned;
