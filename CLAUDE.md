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
- **Latest release: v0.14.0 "Governed Tool Surface", tagged `6b72739`** — the first capability widening since v0.3.0: `remote.file.read`/`write`/`list` on the **existing** `agent-remote-session` edge through an unchanged governance pipeline (ADR-0071 + ADR-0072). Both artifacts signed with SBOMs; VER-19 (§human) proved `approval.granted` precedes `credential.leased`, and proved the denial **by absence** of the lease and the egress. ⚠ The tag is **deliberately not at the branch head** — it is pinned to the commit the release signature attests to and must not be moved.
- *(previous)* **v0.13.0 "Evidence Hardening", tagged `c04554f`** — signed audit-chain heads (ADR-0069) anchored outside Postgres, an offline `aireos-verify` needing no DB and no network, and both release artifacts signed with CycloneDX SBOMs (ADR-0070). Tags are local and are **never pushed** — this repo has no git remote.
- **Release target: v0.15.0 "Governed Decision Policy" — SCOPED 2026-08-28**, criteria at `docs/releases/v0.15.0/v0.15.0-release-criteria.md`, binding ADR **ADR-0074** (**Accepted 2026-08-28** — the ladder is unblocked; T-148 is next). Give `policy-service` the rule store and evaluator it has never had: it owns two tables and neither holds a rule, `deny` is not among its decision constants, and approval granularity is the **resource, not the action**. Discharges ADR-0054 §1 and §3. Gate **WD-15**. Ladder **T-147…T-154** — tickets are JIT-authored per consult, so do not assume a T-NNN file exists.
- ⚠ **The WD ladder is ALLOCATION-ordered, not release-ordered** (ruled 2026-08-28). WD-1..11 built; **WD-12 reserved to containment, WD-13 to v1.0.0, WD-14 to v1.1**; v0.15.0 therefore takes **WD-15**. Renumbering would break criteria references across fourteen releases. Do not read the gap as an error.
- **Containment (B-040 / WD-12) is DEFERRED, not cancelled** (CEO, 2026-08-19), so the path after v0.15.0 is **v1.0.0 Beta 1** (where layer 2 lands *and* the surface freezes). ⚠ **Consequence, recorded deliberately:** the 2026-08-10 record held that containment is a prerequisite for layer 2. It is not a build blocker — without it, harness governance is **cooperative rather than enforced**, which is the same posture the gateway already ships and already discloses. Layer 2 may therefore proceed **provided the limitation is stated, not drifted into**. See `docs/requirements/epic_story_catalog_v4.md` §Part 4.

> ADR numbering: v3.0.1 ADRs (`docs/adr/ADR-0001.md` through `ADR-0040.md`) cover product decisions. New meta-decisions (language, tooling, frameworks) start at **ADR-0041**. **Current head: ADR-0074** — check `docs/adr/` before minting a number, and **never edit an Accepted ADR** (see §Forbidden work).

## Project doctrine
- Build P0 first. P1/P2/P3 features require an explicit ticket and an approved ADR.
- No SaaS runtime dependency.
- No AI autonomy before audit + policy + approval primitives exist.
- All major dependency choices need an ADR.
- Diff budget: ≤12 files / ≤500 LoC per ticket unless explicitly approved.
- Read the **SPEC-2026.1** baseline `docs/` before changing related code (start at `docs/executive/`, `docs/architecture/module-map.md`, `docs/security/agent-permission-model.md`). ⚠ `docs/executive/` is **stale** — 3.5 KB of May-2026 stubs that contradict Accepted **ADR-0002**; its rewrite is the next item on the canon work order.
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

**Gates that enforce the mechanical half:** `task doc-check` (referenced paths resolve; CSVs under `docs/requirements/` + `references/` are well-formed) · `task staleness-check` (the canon's self-descriptive claims are true) · `task adr-ledger-check` (the generated ADR ledger matches the corpus) · `task go-deps-check` (the core's Go dependency surface stays allowlisted — ADR-0073 §2). All four run in `task release-check`.

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
- `git init`, commits, or tags without explicit user direction. **There is no git remote — never push, including tags.**

> **Bootstrap-phase rules retired 2026-08-10.** Two bullets here gated the T-002/T-003 skeleton era — *"no business logic before T-002 and T-003 are complete"* and *"don't touch `services/`, `apps/`, or `packages/shared-types/` with non-scaffolding code."* Both preconditions were satisfied at **v0.1.0**; the repo now ships six services and two apps, and a third bullet's precondition (the `docs/security/` egress policy) was satisfied at **v0.2.0** and is re-scoped above into the standing rule it became. They were **retired rather than left as dead text**, because an implementer subagent reading them mid-ticket would be misdirected away from exactly the directories its ticket targets. The live constraints that replaced them are the **diff budget** and the **ticket + ADR discipline** in §Project doctrine — neither was weakened here.
