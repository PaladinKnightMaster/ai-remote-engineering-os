---
name: T-004b audit-service minimal append-only API
about: The chain-authority service — POST /v1/events ingest with dedup + chained-hash, read surface, forward-only migration
title: "T-004b: audit-service minimal append-only API"
labels: ["ticket", "P0", "bootstrap"]
---

## Ticket ID and link

T-004b — see `docs/tickets/README.md`. The service half of the split T-004
(scaffolding is T-004a). Implements **ADR-0045** (audit-service is the sync HTTP
append-only chain authority) and **ADR-0044** (forward-only embedded migration).
Lead lens: **Audit / Evidence Reviewer** (append-only + tamper-evidence); also
**Permission Model Reviewer** (the capability edge on `POST /v1/events`).
Depends on the target T-004a creates (`packages/go/middleware`,
`packages/go/migrate`, `task codegen`, `aireos.local` prefix).

## Goal

Stand up `services/audit-service` as the append-only chain authority: a chi
`POST /v1/events` ingest that assigns `id`/`prevHash`/`chainHash`, dedupes on
`idempotencyKey`, and serializes the chained-hash extension; a read surface; a
forward-only embedded migration; and the executable test surface.

## Priority

P0. The first service with a real executable test surface.

## Scope decisions (settled in consult)

- **Ingest DTO ≠ stored AuditEvent.** Producers POST `AuditEventIngest`
  (timestamp, actor, edge, target, idempotencyKey, + optional outcome /
  evidencePacketId). `id`/`prevHash`/`chainHash` are **server-assigned**;
  client-supplied chain fields → **400** (`DisallowUnknownFields`).
- **`idempotencyKey` is a body field**, not a header — it is the evidence-row
  identity minted at outbox enqueue and replayed verbatim by the relay (ADR-0045).
