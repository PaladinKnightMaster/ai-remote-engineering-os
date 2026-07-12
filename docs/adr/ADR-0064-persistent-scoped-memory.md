# ADR-0064: Persistent Scoped Memory — governed, forgettable memory over the immutable evidence chain

## Status

**Accepted** — 2026-07-12 (v0.8 scoping consult — theme decisions D1–D4 — plus the ADR
consult whose three §Ratification points R1–R3 were signed off on the recommendations; the
pre-authoring self-audit surfaced zero slate-breaking findings). Author/decider: CEO
(war-room). Binding spec for the v0.8 persistent-scoped-memory ticket ladder; code is
authored just-in-time per ticket from here.

**Builds on / conforms to:** ADR-0045 (transactional outbox + content-free audit ingest),
ADR-0046 (the canonical capability-edge set — which already *names* `agent-memory` as a
reserved edge), ADR-0049 (hash-in-chain: events carry evidence references, never raw
bytes), ADR-0059 (operator-only reads of secret-class persisted material + retention
bounding), ADR-0060 (the keyless agent-facing gateway — preserved trivially here),
ADR-0061 (the Evidence Packet — the read-time assembly this ADR makes durable), ADR-0063
(the chartered Workstream — the owning entity; its §8 read-time scoped-memory slice is
the seed this ADR promotes).

**No ADR divergence (G2-clean):** it **extends** ADR-0061 and ADR-0063 (the evidence
packet / EVD-1 read-time slice becomes a durable, governed store — additive; no existing
read path changes meaning) and **preserves** ADR-0046 (the edge set is untouched;
`agent-memory` stays reserved-*unenforced* — no agent path to memory exists at v0.8),
ADR-0049 (forgetting is chained as content-free tombstone events; the chain itself is
never rewritten), ADR-0059/0060 (sealed-at-rest secret-class handling; zero gateway
change, no new key, no new minting workload). It edits no Accepted ADR.

Companion spec: `docs/architecture/memory-context-architecture-v3.md` (the three-layer
model; the v0.8 implementation slice is appended there).

## Context

