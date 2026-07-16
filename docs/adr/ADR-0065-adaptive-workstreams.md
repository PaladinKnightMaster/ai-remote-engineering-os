# ADR-0065: Adaptive Workstreams — governed agent memory access and agent-composed plans

## Status

**Accepted** — 2026-07-16 (v0.9 scoping consult — theme + decisions D1–D3 ratified — plus
the ADR consult whose four ratification points R1–R4 were signed off on the
recommendations; the pre-authoring as-built review surfaced zero slate-breaking findings).
Author/decider: CEO (war-room). Binding spec for the v0.9 adaptive-workstreams ticket
ladder; code is authored just-in-time per ticket from here.

**Builds on / conforms to:** ADR-0046 (the canonical capability-edge set — which has
*named* `agent-memory` as a reserved edge since v3.0.1; this ADR enforces it), ADR-0056
(the inbound-only MCP trust boundary — the new tools ride the existing wire; agents are
clients, the platform never dials out), ADR-0060 (the keyless agent-facing gateway —
preserved: every new verb is a forward, no new key, no new minting workload), ADR-0061
(the Run engine + the informed-launch full-plan review — resurrected here per tick),
ADR-0062 (delegation grants — the standing consent this ADR reuses as the gate for
plan-time memory reads; the `run.launch` forward pattern reused verbatim), ADR-0063 (the
chartered Workstream — gains an additive `adaptive` kind; its §Deferred
agent-composed-plans item is discharged), ADR-0064 (Persistent Scoped Memory — whose §4
explicitly required "its own consent + containment model" before any agent path to memory
exists: **this is that ADR**, discharging §Deferred's fused `agent-memory` enforcement +
memory→execution coupling items together, as recorded).

**No ADR divergence (G2-clean):** it **extends** ADR-0062/0063/0064 (additive charter
kind; additive memory columns + verbs; a new pre-`pending` Run status) and **preserves**
ADR-0046 (the edge *set* is unchanged — enforcing a named reserved edge is not an eighth
edge), ADR-0056 (three new methods on the existing hand-rolled wire; the trust boundary
is untouched), ADR-0060 (the gateway stays keyless), and ADR-0064's operator-plane
surface (the `/v1/memories*` routes and the operator read path are byte-identical; the
agent plane gains a *separate*, read-only, grant-scoped, per-read-chained path — the
amendment ADR-0064 §4 said would require its own ADR). It edits no Accepted ADR.

Companion spec: `docs/architecture/workstream-engine-architecture.md` §13 (the v0.9
extension, appended alongside §11/§12).

## Context

**The fused trigger.** ADR-0064 §Deferred recorded `agent-memory` enforcement and
memory→execution coupling as **one shared trigger — "they land together or not at all"**
— gated on the adaptive/agent-composed-Workstream consent model. v0.9 is that release:
agent-readable memory with nothing consuming it is a dead surface, and adaptive plans
without memory are v0.6 launches on a timer. Together they close the loop the platform
has been building toward: **remember → plan → govern → act → accrue → remember**.

