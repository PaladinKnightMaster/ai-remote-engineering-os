# ADR-0063: Chartered Workstreams — schedule-fired, grant-bounded Runs (the standing objective that creates Runs)

## Status

**Accepted** — 2026-07-05 (v0.7 scoping consult + a code-grounded self-audit that surfaced
the windowed-budget gap and mandated the shared-launch-core split; the three §Ratification
points signed off on the recommendations). Author/decider: CEO (war-room). Binding spec for
the v0.7 chartered-Workstream ticket ladder; code is authored just-in-time per ticket from
here.

**Builds on / conforms to:** ADR-0046 (in-process PASETO capability tokens; the canonical
edge set), ADR-0054 (policy / approval decision point), ADR-0057 (invocation-anchored
governance), ADR-0059 (informed approval — the consent-binds-to-the-reviewed-artifact
doctrine), ADR-0060 (per-workload asymmetric keys + the keyless agent-facing gateway,
SEC-20/25), ADR-0061 (Governed Runs — the Run engine in `services/api`; reserved the name
"Workstream" for this module), ADR-0062 (agent-initiated Runs — the delegation grant, the
actor/principal split, the revocation cascade).

**No ADR divergence (G2-clean):** it **extends** ADR-0061 (a **third initiator** for the
same Run engine) and **extends** ADR-0062 (two additive grant generalizations: a non-agent
grantee subject and a windowed step budget — no v0.6 grant changes meaning), and it
**preserves** ADR-0060 **trivially** — no new key, no new minting workload, no new
agent-facing surface, and **zero gateway change** (the schedule-fired launch never crosses
the gateway; only `services/api` grows). It edits no Accepted ADR.

Companion spec: `docs/architecture/workstream-engine-architecture.md` §12.

## Context

**Roadmap nesting.** The Run roadmap is **v0.5 operator-driven Runs → v0.6 agent-driven
Runs → v0.7 the chartered Workstream that *creates* Runs.** ADR-0061 §Context defined the
chartered Workstream — "a long-lived objective with sources, rules, schedule, scoped memory,
and evidence-bound runs" — and deliberately reserved the name for the P1 module; ADR-0062
§3 enforced that reservation in tool naming (`run.launch`, not `workstream.*`). This ADR
builds the module. It is strategy pillar 4 ("Living Workstreams — recurring jobs with state
and memory", `docs/executive/strategy.md`) — the defensibility slice the v0.5/v0.6 machinery
was built to serve.

**The privilege delta of the third initiator.** Each release moved who is present at launch:
v0.5 — a human (the cookie identity is the authority; informed launch is the authorization);
v0.6 — an agent under a grant (the grant is the standing authorization; the agent is present
to compose the plan). v0.7 removes the launch-time party entirely: **a schedule fires with
nobody present** — no human, no agent. The required authority is therefore *standing,
bounded, revocable, and consented-to in advance* — which is **exactly the delegation grant**
(ADR-0062). The GOV-4 move (consent per-plan → per-grant) generalizes in time (consent
per-grant → per-charter); **no third authority model is created**, and that is the central
design decision of this ADR.

**As-built findings (the scoping self-audit, HEAD `8ba5bfe`).** Three findings shape the
decisions:

1. **The engine already generalizes.** The sequencer scans for and drives *any* pending Run
   ([`sequencer.go:56` `NextClaimable`](../../services/api/internal/run/sequencer.go)); the
   actor/principal threading is actor-agnostic (`actor != "" → subject=actor,
   principal=operator`, [`sequencer.go:119`](../../services/api/internal/run/sequencer.go));
   the gateway's bracket-edge derivation, per-actor concurrency bucket, and
   resource-label-driven approval gate all accept a novel actor subject unchanged; and
   `delegation_grants.agent` is a free-form subject string
   ([`grants.go:83`](../../services/api/internal/server/grants.go)). A Workstream initiator
   needs **no engine or gateway change at all** — it only needs to create Run rows.
2. **The enforcement gap.** Plan-vs-grant validation (SEC-29) + the PERF-7 bounds live only
   inside the gateway-pinned HTTP handler `LaunchDelegatedRun`
   ([`delegated.go:48`](../../services/api/internal/server/delegated.go)); an in-process
   caller cannot reuse them. Without a split, the Workstream tick would need a **second,
   diverging enforcement path** — the exact defect class this project's review gates exist
   to prevent. Hence §4.
3. **The budget gap.** `step_budget` is **cumulative-forever** — `CountStepsForGrant` counts
   total planned steps across *all* the grant's Runs, ever
   ([`store.go:200`](../../services/api/internal/run/store.go)), and issuance requires a
   finite budget > 0. Any finite budget deterministically exhausts under recurrence, so a
   standing charter riding a v0.6 grant as-is would brick. Hence §3.

**Standards lineage.** The execution shape is the mainstream *scheduled-job* pattern —
Kubernetes CronJob (an immutable job template + a schedule → Job instances) and CI scheduled
pipelines — while the authority side inherits ADR-0062's lineage wholesale (RFC 8693
delegation, RFC 7662 reference-token revocation, capability-security POLA): the charter adds
a **standing schedule** to a **standing authority**. Template immutability (§1) is the
ADR-0059/0061 informed-consent doctrine extended in time: the operator's consent binds to
the exact artifact they reviewed.

