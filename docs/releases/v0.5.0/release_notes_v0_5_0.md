# AI Remote Engineering OS — v0.5.0 release notes

**Status: released** — local annotated tag `v0.5.0` (no remote, per repo doctrine).
WD-5 is green on the compose stack (`task e2e`, chain 95), on the real installed
stack (`task wd1`, chain 33), in the browser smoke suite, and in the captured CEO
§human walkthrough (all three legs — happy / denied-abort / cancel — driven through
the rendered cockpit; evidence retained at `tests/smoke/test-results/wd5/`). v0.5.0 —
**"Governed Workstreams"** (layer 1: Runs) — is the first **consumer** of the v0.3.0
Typed MCP Gateway + v0.4.0 keyless-gateway hardening (per
`docs/releases/v0.5.0/v0.5.0-release-criteria.md`; machine-readable coverage in
`release_manifest_v0_5_0.json`; how-to-verify in `verification_runbook.md`).

Baseline: **v0.4.0** (tag `874db08`) on v3.0.1.

## Why (the foundation was built but unconsumed)

v0.2.0–v0.4.0 built and hardened the governed-egress + agent surface (policy,
connector, broker, the Typed MCP Gateway, per-workload asymmetric keys). Nothing yet
**chained steps under governance**. v0.5.0 ships the first real consumer: an operator
authors and drives an ordered, durable, **multi-step** Run, end to end, on real
wires — the Workstream Engine's execution layer (ADR-0061, "Governed Runs").

The pivotal scoping call (after a red-team that reshaped the wedge): **Runs are
operator-initiated, not agent-initiated.** The initiator is a human with an existing
auth model, so launch UX is fully async and the synchronous block-and-wait approval
composes to N steps without holding a connection. Agent-initiated Runs + delegation
identity are the v0.6 extraction trigger.

## What landed

- **The Run engine — `services/api` `internal/run` (RUN-1..5, DUR-1..6).** A
  self-contained, plane-agnostic sequencer drives an operator-authored plan one step
  at a time through the gateway, **stop-on-first-failure / fail-closed**, non-atomic.
  Crash-safe via a **reclaimable single-driver claim** (lease renewed per step) +
  deterministic `run:<id>:step:<index>` idempotency keys (PERF-2 dedup → no
  double-exec on resume); a stuck-`running` step on restart fails closed (the
  running-orphan policy) and a gateway reaper finalizes stale gateway invocations.
  The engine extracts cleanly at v0.6 (the `internal/run` seam).
- **The gateway step plane (RUN-2/3, SEC-24/25).** `runGovernedStep` was split
  (behavior-preserving; agent `task e2e` green first) from the agent `tools/call`
  wrapper, so the gateway stays the **sole** governed-step orchestrator. One new
  **subject-pinned** (`subject=="api"`) internal `POST /v1/steps` route runs a step to
  terminal via the held-call (reuses the e2e-proven `waitForApproval` with an extended
  per-call timeout). No v0.4 pin is loosened.
- **Operator launch + governance (SEC-23, GOV-1..3, PERF-6).** `POST /v1/runs` (+
  list/get/cancel) is a typed, CSRF-gated **`human-agent`** authorization; launch is
  **informed** (the operator reviews the full plan first); `run.launched` is chained
  (actor = operator, the plan's resource set — never the commands). Bounded plan size
  (≤50 steps → 422) + bounded concurrent Runs per operator (→ 429).
- **Audit linking + Evidence Packet (AUD-14/15, EVD-1/2).** `run.*` lifecycle events
  (`run.launched` → per-step `tool.invoked`/`policy.decision`/`remote.exec`/
  `tool.result` → `run.completed`/`aborted`/`canceled`) carry a `run:<id>` namespace,
  bridged to the per-step chain by `run_steps.invocation_id`. The Evidence Packet is a
  **read-time assembly** (no new blob; commands stay operator-only, out of chain) and
  the whole chain stays hash-intact (`audit-service verify`).
- **The operator `/runs` UI (FE-14/15/16).** Compose a plan (reusing the resource
  picker) → an **informed-launch review** of every command verbatim → per-step status
  → cancel. A parked Run step surfaces its **Run context** ("step k of n") on the
  existing `/invocations` console (the api parses the step idempotency key; the raw key
  never reaches the SPA). No new design-system component or token.
- **Offline / installed (OI-12).** The Run engine ships **inside `services/api`** —
  no new systemd unit, no new Caddy route. `install.sh`'s migrate loop already applies
  the new `api` migrations (`runs` + `run_steps`, `claimed_until`) and `GATEWAY_URL` is
  already wired, so the in-process sequencer drives on the install with zero config
  change.

## Security posture (no v0.4.0 invariant weakened)

- Launch/cancel is the typed `human-agent` capability (SEC-23); the `/v1/steps` plane
  is `subject=="api"`-pinned (SEC-24); the per-step bracket is
  `human-agent → agent-remote-session` and the operator id is **audit metadata only**
  — the gateway→broker / broker→connector tokens stay `mcp-gateway`- / `session-broker`-
  pinned exactly as v0.4.0 (SEC-25). The gateway remains keyless of the cluster key.
- The agent `tools/call` path is **unchanged** (proven by `task e2e` staying green at
  every rung — the behavior guard for the split refactor).

## ADRs

- **ADR-0061 ("Governed Runs")** — the sole new ADR (Accepted 2026-06-23; extends
  ADR-0057, edits no Accepted ADR — G2-clean). ADR-0041..0060 unchanged. The canonical
  ladder (`CANONICAL_SOURCE_OF_TRUTH.md`) is through ADR-0061.

## Verification (recorded in `verification_runbook.md`)

`task e2e` is green every rung — the WD-5 capstone (`TestWD5HappyPath` mixed 3-step
park→approve→continue + `TestWD5DeniedAbort` fail-closed) passes on real wires and the
suite's trailing `audit-service verify` confirms the chain hash-intact. The `/runs` +
`/invocations` browser DoD is the `task smoke` suite (5/5 green). `task wd1` is green on
the real privileged-systemd install — a 2-step governed Run drove through the installed
gateway with approvals to completion (chain intact incl. `run.launched`/`run.completed`).
The HEAD offline bundle rebuilt as `0.5.0` under the OI-4 size cap.

**Sign-off:** the CEO §human WD-5 walkthrough (compose a plan in `/runs` → informed
launch → watch the flagged step park on `/invocations` with its Run context → approve →
continue → deny/cancel separate Runs) was captured through the rendered cockpit and
signed off; v0.5.0 is tagged **locally** (`v0.5.0`, annotated). No remote; nothing pushed.

## Next (v0.6+)

Agent-initiated Runs + delegation identity (the `internal/run` extraction trigger);
the chartered Workstream module (objective / schedule / scoped memory that *creates*
Runs); plan-level blanket approval; parallel/branch/retry DAG; a gateway-side exec
worker. Memory/Context Index stays P2.
