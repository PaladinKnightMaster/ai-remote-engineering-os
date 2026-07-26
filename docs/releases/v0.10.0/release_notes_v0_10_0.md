# AI Remote Engineering OS — v0.10.0 release notes

**Status: released** — local annotated tag `v0.10.0` (no remote, per repo
doctrine); the CEO §human WD-10 walkthrough was driven **live and hands-on**
(2026-07-24) and signed off — chartering an adaptive Workstream in the rendered
cockpit, registering a charter-bound context source over a real directory behind
the GOV-11 consent dialog, watching the in-process indexer sweep it (76 chunks
across 11 files), the composer reading that context over the gateway
(`context.search` → 5 ranked chunks, one content-free `context.read` chained on
the `agent-workspace` edge), a plan composed from it parking as a `proposed` Run,
the operator reading the agent's verbatim command in the informed preview and
approving it, the unchanged engine driving it to `completed` with a content-free
accrual, a live source edit **and** a deletion each reflected on the very next
`context.search` with no rebuild call, and the **N1 cross-grant containment
negative proven with a positive control** (charter B reads its own source;
charter A gets an empty result for B's content *and* for B's own `workstreamId`
— no oracle; containment symmetric both directions; all nine `context.read`
events chained, including the empty ones). WD-10 is green on the compose e2e
stack (`task e2e`, PASS 366.8s, chain intact 616 events) and on the real
installed stack (`task wd1`, PASS 651.6s against the 1500s ceiling, chain intact
222 events, at the true production posture — the 60s indexer floor and the 60s
schedule floor, neither overridden). v0.10.0 — **"Live/Context Index"** — is the
release where the
composer's read plane completes: it already read accrued **memory** (Layer 2,
v0.8/v0.9); v0.10 adds a fresh, deterministic view of the operator's **project
context** (docs/runbooks/repo/tickets) — the reserved `agent-workspace` edge is
enforced, read-only, on exactly one path (per
`docs/releases/v0.10.0/v0.10.0-release-criteria.md`; machine-readable coverage
in `release_manifest_v0_10_0.json`; how-to-verify in
`verification_runbook.md`).

Baseline: **v0.9.0** (tag `053997d`) on v3.0.1.

## Why (the read plane completes)

Layer 1 (Live/Context Index, REQ-013/B-009) sat deferred through four releases
as a feed with no consumer. v0.9 (ADR-0065) gave it a **named consumer** for
the first time: the composer's plan-time context. v0.10 closes the other half
of that read plane: **remember (L2) + know the current context (L1) → compose
→ govern → act → accrue.** The composer today plans against accrued memory but
had no view of the current state of the operator's repo/docs/runbooks; v0.10
gives it one — deterministically, offline, with zero model dependency.

## What landed

- **The context source (R1, CTX-1).** An operator-registered local/mounted
  directory (docs/runbooks/repo/tickets), **bound to one charter**
  (`workstream:<id>`) — so only that charter's composer reads it. Migration
  0014 adds `context_sources` (charter-bound, owner-scoped, staleness
  metadata). Registration/list/remove are typed, CSRF-gated `human-agent`
  routes (`/v1/context-sources*`). The file reader is **confined to the
  registered root** (SEC-33, K5): `../` traversal, absolute paths, and
  symlink-escape are all rejected before any file is touched — proven by a
  live negative (N2).
- **The deterministic FTS index (R3, CTX-2).** Migration 0014 also adds
  `context_chunks`: source content chunked (by document/heading for docs and
  tickets, by a fixed 80-line window for code, K2) and indexed as a generated
  `tsvector` column with a GIN index — **native Postgres full-text search,
  zero embeddings, zero new extension, zero new third-party dependency**.
  Retrieval ranks by `ts_rank` (lexical) + recency only — never learned.
- **The in-process incremental indexer (D3, CTX-3).** Migration 0015 adds
  `context_files` (per-file mtime/size/hash tracking). A background indexer
  rides the api's existing background goroutine (the ticker/reaper precedent
  — no new unit, env, or key): a sweep compares each file's **mtime + size
  first and hashes only changed candidates** (K6 — no full re-hash per pass);
  unchanged files are never re-read; vanished files are removed; each source
  carries a **staleness signal** surfaced to both the composer (on read) and
  the operator (on the source view). An unreadable/absent root marks the
  source **errored + stale** rather than crashing the sweep (CTX-5).
