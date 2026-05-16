# PRD - Project B: Agentic Desktop System

## Problem

Users want an AI coworker that can work on actual project files, terminal, browser, IDE and apps. Frontier models are faster and higher quality when internet is available, but companies also need local/internal model-server support.

## Product statement

A desktop-local AI worker that can operate a user's project and selected apps through policy-gated tools, using frontier BYOK APIs or local/internal OpenAI-compatible endpoints.

## MVP scope

### Must-have

- Desktop app or local web UI.
- Local background worker.
- Project/folder workspace selection.
- Model provider registry.
- BYOK frontier model support.
- OpenAI-compatible internal/local endpoint support.
- File read/write with diff.
- Shell command request.
- Git diff/status.
- Approval timeline.
- Task output artifacts.
- QR phone dispatch via short-lived pairing.
- Audit log export.

### Should-have

- Browser automation.
- IDE awareness.
- Project instructions and memory.
- Local notifications.
- Model budget/cost display.

### Later

- GUI computer-use loop.
- Multi-agent team.
- MicroVM sandbox.
- Public skill marketplace.
- Native mobile app.

## QR pairing

QR code must not contain API keys, passwords or refresh tokens. It contains:

- gateway/local URL
- pairing ID
- desktop public key
- nonce
- expiry timestamp
- optional device label

Pairing requires confirmation on both desktop and phone.

## Tool policy

Default behavior:

| Tool | Default |
|---|---|
| read file | allowed inside workspace |
| write file | diff approval |
| shell command | approval |
| package install | approval |
| browser open | approval or allowlisted |
| network external | policy-gated |
| secret access | denied unless explicit |
| GUI click/type | later; approval-first |

## Acceptance metrics

- User can add model provider and run a project task.
- Agent creates patch and evidence artifact.
- Denied shell command stops execution.
- Phone can approve from QR-paired session.
