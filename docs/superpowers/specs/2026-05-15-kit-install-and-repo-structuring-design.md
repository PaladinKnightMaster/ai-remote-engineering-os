# Design — War-Room Kit Install + v3.0.1 Repo Structuring

> **Layout sections superseded** by [2026-05-16-workspace-restructure-addendum.md](2026-05-16-workspace-restructure-addendum.md). The kit + artifacts now live in sibling directories, not inside the product repo. Read the addendum after this doc for the current layout.

- Date: 2026-05-15
- Project: AI Remote Engineering OS (ai-remote-engineering-os)
- Baseline: v3.0.1 (development-ready, approved per `development_readiness_audit_v3_0_1.md`)
- Source kit: `claude-war-room-agency-kit-v4.2` (at `D:\1_PROJECT\PRIVATE_WORK\Agentic Kit\claude-war-room-agency-kit-v4.2`)
- Source spec pack: `ai_remote_engineering_os_starter_pack_v3_0_1_dev_ready.zip` (currently at `artifacts/kits/`)

## Goal

Bootstrap the AI Remote Engineering OS repository so future agent-driven implementation can run safely against the v3.0.1 baseline. Two coupled outcomes:

1. Install a **project-customized war-room agency kit** so Claude Code (and Codex) operate as a disciplined cross-functional team aware of v3.0.1's modules, priority ladder, and security constraints.
2. Lay down a **v3.0.1 monorepo skeleton** with canonical docs unpacked from the starter ZIP, release pointer files, and empty service/package placeholders ready for T-002…T-010 work.

No business logic, no service code, no ADRs authored, no `git init` in this session.

## Non-goals

- No implementation of Remote Cockpit, audit-service, session-broker, or any service body.
- No real Guacamole/MeshCentral integration.
- No new ADRs authored. (Existing 40 ADRs are relocated; new ADR-0001..0010 queue is documented only.)
- No package manager install / dependency resolution.
- No `git init` or commits — performed after skeleton is in place, per user direction.
- No execution of T-002 through T-010.

## Constraints

- **P0 first.** Do not implement P1/P2/P3 unless an explicit ticket + ADR demands it.
- **No SaaS runtime dependency** for the platform itself.
- **No AI autonomy** before audit, policy, and approval primitives exist.
- **Diff budget** per future ticket: ≤12 files / ≤500 LoC unless explicitly approved.
- **Read before write.** Agents must read relevant v3.0.1 docs before modifying related code.

## Final repo layout after this session

```text
ai-remote-engineering-os/
├── README.md                           NEW (project intro + pointers)
├── CANONICAL_SOURCE_OF_TRUTH.md        from v3.0.1 ZIP, paths rewritten to in-repo locations
├── CLAUDE.md                           war-room kit, customized
├── AGENTS.md                           NEW (Codex-facing mirror of CLAUDE.md core)
├── .gitignore                          ignores artifacts/kits/*.zip|*.pdf|*.xlsx
├── apps/{web,admin}/                   placeholders (README + stub package.json only)
├── services/
│   ├── api/
│   ├── session-broker/
│   ├── audit-service/
│   ├── policy-service/
│   └── connector-service/
├── packages/
│   ├── shared-types/                   placeholder
│   ├── shared-schemas/                 ← v3.0.1 06-schemas/ (+ top-level schemas/)
│   ├── config/                         placeholder
│   └── ui/                             placeholder
├── infra/
│   ├── docker-compose/                 ← v3.0.1 skeleton/ (compose + Makefile + .env.example)
│   └── scripts/                        placeholder
├── docs/
│   ├── executive/                      ← v3.0.1 00-executive/
│   ├── product/                        ← v3.0.1 01-product/
│   ├── architecture/                   ← v3.0.1 02-architecture/
│   ├── security/                       ← v3.0.1 03-security/
│   ├── requirements/                   ← v3.0.1 04-requirements/  + requirements/
│   ├── api-contracts/                  ← v3.0.1 05-api-contracts/ + api/
│   ├── ui-ux/                          ← v3.0.1 07-ui-ux/
│   ├── devops/                         ← v3.0.1 08-devops/
│   ├── evaluation/                     ← v3.0.1 09-evaluation/
│   ├── adr/                            ← v3.0.1 adr/ (40 existing ADRs)
│   ├── diagrams/                       ← v3.0.1 diagrams/
│   ├── deep-dive/                      ← v3.0.1 docs/ (35 deep-dive docs incl. v2/v2.2 history)
│   ├── releases/v3.0.1/
│   │   ├── README.md                   NEW (release pointer)
│   │   ├── development_readiness_audit_v3_0_1.md   moved from artifacts/kits/
│   │   ├── release_notes_v3_0_1.md     NEW
│   │   └── manifest_reference.md       NEW
│   └── superpowers/specs/
│       └── 2026-05-15-kit-install-and-repo-structuring-design.md   THIS DOC
├── references/
│   ├── reference_inventory_v3.csv      ← v3.0.1 10-reference-inventory/
│   └── source_watchlist.md             NEW (empty header)
├── ai/                                 ← war-room kit v4.2 (copied + customized)
│   ├── core/                           as-is
│   ├── examples/                       as-is
│   ├── roles/
│   │   ├── core/                       as-is
│   │   └── specialists/                as-is + 6 NEW project specialist cards
│   ├── templates/                      as-is
│   └── war-room/
│       ├── TEAM.md                     customized
│       ├── activation-matrix.md        customized
│       ├── role-router.md              updated
│       └── output-contracts.md         as-is
├── claude-desktop/                     from kit; project-instructions-v4.md customized
├── scripts/                            from kit, as-is
├── artifacts/kits/                     GITIGNORED contents; .gitkeep retained
│   └── .gitkeep
└── .github/ISSUE_TEMPLATE/ticket.md    NEW (T-NNN template aligned with v3.0.1 backlog)
```

