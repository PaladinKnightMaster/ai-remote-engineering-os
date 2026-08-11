# ADR-0069: Audit chain authenticity — signed chain heads, out-of-Postgres anchoring, and third-party offline verification

## Status

**Accepted** — 2026-08-10 (T-123, the v0.13.0 "Evidence Hardening" opening consult; ten decision
points D1–D10 signed off on the war-room recommendation after a pre-authoring audit that produced
**four corrections to the proposed design**, one of which reversed a Commander recommendation made
earlier the same day). Author/decider: CEO (war-room). Binding spec for T-124…T-127; code is
authored just-in-time per ticket from here.

**This ADR declares two divergences.** Neither is from an Accepted ADR — **G2 is clean**:

1. **From the v0.13.0 release criteria, CON-7.** That criterion specified that signature and `keyId`
   reach the audit read surface as additive fields. **This ADR ships zero OpenAPI fragment change**
   (D7). The criteria document's own change rule requires a new ADR for changes to it; this is that
   ADR, and the criteria document is **not edited** — the same discipline that forbids editing an
   Accepted ADR.
2. **From the post-Beta war room's wording of self-finding #2.** The war room recorded that "an
   attacker with write access recomputes from genesis and it verifies clean." **That is not true as
   written** — an append-only trigger has guarded `audit_events` since v0.1.0. The corrected
   statement is in §Context and supersedes the war-room wording wherever the two conflict.

---

## Context

### What the chain is today

`services/audit-service/internal/chain/chain.go` computes, for each event:

```
chainHash = SHA256( prevHash_bytes ‖ framed(id, timestamp, actor, edge, target,
                                            outcome, evidencePacketId, idempotencyKey) )
```

Each field is length-prefixed (uint32 big-endian) before concatenation, so no value can be shifted
across a boundary to forge a colliding pre-image. The file header states the invariant plainly:
*audit-service is the sole chain authority; only it computes `prevHash` and `chainHash`, in-process,
inside the same transaction as the row insert* (ADR-0045).

`chain.Verify` walks events in `seq` order, checks each `prevHash` links to its predecessor's
`chainHash`, and recomputes every `chainHash`. It is exposed operationally as the `audit-service
verify <dsn>` subcommand and asserted by `task wd1`.

**The chain proves ordering. It does not prove authenticity.** The hash is unkeyed SHA-256, computed
and stored by the same service, in the same database, with no signature and no external witness.
Anyone who can rewrite rows can recompute the chain from genesis and it verifies clean. The audit
trail is the core product claim, which is what makes this the sharpest of the three findings the
post-Beta war room recorded about our own product (OWASP **ASI08**).

### The correction — what actually guards the chain today

The pre-authoring audit for this ADR found a control the war room did not account for.
`migrations/0001_init.up.sql` has carried this since **v0.1.0**:

```sql
CREATE TRIGGER audit_events_no_mutate
    BEFORE UPDATE OR DELETE ON audit_events
    FOR EACH ROW EXECUTE FUNCTION audit_events_append_only();
```

…which raises an exception on any `UPDATE` or `DELETE`. Its own comment claims it "makes a
forged-after-the-fact edit impossible even for a caller with table privileges."

**The accurate threat statement, which supersedes the war room's:**

> The chain cannot be rewritten by ordinary table privileges — `audit_events_no_mutate` rejects
> every `UPDATE` and `DELETE`. It **can** be rewritten by an attacker who first **disables or drops
> that trigger**, and today that is within reach of the service itself, because the deployment uses
> a **single Postgres role** (`cockpit`) which **owns** the table. Dropping a trigger is an owner
> privilege. So the real precondition is not "write access" but "owner access" — which an RCE on any
> service holding the shared DSN has.

This matters three ways. It is what an external reviewer will find, so we state it first (**SEC-46**).
It changes what **SEC-43** must actually do — the negative proof has to drop the trigger to tamper at
all, and that step is itself evidence. And it names a complementary control that is arguably stronger
than signing, recorded at D10.

### What this ADR does not claim

It does not make the evidence chain tamper-**proof**. It makes it tamper-**evident to a third party
who holds no database access**, which is the property the product's commercial claim actually needs
and the one AgentField-style verifiable bundles already offer. The residuals are enumerated at
§Security posture and stated publicly at T-134 rather than left for a red-teamer to find.

---

## Decision

