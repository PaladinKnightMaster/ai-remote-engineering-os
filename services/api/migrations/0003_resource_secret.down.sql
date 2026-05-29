-- Reverse of 0003_resource_secret. Drops the encrypted-credential column only.
-- The pgcrypto extension is intentionally left installed: it is a database-wide
-- object other features may depend on, and dropping a shared extension on a
-- column rollback would be an unexpectedly wide blast radius.
ALTER TABLE resources DROP COLUMN IF EXISTS connection_secret;
