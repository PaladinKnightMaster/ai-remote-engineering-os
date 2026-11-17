# ADR-0080: Agents are enrolled in the browser and exchange a revocable credential for short-lived tokens

## Status

**Proposed** — 2026-11-17 (v1.0.0 Beta 1, theme A1 "agent onboarding in the browser"). Commander seat.
**Decider: CEO.**

**Refines ADR-0046** (auth adapter): the agent tier keeps its **5-minute token**, but its refresh no longer
depends on a live operator session — the same kind of declared divergence ADR-0068 D4 made for paired
sessions. **Conforms to ADR-0056/0057** (the gateway's protocol surface and SEC-16 token scope),
**ADR-0060** (agent tokens signed with api's asymmetric key; the gateway stays keyless) and **ADR-0062**
(delegation grants stay the authority for agent-launched Runs). Supersedes nothing.

---

## Context

**The goal.** Beta 1 (ruled 2026-11-16) must let an operator connect an AI agent from the browser and
watch it do governed work [read: `docs/product/beta1-scoping/ecosystem-map.md` §7]. The terminal
inventory ranked agent connection the product's weakest journey [read:
`docs/product/beta1-scoping/terminal-inventory.md` J7].

**How an agent gets a credential today.**
- The only mint path is `POST /v1/agent-tokens`, operator-gated by the cookie session plus double-submit
  CSRF [read: `services/api/internal/server/server.go:1295`; `docs/api-contracts/openapi.v3.fragment.yaml:505`].
  No cockpit screen calls it; the only example is a test script. An integrator must therefore put **the
  operator's password in a script**, which grants that script full operator power.
- The token lives **5 minutes** (`AgentTTL`) [read: `packages/go/auth/auth.go:21`]. ADR-0046 designed refresh
  only "while the parent operator session is active" [read: `docs/adr/ADR-0046.md:44`], so an agent cannot
  outlive the operator's browser session.
- The token's scope is **one resource plus allowed operations** (`auth.Scope{Resource, Operations}`), and
  the gateway enforces it on every call (SEC-16) [read: `packages/go/auth/auth.go:39`;
  `services/mcp-gateway/internal/catalog/catalog.go:881`].

**What real clients do — measured** [ran: `docs/spike/b041-deep-pass.md` §7–§8, 2026-11-17]:
- Claude Code, the OpenHands SDK and openworker each connected to the real gateway code over its existing
  HTTP transport and called `remote.exec` with a plain `Authorization: Bearer` header. **No adapter was
  needed for the transport.**
- Static-header clients **die at token expiry**. Claude Code's `headersHelper` re-runs on a `401` and
  refreshed a short-lived token **unattended**; Codex documents the same (`http_headers_helper`).
- **There is no endpoint a helper could call** — every mint needs the operator cookie. Unattended
  refresh is impossible today in every client, including the ones built for it.
- OAuth does not work against us: no `WWW-Authenticate`, no `/.well-known/*` metadata, no registration
  endpoint; clients attempted dynamic client registration and failed.

---

## Decision

**An operator enrolls each agent in the cockpit. Enrollment yields a long-lived, revocable credential,
shown once; the agent exchanges it at a new endpoint for the same 5-minute agent-tool token as today.**

1. **Enrolled agents.** A new api-owned registry: each agent has a name (its subject `agent:<name>`), the
   enrolling operator, **one resource and its allowed operations** (exactly today's token scope — SEC-16
   unchanged), an optional expiry, `created_at`, `revoked_at` and `last_exchanged_at`. The cockpit lists,
   enrolls and revokes agents. A paired phone session cannot (deny-by-default, ADR-0068 D5).
2. **The enrollment credential.**
   - 32 random bytes, encoded URL-safe, with a fixed recognisable prefix (e.g. `aireos_agent_`) so secret
     scanners and humans can spot one.
   - **Shown once** at enrollment, with a copy button; **stored only as a hash**; never logged, never in an
     audit event, never returned again. Lost = revoke and enroll anew.
   - Valid until revoked or until its optional expiry. **Rotation = enroll a replacement, then revoke.**
3. **The exchange endpoint: `POST /v1/agent-tokens/exchange`.** No cookie, no CSRF: the request carries
   the enrollment credential as `Authorization: Bearer`. api checks the hash, the revocation and expiry,
   then mints the **same 5-minute agent-tool token** `IssueAgent` mints today, scoped to the agent's
   registered resource and operations. Every exchange is audit-chained as `agent.token.exchanged` (actor
   `agent:<name>`, no credential material), keeping ADR-0046's "every refresh is audited". Rate-limited
   per credential; failures share one response, so the endpoint is no oracle.
4. **Revocation takes effect within one token lifetime.** Revoking stops new exchanges at once; tokens
   already minted live out their ≤5 minutes. That bound is ADR-0046's own, now applied per agent.
5. **ADR-0046's refresh rule changes for this tier, declared.** An enrolled agent refreshes **without** a
   live operator session — that is the point. Its authority is bounded by the operator's enrollment act,
   the single-resource scope, revocation, and the audit of every exchange.
6. **Client integration is "a helper that calls the exchange".**
   - The cockpit's enrollment screen shows ready-to-paste configuration for clients with a refresh hook —
     Claude Code (`headersHelper`) and Codex (`http_headers_helper`) — as a single command that calls the
     exchange endpoint and prints the header.
   - Clients **without** a refresh hook work for at most one token lifetime; the cockpit and the guide say
     so plainly rather than pretend otherwise.
7. **The gateway's `401` names what happened.** It sends `WWW-Authenticate: Bearer` with
   `error="invalid_token"` and distinguishes **expired** from **malformed** (deep pass M7), so a helper
   knows to refresh.
8. **The private CA is offered for download.** The cockpit serves the CA root certificate (public data)
   with its fingerprint, and the integrator guide shows how each client trusts it.

---

## Alternatives considered

1. **Keep the operator-cookie mint; document a script.** It puts the operator's full power in a script
   on the agent's host, and refresh still dies with the operator session. That is today's state, ranked
   the product's worst journey. Rejected.
2. **Lengthen the agent token** (hours or days) and keep static headers. It works with every client, but
   revocation becomes "wait hours" or "rotate the signing key for everyone" (ADR-0046 §Revocation). It
   trades the agent tier's main safety property for convenience. Rejected.
3. **An OAuth 2.1 authorization server** (MCP authorization spec: discovery, dynamic registration,
   refresh tokens). It is the standard and some clients support it. But it is a large new security
   surface, interactive flows suit humans rather than unattended agents, and the two most important
   clients already refresh through a helper. **Deferred, not rejected.** Trigger: a target client that
   supports only OAuth.
4. **Let the gateway accept the enrollment credential directly.** Simplest for clients. But the gateway
   would then hold verification state for a long-lived secret, contradicting ADR-0060's keyless gateway,
   and every tool call would carry the long-lived secret instead of a 5-minute token. Rejected.
5. **Multi-resource agents.** Widen `auth.Scope` to a set of resources. Useful, but it changes the token
   format and SEC-16's enforcement. Deferred; for Beta 1, one agent per resource, with delegation grants
   (ADR-0062) remaining the way an agent is given multi-step authority.

---

## Consequences

**Gained:**
- No operator password ever lives on an agent's host. Connecting an agent is a cockpit action plus a
  pasted configuration.
- Unattended agents become possible for clients with a refresh hook — measured, not assumed.
- Per-agent revocation and per-agent attribution in the audit chain.

**Costs, accepted deliberately:**
- ⚠ **A long-lived bearer credential now exists on the agent's host.** Whoever holds it can act as that
  agent — within its one resource, its operations and every policy and approval gate — until it is
  revoked. That is strictly less power than today's operator password in a script, and it is visible:
  the cockpit shows `last_exchanged_at`, and every exchange is chained.
- **Audit volume grows:** about twelve `agent.token.exchanged` events per hour per active agent.
  Accepted, because ADR-0046 made "every refresh is audited" a property; coalescing would need its own
  decision.
- **Clients without a refresh hook remain short-lived.** Stated, not hidden.
- **New surface:** one table, one exchange route, cockpit screens, and the `401` header change.

---

## How we would know this was wrong

- **Integrators keep pasting the enrollment credential into static-header clients and hitting expiry.**
  Then the guide or the client list is wrong; the answer is not a longer token.
- **An enrollment credential leaks and is used.** The audit chain must show the exchanges and the
  revocation must stop them within five minutes; if either fails, the design failed.
- **A target client supports only OAuth.** Alternative 3's trigger has fired: that is its own ADR.
- **Operators enroll one agent per server and find it unworkable.** Alternative 5's trigger has fired.
