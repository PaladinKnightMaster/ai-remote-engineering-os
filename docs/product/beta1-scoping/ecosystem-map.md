# v1.0.0 Beta 1 — scoping input: the ecosystem map

**Commander seat, 2026-11-16.** ⚠ **A scoping INPUT, not a scope.** It decides nothing; the CEO ranks §7
and the ranking becomes Beta 1's criteria. Companion input:
[`terminal-inventory.md`](terminal-inventory.md) — every journey, step by step, browser vs terminal.

## 1. The question

The CEO's direction after shipping v0.17.0 (2026-11-16): the product is **still too terminal-based**, and
it is **strong only in one slice** (the governed agent workspace). Before an MVP release and an external
tester, look at the **whole ecosystem** and decide what Beta 1 must add.

## 2. What "the ecosystem" already means here — decided, not open

These are Accepted decisions; Beta 1 scoping works inside them, or files a new ADR.

- **One core, companion products, governed substrates** (ADR-0002, ADR-0073). A component is *in* the
  ecosystem **iff** its consequential actions land on the one signed ledger an outsider can verify
  offline. Inference runtimes, execution sandboxes and third-party agent harnesses are **substrates**:
  operator-provided, governed, never forked [read: `docs/executive/vision.md`].
- **The strategy in one line: own the seam every agent must pass through** — don't adopt an agent
  [read: `docs/executive/strategy.md`].
- **What actually differentiates, after measurement:** governance alone does not (Microsoft's
  agent-governance-toolkit and CopilotKit/OpenBot both govern this seam). **Governance + air-gap + one
  externally verifiable ledger** does; none of the sixteen projects read in the v7 pass is both governed
  and air-gapped [read: `docs/executive/competitor-map.md`].

⇒ **"More ecosystem" must not mean "more features like everyone else's."** Every addition has to either
pass the ledger test (a companion) or be governed through the seam (a substrate). That is what keeps the
air-gap + ledger differentiator intact as the product widens.

## 3. The nine product layers — where each stands

The strategy's frozen layer list (`docs/executive/strategy.md`, "strategy layer N"). Status from the
module map and release record.

| Strategy layer | Status | What exists |
|---|---|---|
| 1 Remote Execution Cockpit | **Built (terminal only)** | Recorded SSH terminal sessions since v0.1.0. **No remote desktop, no SFTP, no session replay** (inventory J6, J23) — although `README.md` advertises SFTP |
| 2 Agentic Desktop | **Not started** | Open P1 item |
| 3 Agentic Work Board | **Not started** | A companion product; open P1 item |
| 4 Living Workstreams | **Built** | Runs, delegation grants, chartered and adaptive workstreams (v0.5–v0.9) |
| 5 Harness Runtime | **Seam built, integration not started** | The MCP gateway governs any MCP client (v0.3+); B-041 (OpenHands / openworker integration) is the presumed Beta 1 flagship, NOT-STARTED [read: `docs/spike/b041-remaining-work.md`] |
| 6 Knowledge / Memory / Context | **Built** | Persistent scoped memory (v0.8) + live context index (v0.10); memory L3 deferred and hard-gated |
| 7 Code / Git Intelligence | **Not started** | The `agent-repo` edge is reserved, unused |
| 8 Tool / Sandbox / Gateway | **Gateway built; sandbox deferred** | Typed gateway, egress tools, policy decisions, approval (v0.3–v0.15). Containment (B-040) deferred — governance of a harness is *cooperative*, not enforced |
| 9 Evaluation | **Not started** | Open P2 item |

**Reading it plainly:** depth in layers 1 (terminal), 4, 6 and 8 (gateway); **nothing yet in 2, 3, 7, 9;
the seam but not the integration in 5.** That matches the CEO's read.

## 4. Who uses it — roles and their state today

