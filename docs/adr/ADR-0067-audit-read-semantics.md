# ADR-0067: Audit read semantics — chain-contiguity, server-side filtering, and the lifecycle-noise policy

## Status

**Accepted** — 2026-07-26 (v0.11 "Operator Experience" scoping consult; the nine decision points
D1–D9 were signed off on the war-room recommendation, including three recommendation-level
divergences from the ratified epic catalog: **D2** narrows E-01/S-03 from per-column sorters to
seq-direction-only, **D7** re-frames E-08 from a write-side accrual change to a read-side scope
resolution, and **D5** records the upstream re-ask backoff as deferred rather than in-scope).
Author/decider: CEO (war-room). Binding spec for the v0.11 ticket ladder; code is authored
just-in-time per ticket from here.

**Builds on / conforms to:** ADR-0045 (the Postgres outbox + the chained-hash extended in-process at
the chain authority — this ADR adds **no** write path and does not touch the append side), ADR-0049
(hash-in-chain / content-free events — preserved: no event's stored shape changes), ADR-0044
(`audit-service` migrations are **forward-only by policy** — the constraint that rules out a stored
classification column, §D3), ADR-0043/0047 (chi + `oapi-codegen` server stubs and the
`openapi-typescript` client from the one fragment — the new params are additive, named components),
ADR-0064 §1/MEM-2 (the memory scope-derivation rule — §D7 leaves it **unchanged** and resolves scope
at read time instead), ADR-0065 (Adaptive Workstreams — the source of the lifecycle-event volume this
ADR governs the display of), ADR-0066 (Layer 1 — the per-read chained precedent).

**No ADR divergence (G2-clean):** it **extends** the read side of ADR-0045/0049 (new query
parameters, a new read-time lens, a new response envelope field) and **preserves** the append side
byte-for-byte — no new column, no change to `chain.Compute`'s input domain, no change to any event's
actor/edge/target/outcome vocabulary. ADR-0064's scope derivation is untouched (§D7 is a query, not a
write). It edits no Accepted ADR.

## Context

**The trigger.** Measured live during the §human WD-10 walkthrough (v0.10.0, 2026-07-24): **635 audit
events, 601 of them (95%) adaptive-charter lifecycle events, the newest entries 13 "next page" clicks
deep.** The audit trail **is** this product's core evidence claim; an operator who cannot reach recent
events undermines the governance proposition a commercial Beta 1 makes. Flagged **v1.0-BLOCKING** at
the §human and ratified as such in `docs/requirements/epic_story_catalog_v4.md` §Part 4. This is a
**defect on already-shipped functionality**, which is why it precedes every new surface on the Beta-1
path.

**As-built findings (HEAD `05ec99c`):**

1. **The store is forward-cursor only, with no predicate.**
   `services/audit-service/internal/store/store.go` — `WHERE seq > $1 ORDER BY seq ASC LIMIT $2`.
   Keyset pagination on the `seq` BIGSERIAL; correct and cheap, but only ever walks *away* from the
   newest event.
2. **The UI is a thin, honest client of that store.** `apps/web/src/routes/Audit.tsx` — `PAGE_LIMIT =
   50`, "Load more" appends, "Refresh" re-reads from the head. Its header comment records the
   deliberate v0.1.0 rule this ADR must answer: *"No client-side filtering at v0.1.0 — filtering a
   partial page would misrepresent the chain."*
3. **The api read-proxy forwards only `limit` and `cursor`.**
   `services/api/internal/server/server.go` `ListAuditEvents` — the operator is gated by the shared
   capability middleware, then api reads `audit-service` with its **own** service token, so the chain
   authority's write gate never accepts an operator token. That posture is correct and is preserved
   verbatim; only the forwarded query set grows.