- **The agent read plane (R2/R4, CTX-4).** A sixth gateway control-plane tool,
  **`context.search`**, on the exact `memory.read` forward pattern: the
  keyless gateway forwards `{agent, query, optional workstreamId}` to a
  subject-pinned (`subject=="mcp-gateway"`) api internal route
  (`/v1/internal/context-searches`), which is the sole authority.
  `SearchForAgent` reuses the `ReadForAgent` CTE skeleton over a different
  corpus: live grants → each grant's charter → context sources bound to that
  charter → FTS over their chunks. Cross-grant/foreign-source reads are
  **impossible by construction, never by filter**; an out-of-set
  `workstreamId` answers **empty, never an error** (no oracle, N1). Results
  are clamped 50/200 (K4) and the tool rides the gateway's per-agent
  in-flight cap. `context.search` carries the reserved **`agent-workspace`**
  edge — the **second** reserved edge lit (after `agent-memory` at v0.9); the
  edge *set* is unchanged (ADR-0046 preserved); `agent-repo` **stays
  reserved** for Git Intelligence (REQ-015).
- **Per-read chaining (AUD-21, SEC-33).** Every `context.search` call chains
  **exactly one content-free `context.read` event**, enqueued inside the
  read's own transaction — no event, no data (fail-loud, proven even on
  zero-result calls, N4). Operator context-source management chains its own
  `human-agent`-edge events, `context.source.registered` / `…removed` (one per
  register/remove; a re-remove is a silent no-op, no duplicate event); the
  root path never appears in either event.
- **The operator surface (FE-26, GOV-11).** A "Context sources" section on the
  Workstreams charter detail: lists a charter's bound sources with their
  staleness signal (active/errored badge + relative last-indexed time),
  registers a new source behind a confirm naming the prompt-injection risk
  honestly (a bound source's content becomes readable by this charter's
  composer agent), and removes one behind a confirm stating the recall limit
  (removal stops future reads but cannot recall past ones). No new
  design-system components.
- **Injection stays mitigated by an existing invariant (N6).** A plan grounded
  in indexed content still **always parks** as a `proposed` Run for human
  review (the ADR-0065 always-park invariant carries unchanged) — no new
  mitigation machinery was needed.
- **Offline / installed (OI-17).** **Zero install-wiring diff** (independently
  verified: `git diff v0.9.0 HEAD -- infra/offline/install.sh
  infra/offline/systemd/ infra/offline/Caddyfile` is empty — the only
  `infra/offline/` change since v0.9.0 is `run-wd1.sh`, test-harness wiring
  only): migrations 0014+0015 ride `install.sh`'s existing migrate loop; the
  indexer rides the already-running api goroutine; `context.search` rides the
  already-installed gateway binary + Caddy `/mcp` route — no new systemd
  unit, no new Caddy route, no new key material, no new required env
  (`AIREOS_CONTEXT_INDEX_SECONDS` is an optional override the installer never
  writes). The full WD-10 ran installed at the **true production posture**
  (the 60s adaptive-schedule floor AND the 60s context-indexer sweep floor,
  neither overridden). Bundle rebuilt from HEAD as `0.10.0`: 116 MB / 41 MB gz
  (OI-4 holds; identical to v0.8/v0.9).

## As-built note: `internal/ctxindex`, not `internal/context`

ADR-0066 and `v0.10.0-release-criteria.md` both name the seam
`internal/context` in prose. At **T-097** the implementer renamed it to
**`internal/ctxindex`** on a Commander review ruling: `package context` in a Go
source file collides with the standard library's `context` package (imported
by nearly every file in the module for cancellation/deadlines) — a recognized
Go anti-pattern, not a design change. T-096 (the ticket immediately before)
had wired no external callers into the old name, so the rename was a clean,
same-ticket-ladder sibling-seam fix with zero behavioral difference. Per this
ticket's constraint, ADR-0066 and the release criteria are **not** edited to
match (both are change-controlled documents; drift there requires a new ADR) —
this note is the reconciliation. Every **living** doc (`module-map.md`,
`agent-permission-model.md`, this release-notes file) uses the correct
`ctxindex` name; the ADR/criteria's `internal/context` prose should be read as
"the seam that shipped as `internal/ctxindex`."

## Security posture (SEC-33 — the release's named invariant)

