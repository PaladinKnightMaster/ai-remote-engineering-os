#!/usr/bin/env bash
# build-pgsql-trees.sh — host orchestrator for the source-built PostgreSQL tree
# (ADR-0077 D1-D3, D9; T-171). Needs Docker. Builds STAGING trees only:
#   dist/offline/aireos-pgsql-<ostag>/    (dist/ is git-ignored)
# It does NOT replace the shipped v0.16.0 companion (build-postgres-companions.sh) — T-172 does.
#
# Usage: bash infra/offline/postgres/build-pgsql-trees.sh build|smoke [ostag...]
#   default OS set: ubuntu2404 ubuntu2604 rocky9      (pins: pgsql-build.lock)
#   build  per OS: measure the FLOOR image's library set, source-build in the FLOOR image
#          (the oldest supported release: a tree must not need symbol versions a patched-less host lacks)
#   smoke  per OS: fresh pinned image, --network none, no package installs — on the FLOOR and on the
#          LATEST image — plus the negative controls (rule 12): a smoke never seen failing is not evidence
# Override (for the checksum negative control only): PGSQL_SHA256=<other> ... build ubuntu2404
set -euo pipefail
export MSYS_NO_PATHCONV=1   # Windows/git-bash: don't mangle the -v mount paths

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$HERE/../../.." && pwd)"
LOCK="$HERE/pgsql-build.lock"
OUT_DIR="${OUT_DIR:-$REPO_ROOT/dist/offline}"
WORK="${WORK_DIR:-$REPO_ROOT/dist/pgsql-work}"     # reference sets, logs, control trees
log() { echo "[pgsql-trees] $*"; }
die() { echo "[pgsql-trees] ERROR: $*" >&2; exit 1; }

