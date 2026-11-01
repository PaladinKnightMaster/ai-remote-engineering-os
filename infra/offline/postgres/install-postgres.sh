#!/usr/bin/env bash
# install-postgres.sh — install the self-contained PostgreSQL 16 tree (ADR-0077 D4-D6; T-172a)
# and the product's role / database / pgcrypto. No package manager is involved. Run as root,
# from an unpacked companion layout (any cwd):
#   aireos-postgres-<ostag>/{pgsql/ (a T-171 tree), install-postgres.sh, aireos-postgres.service}
#
# PREFLIGHT FIRST (D6, INS-4, INS-11): every check runs before the host changes. A failure
# prints plain sentences naming what is wrong and what to do, then exits 1 with nothing changed.
#
# Env (prompted if unset, as the v0.16.0 package-based script did):
#   COCKPIT_DB    database name (default: cockpit)      COCKPIT_USER  role name (default: cockpit)
#   COCKPIT_PW    role password (prompted, no echo, if unset and stdin is a terminal; else an error)
#   AIREOS_PG_PORT  TCP port (default: the existing install's port, else 5432). Taken -> refuse (D5).
#
# Idempotent: an existing data dir of the same major is never re-initdb'd; a different tree is
# swapped in (stage + rename, rollback on a failed start); provisioning is re-asserted.
# An existing role keeps its password (as the v0.16.0 script did): only a missing role is created.
set -euo pipefail
export LC_ALL=C PATH=/usr/sbin:/usr/bin:/sbin:/bin

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC="$DIR/pgsql"; SH="$SRC/share/aireos"
PREFIX=/opt/aireos/pgsql; DATA=/var/lib/aireos/pgsql; PGOS=aireos-pg; UNIT=aireos-postgres
UNIT_FILE="/etc/systemd/system/$UNIT.service"; SOCKDIR=/run/aireos-postgres; CONF="$DATA/aireos-managed.conf"
# Locale: C.UTF-8 on every OS (verified present on all three floor images; product SQL does no
# case-folding or collation-sensitive work, and the tree is built --without-icu, so this is libc's).
LOCALE=C.UTF-8
COCKPIT_DB="${COCKPIT_DB:-cockpit}"; COCKPIT_USER="${COCKPIT_USER:-cockpit}"; COCKPIT_PW="${COCKPIT_PW:-}"

log() { printf '\033[1;34m[postgres]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[postgres] ERROR:\033[0m %s\n' "$*" >&2; exit 1; }
sha_of() { sha256sum "$1" 2>/dev/null | cut -d' ' -f1 || true; }

# ---------------------------------------------------------------- preflight (changes NOTHING)
problems=()
bad() { problems+=("$*"); }
osr() { sed -n "s/^$1=//p" /etc/os-release 2>/dev/null | head -1 | tr -d '"' || true; }
port_listening() {   # any TCP listener (v4 or v6) on decimal port $1; /proc works where ss is absent
  local f h; h=":$(printf '%04X' "$1")"
  for f in /proc/net/tcp /proc/net/tcp6; do
    [ -r "$f" ] && awk -v h="$h" '$4=="0A" && substr($2,length($2)-4)==h {f=1} END{exit !f}' "$f" && return 0
  done
  return 1
}
tree_digest() {   # content digest of a tree: file hashes + symlink targets (bash only: a pristine Rocky has no find)
  ( cd "$1" && shopt -s globstar dotglob nullglob
    files=(); links=()
    for p in **; do if [ -L "$p" ]; then links+=("$p"); elif [ -f "$p" ]; then files+=("$p"); fi; done
    { [ "${#files[@]}" -eq 0 ] || sha256sum -- "${files[@]}"; for p in "${links[@]}"; do echo "L $p $(readlink "$p")"; done; } | sha256sum | cut -d' ' -f1 )
}

