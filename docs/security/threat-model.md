# Threat model and disclosure — AI Remote Engineering OS

**Authored 2026-08-18 (T-134, v0.13.0 "Evidence Hardening"). Closes DOC-30…DOC-35, SEC-46.**

This document states what our controls actually do, what they do **not** do, and where each claim
can be checked. It is written to be read by someone trying to find the holes.

## How to read the citations (SEC-46)

**Every claim below was verified against the code at authoring time, not inherited from a prior
document.** That rule exists because v0.12 shipped four criteria asserting things about the as-built
that were false (OI-23, ONB-4, FE-42, AUD-34), and a threat model is the worst place to repeat that.
Claims are marked:

- **`file:line`** — opened and read during authoring on 2026-08-18.
- **ADR-00NN** — governed by an Accepted ADR; the ADR is the specification the code conforms to.
- **measured** — established by a gate that was actually run, with its result recorded.

Where a claim could not be verified to primary source, the limit is stated rather than papered over.

---

## 1. OWASP mapping (DOC-30)

Mapped against the **OWASP Top 10 for Agentic Applications** (released **2025-12-09**). The name is
*Agentic **Applications***, not "Agentic AI" — it is checkable, and getting it wrong discredits the
rest of the document.

> **Verification limit, stated plainly.** The category titles below were confirmed on 2026-08-18 via
> two independent secondary sources. `genai.owasp.org` returned **HTTP 403** to automated retrieval
> of both the resource page and the PDF, so they were **not** read from the primary document at
> authoring time. Nine of the ten corroborate this repo's own prior record
> (`references/references_v4.csv:42`, marked VERIFIED-IN-DOCS). The tenth did not — see §1.1.

| | Category | Our posture |
|---|---|---|
| **ASI01** | Agent Goal Hijack | **Partial.** An agent cannot widen its own authority: the delegation grant is operator-issued and the agent never mints a token (ADR-0062). A hijacked goal still executes only through governed, audited steps — but nothing detects that the *goal* changed. |
| **ASI02** | Tool Misuse & Exploitation | **Gapped → §2.** Four tools now cross the boundary (`remote.exec` plus the v0.14.0 `remote.file.read`/`write`/`list` family, ADR-0071); every invocation is policy-gated and audited, but what a spawned `remote.exec` process then does is still not contained — see §2a. |
| **ASI03** | Identity & Privilege Abuse | **Gapped → §2.** Per-workload asymmetric keys (ADR-0060) mean an agent-plane compromise cannot mint foreign-edge tokens — **measured** by WD-4's negative test. The residual is post-execution, not token-level. |
| **ASI04** | Agentic Supply Chain Vulnerabilities | **Closed this release.** Both shipped artifacts carry detached Ed25519 signatures and CycloneDX 1.6 SBOMs whose coverage is asserted against actual contents (ADR-0070). **No SLSA level is claimed** — see §6. |
| **ASI05** | Unexpected Code Execution (RCE) | **Gapped → §2.** `remote.exec` *is* sanctioned remote code execution; the boundary is what happens after approval. Unaffected by the v0.14.0 file family — see §2a. |
| **ASI06** | Memory & Context Poisoning | **Strong.** The `agent-memory` edge is **read-only** — `memory.read` is the one lit path (`services/mcp-gateway/internal/catalog/catalog.go:253`), containment is enforced inside the query by CTE rather than by filtering after the fact, and sharing is gated. An agent cannot write to memory at all. |
| **ASI07** | Insecure Inter-Agent Communication | **Out of scope, stated rather than left silent.** The product is single-plane: there is no agent-to-agent path. An agent that attempted to sub-delegate was stopped by construction (observed at v0.9, T-092). If a second agent plane is ever added this row becomes live and needs its own ADR. |
| **ASI08** | Cascading Failures | **See §1.1 — this repo previously mapped ASI08 to the wrong category.** |
| **ASI09** | Human-Agent Trust Exploitation | **Strong.** Consent is *informed* and per-tick: an adaptive plan always parks as a `proposed` Run and binds to the exact plan the operator reviewed (ADR-0065). The launch review reads the command back verbatim before it runs. |
| **ASI10** | Rogue Agents | **Partial.** Grants are revocable and revocation cascades (ADR-0062); every action is attributable to an actor/principal pair. There is no behavioural anomaly detection — a rogue agent acting *within* its grant is indistinguishable from a correct one. |

