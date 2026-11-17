# B-041 deep verification pass

**Measurement spike, 2026-11-16/17.** Not product code, not a decision. It replaces the README-level
`[inherited:]` intake behind `docs/spike/b041-remaining-work.md` §4 with things that were opened and run.
The T-153 precedent (`docs/spike/inference-substrate-measurement.md`) applies: a cheap deep pass changes answers.

Provenance markers follow CLAUDE.md rule 11: `[ran]` executed and observed, `[read]` source opened,
`[doc]` vendor documentation page read (not run), `[unverified]` not established.

## 0. The most important finding

⭐ **Two of the three real MCP clients exercised here already work against our gateway with no adapter at all, and
the one hard blocker is not the transport. It is the credential.**

- Real Claude Code 2.1.326, the OpenHands SDK 1.62.0 and openworker 0.3.4 each connected to the *real* gateway HTTP
  surface (`services/mcp-gateway/internal/server`, run over a stub catalog) with a plain `Authorization: Bearer`
  header, listed `remote.exec`, and called it. [ran]
- Claude Code can **refresh a 5-minute token with no human**: its `headersHelper` re-ran after a `401` mid-call and
  the retry succeeded. [ran] A static header cannot: every client stops working at expiry. [ran]
- But a helper script needs something to call. Our only mint path is `POST /v1/agent-tokens`, which needs an
  *operator cookie and CSRF token* (`services/api/internal/server/server.go:1295`). **There is no
  agent-credential-to-token exchange endpoint, so no unattended refresh is possible today, in any client.** The A1
  ADR has to supply one (or a long-lived agent credential the gateway accepts directly).
- OAuth 2.1 does not work against us: no `WWW-Authenticate`, no `/.well-known/*`, no `/register`. Real Claude Code and
  the OpenHands MCP client both fell into dynamic client registration and died on 404. [ran]
- Egress, the intake's "sharpest risk", **does not reproduce in either harness at default**: zero outbound attempts
  from openworker (signed out); one fixable attempt from OpenHands (a LiteLLM price-table fetch from
  `raw.githubusercontent.com`) that one environment variable removes. [ran] The intake's "default-on relay and
  telemetry" is wrong as stated; it is gated on cloud sign-in and an enabled relay connector. [read + ran]

## Method and safety record

- Everything third-party ran **only inside throwaway containers**. No repo mount, no host mount of anything from this
  machine, nothing from `../keyStore`, no real key. Test tokens were minted by a stub signer using a made-up
  symmetric key that exists only in the spike's stub binary.
- **The model was a scripted fake**, never a real endpoint: a ~60-line OpenAI-compatible server (chat completions,
  streaming and not) that answers the first turn with exactly one shell/tool call and any later turn with `done`, and
  a ~50-line Anthropic Messages equivalent for Claude Code. Fake key strings were arbitrary.
- **The governed surface was the real gateway code.** `services/mcp-gateway/internal/server.New` (the router,
  `RequireCapability("agent-tool")`, the JSON-RPC handler) was compiled unchanged in a scratch copy with a stub
  `Catalog` (one `remote.exec` tool that sleeps instead of block-and-waiting) and a real PASETO signer/verifier from
  `packages/go/auth`. So the handshakes below are against the shipped transport, not a re-implementation. The stub
  `Invoke` is not the governed chain; nothing here measures policy, approval or audit.
- **Isolated network + capture** `[ran]`: a Docker `--internal` network (no route out) with three instruments.
  (1) A sink container acts as the harness's DNS server: it answers every name with its own address (except the
  fake-model host), logs every query, and listens on 80/443/8443 logging the Host header or TLS SNI. (2) A
  `tcpdump` sidecar shares the harness's network namespace and logs every packet to a non-local address, which catches
  hard-coded IPs that never touch DNS. (3) The harness's default route points at the sink. Docker's bridge drops
  off-subnet packets before the sink sees them, which is why instrument 2 exists. The first version of the capture
  missed hard-coded IPs; that was found by the negative control, and instrument 2 was added.
