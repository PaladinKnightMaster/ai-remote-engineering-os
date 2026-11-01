#!/usr/bin/env bash
# pgsql-install-test.sh — prove install-postgres.sh on a REAL systemd host per OS (ADR-0077 D4-D6,
# INS-2/3/4/8/11; T-172a). Host = the OS's FLOOR image (pgsql-build.lock) + only systemd, built as a
# local image; the companion layout is assembled from the staging tree and mounted read-only; the
# container runs --network none. Needs Docker, `task pgsql-trees`, and network for the image builds.
#
# Usage: bash infra/offline/postgres/pgsql-install-test.sh [ostag...]   (default: ubuntu2404 ubuntu2604 rocky9)
#   per OS : (a) install + login + pgcrypto  (b) restart + container restart  (c) idempotent re-run + tree-swap upgrade
#            (d) package list unchanged (INS-2)  (e) password never in /proc/*/cmdline
#   negatives, each OBSERVED failing with the host unchanged: N1 newest-built rocky9 tree on the 9.0 floor (INS-11),
#            N2 absent host soname (INS-4), N3 rocky9 tree on ubuntu2404, N4 distro PostgreSQL on 5432 then
#            AIREOS_PG_PORT=5433 (INS-3/8; ubuntu2404 only), N5 data dir of another major version.
set -euo pipefail
export MSYS_NO_PATHCONV=1   # Windows/git-bash: don't mangle -v / cgroup paths

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$HERE/../../.." && pwd)"
LOCK="$HERE/pgsql-build.lock"; TREES="$REPO_ROOT/dist/offline"; WORK="$REPO_ROOT/dist/pgsql-work"
PW="pgit-$RANDOM$RANDOM-Zq9"   # a throwaway test credential
log() { echo "[pgsql-install-test] $*"; }
die() { echo "[pgsql-install-test] FAIL: $*" >&2; exit 1; }
lock_get() { local v; v="$(sed -n "s/^$1=//p" "$LOCK" | head -1)"; [ -n "$v" ] || die "$1 missing from $LOCK"; printf '%s' "$v"; }
OSES=("$@"); [ "${#OSES[@]}" -gt 0 ] || OSES=(ubuntu2404 ubuntu2604 rocky9)
CONTAINERS=(); VOLUMES=()
cleanup() { [ "${KEEP:-0}" = 1 ] || { docker rm -f "${CONTAINERS[@]}" >/dev/null 2>&1 || true; docker volume rm -f "${VOLUMES[@]}" >/dev/null 2>&1 || true; }; }
trap cleanup EXIT

# In-container snippets (quoted heredocs: nothing here is expanded on the host).
PKGS="$(cat <<'EOF'
if command -v dpkg-query >/dev/null; then dpkg-query -W -f='${binary:Package} ${Version}\n'; else rpm -qa --qf '%{NAME}-%{VERSION}-%{RELEASE}.%{ARCH}\n'; fi | sort | sha256sum
EOF
)"
SNAP="$(cat <<'EOF'
shopt -s globstar dotglob nullglob; printf '%s\n' /etc/** /opt/** /var/lib/** /usr/local/** /usr/lib/systemd/system/** /root/** | grep -v '^/var/lib/postgresql' | sort
EOF
)"
XB() { docker exec "$1" bash -c "$2"; }
ok() { echo "  ok [$OS] $*"; }
eq() { [ "$2" = "$3" ] || die "[$OS] $1: expected '$2', got '$3'"; ok "$1: $3"; }
has() { grep -qF -- "$3" <<<"$2" || { echo "$2" | tail -5; die "[$OS] $1: output lacks '$3'"; }; ok "$1"; }

build_host() {   # $1=os -> local image aireos-pgit-host-<os>: the floor image + ONLY systemd
  docker build -q --platform linux/amd64 --build-arg "BASE=$(lock_get "IMAGE_$1")" -t "aireos-pgit-host-$1" - >/dev/null <<'EOF'
ARG BASE
FROM ${BASE}
RUN if command -v apt-get >/dev/null; then \
      apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends systemd systemd-sysv \
      && apt-get clean && rm -rf /var/lib/apt/lists/*; \
    else rpm -q systemd >/dev/null && test -x /sbin/init; fi
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
EOF
}
mk_companion() {   # $1=volume $2=ostag(dir name) $3=tree dir $4=host image $5=mutation run in the copied pgsql/ root
  docker volume rm -f "$1" >/dev/null; docker volume create "$1" >/dev/null; VOLUMES+=("$1")
  docker run --rm --network none -v "$3":/t:ro -v "$HERE":/src:ro -v "$1":/c "$4" sh -c \
    "d=/c/aireos-postgres-$2 && mkdir -p \$d && cp -a /t \$d/pgsql && cp /src/install-postgres.sh /src/aireos-postgres.service \$d/ && chmod 755 \$d/install-postgres.sh && cd \$d/pgsql && ${5:-true}"
}
start_host() {   # $1=name $2=image, then extra docker args
  docker rm -f "$1" >/dev/null 2>&1 || true; CONTAINERS+=("$1")
  docker run -d --platform linux/amd64 --name "$1" --network none --privileged --cgroupns=host --tmpfs /run --tmpfs /run/lock \
    -v /sys/fs/cgroup:/sys/fs/cgroup:rw "${@:3}" "$2" >/dev/null
  wait_up "$1"
}
wait_up() { local i; for i in $(seq 1 90); do case "$(docker exec "$1" systemctl is-system-running 2>&1 || true)" in running|degraded) return 0;; esac; sleep 1; done; die "systemd did not come up in $1"; }
inst() { local c="$1" comp="$2"; shift 2; docker exec -e COCKPIT_PW="$PW" "$@" "$c" bash -c "/comp/$comp/*/install-postgres.sh"; }
sql() {   # $1=container $2=port $3=sql : a TCP password login as cockpit, with the tree's own psql (password in env, never argv)
  docker exec -e PGPASSWORD="${PGPW:-$PW}" "$1" /opt/aireos/pgsql/bin/psql "host=127.0.0.1 port=$2 dbname=cockpit user=cockpit sslmode=disable" -X -At -c "$3"
}
clean_check() {   # $1=container $2=label $3=snapshot taken before: the refused install changed NOTHING
  local after; after="$(XB "$1" "$SNAP")"
  [ "$after" = "$3" ] || { diff <(echo "$3") <(echo "$after") | head -10; die "[$2] the host CHANGED during a refused install"; }
  XB "$1" '[ ! -e /opt/aireos/pgsql ] && [ ! -e /var/lib/aireos/pgsql ] && ! getent passwd aireos-pg >/dev/null && [ ! -e /etc/systemd/system/aireos-postgres.service ] && [ ! -e /run/aireos-postgres ]' \
    || die "[$2] aireos-postgres state exists after a refused install"
}
refuse() {   # $1=container $2=label $3=companion $4=needle, then docker-exec options: must exit non-zero, say $4, change nothing
  local c="$1" lab="$2" comp="$3" needle="$4" before out rc; shift 4
  before="$(XB "$c" "$SNAP")"
  out="$(inst "$c" "$comp" "$@" 2>&1)" && rc=0 || rc=$?
  [ "$rc" -ne 0 ] || { echo "$out" | tail -5; die "NEGATIVE CONTROL INVALID: $lab was installed"; }
  grep -qF -- "$needle" <<<"$out" || { echo "$out"; die "$lab refused but did not say '$needle'"; }
  clean_check "$c" "$lab" "$before"
  log "NEG-OK $lab: refused rc=$rc, said '$needle', nothing changed -> $(grep -F -- "$needle" <<<"$out" | head -1 | cut -c1-240)"
}

