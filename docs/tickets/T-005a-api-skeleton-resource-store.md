---
name: T-005a services/api skeleton + resource-registry store + migration
about: Stand up the operator gateway service with the resources table, store CRUD, and migration (HTTP surface follows in T-005b)
title: "T-005a: services/api skeleton + resource-registry store + migration"
labels: ["ticket", "P0", "bootstrap"]
---

## Ticket ID and link

T-005a — see `docs/tickets/README.md`. The data half of the split T-005
(resource registry CRUD). Stands up `services/api` (the operator-facing gateway,
ADR-0043) with the `resources` table + store; the HTTP surface + oapi-codegen
slice land in **T-005b**. Owning-service decision (consult): resource CRUD lives
in **services/api** — no dedicated resource service exists in the P0 topology;
api manages the inventory, session-broker handles live sessions.

## Goal

Create the `services/api` Go module: a chi/migrate skeleton with a `/healthz`
readiness route, the `resources` table (standard up/down migration), and a
mutable CRUD store, verified against Postgres.

## Priority

P0. Second service; proves the T-004/T-014 pattern generalizes and unblocks T-005b.

## Scope decisions (settled in consult)

- **Owning service: `services/api`.**
- **Split T-005a/T-005b.** This half = service skeleton + store + migration; the
  HTTP surface + per-service oapi-codegen slice + handler tests = T-005b.
- **Standard up/down migration** (services/api is mutable, not forward-only —
  only audit-service is, ADR-0044). `0001_init` ships an `up` and a `down`.
- **UUID v7 ids double as the list cursor** (v7 is time-sortable), so the store
  paginates on `id` without a separate seq column.
- **Per-service migrations table (fix to `packages/go/migrate`).** Surfaced here:
  services share one Postgres database, but golang-migrate defaulted to a single
  `schema_migrations` table, so `api migrate up` saw audit-service's version and
  skipped creating `resources`. The shared `Runner` now sets
  `x-migrations-table=<service>_schema_migrations` per service. Verified: both
  services migrate independently into the same DB (each at v1, separate tables).

## Files in scope

- **Create:**
  - `services/api/go.mod`
  - `services/api/main.go` — `migrate` subcommand + `serve` (`/healthz`).
  - `services/api/internal/resource/resource.go` — `Resource`, `Input`, type enum, `Validate`.
  - `services/api/internal/store/store.go` — `Create`/`Get`/`List`/`Update`/`Delete`.
  - `services/api/internal/store/store_integration_test.go` — DB CRUD round-trip (build tag `integration`).
  - `services/api/migrations/0001_init.up.sql` + `0001_init.down.sql`.
  - `docs/tickets/T-005a-api-skeleton-resource-store.md` — this ticket.
- **Modify:**
  - `packages/go/migrate/runner.go` — per-service `x-migrations-table` (the fix above).
  - `services/api/README.md` — replace the placeholder.
  - `go.work` — `use ./services/api`.
  - `docs/tickets/README.md` — add the T-005a row.

## Acceptance criteria

- [ ] `services/api` builds + vets clean; `go test` passes.
- [ ] `api migrate up` creates the `resources` table; `api migrate version` → 1.
- [ ] **Integration round-trip** (`TestResourceCRUDRoundTrip`): create → get →
      update (fields replaced, optional fields cleared) → list → delete →
      `ErrNotFound` (incl. delete-twice). Passes against Postgres.
- [ ] audit-service and api migrate independently into one database (separate
      version tables); audit-service integration tests still pass.
- [ ] `resources.type` is constrained to the enum at the DB (CHECK) and the API
      boundary (`Input.Validate`).
- [ ] OpenAPI / schemas — **N/A here** (paths land in T-005b).
- [ ] Docs / ADR — **N/A** (ADR-0001..0047 untouched).
- [ ] Unit tests cover valid + invalid + edge — store covered by the integration
      round-trip; handler validation surface lands in T-005b.
- [ ] Verification command runs clean (see below).

## Out of scope

- The `/v1/resources` HTTP surface, oapi-codegen slice, handler tests — **T-005b**.
- Operator login / `/v1/auth/*` / key custody — the auth ticket (ADR-0046).
- Wiring a `down` action into the shared migrate subcommand (the `down.sql`
  exists; the subcommand exposes `up|version|force`) — small follow-up on
  `packages/go/migrate`.

## Diff budget

Split half of T-005. Actual: ~9 created + 4 modified = ~13 files, ≈430 LoC.
(The +1 over the standard 12 is the `packages/go/migrate` per-service-table fix
surfaced during verification; folded in here as it is load-bearing for any
second service.)

## ADRs required

None new. Consumes ADR-0042/0043 (chi, task entry points), ADR-0044 (migrations).

## Verification

Verified in a `golang:1.23` container against the docker-compose Postgres:

```
go build ./services/api/...                 # clean
go vet   ./services/api/...                 # clean
go run   ./services/api migrate up          # creates resources
go run   ./services/api migrate version     # 1
go test -tags integration ./services/api/internal/store/...          # round-trip passes
go test -tags integration ./services/audit-service/internal/store/...# still passes (per-service tables)
```

All **pass**. The per-service migrations-table fix was validated by resetting
the DB and migrating both services from scratch into one database.
