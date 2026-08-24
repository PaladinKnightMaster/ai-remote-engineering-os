# ADR-0072: Go toolchain support-window policy — superseding ADR-0041's version clause

## Status

**Accepted** — 2026-08-24 (T-146, v0.14.0 "Governed Tool Surface" toolchain-currency ticket).
Author/decider: CEO (war-room), authored by the T-146 implementer per the ratified brief.

**Supersedes ONE clause of ADR-0041** — the Go *version* sentence only. Every other decision in
ADR-0041 (Go as primary backend language, TypeScript frontend-only, Python sanctioned sidecar, Rust
held in reserve) is untouched and not re-litigated here. **ADR-0041 itself is not edited** (G2: an
Accepted ADR is conformed to or superseded by a new ADR, never rewritten in place).

---

## Context

### What ADR-0041 actually said, and why it was insufficient

ADR-0041 (2026-05-16, the repo's foundational language ADR) fixed the Go version with one sentence:

> The Go version target is the current Go stable release line at time of first service
> implementation. Toolchain version is pinned per service via `go.mod`.

Read closely, this is a **point-in-time snapshot**, not a standing rule. It resolves once, at
"first service implementation," and has no mechanism that ever asks the question again. ADR-0041's
own **Review triggers** section lists four conditions that would reopen the ADR — a deteriorating Go
hiring market, a module's threat model demanding Rust, a Go supply-chain incident, or the P1+ Python
sidecar failing its audit-edge boundary — and none of them is "the toolchain has aged out of
Go's supported window." The clause was, by construction, unable to reopen itself on a calendar basis.

### What that produced, in fact

`go 1.23` was pinned at the repo skeleton commit and never revisited. It is not merely old — it fell
out of Go's own supported window (a release is supported until two newer majors ship, per
go.dev's release policy) roughly a year before this ADR, during which the builder images used across
`Taskfile.yml`, `infra/docker-compose/docker-compose.e2e.yml`, and the `infra/offline/`
install/WD1 scripts received **zero stdlib security patches**. This was not caught by any process —
it surfaced only because v0.13.0's signed SBOM made the toolchain identity an auditable artifact, and
a later review noticed the gap (`docs/releases/v0.14.0/v0.14.0-release-criteria.md` §12d). A green
release-check gate had passed through every one of the intervening releases without ever asking the
question.

### T-146's spike evidence (criteria §12d), which this ADR's rule is sized against

- The full Go tree builds, vets, and tests clean under `golang:1.26.7` with **zero source changes** —
  the `go` directive in `go.mod` is a *minimum* the toolchain must satisfy, not a ceiling it is
  fixed to. Moving the **builder** does not require moving the **directive**.
- `golangci-lint@v1.61.0` (and the whole v1 line, confirmed through `v1.64.8`) does not run under a
  Go 1.26 builder; `v2.13.0` does. This ADR does not govern lint tooling, but it is the concrete
  shape of "toolchain currency has a cost, and the cost compounds the longer it is deferred" — the
  same failure mode this ADR exists to close, one layer up the stack.

### Why "supported window," not another fixed version

Go's own support policy — a release is supported until two newer majors exist, roughly a one-year
window at Go's six-month cadence — is an **externally defined, checkable fact**, not a judgment call.
A rule phrased against it can be asserted mechanically (by a release checklist, by CI, by a script)
without needing a war room to interpret it, which is precisely the property ADR-0041's clause lacked.

---

## Decision

1. **The builder toolchain used to produce shipped artifacts must stay inside Go's currently
   supported window.** At time of writing that window is 1.25/1.26/1.27; v0.14.0 (T-146) moves the
   builder to **1.26**, the mature line — not 1.27, which released 2026-08-19 with zero field time,
   and a security product does not adopt a `.0` release on its release day.

2. **This is a standing condition, re-checked every release, not a value pinned once.** The concrete
   version this resolves to will change on Go's own cadence (roughly every six months); this ADR
   fixes the *rule that locates the current answer*, not a permanent number. That is the specific
   defect being corrected: ADR-0041's clause fixed a number and stopped: this one fixes a check.

3. **Enforcement: the release checklist for every future release asserts the builder is inside the
   supported window before that release ships.** This is what gives the rule a refresh trigger —
   the next toolchain move is initiated because a checklist item fails, not because someone happens
   to notice a year has passed. No new standalone checklist document is introduced by this ADR; the
   assertion is a criteria-doc line item in the same shape as this release's VER-23..28 (see
   `docs/releases/v0.14.0/v0.14.0-release-criteria.md` §12d), carried forward by whichever release
   criteria doc is current when the assertion is due.

