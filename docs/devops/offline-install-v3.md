# Offline install (v0.1.0)

The real, shipped offline-install procedure for the AI ReOS v0.1.0 cut. Supersedes
the v3.0.1-baseline sketch (Docker images / internal registry), which predated
ADR-0041's single-static-binary deploy. Binding spec: **ADR-0051** (architecture) +
**ADR-0052** (postgres companions). Closes release-criteria **DOC-5**.

Offline-first (ADR-0001) governs the **target** host: the bundle + companions are
built on a connected machine and installed on a host with no public-internet egress.

## What ships

| Artifact | Contents | Built by |
|---|---|---|
| `aireos-offline-<v>.tar.gz` (~28 MB gz) | static `linux/amd64` binaries (api, audit-service, session-broker, **authctl**) + **Caddy** + the SPA + `Caddyfile` + systemd units + `install.sh` + `SHA256_MANIFEST.txt` | `task offline-bundle` (T-021a/b) |
| `aireos-postgres-<ostag>.tar.gz` (~80 MB) | a self-contained postgres-16 + pgcrypto local repo (full closure) + `install-postgres.sh` | `build-postgres-companions.sh` (T-021c) |

Postgres is a separate per-OS companion (ADR-0052), not in the core bundle.
v0.1.0 OS scope: Ubuntu 24.04 + Rocky/RHEL-family 9, amd64. Windows = browser
client only (no server build).

## Build (connected host)

```sh
task offline-bundle                                           # core bundle
bash infra/offline/postgres/build-postgres-companions.sh ubuntu2404 rocky9
# artifacts land in dist/offline/ ; verify with sha256sum -c on the unpacked core bundle.
```

Transfer the core bundle + the postgres companion for your target OS to the
air-gapped host.

## Install (target host, offline, as root)

1. **Postgres** — install from the companion (see
   `docs/devops/offline-postgres-install.md`):
   ```sh
   tar xzf aireos-postgres-<ostag>.tar.gz && cd aireos-postgres-<ostag>
   COCKPIT_PW='strong-password' ./install-postgres.sh
   ```
   This is the one documented prerequisite step (ADR-0051 refines OI-3: a guided
   host-postgres install, then everything is automated).

2. **Application** — unpack the core bundle and run the installer:
   ```sh
   tar xzf aireos-offline-<v>.tar.gz && cd aireos-offline-<v>
   AIREOS_SITE_ADDRESS=cockpit.example.com ./install.sh
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
   - enables + starts the systemd units (api, audit, broker, Caddy).

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

```sh
# on the unpacked core bundle:
sha256sum -c SHA256_MANIFEST.txt
# then the wedge demo (WD-1) against the running stack — see T-021d.
systemctl status aireos-api aireos-audit aireos-broker aireos-caddy
```

## Unattended install (CI / WD-1)

```sh
AIREOS_DB_URL='postgres://cockpit:pw@localhost:5432/cockpit?sslmode=disable' \
AIREOS_BOOTSTRAP_OPERATOR_USER=admin AIREOS_BOOTSTRAP_OPERATOR_PASSWORD=… \
AIREOS_SITE_ADDRESS=cockpit.example.com AIREOS_DEPLOYMENT_PROFILE=vpn-first ./install.sh
```
