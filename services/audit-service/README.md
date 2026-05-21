# audit-service

Append-only audit/event service and **chain authority** (P0). See
`docs/security/` and ADR-0044 / ADR-0045.

Module: `aireos.local/services/audit-service`.

## Surface (T-004b)

- `POST /v1/events` — append an audit event. Producers supply only semantic
  fields; the service assigns `id` / `prevHash` / `chainHash` and dedupes on
  `idempotencyKey` (at-least-once, ADR-0045). Client-supplied chain fields are
  rejected with 400. Returns `200` (also on a deduped replay).
- `GET /v1/events` — list in chain order (forward-only `cursor` = `seq`).
- `GET /v1/events/{id}` — fetch one event.

All routes are gated by `RequireCapability` over the `service-service` /
`human-agent` edges (`packages/go/middleware`). The real PASETO verifier lands
in **T-008** (ADR-0046); until then the service **fails loud** — set
`AUDIT_DEV_INSECURE_AUTH=1` for local dev only.

## Run

```
export DATABASE_URL=postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable
go run ./services/audit-service migrate up     # apply 0001_init (forward-only)
AUDIT_DEV_INSECURE_AUTH=1 go run ./services/audit-service serve
```

## Forward-only

`audit_events` is append-only: no `down` migration, plus a DB trigger rejecting
`UPDATE`/`DELETE`. See `migrations/README.md`.
