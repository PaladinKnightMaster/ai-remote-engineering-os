-- Reverse migration 0010: drop the launch-idempotency index + column (ADR-0062, T-065a).
DROP INDEX IF EXISTS runs_grant_launch_key_idx;
ALTER TABLE runs DROP COLUMN launch_key;
