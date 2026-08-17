#!/usr/bin/env bash
# build-bundle.sh — offline-install bundle builder (§OI-a, ADR-0051).
#
# Runs on a CONNECTED build host (offline-first governs the TARGET host, not the
# build). Produces dist/offline/aireos-offline-<version>.tar.gz containing:
#
#   bin/      api, audit-service, session-broker (static linux/amd64) + caddy
#   web/      the built SPA static assets (apps/web/dist)
#   config/   Caddyfile + systemd unit templates
#   install.sh, setup-loopback-demo.sh, postgres/   included when present
#             (authored in §OI-b; setup-loopback-demo.sh is the ONB-3 demo
#             quickstart's step 2, T-118b)
#   VERSION, SHA256_MANIFEST.txt, SBOM.cdx.json
#
# Enforces the OI-4 size cap (<=500 MB uncompressed / <=150 MB gzipped).
#
# Build-host prereqs: go 1.23, curl OR wget, tar, gzip, sha256sum, sha512sum,
# and a pre-built SPA at apps/web/dist. The `task offline-bundle` target runs
# `pnpm --filter @aireos/web build` first. See docs/tickets/T-021a.
#
# Also needs node/npx with apps/web/node_modules already installed (ADR-0070
# D5, T-129b) — the SBOM step (5c) shells out to cyclonedx-npm for apps/web's
# production deps. `task offline-bundle` already runs `pnpm install
# --frozen-lockfile` before this script; a standalone invocation must do the
# same first.
set -euo pipefail

# --- pinned third-party artifacts (verified at build time) ------------------
CADDY_VERSION="2.11.3"
# NB: Caddy publishes SHA-512 checksums (NOT sha256). Pinned from the official
# caddy_<v>_checksums.txt; the downloaded tarball is verified against this
# before use, so a tampered/unexpected release fails the build.
CADDY_SHA512="ee886eceda0ff9f30610d3be9b5b594026591e19add6b3961a341c72abe468e5eac9d7c2c2450bbb8420db1f827b954521f9336be4872f81090b8618adf8815a"

GOOS_TARGET="linux"
GOARCH_TARGET="amd64"
# connector-service + policy-service joined at v0.2.0 (OI-6, T-033b); mcp-gateway
# (the Typed MCP Gateway, the first agent surface) joins at v0.3.0 (OI-10, T-043a).
SERVICES="api audit-service session-broker connector-service policy-service mcp-gateway"

# OI-4 caps.
MAX_UNCOMPRESSED_MB=500
MAX_GZIPPED_MB=150

# --- locate repo root + output ----------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$REPO_ROOT"

log() { printf '\033[1;34m[bundle]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[bundle] ERROR:\033[0m %s\n' "$*" >&2; exit 1; }

# BUNDLE_VERSION is REQUIRED and never silently defaulted (the OI-21 rule
# applied to the artifact filename itself, not just the profile): a stale
# default here shipped v0.5.0..v0.11.0's release artifacts under a filename
# that still read aireos-offline-0.4.0.tar.gz, eight releases stale, because
# nothing set it. `task offline-bundle` is the single source of truth — it
# passes the current release version; a manual invocation of this script must
# set BUNDLE_VERSION explicitly.
[ -n "${BUNDLE_VERSION:-}" ] || die "BUNDLE_VERSION is required and is never defaulted — set it (task offline-bundle passes the current release version automatically; a direct invocation must set BUNDLE_VERSION=<version>)"
OUT_DIR="${OUT_DIR:-$REPO_ROOT/dist/offline}"
STAGE="$OUT_DIR/aireos-offline-$BUNDLE_VERSION"
TARBALL="$OUT_DIR/aireos-offline-$BUNDLE_VERSION.tar.gz"

fetch() { # fetch <url> <outfile>
  if command -v curl >/dev/null 2>&1; then curl -fsSL -o "$2" "$1"
  elif command -v wget >/dev/null 2>&1; then wget -qO "$2" "$1"
  else die "need curl or wget on the build host"; fi
}

