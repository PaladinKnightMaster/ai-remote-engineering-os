-- Reverse migration 0009: drop runs.actor + runs.grant_id (ADR-0062, T-064).
ALTER TABLE runs DROP COLUMN actor;
ALTER TABLE runs DROP COLUMN grant_id;
