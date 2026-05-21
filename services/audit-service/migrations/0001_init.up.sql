-- audit-service schema, migration 0001 (forward-only, ADR-0044).
-- The audit_events table participates in the chained-hash evidence chain
-- (ADR-0045); it is append-only and MUST NOT be altered destructively. There
-- is intentionally NO 0001_init.down.sql — see migrations/README.md.

CREATE TABLE audit_events (
    -- seq is the monotonic chain order; prev_hash of row N == chain_hash of N-1.
    seq                 BIGSERIAL PRIMARY KEY,
    -- server-assigned canonical event id (UUID v7).
    id                  UUID NOT NULL UNIQUE,
    -- producer-supplied dedup key (UUID v7, minted at outbox enqueue). The
    -- UNIQUE constraint is the at-least-once dedup enforcer (ADR-0045).
    idempotency_key     UUID NOT NULL UNIQUE,
    -- producer occurrence time, stored verbatim as TEXT so the value hashed
    -- into chain_hash is byte-identical on read (no timestamptz normalization).
    "timestamp"         TEXT NOT NULL,
    actor               TEXT NOT NULL,
    edge                TEXT NOT NULL,
    target              TEXT NOT NULL,
    outcome             TEXT,
    evidence_packet_id  TEXT,
    -- chain authority output; only audit-service writes these (ADR-0045).
    prev_hash           CHAR(64) NOT NULL,
    chain_hash          CHAR(64) NOT NULL UNIQUE,
    -- server ingest time (not part of the hash).
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- the closed capability-edge set (agent-permission-model.md + service-service).
    CONSTRAINT audit_events_edge_chk CHECK (edge IN (
        'human-agent','agent-agent','agent-tool','agent-workspace',
        'agent-repo','agent-memory','agent-remote-session','service-service'
    ))
);

-- Append-only enforcement at the database boundary (defense in depth): no
-- UPDATE or DELETE may ever touch the evidence chain. Redactions are overlay
-- tombstones in a future table, never row mutations (Audit/Evidence Reviewer
-- charter). This trigger makes a forged-after-the-fact edit impossible even for
-- a caller with table privileges.
CREATE OR REPLACE FUNCTION audit_events_append_only()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'audit_events is append-only (ADR-0045): % rejected', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER audit_events_no_mutate
    BEFORE UPDATE OR DELETE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_events_append_only();