- **Negative control** `[ran]`, run in the *same* container and instruments immediately after the harness run:
  `curl http://control-probe.example.net/x` and `curl http://203.0.113.9/`. Both were captured every time (table in
  §4). The same instruments also positively caught real harness egress (OpenHands, §4), so "zero" below is not a
  blind capture.
- **Sources pulled** (all fetched inside containers):

| Source | Pinned at | Licence |
|---|---|---|
| github.com/OpenHands/software-agent-sdk | `16decb4927eb39ec8d239b250e702a840657a0e0` (2026-11-16), tag line v1.62.0 | MIT (LICENSE read) |
| github.com/andrewyng/openworker | `f91a61006985b2d4c11d5787d2b41f000467be9c` (2026-11-16), v0.3.4 | MIT (LICENSE read) |
| npm `@anthropic-ai/claude-code` | 2.1.326 | `package.json` says "SEE LICENSE IN README.md"; **not read** [unverified], treat as proprietary |
| npm `@openai/codex` | 0.169.0 (installed, **not exercised**) | Apache-2.0 (`package.json`) |
| PyPI dependencies of both Python harnesses | latest at build time, **unpinned** | **not audited** [unverified] |

Cleanup: all containers, the isolated network, the source volume and the spike images were removed
(`docker ps -a`, `docker network ls`, `docker volume ls`, `docker images` all show nothing from the spike).

## 1. Current state of the two candidates

**OpenHands.** The agent now lives in `OpenHands/software-agent-sdk` (MIT). The old `OpenHands/OpenHands` repo
(93.6k stars) describes itself as the "Agent Canvas" self-hosted control centre and its README boundary table points
the SDK, agent server, tools and canonical server API at `software-agent-sdk` `[doc: github.com/OpenHands/OpenHands]`.
The SDK repo: 3,051 commits, created 2025-08-23, **846 commits and 74 distinct author addresses in the last 90 days**,
119 tags, v1.62.0 on 2026-11-11, v1.61.0 on 2026-11-09, five tags in the six days to 11-11 `[ran: git log in clone]`.
1.4k stars, 786 forks `[doc]`. Very active; fast churn, so pinning and a compatibility test matter more than any
single behaviour below.

**openworker.** Canonical repo is `github.com/andrewyng/openworker` (MIT), 19.4k stars, 2.8k forks `[doc]`; no rename
or abandonment seen. 1,187 commits, created 2026-07-21, **925 commits and 58 distinct author addresses in the last 90
days**, latest tag v0.3.4 (2026-10-06) plus a `skills-v0.1.0-beta.2` tag (2026-11-06) `[ran: git log in clone]`.
It describes itself as a desktop app plus a "Harness SDK"; the server we would integrate with is the local Python
agent server `coworker.server`.

**Do the block-and-wait seams still exist at HEAD? Yes, with one name drift.** `[read]`
- OpenHands: `POST /api/conversations/{id}/confirmation_policy`
  (OpenHands/software-agent-sdk@16decb4 openhands-agent-server/openhands/agent_server/conversation_router.py:579-595),
  `POST .../security_analyzer` (same file :596-611). The "respond" seam is **not** at the top level the intake wrote:
  it is `POST /api/conversations/{id}/events/respond_to_confirmation`, body `{accept: bool, reason: str}` (event_router.py:379-394,
  models.py `ConfirmationResponseRequest`).
- openworker: `GET /v1/inbox` and `POST /v1/inbox/{item_id}/resolve` (andrewyng/openworker@f91a610
  coworker/server/app.py:345 and :383). `resolve` takes `{"resolution": "allow"|"deny"|"always"...}`, is idempotent
  and first-responder-wins.

## 2. The seams exercised for real

Both ran end to end against the fake model, scripted to request exactly one shell action
(`echo B041_MARKER > /tmp/b041_marker.txt; pwd`). Each ran an approve and a deny scenario.

