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
