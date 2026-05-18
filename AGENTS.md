# AGENTS.md — AI Remote Engineering OS

This project follows v3.0.1 of the AI Remote Engineering OS kit. Companion file for non-Claude agents (Codex, etc.). Claude Code uses `CLAUDE.md`.

## Workspace layout

This product repo lives at `AI-Agentic-OS/ai-remote-engineering-os/`. Two sibling concerns:

- `../agency-kit/` — generic war-room agency kit. Owns base CLAUDE.md template, core modules, templates, generic role cards, kit scripts. Read its content when this repo references `../agency-kit/...`.
- `../artifacts/ai-remote-engineering-os/v3.0.1/` — release binaries (ZIP/PDF/XLSX). Not part of this repo's git history.

Key sources of truth (this repo):
- `CANONICAL_SOURCE_OF_TRUTH.md`
- `docs/releases/v3.0.1/README.md`
- `docs/architecture/module-map.md`
- `docs/security/agent-permission-model.md`

## What this project is

A self-hosted AI Engineering Operating System for restricted networks. Combines remote execution, human/AI work boards, agentic desktops, living workstreams, governed memory, code intelligence, tool gateways, and evidence-driven review.

P0 modules: Remote Execution Cockpit, Audit Service, Permission Model, Offline Install.
P1+ modules are blocked until P0 ships.

## Setup commands

Backend language is **Go** (ADR-0041). Monorepo tooling is **Go workspaces + pnpm workspaces + top-level Taskfile** (ADR-0042). HTTP framework for `services/*` is **`go-chi/chi` v5 on `net/http`** with `oapi-codegen` (`chi-server` generator) producing server stubs from `docs/api-contracts/openapi.v3.fragment.yaml`; capability-edge middleware lives in a shared `packages/go/middleware/` module (ADR-0043). DB schema migrations use **`golang-migrate/migrate` v4 in library mode**, embedded per service via `embed.FS`, with a shared runner in `packages/go/migrate/` and per-service `services/<name>/migrations/` directories; `audit-service` is forward-only by policy. Invoked via `task migrate -- <service> <action>` (ADR-0044). Event bus: **no broker at P0**; Postgres transactional outbox in producers + sync HTTP append-only ingest into `audit-service` (chained-hash extended in-process at the chain authority); shared runner in `packages/go/outbox/`; relay exposed as `<service> outbox relay` subcommand; **NATS JetStream held as the named P1 reserve pick** (ADR-0045). Auth adapter: **no IdP daemon at P0**; shared `packages/go/auth/` issues PASETO v4 local capability tokens with a typed `CapabilityToken` claim object (`edge` from `agent-permission-model.md` plus `service-service`); tiered TTLs (agent 5min / operator 8h / service 1h / lease = lease expiry); `packages/go/middleware/RequireCapability` imports `packages/go/auth/`; service-to-service auth via signed PASETO service tokens with workload identity (closes the ADR-0045 S2S hangover; not mTLS at P0); single bootstrap operator account, argon2id; no self-service signup / password reset UI / OAuth providers / JWKS rotation at P0; **Zitadel held as the named P1 reserve pick** when enterprise federation is required (Keycloak rejected on operational-burden + supply-chain grounds) (ADR-0046). T-002 will land `go.work`, `pnpm-workspace.yaml`, `.npmrc`, and `Taskfile.yml` at the repo root. Until then the skeleton intentionally does not commit a Go module file or any package-manager lockfile.

> v3.0.1 ADRs (`docs/adr/ADR-0001.md` through `ADR-0040.md`) cover product decisions. New meta-decisions start at **ADR-0041**.

## Test / Lint / Build

Canonical entry points (per ADR-0042): `task lint`, `task test`, `task build`, `task offline-bundle`. Concrete implementations land with T-002 and the per-service tickets that follow. The kit lint script lives at `../agency-kit/scripts/war_room_lint.py` and runs against this repo as `python ../agency-kit/scripts/war_room_lint.py .` — note: a non-zero exit on the product repo is expected (kit assumes self-containment; run against `../agency-kit/` for kit-health checks).

## Code style

Deferred to per-language conventions. Decided in ADR-0002 and subsequent language-specific ADRs.

## Repo-wide expectations

- Build P0 first. P1/P2/P3 features require an explicit ticket and an approved ADR.
- No SaaS runtime dependency.
- No AI autonomy before audit + policy + approval primitives exist.
- All major dependency choices need an ADR.
- Diff budget: ≤12 files / ≤500 LoC per ticket unless explicitly approved.
- Read v3.0.1 docs before changing related code. Start at `docs/executive/`, `docs/architecture/module-map.md`, `docs/security/agent-permission-model.md`.
- Match existing conventions. Do not introduce new patterns without an ADR.
- Do not edit files under `../agency-kit/` from this repo's tickets. Kit upgrades happen in their own repo.

## Forbidden work (bootstrap phase)

- Adding business logic before T-002 (Docker Compose) and T-003 (shared types) are complete.
- Touching `services/`, `apps/`, or `packages/shared-types/` with non-scaffolding code in this bootstrap phase.
- Introducing network-egress code paths before the egress policy in `docs/security/` is implemented.
- Modifying existing ADRs under `docs/adr/` — propose a new ADR instead.
- `git init` or destructive git ops without explicit user direction.

## Verification

Every claim in a PR description must resolve to a path in this repo or `../agency-kit/`. PRs that fail this check are returned.

## Ticket format

See `.github/ISSUE_TEMPLATE/ticket.md`. Tickets are T-NNN, aligned with the v3.0.1 backlog under `docs/requirements/`.
