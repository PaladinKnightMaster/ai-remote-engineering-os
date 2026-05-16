# Agent Permission Model

## Principle
Agents receive scoped capabilities, not ambient authority.

## Capability edges
A capability edge defines who/what can communicate with or act on another resource:
- human-agent
- agent-agent
- agent-tool
- agent-workspace
- agent-repo
- agent-memory
- agent-remote-session

Each edge defines allowed operations, context visibility, approval rules, rate limits, and audit level.