run_os() {
  OS="$1"; local C="aireos-pgit-$1" img tree="$TREES/aireos-pgsql-$1" v="aireos-pgit-$1" m sysid pid0 hits
  [ -f "$tree/share/aireos/COMPONENTS.json" ] || die "no tree at $tree — run 'task pgsql-trees' first"
  log "[$OS] build the host image (floor + systemd) and the companion layouts"
  build_host "$OS"; img="aireos-pgit-host-$OS"
  mk_companion "$v-ok" "$OS" "$tree" "$img"
  mk_companion "$v-up" "$OS" "$tree" "$img" 'echo upgrade-marker > share/aireos/UPGRADE_MARKER'
  mk_companion "$v-n2" "$OS" "$tree" "$img" 'echo libaireos-absent.so.9 >> share/aireos/HOST_LIBS'
  m=(-v "$v-ok:/comp/ok:ro" -v "$v-up:/comp/up:ro" -v "$v-n2:/comp/n2:ro")
  if [ "$OS" = rocky9 ]; then   # N1: the tree built on the NEWEST Rocky (9.8) needs OPENSSL_3.4.0; the floor (9.0) lacks it
    [ -f "$WORK/newest-built-rocky9/share/aireos/COMPONENTS.json" ] || die "no $WORK/newest-built-rocky9 — run 'task pgsql-trees-smoke -- rocky9' to regenerate it"
    mk_companion "$v-n1" rocky9 "$WORK/newest-built-rocky9" "$img"; m+=(-v "$v-n1:/comp/n1:ro")
  fi
  if [ "$OS" = ubuntu2404 ]; then mk_companion "$v-n3" rocky9 "$TREES/aireos-pgsql-rocky9" "$img"; m+=(-v "$v-n3:/comp/n3:ro"); fi
  start_host "$C" "$img" "${m[@]}"
  local pk0 snap0; pk0="$(XB "$C" "$PKGS")"; snap0="$(XB "$C" "$SNAP")"
  log "[$OS] host up; package list captured ($(XB "$C" 'command -v dpkg-query >/dev/null && dpkg-query -W | wc -l || rpm -qa | wc -l') packages) before any install"

  # ---- negatives: observed failing, host unchanged
  refuse "$C" "N2/$OS (INS-4 absent soname)" n2 libaireos-absent.so.9
  [ "$OS" != rocky9 ] || refuse "$C" "N1/$OS (INS-11 floor skew)" n1 OPENSSL_3.4.0
  [ "$OS" != ubuntu2404 ] || refuse "$C" "N3/$OS (wrong OS)" n3 "built for 'rocky9' but this host is 'ubuntu2404'"

  # ---- (e) sampler: sweeps every /proc/*/cmdline with bash builtins while the installer runs
  docker exec -i "$C" bash -c 'cat >/var/tmp/sampler.sh' < <(sed "s/@PW@/$PW/" <<'EOF'
shopt -s nullglob; n=0
while [ ! -e /var/tmp/sampler.stop ]; do
  for f in /proc/[0-9]*/cmdline; do
    mapfile -d '' -t a <"$f" 2>/dev/null || continue
    s="${a[*]}"
    if [[ "$s" == *@PW@* ]]; then if [[ "$s" == *canary* ]]; then echo canary >>/var/tmp/pw.hits; else echo "${a[0]##*/}" >>/var/tmp/pw.hits; fi; fi
    [[ "${a[0]}" == */psql && "$s" == *" -U aireos-pg "* ]] && echo psql >>/var/tmp/psql.seen
  done
  n=$((n+1)); [ $((n%20)) = 0 ] && echo $n >/var/tmp/sweeps
done
EOF
)
  XB "$C" ": >/var/tmp/pw.hits; : >/var/tmp/psql.seen"; docker exec -d "$C" bash /var/tmp/sampler.sh; sleep 1

  # ---- (a) install
  log "[$OS] (a) install"
  out="$(inst "$C" ok 2>&1)" || { echo "$out"; die "[$OS] install failed"; }
  echo "$out" | tail -4
  eq "(a) unit active" active "$(XB "$C" 'systemctl is-active aireos-postgres')"
  eq "(a) unit enabled" enabled "$(XB "$C" 'systemctl is-enabled aireos-postgres')"
  eq "(a) TCP password login as cockpit, 127.0.0.1:5432, sslmode=disable" cockpit "$(sql "$C" 5432 'SELECT current_user')"
  has "(a) pg_extension contains pgcrypto" "$(sql "$C" 5432 'SELECT extname FROM pg_extension')" pgcrypto
  eq "(a) pgp_sym_encrypt/decrypt round trip" hello "$(sql "$C" 5432 "SELECT pgp_sym_decrypt(pgp_sym_encrypt('hello','k'),'k')")"
  eq "(a) encoding / locale (C.UTF-8)" "UTF8|C.UTF-8" "$(sql "$C" 5432 'SELECT current_setting($$server_encoding$$)||$$|$$||datcollate FROM pg_database WHERE datname=current_database()')"
  eq "(a) server runs as aireos-pg" aireos-pg "$(XB "$C" 'stat -c %U /proc/$(systemctl show -p MainPID --value aireos-postgres)')"
  eq "(a) data dir mode/owner" "700 aireos-pg" "$(XB "$C" 'stat -c "%a %U" /var/lib/aireos/pgsql')"
  eq "(a) tree owner/mode (read-only to the service)" "755 root" "$(XB "$C" 'stat -c "%a %U" /opt/aireos/pgsql')"
  eq "(a) socket in the unit's RuntimeDirectory" yes "$(XB "$C" '[ -S /run/aireos-postgres/.s.PGSQL.5432 ] && echo yes || echo no')"
  PGPW=wrong-password sql "$C" 5432 'SELECT 1' >/dev/null 2>&1 && die "[$OS] a wrong password was accepted over TCP" || ok "(a) wrong password rejected over TCP (scram-sha-256)"
  (clean_check "$C" selftest "$snap0") >/dev/null 2>&1 && die "[$OS] the nothing-changed check cannot fail" || ok "(self-test) the nothing-changed check detects an installed host"
  sql "$C" 5432 'CREATE TABLE pgit_marker(v text); INSERT INTO pgit_marker VALUES ($$kept$$)' >/dev/null

  # ---- (b) restart, then a full container restart
  XB "$C" 'systemctl restart aireos-postgres'
  eq "(b) after systemctl restart" kept "$(sql "$C" 5432 'SELECT v FROM pgit_marker')"
  docker restart "$C" >/dev/null; wait_up "$C"; docker exec -d "$C" bash /var/tmp/sampler.sh   # the boot killed the sampler; hits files persist (/var/tmp)
  eq "(b) after a full container restart (enabled): active" active "$(XB "$C" 'systemctl is-active aireos-postgres')"
  eq "(b) after a full container restart: data intact" kept "$(sql "$C" 5432 'SELECT v FROM pgit_marker')"

  # ---- (c) idempotent re-run: same data, no re-initdb
  sysid="$(sql "$C" 5432 'SELECT system_identifier FROM pg_control_system()')"
  out="$(inst "$C" ok 2>&1)" || { echo "$out"; die "[$OS] re-run failed"; }
  has "(c) re-run exit 0, tree already current" "$out" "already the shipped one"
  has "(c) re-run did not re-initdb" "$out" "not re-initialising"
  eq "(c) same cluster (system_identifier)" "$sysid" "$(sql "$C" 5432 'SELECT system_identifier FROM pg_control_system()')"
  eq "(c) data kept" kept "$(sql "$C" 5432 'SELECT v FROM pgit_marker')"
  # (c2) a different shipped tree is swapped in (stage + rename), service restarted, data kept
  pid0="$(XB "$C" 'systemctl show -p MainPID --value aireos-postgres')"
  out="$(inst "$C" up 2>&1)" || { echo "$out"; die "[$OS] upgrade re-run failed"; }
  has "(c2) tree swap announced" "$out" "install the PostgreSQL tree"
  eq "(c2) new tree in place" upgrade-marker "$(XB "$C" 'cat /opt/aireos/pgsql/share/aireos/UPGRADE_MARKER')"
  eq "(c2) no stage/old leftovers" 0 "$(XB "$C" 'shopt -s nullglob dotglob; a=(/opt/aireos/.pgsql.*); echo ${#a[@]}')"
  eq "(c2) service restarted on the new tree" yes "$(XB "$C" "[ \"\$(systemctl show -p MainPID --value aireos-postgres)\" != $pid0 ] && systemctl is-active --quiet aireos-postgres && echo yes || echo no")"
  eq "(c2) data after the swap" kept "$(sql "$C" 5432 'SELECT v FROM pgit_marker')"
  eq "(c2) same cluster after the swap" "$sysid" "$(sql "$C" 5432 'SELECT system_identifier FROM pg_control_system()')"

  # ---- N5: a data dir of another major version is refused, nothing touched
  XB "$C" 'echo 15 > /var/lib/aireos/pgsql/PG_VERSION'
  out="$(inst "$C" ok 2>&1)" && die "[$OS] N5: a PG_VERSION 15 data dir was accepted"
  has "N5 refused a data dir of another major version" "$out" "holds PostgreSQL 15 data"
  XB "$C" 'echo 16 > /var/lib/aireos/pgsql/PG_VERSION'
  eq "N5 service untouched" active "$(XB "$C" 'systemctl is-active aireos-postgres')"

  # ---- N6: a symlink planted by the service user inside $DATA must not make a root re-run write or chown
  # through it (security review of 159ed4e: the committed installer wrote AND chowned a root file through
  # exactly this link — observed by the Commander, then fixed). Preflight now refuses the link outright;
  # the act section's writes also run as aireos-pg, so a link planted after preflight is equally harmless.
  XB "$C" "cp -p /var/lib/aireos/pgsql/aireos-managed.conf /var/tmp/managed.keep && printf 'SECRET-CANARY\n' >/etc/pgit-canary && chmod 600 /etc/pgit-canary && setpriv --reuid aireos-pg --regid aireos-pg --clear-groups ln -sf /etc/pgit-canary /var/lib/aireos/pgsql/aireos-managed.conf"
  out="$(inst "$C" ok 2>&1)" && die "[$OS] N6: a re-run over a planted symlink was accepted"
  has "N6 refused a symlink planted in the data dir" "$out" "is a symbolic link, which this installer never follows"
  eq "N6 root file not written or chowned through the link" "root:root SECRET-CANARY" "$(XB "$C" 'echo "$(stat -c %U:%G /etc/pgit-canary) $(head -1 /etc/pgit-canary)"')"
  XB "$C" 'cp -p /var/tmp/managed.keep /var/lib/aireos/pgsql/aireos-managed.conf.tmp && mv -f /var/lib/aireos/pgsql/aireos-managed.conf.tmp /var/lib/aireos/pgsql/aireos-managed.conf'
  inst "$C" ok >/dev/null 2>&1 || die "[$OS] N6: the re-run after restoring the real file failed"
  eq "N6 service active after the restore + re-run" active "$(XB "$C" 'systemctl is-active aireos-postgres')"

  # ---- (e) the password never appeared in any cmdline; the canary proves the sampler can see one
  # Wait for the sampler to SEE the canary (a fixed 1 s sleep raced a slow sweep on rocky9), bounded at 20 s.
  XB "$C" 'for f in /proc/[0-9]*/cmdline; do tr "\0" " " <"$f" 2>/dev/null; echo; done | grep -q "[s]ampler\.sh"' || die "[$OS] (e) the cmdline sampler is not running - the check would be blind"
  docker exec -d "$C" bash -c 'sleep 30' "canary-$PW"
  for _ in $(seq 1 20); do XB "$C" 'grep -q canary /var/tmp/pw.hits' && break; sleep 1; done
  XB "$C" 'touch /var/tmp/sampler.stop'; sleep 2
  hits="$(XB "$C" 'sort /var/tmp/pw.hits | uniq -c | tr -s " \n" " "')"
  eq "(e) cmdline hits = the planted canary ONLY (sampler can see a password; installer leaked none)" " $(XB "$C" "grep -c canary /var/tmp/pw.hits") canary " "$hits"
  [ "$(XB "$C" 'grep -c canary /var/tmp/pw.hits')" -ge 1 ] || die "[$OS] (e) the sampler never saw the planted canary - it is blind"
  ok "(e) sampler: $(XB "$C" 'cat /var/tmp/sweeps') sweeps, caught psql in flight $(XB "$C" 'wc -l </var/tmp/psql.seen') times; plus by code path: the password reaches psql only as hex on stdin"

  # ---- (d) INS-2
  eq "(d) INS-2 package database byte-identical (sha256 of the sorted list)" "$pk0" "$(XB "$C" "$PKGS")"
  echo "PGSQL-INSTALL-TEST PASS os=$OS (a)install+login+pgcrypto (b)restart (c)idempotent+swap (d)pkgs-unchanged (e)no-pw-in-cmdline N2$([ "$OS" != rocky9 ] || echo ' N1')$([ "$OS" != ubuntu2404 ] || echo ' N3') N5 N6"
  docker rm -f "$C" >/dev/null
}

