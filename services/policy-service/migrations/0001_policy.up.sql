-- policy-service initial schema, migration 0001 (ADR-0054). policy-service is the
-- approval/policy decision point: it parks a session-start that targets a
-- `requires_approval` resource as a pending approval and brokers the human
-- approve/deny round-trip, every decision audit-chained.
--
-- policy-service owns its tables in the shared cockpit DB (ADR-0048), the same
-- own-tables pattern connector-service uses (ADR-0054 §6: "the same parameterized-
-- table pattern as connector-service") — so the outbox uses its OWN table + NOTIFY
-- channel (policy_outbox / policy_outbox_new) to avoid collision with
-- outbox / api_outbox / connector_outbox.
--
-- This slice (T-031a-i) lands the schema only; the audited store methods + the
-- decide/resolve RPCs that populate these tables are T-031a-ii.
--
-- Standard mutable service (only audit-service is forward-only, ADR-0044) — a
-- destructive down is permitted.

-- pending_approvals: one row per parked session-start awaiting an operator's
-- decision. Keyed by session_id (a SOFT ref to the api-owned session — no
-- cross-service FK, per the connector soft-ref precedent). status is the
-- lifecycle; a timeout is status='denied' with reason='timeout' (fail-closed),
-- not a distinct status. resolved_by is separate from subject so segregation-of-
-- duties (a distinct approver) seats in v0.3.0 with no migration (ADR-0054 §6).
CREATE TABLE pending_approvals (
    id          UUID        PRIMARY KEY,              -- UUID v7, minted by the store
    session_id  TEXT        NOT NULL UNIQUE,          -- soft ref to the parked api session
    subject     TEXT        NOT NULL,                 -- requesting operator (capability subject)
    resolved_by TEXT,                                 -- approving operator; NULL until resolved
    edge        TEXT        NOT NULL,                 -- the gated action's capability edge (e.g. agent-remote-session)
    target      TEXT        NOT NULL,                 -- audited object, e.g. resource:<id>
    action      TEXT        NOT NULL,                 -- e.g. session.start
    status      TEXT        NOT NULL DEFAULT 'pending',
    reason      TEXT        NOT NULL DEFAULT '',      -- '' | timeout | operator
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,                 -- created_at + timeout; the sweeper fail-closes past this
    resolved_at TIMESTAMPTZ,                          -- NULL until resolved
    CONSTRAINT pending_approvals_status_chk CHECK (status IN ('pending', 'granted', 'denied'))
);

-- The fail-closed timeout sweeper (T-031a-ii) scans only still-pending rows past
-- their expiry — a partial index keyed on expires_at serves it directly.
CREATE INDEX pending_approvals_pending_idx ON pending_approvals (expires_at) WHERE status = 'pending';

-- policy_outbox: the transactional outbox (ADR-0045). Decision/approval evidence
-- is enqueued here in the SAME transaction as the pending_approvals mutation
-- (T-031a-ii), so there is no unaudited decision; the in-process relay drains it
-- into the audit-service chain authority.
CREATE TABLE policy_outbox (
    id              BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY, -- delivery order
    idempotency_key UUID        NOT NULL UNIQUE,                          -- sink dedup key (UUID v7)
    payload         JSONB       NOT NULL,                                 -- verbatim audit-ingest body
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at    TIMESTAMPTZ                                           -- NULL until the relay delivers
);

CREATE INDEX policy_outbox_undelivered_idx ON policy_outbox (id) WHERE delivered_at IS NULL;

-- LISTEN/NOTIFY wakeup hint (ADR-0045): NOTIFY after every insert so the relay
-- drains promptly. Hint only — the transport is HTTP, never NOTIFY.
CREATE FUNCTION policy_outbox_notify() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('policy_outbox_new', '');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER policy_outbox_notify_trg
    AFTER INSERT ON policy_outbox
    FOR EACH ROW EXECUTE FUNCTION policy_outbox_notify();
