# AI Remote Engineering OS

Self-hosted AI Engineering Operating System for restricted networks. v3.0.1 baseline.

## Workspace layout

This repo is one of three siblings under `AI-Agentic-OS/`:

- `../agency-kit/` — generic Claude war-room agency kit v4.2 (base CLAUDE.md template, core modules, templates, generic roles, kit scripts).
- `../artifacts/ai-remote-engineering-os/v3.0.1/` — release binaries (ZIP/PDF/XLSX/SHA256).
- `./` (this repo) — product source + project-specific agency overlays + canonical v3.0.1 docs.

## Start here

- `CANONICAL_SOURCE_OF_TRUTH.md` — what v3.0.1 says and where each piece lives across the workspace.
- `docs/executive/vision.md` — strategic vision and wedge.
- `docs/architecture/module-map.md` — the 14 modules and their dependencies.
- `docs/security/agent-permission-model.md` — capability-edge model.
- `docs/requirements/` — priority ladder, requirements, backlog, roadmap.
- `CLAUDE.md` — Claude Code agency instructions (project-customized).
- `AGENTS.md` — Codex / cross-agent instructions.

## Priority ladder

- **P0** — MVP survival (Remote Cockpit, Audit, Permission Model, Offline Install)
- **P1** — defensibility (MCP Gateway, Workstreams, Memory)
- **P2** — research / spike
- **P3** — optional delight

## Development rule

Build P0 first. No P1/P2/P3 without an explicit ticket (`.github/ISSUE_TEMPLATE/ticket.md`) and an approved ADR.

## Current phase

P0 — bootstrap. Repo skeleton in place. T-001 (this skeleton) complete. T-002 through T-010 pending.

## Release assets

Spec-set edition binaries (SPEC-2026.1) live in the sibling `../artifacts/ai-remote-engineering-os/v3.0.1/`, not in this repo. See `docs/spec-sets/v3.0.1/manifest_reference.md`. The **product's** shipped artifacts are `dist/offline/` — see `CANONICAL_SOURCE_OF_TRUTH.md` §Version namespaces.

## License

TBD per ADR.
