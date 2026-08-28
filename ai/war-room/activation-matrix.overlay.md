# Activation Matrix — Project Overlay (AI Remote Engineering OS, spec-set SPEC-2026.1)

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

## Consult gates (mandatory, P0+)

Every war-room consult must clear these two gates **before sign-off**. They exist because both failure modes actually occurred during the SSH slice (T-016…T-020) and were caught late.

- **G1 — End-to-end verification lens.** If a change spans **≥2 services or processes** (e.g. api ↔ session-broker ↔ audit-service, or browser ↔ broker), the consult MUST add an integration/e2e-verification lens and name the **live cross-boundary test** (the real wire — not a mock/fake) as an acceptance item before sign-off. "Unit/integration tests pass with the other side mocked" is **not** "verified end-to-end." Mocked-boundary tests hide coordination bugs (the T-017/T-018 close-coordination audit gap passed every mocked test; T-020b is the retro-fit e2e proof). When a same-session live run isn't feasible, the consult must file the e2e proof as an explicit, release-gating follow-up ticket — not assume correctness.

- **G2 — ADR-divergence gate.** If any option on the table contradicts an Accepted ADR (its text **or** its intent), the consult MUST state the divergence **explicitly at decision time** and resolve it one of two ways: (a) conform to the ADR, or (b) file a new ADR recording the deviation. Never silently choose the divergent path. (T-017b's lifecycle-only audit silently diverged from ADR-0048 §6; corrected in T-019a and ratified in ADR-0049 — it should have been flagged in the T-017b consult.)
