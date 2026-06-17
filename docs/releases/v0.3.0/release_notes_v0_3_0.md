# AI Remote Engineering OS — v0.3.0 release notes

**Status: release-candidate — ready for the human WD-3 walkthrough + tag** (the tag
awaits the CEO §human WD-3 confirmation on the installer-produced stack and explicit
sign-off; local annotated tag only, no remote). v0.3.0 — **"Typed MCP Gateway"** — is
the first **agent surface** on the v0.2.0 governed-egress spine (per
`docs/evaluation/v0.3.0-release-criteria.md`; archival copy beside this file;
machine-readable coverage in `release_manifest_v0_3_0.json`).

## What landed (the agent surface)

An autonomous agent can now call one typed tool — `remote.exec(resourceId, command)`
— and have it routed through the **same governance spine** the v0.2.0 cockpit uses:
scoped agent identity → policy decision → **human approval (block-and-wait)** →
connector credential lease → broker non-interactive exec → hash-intact audit. The
doctrine gate holds: no AI autonomy without audit + policy + approval, all of which
predate and gate this surface.

- **`services/mcp-gateway` — the Typed MCP Gateway** (T-035…T-039; ADR-0056/0057).
  A hand-rolled MCP subset (`initialize`/`tools/list`/`tools/call`) over JSON-RPC,
  **no third-party SDK** (supply-chain/offline doctrine), `agent-tool` Bearer
  (audience `mcp-gateway`), `:8085`. One tool, `remote.exec`, drives a governed
  **invocation** (own `invocations` table + outbox) — scope check (SEC-16, resource +
  operation), per-agent concurrency cap (PERF-3), idempotency (PERF-2), fail-closed
  on any downstream outage (SEC-19).
- **Agent identity** (T-036; AG-1..3). `IssueAgent` (5-min scoped token) +
  operator-gated `POST /v1/agent-tokens` on `services/api` (human-agent + CSRF), mint
  audit-chained. The gateway holds no secret-resource key (SEC-13/17).
- **Broker non-interactive exec** (T-038; ADR-0058). `sshpty.Exec` reuses the
  host-key-pinned dial + the connector lease (the broker stays the **sole** SSH
  egress), bounded output + command timeout (PERF-1), stdout-SHA audit anchor (AUD-10).
- **Invocation-anchored approval** (T-040; ADR-0057 §4 / **ADR-0059**). A flagged
  invocation **parks and blocks** the agent's call; the operator sees the agent +
  the **verbatim command** (ADR-0059 — informed consent) and approves/denies via
  `services/api` (proxied to the gateway's internal plane; cookie/CSRF stay on api,
  ADR-0050), reusing `<ApprovalPrompt>`. Grant → exec; deny/timeout → typed error, no
  egress. A serve-time reaper finalizes orphaned parked invocations.
- **WD-3 live proof at both altitudes** (T-041 compose, T-043a installed).
  `TestGatewayGovernedExecApprovalPath` (compose e2e): agent → gateway block-and-wait
  → operator approve → broker exec, chain `tool.invoked → policy.decision:requires_approval
  → approval.granted → credential.leased → remote.exec(stdout-SHA) → tool.result`,
  plus a deny leg; `audit-service verify` confirms the whole chain intact. The same
  WD-3 runs on the **installer-produced systemd stack** through Caddy HTTPS.
- **Cockpit polish** (T-042; FE-12). A resource **picker** (Sessions page; replaces
  raw-UUID entry) + audit-table fixed-layout truncation. Browser-rendered evidence
  via `task smoke`.
- **Offline install ships the gateway** (T-043a; OI-10). Bundle = six static services
  + authctl + Caddy (115 MB unpacked / 41 MB gz vs 500/150 caps); new
  `aireos-mcp-gateway` unit; `GATEWAY_URL`/`BROKER_URL`/`MCP_ADDR` wired into
  `aireos.env`; Caddy fronts the agent `/mcp` plane same-origin.

## Audit-chain anchoring (as-built note)

One invocation correlates across **two anchor namespaces by design** (AUD-11): the
gateway + broker anchor `invocation:<uuid>`; policy-service + connector-service anchor
`session:<uuid>` — the **same uuid** under each producer's fixed prefix. This is the
deliberate opaque-key reuse (ADR-0054/0057) that keeps policy-service and
connector-service unchanged; the global hash chain is single + intact regardless of
prefix.

## Pre-release audit + red-team (done)

A cumulative code-review + adversarial red-team over T-035…T-041 + ADR-0056…0059 ran
before closure. **No exploitable bypass of policy/approval/lease/audit was found** —
notably, the approved-command-swap attack is provably closed (an idempotent retry
execs the *persisted* command, not the retry's args). Findings were triaged:

- **Fixed pre-tag (T-041h):** ops-scope now *enforced* (SEC-16); request bodies
  capped on the agent + internal planes; the internal operator plane pinned to
  `subject == "api"` (defense in depth); a targeted CSRF test on the resolve route.
- **v0.4.0 residuals (recorded):** command at-rest encryption (today: operator-only
  read + no-log + bounded parked retention; plaintext otherwise) and asymmetric /
  per-workload keys (the agent-facing gateway holds the symmetric cluster key under
  the P0 model — strengthens the ADR-0046 P1 trigger).

## Release gates

All must-pass criteria are met. `task release-check` is the machine gate (lint /
sec13-gate / licenses / test / integration / typecheck / web build / codegen drift /
**e2e with WD-3** / **smoke with the invocation UI**). Remaining before the tag
(release-criteria §human): the **human-run WD-3** against the installer-produced
stack + the CEO tag decision (local annotated tag `v0.3.0`; no remote).

## Known limits / deferred (with triggers)

- **External MCP-server proxying + untrusted-server isolation/sandboxing**, tool
  discovery + typed-client codegen → v0.4.0 (TR-2; trigger: a scoped external-server
  ADR). v0.3.0 is internal-facades-only (TR-1).
- **Full per-edge rate-limiting + quotas; SSH connection pooling** → v0.4.0 (trigger:
  multi-agent / sustained load). v0.3.0 ships the per-agent concurrency floor +
  fresh-dial-per-invocation.
- **Command at-rest encryption; asymmetric/per-workload keys; S2S subject-pin on the
  broker/connector planes** → v0.4.0 (red-team residuals).
- **Workstreams, Memory** → next P1 modules (consume this gateway).
- **Segregation-of-duties on invocation approval; mobile approval** → deferred (any
  operator may approve any invocation at v0.3.0).
- **Durability residuals** (WS-drop reconnect; broker heartbeat / active-orphan
  audit-close) → unchanged deferrals (each needs its own ADR).
- **Platform:** Linux server (Ubuntu/Debian + RHEL-family, amd64); Windows = browser
  client.

## Verify the release

```sh
task release-check          # all must-pass gates incl. sec13-gate + WD-3 e2e + smoke
task offline-bundle         # fresh bundle from HEAD (115 MB / 41 MB gz)
task wd1                    # WD-2 + WD-3 wedges on the installed stack
sha256sum -c SHA256_MANIFEST.txt   # on the unpacked bundle (OI-9)
```
