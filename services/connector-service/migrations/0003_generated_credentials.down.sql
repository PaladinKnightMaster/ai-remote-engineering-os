-- Reverse of 0003_generated_credentials.up.sql.
DROP TABLE IF EXISTS connector_pending_credentials;
ALTER TABLE connector_credentials DROP COLUMN IF EXISTS public_key;
