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
| `services/api` | Operator REST control plane: resource registry + cockpit sessions (`/v1/sessions`) + operator login. Enforces the approval gate at session-create (`pending_approval` park + policy consult) and the operator resolve route. **Proxies the operator's tool-invocation list + approve/deny (`/v1/invocations`, `/v1/invocations/{id}/resolve`) to the gateway's internal plane (v0.3.0; cookie/CSRF stay here).** **Hosts the Run engine (`internal/run`, v0.5.0): operator-launched governed multi-step Runs (`POST /v1/runs` + list/get/cancel) driven by an in-process, crash-safe (reclaimable-claim) sequencer through the gateway `/v1/steps` plane — the Workstream Engine's layer 1. The launch/cancel verb is the typed `human-agent` capability; the engine never drives egress itself.** **v0.6.0 (ADR-0062): hosts the delegation-grant store (`internal/grant`, sibling of `internal/run`): operator-issued, revocable, server-side grants (`POST /v1/grants` + list/revoke, typed `human-agent`; `grant.issued`/`grant.revoked` in-tx) that bound agent-launched Runs. Its FIRST inbound service-service route — internal `POST /v1/internal/delegated-runs` — accepts the keyless gateway's launch forward on a route-scoped, asymmetric-only verifier pinned `subject=="mcp-gateway"` (api holds the gateway's PUBLIC verify key only); api validates plan-vs-grant (the relocated SEC-16), creates the Run {actor=agent, principal=operator, grant_id}, and the SAME sequencer drives it. Revoking a grant cascades — its in-flight Runs are canceled.** **v0.7.0 (ADR-0063): hosts the chartered-Workstream module (`internal/workstream`, sibling of `internal/run`/`internal/grant`): operator-chartered standing objectives (`POST /v1/workstreams` + list/get + pause/resume/retire, typed `human-agent`) whose immutable sealed plan template fires governed Runs on a fixed-interval schedule with nobody present at launch. Chartering auto-issues the Workstream's own delegation grant in-tx (grantee `workstream:<id>`, 1:1); an in-process ticker (a third background goroutine beside the sequencer + reaper — single-winner CAS claim, no backfill, self-retire on a dead grant) fires due charters through the SHARED `run.LaunchBounded` core (the one plan-vs-grant + PERF-7 validator, also used by the delegated forward — SEC-30); the same sequencer drives the Run {actor=`workstream:<id>`, principal=operator}. Windowed step budget (`delegation_grants.budget_window_seconds`); retire revokes the grant → the existing cascade. Zero gateway change.** **v0.8.0 (ADR-0064): hosts Persistent Scoped Memory (`internal/memory`, the fourth sibling seam): a governed, forgettable projection beside the immutable audit chain. The core is accrual by reference — every terminal Run (all three initiators, all outcomes) writes a content-free evidence pointer in the SAME transaction as its once-only terminal flip; operator-authored free text is the secondary path (sealed at rest, operator-only read). Governed forgetting = content-free tombstone events (`memory.redacted`/`memory.expired`) over an idempotent flip — the chain, casts, and prior exports are explicitly out of redaction's reach by design. Operator surface `/v1/memories*` (list/write/redact/pin, owner-scoped, 64 KiB body cap) + a `/memories` UI with client-side manifest export; the expiry sweep rides the existing reaper goroutine (no new goroutine); the canonical `agent-memory` edge stays reserved-UNENFORCED at v0.8 — no agent path to memory exists (SEC-31). Zero gateway change; zero new dependency; zero AI.** **v0.9.0 (ADR-0065): Adaptive Workstreams — memory feeds execution under governance. `workstreams` gains the additive `adaptive` kind (objective + scope + schedule + a named composer agent = the auto-issued grant's grantee; no template); an adaptive tick opens a durable TTL-bounded `plan_requests` row (one open per charter, structurally) instead of instantiating; the composer answers over the gateway and the plan parks as a `proposed` Run (inert; full plan visible on the owner GET — consent binds to each tick's reviewed plan) behind `POST /v1/runs/{id}/approve|deny` (approve → the deferred `run.launched` → the unchanged sequencer; deny → canceled + `run.denied`). Three subject-pinned internal routes serve the gateway's forwards (plan-polls / plan-submissions / memory-reads); `internal/memory` gains `ReadForAgent` — the ONE agent-plane read: readable set derived server-side from the caller's own live grants, tombstone-excluded, authored content only when `agent_visible` (the audited share flip), every read chained fail-loud in-tx. Revocation cascades to all three surfaces. Migration 0013; zero install-wiring diff again.** **v0.10.0 (ADR-0066): hosts the Live/Context Index — the fifth sibling seam, `internal/ctxindex`** (the ADR and the release criteria say `internal/context` in prose; the seam was renamed at T-097 on a Commander review ruling because `package context` collides with the Go stdlib `context` package — an as-built naming fix only, no behavior change; see `docs/releases/v0.10.0/release_notes_v0_10_0.md` for the reconciliation note). It holds `context_sources` (an operator-registered directory bound to one charter, a confined-root file reader — no traversal, no symlink-escape) + `context_chunks` (native Postgres `tsvector` + GIN, chunked by document/heading for docs/tickets and by file/region for code; retrieval ranks by `ts_rank` + recency only — zero embeddings, zero new dependency) + an in-process incremental indexer riding the api's existing background goroutine (mtime-then-hash freshness, a per-source staleness signal, no full rebuild). The agent read plane is `SearchForAgent` — the `ReadForAgent` CTE skeleton reused over a different corpus: live grants → each grant's charter → context sources bound to that charter → FTS over their chunks (cross-grant/foreign-source reads impossible by construction; out-of-set answers empty, never an error); every agent-plane read chains one content-free `context.read` event fail-loud in-tx. Served by a subject-pinned internal route reached only via the gateway's `context.search` forward. Operator surface `/v1/context-sources*` (register/list/remove, typed `human-agent`, the GOV-11 consent copy). Migrations 0014+0015 ride the migrate loop; zero install-wiring diff again; the operator `/v1/memories*` surface stays byte-identical.** Holds NO target-credential key since v0.2.0. | 0043, 0046, 0048 §2, 0054, 0057, 0061, 0062, 0063, 0064, 0065, 0066 |
| `services/session-broker` | Remote Execution Cockpit data plane: WebSocket→SSH PTY (`/v1/sessions/{id}/channel`), asciinema cast recording, per-input-line audit. **Stateful** — own migrations (`0001_outbox`), subcommands `serve\|migrate\|outbox relay\|casts gc`. Authenticates the operator's `api`-audience session cookie; leases target credentials from connector-service at connect (holds no key). | 0048, 0049, 0050, 0053 |
| `services/audit-service` | Append-only chained-hash chain authority (`POST /v1/events`); forward-only migrations. | 0044, 0045 |
| `services/connector-service` | Credential + egress gateway — **BUILT at v0.2.0** (promotes the ADR-0048 §7 reserve): sole `AIREOS_RESOURCE_SECRET_KEY` holder + only pgcrypto decrypt path; internal `POST /v1/credentials/lease` (service-service); every lease audit-chained (`credential.leased`) via its outbox; `credentials genkey\|set-secret\|migrate-legacy` provisioning CLI. | 0048 §7, 0053 |
| `services/policy-service` | Approval/policy decision point — **BUILT at v0.2.0**: internal `POST /v1/decisions` + `POST /v1/approvals/{sessionID}/resolve` (service-service); owns `pending_approvals` + `policy_outbox`; fail-closed approval timeout sweeper; every decision/approval audit-chained. | 0054 |
| `services/mcp-gateway` | Typed MCP Gateway — **BUILT at v0.3.0** (the first agent surface, `:8085`): hand-rolled MCP subset (`initialize`/`tools/list`/`tools/call`) over `agent-tool` Bearer; the `remote.exec` tool drives a governed **invocation** (own `invocations` table + outbox) through policy → connector lease → broker exec → audit. Block-and-wait on a parked invocation; a separate service-service internal plane (`/v1/invocations` + `/v1/invocations/{id}/resolve`) that `services/api` proxies the operator's approve/deny to; serve-time reaper finalizes orphaned parked invocations. **Audit anchors span two namespaces by design** (opaque-key reuse, ADR-0054/0057): gateway/broker use `invocation:<id>`, policy/connector use `session:<id>` — same uuid, each producer's fixed prefix; the global hash chain stays single + intact. Holds NO resource-secret key. **HARDENED at v0.4.0 (ADR-0060): KEYLESS of the symmetric cluster key (SEC-20)** — signs its outbound service tokens with its OWN asymmetric (PASETO v4 public / Ed25519) key and verifies inbound with api's public key; policy/broker/audit accept it via **dual-mode verifiers**; broker-exec + connector-lease are **subject-pinned** (R5-ext); the persisted command is **encrypted at rest** (F3, AES-256-GCM, gateway-local key). A compromised gateway can forge no foreign-edge token (SEC-21). **v0.5.0 (ADR-0061): a subject-pinned (`subject=="api"`) internal `POST /v1/steps` route runs ONE governed Run step to terminal via the held-call — `runGovernedStep` was split (behavior-preserving) from the agent `tools/call` wrapper, so the gateway stays the SOLE governed-step orchestrator; the per-step bracket edge is `human-agent → agent-remote-session`. No v0.4 pin is loosened (the gateway→broker/connector tokens stay `mcp-gateway`-pinned; the operator id is metadata).** **v0.6.0 (ADR-0062): a second tool — `run.launch` — lets an agent launch a grant-bounded Run: a CONTROL-PLANE tool (no single-resource target → the per-call SEC-16 check is N/A; the api-validated grant is the scope), idempotency-keyed, mints no egress. The gateway does NOT drive the Run: `internal/apiforward` forwards {agent-subject, grant-id, plan} to api's pinned internal route (new env `API_URL`) and returns the run id. A delegated step's bracket edge is DERIVED: principal set → `agent-tool → agent-remote-session` (operator-direct stays `human-agent`); `invocations.principal` records the delegating operator for the approver's context.** **v0.9.0 (ADR-0065): three more control-plane tools — `workstream.poll` / `plan.submit` / `memory.read` — all forwards on the `run.launch` pattern (the gateway stays keyless; api stays the sole authority). `memory.read` alone carries the now-ENFORCED `agent-memory` edge (its ToolDef metadata + the pinned api route + the audit event — the reserved seventh edge's one lit path) and alone participates in the per-agent in-flight cap; `plan.submit` is idempotency-keyed on the tick's launch key and subject-pinned api-side to the grant's grantee.** **v0.10.0 (ADR-0066): a fourth control-plane tool — `context.search`** — the exact `memory.read` forward pattern: {agent, query, optional `workstreamId`} forwarded to a subject-pinned (`subject=="mcp-gateway"`) api internal route (`/v1/internal/context-searches`); resolves no target, mints no egress. It carries the second now-ENFORCED reserved edge, **`agent-workspace`** (after `agent-memory` at v0.9 — the reserved-edge-set is still unchanged, ADR-0046 preserved; `agent-repo` stays reserved for Git Intelligence). The gateway diff is the catalog entry + this one forward only — `api`'s `SearchForAgent` is sole authority. | 0056–0062, 0065, 0066 |
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
Actor = `workstream:<id>` on the ADR-0062 split; **zero gateway change**. The
**Knowledge + Memory + Context Index** module's **Layer 2 — Persistent Scoped Memory —
is built** as of v0.8.0 (ADR-0064; the P2 tier opener and the first governance/
data-plane release): the `internal/memory` sibling seam (accrual-by-reference on the
shared terminal flip + operator-authored sealed content), the forgetting verbs
(redact/pin/expire as content-free tombstone events), the reaper-fold expiry sweep,
the owner-scoped `/v1/memories*` surface, and the `/memories` UI with on-demand
manifest export — with the v0.7 charter detail promoted to read the persisted store.
Its **Layer 1 (Live/Context Index)** and **Layer 3 (Reasoning Retrieval — the layer
that would need an embedding/AI dependency, hard-gated by the offline + no-SaaS
doctrine)** remain conceptual, as do the other P2/P3 modules (Workflow Compiler,
Desktop/Browser Automation, Agent Backend Sandbox, etc.) — no implementation, per the
priority ladder. **Adaptive Workstreams are built** as of v0.9.0 (ADR-0065 — memory
feeds execution under governance, the loop closed): the additive `adaptive` charter
kind with a named composer, the inbound plan-request channel (the tick publishes, the
customer's external agent answers over the gateway — the platform never dials out),
the `proposed`-Run plan-preview park (consent binds to each tick's reviewed plan;
adaptive plans ALWAYS park at v0.9), and the **one lit `agent-memory` read path**
(read-only, same-grant by construction, share-gated, per-read chained fail-loud) —
zero model dependency; the plan author is the customer's external MCP client.
The Knowledge + Memory + Context Index module's **Layer 1 (Live/Context Index) is
built** as of v0.10.0 (ADR-0066 — the read plane's second half: remember [L2] +
know the current context [L1] → compose → govern → act → accrue): the
`internal/ctxindex` sibling seam (an operator-registered, charter-bound context
source confined to its registered root; a deterministic native-Postgres-FTS index
— `tsvector`/GIN, zero embeddings, zero new dependency; an in-process
mtime-then-hash incremental indexer with a per-source staleness signal) and the
**second lit reserved edge, `agent-workspace`** (read-only, same-grant-scoped by
construction, confined-root, per-read chained fail-loud, consumed only through a
plan that still always parks). `agent-repo` stays reserved for Git Intelligence
(REQ-015); tree-sitter AST/code-graph retrieval is deferred with a recorded
trigger (when agents gain a code-editing capability, most naturally alongside Git
Intelligence). **Layer 3 (Reasoning Retrieval)** remains hard-gated on the offline
+ no-SaaS doctrine, as do the other P2/P3 modules — no implementation, per the
priority ladder.
