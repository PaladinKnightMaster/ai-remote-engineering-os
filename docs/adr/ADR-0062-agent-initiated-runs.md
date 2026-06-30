# ADR-0062: Agent-initiated Runs — operator-issued delegation grants as the agent's bounded standing authority

## Status

**Accepted** — 2026-06-30 (v0.6 opening ADR consult + a CEO-requested self-audit that
reversed one decision and corrected one edge; the three §Ratification points signed off on
the recommendations). Author/decider: CEO (war-room). Binding spec for the v0.6
agent-initiated-Run ticket ladder; code is authored just-in-time per ticket from here.

**Builds on / conforms to:** ADR-0046 (in-process PASETO capability tokens; the typed
`CapabilityToken` + the seven capability edges + `service-service`), ADR-0054 (policy /
approval decision point), ADR-0057 (gateway-owned invocation + invocation-anchored
governance + operator approval proxied through `services/api`), ADR-0058 (broker
non-interactive exec = the sole SSH egress), ADR-0059 (informed approval), ADR-0060
(per-workload asymmetric keys + the keyless agent-facing gateway, SEC-20/25), ADR-0061
(Governed Runs — the operator-initiated Run engine in `services/api`).

**No ADR divergence (G2-clean):** it **extends** ADR-0061 (a second initiator for the
same Run engine) and **extends** ADR-0046 (a new server-side authority primitive *beside*
capability tokens — it does not change `CapabilityToken`, the edge set, or the
token-revocation posture), and it **preserves** ADR-0060 (no new minting workload; the
agent-facing surface stays on the keyless gateway). It edits no Accepted ADR.

Companion spec: `docs/architecture/workstream-engine-architecture.md` §11.

## Context

**Roadmap nesting (ADR-0061 §Context).** The Run roadmap is **v0.5 operator-driven Runs →
v0.6 agent-driven Runs → P1 Workstreams orchestrate Runs.** ADR-0061 §Deferred explicitly
named *"agent-initiated Runs + a delegation identity model — v0.6; the trigger to extract a
standalone `services/workstream-engine`."* This ADR discharges that item — but **defers the
extraction** (decision locked at scoping): agent-initiation rides the existing `api`
`internal/run` seam; the extraction waits for a real second consumer.

**The privilege delta — why this is not "flip the initiator."** v0.5.0 dissolved the v0.5
red-team's R-1/R-2 by making the initiator a **human**: the operator's authenticated
cookie identity ([`runs.go` `CreateRun`](../../services/api/internal/server/runs.go))
*is* the authority, and **informed launch** — the operator reviewing the full plan before
launch (ADR-0061 §4) — *is* the authorization for unattended execution. Agent-initiation
removes **both**:

1. An agent's `agent-tool` token is **SEC-16-scoped to one resource + one verb**
   ([`catalog.go:228`](../../services/mcp-gateway/internal/catalog/catalog.go) —
   `capb.Scope.Resource != "" && != resourceID → reject`), enforced **per-call, on the
   agent plane only**. An agent therefore **cannot author a multi-step, multi-resource
   plan** inside one token, and widening that scope *is* privilege escalation.
2. There is **no human present at launch** — so informed-launch authorization is absent.

So the load-bearing unknown is exactly **the agent-delegation identity model + the
token-minting risk** that ADR-0061 §Initiator named and scoped out of v0.5.0.

**As-built constraint — the conflated `Subject` (the primary correctness surface).** The
gateway's operator-Run contract carries a **single** `Subject` field
([`transport.go:83`](../../services/mcp-gateway/internal/mcp/transport.go)) that today does
four jobs at once: the policy subject, the invocation's recorded subject + per-initiator
concurrency bucket ([`catalog.go:336` `runGovernedStep`](../../services/mcp-gateway/internal/catalog/catalog.go)),
and — via the hardcoded bracket edge — the audit actor. v0.5 could conflate **actor** and
**principal** because the operator was both. The `/v1/steps` route trusts that field
free-form, safely, for **one reason only**: it is `onlyAPI`-pinned
([`server.go:80`](../../services/mcp-gateway/internal/server/server.go)) and `api` sets it
from the authenticated operator cookie — never anything client-supplied. **Agent-initiation
breaks the conflation** (actor = agent ≠ principal = operator), so the split is the
load-bearing design act of this ADR.