run_n4() {   # ubuntu2404 only: a distro PostgreSQL already on 5432, holding a 'cockpit' DB (models a v0.16.0 host)
  OS=ubuntu2404; local C=aireos-pgit-n4 pk0 snap0 out
  log "[$OS] N4: build a host that ALREADY runs a distro PostgreSQL (separate layer, with network)"
  docker build -q -t aireos-pgit-distropg - >/dev/null <<'EOF'
FROM aireos-pgit-host-ubuntu2404
RUN apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq postgresql && apt-get clean && rm -rf /var/lib/apt/lists/*
EOF
  start_host "$C" aireos-pgit-distropg -v "aireos-pgit-ubuntu2404-ok:/comp/ok:ro"
  for i in $(seq 1 30); do XB "$C" 'systemctl is-active --quiet postgresql@16-main' 2>/dev/null && break; sleep 1; done
  docker exec -u postgres "$C" psql -v ON_ERROR_STOP=1 -q -d postgres -c "CREATE ROLE cockpit LOGIN PASSWORD 'v016pw'" -c "CREATE DATABASE cockpit OWNER cockpit" >/dev/null
  docker exec -u postgres "$C" psql -v ON_ERROR_STOP=1 -q -d cockpit -c "CREATE TABLE v016(x int); INSERT INTO v016 VALUES (16)" >/dev/null
  dsum() { docker exec -u postgres "$C" psql -d cockpit -At -c 'SELECT sum(x) FROM v016'; }
  pk0="$(XB "$C" "$PKGS")"; snap0="$(XB "$C" "$SNAP")"
  eq "N4 pre-existing distro cluster answers on 5432 (cockpit DB marker)" 16 "$(dsum)"
  refuse "$C" "N4/$OS (INS-3/8 port 5432 taken by the distro PostgreSQL)" ok "TCP port 5432 is already in use"
  eq "N4 the existing install keeps working after the refusal" 16 "$(dsum)"
  out="$(inst "$C" ok -e AIREOS_PG_PORT=5433 2>&1)" || { echo "$out"; die "N4 install on 5433 failed"; }
  has "N4 install beside it succeeded, URL hint carries the real port" "$out" "@localhost:5433/cockpit"
  eq "N4 both clusters answer: distro on 5432" 16 "$(dsum)"
  eq "N4 both clusters answer: aireos-postgres on 5433" cockpit "$(sql "$C" 5433 'SELECT current_user')"
  eq "N4 the new cluster is separate (no distro table in it)" 0 "$(sql "$C" 5433 "SELECT count(*) FROM pg_tables WHERE tablename='v016'")"
  eq "N4 distro cluster still online" online "$(XB "$C" 'pg_lsclusters -h | awk "{print \$4}"')"
  eq "N4 INS-2 package database byte-identical (distro packages untouched)" "$pk0" "$(XB "$C" "$PKGS")"
  echo "PGSQL-INSTALL-TEST PASS os=ubuntu2404 N4 (refused on 5432 unchanged; installed beside on 5433; both answer; distro cockpit DB untouched)"
  docker rm -f "$C" >/dev/null
}

for os in "${OSES[@]}"; do lock_get "IMAGE_$os" >/dev/null; done
for os in "${OSES[@]}"; do run_os "$os"; done
for os in "${OSES[@]}"; do [ "$os" != ubuntu2404 ] || run_n4; done
log "done (${OSES[*]})"
