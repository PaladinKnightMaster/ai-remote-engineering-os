#!/usr/bin/env bash
# sign-release.sh — detached Ed25519 signatures over BOTH shipped offline
# artifacts (ADR-0070 D1, T-129a).
#
# HOST-SIDE, OUT-OF-BAND STEP. Run this on the machine where the release
# private key is held. Do NOT run it inside build-bundle.sh, do NOT run it
# inside a build container, and do NOT wire it into `task offline-bundle` —
# build-bundle.sh's cross-compile step already runs inside a golang:1.26
# container on this host (no local go toolchain), and a release key must
# never enter a build context (ADR-0070 D3: "never on a build host that runs
# untrusted code"). Build produces the artifact; this distinct, separate step
# signs it.
#
# THE PRODUCTION KEY IS NOT CREATED BY THIS SCRIPT, and this script never
# generates, prints, or logs a key of any kind — it only CONSUMES a private
# key path you supply. The real release key is minted out-of-band by the CEO:
#
#   openssl genpkey -algorithm ed25519 -out aireos-release.key
#   openssl pkey -in aireos-release.key -pubout -out aireos-release.pub
#
# and is held OUTSIDE this repository, outside any build host, and outside
# any automated run or transcript — see the "Release key custody" section of
# docs/devops/offline-install-v3.md for the full procedure and where the key
# must NOT live. A THROWAWAY fixture keypair for tests only, clearly labelled
# as a fixture and never suitable for a real release, lives at
# infra/offline/testdata/fixture-release-key/.
#
# VERSION DISCIPLINE (the T-129a dependency, RESOLVED 2026-08-18): Taskfile.yml's
# `offline-bundle` now sets BUNDLE_VERSION: "0.13.0". It was stale at "0.12.0"
# from T-129a until the pre-T-133 rebuild, so the signed artifacts on disk in
# that window were named 0.13.0 only because the build was run with an explicit
# override. The bump moved EARLIER than its original T-136 slot because T-133's
# quickstart gate consumes the built bundle, and gating a quickstart on a
# mislabeled artifact undercuts OI-37 — the criterion about exactly this.
# Signing an artifact whose filename/embedded VERSION says the wrong release
# would ATTEST THE WRONG VERSION, so before signing a real release, still
# confirm the artifact filename in dist/offline/ matches the version being cut.
#
# Usage:
#   bash infra/offline/sign-release.sh --key <private-key.pem> [artifact.tar.gz ...]
# With no artifact arguments, signs every dist/offline/*.tar.gz present (both
# the core bundle and any postgres companions that have been built).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
OUT_DIR="${OUT_DIR:-$REPO_ROOT/dist/offline}"

log() { printf '\033[1;34m[sign]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[sign] ERROR:\033[0m %s\n' "$*" >&2; exit 1; }

KEY=""
ARTIFACTS=()
while [ $# -gt 0 ]; do
  case "$1" in
    --key) KEY="${2:-}"; shift 2 ;;
    -h|--help) grep '^#' "$0" | sed 's/^#//'; exit 0 ;;
    *) ARTIFACTS+=("$1"); shift ;;
  esac
done

[ -n "$KEY" ] || die "usage: sign-release.sh --key <private-key.pem> [artifact.tar.gz ...]"
[ -f "$KEY" ] || die "key file not found: $KEY"

if [ "${#ARTIFACTS[@]}" -eq 0 ]; then
  shopt -s nullglob
  for f in "$OUT_DIR"/*.tar.gz; do ARTIFACTS+=("$f"); done
  shopt -u nullglob
fi
[ "${#ARTIFACTS[@]}" -gt 0 ] || die "no *.tar.gz artifacts found in $OUT_DIR — build first (task offline-bundle / infra/offline/postgres/build-postgres-companions.sh)"

# VER-45 (T-162): the default glob above signs every dist/offline/*.tar.gz it finds,
# with no check that they all belong to the SAME release — this is the exact shape
# of the near-miss recorded at the v0.15.0 cut, where a stale v0.14.0 core bundle
# sat unmoved beside the fresh v0.15.0 one and was nearly signed into the wrong
# release (the operating fix at the time was manually moving it to
# dist/offline/_superseded/). Guard it mechanically for the one part that's cheap
# to derive — the versioned core-bundle filename — without touching the postgres
# companion, which is deliberately version-independent (no VERSION file).
if [ "${#ARTIFACTS[@]}" -gt 0 ]; then
  core_versions=()
  for f in "${ARTIFACTS[@]}"; do
    b="$(basename "$f")"
    if [[ "$b" =~ ^aireos-offline-([0-9]+\.[0-9]+\.[0-9]+)\.tar\.gz$ ]]; then
      core_versions+=("${BASH_REMATCH[1]}")
    fi
  done
  if [ "${#core_versions[@]}" -gt 1 ]; then
    n_uniq="$(printf '%s\n' "${core_versions[@]}" | sort -u | wc -l)"
    if [ "$n_uniq" -gt 1 ]; then
      die "refusing to sign a mixed set of core-bundle versions found in $OUT_DIR ($(printf '%s ' "${core_versions[@]}")) — move the stale one out (e.g. to $OUT_DIR/_superseded/) or pass explicit artifact paths instead of relying on the default glob"
    fi
  fi
fi

# Derive the public key ONCE for an immediate self-check per artifact (a bad
# signature must be caught here, not at an operator's first verify attempt).
PUB_TMP="$(mktemp)"
trap 'rm -f "$PUB_TMP"' EXIT
openssl pkey -in "$KEY" -pubout -out "$PUB_TMP" 2>/dev/null || die "could not derive a public key from $KEY — is it a valid Ed25519 private key?"

log "signing ${#ARTIFACTS[@]} artifact(s) with $KEY"
for artifact in "${ARTIFACTS[@]}"; do
  [ -f "$artifact" ] || die "artifact not found: $artifact"
  sig="${artifact}.sig"
  log "sign $(basename "$artifact") -> $(basename "$sig")"
  openssl pkeyutl -sign -rawin -inkey "$KEY" -in "$artifact" -out "$sig"
  openssl pkeyutl -verify -pubin -inkey "$PUB_TMP" -rawin -in "$artifact" -sigfile "$sig" >/dev/null \
    || die "self-check FAILED immediately after signing $artifact — refusing to ship a bad signature"
done

FINGERPRINT="$(openssl pkey -pubin -in "$PUB_TMP" -pubout -outform DER 2>/dev/null | openssl dgst -sha256 | awk '{print $NF}')"
log "DONE — ${#ARTIFACTS[@]} artifact(s) signed and self-verified."
log "Publish this public key's SHA-256 fingerprint in the release notes (ADR-0070 D4): $FINGERPRINT"