**As-built constraint — the keyless agent-facing gateway (ADR-0060/SEC-20).** `services/api`
holds the symmetric cluster key (ADR-0046 §custody) and is the asymmetric agent-token
signer; the **gateway is keyless of the symmetric key** and only *verifies* agent tokens
([`asymmetric.go`](../../packages/go/auth/asymmetric.go)). ADR-0060's invariant is that the
**agent-facing** workload must not hold the symmetric key (an RCE there must not be able to
mint connector leases = decrypt every credential). Any v0.6 design that newly exposes the
**key-holding** `api` to an agent would silently undo SEC-20 — this constraint decides §2.3.

### Standards lineage (this is a localized industry pattern, not a bespoke invention)

The model is a deliberate composition of established authorization standards, specialized
for the offline-first / self-hosted / privacy constraints (ADR-0001):

- **Delegation with a distinct actor — OAuth 2.0 Token Exchange (RFC 8693).** The
  actor/principal split (§2.4) is RFC 8693's `act` (current actor) over `subject` (the
  delegated principal), and its *delegation vs impersonation* distinction: the agent acts
  **as itself, on behalf of** the operator — it does not impersonate the operator. We
  express it in PASETO + a server-side grant rather than a JWT `act` claim, but the shape
  is the standard's.
- **Bounded, time-limited, scoped-down delegation — cloud IAM.** AWS STS `AssumeRole` +
  session policies and GCP service-account impersonation + downscoped tokens (Credential
  Access Boundaries) are the mainstream "a principal lends a bounded, expiring slice of its
  authority to another identity" pattern. The grant (§2.1) is that, single-cluster.
- **Capability security / least authority.** The grant is an object-capability under the
  Principle of Least Authority (POLA) — bounded by resources/verbs/TTL/budget, the
  attenuation idea behind macaroons (Birgisson et al.) and biscuit tokens.
- **Server-side authority for instant revocation — OAuth Token Introspection (RFC 7662) /
  reference tokens.** Choosing a revocable **record** over a stateless bearer (§2.1) is the
  recognized fork you take when *instant revocation and stateful budget* outweigh
  statelessness. RFC 7662 introspection is the standard's name for it.

**The offline-first specialization.** Where we diverge from the stateless-token default, it
is *toward* the vision: a stateless grant token would need either short TTL + key rotation
(too coarse for standing agent autonomy) or an **external introspection/authz server** to
revoke — a SaaS/daemon dependency that violates offline-first (ADR-0001/0046). A local
Postgres record gives instant revocation + budget with **zero external dependency**. The
"less standard" choice is the more offline-native one. This mirrors ADR-0046's own "no IdP
daemon; in-process auth" calculus.

## Decision

### 1. The delegation grant — a new operator-issued, server-side, revocable authority primitive

An operator issues a **delegation grant**: a durable record in `services/api`
(proposed table `delegation_grants`) that pre-authorizes **one agent** to launch governed
Runs against **a bounded set of resources + verbs**, within bounds, until it expires or is
revoked. The grant **replaces informed-launch** as the human authorization for unattended
execution — the consent moves **from per-plan (v0.5) to per-grant (v0.6)**; the agent then
composes plans *within* the grant without per-plan human review (the autonomy payoff),
while flagged steps still park (§5).

Proposed schema:

