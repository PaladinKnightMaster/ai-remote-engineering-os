-- Fixture: clean forward-only migration on the audit_events table.
-- migrate-lint should report zero violations.

CREATE TABLE audit_events (
    seq         BIGSERIAL PRIMARY KEY,
    id          UUID NOT NULL UNIQUE,
    chain_hash  CHAR(64) NOT NULL UNIQUE
);

-- Comment containing the forbidden word DROP — must not trigger
-- (the linter strips line comments before matching).
CREATE INDEX audit_events_id_idx ON audit_events (id);