### Non-trivial mapping rules

- `99-archive/` from ZIP → NOT committed (legacy ZIP-in-ZIP).
- `audit/` + `11-release/` from ZIP → merged into `docs/releases/v3.0.1/`. The readiness audit at `artifacts/kits/development_readiness_audit_v3_0_1.md` is relocated to the release home; if the ZIP's `audit/` contains a same-named file with identical SHA, keep one copy. If contents differ, keep both and surface the diff in `_audit_dedupe_notes.md`.
- ZIP-root `schemas/` and `06-schemas/` → merged under `packages/shared-schemas/`. If name collisions occur, prefer `06-schemas/` content; record collision list in a sibling `_merge_notes.md`.
- `skeleton/` → `infra/docker-compose/` (preserves docker-compose.yml, Makefile, .env.example, README if present).
- `requirements/` (top-level CSVs) → `docs/requirements/` alongside `04-requirements/`. Both numbered and renamed copies kept only if content differs; otherwise dedupe with `_dedupe_notes.md`.
- `api/` (OpenAPI v2/v3) → `docs/api-contracts/openapi/`.
- `docs/` (top-level deep-dive set in ZIP) → `docs/deep-dive/` (avoid clobbering the new top-level `docs/` namespace).

## War-room kit customization (Moderate)

### CLAUDE.md placeholders (filled, project-specific)

```
- Stack: TBD per ADR-0002 (placeholder: TypeScript/Node + Python tooling)
- Package manager: TBD per ADR-0001 (placeholder: pnpm workspaces)
- Dev / Test / Lint / Build: TBD per ADR-0001
- Current phase: P0 — MVP survival
- Release target: v0.1.0 P0 vertical slice
```

### CLAUDE.md "Project doctrine" block (appended)

- Build P0 first. P1/P2/P3 require an explicit ticket + ADR.
- No SaaS runtime dependency.
- No AI autonomy before audit + policy + approval primitives exist.
- All major dependency choices need an ADR.
- Diff budget: ≤12 files / ≤500 LoC per ticket unless approved.
- Read v3.0.1 `docs/` before changing related code.
- Verification: every spec/ADR-cited claim must resolve to a path in this repo.

### 6 new project specialist role cards (`ai/roles/specialists/`)

