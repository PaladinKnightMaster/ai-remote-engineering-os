# ADR-0061: Governed Runs — operator-initiated multi-step execution above the gateway (Workstream Engine, layer 1)

## Status

**Accepted** — 2026-06-23 (ratified in the v0.5.0 opening scoping consult + war-room
council review). The first layer of the v0.5.0 "Governed Workstreams" release — the
**execution engine** beneath the chartered Workstream module, and the **first
consumer** of the agent surface built in v0.3.0 (ADR-0056/ADR-0057) and hardened in
v0.4.0 (ADR-0060). Binding spec for the Run ticket ladder (T-054…).

**Builds on / conforms to:** ADR-0054 (policy/approval + `<ApprovalPrompt>`),
ADR-0056 (gateway protocol surface + two-plane model), ADR-0057 (the gateway-owned
**invocation** + invocation-anchored governance + operator approval proxied through
`services/api`), ADR-0058 (broker non-interactive exec = the **sole** SSH egress),
ADR-0059 (informed approval — persisted command + the gateway reaper), ADR-0060
(per-workload keys + the R5-ext **subject-pins** on the gateway internal plane).
**No ADR divergence (G2-clean):** it *extends* ADR-0057's invocation surface with an
operator/service-service create path and *reuses* every downstream contract
unchanged; it edits no Accepted ADR.

Companion spec: `docs/architecture/workstream-engine-architecture.md`.

## Context

v0.3.0 gave one agent tool, one governed invocation, end-to-end; v0.4.0 made that
surface keyless/asymmetric. But nothing yet **chains steps under governance**. The
strategic purpose of the gateway ("an *Agentic* OS, not a governed cockpit") is
unrealized until something drives a **multi-step plan** through the
policy → approval → lease → exec → audit chain.

**Naming (council finding — Workstream Architect).** The chartered **Workstream** is
a *long-lived objective with sources, rules, schedule, scoped memory, and
evidence-bound runs* (lifecycle: ticket → context-pack → **run** → evidence-packet →
done). What this ADR builds is precisely the **run node**: one ordered governed
execution of steps. Naming that primitive a "Workstream" both misnames it against our
own charter and collides with the future module's table name. So the v0.5.0 entity is
a **Run**; the full Workstream (objective/sources/schedule/scoped-memory) is the **P1
module that creates Runs**, deferred. The roadmap nests cleanly: **v0.5 operator-driven
Runs → v0.6 agent-driven Runs → P1 Workstreams orchestrate Runs.**

**Initiator (red-team).** An *agent-initiated* Run forces two unsolved problems one
release early: (R-1) an agent-delegation identity model — and if the engine minted
agent tokens it would re-introduce the privileged minting workload v0.4.0 removed;
(R-2) synchronous block-and-wait does not compose to N steps. **Operator-initiated**
dissolves both (a human initiator with an existing auth model; a fully asynchronous
operator experience).

**Hard constraint (as-built code).** The broker `/v1/exec` is **subject-pinned to
`subject=="mcp-gateway"`** (ADR-0060 R5-ext, PIN-1). So `services/api` **physically
cannot drive egress** — every governed step must pass through the gateway. The gateway
remains the sole governed-step orchestrator; the broker remains the sole SSH egress.

## Decision

1. **The entity is a Run; the engine is operator-initiated and lives in
   `services/api` behind an extraction seam.** An operator authors an ordered list of
   governed steps (each a `remote.exec` `{resourceId, command}`), launches it once,
   and a **sequencer** in `api` drives it. `api` is chosen over a new service because
   it already holds the gateway internal-plane client, the operator auth/cookie/CSRF
   model (ADR-0050), and the background-sweeper pattern (T-032 `ReapStale`). The
   sequencer + Run store are built as a **self-contained `internal/run` package** with
   a clean gateway-client interface, so the **v0.6 extraction** into a standalone
   `services/workstream-engine` (when agent-driven Runs need a delegation model) is a
   lift-and-shift, not a rewrite — and `api` does not silently become a god-service.

