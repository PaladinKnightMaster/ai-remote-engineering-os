-- Reverse of 0001_invocations (ADR-0044 standard mutable service).
DROP TRIGGER IF EXISTS gateway_outbox_notify_trg ON gateway_outbox;
DROP FUNCTION IF EXISTS gateway_outbox_notify();
DROP TABLE IF EXISTS gateway_outbox;
DROP TABLE IF EXISTS invocations;
