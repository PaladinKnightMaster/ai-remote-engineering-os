# CANONICAL SOURCE OF TRUTH — AI Remote Engineering OS

Baseline: **v3.0.1** (APPROVED FOR DEVELOPMENT — see `docs/releases/v3.0.1/development_readiness_audit_v3_0_1.md`).

Earlier versions (v1, v2, v2.2, v3.0.0) are retained under `docs/deep-dive/` for historical traceability only. Do not start new work from them.

## Workspace layout

This product repo is one of three siblings under `AI-Agentic-OS/`:

| Sibling | Owns |
|---|---|
| `../agency-kit/` | Generic Claude war-room agency kit v4.2: base CLAUDE.md template, core modules, templates, 15 generic core roles, 12 generic specialist roles, scripts. Upgraded independently. |
| `../artifacts/ai-remote-engineering-os/v3.0.1/` | Release binaries: ZIP starter pack, executive PDF, traceability XLSX, SHA256 manifest. Not in this repo's git history. |
| `./` (this repo) | Product source, project-customized agency overlays, project specialists, v3.0.1 canonical docs. |

Version-control strategy across all three siblings: `../VERSION_CONTROL.md` (refresh procedures for `agency-kit/`, integrity verification for `artifacts/`, branching policy for this repo).

## Read in this order

1. `docs/executive/vision.md` — strategic vision and wedge.
2. `docs/executive/strategy.md` — go-to-market and defensibility.
3. `docs/executive/north-star-roadmap.md` — roadmap.
4. `docs/architecture/module-map.md` — the 14 modules and dependencies.
5. `docs/architecture/agent-harness-architecture.md` — agent harness runtime.
6. `docs/architecture/memory-context-architecture-v3.md` — memory scopes and context.
7. `docs/security/agent-permission-model.md` — capability-edge model.
8. `docs/security/egress-policy.md` — outbound traffic governance.
9. `docs/security/sandbox-security.md` — execution sandbox boundaries.
10. `docs/requirements/requirements_v3.csv` — requirements table.
11. `docs/requirements/backlog_v3.csv` — backlog table.
12. `docs/requirements/priority_legend_v3.csv` — P0/P1/P2/P3 meaning.
13. `references/references_v3.csv` — adoption decisions for external references.

## Authoritative directories (this repo)

| Concern | Path |
|---|---|
| Executive vision / roadmap | `docs/executive/` |
| Product / PRDs | `docs/product/` |
| Architecture / module map | `docs/architecture/` |
| Security / permission / egress | `docs/security/` |
| Requirements / backlog / priority legend | `docs/requirements/` |
| Tickets (T-NNN ladder) | `docs/tickets/` |
| API contracts / OpenAPI / event catalog | `docs/api-contracts/` (and `docs/api-contracts/openapi/`) |
| UI / UX specs | `docs/ui-ux/` |
| DevOps / offline install | `docs/devops/` |
| Evaluation / domain packs | `docs/evaluation/` |
| ADRs (40+ accepted) | `docs/adr/` |
| Diagrams (mermaid) | `docs/diagrams/` |
| Schemas (JSON) | `packages/shared-schemas/` |
| Skeleton / compose / Makefile | `infra/docker-compose/` (MVP entry: `docker-compose.mvp.yml`; live e2e: `docker-compose.e2e.yml`) |
| Live e2e harness | `tests/e2e/` (`task e2e`; SSH cockpit cross-service proof, T-020b) |
| Release pointer + audit + manifest | `docs/releases/v3.0.1/` |
| v0.1.0 release artifacts (DOC-6) | `docs/releases/v0.1.0/` (release notes + manifest + archival criteria copy) |
| v0.2.0 release artifacts (DOC-10) | `docs/releases/v0.2.0/` (release notes + manifest + archival criteria copy) |
| v0.3.0 release artifacts (DOC-11) | `docs/releases/v0.3.0/` (release notes + manifest + archival criteria copy) |
| v0.4.0 release artifacts (DOC-12) | `docs/releases/v0.4.0/` (release notes + manifest + archival criteria copy) |
| v0.5.0 release artifacts (DOC-13) | `docs/releases/v0.5.0/` (release notes + manifest + verification runbook + criteria) |
| v0.6.0 release artifacts (DOC-14) | `docs/releases/v0.6.0/` (release notes + manifest + verification runbook + criteria) |
| Reference inventory | `references/references_v3.csv` |
| Reference tags | `references/reference-tags-v3.md` |
| Deep-dive / historical docs | `docs/deep-dive/` |

