# Security policy

This is a governance and evidence plane for AI-assisted remote engineering. It
takes security seriously precisely because its job is to be the thing other
systems' security depends on — which also means its own limitations need to be as
easy to find as its features.

## Reporting a vulnerability

**Use GitHub's private vulnerability reporting** — on this repository, open the
**Security** tab and choose **Report a vulnerability**. That opens a private
advisory visible only to the maintainer; it is not a public issue.

⚠ **Please do not open a public issue for a suspected vulnerability.**

Include what you found and why you believe it is a vulnerability, steps to
reproduce (including the deployment profile — `lan-only` is the only profile
proven end to end today), and the affected release tag or commit if known.

**Scope.** This project is **self-hosted**; there is no service of ours to test
against, and no production system exists to attack. In scope: the services,
gateway, connector, audit chain, offline bundle and install scripts in this
repository. Out of scope: vulnerabilities in third-party dependencies, which
should go to those projects directly (tell us anyway if the way *we* use one makes
it materially worse).

**What to expect.** This is a **single-maintainer project with no bug-bounty
program and no service-level commitment.** Reports are handled on a best-effort
basis. That is stated plainly rather than dressed up as a response-time promise
that would not be kept — an SLA nobody meets is worse than none.

**Coordinated disclosure.** Please give us a reasonable window to investigate and
ship a fix before publishing. We will not ask for an indefinite embargo, and we
will not object to you publishing after a reasonable period whether or not a fix
has landed.

**Good-faith research.** We will not pursue or support action against anyone
reporting in good faith through the channel above, where the research stays within
the scope described and does not involve third parties' systems or data.

---

## Verifying a release before you install it

⭐ **This matters more here than for most projects: verifiable provenance is one of
this product's own claims, so it should be the first thing you exercise.**

Every shipped artifact carries a detached Ed25519 signature and a CycloneDX SBOM.
Verification needs **only `openssl`** — already present on the target host — and
the public key. There is no bespoke verifier binary to trust
[see [`docs/devops/offline-install-v3.md`](docs/devops/offline-install-v3.md)].

**1. Check the public key is the right one.** Compare its SHA-256 fingerprint
against the one published in that release's notes, **obtained through a different
channel than the artifact itself**:

```
openssl pkey -pubin -in aireos-release.pub -pubout -outform DER | openssl dgst -sha256
```

**2. Verify each artifact** — the core bundle and the postgres companion. The
companion matters most: it is the one whose `install-postgres.sh` runs **as root**.

```
openssl pkeyutl -verify -pubin -inkey aireos-release.pub   -rawin -in aireos-offline-<version>.tar.gz -sigfile aireos-offline-<version>.tar.gz.sig
```

Both must print `Signature Verified Successfully`. **A missing `.sig`, or any other
output, means stop** — do not extract or run anything from that artifact.

**3. The audit chain verifies independently**, offline, with no database and no
network, using the `aireos-verify` binary shipped inside the bundle.

⚠ **The honest limit of all this.** If you received the artifact and the public key
through the **same channel**, you have verified internal consistency — **not
provenance.** A party who controlled that channel could have supplied both. The
published fingerprint exists so a second channel is *possible*; we cannot force you
to use one, and you should.

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
