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
- **T-004** audit-service minimal append-only API — *pending*.
- **T-005** resource registry CRUD API — *pending*.
- **T-006** session-broker skeleton — *pending*.
- **T-007** web dashboard shell — *pending*.
- **T-008** auth placeholder adapter — *pending* (now framed by ADR-0046; the placeholder is `packages/go/auth/`).
- **T-009** OpenAPI spec + generated client types — *pending*.
- **T-010** CI checks (lint, typecheck, test, schema validation) — *pending*.

### Verification-gap closures (T-011..)

- [T-011 — Go-side lint suite (ADR-0044/0045/0046)](T-011-go-lint-suite.md) — P1. Closes the four ADR-0044 / ADR-0045 / ADR-0046 review-enforced verification gaps on the Go stack. Owner: Tech Lead.
- [T-012 — JS-side lint suite (ADR-0047)](T-012-js-lint-suite.md) — P1. Closes the two ADR-0047 review-enforced verification gaps on the JS stack. Owner: Frontend Lead.
- [T-013 — Issue-template ladder drift](T-013-issue-template-drift.md) — P2, *complete*. Replaced the stale inline first-10 list in `.github/ISSUE_TEMPLATE/ticket.md` with a pointer to this file. Surfaced during T-002.

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
