-- services/api schema, migration 0003 — encrypted target credentials on the
-- resources table (ADR-0048 §7, v0.1.0 release-criteria §SEC-12).
--
-- connection_secret stores pgp_sym_encrypt(material, AIREOS_RESOURCE_SECRET_KEY)
-- where material is the JSON SSH auth blob {private_key, passphrase}. Non-secret
-- connection coordinates (host, port, login user) live in resources.labels.
-- The encrypt/decrypt key is a separate env var from AIREOS_PASETO_KEY
-- (authority separation; enforced at startup by services/api).
--
-- pgcrypto ships with the standard postgres:16 image — no offline-bundle delta.
CREATE EXTENSION IF NOT EXISTS pgcrypto;

ALTER TABLE resources ADD COLUMN connection_secret BYTEA;
