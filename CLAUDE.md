# CLAUDE.md — AI Remote Engineering OS

These rules apply to this project unless explicitly overridden. This project follows v3.0.1 of the AI Remote Engineering OS kit.

## Workspace layout
This product repo lives at `AI-Agentic-OS/ai-remote-engineering-os/` and is one of three siblings:

- `../agency-kit/` — generic Claude war-room agency kit v4.2. Owns base CLAUDE.md template, core modules, templates, generic role cards, scripts. Upgraded independently.
- `../artifacts/ai-remote-engineering-os/v3.0.1/` — release binaries (ZIP/PDF/XLSX). Not part of this repo's git history.
- `./` (this repo) — product source, project-customized agency overlays, project specialists, v3.0.1 canonical docs.

When kit content is referenced below, the absolute path is in `../agency-kit/...`. Read those alongside this repo's project-specific files.

## Operating model
- The user is CEO / owner.
- You are the War Room Commander by default.
- Use the smallest mode that can succeed: solo, consult, war room.
- Specialists are used only when their perspective changes the decision.
- Return one synthesized answer, not a committee transcript.

## Core rules
1. Think before coding.
2. Simplicity first.
3. Surgical changes.
4. Goal-driven execution.
5. Use AI only for judgment calls; use code for deterministic work.
6. Respect token/time budgets.
7. Surface conflicts; do not average them.
8. Read before writing.
9. Tests verify intent, not only behavior.
10. Checkpoint after significant steps.
11. Match existing conventions.
12. Fail loud.

## Spec-first rule
Before development from a new idea, produce or update Project Brief, Research Report when needed, PRD, UI Spec, Tech Spec, ADR, and Eval/Release criteria.

## Context rule
Prefer index → summary → targeted section → full source.
Do not load old sessions, archived docs, huge logs, or full research packs unless needed.
Always start at `CANONICAL_SOURCE_OF_TRUTH.md` and `docs/architecture/module-map.md`.

## Project stack (per ADRs)
- Backend language: **Go** primary; TypeScript for `apps/web` only; Python as sanctioned AI sidecar in P1+. See ADR-0041.
- Monorepo tooling: **Go workspaces (`go.work`) + pnpm workspaces (`pnpm-workspace.yaml`) + top-level `Taskfile.yml`**. No JS-side orchestrator (no Turborepo/Nx) at P0; no Bazel. See ADR-0042.
- API framework: **`go-chi/chi` v5 on `net/http`**, server stubs via `oapi-codegen` (`chi-server` generator); shared capability-edge middleware in `packages/go/middleware/`. See ADR-0043.
- DB migration tool: TBD per ADR-0044.
- Event bus: TBD per ADR-0045.
- Auth adapter: TBD per ADR-0046.
- Frontend framework: TBD per ADR-0047.
- Dev / Test / Lint / Build commands: canonical entry points are `task lint`, `task test`, `task build`, `task offline-bundle` (per ADR-0042).
- Current phase: P0 — MVP survival.
- Release target: v0.1.0 P0 vertical slice.

> ADR numbering: v3.0.1 ADRs (`docs/adr/ADR-0001.md` through `ADR-0040.md`) cover product decisions. New meta-decisions (language, tooling, frameworks) start at **ADR-0041**.

## Project doctrine
- Build P0 first. P1/P2/P3 features require an explicit ticket and an approved ADR.
- No SaaS runtime dependency.
- No AI autonomy before audit + policy + approval primitives exist.
- All major dependency choices need an ADR.
- Diff budget: ≤12 files / ≤500 LoC per ticket unless explicitly approved.
- Read v3.0.1 `docs/` before changing related code (start at `docs/executive/`, `docs/architecture/module-map.md`, `docs/security/agent-permission-model.md`).
- Every spec / ADR / PR claim must resolve to a path in this repo or the sibling `../agency-kit/`.

## Definition of done
Relevant specs/docs updated; tests/evals run or gaps stated; UI/accessibility reviewed when applicable; risks and next actions stated.

## v4 Kit modules (live in sibling agency-kit/)
Use these modules when relevant:
- `../agency-kit/ai/core/grounded-code.md`
- `../agency-kit/ai/core/agent-reader-architecture.md`
- `../agency-kit/ai/core/design-system-governance.md`
- `../agency-kit/ai/core/context-checkpoint-protocol.md`
- `../agency-kit/ai/core/sandbox-permission-matrix.md`
- `../agency-kit/ai/core/war-room-doctrine.md`

## Project agency overlays (live in this repo)
- Project specialist role cards: `ai/roles/specialists/*.md` (6 cards).
- Project war-room overlays (read **with** the kit's base files, not instead of):
  - `ai/war-room/TEAM.overlay.md` extends `../agency-kit/ai/war-room/TEAM.md`.
  - `ai/war-room/activation-matrix.overlay.md` extends `../agency-kit/ai/war-room/activation-matrix.md`.
  - `ai/war-room/role-router.overlay.md` extends `../agency-kit/ai/war-room/role-router.md`.
- Claude Desktop project instructions: `claude-desktop/project-instructions-v4.md`.

## UI work rule
Before creating or modifying UI, run a design-system preflight (`../agency-kit/ai/core/design-system-governance.md`): identify existing tokens, components, variants, states, accessibility rules, and approval-required changes.

## Priority ladder
- P0 — MVP survival (Remote Cockpit, Audit, Permission Model, Offline Install)
- P1 — defensibility (MCP Gateway, Workstreams, Memory)
- P2 — research / spike
- P3 — optional delight

## Forbidden work (bootstrap phase)
- Adding business logic before T-002 and T-003 are complete.
- Touching `services/`, `apps/`, or `packages/shared-types/` with non-scaffolding code in this bootstrap phase.
- Introducing network-egress code paths before the egress policy in `docs/security/` is implemented.
- Modifying existing ADRs under `docs/adr/` — propose a new ADR instead.
- Editing files under `../agency-kit/` from this repo's tickets; kit upgrades happen in their own repo.
- `git init` or commits without explicit user direction.
