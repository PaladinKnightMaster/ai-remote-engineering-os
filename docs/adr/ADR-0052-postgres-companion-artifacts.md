# ADR-0052: Offline postgres ships as per-OS companion artifacts (not in the core bundle)

## Status

Accepted — 2026-05-31. **Refines ADR-0051 decision 3.** ADR-0051 said the offline
bundle "ships staged install packages (`.deb`/`.rpm`) + a guide" — i.e. inside the
bundle. Building §OI-c showed that is infeasible under the OI-4 size cap once more
than one OS is targeted; this ADR records the companion-artifact shape instead.
Binding spec for T-021c.

## Context

ADR-0051 (OQ-6) settled "require host postgres, ship staged offline install files."
The §OI-c feasibility probe measured the reality:

- A complete, pure-air-gap postgres-16 + pgcrypto dependency closure is **~80–85 MB
  per OS** (Ubuntu 24.04: 89 `.deb`s / 85 MB; Rocky 9: 81 MB of `.rpm`s), and `.deb`/
  `.rpm` files are already compressed (they do not shrink in a tarball).
- The core application bundle (T-021a) is ~28 MB gzipped. Adding even one OS's
  closure approaches the **OI-4 hard cap of 150 MB gzipped**; two OS families
  (the confirmed Ubuntu/Debian + RHEL-family scope) exceed it; multiple OS
  releases blow past it.

So postgres packages cannot co-reside in the single core bundle for the targeted
multi-OS scope. (Also surfaced: a naive `apt-get install --download-only` + `dpkg -i`
is **not** a self-sufficient closure — the closure must be computed on a pristine
base and installed via a local repo so `apt`/`dnf` resolves dependencies + ordering.)

## Decision

1. **Postgres ships as separate per-OS companion artifacts**, one per supported OS:
   `aireos-postgres-<ostag>.tar.gz` (e.g. `aireos-postgres-ubuntu2404.tar.gz`,
   `aireos-postgres-rocky9.tar.gz`). The **core bundle stays small and universal**
   (~28 MB, one artifact for every host); each operator additionally downloads only
   the postgres companion matching their host OS.
2. **Each companion is a self-contained local package repo** carrying the *complete*
   dependency closure (computed on a pristine base, not `--download-only`), plus an
   `install-postgres.sh` that wires a `file://` apt/dnf repo and installs postgres 16
   + pgcrypto, `initdb`s, and creates the `cockpit` role + database — fully offline
   (no internet, no host package mirror).
3. **v0.1.0 OS scope:** Ubuntu 24.04 (`.deb`) + Rocky/RHEL-family 9 (`.rpm`), amd64
   (ADR-0051 platform scope). Additional OS releases are new companions, no design
   change.
4. **Companions are build artifacts, never committed to git** (like the core bundle);
   they live in `dist/offline/` and carry their own checksums.

## Alternatives considered

- **Postgres in the core bundle (literal ADR-0051 §3).** Rejected: busts OI-4 for
  multi-OS; would also force a single-OS bundle, contradicting the confirmed scope.
- **Relax the OI-4 cap.** Rejected: OI-4 is a customer-facing hard shipping limit;
  raising it lets the bundle grow unboundedly per added OS.
- **Ship only postgres-specific packages, assume a host package mirror.** Rejected
  for v0.1.0: weakens the "installs on a truly isolated host" guarantee (CEO call:
  pure air-gap).
- **`apt-get install --download-only` + `dpkg -i`.** Rejected: not a complete
  closure; leaves unmet deps. The local-repo + full-closure approach resolves cleanly.

## Consequences

### Positive
- Core bundle stays small + universal; OI-4 is preserved.
- Truly air-gapped postgres install (verified offline on Ubuntu 24.04 + Rocky 9).
- Adding an OS = one more companion, no architectural change.

### Negative
- Two artifacts for the operator to fetch (core bundle + their OS's postgres pack)
  instead of one. Documented in the offline install guide.
- The closure is OS-release-specific; each release needs its own companion build.
- We track postgres CVEs for the pinned closures (the host still owns runtime
  patching; ADR-0051's host-postgres boundary is unchanged).

## Verification

- T-021c: `fetch-postgres.sh` builds the per-OS repo; `install-postgres.sh` installs
  postgres 16 + pgcrypto + the cockpit DB from it with **`--network none`** on
  pristine `ubuntu:24.04` and `rockylinux:9`; `pgp_sym_encrypt` succeeds over a TCP
  connection as the `cockpit` role (verified 2026-05-31).
- T-021d (WD-1): the installed stack's `install.sh` connects to this postgres and
  the wedge demo passes.

## Related ADRs

- ADR-0051 — offline-install architecture (decision 3 refined here).
- ADR-0048 §7 — pgcrypto-encrypted target credentials (why pgcrypto is required).
- ADR-0001 — offline-first (the pure-air-gap requirement).
