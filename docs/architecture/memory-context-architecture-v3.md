# Memory and Context Architecture v3

## Three-layer context system
1. Live Index - continuously fresh repo/docs/tickets/messages.
2. Persistent Memory - user/project/org/ticket/run/skill/workstream memories.
3. Reasoning Retrieval - vector search, graph search, and tree/page reasoning.

## Memory controls
- export
- redact
- expire
- pin
- audit
- confidence
- source trace
- permission scope

## Storage pattern
Use filesystem-friendly memory manifests plus database indexing. Avoid hidden-only memory.

## v0.8 implementation slice — Persistent Scoped Memory (ADR-0064)

Layer 2 is **built first, alone, and deterministically** (ADR-0064; the P2 tier opener).
The v0.8 slice maps onto this document as follows:

- **Layer 2 (Persistent Memory) → `services/api/internal/memory`** (the fourth sibling
  seam beside `internal/run`/`internal/grant`/`internal/workstream`): one `memories`
  table (migration 0012). Two content classes: the **core is accrual by reference** — a
  terminal Run writes a content-free evidence pointer in-tx (the direct promotion of the
  v0.7 EVD-1 read-time scoped-memory slice, ADR-0063 §8); **operator-authored free text**
  is the secondary, opt-in path (sealed at rest, operator-only read — the ADR-0059
  posture).
- **Layer 1 (Live Index) — deferred** (REQ-013; its own ingestion machinery, B-009
  spike).
- **Layer 3 (Reasoning Retrieval) — deferred and hard-gated**: any embedding/AI
  dependency requires its own ADR against the offline + no-SaaS doctrine.
- **Memory controls, v0.8 status:** `redact` / `expire` / `pin` / `audit` /
  `source trace` / `permission scope` — **implemented deterministically** (tombstone
  flips chained as content-free `memory.redacted`/`memory.expired` events — the ADR-0049
  pattern; the expiry sweep rides the existing api reaper; owner-scoped at the
  single-operator posture). `export` — implemented as an on-demand render of a scope's
  memories to the **filesystem-friendly manifest** this document requires (storage is
  DB-only at v0.8; manifest-primary is the recorded alternative; never hidden-only).
  `confidence` — **recorded, defaulted, not computed** (scoring is an L3 concern).
- **Scopes at v0.8:** `workstream` / `run` / `operator` — the entities that exist
  (ADR-0064 §6). The fuller scope list (org/team/project/ticket/skill) defers with the
  modules that would own those scopes.
- **The governing boundary (ADR-0064 §3):** memory is the **mutable, forgettable
  projection** beside the immutable audit chain — redact/expire govern memory only and
  never rewrite the chain, the cast store, or prior exports. And memory is
  **operator-plane only** at v0.8: the canonical `agent-memory` edge stays
  reserved-unenforced; no agent reads or writes memory (the cross-grant
  injection/exfiltration channel stays closed by construction), and memory feeds no
  execution.

## v0.9 update — the `agent-memory` read path is lit (ADR-0065)

At v0.9 the `agent-memory` edge moved from reserved-unenforced to **enforced on exactly one
read-only path** (the gateway `memory.read` tool): the adaptive charter's composer reads the
memory its own live grants entitle it to, same-grant-scoped by construction, share-gated for
authored content, per-read chained (SEC-32). Layer 2 now **feeds execution under governance** —
but only through a plan that a human reviews (the always-park invariant). Layers 1 and 3 stayed
deferred.

## v0.10 implementation slice — Layer 1 Live/Context Index (ADR-0066)

Layer 1 is **built at v0.10** — deterministically, offline, with **zero new dependency** — now that
v0.9 gave it a named consumer (the composer's plan-time context). It maps onto this document as
follows:

- **Layer 1 (Live Index) → `services/api/internal/context`** (the fifth sibling seam beside
  `internal/run`/`internal/grant`/`internal/workstream`/`internal/memory`): `context_sources` +
  `context_chunks` (migration 0014). The corpus is an **operator-registered, charter-bound local
  context source** (docs/runbooks/repo/tickets) — operator-**curated** context, **not** a live mirror
  of the remote target hosts (which would need governed egress — deferred). The indexer is **confined
  to the registered source root** (SEC-33 — no path traversal, no symlink-escape).
- **The index is deterministic native Postgres FTS** (`tsvector` + GIN over chunked content; `ts_rank`
  + recency ranking) — **no embeddings, no new extension, no new dependency**. An in-process
  incremental indexer (rides the api background goroutine; mtime-then-hash; a per-source **staleness
  signal**) keeps it fresh without a full rebuild. Storage is DB-indexed with an on-demand manifest
  render (the storage-pattern rule above; never hidden-only).
- **The agent read plane** — the reserved **`agent-workspace`** edge is lit on one read path: the
  gateway `context.search` tool → subject-pinned api internal route → `internal/context.SearchForAgent`
  (reusing the memory `ReadForAgent` grant-scoped CTE skeleton; the readable set derives from the
  caller's own live grants → charter → charter-bound sources — cross-grant impossible by construction).
  **Every agent-plane read chains one content-free `context.read` event fail-loud in-tx** (SEC-33). A
  plan grounded in indexed context still **parks** for human review (the ADR-0065 injection control).
  `agent-repo` stays reserved (Git Intelligence, REQ-015).
- **Layer 1 controls, v0.10 status:** `source trace` / `permission scope` (grant/charter-derived) /
  `audit` (per-read chaining) — implemented; **incremental freshness** with a staleness signal —
  implemented; **`confidence` / learned ranking** — deferred (an L3 concern); `export` — the on-demand
  manifest render.
- **Layer 3 (Reasoning Retrieval) — deferred and hard-gated**: unchanged. **tree-sitter AST/code-graph
  — deferred with a recorded trigger** (when agents gain a code-editing capability; likely alongside
  Git Intelligence).
- **Scopes at v0.10:** context sources are **charter-bound** (`workstream`) only — the tightest
  containment; operator-shared / run-scoped sources defer with their consuming reads.
