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
| `human-agent` | Operator login/session/CSRF; the approve/deny verb on a parked session **and on a parked tool invocation** | `services/api/internal/server/server.go` (`CreateSession`, `ResolveSession`, `ResolveInvocation`) |
| `agent-tool` | An agent's MCP `tools/call` on the gateway (the `remote.exec` tool) — scoped (SEC-16) to the agent token's resource; drives a governed invocation | `services/mcp-gateway/internal/server` (`/mcp`; audience `mcp-gateway`) |
| `agent-remote-session` | Cockpit WS attach only for a `pending` session — a parked `pending_approval` session is refused (the approval gate needs no broker code) | `services/session-broker/internal/server` (status guard) |
| `service-service` (api → policy-service) | `requires_approval` decision at session-create + operator resolution relay | `services/policy-service/internal/server/server.go` (decide + resolve handlers; audience `policy-service`) |
| `service-service` (api → mcp-gateway) | Operator invocation list + approve/deny, proxied off the browser plane | `services/mcp-gateway/internal/server` (internal `/v1/invocations*`; audience `mcp-gateway`, edge `service-service`) |
| `service-service` (gateway → policy / broker) | Per-invocation policy decide/resolve + the governed non-interactive exec | `policy-service` (audience `policy-service`) + `session-broker` `/v1/exec` (audience `session-broker`) |
| `service-service` (broker → connector-service) | Just-in-time credential lease — the only decrypt path | `services/connector-service/internal/server/server.go` (lease handler; audience `connector-service`) |

Approval rule (ADR-0054): a resource labeled `requires_approval=true` parks the
session-start until the operator resolves it; the decision authority is
policy-service (`pending_approvals`, fail-closed timeout sweeper in
`services/policy-service/internal/store/store.go`). Audit level: every decision
and resolution is chained (`policy.decision:*`, `approval.*`; SEC-14).

Invocation approval (v0.3.0; ADR-0057/0059): the same flag + the same policy
authority gate an agent tool invocation. The gateway parks the invocation and
**blocks** the agent's `tools/call` (block-and-wait) until the operator resolves it
via `api → gateway` (proxied); the operator sees the agent + the **verbatim command**
(ADR-0059, operator-only — never on the agent plane) before approving. Grant →
governed exec; deny/timeout → typed error, no egress (fail-closed, SEC-19). A
gateway reaper finalizes an invocation orphaned by a disconnected agent.
