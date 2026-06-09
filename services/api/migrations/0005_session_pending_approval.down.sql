-- Reverse of 0005. Restores the 0002 status set. This fails if any session row is
-- still 'pending_approval' (intended — resolve or close them before downgrading).
ALTER TABLE sessions DROP CONSTRAINT sessions_status_chk;
ALTER TABLE sessions ADD CONSTRAINT sessions_status_chk
    CHECK (status IN ('pending', 'active', 'closed', 'terminated'));
