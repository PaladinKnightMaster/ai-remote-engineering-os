# Workstream Engine — Tech Spec (v0.5.0 "Governed Workstreams")

Status: **DRAFT** (spec-first; precedes ADR-0061 + the v0.5.0 release criteria).
Scope: the **thin vertical wedge** only — one operator-authored, ordered, durable,
governed multi-step workstream. Agent-initiated workstreams, parallel/branching
plans, and plan-level blanket approval are explicitly **out of scope** (named v0.6+
below).

Conforms to ADR-0054 (approval), ADR-0056/0057 (gateway surface + invocation-
anchored governance), ADR-0058 (broker exec = sole SSH egress), ADR-0059 (informed
approval + persisted command), ADR-0060 (per-workload keys / subject-pins). Edits no
Accepted ADR. Introduces **ADR-0061** (proposed) for the load-bearing decisions
flagged in §7.

---

## 1. Goal & the wedge

Turn the hardened agent surface (v0.3.0 gateway + v0.4.0 key isolation) into its
**first real consumer**: an operator composes an ordered list of governed steps
(each a `remote.exec` on a resource), launches it once, and the system drives the
steps to completion **through the existing governed invocation path** — policy →
approval → connector lease → broker exec → audit — one step at a time, fail-closed.

WD-5 (the release walkthrough): operator authors a 3-step plan → ungated steps run
sequentially → a step on a flagged resource **parks** and is approved on the
existing `/invocations` surface → the plan continues → a denied/failed step
**aborts the remainder with no further egress** → the audit chain links the
workstream to each step's invocation, intact.

## 2. Why operator-initiated, and why the engine lives in `api`

The red-team established that **agent-initiated** workstreams force two unsolved
problems one release too early: (a) an agent-delegation identity model, and (b) a
multi-minute held agent connection (block-and-wait does not compose to N steps).
**Operator-initiated dissolves both** — the initiator is a human with an existing
auth model, and the operator experience is fully **asynchronous** (launch, watch
per-step status, approve parked steps in the existing console).

The engine is a **capability of `services/api`**, not a new service:

- `api` is already the operator control plane (operator auth, cookie, CSRF) and
  **already holds the gateway internal-plane client** (`internal/gateway`,
  subject-pinned `subject=="api"`). The engine extends that client.
- `api` already runs background sweepers (the T-032 `ReapStale` pattern); the
  workstream **sequencer** is the same shape — a goroutine that drives launched
  plans and resumes non-terminal ones on restart.
- A new `services/workstream-engine` would be a **new privileged peer** that drives
  egress — precisely what the red-team (R-1) warned against — plus a new offline
  unit / systemd service / Caddy route. Folding into `api` keeps the offline/
  installed leg light (OI-12 = an `api` migration, no new unit).

**Extraction trigger (v0.6):** when agent-initiated workstreams arrive and need the
delegation identity model, extract the engine into its own service. Thin wedge now,
extract later — the v0.1/0.2/0.3 discipline.

> Honest cost: this is **not** "free in `api`." The gateway is the *only* component
> that may drive egress (broker `/v1/exec` is subject-pinned to `mcp-gateway`;
> `api` cannot reach it). So the wedge requires real gateway work — see §4.

## 3. Architecture

```text
operator ──(cookie/CSRF)──> services/api
                              │  workstreams + steps tables (durable plan)
                              │  sequencer goroutine (drive + resume)
                              │
                              └─(service-service, subject-pinned)─> mcp-gateway
                                       │  NEW internal "run governed step" route
                                       │  reuses the governed core (policy → park/
                                       │  allow → connector lease → broker exec)
                                       │  parked steps surface on EXISTING /invocations
                                       ▼
                                  policy / connector / broker (UNCHANGED) ──> audit chain
```

- **No new egress path, no new credential custody, no new token-minting workload.**
  The engine is a service-service peer like `api` itself; the gateway remains the
  sole orchestrator; the broker remains sole SSH egress.
- **Approval rides the existing surface.** A parked workstream step *is* an
  invocation, so it appears in the existing `GET /v1/invocations` operator console
  and is resolved via the existing `POST /v1/invocations/{id}/resolve`. No new
  approval UI for the wedge — only plan-compose + per-step status are new UI.

## 4. Gateway changes (the real, bounded work)

1. **Refactor the governed core out of the agent wrapper.** Today
   `catalog.Service.Invoke` couples the governed pipeline (create → policy.Decide →
   park/allow → block-and-wait → broker exec → finish) to the MCP wire and an
   `agent-tool` capability
   ([catalog.go:207](../../services/mcp-gateway/internal/catalog/catalog.go)).
   Extract a `runGovernedStep(initiator, resourceID, command, idempotencyKey,
   waitTimeout)` — the **whole** governed path including `waitForApproval`
   ([catalog.go:297](../../services/mcp-gateway/internal/catalog/catalog.go)) — that
   both the agent MCP path and the new internal route call.
