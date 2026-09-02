# v0.16.0 — verification runbook

**Release: "Public Readiness". Gate: WD-1..15 both stacks + `task smoke` + the five new instrument
gates, each mutation-proven + a CEO §human pass on the published-repo experience (§2 of the release
criteria) — NOT "does the feature work" but "does a stranger succeed" (PUB-6).**

Steps 0–2 are automated and were recorded at **T-163** (capstone). Step 3 (PUB-6) is **the CEO's** — no
automated run may perform it, that is the entire point of it. Step 4 (sign, tag, push) is the CEO's too,
once Steps 1–3 read green, in that exact order.

---

## Step 0 — sync, and the two environmental rules before you run anything

```bash
git -C /d/1_PROJECT/PRIVATE_WORK/AI-Agentic-OS/ai-remote-engineering-os log --oneline -1
```

`main` and `dev` must be equal and the tree clean before the build in Step 2 — a commit landing after the
build changes every binary's `vcs.revision` and therefore the artifact hash (rebuild before signing if
anything lands).

⚠ **VER-39 — pre-warm the Go module cache BEFORE `task e2e`, every time.** `task e2e`'s own
`docker compose down -v` removes the named `gomodcache`/`gocache` volumes on exit.

```bash
docker volume create aireos-e2e_gomodcache
docker volume create aireos-e2e_gocache
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd):/work" -w /work \
  -v aireos-e2e_gomodcache:/go/pkg/mod -v aireos-e2e_gocache:/root/.cache/go-build \
  golang:1.26 bash -c '
for m in packages/go/auth packages/go/middleware packages/go/migrate packages/go/outbox \
         services/api services/audit-service services/connector-service services/mcp-gateway \
         services/policy-service services/session-broker tests/e2e \
         tools/forbidden-features-check tools/migrate-lint; do
  (cd "$m" && go mod download)
done'
```

⚠⚠ **This run mounts the whole repo host-side and can rewrite the tracked `go.work.sum` with an
enormous, unrelated dependency set** (observed at this cut: 14 → 431 lines, cloud SDKs nothing in this
project imports). That is a side effect of workspace-mode resolution against the full mount, not a real
dependency change — `git diff go.work.sum` after this step and `git checkout -- go.work.sum` if it moved,
**before** building anything from the tree. It did not affect the actual `task e2e`/`task smoke`/`task
wd1` runs below (each runs its own compose build), but it would have polluted the commit if left staged.

⚠ **VER-40 — every integration leg starts from a FRESH database** (the T-073 lesson). `task e2e` and
`task wd1` each bring up and tear down their own compose stack, so this applies automatically to both
legs below; it matters if you re-run `task test-integration` separately.

---

## Step 1 — the automated gates (recorded at T-163)

| Gate | What | Evidence |
|---|---|---|
| **VER-46 compose** | WD-1..15 on `task e2e` | `ok aireos.local/tests/e2e`, **46 PASS / 0 FAIL**, chain `INTACT+ATTESTED through seq 716` (includes the SEC-43 forged-chain negative proof, both legs) |
| **VER-46 smoke** | `task smoke` | **23 passed / 2 skipped (quickstart-a/b, unaffected by this release) / 0 failed** (3.2m) |
| **VER-46 installed** | WD-1..15 on `task wd1`, offline bundle + postgres companion **rebuilt from HEAD** (`af85787`) first — see Step 1a's provenance correction: the SIGNING candidate was rebuilt again from `fe4c5bd`, no product code differs between the two | 8 systemd units `active`, Caddy serves HTTPS; **8 PASS / 0 FAIL**, chain `INTACT+ATTESTED through seq 239` before the deliberate final SEC-43 leg, which both `audit-service verify` and the offline `aireos-verify` correctly reported `BROKEN` |
| **staleness-check** | The three version-claim sites agree | `staleness-check OK — 13 self-descriptive claims match the tree (release 0.16.0, ADR head 0076, 76 ADRs, 0 Draft)` |
| **doc-check** | Referenced paths resolve, including this runbook's own self-reference from `CANONICAL_SOURCE_OF_TRUTH.md` | Re-run after this file was written — see closing line below |
| **block-criterion-check** | Every `block`+`SEC-` row in this release's own criteria doc names a real test | This release adds no new `SEC-` rows (PUB-1/VER-41/VER-43 are process/tooling gates covered by SEC-60/61/62, already `[test: none -- reason]`-marked at T-157/158/160) |