4. **`LimitParam` and `CursorParam` are shared fragment components** (`docs/api-contracts/
   openapi.v3.fragment.yaml`) referenced by resources, runs, grants and every other list route.
   **Editing them would silently change every list surface in the product.** New audit params must be
   *new* named components applied to the two audit operations only (`/v1/events`,
   `/v1/audit/events`).
5. **The noise is the steady state of the v0.9 flagship, not an anomaly.** `plan.requested` and
   `plan.expired` are chained content-free lifecycle facts (actor = `workstream:<id>`, edge
   `human-agent`, target = `plan-request:<id>`; `services/api/internal/workstream/store.go`). An
   adaptive charter at the **60s production floor** with no operator answering chains **2 events per
   minute ≈ 2,880/day/charter**, indefinitely.
6. **Operator read routes chain nothing.** `memory.Store.List` and the operator context-source list
   are bare `SELECT`s with no `Enqueue` and no transaction; the chained `memory.read` / `context.read`
   events are on the **agent** planes only (`ReadForAgent`, `SearchForAgent`). Operator polling
   therefore adds **zero** audit events — which is what makes §D6 safe.
7. **The agent read plane already resolves run-scoped memory through the grant; the operator's does
   not.** `ReadForAgent` reaches run-scoped rows via `runs.grant_id` (asserted live in
   `services/api/internal/memory/store_integration_test.go`), while the operator's `List` is a flat
   `scope_type = $2 AND scope_id = $3`. In the charter-detail view **the operator sees less than the
   composer does** — §D7's real defect.

**Why an ADR and not a ticket.** Two of the three v0.11 epics are ordinary work. The third —
suppressing or collapsing lifecycle events — **changes what the evidence record shows an operator**,
and a governance product cannot make that change in a UI patch. The catalog recorded it as an ADR
question; this ADR answers it.

## Decision

### D1. The chain-contiguity invariant (**EVD-8**) — the spine

Today every page is **contiguous in `seq`**, so an operator (or a verifier) can walk
`prevHash`/`chainHash` down the page and confirm linkage locally. Reversing the order preserves that
(still contiguous, walked backwards). **Filtering does not. Sorting by any non-`seq` column does
not.** That loss is real, and it — not filtering as such — is what the v0.1.0 rule was protecting.

> **EVD-8.** A view that is not a **contiguous, `seq`-ordered window** over the chain MUST be
> presented as a **query result**, never as a chain segment. Any such view MUST disclose that it is
> filtered, and MUST disclose the size of the whole chain it was drawn from. The unfiltered,
> `seq`-ordered timeline remains the **canonical evidence view** and MUST always be reachable in one
> interaction.

This invariant governs every subsequent decision in this ADR.

### D2. Ordering — reverse cursor, seq-direction only

`audit-service` gains a **descending** keyset path (`WHERE seq < $1 ORDER BY seq DESC LIMIT $2`)
alongside the existing ascending one, selected by an explicit direction parameter; the operator
default becomes **newest-first**. Both directions remain contiguous, so both remain chain segments
under EVD-8.

**Sorting is restricted to seq direction.** The epic catalog's E-01/S-03 ("per-column asc/desc
sorters") is **narrowed**: per-column sorting over a 50-row page is precisely the partial-page
misrepresentation the v0.1.0 rule banned, wearing new clothes — "sort by actor" over 50 of 635 rows
tells the operator nothing true. Sorting by `timestamp` is additionally unsound as a chain order:
`timestamp` is **producer-supplied** through the outbox, while `seq` is **authority-assigned at
append**; they are near-agreeing but not identical, and only `seq` defines the chain.

**Actor / outcome / edge are expressed as filters (D4), never as sorts.**

### D3. The lifecycle-noise policy — classify at read, never at write, never at rest

The load-bearing decision. Three options were considered and two rejected:

- **Rejected — suppress at write (do not chain lifecycle events).** This destroys evidence.
  `plan.expired` is precisely the record that *the system asked and the human did not answer* — the
  single most governance-relevant fact any future unattended posture would need to prove. It also
  contradicts ADR-0045/0049's append-only, fail-loud posture (G2).