| | OpenHands agent-server 1.62.0 | openworker 0.3.4 |
|---|---|---|
| Parks before executing | yes: status `waiting_for_confirmation`, marker file **absent** while parked [ran] | yes: inbox item `pending`, marker **absent** while parked [ran] |
| Enabled by | `set_confirmation_policy(AlwaysConfirm())` on the conversation (SDK) | default `interactive` mode; a command with a shell operator (`;`) is never auto-allowed [ran] |
| Approve from outside | `POST .../events/respond_to_confirmation {"accept":true}` -> 200; command ran, marker written, status `finished` [ran] | `POST /v1/inbox/{id}/resolve {"resolution":"allow"}` -> 200; command ran, marker written [ran] |
| Deny from outside | `{"accept":false,"reason":"denied by external operator"}` -> 200; **no execution**, a `UserRejectObservation` carries the reason back to the model, status `idle` [ran] | `{"resolution":"deny"}` -> 200; **no execution**, tool finished `status: denied` [ran] |
| Auth to the control API | none by default; optional session API key env [read] | `X-OpenWorker-Token` header (WebSocket: token as a sub-protocol) [read + ran] |

**Parked payload.** This is what an adapter would map onto policy -> approval -> audit.
- OpenHands `ActionEvent`: `tool_name: "terminal"`, `action: {command, is_input, reset, kind:"TerminalAction"}`,
  `tool_call_id`, `tool_call.arguments` (raw JSON string), `summary`, `security_risk` (`UNKNOWN` unless an analyzer is
  set), timestamps, `llm_response_id`. **No cwd and no target host in the action;** the working directory is a property
  of the conversation's workspace. [ran]
- openworker inbox item: `id`, `session_id`, `kind: "approval"`, `title: "Run \`run_shell\`?"`,
  `body: "command: ..."`, `tool_call_id`, `visibility`, and `data: {tool, arguments:{command}, escalation, provenance,
  category:"shell"}`. **No cwd in the item**; it appears in the session's `ready` event (`workspace`) and in the later
  `tool_finished` result (`cwd`). [ran]

**What this means for mapping.** Both give us the command string and a stable call id, which is enough to
anchor an invocation fingerprint. Neither gives us a target resource; our policy rules match
`(subject, edge, action, target)`, so the adapter would have to supply the resource itself (v0.15.0 already ships
the limitation that arguments are not governable). Per-call latency of the seam itself was not measured.

## 3. Can the harness route its tool calls through OUR MCP gateway?

**Yes for all three clients tried, and "MCP only" is reachable in OpenHands.** [ran]
- **OpenHands SDK** speaks MCP as a client (FastMCP 4.x). With `Agent(tools=[], mcp_config={...url, headers...})` the
  tool list the fake model received was exactly `["finish", "remote.exec"]`: the terminal and file-editor tools were
  simply not constructed. The call reached the gateway stub and returned. So "it can only act through us" is a
  configuration, not a patch.
- **openworker** has an MCP client (`mcpServers` JSON in its state dir, `streamablehttp_client`, `${VAR}` expansion,
  per-server `include_tools`/`exclude_tools`, `requires_approval` default true, optional `auth: "oauth"`). Connected,
  listed and called `remote.exec`, exposed as `mcp__aireos__remote_exec`. **But the shell tool stays in the list**
  (36 tools offered, including `run_shell`, `write_file`, `web_fetch`). Removing it via a persona `tools:` allow-list
  is suggested by `personas/manifest.py:375` and was **not exercised** [unverified]. Note the double gate: its own
  approval card parked for the MCP call too (category `mcp`), so it asks the user *and* would go through our policy.
- **Claude Code** connected over HTTP; its built-in Bash/Edit/Write tools remain unless denied by permission
  settings (`--allowedTools` only adds). Disabling them was not run [unverified].

**Consequence for the design question.** Governance is "it asks us first" (block-and-wait adapter, section 2) for the
harness's *own* tools, and "it can only act through us" (MCP) for anything we expose. They are not exclusive, and
neither is enforcement: a harness that keeps a local shell can still use it. That is the cooperative-governance
limitation already ruled for Beta 1 (B-040 deferred), now with concrete evidence that MCP-only is achievable *by
configuration* in OpenHands and unproven in the other two.

## 4. Egress measurement

Isolated network, all instruments above, fake model reachable only by name through the sink. "Startup" = server up
plus 45 s idle. "Task" = the approve and deny scenarios, including the SDK client process for OpenHands.

