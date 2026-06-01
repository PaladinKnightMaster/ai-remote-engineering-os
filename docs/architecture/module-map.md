# v3 Module Map

```text
AI Engineering OS
  - Remote Execution Cockpit
  - Agentic Desktop System
  - Agentic Work Board
  - Living Workspace / Workstream Engine
  - Agent Harness Runtime
  - Knowledge + Memory + Context Index
  - Code + Git Intelligence Layer
  - AI Browser / Desktop Automation Layer
  - Typed MCP Gateway
  - Agent Skill + Plugin Registry
  - Agent Egress + Credential Gateway
  - Agent Backend Sandbox
  - Evaluation + Benchmark Layer
  - Optional Companion / Buddy UX
```

Each module must have clear contracts and can be shipped incrementally. The MVP must not depend on the P2/P3 modules.

## P0 implementation mapping (v0.1.0)

The conceptual modules above ship incrementally as Go services + shared packages
under `go.work` (ADR-0041/0042), with the SPA in `apps/web` (ADR-0047). What
exists at v0.1.0:

| P0 component | Role | ADRs |
|---|---|---|
| `services/api` | Operator REST control plane: resource registry + cockpit sessions (`/v1/sessions`) + operator login. Holds target credentials via `pgcrypto` (interim debt). | 0043, 0046, 0048 §2/§7 |
| `services/session-broker` | Remote Execution Cockpit data plane: WebSocket→SSH PTY (`/v1/sessions/{id}/channel`), asciinema cast recording, per-input-line audit. **Stateful** — own migrations (`0001_outbox`), subcommands `serve\|migrate\|outbox relay\|casts gc`. Authenticates the operator's `api`-audience session cookie. | 0048, 0049, 0050 |
| `services/audit-service` | Append-only chained-hash chain authority (`POST /v1/events`); forward-only migrations. | 0044, 0045 |
| `services/connector-service` | Credential + egress gateway — **held in reserve** (the architecturally-correct home for target secrets; not built at v0.1.0). | 0048 §7 |
| `packages/go/{auth,middleware,migrate,outbox}` | Shared infra: PASETO capability tokens, capability-edge middleware, embedded migrations, transactional outbox (first producer: session-broker). | 0043–0046 |
| `apps/web` + `packages/{ui,shared-types,config}` | SPA; `<TerminalView>` (xterm.js) drives the cockpit WS over the typed client. | 0047 |
| `tests/e2e` + `infra/docker-compose/docker-compose.e2e.yml` | Live cross-service proof (`task e2e`): the SSH cockpit end-to-end walkthrough + audit-chain assertion. | T-020b, 0045 §Verification 3 |

P1/P2/P3 modules (Typed MCP Gateway, Workstream Engine, Memory/Context Index,
Workflow Compiler, Desktop/Browser Automation, Agent Backend Sandbox, etc.)
remain conceptual — no P0 implementation, per the priority ladder.
