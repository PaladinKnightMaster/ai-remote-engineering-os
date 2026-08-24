#!/usr/bin/env bash
# measure-reproducibility.sh — OI-35, VER-6 (G1), ADR-0070 D6/D7, T-130b.
#
# Build the offline bundle TWICE from the SAME commit and diff the result.
# Per D7 this is a MEASUREMENT, not a gate: non-reproducibility is a finding
# that gets published, not a failure that blocks a release. This script's own
# exit code reflects whether the MEASUREMENT ran correctly (both builds
# completed, dist/offline/ was never touched) — never whether the two builds
# happened to match. Do not repurpose this as a release-check gate.
#
# THE ONE WAY THIS GOES VACUOUS: `task offline-bundle` runs
# `pnpm --filter @aireos/web build` ONCE and build-bundle.sh just `cp -a`s
# apps/web/dist into the stage. Running build-bundle.sh twice against the
# SAME pre-built apps/web/dist measures nothing about the SPA layer — the
# layer most likely to be non-deterministic (ADR-0070 D6: Go with -trimpath
# largely reproduces already; pnpm/vite is the prime suspect). So this
# script REMOVES and REBUILDS apps/web/dist from scratch before each of the
# two build-bundle.sh runs, never reusing the prior build's output.
#
# Finding A (Commander audit, established, not re-derived here): the shipped
# Go binaries embed `vcs.revision`/`vcs.modified` (Go's buildvcs stamping,
# NOT stripped by -trimpath). vcs.modified flips with working-tree
# cleanliness, so a dirty-tree build is irreproducible against its own
# commit BY CONSTRUCTION. This script therefore REFUSES to run on a dirty
# tree rather than producing a measurement that means nothing.
#
# Finding C: dist/offline/ holds the real, SIGNED release artifacts (SEC-45
# was just proven against them). build-bundle.sh:107 does
# `rm -rf "$STAGE" "$TARBALL"` on its OUT_DIR first, so this script NEVER
# points OUT_DIR at dist/offline — it uses two scratch dirs under
# dist/.repro-<pid>/, and asserts dist/offline/*.tar.gz SHA-256 is byte-for-
# byte unchanged after both runs (same shape as sec45_bundle_mutation_test.sh
# for the same reason: disk is tight, do not accidentally destroy the real
# artifacts).
#
# Finding E: the manifest/packaging LC_ALL=C rider (build-bundle.sh, this
# same ticket) makes locale-collation determinism a property ASSERTED by
# construction. A same-host run is structurally blind to it (this host is
# already C.UTF-8) — this script does not and cannot measure it, and the
# report says so rather than implying same-host coverage proves it.
#
# Build environment: a local Docker image is built ONCE from `golang:1.26`
# (Debian bookworm) with `nodejs`/`npm` added via apt (needed for
# build-bundle.sh step 5c, the CycloneDX SBOM's node/npx fragment — see
# infra/offline/sbom/generate-sbom.sh). The SAME tagged image is used for
# BOTH builds, so the environment is held constant and the invocation is
# encoded here rather than depended on ad hoc. Exact Go/Node versions
# actually used are queried from that image and printed below, not assumed.
#
# Usage:
#   bash infra/offline/measure-reproducibility.sh
#   BUNDLE_VERSION=0.13.0 bash infra/offline/measure-reproducibility.sh
#
# Needs: git, Docker, the host's pnpm (with `pnpm install --frozen-lockfile`
# already run — apps/web/node_modules must exist), coreutils (sha256sum,
# comm, find).
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$REPO_ROOT"

log() { printf '\033[1;34m[repro]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[repro] ERROR:\033[0m %s\n' "$*" >&2; exit 1; }

pass=0
fail=0
assert_true() {
  local name="$1"; shift
  if "$@" >/dev/null 2>&1; then
    echo "PASS $name"; pass=$((pass + 1))
  else
    echo "FAIL $name"; fail=$((fail + 1))
  fi
}
assert_eq() {
  local name="$1" got="$2" want="$3"
  if [ "$got" = "$want" ]; then
    echo "PASS $name -- '$got'"; pass=$((pass + 1))
  else
    echo "FAIL $name -- got '$got', want '$want'"; fail=$((fail + 1))
  fi
}

