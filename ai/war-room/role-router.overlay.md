# Role Router — Project Overlay (AI Remote Engineering OS v3.0.1)

> Overlay file. Read together with the base kit's `role-router.md` at `../../../agency-kit/ai/war-room/role-router.md`. This file adds project-specific routing on top of the generic groups.

## Project routing additions

- **Engineering** additions: Remote Cockpit Architect, MCP Gateway Reviewer, Workstream Architect.
- **Operations** additions: Offline-Install Engineer.
- **Security / Governance** additions: Permission Model Reviewer, Audit / Evidence Reviewer.

## Escalation rule

Any change touching `services/audit-service`, `services/policy-service`, `services/session-broker`, or `docs/security/` requires at least one of (Permission Model Reviewer, Audit / Evidence Reviewer, Security Reviewer) in Consult mode.