- **Rejected — collapse N events into one rolled-up row at read.** A rolled-up row is a **synthesized
  row that exists in no chain**. The moment the evidence view renders rows that are not events, "the
  audit trail is the evidence" stops being literally true.
- **Rejected — a `significance` column on `audit_events`, set at append.** This is a trap in both
  branches. If `chain.Compute` hashes it, the hash **input domain** changes and every historical
  event's chain breaks — against a **forward-only** migration policy (ADR-0044). If it does not, we
  have added an **unhashed, therefore unauthenticated and mutable, field to the evidence record** —
  strictly worse than a constant that at least does not masquerade as data.

**Accepted — a versioned, named, read-time preset.** The classification is a **query predicate held
in `audit-service`**, not stored state and not a frontend constant:

- Every event is **chained, stored, and returned by the unfiltered API path** exactly as today.
  Nothing about the record changes.
- `audit-service` owns one **named, versioned preset** — `governed-actions@1` — resolving to an
  outcome-set predicate that excludes the content-free charter-lifecycle outcomes
  (`plan.requested`, `plan.expired`). Its membership list lives in exactly one place in
  `audit-service`, is enumerated in the v0.11 release criteria, and is **applied server-side over the
  whole chain** (never over a page).
- The preset is **a lens, not a policy on the record.** Changing its membership is a criteria change
  (and therefore a new ADR, per the criteria-drift rule) — but it can never make an event
  unretrievable, because the unfiltered path is always available.
- The **operator UI opens on the preset** and always shows a one-interaction "Show all events"
  escape, per EVD-8.

### D4. Filtering — server-side, over the whole chain

`audit-service` gains equality predicates on **actor**, **outcome**, and **edge**, applied **before**
the `LIMIT`, composed with the keyset cursor.

This **answers** the v0.1.0 rule rather than overriding it. Read the rule precisely: the harm named
is that the operator fetches 50 events, filters *those 50* in the browser, sees 3 rows, and believes
those 3 are all the matching events in the chain — a result indistinguishable from a complete answer
while being an arbitrary window. `WHERE actor = $1 AND seq < $2 ORDER BY seq DESC LIMIT 50` returns
**the newest 50 matching events in the entire chain**: a complete answer to a well-posed question.

> The rule was never "the audit view must be unfiltered." It was **"a filtered view must be truthful
> about what it filtered over."** Server-side filtering satisfies it; client-side filtering never
> can.

**Client-side filtering of a page remains forbidden** — the v0.1.0 prohibition stands, unamended, and
the `Audit.tsx` header comment is updated to record *why* it stands and what now supersedes its
scope.

### D5. Disclosure — total chain length, not a hidden-count complement

The page envelope carries the filtered/preset state and **`chainLength`**, derived from `MAX(seq)`
(O(1) on the primary key) — **not** a count of what was excluded, which would require a second
aggregate over the complement and would not scale. The operator is told *"showing governed actions —
the full chain has N events"*, which is both honest and the number they actually want.

`nextCursor` semantics are preserved verbatim: emitted only when a full page came back.

### D6. Live detail refresh (E-07) — targeted, panel-scoped polling

The charter-detail lists (plan requests / context sources / runs / scoped memory) currently fetch
once on selection (the deliberate FE-22 no-auto-poll precedent), so a live charter reads as stale.

**Accepted:** poll the open detail panel's reads on a fixed short interval **only while the panel is
open and the charter is `active`**; stop on paused/retired/closed and on panel close; **pause when
`document.visibilityState === "hidden"`** (an unattended cockpit left open overnight must not poll
until morning); surface an "updated Xs ago" affordance.

Safe by as-built finding 6: these are operator read routes that **chain nothing**, so polling
manufactures none of the noise D3 exists to manage. The catalog's thrashing concern is about
*global* polling; one open panel is a bounded, self-limiting cost.

