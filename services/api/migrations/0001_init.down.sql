-- Reverse of 0001_init. services/api is a standard mutable service, so a
-- destructive down is permitted here (unlike audit-service, ADR-0044).
DROP TABLE IF EXISTS resources;
