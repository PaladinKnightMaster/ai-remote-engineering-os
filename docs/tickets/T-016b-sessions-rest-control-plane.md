---
name: T-016b Ticket
about: SSH end-to-end vertical slice (a) — REST control-plane half
title: "T-016b: /v1/sessions REST control plane on services/api"
labels: ["ticket"]
---

## Ticket ID and link

T-016b — second half of the SSH end-to-end vertical slice (a) per
ADR-0048 §Hand-off. No B-NNN counterpart (cockpit-feature ticket against
ADR-0048, not a frozen v3.0.1 backlog row). Depends on **T-016a**, which
created the `sessions` table this surface reads/writes.

## Goal

Expose the cockpit-session control plane on `services/api`: the
`/v1/sessions` REST CRUD (create / list / get / end) over the `sessions`
table, gated by the existing capability edges and operator-scoped. No
WebSocket data plane, no audit emission (both belong to the broker, slice b).

## Priority

P0 — v0.1.0 wedge (ADR-0048 §2 control plane). ADR-0048 is Accepted; no new
ADR required.

## Files in scope

- Modify: `docs/api-contracts/openapi.v3.fragment.yaml` (`sessions` tag, 4 paths,
  reconcile `Session` schema, add `SessionCreate`)
- Modify: `oapi-codegen.api.yaml` (add `sessions` to `include-tags`)
- Modify: `services/api/internal/server/gen.go` (regenerated; `task codegen`)
- Create: `services/api/internal/session/session.go` (domain + Validate)
- Create: `services/api/internal/store/sessions.go` (operator-scoped store methods)
- Modify: `services/api/internal/server/server.go` (4 handlers + Store interface)
- Modify: `services/api/internal/server/server_test.go` (handler tests + session fake)
- Modify: `services/api/internal/store/store_integration_test.go` (session round-trip)
- Create: `docs/tickets/T-016b-sessions-rest-control-plane.md` (this file)
- Modify: `docs/tickets/README.md` (mark T-016b complete)

10 files. Within the ≤12-file / ≤500-LoC budget.

## Scoping decisions (war-room consult, 2026-05-28)

- **Operator-scoped, no cross-operator scope (ADR-0048 §5).** The caller's
  verified capability subject *is* the `operator_id`. Every read/write filters
  on it, so a session owned by another operator is indistinguishable from a
  missing one (404, not 403) — the scope is the security boundary, mirrored in
  both the store SQL and the integration test.
- **Gated on the existing `human-agent` + `service-service` edges**, reusing
  `gateByDeclaredSecurity` unchanged. The `agent-remote-session` edge is the
  **data-plane** edge enforced by the broker on the WS upgrade (slice b), not a
  REST-control-plane edge — so no middleware change here.
- **`Session` schema narrowed to the DB reality.** The T-003-origin mirror
  carried a superset (`mode ∈ {observe,control,terminal,demo}`,
  `status ∈ {pending,active,suspended,ended,failed,terminated}`). Narrowed to
  what `sessions_*_chk` actually allow: `mode = terminal`,
  `status ∈ {pending,active,closed,terminated}`, plus `operatorId`, `startedAt`
  (required) and the nullable `endedAt`/`closedReason`/`castPath`/`castSha256`.
- **`SessionCreate` is `additionalProperties:false`, `required:[resourceId]`.**
  `id`/`operatorId`/`status` are server-assigned; `mode` defaults to `terminal`.
  An over-posted server field (e.g. `status`) is rejected 400 by
  `DisallowUnknownFields`, same posture as `ResourceInput`.
- **Unknown `resourceId` → 404 via the FK.** `CreateSession` maps a Postgres
  `foreign_key_violation` (SQLSTATE 23503) to `ErrNotFound` so a bad resourceId
  answers 404, not 500 — no extra existence round-trip.
- **`end` is idempotent and non-downgrading.** A pending/active session
  transitions to `closed` with `ended_at`/`closed_reason='operator'` stamped; a
  `CASE`/`COALESCE` leaves an already-ended (`closed`/`terminated`) row
  unchanged so a repeated end stays 200 and never downgrades `terminated`. The
  broker still performs the real teardown and emits `session.close` (slice b).
- **Audit-silent**, like resource CRUD today. No `session.*` events: the first
  outbox + lifecycle audit + cast-SHA anchoring are the broker's job (slice b).

