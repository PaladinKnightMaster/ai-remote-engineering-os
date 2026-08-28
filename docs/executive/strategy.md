# Strategy — go-to-market and defensibility

> **Rewritten 2026-08-27** (canon work order, pass 3). **Narrates and links; holds no data.**

## Do not build everything as MVP

The architecture defines the long-term platform; the shipping sequence stays disciplined. Fifteen
releases have shipped. The live plan is
[`requirements/epic_story_catalog_v4.md`](../requirements/epic_story_catalog_v4.md) §Part 4 — **not
this file**.

## The strategy, in one line

**Don't adopt an agent. Own the seam every agent must pass through.** Ratified 2026-08-10; formalised
as a boundary rule in **ADR-0073**. Validated by measurement, not preference: **40+ agent harnesses
exist and churn quarterly**, so forking one picks a loser. The durable positions are
MCP-as-tool-protocol, the approval moment, and the evidence trail.

## ⚠ Product layers — FROZEN ANCHOR, cited by three Accepted ADRs

**Do not renumber, reorder, or remove this list.** **ADR-0064** §Consequences and **ADR-0066**
§Consequences cite *"strategy layer 6"* by number, and **ADR-0063** cites this document's
defensibility phrasing. Gate **G2** forbids editing an Accepted ADR to repoint a citation, so these
numbers are load-bearing and permanent.

1. Remote Execution Cockpit — secure GUI/terminal access
2. Agentic Desktop — local PC worker with phone dispatch
3. Agentic Work Board — ticket-native agent execution
4. Living Workstreams — recurring jobs with state and memory
5. Harness Runtime — reliable model/tool execution
6. **Knowledge / Memory / Context** — fresh context and governed memory ← *cited by ADR-0064, ADR-0066*
7. Code / Git Intelligence — repo, issue, PR and CI graph
8. Tool / Sandbox / Gateway — safe execution boundaries
9. Evaluation — regression and domain benchmark systems

⚠ **"Layer N" is ambiguous across this repo and must never be written bare.** Three incompatible
schemes are live: **this** list; the memory architecture's **L1/L2/L3**
([`architecture/memory-context-architecture-v3.md`](../architecture/memory-context-architecture-v3.md));
and the war-room product layers where *"layer 2"* means the **governed harness (B-041)** and defines
v1.0.0. Under **this** list layer 2 is the Agentic Desktop. **Always qualify: "strategy layer 6",
"memory L2", "war-room layer 2."**

## Positioning — corrected twice by measurement

**Governance is no longer the differentiator.** Two independent findings, both recorded:

- **Microsoft's `agent-governance-toolkit` independently arrived at ~5 of our 7 controls**, including
  the credential-broker property once treated as differentiating (2026-08-10).
- **`CopilotKit/OpenBot` occupies the same seam *with* governance** — per-agent isolation, CEL policy,
  pre-execution decision, audit trail (v7 pass, 2026-08-27). The earlier record held that the
  nearest competitor occupied this seam *without* governance. That is no longer true.

**What survived the measurement:** across all sixteen repos read in the v7 pass, **not one is both
governed and air-gapped.** OpenBot and `apache/maka` need cloud or model keys; `magnitude` is offline
but ungoverned. **The claim narrows to governance + air-gap + one signed ledger** — and per ADR-0073
§1 the ledger's value is that an outsider *holding no database access* can check it, **not** that it
is unforgeable by an insider.

Plan the pitch accordingly. Overstating this is the specific failure mode the review record shows.

## Defensibility

**Strong** — self-hosted/offline runtime · DMZ/VPN deployment profiles · credential custody ·
informed approval in the path · externally verifiable evidence · **integration** of remote execution
+ agent execution + work management.

**Weak on their own** — generic remote desktop · generic coding agent · generic Jira clone ·
generic wiki · generic Git forge.

⚠ **The resolution of that tension is the one-ledger spine.** A wiki is weak. A wiki whose every
edit, agent read, and context extraction lands on the same signed chain as the approval that
authorised it is not a wiki. **This is the test every proposed companion product must pass**
(ADR-0073 §1) — and it is a real cost, not a slogan: more engineering per pillar than a conventional
wiki or board would take.

## Business model options

Open-core self-hosted · enterprise support · per-seat/per-device licence · air-gapped deployment
package · professional services for network deployment. A hosted demo is possible later but **never a
runtime requirement**.

## Competitive inventory

**Not duplicated here.** The live inventory with adoption verdicts is
[`references/references_v4.csv`](../../references/references_v4.csv); the newest batch is
[`references/intake-v7-2026-08-27.md`](../../references/intake-v7-2026-08-27.md). The previous
version of this file carried a hand-written competitor list that went fifteen months without
revision — **that is why this section is a pointer.**
