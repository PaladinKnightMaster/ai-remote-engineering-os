# Reference audit v4 — v3 adoption decisions vs as-built, + the v4 delta

**Status:** Step 1a of the post-v0.10.0 planning refresh (Commander seat, 2026-07-24).
**Inputs:** `references_v3.csv` (184 refs, REF-001..REF-184, each carrying a recorded
*adoption decision*) + `intake-v4-2026-07-23.md` (~250 CEO-collected refs, first-pass
triaged) + as-built through **v0.10.0** (`aab261a`).
**Output:** the **gap list** (§4) that feeds Step 1b (requirements/backlog/epic-story
refresh) and Step 2 (the v1.0 "Beta 1" definition).

> **Why this audit is worth doing.** v3 is not a link dump — every row carries an
> *adoption decision*. That makes it a **scorecard**: for each decision we can ask
> *honored / overdue / invalidated / superseded*. Ten releases in, nobody has scored it.

## 1. The decision filter (binding for what leaves this audit)

A reference earns a backlog item **only** if it:

1. **fills a named gap** in the requirements ladder, **or**
2. **invalidates a decision** we already recorded, **or**
3. offers a **materially better implementation** of something already scoped.

"Interesting" is not a criterion. Everything else is archive-tier: searchable, not planned.
This filter exists because 434 references each suggesting something reasonable produces a
backlog nobody can ship.

## 2. v3 adoption decisions — scored against as-built

### 2.1 HONORED — the decision shipped

| Ref | Decision (v3) | Shipped as |
|---|---|---|
| REF-003 MCPorter | "Adopt typed MCP gateway concept for governed tool access" | **v0.3.0** `services/mcp-gateway` (ADR-0056/0057) |
| REF-180 Warden egress gateway | "no credentials reaching agents" | **v0.2.0** connector custody + broker lease (ADR-0053); the agent never sees a secret |
| REF-093 Bitwarden Agent Access SDK | "credential access with human oversight" | **v0.2.0/v0.3.0** approval gate + lease (ADR-0054/0059) |
| REF-086 Archon | "deterministic workflow, validation and approval gate" | **v0.5.0** Run engine + park/approve (ADR-0061) |
| REF-002 holaOS | "Living Workstream Engine: recurring workspaces with context/rules/history/next run" | **v0.7.0** chartered Workstreams + **v0.9.0** adaptive (ADR-0063/0065) |
| REF-010 agentmemory | "persistent memory server concept" | **v0.8.0** `internal/memory` (ADR-0064) — in-process, not a separate server (deliberate) |
| **REF-018 CocoIndex** | **"Adopt continuous incremental context index concept"** | **v0.10.0** `internal/ctxindex` (ADR-0066) — deterministic, no embeddings |
| REF-158 Playwright MCP | "tool candidate for QA/browser tasks" | **partially** — Playwright drives `task smoke` (browser DoD), not exposed as an agent tool |

### 2.2 OVERDUE — decision recorded, still unshipped, and v4 *reinforces* it