Agent context-index access is **read-only** (indexing is a machine sweep of
operator-registered sources; no agent write path exists), **same-grant-scoped
by construction** (the readable set derives from the caller's own live grants
→ charter-bound sources — never a caller-supplied path, never a foreign
charter), **confined** (the indexer cannot read outside a registered source
root), **staleness-honest**, **per-read chained** on the `agent-workspace`
edge (fail-loud, in-tx), and **consumed only through a plan that parks for
human review** (the ADR-0065 always-park invariant is L1's injection control —
indexed content is untrusted, exactly like shared operator memory). Nothing
new mints; the gateway stays keyless (`context.search` is a forward minted
with the gateway's own service token to a subject-pinned api route — the
`memory.read` pattern); no foreign-edge mint; broker/connector pins unchanged.
SEC-20/21/25/26..31/32 all carry; the operator `/v1/memories*` surface stays
byte-identical (L1 is a **separate** seam from Layer 2 memory).

## ADRs

- **ADR-0066 ("Layer 1 Live/Context Index")** — the sole new ADR (Accepted
  2026-07-23; extends ADR-0062/0063/0064/0065, preserves ADR-0046 [the edge
  *set* is unchanged — enforcement of the reserved eighth-listed edge, not a
  ninth]/0056/0060 and ADR-0064's operator plane; edits no Accepted ADR —
  G2-clean). The canonical ladder is through ADR-0066.

## Verification (recorded in `verification_runbook.md`)

