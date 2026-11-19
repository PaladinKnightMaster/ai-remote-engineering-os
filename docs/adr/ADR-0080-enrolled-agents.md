# ADR-0080: Agents get a revocable access key in the browser; a local bridge turns it into short-lived tokens

## Status

**Proposed** — 2026-11-17 (v1.0.0 Beta 1, theme A1 "agent onboarding in the browser"). Commander seat.
**Decider: CEO.**

**Revised 2026-11-18 (still Proposed).** Two CEO directions shaped the revision:
- **"Check, then revise"** produced D7, the local `aireos-mcp-bridge`. Cline ran through a prototype for
  14 minutes across two token renewals [ran: `docs/spike/bridge-check-2026-11-18.md`].
- **"Manage keys like other MCP servers (Jira, Confluence): 30, 60, 90 days, never, custom"** produced D2,
  access-key lifetimes and management, after a survey of how Atlassian, GitHub and the MCP specification
  handle tokens (§Context, "How others do it").

**Refines ADR-0046** (auth adapter): the agent tier keeps its **5-minute token**, but renewing it no longer
needs a live operator session — the same kind of declared divergence ADR-0068 D4 made for paired sessions.
**Conforms to ADR-0056/0057** (the gateway's protocol surface and SEC-16 token scope), **ADR-0060** (agent
tokens signed with api's asymmetric key; the gateway stays keyless), **ADR-0062** (delegation grants stay
the authority for agent-launched Runs) and **ADR-0070** (release provenance, now also for the bridge).
Supersedes nothing.

---

## Context

**The goal.** Beta 1 must let an operator connect an AI agent from the browser and watch it do governed
work [read: `docs/product/beta1-scoping/ecosystem-map.md` §7]. The terminal inventory ranked connecting an
agent the product's weakest journey [read: `docs/product/beta1-scoping/terminal-inventory.md` J7].

**How an agent gets a credential today.**
- The only way to mint one is `POST /v1/agent-tokens`, behind the operator's cookie session and CSRF check
  [read: `services/api/internal/server/server.go:1295`; `docs/api-contracts/openapi.v3.fragment.yaml:505`].
  No screen calls it, so an integrator must put **the operator's password in a script**, which gives that
  script full operator power.
- The token lives **5 minutes** (`AgentTTL`) [read: `packages/go/auth/auth.go:21`] and can be renewed only
  "while the parent operator session is active" [read: `docs/adr/ADR-0046.md:44`].
- Its scope is **one resource plus allowed operations**, enforced by the gateway on every call (SEC-16)
  [read: `packages/go/auth/auth.go:39`; `services/mcp-gateway/internal/catalog/catalog.go:881`].

**Why the token lives five minutes.** Our tokens are **self-contained**: the gateway checks the signature
and the expiry inside the token and looks nothing up. That is what keeps the gateway **keyless** (it can
verify, never mint; ADR-0060) and independent of api's database. The price is that **a token cannot be
revoked once issued**. ADR-0046 chose "revocation = key rotation + short lifetimes", so **the lifetime is
the revocation delay**: a leaked token works for at most five minutes [read: `docs/adr/ADR-0046.md:57`,
`:142`, `:187`]. A 90-day token of this kind would be a 90-day leak with no off switch.

