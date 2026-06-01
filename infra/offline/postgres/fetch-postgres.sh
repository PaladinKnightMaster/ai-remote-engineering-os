#!/usr/bin/env bash
# fetch-postgres.sh — build a self-contained offline package repo for
# postgres 16 + pgcrypto for the CURRENT OS (§OI-c, ADR-0052).
#
# Run INSIDE the matching base-OS container (the dependency closure is computed
# against that base, so it matches the target). build-postgres-companions.sh
# orchestrates this per supported OS. Output dir ($OUT, default /out):
#   repo/             complete package set + index (apt Packages / dnf repodata)
#   install-postgres.sh, PKG_KIND, OSTAG
#
# Pure air-gap: the FULL closure is fetched on a pristine base, so the target
# needs nothing but the base OS — no internet, no host package mirror.
set -euo pipefail

OUT="${OUT:-/out}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
mkdir -p "$OUT/repo"
. /etc/os-release

if command -v apt-get >/dev/null 2>&1; then
  # --- Debian / Ubuntu (.deb + apt local repo) ------------------------------
  OSTAG="${ID}${VERSION_ID//./}"           # e.g. ubuntu2404
  export DEBIAN_FRONTEND=noninteractive
  apt-get update -qq
  # 1) Download the closure on the PRISTINE base FIRST (order matters: installing
  #    tooling first would shrink what apt considers "missing" and break the
  #    closure — the lesson from the §OI-c probe).
  apt-get install -y --download-only -o Dir::Cache::archives="$OUT/repo" \
    postgresql-16 postgresql-contrib
  # 2) Index the already-downloaded debs (dpkg-dev only needed for the scan).
  apt-get install -y -qq dpkg-dev
  ( cd "$OUT/repo" && rm -rf partial lock && dpkg-scanpackages -m . > Packages && gzip -kf Packages )
  echo "deb" > "$OUT/PKG_KIND"

elif command -v dnf >/dev/null 2>&1; then
  # --- RHEL family: Rocky / Alma / RHEL / CentOS Stream (.rpm + dnf repo) ----
  OSTAG="${ID}${VERSION_ID%%.*}"           # e.g. rocky9
  EL="$(rpm -E %rhel)"
  # PGDG provides postgres 16; disable the older built-in module so deps resolve.
  dnf install -y "https://download.postgresql.org/pub/repos/yum/reporpms/EL-${EL}-x86_64/pgdg-redhat-repo-latest.noarch.rpm"
  dnf -qy module disable postgresql || true
  dnf install -y dnf-plugins-core createrepo_c
  # --alldeps downloads the COMPLETE closure (even base-present deps) → the repo
  # is self-sufficient on a pristine target.
  dnf download --resolve --alldeps --destdir="$OUT/repo" postgresql16-server postgresql16-contrib
  createrepo_c "$OUT/repo"
  echo "rpm" > "$OUT/PKG_KIND"

else
  echo "fetch-postgres: unsupported OS (need apt-get or dnf)" >&2; exit 1
fi

echo "$OSTAG" > "$OUT/OSTAG"
cp "$SCRIPT_DIR/install-postgres.sh" "$OUT/install-postgres.sh"
chmod +x "$OUT/install-postgres.sh"
echo "fetch-postgres: built '$OSTAG' repo ($(du -sh "$OUT/repo" | cut -f1)) -> $OUT"