[ "$(id -u)" = 0 ] || bad "This installer must run as root; re-run it with sudo."
[[ "$COCKPIT_DB" =~ ^[a-z_][a-z0-9_]{0,62}$ ]] || bad "COCKPIT_DB '$COCKPIT_DB' is not a plain identifier (lowercase letters, digits, underscore; at most 63 characters); choose another name."
[[ "$COCKPIT_USER" =~ ^[a-z_][a-z0-9_]{0,62}$ ]] || bad "COCKPIT_USER '$COCKPIT_USER' is not a plain identifier (lowercase letters, digits, underscore; at most 63 characters); choose another name."
[ "$(uname -m)" = x86_64 ] || bad "This PostgreSQL tree is built for amd64 (x86_64) but this host is $(uname -m); there is no companion for this architecture."
{ [ "$(cat /proc/1/comm 2>/dev/null)" = systemd ] && [ -d /run/systemd/system ] && command -v systemctl >/dev/null; } \
  || bad "systemd is not running as PID 1 on this host, and this PostgreSQL runs as a systemd service; run the installer on a systemd host (a container without systemd cannot run it)."

# $DATA belongs to the service user: root never follows a symlink planted there (see the act section).
installed_port=""
if [ -L "$CONF" ] || [ -L "$DATA/postgresql.conf" ] || [ -L "$DATA/PG_VERSION" ]; then
  bad "A file in $DATA (aireos-managed.conf, postgresql.conf or PG_VERSION) is a symbolic link, which this installer never follows; something other than this installer changed it, so check the host, replace the link with the real file, and re-run."
elif [ -f "$CONF" ]; then
  installed_port="$(sed -n 's/^port = \([0-9]*\)$/\1/p' "$CONF")"
fi
PORT="${AIREOS_PG_PORT:-${installed_port:-5432}}"
if ! { [[ "$PORT" =~ ^[0-9]+$ ]] && [ "$PORT" -ge 1024 ] && [ "$PORT" -le 65535 ]; }; then
  bad "AIREOS_PG_PORT '$PORT' is not a TCP port between 1024 and 65535; set it to a free port, for example AIREOS_PG_PORT=5433."
elif port_listening "$PORT" && ! { [ "$installed_port" = "$PORT" ] && systemctl is-active --quiet "$UNIT" 2>/dev/null; }; then
  bad "TCP port $PORT is already in use, probably by an existing PostgreSQL (for example the one a v0.16.0 install set up); that install keeps working untouched and nothing here was changed. To install this PostgreSQL beside it, choose a free port and re-run with AIREOS_PG_PORT=<port>, for example AIREOS_PG_PORT=5433."
fi

if [ ! -f "$SH/COMPONENTS.json" ] || [ ! -f "$SH/HOST_LIBS" ] || [ ! -f "$SH/HOST_LIB_VERSIONS" ] || [ ! -x "$SRC/bin/postgres" ]; then
  bad "The PostgreSQL tree is missing or incomplete at $SRC (expected a 'pgsql' directory beside this script); unpack the companion archive again."
else
  tree_os="$(sed -n 's/^ *"ostag": *"\([^"]*\)".*/\1/p' "$SH/COMPONENTS.json" | head -1)"
  tree_major="$(sed -n 's/^ *"version": *"\([0-9]*\)\..*/\1/p' "$SH/COMPONENTS.json" | head -1)"
  hid="$(osr ID)"; hver="$(osr VERSION_ID)"; hlike="$(osr ID_LIKE)"
  case "$hid $hlike" in
    *rocky*|*rhel*|*almalinux*|*centos*) host_os="rocky${hver%%.*}" ;;
    ubuntu*) host_os="ubuntu${hver//./}" ;;
    *) host_os="${hid}${hver}" ;;
  esac
  [ "$tree_os" = "$host_os" ] || bad "This PostgreSQL tree was built for '$tree_os' but this host is '$host_os' ($(osr PRETTY_NAME)); use the aireos-postgres companion built for this OS."
  ldc="$(ldconfig -p)"
  host_lib() { awk -v s="$1" '$1==s && /x86-64/ {print $NF; exit}' <<<"$ldc"; }
  missing=" "
  while read -r s; do   # INS-4: every host library the tree links against must be resolvable
    [ -n "$s" ] || continue
    [ -n "$(host_lib "$s")" ] || { missing="$missing$s "; bad "The shared library $s, which this PostgreSQL needs, is not installed on this host; install the host package that provides it and re-run (this installer never uses the package manager)."; }
  done <"$SH/HOST_LIBS"
  while read -r s v; do   # INS-11: ...and must define the symbol version the tree was linked against (a NUL-delimited .dynstr lookup: no binutils needed)
    [ -n "$s" ] && [[ "$missing" != *" $s "* ]] || continue
    p="$(host_lib "$s")"
    grep -qaP "\x00\Q$v\E\x00" "$(readlink -f "$p")" \
      || bad "The host library $s is older than this PostgreSQL build needs: it does not provide $v. Update the host's package for $s, or use the companion built for this OS release (this host is older than the tree's build floor)."
  done <"$SH/HOST_LIB_VERSIONS"
