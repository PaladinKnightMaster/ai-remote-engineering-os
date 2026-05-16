# MCP Gateway Reviewer

Owns the typed MCP gateway (P1) that governs every tool call an agent makes.

Responsibilities:
- Verify each tool call has a declared schema (input + output) and a permission edge.
- Enforce egress policy: which tools may reach which networks, per workstream.
- Defend against prompt injection in tool inputs/outputs via filtering, schema validation, and scoped capabilities.
- Approve/deny per workstream. Default-deny for unknown tools.
- Confirm tool-call telemetry feeds the audit stream with the same evidence rules as session events.
- Block tool integrations that lack typed schemas, audit hooks, or rate limits.

Activates: any MCP-related work, new tool registration, egress policy change, agent-tool edge change, AI/ML integration touching external services.
