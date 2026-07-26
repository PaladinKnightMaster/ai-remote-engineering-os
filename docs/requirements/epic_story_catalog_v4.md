# Epic → Story catalog v4

**Step 1b of the post-v0.10.0 planning refresh (2026-07-24).** The sprint-plannable layer
over `requirements_v4.csv` (the ladder) and `backlog_v4.csv` (the strategic list).

**Anchoring rule:** every epic hangs off a **REQ-NNN** — no parallel taxonomy.
**Decomposition rule:** only **Beta-1 and v0.11** epics are broken into stories. Post-Beta
epics are *named and sized only* — writing stories for work a year out is waste, and the
references that would inform them are Tier-2 (pulled when the trigger fires).

**Doctrine constraints that bind every story below:** no SaaS runtime dependency · the
platform runs no models · no new third-party dependency without an ADR · offline/air-gapped
must keep working · diff budget ≤12 files / ≤500 LoC per ticket · every UI change needs
browser-rendered evidence · consent-surface changes need a re-verified DoD.

---

## Part 1 — Beta-1 (v1.0) epics

### E-01 · Audit read surface — **v1.0-BLOCKING**
**REQ-003 · B-014 · gap G2 · size L**

*Why:* the audit trail **is** the product's core evidence claim. Measured live during the
§human WD-10: **635 events, 601 (95%) ticker noise, newest entries 13 "next page" clicks
deep.** An operator who cannot reach recent events undermines the governance proposition a
commercial Beta 1 makes. This is the one finding I would block 1.0 on.

| Story | Detail | Size |
|---|---|---|
| S-01 | **Reverse cursor in `audit-service`** — the store reads `WHERE seq > $1 ORDER BY seq ASC`; add a descending path + API params. Touches the evidence surface, so the chain-integrity semantics of paging backwards need stating. | M |
| S-02 | **Newest-first default** in `Audit.tsx` + cursor plumbing + codegen | S |
| S-03 | **Per-column asc/desc sorters** | S |
| S-04 | **Server-side filtering** (actor / outcome / target). NOTE v0.1's deliberate no-filter rule — *"filtering a partial page would misrepresent the chain"* — must be answered, not ignored: filter server-side over the whole chain, never client-side over a page. | M |
| S-05 | **Decision: ticker-noise policy.** `plan.requested`/`plan.expired` are content-free lifecycle facts; suppressing or collapsing them **changes the evidence record**. This is an **ADR question**, not a UI toggle. | Decision |

*Gate:* an operator finds the most recent governed action in **one screen, no paging**.

### E-02 · Operator mobile & QR pairing
**REQ-001 · B-015 · gap G1 · size M**

*Why:* an **unmet P0 promise** — REQ-001 says "browser/**mobile**", roadmap Phase 1 says
"mobile controls", and v3 REF-095 recorded the pairing pattern. v4 verified both incumbents
ship it. **Scope discipline:** do *not* rebuild mobile control of a Claude/Codex agent
session — the customer's agent brings that. Build **our operator surface**, which is
differentiated precisely because it is self-hosted and air-gapped where theirs is
cloud-relayed.

