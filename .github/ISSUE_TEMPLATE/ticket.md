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

## Reference: the T-NNN ladder

The live, authoritative ticket ladder — including the bootstrap row
descriptions (T-001..T-010) and every post-bootstrap ticket — lives in
[`docs/tickets/README.md`](../../docs/tickets/README.md). Start there.

This template intentionally does **not** duplicate the ladder. The
earlier inline first-10 list drifted out of sync with ADR-0041..ADR-0047
(its event-bus, auth-boundary, and frontend-runtime descriptions all
predated the ADRs that settled those decisions). Keeping a single source
of truth in `docs/tickets/README.md` eliminates that drift class. See
T-013 for the rationale.
