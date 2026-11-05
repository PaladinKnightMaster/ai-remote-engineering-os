# v0.17.0 — verification runbook

**Release: "Install & First-Run Experience". Gate: NO new WD. The gate is HUM-1 (a stranger who is not the CEO
installs from the public README with at most three server commands), the later-host test with the v0.16.0 recipe
as its negative control (SEC-63), the SEC-64…67 negative proofs, and a WD-1..15 regression on both stacks plus
`task smoke`** (§2 of `v0.17.0-release-criteria.md`).

Steps 0–1 are automated and run against artifacts built from the release-candidate commit. **The rest is the
CEO's, in this order: Step 2 sign → Step 3 HUM-1 → Step 4 tag, closure commit, push.** Signing creates no commit.
Nothing is pushed, tagged or signed without the CEO's direction (the repository is public).

---

## Step 0 — sync, and the environmental rules before you run anything

```bash
git log --oneline -1 && git status --porcelain
```

The tree must be clean, on `dev`, with the noreply identity (`git config user.email`). **A commit landing after
the build changes every binary's `vcs.revision` and therefore the artifact hash — rebuild before signing if
anything lands.** The tag is derived from the artifact (Step 4), never typed.

⚠ **VER-39 — pre-warm the Go module cache BEFORE `task e2e`, every time.** `task e2e`'s own `docker compose
down -v` removes the named `gomodcache`/`gocache` volumes on exit.

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

⚠⚠ **The pre-warm can rewrite the tracked `go.work.sum`** with a large unrelated dependency set (workspace-mode
resolution against the full mount). `git diff --stat go.work.sum` after it and `git checkout -- go.work.sum`
**before** building anything, then re-check `git status --porcelain` is empty.

⚠ **VER-40 — every integration leg starts from a FRESH database.** `task e2e`, `task wd1`, the later-host test and
the browser gates each bring up and tear down their own stack, so this is automatic; it matters if you run
`task test-integration` separately.

⚠ **Windows/git-bash:** `MSYS_NO_PATHCONV=1` on docker commands only (never exported); `tar --force-local`; Docker
Desktop must be running.

---

## Step 1 — the automated gates

Build first, **from the clean release-candidate HEAD, with nothing committed afterwards**. `dist/offline/` must
hold exactly the four v0.17.0 `.tar.gz` (core + `ubuntu2404` + `ubuntu2604` + `rocky9` companions), each with its
`.sbom.json`, and **no `.sig`** yet. Move any older core bundle out (do not delete); never touch
`dist/v0.16.0-release/` (it is SEC-63's read-only control) or `dist/sec63-old-recipe/`.

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd):/work" -v gomodcache:/go/pkg/mod -w /work -e BUNDLE_VERSION=0.17.0 aireos-repro-builder:golang1.26-node bash -c 'command -v wget >/dev/null || (apt-get update -qq && apt-get install -y -qq wget >/dev/null 2>&1); bash infra/offline/build-bundle.sh'
```
```bash
MSYS_NO_PATHCONV=1 bash infra/offline/postgres/build-postgres-companions.sh
```

Verify what you built before trusting it, for the core and every companion binary: `vcs.revision` equals
`git rev-parse HEAD` and `vcs.modified=false`.

```bash
grep -a -o 'vcs.revision=[0-9a-f]*' dist/offline/aireos-offline-0.17.0/bin/api | head -1
git rev-parse HEAD
```

⚠ **Never read a gate's result from a wrapper's exit code.** Run each gate unpiped with its real exit captured
(`> log 2>&1; echo "EXIT=$?"`) and assert the **positive marker**, not the absence of failure.

| Gate | Command | Positive marker | Result |
|---|---|---|---|
| **Compose WD-1..15** | `MSYS_NO_PATHCONV=1 task e2e` | `ok aireos.local/tests/e2e`, N PASS / 0 FAIL, `INTACT+ATTESTED` | pending |
| **Browser smoke** | `MSYS_NO_PATHCONV=1 task smoke` | Playwright `N passed`, 0 failed | pending |
| **Installed stack** | `MSYS_NO_PATHCONV=1 task wd1` | `ok aireos.local/tests/e2e`, `INTACT+ATTESTED`, SEC-43 forged chain → BROKEN on both verifiers | pending |
| **Later-host test** (INS-1, N-C1..3, SEC-63/66/67; legs A–D) | `bash infra/offline/postgres/later-host-test.sh ubuntu2404` · `… ubuntu2604` · `… rocky9` (per OS) | `LATER-HOST-TEST PASS` per OS; `SEC-63 PROVED`; `SEC-66 PROVED`; SEC-67 | pending |
| **Docs rehearsal** (DOC-41) | `task docs-rehearsal` | `DOCS-REHEARSAL PASS`, 3 server lines, `ALL VERIFIED` | pending |
| **Companion install** | `task pgsql-install-test` | its PASS summary (INS-2/3/4/8) | pending |
| **SBOM coverage self-test** | `bash infra/offline/sbom/check_coverage_test.sh` | its PASS summary (incl. the v0.16.0 package-closure control refused) | pending |
| **SBOM coverage over all four** | `node infra/offline/sbom/check-coverage.mjs` over each artifact + SBOM | coverage OK on core + 3 companions | pending |
| **Quickstart shell gate** | `bash infra/offline/wd1/quickstart-shell-gate.sh` | exit 0 (generate / bind / `--undo`) | pending |
| **Quickstart browser gate** | `bash infra/offline/wd1/quickstart-browser-gate.sh` | exit 0; both specs; completion marker | pending |
| **Setup browser gate** | `bash infra/offline/wd1/setup-browser-gate.sh` | exit 0; setup page → code → dashboard → page gone | pending |
| **block-criterion-check** | `task block-criterion-check` | exit 0, **fully green** (SEC-63…67 all resolve to real tests) | pending |
| **history-secret-scan** | `task history-secret-scan` | exit 0 | pending |
| **doc-check** | `task doc-check` | exit 0 | pending |
| **staleness-check** | `task staleness-check` | `staleness-check OK … release 0.17.0` | pending |
| **adr-ledger-check** | `task adr-ledger-check` | exit 0 | pending |
| **go-deps-check** | `task go-deps-check` | exit 0 | pending |
| **forbidden-features-check** | `cd tools/forbidden-features-check && go run .` (as `task lint` runs it) | exit 0 | pending |
| **provenance-check, UNSIGNED (sanity)** | `task provenance-check` | **FAILS** with "NO signature" and for no other reason — a check of the gate, not a pass | pending |

