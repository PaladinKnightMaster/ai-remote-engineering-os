-- connector-service schema, migration 0002 — the transactional outbox (ADR-0045).
--
-- connector-service shares the cockpit DB (ADR-0048) with services/api
-- (api_outbox) and the session-broker (outbox), so it uses its OWN table + NOTIFY
-- channel — connector_outbox / connector_outbox_new — to avoid collision. The
-- parameterized outbox.Writer.Table / Relay.Table + Relay.Channel make this clean
-- (T-022 precedent).
--
-- connector enqueues egress-decision evidence ("credential.leased") here inside
-- the lease transaction (T-029b-ii); the in-process relay drains it into the
-- audit-service chain authority.
--
-- Standard mutable service (only audit-service is forward-only, ADR-0044) — a
-- destructive down is permitted.
CREATE TABLE connector_outbox (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, -- delivery order
    idempotency_key UUID        NOT NULL UNIQUE,                     -- sink dedup key (UUID v7)
    payload         JSONB       NOT NULL,                            -- verbatim audit-ingest body
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at    TIMESTAMPTZ                                      -- NULL until the relay delivers
);

CREATE INDEX connector_outbox_undelivered_idx ON connector_outbox (id) WHERE delivered_at IS NULL;

-- LISTEN/NOTIFY wakeup hint (ADR-0045): NOTIFY after every insert so the relay
-- drains promptly. Hint only — the transport is HTTP, never NOTIFY.
CREATE FUNCTION connector_outbox_notify() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('connector_outbox_new', '');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER connector_outbox_notify_trg
    AFTER INSERT ON connector_outbox
    FOR EACH ROW EXECUTE FUNCTION connector_outbox_notify();