# --- 1. refuse a dirty tree (finding A) -------------------------------------
DIRTY="$(git status --porcelain)"
[ -z "$DIRTY" ] || die "working tree is dirty -- vcs.modified would flip between the two builds, which makes the measurement meaningless against this commit (finding A). Commit or stash first. Dirty files:
$DIRTY"

COMMIT="$(git rev-parse HEAD)"
# BUNDLE_VERSION is REQUIRED and never silently defaulted (OI-37: no default
# may equal the value it is meant to detect staleness in — the same rule
# build-bundle.sh:60 already enforces for the same reason). This script used
# to default to "0.13.0", which is exactly the failure mode OI-37 generalizes
# from: the default was invisible only because it happened to match the real
# value, and would silently measure a bundle labelled 0.13.0 once the repo
# moved to v0.14.0. `task offline-bundle` (Taskfile.yml) is the single source
# of truth for the current release version; set BUNDLE_VERSION explicitly.
[ -n "${BUNDLE_VERSION:-}" ] || die "BUNDLE_VERSION is required and is never defaulted (OI-37) — set it explicitly, e.g. BUNDLE_VERSION=0.13.0 bash infra/offline/measure-reproducibility.sh (see Taskfile.yml's offline-bundle task for the current release version)"
log "measuring commit $COMMIT (clean tree confirmed) at BUNDLE_VERSION=$BUNDLE_VERSION"

# --- 2. build environment: one tagged image, reused for BOTH builds --------
# node/pnpm come from NodeSource, not plain apt: Debian bookworm's apt
# nodejs is 18.20.4, but this repo's package.json pins
# "engines.node": ">=22" (pnpm refuses to run otherwise -- observed directly
# while authoring this script), and pnpm itself is installed at the exact
# version this repo's package.json pins ("packageManager": "pnpm@10.32.1"),
# not left to whatever `corepack`/apt would pick.
IMAGE_TAG="aireos-repro-builder:golang1.26-node"
if ! docker image inspect "$IMAGE_TAG" >/dev/null 2>&1; then
  log "building measurement image $IMAGE_TAG (golang:1.26 + Node 22.x + pnpm 10.32.1) -- one-time"
  BUILD_CTX="$(mktemp -d)"
  cat > "$BUILD_CTX/Dockerfile" <<'EOF'
