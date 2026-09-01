# ADR-0075 — Open-source licence for the product

## Status

**Accepted** — 2026-09-01 (v0.16.0 scoping, T-156). Commander seat. **Decider: CEO.** Accepted by the CEO 2026-09-01.

**Supersedes nothing.** Discharges **v0.16.0 PUB-2**.

---

## Context

The product is going public as an open-source repository (CEO, 2026-09-01), after fifteen tagged
releases developed privately with no git remote. **It currently has no licence at all.**

**The stated goals of publishing**, and the ones this decision must serve:

1. **Credibility for the core claim.** This is a governance and evidence plane. *An auditability claim
   nobody can inspect is a weak claim* — publishing is itself the product argument.
2. **Prior art and positioning.** `nearai/ironclaw` is a competing governance plane whose
   `InvocationFingerprint` is the same anti-swap property as ours [read:
   `docs/requirements/backlog_v4.csv:44`].
3. **Permanently forcing the *"would a stranger succeed"* discipline** that has produced this
   project's highest-value catches.

⚠ **A fourth motivation was stated and is deliberately NOT relied on here:** that publishing would
reduce a solo developer's load by attracting issue triage, ideas and implementation. Recorded as a
risk in the v0.16.0 criteria §9 — a new repository with no users typically attracts support questions
rather than patches. **The licence should be chosen to serve goals 1–3, which are real, rather than a
contribution volume that may not arrive.**

### The tension this decision has to resolve honestly

**The product's own doctrine is "No SaaS runtime dependency"** — an explicitly anti-cloud posture, and
the reason the offline bundle and air-gap claim exist at all. **A permissive licence permits exactly
the hosted rehosting that doctrine refuses.** That is a genuine conflict and the reason this needs an
ADR rather than a file copied from another project.

### Constraints that are already fixed

- **Inbound dependency licences are allowlisted** to `MIT, BSD-2-Clause, BSD-3-Clause, Apache-2.0,
  ISC, MPL-2.0, LGPL-2.1, LGPL-3.0, Unlicense, 0BSD, CC0-1.0, Python-2.0` [read: `Taskfile.yml:326`],
  gated by `task licenses` inside `task release-check`. ⭐ **There is no GPL or AGPL in that
  allowlist, so no inbound copyleft forces our outbound choice.** The decision is genuinely free.
- **B-041 will integrate MIT-licensed third parties** (the OpenHands software-agent-sdk, openworker)
  as *clients*, not forks — compatible with any option below.
- **This is a security product.** Its distinguishing mechanisms — invocation anchoring, chained-hash
  evidence, the approval moment — are precisely the kind of thing patents get asserted over.

---

## Decision

**Apache License 2.0.**

Three properties decide it:

1. ⭐ **An explicit patent grant from contributors, with defensive termination.** For a security and
   governance product this is not boilerplate: it means a contributor cannot contribute a mechanism
   and later assert a patent over it, and a party that sues over patents loses its own grant. **MIT
   gives none of this.** It is the single strongest reason to prefer Apache-2.0 here.
2. **Corporate-safe.** It is the licence enterprise legal review passes without escalation — which
   matters for a product whose eventual users are operators inside organisations that care about
   governance.
3. **Compatible with the existing allowlist and with the MIT harnesses B-041 will integrate**, and
   compatible with GPLv3 downstream (not GPLv2 — see Consequences).

---

## Alternatives considered and rejected

**1. MIT.** Simplest and most familiar; would work. **Rejected: no patent grant.** For this product
class that is the deciding gap, and the cost of Apache-2.0 over MIT is a longer file, not friction
that any real user experiences.

**2. AGPL-3.0.** ⭐ **The ideologically consistent choice, and the one that most deserves a real
answer** — it would legally prevent the hosted rehosting our own no-SaaS doctrine refuses.
**Rejected on two grounds.** *Practically:* it deters both the corporate users and the contributors
this publication exists to attract, which sacrifices goals 1–3 to defend against a hypothetical.
*Principally, and more importantly:* **the no-SaaS doctrine is a statement about what WE depend on,
not a restriction on what others may do.** The product refuses a SaaS *runtime dependency* so that an
operator can run it disconnected. Using a licence to stop someone else hosting it would be reaching
for a legal instrument to enforce an architectural preference — and it would not even serve the
doctrine, since a hosted rehost simply contradicts the value proposition on its own.

**3. BUSL / other source-available.** **Rejected: not open source.** It forecloses the credibility and
contribution goals entirely, and it is a licence for organisations with a commercial conversion plan.
This project does not have one today; adopting its licence would be pricing an intention that does
not exist.

**4. No licence (all rights reserved).** **Rejected.** Publishing without a licence means nobody may
legally use, fork, or contribute — publication in appearance only, delivering none of goals 1–3.

---

## Consequences

**Accepted deliberately:**

- **Anyone may rehost this as SaaS.** The defence is not legal but architectural: the product's value
  *is* air-gapped, self-hosted, operator-controlled operation, which a hosted rehost contradicts on
  its own terms. If someone hosts it, they have built something that fails the thing it is for.
- **Contributors grant patent rights**, and the project takes on `NOTICE`/header discipline.

**Constraints created:**

- **Apache-2.0 is GPLv3-compatible but NOT GPLv2-compatible.** The existing allowlist already excludes
  both, so nothing changes today — but a future GPLv2 dependency would now be a licence conflict as
  well as an allowlist violation. `task licenses` already enforces the allowlist; no new gate is
  needed.

⚠ **The consequence that forces the timing:**

⭐ **Relicensing later requires the agreement of every external contributor.** Once contributions
arrive, this choice is effectively permanent unless a CLA is in place. **That is why it must be
decided before the first push, not after** — and it is the strongest argument against deferring the
decision as "we can change it later." **We mostly cannot.**

**Deferred, with a trigger:**

- **CLA vs DCO** is not decided here. It only becomes load-bearing when external contributions are
  actually accepted, and the v0.16.0 criteria recommend publishing with a *"not accepting PRs yet"*
  note. **Trigger: before accepting the first external pull request.** ⚠ Accepting a contribution
  without having settled this is the failure mode — it is the point of no return for relicensing.

---

## How we would know this was wrong

- **Sustained hosted rehosting that measurably harms adoption** — would argue the AGPL analysis was
  too optimistic. Remedy is limited (see the relicensing constraint), which is itself part of the
  cost being accepted here.
- **A commercial conversion becoming desirable** — would require contributor agreement to relicense.
  The mitigation is the CLA/DCO trigger above, taken *before* contributions, not after.
- **Contributors declining over the patent grant** — unlikely (Apache-2.0 is among the most widely
  accepted licences), but it would be the signal that MIT's simplicity was worth more than the patent
  clause.
