---
name: T-004a Shared Go scaffolding (middleware + migrate + codegen wiring)
about: The cross-cutting Go infrastructure every later service ticket depends on, split out of T-004 per the ADR-0043/0044 hand-offs
title: "T-004a: Shared Go scaffolding (packages/go/middleware + packages/go/migrate + codegen/migrate task wiring)"
labels: ["ticket", "P0", "bootstrap"]
---

## Ticket ID and link

T-004a — see `docs/tickets/README.md`. Split from **T-004** (audit-service)
because a whole service plus the shared packages exceeds the diff budget; this
half lands the cross-cutting Go infrastructure that **every** later service
ticket (T-004b, T-005, T-006, T-008) depends on. Executes the §Hand-off notes
of **ADR-0043** (`packages/go/middleware/` + `task codegen`) and **ADR-0044**
(`packages/go/migrate/`, with `services/audit-service` named as the first
stateful service). Activates **Tech Lead** (scaffolding / codegen boundary) and
**Permission Model Reviewer** (the capability-edge enforcement point).

## Goal

Materialize `packages/go/middleware/` and `packages/go/migrate/` as Go modules
under `go.work`, wire `task codegen` (oapi-codegen chi-server, ADR-0043) and
`task migrate` (ADR-0044), and fix the project's Go module-path prefix. No
service code lands here — that is T-004b.

## Priority

P0. Bootstrap; unblocks T-004b and every subsequent Go service ticket.

## Scope decisions (settled in consult)

- **Module-path prefix: `aireos.local/...`** — dot-qualified internal vanity
  prefix. Offline-first (ADR-0001); no remote implied for this deliberately
  unpushed repo; mirrors the `https://example.local/...` posture of the
  shared-schema `$id`s. Rewrites to a real path via `go mod edit` if published.
- **Auth boundary (Option A).** `RequireCapability` depends on a `Verifier`
  **port** defined in `packages/go/middleware/`; the concrete PASETO v4
  implementation (`packages/go/auth/`) lands in **T-008** (ADR-0046 §Hand-off:
  "first auth-touching service ticket"). This keeps the `go-paseto` dependency
  isolated to `packages/go/auth/` (the T-011 depguard rule) and lets the
  capability edge be wired at T-004 before token issuance exists. The audit
  endpoint's "no real verifier until T-008" gap is made **loud** — the service
  (T-004b) refuses to boot without an injected verifier.
- **`packages/go/outbox/` is NOT in scope** — it lands with the first *producer*
  ticket. `audit-service` is the sink / chain authority, not a producer
  (ADR-0045).
- **`packages/go/auth/` is NOT in scope** — T-008 per above.
- **ADR-0044 migrate audit-emission hook** lands as the `Runner.OnApply`
  *interface* only; audit-service's in-process emission of a migration event is
  a documented P0 gap (ADR-0044 §Verification already defers it).

## Files in scope

- **Create:**
  - `packages/go/middleware/go.mod` — `aireos.local/packages/go/middleware`; requires `github.com/go-chi/chi/v5`.
  - `packages/go/middleware/middleware.go` — `RequireCapability(Verifier, allowedEdges...) func(http.Handler) http.Handler`, `CapabilityFromContext`, the `Error`-envelope writer.
  - `packages/go/middleware/verifier.go` — the `Verifier` port, `Capability` / `Scope` types, `ErrUnauthorized`.
  - `packages/go/migrate/go.mod` — `aireos.local/packages/go/migrate`; requires `github.com/golang-migrate/migrate/v4`.
  - `packages/go/migrate/runner.go` — `Runner` (embed.FS + DSN), `Up` / `Version` / `Force`, `OnApply` hook, re-exported `ErrNoChange`.
  - `packages/go/migrate/subcommand.go` — `RunSubcommand(Runner, args)` → `<service> migrate up|version|force <v>` (no `down` — audit-service forward-only).
  - `oapi-codegen.yaml` — chi-server + models config targeting `services/audit-service/internal/server/gen.go`.
  - `docs/tickets/T-004a-shared-go-scaffolding.md` — this ticket file.
