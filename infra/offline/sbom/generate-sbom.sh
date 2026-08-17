#!/usr/bin/env bash
# generate-sbom.sh — CycloneDX 1.6 JSON SBOM generation, one per shipped
# artifact (ADR-0070 D5, OI-34, VER-11, T-129b).
#
# Two kinds, invoked with --kind bundle|companion:
#
#   bundle    — scans the six services + authctl + aireos-verify (pinned
#               cyclonedx-gomod, GOWORK=off, per module — the same idiom
#               `task licenses` uses) + apps/web (pinned cyclonedx-npm) +
#               a hand-described Caddy component carrying the pinned
#               version + SHA-512 build-bundle.sh already verifies. Merged
#               into ONE SBOM via merge-sbom.mjs.
#
#   companion — parses the companion's own repo/Packages deb index (no new
#               scanner tool — see generate-companion-sbom.mjs) into ONE SBOM.
#
# In both cases the result is written to --out (the artifact's PUBLISHED
# sibling, "<artifact>.sbom.json" by convention) AND copied to
# <stage>/SBOM.cdx.json so it is EMBEDDED inside the artifact too — an SBOM
# only readable after extraction is no help to whoever is deciding whether to
# extract (ADR-0070 D5).
#
# Called from build-bundle.sh / build-postgres-companions.sh, which already
# have the pinned Caddy version+hash and the staged repo/Packages in scope —
# so this script does not need to guess or re-derive them.
#
# Prereqs: go 1.23 (for the Go fragments) + node/npx with apps/web's
# node_modules already installed (for the node fragment, bundle kind only) +
# `tar`. `task offline-bundle` already runs `pnpm install --frozen-lockfile`
# before build-bundle.sh, so node_modules is present by the time this runs.
set -euo pipefail
# Callers (build-postgres-companions.sh) export MSYS_NO_PATHCONV=1 on Windows
# so `docker run -v` mount args survive un-mangled. This script never calls
# docker itself, only `go`/`node`/`npx` — real Windows executables that DO
# need git-bash's normal /d/... -> D:/... path translation, so undo the
# inherited setting for everything below or those calls receive a garbled
# path (observed: "/d/foo" -> "D:\d\foo").
unset MSYS_NO_PATHCONV

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="${REPO_ROOT:-$(cd "$SCRIPT_DIR/../../.." && pwd)}"

# Pinned build tools (never shipped — same category as oapi-codegen/go-licenses).
CYCLONEDX_GOMOD_VERSION="v1.9.0"
CYCLONEDX_NPM_VERSION="6.0.1"

log() { printf '\033[1;34m[sbom]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[sbom] ERROR:\033[0m %s\n' "$*" >&2; exit 1; }

KIND=""
STAGE=""
OUT=""
BUNDLE_VERSION=""
OSTAG=""
CADDY_VERSION=""
CADDY_SHA512=""
while [ $# -gt 0 ]; do
  case "$1" in
    --kind) KIND="${2:-}"; shift 2 ;;
    --stage) STAGE="${2:-}"; shift 2 ;;
    --out) OUT="${2:-}"; shift 2 ;;
    --version) BUNDLE_VERSION="${2:-}"; shift 2 ;;
    --ostag) OSTAG="${2:-}"; shift 2 ;;
    --caddy-version) CADDY_VERSION="${2:-}"; shift 2 ;;
    --caddy-sha512) CADDY_SHA512="${2:-}"; shift 2 ;;
    *) die "unknown flag: $1" ;;
  esac
done

[ -n "$STAGE" ] || die "--stage is required"
[ -d "$STAGE" ] || die "--stage '$STAGE' does not exist"
[ -n "$OUT" ] || die "--out is required"