### 1.1 Correction of record — ASI08 is *Cascading Failures*, not *Repudiation and Untraceability*

This repo has been mapping its audit-authenticity work to **ASI08** on the belief that ASI08 is
"Repudiation and Untraceability". **It is not.** ASI08 is **Cascading Failures**. "Repudiation and
Untraceability" is **T8** in OWASP's separate *Agentic AI Threats and Mitigations* taxonomy — a
different series. The two were conflated.

The error is inherited in at least two committed places:

- `docs/requirements/backlog_v4.csv` — B-042 ("OWASP ASI08 (Repudiation and Untraceability)")
- `docs/adr/ADR-0069-audit-chain-authenticity.md:49` — cites ASI08 for the chain work

**ADR-0069 is Accepted and is not edited** (G2: conform, or file a new ADR). The correction is
recorded here instead, which is the right place: this is the document that publishes the mapping
externally, and it is the first time the mapping has been checked against the source rather than
carried forward.

**The corrected mapping.** Our audit-chain authenticity work (§5) answers **T8 — Repudiation and
Untraceability**. Against **ASI08 — Cascading Failures** our posture is **partial**: a Run halts on a
failed step and never auto-continues past one, and grant revocation cascades — but there is no
blast-radius limiter for a sequence of individually-approved steps that are harmful in aggregate.

**This is why SEC-46 exists.** The claim was plausible, twice-committed, and wrong. It survived a war
room and an ADR because every reader inherited it. It was caught only by checking an external fact at
authoring time — the same discipline, pointed outward instead of at our own code.

---

## 2. The containment boundary (DOC-31, S-30)

**Our controls hold for actions that traverse the gateway. They stop at the process boundary.**

Every tool call is policy-evaluated, may require human approval, leases a credential from the sole
decrypt authority, and chains an audit event. **A process spawned by an approved `remote.exec` is not
contained, and its own network activity is entirely ungoverned.** It can open sockets we never see.
Nothing structurally compels an agent to reach the target *through* us — "the gateway is the sole
tool-execution path" is an architectural assumption, not an enforced property.

We state this rather than let a red-teamer discover it. **Containment (B-040, WD-12) is deferred, not
cancelled — the path is v0.14.0 → v1.0.0 Beta 1**, where layer 2 lands and the tool surface freezes
(`CLAUDE.md`, `docs/requirements/epic_story_catalog_v4.md` §Part 4, CEO-ratified 2026-08-19). It did
not land at v0.14.0 as an earlier revision of this document anticipated; the residual register below is
corrected accordingly rather than left to read stale.

### 2a. v0.14.0 addendum — the file tool family narrows the audit gap, not the containment gap (DOC-40)

ADR-0071 (v0.14.0 "Governed Tool Surface") added `remote.file.read`, `remote.file.write` and
`remote.file.list` on the **same** `agent-remote-session` edge, same credential custody, same
sole-SSH-egress broker (§ADR-0071 "Security posture"). Two things follow, carried here verbatim from
that section because they are residuals a reader of *this* document should not have to find in an ADR:

- **The new tools are better governed than the one beside them.** They are typed, bounded (1 MiB
  default, fail-closed on over-cap rather than truncated — a truncated file is a corrupt file), digest-
  anchored (§5 below never carries raw content; the sha256 is what execution and audit both bind to),
  and separately grantable (an agent token must name `file.read`/`file.write` explicitly — an empty
  operations list is unrestricted for `exec` alone, never for the file verbs). `remote.exec` predates
  all four properties.
- **`remote.exec` can still do everything the file tools do, less legibly (R1).** `cat`/shell
  redirection reads and writes the same bytes on the same resources with no typed bounds, no digest
  anchor, and no disclosure line in the approval prompt. This release does not and cannot close that
  gap — it narrows the space of actions that are typed and legible, but the untyped path stays open
  beside it. **Only containment constrains what an approved command may do once it runs**, and
  containment is the deferred item immediately above.