`task e2e` **PASS — 366.8s**, audit chain intact, **616 events**: the WD-10
happy path (legs 1–8 — register a charter-bound context source, the
in-process indexer sweeps it deterministically, the composer's chained
`memory.read` AND `context.search` reads, the composed plan parks and is
approved, the engine executes and accrues, a live source edit is reflected on
the very next `context.search` without a full rebuild) plus the six live
negatives (N1 cross-grant/foreign-source containment, N2 the confined-root
guard, N3 staleness honesty, N4 per-read chaining including zero-result calls,
N5 the zero-new-dependency build-gate check, N6 the always-park invariant),
with WD-5/6/7/8/9 all elevated and green. `AIREOS_CONTEXT_INDEX_SECONDS=6` is
set in the e2e compose environment only (never by the installer). `task wd1`
**PASS — 651.6s against the 1500s ceiling (43%)**, audit chain intact, **222
events**: `TestWD10InstalledStack` (101.46s; legs 1–8, including the
freshness edit reflected on the next sweep, and **double governance** — the
plan-level park plus a flagged resource's invocation-level park) +
`TestWD10ConfinedRootInstalled` (60.19s; N2) — both at the **real production
posture**, the 60s indexer floor AND the 60s schedule floor, neither
overridden. `task smoke` **12/12** (11 pre-existing + the new context-sources
spec, real Chromium). Offline bundle **0.10.0: 116 MB unpacked / 41 MB
gzipped** (OI-4 holds, identical to v0.9.0). **OI-17 zero-install-wiring diff
independently verified**: `git diff v0.9.0 HEAD -- infra/offline/install.sh
infra/offline/systemd/ infra/offline/Caddyfile` is empty.

**Chain counts are observed values, not invariant assertions** — both legs are
timing-dependent (schedule-driven `plan.requested`/`plan.expired` cadence,
context-index sweep cadence); integrity plus per-leg exact counts are the
assertions, as established since v0.9.

**Process note:** built on the Commander–Implementer split (consults,
reviews, and this document in the Commander seat; each ticket implemented by
a dispatched implementation agent, `main` fast-forwarded only after the
Commander's diff review). Nine implementation cycles (T-096..T-103, plus the
combined ADR+criteria consult); zero slate-breaking findings; one internal
naming fix (T-097's `internal/context` → `internal/ctxindex` rename,
reconciled above) — no ADR/criteria divergence.

## Deferred (with promotion triggers)

- **tree-sitter AST / code-graph retrieval** — deferred (native FTS suffices
  for a composer that plans shell commands, not code edits; keeps the
  zero-new-dependency posture). **Recorded trigger (CEO R3 ratification
  note): when agents gain a code-EDITING capability** — code-structure
  retrieval (call graphs, symbol resolution, blast-radius) becomes valuable —
  add tree-sitter AST indexing, most naturally **alongside Git Intelligence
  (REQ-015)**. A *when*, not an *if*.
- **The `agent-repo` edge** — held reserved for Git Intelligence (REQ-015).
- **Remote-target indexing** (content that lives on a remote resource, vs. an
  operator-registered local source) — needs governed egress; deferred.
- **Operator-shared / run-scoped context sources** — v0.10 is charter-bound
  only (the tightest containment); broader scopes defer.
- **Learned ranking / computed `confidence`**, **messages/chat indexing** (no
  chat corpus), **multi-source federation** — deferred.
- **Layer 3 — Reasoning Retrieval** — hard-gated unchanged (any embedding/AI
  dependency is its own ADR against the offline + no-SaaS doctrine).
- **The `internal/*` extraction; cron; DAG; the gateway exec worker; SSH
  pooling; full rate-limiting; TR-2** — unchanged prior deferrals.

## §human findings (CEO, 2026-07-24) — UX debt, none v0.10 defects

The live walkthrough raised three UX findings. **None is a v0.10.0 defect** — every
surface this release shipped behaved as specified — and none blocked sign-off. All
three are on **pre-existing** surfaces, recorded here so they are not rediscovered:

1. **Replace the native `window.confirm` consent modals with a design-system modal.**
   GOV-11 (context-source register/remove), GOV-10 (the `proposed`-Run approve/deny and
   the memory share/unshare flips), and the memory redact verbs all gate on
   `window.confirm(...)`. The copy is correct and the gate works — it is the presentation
   that should be ours. Because these are *consent* surfaces, this needs a design pass, a
   re-verified browser-evidence DoD, and a smoke re-run; it is not a silent refactor.
   **Decision recorded: discuss and confirm the approach before implementing.**
2. **Auto-refresh the charter-detail lists** (plan requests / context sources / run
   history). Today there is deliberately **no auto-poll** (`Workstreams.tsx`: "the FE-22
   memory precedent"), so a live-ticking charter reads as stale — during the walkthrough
   the context source still showed "never indexed / pending" while the indexer had
   already written 76 chunks. Trade-off at implementation: polling is cheap at the
   production 60s floor and thrashes at a 5s demo interval; targeted polling while a
   detail panel is open, or an event push, are the candidates.
3. **`/audit` is unusable at volume — flagged V1.0-BLOCKING (not v0.10-blocking).**
   The store reads `WHERE seq > $1 ORDER BY seq ASC LIMIT $2` (a forward cursor
   mirroring the append-only chain) and the page has `PAGE_LIMIT=50` with **no
   filtering** — a principled v0.1 decision ("filtering a partial page would
   misrepresent the chain") that has aged badly now that the platform generates its own
   high-volume events. Measured live: **635 events, 601 of them (95%) adaptive-ticker
   noise** (`plan.requested`/`plan.expired` at a 5s interval), leaving the newest entries
   **13 "next page" clicks deep**. Wanted: a **newest-first default** plus per-column
   asc/desc sorters. Implementation note: a descending default needs a **reverse cursor
   in the audit-service store + API params + codegen + UI** — not a UI-only patch — and it
   touches the evidence surface, so it warrants its own consult (the chain-integrity
   semantics of paging backwards; possibly server-side actor/outcome filtering; possibly
   de-noising the ticker's lifecycle events, itself an ADR question). Rationale for the
   v1.0 flag: the audit trail **is** this product's core evidence surface, and an
   operator who cannot reach recent events undermines the governance claim a commercial
   Beta 1 makes.

**Environment note (the v0.9-TLS-note class, for future testers):** the Claude Desktop
Browser pane **auto-dismisses `window.confirm` (returns `false`)**, so every
confirm-gated consent action silently cancels there — zero `/v1/context-sources`
requests reached the api while it was used. The §human walkthrough **must** be driven in
a real host browser (Chrome/Edge at `http://localhost:5173`); the same click there
showed the GOV-11 dialog and registered correctly.

## Next (T-104 — release closure, then v1.0-candidate scoping)

**T-104 is complete** — the CEO §human WD-10 walkthrough was driven live and signed off
(2026-07-24), these artifacts flipped to **released**, and the local annotated tag
`v0.10.0` landed (no remote). Beyond that, the v1.0-candidate scan carries forward: the
three §human UX findings above (the `/audit` ordering one flagged v1.0-blocking); agent
memory writes; unattended adaptive execution; tree-sitter (trigger above);
`agent-repo` / Git Intelligence (REQ-015); Layer 3 (hard-gated); computed
`confidence`; a retention default on accrued rows; multi-operator ACL +
operator-read chaining (SoD); manifest-primary storage; the `internal/*`
extraction (trigger still unfired); cron; DAG; the gateway exec worker; SSH
pooling; full rate-limiting; TR-2.