| Ref | Decision (v3) | Status | v4 reinforcement |
|---|---|---|---|
| **REF-095 Claude Code Remote Control** | **"QR/session URL mobile continuation; adapt as short-lived pairing not credential transfer"** | **NOT SHIPPED** — REQ-001 says "browser/**mobile**"; roadmap Phase 1 says "mobile controls" | v4 verified: Claude Code RC ships spacebar→QR pairing; Codex ships ChatGPT-mobile approve. **Table-stakes.** |
| REF-076 Agent of Empires | "tmux persistence, QR/passphrase access, worktrees, Docker sandboxing" | not shipped | same cluster |
| REF-120/121 Grass Expo / Grass IDE | "scan QR, control agent from phone; mobile review/approval UX" | not shipped | same cluster |
| **REF-034 PageIndex** | **"tree/reasoning retrieval as an ALTERNATIVE to vector-only RAG"** | L3 deferred as "hard-gated on embeddings" | **This challenges our own gate** — a non-embedding L3 path exists. See §3.2. |
| REF-022 gitcrawl | "local GitHub issue/PR cache + gh-compatible read shim" | REQ-015 not built | v4: codegraph, GitNexus, Potpie — the `agent-repo` edge is reserved for exactly this |
| REF-023 GitHub Agentic Workflows | "Adopt conceptually for Agent Workflow Compiler" | REQ-010 not built | v4: n8n, Flowise, dify, burr |
| REF-009 agent-desktop | "accessibility-tree-first automation" | REQ-011 not built | v4: DesktopCommanderMCP, agent-device |
| REF-004 mini-browser | "command-line browser/audit/logging for Agent Browser CLI" | REQ-012 not built | v4: **page-agent** (DOM-as-text, MIT, no multimodal — doctrine-aligned), browser-use, nanobrowser |
| REF-026 InsForge | "backend sandbox kit for agent-built apps" | REQ-017 not built | v4: agent-sandbox, bromure, sandboxes |
| REF-024 Harvey Labs | "benchmark/evaluation pattern" | REQ-016 not built | v4: OpenAI macro_evals, PaperGuru-Benchmark, SkillSpector |
| REF-149 OpenAI Symphony (**"Primary reference"**) | "issue tracker as agent control plane; isolated workspaces, continuous runs" | REQ-005 not built | v4: hermes kanban, AppFlowy, Plane |
| REF-084 Guacamole / REF-138 MeshCentral / REF-165 RustDesk | GUI remote-desktop candidates | ADR-0048 deferred guacd; **terminal-only cockpit** | v4: **warpgate** (self-hosted bastion, RDP/VNC, session recording + command-level audit — the closest prior art we have), rustdesk |
| REF-089 Authentik / REF-127 Keycloak | identity/SSO candidates | ADR-0046 named **Zitadel** as the P1 reserve; single-operator posture ships | ties to the deferred multi-operator-ACL/SoD item |
| REF-141 NetBird / REF-153 OpenZiti / REF-154 Pangolin / REF-124 Headscale | ZTNA/VPN candidates | **REQ-002 (P0!) "LAN, VPN-first, DMZ-first profiles"** — base install only | see §3.3 — a P0 requirement may be only partially met |

### 2.3 SUPERSEDED / N-A BY DOCTRINE — no action, recorded so they stop resurfacing

- **The token/context-optimization cluster** (REF-015/016/042/043/050/051/064/068/072/101/109/172):
  these optimize *an agent's own* token spend. **We run no agent and no model** — the
  customer's agent owns its context budget. Permanently N/A unless a model gateway is ever
  built (Agentic Desktop, REQ-004). Archive tier.
- **The local-model / serving cluster** (REF-116/117/129/130/132/133/139/142/143/144/145/
  161/162/166/171/177): same doctrine. They become relevant *only* if REQ-004's model
  gateway is scoped. Archive tier, flagged "REQ-004-triggered".
- **The social-signal rows** (REF-049/055/062/069/073/074): unverifiable point-in-time
  signals. Archive; do not re-review.
- **REF-005 Codex Windows Sandbox**: adopted as a *benchmark* — approvals honored (v0.2/0.3);
  OS-native sandboxing **not** built and correctly deferred (TR-2).

### 2.4 A TAXONOMY GAP the audit exposed — **skills have no requirement**

`roadmap_v3.csv` Phase 4 lists "**skill registry**" as P1 alongside harness/MCP/egress
gateway — **but there is no REQ-NNN for it**, so it has never been scoped, built, or
deferred with a trigger. Meanwhile it is one of the largest reference clusters in *both*
generations: v3 (REF-006 Pika Skills, REF-035, REF-082, REF-090, REF-091, REF-097,
REF-137, REF-152 openskills, REF-168 Superpowers, REF-176) and v4 (an entire "Skills"
section: microsoft/SkillOpt, agentskills, NVIDIA/skills, mattpocock, claude-code-templates,
skill-manager, SkillClaw…).

