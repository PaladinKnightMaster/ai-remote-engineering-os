-- services/api schema, migration 0004 — the transactional outbox (ADR-0045).
-- Completes ADR-0045's `api → audit-service` producer flow + release-criteria
-- SEC-9: services/api enqueues auth/control-plane evidence here and its
-- in-process relay drains it into the audit-service chain authority (T-022).
--
-- The table is named `api_outbox` (not the default `outbox`) because api shares
-- the cockpit database with services/session-broker, which owns `outbox`
-- (ADR-0048 shared Postgres; the broker reads api's sessions/resources). Each
-- producer owns its own outbox table + notify channel in the shared DB.
--
-- Standard mutable service (only audit-service is forward-only, ADR-0044), so a
-- destructive down is permitted.
CREATE TABLE api_outbox (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, -- delivery order
    idempotency_key UUID        NOT NULL UNIQUE,                     -- sink dedup key (UUID v7)
    payload         JSONB       NOT NULL,                            -- verbatim audit-ingest body
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at    TIMESTAMPTZ                                      -- NULL until the relay delivers
);

CREATE INDEX api_outbox_undelivered_idx ON api_outbox (id) WHERE delivered_at IS NULL;

-- LISTEN/NOTIFY wakeup hint on api's own channel (ADR-0045): hint only, the
-- transport is HTTP. Distinct from the broker's `outbox_new` so each relay wakes
-- on its own inserts.
CREATE FUNCTION api_outbox_notify() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('api_outbox_new', '');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER api_outbox_notify_trg
    AFTER INSERT ON api_outbox
    FOR EACH ROW EXECUTE FUNCTION api_outbox_notify();
