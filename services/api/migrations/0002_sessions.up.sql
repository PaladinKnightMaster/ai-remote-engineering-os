-- services/api schema, migration 0002 — cockpit sessions table (ADR-0048 §3).
-- Standard up/down (services/api is NOT forward-only; only audit-service is,
-- per ADR-0044).
--
-- NOTE: ADR-0048 §3's illustrative SQL types resource_id as TEXT. The real
-- resources.id is UUID (0001_init.up.sql), and Postgres rejects a TEXT->UUID
-- foreign key without a cast — so resource_id is UUID here. operator_id stays
-- TEXT (the bearer subject is a string like 'operator:admin', not a UUID).
CREATE TABLE sessions (
    id              UUID PRIMARY KEY,                  -- UUID v7, server-assigned
    resource_id     UUID NOT NULL REFERENCES resources(id),
    operator_id     TEXT NOT NULL,                     -- bearer's subject
    mode            TEXT NOT NULL,                     -- 'terminal' at v0.1.0
    status          TEXT NOT NULL,                     -- pending|active|closed|terminated
    started_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at        TIMESTAMPTZ,
    closed_reason   TEXT,                              -- idle|operator|broker_crash|ttl
    cast_path       TEXT,                              -- asciinema cast path (NULL until close)
    cast_sha256     CHAR(64),                          -- SHA256 of cast file at close
    CONSTRAINT sessions_mode_chk   CHECK (mode IN ('terminal')),
    CONSTRAINT sessions_status_chk CHECK (status IN ('pending','active','closed','terminated'))
);

-- Operator-scoped, id-ordered listing (GET /v1/sessions paginates by id cursor
-- within operator_id; ADR-0048 §5 "no cross-operator scope at v0.1.0").
CREATE INDEX sessions_operator_id_idx ON sessions (operator_id, id);
