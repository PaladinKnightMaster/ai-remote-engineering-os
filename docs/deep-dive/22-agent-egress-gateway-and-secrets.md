# Agent Egress Gateway and Secrets

## Purpose

Agents need to call APIs, databases, SaaS tools, Git forges, package registries, and internal services. They should not receive raw long-lived credentials.

## Responsibilities

- Authenticate every outbound call.
- Authorize every outbound call.
- Inject credentials just-in-time where policy allows.
- Keep credentials out of prompts, memory, logs, and agent environments.
- Support human approval for sensitive credentials.
- Maintain full audit trails.
- Enforce allowlists and network boundaries.

## Pattern

```text
Agent tool call
  -> Harness Runtime
  -> Policy check
  -> Approval check if needed
  -> Egress Gateway
  -> Credential broker
  -> External/internal service
  -> Response filtered and logged
  -> Agent receives minimal result
```

## Security requirements

- No raw secrets in model context.
- No unmanaged `.env` sharing with autonomous agents.
- Short-lived capability tokens.
- Per-ticket and per-run access scoping.
- Revocation and kill switch.
