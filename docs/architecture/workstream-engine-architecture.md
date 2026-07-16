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

Scope: §§1–10 spec the v0.5.0 **thin vertical wedge** — one operator-authored, ordered,
durable, governed, evidence-bound Run. **§11 is the v0.6 extension** (agent-initiated Runs
via operator-issued delegation grants; **ADR-0062**), reusing the §§1–10 engine unchanged.
**§12 is the v0.7 extension** (the chartered **Workstream** module — schedule-fired,
grant-bounded Runs; **ADR-0063**), reusing the §§1–11 engine + grant machinery unchanged.
**§13 is the v0.9 extension** (the **adaptive** charter kind — agent-composed per-tick
plans + governed agent memory access; **ADR-0065**), reusing the §§1–12 engine, grant
machinery, and ticker unchanged. Parallel/branching plans remain out of scope.

Conforms to ADR-0054/0056/0057/0058/0059/0060/0061; §11 conforms to ADR-0062; §12 conforms
to ADR-0063; §13 conforms to ADR-0065 (and ADR-0064's memory store); edits no Accepted ADR.

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

~~The full **Workstream module** (objective/sources/rules/schedule/scoped-memory/owner —
P1; it *creates* Runs)~~ **→ now specced in §12 (ADR-0063), riding the same api-local
seams (sources/rules defer with Memory)**; ~~agent-initiated Runs + delegation identity~~
**→ now specced in
§11 (ADR-0062), riding the `internal/run` seam; the standalone-service extraction stays
deferred**; ~~plan-level blanket approval (v0.6)~~ **→ absorbed for the scheduled case by
§12's reviewed immutable template**; parallel / branching / retry DAG;
cross-resource transactional plans; a gateway-side exec worker; SSH pooling / full
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

---

## 11. v0.6 extension — Agent-initiated Runs via delegation grants (ADR-0062)

Status: **DRAFT** (companion to **ADR-0062**, Accepted 2026-06-30). v0.6 adds a **second initiator**
to the §§1–10 Run engine: an **agent** launches a governed Run **bounded by an
operator-issued delegation grant**. The engine, the gateway `/v1/steps` path, policy /
connector / broker, and the audit chain are **reused unchanged**; the new work is the grant
primitive, the actor/principal split, and a keyless launch surface.

### 11.1 The privilege delta (why this is not "flip the initiator")

v0.5 made the initiator a human, so the operator cookie **is** the authority and
informed-launch **is** the authorization. Agent-initiation removes both: an `agent-tool`
token is SEC-16-scoped to one resource+verb (can't author a plan;
[catalog.go:228](../../services/mcp-gateway/internal/catalog/catalog.go)), and no human is
present at launch. The missing authority is supplied by a **delegation grant**.

### 11.2 Delegation grant (the new primitive)

An operator pre-authorizes **one agent** for **a bounded set of resources + verbs**, with
TTL + bounds + a step budget — a durable, **revocable, server-side record** in `api`
(`delegation_grants`), **not** a bearer token. It is the **multi-step, operator-issued,
revocable generalization of the agent-tool token's SEC-16 scope**, and it **replaces
informed-launch** as the human consent (consent moves per-plan → **per-grant**). Custody is
a record (not a token) so it is **instantly revocable** (ADR-0046 defers token-revocation
lists) and can track **stateful budget** — and so the agent holds nothing mintable
(preserving ADR-0060/SEC-20). Lineage: RFC 8693 delegation, RFC 7662 / reference-token
revocation, capability-security POLA — see ADR-0062 §Context.

### 11.3 Launch flow (keyless-surface-preserving)

```text
agent ──(MCP run.launch: grant-id + plan; existing aud=mcp-gateway token)──> mcp-gateway
                              │  verifies the agent token (KEYLESS of the symmetric key)
                              │  forwards {agent-subject, grant-id, plan}
                              └─(service-service, subject-pinned subject=="mcp-gateway")─> services/api
                                       │  validate plan ⊆ grant (the relocated SEC-16)
                                       │  create Run 'pending' (actor=agent, principal=operator, grant_id)
                                       ▼
                                  internal/run sequencer ──> EXISTING /v1/steps ──> policy/connector/broker ──> audit
```

The agent-facing surface stays on the **already-keyless gateway**; `api` (the symmetric-key
holder) stays **non-agent-reachable**, reached only over a gateway-pinned service edge — the
mirror of `/v1/steps`'s `onlyAPI` pin. *(Self-audit reversal: an `api`-direct agent endpoint
was rejected for re-exposing the key holder — ADR-0062 §3.)*

