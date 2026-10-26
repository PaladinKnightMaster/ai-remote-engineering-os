#!/usr/bin/env bash
# pgsql-tree-smoke.sh — runs INSIDE a FRESH container of the pinned base image
# with --network none and NO package installs (build-pgsql-trees.sh starts it,
# tree mounted read-only at /stage). Proves the tree runs on the pristine base:
# every ELF resolves, bundled libs come from the tree, initdb + start + pgcrypto
# + full-text search work as a non-root user, and the package database is untouched.
# Prints exactly one `PGSQL-TREE-SMOKE PASS` line on success; any failure exits non-zero.
# Env: OSTAG PGSQL_VERSION
set -euo pipefail

PREFIX=/opt/aireos/pgsql
: "${OSTAG:?}" "${PGSQL_VERSION:?}"
die() { echo "[pgsql-smoke:$OSTAG] FAIL: $*" >&2; exit 1; }
say() { echo "[pgsql-smoke:$OSTAG] $*"; }

pkgs() {
  if command -v dpkg-query >/dev/null 2>&1; then dpkg-query -W -f='${binary:Package} ${Version}\n' | sort
  else rpm -qa --qf '%{NAME}-%{VERSION}-%{RELEASE}.%{ARCH}\n' | sort; fi
}

[ -z "${LD_LIBRARY_PATH:-}" ] || die "LD_LIBRARY_PATH is set; the tree must not depend on it"
pkgs >/tmp/pkgs.before
mkdir -p "$(dirname "$PREFIX")"; cp -a /stage "$PREFIX"
SH="$PREFIX/share/aireos"
for f in HOST_LIBS BUNDLED_LIBS COMPONENTS.json; do [ -e "$SH/$f" ] || die "missing manifest $f"; done
[ -s "$SH/HOST_LIBS" ] && [ -s "$SH/COMPONENTS.json" ] || die "HOST_LIBS / COMPONENTS.json is empty"   # BUNDLED_LIBS may be empty

# 1. every ELF resolves — no `not found` anywhere (names the missing library).
elfs=0; missing=""; lddall=/tmp/ldd.all; : >"$lddall"
while read -r f; do
  [ "$(od -An -tx1 -N4 "$f" | tr -d ' \n')" = 7f454c46 ] || continue
  elfs=$((elfs+1)); out="$(ldd "$f" 2>&1 || true)"
  # Canonicalise: the loader reports e.g. <prefix>/bin/../lib/x.so, which must still count as "inside the tree".
  while read -r n a p _; do
    if [ "$a" = "=>" ] && [ "$p" != not ]; then echo "$n => $(readlink -f "$p")" >>"$lddall"; fi
  done <<<"$out"
  nf="$(grep 'not found' <<<"$out" || true)"
  [ -z "$nf" ] || missing="$missing"$'\n'"  $f: $(tr -s ' \t' ' ' <<<"$nf" | tr '\n' ';')"
done < <(find "$PREFIX" -type f)
[ -z "$missing" ] || die "unresolved shared libraries (ldd: not found):$missing"
say "ldd: $elfs ELF files, zero 'not found'"

# 2. bundled libs are present AND are the ones loaded (not a same-named host copy).
nb=0
while read -r s; do
  [ -n "$s" ] || continue; nb=$((nb+1))
  [ -e "$PREFIX/lib/$s" ] || die "bundled library missing from the tree: $s (listed in BUNDLED_LIBS)"
  grep -E "^$s => " "$lddall" | grep -vq "=> $PREFIX/lib/" && die "$s resolved outside $PREFIX/lib"
  grep -qE "^$s => $PREFIX/lib/" "$lddall" || die "bundled $s is never loaded from $PREFIX/lib"
done <"$SH/BUNDLED_LIBS"
# 3. every expected host library is really on this base (the fail-fast check T-172 will own).
nh=0
while read -r s; do
  [ -n "$s" ] || continue; nh=$((nh+1))
  ldconfig -p | awk -v s="$s" '$1==s{f=1} END{exit !f}' || die "expected host library absent on this base: $s"
done <"$SH/HOST_LIBS"

# 4. initdb as a non-root user, start on a unix socket, real queries, clean stop.
"$PREFIX/bin/postgres" --version | grep -qF "(PostgreSQL) $PGSQL_VERSION" || die "wrong server version: $("$PREFIX/bin/postgres" --version)"
d="$(mktemp -d /tmp/pgsmoke.XXXXXX)"; chown 65534:65534 "$d"; chmod 700 "$d"
as_pg() { env -i HOME="$d" PATH="$PREFIX/bin:/usr/bin:/bin" setpriv --reuid 65534 --regid 65534 --clear-groups "$@"; }
sql() { as_pg psql -h "$d" -p 55432 -U postgres -X -At -v ON_ERROR_STOP=1 -c "$1"; }
as_pg initdb -D "$d/data" -E UTF8 --locale=C -A trust -U postgres >"$d/initdb.log" 2>&1 || { cat "$d/initdb.log" >&2; die "initdb failed"; }
as_pg pg_ctl -D "$d/data" -w -l "$d/server.log" -o "-c listen_addresses='' -c unix_socket_directories=$d -c port=55432" start >/dev/null \
  || { cat "$d/server.log" >&2; die "pg_ctl start failed"; }
sql "CREATE EXTENSION pgcrypto" >/dev/null
got="$(sql "SELECT encode(digest('abc','sha256'),'hex')")"
[ "$got" = ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad ] || die "pgcrypto digest wrong: $got"
got="$(sql "SELECT to_tsvector('english','running')::text")"
[ "$got" = "'run':1" ] || die "english full-text search wrong: $got"
got="$(sql "SELECT gen_random_uuid()")"
[ "${#got}" = 36 ] || die "gen_random_uuid returned '$got'"
got="$(sql "SELECT pgp_sym_decrypt(pgp_sym_encrypt('x','k'),'k')")"
[ "$got" = x ] || die "pgp_sym round trip wrong: $got"
as_pg pg_ctl -D "$d/data" -m fast -w stop >/dev/null || die "pg_ctl stop failed"
[ ! -e "$d/data/postmaster.pid" ] || die "postmaster.pid left behind after stop"

# 5. the package database is byte-identical.
pkgs >/tmp/pkgs.after
# coreutils only (a pristine Rocky base has neither cmp nor diff, and the smoke installs nothing).
[ "$(sha256sum </tmp/pkgs.before)" = "$(sha256sum </tmp/pkgs.after)" ] || { comm -3 /tmp/pkgs.before /tmp/pkgs.after >&2; die "package list changed"; }
echo "PGSQL-TREE-SMOKE PASS ostag=$OSTAG pg=$PGSQL_VERSION elfs=$elfs bundled=$nb host_libs=$nh pkgs=$(wc -l </tmp/pkgs.before) unchanged=yes"
