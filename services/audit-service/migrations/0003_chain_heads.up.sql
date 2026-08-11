-- audit-service schema, migration 0003 (forward-only, ADR-0044).
-- Adds audit_chain_heads: signed checkpoints over the tamper-evidence chain
-- (ADR-0069). A chain head commits to the entire chain prefix up to seq (D1):
-- chainHash_N = SHA256(chainHash_{N-1} || framed(event_N)), so ONE Ed25519
-- signature over head N attests every event with seq <= N. This is what
-- turns the unkeyed SHA-256 chain (which has proven ordering only, since
-- v0.1.0) into something a third party holding only an export and a public
-- key can verify offline, with no database access (D6).
--
-- No backfill, ever (D9): signing a head at ANY point attests all
-- pre-existing history for free, because of the prefix property above. This
-- migration performs no re-hashing and no rewrite of audit_events.
--
-- There is intentionally NO 0003_chain_heads.down.sql — see
-- migrations/README.md; audit-service is forward-only by policy (ADR-0044),
-- and the `migrate` subcommand does not even expose `down`.

CREATE TABLE audit_chain_heads (
    -- the checkpointed chain position. FK to audit_events(seq) with NO
    -- ON DELETE clause — RESTRICT is the default and is deliberate: a head
    -- must never outlive, or be insertable ahead of, the event it commits to.
    seq             BIGINT PRIMARY KEY REFERENCES audit_events(seq),
    -- the chain_hash AT seq — the value the signature attests to.
    chain_hash      CHAR(64) NOT NULL,
    -- signer-produced RFC3339Nano occurrence time, stored verbatim as TEXT —
    -- the same rationale as audit_events."timestamp" (0001_init): this value
    -- is part of the signed payload, so it must be byte-identical on read,
    -- with no timestamptz normalization or reformatting.
    head_timestamp  TEXT NOT NULL,
    -- fingerprint of the Ed25519 key that produced signature
    -- (chain.KeyIDFor: first 16 lowercase hex chars of SHA-256(pubkey)) —
    -- derivable by a verifier holding only the public key, so no key
    -- registry is needed (D5). Rotation is additive-forever: multiple keyIds
    -- may appear here over the life of an install.
    key_id          TEXT NOT NULL,
    -- lowercase hex of the 64-byte Ed25519 signature (128 hex chars) over
    -- framed(domain, seq, chain_hash, head_timestamp, key_id) — see
    -- internal/chain/head.go.
    signature       CHAR(128) NOT NULL,
    -- server ingest time — metadata, not part of the signed payload, and not
    -- exposed on the wire at all.
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Same append-only bar as audit_events (0001_init), by deliberate design
-- (ADR-0069 §Context correction): without this trigger, erasing a head would
-- be a plain DELETE while rewriting audit_events requires first dropping
-- audit_events_no_mutate — two different bars guarding one evidence chain.
-- A head must be exactly as hard to erase as the events it attests to.
CREATE OR REPLACE FUNCTION audit_chain_heads_append_only()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'audit_chain_heads is append-only (ADR-0069): % rejected', TG_OP;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER audit_chain_heads_no_mutate
    BEFORE UPDATE OR DELETE ON audit_chain_heads
    FOR EACH ROW EXECUTE FUNCTION audit_chain_heads_append_only();
