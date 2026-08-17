#!/usr/bin/env bash
# build-postgres-companions.sh — build the per-OS offline postgres companion
# artifacts (§OI-c, ADR-0052). Runs on a CONNECTED build host with Docker;
# fetch-postgres.sh runs inside each base-OS image so the closure matches the
# target. Output: dist/offline/aireos-postgres-<ostag>.tar.gz (gitignored).
#
# Usage: bash infra/offline/postgres/build-postgres-companions.sh [os...]
#   default OS set: ubuntu2404 rocky9   (ADR-0051 platform scope)
set -euo pipefail
export MSYS_NO_PATHCONV=1   # Windows/git-bash: don't mangle the -v mount paths

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
OUT_DIR="${OUT_DIR:-$REPO_ROOT/dist/offline}"
mkdir -p "$OUT_DIR"

# os tag -> base image.
image_for() {
  case "$1" in
    ubuntu2404) echo "ubuntu:24.04" ;;
    rocky9)     echo "rockylinux:9" ;;
    *) echo "" ;;
  esac
}

OSES=("$@"); [ "${#OSES[@]}" -gt 0 ] || OSES=(ubuntu2404 rocky9)

for os in "${OSES[@]}"; do
  img="$(image_for "$os")"
  [ -n "$img" ] || { echo "unknown OS tag '$os' (want ubuntu2404|rocky9)" >&2; exit 1; }
  stage="$OUT_DIR/aireos-postgres-$os"
  echo "[companions] build $os from $img"
  rm -rf "$stage"; mkdir -p "$stage"
  # fetch-postgres.sh runs in the base image; mount the repo (for the script) +
  # the output stage. MSYS_NO_PATHCONV is set by the caller on Windows hosts.
  docker run --rm \
    -v "$REPO_ROOT/infra/offline/postgres":/src:ro \
    -v "$stage":/out \
    -w /src "$img" bash /src/fetch-postgres.sh

  # --- SHA256_MANIFEST.txt (ADR-0070 D1/D9, T-129a) ---------------------------
  # Parity with build-bundle.sh's manifest block: this artifact previously
  # shipped with NO integrity structure at all, while being the one that runs
  # install-postgres.sh AS ROOT on the target host (ADR-0070 Context gap 1).
  echo "[companions] generate SHA256_MANIFEST.txt for $os"
  ( cd "$stage" && find . -type f ! -name SHA256_MANIFEST.txt -print0 | sort -z \
      | xargs -0 sha256sum > SHA256_MANIFEST.txt )
  ( cd "$stage" && sha256sum -c SHA256_MANIFEST.txt >/dev/null ) \
    || { echo "[companions] ERROR: manifest self-check failed for $os" >&2; exit 1; }

  # --- normalized, deterministic packaging (ADR-0070 D6) — same flags and same
  # rationale as build-bundle.sh: --sort=name/--mtime/--owner/--group remove
  # build-order and build-host non-determinism from the tar layer, and piping
  # through a standalone `gzip -n` removes gzip's own embedded mtime so the
  # compression layer is normalized too.
  tar -C "$OUT_DIR" --sort=name --mtime="@0" --owner=0 --group=0 --numeric-owner \
      -cf - "aireos-postgres-$os" | gzip -n > "$OUT_DIR/aireos-postgres-$os.tar.gz"
  echo "[companions] -> $OUT_DIR/aireos-postgres-$os.tar.gz ($(du -sh "$OUT_DIR/aireos-postgres-$os.tar.gz" | cut -f1))"
done
echo "[companions] done"