## Acceptance criteria

- [x] `POST /v1/sessions` creates a `pending` session (201) against a real
      resource; unknown `resourceId` → 404; malformed/over-posted body → 400.
- [x] `GET /v1/sessions` returns the caller's own sessions (operator-scoped,
      `items`/`page` envelope); `GET /v1/sessions/{id}` 200/404.
- [x] `POST /v1/sessions/{id}/end` transitions to `closed`/`operator` with
      `endedAt` set, is idempotent, and 404s for unknown/other-operator ids.
- [x] All four routes require a bearer (401 without).
- [x] Cross-operator isolation verified at the store layer (get/list/end).
- [x] OpenAPI reconciled; `oapi-codegen` regen is drift-free (`task codegen`
      produces no diff).
- [x] ADR updated if applicable: **n/a** (ADR-0048 already Accepted).
- [x] Unit + integration tests cover valid + invalid + edge cases.
- [x] `golangci-lint` (T-011 config) clean on `services/api`.
- [x] Verification commands run clean (below).

### Violating vs. passing fixtures

- **Operator scope — violating:** an unscoped `SELECT ... WHERE id=$1` would let
  operator B read/end operator A's session. **Passing:**
  `WHERE id=$1 AND operator_id=$2` → cross-operator access is 404
  (`TestSessionCRUDRoundTrip` asserts both directions).
- **FK 404 — violating:** inserting an unknown `resourceId` without mapping
  23503 surfaces a 500. **Passing:** `foreign_key_violation → ErrNotFound → 404`.
- **Over-post — violating:** a client-supplied `status:"active"` on create would
  forge server state. **Passing:** `DisallowUnknownFields` → 400.
- **Schema drift — violating:** leaving the old `mode`/`status` superset lets the
  contract advertise states the DB CHECK rejects. **Passing:** narrowed enums
  match `sessions_*_chk`.

## Out of scope

- Session-broker WS data plane, host-key pinning, decryption-at-session-open,
  per-input-line audit, asciinema cast files → **T-017 / slice (b)**, **T-019**.
- `packages/go/outbox/` + any `session.*` audit event → slice (b).
- `apps/web` `<TerminalView>` + session list/start/end UI → **T-018 / slice (c)**.
- `apps/web` TS client regen for the new paths (the `task codegen` JS leg) →
  bundled with the frontend slice (c) that consumes it.

## Diff budget

≤12 files / ≤500 LoC. This ticket: 10 files, within budget.

## ADRs required

- None new. Implements ADR-0048 §2 (services/api owns the REST control plane)
  + §5 (operator scope).

## Verification

```sh
# codegen drift + build + vet + unit tests (Dockerized; golang:1.23)
docker run --rm -v "$PWD:/work" -v aireos-gomod:/go/pkg/mod -w /work golang:1.23 \
  bash -c "go run github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@v2.4.1 \
    --config oapi-codegen.api.yaml docs/api-contracts/openapi.v3.fragment.yaml > /dev/null \
    && git diff --exit-code services/api/internal/server/gen.go \
    && go build ./services/api/... && go vet ./services/api/... \
    && go test ./services/api/..."

# session integration round-trip (MVP Postgres on docker-compose_default)
docker compose -f infra/docker-compose/docker-compose.mvp.yml up -d postgres
docker run --rm --network docker-compose_default -v "$PWD:/work" -v aireos-gomod:/go/pkg/mod -w /work \
  -e API_DATABASE_URL=postgres://cockpit:change-me@postgres:5432/cockpit?sslmode=disable \
  -e API_TEST_DATABASE_URL=postgres://cockpit:change-me@postgres:5432/cockpit?sslmode=disable \
  golang:1.23 bash -c \
  "go run ./services/api migrate up && go test -tags integration ./services/api/internal/store/..."

# lint (T-011 config)
docker run --rm -v "$PWD:/work" -v aireos-gomod:/go/pkg/mod -w /work golang:1.23 \
  go run github.com/golangci/golangci-lint/cmd/golangci-lint@v1.61.0 run ./services/api/...
```

Expected: no codegen drift; build/vet clean; `ok .../internal/server` and
`.../internal/store` (incl. `TestSessionCRUDRoundTrip`); lint clean.
(All confirmed 2026-05-30 on Docker / golang:1.23.)
