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
