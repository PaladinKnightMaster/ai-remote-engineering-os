#!/usr/bin/env bash
# check-provenance.sh — task release-check's provenance gate (ADR-0070 D9,
# VER-11, T-129a + T-129b).
#
# Fails if any dist/offline/*.tar.gz artifact:
#   - lacks a detached .sig, or the .sig does not verify against
#     RELEASE_PUBLIC_KEY (T-129a);
#   - lacks a sibling "<artifact>.sbom.json", or that SBOM's coverage does
#     not match what is actually IN the artifact (T-129b) — a presence-only
#     check ("a file called *.sbom.json exists") is NOT enough per ADR-0070
#     D9: the gate must catch a component quietly dropped from an otherwise-
#     present SBOM, not just a wholly-missing one. check-coverage.mjs does
#     the actual comparison (bin/ executables for the core bundle; the
#     repo/Packages entry count for the postgres companion).
#
# "A provenance gate that cannot fail is the S-25 pattern" (ADR-0070 D9) —
# this gate must be demonstrated failing on a missing signature, a missing
# SBOM, AND a SBOM with a component removed, not just shown passing.
#
# Self-skips (loud, exit 0) when dist/offline has NO *.tar.gz at all — most
# release-check runs happen in a dev loop before anyone has run
# `task offline-bundle`, the same posture as the test-integration leg's
# self-skip-without-a-configured-DB convention. Once an artifact EXISTS on
# disk it is held to the gate unconditionally: a missing or invalid signature
# or SBOM on an existing artifact is a hard failure, never silently skipped.
#
# Usage: RELEASE_PUBLIC_KEY=<path to aireos-release.pub> bash infra/offline/check-provenance.sh
#   (CI / a real release run points RELEASE_PUBLIC_KEY at the published key;
#   local dev/testing can point it at a throwaway public key minted per
#   infra/offline/testdata/fixture-release-key/README.md — NEVER use a
#   throwaway key to verify a real release artifact. There is deliberately NO
#   default key: with artifacts present and RELEASE_PUBLIC_KEY unset this gate
#   dies loudly (below); with no artifacts at all it self-skips, as documented.)
#
# Needs `node` (for the SBOM coverage check) in addition to `openssl` and `tar`.
set -euo pipefail

# NB: this script lives at infra/offline/check-provenance.sh — dist/offline/
# is TWO levels up ($SCRIPT_DIR/../..), not one; a one-level ".." resolves to
# infra/ and silently self-skips against a directory that never has artifacts.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
OUT_DIR="${OUT_DIR:-$REPO_ROOT/dist/offline}"
PUB="${RELEASE_PUBLIC_KEY:-}"

log() { printf '\033[1;34m[provenance]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[provenance] FAIL:\033[0m %s\n' "$*" >&2; }
die() { printf '\033[1;31m[provenance] ERROR:\033[0m %s\n' "$*" >&2; exit 1; }

shopt -s nullglob
artifacts=("$OUT_DIR"/*.tar.gz)
shopt -u nullglob

if [ "${#artifacts[@]}" -eq 0 ]; then
  log "SKIP — no *.tar.gz artifacts in $OUT_DIR (nothing built yet; build with task offline-bundle / infra/offline/postgres/build-postgres-companions.sh, then sign with infra/offline/sign-release.sh, before this gate can check anything)"
  exit 0
fi

[ -n "$PUB" ] || die "found ${#artifacts[@]} artifact(s) in $OUT_DIR but RELEASE_PUBLIC_KEY is not set — cannot verify"
[ -f "$PUB" ] || die "RELEASE_PUBLIC_KEY '$PUB' not found"

bad=0
for artifact in "${artifacts[@]}"; do
  sig="${artifact}.sig"
  if [ ! -f "$sig" ]; then
    fail "$(basename "$artifact") has NO signature (expected $(basename "$sig"))"
    bad=1
    continue
  fi
  if openssl pkeyutl -verify -pubin -inkey "$PUB" -rawin -in "$artifact" -sigfile "$sig" >/dev/null 2>&1; then
    log "OK — $(basename "$artifact") verifies against $(basename "$PUB")"
  else
    fail "$(basename "$artifact") signature does NOT verify against $(basename "$PUB")"
    bad=1
  fi

  # --- SBOM: present, AND its coverage matches the artifact (ADR-0070 D9,
  # T-129b) ---------------------------------------------------------------
  sbom="${artifact}.sbom.json"
  if [ ! -f "$sbom" ]; then
    fail "$(basename "$artifact") has NO SBOM (expected $(basename "$sbom"))"
    bad=1
    continue
  fi
  if cov_out="$(node "$SCRIPT_DIR/sbom/check-coverage.mjs" "$artifact" "$sbom" 2>&1)"; then
    log "$cov_out"
  else
    fail "$cov_out"
    bad=1
  fi
done

[ "$bad" -eq 0 ] || die "provenance gate failed — see FAIL lines above"
log "DONE — ${#artifacts[@]} artifact(s) verified against $(basename "$PUB")"
