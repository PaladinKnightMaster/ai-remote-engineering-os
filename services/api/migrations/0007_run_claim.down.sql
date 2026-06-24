-- Reverse 0007_run_claim.
ALTER TABLE runs DROP COLUMN IF EXISTS claimed_until;