The results column is filled in **in the closure commit**, not before. Any red gate stops the cut: a fix is a
commit, a commit is a rebuild, and a rebuild repeats this step.

---

## Step 2 — sign, then both provenance gates (CEO, out of band)

No release key is created or read by anyone but the CEO. **No new keypair is minted this release;** the existing
production key signs these artifacts.

```bash
task sign-release -- --key /path/to/aireos-release.key
```

`dist/offline/` must hold **exactly the four v0.17.0 artifacts** (T-162's mixed-version guard refuses anything
else). Then, against the real public key:

```bash
RELEASE_PUBLIC_KEY=/path/to/aireos-release.pub task provenance-check
```
```bash
RELEASE_PUBLIC_KEY=/path/to/aireos-release.pub task sec45-check
```

`provenance-check` must say **VERIFIED**, not SELF-SKIPPED, with all four artifacts and their SBOMs; `sec45-check`
must say **PROVED** (a run without the real key uses the fixture key and its control correctly fails). Confirm no
stale `.sig` survived a rebuild (`ls -la dist/offline/*.tar.gz.sig`: mtime not older than its artifact).

---

## Step 3 — HUM-1, by a tester who is NOT the CEO — on the SIGNED artifacts

PUB-6 as the stranger test: **does someone who did not write the documents succeed?** The tester uses **only the
public `README.md`** and the signed artifacts, on **two OSes — Ubuntu 26.04 plus one other (Ubuntu 24.04 or Rocky
9)** — each a freshly installed host, patched to the day of the test, with no leftover `dist/`, keys or
`/etc/aireos/`.

- **Count the command lines typed on the server** between "files copied" and "the browser shows the setup page".
  **At most three:** one verification line, one `tar`, one `setup.sh`. `cd` counts; a line the guide presents as
  two steps counts as two.
- **Any step needing knowledge absent from the docs is a defect, not a note** — record it plainly rather than
  filling the gap from memory.
- Specifically settle the open points: the deployment-profile prompt; the `AIREOS_SITE_ADDRESS` on the setup
  line; **the certificate-viewer steps (unverified until now)**; and that the demo-target resource id has no
  screen.
- The run ends at a completed, audited governed action in the browser.

Record everything in a new file (created by the tester's run, not yet present):

```
docs/releases/v0.17.0/hum1-run.md
```

It holds: tester, hosts, OS and patch date, the exact
commands typed (with the count per host), every friction point and defect. A defect means a fix pass, a rebuild
and a re-sign, and HUM-1 re-runs on fresh hosts.

---

## Step 4 — tag, closure commit, push — only on CEO direction

**Tag the commit the artifacts attest — derived, never typed**, refusing on any mismatch:

```bash
REV=$(grep -a -o 'vcs.revision=[0-9a-f]*' dist/offline/aireos-offline-0.17.0/bin/api | head -1 | cut -d= -f2)
[ "$REV" = "$(git rev-parse HEAD)" ] && [ -z "$(git status --porcelain)" ] || { echo "STOP: artifacts do not attest a clean HEAD - rebuild and re-sign"; exit 1; }
git tag -a v0.17.0 "$REV" -m "v0.17.0 Install & First-Run Experience"
```

If it prints STOP, a commit landed after the build: rebuild all four artifacts, re-sign, and run it again. **The
tag must not be moved once set** (ADR-0070 D1).

Then the **tag + 1 closure commit** (the v0.14.0/v0.15.0/v0.16.0 pattern): the tag sha and the four signed SHA-256
values in `release_notes_v0_17_0.md` (replacing every `TBD`), the filled results column in Step 1, the HUM-1 record,
the "Latest release" lines in `CLAUDE.md` and `CANONICAL_SOURCE_OF_TRUTH.md` (drop "release candidate"), and the
ladder row for T-178.

**Push, and the GitHub Release (carrying all four archives, all four `.sig`, all SBOMs, `aireos-release.pub`, and
the fingerprint in its notes), only on the CEO's explicit direction.** Only `main` and release tags are pushed;
check `git log --format=%ae` first — noreply address only.