FROM golang:1.26
RUN apt-get update -qq \
 && curl -fsSL https://deb.nodesource.com/setup_22.x | bash - \
 && apt-get install -y -qq nodejs \
 && npm install -g pnpm@10.32.1 --no-fund --no-audit \
 && rm -rf /var/lib/apt/lists/*
EOF
  docker build -q -t "$IMAGE_TAG" "$BUILD_CTX" >/dev/null || die "failed to build $IMAGE_TAG"
  rm -rf "$BUILD_CTX"
else
  log "reusing existing measurement image $IMAGE_TAG"
fi
GO_VERSION_STR="$(MSYS_NO_PATHCONV=1 docker run --rm "$IMAGE_TAG" go version)"
NODE_VERSION_STR="$(MSYS_NO_PATHCONV=1 docker run --rm "$IMAGE_TAG" node -v)"
PNPM_VERSION_STR="$(MSYS_NO_PATHCONV=1 docker run --rm "$IMAGE_TAG" pnpm -v)"
log "build environment: image=$IMAGE_TAG go=\"$GO_VERSION_STR\" node=\"$NODE_VERSION_STR\" pnpm=\"$PNPM_VERSION_STR\""

# --- 3. baseline: dist/offline/*.tar.gz must be byte-unchanged at the end --
DIST_OFFLINE="$REPO_ROOT/dist/offline"
baseline_shas() {
  if [ -d "$DIST_OFFLINE" ]; then
    ( cd "$DIST_OFFLINE" && LC_ALL=C sha256sum *.tar.gz 2>/dev/null | LC_ALL=C sort )
  fi
}
BASELINE_SHAS="$(baseline_shas)"
log "dist/offline/ baseline recorded ($(printf '%s\n' "$BASELINE_SHAS" | grep -c . || true) tarball(s))"

# --- 4. scratch dirs (never dist/offline), cleaned up on exit --------------
WORKREL="dist/.repro-$$"
WORKROOT="$REPO_ROOT/$WORKREL"
OUT_A_REL="$WORKREL/build-a"
OUT_B_REL="$WORKREL/build-b"
mkdir -p "$REPO_ROOT/$OUT_A_REL" "$REPO_ROOT/$OUT_B_REL"
cleanup() {
  log "cleanup: removing scratch $WORKROOT"
  rm -rf "$WORKROOT"
}
trap cleanup EXIT

GOMOD_VOL="aireos_gomodcache"
GOCACHE_VOL="aireos_gocache"

# --- 5. build once: the FULL `task offline-bundle` sequence ----------------
# All three steps run inside the SAME container invocation (same image, same
# mounted repo, same go/node/pnpm versions) so "the SPA layer is actually
# rebuilt" and "the environment is held constant across both builds" are
# both true at once, rather than trusting the host's own toolchain (which,
# on this Windows host, cannot run vite build at all -- its node_modules
# carries LINUX optional deps, not win32 ones).
#
# `pnpm install --frozen-lockfile` FIRST -- it is `task offline-bundle`'s
# first command (Taskfile.yml), and omitting it meant this harness was not
# measuring the documented build at all. It silently depended on node_modules
# already holding the right platform binaries, which bit for real on
# 2026-08-18: T-132 verified in `node:22-alpine` (musl), leaving only
# @rollup/rollup-linux-x64-musl installed, and the next Debian/glibc build
# died with "Cannot find module @rollup/rollup-linux-x64-gnu". The harness and
# the thing it claims to measure had diverged invisibly, because each side was
# individually correct -- the recurring defect class this release keeps
# finding. Running the install here also makes dependency RESOLUTION part of
# what reproducibility is measured over, which is strictly stronger.
build_once() {
  local out_rel="$1" label="$2"
  log "=== build $label: removing apps/web/dist, then pnpm install + SPA + bundle in $IMAGE_TAG (OUT_DIR=$out_rel) ==="
  rm -rf "$REPO_ROOT/apps/web/dist"
  MSYS_NO_PATHCONV=1 docker run --rm \
    -v "$REPO_ROOT":/w -w /w \
    -v "$GOMOD_VOL":/go/pkg/mod \
    -v "$GOCACHE_VOL":/root/.cache/go-build \
    -e BUNDLE_VERSION="$BUNDLE_VERSION" \
    -e OUT_DIR="/w/$out_rel" \
    -e CI=true \
    "$IMAGE_TAG" bash -c "set -euo pipefail; pnpm install --frozen-lockfile; pnpm --filter @aireos/web build; bash infra/offline/build-bundle.sh" \
    || die "pnpm install + SPA build + build-bundle.sh failed for $label"
  [ -d "$REPO_ROOT/apps/web/dist" ] || die "apps/web/dist missing after SPA build for $label"
}

build_once "$OUT_A_REL" "A"
build_once "$OUT_B_REL" "B"

TARBALL_A="$REPO_ROOT/$OUT_A_REL/aireos-offline-$BUNDLE_VERSION.tar.gz"
TARBALL_B="$REPO_ROOT/$OUT_B_REL/aireos-offline-$BUNDLE_VERSION.tar.gz"
STAGE_A="$REPO_ROOT/$OUT_A_REL/aireos-offline-$BUNDLE_VERSION"
STAGE_B="$REPO_ROOT/$OUT_B_REL/aireos-offline-$BUNDLE_VERSION"

assert_true "build A produced a tarball" test -f "$TARBALL_A"
assert_true "build B produced a tarball" test -f "$TARBALL_B"
assert_true "build A staged a directory" test -d "$STAGE_A"
assert_true "build B staged a directory" test -d "$STAGE_B"

# --- 6. headline: tarball SHA-256 -------------------------------------------
SHA_A="$(sha256sum "$TARBALL_A" | cut -d' ' -f1)"
SHA_B="$(sha256sum "$TARBALL_B" | cut -d' ' -f1)"
echo
echo "=== HEADLINE ==="
echo "tarball A sha256: $SHA_A"
echo "tarball B sha256: $SHA_B"
if [ "$SHA_A" = "$SHA_B" ]; then
  echo "RESULT: REPRODUCIBLE -- both builds produced byte-identical tarballs."
else
  echo "RESULT: NOT REPRODUCIBLE -- the tarballs differ. Per-layer attribution follows."
fi

# --- 7. per-file, per-layer stage comparison (the deliverable) -------------
echo
echo "=== PER-LAYER ATTRIBUTION (stage A vs stage B) ==="
FILES_A="$WORKROOT/files_a.txt"
FILES_B="$WORKROOT/files_b.txt"
( cd "$STAGE_A" && LC_ALL=C find . -type f | sed 's|^\./||' | LC_ALL=C sort ) > "$FILES_A"
( cd "$STAGE_B" && LC_ALL=C find . -type f | sed 's|^\./||' | LC_ALL=C sort ) > "$FILES_B"

ONLY_A="$(comm -23 "$FILES_A" "$FILES_B")"
ONLY_B="$(comm -13 "$FILES_A" "$FILES_B")"
COMMON="$(comm -12 "$FILES_A" "$FILES_B")"

layer_of() {
  case "$1" in
    bin/*) echo "bin/$(basename "$1")" ;;
    web/*) echo "web/" ;;
    config/*) echo "config/" ;;
    SBOM.cdx.json) echo "SBOM.cdx.json" ;;
    SHA256_MANIFEST.txt) echo "SHA256_MANIFEST.txt" ;;
    VERSION) echo "VERSION" ;;
    install.sh) echo "install.sh" ;;
    setup-loopback-demo.sh) echo "setup-loopback-demo.sh" ;;
    *) echo "other:$1" ;;
  esac
}

declare -A LAYER_TOTAL LAYER_DIFF
DIFF_FILES=()
IDENTICAL_COUNT=0

if [ -n "$COMMON" ]; then
  while IFS= read -r f; do
    [ -z "$f" ] && continue
    ha="$(sha256sum "$STAGE_A/$f" | cut -d' ' -f1)"
    hb="$(sha256sum "$STAGE_B/$f" | cut -d' ' -f1)"
    layer="$(layer_of "$f")"
    LAYER_TOTAL["$layer"]=$(( ${LAYER_TOTAL["$layer"]:-0} + 1 ))
    if [ "$ha" != "$hb" ]; then
      DIFF_FILES+=("$f")
      LAYER_DIFF["$layer"]=$(( ${LAYER_DIFF["$layer"]:-0} + 1 ))
    else
      IDENTICAL_COUNT=$((IDENTICAL_COUNT + 1))
    fi
  done <<< "$COMMON"
fi

echo "files present in both stages: $(printf '%s\n' "$COMMON" | grep -c . || true)"
echo "  identical (same SHA-256): $IDENTICAL_COUNT"
echo "  differing (same path, different SHA-256): ${#DIFF_FILES[@]}"
if [ -n "$ONLY_A" ]; then
  echo "files present in A only:"
  printf '%s\n' "$ONLY_A" | sed 's/^/  - /'
else
  echo "files present in A only: none"
fi
if [ -n "$ONLY_B" ]; then
  echo "files present in B only:"
  printf '%s\n' "$ONLY_B" | sed 's/^/  - /'
else
  echo "files present in B only: none"
fi

echo
echo "-- layer summary (differing / total, files present in both stages) --"
for layer in "${!LAYER_TOTAL[@]}"; do
  printf '  %-28s %d / %d differ\n' "$layer" "${LAYER_DIFF[$layer]:-0}" "${LAYER_TOTAL[$layer]}"
done | LC_ALL=C sort

if [ "${#DIFF_FILES[@]}" -gt 0 ]; then
  echo
  echo "-- differing files, by path --"
  for f in "${DIFF_FILES[@]}"; do
    echo "  DIFFERS: $f  (layer: $(layer_of "$f"))"
  done
fi

# --- 8. dist/offline/ must be untouched -------------------------------------
FINAL_SHAS="$(baseline_shas)"
assert_eq "dist/offline/*.tar.gz SHA-256 unchanged (never touched by this measurement)" "$FINAL_SHAS" "$BASELINE_SHAS"

echo
echo "=== SUMMARY ==="
echo "commit measured:   $COMMIT"
echo "build environment: $IMAGE_TAG (go: $GO_VERSION_STR; node: $NODE_VERSION_STR; pnpm: $PNPM_VERSION_STR)"
echo "$pass passed, $fail failed (structural checks; the reproducibility headline above is a FINDING, not a gate -- ADR-0070 D7)"
[ "$fail" -eq 0 ]
