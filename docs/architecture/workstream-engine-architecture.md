# Workstream Engine — Tech Spec (v0.5.0 "Governed Workstreams", layer 1: Runs)

Status: **DRAFT** (spec-first; companion to **ADR-0061** + the v0.5.0 release
criteria). Revised after the war-room council review (Workstream Architect,
Permission Model Reviewer, MCP Gateway Reviewer, Audit/Evidence Reviewer).

**Naming (council, ADR-0061 §Context):** the chartered **Workstream** is a long-lived
objective with sources, rules, schedule, scoped memory, and *evidence-bound runs*. The
v0.5.0 entity is the **Run** — one ordered governed execution of steps (the `run` node
of that lifecycle). The full Workstream module (objective/schedule/memory) is **P1**
and will *create* Runs. So this doc specs the **Run engine = layer 1 of the Workstream
Engine**. Throughout, the entity is a `Run` (`runs` + `run_steps`, `run:<id>`).

Scope: the **thin vertical wedge** only — one operator-authored, ordered, durable,
governed, evidence-bound Run. Agent-initiated Runs, parallel/branching plans, and
plan-level blanket approval are explicitly **out of scope** (v0.6+).

Conforms to ADR-0054/0056/0057/0058/0059/0060; edits no Accepted ADR.

---

## 1. Goal & the wedge

Turn the hardened agent surface (v0.3.0 gateway + v0.4.0 key isolation) into its
**first real consumer**: an operator composes an ordered list of governed steps (each
a `remote.exec` on a resource), launches it once, and the system drives the steps to
completion **through the existing governed invocation path** — policy → approval →
connector lease → broker exec → audit — one step at a time, fail-closed, terminating
in an **evidence packet**.

WD-5 (the release walkthrough): operator authors a 3-step plan → reviews the full plan
→ launches → ungated steps run sequentially → a step on a flagged resource **parks**
and is approved (with plan context) on the existing `/invocations` surface → the plan
continues → a denied/failed step **aborts the remainder with no further egress** → the
Run terminates into an evidence packet whose chain links Run ↔ steps, intact.

## 2. Why operator-initiated, engine in `api`, behind an extraction seam

The red-team established that **agent-initiated** Runs force two unsolved problems one
release early: (R-1) an agent-delegation identity model (and token-minting risk that
would undo v0.4.0), and (R-2) a multi-minute held agent connection. **Operator-
initiated dissolves both** — a human initiator with an existing auth model, and a fully
**asynchronous** operator experience.

The engine is a **capability of `services/api`**, not a new service: `api` already is
the operator control plane (auth/cookie/CSRF), already holds the gateway internal-plane
client (`internal/gateway`, subject-pinned `subject=="api"`), and already runs
background sweepers (T-032 `ReapStale`). A new `services/workstream-engine` would be a
**new privileged egress-driving peer** (R-1) + a new offline unit.

**Extraction seam (council — architecture):** the sequencer + Run store are built as a
self-contained **`internal/run` package** with a clean gateway-client interface, so the
**v0.6 extraction** into a standalone service (when agent-driven Runs arrive) is
lift-and-shift, not a rewrite — and `api` does not become a god-service.

> Honest cost: this is **not** "free in `api`." The gateway is the *only* component
> that may drive egress (broker `/v1/exec` is subject-pinned to `mcp-gateway`; `api`
> cannot reach it). So the wedge requires real gateway work — see §4.

## 3. Architecture

```text
operator ──(cookie/CSRF; informed launch)──> services/api
                              │  runs + run_steps tables (durable plan)
                              │  internal/run: sequencer (drive + reclaimable claim + resume)
                              │
                              └─(service-service, subject-pinned)─> mcp-gateway
                                       │  NEW internal "run governed step" route
                                       │  reuses the governed core (policy → park/
                                       │  allow → waitForApproval → broker exec)
                                       │  parked steps surface on EXISTING /invocations
                                       ▼
                                  policy / connector / broker (UNCHANGED) ──> audit chain
```

- **No new egress path, no new credential custody, no new token-minting workload.**
  The engine is a service-service peer like `api`; the gateway remains the sole
  orchestrator; the broker remains sole SSH egress.
- **Approval rides the existing surface.** A parked step *is* an invocation, so it
  appears in the existing `GET /v1/invocations` console (now carrying Run context) and
  is resolved via the existing `POST /v1/invocations/{id}/resolve`.

## 4. Gateway changes (the real, bounded work)

