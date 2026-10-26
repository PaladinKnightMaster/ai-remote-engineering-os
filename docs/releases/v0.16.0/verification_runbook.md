# v0.16.0 — verification runbook

**Release: "Public Readiness". Gate: WD-1..15 both stacks + `task smoke` + the five new instrument
gates, each mutation-proven + a CEO §human pass on the published-repo experience (§2 of the release
criteria) — NOT "does the feature work" but "does a stranger succeed" (PUB-6).**

Steps 0–2 are automated and were recorded at **T-163** (capstone). **The rest is the CEO's, in this order:
Step 3 sign → Step 4 PUB-6 → Step 5 tag + push.** ⚠ Corrected 2026-10-05: an earlier version put PUB-6
before signing, but the install path says a missing `.sig` means stop, so PUB-6 cannot run on unsigned
artifacts. Signing creates no commit, so it does not disturb the artifacts' attestation.

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

## Step 1b — the two publication blockers — ✅ RESOLVED 2026-09-02 (`8faec48`)

`SECURITY.md` routes reports through **GitHub private vulnerability reporting** (enabled at publication —
PUB-9), and `NOTICE` names **The AI Remote Engineering OS Project (github.com/paladinknightmaster)**.
Neither placeholder remains [ran: `grep -c "CEO to supply" SECURITY.md NOTICE` → 0 and 0].

---

## Step 2 — DOC-16: what this release does and does not do

See `release_notes_v0_16_0.md`, opening section. Restated here because it gates sign-off: **no new
governed capability, no governance-plane runtime change, no new WD** — the PUB-6 fixes change the
installer's re-run behaviour and the cockpit's onboarding display only (release notes). If anything in Steps 1–1a above reads as a behavior
change to a stranger installing the product, that is a defect in this runbook or the release, not an
acceptable surprise for PUB-6 to discover.

---

## Step 3 — sign, then both provenance gates

Sign once Step 1's gates are green. Signing writes `.sig` files beside the artifacts and creates **no
commit**, so the attestation in Step 1a is undisturbed. ⓘ T-162's mixed-version guard runs here live for
the first time; `dist/offline/` must hold exactly the two v0.16.0 artifacts.

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

---

## Step 4 — GATE, the CEO's `§human` pass (PUB-6) — on the SIGNED artifacts

**This differs in kind from every prior release's `§human` pass.** VER-32 (v0.15.0) asked *"does the
feature work"* on a stack the CEO already knew how to run. PUB-6 asks a different question: **does a
stranger succeed**, using *only* the public `README.md` — no tribal knowledge, no reference to this
repo's internal docs, no memory of how previous releases were installed.

**4.1 — Start from a genuinely fresh host** (a clean VM or container the CEO has not already provisioned
for this product). Do not reuse a host that still has `dist/offline/`, prior keys, or a prior install's
`/etc/aireos/` state on it — that would let tribal knowledge substitute for the README without anyone
noticing.

**4.2 — Follow only `README.md`.** Every step needed to get from "I have the artifacts" to "the product
is installed and I can log in" must be findable there. **Any step requiring knowledge absent from the
README is a defect, not a note** — record it plainly rather than silently filling the gap from memory.

**4.3 — Verify the artifacts using only what the README and `SECURITY.md`/the published fingerprint say**
— not by reaching for `docs/devops/offline-install-v3.md` or this runbook.

**4.4 — Record findings.** Friction, unclear copy, a missing prerequisite, a surprising failure mode —
record it here rather than letting it go unrecorded, exactly as VER-32's Step 3.4 did.

### Rehearsal — 2026-10-05, by the Commander (does NOT discharge PUB-6)

Full record: [`pub6-rehearsal.md`](pub6-rehearsal.md). A fresh `ubuntu:24.04` + systemd host with **no
network at all** walked the README path on fixture-signed copies of the artifacts. **It found that the
postgres companion fails on any host patched after the companion was built** — OS package version skew,
proven causally, present since v0.1.0 (ADR-0052), and **structurally invisible to WD-1**, which builds its
host and the companion from one snapshot. That limitation is now stated in the README and both install
guides with a bring-your-own-PostgreSQL workaround; the real fix needs its own ADR. Nine documentation
defects were fixed in the same pass. Everything after postgres — signature verification with a tamper
control, a fully offline `install.sh`, seven active services, a working login — verified clean.

**What this means for your pass:** on a patched host you **will** hit the companion failure. That is now
expected; follow the README's documented workaround and judge whether *that* path is followable from the
README alone. Quickstart steps 3, 5 and 6 (the browser UI) were not exercised in the rehearsal — Docker
Desktop's HTTPS port-forwarding on the Windows host breaks native browsers — so nobody but you has tested
them.

### First PUB-6 run — 2026-10-14, by the CEO — **FAILED on documents; PUB-6 is NOT discharged**

Full record: [`pub6-ceo-run.md`](pub6-ceo-run.md). On a fresh Ubuntu 24.04 VM, with the production-signed
artifacts, **the product passed every step and the documents failed the stranger test** (no signposted
start in the install guide, a verification block that covered one artifact of two, a placeholder pasted
verbatim, a resource id no screen showed, and two success claims that were false). The fix pass is
T-164/T-165/T-166 (criteria §10). **The re-run happens on a FRESH host after the artifacts are rebuilt from
the fixed commit and re-signed**.

### Re-cut — 2026-10-14, artifacts rebuilt from `7ca592f` (published as `e43cece`) after the fix pass (T-164…T-166)

