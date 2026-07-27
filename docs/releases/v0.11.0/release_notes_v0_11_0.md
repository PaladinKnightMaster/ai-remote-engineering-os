# AI Remote Engineering OS — v0.11.0 release notes

**Status: release candidate** — GATE-1 (full WD-1..10 regression, both stacks) and
GATE-2 (browser smoke) are **green**; OI-18..20 hold; the CEO §human GATE-3
spot-check (the four operator-visible behaviors, driven live in a real host
browser) is the **remaining gate**, scripted separately by the War Room
Commander and captured at Phase B. These artifacts stay at **release-candidate**
status until GATE-3 is signed off — do not treat this document as a released
record until the companion closure commit flips it.

Baseline: **v0.10.0** (local annotated tag `v0.10.0` → `e0fee10`, no remote)
on v3.0.1.

## Why (the first of the three-release Beta-1 path)

v0.11.0 is **"Operator Experience"**, the first of the ratified Beta-1 path
(`docs/requirements/epic_story_catalog_v4.md` §Part 4): **v0.11 Operator
Experience → v0.12 Access & Onboarding → v1.0 MVP Beta 1**. It fixes **defects
on already-shipped functionality** and adds **no new governed capability** —
deliberately, because v0.12 adds surfaces and v1.0 freezes the API. The
trigger was measured live at the v0.10.0 §human WD-10 walkthrough
(2026-07-24): **635 audit events, 601 of them (95%) adaptive-charter lifecycle
noise, the newest entries 13 "next page" clicks deep** — flagged
**v1.0-BLOCKING**, because the audit trail is this product's core evidence
claim.

## What landed

- **E-01 — Audit read surface (REQ-003, B-014, gap G2 — v1.0-BLOCKING,
  CLOSED).** `audit-service`'s store gains a **descending keyset path**
  (`WHERE seq < $1 ORDER BY seq DESC LIMIT $2`) alongside the existing
  ascending one (AUD-22); a live test proves both directions **skip no event
  and duplicate none, including across a concurrent append landing mid-page**
  (AUD-23). The operator surface **defaults to newest-first** (AUD-24).
  Equality filters on **actor/outcome/edge**, applied server-side before the
  `LIMIT` and composed with the cursor, return matches **from the whole
  chain** (AUD-25) — client-side filtering of a fetched page stays forbidden,
  unamended since v0.1.0 (AUD-26). Sorting stays **seq-direction only**; no
  per-column sorter exists for any other column (AUD-27, ADR-0067 §D2).
  `audit-service` owns exactly one named, versioned, read-time preset,
  **`governed-actions@1`**, excluding `{plan.requested, plan.expired}`
  (AUD-28) — a query predicate, never write-side suppression, never a
  rollup, never a stored column (EVD-11, ADR-0067 §D3). The response
  envelope discloses the applied preset/filter state and **`chainHead`**
  (`MAX(seq)`, O(1), never a complement count — AUD-29). `outcome`'s
  nullability is handled NULL-safe (`COALESCE(outcome,'') <> ALL(...)`), and
  a live test proves a NULL-outcome event still appears under the preset
  (AUD-31 — the "evidence hidden by a default" failure mode). Migration
  **0002** adds three read-only composite indexes — `(actor, seq DESC)`,
  `(outcome, seq DESC)`, `(edge, seq DESC)` — no column, constraint, or
  trigger (PERF-7); `EXPLAIN` proves `Index Scan`, never `Seq Scan`, for the
  unfiltered, preset, and each filtered read (PERF-10). `Audit.tsx` opens
  newest-first on the preset with a disclosure banner driven **only** by the
  response envelope (never local request state) and a one-interaction "Show
  all events" escape (FE-31..33, EVD-8).
- **E-07 — Live detail refresh (REQ-007, B-019, gap G6).** The charter-detail
  reads (detail / plan requests / context sources / scoped memory / runs)
  poll on a fixed 10s interval **only while the panel is open**, the charter
  is `active`, and the tab is visible (FE-27/28, PERF-11); an "updated Xs
  ago" affordance surfaces freshness (FE-29); a live assertion proves an
  open, polling panel chains **zero** audit events over its lifetime
  (FE-30).
