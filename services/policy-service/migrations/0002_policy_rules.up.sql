-- policy-service migration 0002 (ADR-0074, T-148). Adds the rule store the
-- decision point has never had: policy_rules. This is the store only — no
-- evaluator (T-149), no routes (T-151), no audit events (T-150). An empty table
-- changes nothing until T-149 consults it (CON-1).
--
-- Standard mutable service (only audit-service is forward-only, ADR-0044) — a
-- destructive down is permitted, same as 0001_policy.

-- policy_rules: an operator-authored rule over the (subject, edge, action,
-- target) tuple ADR-0054 §1 already specified. GLOBAL — deliberately NO
-- principal/owner column. The product is single-operator by design (v0.13.0
-- S-23 / DOC-33); the nearest-looking precedent, delegation_grants
-- (services/api/migrations/0008_delegation_grants.up.sql), IS principal-scoped
-- and is the WRONG thing to copy here — a `deny` rule scoped to one operator
-- would be a governance hole, letting another operator's agent walk past a
-- prohibition that was never theirs to bypass. The correct precedent is
-- `resources` (services/api/migrations/0001_init.up.sql), which also has no
-- owner column: operator-authored global config.
--
-- Wildcards are the LITERAL string '*', every column NOT NULL — not NULL-as-
-- wildcard. NULL-as-wildcard makes every future match query three-valued
-- (subject = $1 OR subject IS NULL, times four columns) and the constraints
-- unreadable; '*' keeps matching a plain equality-or-literal comparison the
-- evaluator (T-149) can express directly.
CREATE TABLE policy_rules (
    id          UUID        PRIMARY KEY,              -- UUID v7, minted by the store
    label       TEXT        NOT NULL DEFAULT '',       -- operator-facing name
    subject     TEXT        NOT NULL,                  -- exact value or '*'
    edge        TEXT        NOT NULL,                  -- exact value or '*'
    action      TEXT        NOT NULL,                  -- exact value or '*'
    target      TEXT        NOT NULL,                  -- exact value or '*'
    outcome     TEXT        NOT NULL,                  -- the decision this rule states
    enabled     BOOLEAN     NOT NULL DEFAULT true,      -- disabled rules are kept, not deleted
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT policy_rules_outcome_chk CHECK (outcome IN ('allow', 'requires_approval', 'deny')),
    -- One rule per exact match tuple — a duplicate tuple is a store-level
    -- conflict (ErrDuplicateRule), not a second, silently-shadowed row.
    CONSTRAINT policy_rules_tuple_uq UNIQUE (subject, edge, action, target)
);

-- Serves the future evaluator's (T-149) lookup of live rules for a given
-- edge+action: only enabled rows matter to a decision, so the index excludes
-- disabled ones entirely rather than filtering them out of a full scan.
CREATE INDEX policy_rules_enabled_idx ON policy_rules (edge, action) WHERE enabled;