| Gate (re-cut, artifacts built from `7ca592f` (published as `e43cece`), `vcs.modified=false`) | Result |
|---|---|
| `task wd1` (installed stack) | exit 0; `ok aireos.local/tests/e2e 653.157s`; chain `INTACT+ATTESTED through seq 239`; SEC-43 forged chain → `audit-service verify` BROKEN exit 1, offline `aireos-verify` BROKEN exit 1 |
| quickstart shell gate | exit 0 — generate / bind / `--undo` from the bundle's own script |
| quickstart browser gate | exit 0 — spec A 1 passed (first execution of T-166's P10/P12 assertions), spec B 1 passed + completion marker |
| quickstart browser gate, `no-bind` (negative leg) | exit 1 as required — the Run ends `failed · gateway step: unexpected status 502`, never `completed` |
| `task smoke` | exit 0 — 25 passed / 2 skipped (the quickstart specs, run by the gate above) |
| sign (production key) | exit 0 — 2 artifacts signed + self-verified, fingerprint `968a29b3…` |
| provenance-check | exit 0 — both verify against `aireos-release.pub`; SBOM coverage 9/9 executables, 92/92 packages |
| SEC-45 | **PROVED** 14 passed / 0 failed against `aireos-release.pub`. ⓘ A first run without `RELEASE_PUBLIC_KEY` used the FIXTURE key and its control correctly FAILED (exit 201, 10/4) — an unplanned negative control: the gate refuses a key that did not sign the artifacts |

### PUB-6 RESULT — ✅ **PASSED** (CEO, re-run on the `fresh-install` Hyper-V checkpoint, recorded 2026-10-25)

A fresh, patched Ubuntu Server 24.04 host; the re-signed `7ca592f` (published as `e43cece`) artifacts; `README.md` and the guides it links,
nothing else. The CEO's report: *"works well"* — install through the quickstart's completed, audited action.
The one `[assumed]` claim T-164 carried, the CA-root path in the install guide's "Trusting the certificate", was
**confirmed on the host** ("cert path was fine"). No friction lines were reported. Tagged `v0.16.0` → `7ca592f` (published as `e43cece`)
with the derived command below.

---

## Pre-publication history rewrite — 2026-10-25 (before the first push)

**What changed.** At the CEO's decision, every commit's and tag's author/committer/tagger email was rewritten
from a personal address to the GitHub `noreply` address before the repository was published. The v0.16.0 tag
commit `7ca592f` became **`e43cece`**. Every commit ID changed; **no file content did.** Old IDs cited anywhere
in this repository resolve through [`pre-publication-commit-map.md`](pre-publication-commit-map.md).

| Check | Result |
|---|---|
| Backup | git bundle of all 20 refs, verified and **proven restorable** (445 commits, 16 tags) — kept outside the repo |
| Trees | **identical** for all 20 refs before vs after (`<ref>^{tree}` compared) |
| Email | personal address in commit headers **445 → 0**; in tag headers **16 → 0**; never present in file content, commit messages or tag messages (checked with a header-count control) |
| Core bundle | rebuilt from `e43cece` (`vcs.revision=e43cece…`, `vcs.modified=false`), SHA-256 `e208d12e…` |
| Equivalence to the PUB-6-tested build | same 26 files; every non-binary identical except `SHA256_MANIFEST.txt` (hashes of the changed files) and `SBOM.cdx.json`; **each of the 8 Go executables is byte-identical to the tested one after substituting the revision string and Go build ID — the only remaining 20 bytes lie inside `.note.gnu.build-id`**, which the linker derives from the build ID. ⓘ The SBOM now names the services `v0.16.0`; the tested build's SBOM had named them `v0.15.1-0.20260901155929-53782dde02ef` because the tag did not exist yet when it was built |
| Postgres companion | **not rebuilt** — it records no commit ID (0 hits; control: the old core bundle had 2 per binary); its `61aff02d…` artifact and signature are unchanged |
| Sign / provenance / SEC-45 | core re-signed (production key, fingerprint `968a29b3…`); provenance-check exit 0 (both verify; SBOM 9/9 + 92/92); **SEC-45 PROVED 14/0** |
| Installed host | on a fresh systemd host with the rebuilt bundle + the unchanged companion: quickstart shell gate **PASS** (generate / bind / `--undo`, exit 0); browser gate **PASS** (spec A + spec B, completion marker present, exit 0) |

PUB-6 was **not** re-run: the software is the same bytes apart from the revision identifiers above, which is
what PUB-6 does not test.

## Step 5 — tag, then push — push LAST

**Tag the commit the artifacts attest to — derived, never typed.** Any sha written into this runbook goes
stale the moment the runbook is committed (this section once named `fe4c5bd`; `8faec48` and `a15dc4b`
superseded it in turn), so it reads the sha out of the artifact instead and refuses on any mismatch:

```bash
REV=$(grep -a -o 'vcs.revision=[0-9a-f]*' dist/offline/aireos-offline-0.16.0/bin/api | head -1 | cut -d= -f2)
[ "$REV" = "$(git rev-parse HEAD)" ] && [ -z "$(git status --porcelain)" ] || { echo "STOP: artifacts do not attest a clean HEAD - rebuild and re-sign"; exit 1; }
git tag -a v0.16.0 "$REV" -m "v0.16.0 Public Readiness"
```

⚠⚠ **If it prints STOP, a commit landed after the build:** rebuild both artifacts from the new `HEAD`,
re-sign, and run it again. The tag must not be moved once set (ADR-0070 D1).

**At publication, two more steps, both `block`:** **PUB-9** — enable GitHub private vulnerability reporting
and confirm it from a signed-out view; **PUB-10** — create a GitHub Release carrying both artifacts, both
`.sig` files and `aireos-release.pub`, with the fingerprint in its notes. README step 1 sends strangers to
that Releases page.

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