2. **ONE new internal route** `POST /v1/steps` (working name) on the **same
   service-service verifier** as the existing internal plane, **subject-pinned to
   `api`** (the engine) — the R5-ext discipline, unchanged. It runs one governed
   step **to terminal** (block-and-wait reused, §5) and returns the outcome
   (`completed`/`failed`/`denied`) + the invocation id. A parked step surfaces on the
   existing `/invocations` console for approval *while this call is held*.
3. **Thread the initiator identity.** The store hardcodes the audit actor to the
   agent subject and edge `agent-tool`
   ([store.go:31](../../services/mcp-gateway/internal/store/store.go)). Generalize so
   a workstream step records the **launching operator** as actor and the appropriate
   edge (§7 decision). `policy.Decide` receives the operator as subject → the
   ADR-0054 same-operator self-approve posture is preserved (SoD stays deferred).
4. **Per-call wait timeout.** `waitForApproval`'s deadline is today a per-`Service`
   constant ([catalog.go:163](../../services/mcp-gateway/internal/catalog/catalog.go));
   thread it per-call so a workstream step can use a window ≫125s without changing the
   agent path's default.

The existing `Create`/`Grant`/`ClaimForExec`/`ReapStale`, the outbox, and the
dual-anchor audit model are reused unchanged. **No new exec-on-grant machinery** —
reusing `waitForApproval` means the held call does the claim+exec itself (this is why
held-call beats a poll+dispatch route set; see §5).

## 5. Interaction & timeout model (resolves R-2)

- **Operator: fully async.** Launch returns immediately; the operator watches the
  workstream UI and approves parked steps in the existing console. The operator never
  holds a connection.
- **Engine→gateway: N sequential held calls**, one per step, driven by the sequencer
  goroutine. Each `POST /v1/steps` call **reuses `waitForApproval` wholesale** — the
  e2e-proven poll→grant→claim→exec→terminal primitive — with a **caller-supplied
  extended timeout** (the workstream approval window, ≫125s). So an operator-authored
  plan never fails because approval was slow, and the wedge adds **no new
  exec-on-grant code path**.
- **Cost, stated honestly:** one held service-to-service call (LAN) **and** one
  gateway-side goroutine+timer per *active* step. Acceptable at wedge concurrency
  (steps are sequential; concurrent workstreams are bounded). `api`'s gateway client
  today has a 10s timeout
  ([client.go:71](../../services/api/internal/gateway/client.go)); the step call needs
  a **separate long-timeout client**.
- **Re-attach for free (resolves part of R-3):** if a held step call drops (api
  restart), `waitForApproval` leaves the row parked for the reaper
  ([catalog.go:304](../../services/mcp-gateway/internal/catalog/catalog.go)); the
  sequencer's resume re-issues the step with the **same idempotency key**, `Create`
  dedups (PERF-2), and the new held call **re-attaches** to the same parked row — the
  identical mechanism the agent flow already uses.
- *Rejected for the wedge:* a non-blocking create + poll + a separate dispatch route
  (3 new routes + a fresh claim-race) — more surface, no proven path. *v0.6
  evolution:* a gateway-side "execute granted invocations" worker decouples exec from
  any caller — needed at higher concurrency / agent-initiated.

## 6. Durability, resume, failure (resolves R-3, R-4)

- **Schema (in `api`):** `workstreams` (id, operator/author, status, created/updated)
  + `workstream_steps` (workstream_id, step_index, resource_id, command,
  invocation_id, status, terminal outcome). **Bounded plan size** enforced at launch
  (the only real new abuse vector — a 10⁴-step plan → resource/audit flood, R-5); pick
  a concrete cap in the criteria (e.g. ≤50 steps).
- **`workstream_steps.command` at rest:** operator-authored, operator-readable —
  treated like the existing operator-authored session command (no new secret class).
  Once dispatched it is *also* sealed gateway-side at rest (F3, AES-256-GCM,
  [store.go:92](../../services/mcp-gateway/internal/store/store.go)); the api-side copy
  follows the operator-command posture (F3-GC may later apply). State, not silent.
- **Concurrency (PERF-3):** the gateway's per-subject in-flight cap
  ([store.go:353](../../services/mcp-gateway/internal/store/store.go)) scores
  operator-initiated steps too; since steps run sequentially (≤1 in-flight per
  workstream) the real limiter is concurrent workstreams under one operator — set the
  cap / a per-operator workstream cap in the criteria.
- **Single-driver guarantee:** the sequencer **claims a workstream** (a status-guarded
  transition, the `ClaimForExec` single-winner pattern at workstream grain) before
  driving it, so a resume tick, a periodic sweep, or a second instance cannot
  double-drive one plan. Deployment is single-node (ADR-0051), but correctness must
  not rely on that.
