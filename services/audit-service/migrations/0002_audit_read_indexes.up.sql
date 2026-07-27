-- audit-service schema, migration 0002 (forward-only, ADR-0044).
-- Read-path-only: three composite indexes to keep the audit read surface
-- (ADR-0067, PERF-7) index-backed for the operator's actor/outcome/edge
-- filters. This migration adds NO column, NO constraint, and NO trigger, and
-- touches no stored data or chain input — chain.Compute's input domain, every
-- event's vocabulary, and audit_events' rows are byte-unchanged (EVD-10, as
-- amended at the T-105 consult to permit index-only migrations on this
-- forward-only table). There is intentionally NO
-- 0002_audit_read_indexes.down.sql — see migrations/README.md.

CREATE INDEX IF NOT EXISTS audit_events_actor_seq_idx   ON audit_events (actor, seq DESC);
CREATE INDEX IF NOT EXISTS audit_events_outcome_seq_idx  ON audit_events (outcome, seq DESC);
CREATE INDEX IF NOT EXISTS audit_events_edge_seq_idx     ON audit_events (edge, seq DESC);
