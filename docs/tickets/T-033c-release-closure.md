---
name: T-033c Ticket
about: Governed Egress Foundation — v0.2.0 release closure (VER-5, SEC-13a gate, ADR-0055, DOC-8/9/10)
title: "T-033c: v0.2.0 release closure — VER-5 legs, SEC-13a gate, ADR-0055, DOC-8/9, release artifacts"
labels: ["ticket", "docs", "release"]
---

## Ticket ID and link

T-033c — final slice of **T-033** (split a/b/c at the 2026-06-11 consult; a =
compose-leg WD-2 e2e `3a12169`; b = OI-6 + installed WD-2 `5f6acdb`). Closes
the verification-automation and documentation criteria so `task release-check`
**is** the v0.2.0 machine gate. Does NOT tag — the tag follows the pre-release
audit + the §12 human WD-2 (CEO action).

## Goal

> `task release-check` covers the whole v0.2.0 surface: policy-service in
> lint/test/build/integration; connector+policy in licenses; the SEC-13(a)
> grep gate (`task sec13-gate`); the live e2e leg (`task e2e`). DOC-8
> (module-map shows connector+policy BUILT), DOC-9 (security docs list the
> enforced edges with handler refs), ADR-0055 (WD-2.5/FE-9 amendment per the
> criteria doc's own change rule) + CANONICAL ladder row, DOC-10
> (`docs/releases/v0.2.0/` notes + manifest + archival criteria copy).

## Priority

P1 — VER-5/VER-7 and DOC-7..10 are must-pass release criteria; this slice makes
the release gate executable as one command.

## Files in scope (11)

- Modify: `Taskfile.yml` — lint/test/build/test-integration gain policy-service;
  licenses gains connector+policy; `release-check` gains `sec13-gate` + the
  `e2e` leg; new `sec13-gate` task (fails on `pgp_sym_decrypt(` call or
  `os.Getenv("AIREOS_RESOURCE_SECRET_KEY")` outside `services/connector-service`
  — call-syntax pattern so doc comments about the removal don't false-positive).
- Modify: `docs/architecture/module-map.md` — v0.2.0 mapping: connector +
  policy BUILT; api/broker rows reflect keyless reality; `<ApprovalPrompt>` row.
- Modify: `docs/security/agent-permission-model.md` +
  `docs/security/egress-policy.md` — enforced edges/decisions with concrete
  handler references (DOC-9).
- Create: `docs/adr/ADR-0055-v0.2.0-durability-rider-rescope.md` (Accepted —
  papers the T-032/T-033 consult decisions) + `CANONICAL_SOURCE_OF_TRUTH.md`
  ladder row + v0.2.0 release-artifacts pointer row.
- Create: `docs/releases/v0.2.0/{release_notes_v0_2_0.md,
  release_manifest_v0_2_0.json, v0.2.0-release-criteria.md}` (the criteria copy
  is verbatim — hash-identical to `docs/evaluation/`; mechanical).
- Create: this file + `docs/tickets/README.md` status flip.

## G2

The criteria doc itself stays unedited; ADR-0055 is the amendment record its
own rule demands. No Accepted ADR touched. The `sec13-gate` scope decision
(call-syntax `pgp_sym_decrypt(`; `Getenv` read pattern) is verified against the
current tree before being encoded.

## Verification

Every `release-check` leg green, per-leg Dockerized on this host (ops
convention): doc-check + validate (python3); lint incl. policy-service;
sec13-gate; licenses incl. connector+policy; Go tests incl. policy; JS
typecheck/tests/build; test-integration against compose-mvp postgres
(AUDIT/CONNECTOR/POLICY_TEST_DATABASE_URL, migrations first); codegen +
`git diff --exit-code`; `task e2e` green (both walkthroughs).

## Definition of done

VER-5/VER-7 executable and passing; DOC-7..10 closed; ADR-0055 in the ladder;
release artifacts honest (status: release-candidate pending human WD-2 + tag).
Commit on `dev`, fast-forward `main`. No push, NO tag.
