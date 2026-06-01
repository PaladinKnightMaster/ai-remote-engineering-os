---
name: T-025 Ticket
about: Release-prep automation (1/3) — task release-check + task doc-check
title: "T-025: release-check + doc-check aggregators (§11a-1, VER-3 / DOC-1) (v0.1.0)"
labels: ["ticket", "devops", "release"]
---

## Ticket ID and link

T-025 — first of the §11a release-prep slices (§11a-1). No B-NNN counterpart.
Lands the two gate aggregators the release criteria require: `task release-check`
(VER-3) and `task doc-check` (DOC-1 automation). Pairs with **T-026** (`task
licenses` + `license-policy.md`, SC-6) and **T-027** (`docs/releases/v0.1.0/`
artifacts, DOC-6). Binding spec: release-criteria §9 / §11a.

## Goal

- **`task doc-check`** (DOC-1): assert every in-repo file path referenced in
  `CANONICAL_SOURCE_OF_TRUTH.md` + every `docs/adr/*.md` resolves on disk, so the
  canonical index + the ADR ladder cannot drift from the tree.
- **`task release-check`** (VER-3): run every must-pass gate (§3–§9) in sequence,
  exiting 0 only if all pass — the single command that gates a release build.

## Priority

P0 — release-gating (VER-3 is required to invoke the release build).

## Files in scope

- Create: `infra/scripts/doc_check.py` (the path walker; matches the existing
  `infra/scripts/validate_contracts.py` Python pattern).
- Modify: `Taskfile.yml` (`doc-check` + `release-check` targets).
- Create: `docs/tickets/T-025-release-check-doc-check.md` (this file).
- Modify: `docs/tickets/README.md`.

4 files, ~110 LoC, within budget.

## doc-check scope (pragmatic, low false-positive)

Checks two reference kinds in CANONICAL + the ADRs: markdown link targets, and
backtick spans that are **unambiguous repo-relative file paths** (begin with a
known top-level dir AND end in a known file extension). Deliberately excludes
code symbols (`packages/go/auth/Signer`), bare directory refs (`packages/go/events/`),
and abbreviated prose names (`module-map.md`) — none are unambiguous file paths.
Also skips URLs, anchors, absolute paths, sibling-repo (`../`) refs (the kit may
be absent in CI), and `<…>`/glob placeholders. Resolves relative to the repo root
or the referencing file's dir; exits non-zero listing every unresolved reference.

## release-check composition (VER-2 → VER-3)

`doc-check → validate → lint → test → test-integration → pnpm typecheck →
pnpm web build → codegen → git diff --exit-code` (codegen-drift). Taskfile stops
on the first non-zero, so it exits 0 only if every gate passes. It needs the full
toolchain (go + node/pnpm + python3) — the CI host or a full dev env; the
integration leg needs a Postgres (set `AUDIT_TEST_DATABASE_URL` /
`BROKER_TEST_DATABASE_URL`, else those tests self-skip). On an offline-only dev
box, the per-leg Docker commands used throughout the tickets remain the path.

## Acceptance criteria (verified)

- [x] `task doc-check` passes on the current repo — **63 in-repo path references
      resolve across 53 files** (CANONICAL + all ADRs), and it would fail loudly on
      any stale full-path reference.
- [x] `task release-check` parses and its dry-run resolves to the correct gate
      sequence (doc-check → validate → lint → test → integration → typecheck →
      web build → codegen → drift). Exits 0 only if all legs pass (Taskfile
      fail-fast).
- [x] The Taskfile lists both tasks (`task --list`).

### Violating vs. passing fixtures

- **doc-check — violating:** an ADR referencing `docs/adr/ADR-9999.md` (absent) →
  non-zero with the unresolved ref listed. **Passing:** the current ladder (all
  full-path refs resolve).
- **release-check — violating:** any leg exits non-zero (e.g. lint failure) →
  release-check stops + exits non-zero. **Passing:** all gates green → exit 0.

## Out of scope

- `task licenses` + `docs/security/license-policy.md` (SC-6) → **T-026**.
- `docs/releases/v0.1.0/` artifacts (DOC-6) → **T-027**.
- Running the full `release-check` green here (re-runs already-verified tasks +
  needs the full toolchain + Postgres; it runs in CI per VER-1). Structure +
  doc-check are verified; the aggregate is CI's job.

## ADRs required

- None — automation of existing release criteria (§9 / §11a).

## Verification

```sh
# doc-check (python only):
docker run --rm -v "$PWD":/work -w /work python:3.12-alpine python3 infra/scripts/doc_check.py
# release-check structure (Taskfile parse + dry-run):
docker run --rm -v "$PWD":/work -w /work golang:1.23 bash -c \
  'go run github.com/go-task/task/v3/cmd/task@v3.40.0 release-check --dry'
```

Confirmed 2026-05-31: doc-check OK (63 refs / 53 files); release-check parses +
dry-runs to the full VER-3 gate sequence.
