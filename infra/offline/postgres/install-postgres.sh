#!/usr/bin/env bash
# install-postgres.sh — install postgres 16 + pgcrypto and the cockpit database
# on the TARGET host, fully offline, from this companion's local package repo
# (§OI-c, ADR-0052). Run as root from the unpacked companion directory.
#
# Pure air-gap: the companion's repo/ carries the complete dependency closure, so
# no internet (and no host package mirror) is needed.
#
# Env (prompted if unset):
#   COCKPIT_DB    database name (default: cockpit)
#   COCKPIT_USER  role name     (default: cockpit)
#   COCKPIT_PW    role password (prompted, no-echo, if unset)
#
# Idempotent: re-running re-uses the existing role/db and re-asserts pgcrypto.
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
KIND="$(cat "$DIR/PKG_KIND")"
COCKPIT_DB="${COCKPIT_DB:-cockpit}"
COCKPIT_USER="${COCKPIT_USER:-cockpit}"
COCKPIT_PW="${COCKPIT_PW:-}"

log() { printf '\033[1;34m[postgres]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[postgres] ERROR:\033[0m %s\n' "$*" >&2; exit 1; }

[ "$(id -u)" = "0" ] || die "must run as root"
[ -n "$COCKPIT_PW" ] || { read -r -s -p "Password for the cockpit DB role: " COCKPIT_PW; echo; }
[ -n "$COCKPIT_PW" ] || die "a DB role password is required"

# --- 1. install the packages from the local repo (offline) ------------------
if [ "$KIND" = "deb" ]; then
  export DEBIAN_FRONTEND=noninteractive
  echo "deb [trusted=yes] file:$DIR/repo ./" > /etc/apt/sources.list.d/aireos-postgres.list
  # APT::Sandbox::User=root: apt's unprivileged _apt sandbox user cannot read a
  # file:// repo unpacked under a root-only directory (e.g. /root) — "Permission
  # denied" on the Packages index. Reading as root fixes it wherever the operator
  # unpacks the companion. Any unreachable online sources fail harmlessly; the
  # local repo satisfies the install (the closure is complete, ADR-0052).
  apt-get -o APT::Sandbox::User=root update -qq || true
  apt-get -o APT::Sandbox::User=root install -y postgresql-16 postgresql-contrib
  rm -f /etc/apt/sources.list.d/aireos-postgres.list
  log "start the postgres cluster"
  service postgresql start 2>/dev/null || pg_ctlcluster 16 main start 2>/dev/null || true
else
  cat > /etc/yum.repos.d/aireos-postgres.repo <<EOF
[aireos-postgres]
name=AI ReOS offline postgres
baseurl=file://$DIR/repo
enabled=1
gpgcheck=0
EOF
  dnf install -y --disablerepo='*' --enablerepo='aireos-postgres' postgresql16-server postgresql16-contrib
  rm -f /etc/yum.repos.d/aireos-postgres.repo
  PGDATA=/var/lib/pgsql/16/data; PGBIN=/usr/pgsql-16/bin
  if [ ! -f "$PGDATA/PG_VERSION" ]; then
    # initdb directly: postgresql-16-setup reads PGDATA from the systemd unit
    # (unavailable in minimal/container installs). --auth-host=scram-sha-256 so
    # the cockpit role can connect over loopback TCP — RHEL's default host auth
    # is `ident`, which would reject the password connection install.sh uses.
    su - postgres -c "$PGBIN/initdb -D $PGDATA --auth-host=scram-sha-256"
  fi
  log "start the postgres service"
  systemctl enable --now postgresql-16 2>/dev/null \
    || su - postgres -c "$PGBIN/pg_ctl -D $PGDATA -l /tmp/pg-aireos.log -w start"
fi

# --- 2. create the cockpit role + database, enable pgcrypto (idempotent) ----
psql_postgres() { su - postgres -c "psql -v ON_ERROR_STOP=1 $*"; }
log "provision role + database $COCKPIT_DB"
su - postgres -c "psql -tAc \"SELECT 1 FROM pg_roles WHERE rolname='$COCKPIT_USER'\"" | grep -q 1 \
  || su - postgres -c "psql -v ON_ERROR_STOP=1 -c \"CREATE ROLE \\\"$COCKPIT_USER\\\" LOGIN PASSWORD '$COCKPIT_PW'\""
su - postgres -c "psql -tAc \"SELECT 1 FROM pg_database WHERE datname='$COCKPIT_DB'\"" | grep -q 1 \
  || su - postgres -c "createdb -O \"$COCKPIT_USER\" \"$COCKPIT_DB\""
# pgcrypto is a trusted extension (PG13+): the DB owner can create it, no superuser needed at runtime.
su - postgres -c "psql -v ON_ERROR_STOP=1 -d \"$COCKPIT_DB\" -c 'CREATE EXTENSION IF NOT EXISTS pgcrypto'"

log "DONE — postgres 16 + pgcrypto ready."
log "Connection for install.sh:  postgres://$COCKPIT_USER:<password>@localhost:5432/$COCKPIT_DB?sslmode=disable"