- **E-08 — Charter-scope accrual (REQ-014, B-029).** The charter-detail
  memory read now resolves the charter's scope to include **run-scoped rows
  for Runs bound to that charter's grant** — the same CTE shape
  `ReadForAgent` already uses (MEM-9). `scopeForActor` and ADR-0064 §1/MEM-2
  stay byte-unchanged; no accrual writes to a different scope than before
  (MEM-10); no existing row's `scope_type`/`scope_id` is rewritten — no
  backfill, no migration (MEM-11). WD-9's N1/N3 cross-grant negatives stay
  green, re-proving containment: both `run:<id>` and `workstream:<id>` for
  an adaptive Run resolve to the **same grant**, so the visible set is
  unchanged — only which view surfaces it changes (MEM-12). A live
  assertion shows a completed adaptive Run's accrued pointer appearing in
  the charter-detail widget for Runs accrued **before** this release as well
  as after (MEM-13).

**Hard constraint held.** v0.11.0 adds **no** capability, edge, actor, grant,
token, credential, consent moment, egress path, migration category, service,
unit, env var, key, or third-party dependency, and **no write path of any
kind**. The `audit-service` append side is byte-untouched: `chain.Compute`'s
input domain is unchanged, no event's vocabulary changes, and the only
migration is the read-only index migration (0002).

## ADRs

