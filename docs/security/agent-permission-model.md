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

## Per-workload key isolation (v0.4.0; ADR-0060)

At v0.3.0 every service — including the agent-facing `mcp-gateway` — held the one
symmetric cluster key (`AIREOS_PASETO_KEY`), which under PASETO v4 *local* both
signs and verifies. RCE on the gateway therefore meant *mint any token for any
edge* — including a `connector-service` lease that decrypts any credential. v0.4.0
removes that blast radius:

- **The gateway is keyless of the cluster (SEC-20).** The edges it touches move to
  PASETO **v4 public** (Ed25519): `services/api` signs the `agent-tool` token (and
  its `service-service` token to the gateway's internal plane) with a single
  agent-plane private key; the gateway verifies inbound with api's **public** key
  alone. The gateway signs its own outbound (→ policy / broker / audit) with its
  **own** private key; those peers verify it with the gateway's public key. The
  gateway boots with **no `AIREOS_PASETO_KEY`** and so can mint nothing.
- **Dual-mode verifier (`packages/go/auth`).** policy / audit / broker-exec accept
  *either* a `v4.local` token (the cluster key — api and the other symmetric
  callers) *or* the gateway's `v4.public` token, dispatching on the token type.
  Non-gateway planes (operator cookie, broker → connector, api → policy/broker)
  stay symmetric — only the agent-facing surface is de-keyed.
- **S2S subject-pins (R5-ext).** The broker `/v1/exec` handler rejects any caller
  whose token subject ≠ `mcp-gateway`; the connector lease handler rejects any
  subject ≠ `session-broker` — defense in depth over the `service-service` edge,
  mirroring the gateway internal plane's `subject == "api"` pin.
- **SEC-21 (proven).** With only its own key, a compromised gateway can forge no
  token connector or api accepts (their verifiers never hold the gateway's public
  key) — asserted by the WD-4 negative test and the installed `run-wd1.sh` check.
- **Command at-rest (F3, extends ADR-0059).** The persisted invocation command is
  AES-256-GCM encrypted with a gateway-local key distinct from the cluster and
  resource keys; decrypted only on the operator-only read path.

`connector-service` is deliberately **not** dual-mode — the broker (symmetric) is
its sole caller, so the credential-decrypt authority stays reachable only by the
cluster key, which the agent-facing gateway no longer holds.
