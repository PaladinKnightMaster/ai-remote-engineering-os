-- Fixture: violates ADR-0044 forward-only by attempting to break the
-- audit_events chained-hash chain. migrate-lint MUST flag every line
-- below. (A real production migration like this would block the
-- release.)
--
-- Includes the bypass vectors surfaced by the T-011 review pass:
--   - schema-qualified table names (public.audit_events)
--   - quoted identifiers ("audit_events")
--   - DROP TRIGGER on the append-only enforcer
--   - DROP FUNCTION + CREATE OR REPLACE FUNCTION on the trigger's body
--   - RENAME TO (moves the table without dropping it)

DROP TABLE IF EXISTS audit_events;

DROP TABLE public.audit_events;

DROP TABLE "audit_events";

TRUNCATE TABLE audit_events;

ALTER TABLE audit_events DROP COLUMN chain_hash;

ALTER TABLE audit_events RENAME TO audit_events_old;

DELETE FROM audit_events WHERE id = '00000000-0000-0000-0000-000000000000';

UPDATE audit_events
   SET chain_hash = 'forged'
 WHERE id = '00000000-0000-0000-0000-000000000000';

-- DB-level protections — dropping these disables append-only enforcement
-- without touching the table itself.
DROP TRIGGER IF EXISTS audit_events_no_mutate ON audit_events;

DROP FUNCTION audit_events_append_only();

-- KNOWN UNDETECTED VARIANT (documented in main.go): replacing the body
-- via CREATE OR REPLACE FUNCTION is the canonical PostgreSQL idempotent-
-- setup idiom, and the legitimate 0001_init.up.sql uses it. Banning
-- this form would false-positive on initial creation. Operator review
-- of audit-service migrations is the defense. The line below is
-- INTENTIONALLY NOT detected by migrate-lint:
--   CREATE OR REPLACE FUNCTION audit_events_append_only() RETURNS TRIGGER
--   AS $$ BEGIN RETURN NEW; END; $$ LANGUAGE plpgsql;