2. **The gateway stays the sole orchestrator; the change is one route + a *split*
   refactor.** Today the gateway creates an invocation only through the agent MCP
   `tools/call` path (an `agent-tool` capability, block-and-wait in-request). This ADR:
   - **Refactors** the governed core out of that agent wrapper into a reusable
     `runGovernedStep(initiator, resourceId, command, idempotencyKey, waitTimeout)` —
     create → `policy.Decide` → park/allow → `waitForApproval` → broker exec → finish.
     This touches the v0.4.0-hardened gateway, so it lands as a **behavior-preserving
     step proven green against the existing agent `task e2e` *before* the new route
     rides on it** (gateway-reviewer mandate; default-deny + tool-telemetry-to-audit
     preserved).
   - Adds **one** internal route `POST /v1/steps` (working name) on the **same
     service-service verifier**, **subject-pinned to `api`** (R5-ext, unchanged).
   - `policy-service`, `connector-service`, the broker, the outbox, and the
     dual-anchor audit model are **reused unchanged** — no new credential custody, no
     new key, **no new token-minting workload**. The engine is a service-service peer
     exactly like `api`.

3. **Exec model = held-call reusing `waitForApproval` with an extended per-call
   timeout.** Each `POST /v1/steps` call reuses the e2e-proven
   poll→grant→claim→exec→terminal primitive with a caller-supplied timeout (the Run
   approval window, ≫125s), so an operator-authored plan never fails because approval
   was slow. No new exec-on-grant machinery, no new claim-race. The operator is fully
   async: launch returns immediately; parked steps are approved on the **existing**
   `/invocations` console. *(Deferred: a gateway-side "execute granted invocations"
   worker — the v0.6 scaling evolution.)*

