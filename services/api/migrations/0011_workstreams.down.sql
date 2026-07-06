-- Destructive down (standard mutable service, ADR-0044).
ALTER TABLE delegation_grants DROP COLUMN budget_window_seconds;
DROP TABLE workstream_steps;
DROP TABLE workstreams;