fi
LC_ALL=C locale -a 2>/dev/null | grep -qix 'c\.utf-\?8' || bad "The $LOCALE locale is not available on this host, and the database is created with it; install your OS's C.UTF-8 locale (glibc locale data) and re-run."

if [ -f "$DATA/PG_VERSION" ]; then
  dv="$(head -c 16 "$DATA/PG_VERSION" | tr -cd '0-9')"   # digits only: never echo arbitrary file content
  [ "$dv" = "${tree_major:-}" ] || bad "The data directory $DATA holds PostgreSQL $dv data but this tree is PostgreSQL ${tree_major:-?}; this installer never upgrades across major versions, so move that directory aside or use a matching companion."
elif [ -d "$DATA" ] && [ -n "$(ls -A "$DATA")" ]; then
  bad "$DATA exists and is not empty but is not a PostgreSQL data directory; move it aside and re-run."
fi
[ ! -e "$PREFIX" ] || [ -f "$PREFIX/share/aireos/COMPONENTS.json" ] || bad "$PREFIX exists but is not an AI ReOS PostgreSQL tree; move it aside and re-run."

if [ "${#problems[@]}" -gt 0 ]; then
  for m in "${problems[@]}"; do printf '\033[1;31m[postgres] ERROR:\033[0m %s\n' "$m" >&2; done
  die "Preflight failed; nothing on this host was changed."
fi

# Input that can still fail is resolved before the first change.
if [ -z "$COCKPIT_PW" ]; then
  [ -t 0 ] || die "COCKPIT_PW is not set and there is no terminal to ask on; set COCKPIT_PW and re-run. Nothing was changed."
  read -r -s -p "Password for the $COCKPIT_USER DB role: " COCKPIT_PW; echo
fi
[ -n "$COCKPIT_PW" ] || die "a DB role password is required. Nothing was changed."
export -n COCKPIT_PW   # never inherited by a child process

# ---------------------------------------------------------------- act
changed=0; STAGE="/opt/aireos/.pgsql.new.$$"; OLD="/opt/aireos/.pgsql.old.$$"
trap 'rm -rf "$STAGE"' EXIT
[ -d /opt/aireos ] || mkdir -p /opt/aireos; [ -d /var/lib/aireos ] || mkdir -p /var/lib/aireos
if ! id "$PGOS" >/dev/null 2>&1; then
  log "create system user $PGOS"
  useradd --system --user-group --home-dir "$DATA" --no-create-home --shell "$(command -v nologin || echo /usr/sbin/nologin)" "$PGOS"
fi
PGUID="$(id -u "$PGOS")"; PGGID="$(id -g "$PGOS")"
as_pg() { setpriv --reuid "$PGUID" --regid "$PGGID" --clear-groups env -i PATH="$PREFIX/bin:/usr/bin:/bin" HOME="$DATA" LC_ALL=C "$@"; }

new_digest="$(tree_digest "$SRC")"
if [ -d "$PREFIX" ] && [ "$(tree_digest "$PREFIX")" = "$new_digest" ]; then
  log "PostgreSQL tree at $PREFIX is already the shipped one"
else
  log "install the PostgreSQL tree to $PREFIX (root-owned, read-only to the service)"
  rm -rf "$STAGE"; cp -a "$SRC" "$STAGE"; chown -R 0:0 "$STAGE"; chmod -R go-w "$STAGE"
  if [ -d "$PREFIX" ]; then
    systemctl stop "$UNIT" 2>/dev/null || true
    mv "$PREFIX" "$OLD"
  fi
  mv "$STAGE" "$PREFIX"; changed=1