### 11.4 Identity, policy & audit (the actor/principal split)

- **Actor = agent, principal = operator** (RFC 8693 `act`/`subject`). The conflated
  `Subject` ([transport.go:83](../../services/mcp-gateway/internal/mcp/transport.go)) splits
  to `{actor, principal}` across `/v1/steps`, the gateway invocation, and the `api` `runs`
  rows (new nullable `grant_id` + `actor`; null = a v0.5 operator-direct Run). `api` binds
  both server-side; the gateway trusts neither from an agent. Subject-pins untouched
  (SEC-25).
- **Bracket edge = `agent-tool → agent-remote-session`**, with `principal` + `grant_id`
  recorded — **not** `agent-agent` (which denotes agent-to-agent; there is no second agent,
  and a new edge would trigger an ADR-0046 edge-set revision). A delegated step is
  distinguished by the `run:<id>` anchor + `principal` + `grant_id`. *(Self-audit
  correction — ADR-0062 §4.)*
- **Policy subject = the agent.** This defeats ADR-0054 self-approve under the absent
  operator → **flagged steps genuinely park** for a live human on the existing
  `/invocations` console (now also surfacing the agent actor + grant + principal). Ungated
  steps run unattended.

### 11.5 Revocation, bounds, lifecycle

- **Instant revocation cascades:** `status → revoked` also **cancels the grant's active
  in-flight Runs** (reusing `CancelRun` + the sequencer's between-step `RunStatus` stop;
  [sequencer.go:103](../../services/api/internal/run/sequencer.go)) — not just new launches.
- **Bounds (the forcing-function):** resources / verbs / TTL / `max_steps_per_run` /
  `max_concurrent_runs` / `step_budget`. A compromised agent is capped at its grants, never
  the operator's full authority. **Reconcile** `max_concurrent_runs` with the gateway
  `CountInFlight` PERF-3 cap (default 4,
  [catalog.go:342](../../services/mcp-gateway/internal/catalog/catalog.go)) — else a
  refused step fails a legitimate Run.
- **Issuance surface:** `POST /v1/grants`, `POST /v1/grants/{id}/revoke`, `GET /v1/grants`
  (`human-agent` + CSRF; audited `grant.issued` / `grant.revoked`).

### 11.6 Indicative v0.6 ladder

ADR-0062 (Accepted) → v0.6 criteria → grant schema/store + issuance routes (`api`) → the
actor/principal split on `/v1/steps` + gateway invocation + `runs` (behavior-preserving;
agent + operator-Run e2e green first) → plan-vs-grant validation + the `run.launch` gateway
tool + the gateway→`api` pinned forward → flagged-step delegated context + revocation
cascade → live capstone (agent launches a grant-bounded Run; ungated runs unattended,
flagged parks→approve→continue, out-of-grant denied, revoke cancels in-flight) →
offline/installed → release closure. Each rung its own consult.

---

## 12. v0.7 extension — the chartered Workstream module (ADR-0063)

Status: **DRAFT** (companion to **ADR-0063**, Accepted 2026-07-05). v0.7 adds the **third
initiator** — and the chartered module ADR-0061 reserved the name for: a **Workstream** is
a standing, operator-chartered objective (label + objective text + an **immutable plan
template** + a bounded resource/verb scope + a schedule + bounds) that **creates governed
Runs on a schedule with nobody present at launch**. The Run engine, the grant machinery,
`/v1/steps`, policy / connector / broker, and the audit chain are reused unchanged; there is
**zero gateway diff**.

### 12.1 The privilege delta (nobody at launch)

