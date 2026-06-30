-- services/api runs.launch_key, migration 0010 (ADR-0062; v0.6 "Agent-initiated Runs").
-- Launch idempotency for agent-initiated Runs (T-065a): an agent retries run.launch
-- programmatically (network flake), so the launch carries the agent's idempotency key,
-- forwarded agent -> gateway -> api. api dedups the delegated launch on (grant_id,
-- launch_key) so a retry RE-ATTACHES to the original Run rather than double-launching and
-- double-consuming the grant's step budget. Operator-direct Runs leave launch_key '' and
-- are excluded from the unique index (a human double-submit legitimately creates two Runs,
-- the v0.5 posture). The unique partial index is the atomic dedup gate (no TOCTOU).
ALTER TABLE runs ADD COLUMN launch_key TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX runs_grant_launch_key_idx
    ON runs (grant_id, launch_key) WHERE launch_key <> '';