case "$KIND" in
  bundle)
    [ -n "$BUNDLE_VERSION" ] || die "--version is required for --kind bundle"
    [ -n "$CADDY_VERSION" ] || die "--caddy-version is required for --kind bundle"
    command -v go >/dev/null 2>&1 || die "go is required for the Go SBOM fragments"
    command -v npx >/dev/null 2>&1 || die "npx (node) is required for the node SBOM fragment"
    [ -d "$REPO_ROOT/apps/web/node_modules" ] || die "apps/web/node_modules not found — run 'pnpm install --frozen-lockfile' first (task offline-bundle does this)"

    # Work dir as a SIBLING of $STAGE (not under it — must not leak into the
    # manifest/tar) and explicitly not under $TMPDIR, so it stays on the same
    # mount as the repo bind-mount in a Dockerized run.
    WORK="$(mktemp -d "$(dirname "$STAGE")/sbom-work.XXXXXX")"
    trap 'rm -rf "$WORK"' EXIT

    # --- Go: one app-scoped fragment per shipped binary (GOWORK=off, per
    # module — mirrors `task licenses`'s idiom exactly). "module:mainPkg:binName".
    GO_TARGETS=(
      "services/api:.:api"
      "services/audit-service:.:audit-service"
      "services/audit-service:./cmd/aireos-verify:aireos-verify"
      "services/session-broker:.:session-broker"
      "services/connector-service:.:connector-service"
      "services/policy-service:.:policy-service"
      "services/mcp-gateway:.:mcp-gateway"
      "packages/go/auth:./cmd/authctl:authctl"
    )
    for target in "${GO_TARGETS[@]}"; do
      mod="${target%%:*}"; rest="${target#*:}"; main="${rest%%:*}"; bin="${rest##*:}"
      log "go fragment: $bin (module $mod, main $main)"
      ( cd "$REPO_ROOT/$mod" && GOWORK=off CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
          go run "github.com/CycloneDX/cyclonedx-gomod/cmd/cyclonedx-gomod@${CYCLONEDX_GOMOD_VERSION}" \
          app -json -output-version 1.6 -main "$main" -output "$WORK/go-$bin.json" . ) \
        || die "cyclonedx-gomod failed for $bin"
    done

    # --- Node: apps/web (production deps only) --------------------------------
    log "node fragment: apps/web"
    ( cd "$REPO_ROOT/apps/web" && npx --yes "@cyclonedx/cyclonedx-npm@${CYCLONEDX_NPM_VERSION}" \
        --sv 1.6 --ignore-npm-errors --omit dev --validate \
        --output-file "$WORK/node-web.json" package.json ) \
      || die "cyclonedx-npm failed for apps/web"

    # --- merge + Caddy ----------------------------------------------------------
    log "merge -> $OUT"
    node "$SCRIPT_DIR/merge-sbom.mjs" \
      --name aireos-offline-bundle --version "$BUNDLE_VERSION" \
      --go-dir "$WORK" --node "$WORK/node-web.json" \
      --caddy-version "$CADDY_VERSION" --caddy-sha512 "$CADDY_SHA512" \
      --output "$OUT" || die "merge-sbom failed"
    ;;

  companion)
    [ -n "$OSTAG" ] || die "--ostag is required for --kind companion"
    [ -f "$STAGE/repo/Packages" ] || die "$STAGE/repo/Packages not found — build the companion first"
    PKG_KIND="$(cat "$STAGE/PKG_KIND" 2>/dev/null || echo "")"
    [ "$PKG_KIND" = "deb" ] || die "companion SBOM generation only supports PKG_KIND=deb today (got '$PKG_KIND') — an rpm-format companion needs its own parser, not silently skipped"
    log "companion fragment (deb) -> $OUT"
    node "$SCRIPT_DIR/generate-companion-sbom.mjs" \
      --packages "$STAGE/repo/Packages" --name "aireos-postgres-$OSTAG" \
      --output "$OUT" || die "generate-companion-sbom failed"
    ;;

  *) die "--kind must be 'bundle' or 'companion' (got '$KIND')" ;;
esac

# --- embed inside the artifact too, alongside the existing manifest/VERSION -
cp "$OUT" "$STAGE/SBOM.cdx.json"
log "DONE -> $OUT (embedded as $STAGE/SBOM.cdx.json)"