## Decision

### 1. The Workstream charter — a server-side record with an immutable plan template

A new sibling package `services/api/internal/workstream` (the `internal/grant` shape:
pgx pool + outbox writer, self-contained, extraction-ready) owning:

| Table | Fields (proposed) |
|---|---|
| `workstreams` | `id`, `principal` (the chartering operator/owner), `label`, `objective` (operator-facing text), `grant_id` (the bound authority, §2), `interval_seconds`, `next_fire_at`, `status` (`active` / `paused` / `retired`), timestamps |
| `workstream_steps` | `workstream_id`, `step_index`, `resource_id`, `command` (**sealed at rest** via the same nil-safe `CommandSealer` the Run store uses — F3 parity, [`store.go:48`](../../services/api/internal/run/store.go); the template is operator-authored command material) |

**The template is immutable post-charter.** The operator's consent (§2) binds to the
template they reviewed at charter time; changing the work = **retire + re-charter**. A
mutable template would let later edits ride earlier consent — the hole the informed-approval
ADR line exists to close. (Objective text/label may be cosmetic-editable; the template,
scope, and schedule are not.)

### 2. Charter authority = an auto-issued delegation grant (no third authority model)

Chartering **issues the Workstream's own delegation grant in the same transaction**, with
**grantee = `workstream:<id>`** (a free-form subject today — zero grant-schema change),
principal = the chartering operator, resources/verbs = the charter scope, TTL = the charter
TTL, plus the ADR-0062 bounds (`max_steps_per_run`, `max_concurrent_runs` ≤ the gateway cap,
windowed `step_budget` §3). `workstream.chartered` + `grant.issued` chain together — **one
typed, audited `human-agent` act is the whole consent**: objective + template + scope +
schedule + bounds, reviewed together.

Everything ADR-0062 built then applies wholesale and unmodified: plan-vs-grant validation,
concurrency + budget bounds, the per-operator ceiling, `run.launch.denied` deny-audit, and
the **revocation cascade** (§7). The charter↔grant relationship is **1:1** (the grant
exists for the charter and dies with it).

### 3. Windowed step budget — an additive grant generalization

`delegation_grants` gains a **nullable `budget_window_seconds`** column. When set,
`step_budget` counts only planned steps of Runs created **within the current window**; when
**NULL, the v0.6 cumulative semantics apply unchanged** (behavior-preserving — no existing
grant changes meaning; the additive-column pattern of the v0.6 `runs.grant_id`/`actor`
migration). Charter grants set it (default: the schedule interval, so the budget reads as
"steps per period"); agent-issued grants may leave it NULL. This closes the Context budget
gap without touching v0.6 behavior.