1. **remote-cockpit-architect.md** — browser/mobile GUI + terminal access; Guacamole/MeshCentral adapter boundaries; session recording/audit hooks; DMZ/VPN/LAN connector posture.
2. **permission-model-reviewer.md** — capability edges (human-agent, agent-agent, agent-tool, agent-workspace, agent-repo, agent-memory, agent-remote-session); approval flows; rate limits; visibility/audit levels.
3. **audit-evidence-reviewer.md** — append-only audit events; evidence packets; redaction & export rules; tamper-evidence; replay/correlation.
4. **mcp-gateway-reviewer.md** — typed MCP gateway; tool-call governance; egress policy; prompt-injection defense; allow/deny per workstream.
5. **offline-install-engineer.md** — air-gapped install; DMZ/VPN/LAN profiles; docker-compose hardening; image provenance; offline registry mirroring.
6. **workstream-architect.md** — recurring stateful work; memory scoping (user/project/org/ticket/run/skill/workstream); ticket→run lifecycle; evidence binding.

Each role card follows the existing kit format: one-paragraph charter, then bulleted responsibilities. No placeholders, no TBDs.

### Activation matrix additions (`ai/war-room/activation-matrix.md`)

A new section "P0/P1/P2/P3 × module" is appended:

| Priority | Module | Lead role | Consult |
|---|---|---|---|
| P0 | Remote Cockpit | Remote Cockpit Architect | Security Reviewer, Tech Lead |
| P0 | Audit / Evidence | Audit-Evidence Reviewer | Spec Architect, Security Reviewer |
| P0 | Permission Model | Permission Model Reviewer | Security Reviewer, Tech Lead |
| P0 | Offline Install | Offline-Install Engineer | Release Guard, Performance Engineer |
| P1 | MCP Gateway | MCP Gateway Reviewer | Security Reviewer, AI/ML Reviewer |
| P1 | Workstreams | Workstream Architect | PM Strategist, Tech Lead |
| P1 | Memory | Workstream Architect | Security Reviewer, Token Governor |
| P2 | Workflow Compiler | (research only) | Spec Architect |
| P2 | Desktop Automation | (research only) | Security Reviewer |

### role-router.md update

The new specialists are inserted into appropriate groups:
- **Engineering**: Remote Cockpit Architect, MCP Gateway Reviewer, Workstream Architect.
- **Operations**: Offline-Install Engineer.
- **Research/Docs**: (no change).
- **Security/Governance**: Permission Model Reviewer, Audit-Evidence Reviewer.

### AGENTS.md (new, Codex-facing)

Mirrors CLAUDE.md core rules + project doctrine using the AGENTS.md convention:
- Intro: what this project is.
- Setup commands (TBD placeholders pending ADR-0001).
- Test / lint commands (TBD).
- Code style (deferred to per-language conventions; ADR-driven).
- Repo-wide expectations: P0 first, ADR before big changes, diff budget, ticket-driven, no broad network, no destructive ops without approval.
- Forbidden work block.

### Claude Desktop bundle (`claude-desktop/`)

`project-instructions-v4.md` gets a project-specific preamble naming the AI Remote Engineering OS v3.0.1 baseline and pointing at `CANONICAL_SOURCE_OF_TRUTH.md`. Other files (`custom-style.md`, `knowledge-upload-checklist*.md`, `profile-preferences.md`, `session-starters.md`) copied as-is.

## First-10 ticket backlog (seeded as issue template)

`.github/ISSUE_TEMPLATE/ticket.md` is a single template with the full T-001…T-010 list pre-populated and an "Acceptance criteria" rubric. Tickets:

- T-001 Repo skeleton + root documentation (THIS SESSION)
- T-002 Docker Compose for Postgres + Redis/NATS + API + web
- T-003 Shared domain types/schemas (Organization, User, Resource, Session, AuditEvent)
- T-004 audit-service minimal append-only API
- T-005 resource registry CRUD API
- T-006 session-broker skeleton
- T-007 web dashboard shell
- T-008 auth placeholder adapter (Keycloak/AuthentiK boundary)
- T-009 OpenAPI spec + generated client types
- T-010 CI checks (lint, typecheck, test, schema validation)

T-001 is executed during this session.

## ADR queue (queued, NOT authored)

ADR-0001 monorepo tooling · 0002 backend language · 0003 API framework · 0004 DB migration tool · 0005 event bus · 0006 auth adapter · 0007 frontend framework · 0008 package manager · 0009 CI provider · 0010 Docker strategy.

The v3.0.1 ZIP already contains 40 ADRs under `adr/`. The new ADR numbering will start at the next free integer after relocation; the executor verifies the next free number by inspecting `docs/adr/` before writing.

## Release pointer files (`docs/releases/v3.0.1/`)

