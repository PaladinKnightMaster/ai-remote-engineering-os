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
| `services/api` | Operator REST control plane: resource registry + cockpit sessions (`/v1/sessions`) + operator login. Enforces the approval gate at session-create (`pending_approval` park + policy consult) and the operator resolve route. **Proxies the operator's tool-invocation list + approve/deny (`/v1/invocations`, `/v1/invocations/{id}/resolve`) to the gateway's internal plane (v0.3.0; cookie/CSRF stay here).** **Hosts the Run engine (`internal/run`, v0.5.0): operator-launched governed multi-step Runs (`POST /v1/runs` + list/get/cancel) driven by an in-process, crash-safe (reclaimable-claim) sequencer through the gateway `/v1/steps` plane — the Workstream Engine's layer 1. The launch/cancel verb is the typed `human-agent` capability; the engine never drives egress itself.** **v0.6.0 (ADR-0062): hosts the delegation-grant store (`internal/grant`, sibling of `internal/run`): operator-issued, revocable, server-side grants (`POST /v1/grants` + list/revoke, typed `human-agent`; `grant.issued`/`grant.revoked` in-tx) that bound agent-launched Runs. Its FIRST inbound service-service route — internal `POST /v1/internal/delegated-runs` — accepts the keyless gateway's launch forward on a route-scoped, asymmetric-only verifier pinned `subject=="mcp-gateway"` (api holds the gateway's PUBLIC verify key only); api validates plan-vs-grant (the relocated SEC-16), creates the Run {actor=agent, principal=operator, grant_id}, and the SAME sequencer drives it. Revoking a grant cascades — its in-flight Runs are canceled.** **v0.7.0 (ADR-0063): hosts the chartered-Workstream module (`internal/workstream`, sibling of `internal/run`/`internal/grant`): operator-chartered standing objectives (`POST /v1/workstreams` + list/get + pause/resume/retire, typed `human-agent`) whose immutable sealed plan template fires governed Runs on a fixed-interval schedule with nobody present at launch. Chartering auto-issues the Workstream's own delegation grant in-tx (grantee `workstream:<id>`, 1:1); an in-process ticker (a third background goroutine beside the sequencer + reaper — single-winner CAS claim, no backfill, self-retire on a dead grant) fires due charters through the SHARED `run.LaunchBounded` core (the one plan-vs-grant + PERF-7 validator, also used by the delegated forward — SEC-30); the same sequencer drives the Run {actor=`workstream:<id>`, principal=operator}. Windowed step budget (`delegation_grants.budget_window_seconds`); retire revokes the grant → the existing cascade. Zero gateway change.** Holds NO target-credential key since v0.2.0. | 0043, 0046, 0048 §2, 0054, 0057, 0061, 0062, 0063 |
| `services/session-broker` | Remote Execution Cockpit data plane: WebSocket→SSH PTY (`/v1/sessions/{id}/channel`), asciinema cast recording, per-input-line audit. **Stateful** — own migrations (`0001_outbox`), subcommands `serve\|migrate\|outbox relay\|casts gc`. Authenticates the operator's `api`-audience session cookie; leases target credentials from connector-service at connect (holds no key). | 0048, 0049, 0050, 0053 |
| `services/audit-service` | Append-only chained-hash chain authority (`POST /v1/events`); forward-only migrations. | 0044, 0045 |
| `services/connector-service` | Credential + egress gateway — **BUILT at v0.2.0** (promotes the ADR-0048 §7 reserve): sole `AIREOS_RESOURCE_SECRET_KEY` holder + only pgcrypto decrypt path; internal `POST /v1/credentials/lease` (service-service); every lease audit-chained (`credential.leased`) via its outbox; `credentials genkey\|set-secret\|migrate-legacy` provisioning CLI. | 0048 §7, 0053 |
| `services/policy-service` | Approval/policy decision point — **BUILT at v0.2.0**: internal `POST /v1/decisions` + `POST /v1/approvals/{sessionID}/resolve` (service-service); owns `pending_approvals` + `policy_outbox`; fail-closed approval timeout sweeper; every decision/approval audit-chained. | 0054 |
| `services/mcp-gateway` | Typed MCP Gateway — **BUILT at v0.3.0** (the first agent surface, `:8085`): hand-rolled MCP subset (`initialize`/`tools/list`/`tools/call`) over `agent-tool` Bearer; the `remote.exec` tool drives a governed **invocation** (own `invocations` table + outbox) through policy → connector lease → broker exec → audit. Block-and-wait on a parked invocation; a separate service-service internal plane (`/v1/invocations` + `/v1/invocations/{id}/resolve`) that `services/api` proxies the operator's approve/deny to; serve-time reaper finalizes orphaned parked invocations. **Audit anchors span two namespaces by design** (opaque-key reuse, ADR-0054/0057): gateway/broker use `invocation:<id>`, policy/connector use `session:<id>` — same uuid, each producer's fixed prefix; the global hash chain stays single + intact. Holds NO resource-secret key. **HARDENED at v0.4.0 (ADR-0060): KEYLESS of the symmetric cluster key (SEC-20)** — signs its outbound service tokens with its OWN asymmetric (PASETO v4 public / Ed25519) key and verifies inbound with api's public key; policy/broker/audit accept it via **dual-mode verifiers**; broker-exec + connector-lease are **subject-pinned** (R5-ext); the persisted command is **encrypted at rest** (F3, AES-256-GCM, gateway-local key). A compromised gateway can forge no foreign-edge token (SEC-21). **v0.5.0 (ADR-0061): a subject-pinned (`subject=="api"`) internal `POST /v1/steps` route runs ONE governed Run step to terminal via the held-call — `runGovernedStep` was split (behavior-preserving) from the agent `tools/call` wrapper, so the gateway stays the SOLE governed-step orchestrator; the per-step bracket edge is `human-agent → agent-remote-session`. No v0.4 pin is loosened (the gateway→broker/connector tokens stay `mcp-gateway`-pinned; the operator id is metadata).** **v0.6.0 (ADR-0062): a second tool — `run.launch` — lets an agent launch a grant-bounded Run: a CONTROL-PLANE tool (no single-resource target → the per-call SEC-16 check is N/A; the api-validated grant is the scope), idempotency-keyed, mints no egress. The gateway does NOT drive the Run: `internal/apiforward` forwards {agent-subject, grant-id, plan} to api's pinned internal route (new env `API_URL`) and returns the run id. A delegated step's bracket edge is DERIVED: principal set → `agent-tool → agent-remote-session` (operator-direct stays `human-agent`); `invocations.principal` records the delegating operator for the approver's context.** | 0056–0062 |
| `packages/go/{auth,middleware,migrate,outbox}` | Shared infra: PASETO capability tokens, capability-edge middleware, embedded migrations, transactional outbox (producers: broker, api, connector, policy). **v0.4.0 (ADR-0060):** `auth` adds PASETO v4 **public** (Ed25519) asymmetric `Signer`/`Verifier` + a **dual-mode verifier** (dispatch on token type) for the per-workload-key migration off the symmetric cluster key. | 0043–0046, 0060 |
| `apps/web` + `packages/{ui,shared-types,config}` | SPA; `<TerminalView>` (xterm.js) drives the cockpit WS; `<ApprovalPrompt>` brokers the operator approve/deny on parked **sessions** (§FE-5) and, generalized at v0.3.0, on parked **tool invocations** (the `/invocations` surface — shows the agent + the verbatim command, ADR-0059). | 0047, 0054, 0057, 0059 |
| `tests/e2e` + `infra/docker-compose/docker-compose.e2e.yml` | Live cross-service proof (`task e2e`): the SSH cockpit walkthrough + the governed-egress approval walkthrough (WD-2) + audit-chain assertions. | T-020b, T-033a, 0045 §Verification 3 |

The Typed MCP Gateway (P1) is **built** as of v0.3.0 (see `services/mcp-gateway`
above). The **Workstream Engine's layer 1 — Runs** (operator-initiated governed
multi-step execution) is **built** as of v0.5.0 ("Governed Workstreams"): the Run
engine in `services/api` `internal/run` + the gateway `/v1/steps` plane + the
operator `/runs` UI. **Agent-initiated Runs** (the second initiator) are **built**
as of v0.6.0 (ADR-0062): the delegation-grant store + the `run.launch` launch path +
the actor/principal split + the operator `/grants` UI. The chartered **Workstream**
module — the **third initiator**, a standing operator-chartered objective that
*creates* Runs on a schedule — is **built** as of v0.7.0 (ADR-0063): the charter store
(`services/api` `internal/workstream`: objective + an immutable sealed plan template +
a bounded scope + a fixed-interval schedule, whose charter auto-issues its own
delegation grant in-tx), the in-process **ticker** (a third api background goroutine —
no cron dependency; single-winner CAS claim, no backfill; self-retire on a dead grant),
the **shared bounded-launch core** (`run.LaunchBounded` — one plan-vs-grant + PERF-7
validator used by both the gateway forward and the ticker, SEC-30), the windowed step
budget (`delegation_grants.budget_window_seconds`), and the operator `/workstreams` UI.
Actor = `workstream:<id>` on the ADR-0062 split; **zero gateway change**. The remaining
P1/P2/P3 modules (Memory/Context Index — the Workstream's future scoped-memory owner —
Workflow Compiler, Desktop/Browser Automation, Agent Backend Sandbox, etc.) remain
conceptual — no implementation, per the priority ladder.
