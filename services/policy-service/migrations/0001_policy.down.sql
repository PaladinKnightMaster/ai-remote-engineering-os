-- Reverse of 0001_policy (ADR-0044 standard mutable service).
DROP TRIGGER IF EXISTS policy_outbox_notify_trg ON policy_outbox;
DROP FUNCTION IF EXISTS policy_outbox_notify();
DROP TABLE IF EXISTS policy_outbox;
DROP TABLE IF EXISTS pending_approvals;