- **Modify:**
  - `go.work` — `use ./packages/go/middleware` + `./packages/go/migrate`; document the module-path prefix.
  - `Taskfile.yml` — add `codegen` and `migrate` targets.
  - `packages/go/README.md` — mark `middleware/` + `migrate/` landed.
  - `docs/tickets/README.md` — add the T-004a row.

- **Test:** none executable here (these are infrastructure libraries exercised
  by T-004b's service tests). See *Verification* + the gap note.

## Acceptance criteria

- [ ] `packages/go/middleware/` is a Go module under `go.work` exporting a
      `func(http.Handler) http.Handler` (`RequireCapability`) and a `Verifier`
      port; no `github.com/aidantwoods/go-paseto` import (isolated to T-008).
- [ ] `packages/go/migrate/` is a Go module under `go.work` exporting `Runner`
      and `RunSubcommand`; offers `up|version|force` and **no `down`**.
- [ ] `go.work` activates both modules and records the `aireos.local` prefix.
- [ ] `oapi-codegen.yaml` configures the `chi-server` generator against
      `docs/api-contracts/openapi.v3.fragment.yaml` (ADR-0043).
- [ ] `Taskfile.yml` has `codegen` and `migrate` targets.
- [ ] **Violating fixture:** a Go file outside `packages/go/auth/` that imports
      `go-paseto` must (once T-011's depguard lands) fail lint — this ticket
      keeps middleware free of that import as the passing counterpart.
- [ ] OpenAPI / schemas updated — **N/A** (paths land in T-004b).
- [ ] Docs / ADR updated — **N/A** (ADR-0001..0047 untouched).
- [ ] Unit tests cover valid + invalid + edge — **N/A here**; the executable
      surface is T-004b.
- [ ] Verification commands run clean (see *Verification*) — **see gap**.

## Out of scope

- `services/audit-service/*`, the migration SQL, the chain logic, the tests,
  and the OpenAPI paths — **T-004b**.
- `packages/go/auth/` (PASETO) and operator login — **T-008** (ADR-0046).
- `packages/go/outbox/` — first producer ticket (ADR-0045).
- `task test` / `task build` audit-service extensions — **T-004b** (they need
  the service module to point at).
- `.golangci.yaml` depguard enforcing the paseto-isolation rule — **T-011**.

## Diff budget

≤12 files / ≤500 LoC. Actual: 8 created + 4 modified = 12 files, ≈360 LoC.

## ADRs required

None new. Executes the §Hand-off notes of ADR-0043 (chi + oapi-codegen +
`packages/go/middleware/`) and ADR-0044 (`packages/go/migrate/`). Consumes
ADR-0046 (Verifier port boundary). All Accepted.

## Verification

```
test -f packages/go/middleware/go.mod
test -f packages/go/migrate/go.mod
grep -q "aireos.local/packages/go/middleware" packages/go/middleware/go.mod
grep -q "use" go.work
grep -q "codegen:" Taskfile.yml && grep -q "migrate:" Taskfile.yml
```

With the Go toolchain present (see gap):

```
go work sync
go build ./packages/go/...
go vet ./packages/go/...
```

### Verification gap (toolchain absent in authoring environment)

`go`, `task`, and `oapi-codegen` were **not on PATH** when this ticket was
authored (only Docker was available). Therefore:

- `go work sync`, `go build ./packages/go/...`, and `go vet` were **NOT run** —
  the modules are authored but uncompiled.
- `go.sum` files are absent (`go mod tidy` could not run); they materialize on
  first build where the toolchain + module cache exist.
- `task codegen` was **NOT run**; the generated stub does not exist yet (see
  `oapi-codegen.yaml` header). T-004b wires the audit handlers with plain chi
  routes against the same contract until codegen is run.

These are the only known gaps; run the commands above where the Go toolchain is
installed to close them.
