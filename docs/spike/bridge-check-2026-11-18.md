# Bridge check: Cline through a prototype `aireos-mcp-bridge`

**Commander seat, 2026-11-18.** The hands-on check the CEO set as the precondition for revising ADR-0080
(direction 2026-11-18: "Yes, check then revise"). It answers one question: **can an MCP client that cannot
renew a credential itself (here Cline) keep doing governed work for longer than one 5-minute agent token,
through a small local program that renews it for the client?**

**Answer: yes**, with one design requirement the check found and the first draft did not have (§4).

## 1. What ran

- **Client:** Cline CLI **3.0.74** (`npm i -g cline@3.0.74`, Apache-2.0), headless (`cline --json`), its
  `openai-compatible` provider pointed at a **scripted fake model** [ran]. Roo Code has no CLI on npm
  (`@roo-code/cli` → 404) [ran], so Cline was the candidate the CEO's brief allowed.
- **Bridge:** a ~190-line prototype, standard library only [`bridge-prototype/aireos-mcp-bridge.go`]. Stdio
  JSON-RPC in, HTTPS `POST /mcp` out. It reads the enrollment credential from a file, exchanges it for a
  token, **renews when the token is 4 minutes old** and **once on a `401`**, and trusts **only** the CA root
  file whose SHA-256 matches the pinned fingerprint (the system store is never consulted).
- **Gateway:** the **real** `services/mcp-gateway/internal/server.New`, compiled unchanged from `ba74cdd` in a
  scratch copy, over a stub catalog with one `remote.exec` tool. A command containing `slow` sleeps 130 s,
  standing in for a human approval wait [`bridge-prototype/gateway-stub.go`]. Nothing governed ran: no policy,
  approval, connector, broker or audit. **The stub also served a stand-in for ADR-0080's exchange endpoint**,
  minting with the real `auth.AsymmetricSigner.IssueAgent` (5-minute TTL) and comparing the credential by
  hash only. TLS used a throwaway CA whose root was **not** sent in the chain, as Caddy `tls internal` does
  (T-175e).
- **Model:** a 66-line fake [`bridge-prototype/fake-model.js`]. It asks for five `remote.exec` calls, waiting
  150 s before each one after the first (the second is the `slow` one), then stops. The 150 s waits keep
  the stream open with SSE comments.
- **Isolation:** each run had its own Docker `--internal` network (no route out), gateway, model and Cline
  container. Nothing from the host was mounted except the scratch output folder; no real key existed. All
  containers and networks were removed afterwards [ran: `docker ps -a`, `docker network ls`, 0 left].

## 2. Results

Three runs in parallel, each about 14 minutes, so every run crossed the 5-minute token lifetime twice
[ran: `docs/spike/bridge-prototype/run.sh`, 2026-11-19 00:09–00:24 UTC, which is the evening of 2026-11-18
local time]. Logs are in the session scratch folder, not the repo; the figures below are copied from them.

| Run | Bridge renews? | Cline tool timeout | Calls that returned a result to the model | Token exchanges | Held 130 s call |
|---|---|---|---|---|---|
| **main** | yes | 300 s (set) | **5 of 5** | 3 (initial + 2 proactive) | returned OK after 2 min 10 s |
| **control** | **no** | 300 s (set) | **2 of 5**: calls 3–5 got `401 invalid capability token` | 1 | returned OK |
| **defaults** | yes | **60 s (Cline's default)** | **4 of 5**: the held call **timed out in Cline at 60 s** | 3 | **Cline gave up; the gateway finished it anyway** |

- **Renewal through the bridge works with a client that knows nothing about it.** In the main run Cline was
  configured once and the bridge renewed at 00:16:24 and 00:21:24 with no client involvement.
- **The control proves the test can fail.** Without renewal, every call after the first token's five minutes
  got `401`. ⚠ **Cline still exited 0** and passed the errors to the model, so "the client finished" is not
  a pass signal; the check counts results the model received [read: `out/*/model.log` `lastResult`].
- **CA pinning refuses a mismatch.** With one hex digit of the pin changed, the bridge exited 1 with
  `CA fingerprint MISMATCH … refusing to connect` before opening any connection; with the right pin it
  proceeded [ran].
- **Cline renames our tool** to `aireos__remote_exec_e381c3ae`: the dot replaced plus a hash suffix [read:
  `out/smoke/model.log`]. That is the third client that rewrites the dotted name itself (M5).
- **Cline's MCP tool timeout defaults to 60 s, maximum 3600** [read: bundled `@cline/shared/dist/index.js`,
  `bT=60` … `Math.min(Math.max(e,1),3600)`], and is set by a `"timeout"` field in seconds in
  `cline_mcp_settings.json`. Its error tells the user so.

