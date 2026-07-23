-- services/api context-index schema, migration 0014 (ADR-0066; v0.10.0 "Live/Context
-- Index" — Layer 1). api owns context_sources/context_chunks in the shared cockpit DB —
-- the same own-tables pattern as memories (0012) and the adaptive-workstream deltas
-- (0013). Standard mutable service (only audit-service is forward-only, ADR-0044) — a
-- destructive down is permitted.

-- context_sources: one row per operator-registered context source (ADR-0066 §1). id
-- (UUID v7, time-ordered) is minted in Go like every other store — never a DB default.
-- root_path is the confined-root directory the indexer is allowed to read (SEC-33; the
-- file reader in internal/context/root.go enforces containment in code — this column is
-- only the registered declaration). scope_type is 'workstream' at v0.10 (charter-bound
-- only, K1's ratified scope) — operator-shared / run-scoped sources are deferred.
-- scope_id names the bound charter. permission_scope is the owning operator (the
-- memories owner-scoping pattern — every operator-plane read/list/remove is
-- owner-scoped). status tracks the indexer's own health (CTX-5): 'active' normally,
-- 'errored' when the root is unreadable/absent — surfaced, never a crashed sweep.
-- last_indexed_at is set by ReplaceSourceChunks (T-097's indexer write path) — NULL
-- until the first sweep completes; its absence/staleness is the source's freshness
-- signal (CTX-3).
CREATE TABLE context_sources (
    id                UUID        PRIMARY KEY,                  -- UUID v7
    root_path         TEXT        NOT NULL,
    scope_type        TEXT        NOT NULL,
    scope_id          TEXT        NOT NULL,
    permission_scope  TEXT        NOT NULL,                     -- the owning operator
    status            TEXT        NOT NULL DEFAULT 'active',
    last_indexed_at   TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT context_sources_scope_type_chk CHECK (scope_type IN ('workstream')),
    CONSTRAINT context_sources_status_chk CHECK (status IN ('active', 'errored'))
);

-- The agent read plane's derivation (CTX-4, T-098's SearchForAgent: live grant → the
-- grant's charter → context sources bound to that charter) and the operator list route
-- both key off (scope_type, scope_id).
CREATE INDEX context_sources_scope_idx ON context_sources (scope_type, scope_id);

-- The operator source-management view (list/remove, T-100) is owner-scoped, like
-- memories/runs/workstreams.
CREATE INDEX context_sources_owner_idx ON context_sources (permission_scope);

-- context_chunks: one row per indexed chunk (ADR-0066 §2). source_id cascades on the
-- owning source's removal (CTX-5: removing a source removes its chunks — no orphaned
-- index rows). file_path is relative to the source's root_path (never an absolute host
-- path — the confined-root invariant extends to what gets stored, not just what gets
-- read). locator names the region/heading within the file (K2: a document/heading id
-- for docs+tickets, a file/region id for code) so a returned chunk can be located back
-- in its source. file_hash is the source file's content hash (T-097's incremental
-- indexer uses it to skip re-chunking unchanged files after an mtime+size prefilter).
--
-- content is PLAINTEXT, deliberately NOT sealed at rest (unlike memories.content, which
-- IS sealed — F3 parity). This is intentional, not an oversight: native Postgres FTS
-- (to_tsvector) requires indexable plaintext, and the corpus is operator-curated
-- project docs/runbooks/repo/tickets — not credentials or secrets. SEC-13 (no gateway
-- secret custody) is untouched: this table never holds connector credentials or command
-- output, only operator-registered document content the operator already chose to
-- expose to their own charter's composer (GOV-11's consent copy names this plainly).
--
-- content_tsv is a GENERATED STORED tsvector column (native Postgres full-text search —
-- tsvector/GIN are core Postgres, NO new extension, NO embeddings, NO new dependency;
-- ADR-0066 §2/R3). Retrieval ranks by ts_rank (lexical) + recency only — never learned.
CREATE TABLE context_chunks (
    id          UUID        PRIMARY KEY,                        -- UUID v7
    source_id   UUID        NOT NULL REFERENCES context_sources (id) ON DELETE CASCADE,
    file_path   TEXT        NOT NULL,                            -- relative to root_path
    locator     TEXT        NOT NULL,                            -- region/heading id within the file
    content     TEXT        NOT NULL,                            -- plaintext (see comment above)
    file_hash   TEXT        NOT NULL,                            -- the source file's content hash
    content_tsv TSVECTOR    GENERATED ALWAYS AS (to_tsvector('english', content)) STORED,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The FTS retrieval path (CTX-2/CTX-4): SearchForAgent's ts_rank query rides this GIN
-- index — no seq-scan on a large source (PERF-11(2)).
CREATE INDEX context_chunks_tsv_idx ON context_chunks USING GIN (content_tsv);

-- ReplaceSourceChunks' delete-then-insert (T-097's write path) and any per-file lookup
-- key off (source_id, file_path).
CREATE INDEX context_chunks_source_file_idx ON context_chunks (source_id, file_path);
