# Epic → Story catalog v4

**Step 1b of the post-v0.10.0 planning refresh (2026-07-24).** The sprint-plannable layer
over `requirements_v4.csv` (the ladder) and `backlog_v4.csv` (the strategic list).

**Anchoring rule:** every epic hangs off a **REQ-NNN** — no parallel taxonomy.
**Decomposition rule:** only **Beta-1 and v0.11** epics are broken into stories. Post-Beta
epics are *named and sized only* — writing stories for work a year out is waste, and the
references that would inform them are Tier-2 (pulled when the trigger fires).

**Doctrine constraints that bind every story below:** no SaaS runtime dependency · **the platform
runs no models *in-process*; it governs a runtime running beside it** (re-cut by **ADR-0073** §2,
2026-08-27 — the previous absolute wording had no ADR behind it and contradicted Accepted
ADR-0007/0021/0027/0040) · no new third-party dependency without an ADR · offline/air-gapped
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
**release epic · B-020 · size L · SPLIT 2026-08-10**

> **This epic ships in two halves.** The **hardening half** — S-21, S-23…S-30 — ships as **v0.13.0
> "Evidence Hardening"** (see `docs/releases/v0.13.0/`). The **freeze half** — **S-20 and S-22** —
> defers to **v1.0.0**, when layer 2 lands and the surface has a consumer. See §Part 4 for the
> reasoning and the renumbering map.
>
> **S-20's policy text below is ratified doctrine and is NOT re-derived — only its *stamping*
> defers.** v0.13.0 voluntarily obeys these classes (criteria CON-6) with exactly one recorded
> exception, B-035's removal, so the freeze is preceded by zero churn and can be stamped without
> rework if a buyer appears mid-cycle.

