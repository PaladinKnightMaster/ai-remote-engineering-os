# Competitor and reference map

> **Rewritten 2026-08-27** (canon work order, pass 3). The previous version was a **hand-written
> list of eight competitors, unrevised for fifteen months** while the reference inventory grew to 95
> rows across seven intake batches. **A hand-maintained competitor list is a stale list with a
> delay.** This file now carries only the *findings*; the inventory itself lives in one place.

## The inventory is not here

| Source | What it holds |
|---|---|
| [`references/references_v4.csv`](../../references/references_v4.csv) | **The live inventory** — 95 rows: tier, status vs as-built, gap, adoption verdict |
| [`references/intake-v7-2026-08-27.md`](../../references/intake-v7-2026-08-27.md) | Newest batch (17 sources, read 2026-08-27) |
| [`references/intake-v4-2026-07-23.md`](../../references/intake-v4-2026-07-23.md) | The v4 batch ⚠ *applies the pre-ADR-0073 doctrine filter — archival* |
| [`references/reference-audit-v4.md`](../../references/reference-audit-v4.md) | How the tiering was derived |
| [`references/source_watchlist.md`](../../references/source_watchlist.md) | What to re-check and when |

Tier 1 is reviewed at each release boundary; Tier 2 is pulled when its trigger fires; Tier 3 is
archived and never re-reviewed.

## Findings that survived measurement

These are the only claims here, because they are the only ones that were *verified* rather than
listed.

**1. Governance is a named market category, not a private bet.** `microsoft/agent-governance-toolkit`,
`NVIDIA/SkillSpector`, `denoland/clawpatrol`, `Agent-Field/agentfield`. Validating — and narrowing.

**2. Microsoft's AGT independently reached ~5 of our 7 controls**, including the credential-broker
property once treated as differentiating (2026-08-10).

**3. The seam is occupied *with* governance.** `CopilotKit/OpenBot` (MIT, 3.2k★) ships per-agent
isolation, CEL policy, decision-before-execution and an audit trail. `apache/maka` (ASF incubating,
3,949 commits) ships an append-only log and approval on sandbox-boundary crossing. The earlier record
held the nearest competitor occupied this seam *without* governance; **that is no longer true.**

**4. Nobody is air-gapped.** Across all sixteen repos in the v7 pass, **not one is both governed and
air-gapped.** OpenBot and maka need cloud or model keys; `magnitude` is offline but ungoverned.
**That gap is the product** — and it is the one control no surveyed competitor reproduces.

**5. Two projects reached our substrate rule independently** — maka (*"users must provide their own"*)
and OpenBot (*"no model ships in the box"*). **ADR-0073 §2 is a category norm, not an idiosyncrasy.**

## Screening heuristics earned the hard way

- **Star/commit mismatch.** `OpenExecutive` 1.5k★/**16** commits · `HolyClaude` 2.5k★/**51** ·
  `rome` 374★/**50** · `openworker` 14k★/**130**. Stars measure attention; commits measure whether
  anyone is still there.
- **Licence first.** 5 of 17 in the v7 batch were AGPL-3.0 or carried no licence file. We
  redistribute an offline bundle, so a licence governs the product, not just the dependency.
- **Read the architecture, not the pitch.** Twice now a README's claim was contradicted by the code
  path: KittenTTS's false "25MB int8", and `voice-typing-linux` advertising local voice typing while
  POSTing to a remote STT endpoint.
- **Absence in a README is not absence in the product.** The v7 pass was documentation-level, not
  source-level; claims of "X doesn't do Y" are hedged accordingly.

## Recorded anti-patterns

- **`HolyClaude`** — 2.5k★ for `acceptEdits` by default with **no approval gating and no audit
  layer**. The market signal is real: people want that convenience and nobody is selling them the
  governance.
- **`grass-ide`** — zero authentication; a QR code is not a credential.