| Run | Startup | Task | Verdict |
|---|---|---|---|
| **OpenHands, defaults** | `raw.githubusercontent.com` x3 (TLS SNI, DNS A+AAAA each) | same x3 (the client process, a second `litellm` import) | positive hit: LiteLLM model-price table fetch. No other name, no raw-IP packet. |
| **OpenHands + `LITELLM_LOCAL_MODEL_COST_MAP=True`** | **0** | **0** | the one attempt is config-removable |
| **openworker, defaults (signed out)** | **0** | **0** | nothing leaves the box |
| openworker, **signed in** (fake token seeded in its secret store) | 0 | `api.openworker.com` x4 DNS, 2 TLS connects (one per session created) | positive hit: the content-free session telemetry the code documents |
| **openworker, signed in + `cloud:telemetry.enabled=false`** | **0** | **0** | the documented opt-out removes it |
| Negative control (same container, after the OpenHands run) | 2 DNS queries and 1 TCP/80 connect for `control-probe.example.net`; 3 SYNs to `203.0.113.9:80` | | instruments see both a named and a hard-coded outbound |
| Negative control (same, after the openworker run) | identical | | identical |

Reading it plainly:
- **openworker's "AWS API-Gateway WSS relay" is real but not dialed at default.** The default
  `cloud_relay_ws_url` is the production `wss://...execute-api.us-east-1.amazonaws.com/ocw-connect` (config.py:127) and
  the code says an empty value disables it. It is only used when a messaging connector (Slack/GitHub) is enabled in
  relay mode (`server/manager.py:6745-6770`), and it authenticates with the cloud sign-in token. **I did not enable a
  relay connector, so the "connector in relay mode dials AWS" leg is [read], not [ran].** Disabling it by config was
  therefore not demonstrated, only the sign-in-gated telemetry.
- **openworker telemetry is "default-on, signed-in users only"** by its own comment (cloud.py:278-281, `enabled`
  defaults true but `emit_session_created` returns before any network call without a token). An air-gapped install is
  signed out by definition, so it sends nothing. [read + ran]
- **OpenHands telemetry is off by default**: exporter `none`, needs consent, needs an optional `posthog` extra
  (agent_server/config.py:150-200, telemetry/policy.py) [read]; none was observed [ran].
- **Not measured:** a model call to a real provider (the fake model is local), web-search/`web_fetch` tools
  (openworker defaults to DuckDuckGo when the model asks), Docker/Kubernetes runtimes for OpenHands, the OpenHands
  browser tool (the default preset was run with `cli_mode=True`), code-server/VS Code extension downloads, and update
  checks in the desktop shells. Egress that only occurs on those paths is invisible to this table.

## 5. Other mainstream clients

Documentation level unless marked `[ran]`; do not read a doc cell as a measured behaviour.