**Reproduce the compose + smoke legs yourself before signing**, against the exact build you are about to
sign — do not accept this record on faith:

```bash
MSYS_NO_PATHCONV=1 task e2e
```
```bash
MSYS_NO_PATHCONV=1 task smoke
```

Assert the **positive** markers, never the absence of failures: `ok aireos.local/tests/e2e`,
`INTACT+ATTESTED`, and Playwright's `N passed` line naming zero failed.

---

## Step 1a — the installed-stack leg (VER-46, T-033 lesson)

**The core bundle AND the postgres companion were both rebuilt from HEAD before this leg** — a stale
companion or bundle silently describes a tree that no longer exists (the standing T-033 lesson, and this
release's own VER-45 finding names the companion as version-independent content, which is exactly why it
is easy to skip rebuilding it and easy to be wrong about whether that is safe). **`task wd1`'s WD-1..15
PASS/FAIL evidence in Step 1's table was captured against this build** (commit `af85787`) and remains
valid regardless of the provenance correction below — no product/service/app/package file differs
between `af85787` and the final signing candidate `fe4c5bd`, only docs and this release's own capstone
commits, so the same binaries under test would have resulted either way.

⚠⚠ **PROVENANCE CORRECTION (Commander review, 2026-09-02): the FIRST artifact build was DISCARDED.**
The capstone doc commit (version bump + release notes + this runbook) was made **after** the bundle
above was built, which advanced `HEAD` past the commit the artifacts actually attested to — the exact
"a commit landing after the build changes every binary's `vcs.revision`" hazard this runbook already
warned about below, hit by this ticket's own first pass. Worse, the tree at `af85787` still declared
`BUNDLE_VERSION: "0.15.0"` in `Taskfile.yml` — the artifact's `VERSION=0.16.0` came only from the
container's `-e BUNDLE_VERSION=0.16.0` override, not from the tree a signature would attest to. That is
an internally inconsistent provenance chain in the release whose entire theme is trustworthy evidence.
**Fix applied**: the release notes' artifact-hash table was removed (hashes now land in the closure
commit, after signing — the v0.15.0 pattern, `53782dd`/`8126d34`) and committed as `fe4c5bd`, with
**nothing committed after it**. Both artifacts were then rebuilt a second time from `fe4c5bd` — the
commands below are that second, final build; their output is what the CEO signs. The first build's
files were overwritten in place (`build-bundle.sh`/`build-postgres-companions.sh` both clean their own
target paths before writing).

```bash
CI=true pnpm install --frozen-lockfile && CI=true pnpm --filter @aireos/web build
```
```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd):/work" -v gomodcache:/go/pkg/mod -w /work -e BUNDLE_VERSION=0.16.0 aireos-repro-builder:golang1.26-node bash -c 'command -v wget >/dev/null || (apt-get update -qq && apt-get install -y -qq wget >/dev/null 2>&1); bash infra/offline/build-bundle.sh'
```
```bash
MSYS_NO_PATHCONV=1 bash infra/offline/postgres/build-postgres-companions.sh ubuntu2404
```
```bash
MSYS_NO_PATHCONV=1 task wd1
```