| Story | Detail | Size |
|---|---|---|
| S-06 | **Short-lived pairing state** — a pairing token that transfers *session state only, never credentials* (the exec-brief's non-negotiable). Revocable, TTL-bounded, audited. | M |
| S-07 | **QR render** in the cockpit for pairing | S |
| S-08 | **Mobile-responsive approval view** — the approve/deny surface is the highest-value mobile act (a governance product is fundamentally about the async human-in-the-loop moment) | M |
| S-09 | **Configurable/disable-able** — some locked-down SOCs forbid phones on the floor; mobile must be an offered capability, not an assumed one | S |

### E-03 · Deployment profiles — **P0 gap**
**REQ-002 · B-016 · gap G4 · size M**

*Why:* `deep-dive/03-network-topologies.md` names `lan-only` / `vpn-first` / `dmz-first`
and states *"all documentation and installers must identify which profile is active."*
**Verified 2026-07-24: `install.sh` has no profile concept and no config or env carries one
anywhere.** A **P0** requirement with an unmet implementation clause.

| Story | Detail | Size |
|---|---|---|
| S-10 | **Profile flag** carried by `install.sh` + persisted in env; the stack reports its active profile | M |
| S-11 | **Profile-aware docs/runbook** — install + verification per profile | S |
| S-12 | **Per-profile verification** — at minimum `lan-only` proven end-to-end; VPN/DMZ documented with an honest support statement | M |
| S-13 | **Decision: dependency or documentation?** v3 carries four undispositioned ZTNA candidates (NetBird / OpenZiti / Pangolin / Headscale). Beta-1 likely *documents* the topology and does **not** adopt a dependency. | Decision |

### E-04 · First-run onboarding
**REQ-022 · B-017 · gap G3 · size M**

*Why:* no first-run experience exists — an operator must read docs and hand-seed resources.
The product's value is **invisible until a governed action runs end to end**; for a
commercial Beta 1 that is the whole demo.

| Story | Detail | Size |
|---|---|---|
| S-14 | **Guided first-run** — provisioning checklist reaching a working operator + resource | M |
| S-15 | **Demo path** — a seeded target and a one-click governed action so the loop is visible immediately | M |
| S-16 | **Quickstart doc + reference agent** — a minimal MCP client so the agent plane is exercisable without writing one | M |

### E-05 · Consent modal component — **design-gated**
**cross-REQ (GOV) · B-018 · gap G5 · size M**

*Why:* GOV-10 (approve/deny; memory share) and GOV-11 (context-source register/remove) and
the redact verbs all gate on native `window.confirm`. The copy is correct and the gate
works — the presentation should be ours. **CEO decision recorded: discuss and confirm the
approach before implementing.**

| Story | Detail | Size |
|---|---|---|
| S-17 | **Modal component** in `packages/ui` (design-system preflight first; no ad-hoc component) | M |
| S-18 | **Migrate the call sites** — GOV-10 + GOV-11 + redact | S |
| S-19 | **Re-verify** browser-evidence DoD + smoke — consent surfaces do not ship on jsdom | S |

*Caveat to carry:* the Claude Desktop Browser pane auto-dismisses `window.confirm`; a custom
modal would actually make these surfaces **testable in-pane**, which is a secondary win.

### E-06 · v1.0 stability posture — **what makes 1.0 a claim**
**release epic · B-020 · size L**

| Story | Detail | Size |
|---|---|---|
| S-20 | **OpenAPI surface freeze** + a documented compatibility policy (the fragment has grown every release since v0.5) | M |
| S-21 | **Security-audit capstone** across the full governance spine — cross-check against the now-named governance category (agent-governance-toolkit, SkillSpector) | L |
| S-22 | **Upgrade/migration statement** — what an operator can expect across 1.x | S |
| S-23 | **Single-operator posture stated explicitly** — multi-operator ACL/SoD is post-Beta (G14); Beta-1 must say so rather than imply otherwise | S |

---

## Part 2 — v0.11 (small, near-term)

### E-07 · Live detail refresh
**REQ-007 · B-019 · gap G6 · size S** — targeted polling while a detail panel is open, or an
event push. Do **not** poll globally: cheap at the 60s production floor, thrashing at a 5s
demo interval.

### E-08 · Charter-scope accrual refinement
**REQ-014 · B-029 · size S** — adaptive Runs accrue at `run:<id>` so the charter-detail
memory widget (querying `workstream:<id>`) never shows them. Recorded at T-095; a semantic
fix, not a containment bug.

---

## Part 3 — post-Beta epics (named + sized only; no stories yet)

| Epic | REQ | Backlog | Gap | Size | Trigger / note |
|---|---|---|---|---|---|
| E-09 GUI cockpit (RDP/VNC) | REQ-001 | B-021 | G9 | XL | Own ADR. Prior art: **warpgate** (bastion + session recording + command-level audit), Guacamole, MeshCentral, RustDesk |
| E-10 Browser computer-use | REQ-012 | B-022 | G10 | L | Own ADR. **page-agent** is doctrine-aligned (DOM-as-text, no multimodal, MIT, MCP server) |
| E-11 Git Intelligence | REQ-015 | B-011 | G11 | XL | Owns the reserved **`agent-repo`** edge; carries the **tree-sitter trigger** (when agents gain code-EDITING) |
| E-12 Agentic Work Board | REQ-005 | — | G12 | XL | v3's PRIMARY reference (Symphony); integrate-vs-clone question (Plane) |
| E-13 Eval layer | REQ-016 | B-012 | G13 | L | Nothing currently evaluates our *governance* claims |
| E-14 Identity/SSO + multi-operator ACL | — | B-025 | G14 | L | ADR-0046 Zitadel reserve; Keycloak **rejected** — do not re-litigate |
| E-15 Skill registry | REQ-021 | B-023 | G7 | L | **ADR-gated** — distributing executable agent instructions is a supply-chain + injection surface |
| E-16 Deterministic Layer-3 | REQ-023 | B-024 | G8 | M | The gate is the **embedding/AI dependency**, not the layer — a tree-retrieval slice is scopeable |
| E-17 Self-hosted companion kit | — | B-026 | G15 | M | **Integrate**, don't clone |
| E-18 Agentic Desktop | REQ-004 | — | G16 | XL | Separate product surface; unlocks the archived model cluster |
| E-19 Agent memory writes | REQ-014 | B-027 | — | M | Trigger: a composer need read-only context cannot express |
| E-20 Unattended adaptive execution | REQ-007 | B-028 | — | M | Trigger: operational trust evidence **+ a designed trust primitive** |
| E-21 Workflow compiler / Desktop automation / Backend sandbox / Domain packs | REQ-010/011/017/018 | B-006/008 | — | XL | Later P2 track |
| E-22 Companion UX / Explain mode | REQ-019/020 | B-013 | — | M | P3 — correctly last |

---

## Part 4 — the Beta-1 path — **RATIFIED 2026-07-24 (CEO)**

| Release | Theme | Contents | Gate |
|---|---|---|---|
| **v0.11** | **Operator Experience** | E-01 audit read surface *(v1.0-BLOCKING)* · E-07 live refresh · E-08 charter-scope accrual | **No new WD** — WD-1..10 regression + smoke + a §human spot-check |
| **v0.12** | **Access & Onboarding** | E-02 mobile/QR · E-03 deployment profiles · E-04 onboarding · E-05 consent modals | **WD-11** for pairing (new security semantics) · profiles ride `task wd1` · the rest ride smoke + §human |
| **v1.0.0** | **MVP Beta 1** | **E-06 only** — OpenAPI freeze, security-audit capstone, compat policy, single-operator posture stated, full WD regression sweep | The **security audit is the gate** |

### The two ratified structural rules

**1. v1.0 adds NOTHING functional.** E-05 moved out of v1.0 into v0.12. Rationale: *if you
are still shipping UI changes in the release where you freeze the API, the freeze is
theatre.* A 1.0 that changes nothing and spends itself proving stability is a credible 1.0 —
the version a commercial buyer can be pointed at.

**2. Gates are right-sized: a new WD-N is minted only when a release adds new GOVERNED
capability** — not once per release out of habit. E-01 changes a read surface (no new edge,
no new governance semantics) → no new WD. E-03 is install-level → rides `wd1`. Only E-02's
pairing token introduces new security semantics → earns **WD-11**. This saves roughly a
release's worth of ceremony across the path without weakening a single real gate.

### Recorded risks

- **v0.12 is the fattest release** (four epics). **E-05 is the designated drop candidate** if
  it strains — polish on a gate that already works correctly; dropping it does not touch the
  Beta-1 thesis.
- **E-01 carries an ADR question, not just tickets:** the ticker-noise policy
  (`plan.requested`/`plan.expired` are content-free lifecycle facts; suppressing or
  collapsing them changes the evidence record). Budget a consult inside v0.11.
- **E-03 depth is bounded:** `lan-only` proven end-to-end + an honest support statement for
  VPN/DMZ. **No ZTNA dependency is adopted in Beta-1** (the four v3 candidates stay
  undispositioned Tier-1 watch items).