4. **Typed identity & edges (permission-reviewer mandate).** The **launch is a typed,
   audited authorization act on the `human-agent` edge** — and it is **informed**: the
   operator reviews the **full plan** before launching, because launching consents to
   the *unattended* execution of all ungated steps (the launch-time analog of
   ADR-0059's informed approval). The launching operator is the **actor** on every
   step; `policy.Decide` receives the operator as subject (the ADR-0054 same-operator
   self-approve posture, SoD deferred). The per-step **bracket edge is typed
   `human-agent → agent-remote-session`** — it does **not** inherit the agent path's
   `agent-tool` (an operator-driven step is not an agent calling a tool).

5. **Durability & resume are crash-safe with a *reclaimable* driver claim.** `api`
   owns the durable plan (`runs` + `run_steps`, the latter carrying `invocation_id` +
   terminal status). The sequencer **claims a Run before driving** it (status-guarded
   single-winner), and the claim is **lease/staleness-based and reclaimable** — a
   crashed driver does not strand the Run; a restart or sweep reclaims a stale claim
   (the `ReapStale` pattern). On reclaim it resumes at the first non-terminal step.
   Each dispatch uses a **deterministic idempotency key** `run:<id>:step:<index>`, so a
   re-issued already-completed step **dedups** via the existing PERF-2 path and returns
   the original invocation — **never a double-exec** of a side-effecting command. A
   step crashed mid-exec (stuck `status='running'`, uncovered by the existing
   `ReapStale`) is **fail-closed**: it marks the step and the Run `failed` for operator
   review, backed by a **gateway reaper extension** ageing stale `running` → `failed`.

6. **Lifecycle: stop-on-failure, operator cancel, non-atomic.** A
   denied/failed/timed-out step marks the Run `failed` and dispatches no further steps.
   The operator can **cancel/abort a running Run** mid-flight (a typed, audited op — no
   further steps dispatch; an in-flight step finalizes per the running-orphan policy).
   Fail-closed stops *further* egress; it **does not and cannot** roll back
   already-applied steps. **Non-atomicity is an explicit non-guarantee** stated in the
   operator UI and criteria — a Run is an *ordered governed pipeline*, not a
   transaction.

7. **Approval granularity = per-step, with plan context.** Ungated steps run; a step
   on a flagged resource **parks** and is approved on the existing `/invocations`
   surface, **carrying its Run context** ("step k of n" + the plan) onto the approval
   view (informed approval, plan edition — avoids a milder echo of the ADR-0059
   blind-approval problem). **Plan-level grant-with-preview** (approving the whole plan
   incl. its flagged steps up front) is the real product shape and is **deferred to
   v0.6** — named, not dropped.

8. **Audit = Run lifecycle events + a `run:<id>` namespace + an Evidence Packet
   (audit-reviewer mandate).** `api` (already an outbox producer) chains
   `run.launched` (actor = operator, the plan's resource set), `run.completed`,
   `run.aborted`, `run.canceled`. Each step's invocation keeps its by-design dual
   anchor (`invocation:<id>` gateway/broker, `session:<id>` policy/connector) and
   **additionally carries the parent `run:<id>`**. A terminal Run assembles an
   **Evidence Packet** — the ordered steps + their policy decisions + approvals +
   stdout-SHAs + outcomes, hash-linked into the single chain — the replayable proof of
   the whole Run (the chartered "evidence-bound run"). Operator-authored Runs are
   trivially **reproducible** (the plan is the input). The single hash chain stays
   intact.

9. **Bounded plan size and bounded concurrency.** The plan size is **bounded at
   launch** (a concrete cap in the criteria) and **concurrent Runs per operator** are
   bounded (the one genuinely new abuse vector — a pathological plan or a flood of Runs;
   ties to PERF-3). SSH pooling and full per-edge rate-limiting are **not** added —
   sequential steps need neither.

## Consequences

- The platform gains its first **multi-step governed consumer** — the strategic payoff
  of v0.3.0 + v0.4.0 — on a thin, auditable, resumable, **evidence-bound** Run, with
  **no new egress path, no new credential custody, no new key, no new token-minting
  workload**.
- Bounded, honest gateway work: a *split* behavior-preserving governed-core refactor +
  one subject-pinned route + typed initiator identity. Everything downstream is reused
  unchanged.
- The offline/installed leg stays light: OI-12 is an `api` migration with **no new
  systemd unit and no new Caddy route** (the engine rides `api`).
- A new at-rest copy of the operator-authored command lives in `api` (`run_steps`);
  it follows the existing operator-command posture and is additionally sealed
  gateway-side once dispatched (F3, ADR-0059). Persisted-command GC stays deferred.
- The held-call model holds one service-to-service connection (+ one gateway goroutine)
  per *active* step — acceptable at the wedge's sequential / single-node posture
  (ADR-0051), and the named reason the gateway-side exec worker is the v0.6 evolution.
- **Naming is now honest:** `Run` = the execution primitive (this ADR); `Workstream` =
  the chartered objective/schedule/memory module (P1) that will create Runs — no schema
  collision, no burned name.

## Deferred (recorded)

- **The full Workstream module** (objective, sources, rules, schedule, scoped memory,
  owner — the Workstream Architect's charter) — P1; it *creates* Runs.
- **Agent-initiated Runs + a delegation identity model** — v0.6; the trigger to extract
  a standalone `services/workstream-engine`.
- **Plan-level grant-with-preview** — v0.6.
- **Parallel / branching / retry DAG and cross-resource transactional plans** — out of
  scope; the wedge is strictly sequential, stop-on-first-failure.
- **A gateway-side "execute granted invocations" worker** — higher concurrency /
  agent-driven.
- **SSH pooling + full per-edge rate-limiting** (PERF-4) and **persisted-command GC**
  (F3-GC) — the v0.4.0 residuals; pulled in only on a real forcing function.
- **Segregation-of-duties on who may launch vs approve** — inherits ADR-0054/0057.