### D1. What is signed: the chain head, and why that attests everything before it

A **chain head** is a checkpoint over the chain as of a given sequence number. audit-service signs:

```
signature = Ed25519_sign( SK, framed("aireos-audit-chain-head-v1", seq, chainHash, headTimestamp, keyId) )
```

Framing uses **the same length-prefix idiom as `chain.Compute`** — one framing convention in this
codebase, never two. The leading domain separator is mandatory: a signed head must never be
mistakable for, or replayable as, any other signed artifact in the system (PASETO capability tokens,
the bundle signature of ADR-0070).

**The load-bearing property:** because `chainHash_N = SHA256(chainHash_{N-1} ‖ framed(event_N))`, the
value `chainHash_N` is a cryptographic commitment to the *entire prefix* of the chain. **A single
signature over head N therefore attests every event with `seq ≤ N`.** Any mutation anywhere in
`1..N` changes `chainHash_N` and the signature fails.

This is what turns S-28 from a data migration into a checkpoint, and it is why D9 requires no
backfill.

**`seq` is not itself hashed into `chainHash`** (the hashed field set is fixed and does not include
it) and is `json:"-"` in the wire struct. It is nonetheless safe, because the signature binds
`seq` to `chainHash` together: a renumbering attack produces a head whose `seq` no longer matches the
chain it commits to, which the verifier reports as divergence. `created_at` is likewise unhashed —
and is not exposed on the wire at all, so it is metadata, not evidence.

### D2. Cadence — bounded, and the tamper window is stated, not hidden

Sign a head when **either** 100 events **or** 60 seconds have elapsed since the last head, whichever
comes first; both configurable. Also sign on graceful shutdown. **Signing is off the audit write
path** — a bounded sweep, never per-event — which is what satisfies PERF-14 by construction and
keeps the signing key off the hot path of every governed action.

**The cadence *is* the tamper window, and this is the honest cost of the choice.** Events written
after the last signed head are attested only by the unkeyed chain, and remain forgeable by an
attacker who has cleared the D-Context precondition. The window is at most 60 seconds or 100 events.
It is stated in the threat model at T-134 (**DOC-34**), not glossed.

Rejected: **per-event signing.** It closes the window but contradicts the Accepted criterion AUD-36,
puts the key on the hot path of every governed action, and makes head rows 1:1 with events. The
window is bounded, disclosed, and requires the attacker to act inside it.

### D3. Export does **not** force a checkpoint

**This reverses a Commander recommendation made earlier in the same session, corrected by the
pre-authoring audit.** The initial proposal had export force a head "on demand."

That is wrong. An attacker who tampers **above** the last signed head and then triggers an export
would obtain a signature over their tampered state — with no earlier head covering those events to
contradict it. Export is operator-triggerable, and an attacker with host access can trigger it.

**Export emits the events and the heads that already exist.** It never signs. Operators who want an
immediate checkpoint use an explicit, separate `checkpoint` action; the export path has no signing
authority at all. This removes an attacker-triggerable signing action from the design.

### D4. Anchoring outside Postgres — the truncation detector, not redundancy

Every signed head is appended to an **append-only file outside Postgres** on the install host.

Its purpose is precise and is not backup. Consider an attacker who deletes events `101..150` **and**
their `chain_heads` rows. The database is then *internally consistent*: the chain terminates at
`seq 100`, the head at 100 verifies, and nothing in Postgres reveals the loss. **Truncation is
undetectable from the database alone.** The anchor file still holds head 150; the verifier compares
the two and reports divergence.

So: **the `chain_heads` table detects mutation; the anchor file detects truncation.** Both are
required, for different attacks.

**Per-stack paths are specified, because the two stacks differ.** The installed stack writes under
`/var/lib/aireos` (existing `STATE` root, ADR-0051). The compose/e2e stack runs audit-service as a
host process against a Postgres-only compose file, so its anchor path is host-local and must be
explicitly configured and durable across the test run. **Unspecified, SEC-43's compose leg would
silently exercise a weaker configuration than the installed leg** — the criteria require it green on
both.

Bounded per **PERF-15**: heads are small and arrive at ~1% of event volume; rotate by size and retain
rotated segments. A file that grows without limit is an availability bug on an air-gapped host.

### D5. Key custody, rotation, and installer idempotence