- **Resume-with-skip (R-3):** on (re)start the sequencer scans claimable non-terminal
  workstreams and resumes at the first non-terminal step. Each dispatch uses a
  **deterministic idempotency key** `workstream:<id>:step:<index>`, so a re-issued step
  that already completed gateway-side **dedups** via the existing PERF-2
  `(subject, idempotency_key)` path
  ([store.go:96](../../services/mcp-gateway/internal/store/store.go)) and returns the
  original terminal invocation — **never a double-exec** of a side-effecting command.
- **The `running`-orphan gap (the hard case):** a step that crashed *mid-exec* leaves
  the invocation stuck `status='running'`; `ReapStale` only finalizes
  `pending_approval`
  ([store.go:209](../../services/mcp-gateway/internal/store/store.go)). The engine
  cannot safely re-exec (double-run) or confirm completion. **Policy = fail-closed:** a
  step resolving to a stale `running` marks the step **and the workstream `failed`** for
  operator review; add a **gateway reaper extension** that ages stale `running` →
  `failed` with `tool.result:error`. Nailed in T-056.
- **Failure = stop-on-first-failure, fail-closed.** A denied/failed/timed-out step
  marks the workstream `failed` and **dispatches no further steps**.
- **Non-atomicity is an explicit non-guarantee (R-4):** a workstream is an *ordered
  governed pipeline, not a transaction*. Fail-closed stops *further* egress; it does
  **not** and cannot roll back already-applied steps (arbitrary shell commands have no
  generic compensation). The criteria and operator UI must state this plainly.

## 7. Open decisions for ADR-0061

1. **Step edge & actor model.** Reuse the egress edge `agent-remote-session` for the
   step while recording the operator as actor, or define a dedicated
   operator/workstream edge for the `tool.invoked`/`tool.result` bracket (today
   hardcoded `agent-tool`). *Lean: operator actor; reuse `agent-remote-session` for
   the egress decision; revisit the bracket edge.*
2. **Exec model.** Held-call reusing `waitForApproval` with an extended per-call
   timeout (the wedge recommendation, §5) vs a non-blocking poll+dispatch vs a
   gateway-side exec worker. *Lean: held-call — least new surface, reuses the proven
   path; the worker is the v0.6 scaling evolution.*
3. **Engine placement.** Fold into `api` (wedge) vs new `services/workstream-engine`.
   *Lean: `api`, extract at v0.6 (§2).*
4. **Approval granularity & plan context.** Per-step parking preserved (wedge, no
   theater — ungated steps run, flagged steps park) vs plan-level grant-with-preview.
   And: a parked step on the generic `/invocations` console shows the command but not
   "step k of n" plan context
   ([PendingInvocation](../../services/api/internal/gateway/client.go) has no
   workstream field) — a mild echo of the ADR-0059 blind-approval problem. *Lean:
   per-step for the wedge; carry workstream context onto the parked-step view (informed
   approval, plan edition); plan-level grant → v0.6.*

## 8. Audit model (resolves R-7)

The chain stays single + intact. Each step's invocation keeps the by-design dual
anchor (`invocation:<id>` at gateway/broker, `session:<id>` at policy/connector). Two
additions:

- **Workstream lifecycle events** via `api`'s outbox (api is already a producer):
  `workstream.launched` (actor = operator, the plan's resource set) and a terminal
  `workstream.completed` / `workstream.aborted` — so "who launched which plan, and its
  outcome" is auditable, not just the individual steps.
- **Correlation namespace `workstream:<id>`:** each step invocation carries the parent
  `workstream:<id>` and the workstream record lists its ordered step/invocation ids —
  bidirectional, so "show everything in this workstream" is answerable across the three
  namespaces. Exact carry mechanism pinned in the criteria.

## 9. Out of scope (deferred, recorded)

Agent-initiated workstreams + delegation identity; plan-level blanket approval;
parallel / branching / retry DAG; cross-resource transactional plans; a standalone
workstream-engine service; gateway-side exec worker; SSH pooling / full per-edge
rate-limiting (sequential steps need neither — R-5); Memory/Context Index (P2);
full per-workload key registry + rotation/JWKS (no forcing function yet).

## 10. Indicative ticket ladder

ADR-0061 → release criteria → T-054 schema/store (api) → T-055 sequencer core + the
gateway governed-step refactor & internal route (subject-pinned) → T-056
resume-with-skip + crash-safety (live kill-mid-plan test) → T-057 flagged-step
parking integration + bounded plan size → T-058 workstream-anchored audit linking →
T-059 operator UI (compose + launch + per-step status) → T-060 WD-5 live capstone
(+ park/approve/continue + denied-aborts negatives) → T-061 OI-12 offline/installed
(api migration + bundle-from-HEAD + installed WD-5) → T-062 release closure.