4. **The `go` directive in each `go.mod`/`go.work` is a separate decision, not governed by this
   ADR's trigger.** Moving the *builder* gains the toolchain's stdlib security fixes for a build that
   already declares compatibility with an older minimum. Raising the *directive* additionally opts
   the binary into whatever GODEBUG defaults changed between the two Go versions — on this product's
   SSH/PASETO/Ed25519 path (ADR-0046, ADR-0058), a change to default crypto/TLS behavior is not
   something to take as a side effect of a routine builder bump. T-146 moves the builder only
   (`go 1.23` stays the directive in every module); raising the directive is deferred with its own
   promotion trigger recorded in `v0.14.0-release-criteria.md` §14 ("its own ticket once T-146 has
   landed and the 1.26 builder has a release of field time... never bundled with a toolchain swap —
   one of the two would mask the other's regressions").

5. **Archival documents are not touched by this or any future toolchain move.** Tickets and
   verification runbooks under `docs/tickets/` and `docs/releases/*/verification_runbook.md` record
   what was actually used at the time a release shipped; rewriting them to match a later toolchain
   would falsify the release record. Only *live* configuration (compose files, Taskfile, offline
   install/WD1 scripts, and equivalents in future releases) is in scope for a toolchain move.

---

## Consequences

### Positive
- The toolchain-staleness failure mode this ADR responds to — a version pinned once, never revisited,
  aging silently out of its vendor's own support window for a year with a signed SBOM the whole time
  attesting to it — cannot recur unnoticed: a future release's checklist either asserts the builder
  is in-window or fails loudly.
- Decoupling "builder inside supported window" (routine, low-risk, stdlib-patch-driven) from "raise
  the `go` directive" (a runtime-behavior decision on the crypto/TLS path) means the low-risk move can
  happen on a short, mechanical cadence without dragging the higher-risk one along by default.
- The rule is self-locating: it does not need a future ADR to say "now move to 1.27" — the checklist
  assertion computes the answer from Go's own published support policy at assertion time.

### Negative
- A "supported window" policy is inherently a moving target — the concrete Go version this resolves
  to today (1.26) will not be the answer in a year. Anyone reading this ADR for "which Go version" must
  check the current release checklist result, not this document, for the live answer. This is
  intentional (see Decision §2) but is a real cost: the ADR itself never again states a single
  authoritative version number after this one.
- The checklist assertion is only as good as it being run. Nothing in this ADR makes the check
  *mandatory* in a technical sense (there is no CI in this offline-first repo to enforce it) — it
  relies on the release process actually consulting the checklist, the same trust the rest of this
  repo's release discipline already rests on (G1/G2 gates, the diff budget, ADR conformance).

### Held in reserve
- If a future toolchain move finds the builder cannot advance mechanically (a breaking stdlib or
  tooling incompatibility blocks it, the way `golangci-lint` v1 blocked this one), that becomes its
  own ticket with its own consult and spike, in the same shape as T-146 — this ADR does not pre-commit
  to "always a single ticket."

---

## Relation to prior ADRs

- **ADR-0041** — superseded at the single version-target clause quoted in §Context above; every other
  decision in ADR-0041 (Go primary / TS frontend / Python sidecar / Rust reserve) is inherited
  unchanged and this ADR takes no position on any of them.
- **ADR-0046** (PASETO auth adapter) / **ADR-0058** (session-broker SSH egress) — named in Decision §4
  as the concrete reason the `go` directive is treated separately from the builder: both sit on a
  crypto/TLS-sensitive path where a GODEBUG default change is not a side effect to accept implicitly.
- **ADR-0070** (release provenance, signed SBOMs) — the mechanism that made the `go 1.23` staleness an
  auditable, discoverable fact in the first place (a signed SBOM records the toolchain identity); this
  ADR is the corrective response to what that visibility surfaced.

## Review triggers

Re-open this ADR if any of these become true:

- Go's own release-support-window definition changes materially (e.g., the two-newer-majors rule is
  replaced with a different policy) — the enforcement mechanism in Decision §3 would need to target
  the new definition.
- A future release's checklist assertion finds the builder outside the supported window **and** the
  fix is not a mechanical builder bump (see "Held in reserve" above) — that finding is the trigger for
  a follow-up ticket, not for reopening this ADR's policy itself.
- The `go` directive is raised in a future release — at that point, whoever does it should confirm
  this ADR's Decision §4 separation still holds, and update `.golangci.yaml`'s `run.go` pin in the
  same change (flagged as a live sync surface in that file's own header comment, added at T-146).