A **new Ed25519 keypair per install**, generated by `install.sh` using the idiom it already has for
`AIREOS_API_AGENT_SIGN_KEY`/`_PUBLIC_KEY` (ADR-0060): generate, secret into one service's env,
public into the shared env.

- `AIREOS_AUDIT_CHAIN_SIGN_KEY` → **audit-service env only**, `0600`.
- `AIREOS_AUDIT_CHAIN_PUBLIC_KEY` → shared env, and its **fingerprint printed at install time** for
  out-of-band recording by the operator.

**Idempotence is an evidence-integrity property here, not hygiene (OI-41).** `install.sh` must follow
its existing `${VAR:-$(genkey)}` reuse-if-present pattern exactly. **Regenerating the key on
re-install silently orphans every historical head** — the chain would still verify structurally while
every signature became uncheckable. This is the single most likely implementation error in the
release.

**Rotation is additive-forever.** Each head records the `keyId` that signed it; the verifier accepts
a **key ring**. Public keys are **never retired, only added** — retiring one makes historical evidence
unverifiable. There is no rotation UI; rotation is a documented operator file operation.

**Blast radius, stated honestly.** audit-service holds **no private key today** — it only verifies
(`AIREOS_PASETO_KEY`, `AIREOS_GATEWAY_PUBLIC_KEY`). This ADR gives it one for the first time. But it
**already holds the symmetric cluster key**, so it is already a top-value target: the incremental
blast radius of adding a signing key is small, and the residual it creates was already present. This
is what makes in-service signing defensible rather than a compromise.

### D6. The verifier is a **separate minimal binary**

`aireos-verify` — a standalone tool taking an **export file** and a **public key**, with no database,
no network, and no configuration.

The claim this release makes is that an outsider can check us. **Handing that outsider a service
binary carrying Postgres drivers, an HTTP server and a migration runner in order to validate a text
file undermines exactly that claim.** `aireos-verify` must be small enough that an auditor can read
it in one sitting. The existing `audit-service verify <dsn>` keeps working unchanged for operators
against a live database.

**Verification is a two-step proof and must be performed in this order:**

1. Verify the Ed25519 signature on each head against the key ring.
2. Walk the chain from `Genesis` and confirm it recomputes to exactly the `chainHash` each head
   commits to.

**Step 1 alone proves nothing about the events.** Both steps, together, are the attestation.

**Verification walks incrementally.** `chain.Verify([]audit.Event) int` materializes the whole chain
into a slice, and `runVerify` already accumulates every event in memory. `aireos-verify` must stream —
retaining only the running `prevHash` — or it inherits an O(n) memory bound on precisely the operation
that must survive a large export. `chain.Compute` is reused; `chain.Verify`'s slice signature is not.

**Three outcomes, never two.** A fresh install has no heads at all, so success must never be
ambiguous:

| Outcome | Meaning |
|---|---|
| `INTACT + ATTESTED` | chain recomputes and every head signature verifies; the attested prefix is named |
| `INTACT + UNATTESTED` | chain recomputes, but some or all events lie beyond the last signed head (or none exists yet) — the unattested tail is reported explicitly |
| `BROKEN` | linkage or recompute failed, or a head signature failed; the first divergent point is named |

### D7. **Zero OpenAPI fragment change** — declared divergence from CON-7

v0.13.0 adds **no route, no additive field, no codegen change** for this work. Export and verification
are CLI operations end to end.

CON-7 anticipated additive fields on the audit read surface. Having designed the mechanism, that was
over-specified: the export is produced by a subcommand, the verifier reads a file, and **GATE-5 — the
§human driving the offline verifier as a third party — is a CLI exercise.** The gate is fully
satisfied without touching the contract.

Zero fragment change is also the strongest available posture for a release that **voluntarily obeys a
compatibility policy it has not yet stamped** (CON-6): the only contract change in all of v0.13.0
becomes B-035's *removal*. It leaves ADR-0067 and EVD-8 entirely untouched. An operator-facing signal
remains available later as a purely additive call, which S-20 classes as non-breaking.

### D8. The export must bypass every filter and preset

**The export is the complete, unfiltered chain in `seq` order. It must never be produced through the
`governed-actions@1` preset, an actor filter, an outcome filter, or any read-surface path.**