1. **Refactor the governed core out of the agent wrapper — as a *split*,
   behavior-preserving step.** Today `catalog.Service.Invoke` couples the governed
   pipeline (create → policy.Decide → park/allow → block-and-wait → broker exec →
   finish) to the MCP wire and an `agent-tool` capability
   ([catalog.go:207](../../services/mcp-gateway/internal/catalog/catalog.go)). Extract
   `runGovernedStep(initiator, resourceID, command, idempotencyKey, waitTimeout)` —
   the whole path incl. `waitForApproval`
   ([catalog.go:297](../../services/mcp-gateway/internal/catalog/catalog.go)). Because
   this touches the v0.4.0-hardened gateway, the refactor **lands first and is proven
   green against the existing agent `task e2e`** before the new route rides on it
   (MCP-gateway-reviewer mandate; default-deny + tool-telemetry-to-audit preserved).
2. **ONE new internal route** `POST /v1/steps` (working name) on the **same
   service-service verifier**, **subject-pinned to `api`** (R5-ext, unchanged). It runs
   one governed step **to terminal** (held-call, §5) and returns the outcome
   (`completed`/`failed`/`denied`) + invocation id. A parked step surfaces on the
   existing `/invocations` console *while this call is held*.
3. **Thread the initiator identity & type the edges.** The store hardcodes the audit
   actor to the agent subject and the bracket edge to `agent-tool`
   ([store.go:31](../../services/mcp-gateway/internal/store/store.go)). Generalize so a
   Run step records the **launching operator** as actor and the typed bracket edge
   **`human-agent → agent-remote-session`** — NOT `agent-tool` (an operator-driven step
   is not an agent calling a tool; permission-reviewer mandate). `policy.Decide` gets
   the operator as subject → ADR-0054 same-operator self-approve preserved (SoD
   deferred).
4. **Per-call wait timeout.** `waitForApproval`'s deadline is today a per-`Service`
   constant ([catalog.go:163](../../services/mcp-gateway/internal/catalog/catalog.go));
   thread it per-call so a Run step can use a window ≫125s without changing the agent
   path default.
5. **Reaper extension** for stale `running` invocations (the running-orphan, §6) — the
   existing `ReapStale` only finalizes `pending_approval`
   ([store.go:209](../../services/mcp-gateway/internal/store/store.go)).

The existing `Create`/`Grant`/`ClaimForExec`, the outbox, and the dual-anchor audit
model are reused unchanged. **No new exec-on-grant machinery** — reusing
`waitForApproval` means the held call does the claim+exec itself.

## 5. Interaction & timeout model (resolves R-2)

- **Operator: fully async.** Launch returns immediately; the operator watches the Run
  UI and approves parked steps in the existing console. The operator never holds a
  connection.
- **Engine→gateway: N sequential held calls**, one per step, driven by the sequencer.
  Each `POST /v1/steps` call **reuses `waitForApproval` wholesale** — the e2e-proven
  poll→grant→claim→exec→terminal primitive — with a **caller-supplied extended
  timeout** (the Run approval window, ≫125s). So a plan never fails because approval was
  slow, and the wedge adds **no new exec-on-grant code path**.
- **Cost, stated honestly:** one held service-to-service call (LAN) **and** one
  gateway-side goroutine+timer per *active* step. Acceptable at wedge concurrency
  (steps sequential; concurrent Runs bounded — §6). `api`'s gateway client today has a
  10s timeout ([client.go:71](../../services/api/internal/gateway/client.go)); the step
  call needs a **separate long-timeout client**.
- **Re-attach for free (part of R-3):** a dropped held call leaves the row parked for
  the reaper ([catalog.go:304](../../services/mcp-gateway/internal/catalog/catalog.go));
  resume re-issues with the **same idempotency key**, `Create` dedups (PERF-2), and the
  new held call **re-attaches** — the identical mechanism the agent flow uses.
- *Rejected for the wedge:* non-blocking create + poll + a separate dispatch route (3
  routes + a fresh claim-race). *v0.6 evolution:* a gateway-side "execute granted
  invocations" worker.

## 6. Durability, resume, failure, lifecycle (resolves R-3, R-4 + council)

- **Schema (in `api`):** `runs` (id, operator/author, status, created/updated) +
  `run_steps` (run_id, step_index, resource_id, command, invocation_id, status,
  terminal outcome). **Bounded plan size** + **bounded concurrent Runs per operator**
  enforced at launch (the only real new abuse vector — R-5; concrete caps in the
  criteria, e.g. ≤50 steps).
- **`run_steps.command` at rest:** operator-authored, operator-readable — treated like
  the existing operator session command (no new secret class); also sealed gateway-side
  once dispatched (F3, AES-256-GCM,
  [store.go:92](../../services/mcp-gateway/internal/store/store.go)). State, not silent.
- **Concurrency (PERF-3):** the gateway's per-subject in-flight cap
  ([store.go:353](../../services/mcp-gateway/internal/store/store.go)) scores
  operator-initiated steps too; sequential steps are ≤1 in-flight per Run, so the real
  limiter is concurrent Runs (the per-operator Run cap above).