| Field | Purpose |
|---|---|
| `id` | the grant reference the agent names at launch |
| `principal` | the delegating operator — the lent authority (RFC 8693 `subject`) |
| `agent` | the grantee subject — bound to the agent's token subject at launch |
| `resources[]` | resource ids the agent may target (plan-vs-grant, §6) |
| `verbs[]` | operations allowed, e.g. `["exec"]` (mirrors `Scope.Operations`) |
| `expires_at` | TTL |
| `max_steps_per_run`, `max_concurrent_runs` | bounds (§8) |
| `step_budget` | total steps across all runs under the grant — stateful containment (§8) |
| `status` | `active` / `revoked` / `expired` — the basis for **instant revocation** (§7) |

**Custody = server-side record, not a bearer token** (RFC 7662 lineage). The grant is
**never** a credential the agent holds; the agent references it by id. This (a) keeps the
agent holding nothing mintable — preserving ADR-0060/SEC-20 — (b) enables **instant
revocation** (ADR-0046 defers per-token revocation lists, so a token grant could not be
revoked before its TTL), and (c) tracks **stateful budget/concurrency**, which a stateless
token cannot.

### 2. The agent never mints; `api` / gateway remain the sole authority

No new token-minting workload, no new key, no new credential custody. The agent
authenticates with its **existing** `agent-tool` capability token (asymmetric, ADR-0060);
the **grant** supplies the launch authority; **`api` is the sole validator and Run driver.**
This preserves the v0.4.0 keyless-gateway posture (SEC-20) by construction.

### 3. The agent launches through the **keyless gateway**, not through `api` (SEC-20-preserving)

> **Self-audit reversal (recorded).** The opening consult recommended the agent submit to a
> new **`api`** endpoint with an `agent-tool` token `aud=api`. Grounding against ADR-0060
> showed this would place the **first agent-reachable route onto the symmetric-key-holding
> `api`** — reintroducing exactly the SEC-20 posture ADR-0060 stripped from the gateway. The
> decision was reversed.

- The agent launches a delegated Run via a **new gateway MCP tool, `run.launch`** (named
  `run.*`, not `workstream.*` — ADR-0061 naming discipline reserves "Workstream" for the P1
  module), using its **existing `aud=mcp-gateway` token** — no new credential, no new
  `aud=api` provisioning flow. Tool args: the grant id + the ordered plan.
- The gateway **does not drive the Run.** It forwards `{agent-subject (from the verified
  token), grant-id, plan}` to a **new internal `api` route** over a **gateway-pinned
  service-service edge** (`subject=="mcp-gateway"`, the mirror of `/v1/steps`'s `onlyAPI`
  pin). The agent-facing surface stays on the **already-keyless** gateway; `api` stays
  non-agent-reachable (an attacker must breach the keyless gateway *first*, then pivot
  over a pinned edge).
- `api` validates the plan against the grant (§6), creates the Run `pending`, and the
  **existing sequencer drives it through the unchanged `/v1/steps` path** (ADR-0061). The
  gateway is touched only for the new tool + the forward; per ADR-0061 §2 / the
  gateway-reviewer mandate this lands as a **behavior-preserving split, agent `task e2e`
  green first.**

### 4. Actor/principal split — the audit bracket (RFC 8693)

The conflated `Subject` (Context) splits into **actor = the agent** and **principal = the
operator (from the grant)**, propagated end to end:

- **Grant issuance** = the human-consent act: `human-agent` edge, audited `grant.issued`
  (this is where the operator's consent to unattended execution now lives).
- **`run.launch`** records **actor = agent**, **principal = operator**, **`grant = <id>`**;
  `run.launched` carries all three (the `api` `runs` table gains nullable `grant_id` +
  `actor` columns; a null `grant_id`/`actor` = a v0.5 operator-direct Run — additive,
  back-compatible). The store's hardcoded `Actor: in.Operator`
  ([`store.go:111`](../../services/api/internal/run/store.go)) generalizes to actor=agent /
  principal=operator for the delegated path.
- **Per step**, the gateway `/v1/steps` contract (`mcp.StepRequest`) grows from one
  `Subject` to **`{actor, principal}`** (the one honest gateway contract change); `api`
  binds **both** server-side from the grant, so the gateway never trusts an agent for
  either. The pin (`onlyAPI`) and the v0.4 subject-pins (PIN-1 broker, PIN-2 connector) are
  **untouched** (SEC-25). The principal is recorded on the invocation + its `tool.invoked`/
  `tool.result` audit events.

**Bracket edge = `agent-tool → agent-remote-session`, with `principal` + `grant_id`
recorded** — **corrected** from the consult's `agent-agent`:

> **Self-audit correction (recorded).** Grounding `docs/security/agent-permission-model.md`
> showed `agent-agent` denotes **agent-to-agent** interaction; our delegation has **no
> second agent** (operator → agent → tool). Encoding `agent-agent` would both misname the
> edge **and** force a revision of the canonical seven-edge set — an explicit ADR-0046
> review trigger. The actor is an agent invoking a tool, so the honest, minimal-blast-radius
> bracket is the existing **`agent-tool`** (→ the tool's own `agent-remote-session`), with
> the **delegation captured by the recorded `principal` + `grant_id`** (not by a new edge).
> A delegated step is distinguished from a direct agent call by the presence of the
> `run:<id>` anchor + `principal` + `grant_id`. This keeps the capability-edge model
> (ADR-0046) unchanged.

### 5. Policy subject = the agent/actor; flagged steps still park

`policy.Decide` receives the **agent (actor)** as subject — **not** the operator. This is
deliberate and security-critical: it prevents ADR-0054's same-operator self-approve posture
from auto-resolving a flagged step under the **absent** operator's identity. Approval gating
is driven by the **resource label** (`RequiresApproval`,
[`catalog.go:347`](../../services/mcp-gateway/internal/catalog/catalog.go)), not by
subject-specific policy, so an agent subject neither self-allows nor needs a policy entry.
**Ungated steps run unattended** (the autonomy payoff); **flagged steps park** on the
existing `/invocations` console for a **live human** — reusing v0.5.0 wholesale.

### 6. Plan-vs-grant validation in `api` — the relocated, generalized SEC-16

Because the gateway no longer sees an agent token on the delegated path (`api` drives via
its service token), the **SEC-16 scope check moves to `api`** at launch — and generalizes
from one-resource/one-verb to the grant's bounded set. The grant **is the multi-step,
operator-issued, revocable generalization of the agent-tool token's SEC-16 scope.** `api`
checks, server-side: `agent-token.subject == grant.agent` ∧ `grant.status == active` ∧
`now < expires_at` ∧ `∀ step: resourceId ∈ resources ∧ verb ∈ verbs` ∧
`len(steps) ≤ max_steps_per_run` ∧ `active-runs-under-grant < max_concurrent_runs` ∧
`step_budget ≥ len(steps)`. A rejected plan = **deny + audit** (an agent probing its bounds
is visible). Everything downstream — sequencer, `/v1/steps`, policy / connector / broker,
the dual-anchor chain — is **reused unchanged**.

### 7. Revocation is instant — and cascades to in-flight Runs

> **Self-audit fix (recorded).** "Instant revocation" must define its effect on Runs already
> mid-flight; the sequencer's between-step check reads `RunStatus` only, not grant status
> ([`sequencer.go:103`](../../services/api/internal/run/sequencer.go)).

Revoking a grant (`status → revoked`) **cascades**: it also **cancels the grant's active
Runs** via the existing `CancelRun` path, whose `run.canceled` + the sequencer's
between-step `RunStatus` stop halt further dispatch. Revocation therefore blocks **both**
new launches **and** in-flight Runs — not just new launches. An in-flight (e.g. parked) step
is finalized per the running-orphan policy (ADR-0061 §6), not force-killed.

### 8. Bounds + the security forcing-function

The grant **bounds and audits** what an agent can autonomously drive: resources, verbs, TTL,
`max_steps_per_run`, `max_concurrent_runs`, and a `step_budget`. A compromised agent is
capped at its live grants' bounds + the still-live per-step approval on flagged resources —
**never the operator's full authority** (the project's "ship the capability *with* its
containment" pattern, like SEC-20's keyless gateway).

**Reconciliation (criteria):** `grant.max_concurrent_runs` must be reconciled with the
gateway's per-initiator in-flight cap (PERF-3, `maxInFlight` default 4,
[`catalog.go:342`](../../services/mcp-gateway/internal/catalog/catalog.go)). With actor =
agent, all of an agent's in-flight steps bucket under one subject; a grant allowing more
concurrent Runs than the cap would cause the gateway to refuse a step, which the sequencer
reads as a governance refusal and **fails the Run**. The cap and the grant bound must agree
(and whether a *parked* step counts as in-flight — a latent v0.5 operator-side edge — is
verified in the ladder).

### 9. Issuance surface

`POST /v1/grants` (issue), `POST /v1/grants/{id}/revoke` (revoke + cascade, §7), `GET
/v1/grants` (operator-scoped list) on `services/api` — all `human-agent` + double-submit
CSRF, audited `grant.issued` / `grant.revoked`. An operator may only delegate authority it
holds; at the current single-bootstrap-operator posture (ADR-0046) that is unbounded, and
narrows automatically if/when multi-operator lands.

### 10. Informed approval of a delegated step

A flagged delegated step parks on the existing `/invocations` console (§5). Its view
surfaces the **delegated context** — the agent actor + the grant id + the principal — beside
the verbatim command, so the approving human knows an **agent** is asking under operator
*X*'s grant (extends FE-16's Run-context line; the operator-only command posture, ADR-0059,
is unchanged).

