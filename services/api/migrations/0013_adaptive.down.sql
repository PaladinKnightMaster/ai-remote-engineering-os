-- Destructive down (standard mutable service, ADR-0044). Reverses all four deltas.
ALTER TABLE runs DROP CONSTRAINT runs_status_chk;
ALTER TABLE runs ADD CONSTRAINT runs_status_chk
    CHECK (status IN ('pending', 'running', 'completed', 'failed', 'canceled'));

DROP TABLE plan_requests;

ALTER TABLE workstreams DROP CONSTRAINT workstreams_composer_chk;
ALTER TABLE workstreams DROP COLUMN composer;
ALTER TABLE workstreams DROP CONSTRAINT workstreams_kind_chk;
ALTER TABLE workstreams DROP COLUMN kind;

ALTER TABLE memories DROP COLUMN agent_visible;
