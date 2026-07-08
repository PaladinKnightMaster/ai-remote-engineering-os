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
| `human-agent` | Operator login/session/CSRF; the approve/deny verb on a parked session **and on a parked tool invocation**; **launch/cancel a governed Run (v0.5.0)**; **issue/revoke a delegation grant (v0.6.0)**; **charter/pause/resume/retire a Workstream (v0.7.0)** | `services/api/internal/server` (`CreateSession`, `ResolveSession`, `ResolveInvocation`, `CreateRun`, `CancelRun`, `createGrant`, `revokeGrant`, `CreateWorkstream`, `PauseWorkstream`, `ResumeWorkstream`, `RetireWorkstream`) |
| `agent-tool` | An agent's MCP `tools/call` on the gateway (the `remote.exec` tool) — scoped (SEC-16) to the agent token's resource; drives a governed invocation | `services/mcp-gateway/internal/server` (`/mcp`; audience `mcp-gateway`) |
| `agent-tool` (**`run.launch`**, v0.6.0) | An agent launches a grant-bounded Run — a **control-plane** tool (no single-resource target, so the per-call SEC-16 resource check is N/A; the api-validated **grant** is the scope, SEC-29); mints no egress; idempotency-keyed (a retry re-attaches, never double-launches) | `services/mcp-gateway/internal/catalog` (`run.launch` branch before the target resolver) |
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
