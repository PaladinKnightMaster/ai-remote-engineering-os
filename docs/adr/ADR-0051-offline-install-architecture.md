# ADR-0051: Offline-install architecture — Linux static-binary host, Caddy proxy, host-provided postgres with staged offline packages, dual operator provisioning, self-signed TLS

## Status

Accepted — 2026-05-31. Settles the v0.1.0 release-criteria §11 open questions
**OQ-3, OQ-4, OQ-6, OQ-7** and is the record of "the `install.sh` ticket consult"
the criteria call for (`docs/evaluation/v0.1.0-release-criteria.md` §6 / §11a).
Binding spec for the §OI implementation tickets (OI-a bundle builder / OI-b
install.sh + guide / OI-c installer-stack WD-1 verification). Supersedes the
stale conceptual `docs/devops/offline-install-v3.md` (which DOC-5 flags for
rewrite).

## Context

The v0.1.0 release criteria §6 define the offline-install gate (OI-1…OI-5: a
`task offline-bundle` tarball, an `install.sh` for a no-egress host, the WD-1
wedge demo passing against the installed stack, a ≤500 MB uncompressed / **≤150 MB
gzipped hard cap**, and a verifiable `SHA256_MANIFEST.txt`). They deliberately
left four load-bearing decisions to the installer consult:

- **OQ-3** — Caddy or Traefik as the bundled reverse proxy.
- **OQ-4** — how `install.sh` provisions the single bootstrap operator.
- **OQ-6** — ship postgres in the bundle, or require it on the host (postgres is
  ~the entire OI-4 gzipped budget).
- **OQ-7** — TLS cert origin on a host with no public-internet egress.

A prerequisite the criteria did not state explicitly also had to be settled: the
**runtime model**. `offline-install-v3.md` (a v3.0.1-baseline doc) describes
"Docker images + internal registry bootstrap", but that predates ADR-0041's
single-static-binary decision and OI-1's "per-service single static Go binaries".

The SSH slice (T-016…T-020b) also added two facts the original criteria predate:
the bundle's SPA build now carries the xterm pnpm deps, and the
**session-broker is stateful** (its own DB + migrations), so `install.sh` must
migrate three services, not two.

## Decision

1. **Runtime model: static binaries on a Linux host, supervised by systemd —
   not docker-compose-with-images.** The Go services (api, audit-service,
   session-broker) ship as per-service single static binaries (ADR-0041);
   `apps/web` ships as pre-built static assets (ADR-0047); the reverse proxy
   ships as one binary (below). No Docker runtime is required on the host.

