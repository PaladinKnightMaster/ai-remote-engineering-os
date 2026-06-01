---
name: T-021d-a Ticket
about: Offline install (4a/4) — WD-1 installed-stack bring-up under real systemd + verify CLI
title: "T-021d-a: WD-1 systemd bring-up harness + audit-service verify CLI (v0.1.0)"
labels: ["ticket", "devops"]
---

## Ticket ID and link

T-021d-a — first half of the §OI capstone (T-021d split a/b at consult sign-off).
Proves the **installed** stack comes up under **real systemd** from the offline
bundle + postgres companion, and lands the chain-integrity **verify CLI** (WD-1
step 9). The full WD-1 walkthrough + timeline assertions are **T-021d-b**.
Binding spec: ADR-0051 (install) + ADR-0045 (chain).

## Goal

A `task wd1` harness that runs the **real** offline-install path on a
systemd-in-Docker "host" — `install-postgres.sh` (offline, from the companion)
then `install.sh` with `systemctl enable --now` **not** skipped — and asserts the
stack is actually up: the four `aireos-*` units + postgresql `active`, Caddy
serving the SPA over HTTPS, and `audit-service verify` (the new chain-integrity
CLI) running clean.

## Priority

P0 — release-gating (OI-3 / WD-1; the last gate before §11a, with T-021d-b).

## Environment decision (settled in the consult)

systemd-in-Docker (Option A), enabled by cgroup v2 — **verified to work on the
Windows/WSL2 host** (`--privileged --cgroupns=host --tmpfs /run -v /sys/fs/cgroup:rw`,
`/sbin/init` as PID 1). This runs the *real* `systemctl` bring-up (units, ordering,
Restart) that plain Docker couldn't — strictly better than the earlier "skip
systemctl" fallback, and portable to Linux CI.

## Files in scope

- Create: `infra/offline/wd1/Dockerfile.host` (ubuntu:24.04 + systemd target host)
- Create: `infra/offline/wd1/run-wd1.sh` (bring-up orchestrator + asserts)
- Modify: `services/audit-service/main.go` (`verify` subcommand → `chain.Verify` over the whole chain; WD-1 step 9)
- Modify: `infra/offline/postgres/install-postgres.sh` (apt `_apt`-sandbox fix — see below)
- Modify: `infra/offline/postgres/build-postgres-companions.sh` (`MSYS_NO_PATHCONV` for Windows)
- Modify: `Taskfile.yml` (`task wd1`)
- Create: `docs/tickets/T-021d-a-wd1-systemd-bringup.md` (this) + Modify: `docs/tickets/README.md`

8 files, ~250 LoC, within budget.

## Two real bugs surfaced (and fixed)

- **apt `_apt`-sandbox permission denied** (install-postgres.sh): a `file://` repo
  unpacked under a root-only dir (e.g. `/root`) is unreadable by apt's unprivileged
  `_apt` sandbox user → "Permission denied" on the Packages index. **This would
  bite a real operator** who unpacks the companion in `/root`. Fixed with
  `apt-get -o APT::Sandbox::User=root …` (read as root wherever the companion lands).
- **Windows path mangling** (build-postgres-companions.sh): added `MSYS_NO_PATHCONV=1`
  so the `-v` mounts work in git-bash, matching the other orchestrators. The
  `docker build` context in run-wd1.sh uses a relative `.` for the same reason.

## Acceptance criteria (verified)

- [x] `task wd1` builds the systemd host, runs the **real** `install-postgres.sh`
      (offline, online apt sources removed first) + `install.sh` (systemctl enabled).
- [x] All five units `active`: `aireos-api`, `aireos-audit`, `aireos-broker`,
      `aireos-caddy`, `postgresql`.
- [x] Caddy serves the SPA over **HTTPS** (`curl -sk https://localhost/` → HTML).
- [x] `audit-service verify` runs and reports the chain intact (empty pre-login is
      intact; T-021d-b populates + re-verifies it).
- [x] audit-service build/vet/golangci-lint/tests clean; scripts `bash -n` clean.

### Violating vs. passing fixtures

- **`file://` repo under /root — violating:** apt `_apt` sandbox can't read it →
  install fails. **Passing:** `APT::Sandbox::User=root`.
- **No chain-verify CLI — violating:** WD-1 step 9 unmet (logic existed only
  in-package). **Passing:** `audit-service verify` exposes `chain.Verify`.

## Out of scope

- The WD-1 walkthrough (login → resource → SSH session → command → end → logout)
  + full chained-timeline assertions through Caddy HTTPS → **T-021d-b**.
- arm64 / Rocky companion in the WD-1 harness (Ubuntu host first) → later.

## ADRs required

- None new — install path conforms to ADR-0051; `verify` exposes the existing
  `chain.Verify` (ADR-0045).

## Verification

```sh
# build artifacts first (Docker; see T-021a/T-021c), then:
docker run --rm -e CI=true -v "$PWD":/work -w /work node:22-alpine sh -c 'corepack enable && corepack prepare pnpm@10.32.1 --activate && pnpm install --frozen-lockfile && pnpm --filter @aireos/web build'
docker run --rm -v "$PWD":/work -w /work golang:1.23 bash -c 'apt-get update -qq && apt-get install -y -qq wget && bash infra/offline/build-bundle.sh'
bash infra/offline/postgres/build-postgres-companions.sh ubuntu2404
task wd1     # on Windows: bash already sets MSYS_NO_PATHCONV in the script
```

Expected (confirmed 2026-05-31, Windows/WSL2 Docker): host boots systemd; postgres
+ app installed; 5 units `active`; `SPA OK`; `audit chain intact: 0 event(s) verified`.
