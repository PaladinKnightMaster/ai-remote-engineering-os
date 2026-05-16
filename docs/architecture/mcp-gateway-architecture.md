# Typed MCP Gateway Architecture

## Purpose
Allow agents to use MCP tools without trusting arbitrary MCP servers directly.

## Responsibilities
- discover MCP tools
- generate typed clients
- enforce policy before calls
- broker credentials
- audit all tool calls
- isolate untrusted servers
- expose safe internal facades to agents

## Rule
MCP-compatible, not MCP-trusting.
