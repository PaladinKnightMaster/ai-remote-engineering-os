# Reproducibility report — v0.13.0 offline bundle (OI-35, VER-6, ADR-0070 D6/D7)

Per ADR-0070 D7: **non-reproducibility is a finding, not a blocker.** This report publishes the
measured result either way. It was produced by `infra/offline/measure-reproducibility.sh`
(`task measure-reproducibility`), run once, T-130b.

## Result: REPRODUCIBLE — measured twice, warm cache and cold

The core offline bundle reproduces byte-for-byte. Two separate measurements were run, because the
first one's Go-binary result rested on a shared warm build cache and was therefore weaker than it
looked:

| Measurement | Commit | Go build cache | Result |
|---|---|---|---|
| Run 1 (warm) | `b13dbe8` | **shared** between both builds | byte-identical tarball `6dc9ba70…`, 26/26 files |
| Run 2 (cold) | `deebb5b` | **wiped between the two builds** | byte-identical tarball `69e470a0…`, 26/26 files |

**Run 2 is the load-bearing one.** It answers the question run 1 could not: *do two independent
cold compilations of this source produce identical bytes?* They do.

```
tarball A sha256: 6dc9ba702860f1bd4ade3a950625c8429775effefa21a23f3fa82627c20c4f4f
tarball B sha256: 6dc9ba702860f1bd4ade3a950625c8429775effefa21a23f3fa82627c20c4f4f
```

All 26 files in the unpacked stage matched by SHA-256 across both builds, with zero files
present in only one side:

```
files present in both stages: 26
  identical (same SHA-256): 26
  differing (same path, different SHA-256): 0
files present in A only: none
files present in B only: none
```

Neither hash matches the artifact currently staged in
`dist/offline/aireos-offline-0.13.0.tar.gz` (`e8ea7c9ee605…`), and the two runs' hashes do not
match each other. **Both facts are expected, not discrepancies.** The staged artifact was built
from an earlier, **dirty** commit (`191b001…`, `vcs.modified=true` — Finding A); the two runs were
built from two different clean commits, and `vcs.revision` is stamped into every binary. This
report measures reproducibility of the build process **against itself at a fixed commit** — it is
not a claim that any two builds at different commits should agree, and they demonstrably do not.

## Commits measured

```
run 1 (warm cache):  b13dbe8c9b77801b2941b05aa4a2d21d99cbaa1d
run 2 (cold cache):  deebb5b340c7e8e5aea9f6d81b923ba0450fbacb
```

Run 1's commit is one past the T-130b tooling commit (`27c5bec`): a dry run of the new script
surfaced a build-environment bug (Node version too old — see "Build environment" below), fixed in a
follow-up commit, and *that* clean commit is what was measured. Run 2 was measured at the later
commit that carries run 1's own report. **Every commit in this range is docs/tooling only — no
product code** — which is what makes the cross-run hash difference the clean demonstration
described under "Run 2" below.

Both runs required and verified a clean working tree before building.

## Build environment

Both builds ran inside the **same locally-tagged Docker image**, built once and reused:

| Component | Version |
|---|---|
| Base image | `golang:1.23` (Debian GNU/Linux 12, bookworm) |
| Go | `go1.23.12 linux/amd64` |
| Node.js | `v22.23.2` (NodeSource `setup_22.x`, not Debian's stock 18.20.4 — this repo's `package.json` pins `"engines.node": ">=22"` and `pnpm` refuses to run under an older Node) |
| pnpm | `10.32.1` (installed at the exact version `package.json`'s `"packageManager"` field pins) |
| `BUNDLE_VERSION` | `0.13.0` |

An honest reproducibility claim is relative to this stated environment. A different base-image
digest, a different day's NodeSource/apt snapshot, a different host OS/CPU, or a different Docker
version could all behave differently — see "Scope limit" below.

The SPA build (`pnpm --filter @aireos/web build`) ran **inside this same container**, not on the
host: this Windows dev host's own `node_modules` carries `linux-x64-gnu` optional dependencies
(from an earlier ticket's container-based `pnpm install`), not `win32-x64` ones, so `vite build`
cannot run on the host at all. Running both the SPA build and `build-bundle.sh` in one container
invocation also means the environment is trivially identical between the two builds, not merely
asserted to be.

**Each build re-ran the SPA build from scratch** (`apps/web/dist` removed and rebuilt before each
of the two `build-bundle.sh` invocations) — the SPA is the layer ADR-0070 D6 names as the prime
suspect for non-determinism (Go with `-trimpath` largely reproduces already); measuring twice
against one pre-built `apps/web/dist` would have measured nothing about it.

## Per-layer attribution

Every layer identified in the measurement scope, differing-files / total-files-present-in-both,
of the 26 files in each stage:

| Layer | Differ / Total |
|---|---|
| `bin/aireos-verify` | 0 / 1 |
| `bin/api` | 0 / 1 |
| `bin/audit-service` | 0 / 1 |
| `bin/authctl` | 0 / 1 |
| `bin/caddy` | 0 / 1 |
| `bin/connector-service` | 0 / 1 |
| `bin/mcp-gateway` | 0 / 1 |
| `bin/policy-service` | 0 / 1 |
| `bin/session-broker` | 0 / 1 |
| `web/` | 0 / 4 |
| `config/` | 0 / 8 |
| `install.sh` | 0 / 1 |
| `setup-loopback-demo.sh` | 0 / 1 |
| `VERSION` | 0 / 1 |
| `SHA256_MANIFEST.txt` | 0 / 1 |
| `SBOM.cdx.json` | 0 / 1 |

Every layer reproduced exactly, including the SPA (`web/`, 4 files: `index.html` + the CSS/JS
bundle + its content-hashed filename) and the SBOM (which embeds a generation timestamp field in
CycloneDX metadata but, on this run, did not vary — see caveat below).

## Finding A — the Go binaries are VCS-stamped, and it matters for this measurement

Every shipped Go binary embeds `vcs.revision` / `vcs.time` / `vcs.modified` (Go's `buildvcs`
stamping, on by default, **not** stripped by `-trimpath -ldflags "-s -w"`). Demonstrated directly,
not assumed:

- The currently-shipped `dist/offline/aireos-offline-0.13.0/bin/api` (built from a **dirty** tree
  at an earlier commit):
  ```
  vcs.revision=191b001c10bb1d05819862623cd542f18ecbdf79
  vcs.time=2026-08-17T11:44:11Z
  vcs.modified=true
  ```
- A binary built from **this measurement's clean commit** (`b13dbe8`):
  ```
  vcs.revision=b13dbe8c9b77801b2941b05aa4a2d21d99cbaa1d
  vcs.time=2026-08-17T18:16:50Z
  vcs.modified=false
  ```

`vcs.modified` flips with working-tree cleanliness at build time, which is exactly why
`measure-reproducibility.sh` **refuses to run on a dirty tree**: a dirty-tree build is
irreproducible against its own commit by construction, and would report a false
"non-reproducible" finding for a reason that has nothing to do with the build's actual
determinism. On a clean tree, `vcs.revision`/`vcs.time` are properties of the **commit**, not the
build's wall-clock time — so the stamp does not, by itself, make two builds differ.

## Run 2 — the cold-cache measurement (why it was needed, and what it showed)

Run 1's two builds mounted the **same** Go module and build-cache volumes, and that cache was
warm — 842 MB of modules and **737 MB of build cache**, measured after the run. Go's build cache is
**content-addressed**: on identical inputs it returns the stored output rather than recompiling. So
run 1's build B very largely replayed build A's objects, and `bin/* 0/1 differ` was close to a
**tautology** for the Go layer rather than evidence that two independent compilations agree. Run 1
disclosed the shared cache but drew a conclusion from the Go layer that the cache alone explained.

Run 2 removes that ambiguity. The **build** cache was wiped between the two builds; the **module**
cache was deliberately left shared, because module zips are immutable, `go.sum`-verified *inputs*
rather than compilation outputs — sharing them cannot mask compilation determinism, while
re-downloading 842 MB twice would have measured the network instead of the compiler.

Both builds were verified to start genuinely empty rather than assumed to:

```
BUILD A (fresh build cache: repro_coldbuild_a)
/root/.cache/go-build
4.0K	/root/.cache/go-build          <- empty at start
BUILD A exit=0 elapsed=129s

BUILD B (fresh build cache: repro_coldbuild_b)
/root/.cache/go-build
4.0K	/root/.cache/go-build          <- empty at start
BUILD B exit=0 elapsed=124s

tarball A sha256: 69e470a0749da1a8fa7aaefd1491146eb041bdd671a5e604f598a325187db374
tarball B sha256: 69e470a0749da1a8fa7aaefd1491146eb041bdd671a5e604f598a325187db374
RESULT: COLD-REPRODUCIBLE
identical: 26   differing: 0
```

`go env GOCACHE` was checked against the mount path *before* the run: had `GOCACHE` resolved
anywhere other than `/root/.cache/go-build`, the fresh volume would have been a no-op and the
"cold" build would silently have been warm — producing a confident result that proved nothing
beyond what run 1 already showed.

**Incidental finding: cold and warm builds took the same time** (~124–129 s cold vs ~117 s per
build warm). The Go build cache was contributing very little here; the module cache does the heavy
lifting. That is why run 1's result held up under the stricter test.

### The two runs together prove the binaries are commit-bound

Run 2's tarball (`69e470a0…`) differs from run 1's (`6dc9ba70…`), and **the entire source delta
between the two commits is one documentation file** — `git diff --stat b13dbe8 deebb5b` is
`docs/releases/v0.13.0/reproducibility-report.md | 216 +`, with zero non-`docs/` changes. No
product code, no build script, no dependency moved.

So a **documentation-only commit changed every shipped binary's bytes**, via the `vcs.revision`
stamp and nothing else. That is Finding A demonstrated rather than asserted, and it has a direct
operational consequence:

> **The bundle's hash changes on every commit, including docs-only commits.** A release artifact
> must therefore be built at the exact commit being released, and *any* subsequent commit — even a
> typo fix in a release note — invalidates it. Rebuild and re-sign, or cut the tag first.

This is also a real, checkable, self-reported provenance fact independent of this report — see the
correction in `docs/devops/offline-install-v3.md` (this ticket): the *signature* does not tie an
artifact to a source revision (ADR-0070 D3, unedited), but the binaries themselves do, and only
honestly so from a clean-tree build.

## Finding E — locale collation: asserted by construction, not measured here

This ticket also added an `LC_ALL=C` rider to `build-bundle.sh`'s `SHA256_MANIFEST.txt` generation
(`find … | sort -z`) and its `tar --sort=name` packaging step, because GNU `sort`/`tar` are
locale-collated by default and an `en_US.UTF-8` build host could order archive entries differently
from a `C`/`C.UTF-8` host, given byte-identical file content.

**This measurement is structurally blind to whether that rider does anything**, because both the
host and the `golang:1.23` container used here default to the `C`/`POSIX` locale already — the
condition the rider guards against never occurred in either build. The rider's correctness is
therefore a property asserted by reading the code (a `sort -z`/`tar --sort=name` invocation under
`LC_ALL=C` is provably locale-independent), not one this same-host run measured or could measure.
Proving it empirically would require running the build a second time on a genuinely different-
locale host, which is out of scope for this ticket.

## The postgres companion — deliberately NOT measured, stated analytically

`aireos-postgres-<ostag>.tar.gz` is built by `infra/offline/build-postgres-companions.sh` against
the **floating** `ubuntu:24.04` image tag, pulling its package set from a **live apt mirror** at
build time. It is therefore time-dependent by construction: two builds run back-to-back could
already diverge if the mirror's package index changed in between, and a build run a day (or an
hour) later than another would very plausibly diverge as security updates land continuously. A
back-to-back reproducibility measurement of this artifact would not actually test the property
"does this build reproduce" — it would test "did the mirror change in the last N minutes," and
report a false reproducibility guarantee this artifact does not structurally have. It is
intentionally excluded from this report's build-twice-and-diff method; ADR-0070 D6/D7 scope the
reproducibility *measurement* to the core bundle, and this asymmetry between the two shipped
artifacts is itself worth recording rather than leaving implicit.

## Scope limit

This report measures **same-host, same-container reproducibility**: builds run back to back on one
physical Windows host, from one locally-tagged Docker image. Run 2 extends that to a **cold build
cache**, so the result is no longer contingent on cached compilation output; the module cache
remains shared in both runs, which is sound because module zips are `go.sum`-verified inputs.

It is still **not a cross-host claim.** A genuinely
different host — different OS, different CPU microarchitecture, a different day's
`golang:1.23`/NodeSource snapshot, a different network path for the Caddy download or the SBOM
tool fetches — could produce a different result, and this report does not test that. Establishing
cross-host reproducibility would need a second, independently-provisioned build host; no ticket
currently scopes that work.

## How to reproduce this measurement

```sh
task measure-reproducibility
# or directly:
bash infra/offline/measure-reproducibility.sh
```

Refuses loudly on a dirty working tree. Builds/reuses a local Docker image
(`aireos-repro-builder:golang1.23-node`), never touches `dist/offline/` (asserted on exit), and
cleans up its own scratch directories on exit (`trap`).

That reproduces **run 1** (shared build cache). **Run 2**, the cold-cache measurement, was a
one-off Commander probe and is not committed as tooling — deliberately, since re-running it is a
judgement call about when the compilation-determinism question has been re-opened (a Go toolchain
bump, a change to the build flags in `build-bundle.sh`), not something to run on every release. To
repeat it, run the same two builds with a **fresh** Docker volume mounted at `/root/.cache/go-build`
for each build, keeping the module cache shared, and verify each build starts with that directory
empty before trusting the result.
