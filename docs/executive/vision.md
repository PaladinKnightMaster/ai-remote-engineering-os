# Vision — the AI Remote Engineering OS

> **Rewritten 2026-08-27** (canon work order, pass 3). The prior version described **one combined
> product**, which contradicted **ADR-0002** (Accepted 2026-05, never superseded) for fifteen months
> and is the drift **ADR-0073** was written to stop. ADR-0073 §Context describes this file's former
> state in the present tense; **that description is now history and is deliberately preserved** — an
> ADR records the situation at its decision moment (ADR-0072 §5).
>
> **This file narrates. It does not hold data.** Every fact below lives in exactly one authoritative
> place and is linked, never copied — duplication is precisely how the previous version drifted.

## Product sentence

A **self-hosted, air-gap-capable governance plane** for human and AI work: humans and agents operate
internal machines, run recurring workstreams, share governed memory, and use approved tools — and
**every consequential action lands on one signed evidence ledger that an outsider can verify offline**.

## The wedge

Do not compete as another cloud coding agent, another remote-desktop tool, or another multi-agent
chat UI. Compete as the **self-hosted, restricted-network, evidence-driven operating system for
human + AI work** — the place governance happens, not the place the model runs.

## The shape: one core, companion products, governed substrates

Per **ADR-0002** (the product split) and **ADR-0073** (the membership test). This is **not an
all-in-one application**; it is a core with a defined boundary.

**The spine — one ledger.** A component is *in the ecosystem* **iff** its consequential actions are
recorded on the one signed evidence ledger, verifiable **offline by an outsider holding no database
access**. That is the membership test, and it is what makes this an ecosystem rather than a suite.

| Tier | What it is | Examples |
|---|---|---|
| **Core** *(shipped)* | The governance plane: cockpit, typed MCP gateway, policy, approval, credential broker, and the ledger | `services/*` — see [module map](../architecture/module-map.md) |
| **Companion products** *(ours; must pass the membership test)* | Surfaces that write provable evidence to the ledger | work board, knowledge base, GUI cockpit, mobile approver, model-runtime appliance, companion UX |
| **Substrates** *(governed, not owned)* | Things the system needs that **cannot** meet the test | **inference** runtime · **execution** sandbox · third-party **agent** harness / IDE |

**Substrates are operator-provided by default.** Shipping one ourselves requires a *measured*
feasibility finding plus its own ADR — **preference is not a trigger** (ADR-0073 §3). Third-party
agent runtimes are integrated **behind adapters, never forked** (ADR-0015); existing remote-desktop
and forge tools are integrated before anything is built (ADR-0004, ADR-0010).

**Air-gap is a property of the *system*, not the product** (ADR-0073 §4): any offline-complete claim
must name every artifact the deployment requires.

## Differentiators — narrowed to what survived measurement

The v7 reference pass (`references/intake-v7-2026-08-27.md`, 17 sources read 2026-08-27) tightened
this list rather than lengthening it. See [strategy](strategy.md) for the evidence.

- **Air-gapped operation** — the one control no surveyed competitor reproduces.
- **Externally verifiable evidence** — signed chain heads plus an offline verifier needing no
  database and no network (ADR-0069). ⚠ **External verifiability, not separation of duties**:
  `audit-service` holds its own signing key, a residual ADR-0069 §D5 states deliberately.
- **Credential custody** — the agent never sees a secret (ADR-0053).
- **Informed human approval** in the execution path, not after it (ADR-0054, ADR-0059).
- Self-hosted, offline-installable, LAN/VPN/DMZ deployment profiles.

**Governance alone is no longer differentiating** — see [strategy](strategy.md) §Positioning.

## Where the details live

| For | Read |
|---|---|
| What is built, service by service | [`architecture/module-map.md`](../architecture/module-map.md) |
| Every decision, status, and supersession pointer | [`adr/README.md`](../adr/README.md) — the generated ledger |
| The live plan and release path | [`requirements/epic_story_catalog_v4.md`](../requirements/epic_story_catalog_v4.md) §Part 4 |
| External tools and adoption verdicts | [`references/references_v4.csv`](../../references/references_v4.csv) |
| The original blueprint | [`deep-dive/`](../deep-dive/) — the SPEC-2026.1 source material |
