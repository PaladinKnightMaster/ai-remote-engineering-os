---
name: T-011 Go-side lint suite closing ADR-0044/0045/0046 P0 verification gaps
about: P1 lint ticket enforcing forward-only audit migrations, outbox-only producers, paseto isolation, and forbidden-features absence
title: "T-011: Go-side lint suite (ADR-0044/0045/0046 verification gaps)"
labels: ["ticket", "P1", "lint", "ci"]
---

## Ticket ID and link

T-011 — see `docs/tickets/README.md`. Closes P0 verification gaps documented in ADR-0044 §Verification, ADR-0045 §Verification, and ADR-0046 §Verification.

## Goal

Land four Go-side enforcement mechanisms, wired into `task lint`, that fail CI on commits violating already-decided ADR-0044/0045/0046 policy. The policies themselves are settled; this ticket closes their P1 enforcement deferral.

## Priority

**P1.** Each underlying ADR (0044, 0045, 0046) explicitly defers its lint to P1 and names the gap as review-enforced at P0. No new ADR required.

## Files in scope

- **Create:**
  - `tools/migrate-lint/main.go` — small Go tool parsing `services/audit-service/migrations/*.sql` for forbidden DDL on integrity-bearing tables (the `events` table and any chained-hash participant). Walks files; flags `DROP`, `TRUNCATE`, `ALTER … DROP` on the protected set; non-zero exit on violation. Implements ADR-0044 forward-only / no-destructive-DDL rule.
  - `tools/migrate-lint/testdata/violating/9999_break_audit.down.sql` — fixture containing `DROP TABLE events;` (must be flagged).
  - `tools/migrate-lint/testdata/clean/0001_init.up.sql` — fixture with allowed DDL (must pass).
  - `tools/migrate-lint/main_test.go` — exercises both fixtures.
  - `tools/forbidden-features-check/main.go` — path-existence assertion. Fails if any file matching `password_reset*.go`, `signup*.go`, `oauth_*.go`, or `jwks_*.go` exists under `services/api/internal/auth/`. Implements ADR-0046 forbidden-features rule.
  - `tools/forbidden-features-check/main_test.go` — verifies the check correctly flags a fixture violation and passes on absence.
  - `.golangci.yaml` (or extend if T-002 lands a stub) — adds:
    - `depguard` rule pinning `github.com/aidantwoods/go-paseto` to files under `packages/go/auth/...` only (ADR-0046 paseto isolation).
    - `depguard` rule forbidding the generated audit-service client package outside `packages/go/outbox/...` (ADR-0045 outbox-only).
    - `forbidigo` rule banning string literals matching `/v1/events` outside `packages/go/outbox/` and `services/audit-service/` (ADR-0045 defense-in-depth).
- **Modify:**
  - `Taskfile.yml` — extend `task lint` to invoke `golangci-lint run`, `go run ./tools/migrate-lint ./services/audit-service/migrations`, and `go run ./tools/forbidden-features-check`. Ordering: native Go vet/test first, then the three custom checks.
- **Test:** as listed under *Create* — each tool has a `_test.go` that exercises a violating fixture (must fail) and a clean fixture (must pass).

## Acceptance criteria

- [ ] `task lint` fails when `services/audit-service/migrations/` contains any `DROP|TRUNCATE|ALTER … DROP` against `events` or any other chained-hash-participating table (verified against the `tools/migrate-lint/testdata/violating/` fixture).
- [ ] `task lint` passes on the clean fixture and on the real `services/audit-service/migrations/0001_init.up.sql` once the audit-service ticket lands.
- [ ] `task lint` fails on any Go file outside `packages/go/outbox/...` importing the generated `services/audit-service` client package (`depguard`).
- [ ] `task lint` fails on any string literal matching `/v1/events` outside `packages/go/outbox/` and `services/audit-service/` (`forbidigo`).
- [ ] `task lint` fails on any Go file outside `packages/go/auth/...` importing `github.com/aidantwoods/go-paseto` (`depguard`).
- [ ] `task lint` fails if any file matching the forbidden-features patterns exists under `services/api/internal/auth/`.
- [ ] `task lint` passes on `main` after this ticket lands (all targets clean or non-existent).
- [ ] OpenAPI / schemas updated if applicable — **N/A** (no contract changes).
- [ ] Docs / ADR updated if applicable — append a one-line "Lint enforced by T-011" pointer to each of ADR-0044, ADR-0045, ADR-0046 *Verification* sections **only if** the user re-opens the no-edit-existing-ADRs rule; otherwise leave the ADR text intact and let this ticket be the reverse pointer.
- [ ] Unit tests cover valid + invalid + edge cases — see `_test.go` files under each `tools/<linter>/`.
- [ ] Verification command runs clean (see *Verification* below).

