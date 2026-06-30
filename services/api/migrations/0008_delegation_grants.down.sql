-- Reverse migration 0008: drop the delegation_grants table (ADR-0062). Destructive is
-- permitted on api (mutable service, ADR-0044). The indexes drop with the table.
DROP TABLE IF EXISTS delegation_grants;