**Tier resolution (the v0.8 scoping consult's grounding question).** The requirements
ladder classifies the standalone Memory/Context Index as **P2** (`REQ-013` Continuous
Context Index and `REQ-014` Persistent Memory, both P2/"Future" in
`docs/requirements/requirements_v3.csv`; backlog `B-009`/`B-010`; roadmap Phase 6). The
genuine **P1** pillar was `REQ-007` Living Workstreams — "recurring stateful work with
memory, rules, sources" — whose read-time memory attribute shipped at v0.7 (EVD-1). The
CLAUDE.md priority-ladder bullet listing "Memory" under P1 is an imprecise compression of
REQ-007, outranked by requirements + backlog + roadmap + the priority legend. **v0.8 is
therefore the first release to open the P2 tier** — and P2's "prototype/research track"
character licenses exactly the thin, deterministic slice this ADR specs.

**Not a fourth initiator.** v0.5→v0.7 each moved *who launches* (operator → agent →
schedule). v0.8 turns 90°: it is the platform's first **governance/data-plane** release.
The wedge is not an execution delta — a conscious, CEO-blessed choice (scoping D3).

**Why memory, and why now — the forgettable projection.** The audit chain is immutable by
design: it *cannot* redact, expire, or permission-scope what it records. Yet governed
operation demands **governed forgetting** — retention limits, redaction of
operator-authored notes, expiry of stale working state — over what the autonomous system
did and learned. That capability must live somewhere, and it cannot be the chain.
Persistent Scoped Memory is that somewhere: the **mutable, curated, forgettable
projection** standing beside the immutable evidence spine. The system already holds this
shape in embryo: secret-bearing bytes live *outside* the chain in the access-controlled
cast/exec store (ADR-0049), and the invocation reaper already **bounds command retention**
to the approval window (ADR-0059) — retention governance exists; this ADR generalizes it
into a queryable, scoped store.

**The three-layer system, Layer 2 only.** `memory-context-architecture-v3.md` defines
Live Index (L1) / Persistent Memory (L2) / Reasoning Retrieval (L3). v0.8 builds **L2
only**. L1 (incremental repo/docs/tickets indexing, REQ-013) needs its own ingestion
machinery — deferred. L3 (vector/graph retrieval) is the layer that would demand an
embedding/AI dependency — **hard-gated**: any AI or embedding dependency requires its own
ADR against the offline + no-SaaS doctrine. v0.8 is deterministic end to end: zero new
third-party dependency, zero AI, zero new egress, **zero gateway diff**.

**As-built findings (the ADR consult's grounding, HEAD `47622d7`).** Four findings shape
the decisions:

1. **The `agent-memory` edge already exists in the canonical set**
   (`docs/security/agent-permission-model.md`) — named at v3.0.1, never enforced (only
   `human-agent` / `agent-tool` / `agent-remote-session` / `service-service` are enforced
   today). v0.8 deliberately does **not** light it up (§4): the edge-set is untouched and
   the security decision is grounded in the existing model rather than invented.
2. **The EVD-1 slice is the seed.** v0.7's charter detail assembles run history +
   evidence-packet links at read time from durable state (ADR-0063 §8) and explicitly
   deferred the charter's sources/rules fields "to arrive with Memory." The core accrual
   path (§2) is the direct promotion of that read-time assembly into a durable record.
3. **The sealing and outbox machinery is reusable wholesale.** The nil-safe
   `CommandSealer` ([`store.go`](../../services/api/internal/run/store.go)) and the
   api transactional outbox already handle secret-class-at-rest and in-tx audit events;
   the sibling-seam shape (`internal/grant`, `internal/workstream`) is proven three times
   over.
4. **The reaper pattern absorbs expiry.** The api already runs bounded background sweeps
   (the T-032 `ReapStale` shape, [`main.go`](../../services/api/main.go)); expiry is a
   sweep, not a new architecture (§5).

**Standards lineage.** Retention/erasure governance (GDPR-style right-to-erasure applied
to *operational* memory, not the legal claim itself); the **tombstone** pattern from
append-only log compaction (chain the fact of deletion, never the content); capability
POLA carried from ADR-0062/0063 (memory is reachable only by the operator plane that owns
it). The two-store shape — an immutable event spine plus a mutable governed projection —
is the standard event-sourcing "projection" architecture, applied to agent memory.

## Decision

### 1. A new sibling seam — `services/api/internal/memory`

The fourth sibling package (the proven `internal/grant` / `internal/workstream` shape:
pgx pool + outbox writer, self-contained, extraction-ready), owning one table
(migration **0012**):

| Table | Fields (proposed) |
|---|---|
| `memories` | `id`, `scope_type` (§6), `scope_id`, `key` (operator-facing name), `ref` (the evidence pointer — run/step/invocation ids + evidence anchors; content-free), `content` (**nullable**; operator-authored free text only, **sealed at rest** via the same nil-safe `CommandSealer` the Run and Workstream stores use), `source_trace` (what produced it: `run:<id>` terminal / operator), `confidence` (recorded, defaulted — **not computed**; scoring is an L3 concern), `pinned` (bool), `expires_at` (nullable), `redacted_at` (nullable — the tombstone flip), `permission_scope` (owner-scoped at v0.8, §6), timestamps |

No update-in-place of `content` (a memory is written once; correction = redact + write —
the ADR-0059/0063 consent-binds-to-the-artifact doctrine applied to records).

### 2. Two content classes — reference-based accrual is the core (R1, ratified)

- **The core path is accrual by reference.** A terminal Run (including a Workstream-fired
  Run) writes a memory **pointer** — run/step/invocation ids, evidence-packet linkage,
  stdout-SHAs — in the same transaction as its terminal `run.*` event (the api outbox
  in-tx precedent). **Content-free**: no command text, no stdout bytes enter the memory
  row. This is the literal promotion of the v0.7 EVD-1 read-time assembly into a durable,
  scoped, governed record — and it expands the secret-at-rest surface by **zero**.
- **The secondary path is operator-authored free text** (`content` set): notes, learned
  facts, working state — the only genuinely new secret-bearing surface. It is **sealed at
  rest** (the F3/`CommandSealer` parity), **operator-only on read** (the ADR-0059
  persisted-command posture), and never appears in any audit event (ADR-0049).

*(Rejected: copying raw Run content into memory — it would duplicate the cast/exec store,
expand the secret-at-rest surface for no retrieval gain at L2, and turn every memory row
into secret-class material. R1 ratified the reference-based core.)*

### 3. Governed forgetting = content-free tombstones; the chain is never rewritten (R2, ratified)

- **Redact**: an idempotent flip (`redacted_at` set once) that tombstones the row —
  `content` is erased/overwritten, the pointer skeleton may remain for provenance —
  chained as **`memory.redacted`** (content-free: the fact, the scope, the actor; never
  the content). The `flipWithEvent` once-only precedent applies.
- **Expire**: the sweep (§5) tombstones unpinned rows past `expires_at`, chained as
  **`memory.expired`**. **Pin** exempts a row from expiry (`pinned = true`, operator
  verb, audited).
- **The honest boundary — stated plainly, in the ADR, the criteria, and the UI:**
  redact/expire govern the **mutable memory projection only**. They do not and cannot
  rewrite the append-only evidence chain, the cast/exec store, or any prior export.
  Raw-byte retention remains the separate cast-dir/ADR-0059 GC story. This boundary *is*
  the design: memory is where governed forgetting lives precisely *because* the chain by
  design cannot forget.

### 4. Operator-plane only — `agent-memory` stays reserved-unenforced

All memory verbs — write, read, redact, pin — are **`human-agent`** routes on
`services/api` (operator cookie + CSRF), exactly like every prior operator surface.
**No agent read or write path to memory exists at v0.8**: the canonical `agent-memory`
edge remains named-but-unenforced, the gateway gains no memory tool, and no
service-service route touches the memory store.

The rationale is **security, not sequencing**: the moment memory content feeds an agent's
context, it becomes a **cross-grant prompt-injection and secret-exfiltration channel**
(an agent could be steered by, or leak through, accrued content from outside its grant's
scope). Lighting up `agent-memory` therefore requires its own consent + containment model
— and it is the same trigger that gates adaptive/agent-composed Workstreams (§Deferred).
The named invariant (SEC code pinned at the criteria consult): **memory is operator-plane
only, never agent-reachable, content-free in the chain, secret-class at rest.**

### 5. Expiry rides the existing reaper — no fourth goroutine

The expiry sweep folds into the api's existing background-sweep machinery (the T-032
`ReapStale` shape): scan for unpinned rows past `expires_at`, tombstone + chain
`memory.expired`, bounded batch. **No new systemd unit, no new Caddy route, no new env,
no new key material** (the OI-14 leanness carry); migration 0012 rides `install.sh`'s
existing migrate loop.

### 6. Scope set = `{workstream, run, operator}` (R3, ratified); owner-scoped

`scope_type` is bounded to the entities that exist: **`workstream`** (scope_id = the
charter — the owning entity ADR-0063 named), **`run`** (scope_id = the Run), and
**`operator`** (scope_id = the operator — personal working memory). The architecture
doc's fuller scope list (org/team/project/ticket/skill/user-beyond-operator) defers with
the modules that would own those scopes. `permission_scope` is recorded on every row and
enforced as **owner-scoped** (the single-bootstrap-operator posture, GOV-7 carried); a
real multi-operator ACL defers with SoD.

### 7. Surface — routes, the charter-detail promotion, and export

- **Routes** (OpenAPI fragment + codegen; `human-agent` + CSRF, owner-scoped; exact
  shapes at the criteria consult): `GET /v1/memories?scope_type=&scope_id=` (list/read),
  `POST /v1/memories` (operator-authored write), `POST /v1/memories/{id}/redact`,
  `POST /v1/memories/{id}/pin` (+ unpin). Accrual (§2) is in-process — not a route.
- **UI**: the v0.7 charter-detail scoped-memory view is **promoted to read the persisted
  store** (accrued entries beside the read-time evidence links); an operator memory view
  rides the same list route. No new design-system components anticipated.
- **Export (D4, ratified at scoping):** storage is **DB-only** at v0.8 (consistency with
  every prior `internal/*` module); the architecture doc's "filesystem-friendly memory
  manifests" pattern is satisfied by **`export` producing the manifest on demand** (a
  deterministic, operator-triggered read that renders a scope's memories to a
  filesystem-friendly manifest form — never hidden-only, per the doc's own rule). A
  manifest-*primary* store is the recorded alternative if filesystem-first workflows
  materialize.

### 8. Audit vocabulary

Via the api outbox, content-free (ADR-0049): **`memory.written`** (actor = operator for
authored writes; actor = the accruing Run's actor — `workstream:<id>` or the agent — with
principal, for accrual; target `memory:<id>`; in-tx with the row), **`memory.redacted`**,
**`memory.expired`**, **`memory.pinned`** / **`memory.unpinned`** (operator verbs). Reads
are **not** individually chained at v0.8 (the ADR-0059 read-path precedent — the listed
routes are owner-scoped and CSRF-gated; per-read audit is recorded as the escalation
lever if multi-operator arrives).

## Consequences

- The platform gains its **first governance/data-plane module**: durable, scoped,
  source-traced, redactable, expirable, pinnable, audit-chained memory — the mutable
  counterpart to the immutable evidence spine, and the store the charter's future
  sources/rules fields and any later agent-context work will read. The strategy's
  "governed memory" defensibility line (`docs/executive/strategy.md` layer 6) becomes
  real product surface — self-hosted and offline, which no SaaS memory product offers.
- Honest, bounded new work: one new api package + one table + one migration + the
  accrual hook on Run terminal + the `/v1/memories*` routes + the reaper fold + the
  charter-detail promotion. The engine, gateway, policy, connector, broker, and audit
  chain are reused unchanged; **zero gateway diff, zero new dependency, zero AI**.
- **The wedge is not an execution delta** — stated plainly (scoping D3): v0.5–v0.7 each
  proved a new way work *runs*; v0.8 proves the platform can *remember and forget under
  governance*. WD-8 (pinned at the criteria consult) is a governance walkthrough:
  accrue → read scoped → redact → expire/pin → chain-verify.
- Memory **never feeds execution** at v0.8 (scoping D2): no template, plan, or agent
  context reads from the memory store. The v0.7 immutable-sealed-template invariant is
  untouched, and the injection/exfil channel (§4) stays closed by construction.
- The `internal/*` extraction trigger stays **unfired and further strengthened**: a
  fourth in-process sibling, still no out-of-process consumer.

## Red-team (the stored-content surface)

| # | Attack | Closed by |
|---|---|---|
| 1 | Agent reads memory → cross-grant prompt injection / secret exfiltration | **No agent path exists** (§4): `agent-memory` unenforced, no gateway tool, no service-service route; operator plane only |
| 2 | Memory expands the secret-at-rest surface | Reference-based core is content-free (§2); authored `content` is sealed (`CommandSealer`) + operator-only read (ADR-0059 posture) |
| 3 | "Redaction" is believed to erase the *chain* (compliance confusion) | The §3 boundary stated in ADR + criteria + UI: tombstones govern the projection; the chain, casts, and prior exports are explicitly out of redaction's reach |
| 4 | Memory poisoning — a false memory steers future work | Memory feeds **no** execution at v0.8 (D2); every write is attributed + chained (`memory.written`, actor/principal + `source_trace`), so a poisoned entry is evidence against its author |
| 5 | Cross-operator read | Owner-scoped `permission_scope` on every row + owner-scoped routes (single-operator posture today; ACL named for multi-operator) |
| 6 | Unbounded retention (sweep fails silently) | The reaper-fold is the proven bounded-sweep shape; `memory.expired` is chained per tombstone, so silence is visible in the chain; pinned rows are the only sanctioned exception |
| 7 | Tombstone replay / double-redact inflates the chain | Idempotent once-only flip (`redacted_at` set once; the `flipWithEvent` precedent) — second redact is a no-op, no duplicate event |

**Verdict.** No new agent-reachable surface, no new key, no new minting workload, no new
egress path exists by construction. The genuinely new risk class is **stored operator
content** (#2), confined to the opt-in authored path, sealed, and operator-only. The
accepted residual: redaction cannot reach the immutable spine (#3) — that is the design,
stated honestly, not a gap.

## Deferred (recorded)

- **`agent-memory` enforcement** (any agent read/write of memory) — trigger: the adaptive
  /agent-composed-Workstream consent + containment model; this is the same trigger as the
  next item and they land together or not at all.
- **Memory→execution coupling** (dynamic templates; agent context assembled from memory;
  charter sources/rules *affecting* runs — at v0.8 sources/rules remain deferred
  entirely, arriving as inspectable metadata only when the consuming design exists).
- **Layer 1 — Live/Context Index** (REQ-013 incremental repo/docs/tickets indexing) —
  its own ingestion machinery + spike (B-009).
- **Layer 3 — Reasoning Retrieval** (vector/graph search) — **hard-gated**: any
  embedding/AI dependency requires its own ADR against the offline + no-SaaS doctrine.
- **Computed `confidence`** — recorded/defaulted at v0.8; scoring is an L3 concern.
- **Scopes beyond `{workstream, run, operator}`** (org/team/project/ticket/skill) —
  defer with their owning modules.
- **Multi-operator ACL on `permission_scope`** — defers with SoD; per-read audit is the
  named escalation lever.
- **Manifest-primary storage** — DB-only at v0.8 (D4); `export` renders the manifest;
  trigger: filesystem-first operator workflows.
- **The `internal/*` extraction; DAG; cron; exec worker; SSH pooling; rate-limiting;
  TR-2** — unchanged prior deferrals.

## Relation to prior ADRs

- **ADR-0063 (Chartered Workstreams)** — the Workstream becomes the owning entity it was
  chartered to be: its §8 read-time scoped-memory slice is promoted to a durable store;
  its deferred sources/rules stay deferred until a consuming design exists. Extends, does
  not edit.
- **ADR-0061 (Governed Runs)** — the Evidence Packet's read-time assembly gains a durable
  accrual record referencing it; the packet itself and the `run:<id>` namespace are
  unchanged. Extends, does not edit.
- **ADR-0049 (hash-in-chain)** — the tombstone events are the pattern's direct
  application: chain the fact, never the content. Preserved.
- **ADR-0059 (operator-only secret-class reads + retention bounding)** — authored
  `content` inherits the persisted-command posture wholesale; the reaper-fold extends the
  retention-bounding precedent. Preserved.
- **ADR-0046 (capability edges)** — the edge set is untouched; `agent-memory` remains
  named-and-reserved, now with an explicit non-enforcement decision recorded (§4).
  Preserved.
- **ADR-0060 (keyless gateway)** — preserved trivially: zero gateway diff, no new key,
  no new minting workload, nothing new agent-reachable.

## Ratification (resolved 2026-07-12)

All three open points were signed off on the recommendation, and are now binding:

1. **§2 content model = reference-based accrual as the core** (content-free EVD-1
   promotion), operator-authored sealed free-text as the secondary opt-in path — over
   copying raw Run content into memory — **ratified (R1).**
2. **§3 forgetting boundary = redact/expire govern the mutable projection only**, never
   the append-only evidence chain (raw-byte GC stays the cast-dir story) — **ratified
   (R2).**
3. **§6 scope set = `{workstream, run, operator}`**; further scopes defer with their
   owning modules — **ratified (R3).**

Everything else = the CEO-ratified D1–D4 slate from the v0.8 scoping consult (theme +
Layer-2-only; execution-decoupling on security grounds; the governance-wedge shape;
DB-only storage with export-renders-the-manifest) + the ADR consult's self-audit
(findings A–H, all de-risking or refinements; zero slate-breaking).