- `README.md` — uses the template in the user's planning text: status, source-of-truth pointers, release-asset expectations, development rule (P0 first).
- `development_readiness_audit_v3_0_1.md` — moved from `artifacts/kits/` (already exists).
- `release_notes_v3_0_1.md` — concise: what shipped in v3.0.1 vs v3.0.0 (manifest fix + archived earlier markdown package per the audit doc).
- `manifest_reference.md` — lists expected release asset filenames + the in-ZIP SHA256 manifest path.

## .gitignore

```
# v3.0.1 release artifacts kept local; published as release assets
artifacts/kits/*.zip
artifacts/kits/*.pdf
artifacts/kits/*.xlsx
!artifacts/kits/.gitkeep

# Standard ignores deferred to ADR-0001 (language/toolchain ADR)
# Until then, only the artifact rules above are committed.
```

Standard language-specific ignores (`node_modules/`, `dist/`, `.venv/`, etc.) are deferred until ADR-0001 decides the toolchain, then added in a follow-up ticket. This avoids committing assumptions.

## Verification

- Tree listing matches the layout above (manual diff against this spec).
- `python ./scripts/war_room_lint.py` exits clean.
- Every path referenced in `CANONICAL_SOURCE_OF_TRUTH.md` resolves on disk (script-checkable).
- `artifacts/kits/.gitkeep` exists; ZIP/PDF/XLSX still present locally but listed in `.gitignore`.
- All 6 new specialist role cards render and are referenced from updated `role-router.md` + `activation-matrix.md`.
- `CLAUDE.md` contains no remaining empty placeholders (all filled or marked TBD-per-ADR-NNNN).
- T-001 acceptance: skeleton created, root docs in place, no business logic, summary of changed files provided.

## Risks and mitigations

| Risk | Mitigation |
|---|---|
| Unpack collisions between `schemas/` and `06-schemas/` | Record collisions in `_merge_notes.md` next to the merged content; surface in execution summary. |
| ZIP contents drift from CANONICAL_SOURCE_OF_TRUTH.md path references | Rewrite paths in the in-repo `CANONICAL_SOURCE_OF_TRUTH.md` to match the actual unpacked locations; preserve the original under `docs/releases/v3.0.1/manifest_reference.md`. |
| Existing 40 ADRs may already cover queued ADR-0001..0010 topics | Executor scans `docs/adr/` titles before drafting new ADRs; reuses/extends existing where applicable. |
| Numbered v3 folder convention (e.g. `00-executive`) vs new flat names | Decided: flatten to `docs/{executive,product,architecture,...}/`. Numbered ordering preserved in `CANONICAL_SOURCE_OF_TRUTH.md` reading order. |
| Top-level `docs/` in ZIP collides with new top-level `docs/` namespace | ZIP's top-level `docs/` content moved to `docs/deep-dive/`. |
| Project not currently a git repo | Skeleton is filesystem-only this session; `.gitignore` is staged but not enforced until `git init`. User initializes git after skeleton is verified. |

## Execution order (for writing-plans skill)

1. Extract v3.0.1 ZIP to a temp directory.
2. Create top-level monorepo folders + placeholder READMEs.
3. Copy war-room kit (`ai/`, `claude-desktop/`, `scripts/`, kit `CLAUDE.md` as base).
4. Customize `CLAUDE.md` placeholders + append project doctrine.
5. Write 6 new specialist role cards.
6. Update `ai/war-room/{TEAM,activation-matrix,role-router}.md`.
7. Customize `claude-desktop/project-instructions-v4.md` preamble.
8. Author `AGENTS.md`.
9. Map ZIP content into target locations per the "Non-trivial mapping rules".
10. Author release pointer files in `docs/releases/v3.0.1/`.
11. Rewrite in-repo `CANONICAL_SOURCE_OF_TRUTH.md` paths.
12. Write `.gitignore`, `.github/ISSUE_TEMPLATE/ticket.md`, `references/source_watchlist.md`, root `README.md`.
13. Verify: run `war_room_lint.py`, resolve all canonical paths, list any merge collisions.
14. Surface git-init readiness question to the user.

## Open assumption

The env header reports the project is not a git repo. This spec assumes **no `git init`** is performed during execution. After the skeleton is verified, the user decides on remote, default branch, and commit-signing policy before history starts.