## Consequences

- The platform gains its **first agent-initiated governed consumer** — autonomous,
  multi-step, bounded, revocable, audited — **with no new egress path, no new credential
  custody, no new key, and no new token-minting workload.** The keyless agent-facing gateway
  (SEC-20) and the broker/connector subject-pins (SEC-25) are preserved by construction.
- Honest, bounded new work: a new `delegation_grants` table + issuance routes in `api`; a
  new `run.launch` gateway MCP tool + a gateway→`api` pinned forward; the actor/principal
  split across `/v1/steps`, the gateway invocation, and the `api` `runs` rows; plan-vs-grant
  validation in `api`. Everything downstream is reused unchanged.
- Authorization granularity changes honestly: consent moves from **per-command**
  (informed-launch reviews every command) to **per-resource/verb** (the grant). That
  coarsening **is** the autonomy/risk trade — see Red-team #7. It is contained by the grant
  bounds + flagged-resource parking, and is *more* rigorous than the typical agent-framework
  status quo (which runs agents with the user's full ambient credentials).
- A new at-rest authority record (`delegation_grants`) lives in `api`; it carries no
  command and no secret-class material (the command at-rest posture is unchanged — sealed
  gateway-side per F3/ADR-0059 once dispatched).
- The offline/installed leg stays light: an `api` migration + a gateway tool; **no new
  systemd unit, no new Caddy route** (the engine still rides `api`; the tool rides the
  gateway).

## Red-team (token-minting / privilege-escalation surface)

| # | Attack | Closed by |
|---|---|---|
| 1 | Agent mints/forges a token to widen scope | Agent never mints (§2); ADR-0060 keyless posture preserved; `api`/gateway sole signers |
| 2 | Agent presents a grant issued to a *different* agent | `api` binds `token.subject == grant.agent` (§6) |
| 3 | Agent submits a plan outside the grant | plan-vs-grant validation in `api` (§6); deny + audit |
| 4 | Agent impersonates the operator in audit/policy (the conflated-`Subject` hole) | actor/principal split (§4); `api` sets `principal` server-side from the grant; the gateway carries both, trusts neither from an agent |
| 5 | Flagged step auto-approves under the absent operator's identity | policy-subject = agent (§5), defeating ADR-0054 self-approve; flagged steps park for a live human |
| 6 | Leaked / over-broad grant | TTL + **instant, cascading revocation** (§7) + bounds + budget — the decisive reason the grant is a record, not a token |
| 7 | Confused-deputy: any command on a granted resource+verb, unattended | **Accepted, intentional residual** — the autonomy payoff. Contained by: the operator chose to grant that resource+verb; flagged resources still park; budget/TTL/concurrency cap blast radius |
| 8 | Agent-facing route on the key-holding workload (SEC-20 regression) | §3 — the agent-facing surface stays on the keyless gateway; `api` is reached only over a gateway-pinned service edge |
| 9 | Concurrency flood (PERF-3) | per-grant `max_concurrent_runs` + per-operator caps + gateway `CountInFlight` (bucketed on actor=agent), reconciled per §8 |

**Verdict.** No exploitable token-minting / escalation path **given** (a) the grant is a
revocable server-side record, (b) the actor/principal split is enforced with policy-subject
= agent, (c) plan-vs-grant validation lives in `api`, (d) flagged steps still park, and (e)
the agent-facing surface stays on the keyless gateway. The single accepted residual (#7) is
the deliberate capability, contained by the grant.

## Deferred (recorded)

- **Plan-level grant-with-preview beyond the grant** (approving a whole specific plan incl.
  its flagged steps up front) — a later increment **on top of** the grant.
- **The `internal/run` extraction** into `services/workstream-engine` — deferred until a
  real second consumer forces it (the seam is lift-and-shift; ADR-0061 §1).
- **Fine-grained (per-command / contextual) grant policy** — a Cedar/OPA-style
  authz-as-code layer over `policy-service` for richer grant expression; the grant stays
  resource+verb-coarse for v0.6.
- **Segregation of duties** (who may issue a grant vs who may approve its flagged steps) —
  inherits ADR-0054/0057's deferral; narrows when multi-operator lands.
- **The gateway-side exec worker**, parallel/branch/retry DAG, SSH pooling / full
  rate-limiting, persisted-command GC — unchanged ADR-0061 deferrals.

## Relation to prior ADRs

- **ADR-0061 (Governed Runs)** — this ADR adds a **second initiator** (agent, via a grant)
  to the same Run engine; reuses `runs`/`run_steps`/sequencer/`/v1/steps`/the dual-anchor
  audit unchanged; discharges ADR-0061 §Deferred's named v0.6 item. Extends, does not edit.
- **ADR-0046 (capability tokens)** — adds the delegation grant as a **server-side authority
  primitive beside** capability tokens; `CapabilityToken`, the seven-edge set, and the
  token-revocation posture are unchanged. The agent→gateway auth still uses the existing
  `agent-tool` token. Extends, does not edit.
- **ADR-0060 (keyless agent-facing gateway)** — preserved by construction: no new minting
  workload; the agent-facing surface stays on the keyless gateway; the subject-pins are
  untouched (SEC-20/25).
- **ADR-0054/0057/0058/0059** — reused unchanged (policy decision, invocation governance,
  sole SSH egress, informed approval).

## Ratification (resolved 2026-06-30)

All three open points were signed off on the recommendation, and are now binding:

1. **§4 bracket edge = `agent-tool → agent-remote-session` + recorded `principal`/`grant_id`**
   (the self-audit's correction over `agent-agent`) — **ratified.**
2. **§3 launch tool name = `run.launch`** (`run.*`, not `workstream.*` — ADR-0061 naming
   discipline) — **ratified.**
3. **§8 `step_budget` is in v0.6 scope** (a simple per-grant total-step budget) —
   **ratified.**

Everything else = the CEO-ratified slate (D1 / D3 / D4-core / D5–D8 + the §3 keyless-surface
reversal and the §7 revocation-cascade fix from the self-audit).