log "clean stage $STAGE"
rm -rf "$STAGE" "$TARBALL"
mkdir -p "$STAGE/bin" "$STAGE/web" "$STAGE/config"

# --- 1. cross-compile the Go services + authctl (static linux/amd64) --------
log "cross-compile Go services ($GOOS_TARGET/$GOARCH_TARGET, CGO disabled)"
for svc in $SERVICES; do
  CGO_ENABLED=0 GOOS="$GOOS_TARGET" GOARCH="$GOARCH_TARGET" \
    go build -trimpath -ldflags "-s -w" -o "$STAGE/bin/$svc" "./services/$svc"
done
# authctl: the offline key + argon2id-hash CLI install.sh needs (§OI-b).
CGO_ENABLED=0 GOOS="$GOOS_TARGET" GOARCH="$GOARCH_TARGET" \
  go build -trimpath -ldflags "-s -w" -o "$STAGE/bin/authctl" ./packages/go/auth/cmd/authctl
# aireos-verify: the offline, third-party evidence verifier (ADR-0069 D6,
# T-126b). Same hand-added-special-case shape as authctl above — it is a
# `cmd/` binary inside a service module (services/audit-service), not one of
# the top-level services in $SERVICES, so it is never picked up automatically
# and must be listed here explicitly, or it silently never ships.
CGO_ENABLED=0 GOOS="$GOOS_TARGET" GOARCH="$GOARCH_TARGET" \
  go build -trimpath -ldflags "-s -w" -o "$STAGE/bin/aireos-verify" ./services/audit-service/cmd/aireos-verify

# --- 2. SPA static assets (pre-built by `pnpm build`) -----------------------
[ -d "apps/web/dist" ] || die "apps/web/dist not found — run 'pnpm --filter @aireos/web build' first (the task offline-bundle target does this)"
log "copy SPA static assets"
cp -a apps/web/dist/. "$STAGE/web/"

# --- 3. Caddy (fetch + verify sha512 + extract) -----------------------------
log "fetch + verify Caddy v$CADDY_VERSION"
CADDY_TGZ="$OUT_DIR/caddy_${CADDY_VERSION}_linux_amd64.tar.gz"
fetch "https://github.com/caddyserver/caddy/releases/download/v${CADDY_VERSION}/caddy_${CADDY_VERSION}_linux_amd64.tar.gz" "$CADDY_TGZ"
echo "${CADDY_SHA512}  ${CADDY_TGZ}" | sha512sum -c - >/dev/null || die "Caddy checksum mismatch — refusing tampered/unexpected binary"
tar -xzf "$CADDY_TGZ" -C "$STAGE/bin" caddy
chmod +x "$STAGE/bin/caddy"
rm -f "$CADDY_TGZ"

