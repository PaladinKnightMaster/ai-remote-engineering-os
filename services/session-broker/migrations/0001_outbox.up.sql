-- session-broker schema, migration 0001 — the transactional outbox (ADR-0045).
-- session-broker is the first producer to adopt packages/go/outbox: it enqueues
-- session.open/session.close evidence here inside the session-state transaction
-- (T-017b), and the in-process relay drains the table into the audit-service
-- chain authority. This is the canonical instance of the outbox template in
-- packages/go/outbox/README.md.
--
-- session-broker is a standard mutable service (only audit-service is
-- forward-only, ADR-0044), so a destructive down is permitted.
CREATE TABLE outbox (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, -- delivery order
    idempotency_key UUID        NOT NULL UNIQUE,                     -- sink dedup key (UUID v7)
    payload         JSONB       NOT NULL,                            -- verbatim audit-ingest body
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at    TIMESTAMPTZ                                      -- NULL until the relay delivers
);

-- Partial index on the undelivered tail: the relay's hot path is
-- "SELECT ... WHERE delivered_at IS NULL ORDER BY id".
CREATE INDEX outbox_undelivered_idx ON outbox (id) WHERE delivered_at IS NULL;

-- LISTEN/NOTIFY wakeup hint (ADR-0045): NOTIFY after every insert so the relay
-- drains promptly instead of waiting for the periodic sweep. Hint only — the
-- transport is HTTP, never NOTIFY.
CREATE FUNCTION outbox_notify() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('outbox_new', '');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER outbox_notify_trg
    AFTER INSERT ON outbox
    FOR EACH ROW EXECUTE FUNCTION outbox_notify();
