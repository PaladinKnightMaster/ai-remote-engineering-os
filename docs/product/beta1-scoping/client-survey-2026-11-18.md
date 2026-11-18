# Agent-client survey — how each connects and authenticates to a remote MCP server

**Commander seat, 2026-11-18.** ⚠ **Documentation-level only** — none of these five was run. It extends
[`../../spike/b041-deep-pass.md`](../../spike/b041-deep-pass.md) §5/§7, which measured Claude Code, the
OpenHands SDK and openworker hands-on. Input to the ADR-0080 revision.

| Client | Connects to our gateway as-is? | Renews a credential unattended? | Asks the user before acting? |
|---|---|---|---|
| **Cline** | Yes — remote HTTP with a static `headers` credential | No helper documented; an "Authorize OAuth" option exists (detail unclear) | Per-tool auto-approve list, otherwise asks |
| **Roo Code** (Cline fork) | Yes — `"type": "streamable-http"` + `headers` | **No** — no OAuth and no refresh documented | `alwaysAllow` per tool, off by default |
| **Codex CLI** | Yes — `bearer_token_env_var` / `http_headers` | **Yes, `http_headers_helper`** (re-runs on 401/403) | Per-tool approval modes |
| **Hermes Agent** (Nous Research) | Yes — static `headers` with `${VAR}`, mTLS | **Only via OAuth** (`auth: oauth`, with refresh); no helper | Per-server `trust`; prompts Allow once / Session / Always |
| **Grok Build** (xAI CLI) | Yes — `--header` / `bearer_token_env_var` in `config.toml` | **Only via OAuth** (browser once); no helper documented | `[permission]` section (not detailed) |

## What it means

- **Transport is not the problem** for any of them. All speak remote HTTP MCP (consistent with the deep pass).
- **Credential refresh is.** Helper-based refresh (ADR-0080 as first drafted) covers **only Claude Code and
  Codex**. Hermes, Grok Build, Goose, OpenHands and openworker refresh **only via OAuth**; Roo (and probably
  Cline) cannot refresh at all.
- **Every client can launch a local stdio MCP server.** ⇒ The Commander's proposed revision: ship a small
  **`aireos-mcp-bridge`** (stdio ↔ HTTPS) on the agent host that holds the enrolled credential, renews the
  5-minute token itself, and pins the cockpit's CA fingerprint — one configuration for every client, no
  OAuth server needed. Keep the helper path for Claude Code/Codex; defer full OAuth with a narrower trigger.
  **Needs one hands-on check first:** Roo or Cline through a prototype bridge for more than 5 minutes.

## Sources

[Hermes Agent MCP](https://hermes-agent.nousresearch.com/docs/user-guide/features/mcp) ·
[Roo Code — using MCP](https://roocodeinc.github.io/Roo-Code/features/mcp/using-mcp-in-roo) ·
[Grok Build — MCP servers](https://docs.x.ai/build/features/mcp-servers) ·
[Grok Build — settings reference](https://docs.x.ai/build/settings/reference) ·
[Cline — MCP overview](https://docs.cline.bot/mcp/mcp-overview.md) ·
[Cline — remote server](https://docs.cline.bot/mcp/connecting-to-a-remote-server.md) — all read 2026-11-18.
