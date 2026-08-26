# v0.14.0 — verification runbook

**Release: "Governed Tool Surface". Gate: WD-1..11 both stacks + smoke + the SEC-52 capstone + a CEO
`§human` pass (§2 of the release criteria — no new WD is minted; the gate that substitutes for one).**

Steps 0–2 are automated and were recorded at **T-144** (capstone) and **T-144b** (SEC-52 security
review). Step 3 reuses the **existing** v0.13.0 release key — this release mints no new keypair. Step 4
is **yours** — the `§human` pass is the release's first real contact with the file tools outside a test
harness, and no automated run may perform it.

---

## Step 0 — sync the RC

```bash
git -C /d/1_PROJECT/PRIVATE_WORK/AI-Agentic-OS/ai-remote-engineering-os log --oneline -1
```

`main` and `dev` must be equal and the tree clean. As with v0.13.0: no commit may land after the build
in Step 1 — a docs-only commit changes every binary's `vcs.revision` and therefore the artifact hash.
If anything is committed after the build, rebuild before signing.

---

## Step 1 — the automated gates (recorded at T-144 / T-144b)

| Gate | What | Where recorded |
|---|---|---|
| **VER-17** | WD-1..11 both stacks — compose 61 PASS/0 FAIL (`INTACT+ATTESTED` through seq 653); installed `WD1_EXIT=0`, 8 PASS/0 FAIL (`INTACT+ATTESTED` through seq 242); SEC-43's negative proof correct on both | `v0.14.0-release-criteria.md` §14g |
| **VER-18** | `task smoke` — 21 passed, including the `remote.file.write` leg (T-143's existing leg satisfied this; no padding added) | §14g |
| **VER-2 / CON-13** | Zero OpenAPI fragment change, zero codegen drift, across the whole release | §14g |
| **VER-26 / OI-4** | Core bundle 44.7 MB gz vs the 150 MB cap | §14g |
| **SEC-52** | Cumulative security review before tag — no exploitable defect in what this release changed; one latent finding (SEC-52-1) fixed with zero behavior change at T-145 | `v0.14.0-security-review.md` |
| **VER-21 / F-1** | `task release-check` now states plainly whether provenance was verified or self-skipped; the concrete stale-`.sig` instance T-144 found is fixed in both build scripts | this ticket, T-145 |

**Reproduce the regression yourself before signing**, against the exact build you are about to sign —
do not accept the T-144 record on faith:

```bash
task e2e
```
```bash
MSYS_NO_PATHCONV=1 bash infra/offline/wd1/run-wd1.sh
```
```bash
MSYS_NO_PATHCONV=1 task smoke
```

Assert the **positive** markers, never the absence of failures: `ok aireos.local/tests/e2e`,
`WD1_EXIT=0`, and `INTACT+ATTESTED` from the chain verify. A run that merely prints no `FAIL` has not
passed.

---

## Step 2 — provenance, with the SAME key as v0.13.0

**No new release keypair is minted this release.** The v0.13.0 production key (custody per
`docs/devops/offline-install-v3.md` §Release key custody) signs these artifacts too — a new key per
release would defeat the point of an out-of-band, human-held trust root.

Build, then sign with the existing key:

```bash
task offline-bundle
```
```bash
MSYS_NO_PATHCONV=1 bash infra/offline/postgres/build-postgres-companions.sh
```
```bash
task sign-release -- --key /path/to/aireos-release.key
```

Then both provenance gates, against the real key — this is VER-21's closed obligation, restated for
this release: `task release-check` will now tell you plainly, in its closing summary line, whether the
provenance leg verified something or self-skipped. Confirm it says **VERIFIED**, not
**SELF-SKIPPED**, before proceeding:

```bash
RELEASE_PUBLIC_KEY=/path/to/aireos-release.pub task provenance-check
```
```bash
RELEASE_PUBLIC_KEY=/path/to/aireos-release.pub task sec45-check
```

**Confirm no stale `.sig` survived a rebuild** — the concrete defect T-144 found and this ticket fixed.
List `dist/offline/*.sig` and check each signature's mtime is not older than its artifact's:

```bash
ls -la dist/offline/*.tar.gz dist/offline/*.tar.gz.sig
```

If you rebuilt an artifact of the same name during this session, its old `.sig` should already be gone
(removed automatically by `build-bundle.sh` / `build-postgres-companions.sh`) — a `.sig` present after a
rebuild is either freshly re-signed in Step 2 above, or a bug in this fix.

---

## Step 3 — GATE, the CEO `§human` pass (VER-19)

**This is the release's first real dogfooding contact with the file tools.** No automated harness
grants an agent token and drives the cockpit through a read, a write and a denial — do it as an
operator would, and record findings rather than suppressing them (VER-19).

**3.0 — Prerequisite.** An installed stack (or the WD-1 compose stack) with at least one governed SSH
resource — the loopback demo target from `docs/devops/quickstart-first-governed-action.md` works.

**3.1 — Mint a scoped agent token that names a file verb.** Per ADR-0071 §D7, an empty operations list
is unrestricted for `remote.exec` alone — it grants nothing for the file tools. Mint (or grant) a token
whose operations list explicitly includes `file.read` (and `file.write` if you intend to test the write
leg) for the target resource.

**3.2 — Read a file.** Drive an agent call to `remote.file.read` against a real path on the resource
(e.g. `/etc/hostname`). Confirm in the cockpit's approval surface that the prompt reads *"An AI agent is
requesting to read a file from a governed resource"* with the consequence line *"The file's contents
will be disclosed to the agent."* Approve it, and confirm the read completes and the returned content
matches the file on the host.

**3.3 — Write a file under approval.** Drive an agent call to `remote.file.write` against a scratch
path (do not target a file you care about). Confirm the prompt shows the **"Changes the host"** badge,
the descriptor (`remote.file.write <path> (<size> bytes, sha256:<digest>, mode <mode>)`), and the
consequence line *"You are approving the path, size, digest and mode shown below — not the file's
contents."* Approve it, then verify on the host that the file now exists with the expected bytes and
that its digest matches what the prompt showed.

**3.4 — Watch a denial.** Repeat 3.2 or 3.3 and **deny** it from the cockpit. Confirm the invocation
resolves to `denied` (not `failed`), the target host is unchanged, and the denial is visible on
`/audit` or `/invocations` with the correct terminal outcome (AUD-44).

**⚠ 3.4a — KNOWN, found during the §human pass itself (2026-08-25): the approval queue goes stale.**
`/invocations` does **not** auto-refresh, and a parked approval expires on policy's **120-second TTL** —
after which the TTL sweeper marks it `denied` and the invocation `failed`. So an operator who leaves the
page open for more than two minutes is looking at **approval prompts that are already dead**, with live
Approve/Deny buttons. The backend is correct (`GET /v1/invocations` returns `[]`, and the DB shows
`denied` + `failed` consistently) — **only the rendered page is stale.** Hit **Refresh** before trusting
the queue. Recorded rather than fixed: v0.11's E-07 live refresh does not cover this route, and adding
it is a UI change this release explicitly excludes. **Consequence to weigh at v1.0: a 120s TTL against a
manually-refreshed queue is a short window, and the operator's mental model breaks silently.**

**⚠ 3.3a — FINDING from the §human pass (2026-08-25): the digest wraps MID-HASH.**
The descriptor block uses `break-all` (T-143, chosen to avoid a horizontal scrollbar), so at the
cockpit's width the sha256 splits across lines at an arbitrary character:

```
remote.file.write /tmp/aireos-human-pass.txt (25
bytes, sha256:e3339a969bfd2d73e7c5b61fbccd830aa2
7bd15aab6d2c8774177ec9c23129b3, mode 0644)
```

**The digest is the ONE value the consent model asks the operator to compare character-by-character** —
D3's whole narrowing is "you are approving the digest, not the bytes." Breaking it at an unpredictable
point makes that comparison harder than it needs to be: the operator must mentally rejoin the hash
across a line break before diffing it against the source. The prompt is *accurate*; the **affordance for
the thing it asks you to verify is weaker than the claim it rests on.**

Not fixed in v0.14.0: the obvious fixes either change the **descriptor format** (whose per-tool prefixes
are a security control, criteria §14e) or need client-side parsing of it (explicitly forbidden at T-143
— a second parser in a second language would drift). **A CSS-only fix cannot isolate the digest without
parsing.** → **v1.0 candidate**, with the honest framing: *if we ask an operator to verify a digest, the
UI should make verifying it easy.*

### ✅ VER-19 RESULT — §human pass COMPLETE, 2026-08-25. All four legs passed.

Driven by the CEO on the review stack (compose + the cockpit dev proxy). **Chain evidence captured
live**, not asserted:

| Leg | Result |
|---|---|
| **3.2 read** | Approved. Content + digest returned; chain seq 30-37. |
| **3.3 write** | Approved. **FOUR independently-computed digests agreed** — client-side before the call, the approval prompt, the broker's response, and `sha256sum` **on the target host**. File landed 25 bytes, mode 0644, matching the descriptor exactly. |
| **3.4 denial** | Denied. Agent got a clean tool error (`isError:true`), **not** a crash. |

⭐ **The read/write chains prove the central claim in order:** `approval.granted` (34/42) precedes
`credential.leased` (35/43). **The credential is not leased until a human approves.**

⭐ **The denial proves it by absence.** Its chain is four events —
`tool.invoked` → `policy.decision:requires_approval` → **`approval.denied:operator`** →
`tool.result:error`. The approved write was six. **The two missing events are `credential.leased` and
`remote.file.write`**: no lease, no egress, host never contacted, and `/tmp` left with no orphaned temp
file (execution never began). The `:operator` suffix distinguishes a human denial from a TTL expiry.

**AUD-42 held on every leg** — the chain carries `sha256:` digests, never file content, in either
direction.

**Two findings recorded (3.3a, 3.4a), neither blocking.** Both are affordance issues in the operator
surface, not defects in the governance path.

⚠ **A third, procedural: the Commander's own verification command was wrong.** The guide said
`docker compose exec sshd ls -l /tmp/...` **without `MSYS_NO_PATHCONV=1`**, so git-bash rewrote `/tmp/`
to a Windows path and `ls` reported "No such file" **about the host, never entering the container.**
The right answer for the wrong reason. Re-run correctly it passed — but **a verification that checks the
wrong thing and agrees with you is worse than one that fails.** The `tar --force-local` /
`MSYS_NO_PATHCONV` rule already existed in `ops_conventions`; it was not applied.

**3.5 — Record findings.** Whatever you observe — friction, unclear copy, a surprising failure mode —
record it here or in a follow-up ticket rather than letting it go unrecorded. VER-19 exists specifically
so this contact happens and is not suppressed.

---

## Step 4 — sign-off and tag

Sign off only if Steps 1–3 are green **and** the release notes' non-claims still read as true — in
particular that live file transfer really is proven on one stack only, and that a parked write really
does fail closed on a gateway restart (neither of those is expected to have changed; if either has,
the release notes need a correction before this document is signed off on).

```bash
git tag -a v0.14.0 -m "v0.14.0 — Governed Tool Surface"
```

**The tag is local. This repo has no remote — never push, including tags.**

Tag the commit the artifacts were **built from**. If any commit landed after the build, go back to
Step 1: the artifact you are about to attest would describe a tree that no longer exists.
