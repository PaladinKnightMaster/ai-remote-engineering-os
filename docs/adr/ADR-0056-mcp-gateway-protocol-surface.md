# ADR-0056: Typed MCP Gateway protocol surface & trust boundary (`services/mcp-gateway`, the first agent surface)

## Status

**Accepted** — 2026-06-14 (ratified in the T-035 war-room consult). Recorded as
part of the v0.3.0 "Typed MCP Gateway" release
(`docs/evaluation/v0.3.0-release-criteria.md`). Binding spec for the v0.3.0
gateway tickets (T-035+).

**Builds on / conforms to:** ADR-0041/0043 (`services/mcp-gateway` is a new Go
capability-edge service on chi/net-http), ADR-0046 (capability tokens; the
`agent-tool` edge + the agent 5-min TTL tier), ADR-0045 (transactional
outbox → audit), ADR-0047 (same-origin appliance), ADR-0050 (the operator-cookie
audience model — referenced for *why* the operator plane stays on `services/api`),
`docs/architecture/mcp-gateway-architecture.md` (the "MCP-compatible, not
MCP-trusting" rule), `docs/security/agent-permission-model.md` (the `agent-tool`
edge). **No ADR divergence (G2-clean):** a net-new service that contradicts no
Accepted ADR.

## Context

The doctrine gate — *"No AI autonomy before audit + policy + approval primitives
exist"* — is satisfied as of v0.2.0. v0.3.0 builds the **first agent surface**.
MCP (Model Context Protocol) is the lingua franca for agent↔tool interaction, so
the surface should speak MCP — but the architecture rule is **MCP-compatible, not
MCP-trusting**: the gateway exposes safe internal facades and routes every call
through policy + connector + audit; it does not trust arbitrary MCP servers.
Agents also have **no authentication path** today (only operators + services do).

## Decision

1. **New service `services/mcp-gateway`** (Go, chi/net-http per ADR-0043), an MCP
   **server** exposing a typed tool catalog. Binds **`:8085`**; Caddy fronts it
   same-origin (OI-10).
2. **Transport = MCP streamable HTTP, synchronous POST subset at v0.3.0.** One
   HTTP endpoint; the agent POSTs a JSON-RPC 2.0 request and receives a JSON-RPC
   response. v0.3.0 implements `initialize` (advertising a **pinned MCP protocol
   version**), `tools/list`, `tools/call`, and JSON-RPC error envelopes.
   **Deferred** (feature-pull, v0.4.0): SSE server-streaming, `Mcp-Session-Id`
   server-side session state, and long-running / partial-result tool calls.
3. **Hand-rolled, behind a `ToolTransport` adapter — no third-party MCP SDK at
   v0.3.0.** The protocol surface is a thin, replaceable edge; the governance core
   (invocation lifecycle + policy + audit, ADR-0057) is **protocol-agnostic**.
   Rationale: the minimal-dependency / offline / supply-chain doctrine
   (ADR-0042/0046/0047), MCP spec churn, and that the gateway's value is
   *governance*, not the wire format. **SDK adoption is a recorded future option**,
   triggered only by (a) real conformance burden *and* (b) a mature, well-licensed
   Go MCP SDK that fits the offline posture — and is then a **localized adapter
   swap, not a rewrite**. Conformance of the hand-rolled subset is proven in WD-3
   against a real MCP client.
4. **Auth = PASETO Bearer capability token, `agent-tool` edge, audience
   `mcp-gateway`** (ADR-0046). The MCP endpoint is gated by
   `RequireCapability(v, "agent-tool")` with a verifier built for audience
   `mcp-gateway`. Agents are programs → **Bearer**, never the browser cookie path;
   **not** MCP's OAuth. Tokens are short-lived (agent TTL, 5 min).
5. **Two planes, one browser-free.** The gateway serves (a) the **agent plane** —
   the MCP endpoint (Bearer) — and (b) **internal service-service endpoints**
   consumed by other services. It has **no direct browser surface**: all
   operator-facing HTTP (approve/deny + list pending invocations) is served by
   **`services/api`** and proxied to an internal gateway endpoint (the
   audit-read-proxy / api↔policy pattern, T-024 / T-031b). The HttpOnly-cookie +
   CSRF + audience-`api` concerns stay in `services/api` (ADR-0050) and are **not**
   re-implemented in the gateway.
6. **Trust posture — internal facades only.** v0.3.0 exposes only tools whose
   egress the platform owns; the gateway is an MCP **server**, not an MCP **client**
   of any third party. External/untrusted-server proxying, tool discovery,
   typed-client codegen, and sandbox isolation are **deferred** (criteria TR-2;
   promotion trigger: WD-3 green on `main` + a scoped external-server ADR).
7. **No secret custody.** The gateway holds no `AIREOS_RESOURCE_SECRET_KEY` and has
   no decrypt path (SEC-17; the SEC-13 grep gate extends to it). It holds the
   cluster PASETO key only to **verify** agent tokens and (later) to **mint** the
   service tokens it uses to call policy/broker and the `agent-tool` tokens it
   issues (ADR-0046 shared-key model, consistent with api/broker).

## Consequences

- A new, **owned, small** protocol surface — conformance is our responsibility, but
  it is tested in WD-3 against a real MCP client and the subset is stable.
- **Offline-clean:** zero new third-party dependency; `task licenses` unaffected.
- The gateway becomes the **orchestration layer** the v0.2.0 spine was designed to
  feed; the governance contracts live in ADR-0057.
- The hand-rolled choice trades a little protocol-tracking effort for supply-chain
  cleanliness and version control; the adapter boundary keeps the SDK door open.

## Deferred (recorded)

SSE / streaming + MCP session state; multi-tool catalog + discovery; external /
untrusted-server proxying + sandbox isolation; SDK adoption (with its trigger).
