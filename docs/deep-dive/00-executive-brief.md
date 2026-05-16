# Executive Brief

## Decision

Split the platform into three independent but protocol-compatible products:

1. **Remote Execution Cockpit** - self-hosted browser/mobile control of internal PCs, GUI workspaces, terminals, demos and audit trails.
2. **Agentic Desktop System** - local desktop AI worker that can use files, terminal, browser, IDE/apps and model providers.
3. **Agentic Work Board** - Jira/Linear/Plane-style board where tickets own agent runs, approvals, evidence and reviews.

The future platform combines these into a self-hosted AI-native engineering OS with Jira-like work management, Confluence-like knowledge management and GitHub/GitLab-like forge integration.

## Why this is the best structure

The combined scope is too large for one MVP. Splitting creates clean product value:

- Project A works even without AI.
- Project B works even without company remote access.
- Project C gives agents and humans a shared system of record.
- The combined product becomes more defensible than any single piece.

## MVP promise

> Open a phone or browser, authenticate, access approved internal PCs/workspaces, control or share them, start or review ticket-owned agent work, approve risky actions, and preserve an audit trail - all self-hosted.

## North-star promise

> A self-hosted engineering operating system where humans and AI agents plan work, document knowledge, change code, operate PCs/workspaces, produce evidence and ship software under company governance.

## Non-negotiable constraints

- Runtime can operate in restricted/private networks.
- No direct public exposure of RDP, SSH or VNC.
- VPN-first and DMZ-first deployment modes are supported.
- QR pairing transfers short-lived pairing state only, never credentials.
- AI agents are approval-first and policy-gated.
- Offline install bundle is part of release quality.
- Existing tools are integrated first; full clones are deferred.

## Immediate build order

1. Remote Execution Cockpit MVP.
2. Agentic Desktop MVP.
3. Agentic Work Board MVP.
4. Shared protocol/integration layer.
5. Future knowledge base and code forge integrations.
