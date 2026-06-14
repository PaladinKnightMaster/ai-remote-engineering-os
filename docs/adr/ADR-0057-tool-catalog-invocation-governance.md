# ADR-0057: Typed tool-catalog schema, tool→capability-edge mapping & invocation-anchored governance

## Status

**Accepted** — 2026-06-14 (ratified in the T-035 war-room consult). Part of the
v0.3.0 "Typed MCP Gateway" release (`docs/evaluation/v0.3.0-release-criteria.md`).
Binding spec for the v0.3.0 tool-catalog + invocation tickets (T-037, T-039,
T-040).

**Builds on / conforms to:** ADR-0056 (the gateway protocol surface + planes),
ADR-0053 (connector credential lease), ADR-0054 (policy decide/resolve + the
approval primitive — **reuses its opaque correlation key**), ADR-0046 (the seven
edges), ADR-0045/0049 (hash-in-chain audit), ADR-0047 (`<ApprovalPrompt>` reuse).
**No ADR divergence (G2-clean):** notably **ADR-0054 is unchanged** — policy
already treats its `session_id` field as an opaque required key, so the gateway
reuses it for invocations without a contract change.

## Context

A tool call must be governed by the v0.2.0 spine (policy decision → human
approval → connector custody → audited egress). But the v0.2.0 approval primitive
is **session-anchored** (`policy.Decide` and `pending_approvals` key on a
`session_id`), and an agent tool invocation is **not** a session. The plan
requires `<ApprovalPrompt>` to gate a *tool invocation*. This ADR generalizes the
primitive without reopening ADR-0054.

## Decision

1. **Typed tool definition.** A tool declares `{name, inputSchema (JSON Schema),
   edge, targetResolver, action, requires_approval source}`. The one v0.3.0 tool
   **`remote.exec`** declares edge **`agent-remote-session`**, `targetResolver` =
   `resource:<resourceId from typed input>`, action `exec`, and reads
   `requires_approval` from the **resource label** (reuses the v0.2.0 flag). At
   v0.3.0 the catalog is a **Go-defined registry of one tool**; a declarative
   multi-tool catalog is later (v0.4.0).
2. **Invocation** = a gateway-owned record (`invocations` table + migration,
   T-039) with its own id and an **idempotency key** (PERF-2: a retried invocation
   — agent retry / network blip — reconciles to the original outcome and does
   **not** double-execute the side-effecting command). The gateway is the
   orchestrator.
3. **Policy via the opaque correlation key — no ADR-0054 change.** The gateway
   calls `policy.Decide` with `session_id = invocation_id` (policy treats it as an
   opaque required string, verified against
   `services/policy-service/internal/server/server.go`). `requires_approval` → the
   invocation **parks**. The fail-closed timeout sweeper (ADR-0054 §6) applies
   unchanged.
4. **Operator approval via `services/api`, not the gateway.** The operator
   approves/denies a parked invocation through **api** (`POST
   /v1/invocations/{id}/resolve` + a pending-list read), which proxies to an
   **internal** gateway endpoint (the audit-read-proxy / api↔policy pattern, T-024
   / T-031b). The operator browser talks only to api: HttpOnly cookie (audience
   `api`) + double-submit CSRF stay in api (ADR-0050); the gateway implements **no**
   browser auth. api → gateway is service-service; the gateway → `policy.Resolve`.
   `<ApprovalPrompt>` (presentational, T-031c) is **reused**; the operator
   "pending invocations" surface is the new consumer (T-040 — the T-034 DoD
   addendum applies: browser-rendered evidence).
5. **Governed egress.** On a positive decision the gateway leases the credential
   from connector-service (reuse) and runs the command via the **broker
   non-interactive exec** path (ADR-0058 if warranted; the broker stays the **sole
   SSH egress** engine, preserving host-key pinning + cast/audit). **Fail-closed
   (SEC-19):** policy unreachable / lease failure / exec failure → no egress, typed
   error to the agent. There is **no path** to egress without a positive policy
   decision *and* a successful credential lease.
6. **Audit.** The gateway emits `tool.invoked` (actor `agent:<id>`, edge
   `agent-tool`, evidence `invocation:<id>`) + `tool.result` via its outbox
   (AUD-9); the broker exec emits a **stdout-SHA-anchored** egress event (AUD-10).
   The full chain `tool.invoked → policy.decision → approval.* → credential.leased
   → exec → tool.result` is replayable and hash-intact (AUD-11).
7. **Resource safety.** A **per-agent concurrency cap** bounds in-flight
   invocations (PERF-3); the broker exec path enforces **bounded output + command
   timeout** (PERF-1). Full per-edge rate-limiting / quotas defer to v0.4.0.

## Consequences

- **policy-service and connector-service are unchanged** — the generalization from
  session→invocation costs zero contract change (the opaque-key reuse is the key
  enabler).
- The **gateway** is the new orchestrator (invocation lifecycle + idempotency +
  concurrency + fail-closed); **`services/api`** gains the operator invocation
  routes (proxy); the **session-broker** gains a non-interactive exec mode.
- `<ApprovalPrompt>` proves its v0.2.0 design intent ("the first approval-touching
  action ticket… v0.3.0 reuses it for MCP tool-call approval").

## Deferred (recorded)

Multi-tool declarative catalog; segregation-of-duties / async approval queue
(ADR-0054 deferred); full rate-limiting + quotas; per-tool fine-grained policy
beyond the resource flag.