v0.5 had a human at launch; v0.6 had an agent under a grant. A schedule tick has neither —
the authority must be standing, bounded, revocable, consented-to in advance: **exactly the
ADR-0062 delegation grant.** Chartering **auto-issues the Workstream's own grant in the same
transaction** (grantee = `workstream:<id>` — a free-form subject today; principal = the
chartering operator; scope/TTL/bounds = the charter's), so `workstream.chartered` +
`grant.issued` form **one typed, audited `human-agent` consent act** covering objective +
template + scope + schedule + bounds. No third authority model exists. The template is
**immutable post-charter** (consent binds to the reviewed artifact — the ADR-0059/0061
doctrine extended in time); changing the work = retire + re-charter.

### 12.2 Launch flow (api-local; the sequencer needs no changes)

```text
operator ──(cookie/CSRF; charter = ONE consent act)──> services/api
                   │  workstreams + workstream_steps (immutable, sealed template)
                   │  + the charter's delegation_grant (grantee = workstream:<id>) IN-TX
                   │
              in-process ticker (3rd background goroutine; interval_seconds, no cron dep)
                   │  single-winner status-guarded claim advances next_fire_at (no backfill)
                   │  instantiate template → SHARED bounded-launch core (§12.3)
                   │  launch key workstream:<id>:tick:<fired-at> → (grant_id, launch_key) dedup
                   ▼
              runs row 'pending' {operator=principal, actor=workstream:<id>, grant_id}
                   ▼
              EXISTING sequencer ──> EXISTING /v1/steps ──> policy/connector/broker ──> audit
```

The scoping audit's key finding: the sequencer scans for and drives **any** pending Run —
a tick only creates a row. A bounds-refused tick (429: budget/concurrency/ceiling) audits
`run.launch.denied` and re-fires next interval (never wedges); missed ticks collapse to one.

### 12.3 The shared bounded-launch core (the v0.7 split)

Plan-vs-grant (SEC-29) + the PERF-7 bounds today live only inside the gateway-pinned
`LaunchDelegatedRun` handler ([delegated.go:48](../../services/api/internal/server/delegated.go))
— unreachable in-process. v0.7 extracts them into **one shared function** (working shape:
`run.LaunchBounded`, grant access behind a small read interface) used by **both** the
gateway forward and the ticker — one validator, two callers. The split is
behavior-preserving and lands first, **v0.6 WD-6 e2e green**, before the ticker rides on it
(the `runGovernedStep` precedent, §4).

**Budget generalization (the audit's gap):** `step_budget` is cumulative-forever
([store.go:200](../../services/api/internal/run/store.go)) and would deterministically brick
a standing charter. `delegation_grants` gains a **nullable `budget_window_seconds`**: set →
the budget counts only Runs created in the current window ("steps per period"); **NULL →
v0.6 semantics unchanged** (behavior-preserving, additive).

### 12.4 Identity, policy & audit

Actor = **`workstream:<id>`**, principal = the chartering operator — riding the §11.4 split
unchanged (sequencer: `actor != "" → subject=actor, principal=operator`). Policy subject =
the Workstream (ADR-0054 self-approve stays defeated → **flagged steps genuinely park**,
with the FE-18/GOV-6 delegated context reused verbatim); PERF-3 buckets per Workstream;
bracket edge = the existing **`agent-tool → agent-remote-session`** — ADR-0063 fixes the
reading ("agent" = any non-human actor under delegated authority; no eighth edge). Events:
`workstream.chartered/paused/resumed/retired` via the api outbox (content-free), beside the
paired `grant.*`. Run↔Workstream linkage = `runs.grant_id` (charter↔grant is 1:1).

### 12.5 Lifecycle, containment & the scoped-memory slice

- **Pause** (reversible): ticks stop; in-flight Runs complete; grant stays active.
  **Retire** (terminal): revokes the bound grant → the **existing §11.5 cascade** cancels
  in-flight Runs and plan-vs-grant blocks stale ticks — the kill switch is v0.6 machinery
  re-aimed.
- **Runaway-schedule containment (the one new vector):** interval floor + per-grant
  concurrency (≤ the gateway cap) + windowed budget + the per-operator ceiling — with
  flagged resources still parking regardless.
- **Surface:** `POST /v1/workstreams` + list/get + `pause|resume|retire`
  (`human-agent`+CSRF, owner-scoped; fragment+codegen) and a `/workstreams` UI (charter
  form reusing the FE-12 picker + the Runs-compose step pattern; list; detail). The detail
  view's **run history + evidence-packet links are the v0.7 scoped memory** — read-time
  assembly (EVD-1), no new blob; the full Memory/Context Index module stays deferred, with
  the Workstream as its future owning entity. Sources/rules charter fields defer with it.

### 12.6 Indicative v0.7 ladder

ADR-0063 (Accepted) → v0.7 criteria → workstream schema/store + charter-issues-grant in-tx
→ the shared launch-core split (WD-6 green first) → ticker + windowed budget → lifecycle
verbs + retire-cascade → `/workstreams` UI + codegen → WD-7 live capstone (charter → tick
fires unattended → flagged parks→approve→continue → evidence accrues → retire cancels
in-flight + stops ticks) → offline/installed → release closure. Each rung its own consult.

## 13. v0.9 extension — the adaptive charter kind (ADR-0065)

Status: **DRAFT** (companion to **ADR-0065**, Accepted 2026-07-16). v0.9 adds the
**adaptive** charter kind — the charter's per-tick plan is **agent-composed** instead of
template-instantiated, grounded in the charter's own scoped memory (ADR-0064's store,
read over a newly enforced `agent-memory` edge) — and closes the platform's loop:
remember → plan → govern → act → accrue. The Run engine, sequencer, grant machinery,
ticker, policy/connector/broker, and the audit chain are reused unchanged; the gateway
gains three **control-plane tools** (forwards on the `run.launch` pattern — it stays
keyless, ADR-0060) and **zero key/egress change**.

### 13.1 The privilege delta (composition authority + memory access)

v0.7's schedule fired an **immutable, pre-reviewed** template — consent bound to the
artifact. An adaptive charter has no template: authority splits into **composition**
(a named external agent authors each tick's plan, reading the charter's scoped memory)
and **execution consent** (the operator reviews each composed plan before it runs). The
charter names the composer at consent time and its auto-issued grant's **grantee = that
agent subject** (ADR-0065 R2) — one audited consent act covering objective + scope +
schedule + bounds + *who composes*. The successor invariant: **consent binds to each
tick's reviewed plan**, inside the standing grant's bounds; at v0.9 a composed plan
**always parks** (unattended-adaptive deferred).

### 13.2 The inbound plan channel (the tick publishes, the agent answers)

The platform never dials out (ADR-0056: agents are inbound MCP clients), so "the charter
invokes an agent" compiles to an inversion:

```text
ticker (adaptive branch) ──> plan_request row {workstream, tick launch key, TTL}   plan.requested
        │                          ▲
        │            agent ──(gateway `workstream.poll` — own charters only)──┘
        │            agent ──(gateway `plan.submit`, idempotency-keyed on the tick key)
        │                          │  Bearer subject pinned to the grant's grantee
        ▼                          ▼
   TTL expiry → plan.expired   run.LaunchBounded (plan ⊆ grant, SEC-29 — one validator, 3 callers)
   (never wedges; next tick        ▼
    opens a fresh request)     runs row **`proposed`** {actor = composer agent, principal = operator}
                                   ▼  operator informed plan-preview (approval console)
                          approve → `pending` → EXISTING sequencer   |   deny → terminal, audited, accrues
```

### 13.3 The memory-read plane (`agent-memory` enforced)

`memory.read` (gateway tool → pinned api forward) is **read-only** and **same-grant
scoped by construction**: the readable set derives from the caller's grant — its
charter's `workstream:<id>` scope + that grant's Runs (`runs.grant_id`) — never
`operator` scope, never a foreign charter. Accrued reference rows (content-free) are
readable by default; **operator-authored content requires the audited share flip**
(`agent_visible`, default false; `memory.shared`/`memory.unshared` — migration 0013).
Every agent-plane read is chained (`memory.read`, content-free row ids); the operator
`/v1/memories*` surface (ADR-0064) is byte-identical — the path lights the reserved
`agent-memory` edge as its typed edge (the set unchanged; ADR-0046 preserved).

### 13.4 Identity, policy & audit

Actor = **the composing agent**, principal = the chartering operator (the §11.4 split —
honest attribution: the agent authored the plan, the schedule only asked). Policy subject
= the agent → self-approve stays defeated; flagged resources park per step **in addition
to** the plan-level park. New content-free events: `plan.requested`/`plan.submitted`/
`plan.expired`, `memory.read`, `memory.shared`/`memory.unshared`, + the `proposed`-Run
resolution (exact vocabulary + the new SEC code pinned at the v0.9 criteria consult).

### 13.5 Lifecycle & containment

Pause/retire carry from §12.5; the revocation cascade is **re-aimed at the new
surfaces**: a revoked/expired grant closes its open plan requests, refuses
`plan.submit` and `memory.read`, and a `proposed` Run of a dead grant cannot be
approved. Runaway containment (interval floor, windowed budget, concurrency, ceiling)
carries unchanged; an unanswered plan request expires audited and never wedges the
charter. Template charters (§12) are structurally untouched — WD-5/6/7/8 stay elevated
regression guards.

### 13.6 Indicative v0.9 ladder

ADR-0065 (Accepted) → v0.9 criteria (WD-9 = the closed-loop wedge: request → memory-read
→ composed plan → park → approve → drive → accrue → next tick sees it; negatives:
cross-grant read denied+chained / unshared content invisible / revoked-grant kills all
three surfaces / expiry never wedges / SEC-29 refusal unchanged) → migration 0013 +
adaptive charter schema/store → plan-request store + ticker branch → gateway tools +
forwards + `agent-memory` edge → the `proposed` Run + preview UI → share flip + consent
copy → WD-9 e2e capstone → offline/installed → release closure. Each rung its own
consult.