**How others do it** (read 2026-11-18; external, so marked `[read: URL]`):
- **Atlassian Data Center** personal access tokens (Jira, Confluence): the user may set expiry in days or
  none. An admin can cap the maximum (default 365 days) and disable never-expiring tokens; the token is
  shown once, sent as `Authorization: Bearer`, and revocable by the user or an admin
  [read: https://confluence.atlassian.com/enterprise/using-personal-access-tokens-1026032365.html].
- **Atlassian Cloud** API tokens now **must** expire: 1 to 365 days, default one year. Older tokens were
  given an expiry retroactively in 2024
  [read: https://support.atlassian.com/atlassian-account/docs/manage-api-tokens-for-your-atlassian-account/].
  Atlassian's remote MCP server uses OAuth 2.1 by default and accepts API tokens for non-interactive
  agents only when an admin enables it
  [read: https://support.atlassian.com/atlassian-rovo-mcp-server/docs/authentication-and-authorization/].
- **GitHub** fine-grained tokens default to **30 days**, allow a custom date or no expiry, and an
  organisation can enforce a maximum lifetime that blocks never-expiring ones
  [read: https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/managing-your-personal-access-tokens].
- **The MCP specification (2025-11-25)**:
  - **Authorization is optional.** HTTP servers SHOULD act as OAuth 2.1 resource servers, and **stdio
    servers SHOULD instead "retrieve credentials from the environment"**.
  - Authorization servers **SHOULD issue short-lived access tokens**; servers MUST check the token was
    issued for them and MUST NOT pass tokens through.
  - Invalid or expired tokens get a `401`
    [read: https://modelcontextprotocol.io/specification/2025-11-25/basic/authorization].
- **The same spec's timeout rules**: a client whose request times out **SHOULD send
  `notifications/cancelled`**. A client **MAY reset its timeout when progress notifications arrive** but
  SHOULD still enforce a maximum
  [read: https://modelcontextprotocol.io/specification/2025-11-25/basic/lifecycle §Timeouts;
  https://modelcontextprotocol.io/specification/2025-11-25/basic/utilities/cancellation].

⇒ **Every one of these separates two things: a long-lived, revocable key the user manages, and what is
actually checked on each request.** Jira checks the key itself against its database on every call; OAuth
(and the MCP spec) exchanges it for a short-lived access token. We cannot do the first without giving the
gateway a database lookup and a long-lived secret on every call (Alternative 4), so we do the second. The
key is what the operator sees and manages, with Jira-style lifetimes. The five-minute token becomes an
internal detail nobody handles.

**What real clients do — measured** [ran: `docs/spike/b041-deep-pass.md` §7–§8; `docs/spike/bridge-check-2026-11-18.md`]:
- Claude Code, the OpenHands SDK, openworker and Cline all call our gateway with a plain Bearer header; no
  transport adapter is needed.
- **Only Claude Code and Codex can renew a header themselves** (a helper re-run on `401`). Hermes, Grok
  Build, Goose, OpenHands and openworker renew only through OAuth, and Roo and Cline cannot renew at all
  [inherited: `docs/product/beta1-scoping/client-survey-2026-11-18.md`; Cline measured].
- **Every surveyed client can launch a local stdio MCP server** [inherited: same; Cline measured]. Cline,
  which cannot renew, made 5 of 5 calls over 14 minutes through a prototype stdio bridge that renewed for
  it; with renewal off, every call after five minutes got `401`.
- **Clients give up on long calls.** Claude Code and Cline stop waiting at about 60 seconds by default,
  shorter than our approval wait. **Cline sent no cancellation when it gave up**, contrary to the spec's
  SHOULD. A bridge that kept waiting would let the abandoned call **run after approval while the agent
  believes it failed**, defeating the gateway's own protection: an abandoned call stays parked and never
  executes [ran: `docs/spike/bridge-check-2026-11-18.md` §4; read:
  `services/mcp-gateway/internal/catalog/catalog.go:1328-1342`].

---

## Decision

**The operator creates an agent and an access key for it in the cockpit, choosing how long the key lasts,
as with a Jira or GitHub token. The agent's machine runs `aireos-mcp-bridge`, which holds the key and
trades it every few minutes for the same 5-minute token the gateway checks today. The key can be revoked
at any moment; the agent loses access within five minutes.**

### D1. Agents

An api-owned registry. Each agent has a name (its subject `agent:<name>`), the operator who created it,
**one resource and its allowed operations** (today's token scope, so SEC-16 is unchanged), `created_at`
and `disabled_at`. The cockpit creates, lists, disables and deletes agents. **A paired phone session can do
none of this** (deny-by-default, ADR-0068 D5).

### D2. Access keys — managed like a personal access token

- **Each agent has up to two active keys at a time**, so a key can be rotated without downtime: create the
  new key, switch the agent over, revoke the old one. Agent identity and audit attribution stay the same.
- **Lifetime is chosen at creation:** **7, 30, 60 or 90 days, or a custom date up to 365 days**. The
  default is **30 days** (GitHub's). The expiry cannot be extended later; rotate instead (as on Bitbucket
  and Atlassian Cloud).
- **"No expiry" is available but off by default.** The operator can switch it on under Settings, as
  Atlassian Data Center's and GitHub's admin policies allow. The switch is audit-chained and refused to a
  paired session. A key without expiry carries a permanent "never expires" label on every screen that
  lists it. Switching the setting off again stops new ones; existing ones keep working until revoked (no
  surprise outage), and Settings lists them with a "revoke all" button.
- **What a key looks like:**
  - 32 random bytes with a fixed prefix (`aireos_agent_`) and a short checksum, so secret scanners and
    humans can recognise one and a typo is caught before any network call.
  - **Shown once**, with a copy button and a "download as file" button (the bridge reads a file).
  - **Stored only as a hash.** SHA-256 is enough for a 256-bit random secret; it is not a password.
  - Never logged, never in an audit event, never returned again. A lost key is revoked and replaced.
- **What the operator sees for each key:** name, created, expires, **last used** (time of the last
  exchange) and state. Keys expiring within 7 days are flagged on the agents screen.
- **Revocation** stops new exchanges immediately; the agent loses access within one token lifetime (D4).
  Disabling or deleting an agent revokes all its keys.

### D3. The exchange: `POST /v1/agent-tokens/exchange`

- **The request.** No cookie, no CSRF. The access key comes as `Authorization: Bearer`.
- **The checks.** api verifies the checksum, the hash, the expiry, revocation and the agent's state.
- **The result.** api mints the **same 5-minute agent token** `IssueAgent` mints today, scoped to the
  agent's registered resource and operations, with audience `mcp-gateway`.
- **The response** carries the token, its lifetime and **the key's own expiry**, so the bridge can warn
  before the key runs out.
- **Every exchange is audit-chained** as `agent.token.exchanged`: actor `agent:<name>`, the key's id, no
  key material. This keeps ADR-0046's rule that every refresh is audited.
- **No oracle.** Rate-limited per key and per source address. Every failure gets the same response, so
  the endpoint never says whether a key exists, expired or was revoked.

### D4. Revocation within one token lifetime

Revoking a key or disabling an agent stops new exchanges at once. Tokens already issued live out their
remaining ≤5 minutes. That bound is ADR-0046's own, now applied per key. **The five-minute token stays
precisely because it is what makes revocation work** without a lookup on every call.

### D5. ADR-0046's refresh rule changes for this tier, declared

An enrolled agent renews **without** a live operator session; that is the point. Its authority is bounded
by the operator's act of creating it, its one-resource scope, its key's expiry, revocation, and the audit of
every exchange.

### D6. How a client connects

- **The default: the bridge (D7), for any client that can launch a stdio MCP server.** The agent screen
  shows ready-to-paste configuration for each named client. It launches the bridge, points it at the
  downloaded key and CA files, and **sets the client's tool timeout above the approval wait**.
- **The alternative: a helper, for Claude Code and Codex.** Both can re-run a command to get a fresh header
  (`headersHelper`, `http_headers_helper`). The screen shows that one-line command for operators who
  prefer no extra program.
- **The guide names a client only after it has been run** across token renewals and a call held for
  approval. Documentation alone does not qualify.
- A client that can do neither works for at most one token lifetime. None is known today.

### D7. `aireos-mcp-bridge`

**What it is.** A small static program on the agent's machine, started by the client as a stdio MCP
server: it reads JSON-RPC on stdin, sends each message to the gateway's `/mcp` over HTTPS, and writes the
reply to stdout. **It opens no listening port.** This is the spec's own model for stdio servers: the
credential comes from the local environment.

**Shipped** as a signed release artifact with an SBOM for **Linux amd64, Windows amd64 and macOS arm64**.
ADR-0070's provenance applies to it as to the server bundles.

**Its rules:**
1. **Key handling.** It reads the access key from a file that only the agent's user can read. The key and
   the tokens never appear in its output, its logs or its error messages.
2. **Renewal.** It exchanges before expiry (at 4 of the 5 minutes) and **once more on a `401`** (a laptop
   that slept past expiry). It never retries a tool call for any other reason. When the key itself has 7
   days or less left, it says so in its log.
3. **Trust.** It trusts **only** the CA root file the operator downloaded (D9), after checking that file's
   SHA-256 against the fingerprint the cockpit showed. It never falls back to the system trust store. It
   pins the **root file**, not the certificate the server presents, because Caddy never sends the root
   and its intermediate rotates every 7 days (T-175e).
4. ⚠ **It never holds a call longer than its client will wait for it.** It cancels the upstream request
   when the client sends `notifications/cancelled`, when the client closes stdin, and when a per-call
   deadline expires. That deadline is the same value the cockpit writes into the client's tool timeout.
   Without this rule, a call abandoned while held for approval would run after approval while the agent
   believes it failed.
5. **Progress while held.** If the client's request carries a `progressToken`, the bridge sends
   `notifications/progress` while the gateway holds the call for approval. The spec allows a client to
   reset its timeout on progress, so a client that does needs no raised timeout. Rule 4's deadline still
   applies. **Which clients honour this is measured per client, never assumed.**
6. **No decisions.** It holds no policy and decides nothing. It is a transport and key adapter on the
   agent's side of the seam. ADR-0060 is unchanged: the gateway still checks a 5-minute token and holds no
   long-lived secret.

### D8. The gateway's `401` and `ping`

- **A `401` names what happened.** The gateway's `401` carries `WWW-Authenticate: Bearer
  error="invalid_token"` and tells **expired** apart from **malformed** (deep pass M7). The bridge or a
  helper then knows to renew, not to give up.
- **The gateway answers `ping`** (deep pass M4; the spec requires it).
- ⓘ **Declared divergence from the MCP authorization spec:** we publish no OAuth metadata
  (`/.well-known/oauth-protected-resource`), because we run no authorization server (Alternative 3). The
  spec makes authorization optional and its HTTP profile a SHOULD, so this is a stated choice, not a
  breach.

### D9. The CA root is downloadable

The cockpit serves the CA root certificate (public data) and its fingerprint, alongside each key, so the
bridge and the helper can trust the cockpit without touching the machine's trust store.

---

## Alternatives considered

1. **Keep the operator-cookie mint and document a script.** It puts the operator's full power in a script
   on the agent's host, and renewal still dies with the operator's session. That is today's state, ranked
   the product's worst journey. Rejected.
2. **Lengthen the token itself to 30, 60 or 90 days** (what "a Jira-style token" means if taken literally).
   It works with every client and needs no bridge. But our tokens cannot be revoked once issued (Context),
   so a leaked 90-day token would work for 90 days. The only off switch would be rotating the signing key,
   which signs out every agent and every operator at once. Rejected: D2 gives the operator the same
   lifetimes on the *key*, which **can** be revoked.
3. **An OAuth 2.1 authorization server** (the MCP authorization spec: discovery, client registration,
   refresh tokens). It is the standard, and some clients support only it. But it is a large new security
   surface, its consent flow suits humans rather than unattended agents, and the bridge already serves
   OAuth-only clients that can launch a stdio server. **Deferred, not rejected.** Trigger: **a target
   client that can neither launch a local stdio server nor run a helper**, or an operator requirement for
   per-user interactive consent that an agent key cannot express.
4. **Let the gateway accept the access key directly**, as Jira does. It is simplest for clients. But the
   gateway would need api's key table, or a call to api, **on every tool call**, giving up the
   lookup-free, keyless gateway of ADR-0060. And the long-lived key would cross the network on every call
   instead of once every few minutes. Rejected.
5. **Multi-resource agents.** Widen the scope to a set of resources. Useful, but it changes the token format
   and SEC-16. Deferred; for Beta 1, one agent per resource, with delegation grants (ADR-0062) remaining
   the way an agent gets multi-step authority.
6. **Helper only, no bridge** (this ADR as first drafted). It ships nothing onto agent machines, but only
   Claude Code and Codex could renew; every other client would stop after five minutes. Rejected after the
   bridge check.
7. **A bridge that listens on a local port** (HTTP on `localhost`) instead of stdio. It would serve clients
   that cannot launch programs, but a local listener holding a key can be reached by any process or web
   page on the machine. Rejected; stdio has no such surface.
8. **"No expiry" always available**, or never available. Always: a forgotten key outlives the reason it
   was made, which is why Atlassian Cloud removed it. Never: some operators run unattended agents on
   machines they control and would rather rotate on their own schedule. D2's choice is that it exists
   but must be switched on, as Atlassian Data Center and GitHub allow.

---

## Consequences

**Gained:**
- No operator password ever lives on an agent's machine. Connecting an agent is: create it in the cockpit,
  download two files (key and CA), paste one configuration block.
- Operators manage agent keys the way they already manage Jira, Confluence or GitHub tokens: lifetimes,
  last used, expiry warnings, rotation without downtime, revocation.
- Unattended agents work in every client that can launch a stdio server (measured for Cline; each other
  client proven before the guide names it), and in Claude Code and Codex through a helper.
- Per-key revocation, per-agent attribution, and an audit record of every exchange.

**Costs, accepted deliberately:**
- ⚠ **A long-lived key now lives on the agent's machine.** Whoever holds it can act as that agent, within
  its one resource, its operations and every policy and approval gate, until it expires or is revoked.
  That is strictly less than today's operator password in a script, and it is visible: last used, every
  exchange chained. A "no expiry" key raises this cost, which is why it must be switched on.
- ⚠ **We ship software onto agent machines**, often Windows or macOS laptops, a first for this project:
  three more signed artifacts, their SBOMs, and support for platforms we do not run the server on.
- **Audit volume grows** by about 15 `agent.token.exchanged` events per hour per active agent (one every
  4 minutes). Accepted, because ADR-0046 made "every refresh is audited" a property; coalescing them would
  need its own decision.
- **The approval wait still needs each client's timeout raised** (or progress honoured, D7 rule 5). That
  claim decays as clients change, so it lives in one place, the cockpit's generated configuration, and is
  re-proven each release.
- **New surface:** two tables (agents, keys), the exchange route, the agent screens, the `401` and `ping`
  changes, and one Settings switch.

---

## How we would know this was wrong

- **Operators ask for longer tokens rather than longer keys.** Then the bridge is not reaching them, or a
  client cannot launch it. Fix the reach; do not lengthen the token (Alternative 2).
- **A key leaks and is used.** The audit chain must show its exchanges, and revocation must stop them
  within five minutes. If either fails, the design failed.
- **A call held for approval runs after its agent gave up on it.** D7 rule 4 has failed. That is a
  governance defect, not a usability one.
- **A named client stops working through the bridge after an update** (it changes how it launches stdio
  servers or times them out). The per-client proofs must be re-run each release.
- **A target client can use neither the bridge nor a helper.** Alternative 3's trigger has fired: that is
  its own ADR.
- **Operators create one agent per server and find it unworkable.** Alternative 5's trigger has fired.
