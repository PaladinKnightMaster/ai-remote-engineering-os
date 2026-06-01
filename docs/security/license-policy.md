# License policy (v0.1.0)

Formalizes release-criteria **SC-6**: which dependency licenses are acceptable in
a shipped AI ReOS artifact, and how that posture is enforced. Automated by
**`task licenses`** (T-026).

## Posture

**Permissive + weak-copyleft are acceptable; strong-copyleft (GPL family, AGPL)
blocks the release.** The product ships self-hosted to customers as static
binaries + bundled components (ADR-0001/0041); a strong-copyleft obligation in a
shipped, linked dependency would impose source-disclosure terms we do not accept
at P0.

### Acceptable (allowlist)

`MIT`, `BSD-2-Clause`, `BSD-3-Clause`, `Apache-2.0`, `ISC`, `MPL-2.0`,
`LGPL-2.1`, `LGPL-3.0` (**linking-exception use only** — see caveat),
`Unlicense`, `0BSD`, `CC0-1.0`, `Python-2.0`.

### Blocked

`GPL-2.0`, `GPL-3.0`, `AGPL-1.0`, `AGPL-3.0`, and any other strong-copyleft or
source-available/non-OSI license. Anything not on the allowlist fails the gate
(default-deny).

### Caveats / manual review

- **LGPL** is allowed only when used via dynamic linking / the library's linking
  exception. The automated check sees only the SPDX id, so a static-linked LGPL
  dependency requires manual review. (None present at v0.1.0.)
- **MPL-2.0** (weak-copyleft, file-level) is accepted: it obliges sharing changes
  to MPL-covered *files* only, not the whole work. (Used by `hashicorp/errwrap`,
  `hashicorp/go-multierror`.)

## Scope + tooling (`task licenses`)

- **Go — full transitive closure of the shipped binaries.** `go-licenses check`
  runs against `services/api`, `services/audit-service`, `services/session-broker`
  (whose closures include `packages/go/*` and every third-party module, incl. the
  `authctl` deps), with `--allowed_licenses=<allowlist>` and `--ignore aireos.local`
  (first-party). `GOWORK=off` (go-licenses rejects workspace mode).
- **JS — production dependencies only.** `pnpm licenses list --prod` feeds
  `infra/scripts/check-js-licenses.mjs`, which fails on any non-allowlisted SPDX.
  devDeps (eslint, vite, vitest, …) are build-time, never shipped, so they are out
  of scope for the shipped-artifact posture.
- **Bundled third-party binaries** (not source deps, documented here):
  - **Caddy** (offline bundle, ADR-0050/0051) — **Apache-2.0**. Acceptable.
  - **PostgreSQL 16** (per-OS companion, ADR-0052) — **PostgreSQL License**
    (permissive, BSD/MIT-style). Acceptable. Shipped as the OS distribution's
    packages; not statically linked into our binaries.

## Current posture (verified at T-026)

- Go third-party: `MIT`, `BSD-3-Clause`, `MPL-2.0`, `Apache-2.0` — all acceptable.
- JS production: `ISC`, `MIT` — all acceptable.
- `task licenses` exits 0.

## Review triggers

Re-run `task licenses` (it's part of `task release-check`, VER-3) on any dependency
change. Add a license to the allowlist only by updating this doc + the `ALLOWED`
list in `Taskfile.yml` together, with the rationale recorded here.
