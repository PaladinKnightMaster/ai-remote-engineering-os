# packages/go/outbox

Shared Postgres **transactional-outbox** writer + relay (ADR-0045). Third
shared-infra module after `packages/go/middleware` (ADR-0043) and
`packages/go/migrate` (ADR-0044), following the same shape.

## Why

P0 has exactly one event-shaped flow: producer services fan evidence in to
`services/audit-service` (the chained-hash chain authority). The outbox
eliminates the dual-write problem — the evidence row is written in the **same
transaction** as the producer's state change, so a crash between "state
committed" and "evidence delivered" cannot lose evidence. A relay drains the
table and POSTs each row to `audit-service POST /v1/events`; delivery is
**at-least-once** and the sink dedupes on the UUID v7 `idempotency_key`.

No event-bus daemon ships at P0 (ADR-0045). NATS JetStream is the named P1
reserve pick for the day a real fan-out flow lands.

## Usage

Enqueue inside the producer's state-change transaction:

```go
w := outbox.Writer{}
key, err := w.Enqueue(ctx, tx, outbox.Event{
    Timestamp: time.Now(),
    Actor:     "operator:admin",
    Edge:      "agent-remote-session",
    Target:    "session:" + sessionID,
    Outcome:   "opened",
})
// ... tx.Commit(ctx)
```

Run the relay (in-process goroutine from `main.go`, and/or the
`<service> outbox relay` subcommand):

```go
relay := &outbox.Relay{
    Pool:   pool,
    Client: &outbox.HTTPClient{BaseURL: auditURL, Token: serviceTokenFunc},
}
_ = relay.Run(ctx) // returns on ctx cancel; transient errors self-heal
```

The relay wakes on a Postgres `LISTEN/NOTIFY` hint and falls back to a periodic
sweep (default 10s) — `LISTEN/NOTIFY` is **only** a wakeup hint, never the
transport (ADR-0045). If a deployment disables it, the sweep still drains the
table; correctness never depends on the listener.

## Canonical migration template

Each producer owns its `outbox` table via its **own** migrations (ADR-0044).
Copy this template (the `session-broker` instance is
`services/session-broker/migrations/0001_outbox.up.sql`):

```sql
-- <service> outbox (ADR-0045). Drained by packages/go/outbox Relay into the
-- audit-service chain authority; rows are written in the producer's own
-- state-change transaction.
CREATE TABLE outbox (
    id              BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY, -- delivery order
    idempotency_key UUID        NOT NULL UNIQUE,                     -- sink dedup key (UUID v7)
    payload         JSONB       NOT NULL,                            -- verbatim audit-ingest body
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    delivered_at    TIMESTAMPTZ                                      -- NULL until the relay delivers
);

-- Cheap repeated lookup of the pending tail (partial index on undelivered rows).
CREATE INDEX outbox_undelivered_idx ON outbox (id) WHERE delivered_at IS NULL;

-- LISTEN/NOTIFY wakeup hint: fire after every insert so the relay drains
-- promptly instead of waiting for the next sweep. Hint only — not the transport.
CREATE FUNCTION outbox_notify() RETURNS trigger AS $$
BEGIN
    PERFORM pg_notify('outbox_new', '');
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER outbox_notify_trg
    AFTER INSERT ON outbox
    FOR EACH ROW EXECUTE FUNCTION outbox_notify();
```

Down (mutable services only; not `audit-service`, which is forward-only):

```sql
DROP TRIGGER IF EXISTS outbox_notify_trg ON outbox;
DROP FUNCTION IF EXISTS outbox_notify();
DROP TABLE IF EXISTS outbox;
```

## Payload shape

`Writer.Enqueue` stores the exact JSON `audit-service` accepts at
`POST /v1/events` (`timestamp, actor, edge, target, outcome?, evidencePacketId?,
idempotencyKey`), so the relay POSTs the row bytes verbatim with no re-marshal.
Producers supply only semantic fields; the chain authority assigns
`id`/`prevHash`/`chainHash`.
