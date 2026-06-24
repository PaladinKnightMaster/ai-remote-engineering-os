-- services/api migration 0007 (ADR-0061; v0.5.0 DUR-2). A reclaimable single-driver
-- lease on runs: the sequencer claims a Run by setting claimed_until = now()+lease and
-- renews it per step; a crashed driver stops renewing, the lease expires, and the next
-- scan reclaims the Run (status='running' AND claimed_until < now()) to resume it. NULL
-- = unclaimed (a fresh 'pending' Run). Standard mutable service (ADR-0044) — destructive
-- down permitted.
ALTER TABLE runs ADD COLUMN claimed_until TIMESTAMPTZ;
