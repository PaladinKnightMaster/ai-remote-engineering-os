# Security policy

This is a governance and evidence plane for AI-assisted remote engineering. It
takes security seriously precisely because its job is to be the thing other
systems' security depends on — which also means its own limitations need to be as
easy to find as its features.

## Reporting a vulnerability

**`<SECURITY-CONTACT — CEO to supply before publication>`**

Please report suspected vulnerabilities privately to the address above rather than
opening a public issue. Include:

- what you found and why you believe it's a vulnerability;
- steps to reproduce, including the deployment profile (`lan-only` is the only
  profile proven end to end today — see
  [`docs/devops/quickstart-first-governed-action.md`](docs/devops/quickstart-first-governed-action.md));
  and
- the affected release tag or commit, if known.

We will acknowledge receipt and follow up with a timeline once triaged. There is
no bug-bounty program.

---

## Known limitations — stated plainly

A security product with no disclosure policy, or with its limitations
discoverable only by reading architecture decision records, is worse than one
with neither. These three are already true of the shipped product and already
documented internally; this section makes them public claims rather than
internal notes.

### 1. Harness and tool governance is cooperative, not enforced

Policy, approval, and audit govern **whether an action is allowed to start.** They
do not constrain what a *started, approved* action does once it is running.
`remote.exec` can do everything a denied or ungoverned command family could do,
less legibly, once the broker has begun executing it — a rule can refuse to let a
command *start*; nothing in the product today bounds what an *approved* command
does while it runs.

The layer that would close this — process/filesystem/network containment around
the actually-executing command (tracked internally as **B-040**) — is a real,
scoped piece of future work. It is **deferred, not cancelled**, and it needs its
own architecture decision before it ships. Until it does, the honest description
of this product's posture is: **governance decides whether to let something
start; it does not yet constrain it while running.**

### 2. The post-approval half of governed-tool-use is open

Related to (1), stated as its own item because it is easy to read the existence
of a policy engine as closing this and it does not: an approved policy decision
governs the decision to run — not the running process. The same containment work
above (B-040) is what would close this gap, not a change to the policy engine
itself.

### 3. The audit chain records *that* a decision happened, not *which rule* fired

Every policy decision (`allow` / `deny` / `require-approval`) is recorded on the
append-only, chained-hash audit ledger — including its outcome. It does **not**
currently record which specific rule produced that outcome. An operator reviewing
the chain can see that a decision was made and what it was; identifying which
authored rule caused it today requires cross-referencing the rule-authorship
events recorded separately, not a single audit-event lookup. Encoding a rule
reference directly into the decision event needs a change to the audit wire
schema — including what the chained hash itself attests to — and is intentionally
not something done lightly or without its own review.

---

## What *is* enforced today

For context alongside the above — this is not a product with no controls:

- Every credential decrypt happens through exactly one component
  (`connector-service`), which is the sole holder of the decryption key.
- An operator-authored policy rule store can return a machine `deny` — refusing an
  action outright — before any human is ever asked.
- Every governed action, approval, and policy decision is written to a signed,
  append-only, chained-hash audit ledger, independently verifiable offline
  without database or network access.
- The agent-facing gateway holds no symmetric cluster key; a compromised gateway
  cannot forge tokens for another service's capability edge.

None of this is a substitute for reading the limitations above before deciding
what to trust this product with.

## Supported versions

This project is **pre-1.0** (see `README.md` §Status). There is no formal
long-term-support policy yet; security fixes land on the current release line.