**Finding:** the roadmap promises a capability the requirements ladder does not carry.
Step 1b must either mint a REQ for it or record an explicit "not building this" decision.
*(Note the doctrinal angle: a skill registry means the platform distributes executable
agent instructions — a supply-chain and prompt-injection surface. v3's own rows already
said "must be reviewed before production use" / "do not allow unreviewed plugins". If we
scope it, it needs a governance ADR, not a feature ticket.)*

## 3. The v4 delta — what genuinely changed

### 3.1 Confirmed, not overturned
The v4 set **validates** the 9-layer strategy; the meaningful clusters map onto existing
REQs. Two specific validations worth recording: **governance is a named category** now
(microsoft/agent-governance-toolkit, NVIDIA/SkillSpector, anthropic/defending-code-reference-harness)
— our moat is a recognized market segment, not a private bet; and the **"runs no models"**
posture is reinforced, since the largest v4 cluster is exactly the model-serving work we
deliberately don't do.

### 3.2 NEW — a non-embedding path to Layer 3 (reopens a hard gate)
v3's **REF-034 PageIndex** proposed *tree/reasoning retrieval* as an alternative to
vector-only RAG. We have consistently written L3 as "hard-gated: any embedding/AI
dependency needs its own ADR." **That framing conflates L3 with embeddings.** If a
deterministic tree/hierarchical retrieval layer can deliver reasoning-style retrieval with
no embedding and no model, then part of L3 is *not* gated at all — it is simply unscoped.
**Action:** Step 2 should re-state the L3 gate precisely (gate the *embedding/AI
dependency*, not the *layer*), and decide whether a deterministic L3 slice is in scope.
This is the single most interesting finding in the audit.

### 3.3 NEW — REQ-002 (P0) may be only partially met
REQ-002 requires "**LAN, VPN-first, and DMZ-first** deployment profiles" and is **P0**.
As-built we ship a Linux systemd install behind Caddy (ADR-0051/0052) — but the three
*profiles* are not obviously documented/verified as distinct deployment postures, and v3
carries four ZTNA candidates (NetBird/OpenZiti/Pangolin/Headscale) that were never
dispositioned. **A P0 requirement with an unverified completion claim is a Beta-1
blocker-class question.** Step 1b must score REQ-002 honestly rather than assume v0.1
closed it.

### 3.4 NEW — the self-hosted companion external-services kit
(CEO idea + `mikeroyal/Self-Hosting-Guide`, AppFlowy, AFFiNE, Excalidraw, rustdesk,
besoeasy/gupt, ZeroNote.) The OS **integrates** self-hosted tools rather than cloning them
— consistent with the exec-brief's "integrate existing tools first." A genuinely new
blueprint idea with no v3 antecedent. Post-Beta candidate; needs a scoping decision, not a
ticket.

### 3.5 NEW — the incumbent-surface reality (verified H1-2026)
Claude Code Remote Control (spacebar→QR, mobile viewport into the *local* session), Claude
Tag (@Claude in Slack → web sessions), Codex in Slack + ChatGPT-mobile (approve risky
commands). **Consequence:** do **not** rebuild agent-session mobile control — the
customer's agent brings it. **Do** ship our own operator-surface QR/mobile (§2.2 REF-095),
because ours is self-hosted/air-gapped/credential-free where theirs is cloud-relayed.

## 4. THE GAP LIST (the output that feeds Step 1b)

Each gap names its evidence and a proposed disposition. **Dispositions are proposals for
the Step 2 v1.0 consult, not decisions.**

