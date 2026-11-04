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
# The operator (ADR-0078, T-175c): with AIREOS_BOOTSTRAP_OPERATOR_* set, or already in aireos.env from an earlier install, the
# operator is exactly that and the script ends with the address to open. Otherwise NO operator is made at the terminal: api
# starts in setup mode, and this script prints a one-time SETUP CODE plus the SHA-256 fingerprint of the cockpit's certificate
# authority (so the operator can check the certificate their browser shows before typing the code). The code is only ever
# printed to the terminal, never written to a file, a log or the command line; `sudo ./setup.sh --new-setup-code` issues a
# fresh one (and kills the old one) while no operator exists, and changes nothing else.
#
# The demo target (ADR-0079 D3/D7, T-176c): once the stack is healthy, setup offers to prepare the loopback demo so a governed
# action needs no further server command. It is OPT-IN and says what it creates before it asks: a local account `aireos-demo`
# (no password), one restricted SSH key for it (generated INSIDE connector-service; this host never sees the private half), and
# a pending record the cockpit's "Create loopback demo target" button later binds. AIREOS_LOOPBACK_DEMO=yes|no answers it
# unattended; unset and not a terminal means no. A host with no SSH server on 127.0.0.1:22 skips the demo in one sentence --
# the install itself is unaffected. `sudo ./setup.sh --remove-demo` undoes it (AIREOS_REMOVE_DEMO_ACCOUNT=yes also deletes the
# account, if setup created it). Only operator-run scripts touch authorized_keys or accounts; no service does (v0.12.0 split of trust).
#
# The password: 256 bits from /dev/urandom as hex, kept in /etc/aireos/db-password (root, 0600 from its first byte),
# NEVER printed and NEVER in argv — it reaches install-postgres.sh as COCKPIT_PW and install.sh as AIREOS_DB_URL, both
# environment. It is reused on every re-run: an existing role keeps its password, so a fresh one would write a URL the
# database rejects (T-172a).
set -euo pipefail
export LC_ALL=C

SETUP_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# The demo helpers (account, restricted authorized_keys entry, connector calls) live in ONE place, the sourceable
# setup-loopback-demo.sh. It is sourced BEFORE install.sh so install.sh's ETC/PREFIX/SVC_USER are the ones in force here.
[ -f "$SETUP_DIR/setup-loopback-demo.sh" ] || { echo "[setup] ERROR: $SETUP_DIR/setup-loopback-demo.sh is missing; unpack the aireos-offline tarball again." >&2; exit 1; }
# shellcheck source=setup-loopback-demo.sh
source "$SETUP_DIR/setup-loopback-demo.sh"
# install.sh is sourceable (no installer body runs): reuse its tested helpers rather than copy them.
# shellcheck source=install.sh
source "$SETUP_DIR/install.sh"
PW_FILE="$ETC/db-password"
DB_NAME="${COCKPIT_DB:-cockpit}"; DB_USER="${COCKPIT_USER:-cockpit}"
WORK=""; trap '[ -z "$WORK" ] || rm -rf "$WORK"' EXIT

log() { printf '\033[1;36m[setup]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[setup] ERROR:\033[0m %s\n' "$*" >&2; exit 1; }

NEW_CODE=0; REMOVE_DEMO=0
case "${1:-}" in
  "") ;;
  --new-setup-code) NEW_CODE=1 ;;
  --remove-demo) REMOVE_DEMO=1 ;;
  *) die "unknown argument '$1'. Usage: sudo $SETUP_DIR/setup.sh        (install),  sudo $SETUP_DIR/setup.sh --new-setup-code,  or  sudo $SETUP_DIR/setup.sh --remove-demo" ;;
esac
# A wrong answer is refused before anything changes (not discovered after the install).
[ -z "${AIREOS_LOOPBACK_DEMO:-}" ] || aireos_demo_decision "$AIREOS_LOOPBACK_DEMO" 0 "" >/dev/null || die "AIREOS_LOOPBACK_DEMO must be yes or no (it is '$AIREOS_LOOPBACK_DEMO'). Nothing was changed."

# Caddy's `tls internal` root: aireos-caddy.service sets XDG_DATA_HOME=$STATE/caddy, and Caddy keeps its PKI under caddy/pki.
CA_DIR="$STATE/caddy/caddy/pki/authorities/local"; CA_ROOT="$CA_DIR/root.crt"; CA_INT="$CA_DIR/intermediate.crt"

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
# The operator is install.sh's business (aireos_resolve_operator_mode): env, else the one already persisted, else -- because
# of the signal below -- none, and the browser makes it (ADR-0078). This script only has to notice which one happened.
step_install_app_and_operator() {
  log "install the application (install.sh)"
  ( export AIREOS_DB_URL="$DB_URL" AIREOS_OPERATOR_SETUP=browser; exec bash "$SETUP_DIR/install.sh" )
}