**Doctrine resolution (the scoping consult's grounding question).** An agent *authoring*
plans does not breach the no-AI/no-SaaS doctrine, because the plan author is the
**customer's external agent, inbound over the existing ADR-0056 MCP surface** — exactly
the party the gateway was architected to serve ("MCP-compatible, not MCP-trusting"; the
agent is a Bearer-authenticated client, never something the platform runs or calls).
v0.6 agents **already compose plans**: `run.launch` accepts an agent-authored plan and
the api validates plan ⊆ grant (SEC-29). The v0.9 delta is only the *standing* form — a
charter whose per-tick plan is agent-composed instead of template-instantiated. Zero
model dependency, zero embedding, zero new egress; L3 stays deferred and hard-gated.

**Which primitive gates plan-time memory reads.** "No AI autonomy before audit + policy
+ approval primitives exist" is satisfied, and each primitive takes its position: the
**delegation grant** (ADR-0062) gates the *read* (same-grant scope containment, consent
recorded at charter, revocation-cascaded); **policy + approval** gate the *use* of what
was read (every composed plan parks for informed review); the **audit chain** closes the
loop — agent-plane reads are individually chained, the per-read escalation lever
ADR-0064 §8 named, firing exactly where it said it would (a non-owner plane reading
memory), while operator reads stay unchained.

**The inversion (doctrine-load-bearing).** ADR-0063 §Deferred's phrase "the charter
invokes an agent to author each tick's plan" cannot be literal: the platform never dials
out (no egress; agents are inbound MCP clients). It honestly compiles to an **inbound
plan-request channel**: the tick publishes a durable request; the customer's agent
discovers and answers it over the gateway (§5).

**As-built findings (the ADR consult's grounding, HEAD `58c68fd`):**

1. **The forward pattern is reusable verbatim.** `run.launch` is a control-plane
   `ToolDef` with no edge/action/target metadata
   ([catalog.go](../../services/mcp-gateway/internal/catalog/catalog.go)), an
   `apiforward` client minting the gateway's own asymmetric service token, and an api
   internal route subject-pinned to `mcp-gateway`
   ([delegated.go](../../services/api/internal/server/delegated.go)). Every new v0.9
   agent verb follows it; SEC-20/27 carry untouched.
2. **The operator read path never moves.** `memory.Store.List/Get` are keyed
   `permission_scope = owner`
   ([store.go](../../services/api/internal/memory/store.go)); the agent plane gets a
   **separate** grant-scoped, visibility-filtered read method, so ADR-0064's operator
   surface stays byte-identical and WD-8 remains a cheap regression guard.
3. **No agent-visibility flag exists** — migration **0013** adds it (§3), plus the
   audited share flip pair.
4. **Adaptive is an additive charter `kind`.** `workstream_steps` is the sealed
   immutable template "instantiated verbatim per tick" (migration 0011); the adaptive
   kind carries zero template steps and the ticker's branch opens a plan request
   instead of instantiating. Template charters and WD-7 semantics are structurally
   untouched.
5. **The park has a natural home.** A submitted plan can create a Run in a new
   pre-`pending` status — no parallel plan-approval machinery, one entity, the evidence
   packet included (§6).

**Standards lineage.** Capability POLA carried from ADR-0062/0063/0064 (the readable set
derives from the caller's own grant, nothing else); the consent-binds-to-the-reviewed-
artifact doctrine (ADR-0059/0061/0063) extended in granularity — from the immutable
template to **each tick's reviewed plan**; RFC 8693 actor/principal carried unchanged;
the inbound-only MCP posture (ADR-0056). The plan-request channel is the standard
work-queue/claim inversion applied to plan authorship.

## Decision

### 1. Three new gateway tools on the `run.launch` pattern — the gateway stays keyless

The catalog gains three **control-plane tools** (no target resolver, no SEC-16
single-resource check; dispatched before the `remote.exec` path, exactly like
`run.launch`):

- **`workstream.poll`** — list the caller's open plan requests (its charters only).
- **`plan.submit`** — answer a plan request with an ordered plan; idempotency-keyed on
  the request's tick launch key (a retry re-attaches, never double-submits).
- **`memory.read`** — read the caller's grant-scoped memory (§3).

Each is a **forward**: the keyless gateway (ADR-0060) relays to a subject-pinned
(`subject == "mcp-gateway"`) internal route on `services/api`, which remains the sole
authority. **No new key, no new minting workload, no new egress; zero change to the
symmetric-key posture.**

### 2. The `agent-memory` edge is enforced — on the memory-read path only

The canonical edge set (ADR-0046) has named `agent-memory` since v3.0.1;
reserved-unenforced was recorded as a deliberate decision at v0.8. v0.9 **lights it as
the typed edge on the memory-read path**: the `memory.read` tool's governance metadata,
the api internal memory-read route's typed posture, and the read audit events all carry
`agent-memory`. This is enforcement of the reserved seventh edge, **not** an eighth edge
invented — the set is unchanged, and the ADR-0046-adjacent act is taken deliberately and
recorded here. `workstream.poll`/`plan.submit` are control-plane verbs on the existing
`agent-tool` reading (ADR-0063: "agent" = any non-human actor under delegated
authority).

### 3. Read containment (D1, R3, R4) — read-only, same-grant scope, opt-in content, per-read audit

- **Read-only.** No agent write path to memory exists at v0.9; reference accrual on the
  Run terminal flip (ADR-0064 §2) remains the sole machine write.
- **Same-grant scope containment.** The readable set derives from the calling agent's
  own grant and nothing else: memories scoped to the grant's charter
  (`workstream:<id>`) and to that grant's Runs (via the existing `runs.grant_id`
  linkage). Never `operator` scope, never a foreign charter — cross-grant reads are
  impossible **by construction**, not by filter discipline.
- **Two content tiers.** Accrued reference rows (content-free, machine-generated) are
  readable by default. **Operator-authored content is invisible to agents unless
  shared**: migration **0013** adds `agent_visible` (default **false**), flipped by an
  audited pair — **`memory.shared`** / **`memory.unshared`** (R3; the pin/tombstone
  house pattern — instant, reversible, chained; the UI consent copy names the
  prompt-injection/exfiltration risk plainly). Sealed-at-rest handling (ADR-0064 §2) is
  unchanged; a shared row's content is decrypted for the agent read exactly as it is
  for the operator read.
- **Per-read audit (R4).** Every agent-plane read is chained — **`memory.read`**, one
  content-free event per tool call carrying the row ids returned, the agent actor, the
  grant. Operator reads stay unchained (the ADR-0064 §8 posture; this is its named
  escalation, fired for the non-owner plane only).

### 4. The adaptive charter kind (D2, R2) — additive; the composer is named at consent

`workstreams` gains an additive **`kind`** (`template` | `adaptive`; existing rows =
`template`, semantics untouched). An **adaptive** charter = label + objective + bounded
resource/verb scope + schedule + bounds + **a named composer agent subject** — and no
template steps. **The charter's auto-issued grant names the composer as grantee** (R2):
one typed, audited `human-agent` consent act now covers objective + scope + schedule +
bounds + *who composes*. The ADR-0063 auto-issue-in-tx, windowed budget, pause/retire
lifecycle, and runaway containment all carry unchanged.

### 5. The inbound plan channel (D3) — the tick publishes, the agent answers

An adaptive tick does not instantiate — it opens a durable, TTL-bounded **plan request**
row (new table; scoped to the charter, keyed by the tick's launch key). The composer
discovers it via `workstream.poll` and answers via `plan.submit`. An unanswered request
**expires audited** at its TTL and the next tick opens a fresh one — the charter never
wedges (the ADR-0063 bounds-refusal precedent); missed ticks collapse to one open
request. The platform initiates nothing outbound; the agent remains a pure inbound
client (ADR-0056).

### 6. Plan-preview park = the `proposed` Run (R1) — the consent successor

A submitted plan is validated **plan ⊆ grant first** (the shared `run.LaunchBounded`
core — one validator, now three callers) and then creates a Run in a new pre-`pending`
status **`proposed`**: parked before any step, full sealed plan visible to the operator
(the ADR-0061 informed-launch review surface, per tick), on the existing approval
console. Approve → `pending` → the **existing sequencer** drives it unchanged; deny →
terminal, audited, accrued like every terminal Run. No parallel plan-approval machinery
exists.

- **The consent successor invariant, stated plainly:** the immutable template's
  invariant was *consent binds to the reviewed artifact*; for adaptive charters **the
  reviewed artifact is each tick's composed plan** — reviewed at park, inside the
  standing grant's bounds. At v0.9 an adaptive plan **always parks**; unattended
  ungated execution of composed plans is explicitly deferred.
- **Identity (R2):** launched Runs carry **actor = the composing agent, principal = the
  chartering operator** (the ADR-0062 split, honest attribution — the agent authored
  the plan; the schedule only asked), workstream linkage via `grant_id` as today.
  Policy's subject is the agent → ADR-0054 self-approve stays defeated; flagged
  resources park per step *in addition to* the plan-level park.
- `plan.submit` validates the caller's Bearer subject against the grant's grantee — a
  foreign agent cannot answer another charter's request.

### 7. Kill switches — revocation reaches the new surfaces

Retire/revoke already cancels in-flight Runs and blocks stale ticks (the ADR-0062 §11.5
cascade). v0.9 extends the same guard to the new surfaces: a revoked/expired grant
**closes its open plan requests**, refuses `plan.submit`, refuses `memory.read`, and a
`proposed` Run of a revoked grant cannot be approved into `pending`. One check per
surface, the existing cascade re-aimed.

### 8. Audit vocabulary

Via the existing outboxes, content-free (ADR-0049): **`memory.read`** (agent plane,
per-read, row ids + grant; §3), **`memory.shared`** / **`memory.unshared`** (operator
flip pair), plan-request lifecycle (**`plan.requested`** / **`plan.submitted`** /
**`plan.expired`**), and the `proposed`-Run resolution events. Names indicative — exact
event set, statuses, and the new SEC invariant code are pinned at the v0.9 criteria
consult (the ADR-0064 precedent).

## Consequences

- The loop closes: the platform's memory **feeds execution under governance** — read
  bounded by the grant, use bounded by the park, everything chained. Strategy layers 4
  and 6 (Living Workstreams; governed memory) meet in one surface that is self-hosted
  and offline — the defensibility release.
- **WD-9** (pinned at the criteria consult) is the wedge: an adaptive charter's tick
  publishes a plan request → the agent reads its own scoped memory over the gateway →
  composes a plan grounded in it → the plan parks for informed preview → approval
  drives it through the existing governed plane → the terminal Run accrues → **the next
  tick's read sees what the last tick learned**. Negatives: cross-grant read denied +
  chained; unshared authored content invisible; revoked grant kills reads, submits, and
  approvals; unanswered request expires audited; out-of-grant plan refused (SEC-29
  unchanged).
- Honest, bounded new work: three gateway catalog entries + their forwards, one
  migration (0013: `agent_visible` + the charter `kind`/composer + the plan-request
  table + the `proposed` status — exact split at the criteria consult), the agent-plane
  read method + route, the ticker's adaptive branch, the share/approval UI touches.
  Policy, connector, broker, and the audit chain are reused unchanged; **zero model
  dependency, zero new egress, the gateway stays keyless.**
- Template charters, WD-5/6/7/8 semantics, and the operator memory surface are
  untouched — the elevated regression guards carry.
- The `internal/*` extraction trigger stays **unfired**: every new path rides
  gateway→api in-process; still no out-of-process consumer.

## Red-team (the lit channel)

| # | Attack | Closed by |
|---|---|---|
| 1 | Cross-grant memory read (the SEC-31 rationale) | Scope derivation from the caller's own grant only (§3) — foreign charters/operator scope unreachable by construction; negative leg in WD-9 |
| 2 | Prompt injection via authored memory content | Default-invisible; sharing is an explicit audited operator flip with honest consent copy (§3); and the composed plan parks before execution regardless (§6) |
| 3 | Exfiltration via composed plans (agent encodes read content into submitted commands) | Plan ⊆ grant bounds where commands may run; the operator preview reads exactly those commands pre-execution; `memory.read` chaining makes the read visible beside the plan |
| 4 | Poisoned accrual steers future plans | The plan still parks (a human reads it); accrual is attributed + chained (ADR-0064 §8), so a poisoned row is evidence against its author |
| 5 | Composer impersonation (foreign agent answers a request) | `plan.submit` pins the Bearer subject to the grant's grantee (§6); requests are charter-scoped in `workstream.poll` |
| 6 | Duplicate/replayed plan submission | Idempotency key = the request's tick launch key riding the existing `(grant_id, launch_key)` dedup — a retry re-attaches (§5) |
| 7 | Revocation race (read/submit/approve after revoke) | The cascade extended to all three surfaces (§7); `proposed` Runs of a dead grant cannot be approved |
| 8 | The plan-level park is bypassed for adaptive work | Adaptive plans **always** park at v0.9 (§6); unattended-adaptive is deferred, not configurable |

**Verdict.** The genuinely new risk class is **agent-readable memory** — confined to
content-free reference rows by default, same-grant scope by construction, opt-in for
authored content, per-read chained, and consumed only through a plan that a human
reviews before execution. Nothing new mints; nothing outbound exists; the accepted
residual is that a shared row's content is, once shared, inside the agent's context —
which is precisely what the share flip's consent copy says.

## Deferred (recorded)

- **Agent memory writes** (agent-authored rows) — a second consent problem; accrual
  stays the sole machine write.
- **Unattended adaptive execution** (composed plans running ungated without the
  plan-level park) — trigger: operational trust evidence from parked adaptive charters.
- **Layer 1 — Live/Context Index** (REQ-013, B-009) — now with a named consumer once
  v0.9 ships.
- **Layer 3 — Reasoning Retrieval** — hard gate unchanged (any embedding/AI dependency
  = its own ADR against the offline + no-SaaS doctrine).
- **Computed `confidence`** (L3), **retention default on accrued rows** (trigger:
  observed growth), **multi-operator ACL + operator-read chaining** (SoD),
  **manifest-primary storage** — all carried from ADR-0064.
- **The `internal/*` extraction; cron; DAG; the gateway exec worker; SSH pooling; full
  rate-limiting; TR-2** — unchanged prior deferrals.

## Relation to prior ADRs

- **ADR-0064 (Persistent Scoped Memory)** — its §4 required a consent + containment ADR
  before any agent path to memory: this ADR is that document, and it keeps every
  operator-plane clause intact (routes, sealing, tombstones, owner scoping untouched).
  Extends, does not edit.
- **ADR-0063 (Chartered Workstreams)** — gains the additive `adaptive` kind; the
  immutable-template invariant survives unchanged for `template` charters, and its
  §Deferred agent-composed-plans item is discharged with the recorded trigger. Extends,
  does not edit.
- **ADR-0062 (Delegation grants)** — the grant is the gate for plan-time reads and the
  composer's standing authority; the cascade is re-aimed at the new surfaces. Extends,
  does not edit.
- **ADR-0061 (Governed Runs)** — the informed full-plan review returns per tick as the
  `proposed` park; the sequencer, evidence packet, and `run:<id>` namespace are
  unchanged. Extends, does not edit.
- **ADR-0056 / ADR-0060 (gateway surface / keyless posture)** — three new methods on
  the existing wire, all forwards; the trust boundary and key posture are preserved.
- **ADR-0046 (capability edges)** — the reserved `agent-memory` edge is enforced on one
  path; the edge set is unchanged. Preserved.
- **ADR-0049 (hash-in-chain)** — every new event is content-free; the read events chain
  facts, never content. Preserved.

## Ratification (resolved 2026-07-16)

All four open points were signed off on the recommendation, and are now binding:

1. **R1 — plan-preview mechanism = the `proposed` Run status** (one entity, existing
   sequencer/evidence/console) over a parallel plan-approval machinery.
2. **R2 — adaptive identity model:** the charter names the composer agent; the
   auto-issued grant's grantee = that agent subject; launched Runs carry actor = the
   composing agent, principal = the operator.
3. **R3 — sharing = the audited flip pair** `memory.shared`/`memory.unshared` on any
   row type, default invisible, over write-time-only immutable visibility.
4. **R4 — `memory.read` chaining:** agent-plane reads chained per tool call
   (content-free row ids); operator reads stay unchained.

Everything else = the CEO-ratified v0.9 scoping slate (theme; D1 containment; D2 the
additive adaptive kind with the always-park consent successor; D3 the inbound plan
channel) + the ADR consult's as-built review (findings F1–F6, zero slate-breaking).
