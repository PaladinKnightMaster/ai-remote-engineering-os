# v0.14.0 — Governed Tool Surface

**The release that widens what an agent can reach, without widening what we trust.**

Since v0.3.0 the gateway registry has held six tools, five of them control-plane. **Exactly one reached
the outside world.** v0.14.0 adds three more — `remote.file.read`, `remote.file.write`,
`remote.file.list` — on the same trust boundary `remote.exec` already crosses: same edge, same
credential custody, same sole-SSH-egress broker, same policy decision point, same approval primitive,
same chained-hash audit. No new daemon, no new datastore, no new network path, no new key.

---

## What shipped

**The `remote.file.*` family (ADR-0071).** Read, write and list one file/directory at a time on a
governed SSH resource, reusing the invocation, approval and audit machinery `remote.exec` already has.
`execAndFinish` and the `Broker` interface became operation-dispatching; everything above that seam —
the concurrency cap, the approval park, idempotency, the fail-closed rule, the audit brackets — is
byte-for-byte unchanged (proved by the existing suite staying green with zero test edits at T-138).

**Typed, bounded, digest-anchored — properties `remote.exec` does not have.** A 1 MiB default transfer
cap enforced both directions; the read path refuses an over-cap file rather than truncating it (a
truncated file is a corrupt file, not partial evidence); a write's execution step recomputes the sha256
over the bytes about to be written and refuses on any mismatch against the approved descriptor,
**before** the broker is called (SEC-49) — closing the same class of vector as the v0.3.0
approved-command-swap fix, on a path that deliberately holds the approved content only in memory for
the blocking call rather than in the database (§Residuals below).

**A scope narrowing found during authoring, not after (ADR-0071 §D7).** SEC-16's empty-operations
back-compat — "no operations list means unrestricted" — is now **`remote.exec`-only**. Every file verb
must be explicitly present in an agent token's scope. Left unfixed, every agent token ever minted
without an explicit operations list would have silently gained file read and write the moment the file
tools registered; no test would have caught it. Proven by a negative test in both directions (SEC-48).

**The operator surface tells the truth about what changed (T-143, FE-43).** `<ApprovalPrompt>` now
derives its description, payload label and consequence line from the invocation's persisted tool name
instead of a hardcoded "run a command" — see `docs/security/governed-tool-reference.md` (new, DOC-37)
for the full operator-facing reference: what each tool does, what approving it authorizes, and what the
digest binds.

**The toolchain moved off a year-stale line (ADR-0072, T-146).** `go 1.23`'s last stdlib security patch
shipped 2025-08-06 — this product had been built on it for roughly a year, **while our own signed
CycloneDX SBOM recorded that fact**. The clause that pinned it (ADR-0041) was a point-in-time snapshot
from the repo's skeleton commit with no refresh trigger; none of its four review-trigger conditions
could ever reopen it on toolchain age alone. ADR-0072 replaces it with a standing support-window rule,
re-checked every release. **This release moves the *builder* to Go 1.26** (the mature supported line,
not 1.27, which shipped the same week with zero field time) — the `go` directive in each `go.mod` is
deliberately **not** raised; raising it separately opts into new GODEBUG crypto/TLS defaults, which is
its own decision on this product's SSH/PASETO/Ed25519 path and is deferred with its own trigger.
`golangci-lint` moved to v2.13.0 in the same ticket — the entire v1 line does not run on Go 1.26.

---

## Finding F-1 closed, honestly (VER-21)

v0.13.0's capstone recorded **F-1**: `task release-check` green does not, by itself, prove provenance
was verified — `provenance-check` self-skips (loudly) when no bundle has been built yet in the current
run, which is the common state of a dev-loop invocation. **The self-skip is correct and stays**: a hard
failure there would wrongly block ordinary development. What was missing was the aggregate *saying*
which of the two outcomes happened. `task release-check` now closes with an explicit summary line
naming whether provenance was verified (and how many artifacts) or self-skipped — so a green run can no
longer be misread as "everything was checked" when it may only mean "there was nothing to check."

**A concrete instance of exactly what F-1 warned about surfaced at T-144.** Rebuilding the postgres
companion left `aireos-postgres-ubuntu2404.tar.gz.sig` — five days stale — sitting beside an artifact it
no longer matched, looking entirely valid on a directory listing. `provenance-check` would have caught
the mismatch, but only if someone ran it before shipping. Both build scripts
(`infra/offline/build-bundle.sh`, `infra/offline/postgres/build-postgres-companions.sh`) now remove any
stale sibling `.sig` the moment they write a new artifact of the same name. A missing signature is
honest — it says "not yet signed." A wrong one is dangerous — it says "trust this" about bytes it never
attested.

