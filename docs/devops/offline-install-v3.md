# Offline install

How to install the product on a host with no internet access. The core bundle and a
per-OS postgres companion are built on a connected machine and installed on a target
host with no public-internet egress. Design records: **ADR-0051** (architecture) and
**ADR-0052** (postgres companions).

> ⚠ **Known limitation — read §Install step 1 before you start.** The prebuilt postgres
> companion fails on hosts that have applied OS security updates since it was built.
> A bring-your-own-PostgreSQL workaround is documented there.

Offline-first (ADR-0001) governs the **target** host: the bundle + companions are
built on a connected machine and installed on a host with no public-internet egress.

## What ships

| Artifact | Contents | Built by |
|---|---|---|
| `aireos-offline-<v>.tar.gz` (~45 MB gz) | static `linux/amd64` binaries (api, audit-service, session-broker, **connector-service, policy-service, mcp-gateway** [P1, v0.3.0/v0.4.0 — this table was frozen at v0.1.0 and undercounted them until v0.12.0's DOC-24 pass], **authctl**) + **Caddy** + the SPA + `Caddyfile` + systemd units + `install.sh` + **`setup-loopback-demo.sh`** (the ONB-3 loopback demo target, when present in the source tree — v0.12.0) + `SHA256_MANIFEST.txt` | `task offline-bundle` (T-021a/b) |
| `aireos-postgres-<ostag>.tar.gz` (~90 MB) | a self-contained postgres-16 + pgcrypto local repo (full closure) + `install-postgres.sh` + **`SHA256_MANIFEST.txt`** (v0.13.0, ADR-0070 D1) | `build-postgres-companions.sh` (T-021c) |

Postgres is a separate per-OS companion (ADR-0052), not in the core bundle.
OS scope: Ubuntu 24.04 + Rocky/RHEL-family 9, amd64. Windows = browser
client only (no server build).

## Build (connected host)

```sh
task offline-bundle                                           # core bundle
bash infra/offline/postgres/build-postgres-companions.sh ubuntu2404 rocky9
# artifacts land in dist/offline/ ; verify with sha256sum -c on the unpacked core bundle.
```

⚠ **`task offline-bundle` runs directly on the host — it needs Go 1.26, Node 22 and pnpm
installed there.** It does **not** containerise itself. On a host without them, run the
build inside a container that has both toolchains; the procedure the project itself uses
is recorded in `docs/releases/v0.15.0/verification_runbook.md` (Step 2).

**Sign both artifacts — a separate, host-side step, never part of the build above**
(ADR-0070 D1/D3). The release private key must never enter a build environment. Signing
runs afterward, on the machine holding the release private key:

```sh
task sign-release -- --key /path/to/aireos-release.key      # signs BOTH artifacts present
# -> dist/offline/aireos-offline-<version>.tar.gz.sig
# -> dist/offline/aireos-postgres-<ostag>.tar.gz.sig
```

**Both artifacts are signed, not just the core bundle.** The postgres companion is
~90 MB, ships a full package repository, and runs `install-postgres.sh` **as root**
on the target host — leaving it unsigned while signing the artifact that already had
a manifest would be indefensible (ADR-0070 Context, gap 1).

The signing step prints the public key's SHA-256 fingerprint — publish it in the
release notes (ADR-0070 D4) so an operator who obtains the public key over a second
channel can cross-check it. See **"Release key custody"** below for where the
private key comes from and where it must never live.

Transfer the core bundle, its `.sig`, the postgres companion for your target OS, and
its `.sig` to the air-gapped host — **and the public key**, `aireos-release.pub`,
distributed out-of-band (SEC-44: the verifier is `openssl`, already on the target
host; only the public key needs distributing).

## Release key custody

**The production release key is never generated, stored, printed, or committed by
any script in this repo.** It is minted out-of-band by the CEO, on a machine that
never runs untrusted build code:

```sh
openssl genpkey -algorithm ed25519 -out aireos-release.key
openssl pkey -in aireos-release.key -pubout -out aireos-release.pub
```

- The **private key** (`aireos-release.key`) is held by the maintainer, **never**
  committed to this repository, and **never** placed on a build host — the same host
  that runs `task offline-bundle`'s container must not also hold this key.
- It is **distinct from every key `install.sh` mints** (the per-workload PASETO keys,
  ADR-0069's chain-head key) — those are per-install and machine-generated; this one
  is per-project and human-held. Never substitute one for the other.
- The **public key** (`aireos-release.pub`) and its SHA-256 fingerprint are published
  with each release (out-of-band distribution, SEC-44) and never retired — key
  rotation is additive-forever (ADR-0070 D8): an operator verifying an older release
  needs the key it was signed under.
- **What the signature proves, stated plainly:** this artifact was produced by
  someone holding the release key and has not been altered since. **The signature
  itself** does not prove the build was clean, and does not tie the artifact to a
  source revision, and claims **no SLSA level** — this repo has no CI and no remote
  (ADR-0070 D3, unedited by this correction). Separately — and NOT part of what
  the signature attests — every shipped Go binary embeds its own
  `vcs.revision`/`vcs.modified` (Go's `buildvcs` stamping, on by default, not
  stripped by `-trimpath -ldflags "-s -w"`); inspect with `go version -m
  bin/api`. That is a real, checkable provenance fact, but it is self-reported by
  the build and unsigned, and it is only trustworthy when the release is cut from
  a clean tree (`vcs.modified=false`) — a dirty tree stamps `vcs.modified=true`
  into every binary it ships (see the OI-35 reproducibility report, T-130b).

**A throwaway fixture keypair for tests** lives at
`infra/offline/testdata/fixture-release-key/` — clearly labelled
`FIXTURE-DO-NOT-USE-*`, committed on purpose, and unsuitable for any real release
(anyone with this repo already holds it). Use it only to exercise `task sign-release`
and `task provenance-check`; never point a real verification at it.

## Install (target host, offline, as root)

**Verify BEFORE you extract anything (ADR-0070 D2/D4, SEC-44).** `install.sh` runs
*from inside* the extracted bundle, so it can never verify the archive it came from —
a check that runs after untrusted content is already unpacked is theatre. Verification
is therefore a separate operator step, using `openssl` (already on the target host —
nothing extra to distribute; only the public key needs to travel out-of-band):

```sh
# core bundle — run this in the directory holding the .tar.gz, its .sig, and
# aireos-release.pub (obtained out-of-band; compare its fingerprint against the
# one published in the release notes before trusting it):
openssl pkey -pubin -in aireos-release.pub -pubout -outform DER | openssl dgst -sha256
# ^ must print exactly the fingerprint published in the release notes. Only then:
openssl pkeyutl -verify -pubin -inkey aireos-release.pub \
  -rawin -in aireos-offline-<version>.tar.gz -sigfile aireos-offline-<version>.tar.gz.sig

# postgres companion — signed too, and for the same reason it matters most: this
# is the artifact that runs install-postgres.sh AS ROOT:
openssl pkeyutl -verify -pubin -inkey aireos-release.pub \
  -rawin -in aireos-postgres-<ostag>.tar.gz -sigfile aireos-postgres-<ostag>.tar.gz.sig
```

Both must print `Signature Verified Successfully` before you proceed. A missing
`.sig`, or any other output, means **stop** — do not extract or run anything from
that artifact. This deliberately uses no bespoke verifier binary: only the public key
needs distributing, which is what closes SEC-44's circularity (a trust root must not
ship solely inside the artifact it validates).

1. **Postgres** — install from the companion (see
   `docs/devops/offline-postgres-install.md`):
   ```sh
   tar xzf aireos-postgres-<ostag>.tar.gz && cd aireos-postgres-<ostag>
   COCKPIT_PW='strong-password' ./install-postgres.sh
   ```
   This is the one documented prerequisite step (ADR-0051 refines OI-3: a guided
   host-postgres install, then everything is automated).

   ⚠ **Known limitation (v0.16.0).** The companion's packages are pinned to the OS
   patch level current when it was built. **On a host that has applied OS security
   updates since then, this step fails** with an unmet dependency (for example
   `perl : Depends: perl-base (= …) but … is to be installed`). apt will not downgrade
   a core package, so there is no safe in-place fix. **Instead, bring your own
   PostgreSQL 16 + `pgcrypto`** from your organisation's package mirror — see
   `docs/devops/offline-postgres-install.md` §"Bring your own PostgreSQL 16" — and
   continue at step 2 with its connection string.

   ⓘ On a genuinely air-gapped host, `install-postgres.sh` also prints
   `Failed to fetch http://security.ubuntu.com/…` warnings: it adds the companion as an
   *extra* package source while the host's normal online sources stay configured. The
   warnings are harmless; the companion repo is what installs.

2. **Application** — unpack the core bundle and run the installer:
   ```sh
   tar xzf aireos-offline-<v>.tar.gz && cd aireos-offline-<v>
   AIREOS_SITE_ADDRESS=cockpit.example.com AIREOS_DEPLOYMENT_PROFILE=lan-only ./install.sh
   ```
   `install.sh` (interactive prompts by default; env-var overrides for unattended):
   - **resolves + asserts the deployment profile first (v0.12.0, ADR-0068 §D1) — a
     refusal here creates no user, no directories, and deploys nothing;**
   - creates the `aireos` user + `/opt/aireos`, `/etc/aireos`, `/var/lib/aireos`;
   - deploys binaries/SPA/config; mints the cluster PASETO key + the separate
     resource-secret key (offline, via the bundled `authctl`/`api`); provisions the
     bootstrap operator (argon2id);
   - prompts for the postgres connection string from step 1, writes the 0600
     `/etc/aireos/aireos.env` (including the deployment profile), validates the DB +
     runs all migrations;
   - enables + starts the seven systemd units: api, audit, broker, connector, policy,
     mcp-gateway and Caddy.

   **Deployment profile (required, v0.12.0).** Set `AIREOS_DEPLOYMENT_PROFILE` to one
   of `lan-only`, `vpn-first`, or `dmz-first` (see
   `docs/deep-dive/03-network-topologies.md`) — env beats a previously persisted value
   beats an interactive prompt (which shows the persisted value as its default). There
   is **no silent default**: an unset value with nothing persisted, or an unrecognised
   value, fails the install loudly. For `vpn-first`/`dmz-first`, the installer
   additionally **refuses** to proceed if `AIREOS_SITE_ADDRESS` resolves to a
   loopback-only address — those two profiles exist specifically to be reached from
   outside this host, and Caddy is the product's only externally-bound process,
   so a loopback edge would serve nothing reachable. The remedy is either of:
   set `AIREOS_SITE_ADDRESS` to a routable hostname/IP, or install with
   `AIREOS_DEPLOYMENT_PROFILE=lan-only` instead. **Re-running is idempotent**: an
   unchanged profile is a no-op; supplying a different one updates `aireos.env` and
   re-runs the same assertion. Note: `install.sh` does not (yet) preserve
   `AIREOS_SITE_ADDRESS` across re-runs (it re-defaults to `localhost` unless
   re-supplied) — a `vpn-first`/`dmz-first` host re-run without `AIREOS_SITE_ADDRESS`
   will now correctly **fail loudly** rather than silently reverting to a loopback
   edge; carrying the site address forward is a known follow-up, tracked separately.

   **Pairing / QR device pairing (optional, v0.12.0, ADR-0068 §D10).** Set
   `AIREOS_PAIRING_ENABLED=1` to turn on QR device pairing — a phone can then be
   paired to a scope-narrowed, revocable operator session (`approvals@1`: read +
   approve/deny pending approvals, nothing else). **Off by default**: a fresh
   install with the variable unset writes `AIREOS_PAIRING_ENABLED=0` into
   `aireos.env`, and some locked-down SOCs forbid phones on the floor entirely —
   pairing enabled by default would be a posture regression shipped as a feature.
   With pairing off, every pairing route answers `404` (not `403`) — a disabled
   capability must not advertise its own existence.
   Resolution is **env beats a previously persisted value beats `0`** — the same
   precedence the deployment profile uses, minus the interactive prompt (a
   phone-pairing question on every air-gapped install run would be worse than a
   safe default, so this one is never prompted). Concretely:
   - `AIREOS_PAIRING_ENABLED=1 ./install.sh` turns pairing on and **the choice
     persists** — a later plain `./install.sh` re-run (with the variable unset)
     reads the persisted `1` back out of `aireos.env` and keeps pairing on,
     rather than silently reverting to disabled.
   - Explicitly passing `AIREOS_PAIRING_ENABLED=0` on a re-run turns it back off,
     the same way an explicit env value always wins for the deployment profile.
   - A brand-new install with the variable unset and nothing yet persisted
     writes `0` — the shipped default.

3. **TLS** — Caddy serves `https://<AIREOS_SITE_ADDRESS>` with a self-signed local
   CA (`tls internal`). Import the CA root (under `/var/lib/aireos/caddy`) into
   operator browsers to remove warnings, or replace with an operator-supplied cert
   (ADR-0051 OQ-7).

## Verify

Two layers, deliberately (ADR-0070 D1): the **signature** (above, "Install" §1
preamble) verifies the whole archive **before** extraction — that is the trust
decision. The **manifest** below then gives per-file granularity **after**
extraction, and its build-time self-check already worked before this ticket:

```sh
# on the unpacked core bundle:
sha256sum -c SHA256_MANIFEST.txt
# on the unpacked postgres companion (parity with the core bundle, ADR-0070 D1):
sha256sum -c SHA256_MANIFEST.txt
# all seven units should be active:
systemctl status aireos-api aireos-audit aireos-broker aireos-connector \
  aireos-policy aireos-mcp-gateway aireos-caddy
# then walk the quickstart (docs/devops/quickstart-first-governed-action.md)
```

## Unattended install (CI / WD-1)

```sh
AIREOS_DB_URL='postgres://cockpit:pw@localhost:5432/cockpit?sslmode=disable' \
AIREOS_BOOTSTRAP_OPERATOR_USER=admin AIREOS_BOOTSTRAP_OPERATOR_PASSWORD=… \
AIREOS_SITE_ADDRESS=cockpit.example.com AIREOS_DEPLOYMENT_PROFILE=vpn-first ./install.sh
```