**Deferred — SSE / event push.** It is the correct long-term mechanism and the wrong thing to add to
the api surface two releases before the E-06 OpenAPI freeze.

### D7. Charter-scope accrual (E-08) — resolve at read, do not rewrite history

A template Run's actor is `workstream:<id>` (`ticker.go`) so it accrues to the charter's scope; an
adaptive Run's actor is `agent:<composer>` (`adaptive_internal.go` passes `body.AgentSubject` to
`LaunchBoundedProposed`) so `scopeForActor` sends it to `run:<id>`. The charter-detail widget queries
`scopeType: "workstream"` only, and therefore **never shows adaptive Runs' accrued memory**.

Two fixes were considered:

- **Rejected — write-side (change `scopeForActor` so adaptive Runs scope to the charter).** It leaves
  all history wrong and forces a backfill decision; backfilling would rewrite `scope_type`/`scope_id`
  so the row **disagrees with its own already-chained `memory.written` event** — the exact
  evidence-integrity failure this ADR exists to prevent.
- **Accepted — read-side.** The charter-detail memory read resolves the charter's scope to include
  run-scoped rows for Runs bound to that charter's grant. This fixes **past and future**, needs **no
  migration**, contradicts **no chained event**, and changes **no scope derivation** — so ADR-0064
  §1/MEM-2 is preserved, not amended. It is additive, and it is the **same CTE shape `ReadForAgent`
  already uses**.

The framing that matters: the agent read plane already does this correctly and the operator's does
not, so in the charter-detail view **the operator currently sees less than the composer does**. For a
governance product that is an inversion, which is why this item is worth more than its P3/size-S
billing in `backlog_v4.csv` (B-029).

**No containment change.** Both `run:<id>` and `workstream:<id>` for an adaptive Run resolve to the
**same grant** (`ws.GrantID` is what `LaunchBoundedProposed` is handed). The visible set is
unchanged; only which view surfaces it changes. The v0.11 ladder re-proves this: WD-9's N1/N3
cross-grant negatives stay green.

### D8. The gate — no new WD (re-tested, not inherited)

The Beta-1 path ratified "no new WD" for v0.11 under structural rule #2 (*a new WD-N is minted **only**
when a release adds new governed capability*). That was re-tested against the as-built at this
consult rather than assumed:

| Epic | New governed capability? | |
|---|---|---|
| E-01 | New query params on an existing operator GET already gated by `operatorSubject`. No new edge, actor, grant, token, or consent moment. | **No** |
| E-07 | Polling an existing GET more often. Chains nothing (finding 6). | **No** |
| E-08 | Memory scope **is** a containment boundary, so this needed a real look — but both scopes resolve to the same grant (§D7). The label moves; the visible set does not. | **No** |

**Gate = WD-1..10 full regression + browser smoke + a §human spot-check.** Confirmed.

### D9. Fragment discipline

New parameters are **new named components** (direction, actor, outcome, edge, preset) referenced by
`/v1/events` and `/v1/audit/events` **only**. `LimitParam` and `CursorParam` are **not edited** — they
are shared by every list route in the product (as-built finding 4). `task codegen` drift stays zero
(VER-2). The api read-proxy forwards the new params and preserves its own-service-token posture
unchanged.

## Security posture

This release adds **no** capability, edge, actor, token, credential, or egress path, and **no write
path of any kind**. The chain authority's write gate is untouched and still service-token-only. The
new surface is **read** on an already-gated operator route.

The residual worth naming: **a default view that hides events is a place where evidence can be lost
by omission.** EVD-8 is the control — the hiding is server-side and named, the preset is versioned
and enumerated in the criteria, the excluded classes are content-free lifecycle facts only, the whole
chain's size is always disclosed, and the unfiltered view is always one interaction away. **No event
is ever unretrievable, and no rendered row is ever a row that is not an event.**

