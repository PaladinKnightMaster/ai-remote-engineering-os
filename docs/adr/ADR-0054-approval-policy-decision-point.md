# ADR-0054: policy-service approval/policy decision point + `<ApprovalPrompt>` (the human-in-the-loop primitive)

## Status

**Proposed** — 2026-06-01. Recorded as part of the v0.2.0 "Governed Egress
Foundation" scoping consult (`docs/evaluation/v0.2.0-release-criteria.md`). **Not
yet Accepted**: ratified in the T-028 ADR-authoring consult, where the open
questions below are settled. Published Proposed now to anchor the v0.2.0 criteria
and reserve the ADR number.

**Builds on / conforms to:** ADR-0041/0043 (`services/policy-service` was always a
P0-planned Go capability-edge: "Policy engine. Capability-edge enforcement."),
ADR-0046 (capability tokens + edges), ADR-0045 (content-free chained-hash audit),
ADR-0049 (hash-in-chain audit content model), ADR-0047 (`<ApprovalPrompt>` was a
deferred `packages/ui` component, v0.1.0 §FE-5), `docs/security/agent-permission-model.md`
+ `docs/security/egress-policy.md`. **No ADR divergence (G2-clean):** this builds
the scaffolded policy-service and the deferred approval UI; it contradicts no
Accepted ADR.

## Context

CLAUDE.md doctrine: "No AI autonomy before audit + policy + **approval** primitives
exist." v0.1.0 shipped audit (chained-hash) + capability gating, but **no approval
primitive**: there is no point in the capability path where a privileged action
pauses for explicit human authorization, and `<ApprovalPrompt>` is still one of
the three deferred `packages/ui` components (v0.1.0 §FE-5 landed `<TerminalView>`
only). The v0.1.0 §10 deferral list ties the approval workflow to the "first
egress-touching ticket" — which is exactly the connector-service egress boundary
(ADR-0053) that v0.2.0 builds.

`services/policy-service/` exists (`.gitkeep` + README "Policy engine.
Capability-edge enforcement.") — scaffolded, never built. v0.2.0 activates it as
the **decision point**: given an actor + edge + target + requested action, it
returns allow / deny / **requires-approval**, and brokers the human approval
round-trip when the answer is requires-approval.

## Decision (direction — ratified in the T-028 consult)

1. **The decision point lives in `services/policy-service`**, not bolted onto
   `services/api`. It is a capability-edge service (ADR-0043) returning a typed
   decision (`allow` | `deny` | `requires_approval`) for an `(actor, edge, target,
   action)` tuple, consulted by the enforcing service before the action proceeds.
2. **`requires_approval` brokers a human round-trip.** A pending approval is
   surfaced to the operator via the (now-built) `<ApprovalPrompt>` `packages/ui`
   component over the typed client; the operator's approve/deny resolves the
   pending action.
3. **Every decision is audit-chained** (ADR-0045/0049 hash-in-chain): the policy
   decision and the human approve/deny each emit a chained-hash event
   (`policy.decision`, `approval.granted` / `approval.denied`), evidence carried as
   `evidencePacketId = sha256:<…>` — no raw payloads in the immutable chain.
4. **v0.2.0 trigger surface is deliberately small.** The wedge demonstrates the
   spine, not a rich policy catalog: at least one concrete action on the existing
   SSH/connector path routes through `requires_approval` (e.g. a session start /
   credential lease to a resource flagged `requires_approval`). Rich rule
   authoring + a broad action taxonomy are v0.3.0 (the MCP Gateway consumes this).

## Alternatives considered (sketch — expanded at ratification)

- **Approval logic inside `services/api` (no policy-service).** Rejected: spreads
  authorization decisions across enforcing services (the same diffuse-authority
  failure ADR-0053 fixes for credentials) and wastes the scaffolded policy-service.
- **Async approval queue only (no synchronous block).** Deferred as a v0.2.0
  default; the synchronous block-and-wait shape for the SSH wedge vs an async
  queue is OQ-C below.
- **Mobile approval delivery now** (roadmap "Mobile Approval"). Deferred: v0.2.0 =
  in-cockpit `<ApprovalPrompt>` only; mobile push is a v0.3.0+ delivery channel on
  the same decision point.

## Open questions (settle in the T-028 ratification consult)

- **OQ-C.** Synchronous block-and-wait vs async approval queue for the v0.2.0 SSH
  wedge; which concrete action(s) trigger `requires_approval`.
- **OQ-D.** Approval-request persistence: policy-service own store/migrations vs
  shared cockpit DB; timeout/expiry semantics for an un-actioned approval.
- **OQ-F.** Decision-point transport: sync RPC from the enforcing service
  (`edge: service-service`) vs in-process library — and which service enforces
  (api control-plane vs connector-service at the egress).

## Verification (blocks the v0.2.0 policy/approval tickets, not this ADR)

- **SEC-14:** every approve/deny decision emits a chained audit event; a denied
  action is blocked and chained.
- **FE (§FE-5 closure):** `<ApprovalPrompt>` lands in `packages/ui`,
  capability-gated, a11y-baseline per v0.1.0 §FE-7.
- **WD-2 (live, G1):** a `requires_approval` action surfaces `<ApprovalPrompt>`;
  approve proceeds + chains, deny blocks + chains — asserted end-to-end through
  the `tests/e2e` + `task wd1` harness on real wires (no mocked boundary).

## Related ADRs

- ADR-0046 — capability edges + tokens (the decision operates over edges).
- ADR-0045 / ADR-0049 — chained-hash, content-free audit (decision events).
- ADR-0047 — `<ApprovalPrompt>` as the deferred `packages/ui` component now built.
- ADR-0053 — connector-service egress boundary whose actions this gates.
