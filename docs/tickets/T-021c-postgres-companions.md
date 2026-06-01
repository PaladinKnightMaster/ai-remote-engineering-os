---
name: T-021c Ticket
about: Offline install (3/4) — postgres companion artifacts + guide + DOC-5 + ADR-0052
title: "T-021c: offline postgres companions (Ubuntu 24.04 + Rocky 9) + guide (v0.1.0)"
labels: ["ticket", "devops"]
---

## Ticket ID and link

T-021c — third §OI slice. No B-NNN counterpart. Provides the air-gapped postgres
install the host-postgres model (ADR-0051 OQ-6) depends on, as **per-OS companion
artifacts** (ADR-0052). Binding spec: **ADR-0051** + **ADR-0052** (authored here).

## Goal

Let an air-gapped host install postgres 16 + pgcrypto + the cockpit database with
no internet and no host package mirror, for Ubuntu 24.04 and Rocky/RHEL-family 9.

## Priority

P0 — release-gating (install.sh / WD-1 need a reachable cockpit postgres).

## Files in scope

- Create: `infra/offline/postgres/fetch-postgres.sh` (build a per-OS local repo)
- Create: `infra/offline/postgres/install-postgres.sh` (target-side offline install)
- Create: `infra/offline/postgres/build-postgres-companions.sh` (build-host orchestrator)
- Modify: `infra/offline/build-bundle.sh` (drop the old postgres-in-bundle hook; companions are separate per ADR-0052)
- Create: `docs/adr/ADR-0052-postgres-companion-artifacts.md`
- Create: `docs/devops/offline-postgres-install.md` (operator guide)
- Rewrite: `docs/devops/offline-install-v3.md` (real shipped procedure — **DOC-5**)
- Create: `docs/tickets/T-021c-postgres-companions.md` (this file)
- Modify: `docs/tickets/README.md` (+ register the SEC-9 / api-outbox gap as T-022)
- Modify: `CANONICAL_SOURCE_OF_TRUTH.md` (ADR-0052 row)

10 files. Companion artifacts themselves are gitignored (`dist/`).

## Design (grounded by the §OI-c probe)

- **Pure air-gap, full closure (CEO call).** The companion carries postgres + every
  transitive dependency, computed on a **pristine** base — so it installs on a host
  with nothing but the base OS.
- **Local package repo, not loose packages.** The probe proved `apt-get install
  --download-only` + `dpkg -i` is **not** a self-sufficient closure (unmet deps).
  Correct pattern: download the closure on a pristine base **first**, build a repo
  index (`dpkg-scanpackages` / `createrepo_c`), and `apt`/`dnf install` from a
  `file://` repo so dependencies + ordering resolve.
- **Per-OS, run in the matching base image.** `build-postgres-companions.sh` runs
  `fetch-postgres.sh` inside `ubuntu:24.04` / `rockylinux:9` (closure matches target);
  postgres 16 comes from PGDG on RHEL.
- **RHEL specifics fixed:** `initdb` directly (not `postgresql-16-setup`, which needs
  systemd to find PGDATA) + `--auth-host=scram-sha-256` (RHEL's default loopback auth
  is `ident`, which would reject the cockpit password connection).

## Acceptance criteria (verified offline, `--network none`)

- [x] **Ubuntu 24.04:** `fetch-postgres.sh` builds an 89-deb / 85 MB local apt repo;
      `install-postgres.sh` installs postgres + pgcrypto + the cockpit DB offline;
      `pgp_sym_encrypt` works over a TCP connection as the `cockpit` role.
- [x] **Rocky 9:** `fetch-postgres.sh` builds an 81 MB dnf repo (PGDG); offline
      install + cluster init + cockpit DB + pgcrypto; TCP-as-`cockpit` `pgp_sym_encrypt`.
- [x] Companions are separate `aireos-postgres-<ostag>.tar.gz` artifacts; the core
      bundle is unchanged + stays under OI-4 (ADR-0052).
- [x] `install-postgres.sh` is idempotent (reuses an existing role/db).
- [ ] Full installed-stack bring-up under systemd → **T-021d (WD-1)**.

### Violating vs. passing fixtures

- **`--download-only` + `dpkg -i` — violating:** unmet deps on a clean host.
  **Passing:** full closure on a pristine base + `apt-get install` from a local repo.
- **RHEL `ident` loopback auth — violating:** cockpit TCP password connection
  refused. **Passing:** `initdb --auth-host=scram-sha-256`.

## Out of scope

- The api outbox + SEC-9 auth/control-plane audit events → **T-022** (release-gating,
  surfaced by the pre-T-021c review; must precede T-021d).
- WD-1 wedge demo against the installed stack + live systemd bring-up → **T-021d**.
- arm64 / additional OS releases (new companions, no design change).

## Diff budget

≤12 files / ≤500 LoC. This ticket: 10 files (3 scripts + build-bundle edit + 4 docs +
README + CANONICAL), ~400 LoC, within budget.

## ADRs required

- **ADR-0052** (Accepted, authored here) — postgres companion artifacts (refines
  ADR-0051 §3).

## Verification

```sh
# per OS: build the companion in the base image, then install it OFFLINE.
docker volume create pgc
docker run --rm -v pgc:/out -v "$PWD/infra/offline/postgres":/src:ro -w /src ubuntu:24.04 bash /src/fetch-postgres.sh
docker run --rm --network none -v pgc:/c ubuntu:24.04 bash -c 'COCKPIT_PW=secret bash /c/install-postgres.sh &&
  PGPASSWORD=secret psql "host=localhost user=cockpit dbname=cockpit" -tAc "SELECT pgp_sym_encrypt('"'"'x'"'"','"'"'k'"'"') IS NOT NULL"'
# (repeat with rockylinux:9 + /usr/pgsql-16/bin/psql)
```

Expected (confirmed 2026-05-31): Ubuntu 85 MB repo + Rocky 81 MB repo; offline
install succeeds on both; `pgp_sym_encrypt` over TCP as `cockpit` returns `t`.
