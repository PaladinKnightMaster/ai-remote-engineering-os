# AI Remote Engineering OS — v0.1.0 release notes

**Status: in preparation.** v0.1.0 is the P0 MVP-survival vertical slice (per
`docs/evaluation/v0.1.0-release-criteria.md`). This file is the human-readable
roll-up; the machine-readable coverage is `release_manifest_v0_1_0.json`, and an
archival copy of the criteria sits beside both.

## What landed (the wedge)

An operator can, from an offline-installed stack, drive a real remote SSH session
through the cockpit and a reviewer can replay a tamper-evident audit trail:

- **Remote Cockpit (SSH terminal).** `services/session-broker` brokers a
  WebSocket→SSH PTY (`golang.org/x/crypto/ssh`, pinned host key, idle timeout,
  byte-safe asciinema cast); `services/api` owns the `/v1/sessions` control plane.
  Proven live end-to-end (T-020b). ADR-0048/0049/0050.
- **Audit chain (tamper-evident).** `services/audit-service` is the append-only
  chained-hash authority; producers fan in via the Postgres transactional outbox
  (`packages/go/outbox`). Cockpit sessions emit `session.open` /
  `terminal.input.line` (hash-in-chain + cast-anchored) / `session.close` (cast
  SHA); `services/api` emits `auth.login.success` / `auth.refresh` / `auth.logout`
  (T-022, SEC-9). `audit-service verify` checks chain integrity. ADR-0045.
- **Capability-edge permission model.** PASETO v4 capability tokens
  (`packages/go/auth`), `RequireCapability` middleware on every operator + S2S
  edge, HttpOnly + SameSite=Strict session cookie + double-submit CSRF. ADR-0046/0047.
- **Cockpit SPA.** Vite + React, `<TerminalView>` (xterm.js) over the broker WS,
  served same-origin behind Caddy. ADR-0047.
- **Offline install.** `task offline-bundle` → a ~30 MB-gz bundle (static Linux
  binaries + SPA + Caddy + config + SHA256 manifest); per-OS postgres companions
  (Ubuntu 24.04 + Rocky/RHEL 9, full air-gapped closure); `install.sh` brings the
  stack up under systemd. The WD-1 wedge demo passes against the installed stack
  through Caddy HTTPS (T-021a/b). ADR-0051/0052.
- **Release-prep automation.** `task release-check` (VER-3 aggregator),
  `task doc-check` (DOC-1), `task licenses` (SC-6) + `docs/security/license-policy.md`.

## Release gates — all passing

All v0.1.0 must-pass gates are met; the release is **ready to tag** (the tag
itself awaits explicit sign-off).

- **T-024 — operator audit-timeline view (closed).** The operator now views the
  chained evidence log in the cockpit. `services/api` exposes an operator-gated
  read-proxy `GET /v1/audit/events` → `audit-service` using its own minted
  service token (T-024-a; one audience boundary, no new Caddy route — `/v1/*`
  already fronts api), and `apps/web` serves a capability-gated `/audit` timeline
  page (T-024-b). The WD-1 walkthrough asserts the operator reads their own
  chained `auth.login.success` back through Caddy → api → `audit-service` on real
  wires.

## Known limits / deferred (with triggers)

- **Reconnect:** a WebSocket drop ends the session; fresh-shell reconnect → v0.2.0
  (ADR-0049).
- **Target credentials** live pgcrypto-encrypted in `services/api`'s Postgres
  (interim debt); `services/connector-service` is the architecturally-correct home
  (ADR-0048 §7 promotion trigger).
- **`auth.service-token.mint` audit** (SEC-9's 4th event) deferred — circular with
  the audit-delivery relay; needs token caching (T-023). Not WD-1-required.
- **Platform:** Linux server (Ubuntu/Debian + RHEL-family, amd64). Windows =
  browser client only; Windows-server + arm64 deferred (ADR-0051).
- **No GUI/RDP/VNC** (guacd deferred), **no cross-operator session sharing**,
  **single bootstrap operator**, **token replay accepted during TTL** (revocation =
  key rotation + short TTL, ADR-0046).
- **Audit timeline (read-only):** chronological table + cursor "Load more" +
  manual "Refresh". Server-side filtering/search and live auto-refresh → v0.2.0.

## Verify the release

```sh
task release-check          # all must-pass gates (VER-3)
task offline-bundle && bash infra/offline/postgres/build-postgres-companions.sh ubuntu2404
task wd1                    # WD-1 wedge demo on the installed stack
sha256sum -c SHA256_MANIFEST.txt   # on the unpacked bundle (OI-5)
```
