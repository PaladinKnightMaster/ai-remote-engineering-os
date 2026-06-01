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
   - creates the `aireos` user + `/opt/aireos`, `/etc/aireos`, `/var/lib/aireos`;
   - deploys binaries/SPA/config; mints the cluster PASETO key + the separate
     resource-secret key (offline, via the bundled `authctl`/`api`); provisions the
     bootstrap operator (argon2id);
   - prompts for the postgres connection string from step 1, writes the 0600
     `/etc/aireos/aireos.env`, validates the DB + runs all migrations;
   - enables + starts the systemd units (api, audit, broker, Caddy).

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
AIREOS_SITE_ADDRESS=cockpit.example.com ./install.sh
```
