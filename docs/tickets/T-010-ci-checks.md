---
name: T-010 CI checks (build, test, schema validation, codegen drift, lint hook)
about: A GitHub Actions workflow that runs the verified build/test/validation loop on every push and PR
title: "T-010: CI checks (lint, typecheck, test, schema validation)"
labels: ["ticket", "P0", "bootstrap"]
---

## Ticket ID and link

T-010 — see `docs/tickets/README.md`. Last of the bootstrap ladder (T-001..T-010).
Wires the build/test/validation loop that T-004/T-014 verified by hand into
CI so it runs automatically. Owns the JSON-Schema / OpenAPI validator that
T-003 and T-009 explicitly deferred here. Lead lens: **Tech Lead**;
**Offline-Install Engineer** (supply-chain surface of CI tooling).

## Goal

Add `.github/workflows/ci.yml` running, on every push to `main`/`dev` and every
PR: Go build + vet + unit tests + DB-backed integration tests (with a Postgres
service), oapi-codegen drift detection, OpenAPI + JSON-Schema validation, and
the `task lint` stub hook for T-011/T-012 to extend.

## Priority

P0. Bootstrap; locks in the green state before more services land. (The two
bugs found by hand in T-004b are exactly the class this would catch.)

## Scope decisions (settled in consult)

- **GitHub Actions** as the CI platform (`.github/` already present). The repo
  has **no remote yet** (`../VERSION_CONTROL.md`), so the workflow is committed
  but **dormant until the repo is pushed to a GitHub remote**. Every job mirrors
  a command already proven locally against the docker-compose Postgres.
- **Validation is Python-based — no npm dependency introduced here.** `jsonschema`
  (draft 2020-12) validates the T-003 fixtures; `pyyaml` backs the OpenAPI
  structural checks. **Deviation from T-009's note** (which guessed T-010 would
  own "the first npm devDependency / root package.json"): the only validation
  need today is schema/contract checking, which Python covers with zero lockfile
  to maintain. The first npm/pnpm dependency + `pnpm-lock.yaml` precedent is
  deferred to **T-007** (the frontend ticket), which needs the JS toolchain
  regardless. This keeps the P0 supply-chain surface minimal (ADR-0041) and
  avoids a lockfile that exists only for a linter.
- **Go version pinned to 1.23** in CI to match `go.work` / the `go.mod` files
  (the oapi-codegen runtime is pinned to a 1.23-compatible release in T-014).
- **Codegen drift is a CI gate**: CI regenerates `gen.go` and fails on any diff,
  so the committed stub can never drift from the contract.
- **Lint is a stub hook**: CI runs `task lint` (a no-op banner today). The Go
  suite lands with **T-011** and the JS suite with **T-012**, both extending
  `task lint`; CI picks them up automatically. Typecheck is JS-only and has no
  surface until `apps/web` (T-007).

## Files in scope

- **Create:**
  - `.github/workflows/ci.yml` — jobs: `go`, `codegen`, `contract`, `lint`.
  - `infra/scripts/validate_contracts.py` — JSON-Schema fixture validation
    (valid passes / invalid fails, keyed by schema title) + OpenAPI structural
    checks (3.1.0, audit paths, ingest DTO rejects chain fields, refs resolve).
  - `docs/tickets/T-010-ci-checks.md` — this ticket.
- **Modify:**
  - `Taskfile.yml` — add `validate` (runs the python validator) and
    `test-integration` (the `-tags integration` store tests) targets.
  - `docs/tickets/README.md` — mark T-010 complete.

## Acceptance criteria

- [ ] `.github/workflows/ci.yml` defines `go`, `codegen`, `contract`, and `lint`
      jobs and triggers on push (`main`/`dev`) + PR.
- [ ] The `go` job stands up a `postgres:16` service, builds, vets, runs unit
      tests, applies migrations, and runs the `-tags integration` tests.
- [ ] The `codegen` job regenerates `gen.go` and fails on drift
      (`git diff --exit-code`).
- [ ] The `contract` job runs `validate_contracts.py`; **passing fixture:** all
      `valid.json` entries validate; **violating fixture:** every `invalid.json`
      entry is rejected, and a forged `AuditEventIngest` chain field is caught by
      the OpenAPI check.
- [ ] `validate_contracts.py` exits non-zero on any schema/contract failure.
- [ ] `Taskfile.yml` has `validate` and `test-integration` targets.
- [ ] No npm/pnpm dependency or lockfile is introduced (deferred to T-007).
- [ ] OpenAPI / schemas updated — **N/A** (no contract change).
- [ ] Docs / ADR updated — **N/A** (ADR-0001..0047 untouched).
- [ ] Unit tests cover valid + invalid + edge — the validator's valid/invalid
      fixtures are exactly this surface.
- [ ] Verification command runs clean (see *Verification*).

## Out of scope

- The Go lint suite (`.golangci.yaml`, depguard rules) — **T-011**.
- The JS lint/typecheck suite (eslint, tsconfig) and the first npm/pnpm
  dependency + `pnpm-lock.yaml` — **T-012 / T-007**.
- A full OpenAPI linter (redocly/spectral) — the structural checks here cover
  the P0 invariants; a deeper lint can land with the JS toolchain if wanted.
- Release/publish pipelines, container builds, deploy — feature/devops tickets.

## Diff budget

≤12 files / ≤500 LoC. Actual: 2 created (workflow + validator) + 2 modified
(Taskfile, README) + this ticket = 5 files, ≈230 LoC.

## ADRs required

None new. Consumes ADR-0042 (`task` entry points), ADR-0043 (oapi-codegen),
ADR-0044/0045 (the audit migration + integration tests). All Accepted.

## Verification

Locally (the CI commands, proven via Docker):

```
# contract + schema validation
python3 infra/scripts/validate_contracts.py        # all checks pass

# the workflow parses as valid YAML
python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci.yml')); print('ci.yml ok')"

# the Go loop CI runs (golang:1.23 + Postgres)
go build ./packages/go/... ./services/audit-service/...
go test ./packages/go/... ./services/audit-service/...
go test -tags integration ./services/audit-service/internal/store/...
```

### Status

`validate_contracts.py` and the YAML parse were run and **pass**; the Go loop
was verified during T-004/T-014. The workflow itself runs on GitHub once a
remote is configured (see `../VERSION_CONTROL.md`) — documented-dormant until
then, not a gap in the checks themselves.
