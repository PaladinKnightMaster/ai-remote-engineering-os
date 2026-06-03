-- Reverse of 0002_outbox.up.sql.
DROP TRIGGER IF EXISTS connector_outbox_notify_trg ON connector_outbox;
DROP FUNCTION IF EXISTS connector_outbox_notify();
DROP TABLE IF EXISTS connector_outbox;