| Client | Remote MCP transport | Static Bearer | Env var | Dynamic/refresh | OAuth 2.1 (DCR) | Pre-tool approval hook |
|---|---|---|---|---|---|---|
| **Claude Code 2.1.326** | HTTP (SSE deprecated, HTTP tried first) [doc] | `--header`; **connected** to our gateway [ran] | `${VAR}` in `.mcp.json` headers [doc] | **`headersHelper`: re-run on `401`, reconnect, one retry. Proven.** [ran] | yes, DCR + CIMD + pre-registered client id; **failed against us** [ran] | **`PreToolUse` hook** matching `mcp__server__tool`, types `command`/`http`/`mcp_tool`/`prompt`/`agent`; returns allow/deny/ask. A timeout does not block unless `onFailure:"block"` [doc] |
| **Codex CLI 0.169.0** | Streamable HTTP only (no SSE listed) [doc]; installed, **not exercised** | `bearer_token_env_var`, `http_headers` [doc] | `env_http_headers` [doc] | **`http_headers_helper`**: cached, refreshed once on `401/403` if the output changed [doc] | `codex mcp login`, CIMD/DCR/pre-registered [doc] | per-server `default_tools_approval_mode` (`auto/prompt/writes/approve`), per-tool override [doc]; hooks page not retrieved [unverified] |
| **Cline** | Streamable HTTP; `sse` is the default if `type` omitted [doc] | `headers` object [doc] | not documented [doc] | not documented [doc] | "Authorize OAuth" action exists; detail not retrieved [doc] | per-server `autoApprove` array; no hook documented [doc]. Source file lookup 404'd; low confidence |
| **Goose** | `streamable_http` extension [doc] | `headers` map and `env_keys` appear in its tests, but the docs page does not describe them [doc, partial] | `env_keys` [doc, partial] | not covered [doc] | `client_id`, `client_secret_key`, `scopes`; DCR/CIMD automatic [doc] | permission modes exist, details not retrieved [unverified] |
| **Aider** | **No MCP client.** The FAQ does not mention MCP [doc]; no third-party bridge surveyed | n/a | n/a | n/a | n/a | n/a |
| **OpenHands SDK 1.62.0** | `http`/`streamable-http`/`sse`/`stdio` [read] | `headers`, `auth: {strategy: bearer/api_key/basic/header}`; **connected** [ran] | `${VAR}` expansion [read] | none for static headers; **401 at expiry became a tool error, no re-auth** [ran] | strategy `oauth2` via FastMCP, refresh token supported [read]; **failed against us** [ran] | `confirmation_policy` + `respond_to_confirmation` (section 2) |
| **openworker 0.3.4** | streamable HTTP via the `mcp` SDK [read]; **connected** [ran] | `headers` in `mcp.json` [read + ran] | `${VAR}` via secret store [read] | none observed | `auth: "oauth"` browser flow + DCR [read] | its own approval card, `requires_approval` default true [ran] |

⭐ **Answer to the brief's question: yes, a mainstream client can be pointed at our gateway with no adapter**
(Claude Code and OpenHands and openworker did, with a static Bearer header), **so "documented integration plus
onboarding" is real for the MCP path.** It is true only while the token is valid; see §7. The block-and-wait seams
(§2) remain the only way to govern a harness's *own* tools, and only two of the tools surveyed expose a hook for it:
Claude Code's `PreToolUse` (including an HTTP hook, so no local script is needed [doc]) and Codex's per-tool approval
mode [doc].

## 6. Licences and air-gap fitness

