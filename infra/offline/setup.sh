#!/usr/bin/env bash
# setup.sh — the ONE install command (INS-5/INS-6, T-174; ADR-0077 D5/D8):
#
#     sudo ./aireos-offline-<version>/setup.sh        (any working directory)
#
# It finds the PostgreSQL companion BESIDE the unpacked bundle (<bundle>/../aireos-postgres-<ostag>.tar.gz), checks
# everything before changing anything, installs the companion with a database password it generates, runs install.sh
# against it, and ends by printing the one address to open in the browser. Unattended: every install.sh value is
# still an environment variable (AIREOS_DEPLOYMENT_PROFILE, AIREOS_BOOTSTRAP_OPERATOR_*, AIREOS_SITE_ADDRESS, ...) —
# with them all set nothing is prompted. AIREOS_PG_PORT picks the companion's port (default 5432).
#
# Which database (decided from what is already on the host, never guessed):
#   bring-your-own   AIREOS_DB_URL is set                      -> no companion; install.sh gets that URL (ADR-0077 D8)
#   managed re-run   /etc/aireos/db-password exists             -> reuse the password, re-run the (idempotent) companion
#   existing install aireos.env has DATABASE_URL, no password   -> INS-8: its PostgreSQL is left alone; that URL is reused
#   fresh            none of the above                          -> generate the password, install the companion
#
# The password: 256 bits from /dev/urandom as hex, kept in /etc/aireos/db-password (root, 0600 from its first byte),
# NEVER printed and NEVER in argv — it reaches install-postgres.sh as COCKPIT_PW and install.sh as AIREOS_DB_URL, both
# environment. It is reused on every re-run: an existing role keeps its password, so a fresh one would write a URL the
# database rejects (T-172a).
set -euo pipefail
export LC_ALL=C

SETUP_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# install.sh is sourceable (no installer body runs): reuse its tested helpers rather than copy them.
# shellcheck source=install.sh
source "$SETUP_DIR/install.sh"
PW_FILE="$ETC/db-password"
DB_NAME="${COCKPIT_DB:-cockpit}"; DB_USER="${COCKPIT_USER:-cockpit}"
WORK=""; trap '[ -z "$WORK" ] || rm -rf "$WORK"' EXIT

log() { printf '\033[1;36m[setup]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[setup] ERROR:\033[0m %s\n' "$*" >&2; exit 1; }

# ---- 1. preflight: nothing on the host changes until every check here has passed --------------------------------------
osr() { sed -n "s/^$1=//p" /etc/os-release 2>/dev/null | head -1 | tr -d '"' || true; }

step_preflight() {
  [ "$(id -u)" = 0 ] || die "This must run as root: use  sudo $SETUP_DIR/setup.sh"
  [ -d "$SETUP_DIR/bin" ] && [ -d "$SETUP_DIR/web" ] && [ -d "$SETUP_DIR/config" ] || die "$SETUP_DIR is not an unpacked AI ReOS bundle (bin/ web/ config/ are missing); unpack the aireos-offline tarball again."
  if [ -n "${AIREOS_DB_URL:-}" ]; then
    DB_MODE=byo; DB_URL="$AIREOS_DB_URL"
    log "AIREOS_DB_URL is set: using that PostgreSQL; the PostgreSQL companion is not installed."
  elif [ -f "$PW_FILE" ]; then DB_MODE=managed
  elif [ -n "$(aireos_persisted_value DATABASE_URL "$ENV_FILE")" ]; then
    DB_MODE=existing; DB_URL="$(aireos_persisted_value DATABASE_URL "$ENV_FILE")"
    aireos_valid_db_url "$DB_URL" || die "DATABASE_URL in $ENV_FILE is not a PostgreSQL URL; fix it, or set AIREOS_DB_URL, and run this again. Nothing was changed."
    log "This host already has an install whose database is configured in $ENV_FILE: that PostgreSQL is left untouched, and no PostgreSQL companion is installed."
  else
    DB_MODE=fresh
    [ ! -e /var/lib/aireos/pgsql/PG_VERSION ] || die "An AI ReOS PostgreSQL is already installed here, but this script has no record of its password. Set AIREOS_DB_URL to its URL (postgres://cockpit:<password>@localhost:<port>/cockpit?sslmode=disable) and run this again. Nothing was changed."
  fi
  [ "$DB_MODE" = byo ] || [ "$DB_MODE" = existing ] || step_preflight_companion
}

