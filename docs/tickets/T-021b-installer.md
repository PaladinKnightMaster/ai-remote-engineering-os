---
name: T-021b Ticket
about: Offline install (2/4) — install.sh (deploy + provision + migrate)
title: "T-021b: install.sh — offline installer (deploy, keys, operator, migrations) (v0.1.0)"
labels: ["ticket", "devops"]
---

## Ticket ID and link

T-021b — second of the §OI gate's slices (re-baselined to four: T-021a builder /
T-021b installer / T-021c postgres companions + guide + ADR-0052 / T-021d WD-1).
No B-NNN counterpart. The installer that turns an unpacked bundle into a running
cockpit on a Linux host. Binding spec: **ADR-0051** (OQ-4 provisioning).

## Goal

`infra/offline/install.sh` — run as root on the target host from the unpacked
bundle, after postgres 16 + pgcrypto exist (per T-021c's guide/companions):

> create the `aireos` system user + state dirs → deploy binaries/SPA/config to
> the FHS paths the units expect → mint the cluster PASETO key + the separate
> resource-secret key (offline, from bundled CLIs) → provision the single
> bootstrap operator (prompt default, env override; OQ-4) → write the 0600 env
> file → validate the DB connection + run all three services' migrations →
> enable + start the systemd units. Idempotent (OI-2).

## Priority

P0 — release-gating (the installer is how the bundle becomes a running stack).

## Files in scope

- Create: `infra/offline/install.sh`
- Modify: `infra/offline/build-bundle.sh` (also build + bundle `authctl`)
- Create: `docs/tickets/T-021b-installer.md` (this file)
- Modify: `docs/tickets/README.md`

4 files. The builder auto-includes `install.sh` (already wired in T-021a).

## Grounded design note — the `authctl` finding

`install.sh` must, fully offline, (a) mint the cluster PASETO key and (b) turn
the prompted operator password into an **argon2id** hash. The bundle shipped only
api/audit/broker — none hash passwords. Fix: T-021b extends the builder to also
bundle **`authctl`** (the static auth CLI: `authctl genkey` for the PASETO key,
`authctl hash <pw>` for the operator hash). The separate resource-secret key
comes from `api resources genkey` (kept distinct from the PASETO key — ADR-0048
§7 authority separation, asserted by install.sh).

## Behaviour highlights

- **Provisioning (OQ-4):** no-echo prompts by default; `AIREOS_BOOTSTRAP_OPERATOR_USER`
  + `AIREOS_BOOTSTRAP_OPERATOR_PASSWORD` (or a pre-computed `_PASSWORD_HASH`),
  `AIREOS_DB_URL`, `AIREOS_SITE_ADDRESS` env vars drive an unattended install
  (the path the WD-1 demo + CI use).
- **Idempotent (OI-2):** an existing `/etc/aireos/aireos.env` → keys are
  preserved (only the two key lines are re-sourced, never the hash/URL);
  migrations re-apply as no-ops; the operator is env-defined (no duplicate rows).
- **Env file** is 0600, owned by `aireos`; the operator password is only ever
  stored as its argon2id hash.
- **Fail-loud:** non-root, wrong directory, key-separation collision, or an
  unreachable DB / missing pgcrypto all abort with a pointed message.
- **`AIREOS_SKIP_SYSTEMCTL=1`** (or no systemd present) deploys + provisions but
  skips unit enablement — used for container verification; the live enable+start
  is exercised by T-021d (WD-1) on a real host.

## Acceptance criteria

- [x] Deploys all binaries (api/audit/broker/caddy/**authctl**) to `/opt/aireos/bin`,
      the SPA to `/opt/aireos/web`, the Caddyfile to `/etc/aireos`, and the four
      units to `/etc/systemd/system`.
- [x] Mints the PASETO + resource keys offline from bundled CLIs and asserts they
      differ; writes a 0600 `aireos`-owned env file.
- [x] Validates the DB and runs api + audit + session-broker migrations against
      one postgres (verified: `resources`, `sessions`, `outbox`, `audit_events`
      + per-service migration tables created).
- [x] Idempotent: a second run preserves the cluster keys and errors on nothing.
- [ ] systemd enable+start brings the four services up — **verified live in
      T-021d** (needs a real systemd host; out of scope here).

### Violating vs. passing fixtures

- **Key regeneration on re-run — violating:** unconditionally minting keys would
  rotate them on every install, breaking existing tokens/secrets. **Passing:**
  reuse keys from an existing env file (idempotent).
- **Operator password in the chain — violating:** storing the plaintext.
  **Passing:** only the argon2id hash is written (`authctl hash`).

## Out of scope

- Staged offline postgres `.deb`/`.rpm` **companion** packages + the offline
  postgres install guide + the `offline-install-v3.md` rewrite (DOC-5) +
  **ADR-0052** (ratifying the companion-artifact shape) → **T-021c**.
- WD-1 wedge demo + the live systemd enable+start on a real host → **T-021d**.

## Diff budget

≤12 files / ≤500 LoC. This ticket: 4 files, ~210 LoC, within budget.

## ADRs required

- **ADR-0051** (Accepted) — offline-install architecture (OQ-4 provisioning).
- **ADR-0052** (in T-021c) — postgres companion-artifact refinement; does not
  block this ticket.

## Verification

```sh
# 1) build the bundle (now includes authctl + install.sh)
docker run --rm -v "$PWD":/work -w /work -v aireos-gomod:/go/pkg/mod golang:1.23 bash -c '
  apt-get update -qq && apt-get install -y -qq wget && bash infra/offline/build-bundle.sh'

# 2) install against a real postgres (skip systemctl; non-interactive env path),
#    run twice for idempotency, then confirm the migrated tables.
docker network create aireos-oitest
docker run -d --name oitest-pg --network aireos-oitest \
  -e POSTGRES_DB=cockpit -e POSTGRES_USER=cockpit -e POSTGRES_PASSWORD=secret postgres:16
docker run --rm --network aireos-oitest \
  -e AIREOS_SKIP_SYSTEMCTL=1 \
  -e AIREOS_DB_URL="postgres://cockpit:secret@oitest-pg:5432/cockpit?sslmode=disable" \
  -e AIREOS_BOOTSTRAP_OPERATOR_USER=admin -e AIREOS_BOOTSTRAP_OPERATOR_PASSWORD=pw \
  -v "$PWD":/work -w /work ubuntu:24.04 bash -c '
    cp -a dist/offline/aireos-offline-0.1.0 /tmp/b && cd /tmp/b && bash install.sh && bash install.sh'
docker exec oitest-pg psql -U cockpit -d cockpit -c "\dt"
docker rm -f oitest-pg && docker network rm aireos-oitest
```

Expected (confirmed 2026-05-31, Docker / golang:1.23 + ubuntu:24.04 + postgres:16):
deploy layout correct; env 0600 `aireos:aireos`; keys present + separated; four
units deployed; all three migrations applied; idempotent re-run preserves keys.
