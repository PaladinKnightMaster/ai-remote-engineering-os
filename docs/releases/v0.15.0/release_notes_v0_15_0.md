# v0.15.0 — Governed Decision Policy

**The release that gives the decision point an actual decision.**

Since v0.2.0, `policy-service` has stood at the trust boundary of every governed action without ever
having a rules table, without `deny` among its decision constants, and without a way to distinguish
`remote.exec` from `remote.file.read` — approval granularity was the *resource*, not the *action*.
v0.15.0 gives it the rule store and evaluator it has never had (ADR-0074): an operator can now author a
rule over `(subject, edge, action, target)` and have the product actually enforce it, including a
machine-evaluated `deny` no human authorized in the moment.

---

## What shipped

**The rule store + evaluator (T-148, T-149a, T-150, T-150b).** `policy_rules` — four exact-or-wildcard
match fields, an outcome of `allow` / `requires_approval` / `deny`, a `label`, `enabled`. The evaluator
takes the **most severe match across every enabled rule**: `deny` beats `requires_approval` beats
`allow`, and severity beats specificity with **no tiebreak at all** — a wildcard `deny` is not rescued by
a more specific `allow` written afterward. No matching rule falls back to the resource's existing
`requires_approval` label, unchanged. `api` now consults the decision point on **every** session-start,
not only resources flagged `requires_approval` — closing the exact gap the release exists to fix: an
unflagged host could run `remote.exec` completely ungoverned under v0.14.0.

**Every decision is chained (AUD-35/36).** `policy.decision:allow` and `policy.decision:deny` are now
emitted transactionally, alongside the existing `policy.decision:requires_approval`. A denial is
**positively recorded** — the chain carries the fact that a machine refused, not merely the absence of
what an approval would have produced.

**The callers learned to refuse before the producer could emit (T-149b, then T-150).** The gateway and
`api` both gained an explicit switch over the decision value with a fail-closed default — a `deny` (or
anything the caller doesn't recognize) **never becomes a park a human can approve past**. This was
shipped in that order deliberately: at no commit in the ladder could a machine `deny` have reached a
caller that mishandled it.

**Operator-authored only (SEC-53).** No agent, run, charter, grant or agent-token subject can create,
edit, enable or disable a rule — every mutating route requires the `human-agent` capability edge
specifically, closing the one gap a uniform contract-level capability tag would have missed (a
`service-service` token, which `mcp-gateway` holds).

**The operator surface (T-151b, T-152, T-152b).** `/v1/policy-rules` — list, create, enable/disable,
delete, reachable from the Dashboard. A rule's blast radius renders **inline, before the rule is saved**
— not behind a confirm dialog at submit time — because the real hazard is comprehension: an operator who
writes `deny * * * *` must understand that no more-specific `allow` they write next will rescue it.

**Rule authorship is itself audited (AUD-40).** Create / enable / disable / delete all chain their own
event, transactionally, with the operator who acted. Before this, an operator could author a wildcard
`allow`, act under it, and delete it, leaving the ledger with one legitimate-looking decision and no
trace the rule ever existed.

---

## Four things to say plainly, because each is easy to assume past

**Rules are operator-authored only.** No agent, no delegation grant, no run, no charter, no
agent-token — human-agent capability, checked explicitly at every mutating route, not inferred from a
contract-level tag.

**Severity beats specificity, with no exception.** `deny` always outranks `requires_approval`, which
always outranks `allow`, and a wildcard match is not weaker than a specific one for this purpose. If an
operator writes `deny * * * *`, the product is now denying everything, and the next rule they write —
however precisely targeted its `allow` — will not undo that. This is the release's sharpest operational
hazard, and it is a UX problem wearing a policy costume: FE-25's blast-radius rendering is the mitigation,
and it is a `block` criterion for exactly this reason.

**An empty rule table changes nothing.** This is the release's most load-bearing claim (CON-1), and it
is proven, not assumed: the existing v0.14.0 approval e2e suite
(`tests/e2e/approval_test.go`, `tests/e2e/invocation_approval_test.go`) runs **unchanged** against the
current evaluator, and passes, because the compose test stack never seeds a policy rule — the table is
empty for every test in the suite except this release's own capstone rule-evaluation legs, each of which
creates and deletes exactly one exact-subject-scoped rule within its own execution window. An operator
who never authors a rule gets v0.14.0's behavior, byte for byte.

**Arguments are not yet governable.** A rule matches on `(subject, edge, action, target)` — never on
the command, path, or any other argument. An operator can deny `remote.exec` against a resource
entirely, or leave it alone; they cannot yet write a rule that permits `ls` but denies `rm`. This is a
genuine limitation, stated here rather than left for an operator to discover: the invocation row persists
one F3-encrypted command field with no argument column, and adding predicate matching over it needs both
a migration and an encryption-scope ruling this release deliberately does not attempt (ADR-0074 §D5).
The promotion trigger is an operator rule that four-field matching genuinely cannot express.

---

## R1's status — the decision plane closes, the post-approval plane stays open

v0.15.0 closes the **decision-plane** half of this product's oldest recorded residual (R1, carried since
v0.14.0): governance now happens at the level of the **action**, not just the resource, and a machine can
say no before a human is ever asked. **That is a real closure and should be read as one.**