step_preflight_companion() {
  local hid hver hlike archive comp
  hid="$(osr ID)"; hver="$(osr VERSION_ID)"; hlike="$(osr ID_LIKE)"
  case "$hid $hlike" in   # the same mapping as install-postgres.sh
    *rocky*|*rhel*|*almalinux*|*centos*) OSTAG="rocky${hver%%.*}" ;;
    ubuntu*) OSTAG="ubuntu${hver//./}" ;;
    *) OSTAG="${hid}${hver}" ;;
  esac
  case "$OSTAG" in ubuntu2404|ubuntu2604|rocky9) ;; *)
    die "This host ($(osr PRETTY_NAME)) has no bundled PostgreSQL; supported are Ubuntu 24.04, Ubuntu 26.04 and Rocky/RHEL 9. To use your own PostgreSQL 16 with pgcrypto, run this again with AIREOS_DB_URL=postgres://<user>:<password>@<host>:5432/<database>?sslmode=disable set. Nothing was changed." ;;
  esac
  archive="$(cd "$SETUP_DIR/.." && pwd)/aireos-postgres-$OSTAG.tar.gz"
  [ -f "$archive" ] || die "The PostgreSQL companion for this OS is missing: expected the file $archive. Download aireos-postgres-$OSTAG.tar.gz into the same folder as the unpacked aireos-offline folder and run this again. Nothing was changed."
  WORK="$(mktemp -d "${TMPDIR:-/var/tmp}/aireos-setup.XXXXXX")"
  tar -xzf "$archive" -C "$WORK" || die "Could not unpack $archive (is the download complete?). Nothing was changed."
  comp="$WORK/aireos-postgres-$OSTAG"
  [ -f "$comp/install-postgres.sh" ] && [ -f "$comp/SHA256_MANIFEST.txt" ] || die "$archive does not contain aireos-postgres-$OSTAG/ with install-postgres.sh and SHA256_MANIFEST.txt; download it again. Nothing was changed."
  ( cd "$comp" && sha256sum --quiet -c SHA256_MANIFEST.txt >/dev/null 2>&1 ) || die "$archive fails its own SHA256_MANIFEST.txt (damaged download, or changed after it was built); download it again and verify its signature. Nothing was changed."
  COMP="$comp"
}

# ---- 2. the database ---------------------------------------------------------------------------------------------------
step_database() {
  [ "$DB_MODE" = managed ] || [ "$DB_MODE" = fresh ] || return 0
  local port="${AIREOS_PG_PORT:-}" pw url
  if [ -z "$port" ] && [ "$DB_MODE" = managed ]; then   # keep the port the existing install uses
    url="$(aireos_persisted_value DATABASE_URL "$ENV_FILE")"; port="$(sed -n 's#^[^@]*@[^:/]*:\([0-9][0-9]*\)/.*#\1#p' <<<"$url")"
  fi
  port="${port:-5432}"
  if [ -f "$PW_FILE" ]; then
    pw="$(<"$PW_FILE")"; [[ "$pw" =~ ^[A-Za-z0-9._~-]{16,}$ ]] || die "$PW_FILE does not hold a usable password (expected the 64 hex characters this script wrote); restore it, or set AIREOS_DB_URL to use your own PostgreSQL."
  else
    pw="$(head -c 32 /dev/urandom | od -An -vtx1 | tr -d ' \n')"; [ "${#pw}" = 64 ] || die "could not read random bytes from /dev/urandom"
    install -d -m 755 "$ETC"; rm -f "$PW_FILE.new"
    ( umask 077; printf '%s\n' "$pw" >"$PW_FILE.new" ) && mv -f "$PW_FILE.new" "$PW_FILE"   # 0600 from creation: no chmod window
    log "generated the database password and stored it in $PW_FILE (root only); it is never printed."
  fi
  log "install the PostgreSQL companion for $OSTAG (port $port)"
  ( export COCKPIT_PW="$pw" AIREOS_PG_PORT="$port" COCKPIT_DB="$DB_NAME" COCKPIT_USER="$DB_USER"; exec bash "$COMP/install-postgres.sh" ) \
    || die "PostgreSQL was not installed (the message above says why), so the application was not installed either. If the reason is that the port is in use, choose a free one and run this again: sudo AIREOS_PG_PORT=5433 $SETUP_DIR/setup.sh"
  DB_URL="postgres://$DB_USER:$pw@localhost:$port/$DB_NAME?sslmode=disable"
}

# ---- 3. the application ------------------------------------------------------------------------------------------------
# T-175 SEAM: operator provisioning happens in this one step. Today it is install.sh's own (a prompt, or
# AIREOS_BOOTSTRAP_OPERATOR_* env); ADR-0078's setup code replaces it HERE without touching the database steps above.
step_install_app_and_operator() {
  log "install the application (install.sh)"
  ( export AIREOS_DB_URL="$DB_URL"; exec bash "$SETUP_DIR/install.sh" )
}

step_preflight
step_database
step_install_app_and_operator

# ---- 4. the one address to open: what Caddy actually serves (install.sh wrote it to aireos.env) ------------------------
read -r tok _ <<<"$(aireos_persisted_value AIREOS_SITE_ADDRESS "$ENV_FILE")"
scheme=https; tok="${tok:-localhost}"; [[ "$tok" != http://* ]] || scheme=http
tok="${tok#http://}"; tok="${tok#https://}"; tok="${tok%%/*}"; [[ "$tok" != :* ]] || tok="localhost$tok"   # a bare ":port" binds every interface
addr="$scheme://$tok/"
printf '\n\033[1;36m[setup]\033[0m Setup is complete. Open %s in your browser to continue.\n' "$addr"
