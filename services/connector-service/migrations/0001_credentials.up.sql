-- connector-service schema, migration 0001 — credential custody (ADR-0053).
--
-- connector-service is the sole holder of AIREOS_RESOURCE_SECRET_KEY and the
-- only service that runs pgp_sym_encrypt/decrypt on target credentials (closing
-- the v0.1.0 SEC-12 debt). The encrypted {private_key, passphrase} JSON moves
-- here off the legacy api-owned resources.connection_secret column; the
-- byte-identical Material shape lets the T-029a backfill copy ciphertext
-- verbatim (same key — no decrypt/re-encrypt).
--
-- resource_id is a SOFT reference to the api-owned resources(id): no
-- cross-service foreign key, keeping connector's schema independent of api's
-- even though they share the cockpit DB (ADR-0048). Non-secret connection
-- coordinates (host, port, login user, ssh_host_key) stay on resources.labels.
--
-- pgcrypto ships with postgres:16 (already created by api's 0003); the IF NOT
-- EXISTS keeps this migration self-contained without conflicting.
--
-- connector-service is a standard mutable service (only audit-service is
-- forward-only, ADR-0044), so a destructive down is permitted.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE connector_credentials (
    resource_id       UUID        PRIMARY KEY,             -- soft ref to resources(id); no cross-service FK
    connection_secret BYTEA       NOT NULL,                -- pgp_sym_encrypt(material, AIREOS_RESOURCE_SECRET_KEY)
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);
