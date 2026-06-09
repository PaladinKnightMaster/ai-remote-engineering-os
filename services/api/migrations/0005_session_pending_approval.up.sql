-- services/api migration 0005 — add the 'pending_approval' session status
-- (ADR-0054 / T-031b). A session-start to a requires_approval resource is parked
-- 'pending_approval' until the operator approves (→ 'pending') or denies (→
-- 'closed'). The session-broker's existing status guard rejects any status not in
-- {pending, active}, so a 'pending_approval' session cannot open a WS until
-- services/api flips it to 'pending' on approval — the enforcement is purely
-- control-plane (ADR-0054 §5; the broker is unchanged).
--
-- Standard mutable service (only audit-service is forward-only, ADR-0044).
ALTER TABLE sessions DROP CONSTRAINT sessions_status_chk;
ALTER TABLE sessions ADD CONSTRAINT sessions_status_chk
    CHECK (status IN ('pending', 'pending_approval', 'active', 'closed', 'terminated'));