# --- 4. runtime config the bundle ships -------------------------------------
log "stage runtime config (Caddyfile + systemd units)"
cp "$SCRIPT_DIR/Caddyfile" "$STAGE/config/Caddyfile"
cp "$SCRIPT_DIR"/systemd/*.service "$STAGE/config/"

# --- 5. include the installer (§OI-b). Postgres ships as SEPARATE per-OS
#        companion artifacts (aireos-postgres-<os>.tar.gz, ADR-0052) — NOT in
#        this core bundle (their ~80 MB closures would bust the OI-4 cap).
if [ -f "$SCRIPT_DIR/install.sh" ]; then
  cp "$SCRIPT_DIR/install.sh" "$STAGE/install.sh"; chmod +x "$STAGE/install.sh"
else
  log "NOTE: infra/offline/install.sh not present — added by §OI-b"
fi

# --- 5b. include the ONB-3 loopback demo quickstart's step-2 script (T-118b).
#         Same conditional-copy shape as install.sh above — this is the
#         operator-run half of the demo target setup (T-124: it must ship in
#         the bundle for a genuinely air-gapped install to reach it).
if [ -f "$SCRIPT_DIR/setup-loopback-demo.sh" ]; then
  cp "$SCRIPT_DIR/setup-loopback-demo.sh" "$STAGE/setup-loopback-demo.sh"; chmod +x "$STAGE/setup-loopback-demo.sh"
else
  log "NOTE: infra/offline/setup-loopback-demo.sh not present — added by T-118b"
fi

# --- 5c. SBOM (ADR-0070 D5, OI-34, T-129b) ----------------------------------
# CycloneDX 1.6 JSON covering every staged Go binary (the six services +
# authctl + aireos-verify) + apps/web's production deps + Caddy (pinned
# version/hash from step 3, already verified above). Written to the artifact's
# published sibling AND copied into $STAGE so it ships EMBEDDED too — an SBOM
# only readable after extraction is no help to whoever is deciding whether to
# extract. Runs BEFORE the manifest step so SHA256_MANIFEST.txt covers it.
log "generate SBOM (CycloneDX 1.6)"
bash "$SCRIPT_DIR/sbom/generate-sbom.sh" --kind bundle --stage "$STAGE" \
  --version "$BUNDLE_VERSION" --caddy-version "$CADDY_VERSION" --caddy-sha512 "$CADDY_SHA512" \
  --out "$OUT_DIR/aireos-offline-$BUNDLE_VERSION.tar.gz.sbom.json"

# --- 6. VERSION + SHA256 manifest (OI-5) ------------------------------------
echo "$BUNDLE_VERSION" > "$STAGE/VERSION"
log "generate SHA256_MANIFEST.txt"
( cd "$STAGE" && find . -type f ! -name SHA256_MANIFEST.txt -print0 | sort -z \
    | xargs -0 sha256sum > SHA256_MANIFEST.txt )
( cd "$STAGE" && sha256sum -c SHA256_MANIFEST.txt >/dev/null ) || die "manifest self-check failed"

# --- 7. size gate (OI-4) ----------------------------------------------------
UNCOMPRESSED_MB=$(du -sm "$STAGE" | cut -f1)
log "uncompressed bundle: ${UNCOMPRESSED_MB} MB (cap ${MAX_UNCOMPRESSED_MB})"
[ "$UNCOMPRESSED_MB" -le "$MAX_UNCOMPRESSED_MB" ] || die "bundle exceeds ${MAX_UNCOMPRESSED_MB}MB uncompressed (OI-4)"

# --- 8. package + gzipped size gate -----------------------------------------
# Normalized, deterministic packaging (ADR-0070 D6): --sort=name orders entries
# independent of directory-walk order, a fixed --mtime removes per-build
# timestamps, and --owner/--group/--numeric-owner remove the building host's
# uid/gid from the archive. Without these, two builds of byte-identical
# CONTENT still produce different tar bytes, which would make T-130's later
# reproducibility measurement vacuous before it even compares real output.
# gzip ALSO stamps an mtime by default (`tar -czf`'s built-in gzip does this
# too) — piping through a standalone `gzip -n` suppresses it, so the
# compression layer is normalized as well as the tar layer; skipping this
# would leave the normalization incomplete and T-130 would measure nothing.
log "package $TARBALL"
tar -C "$OUT_DIR" --sort=name --mtime="@0" --owner=0 --group=0 --numeric-owner \
    -cf - "aireos-offline-$BUNDLE_VERSION" | gzip -n > "$TARBALL"
GZIPPED_MB=$(du -sm "$TARBALL" | cut -f1)
log "gzipped tarball: ${GZIPPED_MB} MB (cap ${MAX_GZIPPED_MB})"
[ "$GZIPPED_MB" -le "$MAX_GZIPPED_MB" ] || die "tarball exceeds ${MAX_GZIPPED_MB}MB gzipped (OI-4)"

log "DONE -> $TARBALL (${GZIPPED_MB}MB gz, ${UNCOMPRESSED_MB}MB unpacked)"
