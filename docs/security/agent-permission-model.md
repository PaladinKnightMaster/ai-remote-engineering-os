# Agent Permission Model

## Principle
Agents receive scoped capabilities, not ambient authority.

## Capability edges
A capability edge defines who/what can communicate with or act on another resource:
- human-agent
- agent-agent
- agent-tool
- agent-workspace
- agent-repo
- agent-memory
- agent-remote-session

Each edge defines allowed operations, context visibility, approval rules, rate limits, and audit level.

## Enforced edges at v0.2.0 (implementation references; DOC-9)

Every edge below is gated by `packages/go/middleware` `RequireCapability` over
PASETO v4 capability tokens (`packages/go/auth`, ADR-0046). `service-service` is
the eighth, infra-level edge ADR-0046 added to the seven above.

| Edge | Decision enforced | Handler |
|---|---|---|
| `human-agent` | Operator login/session/CSRF; the approve/deny verb on a parked session | `services/api/internal/server/server.go` (`CreateSession`, `ResolveSession`) |
| `agent-remote-session` | Cockpit WS attach only for a `pending` session — a parked `pending_approval` session is refused (the approval gate needs no broker code) | `services/session-broker/internal/server` (status guard) |
| `service-service` (api → policy-service) | `requires_approval` decision at session-create + operator resolution relay | `services/policy-service/internal/server/server.go` (decide + resolve handlers; audience `policy-service`) |
| `service-service` (broker → connector-service) | Just-in-time credential lease — the only decrypt path | `services/connector-service/internal/server/server.go` (lease handler; audience `connector-service`) |

Approval rule (ADR-0054): a resource labeled `requires_approval=true` parks the
session-start until the operator resolves it; the decision authority is
policy-service (`pending_approvals`, fail-closed timeout sweeper in
`services/policy-service/internal/store/store.go`). Audit level: every decision
and resolution is chained (`policy.decision:*`, `approval.*`; SEC-14).
