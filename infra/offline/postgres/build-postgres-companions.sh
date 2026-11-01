#!/usr/bin/env bash
# build-postgres-companions.sh — package the per-OS postgres companion artifacts
# (ADR-0077 D1-D4, D9; supersedes the package-closure build of ADR-0052). The companion is the
# SOURCE-BUILT PostgreSQL tree (build-pgsql-trees.sh) + its installer; it never touches the
# host package manager. Runs on a CONNECTED build host with Docker and node.
# Output: dist/offline/aireos-postgres-<ostag>.tar.gz (+ .sbom.json), git-ignored.
#
#   aireos-postgres-<ostag>/
#     pgsql/                  the tree (bin/ lib/ share/, incl. share/aireos/COMPONENTS.json)
#     install-postgres.sh     the operator-facing entry point (run as root)
#     aireos-postgres.service the systemd unit install-postgres.sh installs
#     OSTAG  SBOM.cdx.json  SHA256_MANIFEST.txt
#
# Usage: bash infra/offline/postgres/build-postgres-companions.sh [os...]
#   default OS set: ubuntu2404 ubuntu2604 rocky9      (ADR-0077 D9; pins: pgsql-build.lock)
#   TREES=auto   (default) reuse a staging tree whose COMPONENTS.json matches pgsql-build.lock
#                (version, source checksum, source URL, pinned build image); otherwise build it.
#                ⚠ the check reads the LOCK, not the build scripts: after editing
#                pgsql-tree-build.sh, force a rebuild with TREES=build.
#   TREES=build  always rebuild the trees first (build-pgsql-trees.sh build)
#   PGSQL_TREE=<dir>  package this tree instead of dist/offline/aireos-pgsql-<os> (one OS only; used
#                by the SBOM gate's self-test to package the OpenSSL-bundled control tree)
#   OUT_DIR=<dir>  output (default dist/offline)
#
# Symlinks: the tree carries .so symlinks (libpq.so -> libpq.so.5.16). Every step that copies,
# lists or archives the tree runs in a Linux container — a Windows host would flatten or drop them.
set -euo pipefail
export MSYS_NO_PATHCONV=1   # Windows/git-bash: don't mangle the -v mount paths

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$HERE/../../.." && pwd)"
LOCK="$HERE/pgsql-build.lock"
OUT_DIR="${OUT_DIR:-$REPO_ROOT/dist/offline}"
TREES_MODE="${TREES:-auto}"
mkdir -p "$OUT_DIR"
log() { echo "[companions] $*"; }
die() { echo "[companions] ERROR: $*" >&2; exit 1; }

lock_get() { local v; v="$(sed -n "s/^$1=//p" "$LOCK" | head -1)"; [ -n "$v" ] || die "$1 missing from $LOCK"; printf '%s' "$v"; }
# Packaging runs in ONE pinned image for every OS (GNU tar + gzip + coreutils, digest-pinned in the
# lock): the archive layer then depends on a pinned tar version, not on the OS being packaged
# (a pristine Rocky 9.0 image has no findutils).
PACK_IMAGE="$(lock_get LATEST_ubuntu2404)"
[[ "$PACK_IMAGE" =~ @sha256:[0-9a-f]{64}$ ]] || die "LATEST_ubuntu2404 is not digest-pinned: $PACK_IMAGE"

OSES=("$@"); [ "${#OSES[@]}" -gt 0 ] || OSES=(ubuntu2404 ubuntu2604 rocky9)
for os in "${OSES[@]}"; do
  case "$os" in ubuntu2404|ubuntu2604|rocky9) ;; *) die "unknown OS tag '$os' (want ubuntu2404|ubuntu2604|rocky9)" ;; esac
done
[ -z "${PGSQL_TREE:-}" ] || [ "${#OSES[@]}" -eq 1 ] || die "PGSQL_TREE needs exactly one OS tag"

