# ADR-0074: `policy-service` evaluates operator-authored rules, so the decision point can decide

## Status

**Accepted** — 2026-08-28 (the v0.15 scoping session; authored by the War Room Commander after a
pre-authoring audit of the as-built decision plane, and accepted by the CEO the same day).
**Decider: CEO.**

**Discharges ADR-0054 §1 and §3** — both Accepted 2026-06-01, both only half-built. ADR-0054 §1
decided that the decision point returns `allow | deny | requires_approval` for an
`(actor, edge, target, action)` tuple; the as-built returns two of those three and consults none of
the tuple. ADR-0054 §3 decided that *every* decision is audit-chained; the as-built chains one of the
two outcomes. **This ADR supersedes nothing and edits nothing** — it builds what ADR-0054 already
decided, and adds the one thing ADR-0054 left open: where the rules come from.

**Extends ADR-0057 §invocation-anchored governance** — the tuple this evaluates is the one the typed
tool catalog already carries and already sends. No tool definition changes.

> **Accept-time provenance check (ops rule 11).** Swept for surviving `[inherited:]` / `[assumed]`
> markers, with a control proving the sweep fires on a planted **and newline-wrapped** instance. **One
> `[inherited:]` was found** — an unverifiable third-party attribution in §Held in reserve — **and the
> claim was deleted rather than upgraded**, since no clause depended on it. **Zero unresolved markers
> remain.** *(This note sits outside the Status block on purpose: `adr_ledger.py` extracts that block
> verbatim into the ledger's relations column, so prose placed there pollutes the reverse index.)*

---

## Context

**The decision point has never made a policy decision.** This was found by opening the service, not
by re-reading its ADR.

- `policy-service` owns exactly two tables, `pending_approvals` and `policy_outbox`.
  **There is no rules table.** [read: `services/policy-service/migrations/0001_policy.up.sql:24,48`]
- Two decision constants exist — `DecisionAllow` and `DecisionRequiresApproval`. **`deny` is not
  among them.** [read: `services/policy-service/internal/store/store.go:32-38`]
- The gateway's client states the gap in a comment: *"deny is reserved for a later rule engine."*
  [read: `services/mcp-gateway/internal/policy/policy.go:20`]
- `decideRequest` carries `{session_id, subject, edge, target, action, requires_approval}`. `edge`,
  `target` and `action` are validated non-empty, persisted for display, and **never consulted**. The
  entire decision is `if !req.RequiresApproval → allow`.
  [read: `services/policy-service/internal/server/server.go:48-55,84-87`]
- The store's own comment records the deferral that never landed: *"deny is not produced at v0.2.0 —
  there are no deny rules until the v0.3.0 rule engine."* **v0.3.0 shipped fourteen releases ago.**
  [read: `services/policy-service/internal/store/store.go:32-34`]

**Every `deny` in the product is a human clicking deny.** [ran: `grep -rn 'DecisionDeny|"deny"'` over
`services/` — every hit is an operator-resolve path in `services/api`, none a policy evaluation.]

### The granularity is the host, not the action

`requiresApproval` is read from the **resource** row and nothing else:
[read: `services/mcp-gateway/internal/catalog/catalog.go:1246`]. One boolean per host. Two live
consequences:

- On an **unflagged** resource, `remote.exec` runs with **no approval and no policy evaluation at
  all**.
- On a **flagged** resource, `remote.file.list` is gated exactly as hard as an arbitrary command.

**There is no middle setting, and v0.14.0 created the pressure for one.** That release shipped three
typed, bounded tools [read: `catalog.go:88` — nine tools, four of them egress] onto a decision plane
that cannot tell them apart from `remote.exec`. This is residual **R1** in its structural form: the
file family is better governed than the tool beside it because the *tools* carry bounds, while the
*decision point* carries none. [read: `docs/releases/v0.14.0/v0.14.0-security-review.md:134`]

### What the product does have — so the gap is not overstated

A coarse constrain rung exists, but it lives in **grants**, not in policy:

- `delegation_grants` bounds an agent by `resources[]` × `verbs[]` × `max_steps_per_run` ×
  `max_concurrent_runs` × `step_budget` × `expires_at`
  [read: `services/api/migrations/0008_delegation_grants.up.sql:24-29`]
- the agent token's `Scope.Operations` gates each tool's verb
  [read: `catalog.go:886-900`]

So the ladder as-built is **grant → human stop → audit**. Grants are *per-agent standing authority*.
What is missing is anything the **operator** can state once that binds every agent, every session and
every initiator — including the operator's own.

### Half of ADR-0054 §3 is also unbuilt

`policy.decision:requires_approval` is emitted, in the same transaction as the pending row
[read: `services/policy-service/internal/store/store.go:119`; confirmed live at
`tests/e2e/approval_test.go:98`]. But the `allow` path returns before touching the store
[read: `server.go:84-87`], so **an allow decision produces no row and no event**. ADR-0054 §3 says
every decision is audit-chained. Today, one of two outcomes is.

Under ADR-0073 §1 — membership means consequential actions are on the ledger, verifiable offline by
an outsider — a governance decision that leaves no trace is a hole in the core claim. It is partly
masked today because `allow` means "no approval was required." **Once rules exist, an `allow` is a
rule firing, and it must be on the chain.**

### The agent-facing surface asserts this control already exists

Every egress tool's description, shipped to the model on `tools/list`, says *"The call is
policy-checked."* [read: `catalog.go:130,213,321,478`] No policy is checked. This is not a missing
feature so much as an untrue statement on the interface the governance claim rests on.

---

## Decision

**`policy-service` evaluates operator-authored rules over the tuple ADR-0054 §1 already specified,
and may return `deny` without a human present.**

1. **A rule store.** `policy-service` owns a `policy_rules` table: operator-authored rows matching on
   `(subject, edge, action, target)` — each field an exact value or the wildcard `*` — yielding an
   outcome of `allow | requires_approval | deny`, with a `label` and an `enabled` flag. Rules are
   **operator-authored only**; no agent, run, charter or grant may create, edit or enable one.

2. **Evaluation and precedence, stated so they are conformable-to.** All enabled rules are matched;
   the effective outcome is the **most severe** match — `deny` > `requires_approval` > `allow`.
   Ties within a severity are irrelevant because the outcome is identical. **Specificity does not
   override severity**: a wildcard `deny` beats a fully-specific `allow`. This is the only precedence
   rule that is safe to reason about under time pressure, which is when it will be read.

3. **No matching rule falls back to today's behaviour** — the resource's `requires_approval` label.
   The rule table is therefore **additive and backward-compatible**: an empty table reproduces
   v0.14.0 semantics exactly, and that equivalence is a release criterion, not an assumption.

4. **Every decision is audit-chained, completing ADR-0054 §3.** The `allow` and `deny` paths emit
   `policy.decision:allow` and `policy.decision:deny` on the existing `policy_outbox`, in the same
   transaction as the decision, alongside today's `policy.decision:requires_approval`. A rule-denied
   invocation fails closed and is **positively recorded** — never proven by absence.

5. **Argument-level predicates are OUT of scope, and the reason is measured, not stylistic.** A
   predicate over a command string or a file path has nowhere to read its input: the invocation row
   persists **exactly one field**, `Command`, F3-encrypted at rest, with no args column — the file
   family smuggles its path through a descriptor prefix the gateway itself writes and strips back
   off. [read: `catalog.go:181-188`] Adding one requires a migration **and** a ruling on whether
   arguments fall inside the F3 encryption scope. That is the next rung, not this one.

---

## Consequences

### Positive

- The product can express **"never, regardless of who approves"** for the first time. Today the
  strongest available statement is "ask a human," and a human under time pressure is not a control.
- Reads become cheap and writes stay gated **on the same host** — the setting v0.14.0 needs and
  cannot express. This is the concrete unblock for dogfooding.
- The word *"policy-checked"* in every tool description becomes true.
- `allow` decisions join the ledger, closing the second half of ADR-0054 §3 and removing a
  ADR-0073 §1 hole in the evidence claim.
- It costs **no new dependency** (`task go-deps-check` stays green at 10), no new daemon, no new key,
  no new runtime, no new edge.

### Negative — including the parts the decider will not like

- **This is the last release that can safely change the decision surface.** §D5's argument-level rung
  will want the same tables and routes to change again, after the v1.0.0 freeze has bound them. We
  are freezing a decision surface we already know is one rung short, and doing it deliberately.
- **A wildcard `deny` is a foot-gun.** Clause 2 makes severity beat specificity, so an operator who
  writes `deny * * * *` to "be safe" disables their own product and the most-specific `allow` they
  write next will not rescue it. The UI must make the blast radius of a rule visible before it is
  saved; a criterion covers this, but it is a real ongoing cost.
- **Two authority systems now bound one agent** — grants (per-agent standing authority) and rules
  (operator policy). An operator debugging "why was this denied" must check both. We are choosing
  this over folding rules into grants because rules must bind initiators that hold no grant,
  including the operator.
- **The fallback in clause 3 is a compatibility shim with no expiry.** It will be load-bearing
  forever unless something forces the resource label's retirement, and nothing here does.

### Held in reserve

- **Argument-level predicates** (path prefixes, command patterns) — reopened by the migration in §D5.
- **Default-deny posture.** Adding the *capability* to deny is this ADR; flipping the *default* is a
  deployment-semantics decision that should follow operator evidence, not precede it.
- **A policy expression language** (CEL or similar). Rejected for now on the dependency surface
  alone, which is a fact about this repo rather than a judgement about the option: any such runtime
  is a **new direct Go dependency** and would fail `task go-deps-check` by design
  [read: `infra/scripts/go_deps_check.py:42-53`]. Four-field tuple matching needs none. Reopens if a
  rule shape appears that tuple matching genuinely cannot express — *an operator request, not an
  aesthetic judgement*.
  > A prior draft cited `CopilotKit/OpenBot` here as a worked example of an expression-based policy
  > seam. **Removed at Accept time rather than carried**: it rested on a README-level intake note
  > never verified against that codebase, and no clause above depends on it. Recorded because the
  > deletion *is* the provenance rule working — an unverifiable aside does not earn a place in an
  > Accepted ADR merely because it is interesting.

---

## Relation to prior ADRs

- **ADR-0054** (approval/policy decision point) — **discharged, not superseded.** §1's third decision
  value and §3's "every decision" both land here. Every clause of ADR-0054 remains true; two of them
  become true *in the code* for the first time. Read before writing this: its §1 already specifies
  the exact tuple, which is why this ADR invents no new one.
- **ADR-0057** (typed tool catalog, invocation-anchored governance) — extended, unedited. The
  `Edge`/`Action` metadata it defines is already transmitted to `policy.Decide` and discarded
  [read: `catalog.go:1273`]; this makes the receiver use it. No tool definition changes.
- **ADR-0062** (delegation grants) — untouched. Grants remain per-agent standing authority. Rules are
  operator policy over the same tuple. The Negative section records the cost of having both.
- **ADR-0073** (the product boundary) — conformed to. Clause 4 puts a governance decision on the one
  signed ledger, which is §1's membership test. Nothing here is a substrate: no inference, no
  sandbox, no third-party harness, and no new dependency.
- **ADR-0045 / ADR-0049** (transactional outbox, hash-in-chain) — reused unchanged. Clause 4 adds
  outcome values to an existing outbox, not a write path.

---

## Review triggers

Re-open if any of these becomes observable:

1. **`task go-deps-check` fails on a policy-engine dependency.** Any attempt to adopt CEL or another
   expression runtime trips the allowlist [read: `infra/scripts/go_deps_check.py:42-53`] — the gate
   fires automatically and an ADR is already required. **This trigger is live and mechanical.**
2. **An operator files a rule they cannot express in four-field matching.** Observable as a support
   or dogfooding record naming the rule they wanted. Reopens the reserved expression language.
   **This trigger is manual** — no gate can observe a rule nobody could write.
3. **The v1.0.0 surface freeze is declared while §D5's argument rung is still open.** Observable at
   the freeze ticket: if `policy_rules` has no args column and the freeze is being stamped, the cost
   recorded in Negative has come due and must be re-consented to, not inherited.
4. **A `policy.decision:allow` event is absent from a chain export containing a governed
   invocation.** Observable by the offline verifier over any export — a regression in clause 4 is
   externally detectable, which is the standard ADR-0073 §1 sets.
