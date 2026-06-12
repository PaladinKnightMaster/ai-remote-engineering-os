# AI Remote Engineering OS — v0.2.0 release notes

**Status: release-candidate — ready for the pre-release audit + tag** (the tag
awaits the human-run WD-2 confirmation per release-criteria §12 and explicit
CEO sign-off; local annotated tag only, no remote). v0.2.0 — **"Governed Egress
Foundation"** — is the P1-spine release on the v0.1.0 P0 wedge (per
`docs/evaluation/v0.2.0-release-criteria.md`; archival copy beside this file;
machine-readable coverage in `release_manifest_v0_2_0.json`).

## What landed (the governed wedge)

Every privileged egress now flows through centralized, audited credential
custody, and a human-approval primitive gates flagged session-starts — the
doctrine gate ("no AI autonomy before audit + policy + approval primitives
exist") is satisfied before any agent surface ships:

- **connector-service — credential custody + egress gateway** (T-029, T-030;
  ADR-0053). The **sole** `AIREOS_RESOURCE_SECRET_KEY` holder and only
  `pgp_sym_decrypt` path; the broker leases the target credential per
  session-open via an internal `service-service` RPC and holds no key (SEC-12
  closed, SEC-13 enforced by grep gate + runtime proof). Every lease is
  audit-chained (`credential.leased`). Provisioning: `connector-service
  credentials genkey|set-secret|migrate-legacy`.
- **policy-service — approval/policy decision point** (T-031a; ADR-0054).
  Internal decide/resolve RPCs; owns `pending_approvals` + `policy_outbox`;
  fail-closed ~120 s approval timeout (`approval.denied:timeout`); every
  decision/approval chained (`policy.decision:*`, `approval.granted`,
  `approval.denied:*`).
- **api enforcement + `<ApprovalPrompt>`** (T-031b/c; ADR-0054 §5, §FE-5
  closed). A `requires_approval=true` resource parks the session-start
  `pending_approval`; the operator approves/denies via
  `POST /v1/sessions/{id}/resolve` and the cockpit's `<ApprovalPrompt>`; the
  broker stays a pure data plane (its status guard alone blocks parked
  sessions).
- **Session reaper** (T-032; ADR-0048/0049, re-scoped by ADR-0055). An
  api-owned goroutine closes stranded pre-open sessions (`pending`→`ttl`,
  `pending_approval`→`approval_timeout`), status-only.
- **WD-2 live proof at both altitudes** (T-033a/b). Compose harness:
  `TestGovernedEgressApprovalEndToEnd` — login → parked create → approve →
  live SSH → end, chain carrying `policy.decision:requires_approval` +
  `approval.granted` + `credential.leased` + `session.open` +
  `terminal.input.line` + `session.close` (cast SHA), plus the deny leg
  (blocked WS attach + `approval.denied:operator`). Installed stack: the same
  walkthrough through Caddy HTTPS on the installer-produced systemd host.
- **Offline install ships the spine** (T-033b; OI-6..9). Bundle = five static
  services + authctl + Caddy (104 MB unpacked / 37 MB gz vs 500/150 caps); two
  new units (`aireos-connector`, `aireos-policy`); the resource key lives in a
  connector-only `/etc/aireos/connector.env`.
- **Verification automation** (T-033c; VER-5/6). `task release-check` gains
  policy-service everywhere, connector+policy license legs, the SEC-13(a) grep
  gate (`task sec13-gate`), and the live e2e leg (`task e2e`).

## v0.1.0 → v0.2.0 offline upgrade (the documented procedure)

On the installed host, from the unpacked v0.2.0 bundle (idempotent — existing
keys and operator are preserved):

1. `sha256sum -c SHA256_MANIFEST.txt` — verify the bundle (OI-9).
2. Re-run `./install.sh` with the same `AIREOS_DB_URL` (and operator env vars
   if unattended). It reads the resource key from the legacy `aireos.env` line,
   **relocates it to `/etc/aireos/connector.env`** (0600; the rewritten
   `aireos.env` no longer carries it), deploys the five binaries + two new
   units, runs all five services' migrations (connector + policy are additive),
   and re-enables the units.
3. If target credentials were provisioned before v0.2.0 into
   `resources.connection_secret`: `connector-service credentials migrate-legacy`
   backfills them into connector custody (the legacy column is dropped at
   v0.3.0 per §10a). New credentials: `connector-service credentials
   set-secret`.
4. Verify: all six units active; `audit-service verify` reports the chain
   intact; a flagged session-start surfaces `<ApprovalPrompt>`.

A fully automated in-place upgrade tool stays deferred (§10).

## Release gates

All must-pass criteria are met, with WD-2.5/FE-9 **amended by ADR-0055** (the
durability riders re-scoped at the T-032 consult: reconnect + active-orphan
audit-close → v0.3.0; the reaper ships status-only). §10a residuals ship as
accepted: `resources.connection_secret` column retained (inert; drop = v0.3.0
contract migration), connector→broker hop plaintext on-host (TLS = P2
multi-host), connector single-instance (HA = P1+).

Remaining before the tag (release-criteria §12): `task release-check` green
(machine gate — passing at this commit), the **human-run WD-2** against the
installer-produced stack, and the pre-release audit (cumulative diff review +
install.sh idempotency re-run).

## Known limits / deferred (with triggers)

- **WS-drop reconnect + active-orphan audit-close** → v0.3.0 (ADR-0055; the
  latter needs a broker heartbeat).
- **MCP Gateway, Workstreams, Memory** → v0.3.0+ (this spine is their
  prerequisite; the gateway is its first consumer).
- **Segregation-of-duties approval + async queue** → v0.3.0 (v0.2.0 proves the
  mechanism with same-operator self-approve, ADR-0054).
- **Server-side audit filtering, guacd/GUI adapters, mobile approval,
  cast-replay UI** → unchanged deferrals (§10).
- **Platform:** Linux server (Ubuntu/Debian + RHEL-family, amd64); Windows =
  browser client.

## Verify the release

```sh
task release-check          # all must-pass gates incl. sec13-gate + live e2e (VER-5/7)
task offline-bundle         # fresh bundle from HEAD (104 MB / 37 MB gz)
task wd1                    # WD-2 wedge demo on the installed stack
sha256sum -c SHA256_MANIFEST.txt   # on the unpacked bundle (OI-9)
```