A filtered export has gaps. Gaps break `prevHash` linkage. **A gapped export is unverifiable by
construction** — the verifier cannot distinguish a filtered view from a tampered one, and correctly
reports `BROKEN`. This is ADR-0067's EVD-8 chain-contiguity rule reaching the export path, and it is
stated here because it is the mistake T-125 would otherwise make.

**What the export discloses, and why it is safe to hand to a stranger.** ADR-0045's content-free
ingest and ADR-0049's hash-in-chain model already ensure events carry `evidencePacketId = sha256:<…>`
and never raw bytes; ADR-0059 keeps invocation commands off the audit plane entirely; `created_at` is
not in the wire struct. An export therefore discloses **actor and target identifiers, edges, outcomes
and hashes — never commands, stdout, credentials, or memory content.** Third-party verification is
safe to offer *because of decisions already made*, not because of anything added here.

### D9. No backfill, ever — including for existing installs

Because a head commits to its entire prefix (D1), **signing the current head at migration time
attests all pre-existing history.** Existing installs gain full coverage from their first checkpoint.
There is no backfill job, no re-hashing, and no migration of historical rows — which is also required,
since `audit_events` is append-only and forward-only by policy (ADR-0044/0045).

### D10. Failure, concurrency, and the two forward constraints

**Signing failure is fail-loud, never fail-closed.** Signing sits off the write path, and blocking
audit writes would halt the product — every governed action chains an event in-transaction. A signing
failure therefore: chains an audit event of its own (itself unsigned, and that is fine — it is still
chained and still externally visible), fails the service health check, and surfaces as head staleness
in the verifier. Core rule 12.

**Head creation is single-writer.** Two audit-service instances at cadence would race. `chain_heads`
carries a unique constraint on `seq`, and checkpointing takes a Postgres advisory lock. A losing
writer skips its turn; it never retries into a duplicate.

**Forward constraint — retention.** Any future pruning, archival or redaction of `audit_events` makes
every head over the pruned prefix permanently unverifiable. Retention is a recorded post-Beta
candidate; **this ADR constrains it**, and any such design needs its own ADR that resolves the
conflict explicitly.

**Named complementary control — DB privilege separation (deferred to v0.14, recorded here).** The
single-role deployment (§Context) is what lets a compromised service drop the append-only trigger. If
migrations ran as an **owner** role while services connected as a **non-owner with `INSERT`/`SELECT`
only** on `audit_events`, an audit-service RCE could not rewrite history at all — it could only forge
*new* heads, which the anchor file and cadence bound. That is arguably a stronger control than
signing and it materially shrinks the D2 residual.

It is **not** in v0.13.0. It changes `install.sh` and every service's DSN — a wide blast radius for a
release whose defining posture is zero new capability, with no new WD to catch a regression — and it
would be the **fifth** boundary-rule candidate, which is the tripwire the rule names for itself.
**Trigger: the v0.14 Containment ADR**, where installer and privilege changes already belong.

---

## Security posture

**What this closes.** An attacker who obtains owner access to Postgres, drops `audit_events_no_mutate`,
rewrites events and recomputes the chain from genesis now produces a chain that **fails head
signature verification** at the first head covering any mutated event. Detection requires no database
access — an auditor with an export and a public key finds it offline. Truncation of the tail, which is
invisible inside Postgres, is caught by anchor-file divergence (D4).

**What this does not close — all three stated at T-134 (DOC-34):**

1. **audit-service RCE.** The signing key lives in the process that writes the chain. An attacker who
   owns that process can rewrite history *and* sign the result. Closing this requires a signer the
   writer cannot compromise — the external witness, named as the successor in the v0.14 Containment
   ADR. D5 records why the incremental blast radius is nonetheless small.
2. **The cadence tamper window (D2).** At most 60 seconds or 100 events of tail remain attested only
   by the unkeyed chain.
3. **Simultaneous truncation of the database and the anchor file.** Defeating both requires database
   owner access *and* host filesystem write access. Layered, not absolute.

**Test-design constraint for SEC-43, binding on T-127.** The negative proof must tamper an event that
lies **below** the last signed head. Tampering above it and observing that verification passes is
*correct behaviour*, so a naive test would be vacuous or timing-flaky. T-127 must: force a checkpoint,
then **drop the append-only trigger** (that step is itself evidence of the precondition), then tamper
below the head, recompute the chain from genesis so `chain.Verify` returns intact, and prove head
verification **fails and names the first divergent head**. Per VER-16 it must also be shown *passing*
against an untampered export — a negative proof that cannot fail correctly is the S-25 failure class
aimed at our own gate.

