-- Reverse migration 0016: drop both pairing tables (ADR-0068). Destructive is
-- permitted on api (mutable service, ADR-0044). The indexes drop with the tables.
DROP TABLE IF EXISTS paired_sessions;
DROP TABLE IF EXISTS pairing_requests;
