# Agent Egress Policy

## Goals
Agents must not receive raw persistent secrets. They request operations through a gateway.

## Controls
- just-in-time credentials
- credential lease expiration
- domain/API allowlists
- human approval for sensitive access
- per-ticket egress scopes
- full audit trail