- **An operator approving a file write approves the path, size, digest and mode — not the file's
  contents** (ADR-0071 §D3). `<ApprovalPrompt>` renders this as a plain-language consequence line
  (`packages/ui/src/components/ApprovalPrompt.tsx`) rather than only in the descriptor payload,
  because an accurate prompt that omits the consequence is still uninformed consent. This is a genuine
  narrowing of "informed" for a write, and the read/list disclosure line ("the file's contents will be
  disclosed to the agent" / "this directory's filenames, sizes and timestamps will be disclosed to the
  agent") carries the equivalent honesty for the two non-mutating members.
- **Live file transfer is proven on one stack, not two.** The family is exercised end-to-end on compose
  only (T-144); WD-1 does not exercise it and VER-17 does not require it. Stated here rather than left
  to be assumed at parity with `remote.exec`, which both stacks do exercise.

---

## 3. Session revocation (DOC-32, S-24)

**Operator sessions are stateless bearer tokens with an 8-hour TTL, and `logout` does not invalidate
them.** It clears the cookie; the token stays valid until it expires. There is no denylist — none
exists in the codebase (searched at authoring; no revocation store).

**The paired tier is the product's only revocable session.** Verified at
`services/api/internal/server/server.go:787-795`: on logout, a token whose scope is `approvals@1` *is*
revoked through the pairing store.

**A detail the criteria did not record, and a reader deserves it:** that revoke is **best-effort**. If
the database is unavailable the failure is logged, logout still succeeds, and the paired session
survives until its own row expiry. The row's liveness is the fallback — deliberately, so a database
outage cannot trap an operator in a session — but it means revocation is not *guaranteed* at the
moment it is requested.

Mitigations in force: 8h TTL, `HttpOnly; Secure; SameSite=Strict`, same-origin deployment (ADR-0047).
Generalized session revocation is **B-032, v1.1**.

---

## 4. Single-operator posture (DOC-33, S-23)

**There is one operator account.** No multi-operator ACL, no roles beyond the capability edges, and
**no segregation of duties**: the operator who launches a governed action is the same one who can
approve it. Beta says so plainly rather than implying otherwise. Multi-operator authorization is
post-Beta (G14).

The paired tier (v0.12) is a *second session*, not a second operator — narrowed to `approvals@1` and
off by default.

---

## 5. The audit chain, and what it still does not prove (DOC-34)

**What closed this release.** Chain heads are Ed25519-signed in-service, anchored to an append-only
file outside Postgres, and verifiable offline by `aireos-verify` using the public key alone — no
database, no network, and no trust in our control plane (ADR-0069).

**Proven, not asserted (measured):**

- **SEC-43** — an event was tampered *below* a signed head and the chain recomputed from genesis so
  that `chain.Verify` returns **INTACT**; both verifiers still rejected it. Green on **both** stacks.
- The tamper requires **owner** privilege, not merely write access: `audit_events_no_mutate` has
  guarded the table since v0.1.0, and dropping it is an owner operation. T-127b proved this
  executably — the UPDATE is rejected while the trigger is armed and succeeds once it is disabled.
  ADR-0069's original threat statement said "write access" and was corrected before it shipped.
- **SEC-45** — a single mutated byte in either signed artifact fails signature verification.

**The residual, stated as plainly as the controls.** The signer and the signed sit inside one trust
boundary: **an RCE on `audit-service` itself can sign forged heads.** The key is held *alone* in
`/etc/aireos/audit.env` (`infra/offline/install.sh:294`) rather than in the shared environment file,
precisely so an RCE on any *other* service cannot — had it gone in the shared file, this residual
would have read "any service" on the day it shipped.

**Anchor divergence is classified three ways**, because every false tamper alarm spends credibility
needed for a real one:

1. **behind by one** — benign crash window, self-healed at startup;
2. **behind by many** — the *writer* is broken (disk, permissions, path): an **operational alarm, not
   a tamper finding**. A full disk must not masquerade as an attack.
3. **ahead** — the truncation signature.

A missing anchor is recreated **with an initialization marker**, never silently rebuilt from the
database: rebuilding would let an attacker who truncated both produce a file agreeing perfectly with
the truncated chain.

**Successors, with triggers.** An **external witness / transparency log** (v0.14 containment ADR) is
what actually closes the RCE residual. **Database privilege separation** — a non-owner service role on
`audit_events` — is arguably stronger than signing and is deferred to v0.14; it was declined for
v0.13 because it would have been the *fifth* boundary-rule candidate, the tripwire that says scope
has gone elastic.

---

## 6. Provenance, and the claim we deliberately do not make (ASI04, DOC-35)

Both shipped artifacts — the core bundle **and** the postgres companion, which is the one that runs as
root — carry detached Ed25519 signatures and coverage-checked SBOMs. Verification is a
copy-pasteable `openssl` command, so **the verifier is not ours to distribute**; only the public key
needs an out-of-band channel (ADR-0070).

**No SLSA level is claimed, at all.** This repository has no CI and no remote, so a hosted-builder
attestation is *unavailable* — not deferred. The honest description is **build-host attested**.

What the signature does **not** prove: that the build host was clean, that the toolchain was
unmodified, or — from the signature alone — which source revision it came from.

**Separately, and not part of what the signature attests:** every shipped Go binary embeds its own
`vcs.revision` / `vcs.modified` (Go's `buildvcs` stamping survives `-trimpath -ldflags "-s -w"`). That
is a real, checkable provenance fact, but it is **self-reported by the build and unsigned**, and it is
only meaningful when the release is cut from a clean tree. **The bundle reproduces byte-for-byte** from
the same commit, measured twice — including with the Go build cache wiped between the two builds, so
the result is not a cache artifact (`docs/releases/v0.13.0/reproducibility-report.md`).

**Positioning, corrected (DOC-35).** Microsoft's agent-governance-toolkit independently arrived at
roughly five of our seven controls — **including the credential-broker property we had treated as
differentiating** — and it both chains *and* HMACs each audit entry. Our genuinely unmatched control is
the **air-gap posture**: the whole governance spine runs with no SaaS dependency and no egress.
Recorded because a buyer will discover it, and it is better said by us.

---

## 7. Residual register

| # | Residual | Closes at |
|---|---|---|
| 1 | Spawned-process network activity is ungoverned (§2) | **v1.0.0 Beta 1** — B-040, WD-12 (deferred past v0.14.0, CEO-ratified 2026-08-19; corrected here from an earlier "v0.14" target) |
| 2 | `audit-service` RCE can sign forged heads (§5) | v0.14 — external witness |
| 3 | The Postgres owner role can drop the append-only trigger (§5) | v0.14 — privilege separation |
| 4 | Operator logout does not invalidate the token (§3) | v1.1 — B-032 |
| 5 | Paired-session revoke is best-effort on database failure (§3) | open — recorded here first |
| 6 | No segregation of duties (§4) | post-Beta — G14 |
| 7 | A compromised build host produces a validly-signed artifact (§6) | needs a trusted builder |
| 8 | First-contact trust: artifact and key over one channel (§6) | fingerprint published; cannot be forced |
| 9 | No behavioural anomaly detection for in-grant rogue behaviour (ASI10) | unscheduled |
| 10 | **`remote.exec` can do everything the v0.14.0 file tools do, less legibly** — no typed bounds, no digest anchor, no disclosure line (§2a, ADR-0071 R1) | same as #1 — containment is what closes it |
| 11 | Live file transfer (`remote.file.*`) is proven end-to-end on the compose stack only, not the installed stack (§2a, ADR-0071 R2) | promotion trigger: the first release that adds a WD touching file transfer |

Residuals 1, 2, 3 and 10 share one shape: **the enforcement and the thing being enforced sit inside a
single trust boundary.** That is the honest through-line of this release, and it is what a containment
layer and an external witness begin to address.
