-- Reverse migration 0005: drop invocations.principal (ADR-0062, T-064).
ALTER TABLE invocations DROP COLUMN principal;
