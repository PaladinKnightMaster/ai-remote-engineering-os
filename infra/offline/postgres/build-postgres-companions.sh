#!/usr/bin/env bash
# build-postgres-companions.sh — build the per-OS offline postgres companion
# artifacts (§OI-c, ADR-0052). Runs on a CONNECTED build host with Docker;
# fetch-postgres.sh runs inside each base-OS image so the closure matches the
# target. Output: dist/offline/aireos-postgres-<ostag>.tar.gz (gitignored).
#
# Usage: bash infra/offline/postgres/build-postgres-companions.sh [os...]
#   default OS set: ubuntu2404 rocky9   (ADR-0051 platform scope)
#
# Also generates a CycloneDX 1.6 SBOM per deb-format companion (ADR-0070 D5,
# T-129b) — needs `node` on the HOST (not inside the per-OS containers).
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
  # Clear the previous tarball AND any stale sibling .sig from a prior build
  # (sign-release.sh is a separate, out-of-band step never run by this
  # script). Without this, a rebuild that changes the artifact's bytes
  # leaves an old-but-still-present .sig next to the new tarball — the exact
  # R4 finding (v0.14.0 security review): a directory listing showed a
  # signature dated days before the artifact it sat beside, looking
  # entirely valid. check-provenance.sh WOULD catch the mismatch, but only
  # if someone runs it before shipping; a missing .sig is honest, a wrong
  # one is dangerous.
  rm -rf "$stage" "$OUT_DIR/aireos-postgres-$os.tar.gz" "$OUT_DIR/aireos-postgres-$os.tar.gz.sig"
  mkdir -p "$stage"
  # fetch-postgres.sh runs in the base image; mount the repo (for the script) +
  # the output stage. MSYS_NO_PATHCONV is set by the caller on Windows hosts.
  docker run --rm \
    -v "$REPO_ROOT/infra/offline/postgres":/src:ro \
    -v "$stage":/out \
    -w /src "$img" bash /src/fetch-postgres.sh

  # --- SBOM (ADR-0070 D5, T-129b) --------------------------------------------
  # CycloneDX 1.6 JSON parsed straight from the companion's OWN repo/Packages
  # deb index — no new scanner tool. Only "deb" is parsed today; an rpm-format
  # companion (rocky9) is skipped with a loud NOTE rather than aborting the
  # whole multi-OS build. That means release-check (T-129b) will correctly
  # REFUSE to ship an rpm companion without an SBOM (ADR-0070 D9) — this
  # script does not invent one it cannot honestly produce.
  pkg_kind="$(cat "$stage/PKG_KIND" 2>/dev/null || echo "")"
  if [ "$pkg_kind" = "deb" ]; then
    echo "[companions] generate SBOM for $os"
    bash "$REPO_ROOT/infra/offline/sbom/generate-sbom.sh" --kind companion --stage "$stage" \
      --ostag "$os" --out "$OUT_DIR/aireos-postgres-$os.tar.gz.sbom.json"
  else
    echo "[companions] NOTE: no SBOM parser for PKG_KIND='$pkg_kind' yet (only deb is supported as of T-129b) — $os ships WITHOUT an SBOM; release-check will correctly refuse to release it until an rpm parser exists (ADR-0070 D9)"
  fi

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
