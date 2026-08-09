# Agent Permission Model

## Principle
Agents receive scoped capabilities, not ambient authority.

## Capability edges
A capability edge defines who/what can communicate with or act on another resource:
- human-agent
- agent-agent
- agent-tool
- agent-workspace
- agent-repo
- agent-memory
- agent-remote-session

Each edge defines allowed operations, context visibility, approval rules, rate limits, and audit level.

## Enforced edges at v0.2.0 (implementation references; DOC-9)

Every edge below is gated by `packages/go/middleware` `RequireCapability` over
PASETO v4 capability tokens (`packages/go/auth`, ADR-0046). `service-service` is
the eighth, infra-level edge ADR-0046 added to the seven above.

| Edge | Decision enforced | Handler |
|---|---|---|
| `human-agent` | Operator login/session/CSRF; the approve/deny verb on a parked session **and on a parked tool invocation**; **launch/cancel a governed Run (v0.5.0)**; **issue/revoke a delegation grant (v0.6.0)**; **charter/pause/resume/retire a Workstream (v0.7.0)**; **list/author/redact/pin a scoped memory (v0.8.0 — the operator memory surface, SEC-31)**; **share/unshare a memory to the composer + approve/deny a `proposed` (agent-composed) Run (v0.9.0 — the two GOV-10 consent surfaces)**; **register/list/remove a charter-bound context source (v0.10.0 — the GOV-11 consent surface)** | `services/api/internal/server` (`CreateSession`, `ResolveSession`, `ResolveInvocation`, `CreateRun`, `CancelRun`, `createGrant`, `revokeGrant`, `CreateWorkstream`, `PauseWorkstream`, `ResumeWorkstream`, `RetireWorkstream`, `ListMemories`, `CreateMemory`, `RedactMemory`, `PinMemory`, `UnpinMemory`, `ShareMemory`, `UnshareMemory`, `ApproveRun`, `DenyRun`, `ListContextSources`, `CreateContextSource`, `DeleteContextSource`) |
| `agent-tool` | An agent's MCP `tools/call` on the gateway (the `remote.exec` tool) — scoped (SEC-16) to the agent token's resource; drives a governed invocation | `services/mcp-gateway/internal/server` (`/mcp`; audience `mcp-gateway`) |
| `agent-tool` (**`run.launch`**, v0.6.0) | An agent launches a grant-bounded Run — a **control-plane** tool (no single-resource target, so the per-call SEC-16 resource check is N/A; the api-validated **grant** is the scope, SEC-29); mints no egress; idempotency-keyed (a retry re-attaches, never double-launches) | `services/mcp-gateway/internal/catalog` (`run.launch` branch before the target resolver) |
| `agent-tool` (**`workstream.poll`** / **`plan.submit`**, v0.9.0) | The composer discovers its charters' open plan requests (with composition context) and answers one with an ordered plan — control-plane forwards like `run.launch`; the api pins `plan.submit` to **the grant's grantee** (an unknown request and a foreign agent's answer the byte-identical refusal — no oracle); the submitted plan runs plan-vs-grant (SEC-29) and parks as a `proposed` Run (it executes NOTHING) | `services/mcp-gateway/internal/catalog` (control-plane branches) → pinned api internal routes (`/v1/internal/plan-polls`, `/v1/internal/plan-submissions`) |
| **`agent-memory`** (**`memory.read`**, v0.9.0 — **the reserved edge's ONE enforced path**) | The composer reads the memory its own live grants entitle it to — **read-only**; the readable set is derived **entirely server-side** (charter scope + that grant's Runs — cross-grant impossible by construction); tombstoned rows excluded; authored content only when the operator's **share flip** set `agent_visible`; results clamped 50/200; **every read chains one `memory.read` event fail-loud in the read's own transaction** (no event, no data); rides the gateway's per-agent in-flight cap | `services/mcp-gateway/internal/catalog` (`memory.read`, `Edge: "agent-memory"`) → `services/api/internal/server` (`/v1/internal/memory-reads`) → `internal/memory.ReadForAgent` (the operator-keyed `List`/`Get` are a separate, untouched surface) |
| **`agent-workspace`** (**`context.search`**, v0.10.0 — **the reserved edge's second enforced path**) | The composer searches the charter-bound context its own live grants entitle it to — **read-only** (no agent write path; indexing is a machine sweep of operator-registered sources); the readable set is derived **entirely server-side** (live grants → each grant's charter → context sources bound to that charter → FTS over their chunks — cross-grant/foreign-source reads impossible by construction, never by filter); an out-of-set `workstreamId` answers **empty, never an error** (no oracle); the indexer is **confined to the registered source root** (no path traversal, no symlink-escape, no out-of-root read); results clamped 50/200; **every read chains one `context.read` event fail-loud in the read's own transaction** (no event, no data); rides the gateway's per-agent in-flight cap; the composed plan **still always parks** (ADR-0065's injection control carries) | `services/mcp-gateway/internal/catalog` (`context.search`, `Edge: "agent-workspace"`) → `services/api/internal/server` (`/v1/internal/context-searches`) → `internal/ctxindex.SearchForAgent` (the operator `/v1/context-sources*` register/list/remove surface is a separate, untouched route set) |
| `service-service` (mcp-gateway → api, **launch forward**, v0.6.0) | The keyless gateway forwards {agent-subject, grant-id, plan} for plan-vs-grant validation + Run creation; **subject-pinned `subject=="mcp-gateway"`** on a route-scoped asymmetric-only verifier (SEC-27) | `services/api/internal/server` (internal `POST /v1/internal/delegated-runs`; api holds the gateway's PUBLIC key only — a verify key, not a mint key) |
| `agent-remote-session` | Cockpit WS attach only for a `pending` session — a parked `pending_approval` session is refused (the approval gate needs no broker code) | `services/session-broker/internal/server` (status guard) |
| `service-service` (api → policy-service) | `requires_approval` decision at session-create + operator resolution relay | `services/policy-service/internal/server/server.go` (decide + resolve handlers; audience `policy-service`) |
| `service-service` (api → mcp-gateway) | Operator invocation list + approve/deny, proxied off the browser plane | `services/mcp-gateway/internal/server` (internal `/v1/invocations*`; audience `mcp-gateway`, edge `service-service`) |
| `service-service` (api Run engine → mcp-gateway, v0.5.0) | One governed Run step to terminal via the held-call; **subject-pinned `subject=="api"`** (SEC-24) | `services/mcp-gateway/internal/server` (internal `POST /v1/steps`; bracket edge `human-agent → agent-remote-session`) |
| `service-service` (gateway → policy / broker) | Per-invocation policy decide/resolve + the governed non-interactive exec | `policy-service` (audience `policy-service`) + `session-broker` `/v1/exec` (audience `session-broker`) |
| `service-service` (broker → connector-service) | Just-in-time credential lease — the only decrypt path | `services/connector-service/internal/server/server.go` (lease handler; audience `connector-service`) |

Approval rule (ADR-0054): a resource labeled `requires_approval=true` parks the
session-start until the operator resolves it; the decision authority is
policy-service (`pending_approvals`, fail-closed timeout sweeper in
`services/policy-service/internal/store/store.go`). Audit level: every decision
and resolution is chained (`policy.decision:*`, `approval.*`; SEC-14).

Invocation approval (v0.3.0; ADR-0057/0059): the same flag + the same policy
authority gate an agent tool invocation. The gateway parks the invocation and
**blocks** the agent's `tools/call` (block-and-wait) until the operator resolves it
via `api → gateway` (proxied); the operator sees the agent + the **verbatim command**
(ADR-0059, operator-only — never on the agent plane) before approving. Grant →
governed exec; deny/timeout → typed error, no egress (fail-closed, SEC-19). A
gateway reaper finalizes an invocation orphaned by a disconnected agent.

## Per-workload key isolation (v0.4.0; ADR-0060)

At v0.3.0 every service — including the agent-facing `mcp-gateway` — held the one
symmetric cluster key (`AIREOS_PASETO_KEY`), which under PASETO v4 *local* both
signs and verifies. RCE on the gateway therefore meant *mint any token for any
edge* — including a `connector-service` lease that decrypts any credential. v0.4.0
removes that blast radius:

- **The gateway is keyless of the cluster (SEC-20).** The edges it touches move to
  PASETO **v4 public** (Ed25519): `services/api` signs the `agent-tool` token (and
  its `service-service` token to the gateway's internal plane) with a single
  agent-plane private key; the gateway verifies inbound with api's **public** key
  alone. The gateway signs its own outbound (→ policy / broker / audit) with its
  **own** private key; those peers verify it with the gateway's public key. The
  gateway boots with **no `AIREOS_PASETO_KEY`** and so can mint nothing.
- **Dual-mode verifier (`packages/go/auth`).** policy / audit / broker-exec accept
  *either* a `v4.local` token (the cluster key — api and the other symmetric
  callers) *or* the gateway's `v4.public` token, dispatching on the token type.
  Non-gateway planes (operator cookie, broker → connector, api → policy/broker)
  stay symmetric — only the agent-facing surface is de-keyed.
- **S2S subject-pins (R5-ext).** The broker `/v1/exec` handler rejects any caller
  whose token subject ≠ `mcp-gateway`; the connector lease handler rejects any
  subject ≠ `session-broker` — defense in depth over the `service-service` edge,
  mirroring the gateway internal plane's `subject == "api"` pin.
- **SEC-21 (proven).** With only its own key, a compromised gateway can forge no
  token connector or api accepts (their verifiers never hold the gateway's public
  key) — asserted by the WD-4 negative test and the installed `run-wd1.sh` check.
- **Command at-rest (F3, extends ADR-0059).** The persisted invocation command is
  AES-256-GCM encrypted with a gateway-local key distinct from the cluster and
  resource keys; decrypted only on the operator-only read path.

`connector-service` is deliberately **not** dual-mode — the broker (symmetric) is
its sole caller, so the credential-decrypt authority stays reachable only by the
cluster key, which the agent-facing gateway no longer holds.

## Governed Runs (v0.5.0; ADR-0061)

A **Run** is one operator-initiated, ordered, durable, governed multi-step
execution (the Workstream Engine's layer 1). It adds an *operator-driven* path over
the same agent surface without weakening any v0.3/v0.4 invariant — every step still
flows through the gateway → policy → connector → broker (the gateway stays the sole
governed-step orchestrator; `services/api` cannot drive egress).

- **Typed launch authorization (`human-agent`; SEC-23).** Launching and canceling a
  Run is a `human-agent` capability on `services/api` (operator cookie + CSRF), the
  same gate as session/invocation approval — `CreateRun` / `CancelRun` in
  `services/api/internal/server`. Launch is **informed** (the operator reviews the
  full plan before the POST) and audited (`run.launched` / `run.canceled`, actor =
  operator, the plan's resource set — never the commands). No anonymous or
  agent-plane launch path exists at v0.5.0.
- **The step plane is subject-pinned (`subject=="api"`; SEC-24).** The gateway's
  internal `POST /v1/steps` (one governed step to terminal via the held-call) rejects
  any caller whose token subject ≠ `api` — the same pin shape as the gateway's
  `/v1/invocations` plane. Not in the OpenAPI fragment (service-service RPC).
- **Per-step bracket edge `human-agent → agent-remote-session` (SEC-25).** A Run step
  carries the launching operator as actor on a `human-agent → agent-remote-session`
  bracket (NOT `agent-tool` — it is not an agent acting). The underlying
  gateway→broker / broker→connector tokens stay **`mcp-gateway`- / `session-broker`-
  pinned** exactly as v0.4.0; the operator identity is audit metadata and loosens no
  pin. A flagged step still parks on the existing `/invocations` console (with its
  Run context) and is approved by the same `human-agent` verb.

Audit: each step reuses the invocation anchor (`invocation:<id>`); the Run is
correlated by a `run:<id>` namespace on the `run.*` lifecycle events, bridged to the
per-step chain via `run_steps.invocation_id` (the read-time Evidence Packet). The
whole chain stays single + hash-intact (`audit-service verify`).

## Delegated Runs (v0.6.0; ADR-0062)

v0.6.0 adds the **second initiator**: an agent launches a governed Run **bounded by
an operator-issued delegation grant**. The consent moves from per-plan (v0.5
informed launch) to **per-grant**; the Run engine, `/v1/steps`, policy → connector →
broker, and the audit chain are reused unchanged.

- **The grant is the authority — server-side, revocable; the agent never mints
  (SEC-26).** A `delegation_grants` record in `services/api` (`internal/grant`):
  principal / agent / resources[] / verbs[] / TTL / max-steps-per-run /
  max-concurrent-runs / step-budget / status. RFC 7662 reference-token custody: the
  agent references it **by id** and holds nothing mintable — instant revocation +
  stateful budget stay server-side. Issue/list/revoke are typed, CSRF-gated
  `human-agent` routes (`/v1/grants*`), the GOV-4 coarsening of consent made
  explicit in the issuance UI and contained by bounds + flagged-resource parking.
- **The launch surface stays on the keyless gateway (SEC-27).** The agent's existing
  `aud=mcp-gateway` token calls the **`run.launch`** control-plane tool; the gateway
  **forwards** {agent-subject (from the verified token), grant-id, plan} to `api`
  over the internal `POST /v1/internal/delegated-runs` route — **subject-pinned
  `subject=="mcp-gateway"`** on a **route-scoped, asymmetric-only** verifier
  (stronger than dual-mode: even a cluster-key holder cannot forge onto it). The
  symmetric-key-holding `api` is **not** agent-reachable (WD-6 N3, compose +
  installed). At install, api verifies the forward with
  `AIREOS_GATEWAY_PUBLIC_KEY`, which has ridden shared `aireos.env` since OI-11 —
  **v0.6 provisions zero new key material**; the one new wire is `API_URL` on
  `gateway.env` (OI-13).
- **Plan-vs-grant is the relocated, generalized SEC-16 (SEC-29).** Before any
  dispatch `api` validates server-side: agent == grant.agent ∧ active ∧ unexpired ∧
  every step's resource ∈ resources ∧ verb ∈ verbs ∧ plan ≤ max-steps ∧ active runs
  < max-concurrent (and < the per-operator ceiling) ∧ budget covers the plan. A
  rejected plan denies + audits (`run.launch.denied`) with **no egress** (WD-6 N1).
- **Actor/principal split (SEC-28; RFC 8693).** A delegated Run records
  **actor = the agent, principal = the delegating operator, grant_id** on the `runs`
  row, `/v1/steps`, and the gateway invocation — bound **server-side from the
  grant**; the gateway trusts neither from an agent. The delegated step bracket edge
  is **`agent-tool → agent-remote-session`** (derived: principal set → `agent-tool`;
  operator-direct stays `human-agent`) — the canonical seven-edge set is unchanged.
  No v0.4/v0.5 pin is loosened: `/v1/steps` stays `subject=="api"`, broker/connector
  stay `mcp-gateway`/`session-broker`-pinned (SEC-25 carried).
- **Flagged steps still park; `policy.Decide` subject = the agent (GOV-5/6).** With
  the agent as policy subject, ADR-0054's same-operator self-approve cannot resolve
  a flagged step under the absent principal — it parks for a **live human**, whose
  `/invocations` view carries the full delegated context (**agent · principal ·
  grant · run**) beside the verbatim command (operator-only, ADR-0059 unchanged).
- **Revocation cascades (DUR-7).** Revoke flips the grant AND cancels its active
  in-flight Runs via the existing `CancelRun` (`run.canceled` + the sequencer's
  between-step status stop); an in-flight parked step finalizes per the DUR-4
  running-orphan policy — proven live mid-park (WD-6 N2, compose + installed).

Event vocabulary added (AUD-16/17; content-free, ADR-0049): `grant.issued` /
`grant.revoked` (actor = operator, edge `human-agent`, target `grant:<id>`, in-tx
with the row change) and `run.launch.denied`; a **delegated** Run's `run.launched` +
terminal `run.*` carry **actor = the agent** (an operator Run keeps actor =
operator), and its steps' `tool.invoked`/`tool.result` carry the agent actor on the
`agent-tool` edge. Principal + grant provenance live on the durable rows
(`runs.actor`/`grant_id`, `invocations.principal`) and are assembled at read time
into the Evidence Packet (EVD-3) — a delegated Run is attributable to **both** the
agent that drove it and the operator whose grant authorized it.

## Chartered Workstreams (v0.7.0; ADR-0063)

v0.7.0 adds the **third initiator**: a standing operator-chartered **Workstream**
fires governed Runs **on a schedule, with nobody present at launch**. It creates no
new authority model — the charter rides the v0.6 delegation grant — and no new
capability edge; the Run engine, `/v1/steps`, policy → connector → broker, and the
audit chain are reused unchanged. There is **zero gateway change** (the schedule-fired
launch never crosses the gateway).

- **The charter is one consent act; its authority IS a delegation grant (WS-1/WS-2).**
  A `workstreams` + `workstream_steps` record in `services/api` (`internal/workstream`,
  sibling of `internal/run`/`internal/grant`): objective + a bounded resource/verb
  scope + an **immutable, sealed plan template** + a fixed-interval schedule + bounds.
  Chartering **auto-issues the Workstream's own delegation grant in the same
  transaction** (grantee = `workstream:<id>`, principal = the chartering operator, 1:1);
  `workstream.chartered` + `grant.issued` chain together. The template is immutable
  post-charter (edit = retire + re-charter — consent binds to the reviewed artifact,
  the ADR-0059 doctrine extended in time). Charter/pause/resume/retire are typed,
  CSRF-gated `human-agent` routes (`/v1/workstreams*`).
- **The schedule fires through the shared bounded-launch core (WS-3, SEC-30).** An
  in-process ticker (a third `services/api` background goroutine — no cron dependency;
  a durable `next_fire_at`, single-winner CAS claim, no backfill) fires due charters
  through the **same** `run.LaunchBounded` core the gateway forward uses — one
  validator, two callers; the existing sequencer drives the Run it creates.
- **"Agent" = any non-human actor under delegated authority.** A Workstream Run
  records **actor = `workstream:<id>`, principal = the chartering operator, grant_id**
  on the ADR-0062 actor/principal split, unchanged. `policy.Decide` subject = the
  Workstream, so ADR-0054 self-approve cannot fire — a flagged tick **parks** for a
  live human, whose `/invocations` view shows **scheduled by `workstream:<id>` ·
  principal · grant · run** (FE-21/GOV-6). The step bracket edge is the existing
  **`agent-tool → agent-remote-session`** (derived: principal set → `agent-tool`) — the
  canonical seven-edge set is **unchanged** (this ADR fixes the *reading* of
  `agent-tool`, not the set: an LLM agent under a grant, or a chartered Workstream
  under its grant, are both non-human actors executing delegated authority).
- **Windowed budget + containment (WS-4, PERF-8).** The grant's `step_budget` gains a
  nullable `budget_window_seconds` (NULL = the v0.6 cumulative semantics unchanged);
  the one new abuse vector — a runaway schedule — is quadruple-bounded: an interval
  floor (60s production default) + per-grant concurrency ≤ the gateway in-flight cap +
  the windowed budget + the per-operator ceiling, with flagged resources still parking.
- **Lifecycle (WS-5, DUR-7/8).** Pause is **soft** (ticks stop, in-flight Runs
  complete, the grant stays active); resume re-arms `next_fire_at = now + interval`
  (no catch-up fire); **retire** is the kill switch — one transaction flips the charter
  terminal + revokes its grant, then the existing cascade cancels its in-flight Runs.
  A **dead grant self-retires** the charter on the next tick (`workstream.retired:
  grant-dead`, actor = the Workstream) so a revoked/expired grant cannot spin eternal
  denials.

Event vocabulary added (AUD-18; content-free, ADR-0049): `workstream.chartered` /
`workstream.paused` / `workstream.resumed` / `workstream.retired` (actor = operator,
edge `human-agent`, target `workstream:<id>`) + the machine `workstream.retired:
grant-dead` (actor = the Workstream). A schedule-fired Run is distinguishable in the
chain by its `workstream:<id>` actor (vs an operator Run's operator actor and a v0.6
delegated Run's `agent:<id>` actor). **Offline (OI-14):** the ticker rides the
existing `GATEWAY_URL` on the installed api, migration 0011 rides `install.sh`'s
migrate loop, and the schedule floor is the 60s default — **no new unit, no new Caddy
route, no new env, no new key material** (the leanest install leg since the module
line began).

## Persistent Scoped Memory (v0.8.0; ADR-0064)

v0.8.0 is **not a fourth initiator** — it is the platform's first governance/
data-plane module: a durable, scoped, **forgettable** memory projection beside the
immutable audit chain (the chain cannot redact, expire, or permission-scope what it
records — by design; memory is where governed forgetting lives). It adds **no new
capability edge, no new plane, and zero gateway change**.

- **SEC-31 — the memory invariant (the release's named invariant).** Memory is
  **operator-plane only**: every verb (list / author / redact / pin) is a typed,
  CSRF-gated `human-agent` route on `services/api` (`/v1/memories*`, owner-scoped —
  a foreign or unknown id is a 404); **no agent path to memory exists** — no gateway
  tool (`tools/list` = `remote.exec` + `run.launch` only), no agent-audience route,
  no service-service route into the memory store; memory content **never** appears in
  any audit event (content-free tombstones, ADR-0049); authored content is
  **secret-class at rest** (sealed via the shared `CommandSealer`, operator-only
  read — the ADR-0059 persisted-command posture); and memory **feeds no execution**
  (no template, plan, policy input, or agent context reads from the store). Proven
  live by the WD-8 N2 negative on both stacks: a **valid** `agent-tool` Bearer is
  rejected 401 at `/v1/memories*` (compose e2e + installed wd1).
- **The `agent-memory` edge stays reserved-UNENFORCED — a recorded decision, not an
  omission.** The canonical seven-edge set has named `agent-memory` since v3.0.1;
  v0.8.0 deliberately does not light it up. The moment memory content feeds an
  agent's context it becomes a **cross-grant prompt-injection and secret-exfiltration
  channel** — so enforcing `agent-memory` requires its own consent + containment ADR,
  and it shares that trigger with adaptive/agent-composed Workstreams (they land
  together or not at all). The edge set is unchanged. *(Superseded at v0.9.0: that
  ADR now exists — ADR-0065 — and the edge is enforced on exactly one read-only path
  under its containment model; see the v0.9.0 section below. The v0.8 posture above
  stands as the historical record of why it waited.)*
- **Accrual is attributed, not anonymous.** The core write path is machine accrual:
  a terminal Run (operator-direct, agent-delegated, or workstream-fired; completed,
  failed, or canceled alike) writes ONE **content-free evidence pointer** in the same
  transaction as its once-only terminal flip — so a no-op flip can never
  double-accrue, and the pointer commits or rolls back with the Run's terminal event.
  The `memory.written` event carries the Run's actor (`workstream:<id>` / the agent /
  the operator) with the operator as `permission_scope` owner.

Event vocabulary added (AUD-19; content-free, ADR-0049): `memory.written` (accrual —
actor = the Run's actor; authored — actor = operator), `memory.redacted` (the
tombstone flip; exactly once — a re-redact is a silent no-op), `memory.expired` (the
sweep; actor = the row's owner, whose retention instruction fired), `memory.pinned` /
`memory.unpinned` (operator verbs). Reads are not individually chained at v0.8 (the
ADR-0059 read-path precedent; per-read audit is the named multi-operator escalation
lever). **Offline (OI-15):** migration 0012 rides `install.sh`'s migrate loop and the
expiry sweep rides the api reaper goroutine that starts unconditionally — **the first
release with zero install-wiring diff of any kind** (no new unit, route, env, or key;
`AIREOS_MEMORY_SWEEP_SECONDS` is an optional e2e-only override, not written by the
installer).

## Adaptive Workstreams — the lit `agent-memory` read path (v0.9.0; ADR-0065)

v0.9.0 discharges the fused trigger the section above recorded: ADR-0065 is the
consent + containment model, and the reserved `agent-memory` edge is **enforced on
exactly one path** — read-only, through the gateway `memory.read` tool (see the
enforcement table). The edge *set* is unchanged (ADR-0046 preserved); the operator
memory surface of v0.8 is **byte-identical** (a separate store method serves the
agent plane — WD-8 stays green as the proof). The plan author is the **customer's
external agent, inbound over the ADR-0056 MCP surface**: zero model dependency, zero
new egress, the L3 hard gate untouched.

- **SEC-32 — the adaptive invariant (the release's named invariant).** Agent memory
  access is **read-only** (accrual on the terminal flip remains the sole machine
  write); the readable set derives from **the caller's own live grants by
  construction** (its charter's `workstream:<id>` scope + that grant's Runs — never
  `operator` scope, never a foreign charter; an out-of-set filter answers EMPTY, not
  an error — no oracle); **tombstoned rows are excluded** (forgetting means gone
  from the feeding surface); operator-authored content is **share-gated**
  (`agent_visible`, default false — the audited `memory.shared`/`memory.unshared`
  flip pair; a tombstoned row cannot be shared); **every agent-plane read is chained
  fail-loud** (`memory.read`, one content-free event per call, edge `agent-memory`,
  enqueued inside the read's transaction — no event, no data; the ADR-0064 §8
  escalation fired for the non-owner plane only, operator reads stay unchained); and
  **every agent-composed plan parks** as a `proposed` Run for informed full-plan
  review before anything executes (unattended-adaptive does not exist at v0.9, not
  even as configuration). Proven live by WD-9's negatives on both stacks.
- **The consent-successor invariant.** The v0.7 immutable template bound consent to
  the reviewed artifact; for an **adaptive** charter the reviewed artifact is **each
  tick's composed plan** — its full step list (resource + verbatim command, unsealed
  for the owner ONLY while the Run is `proposed`; the ADR-0059 operator-only
  posture, deliberately inverted from "the operator submitted it" to "the operator
  has never seen it") on the approve/deny surface. The charter's single consent act
  additionally names **who composes**: the composer agent is the auto-issued grant's
  grantee, so `plan.submit` is subject-pinned to it.
- **Revocation reaches everything** (DUR-10): a revoked/expired grant closes its
  open plan request (atomically with retire/self-retire), refuses poll/submit/read,
  and its `proposed` Run is cascade-canceled with `run.canceled` — **never
  `run.denied`**, which stays exclusively the human review verdict.

Event vocabulary added (AUD-20; content-free, ADR-0049): `plan.requested` /
`plan.expired` (the request's transaction; actor = the charter — the machine
transition), `plan.submitted` (in-tx with the request flip; actor = the composer),
`memory.read` (agent plane only; Target = `memory:<comma-joined row ids>`),
`memory.shared` / `memory.unshared` (operator flips), `run.denied` (beside the
terminal event; the deferred `run.launched` chains at approve, not at submit).
**Offline (OI-16):** migration 0013 rides the migrate loop; the three tools ride the
already-installed gateway binary + `/mcp` route — **zero install-wiring diff again**
(no new unit, route, env, or key).

## Layer 1 Live/Context Index — the lit `agent-workspace` read path (v0.10.0; ADR-0066)

v0.10.0 lights the **second** reserved edge: `agent-workspace`, enforced on
exactly one path, `context.search` (see the enforcement table above). The edge
*set* is unchanged (ADR-0046 preserved); `agent-repo` **stays reserved** — held
for Git Intelligence (REQ-015: the issue/PR/CI/code graph), a deliberately
distinct module from the docs/runbooks/repo/tickets project-workspace corpus
`agent-workspace` serves. The seam is `services/api/internal/ctxindex` (the ADR
and the release criteria say `internal/context` in prose; see the module map for
the as-built rename note). The plan author remains the customer's external agent,
inbound over the ADR-0056 MCP surface — zero model dependency, zero new egress,
the L3 hard gate untouched.

- **SEC-33 — the context-index invariant (the release's named invariant).** Agent
  context-index access is **read-only** (indexing is a machine sweep of
  operator-registered sources — no agent write path exists); the readable set
  derives from **the caller's own live grants by construction** (its grant's
  charter → the context sources bound to that charter → FTS over their chunks —
  never a caller-supplied path, never a foreign charter); an out-of-set
  `workstreamId` answers **empty, never an error** (no oracle — same posture as
  `agent-memory`'s cross-grant containment); the indexer is **confined to a
  registered source root** (no path traversal, no symlink-escape, no
  out-of-root read — the confined-root guard is a live, tested negative, N2);
  **every agent-plane read is chained fail-loud** (`context.read`, content-free,
  enqueued inside the read's own transaction — no event, no data); results are
  clamped 50/200 and ride the gateway's per-agent in-flight cap; and **every
  agent-composed plan still parks** as a `proposed` Run before any step executes
  (the ADR-0065 always-park invariant is L1's injection control — indexed
  content is untrusted, exactly like shared operator memory). The operator
  `/v1/memories*` and `/v1/context-sources*` surfaces are **separate, untouched**
  route sets from the agent read plane. Proven live by WD-10's N1/N2/N4/N6
  negatives on both stacks.
- **The confined-root guard (SEC-33, N2).** The context source's file reader
  resolves every candidate path against the registered root and rejects any
  result (via `../` traversal or a symlink target) that escapes it — indexing
  and reading both refuse an out-of-root file; the source is marked errored
  rather than the sweep crashing.
- **GOV-11 — the context-source consent surface.** Registration copy states
  plainly that a bound source's content becomes readable by this charter's
  composer agent (naming the prompt-injection risk honestly) and that removal
  stops future reads but cannot recall past ones.

Event vocabulary added (**AUD-21**; content-free, ADR-0049): **`context.read`**
(agent plane only, one per `context.search` call, edge `agent-workspace`, Target
= `context:<comma-joined chunk ids>` — never source content) and the operator
`human-agent`-edge pair **`context.source.registered`** / **`context.source.removed`**
(one per register/remove, content-free — the root path never appears in the
event; re-removing an already-removed source is a silent no-op, no duplicate
event). Operator context-source reads (if any admin preview exists) stay
unchained, matching the ADR-0064 §8 posture. **Offline (OI-17):** migrations
0014+0015 ride the migrate loop; the indexer rides the already-running api
goroutine; `context.search` rides the already-installed gateway binary + Caddy
`/mcp` route — **zero install-wiring diff again** (no new unit, route, env, or
key; `AIREOS_CONTEXT_INDEX_SECONDS` is an optional e2e-only override, not
written by the installer).

## Paired operator sessions — a scope within `human-agent`, not an eighth edge (v0.12.0; ADR-0068)

v0.12.0 ships the product's **first revocable session**: an operator pairs a phone (or
any second browser) with a QR code and drives the approval surface from it. This adds
**no capability edge**. `packages/go/auth`'s `PairedTTL` is a fifth **token tier**
alongside the four ADR-0046 fixed (agent 5min / operator 8h / service 1h / lease =
lease expiry) — the one declared divergence from an Accepted ADR this release makes
(ADR-0068, filed as a new ADR; **ADR-0046 is not edited**, its seven-edge set, its
tiered-TTL model, and its single-bootstrap-operator posture all stand). Read literally
against the enforcement table above: the paired session travels the existing
`human-agent` edge end to end (`services/api/internal/server`, operator cookie + CSRF,
the same SPA) — it is **narrower**, not different. **The canonical seven-edge set is
unchanged.**

- **The scope: `approvals@1`.** A paired token's `Scope.Name` is the versioned string
  `approvals@1` (`packages/go/auth`'s `Capability.Scope`). Membership is
  **single-sourced in one place in `services/api`** — `services/api/internal/pairing`
  (the `ApprovalsScopeV1` constant and its doc comment enumerating what the scope
  permits) plus `services/api/internal/server/pairing.go`'s `pairingRouteActions` map,
  which is the **only** place a route is mapped onto an `approvals@1` action and
  therefore the sole enforcement chokepoint (SEC-35/SEC-36) — not the frontend, not
  the database, not a migration. `approvals@1` **permits**: read pending
  approvals/invocations, approve/deny, read the audit surface, read its own session.
  It **denies**: launching a Run, issuing or revoking a delegation grant, registering
  or removing a context source, minting an agent token, opening a terminal session,
  and creating a further pairing. `TestPairingRouteWalkDenyByDefault` drives a live
  paired session against every unmapped route and asserts the refusal — the capability
  claim is proven by what it refuses (SEC-21's precedent). Widening `approvals@1` is a
  criteria change and therefore requires a new ADR (ADR-0068 §D5).
- **Liveness is checked per request, not at mint time (SEC-34).** A paired session's
  `paired_sessions` row is read on every request carrying a paired-scope token — a
  revoked or expired session is refused live, not only once its token would otherwise
  expire. A desktop token takes no extra work at all: the liveness check is keyed on
  `Scope.Name == "approvals@1"`, so an unscoped operator token skips it entirely
  (PERF-12).
- **No sliding refresh on the paired plane (SEC-39).** The paired session expires hard
  at `PairedTTL = 8h` and must be re-paired; ADR-0047's server-side cookie rotation
  for the desktop plane is unchanged and does not apply here.
- **The paired session cannot reach another service (SEC-38).** It exists on the
  `services/api` operator plane only. `packages/go/middleware`'s `RequireCapability`
  gained no database dependency, and `session-broker` / `policy-service` /
  `connector-service` / `audit-service` / `mcp-gateway` are byte-unchanged by this
  release.
- **Off by default (SEC-41).** `AIREOS_PAIRING_ENABLED` defaults to `0`; `install.sh`
  writes it off. With pairing disabled the four pairing routes answer a plain `404` —
  byte-identical to chi's own unmatched-route 404, no oracle distinguishing
  "disabled" from "doesn't exist."
- **Audit provenance, disclosed.** A paired-session approval chains `approval.granted`
  (or `approval.denied:operator`) with actor `operator:<id>#paired` — the only
  mechanism available that satisfies both SEC-38 (policy-service/audit-service stay
  byte-unchanged) and AUD-34 (a paired approval must be attributable as such).
  **Known consequence:** `audit-service`'s actor filter is exact-match, so filtering
  for `operator:<id>` does not return that operator's paired-device approvals — a
  real fork in the audit actor, disclosed at review (T-114b) and carried as a v1.x
  residual (`docs/requirements/backlog_v4.csv` B-034: provenance as a structured
  field, not an actor-string suffix).

Event vocabulary added (AUD-32/33; content-free, ADR-0049): `pairing.requested` /
`pairing.redeemed` / `pairing.revoked`, each chained inside
`services/api/internal/pairing/store.go`'s own transaction for the state change it
records (actor = the operator, edge `human-agent`, target `pairing:<id>` /
`paired-session:<id>`). There is deliberately **no** `pairing.expired` event and no
expiry-sweep event (AUD-33) — conforming to ADR-0067 §D3's lifecycle-noise finding,
the same discipline that shaped `governed-actions@1`.
