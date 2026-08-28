# TEAM — Project Overlay (AI Remote Engineering OS, spec-set SPEC-2026.1)

> Overlay file. Read together with the base kit's `TEAM.md` at `../../../agency-kit/ai/war-room/TEAM.md`. This file adds only the project-specific roster on top of the generic war-room team.

## Project specialists

P0 lead roles:
- **Remote Cockpit Architect** — Remote Execution Cockpit, session broker, connector adapters.
- **Permission Model Reviewer** — capability edges, approvals, rate limits.
- **Audit / Evidence Reviewer** — append-only events, evidence packets, redaction.
- **Offline-Install Engineer** — air-gapped install, DMZ/VPN/LAN profiles, docker-compose hardening.

P1 lead roles:
- **MCP Gateway Reviewer** — typed MCP gateway, egress policy, prompt-injection defense.
- **Workstream Architect** — recurring stateful work, scoped memory, ticket→run lifecycle.

These specialists must be consulted (Consult or War Room mode) on any change touching their domain.

Role cards live in `ai/roles/specialists/` (project-specific, not in the generic kit).
