# Source Watchlist — ACTIVE

External sources this project actively tracks. **Activated 2026-07-24** (Step 1a of the
post-v0.10.0 planning refresh); the stub sat empty from P0 through v0.10.0.

Authoritative RAW inventories: `references_v3.csv` (184) + `intake-v4-2026-07-23.md` (~250).
The **decision layer** (tier / status / disposition) is `references_v4.csv`; the reasoning
is `reference-audit-v4.md`. This file is the **operating procedure** for keeping them fresh.

## Why this exists

Open-source references move fast — a repo that was "research only" last quarter may become
the obvious prior art for a gap we are about to scope. But an unbounded "re-review
everything every sprint" is a scope treadmill that produces a feature-shopping list nobody
can ship. This procedure keeps the upside and bounds the cost.

## The rules

**1. Tiers — only Tier 1 is on a cadence.**

| Tier | What | Review |
|---|---|---|
| **1** | Direct prior art for a **scoped or Beta-1-adjacent** gap (~20 sources; listed in `references_v4.csv`) | **Each release boundary** |
| **2** | Relevant only when a specific trigger fires (e.g. the model cluster ⇢ REQ-004; the skills cluster ⇢ G7's outcome) | **Never on a cadence** — pulled when the trigger fires |
| **3** | Archive: history, superseded, doctrine-N/A, social signals | **Never re-reviewed** |

**2. Cadence — release boundaries, not sprints.** Tier-1 review happens once per release,
at the scoping consult, **time-boxed to a single session**. Not continuous, not per-sprint.

**3. The decision filter — what may leave a review as a backlog item.** A reference earns a
backlog item **only** if it:
1. **fills a named gap** in the requirements ladder, **or**
2. **invalidates a decision** we already recorded, **or**
3. offers a **materially better implementation** of something already scoped.

"Interesting" is not a criterion. A review that produces zero backlog items is a **successful
review**, not a wasted one.

**4. Deep-read discipline.** Tier-1 sources are read **when their gap is scoped**, not
speculatively. Annotations + prior knowledge are sufficient for triage.

**5. Recording.** Each review updates `Last reviewed` below and, if anything changed,
appends to `reference-audit-v4.md`. A reference that stops being prior art for any live gap
**drops to Tier 2 or 3** — the list must be allowed to shrink.

## Tier-1 watch (reviewed at each release boundary)

Full rows, with status + disposition, live in `references_v4.csv`. Grouped by the gap they
serve (gap ids from `reference-audit-v4.md` §4):

| Group | Sources | Gap | Last reviewed |
|---|---|---|---|
| Mobile / QR pairing | Claude Code Remote Control (REF-095), Agent of Empires (REF-076), Grass Expo/IDE (REF-120/121) | G1 | 2026-07-24 |
| GUI cockpit (RDP/VNC) | **warpgate**, Guacamole (REF-084), MeshCentral (REF-138), RustDesk (REF-165) | G9 | 2026-07-24 |
| Browser / computer-use | **page-agent**, mini-browser (REF-004) | G10 | 2026-07-24 |
| Context / code intelligence | **codegraph**, CocoIndex (REF-018), gitcrawl (REF-022), GitNexus (REF-115) | G11 | 2026-07-24 |
| **L3 non-embedding retrieval** | **PageIndex (REF-034)** | **G8** | 2026-07-24 |
| Harness / runtime | **hermes-agent**, Cline SDK (REF-099) | — (patterns only) | 2026-07-24 |
| MCP gateway | mcp-context-forge, fastmcp (the ADR-0056 recorded option) | — | 2026-07-24 |
| Governance / security | agent-governance-toolkit, SkillSpector, defending-code-reference-harness | — (v1.0 audit cross-check) | 2026-07-24 |
| Work board | OpenAI Symphony (REF-149), Plane (REF-157) | G12 | 2026-07-24 |
| Deployment profiles (ZTNA) | NetBird (REF-141), OpenZiti (REF-153), Pangolin (REF-154), Headscale (REF-124) | G4 | 2026-07-24 |
| Identity / SSO | Authentik (REF-089); Zitadel (the ADR-0046 reserve). Keycloak REJECTED — do not re-litigate | G14 | 2026-07-24 |
| Self-hosted companion kit | Self-Hosting-Guide, AppFlowy, AFFiNE, Excalidraw, gupt, ZeroNote | G15 | 2026-07-24 |
| Eval | Harvey Labs (REF-024), OpenAI macro_evals | G13 | 2026-07-24 |
| Sandbox | kubernetes-sigs/agent-sandbox, bromure | — (TR-2 deferral) | 2026-07-24 |

## Tier-2 triggers (pulled only when the trigger fires)

| Cluster | Trigger |
|---|---|
| Skills / skill registry | **G7** — Step 1b mints a REQ or records "not building". If scoped: a **governance ADR**, not a feature ticket (distributing executable agent instructions is a supply-chain + injection surface) |
| Local models / serving / model gateway | **REQ-004** Agentic Desktop scopes a model gateway |
| Process / meta (agent-team patterns, skill hygiene, adversarial review) | Feeds the **agency kit / CLAUDE.md build process** — never the product backlog |

## Review log

| Date | Scope | Outcome |
|---|---|---|
| 2026-07-24 | **Activation + full v3↔v4 audit** (Step 1a, post-v0.10.0) | 184 v3 adoption decisions scored against as-built; ~250 v4 refs triaged; **16 gaps (G1–G16)** produced for Step 1b. Highlights: 8 v3 decisions HONORED (incl. CocoIndex ⇢ v0.10 L1); the QR/mobile decision **OVERDUE and now table-stakes**; a **taxonomy gap** (skill registry promised by the roadmap, absent from the ladder); **REQ-002 (P0) completion unverified**; and **PageIndex challenges our own L3 "hard gate" framing** (deterministic tree retrieval needs no embedding). |
