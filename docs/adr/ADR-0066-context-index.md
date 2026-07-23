# ADR-0066: Layer 1 Live/Context Index — governed deterministic context retrieval for plan-time composition

## Status

**Accepted** — 2026-07-23 (v0.10 scoping consult — theme ratified — plus this ADR consult, whose
four ratification points R1–R4 were signed off on the war-room recommendation, including two
recommendation-level revisions off the initial consult draft: R2 lights `agent-workspace` rather than
`agent-repo`, and R3 ships native Postgres FTS with **zero new dependency**, deferring tree-sitter).
Author/decider: CEO (war-room). Binding spec for the v0.10 Live/Context Index ticket ladder; code is
authored just-in-time per ticket from here.

**Builds on / conforms to:** ADR-0046 (the canonical capability-edge set — which has *named*
`agent-workspace` as a reserved edge since v3.0.1; this ADR enforces it on one read-only path),
ADR-0056 (the inbound-only MCP trust boundary — the new tool rides the existing wire; the agent is a
client, the platform never dials out), ADR-0060 (the keyless agent-facing gateway — preserved: the new
verb is a forward, no new key, no new minting workload), ADR-0062 (delegation grants — the standing
consent this ADR reuses as the gate for plan-time context reads), ADR-0063 (chartered Workstreams — a
context source is bound to a charter; the composer reads its charter's context), ADR-0064 (Persistent
Scoped Memory — Layer 2; L1 is the **sibling read plane**, reusing the `ReadForAgent` grant-scoped,
per-read-chained skeleton over a different corpus), ADR-0065 (Adaptive Workstreams — the composer is
the **named consumer**; its always-park invariant is L1's injection mitigation),
`docs/architecture/memory-context-architecture-v3.md` (Layer 1, REQ-013/B-009).

**No ADR divergence (G2-clean):** it **extends** ADR-0062/0063/0064/0065 (additive context-source
entity + FTS index + a new read verb) and **preserves** ADR-0046 (the edge *set* is unchanged —
enforcing a named reserved edge is not an eighth edge), ADR-0056 (one new method on the existing
hand-rolled wire; the trust boundary is untouched), ADR-0060 (the gateway stays keyless), and
ADR-0064's operator-plane memory surface (byte-untouched — L1 is a *separate* store/seam). It edits no
Accepted ADR.

Companion spec: `docs/architecture/memory-context-architecture-v3.md` (Layer 1 slice, appended).

## Context

**The fused trigger (why now).** Layer 1 has sat deferred through four releases as a feed with no
consumer. v0.9 (ADR-0065) gave it a **named consumer** for the first time: the composer's plan-time
context. v0.8 built Layer 2 (persistent memory); v0.9 lit the agent read path to Layer 2 and made a
charter's per-tick plan agent-composed. L1 is the **other half of that read plane** — the composer
today plans against accrued *memory* but has no view of the current state of the operator's
repo/docs/runbooks. L1 closes that: **remember (L2) + know the current context (L1) → compose → govern
→ act → accrue.**

**Doctrine resolution.** A deterministic keyword/structural index breaches no doctrine: **zero model
dependency, zero embedding, zero egress** (the indexer reads only operator-registered *local* sources;
Postgres native full-text search needs no extension). Layer 3 (reasoning retrieval — any embedding/AI
dependency) stays **hard-gated** and is untouched. The plan author remains the customer's external
agent inbound over the ADR-0056 MCP surface.

**The corpus question (the consult's load-bearing decision, R1).** REQ-013 names "repo/docs/tickets,"
but the platform hosts no forge/board yet (REQ-005 Work Board, REQ-015 Git Intelligence are deferred).
So L1 needs a corpus. v0.10 introduces the minimal coherent one: an **operator-registered context
source** — a local/mounted directory of the docs/runbooks/repo/tickets the operator maintains, bound
to a charter. This is **operator-curated context**, deliberately **NOT a live mirror of the remote
target hosts** (indexing content that lives *on* a remote resource requires governed egress — a
governed Run in itself — and is deferred). Framing this boundary prevents a Beta expectation mismatch.

**The edge choice (R2).** The v0.10 corpus is docs + runbooks + repo + tickets = the **project
workspace**; `agent-workspace` is the semantically correct reserved edge. `agent-repo` is deliberately
**left reserved for REQ-015 Git Intelligence** (the issue/PR/CI/code graph — true VCS access), keeping
the taxonomy clean for the module that will own it. Lighting `agent-workspace` is the exact v0.9
playbook (enforce one reserved edge; the set is unchanged).

**As-built findings (HEAD `053997d`):**

1. **The forward pattern is reusable verbatim.** `memory.read` is a control-plane `ToolDef` (no
   target/edge-policy path; dispatched before `remote.exec`) forwarding to a subject-pinned api
   internal route ([catalog.go](../../services/mcp-gateway/internal/catalog/catalog.go)). `context.search`
   follows it exactly; SEC-20/27 carry.
2. **The grant-scoped read is a proven skeleton.** `ReadForAgent` derives the readable set entirely
   server-side from the caller's own live grants via a CTE (cross-grant impossible by construction),
   and **chains one event inside the read's own transaction — no event, no data**
   ([store.go](../../services/api/internal/memory/store.go)). L1's `SearchForAgent` is the same shape
   over a different corpus.
3. **The reserved-edge playbook exists** (v0.9 lit `agent-memory`); `agent-workspace` is next.
4. **The in-process background precedent exists** (the workstream ticker + the api reaper goroutine);
   the indexer rides it, so the `internal/*` extraction trigger stays unfired.
5. **Native Postgres FTS is zero-dependency** (`tsvector`/`tsquery` + GIN; pgcrypto stays the only
   extension). Migration lands at **0014** (latest is `0013_adaptive`).

**Standards lineage.** Capability POLA carried from ADR-0062/0064 (the readable set derives from the
caller's own grant, nothing else); the deterministic full-text + incremental-index mechanism is the
industry-proven no-embedding path (independently confirmed by the reference scan: Hermes FTS5,
codegraph FTS5/tree-sitter); the confined-root file-read is the standard indexer sandboxing pattern.

## Decision

### 1. The context source (R1) — operator-registered, charter-bound, confined

A **context source** is an operator-registered local/mounted directory (docs/runbooks/repo/tickets),
**bound to one charter** (`workstream:<id>`) — so the charter's composer reads it and nothing else.
Registration/list/remove are typed, CSRF-gated `human-agent` routes (`/v1/context-sources*`). The
indexer is **confined to the registered root** (SEC-33): no path traversal, no following symlinks out
of the root, no reading outside the declared directory — a file-reading process in a security product
is jailed to its source. Charter-bound is the only v0.10 scope; operator-shared / run-scoped sources
are deferred.

### 2. Deterministic FTS index (R3) — native Postgres, zero new dependency, no embeddings

The index is **native Postgres full-text search** (`tsvector` + GIN): source files are chunked
(by document/heading for docs and tickets; by file/region for code) and indexed as `tsvector`.
Retrieval ranks by **lexical relevance (`ts_rank`) + recency only — never learned**. **No embeddings,
no vector store, no new extension, no new dependency** — the zero-new-dependency posture of v0.8/v0.9
is preserved, and **Layer 3 stays hard-gated** (any embedding/AI dependency = its own ADR against the
offline + no-SaaS doctrine). tree-sitter AST/code-graph is **deferred with a recorded trigger** (see
§Deferred) — v0.10's consumer plans governed shell commands, not code edits, so full-text retrieval is
sufficient for it.

### 3. In-process incremental indexer (D3)

A background indexer **rides the api's existing background goroutine** (the ticker/reaper precedent —
no new unit, env, or key; the `internal/*` extraction trigger stays unfired). Freshness is
**incremental**: a sweep compares each file's **mtime + size first and hashes only changed
candidates** (no full re-hash per pass); the index stays fresh without a full rebuild. Each source
carries a **staleness signal** surfaced to both the composer (on read) and the operator (on the source
view) — the fail-loud honesty posture. Storage is DB-indexed with an **on-demand manifest render** (the
memory-context storage rule; never hidden-only).

### 4. The agent read path + the `agent-workspace` edge (R2/R4) — SEC-33

The gateway gains ONE new **control-plane tool** on the `memory.read` forward pattern:

- **`context.search`** — the composer searches the context its charter's grant entitles it to. It
  takes a query (and an optional `workstreamId` narrow); it resolves **no target, mints no egress**.
  The keyless gateway (ADR-0060) forwards {agent, query, optional workstream} to a subject-pinned
  (`subject == "mcp-gateway"`) api internal route (`/v1/internal/context-searches`), which is the sole
  authority.

The readable set is derived **entirely server-side from the caller's own ACTIVE, unexpired grants**
(the `ReadForAgent` CTE skeleton): live grants → each grant's charter (`workstreams.grant_id`) →
context sources bound to that charter → FTS over those sources' chunks. **Cross-grant / foreign-source
reads are impossible by construction, never by filter**; a `workstreamId` outside the caller's derived
set matches nothing and answers an **empty result, never an error** (no oracle). The tool carries the
reserved **`agent-workspace`** edge (the enforcement of the reserved edge; the set is unchanged).

**Per-read audit (SEC-33, R4).** Every agent-plane context read chains **exactly one `context.read`
event, enqueued INSIDE the read's own transaction — no event, no data** (the ADR-0064 §8 escalation,
fired for the non-owner plane; operator context management stays unchained). The event is
**content-free**: the query is recorded as metadata, the target lists the returned chunk ids, the actor
is the reading agent, the edge is `agent-workspace`.

### 5. Migration 0014 + offline

Migration **0014** adds `context_sources` (id, root_path, scope binding, index/staleness metadata) and
`context_chunks` (source_id, locator, `tsvector`, GIN index). **Offline (OI-17):** 0014 rides
`install.sh`'s existing migrate loop; the indexer rides the already-running api goroutine; the
`context.search` tool rides the already-installed gateway binary + Caddy `/mcp` route — **targeting
zero install-wiring diff** (the v0.8/v0.9 posture). Exact table split and event vocabulary are pinned
at the v0.10 criteria.

## Security posture (SEC-33 — the release's named invariant)

Agent context-index access is **read-only** (no agent write path to the index exists; indexing is a
machine sweep of operator-registered sources), **same-grant-scoped by construction** (the readable set
derives from the caller's own live grants → charter-bound sources; never a caller-supplied path, never
a foreign charter), **confined** (the indexer cannot read outside a registered source root),
**staleness-honest**, **per-read chained** on the `agent-workspace` edge (fail-loud in-tx), and
**consumed only through a plan that parks for human review** (ADR-0065's always-park invariant carries
— a composed plan grounded in indexed context is still reviewed before anything executes). The indexer
reads only local sources: **no egress, no network, no embedding/model.** The `agent-workspace` edge set
is unchanged (ADR-0046 preserved).

## Consequences

- **The read plane completes:** memory (L2) + context (L1) both feed the composer under governance —
  read bounded by the grant, use bounded by the park, every agent-plane read chained. Strategy layer 6
  (Knowledge/Memory/Context) advances on a self-hosted, offline, deterministic footing — the
  defensible, doctrine-clean version of what cloud-embedding competitors do.
- **Doctrine held:** zero model dependency, zero embedding, zero new egress, **zero new dependency**;
  L3 hard gate untouched; the gateway stays keyless.
- **Injection is mitigated by an existing invariant:** indexed content is untrusted (a poisoned doc
  could steer a plan), but every agent-composed plan **parks for human review** (ADR-0065) — the same
  mitigation as shared operator memory (ADR-0065 red-team #2). No new mitigation machinery is needed.
- **Honest, bounded new work:** one migration (0014), the `internal/context` seam (store + indexer +
  the agent-plane `SearchForAgent`), the subject-pinned internal route, one gateway catalog entry + its
  forward, and the operator `/v1/context-sources*` surface + UI. Policy, connector, broker, and the
  audit chain are reused unchanged.
- **Template/operator surfaces untouched:** ADR-0064's `/v1/memories*` operator plane is byte-identical
  (L1 is a separate seam); the elevated regression guards (WD-8) carry.
- **The `internal/*` extraction trigger stays unfired:** the indexer + read ride the api in-process;
  still no out-of-process consumer.

## Red-team (the lit channel)

| # | Attack | Closed by |
|---|---|---|
| 1 | Cross-grant / foreign-source read | Readable sources derived from the caller's own live grants → charter-bound sources only (§4); out-of-set → empty, no oracle; negative leg in WD-10 |
| 2 | Prompt injection via an indexed doc | The composed plan **always parks** for human review (ADR-0065); the operator reads the plan pre-execution |
| 3 | Exfiltration via `context.search` queries | Read-only, grant-scoped, per-read chained (§4); the query + returned chunk ids are audited |
| 4 | Indexer escapes its source root (path traversal / symlink) | The indexer is **confined to the registered root** (§1/SEC-33) — no traversal, no symlink-escape, no out-of-root read |
| 5 | Stale index → a plan grounded in wrong state | Per-source staleness signal surfaced to composer + operator (§3); a source edit is reflected on the next `context.search` |
| 6 | Embedding/model creep past the doctrine | §2 forbids it; native FTS only; L3 stays hard-gated (its own ADR if ever) |

**Verdict.** The genuinely new capability is **agent-readable project context** — confined to
operator-registered local sources, same-grant-scoped by construction, read-only, per-read chained, and
consumed only through a plan a human reviews before execution. Nothing new mints; nothing outbound or
model-bound exists; the accepted residual is that indexed content, once in scope, is inside the
composer's context — which the always-park review is the control for.

## Deferred (recorded)

- **tree-sitter AST / code-graph retrieval** — deferred for v0.10 (native FTS is sufficient for a
  composer that plans shell commands, not code edits, and it keeps the zero-new-dependency posture).
  **RECORDED TRIGGER (per the CEO's R3 ratification note): when agents gain a code-EDITING capability**
  — at which point code-structure retrieval (call graphs, symbol resolution, blast-radius) becomes
  genuinely valuable — tree-sitter AST indexing should be added, most naturally alongside **Git
  Intelligence (REQ-015)**. This is a *when*, not an *if*: it is on the forward path, gated on the
  code-editing consumer existing.
- **The `agent-repo` edge** — held reserved for Git Intelligence (REQ-015: the issue/PR/CI/code graph).
- **Remote-target indexing** — indexing content that lives on a remote resource (vs. an
  operator-registered local source) needs governed egress; deferred.
- **Operator-shared / run-scoped context sources** — v0.10 is charter-bound only (the tightest
  containment); broader scopes defer.
- **Learned ranking / computed `confidence`** (L3), **"messages"/chat indexing** (no chat corpus
  exists yet), **multi-source federation** — deferred.
- **Layer 3 — Reasoning Retrieval** — hard gate unchanged (any embedding/AI dependency = its own ADR
  against the offline + no-SaaS doctrine).
- **The `internal/*` extraction; cron; DAG; the gateway exec worker; SSH pooling; full rate-limiting;
  TR-2** — unchanged prior deferrals.

## Relation to prior ADRs

- **ADR-0064 (Persistent Scoped Memory)** — L1 is the sibling read plane; it reuses the `ReadForAgent`
  grant-scoped, per-read-chained skeleton and keeps ADR-0064's operator surface byte-identical.
  Extends, does not edit.
- **ADR-0065 (Adaptive Workstreams)** — supplies the named consumer (the composer) and the injection
  mitigation (always-park); `context.search` joins `memory.read` as the second read the composer makes
  at plan time. Extends, does not edit.
- **ADR-0062/0063 (grants / charters)** — the grant is the containment gate; a context source is bound
  to a charter; the cascade need not change (a revoked grant's charter has no live composer to read).
  Extends, does not edit.
- **ADR-0056 / ADR-0060 (gateway surface / keyless posture)** — one new method on the existing wire, a
  forward; the trust boundary and key posture are preserved.
- **ADR-0046 (capability edges)** — the reserved `agent-workspace` edge is enforced on one path; the
  edge set is unchanged. `agent-repo` stays reserved. Preserved.
- **ADR-0049 (hash-in-chain)** — the `context.read` event is content-free; it chains facts (query
  metadata + returned chunk ids), never content. Preserved.

## Ratification (resolved 2026-07-23)

All four open points were signed off on the war-room recommendation, and are now binding:

1. **R1 — Corpus model = an operator-registered local context source** (charter-bound; operator-curated
   context, not a remote-target mirror), over deferring L1.
2. **R2 — Light the reserved `agent-workspace` edge** for the context read (not `agent-repo`, which is
   reserved for Git Intelligence; not overloading `agent-memory`).
3. **R3 — Mechanism = native Postgres FTS, no embeddings, no new extension/dependency**, lexical +
   recency ranking; **tree-sitter deferred with the code-editing trigger recorded** (per the CEO's
   note).
4. **R4 — Read path = a new `context.search` tool on the `memory.read` forward pattern** —
   grant-scoped, per-read chained (**SEC-33**), fed by an in-process mtime-then-hash incremental indexer
   confined to the source root.

Everything else = the CEO-ratified v0.10 theme (Layer 1 Live/Context Index) + this consult's as-built
review (findings 1–5, zero slate-breaking).