## Consequences

- **The core evidence surface becomes usable at production volume**, closing the one finding flagged
  v1.0-BLOCKING and clearing REQ-003's read-surface gap (G2) — capture was already complete.
- **The append side is byte-untouched**: no migration on `audit-service`, no change to
  `chain.Compute`, no change to any event's vocabulary. The forward-only migration policy is honored
  by *not needing* a migration.
- **The v0.1.0 no-client-side-filtering rule survives intact** and is now backed by a stated
  invariant instead of a code comment.
- **This is the last substantial growth of the audit surface in the fragment**, and it lands
  **before** the E-06 freeze rather than needing an exception after it.
- **Bounded work:** the store's `List`, the audit-service handler, the fragment + codegen, the api
  proxy's forwarded query set, `Audit.tsx`, one polling effect in `Workstreams.tsx`, and one widened
  charter-detail memory read. No new service, unit, env, key, or dependency.
- **Accepted cost:** a filtered or preset view cannot be chain-verified in the browser by walking the
  page. EVD-8 converts that from a silent defect into a disclosed property with a one-click escape.

## Red-team

| # | Attack / failure | Closed by |
|---|---|---|
| 1 | An operator is shown a filtered page and believes it is the whole chain | EVD-8 (D1) + the mandatory filtered/preset disclosure and `chainLength` (D5) |
| 2 | An operator is shown a filtered page and believes it is a verifiable chain segment | EVD-8: filtered views are labelled query results; the contiguous unfiltered timeline is always one interaction away |
| 3 | Evidence is destroyed by suppressing lifecycle events | D3 rejects write-side suppression outright; every event is still chained and still returned unfiltered |
| 4 | The evidence view renders rows that are not events | D3 rejects read-time collapse/rollup; every row is exactly one stored event |
| 5 | A mutable, unauthenticated field is smuggled into the evidence record | D3 rejects the stored `significance` column in both its hashed and unhashed branches |
| 6 | The preset is silently widened to hide inconvenient events | Membership lives in one enumerated place, is versioned (`governed-actions@1`), is named in the response envelope, and is criteria-governed (drift ⇒ new ADR); the unfiltered path can never be removed |
| 7 | Client-side filtering returns by the back door | The v0.1.0 prohibition stands unamended (D4); filtering exists only as a server-side predicate over the whole chain |
| 8 | Reverse paging skips or duplicates events under concurrent appends | Keyset (not offset) pagination in both directions; a descending window from a fixed cursor is stable because new rows land at higher `seq`, outside it. Proven by an explicit live test (v0.11 criteria) |
| 9 | Detail polling manufactures the very noise D3 manages | Operator read routes chain nothing (finding 6); polling is panel-scoped, active-only, and visibility-paused (D6) |
| 10 | The E-08 read widening leaks cross-grant memory | Both scopes resolve to the same grant (D7); WD-9 N1/N3 cross-grant negatives stay green in the gate |
| 11 | A shared fragment param is edited and silently changes every list route | D9: new named components only; `LimitParam`/`CursorParam` untouched; codegen drift zero |

**Verdict.** No new capability, no new attack surface, no write path. The single genuinely new risk is
**evidence hidden by a default**, and it is controlled by making the hiding server-side, named,
versioned, enumerated, disclosed, counted, and always reversible in one interaction.

## Deferred (recorded)

- **Upstream re-ask backoff (the noise at its source).** 601 of 635 events came from a plan-request
  open/expire cycle repeating every 60s because nobody answered; a charter that has re-asked the same
  unanswered question 300 times is arguably malfunctioning. Candidate fixes — back off the re-ask
  interval after K unanswered expiries, or hold one request open rather than expire-and-reopen —
  **change governed lifecycle behavior**, which under structural rule #2 would mint a new WD and break
  the v0.11 gate. **Recorded as a v1.x backlog row (E-20-adjacent), deliberately out of v0.11.** This
  ADR governs the *display* of the volume; it does not claim to fix its cause.
