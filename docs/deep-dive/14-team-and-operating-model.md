# Team and Operating Model

## Recommended war-room team

| Role | Responsibility |
|---|---|
| Product Lead | ICP, scope, roadmap, customer validation |
| Tech Lead / Architect | System boundaries, ADRs, API contracts, integration strategy |
| Principal Backend Engineer | API, session broker, policy, audit, board workflow |
| Principal Frontend Engineer | PWA cockpit, board UI, mobile controls |
| Desktop Engineer | Tauri/Electron app, local worker, OS integration |
| Staff AI/ML Engineer | Model gateway, agent adapters, context, evaluation |
| Network/Infra Engineer | DMZ/VPN, routing, DNS, certificates |
| DevOps/Platform Engineer | CI/CD, offline bundles, installer, registry |
| Cybersecurity Engineer | Threat model, RBAC, sandboxing, approvals, audit |
| QA/SDET | Session reliability, offline install, browser/device matrix |
| UI/UX Designer | Mobile-first remote control, board and approval UX |
| Technical Writer | Docs, ADRs, runbooks, install guides |
| Solutions Engineer | Customer lab deployments and feedback |
| Developer Advocate / Marketing | Demos, launch assets, positioning later |
| Legal/Open-source Reviewer | License and compliance review before embedding/forking |

## Operating cadence

- Weekly architecture review.
- Weekly product scope review.
- Biweekly threat model update.
- Sprint planning with strict MVP boundary.
- ADR required for major dependency or security decision.
- Release readiness checklist before every bundle.

## Working agreements

- Integrate before building clones.
- Every risky action needs a policy.
- Every state change emits an event.
- Every release installs offline in a clean lab.
- Every AI capability has a human fallback.
