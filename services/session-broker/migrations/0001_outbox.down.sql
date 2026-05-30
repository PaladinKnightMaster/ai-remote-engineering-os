-- Reverse of 0001_outbox. session-broker is mutable (not forward-only, ADR-0044),
-- so dropping the outbox table + its wakeup trigger/function is permitted.
DROP TRIGGER IF EXISTS outbox_notify_trg ON outbox;
DROP FUNCTION IF EXISTS outbox_notify();
DROP TABLE IF EXISTS outbox;