fi

if [ "$(sha_of "$DIR/$UNIT.service")" != "$(sha_of "$UNIT_FILE")" ]; then
  install -m 0644 "$DIR/$UNIT.service" "$UNIT_FILE"; systemctl daemon-reload; changed=1
fi

if [ -f "$DATA/PG_VERSION" ]; then
  log "data directory $DATA exists (PostgreSQL $dv) — not re-initialising"
else
  log "initdb $DATA (UTF8, $LOCALE; scram-sha-256 over TCP, peer on the socket)"
  install -d -m 0700 -o "$PGUID" -g "$PGGID" "$DATA"
  as_pg "$PREFIX/bin/initdb" -D "$DATA" -E UTF8 --locale="$LOCALE" -U "$PGOS" --auth-host=scram-sha-256 --auth-local=peer >/dev/null
fi
# $DATA is owned by $PGOS, so root never reads, writes or chowns a path inside it: a planted symlink
# (e.g. aireos-managed.conf -> /etc/shadow) would otherwise turn a re-run into root writing through it.
# Every access below runs AS $PGOS, so it can touch nothing that user could not already touch.
as_pg sh -c 'grep -q aireos-managed.conf "$1/postgresql.conf" || echo "include_if_exists = '\''aireos-managed.conf'\''" >>"$1/postgresql.conf"' sh "$DATA"
want="# managed by install-postgres.sh; edits are overwritten on re-run
listen_addresses = 'localhost'
port = $PORT
unix_socket_directories = '$SOCKDIR'"
if [ "$(as_pg cat "$CONF" 2>/dev/null)" != "$want" ]; then
  printf '%s\n' "$want" | as_pg sh -c 'umask 077 && cat >"$1.tmp" && mv -f "$1.tmp" "$1"' sh "$CONF"; changed=1
fi

systemctl enable "$UNIT" >/dev/null 2>&1
if systemctl is-active --quiet "$UNIT"; then
  [ "$changed" = 0 ] || { log "restart $UNIT (configuration or tree changed)"; systemctl restart "$UNIT" || start_failed=1; }
else
  log "start $UNIT"; systemctl start "$UNIT" || start_failed=1
fi
if [ "${start_failed:-0}" = 1 ]; then
  journalctl -u "$UNIT" -n 15 --no-pager >&2 2>/dev/null || true
  if [ -d "$OLD" ]; then
    rm -rf "$PREFIX"; mv "$OLD" "$PREFIX"; systemctl start "$UNIT" || true
    die "the new PostgreSQL tree failed to start; the previous tree was restored."
  fi
  die "$UNIT failed to start; see the journal lines above (journalctl -u $UNIT)."
fi
rm -rf "$OLD"

# --- role + database + pgcrypto over the unit's unix socket as aireos-pg (peer auth), idempotent.
# The password reaches psql only on STDIN, hex-encoded and decoded by the server: never argv, never env.
psql_su() { as_pg "$PREFIX/bin/psql" -X -q -At -v ON_ERROR_STOP=1 -h "$SOCKDIR" -p "$PORT" -U "$PGOS" "$@"; }
log "provision role $COCKPIT_USER + database $COCKPIT_DB"
hex="$(printf '%s' "$COCKPIT_PW" | od -An -v -tx1 | tr -d ' \n')"
printf '%s\n' "SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', :'u', convert_from(decode('$hex','hex'),'UTF8'))
 WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'u') \gexec
SELECT format('CREATE DATABASE %I OWNER %I', :'d', :'u')
 WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = :'d') \gexec" \
  | psql_su -d postgres -v "u=$COCKPIT_USER" -v "d=$COCKPIT_DB"
unset hex
# pgcrypto is a trusted extension (PG13+); created by the superuser here, idempotent.
psql_su -d "$COCKPIT_DB" -c 'CREATE EXTENSION IF NOT EXISTS pgcrypto'

log "DONE — PostgreSQL ${tree_major} + pgcrypto ready under $UNIT (port $PORT)."
log "Connection for install.sh:  postgres://$COCKPIT_USER:<password>@localhost:$PORT/$COCKPIT_DB?sslmode=disable"
