-- services/api runs.actor + runs.grant_id, migration 0009 (ADR-0062; v0.6 "Agent-
-- initiated Runs"). The actor/principal split (T-064). A v0.5 operator-direct Run has
-- operator = the launching operator and NO actor/grant (both NULL). An agent-initiated
-- Run (T-065) sets operator = the delegating operator (the PRINCIPAL + the owner for
-- listing/cancel, per the ownership decision), actor = the AGENT that drove it, and
-- grant_id = the delegation grant it was bounded by. Both columns are NULLABLE and
-- additive — behavior-preserving: nothing populates them until T-065's run.launch, so
-- every existing Run keeps actor/grant_id NULL and its run.* audit actor = operator.
ALTER TABLE runs ADD COLUMN grant_id UUID;   -- the bounding delegation grant (NULL = operator-direct)
ALTER TABLE runs ADD COLUMN actor    TEXT;   -- the agent that drove a delegated Run (NULL = operator-direct; actor = operator)
