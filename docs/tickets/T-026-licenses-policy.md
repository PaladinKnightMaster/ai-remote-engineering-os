---
name: T-026 Ticket
about: Release-prep automation (2/3) — task licenses + license-policy.md
title: "T-026: task licenses license gate + license-policy.md (§11a-2, SC-6) (v0.1.0)"
labels: ["ticket", "devops", "security", "release"]
---

## Ticket ID and link

T-026 — second §11a release-prep slice (§11a-2). No B-NNN counterpart. Lands the
SC-6 license-posture automation + policy. Pairs with T-025 (release-check +
doc-check) and T-027 (release artifacts). Binding spec: release-criteria SC-6.
`task licenses` is wired into `task release-check` (VER-3).

## Goal

- **`task licenses`** (SC-6): report/check the full transitive license set of the
  shipped artifact — Go (the service binaries' closure) + JS (production deps) —
  and exit non-zero on any license not in the allowlist.
- **`docs/security/license-policy.md`**: the acceptable-license list + rationale +
  the check's scope (incl. bundled Caddy + postgres).

## Priority

P0 — release-gating (SC-6; part of release-check / VER-3).

## Files in scope

- Create: `infra/scripts/check-js-licenses.mjs` (JS production-license gate over `pnpm licenses list --prod --json`).
- Modify: `Taskfile.yml` (`licenses` target; wired into `release-check`).
- Create: `docs/security/license-policy.md` (SC-6 policy).
- Create: `docs/tickets/T-026-licenses-policy.md` (this) + Modify: `docs/tickets/README.md`.

5 files, ~150 LoC, within budget.

## Design (grounded by tooling probes)

- **Go:** the built-in `go-licenses check ./... --allowed_licenses=<allowlist>
  --ignore aireos.local` against each shipped service (`api`, `audit-service`,
  `session-broker`), `GOWORK=off` (go-licenses rejects workspace mode). `--ignore`
  skips first-party "Unknown" packages; the asm-file warnings are non-fatal. The
  three services' closures cover every runtime Go dependency (incl. `authctl`'s).
- **JS:** `pnpm licenses list --prod --json` → `check-js-licenses.mjs` checks each
  SPDX id against the allowlist. **Production deps only** — devDeps are build-time,
  never shipped.
- **Allowlist (SC-6):** MIT, BSD-2/3-Clause, Apache-2.0, ISC, MPL-2.0, LGPL-2.1/3.0
  (linking-exception only; manual caveat), Unlicense, 0BSD, CC0-1.0, Python-2.0.
  Default-deny: anything unlisted (GPL/AGPL family included) fails.
- **Bundled binaries** (documented in the policy, not source-scanned): Caddy
  (Apache-2.0), PostgreSQL 16 (PostgreSQL License) — both acceptable.

## Acceptance criteria (verified)

- [x] `task licenses` Go leg: `go-licenses check` exits 0 for all three services
      (third-party licenses = MIT / BSD-3-Clause / MPL-2.0 / Apache-2.0).
- [x] `task licenses` JS leg: `check-js-licenses.mjs` → "all production dependency
      licenses allowed (ISC, MIT)".
- [x] `docs/security/license-policy.md` documents the allowlist + rationale + scope
      + bundled-binary licenses.
- [x] `task licenses` is part of `task release-check`; the Taskfile parses + the
      `licenses` dry-run resolves to the Go loop + the JS check.

### Violating vs. passing fixtures

- **Disallowed license — violating:** a GPL-3.0 dep → `go-licenses check` /
  `check-js-licenses.mjs` exits non-zero, naming the package. **Passing:** the
  current closure (all permissive + MPL-2.0 weak-copyleft) → exit 0.

## Out of scope

- `docs/releases/v0.1.0/` artifacts (DOC-6) → **T-027**.
- Deep license-text auditing / SBOM generation (beyond SPDX-id allowlisting) — not
  required at v0.1.0.

## ADRs required

- None — automates release-criteria SC-6.

## Verification

```sh
ALLOWED="MIT,BSD-2-Clause,BSD-3-Clause,Apache-2.0,ISC,MPL-2.0,LGPL-2.1,LGPL-3.0,Unlicense,0BSD,CC0-1.0,Python-2.0"
# Go (per shipped service):
docker run --rm -v "$PWD":/work -w /work golang:1.23 bash -c "
  for m in services/api services/audit-service services/session-broker; do
    (cd \$m && GOWORK=off go run github.com/google/go-licenses@v1.6.0 check ./... --allowed_licenses=\"$ALLOWED\" --ignore aireos.local); done"
# JS (production):
docker run --rm -v "$PWD":/work -w /work node:22-alpine sh -c "
  corepack enable && corepack prepare pnpm@10.32.1 --activate && pnpm install --frozen-lockfile &&
  node infra/scripts/check-js-licenses.mjs \"$ALLOWED\""
```

Confirmed 2026-05-31: Go all three services OK; JS prod = ISC/MIT OK.
