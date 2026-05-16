# v2 Implementation Plan - Next 90 Days

## Days 1-15: foundation

- Finalize monorepo structure.
- Set up CI, linting, tests, ADR workflow.
- Define canonical schemas.
- Build auth, org, project, resource, session skeleton.
- Create local Docker Compose development environment.

## Days 16-35: Remote Execution Cockpit alpha

- Integrate Guacamole for RDP/VNC/SSH.
- Add resource registry.
- Add session broker.
- Add mobile-first viewer shell.
- Add audit events.
- Add DMZ/VPN/LAN deployment profiles in docs.

## Days 36-55: Agentic Desktop alpha

- Desktop worker skeleton.
- Model gateway config.
- File and terminal tools.
- Approval timeline.
- QR pairing/dispatch.
- Local AI setup profile with LM Studio/Ollama/Continue.

## Days 56-75: Agentic Work Board alpha

- Ticket model.
- Kanban states.
- Ticket -> agent run lifecycle.
- Evidence packet model.
- Approval cards.
- Mobile approval view.

## Days 76-90: Harness and security hardening

- Agent Harness Runtime skeleton.
- Policy and capability edge model.
- Egress gateway design spike.
- Context pack builder.
- Risk register update.
- First internal demo.


## P2/P3 tracking guardrail

P2 Multimodal/Proactive Desktop and P3 AI Buddy/Avatar are tracked as future research/delight lanes only.
They must not consume MVP engineering capacity until Remote Cockpit, Agentic Desktop, Agentic Work Board,
Harness Runtime, Egress Gateway, and Memory/Context foundations are stable.
