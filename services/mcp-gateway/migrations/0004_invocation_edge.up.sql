-- mcp-gateway migration 0004 (ADR-0061; v0.5.0 RUN-5). The bracket audit edge
-- (tool.invoked / tool.result) is now PER-INVOCATION: the agent plane records
-- 'agent-tool' (the default, preserving the v0.3/v0.4 chain), the operator Run engine
-- records 'human-agent' (an operator-initiated step is not an agent calling a tool).
-- Stored at Create, read back at Finish / FinalizeIfPending so the tool.invoked and
-- the terminal event carry the same edge. Existing rows + omitted inserts default to
-- 'agent-tool' (back-compat). Standard mutable service (ADR-0044) — destructive down
-- permitted.
ALTER TABLE invocations ADD COLUMN edge TEXT NOT NULL DEFAULT 'agent-tool';