| # | Gap | Evidence | Proposed disposition |
|---|---|---|---|
| **G1** | **Operator-surface QR pairing + mobile approval** | REQ-001 "mobile" + roadmap Phase 1 "mobile controls" + REF-095/076/120/121 + v4 incumbent parity | **Beta-1 (v1.0) candidate** — unshipped P0 promise, doctrine-differentiated |
| **G2** | **`/audit` newest-first + sorters (+ filtering)** | §human WD-10 finding; 635 events / 601 noise / 13 pages deep | **v1.0-BLOCKING** (already flagged) |
| **G3** | **First-run / onboarding UX** | Beta-1 gap named at the v1.0-candidate consult; v4 Hermes-setup UX note | **Beta-1 candidate** |
| **G4** | **REQ-002 deployment profiles — verify or close** | §3.3; P0 requirement, unverified completion | **Beta-1 gate question** — score it honestly first |
| **G5** | **Consent modals (custom, not `window.confirm`)** | §human finding across GOV-10/GOV-11 | **Beta-1 candidate** (CEO: discuss before implementing) |
| **G6** | **Charter-detail auto-refresh** | §human finding | v0.11/Beta-1 UX pass |
| **G7** | **Skill registry — mint a REQ or record "not building"** | §2.4 taxonomy gap; roadmap promises it, ladder lacks it | **Step 1b decision** (if scoped → governance ADR) |
| **G8** | **L3 gate re-statement + deterministic-L3 scoping** | §3.2 REF-034 PageIndex | **Step 2 decision** — highest-signal finding |
| **G9** | GUI cockpit modality (RDP/VNC) | REF-084/138/165 + v4 warpgate | **post-Beta**, own ADR |
| **G10** | Browser/computer-use modality | REF-004 + v4 page-agent (MIT, DOM-as-text) | **post-Beta**, own ADR |
| **G11** | Git Intelligence (REQ-015) | REF-022/115/159 + v4 codegraph; `agent-repo` edge reserved for it; tree-sitter trigger recorded | **post-Beta** — carries the tree-sitter trigger |
| **G12** | Work Board (REQ-005) | REF-149 "primary reference" + REF-157/105/106 | **post-Beta** (per the ratified v1.0 line) |
| **G13** | Eval layer (REQ-016) | REF-024 + v4 macro_evals | **post-Beta**; note: no harness exists to *evaluate our governance claims* |
| **G14** | Identity/SSO + multi-operator ACL/SoD | REF-089/127 + ADR-0046 Zitadel reserve + the deferred SoD item | **post-Beta**, but Beta-1 must state the single-operator posture explicitly |
| **G15** | Self-hosted companion external-services kit | §3.4 | **post-Beta** blueprint item |
| **G16** | Agentic Desktop (REQ-004) + model gateway | REQ-004; unlocks the entire archived model cluster | **post-Beta v1.x** (per the ratified v1.0 line) |

## 5. Tiering (drives `references_v4.csv` + the activated watchlist)

- **Tier 1 — active watch (~20).** Direct prior art for a scoped or Beta-1-adjacent gap:
  warpgate, rustdesk, page-agent, codegraph, hermes-agent, cline-sdk, mcp-context-forge,
  agent-governance-toolkit, SkillSpector, Guacamole, MeshCentral, PageIndex, gitcrawl/
  GitNexus, Plane, OpenAI Symphony, NetBird/OpenZiti/Pangolin, Authentik/Keycloak,
  Self-Hosting-Guide, agent-sandbox, macro_evals. **Reviewed at each release boundary.**
- **Tier 2 — trigger-gated.** Relevant only when a specific REQ is scoped (e.g. the model
  cluster ⇢ REQ-004; the skills cluster ⇢ G7's outcome). **Not reviewed on a cadence** —
  pulled when the trigger fires.
- **Tier 3 — archive.** Everything else: searchable history in `references_v3.csv` +
  `intake-v4-2026-07-23.md`. **Never re-reviewed on a cadence.**

## 6. What this audit deliberately did NOT do

- It did **not** re-transcribe 434 rows into a new CSV. v3's inventory and the v4 intake
  remain the authoritative raw sources; `references_v4.csv` carries the **decision layer**
  (tier + status + disposition) over them, so there is one source of truth per fact.
- It did **not** deep-read every reference. Four v4 decision-movers were deep-read
  (codemote, page-agent, warpgate, hermes-agent, codegraph, pg_durable); the rest are
  triaged from annotations. Tier-1 items get read **when their gap is scoped**, not before.
- It made **no scope decisions**. Every disposition above is a proposal for Step 2.