| Story | Detail | Size |
|---|---|---|
| S-20 | **→ DEFERRED to v1.0.0 (freeze half).** **OpenAPI surface freeze** + a documented compatibility policy (the fragment has grown every release since v0.5). *(The former "every other E-06 story depends on this — do it first" no longer holds: the hardening half depends on none of it, which is what made the split possible.)* Frozen: the fragment in full, the seven edge names, versioned scope strings (`approvals@1`), audit event names + chain envelope, `governed-actions@1`, installer env var names. **Deliberately NOT frozen: the `mcp-gateway` MCP tool surface** (agent plane is still learning — B-027/B-028/E-11), the web UI and `packages/ui` APIs, DB schema/migrations, event payload bodies beyond the envelope, error strings, profile support depth. Policy classes — *non-breaking in 1.x:* new paths, new optional request fields, new response fields, new output-only enum values, new event names, **strengthening a security guarantee on an existing route**; *breaking (2.0 only):* removal/rename, optional→required, narrowed response type, changed status code for an existing condition, changed meaning of an existing event name; *pre-decided:* a new **input** enum value is non-breaking because server and client ship as one artifact. | M |
| S-21 | **Security-audit capstone** across the full governance spine — cross-check against the now-named governance category (agent-governance-toolkit, SkillSpector, agentfield, clawpatrol) | L |
| S-22 | **→ DEFERRED to v1.0.0 (freeze half).** **Upgrade/migration statement** — what an operator can expect across 1.x. Meaningless without the freeze it describes. | S |
| S-23 | **Single-operator posture stated explicitly** — multi-operator ACL/SoD is post-Beta (G14); Beta-1 must say so rather than imply otherwise. **Pulled into the hardening half at the split**: it is an honest-disclosure statement, the same family as S-24 and S-30, and a threat model that states the containment boundary while staying silent on this has a hole a reviewer finds in one pass. | S |
| S-24 | **Session-revocation posture stated explicitly** — operator sessions are stateless bearer tokens with an 8h TTL; `logout` clears the cookie but does not invalidate the token; the paired tier is the product's only revocable session. Mitigations: TTL, HttpOnly/Secure/SameSite=Strict, same-origin. Fix is B-032 at **v1.1**, pre-authorised by S-20's security-strengthening clause. **Pulled into the hardening half at the split** (with S-23, S-30) — a non-revocable bearer token is a live posture a reviewer will probe, so leaving it unstated until the freeze release was the sharper omission of the two. | S |
| S-25 | **Enforcement-of-enforcement (B-038)** — every lint override, config scan root and whole-module test mock carries a test asserting it matches / covers / exercises real code. Three known: the dead SEC-7 ESLint override, Tailwind's `packages/ui/src` scan gap, the workspace-package whole-module mock. *A freeze is only credible if the enforcement protecting it demonstrably runs.* | M |
| S-26 | **The documented quickstart is a gate** — an automated run executes the published quickstart from the **built bundle on a fresh host**, not from the repo and not with repo defaults. GATE-4 §human caught two step-1 blockers no automated gate could (a bundle version hard-coded stale for eight releases, concealed because `BUNDLE_VERSION` defaulted to the same value; `setup-loopback-demo.sh` rejecting socket-activated sshd — Ubuntu 24.04's default and our primary target). **A default that matches the stale value hides the staleness.** | M |
| S-27 | **OWASP ASI01–10 control mapping** — map our controls to *OWASP Top 10 for Agentic Applications* (released 2025-12-09; note the name is "Agentic **Applications**", not "Agentic AI"). Strong: ASI06, ASI09. Gapped: ASI02/03/05 (→ S-30), ASI04 (→ S-29). **Correction of record 2026-08-19: this row previously mapped "ASI08 (→ S-28)". ASI08 is *Cascading Failures*; the audit-chain work answers **T8 — Repudiation and Untraceability**, a different taxonomy.** This was the FOURTH live instance of that error — `docs/security/threat-model.md` §1.1 named `backlog_v4.csv` B-042 and `ADR-0069:49` but missed this line, which is the one a scoping consult actually reads. Against genuine ASI08 our posture is **partial** (no blast-radius limiter across individually approved steps). **State ASI07 as out of scope** (single-plane, no agent-to-agent path) rather than leaving it silent — reviewers will ask. A published external taxonomy is worth more to a commercial buyer than any internal checklist. | M |
| S-28 | **Audit-chain external verifiability** — our chain is unkeyed SHA-256 computed and stored by `audit-service` itself, so it proves *ordering*, not *authenticity*: an attacker with write access recomputes from genesis and it verifies clean. Sign chain heads with a key the writing service does not hold (and/or periodic witness). **No new capability — this hardens the control the whole product claim rests on.** ASI08. | M |
| S-29 | **Bundle signing + SBOM** — for an air-gapped product **the offline bundle is the trust root**, and today's release gates are functional (size/version), not provenance. An operator on a disconnected network has no cryptographic way to confirm the bundle is the one we built. Release engineering, not product capability. ASI04. | M |
| S-30 | **Containment boundary stated explicitly** — our controls hold for actions traversing the gateway; **a process spawned by an approved `remote.exec` is not contained, and its own network activity is ungoverned**. Say so in the threat model. Enforcement lands at **v0.14**; **v0.13** states the boundary honestly rather than letting a red-teamer discover it. ASI02/03/05. | S |

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

## Part 4 — the Beta-1 path — **RATIFIED 2026-07-24 (CEO)**, **RE-ORDERED 2026-08-10 (CEO, post-Beta war room)**, **E-06 SPLIT 2026-08-10 (CEO, v1.0 scoping consult)**

v0.11 and v0.12 shipped as ratified. **The v1.0-and-beyond path below replaces the original
single-row v1.0 ending**, following the post-Beta roadmap war room of 2026-08-10.

**The war room left one item open: *is there a near-term buyer, pilot, or demo for v1.0?* It had
gone unanswered across three turns. Answered at the v1.0 scoping consult the same day: NO.** There
is no external recipient of the bundle in the near term. That answer **splits E-06** and renumbers
everything after it.

E-06 bundled two different kinds of work. **Hardening** (the capstone, ASI mapping, chain
authenticity, bundle provenance, the enforcement tests, the quickstart gate, the boundary
statements) is pure upside — it gates nothing and every finding it closes is one a reviewer would
otherwise find for us. **The freeze** (S-20 surface freeze, its compatibility policy, S-22's upgrade
statement) buys nothing without someone writing against the surface, and constrains every release
after it. **So the hardening ships now as v0.13.0, and 1.0 is minted when layer 2 is real.**

**This ordering is better than the war room's, not merely cheaper:** S-28 adds signature and key-id
material to audit responses, so freezing first would have frozen a surface we were about to change.

| Release | Theme | Contents | Gate |
|---|---|---|---|
| **v0.11** ✅ | **Operator Experience** | E-01 audit read surface · E-07 live refresh · E-08 charter-scope accrual | **No new WD** — WD-1..10 regression + smoke + a §human spot-check |
| **v0.12** ✅ | **Access & Onboarding** | E-02 mobile/QR · E-03 deployment profiles · E-04 onboarding · E-05a Button (E-05b cut) | **WD-11** for pairing (new security semantics) · profiles ride `task wd1` · the rest ride smoke + §human |
| **v0.13.0** ✅ | **Evidence Hardening** — *the E-06 hardening half* | S-21 capstone · S-23 single-operator · S-24 session-revocation · S-25 enforcement-of-enforcement · S-26 quickstart-as-gate · S-27 ASI01–10 mapping · S-28 audit-chain external verifiability · S-29 bundle signing + SBOM · S-30 containment boundary · B-033 (narrowed to `CreateResource`) · B-035 **removal** | The **security audit is the gate**; full WD-1..11 regression; **no new WD**; two negative proofs + a §human offline verification |
| **v0.14.0** | **Governed Tool Surface** — *re-scoped 2026-08-19* | Widen the **egress** tool surface, which today is one tool (`remote.exec`). Five further gateway tools exist (`run.launch`, `workstream.poll`, `plan.submit`, `memory.read`, `context.search`) but are **control-plane only**, so the product governs exactly one outward capability. **File transfer is absent from the repo entirely** — no SFTP, no SCP — which is the conspicuous gap for an agent-driven remote engineering OS. Rides the ADR-0057 typed catalog with policy, approval, connector lease, audit, Runs and grants **unchanged**, and reuses ADR-0049's `evidencePacketId = sha256:<bytes>` rather than inventing an evidence model. One ADR for the tool family. | **TBD at the criteria consult** — likely no new WD under the §2 gate-sizing rule, since the governance semantics are unchanged |
| **v0.15.0** | **Governed Decision Policy** — *inserted 2026-08-28; see the note below* | Give `policy-service` the rule store and evaluator it has never had. It owns two tables and neither holds a rule; `deny` is not among its decision constants; and approval granularity is the **resource**, not the action — so an unflagged host runs `remote.exec` ungoverned while a flagged one gates `remote.file.list` as hard as an arbitrary command. Discharges **ADR-0054 §1** (the third decision value) and **§3** (every decision chained — the `allow` path emits nothing today). Carries the **inference-substrate measurement** as one spike ticket. **No new dependency, daemon, edge or key.** One ADR: **ADR-0074**. | **WD-15** — a machine-evaluated `deny` is new governed capability. ⚠ WD-12/13/14 are reserved (below), so the ladder is now **allocation-ordered, not release-ordered** |
| **v1.0.0** | **Beta 1 — Governed Remote Operations** — *layer 2 lands, and the surface freezes* | Reference integrations of **OpenHands SDK** (`POST /respond_to_confirmation`) and **openworker** (`GET /v1/inbox` + `POST /v1/inbox/{id}/resolve`) via their HTTP approval endpoints, inside the v0.14 boundary. **Zero fork.** **Plus the E-06 freeze half — S-20 surface freeze + compatibility policy, S-22 upgrade statement.** | **WD-13** |
| **v1.1** | **Session & Evidence** | B-032 generalized revocation · B-034 provenance field · B-036 FK/deletion · B-037 design pass · B-031 re-ask backoff | **WD-14** |
| **v1.2+** | **Post-Beta reach** | Mobile PWA + `cockpit@1` · Mattermost as a governed connector · B-008 (agent-device) · B-024 (pgGraph) · voice (KittenTTS) | per epic |

**Renumbering map** (the war-room table used pre-split numbers; this is the translation):
war-room **v1.0** → *split* into **v0.13.0** (hardening) + the freeze half folded into **v1.0.0** ·
war-room **v1.1** Containment → **v0.14.0** · war-room **v1.2** Governed Harness → **v1.0.0** ·
war-room **v1.3** → **v1.1** · war-room **v1.4+** → **v1.2+**.

### ⭐ v0.15 INSERTED — **RATIFIED 2026-08-28 (CEO)**, at the v0.15 scoping session

**Until this date the path ran v0.14.0 → v1.0.0 and there was no v0.15 row here**; "v0.15" existed
only as a session pointer in `CLAUDE.md`, `CANONICAL`, `README`, the north-star roadmap and
ADR-0073:6, and `backlog_v4.csv` had no row targeted at it. The scoping session's pre-authoring audit
recommended inserting one, and the insertion rests on **one argument** — recorded here because
"we always ship another 0.x" is how a 1.0 never ships:

> **The boundary rule makes this work impossible to do later.** A freeze-track release *"may not add
> a governed mutation"* (§The boundary rule, below). v1.0.0 is bound by it, so a decision-plane
> change **cannot land in v1.0.0**. It lands before, or the surface freezes over a policy plane that
> cannot deny. **This is the S-28 precedent applied a second time** — the same reasoning that split
> hardening from the freeze in 2026-08-10.

**The audit finding that forced the theme:** `policy-service` has never made a policy decision. It
owns `pending_approvals` + `policy_outbox` and **no rules table**; `deny` is not among its decision
constants (*"deny is reserved for a later rule engine"* — a v0.3.0 deferral, fourteen releases ago);
and its `decide` handler validates `edge`/`target`/`action` non-empty, persists them for display, and
**never consults them** — the whole decision is `if !requires_approval → allow`, on a boolean read
from the **resource** row. Meanwhile every egress tool tells the model *"The call is policy-checked."*

⚠ **Two consequences recorded rather than drifted into.** (1) **R1 is only half-closed by v0.15** —
the decision plane gains per-action governance and a real deny, but what an *approved* command may do
afterwards is still bounded only by containment (B-040, deferred). The release notes must say so.
(2) **WD-12/13/14 are reserved to containment, v1.0.0 and v1.1**, so v0.15 takes **WD-15** and the WD
ladder is henceforth **allocation-ordered, not release-ordered**. Renumbering would break criteria
references across fourteen releases — the same class of mistake as the "layer 6" near-miss.

**Also carried, as one ticket and not a theme:** the **inference-substrate measurement** (ADR-0073
§3(a), in the ADR-0052 §OI-c *build-it-and-measure* shape). Zero inference code exists in the
as-built, and v1.0.0's flagship needs a model to be demonstrated. Measuring one release early
converts that blocker from an unknown into a number. It adopts nothing.

### ⭐ v0.14 RE-SCOPE — **RATIFIED 2026-08-19 (CEO)**, at the v0.14 checkpoint consult

**v0.14.0 is no longer Containment.** The checkpoint ran a pre-authoring audit against the as-built
and a re-scan of `references/` against a new reference collection, then re-ranked the candidate
tracks. Two decisions were taken.

**Decision 1 — v0.14.0 becomes "Governed Tool Surface."** The diagnosis is a deep governance spine
wrapped around a narrow capability surface: seven controls, but exactly one *egress* tool. Widening
that surface is the cheapest available capability release — no new daemon, no new runtime dependency,
no OpenAPI fragment change (gateway tools are off-fragment), no new evidence model, and it
parallelises one tool per implementer. It is also what makes **dogfooding** possible: the standing
blocker was always that one tool is not enough to do real work with.

**Screenshare / remote PC control was considered and deliberately deferred**, on evidence rather than
preference. It is well-scouted (REF-084 Guacamole, REF-138 MeshCentral, REF-165 RustDesk, WARPGATE)
and **pre-authorized** — ADR-0048 §Held-in-reserve carries the promotion trigger "first RDP or VNC
resource type ticket" — so it is G2-clean whenever it starts. But the audit found the work is far
larger than the data model suggests:

- ⛔ **There is no evidence model for a GUI session.** Recording is asciinema v2 (`cast.go`), a
  text-terminal format that cannot represent a framebuffer, and audit granularity is
  **Enter-delimited input lines** (`sshpty.go:174`). RDP/VNC carry mouse coordinates and raw
  keystrokes: no line boundary, no command, **nothing to approve and nothing to anchor**. The
  product's core claim does not carry over. ADR-0048 anticipated this ("Guacamole's audit format is
  not chained-hash").
- The session `mode` enum is `[terminal]` only, so it needs an OpenAPI fragment change — better
  before the v1.0 freeze than after, but it is not "no contract change."
- **Size is not the blocker.** The core bundle is 42.5 MB gz against the 150 MB OI-4 cap, and
  ADR-0052's companion-artifact pattern already ships heavy dependencies outside the core bundle.
  The real conflict is `guacd` as a **C daemon with X11 dependencies and its own CVE feed** against
  ADR-0051's static-binary systemd host.

It re-enters as a **deliberate two-release program**, sequenced after a dogfooding pass tells us
whether operators want a desktop or better tools.

**Decision 2 — Containment (B-040 / WD-12) is DEFERRED, not cancelled, and the consequence is
recorded rather than drifted into.** The 2026-08-10 record states *"Containment is the PREREQUISITE
for layer 2, not a parallel track."* On re-examination that dependency is **weaker than stated**:
without containment, governance of a third-party harness is **cooperative rather than enforced** —
the harness must choose to call us. That is a weaker *claim*, not an impossible build, and **it is
the identical posture the product already ships for its own gateway** and already discloses as a
self-finding ("our sole tool-execution path is an assumption, not a boundary").

**So layer 2 may proceed without containment, on one condition: the limitation is STATED.** v1.0.0's
criteria must say plainly that harness governance is cooperative until B-040 lands. Containment
keeps its own ADR + WD-12 whenever it is scheduled, and remains the thing that upgrades the claim
from cooperative to enforced.

⚠ **Backlog targets reconciled in the same pass:** `backlog_v4.csv` still carried the **pre-split**
war-room numbers (B-040 → v1.1, B-041 → v1.2). Both are corrected — B-041 to **v1.0.0**, B-040 to
**deferred / unscheduled**.

### Why this ordering (the three dependencies that force it)

**1. 1.0 is minted when layer 2 is real, not on a calendar.** With no buyer, a freeze is ceremony
that constrains the next ten releases. With layer 2 landed, the freeze has a consumer and is stamped
on a surface that has already been hardened. **The version number follows the product, not the other
way round.**

**Containment is v1.0-adjacent but NOT in it.** It is new governed capability (ADR + WD), and
it would place the trust root on an unmeasured dependency inside the very release whose purpose
is proving stability. **v0.13 STATES the boundary (S-30); v0.14 BUILDS it.**

**Containment is the PREREQUISITE for layer 2, not a parallel track.** You cannot safely govern a
third-party harness that is not contained — the harness would have to *choose* to call us. That
dependency forces v0.14 before v1.0; it is not a preference.

### The two ratified structural rules

**1. The stability release adds NOTHING functional.** E-05 moved out of it into v0.12.
Rationale: *if you are still shipping UI changes in the release where you freeze the API, the
freeze is theatre.* A release that changes nothing and spends itself proving stability is what
makes a version number a claim — the one a commercial buyer can be pointed at.

*(Generalized from "v1.0 adds NOTHING functional" at the E-06 split, 2026-08-10 — same
generalization as the boundary rule below, and for the same reason. **The release this now binds
is v0.13.0**, which is why B-037's design pass and every UI change are excluded from it. It binds
**v1.0.0 again** when the freeze half lands there.)*

**2. Gates are right-sized: a new WD-N is minted only when a release adds new GOVERNED
capability** — not once per release out of habit. E-01 changes a read surface (no new edge,
no new governance semantics) → no new WD. E-03 is install-level → rides `wd1`. Only E-02's
pairing token introduces new security semantics → earns **WD-11**. This saves roughly a
release's worth of ceremony across the path without weakening a single real gate.

### The boundary rule — **added 2026-08-10, load-bearing**

> **A freeze-track release may close an evidence gap on an existing governed mutation; it may not
> add a governed mutation.**

*(Generalized from "v1.0 may…" at the E-06 split: the rule binds the hardening release, which is
now v0.13.0, and continues to bind v1.0.0 when the freeze lands there.)*

This is what admits S-27/S-28/S-29 and B-033 (which chains an event on an existing route) while
excluding B-032 (adds an auth-session table, a revoke verb and a session-list read) and
containment itself. It is the operational form of structural rule #1 above. It has now held under
pressure **three** times — quote it when scope is challenged.

**The admitted-candidate count stands at FOUR: S-28, S-29, B-033, B-035.** Treat a *fifth* as the
signal that the boundary has become elastic — the next addition requires re-opening the rule, not
stretching it. **B-035 is admitted as strictly subtractive** (it *removes* a governed mutation); see
the v0.13.0 release criteria §8 for why its "last fragment change before the freeze" sequencing
constraint is satisfied by landing early rather than violated by it.

### Findings that drove the re-order (recorded 2026-08-10)

1. **"Sole tool-execution path" is an assumption, not a boundary.** Our controls hold only if the
   agent chooses to call the gateway. Our one tool is `remote.exec` — once an approved command
   runs on the target host, **that process's own network activity is entirely ungoverned**.
2. **The hash chain proves ordering, not authenticity.** Unkeyed SHA-256 computed and stored by
   `audit-service` itself; an attacker with write access recomputes from genesis and it verifies
   clean. The audit trail is the core product claim, which is what makes this the sharpest finding.
3. **No supply-chain attestation, including on the bundle** — and for an air-gapped product the
   bundle is the trust root.

**Positioning correction:** Microsoft's agent-governance-toolkit independently arrived at ~5 of
our 7 controls, *including* the credential-broker property we treat as differentiating. Our
genuinely unmatched control is the **air-gap posture**. Plan the pitch accordingly.

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
