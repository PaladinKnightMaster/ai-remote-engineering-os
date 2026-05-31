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
| Skeleton / compose / Makefile | `infra/docker-compose/` (entry: `docker-compose.mvp.yml`) |
| Release pointer + audit + manifest | `docs/releases/v3.0.1/` |
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
| ADR-0048 | Cockpit session lifecycle: in-process Go SSH (`golang.org/x/crypto/ssh`) via `services/session-broker` for terminal sessions (guacd deferred to GUI types); `services/api` owns REST control plane (`/v1/sessions` CRUD), session-broker owns WebSocket data plane; per-input-line audit (Enter-delimited PTY input → chained-hash events; shell-agnostic, no prompt parsing); asciinema cast files at `/var/lib/aireos/sessions/<id>.cast` with SHA256 anchored in the close event; target credentials in `services/api`'s Postgres via `pgcrypto`-encrypted column at v0.1.0 (debt; `services/connector-service` held in reserve); 8h hard TTL / 30min idle / fresh-prompt-on-reconnect. **§6/§8 refined by ADR-0049.** Implemented T-016…T-020. | Accepted 2026-05-28 (post-spike) |
| ADR-0049 | Cockpit session audit content model + close authority + reconnect scope (refines ADR-0048 §6/§8): **hash-in-chain + cast-anchored** — audit events carry `evidencePacketId = sha256:<bytes>`, never raw bytes (content-free ingest per ADR-0045; keeps typed secrets out of the immutable chain; raw bytes live in the access-controlled cast); **broker-authoritative finalize-once close** (`store.Close WHERE cast_sha256 IS NULL`, since `api.end` is audit-silent); **v0.1.0 WS drop ends the session**, reconnect-with-fresh-shell deferred to v0.2.0; `session.terminated` reserved-unused at v0.1.0 | Accepted 2026-05-31 |

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