It does **not** close the other half. **`remote.exec` can still do everything a denied or ungoverned
command family could do, less legibly, once it is actually running** — a rule can refuse to let a
command *start*; nothing in this release constrains what an *approved* command does once the broker has
executed it. Only containment (B-040, WD-12) constrains that, and containment remains **deferred, not
cancelled**. Read this release's contribution as: *"the product can now say no to more things, more
precisely, before they happen"* — not as *"the product now bounds what a running command can do."* Those
are different claims, and conflating them would misstate what shipped.

**A related, narrower limitation, worth stating alongside R1:** the audit chain records **that** a
policy decision happened and **what it was** (allow/requires_approval/deny) — it does not yet record
**which rule** produced it. `AUD-40`'s rule-authorship events are the partial mitigation (an operator
reviewing the chain can at least see that a rule existed, when, and what it said), but a decision event
itself carries no rule reference. Encoding one needs its own ADR (the outbox event schema has no free
field, and every cheap workaround interacts badly with an existing filter or anchor) — deliberately not
attempted in v0.15.0.

---

## What this release explicitly does **not** claim

**A rule cannot widen authority (SEC-57) — CLOSED at T-155, mutation-proven live.** This session's
cumulative security review (`v0.15.0-security-review.md` §f) first found SEC-57 `block` with zero
dedicated tests, holding only by architectural reasoning; the Commander ruled that reasoning was not the
proof VER-35 requires and filed T-155. `tests/e2e/sec57_test.go`
(`TestSEC57RuleAllowCannotWidenTokenScope`) now proves it live: a wildcard-target `allow` rule for
`agent-remote-session`/`remote.exec` is still refused — by name, the exact SEC-16 message at
`catalog.go:884` — when the calling token is scoped to a different resource than the call targets. The
control leg proves the rule was actually live rather than a silent no-op by targeting a
`requires_approval`-flagged resource and asserting the discriminating fact directly on the chain: a
`policy.decision:allow` was recorded for that call (only possible when the rule's matched outcome fires —
an unmatched rule falls back to parking the flagged resource for approval, per CON-4). This corrects the
control leg's first cut, which ran against an unflagged resource and so could not distinguish the rule
firing from it never firing at all (Commander review, 2026-09-01, same day) — the conclusion is unchanged,
the instrument is now sound. See `verification_runbook.md` Step 1c for the run.

**The two remaining pre-existing findings this release did not touch:** `doc-check` still does not scan
`docs/releases/`/`docs/requirements/*.md` (VER-36), and 26 `.down.sql` files across the product still
have no code path that can run one (VER-37). Both predate this release and are recorded, not silently
carried forward unstated.

Full detail: `v0.15.0-security-review.md`.

---

## Install-wiring diff: zero

No new daemon, edge, key, network path, or datastore. `policy-service` gains one table and its own
internal CRUD plane; `api` and `mcp-gateway` gain a decision they now actually consult and can act on.
`go-deps-check` stays at 10 third-party dependencies across 13 `go.work` modules — the structural proof
that no expression runtime (CEL or otherwise) was adopted for rule matching.

---

## Upgrading

One migration: `policy-service`'s `0002_policy_rules.up.sql` (a new, empty table — CON-1 makes this
additive, not a behavior change until an operator authors a rule). `POLICY_URL` is now boot-mandatory in
`api` (`OI-38`); every shipped compose file and the offline installer already set it. No new keys, no new
environment variables beyond what was already required.

---

## Verification

See `verification_runbook.md` for the full walkthrough, including the WD-15 runbook (a rule-denied
invocation reaches neither the connector nor the broker, and the denial is chained positively) and the
CEO's `§human` pass (VER-32): author a rule, watch it deny, watch a more-specific `allow` fail to rescue
a wildcard `deny`, then delete the rule and watch v0.14.0 behavior return.
