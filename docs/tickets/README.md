# Tickets — T-NNN ladder

This directory is the canonical home for committed T-NNN ticket files. Each file follows the format in `.github/ISSUE_TEMPLATE/ticket.md`.

Two ladders coexist in this repo and should not be confused:

- **B-NNN** — backlog rows in `../requirements/backlog_v3.csv` (frozen v3.0.1 baseline). Requirements/feature scope.
- **T-NNN** — tickets here. Implementation units. Some T-NNN tickets implement a B-NNN row; others are bootstrap or hygiene tickets (skeleton, lint enforcement) with no B-NNN counterpart.

## Index

### Bootstrap and skeleton (T-001..T-010)

These are enumerated in `../../.github/ISSUE_TEMPLATE/ticket.md` and tracked there until each is filed as a ticket file in this directory.

- **T-001** Repo skeleton + root documentation — *complete* (the existing repo is the artifact; see commit history).
- [T-002 — Repo skeleton commit (go.work, pnpm-workspace, Taskfile, MVP compose)](T-002-repo-skeleton-commit.md) — *complete*. (Backlog row title "Postgres + Redis/NATS + API + web" predates ADR-0045 and ADR-0047; the actual P0 compose is Postgres only.)
- [T-003 — Shared domain schemas (Organization, User, Resource, Session, AuditEvent)](T-003-shared-domain-schemas.md) — *complete*.
- **T-004** audit-service minimal append-only API — *split* (a whole service plus the shared Go packages exceeds the diff budget; split into a scaffolding half and a service half at consult sign-off):
  - [T-004a — Shared Go scaffolding (middleware + migrate + codegen wiring)](T-004a-shared-go-scaffolding.md) — *complete*. Lands `packages/go/middleware/` + `packages/go/migrate/`, `task codegen` / `task migrate`, and the `aireos.local` module-path prefix (ADR-0043 / ADR-0044 §Hand-off).
  - [T-004b — audit-service minimal append-only API](T-004b-audit-service.md) — *complete*. The chain-authority service: `POST /v1/events` ingest (dedup + chained-hash), read surface, forward-only migration, tests (ADR-0045 / ADR-0044).
- **T-005** resource registry CRUD API — *pending*.
- **T-006** session-broker skeleton — *pending*.
- **T-007** web dashboard shell — *pending*.
- [T-008 — Auth placeholder adapter (packages/go/auth/, PASETO v4)](T-008-auth-placeholder.md) — *complete*. In-process PASETO v4 capability tokens (`Signer`/`Verifier`/`CapabilityToken`), `AsMiddlewareVerifier` adapter to the T-004a port, and audit-service wired to verify real tokens (`AIREOS_PASETO_KEY`). Operator login + key rotation + `install.sh` deferred to the `services/api` ticket (ADR-0046 §Hand-off).
- [T-009 — OpenAPI contract scaffold (shared components + security + envelopes)](T-009-openapi-contract.md) — *complete*. (Backlog title "+ generated client types" predates ADR-0043/0047, which assign codegen to the consumer tickets — T-004 Go stubs, T-007 TS types; T-009 lands the contract they generate from.)
- [T-010 — CI checks (build, test, schema validation, codegen drift, lint hook)](T-010-ci-checks.md) — *complete*. GitHub Actions `ci.yml`: Go build/vet/test + Postgres-backed integration, oapi-codegen drift gate, OpenAPI + JSON-Schema validation (`infra/scripts/validate_contracts.py`), and the `task lint` stub hook. Dormant until a remote is configured; validation Python-based (npm precedent deferred to T-007).

### Verification-gap closures (T-011..)

- [T-011 — Go-side lint suite (ADR-0044/0045/0046)](T-011-go-lint-suite.md) — P1. Closes the four ADR-0044 / ADR-0045 / ADR-0046 review-enforced verification gaps on the Go stack. Owner: Tech Lead.
- [T-012 — JS-side lint suite (ADR-0047)](T-012-js-lint-suite.md) — P1. Closes the two ADR-0047 review-enforced verification gaps on the JS stack. Owner: Frontend Lead.
- [T-013 — Issue-template ladder drift](T-013-issue-template-drift.md) — P2, *complete*. Replaced the stale inline first-10 list in `.github/ISSUE_TEMPLATE/ticket.md` with a pointer to this file. Surfaced during T-002.
- [T-014 — Per-service OpenAPI contract slicing for oapi-codegen](T-014-per-service-openapi-codegen-slice.md) — P1, *complete*. `task codegen` now emits only the owning service's chi-server surface (operation tags + `include-tags`); audit-service is re-seated onto the generated `ServerInterface`. Closes ADR-0043 §Verification item 2; surfaced during T-004b. Owner: Tech Lead.

## Conventions

- Filename: `T-NNN-short-kebab-slug.md`.
- Format: `.github/ISSUE_TEMPLATE/ticket.md`.
- T-NNN numbers are assigned at ticket creation and never reused. T-001..T-010 are reserved for the bootstrap ladder above; subsequent tickets start at T-011.
- A ticket's dependencies should be expressed by the *target it creates* ("whichever ticket first creates `packages/go/outbox/`") in preference to a T-NNN reference, so the dependency survives renumbering.
- Acceptance criteria must include a concrete violating fixture and a concrete passing fixture for any rule the ticket lands.

## Out of scope for this directory

- Backlog/feature rows — those live in `../requirements/backlog_v3.csv` (frozen) and any future post-v3.0.1 backlog artifact.
- ADRs — those live in `../adr/`.
- Spec docs — those live under the relevant `../<concern>/` directory.
