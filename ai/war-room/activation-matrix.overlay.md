# Activation Matrix — Project Overlay (AI Remote Engineering OS v3.0.1)

> Overlay file. Read together with the base kit's `activation-matrix.md` at `../../../agency-kit/ai/war-room/activation-matrix.md`. This file adds priority/module-specific activations on top of the generic activation matrix.

## Project activation (P0 / P1 / P2)

| Priority | Module | Lead role | Consult |
|---|---|---|---|
| P0 | Remote Cockpit | Remote Cockpit Architect | Security Reviewer, Tech Lead |
| P0 | Audit / Evidence | Audit / Evidence Reviewer | Spec Architect, Security Reviewer |
| P0 | Permission Model | Permission Model Reviewer | Security Reviewer, Tech Lead |
| P0 | Offline Install | Offline-Install Engineer | Release Guard, Performance Engineer |
| P1 | MCP Gateway | MCP Gateway Reviewer | Security Reviewer, AI/ML Reviewer |
| P1 | Workstreams | Workstream Architect | PM Strategist, Tech Lead |
| P1 | Memory | Workstream Architect | Security Reviewer, Token Governor |
| P2 | Workflow Compiler | (research only) | Spec Architect |
| P2 | Desktop Automation | (research only) | Security Reviewer |

Priority-ladder rule: do not activate P1/P2/P3 implementation work unless an explicit ticket exists in `.github/ISSUE_TEMPLATE/ticket.md` form and the ADR queue covers the decision.