### 4. One enforcement path — the shared bounded-launch core

Extract plan-vs-grant (SEC-29) + the PERF-7 bounds checks + the idempotent `run.Create` from
the `LaunchDelegatedRun` handler into **one shared function** (working shape:
`run.LaunchBounded(ctx, grantSource, run.Input)` in `internal/run`, taking a small
grant-read interface — the T-063 seam pattern), called by **both** the gateway-forward HTTP
handler **and** the Workstream ticker. This is the release's behavior-preserving split, and
it follows the proven precedent (`runGovernedStep`, ADR-0061 §4): **it lands first and the
v0.6 WD-6 e2e stays green before the ticker rides on it.** Two callers, one validator —
never two validators.

### 5. The scheduler — an in-process ticker; fixed interval; single-winner claim; no backfill

- A third background goroutine in `api/main.go` (the reaper/sequencer pattern,
  [`main.go:109`](../../services/api/main.go)) — **no new systemd unit, no new Caddy route,
  no cron daemon**.
- **Schedule = `interval_seconds` + `next_fire_at`. No cron syntax at v0.7** — a cron parser
  is a new third-party dependency against the zero-new-dep doctrine (VER-17); cron
  expressions are the named reserve if operators need calendar semantics. A **minimum
  interval floor** is enforced at charter (concrete value at the criteria consult).
- **Fire = a single-winner, status-guarded claim** (`UPDATE … SET next_fire_at = next_fire_at
  + interval WHERE id = $1 AND status = 'active' AND next_fire_at <= now()` — the `ClaimRun`
  pattern): deployment is single-node (ADR-0051) but correctness must not rely on it.
- **A fired tick** instantiates the template and calls the shared launch core (§4) with
  launch key **`workstream:<id>:tick:<fired-at-unix>`** — riding the existing
  `(grant_id, launch_key)` dedup, so a tick is idempotent by construction. The **existing
  sequencer picks the Run up with zero changes.**
- **Missed ticks collapse to one** (no catch-up storm after downtime): the claim advances
  `next_fire_at` from *now*, not by replaying the gap.
- **A bounds-refused tick never wedges the charter:** a 429 from the launch core (budget /
  concurrency / ceiling) is audited (`run.launch.denied`, reused) and the charter simply
  fires again next interval.

### 6. Identity — actor = `workstream:<id>`, principal = the chartering operator

Rides the ADR-0062 actor/principal split **unchanged end to end**: the sequencer sets
subject = `workstream:<id>` / principal = the operator; `policy.Decide` receives the
Workstream as subject — so ADR-0054 same-operator self-approve stays defeated and **flagged
steps genuinely park** for a live human, whose `/invocations` view already renders the full
delegated context (actor · principal · grant · run — FE-18/GOV-6, reused verbatim); the
gateway's PERF-3 in-flight cap buckets per Workstream.

**Bracket edge = the existing `agent-tool → agent-remote-session`** (per the derivation,
[`catalog.go:400`](../../services/mcp-gateway/internal/catalog/catalog.go)). This ADR fixes
the permission-model reading: **"agent" on that edge means any non-human actor executing
under delegated authority** — an LLM agent under a grant (v0.6) or a chartered Workstream
under its grant (v0.7). No eighth edge (an ADR-0046 edge-set revision trigger), no gateway
change; a Workstream step is distinguished in the chain by its actor prefix
(`workstream:<id>`) + `principal` + `grant_id` + the `run:<id>` anchor — the exact ADR-0062
§4 mechanism.

### 7. Lifecycle — pause is soft; retire is the kill switch

- **Pause** (reversible): `status → paused`; ticks stop; **in-flight Runs complete**; the
  grant stays active. **Resume** re-arms `next_fire_at`.