2. **Reverse proxy: Caddy.** A single static Go binary that serves the SPA
   static assets and reverse-proxies the two upstreams on one origin (ADR-0047):
   `/v1/*` → `services/api` and the WebSocket `/v1/sessions/{id}/channel` →
   `services/session-broker` (ADR-0050). Chosen over nginx/Apache because it is a
   single drop-in binary with no OS package + dependency chain to stage offline
   (consistent with decision 3's reasoning), proxies WebSockets with no extra
   config, and its `tls internal` directive delivers decision 5's self-signed
   TLS with zero scripting.

3. **Postgres: required on the host (not bundled), with staged offline install
   files + a detailed guide.** Bundling postgres (the Docker image is ~the entire
   ≤150 MB gzipped cap, and would also force Docker on the host) is rejected.
   Instead the host runs postgres 16 + `pgcrypto`, and the offline bundle ships
   **staged install packages** — `.deb` (Ubuntu/Debian) and `.rpm` (RHEL-family:
   CentOS Stream 9 / Rocky / Alma / RHEL), amd64 — plus a step-by-step offline
   install guide, so an air-gapped host can install postgres without internet.
   `install.sh` **validates** the DB connection and runs all migrations
   (api + audit-service + the stateful session-broker); it does **not** create
   the server. Staged packages live in the bundle / `artifacts` output (never in
   git history, per the repo convention) and are covered by `SHA256_MANIFEST.txt`.

4. **Operator provisioning: both — interactive prompt by default, env-var
   override.** `install.sh` provisions exactly one argon2id bootstrap operator
   (OI-2). By default it prompts (no-echo) for username + password and hashes via
   the bundled `authctl`/`api`; if `AIREOS_BOOTSTRAP_OPERATOR_USER` (+ a password
   or a pre-computed `_PASSWORD_HASH`) is set, it runs unattended (required for
   the automated WD-1 demo and CI). Idempotent: an existing operator is a no-op,
   no duplicate rows.

5. **TLS: self-signed local-CA by default, operator-supplied cert as an escape
   hatch, ACME rejected.** Caddy `tls internal` mints a local CA + leaf cert at
   first boot (works offline by construction); the guide documents exporting the
   CA root for the operator's trust store. An operator-supplied cert is accepted
   as a documented first-boot alternative. ACME / Let's-Encrypt is rejected — it
   needs public-internet egress and contradicts offline-first (ADR-0001).

6. **Platform scope: Linux server; Windows is an operator client only.** The
   server stack targets Linux (Ubuntu/Debian + RHEL-family, amd64 baseline).
   Windows operators use the cockpit through the browser SPA — no server build.
   Windows-as-a-server-host (a PowerShell installer, Windows service management,
   the EDB postgres installer) is **deferred to a future ADR + ticket** if a
   deployment ever requires it; it is explicitly out of v0.1.0 scope.

7. **OI-3 refinement (recorded honestly).** OI-3's "no manual steps after
   `install.sh` exits" is refined to "no manual steps after the **documented
   postgres prerequisite** is satisfied (using the staged offline files)." The
   one-time host-postgres install is an explicit, guided pre-step; everything
   after it is automated. This is a deliberate consequence of decision 3.

## Alternatives considered

- **Bundle postgres (Docker image or binaries).** Rejected: the image busts the
  OI-4 gzipped cap and forces Docker on the host; bundling distro packages/initdb
  makes `install.sh` own postgres lifecycle + OS/arch matching (real ops
  complexity) and puts postgres CVE patching in our release cadence. Host-managed
  postgres is the cleaner trust/patching boundary for v0.1.0.
- **nginx or Apache httpd as the proxy.** Rejected for v0.1.0: both are C
  packages needing OS/arch-matched packages + dependencies staged offline
  (reintroducing exactly the cost decision 3 avoids), require hand-rolled
  `openssl` self-signed cert generation, and need explicit WebSocket-upgrade
  config (a frequent misconfiguration). nginx is a defensible choice only for an
  org already standardized on it; Apache offers no advantage here.
- **Env-only or prompt-only operator provisioning.** Rejected: env-only risks
  leaking the password into history/process listings and gives no secure human
  path; prompt-only can't run unattended (breaks the automated WD-1 demo).
- **docker-compose-with-images runtime.** Rejected: contradicts ADR-0041's
  single-static-binary deploy and OI-1; would force a Docker runtime + image
  loading on the host and bloat the bundle.

## Consequences

### Positive
- Bundle stays within the OI-4 cap; the proxy and TLS are fully offline-capable
  with zero host dependencies (one Caddy binary).
- Clean security boundary: the host/operator owns postgres patching + backups.
- Broad Linux coverage (Ubuntu/Debian + RHEL-family) in two package sets.
- The automated WD-1 demo (and our e2e) can provision the operator unattended.

### Negative
- Host postgres is a documented manual pre-step → OI-3 is relaxed (decision 7).
- Two package families to stage + two guide tracks; arm64 is deferred (amd64
  baseline) until a deployment needs it.
- We rely on the host's postgres being correctly installed from the staged
  files; the guide + an `install.sh` connection-validation step mitigate this.
- Self-signed TLS shows browser warnings until the operator imports the CA root
  (documented); real PKI is the operator-cert escape hatch.

## Verification

These block the §OI implementation tickets (not this ADR's acceptance):

- **OI-1/4/5:** `task offline-bundle` produces the tarball (Linux static binaries
  + SPA assets + Caddy + `.deb`/`.rpm` postgres packages + `install.sh` +
  `SHA256_MANIFEST.txt`), ≤500 MB uncompressed / ≤150 MB gzipped, verifying via
  `sha256sum -c`.
- **OI-2:** `install.sh` on a no-egress host generates the cluster PASETO key,
  provisions one operator (prompt or env), validates the DB connection, and runs
  api + audit + session-broker migrations; idempotent on re-run.
- **OI-3:** the WD-1 wedge demo passes against the installed stack with no manual
  steps after the documented postgres prerequisite (reusing the T-020b harness
  pattern against the installed stack).
- **DOC-5:** `docs/devops/offline-install-v3.md` is rewritten to the real
  `install.sh` + offline-postgres shape that ships.

## Related ADRs

- ADR-0001 — self-hosted / offline-first (no public-internet dependency; ACME
  rejected here).
- ADR-0041 — single static binary per service (the runtime model in decision 1).
- ADR-0042 — Taskfile orchestration (`task offline-bundle`).
- ADR-0044 — per-service embedded migrations (`install.sh` runs three services').
- ADR-0046 — PASETO + single argon2id bootstrap operator (decision 4).
- ADR-0047 — Vite SPA built to static assets + same-origin cookie (what Caddy
  serves + proxies).
- ADR-0048 / ADR-0050 — cockpit session control + WS data-plane contract (the
  `/v1/sessions/{id}/channel` route Caddy proxies).
