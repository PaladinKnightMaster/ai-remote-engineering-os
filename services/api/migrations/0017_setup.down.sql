-- Reverse migration 0017: drop the first-run setup tables (ADR-0078). Destructive is
-- permitted on api (mutable service, ADR-0044). The index drops with its table.
DROP TABLE IF EXISTS operators;
DROP TABLE IF EXISTS setup_codes;
