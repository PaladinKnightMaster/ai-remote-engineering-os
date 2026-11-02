-- services/api first-run setup schema, migration 0017 (ADR-0078 §D1/§D4; v0.17
-- "Install & First-Run Experience", FRX-1/2/3, SEC-64). api owns both tables in the
-- shared cockpit DB (ADR-0048), the same own-tables pattern as pairing (0016).
-- Standard mutable service (only audit-service is forward-only, ADR-0044) -- a
-- destructive down is permitted.

-- setup_codes: the one-time first-run setup code `api setup-code issue` prints on the
-- server's terminal (ADR-0078 §D1). code_hash is the SHA-256 of the NORMALISED 20-char
-- Crockford base32 code -- the plaintext is shown once and NEVER persisted (SEC-67).
-- attempts counts wrong completions; the 5th invalidates the code in the same
-- transaction as the increment. A code is live while consumed_at and invalidated_at
-- are both NULL; expiry (created_at + 60 min) is COMPUTED against expires_at, never
-- swept (no expiry event, ADR-0078 §D6 / ADR-0068 §D6).
CREATE TABLE setup_codes (
    id             UUID        PRIMARY KEY,                 -- UUID v7; the setup-code:<id> audit anchor
    code_hash      BYTEA       NOT NULL,                    -- SHA-256 of the normalised code
    expires_at     TIMESTAMPTZ NOT NULL,                    -- issue time + 60 min
    attempts       INTEGER     NOT NULL DEFAULT 0,          -- wrong completions so far
    consumed_at    TIMESTAMPTZ,                             -- NULL = unused; single-use
    invalidated_at TIMESTAMPTZ,                             -- re-issue, or the attempt cap
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- At most ONE live code, enforced by the database: a new issue must invalidate the
-- previous one in the same transaction or this index rejects the insert.
CREATE UNIQUE INDEX setup_codes_one_live_idx ON setup_codes ((true))
    WHERE consumed_at IS NULL AND invalidated_at IS NULL;

-- operators: the single operator created through the browser (ADR-0078 §D4). AT MOST
-- ONE ROW, enforced here and not in Go: `singleton` is a constant-true primary key, so
-- a second row -- from any code path, or from raw SQL -- violates the PK, and a row
-- that tries a different constant violates the CHECK. An operator in the environment
-- (AIREOS_BOOTSTRAP_OPERATOR_*) always wins over this table; api checks that, not SQL.
CREATE TABLE operators (
    singleton     BOOLEAN     PRIMARY KEY DEFAULT true CHECK (singleton),
    username      TEXT        NOT NULL CHECK (username <> ''),
    password_hash TEXT        NOT NULL,                      -- argon2id PHC string
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
