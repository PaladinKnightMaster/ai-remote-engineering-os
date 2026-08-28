# North-star roadmap

> **Rewritten 2026-08-27** (canon work order, pass 3). **This file is the north star, not the plan.**
> The live, authoritative release path is
> [`requirements/epic_story_catalog_v4.md`](../requirements/epic_story_catalog_v4.md) §Part 4;
> per-requirement status is [`requirements/requirements_v4.csv`](../requirements/requirements_v4.csv).
> Both are maintained. **This file is not a second copy of them** — the previous version drifted
> because it duplicated a plan instead of pointing at one.

## Priority tiers — stable, and used by the requirements ladder

⚠ **Do not renumber.** `requirements_v4.csv` scores every REQ against these tiers, and the v0.1.0
release criteria cite this file's **P0** line as the definition of the shipped wedge.

**P0 — MVP survival.** Remote cockpit, auth, LAN/VPN/DMZ profiles, terminal sessions, audit,
offline installer. — **shipped v0.1.0–v0.12.0**

**P1 — Defensibility.** Typed MCP gateway, egress gateway, harness governance, evidence packets,
mobile approval, Living Workstreams. — **shipped v0.3.0–v0.7.0**; agentic desktop, work board, skill
registry and model gateway remain open.

**P2 — Strategic capabilities.** Persistent memory, continuous context indexing, Git/issue
intelligence, workflow compiler, backend sandbox, desktop/browser automation, evaluation, multimodal.
— **memory shipped v0.8.0, context index v0.10.0**; the rest are open.

**P3 — Delight and brand.** Companion/avatar, voice, teaching mode, domain personas.

## Where the product actually is

**v0.14.0 "Governed Tool Surface" — released 2026-08-26**, tagged `6b72739`. Fifteen releases: the
P0 cockpit, the P1 governance spine, the P2 data plane, evidence hardening (signed chain heads,
offline verifier, signed bundles + SBOMs), and the first egress-tool widening since v0.3.0.

**Next: v0.15 scoping**, against the **ADR-0073** product boundary. Then **v1.0.0 Beta 1** — where
the governed-harness integration (B-041) lands *and* the OpenAPI surface freezes.

⚠ **Two dependencies on the record, both easy to lose:**
- **Containment (B-040) is DEFERRED, not cancelled.** Until it lands, harness governance is
  **cooperative, not enforced** — and v1.0.0's criteria must *say so*.
- **v1.0.0's reference integrations need a model to run.** Under ADR-0073 a model runtime is an
  operator-provided **substrate**; shipping one requires a measured feasibility finding. **Until
  either happens, v1.0.0 cannot be demonstrated in the `lan-only` profile** — the segment the
  positioning rests on.

## The north star

> A self-hosted engineering operating system where humans and AI agents plan work, document
> knowledge, change code, operate machines, produce evidence, and ship software under company
> governance — **with every consequential action on one ledger an outsider can verify offline.**

The pillars that reach it — work board, knowledge base, code forge, execution, governance — come from
the SPEC-2026.1 blueprint ([`deep-dive/01-north-star-vision.md`](../deep-dive/01-north-star-vision.md))
and are bounded by **ADR-0073**: each is a companion product **only if** it writes provable evidence
to the ledger. Anything that cannot is a governed substrate, not a member.