## 3. What the bridge does not fix

- **M3, the approval wait.** The client's tool timeout applies to a stdio server exactly as to a remote one.
  The defaults run shows it: Cline abandoned the held call at 60 s. Every client configuration we ship must
  set the client's timeout above the approval wait, or the gateway must keep a held call alive in a way the
  clients honour. The bridge cannot do it alone.

## 4. ⚠ The design requirement the check found

**In the defaults run, Cline abandoned the held call at 60 s, but the call carried on.** Cline sent no
cancellation: the bridge saw only `initialize`, `notifications/initialized`, `tools/list` and five
`tools/call` [ran: count of `method=` in `out/r3-defaulttimeout/bridge.log`]. The prototype bridge has no
per-call deadline, so it kept its HTTPS request open. The gateway therefore kept waiting and completed the
call at 2 min 10 s, while the model had been told it timed out.

**On the real gateway that would mean an approved command runs while the agent believes it failed**, and
the agent may retry it. The real gateway was built to avoid exactly this. When the agent's request goes
away, `waitForApproval` returns on `ctx.Done()` and leaves the row parked for the reaper. Nothing claims or
executes it [read: `services/mcp-gateway/internal/catalog/catalog.go:1328-1342`]. **A bridge that outlives
its client's call defeats that protection.**

⇒ **Requirement for the product bridge:** it must **never hold a call longer than the client will wait for
it.** It cancels its upstream request when the client cancels (`notifications/cancelled`), when the client
closes stdio, and when a per-call deadline expires. That deadline is set from the same timeout value the
cockpit writes into the client's configuration. This is now ADR-0080 D9 and Beta 1 SEC-74.

## 5. What this check did not establish

- **The renew-after-`401` path never ran.** Proactive renewal at 4 minutes always got there first, and the
  control had renewal off. A laptop that sleeps past expiry would hit it. Unverified; the product ticket
  must prove it.
- **Roo Code, Hermes, Grok Build, Goose:** not run. Each launches stdio MCP servers per its documentation
  [inherited: `docs/product/beta1-scoping/client-survey-2026-11-18.md`]. "Every client" is therefore a
  documented claim for those four and a measured one for Cline only.
- **Whether a direct-HTTP client that times out closes its connection** (so the real gateway's `ctx.Done()`
  protection fires) was not tested here. It matters for the helper path (Claude Code, Codex).
- **The governed chain:** policy, approval, audit and the real exchange endpoint were stubs. WD-13 covers them.
- **Platforms:** the bridge ran on Linux amd64 only.

## 6. Reproducing it

The four files in [`bridge-prototype/`](bridge-prototype/) are the exact sources run. They are **throwaway
spike code, not product code**: not in `go.work` and not built by any task. `gateway-stub.go` must be
placed at services/mcp-gateway/cmd/bridgespike/main.go (scratch copy only, never committed) in a scratch copy of the tree (it imports the
gateway's `internal` packages). Both Go programs were built with `golang:1.26`, `CGO_ENABLED=0`. `run.sh`
is written for Git Bash on Windows (`cygpath`) and takes `<name> <model-delay-s> <slow-s> <timeout|none>
<no-refresh 0|1>`. The three runs were `r1-main 150 130 300 0`, `r2-norefresh 150 130 300 1` and
`r3-defaulttimeout 150 130 none 0`.