- **SSE / event push** for live detail (D6) — deferred past the E-06 OpenAPI freeze.
- **Full-text / target search, time-range filtering, saved views, CSV export** on the audit surface —
  not required to close the blocking finding; defer to demand.
- **Per-column sorting on non-`seq` columns** — rejected on EVD-8 grounds (D2), not merely deferred.
  A future need would require a new ADR arguing past the invariant.
- **In-browser chain verification of a page** (walking `prevHash`/`chainHash` client-side as a visible
  affordance) — the data is already returned; the affordance is not built. Recorded as a natural
  companion to the E-06 security-audit capstone.
- **Backfill of existing run-scoped adaptive accruals** — rejected in D7 (it would make rows disagree
  with their own chained events), not deferred.
- **Multi-operator ACL on the audit read surface** — the single-operator posture (E-06/S-23) governs;
  unchanged.

## Relation to prior ADRs

- **ADR-0045 (outbox + chained hash)** — the append path, the advisory-lock serialization, and the
  outbox are untouched; this ADR is entirely on the read side. Extends, does not edit.
- **ADR-0049 (hash-in-chain, content-free events)** — no event's stored shape, vocabulary, or hash
  input changes. Preserved.
- **ADR-0044 (migrations; `audit-service` forward-only)** — load-bearing in D3's rejection of a stored
  classification column. No `audit-service` migration is added. Preserved.
- **ADR-0043 / ADR-0047 (chi + `oapi-codegen`; the one fragment, two consumers)** — new params are
  additive named components; codegen stays byte-deterministic. Preserved.
- **ADR-0064 (Persistent Scoped Memory)** — §1/MEM-2 scope derivation is **unchanged**; D7 resolves
  scope at read time only. Extends, does not edit.
- **ADR-0065 (Adaptive Workstreams)** — supplies the lifecycle-event volume this ADR governs the
  display of; the always-park invariant and the plan-request lifecycle are untouched. Preserved.
- **ADR-0066 (Layer 1)** — the per-read chained agent planes are untouched; D6 relies on the fact that
  the *operator* planes deliberately are not chained. Preserved.
- **ADR-0046 (capability edges)** — no edge is added, lit, or reserved. Preserved.

## Ratification (resolved 2026-07-26)

All nine decision points were signed off on the war-room recommendation, and are now binding:

1. **D1 — EVD-8, the chain-contiguity invariant**, as the spine of the audit read surface.
2. **D2 — Reverse cursor, newest-first default, seq-direction ordering only**; E-01/S-03 narrowed from
   per-column sorters (a recommendation-level divergence from the ratified catalog).
3. **D3 — Lifecycle-noise policy = a versioned, named, read-time preset** (`governed-actions@1`);
   write-side suppression, read-time rollup, and a stored `significance` column all rejected.
4. **D4 — Server-side filtering over the whole chain** answers the v0.1.0 rule; client-side filtering
   stays forbidden.
5. **D5 — Disclosure via total `chainLength` from `MAX(seq)`**, not a hidden-count complement.
6. **D6 — E-07 = targeted panel-scoped polling**, active-only and visibility-paused; SSE deferred past
   the freeze.
7. **D7 — E-08 = read-side scope resolution, no backfill** (a recommendation-level divergence from
   B-029's write-side framing).
8. **D8 — Gate = WD-1..10 regression + smoke + §human spot-check, no new WD** — re-tested against the
   as-built, not inherited.
9. **D9 — Fragment discipline**: new named components only; shared `LimitParam`/`CursorParam`
   untouched; codegen drift zero.

Everything else = the CEO-ratified Beta-1 path (`docs/requirements/epic_story_catalog_v4.md` §Part 4)
plus this consult's as-built review (findings 1–7, of which 6 and 7 changed a recommendation).