- **ADR-0067 ("Audit read semantics")** — the sole new ADR (Accepted
  2026-07-26; extends ADR-0045/0049/0044/0043/0047/0064/0065/0066, preserves
  ADR-0046 [no edge added, lit, or reserved]; edits no Accepted ADR —
  G2-clean). Nine ratified decision points (D1-D9), three of them
  recommendation-level divergences from the ratified epic catalog:
  - **D2** narrows E-01/S-03 from per-column sorters to seq-direction-only
    (per-column sorting over a 50-row page is the partial-page
    misrepresentation the v0.1.0 rule banned, wearing new clothes).
  - **D5** renames the disclosed quantity `chainLength` → **`chainHead`**
    (same `MAX(seq)` O(1) computation, decided unchanged at ADR-0067; the
    rename corrects a misdescription — `seq` is `BIGSERIAL` and `nextval` is
    non-transactional, so a rolled-back `Append` burns a value and leaves a
    gap, meaning `MAX(seq)` **overstates** a row count if reported as "the
    chain has N events").
  - **D7** re-frames E-08 from a write-side accrual change to a read-side
    scope resolution (a write-side fix would force a backfill decision that
    rewrites `scope_type`/`scope_id`, making a row disagree with its own
    already-chained `memory.written` event).
  - Also settled: **D3** (the lifecycle-noise policy = a versioned, named,
    read-time preset, not write-side suppression/rollup/a stored column),
    **D4** (server-side filtering over the whole chain answers, rather than
    overrides, the v0.1.0 rule), **D6** (E-07 = targeted panel-scoped
    polling, SSE deferred past the E-06 freeze), **D8** (the gate = WD-1..10
    regression + smoke + §human, no new WD — re-tested against the as-built,
    not inherited), **D9** (new named fragment components only;
    `LimitParam`/`CursorParam` untouched).
  - **Amended twice, both before any code existed** (the T-105 consult, one
    commit after acceptance): EVD-10 originally banned *all*
    `audit-service` migrations, contradicting PERF-7 (no index on
    `actor`/`outcome`/`edge` existed, so a selective filter would degrade to
    a full scan) — amended to permit index-only DDL. AUD-29 was renamed
    `chainLength` → `chainHead` per D5 above.

## Verification

`task e2e` **PASS — 346.8s**, audit chain intact, **613 events**: WD-1..10
all green (WD-10's happy path + six live negatives + the elevated WD-4..9
regression). `task wd1` **PASS** (bring-up + walkthrough ≈ 11m33s wall
clock; the go-test leg **651.7s against the 1500s ceiling, 43%**), audit
chain intact, **222 events**, at the **true production posture** (60s
adaptive-schedule floor, 60s context-indexer floor, neither overridden) — the
same real-systemd installed stack v0.10.0 proved, now carrying the v0.11
audit-read-surface + live-detail-refresh + charter-scope-accrual code paths
with **zero install-wiring diff** (OI-18, independently verified by the War
Room Commander: `git diff v0.10.0 HEAD -- install.sh systemd/ Caddyfile` is
empty). `task smoke` **14/14 GREEN** (real Chromium): `audit.smoke.spec.ts`
(newest-first + preset disclosure + server-side filtering),
`context-sources.smoke.spec.ts`, `governed-egress.smoke.spec.ts`,
`grants.smoke.spec.ts`, `invocation-approval.smoke.spec.ts`,
`memories.smoke.spec.ts` (×2), `runs.smoke.spec.ts` (×4),
`workstreams.smoke.spec.ts` (×2, including the FE-30 zero-audit-events-while-
polling assertion). Offline bundle rebuilt from HEAD as `0.11.0`: **116 MB
unpacked / 42 MB gzipped** (v0.10.0 baseline: 116 MB / 41 MB — the OI-4 caps
[500 MB / 150 MB] hold with wide margin; OI-19). `task licenses` — every Go
service (`api`, `audit-service`, `connector-service`, `mcp-gateway`,
`policy-service`, `session-broker`) and the JS production dependency set both
clean against the allowlist; **zero new third-party dependency** confirmed
two ways: the license gate itself, and `git diff v0.10.0 HEAD --stat -- \
'**/go.sum' go.work.sum pnpm-lock.yaml` is **empty** (OI-20).

## As-built / findings

Three defects were caught and fixed **before merge** (Commander review or the
implementer's own live diagnosis), none shipped, all recorded here per the
project's fail-loud / no-silent-fix discipline:

1. **T-105 — the OR-guard made the new indexes unreachable under a generic
   plan.** The first pass at `ListQuery.build()` used the idiom
   `($n::text IS NULL OR col = $n)` for each optional filter. Custom plans
   (pgx's per-connection statement cache produces these for a statement's
   first few executions) fold in the literal value and hide the defect;
   only a **forced generic plan** exposes it — under one, the planner
   cannot know at PREPARE time whether the parameter will be NULL, so the
   clause can only ever degrade to a post-fetch `Filter`, never an `Index
   Cond`, silently defeating migration 0002's composite indexes and
   degrading every filtered read to exactly the backward PK-scan-with-Filter
   PERF-7 exists to rule out. Fixed by appending each filter clause to the
   statement text **only when that filter is set** (a bare `AND actor =
   $n`), never an OR-guard; placeholders renumbered as clauses are
   appended; values remain always-bound `$n` parameters. Verified live via
   `PREPARE`/`EXPLAIN EXECUTE` under `plan_cache_mode = force_generic_plan`
   (commit `c8a3c76`). A related test-quality gap (too few distinct
   `outcome` values, making the generic-plan cost estimate noisy) was found
   and fixed in the same pass.
2. **T-106 — the shared-`PageMeta` trap, caught at consult.** `PageMeta` is
   referenced by eight operations (resources, runs, grants, workstreams,
   memories, context-sources, both audit routes); folding `chainHead`/
   `preset`/filter-echo fields into it would have silently changed every
   list response in the product. A **new** `AuditPageMeta` schema was
   introduced instead, referenced only by the two audit operations — the
   same CON-1 discipline that keeps `LimitParam`/`CursorParam` untouched.
3. **T-107 — a per-keystroke fetch, and a stale-response race, both caught
   by Commander review.** The first pass wired the actor/outcome filter
   inputs directly to `onChange`, which fires per keystroke: typing
   `"operator:admin"` issued 14 filtered chain reads plus 14 `MAX(seq)`
   queries against the very surface this release exists to make fast at
   production volume. Fixed with local draft state committed only on blur
   or form submit (Enter / an explicit "Apply" button — a bare
   `<form onSubmit>` alone does not catch Enter once a form has more than
   one text input and no submit button, which the browser smoke leg caught
   independently). A second, independent defect: two committed requests can
   still race (a slow response to an earlier filter resolving after a fast
   response to a later one); fixed with a ref-held monotonic `requestSeq`
   guarding every response (items, page meta, cursor, error) so only the
   **latest** request's response is ever applied — otherwise EVD-8's
   disclosure banner could describe a filter that did not produce the rows
   on screen, a race rather than a decision.

**The WD-10 flake (T-110 Task 1 — diagnosed, not papered over).** During
verification, `tests/e2e/wd10_test.go`'s leg 4 failed once with
`context.read count for composer %s = 2, want exactly 1`, then passed on
rerun. **Root cause: scenario (a), a test/harness over-count, not a chain
defect.** Leg 2's `waitForSearchHit` polls `context.search` under the
**same composer actor** while it waits for the index sweep to surface the
term, and `SearchForAgent` chains exactly one `context.read` event **per
call, hit or miss** (`services/api/internal/ctxindex/store.go`'s fail-loud
N4 rule — independently proven by the unit-level `TestSearchForAgent`: 7
calls → 7 events). Leg 4's original assertion counted **all** `context.read`
events for the composer since the actor was first used, so it silently
depended on leg 2 settling in exactly one poll iteration **and** on leg 4's
own event not yet being outbox-relayed by the time the poll helper's first
check ran — a harness race on event-visibility timing, not on the SEC-33
per-call invariant itself. **Fixed by scoping the assertion precisely**:
capture the composer's `context.read` count immediately before leg 4's own
`context.search` call, then assert the count grows by exactly
`baseline + 1` — the actual invariant under test, independent of how many
prior calls leg 2 made. No production code changed. Per the T-110 brief's
explicit instruction, this is recorded here because it is a change to a
ratified v0.10 WD-10 gate assertion: `tests/e2e/wd10_test.go`, commit
`034ba3e` — a loosened gate is never a silent edit, and this one was
precision-scoped, not weakened (it still asserts an exact count, just the
correct one).

**Accepted diff-budget overages (VER-8), recorded not hidden.** Three
tickets exceeded the ≤500 LoC guideline, all driven by criteria-mandated
live tests, `EXPLAIN`-plan proof, generated code, and browser evidence
rather than scope creep:

| Ticket | Files | Insertions | Driven by |
|---|---|---|---|
| T-105 (+follow-up) | 7 | 836 | Live PG store tests for both cursor directions × filter dimensions × the preset × `chainHead`, plus the generic-plan `EXPLAIN`/`PREPARE` proof added at the follow-up review |
| T-106 | 8 | 893 | Generated `gen.go` (×2 services) + `openapi.ts` (mechanical, codegen-produced) alongside the fragment + handler + proxy + their tests |
| T-107 | 3 | 632 | `Audit.tsx` + its Vitest suite + `tests/smoke/audit.smoke.spec.ts` (FE-34's mandatory browser-rendered-evidence DoD) |

T-108 (343) and T-109 (291) stayed within budget.

## Deferred (recorded, with promotion triggers — ADR-0067 §Deferred, criteria §14)

- **The upstream re-ask backoff** (back off the re-ask interval after K
  unanswered expiries, or hold one request open rather than expire-and-reopen)
  — 601 of 635 measured events were this ticker noise. **Deliberately kept
  out of v0.11.0**: it changes **governed lifecycle behavior**, which under
  structural rule #2 mints a new WD-N plus its own ADR + gate. v0.11.0
  governs only the **display** of the volume (the audit read surface), not
  its cause. Minted as backlog row **B-031** (v1.x, trigger-gated).
- **SSE / event push** for live detail — post-E-06; the wrong order two
  releases before the OpenAPI freeze.
- **Full-text/target search, time-range filters, saved views, CSV export**
  on the audit surface — demand-gated, not required to close the blocking
  finding.
- **In-browser chain verification** (walking `prevHash`/`chainHash`
  client-side) — a natural companion to the E-06 security-audit capstone;
  the data is already returned.
- **A partial index on the preset predicate** — rejected for coupling the
  lens to the schema; promotion trigger is the noise ratio exceeding ~99%
  (PERF-8).
- **Per-column sorting on non-`seq` columns** — **rejected, not deferred**
  (EVD-8/ADR-0067 §D2); a future need requires a new ADR arguing past the
  invariant.
- **Backfill of existing run-scoped accruals** — **rejected, not deferred**
  (ADR-0067 §D7); it would make rows disagree with their own chained events.
- Consent modals (E-05), mobile/QR (E-02), deployment profiles (E-03),
  onboarding (E-04) — **v0.12** per the ratified Beta-1 path.
- OpenAPI freeze, security-audit capstone, compat policy — **v1.0** (E-06
  only).

## Process note

Built on the Commander–Implementer split: the opener (ADR-0067 + criteria,
docs-only) plus five implementation tickets (T-105..T-109), each
Dockerized-verified and Commander-reviewed before `main` advanced, then this
closure ticket (T-110). Three review-caught defects (above), zero
ADR/criteria divergence beyond the two pre-code amendments recorded at the
T-105 consult (EVD-10, AUD-29 — both before any implementation code
existed, both already reflected in the accepted criteria document, not a
drift event under the criteria-drift rule).

## Next — T-110 Phase B (this ticket's remaining half)

**Not done here.** The CEO §human GATE-3 spot-check (the four
operator-visible behaviors in `docs/releases/v0.11.0/v0.11.0-release-criteria.md`
§2), scripted by the War Room Commander as `verification_runbook.md` Step 6,
driven live in a real host browser per the recorded Windows-host route. On
sign-off: these artifacts flip to **released**, `main` fast-forwards, and the
local annotated tag `v0.11.0` lands (no remote, per repo doctrine). Beyond
that: **v0.12 — Access & Onboarding** per the ratified Beta-1 path.
