# ADR-NNNN: <one decision, stated as a decision — not a topic>

> **Delete this blockquote when you copy the file.** Every instruction here exists because the
> measured corpus violated it. See `README.md` §Known corpus defects for the numbers.

## Status

**Proposed** — YYYY-MM-DD (what session/ticket produced it). Author. **Decider: CEO.**

**Supersedes / Refines / Extends / Discharges ADR-NNNN §X** — or *"Supersedes nothing."*

> **Use `## Status`, never inline `Status:`.** ADR-0019..0040 use the inline form, so any tool
> grepping `## Status` silently misses 22 ADRs. One template, not two.
>
> **Declare relations HERE, in the Status block.** `adr_ledger.py` extracts them from this block
> verbatim to build the reverse index. A relation stated only in body prose is invisible to the
> ledger, and is reported as uncurated rather than guessed.
>
> **Never edit an Accepted ADR (G2).** Supersede it with a new one naming the clause it replaces.
> The ledger's *Named by* column supplies the forward pointer additively.

---

## Context

What is true now, and why the decision is forced. **Mark every load-bearing claim with how it was
verified** — this is the highest-value discipline in this repo, because the dominant defect class is
*a claim about an artifact the author did not open*:

- `[read: path:line]` — you opened the file
- `[ran: command]` — you executed it and saw the output
- `[inherited: doc]` — taken from another document ⚠ **this is the defect population**
- `[assumed]` — no verification ⚠

**No `[inherited:]` or `[assumed]` marker may survive on a load-bearing claim at Accept time.**
Upgrade it or delete the claim. Session record: eight defects in one ADR, five of them false claims
about the as-built — every one fixed by *opening a source*, none by re-reading.

⚠ **Memory files, planning docs and prior briefs record what was true when written.** ADR-0073's
first draft asserted a security property the product does not have because it inherited the *goal*
from a memory file instead of reading the ADR that decided it. Verify against the as-built.

## Decision

**One decision.** If you are numbering sub-decisions D1…D9, you are writing several ADRs in one
file: ADR-0067 and ADR-0068 carry 21 internal decision points each; 0069/0070/0071 carry 9.
**ADR-0072 exists only because ADR-0041 bundled five decisions**, so retiring one required a whole
new ADR plus a careful "supersedes ONE clause" argument. Bundling has a measured cost — split.

Numbered clauses, each independently conformable-to.

## Consequences

### Positive
What this buys, concretely.

### Negative — including the parts the decider will not like
> **39% of this corpus (28 of 73) has boilerplate here** — *"The decision becomes part of the
> implementation baseline / Changes require a follow-up ADR / Related requirements, tests and
> documentation must stay aligned."* That is a form letter, and Consequences is the section that
> makes an ADR an ADR. **If you cannot name a real cost, you have not understood the decision.**

### Held in reserve
Options deliberately not taken, with what would reopen them.

## Relation to prior ADRs

Each named ADR and what this does to it. Do not claim "preserves X" without reading X.

## Review triggers

Re-open if any of these becomes true. **Every trigger must be an OBSERVABLE EVENT, not a
judgement call.**

> **This is the section that fails most often.** ADR-0041 fixed the Go version as a point-in-time
> snapshot and **none of its four review triggers could fire on toolchain age** — so `go 1.23`
> fossilised for a year past its final security patch *while a signed SBOM attested to it*.
> ADR-0072 exists solely to repair that.
>
> Test each trigger: **what artifact, run by whom, produces the observation that fires it?** If the
> answer is "someone notices," it is not a trigger. If you name a gate, **read the gate first** —
> ADR-0073's draft named a control that inspects `apps/*/package.json` and cannot see a Go module.
> If no gate covers it, say **"this trigger is manual until X is built"** rather than implying one.
