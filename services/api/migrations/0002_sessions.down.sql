-- Reverse of 0002_sessions. services/api is a standard mutable service, so a
-- destructive down is permitted here (unlike audit-service, ADR-0044).
DROP TABLE IF EXISTS sessions;