---

## SEC-52-1 — mixed provenance at the allow-now call site (fixed, zero behavior change)

The v0.14.0 cumulative security review (`v0.14.0-security-review.md`, T-144b) found that the gateway's
two `performAndFinish` call sites read from different sources: the post-approval site already took
`ResourceID`/`Command`/`Tool` entirely from the persisted invocation row, but the allow-now site took
`ResourceID`/`Command` from the caller's request and only `Tool` from the persisted row. **Not
exploitable** — the allow-now branch is reachable only when the invocation was just created from that
same request, so the values are equal by construction — but the safety rested on an invariant invisible
at the call site, and the T-138 fix that closed the tool-confusion vector had moved only `tool`, not the
other two fields. Fixed at T-145: the allow-now site now reads all three from the persisted row, exactly
like the post-approval site. Runtime behavior is unchanged; the full test suite stayed green with zero
test edits, confirming the change altered nothing observable.

---

## What this release explicitly does **not** claim

**Live file transfer is proven on one stack, not two.** `remote.file.*` is exercised end-to-end on the
compose stack only (T-144). WD-1 does not exercise it, VER-17 does not require it, and this release
mints no new WD. Say this plainly rather than imply parity with `remote.exec`, which both stacks do
exercise.

**A parked write does not survive a gateway restart.** The bytes behind a write's SEC-49 digest live in
memory for the life of the blocking approval call — deliberately never persisted, so they cannot bloat
the gateway's audit ledger with every file ever written, forever, with no erasure path (the option that
was rejected on exactly that fact). The consequence: a gateway bounce while a write is parked fails that
invocation closed. This is the correct failure direction, and it is stated here rather than left for an
operator to discover in the field.

**`remote.exec` can still do everything the file tools do, less legibly.** `cat`, `base64` and shell
redirection reach the same files with no typed bounds, no digest anchor, and no disclosure line in the
approval prompt. This release makes the file family *better governed* than the tool sitting beside it —
it does not, and structurally cannot, close that gap. Only containment (B-040) constrains what an
approved command may do once it runs, and containment is **deferred, not cancelled**: the path is
v0.14.0 → v1.0.0 Beta 1, where layer 2 lands and the tool surface freezes together.

**SFTP had never once worked end-to-end until the capstone found it.** The e2e fixture's generated
`sshd_config` registered no SFTP subsystem, so every *live* file call failed with
`ssh: subsystem request failed` — through five tickets and three Commander reviews (T-139a → T-143),
because every unit test stubbed the SFTP layer and T-139a's own tests used a different in-process
server. The fix was a one-line fixture change (`Subsystem sftp internal-sftp`); the finding is that the
gap survived that much scrutiny — a direct instance of this release's own diagnosis that every
walkthrough is synthetic until proven otherwise. Recorded here rather than quietly folded into "all
gates green," because a release that hides how its worst near-miss was caught teaches nobody.

**An operator approving a write approves path, size, digest and mode — not the file's contents.** This
is a genuine narrowing of "informed consent," stated plainly rather than only inside the ADR. The digest
binds what is written to what was approved; it does not mean the operator reviewed the bytes. See
`docs/security/governed-tool-reference.md` for the full statement and its residual.

Full detail and the residual register: `docs/security/threat-model.md` §2a and §7.

---

## Install-wiring diff: zero

Unlike v0.13.0 (two additions) or v0.12.0, this release changes nothing about `install.sh` or the
on-host provisioning path. The hard constraints held: no new daemon, no new datastore, no new network
path, no new key, and no migration (the tool family's scope lives entirely in existing free-form
operation-list columns). The only infrastructure-script changes are the toolchain move's container image
references (`golang:1.23` → `golang:1.26` in the live build/test/e2e scripts) — archival scripts and
runbooks that record what actually shipped at the time are deliberately left untouched.

---

## Upgrading

No migrations. No new keys, no new environment variables. `install.sh` is unchanged. An existing
v0.13.0 install upgrades by replacing the binaries and the SPA assets; the audit chain, its signing key,
and the release provenance key all carry forward unchanged.

---

## Verification

See `verification_runbook.md` for the full walkthrough, including the CEO's `§human` dogfooding pass
(VER-19: mint a scoped token, read a file, write a file under approval, watch a denial) — this
release's first real contact with the new surface outside a test harness.
