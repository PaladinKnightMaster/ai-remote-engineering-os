-- mcp-gateway initial schema, migration 0001 (ADR-0057). The gateway owns the
-- invocations table + its outbox in the shared cockpit DB (ADR-0048), the same
-- own-tables pattern policy-service/connector-service use — its OWN outbox table +
-- NOTIFY channel (gateway_outbox / gateway_outbox_new) avoid collision with
-- outbox / api_outbox / connector_outbox / policy_outbox.
--
-- T-039a lands the schema + store; the governed Invoke that populates it is T-039b.
-- Standard mutable service (only audit-service is forward-only, ADR-0044) — a
-- destructive down is permitted.

-- invocations: one row per agent tool-call (ADR-0057). id is the correlation
-- anchor (invocation:<id>) the policy decision, the broker exec, and the audit
-- chain share. The command text is NOT stored here — it lives in the broker's
-- audited <invocationId>.exec artifact (ADR-0058); the gateway keeps metadata only.
CREATE TABLE invocations (
    id              UUID        PRIMARY KEY,                 -- UUID v7, the invocation id
    idempotency_key TEXT,                                    -- client Idempotency-Key; NULL = no dedup
    agent_subject   TEXT        NOT NULL,                    -- the agent capability subject (agent:<id>)
    resource_id     TEXT        NOT NULL,                    -- target resource
    tool            TEXT        NOT NULL,                    -- e.g. remote.exec
    status          TEXT        NOT NULL DEFAULT 'pending_approval',
    decision        TEXT        NOT NULL DEFAULT '',         -- '' | allow | requires_approval
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT invocations_status_chk
        CHECK (status IN ('pending_approval', 'running', 'completed', 'denied', 'failed'))
);

-- Idempotency (PERF-2): a client-supplied key dedups retries per agent. Partial —
-- a NULL key (none supplied) never dedups, so identical keyless calls each run.
CREATE UNIQUE INDEX invocations_idem_idx
    ON invocations (agent_subject, idempotency_key) WHERE idempotency_key IS NOT NULL;

-- Per-agent concurrency cap (PERF-3): the in-flight count query filters on these.
CREATE INDEX invocations_inflight_idx
    ON invocations (agent_subject) WHERE status IN ('pending_approval', 'running');

-- gateway_outbox: the transactional outbox (ADR-0045). tool.invoked / tool.result
-- evidence is enqueued in the SAME transaction as the invocations mutation, so
-- there is no unaudited tool call; the in-process relay drains it to audit-service.
CREATE TABLE gateway_outbox (
    id              BIGINT      GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    idempotency_key UUID        NOT NULL UNIQUE,
    payload         JSONB       NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at    TIMESTAMPTZ
);

CREATE INDEX gateway_outbox_undelivered_idx ON gateway_outbox (id) WHERE delivered_at IS NULL;

-- LISTEN/NOTIFY wakeup hint (ADR-0045): NOTIFY after every insert so the relay
-- drains promptly. Hint only — the transport is HTTP, never NOTIFY.
CREATE FUNCTION gateway_outbox_notify() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('gateway_outbox_new', '');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER gateway_outbox_notify_trg
    AFTER INSERT ON gateway_outbox
    FOR EACH ROW EXECUTE FUNCTION gateway_outbox_notify();
