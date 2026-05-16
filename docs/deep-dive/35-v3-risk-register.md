# v3 Risk Register

| Risk | Impact | Mitigation |
|---|---|---|
| Tool/plugin supply-chain attack | Critical | Reviewed skill registry, signatures, sandbox, allowlists |
| MCP server leakage/destructive action | Critical | Typed MCP Gateway, approvals, egress gateway, audit |
| Memory cross-tenant leakage | Critical | Scoped memory, access policy, audit, export/redaction |
| Agent runaway loops | High | rate limits, budgets, timeouts, stuck-agent detection |
| Too much scope | High | P0/P1/P2/P3 ladder and phased implementation |
| Screenshot-only automation fragility | Medium | accessibility/DOM first, vision fallback |
| Local model underperformance | Medium | model gateway, oracle escalation, frontier BYOK when allowed |
