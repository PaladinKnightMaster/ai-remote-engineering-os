-- Fixture: violates ADR-0044 forward-only by attempting to break the
-- audit_events chained-hash chain. migrate-lint MUST flag every line
-- below. (A real production migration like this would block the
-- release.)

DROP TABLE IF EXISTS audit_events;

TRUNCATE TABLE audit_events;

ALTER TABLE audit_events DROP COLUMN chain_hash;

DELETE FROM audit_events WHERE id = '00000000-0000-0000-0000-000000000000';

UPDATE audit_events
   SET chain_hash = 'forged'
 WHERE id = '00000000-0000-0000-0000-000000000000';