- **`200` for append and dedup replay alike** (idempotent; ADR-0045 "returns 200
  only after commit").
- **Chain hash:** `SHA256(prevHash ‖ length-prefixed(id,timestamp,actor,edge,
  target,outcome,evidencePacketId,idempotencyKey))`; genesis prevHash = 64 hex
  zeros; lowercase hex. Appends serialized via `pg_advisory_xact_lock`; dedup via
  `UNIQUE(idempotency_key)`.
- **Timestamp stored as TEXT** (verbatim) so the hashed string is byte-identical
  on read (no timestamptz normalization). A separate `created_at` records ingest
  time and is not hashed.
- **Forward-only:** `0001_init.up.sql` only (no down for the chain table) + a DB
  trigger rejecting UPDATE/DELETE (ADR-0044).
- **Auth (Option A):** routes gated by `RequireCapability` over the
  `service-service` edge; the real PASETO verifier is **T-008**. The service
  fails loud without a verifier (`AUDIT_DEV_INSECURE_AUTH=1` for dev only).

## Files in scope

- **Create:**
  - `services/audit-service/go.mod`
  - `services/audit-service/main.go` — `serve` + `migrate` subcommands, embedded migrations, fail-loud verifier.
  - `services/audit-service/internal/audit/event.go` — `Ingest` DTO + stored `Event` + edge set.
  - `services/audit-service/internal/chain/chain.go` (+ `chain_test.go`) — `Compute` / `Verify` / `Genesis`.
  - `services/audit-service/internal/store/store.go` — serialized `Append` (dedup + chain extension), `Get`, `List`.
  - `services/audit-service/internal/store/store_integration_test.go` — concurrent-append integrity + dedup (build tag `integration`, real Postgres).
  - `services/audit-service/internal/server/server.go` (+ `server_test.go`) — chi handlers, DTO decode, reject chain fields.
  - `services/audit-service/migrations/0001_init.up.sql` + `migrations/README.md`.
  - `services/audit-service/testdata/ingest_valid.json` + `ingest_invalid.json`.
  - `docs/tickets/T-004b-audit-service.md` — this ticket.
- **Modify:**
  - `docs/api-contracts/openapi.v3.fragment.yaml` — add `/v1/events` (POST/GET) + `/v1/events/{id}` and the `AuditEventIngest` schema, referencing the T-009 shared components.
  - `go.work` — `use ./services/audit-service`.
  - `Taskfile.yml` — extend `test` / `build` with audit-service targets.
  - `services/audit-service/README.md` — replace the placeholder.
  - `docs/tickets/README.md` — flip T-004b to complete.

## Acceptance criteria

- [ ] `POST /v1/events` assigns `id`/`prevHash`/`chainHash`; the producer cannot
      set them.
- [ ] **Violating fixture** `testdata/ingest_invalid.json` (carries `chainHash`)
      → 400; **passing fixture** `testdata/ingest_valid.json` → 200. Mirrored by
      `TestRejectClientSuppliedChainFields` / `TestAppendAssignsChainFields`.
- [ ] Replaying an `idempotencyKey` returns the stored event and does not extend
      the chain (`TestDedupOnReplay`, `TestDedupReplayIntegrity`).
- [ ] Concurrent appends form one linear chain, no forks/gaps
      (`TestConcurrentAppendIntegrity`, ADR-0045 §Verification).
- [ ] Chain hash is deterministic and tamper-evident (`chain_test.go`).
- [ ] Migration is forward-only (no down for `audit_events`); DB trigger rejects
      UPDATE/DELETE.
- [ ] Routes gated by `RequireCapability`; no bearer → 401
      (`TestUnauthorizedWithoutBearer`).
- [ ] OpenAPI updated — **done** (paths + `AuditEventIngest`).
- [ ] Docs / ADR updated — **N/A** (ADR-0001..0047 untouched).
- [ ] Unit tests cover valid + invalid + edge — **done** (see *Verification* gap).
- [ ] Verification command runs clean — **see gap**.

## Out of scope

- `packages/go/auth/` PASETO verifier + operator login — **T-008** (ADR-0046).
- The outbox writer/relay that produces these events — first producer ticket
  (ADR-0045); audit-service is the sink.
- Redaction-tombstone overlay table — follow-up audit ticket.
- Migration audit-emission wiring (`Runner.OnApply` into the chain) — documented
  P0 gap (ADR-0044 §Verification).
- Swapping the hand-authored chi handlers onto the oapi-codegen `ServerInterface`
  — pending a `task codegen` run (toolchain gap, below).
- A `GET /v1/events/head` chain-head endpoint — deferred (not needed at P0).

## Diff budget

Expanded budget approved at consult (the service half of the T-004 split).
Actual: ~15 created + 5 modified = ~20 files, ≈700 LoC. (Standard ≤12/≤500 was
explicitly waived for this half; the scaffolding half T-004a stays within budget.)

## ADRs required

None new. Implements ADR-0045 (chain authority, sync HTTP append-only, dedup)
and ADR-0044 (forward-only embedded migration). Consumes ADR-0043 (chi +
oapi-codegen), ADR-0046 (capability edge / Verifier port). All Accepted.

## Verification

```
# contract parses and declares the new surface
python -c "import yaml; d=yaml.safe_load(open('docs/api-contracts/openapi.v3.fragment.yaml')); \
assert '/v1/events' in d['paths']; assert 'AuditEventIngest' in d['components']['schemas']; print('ok')"
```

With the Go toolchain + a Postgres present:

```
go build ./services/audit-service/...
go test ./packages/go/... ./services/audit-service/...          # unit
docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
export AUDIT_TEST_DATABASE_URL=postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable
go run ./services/audit-service migrate up
go test -tags integration ./services/audit-service/internal/store/...   # concurrent + dedup
```

### Verification — run and passed

The whole surface was verified in a `golang:1.23` container against the
Postgres from `infra/docker-compose/docker-compose.mvp.yml`:

- `go build` / `go vet` over `packages/go/...` + `services/audit-service/...` —
  **clean**.
- Unit tests (`internal/chain`, `internal/server`) — **pass** (chain
  determinism/tamper, reject-client-chain-fields, dedup, 401-without-bearer).
- `go run ./services/audit-service migrate up` applied `0001_init`
  (`migrate version` → `1, dirty=false`).
- Integration tests (`-tags integration`) — **pass**:
  `TestConcurrentAppendIntegrity` (50 concurrent appends form one linear chain,
  `chain.Verify` intact) and `TestDedupReplayIntegrity`.

`go.sum` files were generated by `go mod tidy` and are committed.

**Two bugs the integration tests caught and that are now fixed:**

1. **Boundary validation gap.** `Ingest.Validate` accepted any non-empty
   `idempotencyKey`/`timestamp`, so a non-UUID key surfaced as a Postgres 500
   instead of a 400. `Validate` now rejects non-UUID `idempotencyKey` and
   non-RFC3339 `timestamp` at the API edge.
2. **Read surface omitted `idempotency_key`.** `store.selectCols` did not select
   `idempotency_key`, so events read via `GET`/`List` had an empty key — and
   because the key is hashed, `chain.Verify` over the read API always failed.
   The column is now selected and scanned.

### Remaining deferred item (not a regression)

- `task codegen` is intentionally **not run yet**: the shared
  `openapi.v3.fragment.yaml` still carries the P1 stub paths, so `oapi-codegen`
  would emit a whole-product `ServerInterface`. Handlers use plain chi against
  the contract until a per-service contract slice (include-tags or a split
  fragment) is decided — tracked as a follow-up (see T-004b *Out of scope*).
