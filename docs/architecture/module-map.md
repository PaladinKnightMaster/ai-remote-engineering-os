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

## P0/P1-spine implementation mapping (v0.2.0)

The conceptual modules above ship incrementally as Go services + shared packages
under `go.work` (ADR-0041/0042), with the SPA in `apps/web` (ADR-0047). What
exists at v0.2.0 ("Governed Egress Foundation" — the P1 spine on the v0.1.0 P0
wedge):

| Component | Role | ADRs |
|---|---|---|
| `services/api` | Operator REST control plane: resource registry + cockpit sessions (`/v1/sessions`) + operator login. Enforces the approval gate at session-create (`pending_approval` park + policy consult) and the operator resolve route. Holds NO target-credential key since v0.2.0. | 0043, 0046, 0048 §2, 0054 |
| `services/session-broker` | Remote Execution Cockpit data plane: WebSocket→SSH PTY (`/v1/sessions/{id}/channel`), asciinema cast recording, per-input-line audit. **Stateful** — own migrations (`0001_outbox`), subcommands `serve\|migrate\|outbox relay\|casts gc`. Authenticates the operator's `api`-audience session cookie; leases target credentials from connector-service at connect (holds no key). | 0048, 0049, 0050, 0053 |
| `services/audit-service` | Append-only chained-hash chain authority (`POST /v1/events`); forward-only migrations. | 0044, 0045 |
| `services/connector-service` | Credential + egress gateway — **BUILT at v0.2.0** (promotes the ADR-0048 §7 reserve): sole `AIREOS_RESOURCE_SECRET_KEY` holder + only pgcrypto decrypt path; internal `POST /v1/credentials/lease` (service-service); every lease audit-chained (`credential.leased`) via its outbox; `credentials genkey\|set-secret\|migrate-legacy` provisioning CLI. | 0048 §7, 0053 |
| `services/policy-service` | Approval/policy decision point — **BUILT at v0.2.0**: internal `POST /v1/decisions` + `POST /v1/approvals/{sessionID}/resolve` (service-service); owns `pending_approvals` + `policy_outbox`; fail-closed approval timeout sweeper; every decision/approval audit-chained. | 0054 |
| `packages/go/{auth,middleware,migrate,outbox}` | Shared infra: PASETO capability tokens, capability-edge middleware, embedded migrations, transactional outbox (producers: broker, api, connector, policy). | 0043–0046 |
| `apps/web` + `packages/{ui,shared-types,config}` | SPA; `<TerminalView>` (xterm.js) drives the cockpit WS; `<ApprovalPrompt>` brokers the operator approve/deny on parked sessions (§FE-5 closed). | 0047, 0054 |
| `tests/e2e` + `infra/docker-compose/docker-compose.e2e.yml` | Live cross-service proof (`task e2e`): the SSH cockpit walkthrough + the governed-egress approval walkthrough (WD-2) + audit-chain assertions. | T-020b, T-033a, 0045 §Verification 3 |

P1/P2/P3 modules (Typed MCP Gateway, Workstream Engine, Memory/Context Index,
Workflow Compiler, Desktop/Browser Automation, Agent Backend Sandbox, etc.)
remain conceptual — no P0 implementation, per the priority ladder.
