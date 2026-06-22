-- Revert 0003: command back to TEXT. convert_from is only meaningful for rows that
-- held UTF-8 plaintext (the dev/transition fallback); ciphertext rows are ephemeral.
ALTER TABLE invocations ALTER COLUMN command DROP DEFAULT;
ALTER TABLE invocations ALTER COLUMN command TYPE TEXT USING convert_from(command, 'UTF8');
ALTER TABLE invocations ALTER COLUMN command SET DEFAULT '';