# The lock is parsed, never sourced (standing rule); each value is shape-checked.
lock_opt() { sed -n "s/^$1=//p" "$LOCK" | head -1; }
lock_get() { local v; v="$(lock_opt "$1")"; [ -n "$v" ] || die "$1 missing from $LOCK"; printf '%s' "$v"; }
PGSQL_VERSION="$(lock_get PGSQL_VERSION)"; PGSQL_URL="$(lock_get PGSQL_URL)"
PGSQL_SHA256_LOCK="$(lock_get PGSQL_SHA256)"; PGSQL_SHA256="${PGSQL_SHA256:-$PGSQL_SHA256_LOCK}"
[[ "$PGSQL_VERSION" =~ ^16\.[0-9]+$ ]] || die "PGSQL_VERSION '$PGSQL_VERSION' is not a 16.x release"
[[ "$PGSQL_URL" =~ ^https://[^[:space:]]+\.tar\.bz2$ ]] || die "PGSQL_URL must be an https .tar.bz2"
[[ "$PGSQL_SHA256" =~ ^[0-9a-f]{64}$ ]] || die "PGSQL_SHA256 must be 64 lowercase hex"
[ "$PGSQL_SHA256" = "$PGSQL_SHA256_LOCK" ] || log "WARNING: PGSQL_SHA256 overridden from the environment (lock says $PGSQL_SHA256_LOCK)"
image_for() {   # $1=ostag $2=floor|latest
  local k="IMAGE_$1"; [ "${2:-floor}" = latest ] && k="LATEST_$1"
  local i; i="$(lock_get "$k")" || exit 1
  [[ "$i" =~ @sha256:[0-9a-f]{64}$ ]] || die "$k is not digest-pinned: $i"
  printf '%s' "$i"
}

export PGSQL_VERSION PGSQL_URL PGSQL_SHA256
CMD="${1:-build}"; shift || true
OSES=("$@"); [ "${#OSES[@]}" -gt 0 ] || OSES=(ubuntu2404 ubuntu2604 rocky9)
mkdir -p "$OUT_DIR" "$WORK"

# Run the in-container build script. $1=ostag $2=reference-file(in $WORK) $3=out dir; extra docker args follow.
# BUILD_BASE=latest builds on the newest image (only the symbol-skew control does that).
run_build() {
  local os="$1" ref="$2" out="$3" img vault; shift 3; img="$(image_for "$os" "${BUILD_BASE:-floor}")"
  vault=""; [ "${BUILD_BASE:-floor}" = floor ] && vault="$(lock_opt "VAULT_$os")"
  [ -z "$vault" ] || [[ "$vault" =~ ^https://[^[:space:]]+$ ]] || die "VAULT_$os must be an https URL"
  docker run --rm --platform linux/amd64 -v "$HERE":/src:ro -v "$WORK":/work:ro -v "$out":/out \
    -e OSTAG="$os" -e PGSQL_VERSION -e PGSQL_URL -e PGSQL_SHA256 -e BASE_IMAGE="$img" -e DNF_VAULT_BASEURL="$vault" \
    -e REFERENCE_LIBS="/work/$ref" "$@" "$img" bash /src/pgsql-tree-build.sh
}

# Run the smoke in a FRESH pinned container: no network, tree read-only, no installs.
smoke_run() {   # $1=ostag $2=tree dir $3=floor|latest  -> SMOKE_OUT, SMOKE_RC
  local img; img="$(image_for "$1" "${3:-floor}")"
  SMOKE_OUT="$(docker run --rm --platform linux/amd64 --network none -v "$HERE":/src:ro -v "$2":/stage:ro \
    -e OSTAG="$1" -e PGSQL_VERSION -e SMOKE_BASE="${3:-floor}" "$img" bash /src/pgsql-tree-smoke.sh 2>&1)" && SMOKE_RC=0 || SMOKE_RC=$?
}
# Copy a tree and optionally mutate it, inside the container (host cp -a would mangle symlinks on Windows).
copy_tree() {   # $1=ostag $2=src tree $3=name under $WORK $4=shell mutation run in the copy's root
  docker run --rm --platform linux/amd64 --network none -v "$2":/s:ro -v "$WORK":/w "$(image_for "$1")" \
    sh -c "rm -rf /w/$3 && cp -a /s /w/$3 && cd /w/$3 && $4"
}
reference_libs() {   # $1=ostag $2=floor|latest: sonames resolvable via ldconfig in a fresh container of that image (ADR-0077 D3)
  local f="$WORK/ref-$1.txt"; [ "${2:-floor}" = latest ] && f="$WORK/ref-$1-latest.txt"
  docker run --rm --platform linux/amd64 --network none "$(image_for "$1" "${2:-floor}")" ldconfig -p \
    | awk '/=>/ && /x86-64/ {print $1}' | sort -u >"$f"
  [ -s "$f" ] || die "empty reference set for $1"
  log "$1: ${2:-floor} reference set = $(wc -l <"$f") sonames (pinned $(image_for "$1" "${2:-floor}"))"
}

do_build() {
  local os="$1" t0=$SECONDS stage="$OUT_DIR/aireos-pgsql-$1" part="$OUT_DIR/aireos-pgsql-$1.partial"
  reference_libs "$os"
  rm -rf "$part"; mkdir -p "$part"
  log "$os: building PostgreSQL $PGSQL_VERSION from source on the FLOOR image (log: $WORK/build-$os.log)"
  # Build into .partial and swap on success: a failed run (e.g. the checksum control) never destroys a good tree.
  if ! run_build "$os" "ref-$os.txt" "$part" >"$WORK/build-$os.log" 2>&1; then
    tail -15 "$WORK/build-$os.log" >&2; rm -rf "$part"; die "$os: build FAILED"
  fi
  rm -rf "$stage"; mv "$part" "$stage"
  grep -E '\] (sha256 verified|BUNDLE|WARNING|rpath proof|tree:)' "$WORK/build-$os.log" || true
  log "$os: OK in $((SECONDS-t0))s — $(du -sh "$stage" | cut -f1); bundled: [$(tr '\n' ' ' <"$stage/share/aireos/BUNDLED_LIBS")]; host libs: $(wc -l <"$stage/share/aireos/HOST_LIBS")"
  log "$os: required host symbol versions: $(tr '\n' ';' <"$stage/share/aireos/HOST_LIB_VERSIONS")"
}

# expect_fail <what> <needle>...: the last smoke_run must have failed AND said every needle.
expect_fail() {
  local what="$1" n; shift
  [ "$SMOKE_RC" -ne 0 ] || { echo "$SMOKE_OUT" | tail -5; die "NEGATIVE CONTROL INVALID: '$what' still passed — the smoke cannot fail"; }
  for n in "$@"; do
    grep -qF -- "$n" <<<"$SMOKE_OUT" || { echo "$SMOKE_OUT" | tail -8; die "negative control '$what' failed but did not name '$n'"; }
    log "negative control OK ($what): smoke FAILED rc=$SMOKE_RC and said '$n' -> $(grep -F -- "$n" <<<"$SMOKE_OUT" | head -1 | cut -c1-230)"
  done
}

do_smoke() {
  local os="$1" stage="$OUT_DIR/aireos-pgsql-$1" base
  [ -f "$stage/share/aireos/COMPONENTS.json" ] || die "$os: no built tree at $stage (run 'build' first)"
  for base in floor latest; do   # the floor is what the tree was built for; latest proves it also runs on a patched host
    smoke_run "$os" "$stage" "$base"; echo "$SMOKE_OUT" | tail -3
    [ "$SMOKE_RC" = 0 ] && grep -q "^PGSQL-TREE-SMOKE PASS .*base=$base " <<<"$SMOKE_OUT" || die "$os: smoke on the $base image FAILED (rc=$SMOKE_RC)"
  done
  # Negative control 1: remove libpq from a copy -> must fail by naming it.
  copy_tree "$os" "$stage" "neg-libpq-$os" 'rm -f lib/libpq.so.5*'
  smoke_run "$os" "$WORK/neg-libpq-$os"; expect_fail "libpq removed" "libpq.so.5"
  # Control 2 (the bundling path): drop OpenSSL from the reference set, as if the base lacked it. The
  # tree must then BUNDLE libssl/libcrypto, still pass, and removing a bundled lib must make it fail.
  grep -vxF -e libssl.so.3 -e libcrypto.so.3 "$WORK/ref-$os.txt" >"$WORK/ref-$os-noossl.txt"
  rm -rf "$WORK/ctl-$os"; mkdir -p "$WORK/ctl-$os"
  run_build "$os" "ref-$os-noossl.txt" "$WORK/ctl-$os" -e PHASE=bundle -v "$stage":/in:ro >"$WORK/ctl-$os.log" 2>&1 \
    || { tail -15 "$WORK/ctl-$os.log" >&2; die "$os: bundle-path control build failed"; }
  grep -E '\] (BUNDLE|WARNING).*(libssl|libcrypto)' "$WORK/ctl-$os.log" || die "$os: doctored reference did not trigger bundling — the bundler is blind"
  smoke_run "$os" "$WORK/ctl-$os"; echo "$SMOKE_OUT" | tail -2
  [ "$SMOKE_RC" = 0 ] && grep -q '^PGSQL-TREE-SMOKE PASS .*bundled=[1-9]' <<<"$SMOKE_OUT" || die "$os: smoke on the OpenSSL-bundled tree FAILED (rc=$SMOKE_RC)"
  copy_tree "$os" "$WORK/ctl-$os" "neg-bundled-$os" 'rm -f lib/libssl.so.3'
  smoke_run "$os" "$WORK/neg-bundled-$os"; expect_fail "bundled libssl.so.3 removed" "libssl.so.3"
  # Control 3 (symbol skew, R1 in reverse): build the SAME tree on the LATEST image. If its symbol-version
  # requirements exceed the floor tree's, the FLOOR smoke must reject it by name — that is what proves the
  # floor build is the fix. Where the two are identical there is no skew to catch on this OS; say so.
  local new="$WORK/newest-built-$os" extra tag
  reference_libs "$os" latest
  rm -rf "$new"; mkdir -p "$new"
  BUILD_BASE=latest run_build "$os" "ref-$os-latest.txt" "$new" >"$WORK/newest-built-$os.log" 2>&1 \
    || { tail -15 "$WORK/newest-built-$os.log" >&2; die "$os: newest-image build failed"; }
  extra="$(LC_ALL=C comm -13 "$stage/share/aireos/HOST_LIB_VERSIONS" "$new/share/aireos/HOST_LIB_VERSIONS" | head -1)"
  log "$os: newest-built requirements: $(tr '\n' ';' <"$new/share/aireos/HOST_LIB_VERSIONS") | beyond the floor tree's: ${extra:-none}"
  if [ -n "$extra" ]; then
    tag="${extra##* }"
    smoke_run "$os" "$new" floor
    expect_fail "newest-built tree on the floor image" "$tag" "host library version missing" "unresolved shared libraries (ldd"
  else
    log "$os: skew control not applicable — the newest-built tree requires no symbol version beyond the floor tree's"
  fi
}

case "$CMD" in
  build) for os in "${OSES[@]}"; do image_for "$os" >/dev/null; do_build "$os"; done ;;
  smoke) for os in "${OSES[@]}"; do image_for "$os" >/dev/null; image_for "$os" latest >/dev/null; do_smoke "$os"; done ;;
  *) die "usage: $0 build|smoke [ostag...]" ;;
esac
log "done ($CMD: ${OSES[*]})"