- **Reclaimable single-driver claim:** the sequencer **claims a Run** before driving it
  (status-guarded single-winner), and the claim is **lease/staleness-based** — a crashed
  driver does **not** strand the Run; a restart or sweep reclaims a stale claim (the
  `ReapStale` pattern). Deployment is single-node (ADR-0051), but correctness must not
  rely on it.
- **Resume-with-skip (R-3):** on reclaim the sequencer resumes at the first non-terminal
  step. Each dispatch uses a **deterministic idempotency key** `run:<id>:step:<index>`,
  so a re-issued already-completed step **dedups** via the existing PERF-2 path
  ([store.go:96](../../services/mcp-gateway/internal/store/store.go)) and returns the
  original terminal invocation — **never a double-exec** of a side-effecting command.
- **The `running`-orphan gap (the hard case):** a step crashed *mid-exec* is stuck
  `status='running'`; the engine cannot safely re-exec or confirm. **Policy =
  fail-closed:** mark the step and the Run `failed` for operator review; backed by the
  §4.5 gateway reaper extension. Nailed in T-056.
- **Lifecycle ops:** **stop-on-first-failure** (a denied/failed/timed-out step marks the
  Run `failed`, dispatches no further steps) **+ operator cancel/abort** of a running
  Run mid-flight (a typed, audited op; no further dispatch; an in-flight step finalizes
  per the running-orphan policy).
- **Non-atomicity is an explicit non-guarantee (R-4):** a Run is an *ordered governed
  pipeline, not a transaction*. Fail-closed stops *further* egress; it does **not** and
  cannot roll back already-applied steps. The criteria and operator UI state this
  plainly.

## 7. Identity, edges & approval (permission-reviewer)

- **Launch = a typed, informed, audited authorization act on `human-agent`.** The
  operator reviews the **full plan** before launching — launching consents to the
  *unattended* execution of all ungated steps (the launch-time analog of ADR-0059's
  informed approval).
- **Per-step bracket edge = `human-agent → agent-remote-session`** (not `agent-tool`).
- **Per-step approval, no theater:** ungated steps run; flagged steps **park** and are
  approved on the existing `/invocations` surface, **carrying Run context** ("step k of
  n" + the plan) onto the view (informed approval, plan edition). Plan-level
  grant-with-preview → v0.6.

## 8. Audit model + Evidence Packet (resolves R-7 + audit-reviewer)

The chain stays single + intact. Three elements:

- **Run lifecycle events** via `api`'s outbox (api is already a producer):
  `run.launched` (actor = operator, the plan's resource set), `run.completed`,
  `run.aborted`, `run.canceled`.
- **Correlation namespace `run:<id>`:** each step invocation keeps its by-design dual
  anchor (`invocation:<id>` gateway/broker, `session:<id>` policy/connector) and
  **additionally carries the parent `run:<id>`**; the Run record lists its ordered
  step/invocation ids — bidirectional.
- **Evidence Packet (the chartered "evidence-bound run"):** a terminal Run assembles a
  packet — the ordered steps + their policy decisions + approvals + stdout-SHAs +
  outcomes, hash-linked into the single chain — the **replayable proof** of the whole
  Run. Operator-authored Runs are trivially **reproducible** (the plan is the input).
  Exact packet shape pinned in the criteria with the Audit/Evidence Reviewer.

## 9. Out of scope (deferred, recorded)

The full **Workstream module** (objective/sources/rules/schedule/scoped-memory/owner —
P1; it *creates* Runs); agent-initiated Runs + delegation identity (v0.6, the
extraction trigger); plan-level blanket approval (v0.6); parallel / branching / retry
DAG; cross-resource transactional plans; a gateway-side exec worker; SSH pooling / full
rate-limiting (sequential needs neither — R-5); persisted-command GC (F3-GC);
Memory/Context Index (P2); full per-workload key registry + rotation/JWKS.

## 10. Indicative ticket ladder

ADR-0061 (Accepted) → release criteria → T-054 Run schema/store (`api`, `internal/run`)
→ T-055 the gateway governed-core *split* refactor (agent e2e green) → T-055b the
`/v1/steps` route + sequencer core → T-056 resume-with-skip + reclaimable claim +
running-orphan reaper (live kill-mid-plan test) → T-057 flagged-step parking + Run
context + bounds → T-058 Run audit linking + evidence packet → T-059 operator UI
(compose + informed launch + per-step status + cancel) → T-060 WD-5 live capstone
(+ park/approve/continue + denied-aborts + cancel negatives) → T-061 OI-12
offline/installed (`api` migration + bundle-from-HEAD + installed WD-5) → T-062 release
closure.
