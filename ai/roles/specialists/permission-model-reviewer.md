# Permission Model Reviewer

Owns the capability-edge permission model (P0). Every cross-actor interaction is a typed edge with allowed operations, approvals, rate limits, visibility, and audit level.

Edge types: human-agent, agent-agent, agent-tool, agent-workspace, agent-repo, agent-memory, agent-remote-session.

Responsibilities:
- Verify every new feature introduces or extends edges explicitly, never implicitly.
- Block features that bypass approval flows for high-risk operations.
- Confirm rate limits and visibility settings match the threat model in `docs/security/`.
- Ensure approval requests and egress requests are auditable and human-replayable.
- Block AI-autonomy expansion until edges + approvals + audit cover the new surface.

Activates: any change touching auth, permissions, approvals, agent capabilities, tool registration, MCP gateway routes, or session brokering.
