# ADR-0076 — Contribution provenance: DCO, not a CLA

## Status

**Accepted** — 2026-09-01 (v0.16.0, T-156 follow-on). Commander seat. **Decider: CEO.** Accepted by
the CEO 2026-09-01.

**Discharges ADR-0075's deferred item** ("CLA vs DCO", which that ADR left open with an explicit
trigger). **Refines ADR-0075.** ⚠ ADR-0075 is Accepted and immutable under gate G2, which is why this
is a separate decision record rather than an amendment.

---

## Context

ADR-0075 set the product's licence to **Apache-2.0** and deliberately deferred the contribution-intake
question, naming its trigger: *before accepting the first external pull request*, because that is the
point after which relicensing requires every contributor's agreement.

**The CEO's stated position, 2026-09-01: commercialization is an OPEN POSSIBILITY, not a concrete
plan.** That is the fact this decision turns on.

### What is already true, and narrows the question

- ⭐ **Apache-2.0 §5 already provides the inbound licence.** A contribution intentionally submitted
  for inclusion is, unless the contributor explicitly states otherwise, under the terms of the
  Licence. **So neither a CLA nor a DCO is legally required in order to accept contributions.** The
  question is not *"may we accept PRs"* — it is *what provenance we certify* and *what optionality we
  keep*.
- **v0.16.0 recommends publishing with a plain "not accepting PRs yet" note** [read:
  `docs/releases/v0.16.0/v0.16.0-release-criteria.md` §8].
- **This is a security product.** Contaminated code entering its history is a materially worse outcome
  here than in most projects.

---

## Decision

**Adopt the Developer Certificate of Origin (DCO) 1.1.** Contributions are certified by a
`Signed-off-by:` line in each commit. **No CLA is adopted.**

Enforcement is by sign-off convention, checked at review; a DCO check can be wired up once the
repository is public and has CI. ⚠ **This project has no CI today**, so until it does, enforcement is
a review step and must be listed as one rather than assumed.

---

## ⭐⭐ The consequence that must not be missed

**DCO does NOT preserve relicensing optionality. It certifies provenance only.**

What is actually keeping the commercialization option open is **the "not accepting PRs yet" posture —
not this ADR.** The moment a substantial external contribution is merged, relicensing requires that
contributor's agreement, DCO sign-off or not.

⚠ **So the failure mode is specific and easy to walk into: opening PRs while commercialization is
still an open possibility spends the option without anyone deciding to spend it.** It would not feel
like a decision. It would feel like being welcoming.

**If commercialization firms up from *possible* to *intended*, the CLA question must be reopened
BEFORE the first substantial merge — not after.** That is the same point of no return ADR-0075
named; this ADR does not move it, it only declines to spend it today.

---

## Alternatives considered and rejected

**1. A CLA now.** The only instrument that preserves dual-licensing or a commercial edition.
**Rejected for now**: it is the single largest friction point for a drive-by contributor, and drive-by
contributions are precisely what a newly published repository attracts. With effectively zero
contributors today, adopting a CLA would price an intention that does not yet exist — the same
reasoning that rejected BUSL in ADR-0075. ⚠ **Rejected *for now* is not rejected permanently**; see
the trigger below.

**2. Neither DCO nor CLA**, relying on Apache-2.0 §5 alone. Legally sufficient. **Rejected** because
it certifies nothing about whether a contributor had the right to submit what they submitted. For a
governance and evidence product, unverifiable provenance in the source history is a poor look and a
real risk, and DCO's cost is one flag.

**3. Copyright assignment.** **Rejected**: heaviest possible friction, widely disliked, and it
purchases more than even a commercial plan would need.

---

## Consequences

- **Contributors keep their copyright.** The project gains a certified chain of provenance and no
  relicensing rights.
- **`CONTRIBUTING.md` must carry the DCO text and the sign-off instruction** (`git commit -s`), and
  must state plainly that PRs are not being accepted yet, so the posture is explicit rather than
  inferred from silence.
- **Relicensing stays available only while no substantial external contribution has been merged.**

**Trigger to revisit — whichever comes first:**

1. **Commercialization moves from possible to intended**, or
2. **Before merging the first substantial external contribution.**

⚠ **A trivial fix — a typo, a broken link — is not the trigger; a substantial contribution is.** The
line between them is a judgement call, and *the judgement must be made deliberately at the time*
rather than discovered afterwards.

⚠ **This ADR is an engineering decision record, not legal advice.** If commercialization becomes a
concrete plan, that decision warrants actual legal counsel — the analysis here reasons about licence
texts and project mechanics, which is not the same thing.

---

## How we would know this was wrong

- **A commercial plan arrives after substantial contributions have been merged** — the failure this
  ADR exists to make visible. The remedy would be per-contributor agreement, which is expensive and
  sometimes impossible.
- **Contributors object to sign-off friction** — very unlikely (DCO is the lightest instrument in
  common use), but it would indicate even that cost was too high for this project's contributor
  profile.