⚠⚠ **BEFORE building anything, move the prior release's core bundle out of `dist/offline/` — do not
delete it.** `sign-release.sh` (T-162) now refuses to sign a directory holding more than one core-bundle
version, and `run-wd1.sh`'s own `assert_exactly_one_bundle` refuses to run against an ambiguous
`dist/offline/`. At this cut, `aireos-offline-0.15.0.tar.gz` (+ `.sig` + `.sbom.json` + its staged
directory) were moved to `dist/offline/_superseded/` before building v0.16.0. **The postgres companion
does not need this treatment** — it carries no `VERSION`/`BUNDLE_VERSION` dependence, so an older one
sitting beside a newer core bundle does not trip the guard; it was rebuilt anyway per this release's
explicit brief, not because leaving it would have failed anything.

Verify the artifact before trusting it:

```bash
tar xzf dist/offline/aireos-offline-0.16.0.tar.gz -O aireos-offline-0.16.0/VERSION
```
```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd)/dist/offline/aireos-offline-0.16.0:/b:ro" golang:1.26 \
  bash -c 'go version -m /b/bin/api | grep -E "vcs.revision|vcs.modified"'
```

**Observed on the FINAL (post-correction) build**: `VERSION=0.16.0`,
`vcs.revision=fe4c5bd5f0d33720fd3d587a99dfe44f8e97e901`, `vcs.modified=false` — matching
`git rev-parse HEAD` (`fe4c5bd`) at the time of this build, and matching `git show
fe4c5bd:Taskfile.yml`'s own `BUNDLE_VERSION: "0.16.0"` [ran: both commands, both positively confirmed].
**This is the commit the CEO tags.**

Unsigned artifact hashes are recorded nowhere in this repo (deliberately — see the release notes'
"Release provenance" section for why) and land in the closure commit, alongside the tag sha, once the
CEO has signed:

```bash
sha256sum dist/offline/aireos-offline-0.16.0.tar.gz dist/offline/aireos-postgres-ubuntu2404.tar.gz
```

⚠⚠ **Nothing may be committed between this rebuild and the tag.** Any further commit — even a
documentation fix — repeats exactly the defect this correction just fixed. If something must change,
change it, rebuild both artifacts again, and re-verify `vcs.revision` against the new `HEAD` before
tagging.

⚠ **Never read the build's success from a wrapper's exit code.** Capture it explicitly
(`> log 2>&1; echo "EXIT=$?"`) and read the log — `task` has previously reported `exit status 1` while a
shell wrapper reported `0` and the real code was `201` (v0.15.0 cut).

---

## Step 1b — the two publication blockers (not this ticket's to resolve)

⚠⚠ **`SECURITY.md` still carries `<SECURITY-CONTACT — CEO to supply before publication>`.** Verified
present [ran: `grep -n "SECURITY-CONTACT" SECURITY.md`] — not invented, not guessed.

⚠⚠ **`NOTICE` still carries `<COPYRIGHT HOLDER -- CEO to supply before publication>`.** Verified present
[ran: `grep -n "COPYRIGHT HOLDER" NOTICE`] — not invented, not guessed.

**Both gate the push, not this ticket.** Neither is a defect in this release's own work — inventing a
disclosure contact or a legal copyright-holder name would itself be an unverifiable claim, exactly the
thing this release exists to stop the product from making.

---

## Step 2 — DOC-16: what this release does and does not do

See `release_notes_v0_16_0.md`, opening section. Restated here because it gates sign-off: **no new
governed capability, no runtime change, no new WD.** If anything in Steps 1–1a above reads as a behavior
change to a stranger installing the product, that is a defect in this runbook or the release, not an
acceptable surprise for PUB-6 to discover.

---

## Step 3 — GATE, the CEO's `§human` pass (PUB-6)

**This differs in kind from every prior release's `§human` pass.** VER-32 (v0.15.0) asked *"does the
feature work"* on a stack the CEO already knew how to run. PUB-6 asks a different question: **does a
stranger succeed**, using *only* the public `README.md` — no tribal knowledge, no reference to this
repo's internal docs, no memory of how previous releases were installed.

