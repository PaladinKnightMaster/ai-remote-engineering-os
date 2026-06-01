---
name: T-021a Ticket
about: Offline install (1/3) — task offline-bundle builder (app tier)
title: "T-021a: offline-bundle builder — static binaries + SPA + Caddy + manifest (v0.1.0)"
labels: ["ticket", "devops"]
---

## Ticket ID and link

T-021a — first of the §OI gate's three slices (per ADR-0051 §OI ticket split). No
B-NNN counterpart. Builds the offline-install bundle's **application tier** and
its packaging machinery. Pairs with **T-021b** (`install.sh` + staged postgres
offline packages + offline guide / DOC-5) and **T-021c** (WD-1 against the
installed stack). Binding spec: **ADR-0051**.

## Goal

Make `task offline-bundle` produce a verifiable, size-capped offline bundle of
the application tier on a connected build host:

> static `linux/amd64` service binaries (api, audit-service, session-broker) +
> the built SPA + a checksum-pinned Caddy + the Caddyfile + systemd unit
> templates + `VERSION` + `SHA256_MANIFEST.txt`, packaged to
> `dist/offline/aireos-offline-<version>.tar.gz`, gated against the OI-4 size cap.

`install.sh` and the staged postgres packages are **out of scope** (§OI-b); the
builder is forward-compatible and includes them automatically once they exist.

## Priority

P0 — release-gating (the offline bundle is the v0.1.0 release vehicle).

## Files in scope

- Create: `infra/offline/build-bundle.sh` (the builder)
- Create: `infra/offline/Caddyfile` (proxy: SPA + `/v1`→api + WS `/channel`→broker)
- Create: `infra/offline/systemd/aireos-{api,audit,broker,caddy}.service` (4 unit templates)
- Modify: `Taskfile.yml` (`offline-bundle` target: `pnpm build` → `build-bundle.sh`)
- Create: `docs/tickets/T-021a-offline-bundle-builder.md` (this file)
- Modify: `docs/tickets/README.md`

9 files. `.gitignore` already ignores `dist/` (the build output) — no change. The
bundle artifacts themselves are never committed (heavy binaries live outside git).

## How it stays real (and within ADR-0051)

- **Static binaries (verified):** `CGO_ENABLED=0 GOOS=linux GOARCH=amd64` — `ldd`
  reports "not a dynamic executable" for all three (api 10.7 MB, audit 10.6 MB,
  broker 12.0 MB). Single-static-binary deploy per ADR-0041.
- **Caddy pinned + verified:** v2.11.3, fetched at build time and checked against
  the official **SHA-512** checksum (Caddy publishes sha512, not sha256 — the
  builder uses the right algorithm; mismatch fails the build).
- **Caddyfile (ADR-0050):** `handle @channel` (regexp `^/v1/sessions/[^/]+/channel$`)
  → broker:8082 is matched **before** `handle /v1/*` → api:8080; everything else
  is the SPA with `try_files … /index.html` client-routing fallback. `tls internal`
  gives the offline self-signed local CA (ADR-0051 OQ-7a). Validates clean + is
  `caddy fmt`-clean.
- **systemd units:** the install contract — binaries at `/opt/aireos/bin`, env at
  `/etc/aireos/aireos.env`, state at `/var/lib/aireos`, user `aireos`, hardened
  (`ProtectSystem=strict`, `NoNewPrivileges`); broker/caddy get
  `ReadWritePaths=/var/lib/aireos`; caddy gets `CAP_NET_BIND_SERVICE`. `install.sh`
  (§OI-b) must honor these paths.
- **Build-on-connected-host:** offline-first (ADR-0001) governs the *target*; the
  builder may fetch Caddy at build time. The produced tarball installs with zero
  egress.

## Acceptance criteria

- [x] `task offline-bundle` (`pnpm --filter @aireos/web build` → `build-bundle.sh`)
      produces `dist/offline/aireos-offline-0.1.0.tar.gz` with `bin/{api,
      audit-service,session-broker,caddy}` + `web/` + `config/{Caddyfile,4 units}`
      + `VERSION` + `SHA256_MANIFEST.txt`. (OI-1, app tier)
- [x] **OI-4:** bundle = **80 MB uncompressed / 28 MB gzipped** — under the
      500 MB / 150 MB caps; the builder fails loud if either is exceeded.
- [x] **OI-5:** `sha256sum -c SHA256_MANIFEST.txt` verifies on the unpacked bundle.
- [x] Caddy verified against its official SHA-512 checksum; tampered/unexpected
      binary aborts the build.
- [x] `caddy validate` passes for the bundled Caddyfile (localhost + a real HTTPS
      site address); `caddy fmt` reports clean.
- [x] `install.sh` / `postgres/` absent → builder logs the §OI-b deferral and
      still produces a valid bundle (forward-compatible).

### Violating vs. passing fixtures

- **Wrong checksum algorithm — violating:** verifying Caddy with `sha256sum`
  against the published sha512 line fails every time. **Passing:** `sha512sum -c`
  (the builder's pinned check).
- **Route order — violating:** `/v1/*`→api before the `@channel` matcher would
  send the WS upgrade to api (404/no-upgrade). **Passing:** `@channel` handled
  first → broker:8082.

## Out of scope

- `install.sh` (genkey, operator prompt/env provisioning, DB-connection validate +
  run api/audit/broker migrations, enable units) → **T-021b**.
- Staged offline postgres `.deb` (Ubuntu/Debian) + `.rpm` (RHEL-family) packages +
  the offline postgres install guide + the `offline-install-v3.md` rewrite (DOC-5)
  → **T-021b**.
- WD-1 wedge demo against the installed stack (the live G1 cross-boundary proof,
  reusing the T-020b harness pattern) → **T-021c**.
- arm64 (amd64 baseline per ADR-0051); Windows server (browser-client only).

## Diff budget

≤12 files / ≤500 LoC. This ticket: 9 files, ~400 LoC, within budget.

## ADRs required

- **ADR-0051** (Accepted) — offline-install architecture. No new ADR.

## Verification

```sh
# 1) build the SPA (node 22 / pnpm)
docker run --rm -e CI=true -v "$PWD":/work -w /work -v aireos-pnpm:/pnpm-store node:22-alpine sh -c '
  corepack enable && corepack prepare pnpm@10.32.1 --activate &&
  pnpm install --frozen-lockfile --store-dir /pnpm-store &&
  pnpm --filter @aireos/web build'

# 2) build + verify the bundle (go 1.23 + wget)
docker run --rm -v "$PWD":/work -w /work -v aireos-gomod:/go/pkg/mod golang:1.23 bash -c '
  apt-get update -qq && apt-get install -y -qq wget &&
  bash infra/offline/build-bundle.sh &&
  ( cd dist/offline/aireos-offline-0.1.0 && sha256sum -c SHA256_MANIFEST.txt )'

# 3) validate the proxy config (linux caddy binary from the bundle)
docker run --rm -v "$PWD":/work -w /work alpine:3.20 sh -c '
  B=dist/offline/aireos-offline-0.1.0;
  AIREOS_SITE_ADDRESS=cockpit.example.com $B/bin/caddy validate --config infra/offline/Caddyfile --adapter caddyfile'
```

Expected (confirmed 2026-05-31, Docker / golang:1.23 + node:22-alpine + alpine:3.20):
static binaries (`ldd`: not a dynamic executable); bundle 80 MB / 28 MB gz under
the OI-4 caps; `sha256sum -c: OK`; Caddy sha512-verified; "Valid configuration".
