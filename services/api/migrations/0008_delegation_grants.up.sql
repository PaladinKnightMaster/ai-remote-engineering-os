-- services/api delegation_grants schema, migration 0008 (ADR-0062; v0.6 "Agent-
-- initiated Runs"). api owns the delegation_grants table in the shared cockpit DB
-- (ADR-0048), the same own-tables pattern as runs/run_steps. An operator issues a
-- scoped, revocable grant; an agent later launches a governed Run naming the grant,
-- and api validates the plan against it (T-065). The grant is a SERVER-SIDE record,
-- never a bearer token the agent holds — so the agent mints nothing (SEC-26) and
-- revocation is instant (status flip, T-063; cascade to in-flight Runs, T-066).
-- Standard mutable service (only audit-service is forward-only, ADR-0044) — a
-- destructive down is permitted.

-- delegation_grants: one operator-issued delegation (ADR-0062). id (UUID v7,
-- time-ordered) is the grant:<id> audit anchor + the list cursor. principal is the
-- delegating operator (the lent authority + the owner for listing/revoking); agent is
-- the grantee subject (bound to the agent token's subject at launch, T-065). resources
-- and verbs bound the plans the agent may launch; the integer bounds + step_budget cap
-- its autonomy. "expired" is COMPUTED (now > expires_at) at plan-vs-grant time — it is
-- NOT a stored status, so no sweeper is needed. step_budget is the total-steps cap; its
-- consumption is computed from the grant's runs at launch (T-065), so no counter column.
CREATE TABLE delegation_grants (
    id                  UUID        PRIMARY KEY,                  -- UUID v7
    principal           TEXT        NOT NULL,                     -- delegating operator (owner)
    agent               TEXT        NOT NULL,                     -- grantee agent subject
    label               TEXT        NOT NULL DEFAULT '',          -- operator-facing name (UI)
    resources           TEXT[]      NOT NULL,                     -- granted resource ids
    verbs               TEXT[]      NOT NULL,                     -- granted operations (e.g. {exec})
    expires_at          TIMESTAMPTZ NOT NULL,
    max_steps_per_run   INTEGER     NOT NULL CHECK (max_steps_per_run   > 0),
    max_concurrent_runs INTEGER     NOT NULL CHECK (max_concurrent_runs > 0),
    step_budget         INTEGER     NOT NULL CHECK (step_budget         > 0),  -- total-steps cap
    status              TEXT        NOT NULL DEFAULT 'active',
    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT delegation_grants_status_chk CHECK (status IN ('active', 'revoked'))
);

-- The operator's grant list is principal-scoped (owner-only, like runs).
CREATE INDEX delegation_grants_principal_idx ON delegation_grants (principal);

-- Plan-vs-grant (T-065) looks up a specific agent's live grants; a partial index keeps
-- it cheap as revoked rows accumulate.
CREATE INDEX delegation_grants_agent_active_idx ON delegation_grants (agent) WHERE status = 'active';
