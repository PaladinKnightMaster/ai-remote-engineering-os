---
name: T-NNN Ticket
about: A scoped ticket aligned with the v3.0.1 backlog
title: "T-NNN: <one-line summary>"
labels: ["ticket"]
---

## Ticket ID and link

T-NNN — see `docs/requirements/backlog_v3.csv`.

## Goal

<one or two sentences>

## Priority

P0 / P1 / P2 / P3 (P0 only unless an approved ADR justifies otherwise)

## Files in scope

- Create: ...
- Modify: ...
- Test: ...

## Acceptance criteria

- [ ] ...
- [ ] ...
- [ ] OpenAPI / schemas updated if applicable
- [ ] Docs / ADR updated if applicable
- [ ] Unit tests cover valid + invalid + edge cases
- [ ] Verification command runs clean

## Out of scope

- ...

## Diff budget

≤12 files / ≤500 LoC unless explicitly approved here.

## ADRs required

- None / ADR-NNNN (specify)

## Verification

Command(s) and expected output.

---

## Reference: v3.0.1 first-10 backlog

- T-001 Repo skeleton + root documentation
- T-002 Docker Compose for Postgres + Redis/NATS + API + web
- T-003 Shared domain types/schemas (Organization, User, Resource, Session, AuditEvent)
- T-004 audit-service minimal append-only API
- T-005 resource registry CRUD API
- T-006 session-broker skeleton (no real remote desktop yet)
- T-007 web dashboard shell
- T-008 auth placeholder adapter (Keycloak/AuthentiK boundary)
- T-009 OpenAPI spec + generated client types
- T-010 CI checks (lint, typecheck, test, schema validation)
