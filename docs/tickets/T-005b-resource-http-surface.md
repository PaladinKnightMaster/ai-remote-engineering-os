---
name: T-005b Resource registry HTTP surface (/v1/resources CRUD) + oapi-codegen slice
about: The HTTP half of T-005 — chi-server resource CRUD on services/api, generated per-service from the contract, gated by capability tokens
title: "T-005b: Resource registry HTTP surface (/v1/resources CRUD)"
labels: ["ticket", "P0", "bootstrap"]
---

## Ticket ID and link

T-005b — see `docs/tickets/README.md`. The HTTP half of the split T-005; builds
on T-005a's `services/api` skeleton + store. Adds the `/v1/resources` CRUD
surface, the per-service oapi-codegen slice (the T-014 pattern, second
application), and handler tests, gated by `RequireCapability` + PASETO (T-008).

## Goal

Expose full resource CRUD on `services/api`: contract paths tagged `resources`,
a generated chi-server `ServerInterface` implemented over the T-005a store, and
capability-gated routes — verified end to end.

## Priority

P0. Completes T-005; the first non-audit service with a real HTTP + auth surface.

## Scope decisions (settled in consult / during implementation)

- **Per-service codegen slice** (T-014 pattern): operations tagged `resources`;
  `oapi-codegen.api.yaml` with `include-tags: [resources]` → only the resource
  surface generates. `models: true` (chi-server needs the param/error types);
  generated body models are unused — handlers use the `internal/resource` domain
  types (store carries created_at/updated_at not on the wire).
- **`oapi-codegen/runtime` pinned to `v1.1.1`** in `services/api` (same Go-1.23
  constraint as audit-service; `v1.4.1` needs Go ≥ 1.24).
- **Status codes**: create → 201, get/update → 200, list → 200, delete → 204;
  missing → 404; bad body / bad type / client-supplied `id` → 400 (the last via
  `ResourceInput.additionalProperties:false` + `DisallowUnknownFields`).
- **Capability edges**: routes gated over `human-agent` (operator) +
  `service-service`. Verifier resolves via `AIREOS_PASETO_KEY` (audience `api`),
  else `API_DEV_INSECURE_AUTH=1`, else fail loud — same shape as audit-service.
- **CI extended** to cover `packages/go/auth` + `services/api` (the auth module
  was missing from the T-010 `go` job's list) and the api codegen-drift check.

## Files in scope

- **Create:**
  - `oapi-codegen.api.yaml` — per-service codegen config.
  - `services/api/internal/server/gen.go` — generated chi-server stub (committed).
  - `services/api/internal/server/server.go` — implements `ServerInterface`
    (Create/List/Get/Update/Delete) over the store.
  - `services/api/internal/server/server_test.go` — handler tests (in-memory fake).
  - `docs/tickets/T-005b-resource-http-surface.md` — this ticket.
- **Modify:**
  - `docs/api-contracts/openapi.v3.fragment.yaml` — `resources` tag, `/v1/resources`
    (POST/GET) + `/v1/resources/{id}` (GET/PUT/DELETE), `ResourceInput` schema.
  - `services/api/main.go` — `serve` mounts `server.New` with the capability verifier.
  - `services/api/go.mod` — add `auth` + `middleware` + `oapi-codegen/runtime v1.1.1`.
  - `Taskfile.yml` — `task codegen` also generates the api stub.
  - `.github/workflows/ci.yml` — add `auth` + `api` to the go job; api codegen-drift.
  - `docs/tickets/README.md` — mark T-005b complete.

## Acceptance criteria

- [ ] `task codegen` regenerates `services/api/internal/server/gen.go` with only
      the five resource operations; no audit/other-service symbols leak.
- [ ] CRUD works: create → 201, get → 200, update → 200, delete → 204; missing →
      404 (incl. delete-twice). (`TestCreateGetUpdateDelete`.)
- [ ] **Violating fixtures** (400): bad type, missing type, client-supplied `id`.
      **Passing fixture**: a valid `ResourceInput`. (`TestCreateValidation`.)
- [ ] List returns `{items, page}` (`TestListReturnsItems`); no bearer → 401
      (`TestUnauthorizedWithoutBearer`).
- [ ] Routes gated by `RequireCapability` over human-agent + service-service.
- [ ] OpenAPI updated; all internal `$ref`s resolve (validator passes).
- [ ] Docs / ADR — **N/A** (ADR-0001..0047 untouched).
- [ ] Unit tests cover valid + invalid + edge — done.
- [ ] Verification command runs clean (see below).

## Out of scope

- Operator login / `/v1/auth/*` / key custody — the auth ticket (ADR-0046).
- Resource ↔ Session linkage, session lifecycle — T-006 (session-broker).
- `down`-action wiring in the migrate subcommand — follow-up on `packages/go/migrate`.

## Diff budget

Split half of T-005. Actual: 5 created + 6 modified = 11 files, ≈480 LoC
(gen.go is generated).

## ADRs required

None new. Implements ADR-0043 (chi + oapi-codegen, the T-014 per-service slice)
and consumes ADR-0046 (capability verifier). All Accepted.

## Verification

Verified in a `golang:1.23` container against the docker-compose Postgres:

```
task codegen                                   # regenerates api gen.go (only resource ops)
go build/vet ./packages/go/... ./services/api/... ./services/audit-service/...   # clean
go test ./services/api/...                     # handler tests pass
go run ./services/api migrate up
go test -tags integration ./services/api/internal/store/...   # CRUD round-trip
python infra/scripts/validate_contracts.py     # contract + schema validation passes
```

All **pass**: handler CRUD/validation/401, generated-stub slice contains only
resource ops, integration round-trip, contract validation (resource paths +
ResourceInput present, refs resolve).
