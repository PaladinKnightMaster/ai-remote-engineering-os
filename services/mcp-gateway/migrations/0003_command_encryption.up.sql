-- mcp-gateway migration 0003 (F3, ADR-0059 extension): the persisted command is
-- encrypted at rest with a gateway-local key (AES-256-GCM, internal/cmdcrypt), so
-- the column holds ciphertext (nonce||ciphertext) bytes, not text. Invocations are
-- ephemeral (reaper-bounded, ADR-0059) — there is no durable plaintext to preserve
-- through the type change. Application-level (not pgcrypto): the key never reaches
-- Postgres, and the SEC-13 grep gate (connector-only pgp_sym_decrypt) stays clean.
ALTER TABLE invocations ALTER COLUMN command DROP DEFAULT;
ALTER TABLE invocations ALTER COLUMN command TYPE BYTEA USING convert_to(command, 'UTF8');
ALTER TABLE invocations ALTER COLUMN command SET DEFAULT ''::bytea;