## Agency instructions

- Claude Code agents: read `CLAUDE.md` first.
- Codex and other agents: read `AGENTS.md` first.
- **Generic kit content** (core modules, templates, generic roles): `../agency-kit/ai/`.
- **Project agency overlays**:
  - `ai/roles/specialists/*.md` (6 project specialists).
  - `ai/war-room/TEAM.overlay.md` — extends `../agency-kit/ai/war-room/TEAM.md`.
  - `ai/war-room/activation-matrix.overlay.md` — extends `../agency-kit/ai/war-room/activation-matrix.md`.
  - `ai/war-room/role-router.overlay.md` — extends `../agency-kit/ai/war-room/role-router.md`.
  - `claude-desktop/project-instructions-v4.md` — project-customized Claude Desktop instructions.

## ADR ladder

| Range | Topic | Status |
|---|---|---|
| ADR-0001..ADR-0040 | Product decisions (from v3.0.1 baseline) | Accepted/Proposed per individual files |
| ADR-0041 | Backend language: Go primary, TS frontend-only, Python sanctioned sidecar | Accepted 2026-05-16 |
| ADR-0042 | Monorepo tooling: Go workspaces + pnpm workspaces + Taskfile | Accepted 2026-05-16 |
| ADR-0043 | API framework: `go-chi/chi` v5 on `net/http`, `oapi-codegen` chi-server stubs | Accepted 2026-05-17 |
| ADR-0044 | DB migration tool: `golang-migrate/migrate` v4 (library mode, embedded per service); shared runner in `packages/go/migrate/`; per-service migrations; `audit-service` forward-only | Accepted 2026-05-17 |
| ADR-0045 | Event bus: no broker at P0; Postgres transactional outbox + sync HTTP append-only ingest into `audit-service`; shared `packages/go/outbox/`; NATS JetStream held as the named P1 reserve pick | Accepted 2026-05-17 |
| ADR-0046 | Auth adapter: no IdP daemon at P0; in-process `packages/go/auth/` issuing PASETO v4 capability tokens; PASETO service tokens with workload identity for service-to-service (closes ADR-0045 S2S hangover); Zitadel held as the named P1 reserve pick | Accepted 2026-05-18 |
| ADR-0047 | Frontend framework for `apps/web`: no meta-framework at P0; plain Vite 6 + React 18 SPA built to static assets; `react-router` v7 SPA mode; TS client via `openapi-typescript` + `openapi-fetch` (same OpenAPI fragment as Go server stubs); Tailwind v4 + vendored shadcn/ui + Radix; httpOnly + SameSite=Strict session cookie with server-side sliding-refresh + double-submit CSRF; capability-edge UI via `GET /v1/auth/me` + `<CapabilityProvider>` + `<CapabilityGate>`; same stack commits to `apps/admin`; Remix (SPA mode) / TanStack Start held as the named P1 reserve; Next.js explicitly NOT held in reserve | Accepted 2026-05-19 |
| ADR-0048 | Cockpit session lifecycle: in-process Go SSH (`golang.org/x/crypto/ssh`) via `services/session-broker` for terminal sessions (guacd deferred to GUI types); `services/api` owns REST control plane (`/v1/sessions` CRUD), session-broker owns WebSocket data plane; per-input-line audit (Enter-delimited PTY input → chained-hash events; shell-agnostic, no prompt parsing); asciinema cast files at `/var/lib/aireos/sessions/<id>.cast` with SHA256 anchored in the close event; target credentials in `services/api`'s Postgres via `pgcrypto`-encrypted column at v0.1.0 (debt; `services/connector-service` held in reserve); 8h hard TTL / 30min idle / fresh-prompt-on-reconnect. **§6/§8 refined by ADR-0049; §1/§4 refined by ADR-0050.** Implemented T-016…T-020. | Accepted 2026-05-28 (post-spike) |
| ADR-0049 | Cockpit session audit content model + close authority + reconnect scope (refines ADR-0048 §6/§8): **hash-in-chain + cast-anchored** — audit events carry `evidencePacketId = sha256:<bytes>`, never raw bytes (content-free ingest per ADR-0045; keeps typed secrets out of the immutable chain; raw bytes live in the access-controlled cast); **broker-authoritative finalize-once close** (`store.Close WHERE cast_sha256 IS NULL`, since `api.end` is audit-silent); **v0.1.0 WS drop ends the session**, reconnect-with-fresh-shell deferred to v0.2.0; `session.terminated` reserved-unused at v0.1.0 | Accepted 2026-05-31 |
| ADR-0050 | Cockpit WebSocket data-plane contract (refines ADR-0048 §1/§4 + ADR-0046 audience model): WS route is `GET /v1/sessions/{id}/channel` (not `…/stream`); the `session-broker` authenticates the operator's existing `api`-audience session cookie (no separate broker-audience token — ADR-0048 §4 "the cookie flows through"). Surfaced + fixed by the T-020b live e2e proof (the broker had required an unsatisfiable `session-broker` audience → every real-PASETO WS upgrade 401'd) | Accepted 2026-05-31 |
| ADR-0051 | Offline-install architecture (settles release-criteria OQ-3/4/6/7): Linux static-binary host via systemd (no Docker runtime); **Caddy** reverse proxy (single binary; serves the SPA + proxies `/v1` → api and the WS `/channel` → broker; `tls internal` self-signed); **postgres required on host** (not bundled — would bust the OI-4 ≤150 MB gz cap) with **staged offline `.deb`/`.rpm` packages + guide**, `install.sh` validates + migrates (api/audit/broker) but doesn't create the server; **operator provisioning prompt-default + env-override** (idempotent); **self-signed local-CA TLS default** + operator-cert escape hatch (ACME rejected); **Linux server only, Windows = browser client** (Windows-server deferred). Refines OI-3 (host-postgres is a guided pre-step). Binding spec for the §OI tickets | Accepted 2026-05-31 |
| ADR-0052 | Offline postgres ships as **per-OS companion artifacts** (`aireos-postgres-<ostag>.tar.gz`), not in the core bundle (refines ADR-0051 §3): each is a self-contained local package repo with the full postgres-16 + pgcrypto dependency closure (~80–85 MB/OS — would bust the OI-4 150 MB-gz cap if co-resident) + an offline `install-postgres.sh`; core bundle stays ~28 MB + universal. v0.1.0 scope Ubuntu 24.04 + Rocky/RHEL 9, amd64. Verified offline on both. | Accepted 2026-05-31 |
| ADR-0053 | **connector-service — credential custody + egress gateway** (v0.2.0 "Governed Egress Foundation"; promotes the ADR-0048 §7 reserve, closes the SEC-12 pgcrypto debt). connector-service is the **sole** key-holder/decrypt authority — removes the `pgp_sym_decrypt` path from `services/api` (`internal/secret`) and the broker mirror (`internal/store.LoadTarget`); owns its own credential table + transactional outbox in the shared cockpit DB; the **session-broker leases** the credential at connect via sync RPC (`edge: service-service`) — **refines ADR-0045**'s illustrative `api→connector` row to `broker→connector` (intent — lease=sync command not event — preserved). Existing ciphertext data-migrated into connector custody; `resources.connection_secret` dropped; `AIREOS_RESOURCE_SECRET_KEY` relocates api→connector env. | Accepted 2026-06-01 |
| ADR-0054 | **policy-service approval/policy decision point + `<ApprovalPrompt>`** (v0.2.0; the human-in-the-loop primitive — doctrine's missing "approval" leg). Typed `allow`/`deny`/`requires_approval` decision over `(actor, edge, target, action)`; **`services/api` enforces at session-create** (the broker stays a pure data plane); **synchronous block-and-wait** approval (same-operator self-approve proves the mechanism; segregation-of-duties + async queue → v0.3.0); policy-service owns its DB (`pending_approvals` + `policy_outbox`), ~120 s **fail-closed** timeout (`approval.denied:timeout`); every decision audit-chained (ADR-0045/0049 hash-in-chain). | Accepted 2026-06-01 |
| ADR-0055 | **v0.2.0 criteria amendment — durability riders re-scoped** (amends `docs/evaluation/v0.2.0-release-criteria.md` WD-2.5/FE-9 per that doc's own change rule, without editing it): WD-2.5 narrows to the T-032 reaper as shipped (api-owned, **status-only**, pre-open sessions; the broker stays the sole `session.close` authority per ADR-0049); WS-drop reconnect (needs its own design pass — naive fresh-shell re-triggers approval; resume would contradict ADR-0049 §3) + the active-orphan audit-close (**needs a broker heartbeat**) defer to v0.3.0; FE-9 defers with reconnect. Papers the T-032/T-033 consult decisions. | Accepted 2026-06-11 |
| ADR-0056 | **Typed MCP Gateway protocol surface & trust boundary** (v0.3.0 "Typed MCP Gateway"; the first agent surface). New `services/mcp-gateway` (Go/chi, `:8085`), an MCP **server** over **streamable HTTP** — v0.3.0 implements the synchronous POST subset (`initialize`/`tools/list`/`tools/call` + JSON-RPC errors; SSE + session state deferred). **Hand-rolled behind a `ToolTransport` adapter** (no third-party MCP SDK; supply-chain/offline doctrine; SDK = recorded future option = localized swap, not rewrite). Agent auth = PASETO **Bearer**, edge `agent-tool`, audience `mcp-gateway` (not MCP OAuth). **Two planes, browser-free:** agent (Bearer MCP endpoint) + internal service-service; **all operator HTTP stays on `services/api`** (proxy; ADR-0050 cookie/CSRF not re-implemented). **MCP-compatible, not MCP-trusting:** internal facades only — external/untrusted-server proxying + isolation deferred. No secret custody (SEC-13/17). | Accepted 2026-06-14 |
| ADR-0057 | **Tool-catalog schema, tool→edge mapping & invocation-anchored governance** (v0.3.0). A typed tool declares `{name, inputSchema, edge, targetResolver, action, requires_approval}`; the wedge tool `remote.exec` → edge `agent-remote-session`, target `resource:<id>`, approval from the resource label. **Invocation** = gateway-owned record + **idempotency key** (no double-exec); the gateway calls `policy.Decide` with `session_id = invocation_id` — policy's opaque key, **so ADR-0054 is unchanged**. **Operator approval via `services/api`** (`POST /v1/invocations/{id}/resolve`, proxied to the gateway), reusing `<ApprovalPrompt>`. Governed egress = connector lease + **broker non-interactive exec** (sole SSH egress); **fail-closed** (no egress without decision+lease). Audit `tool.invoked`/`tool.result` + stdout-SHA exec anchor; per-agent concurrency cap + bounded exec. | Accepted 2026-06-14 |
| ADR-0058 | **Broker non-interactive exec egress + audit content model** (v0.3.0; extends ADR-0048/0049 with a one-shot egress modality). The broker gains `sshpty.Exec` — reuses the host-key-pinned dial + the connector lease (sole SSH egress, SEC-18); **stateless, no `sessions` row** (an agent tool call ≠ a cockpit session). Internal `POST /v1/exec` (service-service, audience `session-broker`) `{resourceId,command,invocationId}`→`{stdout,exitCode,truncated}`, **not** in the OpenAPI fragment. **Bounded** (PERF-1: `BROKER_EXEC_MAX_BYTES` 1 MiB + `BROKER_EXEC_TIMEOUT_SECONDS` 60s). One audit event — Outcome `remote.exec`, **Target `invocation:<id>`** (the v0.2.0 `session:<id>` role), **Evidence `sha256:<stdout>`**, the captured stdout **retained** as `<invocationId>.exec` under `AIREOS_CAST_DIR` (bytes-in-cast parity). `invocationId` = the opaque correlation through connector + the event — **no connector/policy change**. | Accepted 2026-06-14 |
| ADR-0059 | **Informed invocation approval — command visibility & parked-invocation lifecycle** (v0.3.0; extends ADR-0057's approval surface). Fixes the audit's F1 gap: an operator was approving `remote.exec` **blind** (the invocations row stored no command; `<ApprovalPrompt>` showed only the resource id). **Persist the `command` on the invocation row** at park (supersedes a non-normative T-039a migration *comment*, not an ADR); **operator-only read** via the api `human-agent` gate proxied to the gateway's internal list — **never on the agent plane, never logged**, treated as potentially secret-bearing like retained stdout (ADR-0049). `<ApprovalPrompt>` generalized **additively** (optional `command`/`agent`/`title`; session defaults preserved). A **gateway reaper** (the api T-032 `ReapStale` pattern) finalizes never-resolved parked invocations → `failed` past a TTL (≥ ADR-0054 approval TTL) — frees the PERF-3 slot + **bounds command retention** to the approval window. policy/connector/broker unchanged. Command at-rest encryption + persisted-command GC + SoD deferred. | Accepted 2026-06-15 |
| ADR-0060 | **Asymmetric per-workload keys on the agent-facing planes — removing the cluster key from `services/mcp-gateway`** (v0.4.0 "Hardened Agent Foundation"; promotes the ADR-0046 P1 key-separation trigger). The agent-facing gateway holds the symmetric **cluster** key for both verify + mint, so RCE = mint any edge/audience (incl. a connector-service lease = decrypt any credential). Symmetric keys give no partial isolation → move every **gateway-touching** edge to PASETO **v4 public** (Ed25519): one **api** agent-plane keypair (signs the `agent-tool` + gateway-bound `service-service` tokens; gateway verifies with api's public key) + one **gateway** keypair (signs gateway→policy/broker/audit; those peers verify with the gateway's public key). The **gateway boots with `AIREOS_PASETO_KEY` unset**. A **dual-mode verifier** in `packages/go/auth` dispatches `v4.local` (cluster symmetric) vs `v4.public` (one registered public key); broker-exec is pure-asymmetric + subject-pinned (R5-ext). Non-gateway planes stay v4-local; no per-workload registry / rotation tooling / JWKS at v0.4.0. **Refines ADR-0056 §7; edits no Accepted ADR (G2-clean).** | Accepted 2026-06-21 |
| ADR-0061 | **Governed Runs — operator-initiated multi-step execution above the gateway** (v0.5.0 "Governed Workstreams", layer 1; the Workstream Engine's execution layer + the first consumer of the v0.3/v0.4 agent surface). The v0.5.0 entity is a **Run** (one ordered governed execution of steps); the chartered **Workstream** (objective/sources/schedule/scoped-memory) is the **P1 module that creates Runs**, deferred. Operator-initiated; the engine is a capability of `services/api` behind an `internal/run` extraction seam (extract to its own service at v0.6 for agent-driven Runs). The gateway stays the **sole** governed-step orchestrator (broker `/v1/exec` is subject-pinned to `mcp-gateway` → `api` cannot drive egress): a **split, behavior-preserving** refactor extracts `runGovernedStep` from the agent wrapper (agent `task e2e` green first) + **one** new subject-pinned (`subject=="api"`) internal `POST /v1/steps` route; policy/connector/broker/audit reused unchanged (no new key/credential/minting workload). **Held-call** reuses `waitForApproval` with an extended per-call timeout; operator UX fully async (parked steps approved on the existing `/invocations` console). Launch = a typed **informed** `human-agent` authorization (full-plan review) consenting to unattended ungated execution; per-step bracket edge `human-agent → agent-remote-session` (NOT `agent-tool`); ADR-0054 self-approve preserved. Crash-safe resume via a **reclaimable** single-driver claim + deterministic `run:<id>:step:<index>` idempotency keys (PERF-2 dedup, no double-exec); **fail-closed** running-orphan + gateway reaper extension; **operator cancel/abort**; **non-atomic** (no rollback). Audit = `run.*` lifecycle events + a `run:<id>` correlation namespace + an **Evidence Packet** (ordered steps/decisions/approvals/stdout-SHAs/outcomes, hash-linked, replayable, reproducible). Bounded plan size + concurrent Runs. **Extends ADR-0057; edits no Accepted ADR (G2-clean).** | Accepted 2026-06-23 |
| ADR-0062 | **Agent-initiated Runs — operator-issued delegation grants as the agent's bounded standing authority** (v0.6; the second initiator for the ADR-0061 Run engine; discharges its §Deferred agent-driven-Run item, extraction still deferred onto the `internal/run` seam). An operator issues a **delegation grant** — a durable, **revocable, server-side record** in `services/api` (`delegation_grants`: principal/agent/resources/verbs/TTL/max-steps/max-concurrent/step-budget) that pre-authorizes ONE agent for a bounded resource+verb set; it **replaces informed-launch** as the human consent (per-plan → **per-grant**) and is the multi-step revocable **generalization of the agent-tool SEC-16 scope**. The **agent never mints** (holds nothing mintable; ADR-0060/SEC-20 preserved): it launches via a new **`run.launch` gateway MCP tool** (existing `aud=mcp-gateway` token), the **keyless gateway forwards** {agent-subject, grant-id, plan} to `api` over a **gateway-pinned (`subject=="mcp-gateway"`) service edge** — so the symmetric-key-holding `api` stays non-agent-reachable; `api` validates **plan ⊆ grant** and drives the EXISTING `/v1/steps` path unchanged. **Actor/principal split** (RFC 8693 `act`/`subject`): the conflated `Subject` becomes `{actor=agent, principal=operator}` across `/v1/steps` + the gateway invocation + `api` `runs` (nullable `grant_id`+`actor`); bracket edge **`agent-tool → agent-remote-session` + recorded principal/grant** (NOT `agent-agent` — no 2nd agent; keeps the canonical edge-set unchanged). **`policy.Decide` subject = the agent** → defeats ADR-0054 self-approve under the absent operator, so **flagged steps still park** for a live human; ungated steps run **unattended**. **Instant revocation cascades** (cancels the grant's in-flight Runs). Issuance `POST /v1/grants` / `…/{id}/revoke` / `GET /v1/grants` (`human-agent`+CSRF; `grant.issued`/`grant.revoked`). Lineage: RFC 8693 + RFC 7662 reference-token + capability-security POLA + STS/GCP bounded delegation, localized offline-first. **Extends ADR-0061 + ADR-0046; preserves ADR-0060; edits no Accepted ADR (G2-clean).** | Accepted 2026-06-30 |
| ADR-0063 | **Chartered Workstreams — schedule-fired, grant-bounded Runs** (v0.7; the **third initiator** for the ADR-0061 Run engine and the chartered P1 module it reserved the name for). A **Workstream** = a standing operator-chartered objective: label + objective + an **immutable plan template** (sealed commands; edit = retire + re-charter — consent binds to the reviewed artifact) + a bounded resource/verb scope + a fixed-interval schedule + bounds, in `services/api` `internal/workstream` (`workstreams` + `workstream_steps`, sibling of `internal/run`/`internal/grant`). **Chartering auto-issues the Workstream's own delegation grant in-tx** (grantee = `workstream:<id>`; principal = the operator) — ONE typed audited `human-agent` consent act; **no third authority model**. An in-process **ticker** (3rd api background goroutine; `interval_seconds` + `next_fire_at`, NO cron dep; single-winner status-guarded claim; missed ticks collapse; tick launch key `workstream:<id>:tick:<t>` rides the `(grant_id, launch_key)` dedup) instantiates the template through a **shared bounded-launch core** (plan-vs-grant SEC-29 + PERF-7 bounds extracted from the gateway-pinned `LaunchDelegatedRun` handler — one validator, two callers; lands behavior-preserving, WD-6 green first) → the EXISTING sequencer drives it (**zero gateway diff**). Actor = `workstream:<id>` / principal = operator on the ADR-0062 split unchanged; policy subject = the Workstream (flagged steps still park; FE-18/GOV-6 context reused); bracket edge = the existing `agent-tool` ("agent" = any non-human actor under delegated authority; no eighth edge). **Windowed budget**: additive nullable `delegation_grants.budget_window_seconds` (NULL = v0.6 cumulative semantics) closes the cumulative-`step_budget`-bricks-a-standing-charter gap. **Pause = soft** (ticks stop, in-flight completes) / **retire = revoke the grant → the existing cascade**. `workstream.*` lifecycle events; Run↔Workstream linkage = `runs.grant_id` (1:1). Surface: `/v1/workstreams*` + a `/workstreams` UI; the detail view's run history + evidence links = the v0.7 **scoped memory** (read-time, EVD-1; the full Memory/Context Index stays deferred with the Workstream as its future owning entity). **Extends ADR-0061 + ADR-0062 (additive grant generalizations); preserves ADR-0060 trivially; edits no Accepted ADR (G2-clean).** | Accepted 2026-07-05 |

## Development rule

Build P0 first. Do not implement P1/P2/P3 without an explicit ticket (`.github/ISSUE_TEMPLATE/ticket.md`) and an approved ADR.

## Release artifacts

Heavy binaries live in `../artifacts/ai-remote-engineering-os/v3.0.1/`, separate from this repo's git history:

- `ai_remote_engineering_os_starter_pack_v3_0_1_dev_ready.zip` (canonical content source).
- `AI_Remote_Engineering_OS_Executive_Spec_v3.pdf` (when delivered).
- `requirements_roadmap_traceability_v3.xlsx` (when delivered).
- `sha256_manifest.txt` (when delivered).

See `docs/releases/v3.0.1/manifest_reference.md` for verification commands.

## Notes

If a path referenced here does not resolve on disk, the v3.0.1 ZIP under `../artifacts/ai-remote-engineering-os/v3.0.1/` is the binary source of truth. Re-extract and re-map per `docs/superpowers/plans/2026-05-15-kit-install-and-repo-structuring.md` (see addendum for current sibling layout).