| Role | Today | Gap for an MVP |
|---|---|---|
| **Operator** (installs, runs, approves) | Install = 3 server lines + browser (v0.17); daily work mostly browser | Remaining terminal steps — see the inventory |
| **Approver on a phone** | Paired, revocable approvals-only session (v0.12) | — |
| **Agent integrator** (connects an AI agent/harness) | Any MCP client can call the gateway with an agent-tool token | ⚠⚠ **The weakest journey in the product** (inventory J7, ranked #1): the token comes only from `POST /v1/agent-tokens` behind a cookie login + CSRF — **a script holding the operator's password** — lasts **5 minutes with no refresh**, has **no cockpit screen and no integrator guide** (the only example is a test script), and the harness must also be made to trust the private CA. **Any agent integration, B-041 included, is blocked in practice until this exists** [ran: grep — no `apps/web` caller of `/v1/agent-tokens`; `AgentTTL` in `packages/go/auth`] |
| **Auditor / outsider** | Offline verifier for the signed chain (v0.13) | — |
| **A second human / a team** | **Not possible.** One operator account (ADR-0046); adding users is ADR-0046's named Zitadel trigger | The north star says "under company governance" — a single-operator product is a personal tool, not a company one |

## 5. Agents, models, targets — the three substrate edges

- **Agents.** The 2026 market has 40+ harnesses churning quarterly; the strategy is to integrate behind
  adapters, never fork. B-041 picked two with first-class block-and-wait protocols (OpenHands
  software-agent-sdk, openworker); both are **`[inherited:]` README-level intake** and openworker
  phones home by default — **a deep verification pass is step 1** before any ADR
  [read: `docs/spike/b041-remaining-work.md` §4–§5]. Mainstream coding agents that already speak MCP
  are clients of the existing gateway today.
- **Models.** Operator-provided substrate; T-153 measured that an offline runtime can serve inference
  with the network severed, so a `lan-only` air-gapped demo is now feasible.
- **Targets.** **SSH to Linux only.** No Windows (RDP/WinRM), containers, Kubernetes, cloud APIs,
  databases or Git forges. ⚠ Each new target type is a new egress path, and **a new egress path needs its
  own ADR** (CLAUDE.md §Forbidden work) — target breadth is ADR-heavy by design.

## 6. Constraints any Beta 1 scope must carry

1. **A stranger test is a precondition** (v0.17.0 waived HUM-1; `docs/releases/v0.17.0/hum1-run.md`).
2. **Governance of third-party harnesses is cooperative** until B-040 lands — it must be *stated*.
3. **The API surface is not frozen in Beta 1** (ratified 2026-09-01); v1.0.0 final carries its own freeze.
4. **Resolve DOC-17 first** — the epic catalog still contains stale text calling containment a prerequisite.
5. **Deliberate terminal steps stay** unless an ADR changes them (the operator-run trust split, root-only
   setup code, no self-service signup or reset UI) — see the inventory's "deliberate" list.

## 7. Candidate themes for Beta 1 — for the CEO to rank

The inventory counted **150 steps across 25 journeys: 46 browser, 60 terminal, 44 not possible at all**
[inherited: `terminal-inventory.md` — counts generated from its column 3]. Its findings reshape the themes:
some "terminal" problems are really **missing basics**, and the agent journey is broken before any
harness integration starts.

| # | Theme | What it contains (inventory refs) | Size / needs |
|---|---|---|---|
| **A1** | **Agent onboarding in the browser** | A cockpit screen to create an agent identity and its credential **without the operator's password in a script**; a usable token lifetime/refresh model; the CA-trust step for the harness; an integrator guide (J7, ranked #1) | Medium; **needs an ADR** — ADR-0046's 5-minute agent TTL and minting model are touched |
| **A2** | **MVP basics that do not exist** | Sign-out button; change password; **a general "add target" form** with host-key capture (today the cockpit can only create the demo); edit a target; test connection; delete a target that has sessions (J4, J21, b-list) | Small–medium; mostly no ADR |
| **A3** | **Day-2 operations** | Upgrade guide and path; backup/restore; health screen; logs; uninstall (J13, J14, J18, J19) | Medium; check the deliberate list |
| **A4** | **Evidence in the browser** | Chain-integrity screen, audit export button, a verifier for Windows/macOS (J12) — keeping the trust root outside the server (ADR-0069) | Medium |
| **B** | **Governed harness integration (B-041)** | A real agent doing governed work end to end; **depends on A1** | Release-sized; deep verification pass first; new ADR + WD-13 |
| **C** | **Teams: more than one operator** | Users, roles, per-person audit attribution; SSO later | Large; ADR (ADR-0046's Zitadel trigger fires) |
| **D** | **Agentic Work Board** | Strategy layer 3, a companion product on the ledger | Large; ADR-0073 membership + new ADR |
| **E** | **More target types** | Windows/RDP, containers, Kubernetes; remote desktop | Large; one egress ADR each |
| **F** | **Containment (B-040)** | Enforced, not cooperative, harness governance | Large; WD-12 + ADR |

**Deliberately terminal, and staying so unless an ADR says otherwise** (inventory §c): the operator-run
trust split for `authorized_keys` (D1), the root-only setup code (D2), no self-service signup or reset UI
(D3), the browser never accepting a private key (D5), release verification before extraction (D6), and
the audit trust root held off the server (D7). **These are not UX debt** — removing one is a security
decision.

### Commander's recommendation

**Beta 1 = A1 + A2 + B, gated by the stranger test.** A3 and A4 if they fit; C next release.

- **A1 is now a prerequisite, not polish.** Without it no agent — ours or a harness — can be connected
  without a script holding the operator's password. B-041 built on today's token flow would ship a demo
  nobody can reproduce.
- **A2 is what a stranger hits first.** The stranger test will fail on "how do I add my own server?" and
  "how do I sign out?" long before it reaches anything agentic.
- **B** remains the flagship: it is what makes the MVP *do* something.
- **C** is the gap between a personal tool and a company one, but it reopens the auth model; it should
  follow A1 (which already touches that model) rather than run beside B.
- **D, E, F** are each release-sized and ADR-heavy.

## 8. What the CEO decides next

1. **Rank §7** (or reshape it).
2. **Confirm the MVP sentence**, e.g. *"An operator installs it in three lines, adds their own server and
   connects one agent from the browser, and watches that agent do governed work on that server — with every
   action on a ledger an outsider can verify offline."*
3. Then the Commander runs, in order: DOC-17; the A1 ADR (agent onboarding / credential model); the B-041
   deep verification pass; the Beta 1 criteria.
