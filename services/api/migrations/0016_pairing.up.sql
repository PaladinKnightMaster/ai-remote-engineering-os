-- services/api pairing schema, migration 0016 (ADR-0068 §D4-§D6; v0.12 "Access &
-- Onboarding", E-02). api owns both tables in the shared cockpit DB (ADR-0048), the
-- same own-tables pattern as delegation_grants (0008). Standard mutable service
-- (only audit-service is forward-only, ADR-0044) — a destructive down is permitted.
--
-- Two tables, two very different lifecycles:
--   pairing_requests — a 120-second one-time secret exchanged for a session. It is
--     the ONLY row PERF-13's cleanup ever touches.
--   paired_sessions  — a durable, revocable operator session (the product's first —
--     ADR-0068's declared ADR-0046 divergence). It is never swept: liveness is
--     COMPUTED (revoked_at IS NULL AND expires_at > now()), exactly the ruling
--     migration 0008 made for delegation_grants' "expired" status, and here it is
--     also what AUD-33 requires — no pairing.expired event and no expiry-sweep
--     event may exist (ADR-0067 §D3 lifecycle-noise conformance).

-- pairing_requests: one desk-initiated pairing attempt (ADR-0068 §D6). id (UUID v7,
-- time-ordered) is the pairing:<id> audit anchor and the id half of the QR fragment
-- (https://<site>/pair#<id>.<secret>). secret_hash is the SHA-256 digest of the
-- 32-byte secret — the plaintext is returned to the requesting operator exactly
-- once (Store.Create) and NEVER persisted (GOV-12). label is set by the desk at
-- request time, not by the redeeming device (T-113's consult: a hostile redeemer
-- must not choose how their session appears in the operator's revoke list).
-- attempts counts failed redeems; a failed-attempt cap (T-113: 3) consumes the
-- request in the same transaction as the increment, per ADR-0068 §D6.
CREATE TABLE pairing_requests (
    id          UUID        PRIMARY KEY,                  -- UUID v7
    operator    TEXT        NOT NULL,                      -- requesting desktop operator
    label       TEXT        NOT NULL DEFAULT '',            -- set by the desk at request time
    secret_hash BYTEA       NOT NULL,                       -- SHA-256 of the 32 raw secret bytes
    expires_at  TIMESTAMPTZ NOT NULL,                       -- now() + 120s
    consumed_at TIMESTAMPTZ,                                -- NULL = unconsumed; single-use
    attempts    INTEGER     NOT NULL DEFAULT 0,             -- failed-redeem counter
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- PERF-13's bounded cleanup sweep (consumed OR expired rows) reads by expires_at.
CREATE INDEX pairing_requests_expires_at_idx ON pairing_requests (expires_at);

-- paired_sessions: a durable, revocable operator session narrowed to approvals@1
-- (ADR-0068 §D4/§D5). id (UUID v7) IS the minted token's jti — PERF-12's single
-- indexed primary-key liveness lookup on every request. scope carries NO CHECK
-- constraint: a CHECK would put membership policy in the schema, which is exactly
-- what SEC-36 requires stay single-sourced in Go (services/api/internal/pairing).
-- No pairing_request_id column: the audit chain ties the request and the session
-- together at T-114 (pairing:<id> / paired-session:<id> anchors on the same actor),
-- so the column would buy nothing (ADR-0068 §D6 consult).
CREATE TABLE paired_sessions (
    id         UUID        PRIMARY KEY,                    -- UUID v7 = the token's jti
    operator   TEXT        NOT NULL,                        -- the subject the session acts as
    label      TEXT        NOT NULL DEFAULT '',              -- carried over from the request
    scope      TEXT        NOT NULL,                         -- 'approvals@1'; no CHECK (SEC-36)
    expires_at TIMESTAMPTZ NOT NULL,                         -- now() + 8h (PairedTTL); no sliding refresh
    revoked_at TIMESTAMPTZ,                                  -- NULL = live; the audited terminal flip
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The desktop cockpit's "your paired devices" list is operator-scoped.
CREATE INDEX paired_sessions_operator_idx ON paired_sessions (operator);

-- The per-request liveness check (PERF-12) filters on revoked_at IS NULL; a partial
-- index keeps it cheap as revoked rows accumulate — the delegation_grants_agent_active_idx
-- precedent (migration 0008).
CREATE INDEX paired_sessions_active_idx ON paired_sessions (id) WHERE revoked_at IS NULL;
