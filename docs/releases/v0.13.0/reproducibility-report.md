# Reproducibility report — v0.13.0 offline bundle (OI-35, VER-6, ADR-0070 D6/D7)

Per ADR-0070 D7: **non-reproducibility is a finding, not a blocker.** This report publishes the
measured result either way. It was produced by `infra/offline/measure-reproducibility.sh`
(`task measure-reproducibility`), run once, T-130b.

## Result: REPRODUCIBLE

Two independent builds of the core offline bundle, from the same clean commit, each rebuilding
the SPA from scratch, produced a **byte-identical tarball**:

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

Note this hash does **not** match the currently-shipped
`dist/offline/aireos-offline-0.13.0.tar.gz` (`e8ea7c9ee6051f2869ca3eb37330c086731b2d03f1b3cd7b7e71a9910f90916d`).
That is expected, not a discrepancy: the shipped artifact was built from an earlier, **dirty**
commit (`191b001c10bb1d05819862623cd542f18ecbdf79`, `vcs.modified=true` — see Finding A), not from
this measurement's clean commit. This report measures reproducibility of the build process against
itself, not equality with whatever happens to be staged in `dist/offline/`.

## Commit measured

```
b13dbe8c9b77801b2941b05aa4a2d21d99cbaa1d
```

One commit past the T-130b tooling commit (`27c5bec`): a dry run of the new script surfaced a
build-environment bug (Node version too old — see "Build environment" below), fixed in a
follow-up commit, and *that* clean commit is what was actually measured. Both commits carry only
tooling/doc changes, not product code.

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
build's wall-clock time, which is part of why the Go binary layer above reproduced byte-for-byte
across two separate `go build` invocations, in two separate container instances, run minutes
apart.

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

This report measures **same-host, same-container reproducibility only**: two builds run back to
back on one physical Windows host, from one locally-tagged Docker image, against the same
populated Go module/build-cache Docker volumes. It is **not** a cross-host claim. A genuinely
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
