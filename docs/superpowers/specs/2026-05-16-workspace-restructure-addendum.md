# Addendum — Workspace Restructure (3 siblings)

- Date: 2026-05-16
- Supersedes: layout sections of [2026-05-15-kit-install-and-repo-structuring-design.md](2026-05-15-kit-install-and-repo-structuring-design.md) and corresponding tasks in [2026-05-15-kit-install-and-repo-structuring.md](../plans/2026-05-15-kit-install-and-repo-structuring.md).
- Trigger: original layout co-located the war-room agency kit, release binaries, and product source in one repo. The user flagged that each has a different cadence, audience, and size profile — bundling them churns the product git history with kit upgrades and binary swaps.

## Decision

Adopt a **three-sibling workspace** under `D:\1_PROJECT\PRIVATE_WORK\AI-Agentic-OS\`:

```text
AI-Agentic-OS/
├── agency-kit/                              # generic Claude war-room agency kit v4.2
├── artifacts/                               # release binaries by <project>/<version>/
│   └── ai-remote-engineering-os/v3.0.1/
└── ai-remote-engineering-os/                # product repo (this one)
```

Each sibling can become its own git repo independently. References from the product repo to the kit use relative paths (`../agency-kit/...`); no submodules.

## What moved

| Was at | Is at |
|---|---|
| `ai-remote-engineering-os/ai/core/` | `agency-kit/ai/core/` |
| `ai-remote-engineering-os/ai/examples/` | `agency-kit/ai/examples/` |
| `ai-remote-engineering-os/ai/roles/core/` | `agency-kit/ai/roles/core/` |
| `ai-remote-engineering-os/ai/roles/specialists/{12 generic}` | `agency-kit/ai/roles/specialists/{12 generic}` |
| `ai-remote-engineering-os/ai/templates/` | `agency-kit/ai/templates/` |
| `ai-remote-engineering-os/ai/war-room/{TEAM,activation-matrix,role-router,output-contracts}.md` | `agency-kit/ai/war-room/{...same}` (base, unmodified) |
| `ai-remote-engineering-os/claude-desktop/{custom-style, knowledge-upload-checklist*, profile-preferences, session-starters, project-instructions}.md` | `agency-kit/claude-desktop/{same}` |
| `ai-remote-engineering-os/scripts/` (4 kit scripts) | `agency-kit/scripts/` |
| `ai-remote-engineering-os/artifacts/kits/*.zip` | `artifacts/ai-remote-engineering-os/v3.0.1/*.zip` |

## What stayed in the product repo

- All v3.0.1 canonical docs under `docs/` (executive, product, architecture, security, requirements, api-contracts, ui-ux, devops, evaluation, adr, diagrams, deep-dive, releases, superpowers).
- `packages/shared-schemas/`, `infra/docker-compose/`, `references/`, `.github/`.
- Service / app placeholders: `apps/`, `services/`, `packages/`.
- All project-customized agency files (the project's view of the kit):
  - `CLAUDE.md`, `AGENTS.md` (rewritten to reference `../agency-kit/`).
  - `CANONICAL_SOURCE_OF_TRUTH.md` (rewritten with workspace layout section).
  - `README.md` (rewritten with workspace layout section).
  - `ai/roles/specialists/*.md` — 6 project-specific specialist cards.
  - `ai/war-room/*.overlay.md` — 3 overlay files containing only the project additions (read **with** the kit's base files, not instead of).
  - `claude-desktop/project-instructions-v4.md` — project-customized Claude Desktop instructions.

## What changed in references

- `CLAUDE.md` gained a `## Workspace layout` section and now references kit modules via `../agency-kit/ai/core/...`.
- `AGENTS.md` gained a `## Workspace layout` section and the same relative path convention.
- `CANONICAL_SOURCE_OF_TRUTH.md` gained a `## Workspace layout` table and a `## Release artifacts` section pointing at `../artifacts/ai-remote-engineering-os/v3.0.1/`.
- `README.md` gained a `## Workspace layout` section.
- `.gitignore` lost the `artifacts/kits/*.zip` rules — no longer needed because the path is gone from this repo.
- A new top-level `AI-Agentic-OS/README.md` documents the workspace layout and sibling rules.

## Verification (passed 2026-05-16)

- `python ../agency-kit/scripts/war_room_lint.py ../agency-kit/` → `ok: true`, exit 0.
- ZIP confirmed at `D:\1_PROJECT\PRIVATE_WORK\AI-Agentic-OS\artifacts\ai-remote-engineering-os\v3.0.1\ai_remote_engineering_os_starter_pack_v3_0_1_dev_ready.zip` (317,581 bytes).
- Zero kit residue in the product repo (verified for `ai/core`, `ai/examples`, `ai/roles/core`, `ai/templates`, `scripts/`, `artifacts/`, base war-room files, and generic claude-desktop files).
- Product repo file count dropped from ~307 → 222 (excluding `docs/superpowers/`: 220).
- All canonical paths in `CANONICAL_SOURCE_OF_TRUTH.md` resolve, including `../agency-kit/` references.

## Known divergence

Running the kit's `war_room_lint.py` against the **product** repo reports `missing_shared_core` and `ok: false`. This is **expected**: the kit assumes self-containment of `ai/core/`, `ai/templates/`, `ai/war-room/`. In the three-sibling model, those live in `../agency-kit/`, not in the product repo. Run the linter against `agency-kit/` for kit-health checks; do not run it against the product repo unless you re-vendor the kit.

## Follow-ups (not done in this session)

1. `git init` for each sibling separately (or for whichever the user wants tracked first).
2. Decide whether `artifacts/` is tracked at all (git-lfs vs ignored vs separate repo).
3. Optional: convert `agency-kit/` to a fork of the canonical kit at `D:\1_PROJECT\PRIVATE_WORK\Agentic Kit\claude-war-room-agency-kit-v4.2` for sync workflows.
4. Optional: add a sync script under `AI-Agentic-OS/` that refreshes `agency-kit/` from the canonical source when the kit ships a new version.