# Does the staging tree match the lock? (content, not mtimes: a checkout rewrites mtimes.)
tree_matches_lock() {   # $1=os $2=tree dir
  [ -f "$2/share/aireos/COMPONENTS.json" ] || return 1
  # node is a native Windows program: it needs git-bash's /d/... -> D:/... path translation, which
  # the MSYS_NO_PATHCONV above (for docker -v) switches off — so undo it for node only.
  ( unset MSYS_NO_PATHCONV; node -e '
    const [f, ver, sha, url, img, os] = process.argv.slice(1);
    const c = JSON.parse(require("fs").readFileSync(f, "utf8")), p = c.postgres || {};
    process.exit(p.version === ver && p.sha256 === sha && p.source_url === url && c.base_image === img && c.ostag === os ? 0 : 1);
  ' "$2/share/aireos/COMPONENTS.json" "$(lock_get PGSQL_VERSION)" "$(lock_get PGSQL_SHA256)" "$(lock_get PGSQL_URL)" "$(lock_get "IMAGE_$1")" "$1" )
}

# Run a command in the packaging image. $1=shell script; mounts: out dir rw, scripts ro, tree ro (at /t).
pack_run() {   # $1=tree dir; remaining: the script text
  local tree="$1"; shift
  docker run --rm --platform linux/amd64 --network none -e LC_ALL=C \
    -v "$OUT_DIR":/o -v "$HERE":/src:ro -v "$tree":/t:ro "$PACK_IMAGE" bash -c "$1"
}

for os in "${OSES[@]}"; do
  name="aireos-postgres-$os"
  tree="${PGSQL_TREE:-$OUT_DIR/aireos-pgsql-$os}"
  stage="$OUT_DIR/$name"

  # --- 1. the tree: reuse (fresh) or build -------------------------------------------------------
  if [ -n "${PGSQL_TREE:-}" ]; then
    [ -f "$tree/share/aireos/COMPONENTS.json" ] || die "PGSQL_TREE '$tree' has no share/aireos/COMPONENTS.json"
    log "$os: packaging the tree given by PGSQL_TREE ($tree)"
  elif [ "$TREES_MODE" = auto ] && tree_matches_lock "$os" "$tree"; then
    log "$os: reusing the staging tree $tree (COMPONENTS.json matches pgsql-build.lock; TREES=build forces a rebuild)"
  else
    [ "$TREES_MODE" = auto ] && log "$os: no up-to-date staging tree — building it" || log "$os: TREES=$TREES_MODE — building the tree"
    OUT_DIR="$OUT_DIR" bash "$HERE/build-pgsql-trees.sh" build "$os"
    tree_matches_lock "$os" "$tree" || die "$os: the tree just built does not match pgsql-build.lock"
  fi

  # --- 2. clear the previous artifact, AND every stale sibling, then assemble the stage ----------
  # sign-release.sh is a separate, out-of-band step never run by this script. Without this, a rebuild
  # that changes the artifact's bytes leaves an old-but-still-present .sig (or .sbom.json) beside the
  # new tarball — the R4 finding (v0.14.0 security review): a listing showed a signature dated days
  # before the artifact it sat beside, looking entirely valid. check-provenance.sh WOULD catch the
  # mismatch, but only if someone runs it first; a missing .sig is honest, a wrong one is dangerous.
  # Removal and assembly run in the container (the stage holds symlinks; no rm on a host variable path).
  log "$os: assemble $name from $(basename "$tree")"
  pack_run "$tree" "
    set -e
    cd /o
    rm -rf $name $name.tar.gz $name.tar.gz.sig $name.tar.gz.sbom.json
    mkdir $name
    cp -a /t $name/pgsql
    cp /src/install-postgres.sh /src/aireos-postgres.service $name/
    chmod 755 $name/install-postgres.sh; chmod 644 $name/aireos-postgres.service
    echo $os > $name/OSTAG"

  # --- 3. SBOM (ADR-0070 D5): generated from COMPONENTS.json for every OS, deb and rpm -----------
  log "$os: generate SBOM"
  bash "$REPO_ROOT/infra/offline/sbom/generate-sbom.sh" --kind companion --stage "$stage" \
    --ostag "$os" --out "$OUT_DIR/$name.tar.gz.sbom.json"

  # --- 4. SHA256_MANIFEST.txt (ADR-0070 D1/D9) + self-check, then deterministic packaging (D6) ----
  # This artifact runs install-postgres.sh AS ROOT on the target (ADR-0070 Context gap 1). Same flags
  # and rationale as build-bundle.sh: --sort=name/--mtime/--owner/--group remove build-order and
  # build-host non-determinism from the tar layer, and a standalone `gzip -n` removes gzip's own
  # embedded mtime. The manifest lists regular files; the symlinks are listed in SYMLINKS.txt
  # (hashed by the manifest), so a retargeted link changes a hashed file.
  log "$os: SHA256_MANIFEST.txt + deterministic tar"
  pack_run "$tree" "
    set -e
    cd /o/$name
    find . -type l -printf '%p -> %l\n' | sort > SYMLINKS.txt
    find . -type f ! -name SHA256_MANIFEST.txt -print0 | sort -z | xargs -0 sha256sum > SHA256_MANIFEST.txt
    sha256sum -c SHA256_MANIFEST.txt >/dev/null || { echo 'manifest self-check failed' >&2; exit 1; }
    cd /o
    tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner -cf - $name | gzip -n > $name.tar.gz"
  art="$OUT_DIR/$name.tar.gz"
  nlinks="$(tar --force-local -tvzf "$art" | grep -c '^l' || true)"
  [ "$nlinks" -gt 0 ] || die "$os: the archive holds no symlinks — the tree's libpq.so links were flattened or dropped"
  log "$os: -> $art ($(du -sh "$art" | cut -f1), sha256 $(sha256sum "$art" | cut -d' ' -f1); $nlinks symlink entries preserved)"

  # --- 5. the INS-7 coverage gate on what was just built (asserts against the archive itself) -----
  ( unset MSYS_NO_PATHCONV; node "$REPO_ROOT/infra/offline/sbom/check-coverage.mjs" "$art" "$art.sbom.json" ) \
    || die "$os: the freshly built companion fails its own SBOM coverage gate"
done
log "done (${OSES[*]})"
