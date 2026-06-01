-- Reverse 0004_outbox.
DROP TRIGGER IF EXISTS api_outbox_notify_trg ON api_outbox;
DROP FUNCTION IF EXISTS api_outbox_notify();
DROP TABLE IF EXISTS api_outbox;
