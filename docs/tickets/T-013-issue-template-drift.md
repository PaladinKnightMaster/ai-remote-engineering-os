---
name: T-013 Issue-template ladder drift — replace stale inline list with a pointer
about: Doc-hygiene ticket removing the duplicated first-10 backlog list from the issue template so the T-NNN ladder has one source of truth
title: "T-013: Issue-template ladder drift (delete-and-point to docs/tickets/README.md)"
labels: ["ticket", "P2", "hygiene", "docs"]
---

## Ticket ID and link

T-013 — see `docs/tickets/README.md`. No B-NNN counterpart; pure doc
hygiene surfaced during T-002.

## Goal

Remove the duplicated "first-10 backlog" list from
`.github/ISSUE_TEMPLATE/ticket.md` and replace it with a pointer to the
live ladder in `docs/tickets/README.md`, eliminating the drift class
where the template's inline list falls out of sync with the ADRs.

## Priority

P2. Hygiene. The template's inline list was actively misleading
(predated ADR-0045 / ADR-0046 / ADR-0047) but caused no runtime or
build impact; no ADR required.

## Files in scope

- **Modify:**
  - `.github/ISSUE_TEMPLATE/ticket.md` — delete the `## Reference:
    v3.0.1 first-10 backlog` inline list (stale on T-002 "Redis/NATS",
    T-008 "Keycloak/AuthentiK", and the "+ web" Node assumption);
    replace with a `## Reference: the T-NNN ladder` pointer to
    `docs/tickets/README.md`.
  - `docs/tickets/README.md` — index this ticket.

- **Test:** N/A (docs only).

## Acceptance criteria

- [x] `.github/ISSUE_TEMPLATE/ticket.md` no longer contains the literal
      strings "Redis/NATS", "Keycloak/AuthentiK", or a duplicated
      T-001..T-010 list.
- [x] The template points readers to `docs/tickets/README.md` as the
      single source of truth for the ladder.
- [x] `docs/tickets/README.md` indexes T-013.
- [x] No edits to ADR-0001..ADR-0047 or to frozen baseline CSVs
      (`backlog_v3.csv`, `adr_index.csv`, `decision_log_v2.csv`).

## Out of scope

- Refreshing the five `services/*/README.md` "Stack TBD per
  ADR-0002/0003" lines — each per-service ticket (T-004, T-006, …)
  refreshes its own README when it lands real code.
- Editing `docs/requirements/backlog_v3.csv` — frozen v3.0.1 baseline;
  its row text is historical and not corrected here.

## Diff budget

≤12 files / ≤500 LoC. Actual: 2 files, ≈20 LoC.

## ADRs required

None.

## Verification

```
! grep -Eq "Redis/NATS|Keycloak/AuthentiK" .github/ISSUE_TEMPLATE/ticket.md
grep -q "docs/tickets/README.md" .github/ISSUE_TEMPLATE/ticket.md
```