- **Retire** (terminal): `status → retired` **+ revoke the bound grant** — the existing
  ADR-0062 §7 cascade cancels the charter's in-flight Runs, and plan-vs-grant's status check
  blocks any straggler tick. The kill switch is v0.6 machinery re-aimed, not new code.
- **Events** via the api outbox (content-free, ADR-0049): `workstream.chartered` /
  `workstream.paused` / `workstream.resumed` / `workstream.retired` (actor = the operator,
  `human-agent` edge), alongside the paired `grant.issued`/`grant.revoked`.
- **Run ↔ Workstream linkage = `runs.grant_id`** (charter↔grant is 1:1): no new `runs`
  column, no actor-string parsing.

### 8. Surface + scoped memory (the v0.7 slice of it)

- **Routes** (OpenAPI fragment + codegen; `human-agent` + CSRF, owner-scoped):
  `POST /v1/workstreams` (charter — the one consent act), `GET /v1/workstreams` (list),
  `GET /v1/workstreams/{id}` (detail), `POST /v1/workstreams/{id}/pause` / `…/resume` /
  `…/retire`.
- **UI**: a `/workstreams` route in `apps/web` — the charter form (objective + the FE-12
  resource picker + the Runs-compose template-step pattern + interval + bounds), a list
  (status / next fire / last Run), and a detail view whose **run history + evidence-packet
  links are the v0.7 scoped memory** — assembled at read time from existing durable state
  (the EVD-1 contract; no new blob, no index, no retrieval). The full Memory/Context Index
  module stays deferred; this release creates the entity that will *own* memory.

## Consequences

- The platform gains **recurring autonomous work** — the chartered, schedule-fired,
  grant-bounded, evidence-accruing Workstream — with **no new egress path, no new credential
  custody, no new key, no new minting workload, and zero gateway diff**. The strategy's
  "Living Workstreams" pillar becomes real product surface.
- Honest, bounded new work: one new api package + two tables + one additive grant column +
  the shared-launch-core split + a ticker goroutine + the `/v1/workstreams*` routes + the
  `/workstreams` UI. The engine, gateway, policy, connector, broker, and audit chain are
  reused unchanged.
- Consent granularity is unchanged from v0.6 (resource+verb grant + parked flagged steps);
  what changes is **time**: consent now covers future scheduled firings of a reviewed,
  immutable template. That is the product; it is contained by §5/§7/§8's bounds and the
  still-live human gate on flagged resources.
- The offline/installed leg stays light (the OI-13 shape): an api migration + env-free
  in-process scheduling; no new unit, no new key material.
- The `internal/run`+`internal/grant` extraction trigger stays **unfired and strengthened**:
  the Workstream is an in-process api consumer; no out-of-process consumer materializes.

## Red-team (the runaway-schedule surface)