**3.1 — Start from a genuinely fresh host** (a clean VM or container the CEO has not already provisioned
for this product). Do not reuse a host that still has `dist/offline/`, prior keys, or a prior install's
`/etc/aireos/` state on it — that would let tribal knowledge substitute for the README without anyone
noticing.

**3.2 — Follow only `README.md`.** Every step needed to get from "I have the artifacts" to "the product
is installed and I can log in" must be findable there. **Any step requiring knowledge absent from the
README is a defect, not a note** — record it plainly rather than silently filling the gap from memory.

**3.3 — Verify the artifacts using only what the README and `SECURITY.md`/the published fingerprint say**
— not by reaching for `docs/devops/offline-install-v3.md` or this runbook.

**3.4 — Record findings.** Friction, unclear copy, a missing prerequisite, a surprising failure mode —
record it here rather than letting it go unrecorded, exactly as VER-32's Step 3.4 did.

### PUB-6 RESULT — **PENDING.** Not run by this ticket; the CEO records the outcome here.

---

## Step 4 — sign, tag, push — in that order, push LAST

Sign off only if Step 1's gates are green **and** PUB-6 (Step 3) has passed. Do not proceed to sign, tag,
or push while either publication blocker (Step 1b) is still an open placeholder — both must be resolved
by the CEO before the push, though signing and tagging do not themselves require it.

**No new release keypair is minted this release.** The existing production key (custody per
`docs/devops/offline-install-v3.md` §Release key custody) signs these artifacts too.

```bash
task sign-release -- --key /path/to/aireos-release.key
```

Then both provenance gates, against the real key:

```bash
RELEASE_PUBLIC_KEY=/path/to/aireos-release.pub task provenance-check
```
```bash
RELEASE_PUBLIC_KEY=/path/to/aireos-release.pub task sec45-check
```

Confirm `provenance-check`'s summary says **VERIFIED**, not **SELF-SKIPPED**, and that no stale `.sig`
survived a rebuild (`ls -la dist/offline/*.tar.gz.sig`, mtime not older than its artifact).

**Tag `fe4c5bd`** — the commit the final artifacts in Step 1a were built from, confirmed by its own
`vcs.revision` stamp. ⚠⚠ **If ANY commit has landed since `fe4c5bd` (including from this runbook's own
edits) — STOP. Rebuild both artifacts from the new `HEAD`, re-verify `vcs.revision` positively, and tag
that instead.** The tag must not be moved once set (ADR-0070 D1) — this is exactly the mistake this
release's own capstone made once already (see Step 1a's provenance correction) and it must not repeat
at the tag step itself.

```bash
git rev-parse HEAD   # must print fe4c5bd5f0d33720fd3d587a99dfe44f8e97e901 -- if not, STOP per above
git tag -a v0.16.0 -m "v0.16.0 — Public Readiness"
```

**The tag is local. This repo has no remote — never push a tag, or anything else, until PUB-6 has
already passed.**

⚠⚠ **The push is the LAST act, after PUB-6 passes — not before.** Publishing before a stranger has
succeeded inverts this release's own thesis (§1 of the release criteria). Only after signing, tagging,
and a passing PUB-6 does creating the remote and pushing become in scope — and that is a decision this
runbook deliberately does not make for the CEO.

**Afterward:** a closure commit (the v0.14.0/v0.15.0 "tag + 1" pattern) records the tag sha in
`CLAUDE.md`'s and `CANONICAL_SOURCE_OF_TRUTH.md`'s "Latest release" lines, publishes the signed SHA-256
hashes and the release public key fingerprint in `release_notes_v0_16_0.md` (already
`968a29b3623fa79a6d12e37dfe4c9e156b6c66c716b0e57a076026a9d1bc0bbf` — no new keypair this release), and
applies §3a's repository-metadata proposal once a hosting remote actually exists.
