---
name: T-027 Ticket
about: Release-prep automation (3/3) — docs/releases/v0.1.0/ artifacts (DOC-6)
title: "T-027: v0.1.0 release artifacts — notes + manifest + criteria archive (§11a-3, DOC-6)"
labels: ["ticket", "release", "docs"]
---

## Ticket ID and link

T-027 — third and final §11a release-prep slice (§11a-3). No B-NNN counterpart.
Lands the DOC-6 release artifact set. Completes §11a. Binding spec:
release-criteria DOC-6.

## Goal

Create `docs/releases/v0.1.0/` with the three DOC-6 artifacts:
- `release_notes_v0_1_0.md` — what landed, the remaining gate, known limits/deferrals.
- `release_manifest_v0_1_0.json` — ADR coverage (0041–0052), ticket roll-up, release-
  gate status, and the offline-bundle SHA256-manifest reference.
- `v0.1.0-release-criteria.md` — a verbatim archival copy of the criteria (immutability).

## Priority

P0 — release-gating (DOC-6).

## Files in scope

- Create: `docs/releases/v0.1.0/release_notes_v0_1_0.md`
- Create: `docs/releases/v0.1.0/release_manifest_v0_1_0.json`
- Create: `docs/releases/v0.1.0/v0.1.0-release-criteria.md` (copied from `docs/evaluation/`)
- Modify: `CANONICAL_SOURCE_OF_TRUTH.md` (release-pointer row for the v0.1.0 artifacts)
- Create: `docs/tickets/T-027-release-artifacts.md` (this) + Modify: `docs/tickets/README.md`

6 files, docs-only, within budget.

## Notes

- These are **living artifacts** finalized at tag time: status is
  `in-preparation` while **T-024** (operator audit-timeline view) — the last
  release gate — is still pending. The manifest lists T-024 under
  `remaining_release_gates`; the notes call it out under "Remaining release gate."
- The SHA256-manifest reference points at the per-build
  `dist/offline/.../SHA256_MANIFEST.txt` (built by `task offline-bundle`), verified
  with `sha256sum -c` (OI-5) — not embedded hashes (those are per-build).

## Acceptance criteria (verified)

- [x] `docs/releases/v0.1.0/` contains the three DOC-6 artifacts; the manifest is
      valid JSON; the criteria copy is a verbatim duplicate of the source.
- [x] `CANONICAL_SOURCE_OF_TRUTH.md` points at the v0.1.0 release artifacts; `task
      doc-check` still passes (63 refs / 53 files).
- [x] The manifest's ticket roll-up + ADR coverage + gate status reflect the actual
      repo state (T-024 pending; everything else for v0.1.0 complete).

## Out of scope

- The actual git tag / release build — happens after **T-024** lands (the last
  release-gating item). These artifacts are then finalized (status →
  `released`, hashes attached).

## ADRs required

- None — DOC-6 artifact authoring.

## Verification

```sh
python3 -c "import json; json.load(open('docs/releases/v0.1.0/release_manifest_v0_1_0.json'))"
diff docs/evaluation/v0.1.0-release-criteria.md docs/releases/v0.1.0/v0.1.0-release-criteria.md
docker run --rm -v "$PWD":/work -w /work python:3.12-alpine python3 infra/scripts/doc_check.py
```

Confirmed 2026-05-31: manifest valid JSON; criteria copy verbatim; doc-check OK.