| # | Attack | Closed by |
|---|---|---|
| 1 | Runaway / hijacked schedule floods Runs | quadruple-bounded: interval floor (§5) + per-grant `max_concurrent_runs` ≤ gateway cap + windowed budget (§3) + the per-operator ceiling; flagged resources still park regardless |
| 2 | Charter forged / issued without consent | chartering = operator cookie + CSRF on `human-agent` (the v0.5 posture, unchanged); audited `workstream.chartered` + `grant.issued` |
| 3 | Template mutated after consent (ride old approval) | **structural immutability** (§1): retire + re-charter is the only edit path |
| 4 | Tick replay / double-fire (crash between claim and launch) | single-winner status-guarded claim (§5) + tick-deterministic launch key riding the proven `(grant_id, launch_key)` dedup — a re-fired tick re-attaches |
| 5 | A second, diverging enforcement path (the in-process launch skips SEC-29/PERF-7) | §4 — one shared launch core, both callers; the split lands first with WD-6 green |
| 6 | Workstream actor exploits policy/approval (self-approve under nobody) | policy subject = `workstream:<id>` (§6) — ADR-0054 self-approve defeated exactly as v0.6's agent actor; approval gates off the resource label |
| 7 | Retired/paused charter keeps executing | retire revokes the grant → cascade cancels in-flight + plan-vs-grant blocks stale ticks (§7); pause stops the claim predicate (§5) |
| 8 | Confused-deputy: anything on a granted ungated resource, now on a clock | **Accepted, intentional residual** (the ADR-0062 #7 residual with a schedule) — it *is* the product; contained by scope/TTL/windowed-budget/parking |

**Verdict.** No new minting/escalation surface exists by construction (nothing new mints;
nothing new is agent-reachable; api was already the authority). The genuinely new vector is
the schedule itself (#1), quadruple-bounded; the single accepted residual (#8) is v0.6's,
extended in time and identically contained.

## Deferred (recorded)

- **Cron-expression schedules** (calendar semantics) — the named reserve over
  `interval_seconds`; trigger: operator demand for calendar alignment.
- **Agent-composed plans inside a Workstream** (the charter invokes an agent to author each
  tick's plan instead of instantiating a template) — the agent path exists (`run.launch`);
  trigger: a real objective that a fixed template cannot express.
- **Sources / rules** (the charter fields beyond objective+template+schedule) — arrive with
  the Memory/Context Index (P2) that gives them meaning.
- **The `internal/run`+`internal/grant` extraction** — trigger unfired (see Consequences).
- **Standalone plan-level grant-with-preview** — absorbed by the reviewed immutable template
  for the scheduled case; anything beyond waits for ergonomic pressure.
- **Parallel/branch/retry DAG; the gateway exec worker; fine-grained grant policy; SoD; the
  full Memory/Context Index; SSH pooling; full rate-limiting; TR-2 external-MCP proxying** —
  unchanged prior deferrals.

## Relation to prior ADRs

- **ADR-0061 (Governed Runs)** — adds the **third initiator** and completes the module the
  ADR named and reserved: the chartered Workstream that creates Runs. Engine, `/v1/steps`,
  durability, evidence — reused unchanged. Extends, does not edit.
- **ADR-0062 (delegation grants)** — the charter's authority *is* the grant; two additive
  generalizations (non-agent grantee subject; nullable `budget_window_seconds`) with NULL/
  existing-value semantics preserved. The launch-core split (§4) relocates no check — it
  re-homes the ADR-0062 §6 validation into a shared function. Extends, does not edit.
- **ADR-0060 (keyless gateway)** — preserved trivially: zero gateway diff; no new key or
  minting workload; the only new actor is an api-internal identity.
- **ADR-0046 (capability tokens)** — the edge set is unchanged; §6 fixes the *reading* of
  `agent-tool` (any non-human actor under delegated authority) without revising the set.
- **ADR-0054/0057/0058/0059** — reused unchanged (decision point, invocation governance,
  sole SSH egress, informed approval — extended in time by §1's immutability).

## Ratification (resolved 2026-07-05)

All three open points were signed off on the recommendation, and are now binding:

1. **§6 bracket edge = the existing `agent-tool`**, with the permission model's "agent"
   defined as *any non-human actor executing under delegated authority* — no eighth edge,
   no ADR-0046 revision — **ratified.**
2. **§3 windowed budget = a nullable `budget_window_seconds` column on `delegation_grants`**
   (NULL = v0.6 cumulative semantics, behavior-preserving) — over charter-side accounting —
   **ratified.**
3. **§7 pause is soft** (in-flight Runs complete; retire is the hard stop that revokes the
   grant and cascades) — **ratified.**

Everything else = the CEO-ratified D1–D8 slate from the v0.7 scoping consult + its
code-grounded self-audit (the shared-launch-core split and the windowed-budget gap being
that audit's two load-bearing findings).