# ---- 4. the one address to open: what Caddy actually serves (install.sh wrote it to aireos.env) ------------------------
open_address() {
  local tok scheme=https
  read -r tok _ <<<"$(aireos_persisted_value AIREOS_SITE_ADDRESS "$ENV_FILE")"
  tok="${tok:-localhost}"; [[ "$tok" != http://* ]] || scheme=http
  tok="${tok#http://}"; tok="${tok#https://}"; tok="${tok%%/*}"; [[ "$tok" != :* ]] || tok="localhost$tok"   # a bare ":port" binds every interface
  printf '%s://%s/' "$scheme" "$tok"
}

# ---- 5. the setup code (ADR-0078 D1/D6/D7) -----------------------------------------------------------------------------
# What a browser can show (T-175e): Caddy serves the leaf and the INTERMEDIATE ("Caddy Local Authority - ECC Intermediate"), never the
# root -- so the fingerprint to COMPARE is the intermediate's. The root is printed second, for an operator who imports the CA to remove
# the warning. The intermediate rotates (7 days), so every issue (setup.sh and --new-setup-code) re-reads both files. Coreutils only.
cert_fp() {   # SHA-256 of the first PEM certificate in file $1, as the colon-separated upper-case hex a browser shows
  sed -n '/BEGIN CERTIFICATE/,/END CERTIFICATE/{/-----/!p;/END CERTIFICATE/q}' "$1" | base64 -d | sha256sum | cut -d' ' -f1 | tr a-f A-F | sed 's/../&:/g; s/:$//'
}
# issue_setup_code <database-url> -> SETUP_CODE, SETUP_CA_INT, SETUP_CA_ROOT. Returns 2 when an operator exists (no code is issued
# then). The URL travels as environment inside a subshell (never argv); the code is read from the command's stdout into this shell's
# memory and printed to the terminal -- it is never written anywhere.
issue_setup_code() {
  local out rc=0 fp
  [ -f "$CA_ROOT" ] && [ -f "$CA_INT" ] || die "Caddy's certificate authority files are not there yet ($CA_DIR), so no setup code was issued. Check that aireos-caddy is running (systemctl status aireos-caddy) and run  sudo $SETUP_DIR/setup.sh --new-setup-code"
  out="$( export API_DATABASE_URL="$1" DATABASE_URL="$1"; exec "$PREFIX/bin/api" setup-code issue --ca-root "$CA_ROOT" 2>&1 )" || rc=$?
  if [ "$rc" != 0 ]; then
    if grep -qF 'setup is complete' <<<"$out"; then return 2; fi
    die "a setup code could not be issued: $(tail -n1 <<<"$out")"
  fi
  SETUP_CODE="$(grep -Em1 '^[0-9A-Z]{5}(-[0-9A-Z]{5}){3}$' <<<"$out" || true)"
  fp="$(sed -n 's/^ca-sha256: \([0-9a-f]\{64\}\)$/\1/p' <<<"$out")"
  [ -n "$SETUP_CODE" ] && [ -n "$fp" ] || die "api printed a setup code or fingerprint in a form this script does not recognise; run  sudo $SETUP_DIR/setup.sh --new-setup-code  and report this if it happens again"
  SETUP_CA_ROOT="$(sed 's/../&:/g; s/:$//' <<<"$fp" | tr a-f A-F)"
  SETUP_CA_INT="$(cert_fp "$CA_INT")"
  [[ "$SETUP_CA_INT" =~ ^([0-9A-F]{2}:){31}[0-9A-F]{2}$ ]] || die "could not compute the fingerprint of $CA_INT; run  sudo $SETUP_DIR/setup.sh --new-setup-code  once Caddy is healthy"
}

print_setup_block() {   # $1=address to open
  printf '\n  Setup code:   \033[1m%s\033[0m      (works once, for 60 minutes)\n\n' "$SETUP_CODE"
  printf '  Before you type the code, check you are talking to this server. Your browser will warn that the certificate is not trusted:\n'
  printf '  open its details, select the certificate named "Caddy Local Authority - ECC Intermediate", and check that its SHA-256 fingerprint is\n      %s\n' "$SETUP_CA_INT"
  printf '  (If you import this server'"'"'s certificate authority to remove the warning, its root certificate'"'"'s SHA-256 fingerprint is\n      %s)\n\n' "$SETUP_CA_ROOT"
  printf '  Missed it, or it expired?   sudo %s/setup.sh --new-setup-code   (the old code then stops working)\n' "$SETUP_DIR"
  printf '\n\033[1;36m[setup]\033[0m Open %s in your browser and enter the setup code to create the operator account.\n' "$1"
}

# ---- 6. the demo target (ADR-0079 D3) ----------------------------------------------------------------------------------
DEMO_READY=0
step_demo() {   # never fails the install: every problem is one plain sentence and the demo is skipped
  local answer="" tty=0 decision rc=0
  [ -t 0 ] && tty=1
  if aireos_loopback_demo_present "$DEMO_ACCOUNT_DEFAULT"; then DEMO_READY=1; log "The demo target is already set up; leaving it as it is."; return 0; fi
  if [ -z "${AIREOS_LOOPBACK_DEMO:-}" ] && [ "$tty" = 1 ]; then
    aireos_loopback_ask_demo "$SETUP_DIR"; answer="$DEMO_ANSWER"
  fi
  decision="$(aireos_demo_decision "${AIREOS_LOOPBACK_DEMO:-}" "$tty" "$answer")" || return 0
  [ "$decision" = yes ] || { log "Skipping the demo target. To add it later, run setup.sh again with AIREOS_LOOPBACK_DEMO=yes."; return 0; }
  aireos_loopback_prepare_demo || rc=$?
  if [ "$rc" = 0 ] || [ "$rc" = 10 ]; then DEMO_READY=1; fi
  return 0
}
demo_pointer() { [ "$DEMO_READY" = 1 ] && printf '
[1;36m[setup][0m In the cockpit, click [1mCreate loopback demo target[0m.
' || true; }

if [ "$REMOVE_DEMO" = 1 ]; then   # ADR-0079 D7: the operator-run undo; nothing else on the host changes
  [ "$(id -u)" = 0 ] || die "This must run as root: use  sudo $SETUP_DIR/setup.sh --remove-demo"
  aireos_loopback_remove_demo
  rm_acct="${AIREOS_REMOVE_DEMO_ACCOUNT:-}"
  case "$rm_acct" in yes|no) ;; "") rm_acct=no
    if [ -t 0 ] && id "$DEMO_ACCOUNT_DEFAULT" >/dev/null 2>&1; then
      read -r -p "Also delete the local account \"$DEMO_ACCOUNT_DEFAULT\" and its home directory? [y/N] " rm_acct || rm_acct=n
      case "${rm_acct,,}" in y|yes) rm_acct=yes ;; *) rm_acct=no ;; esac
    fi ;;
    *) die "AIREOS_REMOVE_DEMO_ACCOUNT must be yes or no (it is '$rm_acct')" ;;
  esac
  [ "$rm_acct" != yes ] || aireos_loopback_remove_account
  log "If you already clicked 'Create loopback demo target': the demo resource and its stored key are still there, and the key no longer works (its authorized_keys entry is gone)."
  log "The cockpit's 'Remove the demo target' deletes the resource but NOT the stored key (they live in separate services). To delete the key too:  sudo $SETUP_DIR/setup-loopback-demo.sh --undo --resource-id <the resource id>"
  exit 0
fi

if [ "$NEW_CODE" = 1 ]; then
  # Re-issue only: no preflight of the bundle or the companion, nothing installed, nothing but a fresh code changes.
  [ "$(id -u)" = 0 ] || die "This must run as root: use  sudo $SETUP_DIR/setup.sh --new-setup-code"
  { [ -x "$PREFIX/bin/api" ] && [ -f "$ENV_FILE" ] && [ -n "$(aireos_persisted_value DATABASE_URL "$ENV_FILE")" ]; } \
    || die "AI ReOS is not installed on this host, so there is no setup to continue. Run  sudo $SETUP_DIR/setup.sh  to install it. Nothing was changed."
  [ -z "$(aireos_persisted_value AIREOS_BOOTSTRAP_OPERATOR_USER "$ENV_FILE")" ] \
    || die "Setup is already complete: this install has an operator account, so no setup code can be issued. Nothing was changed."
  rc=0; issue_setup_code "$(aireos_persisted_value DATABASE_URL "$ENV_FILE")" || rc=$?
  [ "$rc" != 2 ] || die "Setup is already complete: an operator account exists, so no setup code can be issued. Nothing was changed."
  print_setup_block "$(open_address)"
  exit 0
fi

step_preflight
step_database
step_install_app_and_operator
step_demo

addr="$(open_address)"
rc=0
if [ -z "$(aireos_persisted_value AIREOS_BOOTSTRAP_OPERATOR_USER "$ENV_FILE")" ]; then issue_setup_code "$DB_URL" || rc=$?; else rc=2; fi   # no operator line: the browser path
if [ "$rc" = 0 ]; then
  print_setup_block "$addr"
else   # the operator came from the environment, from an earlier install, or from an earlier browser setup: nothing to enter
  printf '\n\033[1;36m[setup]\033[0m Setup is complete. Open %s in your browser to continue.\n' "$addr"
fi
demo_pointer
