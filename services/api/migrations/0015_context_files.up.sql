-- services/api context-index incremental state, migration 0015 (ADR-0066 §3; v0.10.0
-- "Live/Context Index" — T-097). context_files tracks each indexed file's mtime + size
-- + content hash — the state the incremental indexer's mtime-then-hash prefilter (K6)
-- compares against on every sweep pass: unchanged (mtime+size match) → skip entirely,
-- never re-hashed or re-read (PERF-11(3)); changed → hash, and only a genuine content
-- change triggers re-chunking.
--
-- context_chunks (0014) keeps its own file_hash column populated and untouched by this
-- migration — a deliberate, minor, harmless redundancy (chunk-level provenance, not the
-- sweep's comparison basis). context_files is the sole authoritative incremental state;
-- context_chunks carries no mtime/size and was never meant to serve this prefilter.
CREATE TABLE context_files (
    source_id   UUID        NOT NULL REFERENCES context_sources (id) ON DELETE CASCADE,
    file_path   TEXT        NOT NULL,                       -- relative to root_path
    mtime       TIMESTAMPTZ NOT NULL,
    size        BIGINT      NOT NULL,
    file_hash   TEXT        NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (source_id, file_path)
);