| | OpenHands software-agent-sdk | openworker | Claude Code (client) | Codex CLI (client) |
|---|---|---|---|---|
| Licence | MIT | MIT | not read, proprietary-looking [unverified] | Apache-2.0 |
| Phones home at default | one LiteLLM price-table fetch; telemetry off [ran] | nothing signed out; session telemetry and relay gated on sign-in [ran + read] | not measured | not measured |
| Runs offline with an operator-provided model | **yes**, any OpenAI-compatible endpoint (proved with a local fake, plus T-153's runtime) [ran] | **yes**, via its local-server providers (vLLM/llama.cpp/LM Studio...) [ran with the fake] | needs an Anthropic-compatible endpoint; ran against a local fake [ran] | not exercised |
| Install footprint (container, measured) | image 1.54 GB, 193 Python distributions, ~731 MB site-packages | image 840 MB, 75 distributions, ~250 MB site-packages | npm global, image 2.2 GB incl. Codex | same image |
| Maturity (commits vs stars) | 3,051 commits / 1.4k stars (SDK repo); 846 commits in 90 days; 74 authors | 1,187 commits / 19.4k stars; 925 in 90 days; 58 authors | n/a | n/a |
| Churn risk | high: five tags in six days | high: 925 commits in ~16 weeks, v0.x | n/a | v0.169 |

Screening heuristic from `docs/executive/competitor-map.md` (stars vs commits): **openworker no longer trips it.**
At intake it was 14k stars and 130 commits; it is now 19.4k and 1,187. The "three weeks old" caveat is now
about four months, still a v0.x. OpenHands' SDK repo has fewer stars than the commit-count suggests but no
mismatch warning. Licences: both MIT, so neither is blocked on the screening rule, but **the dependency trees (193 and
75 distributions) were not licence-audited** [unverified]; an offline bundle redistributes them, so that audit is a
precondition of shipping either, not a footnote.

## 7. How MCP clients authenticate to a remote server, and whether they can refresh

Our token: 5-minute PASETO v4, audience `mcp-gateway`, edge `agent-tool` (`packages/go/auth/auth.go:21,187`); minted by
`POST /v1/agent-tokens`, which requires an operator session cookie plus CSRF (`services/api/internal/server/server.go:1295`).
The gateway verifies expiry on every request; an expired token returns `401 {"code":"unauthorized","message":"invalid
capability token"}` with **no `WWW-Authenticate` header**. [ran]

| Client | Static header | Env-var token | OAuth 2.1 (discovery + DCR + refresh token) | Unattended refresh of a 5-minute credential |
|---|---|---|---|---|
| Claude Code | yes [ran] | yes (`${VAR}` in `.mcp.json`) [doc] | yes [doc]; **fails against our gateway** [ran] | **YES, via `headersHelper`** [ran]: after `401` it re-ran the helper, reconnected, retried once, and the call succeeded. Needs a trusted workspace for project/local scope [ran]. Not proactive: refresh is reactive to a failure. |
| Codex CLI | yes (`bearer_token_env_var`, `http_headers`) [doc] | yes [doc] | yes [doc] | **YES in principle via `http_headers_helper`** [doc]: refreshed once after `401/403`, only if the helper's output changed. Not run [unverified] |
| Cline | yes [doc] | not documented [doc] | partial [doc] | not documented; assume no [unverified] |
| Goose | `headers` map per its tests [doc, partial] | `env_keys` [doc, partial] | yes (`client_id`, DCR) [doc] | not documented [unverified] |
| OpenHands SDK | yes [ran] | `${VAR}` [read] | yes (`oauth2`) [read]; **fails against us** [ran] | **No for static headers** [ran]: at expiry the call became a tool error, the conversation continued with no re-auth. OAuth refresh exists only against an OAuth server. |
| openworker | yes [ran] | `${VAR}` [read] | yes (`auth: oauth`) [read] | no mechanism seen for headers [unverified] |

**Answer.** A client that only takes a static header **cannot** use our agent token past five minutes (OpenHands:
observed; Cline and Goose: undocumented). **Claude Code and Codex have a documented/observed reactive-refresh path, but
it calls a helper that must be able to mint a token without a human, and we do not have that endpoint.** Two
ways out are visible, neither chosen here: (a) a credential-exchange endpoint (long-lived agent credential in, 5-minute
token out) called by a `headersHelper`/`http_headers_helper`; (b) implementing the MCP authorization
discovery on the gateway (RFC 9728 metadata, a `WWW-Authenticate` header, an authorization server with DCR and a refresh
grant), which would make every OAuth-capable client work with no per-client helper, at the cost of building an
OAuth server the product does not have and ADR-0046 deliberately deferred (Zitadel is the named P1 reserve).

## 8. Does our gateway's transport match what those clients speak?

**What we serve** `[read: services/mcp-gateway/internal/server/server.go:38-60,185-215]` and
`[ran: curl against the real handler]`:
- A single route: `POST /mcp`, JSON in, **JSON out only** (`Content-Type: application/json`, HTTP 200 with errors in
  the body). No SSE response mode.
- `GET /mcp` -> **405** (`Allow: POST`); `DELETE /mcp` -> 405. Stateless: no `Mcp-Session-Id`. Notifications ->
  **202**.
- `initialize` answers `protocolVersion: "2025-06-18"` regardless of what the client offered
  (`services/mcp-gateway/internal/mcp/transport.go:18`), capabilities `{tools:{}}`.
- Methods: `initialize`, `tools/list`, `tools/call`. **Anything else, including `ping`, returns `-32601`** (observed
  for `ping` and for the `server/discover` probe).
- Auth: bearer only; `401` carries no `WWW-Authenticate`; `/.well-known/oauth-protected-resource` -> 404.
- Block-and-wait: `tools/call` can hold the HTTP response open for up to 125 s (`MCP_APPROVAL_WAIT_SECONDS`) and has no
  server write timeout.

**What the clients do** (`[ran]`, from the gateway's own request log):
1. Claude Code, the OpenHands SDK and openworker's `mcp` SDK all open with a `server/discover` request carrying
   `Mcp-Protocol-Version: 2026-07-28` (a newer MCP revision's stateless discovery); the gateway answers `-32601`,
   and **all three fall back cleanly to `initialize` offering `2025-11-25`**, accept our `2025-06-18` reply, send
   `notifications/initialized` and proceed with `tools/list` / `tools/call`. Claude Code and the OpenHands SDK also
   `GET /mcp` for a server-push stream; the `405` is tolerated by both.
2. Streamable HTTP clients that accept plain JSON responses work. **SSE-only legacy clients would not** (there is no
   event stream endpoint); Cline defaults to SSE when `type` is omitted [doc], so a Cline user must set
   `"type":"streamableHttp"`; Codex lists only Streamable HTTP [doc].

**Mismatches, stated plainly.**
| # | Mismatch | Evidence | Effect |
|---|---|---|---|
| M1 | **No credential-to-token exchange; the only mint is operator-cookie-gated.** | §7 | No unattended refresh in any client. The decisive one. |
| M2 | **No OAuth 2.1 discovery or registration; `401` has no `WWW-Authenticate`.** | Claude Code and OpenHands fell into DCR and failed on `/register` 404 [ran] | An OAuth-capable client configured for OAuth cannot use us; only static or helper auth works. |
| M3 | **Client tool-call timeouts are shorter than our 125 s approval wait.** | Claude Code timed out at about 60 s with `The operation timed out.`; the same call with `MCP_TOOL_TIMEOUT=200000` succeeded after 130 s. OpenHands waited 135 s without issue [ran]. Codex `tool_timeout_sec` default [unverified] | A human who takes over a minute to approve loses the call unless the onboarding instructions set the timeout. |
| M4 | **`ping` is not implemented** (`-32601`), though the MCP spec requires servers to answer it. | [ran] | No client here pinged during the runs; a client or proxy that health-checks with `ping` would mark us dead. |
| M5 | **Tool name `remote.exec` contains a dot.** | OpenHands passed `remote.exec` unchanged to the model [ran]; Claude Code and openworker rewrote it to `remote_exec` and mapped back [ran]. Whether a *real* provider rejects a dotted name from the OpenHands path was **not** tested [unverified] | Works today; the OpenHands path is at risk with providers that enforce `^[a-zA-Z0-9_-]+$`. |
| M6 | **Stale-version negotiation is one-sided.** | We reply `2025-06-18` to a `2025-11-25` offer and clients accepted [ran] | Fine today; the gateway pins a version that is two spec revisions behind the clients' preference and one behind their discovery probe. |
| M7 | **Header-expiry error is opaque.** | `401` body `invalid capability token` for both malformed and expired [ran] | Clients (OpenHands) surface only "server returned an error response"; nothing tells an agent to refresh. |

**"No adapter needed" is true for transport; it is not true for auth.** Both must line up, and the auth leg does not.

## What changed vs the 2026-08-10 intake

| Inherited claim (`docs/spike/b041-remaining-work.md` §4, `docs/executive/competitor-map.md`, `references/intake-v7-2026-08-27.md`) | Verdict | Detail |
|---|---|---|
| OpenHands agent moved to `OpenHands/software-agent-sdk` (MIT) | **Confirmed** | §1 |
| Seams `POST /confirmation_policy`, `/security_analyzer`, `/respond_to_confirmation` | **Changed** | Exist, but under `/api/conversations/{id}/...` and `respond_to_confirmation` is under `.../events/`, body `{accept, reason}` |
| openworker seams `GET /v1/inbox`, `POST /v1/inbox/{id}/resolve` | **Confirmed** | exact paths at HEAD |
| openworker "14k stars / 130 commits / 8 contributors / 3 weeks old", star-commit mismatch | **Changed** | 19.4k / 1,187 / 58 authors in 90 days / ~4 months; the mismatch signal no longer applies |
| openworker `permissions.py` rejects shell operators before prefix-matching | **Confirmed in behaviour, source not re-read** | `echo ...; pwd` was never auto-allowed and always parked [ran]; the file (1,846 lines) was not opened for the specific rule [unverified] |
| openworker has an AWS API-Gateway WSS relay, default-on | **Refuted as stated** | The URL is the default value, but it is dialed only with an enabled relay connector and a sign-in token; zero egress at default [read + ran] |
| openworker has default-on telemetry | **Refuted as stated** | default-on *only when signed in*; signed out sends nothing; opt-out proven to silence it [ran] |
| "A dependency that phones home by default is the sharpest risk in B-041" | **Narrowed** | OpenHands makes one fixable LiteLLM price-table fetch at default; neither sends product telemetry at default. The risk class stands but this instance is small |
| "Integration is a client, not a patch" | **Confirmed** | both exposed the park/approve/deny seam via a remote API; both are MCP clients |
| Governance only cooperative while B-040 is deferred | **Confirmed, sharper** | MCP-only is a configuration in OpenHands (`tools=[]`); openworker keeps a 36-tool list including a shell; Claude Code keeps Bash |
| (not in the intake) a mainstream client needs no adapter | **New** | §5, §7 |
| (not in the intake) the agent-token mint blocks unattended refresh | **New** | the decisive finding |

## Recommendation inputs for the B-041 ADR (options and evidence, no decision)

1. **Integration shape.**
   - *A. MCP-first (no adapter for the MCP path).* Document the exact client config (header, timeout, tool-name note)
     for Claude Code, OpenHands, openworker. Evidence: three real clients connected to the shipped transport unchanged.
     Limit: governs only tools we expose.
   - *B. Block-and-wait adapter for the harness's own tools.* A small client that polls the harness's pending items
     and maps them to `policy -> approval -> audit`. Evidence: both seams parked, approved and denied correctly;
     OpenHands payload has no target resource, openworker's likewise (§2).
   - *C. Claude Code `PreToolUse` HTTP hook to the gateway* as a no-adapter block-and-wait for a client that has
     none, [doc] only, fails open on timeout unless `onFailure:"block"`; not run.
   - These combine. B needs the most code; A and C are mostly documentation plus the gateway changes below.
2. **Auth for unattended agents (the unavoidable item).** Options: a credential-exchange endpoint behind a
   `headersHelper`/`http_headers_helper` (proven for Claude Code, doc-level for Codex); or gateway-side MCP
   authorization (M2). Static-header-only clients (OpenHands as run, Cline, Goose) remain limited to a 5-minute
   credential under either option unless the TTL or the client changes.
3. **Gateway deltas that are small and evidence-backed:** answer `ping` (M4); add `WWW-Authenticate` to `401`
   (M2, M7); distinct error text for expiry (M7); document or raise client timeouts (M3); decide whether to keep or
   rename `remote.exec` (M5). The surface is explicitly **not frozen** at Beta 1, so these are open.
4. **Which harness to reference.** OpenHands: the seams are the cleaner client API (typed events, explicit status,
   `tools=[]` MCP-only), MIT, very high churn. openworker: simpler REST, but a 36-tool surface with a built-in
   shell, a desktop-product shape, v0.x, cloud-sign-in paths that must be kept off. Neither is blocked on egress or
   licence at the level measured.
5. **Air-gap demo.** Both harnesses ran fully offline against a local OpenAI-compatible endpoint; combining with the
   T-153 runtime is viable. Unmeasured: the dependency licence audit, browser/web tools, relay-connector mode,
   Docker/K8s runtimes.

## What I could not verify

- openworker relay-connector egress and its config disable (read, not run).
- Disabling Bash/shell in Claude Code and openworker (so "MCP-only" is proven only for OpenHands).
- Codex CLI, Cline and Goose behaviour at runtime (documentation only; Codex was installed but not exercised;
  Cline and Goose pages were partial).
- Claude Code's licence text; PyPI dependency licences; pinned reproducibility of the Python installs.
- Whether a real LLM provider rejects `remote.exec` as a function name on the OpenHands path (M5).
- Codex's default tool-call timeout; Cline's and Goose's refresh behaviour.
- The governed chain itself (policy, approval, audit, connector, broker): the gateway ran over a stub catalog, so
  nothing here says anything about it.
- Anything about a real model's behaviour; the fake model emitted exactly one scripted call.