## Out of scope

- The ESLint and `package.json`-shape lints for ADR-0047 — covered by T-012.
- Re-evaluating Atlas as a SQL linter — ADR-0044 holds Atlas in reserve; reopening that is a new ADR, not this ticket.
- Per-token revocation lists (ADR-0046 explicitly defers; not a lint).
- Outbox-depth alerting thresholds (ADR-0045 *Verification* gap, owned by a metrics ticket).
- Auto-fix / `--fix` modes; lints are detection-only at P0.

## Diff budget

≤12 files / ≤500 LoC. The four tools plus their tests, fixtures, the `.golangci.yaml` extension, and the `Taskfile.yml` modification are within budget.

## ADRs required

None new. This ticket implements verification-gap closure for **ADR-0044**, **ADR-0045**, and **ADR-0046**. If during implementation a contributor proposes Atlas (held in reserve in ADR-0044) as the SQL linter, that requires a new ADR — stop and propose ADR-0048 before continuing.

## Verification

```sh
# From repo root, with the ticket's changes applied:
task lint                                  # passes on main
task lint -- --golangci-args="-v"          # detailed run

# Negative tests — each fixture must fail:
go test ./tools/migrate-lint/...
go test ./tools/forbidden-features-check/...
```

Expected output: each `_test.go` asserts both a violating fixture produces non-zero exit and a clean fixture produces zero exit.

## Activation prerequisites

This ticket sits in the backlog until **all** of the following land. Each prerequisite below is the *first* ticket that creates the named target; T-NNN renumbering does not affect this dependency.

1. T-002 (skeleton) — provides `Taskfile.yml` with a `task lint` target stub and the Go workspace under `go.work`.
2. Whichever ticket first creates `services/audit-service/migrations/` (likely T-004 per the v3.0.1 first-10 backlog).
3. Whichever ticket first creates `packages/go/outbox/` (binds the ADR-0045 outbox-only rule's target package).
4. Whichever ticket first creates `packages/go/auth/` and `services/api/internal/auth/` (binds the ADR-0046 paseto-isolation and forbidden-features rules' targets).

The lint MUST land in the same milestone as prerequisite (4) at the latest — not as a follow-up — so the rule is born already enforced against real code rather than a skip-list.

## Owner / cross-review

- **Owner:** Tech Lead.
- **Cross-review (required):** Release Guard (CI surface), Security Reviewer (forbidden-features list + paseto isolation).
- **Cross-review (optional):** Frontend Lead — not required; rules do not touch JS surface.

## Related

- ADR-0044 — DB migration tool. *Verification* §Known gap at P0: "No CI lint enforces the `audit-service` forward-only / no-destructive-DDL rule." Closed here.
- ADR-0045 — Event bus. *Verification* §Known gap at P0: "No CI lint enforces that producer services use the outbox for `audit-service` writes instead of direct HTTP POSTs." Closed here.
- ADR-0046 — Auth adapter. *Verification* §Known gaps at P0: (a) "No CI lint enforces 'do not import `github.com/aidantwoods/go-paseto` outside `packages/go/auth/`'", (b) "No CI lint enforces the *forbidden features* list." Both closed here.
- ADR-0042 — Monorepo tooling. `task lint` is the canonical orchestration entry point this ticket extends.
- ADR-0043 — API framework. Generated audit-service client (the target of the ADR-0045 depguard rule) is produced by `oapi-codegen` client mode under this ADR's contract.
- T-012 — JS-side lint suite (ADR-0047). Sibling ticket on the JS stack.
