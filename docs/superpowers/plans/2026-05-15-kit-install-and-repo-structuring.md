# War-Room Kit Install + v3.0.1 Repo Structuring — Implementation Plan

> **Status: executed 2026-05-15, then partially superseded 2026-05-16.** Tasks 3 (kit copy into product repo) and parts of Task 2 (artifacts/kits inside product repo) have been undone by the workspace restructure — see [2026-05-16-workspace-restructure-addendum.md](../specs/2026-05-16-workspace-restructure-addendum.md). The 6 project specialists, overlays, CLAUDE.md / AGENTS.md / CANONICAL contents, and the v3.0.1 doc unpacking remain valid.

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Bootstrap the AI Remote Engineering OS repo with a project-customized Claude war-room agency kit and the v3.0.1 monorepo skeleton (canonical docs unpacked, release pointers in place, no business logic).

**Architecture:** Filesystem-only setup. Extract v3.0.1 starter pack ZIP to a staging dir, copy war-room kit v4.2 files into the repo root, customize CLAUDE.md / AGENTS.md / specialist role cards / activation matrix for v3.0.1 modules, then map ZIP content into proper monorepo locations. No git init, no service code, no ADRs authored.

**Tech Stack:** Windows PowerShell 5.1 (host shell), Python 3 (for the kit's `war_room_lint.py`). No language toolchain decisions made yet — those are deferred to ADR-0001/0002.

**Source spec:** [`docs/superpowers/specs/2026-05-15-kit-install-and-repo-structuring-design.md`](../specs/2026-05-15-kit-install-and-repo-structuring-design.md)

**Key paths:**
- Repo root: `D:\1_PROJECT\PRIVATE_WORK\AI-Agentic-OS\ai-remote-engineering-os`
- War-room kit source: `D:\1_PROJECT\PRIVATE_WORK\Agentic Kit\claude-war-room-agency-kit-v4.2`
- ZIP source: `D:\1_PROJECT\PRIVATE_WORK\AI-Agentic-OS\ai-remote-engineering-os\artifacts\kits\ai_remote_engineering_os_starter_pack_v3_0_1_dev_ready.zip`
- Staging dir for ZIP extract: `D:\Temp\v3_0_1_stage`

**Commit policy:** This repo is not yet a git repo. Each task ends with a logical "checkpoint" boundary. After all tasks complete and verification passes, the user decides whether to `git init` and how to chunk the initial commits.

---

## Task 1: Extract v3.0.1 ZIP to staging directory

**Files:**
- Create: `D:\Temp\v3_0_1_stage\` (staging, outside the repo)

- [ ] **Step 1: Create staging dir and extract**

Run in PowerShell:
```powershell
$stage = 'D:\Temp\v3_0_1_stage'
if (Test-Path $stage) { Remove-Item $stage -Recurse -Force }
New-Item -ItemType Directory -Path $stage | Out-Null
$zip = 'D:\1_PROJECT\PRIVATE_WORK\AI-Agentic-OS\ai-remote-engineering-os\artifacts\kits\ai_remote_engineering_os_starter_pack_v3_0_1_dev_ready.zip'
Expand-Archive -Path $zip -DestinationPath $stage -Force
```

Expected: no error.

- [ ] **Step 2: Locate the canonical root inside the staging dir**

Run:
```powershell
Get-ChildItem $stage -Recurse -Depth 2 -Directory | Select-Object -First 20 | ForEach-Object { $_.FullName }
```

Expected: a folder like `D:\Temp\v3_0_1_stage\v3_0_1_build\ai_remote_engineering_os_starter_pack_v3_0_1` containing `00-executive`, `01-product`, `02-architecture`, etc. Record this path as `$canon` for later steps.

- [ ] **Step 3: Set $canon and verify expected subfolders**

```powershell
# Discover canonical root automatically (deepest dir containing 00-executive)
$canon = (Get-ChildItem $stage -Recurse -Directory -Filter '00-executive' | Select-Object -First 1).Parent.FullName
Write-Host "canon = $canon"
$expected = @('00-executive','01-product','02-architecture','03-security','04-requirements','05-api-contracts','06-schemas','07-ui-ux','08-devops','09-evaluation','10-reference-inventory','11-release','adr','api','audit','diagrams','docs','requirements','schemas','skeleton','99-archive')
$missing = $expected | Where-Object { -not (Test-Path (Join-Path $canon $_)) }
if ($missing) { Write-Host "MISSING: $missing" -ForegroundColor Red } else { Write-Host "All expected subfolders present." -ForegroundColor Green }
```

Expected: `All expected subfolders present.`

**Checkpoint:** staging area ready. Persist `$canon` value for the rest of the session (or recompute via the same discovery snippet).

---

## Task 2: Create top-level monorepo skeleton folders

**Files:**
- Create: `apps/web/`, `apps/admin/`, `services/{api,session-broker,audit-service,policy-service,connector-service}/`, `packages/{shared-types,shared-schemas,config,ui}/`, `infra/{docker-compose,scripts}/`, `references/`, `.github/ISSUE_TEMPLATE/`, `docs/superpowers/plans/` (already exists), `artifacts/kits/.gitkeep`

- [ ] **Step 1: Create all empty directories with `.gitkeep` placeholders**

```powershell
$repo = 'D:\1_PROJECT\PRIVATE_WORK\AI-Agentic-OS\ai-remote-engineering-os'
$dirs = @(
  'apps\web','apps\admin',
  'services\api','services\session-broker','services\audit-service','services\policy-service','services\connector-service',
  'packages\shared-types','packages\shared-schemas','packages\config','packages\ui',
  'infra\docker-compose','infra\scripts',
  'references',
  '.github\ISSUE_TEMPLATE',
  'docs\releases\v3.0.1'
)
foreach ($d in $dirs) {
  $p = Join-Path $repo $d
  if (-not (Test-Path $p)) { New-Item -ItemType Directory -Path $p -Force | Out-Null }
  # Add .gitkeep for empty service/app/package dirs only
  if ($d -match '^(apps|services|packages)\\' -or $d -in @('infra\scripts','references','.github\ISSUE_TEMPLATE')) {
    $kp = Join-Path $p '.gitkeep'
    if (-not (Test-Path $kp)) { New-Item -ItemType File -Path $kp | Out-Null }
  }
}
# artifacts/kits already exists; ensure .gitkeep
$ak = Join-Path $repo 'artifacts\kits\.gitkeep'
if (-not (Test-Path $ak)) { New-Item -ItemType File -Path $ak | Out-Null }
```

- [ ] **Step 2: Verify directory tree**

```powershell
Get-ChildItem $repo -Directory | Select-Object -ExpandProperty Name
```

Expected output (order may vary):
```
.github
ai-remote-engineering-os    # (only if running from parent; ignore)
apps
artifacts
docs
infra
packages
references
services
```

If running directly inside `$repo`, the `ai-remote-engineering-os` line will not appear.

- [ ] **Step 3: Drop placeholder README in each service / app / package dir**

```powershell
$placeholders = @{
  'apps\web' = 'Web app shell (P0). Stack TBD per ADR-0002/0007.'
  'apps\admin' = 'Admin app (P1+). Do not implement before P0 ships.'
  'services\api' = 'API service shell (P0). Stack TBD per ADR-0002/0003.'
  'services\session-broker' = 'Session broker (P0). See docs/architecture/module-map.md.'
  'services\audit-service' = 'Append-only audit/event service (P0). See docs/security/.'
  'services\policy-service' = 'Policy engine (P0). Capability-edge enforcement.'
  'services\connector-service' = 'Connector service for Guacamole/MeshCentral adapters (P0).'
  'packages\shared-types' = 'Shared domain types (Organization, User, Resource, Session, AuditEvent). Stack TBD.'
  'packages\shared-schemas' = 'JSON schemas from v3.0.1 (populated by Task 9).'
  'packages\config' = 'Shared config. Stack TBD.'
  'packages\ui' = 'Shared UI components. Stack TBD per ADR-0007.'
  'infra\docker-compose' = 'Docker Compose dev environment from v3.0.1 skeleton/ (populated by Task 9).'
  'infra\scripts' = 'Infrastructure scripts. Empty for now.'
  'references' = 'External references. See reference_inventory_v3.csv (populated by Task 9).'
}
foreach ($k in $placeholders.Keys) {
  $r = Join-Path $repo "$k\README.md"
  if (-not (Test-Path $r)) {
    Set-Content -Path $r -Value "# $($k.Split('\')[-1])`r`n`r`n$($placeholders[$k])`r`n" -Encoding utf8
  }
}
```

- [ ] **Step 4: Verify placeholder READMEs**

```powershell
Get-ChildItem $repo -Recurse -Filter README.md | Select-Object FullName, Length
```

Expected: ~14 README.md files (one per placeholder dir) plus this plan dir's eventual ones.

**Checkpoint:** monorepo skeleton folders live.

---

## Task 3: Copy war-room kit (ai/, claude-desktop/, scripts/, base CLAUDE.md)

**Files:**
- Create: `ai/` (full tree from kit), `claude-desktop/`, `scripts/`, `CLAUDE.md` (kit's version as base)

- [ ] **Step 1: Copy kit folders verbatim**

```powershell
$repo = 'D:\1_PROJECT\PRIVATE_WORK\AI-Agentic-OS\ai-remote-engineering-os'
$kit  = 'D:\1_PROJECT\PRIVATE_WORK\Agentic Kit\claude-war-room-agency-kit-v4.2'
Copy-Item -Path (Join-Path $kit 'ai')             -Destination $repo -Recurse -Force
Copy-Item -Path (Join-Path $kit 'claude-desktop') -Destination $repo -Recurse -Force
Copy-Item -Path (Join-Path $kit 'scripts')        -Destination $repo -Recurse -Force
Copy-Item -Path (Join-Path $kit 'CLAUDE.md')      -Destination $repo -Force
```

- [ ] **Step 2: Verify kit copy**

```powershell
@('ai\core\war-room-doctrine.md','ai\roles\core\commander.md','ai\war-room\TEAM.md','ai\templates\PRD.md','claude-desktop\project-instructions-v4.md','scripts\war_room_lint.py','CLAUDE.md') |
  ForEach-Object {
    $p = Join-Path $repo $_
    "{0}  {1}" -f ($(if (Test-Path $p) {'OK'} else {'MISSING'})), $_
  }
```

Expected: all `OK`.

**Checkpoint:** raw war-room kit in place.

---

## Task 4: Customize root CLAUDE.md (placeholders + project doctrine)

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Replace CLAUDE.md with the customized version**

Overwrite `CLAUDE.md` with this content (use Write tool, full file):

```markdown
# CLAUDE.md — AI Remote Engineering OS

These rules apply to this project unless explicitly overridden. This project follows v3.0.1 of the AI Remote Engineering OS kit.

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

## Project placeholders
- Stack: TBD per ADR-0002 (placeholder TypeScript/Node + Python tooling)
- Package manager: TBD per ADR-0001 (placeholder pnpm workspaces)
- Dev: TBD per ADR-0001
- Test: TBD per ADR-0001
- Lint: TBD per ADR-0001
- Build: TBD per ADR-0001
- Current phase: P0 — MVP survival
- Release target: v0.1.0 P0 vertical slice

## Project doctrine
- Build P0 first. P1/P2/P3 features require an explicit ticket and an approved ADR.
- No SaaS runtime dependency.
- No AI autonomy before audit + policy + approval primitives exist.
- All major dependency choices need an ADR.
- Diff budget: ≤12 files / ≤500 LoC per ticket unless explicitly approved.
- Read v3.0.1 `docs/` before changing related code (start at `docs/executive/`, `docs/architecture/module-map.md`, `docs/security/agent-permission-model.md`).
- Every spec / ADR / PR claim must resolve to a path in this repo.

## Definition of done
Relevant specs/docs updated; tests/evals run or gaps stated; UI/accessibility reviewed when applicable; risks and next actions stated.

## v4 Modules
Use these modules when relevant:
- `ai/core/grounded-code.md`
- `ai/core/agent-reader-architecture.md`
- `ai/core/design-system-governance.md`
- `ai/core/context-checkpoint-protocol.md`
- `ai/core/sandbox-permission-matrix.md`

## UI work rule
Before creating or modifying UI, run a design-system preflight: identify existing tokens, components, variants, states, accessibility rules, and approval-required changes.

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
- `git init` or commits without explicit user direction.
```

- [ ] **Step 2: Verify CLAUDE.md has no empty placeholders**

```powershell
Select-String -Path (Join-Path $repo 'CLAUDE.md') -Pattern '^- \w+:\s*$' -AllMatches
```

Expected: no matches (every `- Key:` line has a value or `TBD per ADR-NNNN`).

**Checkpoint:** root CLAUDE.md customized.

---

## Task 5: Write 6 new project specialist role cards

**Files:**
- Create: `ai/roles/specialists/remote-cockpit-architect.md`
- Create: `ai/roles/specialists/permission-model-reviewer.md`
- Create: `ai/roles/specialists/audit-evidence-reviewer.md`
- Create: `ai/roles/specialists/mcp-gateway-reviewer.md`
- Create: `ai/roles/specialists/offline-install-engineer.md`
- Create: `ai/roles/specialists/workstream-architect.md`

- [ ] **Step 1: Write `remote-cockpit-architect.md`**

```markdown
# Remote Cockpit Architect

Owns the Remote Execution Cockpit (P0): browser/mobile GUI + terminal access to internal resources via Guacamole and MeshCentral adapter boundaries.

Responsibilities:
- Define adapter contracts for Guacamole (RDP/VNC/SSH gateway) and MeshCentral (remote desktop). Adapters never leak provider-specific types past the contract.
- Specify session lifecycle: request → policy check → broker provision → connect → record → terminate.
- Hook every session into the audit/evidence stream with the agent-remote-session capability edge.
- Enforce DMZ/VPN/LAN connector posture. No public internet exposure of internal resources.
- Govern recording, replay, and redaction policy for session artifacts.
- Set mobile/responsive UX constraints for GUI access.

Activates: Remote Cockpit work (T-006 session-broker, T-005 resource registry, T-007 web dashboard shell), DMZ/VPN/LAN deployment changes, any Guacamole/MeshCentral integration.
```

- [ ] **Step 2: Write `permission-model-reviewer.md`**

```markdown
# Permission Model Reviewer

Owns the capability-edge permission model (P0). Every cross-actor interaction is a typed edge with allowed operations, approvals, rate limits, visibility, and audit level.

Edge types: human-agent, agent-agent, agent-tool, agent-workspace, agent-repo, agent-memory, agent-remote-session.

Responsibilities:
- Verify every new feature introduces or extends edges explicitly, never implicitly.
- Block features that bypass approval flows for high-risk operations.
- Confirm rate limits and visibility settings match the threat model in `docs/security/`.
- Ensure approval requests and egress requests are auditable and human-replayable.
- Block AI-autonomy expansion until edges + approvals + audit cover the new surface.

Activates: any change touching auth, permissions, approvals, agent capabilities, tool registration, MCP gateway routes, or session brokering.
```

- [ ] **Step 3: Write `audit-evidence-reviewer.md`**

```markdown
# Audit / Evidence Reviewer

Owns append-only audit events (P0) and the evidence packet contract that proves an output before it is marked Done.

Responsibilities:
- Enforce append-only semantics. No in-place edits, no deletions, only redaction tombstones.
- Specify required fields for every event in the 18-event catalog: timestamp, actor, edge, target, outcome, evidence-ref.
- Define evidence packet shape: inputs, tool calls, approvals, outputs, hashes.
- Verify tamper-evidence (chained hashes or equivalent) for audit streams.
- Specify replay/correlation paths for incident response.
- Govern export and redaction rules. Redactions never destroy the underlying event row — they overlay tombstones.

Activates: T-003 shared types (AuditEvent), T-004 audit-service, any new domain event, any change to evidence packet shape, any incident response workflow.
```

- [ ] **Step 4: Write `mcp-gateway-reviewer.md`**

```markdown
# MCP Gateway Reviewer

Owns the typed MCP gateway (P1) that governs every tool call an agent makes.

Responsibilities:
- Verify each tool call has a declared schema (input + output) and a permission edge.
- Enforce egress policy: which tools may reach which networks, per workstream.
- Defend against prompt injection in tool inputs/outputs via filtering, schema validation, and scoped capabilities.
- Approve/deny per workstream. Default-deny for unknown tools.
- Confirm tool-call telemetry feeds the audit stream with the same evidence rules as session events.
- Block tool integrations that lack typed schemas, audit hooks, or rate limits.

Activates: any MCP-related work, new tool registration, egress policy change, agent-tool edge change, AI/ML integration touching external services.
```

- [ ] **Step 5: Write `offline-install-engineer.md`**

```markdown
# Offline-Install Engineer

Owns the air-gapped install path (P0) and the DMZ/VPN/LAN deployment profiles.

Responsibilities:
- Specify offline image bundles: every container, base image, and dependency mirrored locally with a provenance manifest.
- Define DMZ/VPN/LAN profiles: which services run where, what the trust boundaries are.
- Harden docker-compose for production: non-root, read-only FS where possible, secrets via env-file or external mounts, no privileged ports unless audited.
- Document offline registry mirroring and update flow.
- Verify the install path requires no SaaS runtime call.
- Provide a release-readiness checklist row for "offline install passes on a freshly-imaged host."

Activates: T-002 docker-compose, any infrastructure change, dependency addition, registry/mirror work, release readiness gate.
```

- [ ] **Step 6: Write `workstream-architect.md`**

```markdown
# Workstream Architect

Owns recurring stateful work (P1): long-lived objectives with sources, rules, schedule, scoped memory, and evidence-bound runs.

Responsibilities:
- Specify the Workstream schema: objective, sources, rules, memory scope, schedule, owner.
- Define the ticket → context-pack → run → evidence-packet → done lifecycle.
- Choose memory scope per use case (user/project/org/ticket/run/skill/workstream). Enforce expiry, export, redact, audit per scope.
- Ensure runs are reproducible: same inputs + same workstream rules + same memory state produce the same plan shape.
- Block workstreams that accumulate memory without expiry or audit policy.
- Coordinate with PM Strategist on which workstreams are P0 vs deferred.

Activates: any Workstream / Memory work, ticket lifecycle change, evidence packet schema change, P1 prioritization decisions.
```

- [ ] **Step 7: Verify all 6 role cards exist**

```powershell
$specialists = @('remote-cockpit-architect','permission-model-reviewer','audit-evidence-reviewer','mcp-gateway-reviewer','offline-install-engineer','workstream-architect')
$specialists | ForEach-Object {
  $p = Join-Path $repo "ai\roles\specialists\$_.md"
  "{0}  {1}" -f ($(if (Test-Path $p) {'OK'} else {'MISSING'})), $_
}
```

Expected: all `OK`.

**Checkpoint:** 6 new specialist role cards live.

---

## Task 6: Update TEAM.md, activation-matrix.md, role-router.md

**Files:**
- Modify: `ai/war-room/TEAM.md`
- Modify: `ai/war-room/activation-matrix.md`
- Modify: `ai/war-room/role-router.md`

- [ ] **Step 1: Append project specialists to `TEAM.md`**

Append (Edit tool) to the end of `ai/war-room/TEAM.md`:

```markdown

## Project specialists (AI Remote Engineering OS v3.0.1)

P0 lead roles:
- Remote Cockpit Architect — Remote Execution Cockpit, session broker, connector adapters.
- Permission Model Reviewer — capability edges, approvals, rate limits.
- Audit / Evidence Reviewer — append-only events, evidence packets, redaction.
- Offline-Install Engineer — air-gapped install, DMZ/VPN/LAN profiles, docker-compose hardening.

P1 lead roles:
- MCP Gateway Reviewer — typed MCP gateway, egress policy, prompt-injection defense.
- Workstream Architect — recurring stateful work, scoped memory, ticket→run lifecycle.

These specialists must be consulted (Consult or War Room mode) on any change touching their domain.
```

- [ ] **Step 2: Append project activation matrix to `activation-matrix.md`**

Append to the end of `ai/war-room/activation-matrix.md`:

```markdown

## Project activation (AI Remote Engineering OS v3.0.1)

| Priority | Module | Lead role | Consult |
|---|---|---|---|
| P0 | Remote Cockpit | Remote Cockpit Architect | Security Reviewer, Tech Lead |
| P0 | Audit / Evidence | Audit / Evidence Reviewer | Spec Architect, Security Reviewer |
| P0 | Permission Model | Permission Model Reviewer | Security Reviewer, Tech Lead |
| P0 | Offline Install | Offline-Install Engineer | Release Guard, Performance Engineer |
| P1 | MCP Gateway | MCP Gateway Reviewer | Security Reviewer, AI/ML Reviewer |
| P1 | Workstreams | Workstream Architect | PM Strategist, Tech Lead |
| P1 | Memory | Workstream Architect | Security Reviewer, Token Governor |
| P2 | Workflow Compiler | (research only) | Spec Architect |
| P2 | Desktop Automation | (research only) | Security Reviewer |

Priority-ladder rule: do not activate P1/P2/P3 implementation work unless an explicit ticket exists in `.github/ISSUE_TEMPLATE/ticket.md` form and the ADR queue covers the decision.
```

- [ ] **Step 3: Append project roles to `role-router.md`**

Append to the end of `ai/war-room/role-router.md`:

```markdown

## Project routing additions (AI Remote Engineering OS v3.0.1)

Engineering additions: Remote Cockpit Architect, MCP Gateway Reviewer, Workstream Architect.
Operations additions: Offline-Install Engineer.
Security/Governance additions: Permission Model Reviewer, Audit / Evidence Reviewer.

Escalation rule: any change touching `services/audit-service`, `services/policy-service`, `services/session-broker`, or `docs/security/` requires at least one of (Permission Model Reviewer, Audit / Evidence Reviewer, Security Reviewer) in Consult mode.
```

- [ ] **Step 4: Verify appends took**

```powershell
Select-String -Path (Join-Path $repo 'ai\war-room\TEAM.md') -Pattern 'Project specialists \(AI Remote Engineering OS'
Select-String -Path (Join-Path $repo 'ai\war-room\activation-matrix.md') -Pattern 'Project activation \(AI Remote Engineering OS'
Select-String -Path (Join-Path $repo 'ai\war-room\role-router.md') -Pattern 'Project routing additions \(AI Remote Engineering OS'
```

Expected: one match per file.

**Checkpoint:** war-room metadata customized.

---

## Task 7: Customize claude-desktop/project-instructions-v4.md

**Files:**
- Modify: `claude-desktop/project-instructions-v4.md`

- [ ] **Step 1: Prepend project preamble**

Replace `claude-desktop/project-instructions-v4.md` with this content (use Write tool, full file):

```markdown
# Claude Desktop Project Instructions — AI Remote Engineering OS v3.0.1

## Project context
You are the War Room Center for the AI Remote Engineering OS project, v3.0.1 baseline. Self-hosted AI Engineering OS for restricted networks. The user is CEO / owner.

Read `CANONICAL_SOURCE_OF_TRUTH.md` and `docs/architecture/module-map.md` before any substantive question.

## Operating model
Speak as one Commander voice. Use Solo, Consult, or War Room mode depending on task complexity. Do not simulate a committee transcript.

For new ideas, produce research report, project brief, PRD outline, ADR candidates, and MVP plan before development.
For UI work, run design-system preflight and enforce token/component governance.
For implementation planning, prefer Grounded Code: feature-local specs close to the code.
For long sessions, create checkpoints and summarize decisions.

## Priority ladder
P0 — MVP survival (Remote Cockpit, Audit, Permission Model, Offline Install).
P1 — defensibility (MCP Gateway, Workstreams, Memory).
P2 — research / spike.
P3 — optional delight.

Default rule: build P0 first. P1/P2/P3 features require an explicit ticket and an approved ADR.

## Non-negotiables
- No SaaS runtime dependency.
- No AI autonomy before audit + policy + approval primitives exist.
- All major dependency choices need an ADR.
- Read v3.0.1 docs before changing related code.
- Every claim must resolve to a path in this repo.
```

**Checkpoint:** Claude Desktop bundle customized.

---

## Task 8: Author AGENTS.md (Codex-facing)

**Files:**
- Create: `AGENTS.md`

- [ ] **Step 1: Write `AGENTS.md`**

Create `AGENTS.md` with this content (use Write tool):

```markdown
# AGENTS.md — AI Remote Engineering OS

This project follows v3.0.1 of the AI Remote Engineering OS kit. Companion file for non-Claude agents (Codex, etc.). Claude Code uses `CLAUDE.md`.

Key sources of truth:
- `CANONICAL_SOURCE_OF_TRUTH.md`
- `docs/releases/v3.0.1/README.md`
- `docs/architecture/module-map.md`
- `docs/security/agent-permission-model.md`

## What this project is

A self-hosted AI Engineering Operating System for restricted networks. Combines remote execution, human/AI work boards, agentic desktops, living workstreams, governed memory, code intelligence, tool gateways, and evidence-driven review.

P0 modules: Remote Execution Cockpit, Audit Service, Permission Model, Offline Install.
P1+ modules are blocked until P0 ships.

## Setup commands

TBD per ADR-0001 (monorepo tooling) and ADR-0002 (backend language). The skeleton intentionally does not commit a package manager lockfile until those ADRs are approved.

## Test / Lint / Build

TBD per ADR-0001. Until then, only `python ./scripts/war_room_lint.py` (kit's own linter) is expected to pass.

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

## Forbidden work (bootstrap phase)

- Adding business logic before T-002 (Docker Compose) and T-003 (shared types) are complete.
- Touching `services/`, `apps/`, or `packages/shared-types/` with non-scaffolding code in this bootstrap phase.
- Introducing network-egress code paths before the egress policy in `docs/security/` is implemented.
- Modifying existing ADRs under `docs/adr/` — propose a new ADR instead.
- `git init` or destructive git ops without explicit user direction.

## Verification

Every claim in a PR description must resolve to a path in this repo (spec, ADR, schema, or test). PRs that fail this check are returned.

## Ticket format

See `.github/ISSUE_TEMPLATE/ticket.md`. Tickets are T-NNN, aligned with the v3.0.1 backlog under `docs/requirements/`.
```

**Checkpoint:** AGENTS.md authored.

---

## Task 9: Map v3.0.1 ZIP content into repo locations

This is the largest task. Each step is a discrete content move + verification.

**Files:**
- Create / populate: `docs/executive/`, `docs/product/`, `docs/architecture/`, `docs/security/`, `docs/requirements/`, `docs/api-contracts/`, `docs/ui-ux/`, `docs/devops/`, `docs/evaluation/`, `docs/adr/`, `docs/diagrams/`, `docs/deep-dive/`, `packages/shared-schemas/`, `infra/docker-compose/`, `references/reference_inventory_v3.csv`

- [ ] **Step 1: Recompute `$canon` and `$repo`**

```powershell
$repo = 'D:\1_PROJECT\PRIVATE_WORK\AI-Agentic-OS\ai-remote-engineering-os'
$stage = 'D:\Temp\v3_0_1_stage'
$canon = (Get-ChildItem $stage -Recurse -Directory -Filter '00-executive' | Select-Object -First 1).Parent.FullName
Write-Host "repo  = $repo"
Write-Host "canon = $canon"
```

Expected: both paths resolve.

- [ ] **Step 2: Map numbered doc folders → `docs/<name>/`**

```powershell
$map = @{
  '00-executive'         = 'docs\executive'
  '01-product'           = 'docs\product'
  '02-architecture'      = 'docs\architecture'
  '03-security'          = 'docs\security'
  '04-requirements'      = 'docs\requirements'
  '05-api-contracts'     = 'docs\api-contracts'
  '07-ui-ux'             = 'docs\ui-ux'
  '08-devops'            = 'docs\devops'
  '09-evaluation'        = 'docs\evaluation'
  'adr'                  = 'docs\adr'
  'diagrams'             = 'docs\diagrams'
}
foreach ($src in $map.Keys) {
  $s = Join-Path $canon $src
  $d = Join-Path $repo  $map[$src]
  if (Test-Path $s) {
    if (-not (Test-Path $d)) { New-Item -ItemType Directory -Path $d -Force | Out-Null }
    Copy-Item -Path (Join-Path $s '*') -Destination $d -Recurse -Force
    Write-Host "Copied $src -> $($map[$src])"
  } else {
    Write-Host "SKIP (missing in canon): $src" -ForegroundColor Yellow
  }
}
```

Expected: 11 `Copied ...` lines.

- [ ] **Step 3: Map ZIP's top-level `docs/` → `docs/deep-dive/`**

```powershell
$src = Join-Path $canon 'docs'
$dst = Join-Path $repo  'docs\deep-dive'
if (Test-Path $src) {
  New-Item -ItemType Directory -Path $dst -Force | Out-Null
  Copy-Item -Path (Join-Path $src '*') -Destination $dst -Recurse -Force
  Write-Host "Copied docs/ -> docs/deep-dive/"
}
```

- [ ] **Step 4: Merge `06-schemas/` and top-level `schemas/` → `packages/shared-schemas/`**

```powershell
$dst = Join-Path $repo 'packages\shared-schemas'
$collisions = @()
foreach ($srcName in @('06-schemas','schemas')) {
  $s = Join-Path $canon $srcName
  if (Test-Path $s) {
    Get-ChildItem -Path $s -Recurse -File | ForEach-Object {
      $rel = $_.FullName.Substring($s.Length).TrimStart('\')
      $target = Join-Path $dst $rel
      $targetDir = Split-Path $target -Parent
      if (-not (Test-Path $targetDir)) { New-Item -ItemType Directory -Path $targetDir -Force | Out-Null }
      if (Test-Path $target) {
        # Compare hashes
        $h1 = (Get-FileHash $_.FullName -Algorithm SHA256).Hash
        $h2 = (Get-FileHash $target     -Algorithm SHA256).Hash
        if ($h1 -ne $h2) { $collisions += "$srcName/$rel" }
      } else {
        Copy-Item $_.FullName $target -Force
      }
    }
  }
}
if ($collisions.Count -gt 0) {
  $note = "# Schema merge collisions`r`n`r`nFiles below differed between ZIP's `06-schemas/` and top-level `schemas/`. The earlier-copied version (06-schemas) was kept. Review and resolve.`r`n`r`n" + ($collisions -join "`r`n")
  Set-Content -Path (Join-Path $dst '_merge_notes.md') -Value $note -Encoding utf8
  Write-Host "Collisions recorded: $($collisions.Count)" -ForegroundColor Yellow
} else {
  Write-Host "Schemas merged cleanly." -ForegroundColor Green
}
```

- [ ] **Step 5: Merge ZIP's top-level `requirements/` into `docs/requirements/`**

```powershell
$s = Join-Path $canon 'requirements'
$d = Join-Path $repo  'docs\requirements'
$collisions = @()
if (Test-Path $s) {
  Get-ChildItem -Path $s -Recurse -File | ForEach-Object {
    $rel = $_.FullName.Substring($s.Length).TrimStart('\')
    $target = Join-Path $d $rel
    $targetDir = Split-Path $target -Parent
    if (-not (Test-Path $targetDir)) { New-Item -ItemType Directory -Path $targetDir -Force | Out-Null }
    if (Test-Path $target) {
      $h1 = (Get-FileHash $_.FullName -Algorithm SHA256).Hash
      $h2 = (Get-FileHash $target     -Algorithm SHA256).Hash
      if ($h1 -ne $h2) { $collisions += "requirements/$rel" }
    } else {
      Copy-Item $_.FullName $target -Force
    }
  }
}
if ($collisions.Count -gt 0) {
  $note = "# Requirements merge collisions`r`n`r`nFiles below differed between ZIP's `04-requirements/` and top-level `requirements/`. The 04-requirements version was kept. Review and resolve.`r`n`r`n" + ($collisions -join "`r`n")
  Set-Content -Path (Join-Path $d '_dedupe_notes.md') -Value $note -Encoding utf8
}
Write-Host "Requirements merged. Collisions: $($collisions.Count)"
```

- [ ] **Step 6: Map ZIP's `api/` → `docs/api-contracts/openapi/`**

```powershell
$s = Join-Path $canon 'api'
$d = Join-Path $repo  'docs\api-contracts\openapi'
if (Test-Path $s) {
  New-Item -ItemType Directory -Path $d -Force | Out-Null
  Copy-Item -Path (Join-Path $s '*') -Destination $d -Recurse -Force
  Write-Host "Copied api/ -> docs/api-contracts/openapi/"
}
```

- [ ] **Step 7: Map `skeleton/` → `infra/docker-compose/`**

```powershell
$s = Join-Path $canon 'skeleton'
$d = Join-Path $repo  'infra\docker-compose'
if (Test-Path $s) {
  # Remove the placeholder README.md first so it doesn't shadow the real one (if any)
  $placeholder = Join-Path $d 'README.md'
  if (Test-Path $placeholder) {
    $size = (Get-Item $placeholder).Length
    if ($size -lt 500) { Remove-Item $placeholder -Force }
  }
  Copy-Item -Path (Join-Path $s '*') -Destination $d -Recurse -Force
  Write-Host "Copied skeleton/ -> infra/docker-compose/"
}
```

- [ ] **Step 8: Map `10-reference-inventory/` content → `references/`**

```powershell
$s = Join-Path $canon '10-reference-inventory'
$d = Join-Path $repo  'references'
if (Test-Path $s) {
  Copy-Item -Path (Join-Path $s '*') -Destination $d -Recurse -Force
  Write-Host "Copied 10-reference-inventory/ -> references/"
}
```

- [ ] **Step 9: Merge `audit/` and `11-release/` into `docs/releases/v3.0.1/`**

```powershell
$d = Join-Path $repo 'docs\releases\v3.0.1'
foreach ($srcName in @('audit','11-release')) {
  $s = Join-Path $canon $srcName
  if (Test-Path $s) {
    Get-ChildItem -Path $s -Recurse -File | ForEach-Object {
      $rel = $_.FullName.Substring($s.Length).TrimStart('\')
      $target = Join-Path $d $rel
      $targetDir = Split-Path $target -Parent
      if (-not (Test-Path $targetDir)) { New-Item -ItemType Directory -Path $targetDir -Force | Out-Null }
      Copy-Item $_.FullName $target -Force
    }
    Write-Host "Merged $srcName -> docs/releases/v3.0.1/"
  }
}
```

- [ ] **Step 10: Move readiness audit from `artifacts/kits/` into `docs/releases/v3.0.1/`**

```powershell
$src = Join-Path $repo 'artifacts\kits\development_readiness_audit_v3_0_1.md'
$dst = Join-Path $repo 'docs\releases\v3.0.1\development_readiness_audit_v3_0_1.md'
if (Test-Path $src) {
  if (Test-Path $dst) {
    $h1 = (Get-FileHash $src -Algorithm SHA256).Hash
    $h2 = (Get-FileHash $dst -Algorithm SHA256).Hash
    if ($h1 -eq $h2) {
      Remove-Item $src -Force
      Write-Host "Audit doc identical at destination; removed source copy." -ForegroundColor Green
    } else {
      $note = "Audit doc at artifacts/kits/ differs from docs/releases/v3.0.1/. Source preserved at artifacts/kits/development_readiness_audit_v3_0_1.md. Review and resolve.`r`nSource SHA: $h1`r`nDest   SHA: $h2`r`n"
      Set-Content -Path (Join-Path $repo 'docs\releases\v3.0.1\_audit_dedupe_notes.md') -Value $note -Encoding utf8
      Write-Host "Audit docs differ. _audit_dedupe_notes.md written." -ForegroundColor Yellow
    }
  } else {
    Move-Item $src $dst -Force
    Write-Host "Moved audit doc to release home." -ForegroundColor Green
  }
}
```

- [ ] **Step 11: Skip `99-archive/`**

```powershell
Write-Host "Skipping 99-archive/ (legacy ZIP-in-ZIP — kept inside the gitignored artifact only)."
```

(No file operation. Just a recorded decision.)

- [ ] **Step 12: Locate canonical-source-of-truth from ZIP**

```powershell
# CANONICAL_SOURCE_OF_TRUTH.md and START_HERE.md live at $canon root
foreach ($name in @('CANONICAL_SOURCE_OF_TRUTH.md','START_HERE.md','README.md')) {
  $s = Join-Path $canon $name
  if (Test-Path $s) {
    Copy-Item $s (Join-Path $repo "_v3_$name") -Force
    Write-Host "Staged ZIP's $name -> _v3_$name (will be rewritten/merged in Tasks 10-11)."
  }
}
```

This stages the originals so Task 11 can compare against them.

- [ ] **Step 13: Verify mapping completeness**

```powershell
$shouldExist = @(
  'docs\executive','docs\product','docs\architecture','docs\security','docs\requirements',
  'docs\api-contracts','docs\ui-ux','docs\devops','docs\evaluation','docs\adr','docs\diagrams','docs\deep-dive',
  'packages\shared-schemas','infra\docker-compose','references\reference_inventory_v3.csv',
  'docs\releases\v3.0.1\development_readiness_audit_v3_0_1.md'
)
$shouldExist | ForEach-Object {
  $p = Join-Path $repo $_
  "{0}  {1}" -f ($(if (Test-Path $p) {'OK'} else {'MISSING'})), $_
}
```

Expected: all `OK`. The `references\reference_inventory_v3.csv` check assumes the ZIP's `10-reference-inventory/` contained that filename — if it differs, capture the actual filename and update the verification reference.

**Checkpoint:** v3.0.1 content mapped into repo. Original ZIP remains untouched in `artifacts/kits/`.

---

## Task 10: Author release pointer files

**Files:**
- Create: `docs/releases/v3.0.1/README.md`
- Create: `docs/releases/v3.0.1/release_notes_v3_0_1.md`
- Create: `docs/releases/v3.0.1/manifest_reference.md`

- [ ] **Step 1: Write `docs/releases/v3.0.1/README.md`**

```markdown
# AI Remote Engineering OS v3.0.1 Baseline

v3.0.1 is the canonical development baseline. Status: **APPROVED FOR DEVELOPMENT BASELINE** per `development_readiness_audit_v3_0_1.md` (2026-05-15).

## Source of truth

- `CANONICAL_SOURCE_OF_TRUTH.md` (repo root)
- `docs/releases/v3.0.1/development_readiness_audit_v3_0_1.md`
- `docs/releases/v3.0.1/release_notes_v3_0_1.md`
- `docs/releases/v3.0.1/manifest_reference.md`
- `references/reference_inventory_v3.csv`

## Release assets

Large release assets are not committed to normal Git history. They are kept locally under `artifacts/kits/` (gitignored) and published as GitHub/GitLab/Gitea release assets, or stored in an internal artifact registry for offline environments.

Expected local paths:
- `artifacts/kits/ai_remote_engineering_os_starter_pack_v3_0_1_dev_ready.zip`
- `artifacts/kits/AI_Remote_Engineering_OS_Executive_Spec_v3.pdf`
- `artifacts/kits/requirements_roadmap_traceability_v3.xlsx`

Expected release assets:
- `ai_remote_engineering_os_starter_pack_v3_0_1_dev_ready.zip`
- `AI_Remote_Engineering_OS_Executive_Spec_v3.pdf`
- `requirements_roadmap_traceability_v3.xlsx`
- `sha256_manifest.txt`

## Development rule

Build P0 first. Do not implement P1/P2/P3 without an approved ticket and ADR.
```

- [ ] **Step 2: Write `docs/releases/v3.0.1/release_notes_v3_0_1.md`**

```markdown
# Release Notes — v3.0.1

Date: 2026-05-15
Status: APPROVED FOR DEVELOPMENT BASELINE

## What changed vs v3.0.0

v3.0.1 is a release-hygiene patch on top of v3.0.0:

1. SHA256 manifest regenerated. The v3.0.0 manifest had stale checksums for `manifest.txt` and `sha256_manifest.txt`.
2. The earlier `AI_Agentic_Architecture_Project_Markdown_Package.zip` is now explicitly archived inside the v3 ZIP under `99-archive/`.

No architecture changes. No content loss versus v2.2.

## What v3.0.0 introduced (recap)

- Workstream + Harness + Memory upgrade.
- P0/P1/P2/P3 priority ladder.
- Capability-edge permission model.
- Typed MCP Gateway design.
- 40 ADRs.
- 18 domain events.
- 8 JSON schemas.
- Docker Compose MVP skeleton.
- 40+ tracked references with adoption decisions.

## Canon

Use this v3.0.1 package as the development baseline. Earlier versions (v1, v2, v2.2, v3.0.0) are retained under `docs/deep-dive/` for historical traceability only.
```

- [ ] **Step 3: Write `docs/releases/v3.0.1/manifest_reference.md`**

````markdown
# Release Asset Manifest Reference — v3.0.1

Large release assets are not committed to normal Git history. They live under `artifacts/kits/` locally (gitignored) and are published as GitHub/GitLab/Gitea release assets, or stored in an internal artifact registry for offline environments.

## Expected local paths

- `artifacts/kits/ai_remote_engineering_os_starter_pack_v3_0_1_dev_ready.zip`
- `artifacts/kits/AI_Remote_Engineering_OS_Executive_Spec_v3.pdf`
- `artifacts/kits/requirements_roadmap_traceability_v3.xlsx`

## Expected release assets

- `ai_remote_engineering_os_starter_pack_v3_0_1_dev_ready.zip`
- `AI_Remote_Engineering_OS_Executive_Spec_v3.pdf`
- `requirements_roadmap_traceability_v3.xlsx`
- `sha256_manifest.txt`

## In-ZIP authoritative manifests

The ZIP contains its own SHA256 manifest under `11-release/` (now merged into this directory). The audit doc at `development_readiness_audit_v3_0_1.md` confirms the v3.0.1 manifest is consistent.

## Verification command (PowerShell)

```powershell
Get-FileHash -Algorithm SHA256 'artifacts\kits\ai_remote_engineering_os_starter_pack_v3_0_1_dev_ready.zip'
```

Compare the output against `sha256_manifest.txt` from the release assets when available.
````

- [ ] **Step 4: Verify three new release pointer files exist**

```powershell
@('README.md','release_notes_v3_0_1.md','manifest_reference.md','development_readiness_audit_v3_0_1.md') |
  ForEach-Object {
    $p = Join-Path $repo "docs\releases\v3.0.1\$_"
    "{0}  {1}" -f ($(if (Test-Path $p) {'OK'} else {'MISSING'})), $_
  }
```

Expected: all `OK`.

**Checkpoint:** release pointer set complete.

---

## Task 11: Author / rewrite root `CANONICAL_SOURCE_OF_TRUTH.md`

**Files:**
- Create: `CANONICAL_SOURCE_OF_TRUTH.md` (repo root)

- [ ] **Step 1: Read the staged original from the ZIP**

```powershell
$staged = Join-Path $repo '_v3_CANONICAL_SOURCE_OF_TRUTH.md'
if (Test-Path $staged) { Get-Content $staged | Out-Host }
```

Expected: see the original v3 CANONICAL_SOURCE_OF_TRUTH content. Note: the original references paths like `00-executive/vision.md` etc. We need to rewrite these for the in-repo layout.

- [ ] **Step 2: Write the rewritten in-repo version**

Overwrite `CANONICAL_SOURCE_OF_TRUTH.md` at the repo root with this content (use Write tool):

```markdown
# CANONICAL SOURCE OF TRUTH — AI Remote Engineering OS

Baseline: **v3.0.1** (APPROVED FOR DEVELOPMENT — see `docs/releases/v3.0.1/development_readiness_audit_v3_0_1.md`).

Earlier versions (v1, v2, v2.2, v3.0.0) are retained under `docs/deep-dive/` for historical traceability only. Do not start new work from them.

## Read in this order

1. `docs/executive/vision.md` — strategic vision and wedge.
2. `docs/executive/v3-war-room-decision.md` — why v3 / war-room model.
3. `docs/architecture/module-map.md` — the 14 modules and dependencies.
4. `docs/architecture/agent-harness-architecture.md` *(if present)* — agent harness runtime.
5. `docs/architecture/memory-architecture.md` *(if present)* — memory scopes.
6. `docs/security/agent-permission-model.md` — capability-edge model.
7. `docs/requirements/requirements_v3.csv` *(or `requirements.csv`)* — requirements table.
8. `docs/requirements/backlog_v3.csv` *(or `backlog.csv`)* — backlog table.
9. `docs/requirements/priority-legend.csv` *(or `priority_legend.csv`)* — P0/P1/P2/P3 meaning.
10. `references/reference_inventory_v3.csv` — adoption decisions for external references.

## Authoritative directories

| Concern | Path |
|---|---|
| Executive vision / roadmap | `docs/executive/` |
| Product / PRDs | `docs/product/` |
| Architecture / module map | `docs/architecture/` |
| Security / threat model / permission model | `docs/security/` |
| Requirements / backlog / priority legend | `docs/requirements/` |
| API contracts / OpenAPI / event catalog | `docs/api-contracts/` |
| UI / UX specs | `docs/ui-ux/` |
| DevOps / offline install | `docs/devops/` |
| Evaluation / domain packs | `docs/evaluation/` |
| ADRs (40+ accepted) | `docs/adr/` |
| Diagrams (mermaid) | `docs/diagrams/` |
| Schemas (JSON) | `packages/shared-schemas/` |
| Skeleton / compose / Makefile | `infra/docker-compose/` |
| Release pointer + audit + manifest | `docs/releases/v3.0.1/` |
| Reference inventory | `references/reference_inventory_v3.csv` |
| Deep-dive / historical docs | `docs/deep-dive/` |

## Agency instructions

- Claude Code agents: read `CLAUDE.md` first.
- Codex and other agents: read `AGENTS.md` first.
- War-room kit role cards: `ai/roles/`.
- War-room activation matrix: `ai/war-room/activation-matrix.md`.

## Development rule

Build P0 first. Do not implement P1/P2/P3 without an explicit ticket (`.github/ISSUE_TEMPLATE/ticket.md`) and an approved ADR.

## Notes

If a path referenced here does not resolve on disk, the v3.0.1 ZIP under `artifacts/kits/` is the binary source of truth. Re-extract and re-map per `docs/superpowers/plans/2026-05-15-kit-install-and-repo-structuring.md`.
```

- [ ] **Step 3: Verify every referenced path resolves**

```powershell
$check = @(
  'docs\executive\vision.md',
  'docs\architecture\module-map.md',
  'docs\security\agent-permission-model.md',
  'references\reference_inventory_v3.csv',
  'docs\releases\v3.0.1\development_readiness_audit_v3_0_1.md',
  'CLAUDE.md','AGENTS.md',
  'ai\roles\core\commander.md',
  'ai\war-room\activation-matrix.md'
)
$missing = $check | Where-Object { -not (Test-Path (Join-Path $repo $_)) }
if ($missing) { Write-Host "MISSING:" -ForegroundColor Red; $missing } else { Write-Host "All canon paths resolve." -ForegroundColor Green }
```

Expected: `All canon paths resolve.` If anything is missing (e.g., the ZIP's filename differs from `requirements_v3.csv`), update `CANONICAL_SOURCE_OF_TRUTH.md` to match the actual filename found under `docs/requirements/`.

- [ ] **Step 4: Clean up staged originals**

```powershell
@('_v3_CANONICAL_SOURCE_OF_TRUTH.md','_v3_START_HERE.md','_v3_README.md') | ForEach-Object {
  $p = Join-Path $repo $_
  if (Test-Path $p) { Remove-Item $p -Force }
}
```

**Checkpoint:** canonical source of truth in place and self-consistent.

---

## Task 12: Author supporting files (.gitignore, ticket template, source watchlist, root README)

**Files:**
- Create: `.gitignore`
- Create: `.github/ISSUE_TEMPLATE/ticket.md`
- Create: `references/source_watchlist.md`
- Create: `README.md` (repo root)

- [ ] **Step 1: Write `.gitignore`**

```gitignore
# v3.0.1 release artifacts — kept local, published as release assets
artifacts/kits/*.zip
artifacts/kits/*.pdf
artifacts/kits/*.xlsx
!artifacts/kits/.gitkeep

# OS / editor cruft
.DS_Store
Thumbs.db
*.swp
*.swo
.vscode/
.idea/

# Staging
/_v3_*.md

# Language-specific ignores deferred until ADR-0001 picks the toolchain.
```

- [ ] **Step 2: Write `.github/ISSUE_TEMPLATE/ticket.md`**

```markdown
---
name: T-NNN Ticket
about: A scoped ticket aligned with the v3.0.1 backlog
title: "T-NNN: <one-line summary>"
labels: ["ticket"]
---

## Ticket ID and link

T-NNN — see `docs/requirements/backlog_v3.csv` (or `backlog.csv`).

## Goal

<one or two sentences>

## Priority

P0 / P1 / P2 / P3 (P0 only unless an approved ADR justifies otherwise)

## Files in scope

- Create: ...
- Modify: ...
- Test: ...

## Acceptance criteria

- [ ] ...
- [ ] ...
- [ ] OpenAPI / schemas updated if applicable
- [ ] Docs / ADR updated if applicable
- [ ] Unit tests cover valid + invalid + edge cases
- [ ] Verification command runs clean

## Out of scope

- ...

## Diff budget

≤12 files / ≤500 LoC unless explicitly approved here.

## ADRs required

- None / ADR-NNNN (specify)

## Verification

Command(s) and expected output.

---

## Reference: v3.0.1 first-10 backlog

- T-001 Repo skeleton + root documentation
- T-002 Docker Compose for Postgres + Redis/NATS + API + web
- T-003 Shared domain types/schemas (Organization, User, Resource, Session, AuditEvent)
- T-004 audit-service minimal append-only API
- T-005 resource registry CRUD API
- T-006 session-broker skeleton (no real remote desktop yet)
- T-007 web dashboard shell
- T-008 auth placeholder adapter (Keycloak/AuthentiK boundary)
- T-009 OpenAPI spec + generated client types
- T-010 CI checks (lint, typecheck, test, schema validation)
```

- [ ] **Step 3: Write `references/source_watchlist.md`**

```markdown
# Source Watchlist

External sources this project tracks beyond the v3 reference inventory. The authoritative inventory is `references/reference_inventory_v3.csv`.

| Source | Type | Why we track it | Last reviewed |
|---|---|---|---|

(Empty — populate as references emerge during P0 work.)
```

- [ ] **Step 4: Write root `README.md`**

```markdown
# AI Remote Engineering OS

Self-hosted AI Engineering Operating System for restricted networks. v3.0.1 baseline.

## Start here

- `CANONICAL_SOURCE_OF_TRUTH.md` — what v3.0.1 says and where each piece lives in this repo.
- `docs/executive/vision.md` — strategic vision and wedge.
- `docs/architecture/module-map.md` — the 14 modules and their dependencies.
- `docs/security/agent-permission-model.md` — capability-edge model.
- `docs/requirements/` — priority ladder, requirements, backlog, roadmap.
- `CLAUDE.md` — Claude Code agency instructions.
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

Binary release artifacts (ZIP, PDF, XLSX) are gitignored. See `docs/releases/v3.0.1/manifest_reference.md`.

## License

TBD per ADR.
```

- [ ] **Step 5: Verify all four files exist**

```powershell
@('.gitignore','.github\ISSUE_TEMPLATE\ticket.md','references\source_watchlist.md','README.md') | ForEach-Object {
  $p = Join-Path $repo $_
  "{0}  {1}" -f ($(if (Test-Path $p) {'OK'} else {'MISSING'})), $_
}
```

Expected: all `OK`.

**Checkpoint:** supporting files in place.

---

## Task 13: Verification pass

**Files:** none (read-only).

- [ ] **Step 1: Run the kit's own linter**

```powershell
python (Join-Path $repo 'scripts\war_room_lint.py')
```

Expected: exit code 0, no errors. If the script requires a working dir argument, run with `python scripts\war_room_lint.py .` from inside `$repo`. Capture any complaints and decide whether to fix or document as known-issue.

- [ ] **Step 2: Confirm `artifacts/kits/` contents are gitignored-only**

```powershell
Get-ChildItem (Join-Path $repo 'artifacts\kits') -File | Select-Object Name, Length
```

Expected: the ZIP / PDF / XLSX remain present locally; `.gitkeep` also present. (Git-ignore enforcement is verified after `git init`.)

- [ ] **Step 3: Full canonical-path resolution sweep**

```powershell
$canonPaths = @(
  'CANONICAL_SOURCE_OF_TRUTH.md','CLAUDE.md','AGENTS.md','README.md',
  'docs\executive\vision.md','docs\architecture\module-map.md','docs\security\agent-permission-model.md',
  'docs\requirements','docs\api-contracts','docs\ui-ux','docs\devops','docs\evaluation',
  'docs\adr','docs\diagrams','docs\deep-dive',
  'docs\releases\v3.0.1\README.md','docs\releases\v3.0.1\development_readiness_audit_v3_0_1.md',
  'docs\releases\v3.0.1\release_notes_v3_0_1.md','docs\releases\v3.0.1\manifest_reference.md',
  'packages\shared-schemas','infra\docker-compose',
  'references\reference_inventory_v3.csv','references\source_watchlist.md',
  'ai\core\war-room-doctrine.md','ai\war-room\TEAM.md','ai\war-room\activation-matrix.md','ai\war-room\role-router.md',
  'ai\roles\core\commander.md',
  'ai\roles\specialists\remote-cockpit-architect.md',
  'ai\roles\specialists\permission-model-reviewer.md',
  'ai\roles\specialists\audit-evidence-reviewer.md',
  'ai\roles\specialists\mcp-gateway-reviewer.md',
  'ai\roles\specialists\offline-install-engineer.md',
  'ai\roles\specialists\workstream-architect.md',
  '.github\ISSUE_TEMPLATE\ticket.md','.gitignore',
  'scripts\war_room_lint.py','claude-desktop\project-instructions-v4.md'
)
$missing = $canonPaths | Where-Object { -not (Test-Path (Join-Path $repo $_)) }
if ($missing) { Write-Host "MISSING:" -ForegroundColor Red; $missing | ForEach-Object { Write-Host "  $_" } } else { Write-Host "All canonical paths resolve." -ForegroundColor Green }
```

Expected: `All canonical paths resolve.`

If `references\reference_inventory_v3.csv` is missing, check the actual filename produced by the ZIP's `10-reference-inventory/` folder and update both `CANONICAL_SOURCE_OF_TRUTH.md` and this verification step.

- [ ] **Step 4: Top-level tree summary for the user**

```powershell
Get-ChildItem $repo -Directory | Select-Object Name | ForEach-Object { "- $($_.Name)/" }
Get-ChildItem $repo -File | Select-Object Name | ForEach-Object { "- $($_.Name)" }
```

Expected directories include: `ai`, `apps`, `artifacts`, `claude-desktop`, `docs`, `infra`, `packages`, `references`, `scripts`, `services`, `.github`.
Expected files include: `AGENTS.md`, `CANONICAL_SOURCE_OF_TRUTH.md`, `CLAUDE.md`, `README.md`, `.gitignore`.

**Checkpoint:** repo skeleton verified.

---

## Task 14: Clean up staging and surface git-init question

**Files:** none in the repo.

- [ ] **Step 1: Remove staging dir**

```powershell
$stage = 'D:\Temp\v3_0_1_stage'
if (Test-Path $stage) { Remove-Item $stage -Recurse -Force }
Write-Host "Staging dir removed."
```

- [ ] **Step 2: Summarize for the user and surface git-init question**

Print a short summary to chat covering:
- Repo skeleton: directories created, files committed (file count).
- War-room kit: copied + customized; 6 new specialist roles added; activation matrix tuned for P0/P1/P2/P3.
- v3.0.1 docs: mapped per the spec; collisions logged in `_merge_notes.md` / `_dedupe_notes.md` if any.
- Out-of-scope reminders (no `git init`, no service bodies, no ADRs).
- Next decision points to ask the user:
  1. Initialize git here? Default branch name? Commit signing?
  2. Approve ADR-0001 (monorepo tooling) as the next ticket?
  3. File T-002 through T-010 as issues now (requires git remote) or later?

**Checkpoint:** session complete; user takes the next decision.

---

## Self-review

**Spec coverage:** every section of the spec maps to at least one task:
- Layout (spec §1) → Tasks 2, 9.
- War-room customization (spec §2): CLAUDE.md → Task 4; 6 role cards → Task 5; activation matrix / TEAM / role-router → Task 6; Claude Desktop → Task 7; AGENTS.md → Task 8.
- First-10 tickets (spec §3) → Task 12 Step 2 (issue template).
- ADR queue (spec §4) → noted in CLAUDE.md/AGENTS.md as "TBD per ADR-NNNN"; no ADR authored, matching spec.
- Release pointer (spec §5) → Task 10.
- Verification (spec §5 "Verification") → Task 13.
- Mapping rules (spec "Non-trivial mapping rules") → Task 9 Steps 2-11.
- Risks (collision handling) → Task 9 Steps 4-5, 10.
- Open assumption (git init) → Task 14 Step 2.

**Placeholder scan:** "TBD" appears in CLAUDE.md / AGENTS.md content blocks; in every case it is qualified with "per ADR-NNNN" — these are intentional, spec-aligned placeholders, not plan-failure placeholders. No "fill in details" / "appropriate error handling" / "similar to Task N" patterns.

**Type / path consistency:** `$repo`, `$canon`, `$stage` recomputed at the top of any task that uses them. Specialist role card filenames match between Task 5 (creation) and Task 13 (verification). The `references\reference_inventory_v3.csv` filename appears in three places (Task 9 Step 8 verification, Task 11 Step 2 content, Task 13 Step 3 verification) — Task 9 Step 13 and Task 13 Step 3 both surface the fallback if the actual filename differs.