---

## Consequences

- audit-service holds a private key for the first time; its key posture changes for the first time
  since ADR-0060.
- One forward-only migration (`0003`) adds `chain_heads`. `audit_events` is untouched.
- Two new env vars, one new install-time keypair, one new on-disk anchor path per stack.
- One new shipped artifact (`aireos-verify`), which must also be signed under ADR-0070 and
  distributed such that SEC-44's non-circularity rule holds.
- **Zero contract change, zero UI, zero agent-plane change.** The `mcp-gateway` stays keyless
  (SEC-20); both agent read planes are byte-unchanged.
- Retention becomes constrained (D10).
- The evidence export becomes a supported artifact we must keep verifiable across versions — a
  compatibility surface that is **not** in the S-20 freeze but behaves like one in practice. Its
  format carries the `-v1` domain separator so a future format is distinguishable rather than
  ambiguous.

---

## Relation to prior ADRs

| ADR | Relation |
|---|---|
| **ADR-0045** — event bus, chained-hash ingest | **Extended, not altered.** audit-service remains the sole chain authority; signing is performed by that same authority. Head payloads carry a hash and no event content, preserving content-free ingest. |
| **ADR-0044** — migrations | Conforms. One forward-only migration (`0003`); no `.down.sql`, consistent with `audit_events`' append-only policy. |
| **ADR-0049** — audit content model | Untouched, and **load-bearing for D8**: hash-in-chain is why an export can be handed to a third party at all. |
| **ADR-0059** — informed invocation approval | Untouched, and also load-bearing for D8: commands never reach the audit plane. |
| **ADR-0060** — per-workload asymmetric keys | **A new instance of its pattern, not a divergence.** The chain-head keypair follows the established Ed25519 generate/split/distribute idiom. |
| **ADR-0067** — audit read semantics | **Entirely untouched** under D7. EVD-8's chain-contiguity rule is *reinforced* by D8, which forbids exporting through any preset. |
| **ADR-0051** — offline installer | Extended by one keypair and one anchor path; **idempotence preserved and made load-bearing (D5/OI-41)**. |
| ADR-0046 / ADR-0053 / ADR-0065 / ADR-0066 | Unaffected. |

**G2: clean.** No Accepted ADR is contradicted or edited. The two declared divergences (§Status) are
from the v0.13.0 criteria and from the war room's informal wording — neither is an Accepted ADR.

---

## Deferred (recorded)

| Item | Trigger |
|---|---|
| **External signer / witness** — a signer the chain writer cannot compromise | The v0.14 Containment ADR. Closes residual #1. |
| **DB privilege separation** — non-owner service role on `audit_events` | The v0.14 Containment ADR (D10). Closes the trigger-drop precondition. |
| **Per-event signing** — zero tamper window | A demonstrated need to attest sub-minute tails; contradicts AUD-36 today. |
| **Operator-facing chain-health surface** in the cockpit | A UI release. Purely additive under S-20; deliberately out of a release that ships no UI. |
| **Cross-install / transparency-log anchoring** | Out of scope for an air-gapped product by doctrine; would need its own ADR and a no-SaaS story. |
| **Retention / pruning of audit events** | Constrained by D10; needs its own ADR resolving the verifiability conflict. |

---

## Ratification (resolved 2026-08-10)

Ten decision points, signed off on the war-room recommendation at the T-123 consult. **A pre-authoring
audit was run before this document was written and changed four things** — recorded here because the
corrections, not the original proposal, are what is Accepted:

1. **The threat statement was wrong.** `audit_events_no_mutate` has guarded the table since v0.1.0;
   the war room's "write access" precondition is really "owner access." Corrected in §Context.
2. **Export must not force a checkpoint (D3)** — reversing the Commander's own earlier
   recommendation, which would have handed an attacker a signature over tampered state.
3. **The export must bypass all filters and presets (D8)**, or it is unverifiable by construction.
4. **SEC-43's test design was vacuous as specified** — the tampered event must lie below a signed
   head. Pinned in §Security posture.

Three of those four came from reading the migration and the struct tags rather than from re-reading
the release criteria. **SEC-46's discipline — every claim verified against the as-built, never
inherited from a prior document — is hereby applied to ADRs, not only to the disclosure documents it
was written for.**
