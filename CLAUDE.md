# CLAUDE.md — AI Remote Engineering OS

These rules apply to this project unless explicitly overridden. This project follows spec-set edition **SPEC-2026.1** (legacy designation *v3.0.1*) — the product's own canonical docs/ADR bundle (`docs/spec-sets/v3.0.1/`, ADR-0001…ADR-0040). ⚠ **A spec-set edition is not a product version.** The product line is `v0.x → v1.0.0` and is the only one that is tagged or shipped; see `CANONICAL_SOURCE_OF_TRUTH.md` §Version namespaces. **Also not to be confused with the war-room agency kit v4.6.0** in `../agency-kit/` — all three version lines are unrelated and move independently.

## Workspace layout
This product repo lives at `AI-Agentic-OS/ai-remote-engineering-os/` and is one of three siblings:

- `../agency-kit/` — generic Claude war-room agency kit **v4.6.0**. Owns base CLAUDE.md template, core modules, templates, generic role cards, scripts. Upgraded independently. This directory is a **refreshable snapshot**; the canonical source is `D:\1_PROJECT\PRIVATE_WORK\Agentic Kit\war-room-agency-workspace\core\war-room-agency-kit-<version>\claude\` (see `../VERSION_CONTROL.md`). Read the snapshot, not the canonical tree.
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
- DB migration tool: **`golang-migrate/migrate` v4 in library mode**, embedded per service via `embed.FS`; shared runner in `packages/go/migrate/`; per-service `services/<name>/migrations/` directories with pure-SQL files; `audit-service` is forward-only by policy. Invoked via `task migrate -- <service> <action>`. See ADR-0044.
- Event bus: **no broker at P0**; Postgres transactional outbox in producers + sync HTTP append-only ingest into `audit-service` (chained-hash extended in-process at the chain authority); shared `packages/go/outbox/` module follows the `packages/go/middleware/` and `packages/go/migrate/` shape; relay exposed as `<service> outbox relay` subcommand, woken by Postgres `LISTEN/NOTIFY` with periodic-sweep fallback; **NATS JetStream held as the named P1 reserve pick**. See ADR-0045.
- Auth adapter: **no IdP daemon at P0**; in-process `packages/go/auth/` issues PASETO v4 local capability tokens with a typed `CapabilityToken` claim object (`edge` from the seven `agent-permission-model.md` edges plus `service-service`); tiered TTLs (agent 5min / operator 8h / service 1h / lease = lease expiry); `packages/go/middleware/RequireCapability` imports `packages/go/auth/`; service-to-service auth via signed PASETO service tokens with workload identity (closes ADR-0045's S2S hangover, not mTLS at P0); single bootstrap operator account, argon2id, no self-service signup / no password reset UI / no OAuth providers / no JWKS rotation at P0; **Zitadel held as the named P1 reserve pick** when enterprise federation is required (Keycloak rejected on operational-burden + supply-chain grounds). See ADR-0046.
- Frontend framework: **no meta-framework at P0**; `apps/web` (and, when scoped, `apps/admin`) are plain **Vite 6 + React 18 SPAs** built to static assets and served by Caddy/Traefik (per deep-dive 09). Routing via `react-router` v7 in declarative SPA mode. TS API client generated by **`openapi-typescript` + `openapi-fetch`** from the same `docs/api-contracts/openapi.v3.fragment.yaml` as the Go server stubs (one schema, two consumers; `task codegen` extends). Styling via **Tailwind CSS v4** + **vendored shadcn/ui** (copy-pasted into `packages/ui/src/components/`, not an npm dep) + **Radix UI** primitives. Auth-token storage via **`HttpOnly; Secure; SameSite=Strict` cookie** set by `services/api` (PASETO token stays opaque to JS); 8h sliding refresh via server-side cookie rotation in the second half of TTL; **double-submit `X-CSRF-Token`** for non-idempotent mutations; **same-origin deploy** posture. Capability-edge UI via **`GET /v1/auth/me`** + `<CapabilityProvider>` + `<CapabilityGate>` + route guards (server-side `RequireCapability` per ADR-0046 remains the security boundary). Native `WebSocket` / `EventSource` with thin React hooks in `packages/ui/` for the session-broker channel. `guacamole-common-js` mounted in `<RemoteSessionView>`; `xterm.js` mounted in `<TerminalView>`. Package boundaries: `packages/ui` = components + tokens + capability provider + WS/SSE hooks + Guacamole/terminal wrappers; `packages/shared-types` = generated OpenAPI types + `openapi-fetch` client wrapper (including the 401 chokepoint); `packages/config` = shared eslint/prettier/tsconfig/tailwind. **Remix (SPA mode) or TanStack Start held as the named P1 meta-framework reserve**; Next.js explicitly NOT held in reserve (Vercel-deploy-primitive footgun from ADR-0042 applies doubly). See ADR-0047.
- Dev / Test / Lint / Build commands: canonical entry points are `task lint`, `task test`, `task build`, `task offline-bundle` (per ADR-0042).
- Current phase: **P0, P1, and the P2 governance/data plane are all complete.** Shipped since: **v0.8.0** Persistent Scoped Memory (Layer 2, ADR-0064) · **v0.9.0** Adaptive Workstreams — the `agent-memory` read path enforced + agent-composed plans under per-tick informed consent (ADR-0065) · **v0.10.0** Live/Context Index (Layer 1, deterministic native-Postgres-FTS retrieval, the `agent-workspace` edge lit read-only, ADR-0066) · **v0.11.0** Operator Experience (audit read surface, ADR-0067) · **v0.12.0** Access & Onboarding (paired operator sessions — the product's first revocable session — plus enforced deployment profiles and first-run onboarding, ADR-0068).
- **Latest release: v0.16.0 "Public Readiness"** — the release that makes the product publishable and its own verification instruments trustworthy, on one question: **can somebody who is not us rely on what we claim?** ⭐ **Mints NO WD** — no new governed capability, no governance-plane runtime change (the PUB-6 fixes touch only the installer's re-run behaviour and the cockpit's onboarding display); WD-1..15 regression green on both the compose and installed stacks plus `task smoke` (VER-46), five instrument gates added across the ladder (PUB-1/history-secret-scan, block-criterion-check, extended doc-check), a content-level git-history scan proven clean, and the first public-facing documents (`README.md`/`SECURITY.md`/`CONTRIBUTING.md`/`LICENSE`/`NOTICE`, ADR-0075 Apache-2.0 + ADR-0076 DCO). ⚠ **TAGGED `e43cece`** (2026-10-25, after PUB-6 passed on re-run) — pinned to the commit both signed artifacts attest; **must not be moved**. ⭐ **PUBLISHED 2026-10-25** — https://github.com/PaladinKnightMaster/ai-remote-engineering-os (public): `main` + the 16 release tags (not `dev`/`wip/*`); 12 topics + description per §3a; private vulnerability reporting ON (PUB-9); GitHub Release `v0.16.0` with both archives, both `.sig`, both SBOMs and `aireos-release.pub` (PUB-10). Before the push every commit/tag email was rewritten to the GitHub noreply address (CEO decision; trees identical; old IDs → `docs/releases/v0.16.0/pre-publication-commit-map.md`). ⚠⚠⚠ **PUB-6 RAN 2026-10-14 (CEO, fresh Ubuntu 24.04 VM) and FAILED as a stranger test — the product passed every step, the documents did not** (stuck at the install guide's build-first order and at a resource id no screen shows; two false success claims — an `install.sh` re-run never restarts units, and the quickstart promises output that is never stored). **Fixed by T-164…T-166, rebuilt from `e43cece`, re-signed, and PUB-6 PASSED on re-run (recorded 2026-10-25) → tagged.** See `docs/releases/v0.16.0/pub6-ceo-run.md`. Both publication blockers were resolved 2026-09-02 (`8faec48`: GitHub private vulnerability reporting; `NOTICE` names the project + `github.com/paladinknightmaster`). ⚠⚠ **A PUB-6 rehearsal (2026-10-05) found a KNOWN INSTALL LIMITATION, stated rather than fixed in this release: the prebuilt postgres companion fails on any host patched after it was built** — OS package version skew, an ADR-0052 design issue present since v0.1.0 and **structurally invisible to WD-1**, which builds its host and the companion from one snapshot. Documented workaround: bring-your-own PostgreSQL 16 + pgcrypto. See `docs/releases/v0.16.0/verification_runbook.md` and `docs/releases/v0.16.0/pub6-rehearsal.md`.
- *(previous)* **v0.15.0 "Governed Decision Policy", tagged `53782dd`** — the release in which `policy-service` made its first actual policy decision: an operator-authored rule store, a real `deny` a machine can issue before any human is asked, and governance at the level of the **action** rather than the resource (ADR-0074). Rules are **operator-authored only**; **severity beats specificity** with no tiebreak; an **empty rule table changes nothing** (CON-1, proven live against the unchanged v0.14.0 approval suites); **arguments are not yet governable**. ⚠ R1 is only HALF closed — the decision plane, not the post-approval plane; only containment (B-040) constrains what an approved command does. ⚠ The chain records THAT a policy decision happened, not WHICH RULE fired. Both artifacts signed with the production key (fingerprint `968a29b3623fa79a…`, published in the release notes); **SEC-45 PROVED** against both. ⚠ The tag is pinned to the commit the signature attests to and **must not be moved**.
- **Release target: v0.17.0 "Install & First-Run Experience" — scope RATIFIED 2026-10-26 (CEO).** Criteria: `docs/releases/v0.17.0/v0.17.0-release-criteria.md`. **One command on the server (`sudo ./setup.sh`), everything after it in the browser**: (A) self-contained PostgreSQL — **fixes R1** — per **ADR-0077, Accepted 2026-10-26**, OS scope Ubuntu 24.04 + **26.04** + Rocky 9, each proven on a LATER, independently built host with the v0.16.0 companion as the negative control (SEC-63); (B) one-command setup that generates and wires the DB password; (C) browser first-run setup behind a one-time code — **ADR-0078, to be written (T-169)**; (D) connector-generated target keys + a loopback demo with no server command — **ADR-0079, to be written (T-170)**; (E) installer residuals (P8, post-restart health). **NO new WD**; the gate is **HUM-1: PUB-6 by a tester who is NOT the CEO, ≤3 server commands**, plus SEC-63…67. ⚠ `block-criterion-check` is red on the v0.17.0 criteria by design until those tests exist. Ladder T-169…T-178.
- ⚠ **The WD ladder is ALLOCATION-ordered, not release-ordered** (ruled 2026-08-28). WD-1..11 built; **WD-12 reserved to containment, WD-13 to v1.0.0, WD-14 to v1.1**; v0.15.0 therefore takes **WD-15**. Renumbering would break criteria references across fourteen releases. Do not read the gap as an error.
- **Containment (B-040 / WD-12) is DEFERRED, not cancelled** (CEO, 2026-08-19). ⚠ **This bullet's original sequencing clause is SUPERSEDED — see the SEQUENCING bullet below.** It read *"the path after v0.15.0 is v1.0.0 Beta 1 (where layer 2 lands and the surface freezes)"*; **both halves have since changed** — v0.16.0 now precedes Beta 1, and **the surface freeze is split out to v1.0.0 final**. Corrected in place rather than left to contradict, because two canon statements in conflict make a reader assume one is an error and pick the wrong one. What SURVIVES from this bullet is the containment consequence itself: ⚠ **Consequence, recorded deliberately:** the 2026-08-10 record held that containment is a prerequisite for layer 2. It is not a build blocker — without it, harness governance is **cooperative rather than enforced**, which is the same posture the gateway already ships and already discloses. Layer 2 may therefore proceed **provided the limitation is stated, not drifted into**. See `docs/requirements/epic_story_catalog_v4.md` §Part 4.

- ⭐ **SEQUENCING — DECIDED 2026-09-01 (CEO), closing the question raised earlier the same day.** The path is **v0.16.0 "Public Readiness" → v1.0.0 Beta 1**. v1.0.0's flagship is **B-041** (governed harness integration, layer 2) — NOT-STARTED, needing **its own ADR + WD-13**; see `docs/spike/b041-remaining-work.md`. ⭐⭐ **The API SURFACE FREEZE IS SPLIT OUT of Beta 1** and lands at **v1.0.0 final**, once the integration has actually been used — freezing a contract whose newest consumer has existed for weeks binds a promise to something nobody has run. ⚠ **Consequences: Beta 1's criteria must STATE that the surface is not frozen** (an unstated non-freeze reads as a freeze); v0.15.0's "last fragment change before the freeze" framing is **superseded, not wrong**; and **v1.0.0 final must carry its OWN freeze criteria** rather than assume the surface settled by itself. B-041 ships as **COOPERATIVE** governance while B-040 is deferred — a limitation that must be stated, not drifted into.

> ADR numbering: v3.0.1 ADRs (`docs/adr/ADR-0001.md` through `ADR-0040.md`) cover product decisions. New meta-decisions (language, tooling, frameworks) start at **ADR-0041**. **Current head: ADR-0079** (⚠ **ADR-0078 and ADR-0079 are Proposed, not Accepted** — v0.17.0's browser first-run setup code, refining ADR-0046, and connector-generated target credentials, extending ADR-0053; **ADR-0077 Accepted 2026-10-26**, the self-contained PostgreSQL companion) — check `docs/adr/` before minting a number, and **never edit an Accepted ADR** (see §Forbidden work).

## Project doctrine
- Build P0 first. P1/P2/P3 features require an explicit ticket and an approved ADR.
- No SaaS runtime dependency.
- No AI autonomy before audit + policy + approval primitives exist.
- All major dependency choices need an ADR.
- Diff budget: ≤12 files / ≤500 LoC per ticket unless explicitly approved.
- Read the **SPEC-2026.1** baseline `docs/` before changing related code (start at `docs/executive/`, `docs/architecture/module-map.md`, `docs/security/agent-permission-model.md`). ⭐ `docs/executive/` was **REWRITTEN 2026-08-27** (canon work order, pass 3) and is now current — all four files (`vision.md`, `strategy.md`, `competitor-map.md`, `north-star-roadmap.md`) **narrate and link rather than duplicate**, which is what let the previous version drift for fifteen months. ⚠ **This bullet warned they were stale long after they were fixed** — corrected 2026-09-02 at the v0.16.0 cut. A stale warning pointing at corrected canon is the same defect class as stale canon itself: a reader trusts it and skips the authoritative source. `doc_check.py` has scanned these files since the rewrite precisely because every link in them is now load-bearing.
- Every spec / ADR / PR claim must resolve to a path in this repo or the sibling `../agency-kit/`.

## Definition of done
Relevant specs/docs updated; tests/evals run or gaps stated; UI/accessibility reviewed when applicable; risks and next actions stated.
- Changes to `packages/ui` or `apps/web` rendering output require browser-rendered evidence (a screenshot or a passing Playwright assertion against the rendered DOM) before "done" — jsdom unit tests do not satisfy this.
- **No `[inherited:]` or `[assumed]` provenance marker may survive on a load-bearing claim** — see §Verification discipline below.

## Verification discipline — the two rules that bind every claim

> ⭐⭐ **The root cause, measured across this project's whole defect record.** **Every high-value catch came from OPENING A SOURCE THAT HAD NOT BEEN OPENED. Not one came from re-reading.** ADR-0073 was audited three times; the first two passes were the same mind re-reading the same text. What broke the loop each time was new information — reading the tool instead of its name, reading the ADR instead of a summary of it, running a repo-wide sweep, opening the archive. Historically identical: **SFTP survived five tickets and three reviews** because every unit test stubbed it, and was found by *running a real transfer*; a bundle version **stale for eight releases** was found by *running the quickstart on a fresh host*. **More care does not fix this. More sources does.**

**Rule 11 — claim-source provenance.** Mark how every load-bearing claim was verified:
`[read: path:line]` (you opened it) · `[ran: cmd]` (you executed it and saw the output) · `[inherited: doc]` (taken from another document) · `[assumed]` (not verified).
**`[inherited:]` and `[assumed]` are the defect population.** Neither may survive on a load-bearing claim at sign-off — upgrade it or delete the claim. This turns *"verify carefully"* (unbounded, forgettable) into *"resolve N markers"* (countable, finishable).
⚠ **Briefs, plans, criteria docs and prior summaries record what was true WHEN WRITTEN — treat them as `[inherited:]`, never as source.** A ticket brief once asserted a security property the product does not have, because it took a *goal* from a planning doc instead of reading the ADR that decided otherwise. Worked form: `docs/adr/TEMPLATE.md`.

**Rule 12 — negative control on every instrument, not just gates.** A gate command must not be piped and must write its real exit into the log; **this extends that to any observation.** **A sweep, grep or check that asserts ABSENCE must ship with a self-test proving it detects a planted instance, in the same run.** Real failures, each producing a *wrong belief about a result*: a line-anchored grep reported CLEAN because the phrase **wrapped across a newline**; `grep -c` returning 0 **broke an `&&` chain** so the next check silently never ran; a reference count rising 197→207 was mistaken for coverage until the mutation proof **failed**; and a check written as `python -c "…\`…\`…"` was corrupted because **bash command-substitutes backticks inside double quotes** — use a `<<'EOF'` heredoc. ⭐ **Fifth instance, 2026-08-28 (v0.15 scoping), and it was in the gate itself:** a planted broken path in a new ADR **did not fire `doc-check`**. Cause — `has_ext()` tested the last path segment, which for a provenance citation ends in the **line number**, not the extension, so **every `[read: path:line]` marker in the corpus was silently unchecked** — the population `TEMPLATE.md` mandates for exactly the load-bearing claims that matter most. 15 such citations, none broken (an ungated population, not live rot). Fixed + mutation-proven; the gate went 247 → **262** references. **The green number was real and the coverage was not** — which is rule 12's whole thesis. ⭐⭐ **Sixth instance, same day, at ADR-0074's Accept — and it is the sharpest:** the generated ADR ledger recorded ADR-0074 as **Accepted** while the ADR file read **`Proposed`**, and **`adr-ledger-check` was green over it**. `adr_ledger.py` picked the status by scanning `STATUS_WORDS` in **tuple order** (`Accepted` first) rather than by position, so **any Proposed ADR whose Status block named an Accepted one — the shape `TEMPLATE.md` mandates — was reported Accepted.** Fixed + mutation-proven; 0 other ADRs affected. ⚠ **The durable lesson: `adr-ledger-check` regenerates with the SAME parser and compares the output to itself, so a parser error is invisible to it BY CONSTRUCTION. A self-comparing gate proves consistency, never correctness** — and this ledger was built specifically so an ADR could not sit in the wrong status undetected, which ADR-0017 did for fifteen months. **Assert a generator's output against something that is not the generator.**
Practical form: normalise whitespace (`re.sub(r"\s+"," ",text)`) before searching, assert the **corrected text is PRESENT** as well as the banned text absent, and print the self-test result beside the verdict.

**Gates that enforce the mechanical half:** `task doc-check` (referenced paths resolve; CSVs under `docs/requirements/` + `references/` are well-formed) · `task staleness-check` (the canon's self-descriptive claims are true) · `task adr-ledger-check` (the generated ADR ledger matches the corpus) · `task block-criterion-check` (every `block`+`SEC-` release criterion carries a `[test: Name]` naming a test that actually exists, or an explicit `[test: none — reason]`; VER-41/SEC-61, T-158) · `task go-deps-check` (the core's Go dependency surface stays allowlisted — ADR-0073 §2). All five run in `task release-check`.

## Kit modules — v4.6.0 (live in sibling agency-kit/)
Use these modules when relevant:
- `../agency-kit/ai/core/grounded-code.md`
- `../agency-kit/ai/core/agent-reader-architecture.md`
- `../agency-kit/ai/core/design-system-governance.md`
- `../agency-kit/ai/core/context-checkpoint-protocol.md`
- `../agency-kit/ai/core/sandbox-permission-matrix.md`
- `../agency-kit/ai/core/war-room-doctrine.md`

Cost, routing, and response shape — added at the v4.6.0 kit bump, and the modules where v4.6.0's behavior actually changed:
- `../agency-kit/ai/core/token-management.md` — effort is the primary cost lever; mode carries effort. Effort changes reasoning depth, **not** visible response length.
- `../agency-kit/ai/war-room/output-contracts.md` — output contracts scale by mode. A contract is the most a response should contain, not a form to fill in.
- `../agency-kit/ai/core/agent-harness.md` — gates are checks, not re-runs. A gate reads evidence that already exists rather than re-running the check. Directly relevant to this repo's stacked G1/G2 + WD gates.
- `../agency-kit/ai/models/MODEL_ROUTING.md` — routing aliases and per-role effort. Cards declare `model: haiku|sonnet|opus`, never a pinned model id.
- `../agency-kit/ai/engines/context/CONTEXT_GOVERNOR_V2.md` — delegation caps; do not delegate verification of your own work.
- `../agency-kit/ai/engines/context/TOOL_OUTPUT_POLICY.md` — narration cadence and large-output handling.

**Effort is a session-level setting, not an agent-card field.** `MODEL_ROUTING.md` records intended effort per role as doctrine; whoever dispatches applies it. Agent cards carry `model:` only.

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
- P0 — MVP survival (Remote Cockpit, Audit, Permission Model, Offline Install) — complete at v0.1.0
- P1 — defensibility (MCP Gateway, Workstreams) — complete at v0.7.0
- P2 — Memory / Context Index — **Layer 2 "Persistent Scoped Memory" shipped at v0.8.0** (ADR-0064); **Layer 1 "Live/Context Index" shipped at v0.10.0** (ADR-0066). **Layer 3 remains deferred and hard-gated** on the offline/no-SaaS doctrine (backlog B-024 — see its row for the 2026-08-10 pgGraph assessment, which is adoptable for operator-plane analytics only)
- P3 — optional delight

> Tier note (v0.8.0 reconciliation, DOC-16): the standalone Memory/Context Index module
> is **P2** per `docs/requirements/requirements_v3.csv` (REQ-013/REQ-014, backlog
> B-009/B-010, roadmap Phase 6) — the P1 pillar was REQ-007 Living Workstreams, whose
> scoped-memory attribute shipped read-time at v0.7.0 (EVD-1) and durable at v0.8.0
> (ADR-0064). An earlier revision of this list compressed that as "Memory" under P1;
> the requirements ladder outranks the shorthand.

## Forbidden work
- Introducing an **ad-hoc network-egress code path**. Every egress path goes through the governed connector/policy chain (shipped v0.2.0; ADR-0053 keeps the connector the sole decrypt authority). A genuinely new egress path needs its own ADR.
- Modifying existing ADRs under `docs/adr/` — **propose a new ADR instead** (gate G2: flag any Accepted-ADR contradiction at decision time, then conform or file new).
- Editing files under `../agency-kit/` from this repo's tickets; kit upgrades happen in their own repo.
- `git init`, commits, or tags without explicit user direction. **Pushing (branches or tags) needs explicit CEO direction every time** — the repo has been public at `origin` (https://github.com/PaladinKnightMaster/ai-remote-engineering-os) since 2026-10-25, so a push is a publication. Only `main` and release tags are pushed; `dev`/`wip/*` stay local. ⚠ **This repo commits as the GitHub noreply address** (`git config --local user.email`); never push a commit carrying a personal email — check `git log --format=%ae` first.

> **Bootstrap-phase rules retired 2026-08-10.** Two bullets here gated the T-002/T-003 skeleton era — *"no business logic before T-002 and T-003 are complete"* and *"don't touch `services/`, `apps/`, or `packages/shared-types/` with non-scaffolding code."* Both preconditions were satisfied at **v0.1.0**; the repo now ships six services and two apps, and a third bullet's precondition (the `docs/security/` egress policy) was satisfied at **v0.2.0** and is re-scoped above into the standing rule it became. They were **retired rather than left as dead text**, because an implementer subagent reading them mid-ticket would be misdirected away from exactly the directories its ticket targets. The live constraints that replaced them are the **diff budget** and the **ticket + ADR discipline** in §Project doctrine — neither was weakened here.
