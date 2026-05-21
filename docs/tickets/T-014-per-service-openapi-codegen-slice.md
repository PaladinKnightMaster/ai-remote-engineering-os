---
name: T-014 Per-service OpenAPI contract slicing for oapi-codegen
about: Make `task codegen` emit only the owning service's chi-server surface, then re-seat audit-service handlers onto the generated ServerInterface
title: "T-014: Per-service OpenAPI contract slicing for oapi-codegen (ADR-0043 closure)"
labels: ["ticket", "P1"]
---

## Ticket ID and link

T-014 — see `docs/tickets/README.md`. Surfaced during T-004b: the shared
`docs/api-contracts/openapi.v3.fragment.yaml` is the whole-product contract, so
running `oapi-codegen` against it would emit a `ServerInterface` spanning the
P1 stub paths (`/v1/workstreams`, `/v1/mcp/...`, `/v1/evals/...`) plus every
service's paths — not a clean per-service stub. T-004b therefore shipped
hand-authored plain-chi handlers with `task codegen` left unrun. This ticket
closes the ADR-0043 "oapi-codegen chi-server" verification target properly.
Lead lens: **Tech Lead** (codegen boundary); **Offline-Install Engineer**
(the oapi-codegen tool stays a `go run` dev dependency, not a runtime dep).

## Goal

Give `oapi-codegen` a per-service filter so `task codegen` produces a stub
containing only the owning service's operations, then re-seat
`services/audit-service` onto the generated `ServerInterface` and generated
models — removing the hand-authored plain-chi workaround while preserving the
exact request/response behaviour T-004b verified.

## Priority

P1. ADR-0043 codegen compliance + a quality/consistency unblocker for the next
service tickets (T-005, T-006). Not feature-blocking — plain-chi works — but
cheapest to do now while only one service must be re-seated.

## Decision to settle in this ticket

Two viable shapes; pick one in the ticket consult:

- **(A, recommended) Tag + `include-tags`.** Tag each operation with its owning
  service (`tags: [audit]`, `[resources]`, `[sessions]`, …) in the single
  canonical fragment, and drive per-service generation with `oapi-codegen`'s
  `include-tags` (one `oapi-codegen.<service>.yaml` per service). Keeps one
  canonical contract and the T-009 inline-mirror convention intact.
- **(B) Split fragments.** Break the fragment into `openapi/<service>.yaml`
  files that `$ref` the shared components. Cleaner generation inputs, but
  reintroduces the external-`$ref` resolution issue T-009 deliberately avoided
  and creates N contracts to keep in sync.

Recommendation: **A** — minimal change, single source of truth preserved.

## Files in scope (under option A)

- **Modify:**
  - `docs/api-contracts/openapi.v3.fragment.yaml` — add `tags: [audit]` to the
    three `/v1/events*` operations (and a top-level `tags` list).
  - `oapi-codegen.yaml` → `oapi-codegen.audit.yaml` — add `include-tags: [audit]`;
    keep `chi-server: true` + `models: true`; output
    `services/audit-service/internal/server/gen.go`.
  - `Taskfile.yml` — `task codegen` runs the per-service config(s).
  - `services/audit-service/internal/server/server.go` — implement the generated
    `ServerInterface` and mount via the generated `HandlerFromMux`/router helper;
    use generated request/response model types instead of `internal/audit` DTOs
    where they overlap (keep `audit.Event`/`Ingest` only where the store needs
    them, or alias to generated types).
- **Create:**
  - `services/audit-service/internal/server/gen.go` — the committed generated
    stub (output of `task codegen`; reviewed as a normal diff).
  - `docs/tickets/T-014-per-service-openapi-codegen-slice.md` — this ticket.

## Acceptance criteria

- [ ] `task codegen` regenerates `gen.go` containing **only** the audit
      operations (`appendEvent`, `listEvents`, `getEvent`).
- [ ] **Violating fixture:** the generated `gen.go` contains no symbol for a
      non-audit operation (e.g. no `compileAgentWorkflow` / workstream / mcp
      handler) — assert by grep in the verification step. **Passing fixture:**
      the three audit operIDs are present.
- [ ] `services/audit-service` builds against the generated `ServerInterface`;
      all T-004b tests (unit + `-tags integration`) still pass unchanged.
- [ ] `go build` / `go vet` clean across `services/audit-service/...`.
- [ ] No non-stdlib HTTP router other than chi/v5 is introduced (ADR-0043).
- [ ] OpenAPI / schemas updated — operation tags only; shared components
      untouched.
- [ ] Docs / ADR updated — **N/A** (ADR-0001..0047 untouched).
- [ ] Unit tests cover valid + invalid + edge — reuse T-004b's surface.
- [ ] Verification command runs clean.

## Out of scope

- Generating stubs for `services/api` / `services/resource-*` / session-broker —
  those land with T-005 / T-006 using the same per-service config pattern this
  ticket establishes.
- `openapi-typescript` client generation — first frontend ticket (T-007, ADR-0047).
- Reconciling/retiring the legacy `docs/api-contracts/openapi/openapi.yaml` —
  separate doc-hygiene ticket.

## Diff budget

≤12 files / ≤500 LoC.

## ADRs required

None new. Closes the ADR-0043 §Verification item 2 ("`oapi-codegen` wired via
`task codegen` … produces a chi-server stub … that compiles cleanly") for
audit-service. All consumed ADRs already Accepted.

## Verification

```
# only audit ops generated
test -f services/audit-service/internal/server/gen.go
grep -q "appendEvent\|AppendEvent" services/audit-service/internal/server/gen.go
! grep -Eqi "workstream|mcp|eval|compileAgentWorkflow" services/audit-service/internal/server/gen.go
```

With the Go toolchain + a Postgres (the verified T-004b loop):

```
go build ./services/audit-service/...
go test ./services/audit-service/...
go test -tags integration ./services/audit-service/internal/store/...
```
