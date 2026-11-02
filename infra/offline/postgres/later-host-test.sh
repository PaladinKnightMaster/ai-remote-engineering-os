#!/usr/bin/env bash
# later-host-test.sh — INS-1 + SEC-63 (ADR-0077 D7, D9; T-173): does the PostgreSQL companion install and run on a
# host built LATER than the companion, with every OS update applied?  WD-1 cannot answer that: it co-builds the host
# and the companion from one snapshot, so they cannot drift (ADR-0077 §Context). This test provisions the host
# INDEPENDENTLY — the OS's LATEST_<ostag> image (pgsql-build.lock) + a full OS upgrade WITH network, at test time —
# and then installs with --network none. It never uses the floor image or a build image (the layers the companion
# came from).
#
#   TestINS1LaterHost                       per OS (ubuntu2404 ubuntu2604 rocky9), each on its own host:
#       "later" is a CHECKED property: glibc/OpenSSL (+perl-base) of the host vs the companion's build floor, compared
#       with dpkg/rpm's own version comparison — if the host is not strictly newer, the test is not measuring skew: die
#       leg A  the new companion: install-postgres.sh, unit active, TCP password login, pgcrypto round trip, INS-2
#       leg B  the full stack: the core bundle's real install.sh against that PostgreSQL, CHECKPOINT + restart
#              (T-172c), all eight units active, Caddy serves the SPA
#   TestSEC63OldCompanionFailsOnLaterHost   the negative control (CEO ruling 2026-11-01): the v0.16.0 RECIPE, unchanged
#       (fetch-postgres.sh + install-postgres.sh from tag v0.16.0, blob-checked), rebuilt against Ubuntu's archive
#       snapshot of 2026-09-01 into dist/sec63-old-recipe/ (NEVER dist/offline/), goes through the SAME try_install on
#       the ubuntu2404 later host: it must FAIL, and FOR THE RIGHT REASON — apt's unmet-dependency signature, with the
#       host's perl-base strictly newer than the control's pin. Any other failure is "SEC-63 INVALID". If it PASSES,
#       the test does not measure skew (ADR-0077 D7): reported as such, never adjusted until green. The SHIPPED v0.16.0
#       companion (dist/v0.16.0-release, read-only, sha256-pinned) is also run, as an informational NOTE only.
#
# Usage: bash infra/offline/postgres/later-host-test.sh [ostag...]     (default: ubuntu2404 ubuntu2604 rocky9)
# Needs Docker, network (image builds; snapshot.ubuntu.com for the SEC-63 control, built once and reused), `task pgsql-trees` + `bash build-postgres-companions.sh` +
# `task offline-bundle` output in dist/offline/ (rebuild if stale — the test checks the shipped installers match HEAD).
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$HERE/../../.." && pwd)"
LOCK="$HERE/pgsql-build.lock"; DIST="$REPO_ROOT/dist/offline"; OLD_DIR="$REPO_ROOT/dist/v0.16.0-release"
OLD_SHA=61aff02d526c37468c1be38fe506c7c79404adb61ad530c2b26f2715e00f85a6   # the v0.16.0 companion, as released
PW="lht-$RANDOM$RANDOM-Zq9"; OPPW="lht-op-$RANDOM$RANDOM-Zq9"            # throwaway test credentials
UNITS="aireos-api aireos-audit aireos-broker aireos-connector aireos-policy aireos-mcp-gateway aireos-caddy aireos-postgres"
log() { echo "[later-host-test] $*"; }
die() { echo "[later-host-test] FAIL: $*" >&2; exit 1; }
# MSYS_NO_PATHCONV for docker ONLY (mount paths); exporting it globally would break other tools on Windows git-bash.
docker() { MSYS_NO_PATHCONV=1 command docker "$@"; }
lock_get() { local v; v="$(sed -n "s/^$1=//p" "$LOCK" | head -1)"; [ -n "$v" ] || die "$1 missing from $LOCK"; printf '%s' "$v"; }
OSES=("$@"); [ "${#OSES[@]}" -gt 0 ] || OSES=(ubuntu2404 ubuntu2604 rocky9)
CONTAINERS=()
cleanup() { [ "${KEEP:-0}" = 1 ] || docker rm -f "${CONTAINERS[@]}" >/dev/null 2>&1 || true; }
trap cleanup EXIT

XB() { docker exec "$1" bash -c "$2"; }
ok() { echo "  ok [$OS] $*"; }
eq() { [ "$2" = "$3" ] || die "[$OS] $1: expected '$2', got '$3'"; ok "$1: $3"; }
has() { grep -qF -- "$3" <<<"$2" || { echo "$2" | tail -8; die "[$OS] $1: output lacks '$3'"; }; ok "$1"; }

# --- in-container snippets (quoted heredocs: nothing is expanded on the host) ---------------------------------------
LIBS="$(cat <<'EOF'
if command -v dpkg-query >/dev/null; then
  for p in libc6 libssl3t64 libssl3 perl-base; do v="$(dpkg-query -W -f='${Version}' "$p" 2>/dev/null)" && echo "$p=$v"; done
else for p in glibc openssl-libs; do echo "$p=$(rpm -q --qf '%{VERSION}-%{RELEASE}' "$p")"; done; fi
true
EOF
)"
GT="$(cat <<'EOF'
# gt A B : exit 0 iff A is strictly newer than B, by the package manager's OWN version comparison
if command -v dpkg >/dev/null; then dpkg --compare-versions "$1" gt "$2"
else [ "$(rpm --eval "%{lua:print(rpm.vercmp('$1','$2'))}")" = 1 ]; fi
EOF
)"
PKGS="$(cat <<'EOF'
if command -v dpkg-query >/dev/null; then dpkg-query -W -f='${binary:Package} ${Version}\n'; else rpm -qa --qf '%{NAME}-%{VERSION}-%{RELEASE}.%{ARCH}\n'; fi | sort | sha256sum
EOF
)"
gt() { docker exec -i "$1" bash -s -- "$2" "$3" <<<"$GT"; }
get() { sed -n "s/^$2=//p" <<<"$1" | head -1; }   # value of KEY from a KEY=value list

# --- hosts -----------------------------------------------------------------------------------------------------------
build_host() {   # $1=os -> image aireos-later-host-<os>: the LATEST image + a FULL OS upgrade (network, at test time) + systemd/curl
  log "[$1] build the LATER host: $(lock_get "LATEST_$1") + full OS upgrade (network on, --no-cache)"
  docker build -q --no-cache --platform linux/amd64 --build-arg "BASE=$(lock_get "LATEST_$1")" -t "aireos-later-host-$1" - >/dev/null <<'EOF'
ARG BASE
FROM ${BASE}
RUN if command -v apt-get >/dev/null; then \
      export DEBIAN_FRONTEND=noninteractive; apt-get update -qq && apt-get -y -qq full-upgrade \
      && apt-get install -y -qq --no-install-recommends systemd systemd-sysv curl ca-certificates \
      && apt-get clean && rm -rf /var/lib/apt/lists/*; \
    else dnf -y -q upgrade && { rpm -q systemd >/dev/null || dnf -y -q install systemd; } \
      && { command -v curl >/dev/null || dnf -y -q install curl; } && dnf -q clean all; fi
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
EOF
}
start_host() {   # $1=name $2=image, then extra docker args: systemd as PID 1, NO network
  docker rm -f "$1" >/dev/null 2>&1 || true; CONTAINERS+=("$1")
  docker run -d --platform linux/amd64 --name "$1" --network none --privileged --cgroupns=host --tmpfs /run --tmpfs /run/lock \
    -v /sys/fs/cgroup:/sys/fs/cgroup:rw -v "$DIST":/artifacts:ro "${@:3}" "$2" >/dev/null
  local i; for i in $(seq 1 90); do case "$(docker exec "$1" systemctl is-system-running 2>&1 || true)" in running|degraded) return 0;; esac; sleep 1; done
  die "systemd did not come up in $1"
}
# The one install path both the new companions (leg A) and the v0.16.0 companion (SEC-63) go through.
# $1=container $2=tarball path inside it $3=dir name in the tarball ; sets TI_RC and TI_OUT (never dies on a failed install)
try_install() {
  TI_OUT="$(docker exec -e COCKPIT_PW="$PW" "$1" bash -c "set -e; rm -rf /root/inst-\$\$; d=/root/inst-\$\$; mkdir -p \$d && cd \$d && tar xzf '$2' && cd '$3' && ./install-postgres.sh" 2>&1)" && TI_RC=0 || TI_RC=$?
}
psql_tcp() {   # $1=container $2=sql : a TCP password login as cockpit with the tree's OWN psql (password in env, never argv)
  docker exec -e PGPASSWORD="$PW" "$1" /opt/aireos/pgsql/bin/psql "host=127.0.0.1 port=5432 dbname=cockpit user=cockpit sslmode=disable" -X -At -c "$2"
}

# --- artifacts must be what HEAD ships --------------------------------------------------------------------------------
check_artifacts() {
  local os core t list
  [ -f "$OLD_DIR/aireos-postgres-ubuntu2404.tar.gz" ] || die "no v0.16.0 companion at $OLD_DIR"
  [ "$(sha256sum "$OLD_DIR/aireos-postgres-ubuntu2404.tar.gz" | cut -d' ' -f1)" = "$OLD_SHA" ] \
    || die "the v0.16.0 companion does not hash to $OLD_SHA — it is not the released artifact; refusing to use it as the negative control"
  # A control artifact (or any package-closure companion) in dist/offline/ could be signed and SHIPPED by sign-release /
  # sec45-check, which glob dist/offline/*.tar.gz. Every tarball there must be the core bundle or a source-built companion.
  for t in "$DIST"/*.tar.gz; do
    case "$(basename "$t")" in aireos-offline-*.tar.gz) continue;; aireos-postgres-*.tar.gz) ;; *) die "unexpected tarball in dist/offline/: $t";; esac
    list="$(tar --force-local -tzf "$t")"   # a variable, not a pipe: grep -q exiting early would SIGPIPE tar under pipefail
    grep -q '^aireos-postgres-[a-z0-9]*/pgsql/bin/postgres$' <<<"$list" || die "$t has no pgsql/ tree — a package-closure/control companion must never sit in dist/offline/ (it would be signed and shipped)"
  done
  local cores=("$DIST"/aireos-offline-*.tar.gz); [ "${#cores[@]}" = 1 ] && [ -f "${cores[0]}" ] || die "expected exactly ONE aireos-offline-*.tar.gz in $DIST, found ${#cores[@]}"
  CORE="$(basename "${cores[0]}")"
  # A stale artifact would test yesterday's installers: the shipped scripts must be byte-identical to HEAD's.
  for os in "${OSES[@]}"; do
    [ -f "$DIST/aireos-postgres-$os.tar.gz" ] || die "no $DIST/aireos-postgres-$os.tar.gz — bash infra/offline/postgres/build-postgres-companions.sh $os"
    for t in install-postgres.sh aireos-postgres.service; do
      [ "$(tar --force-local -xzOf "$DIST/aireos-postgres-$os.tar.gz" "aireos-postgres-$os/$t" | sha256sum | cut -d' ' -f1)" = "$(sha256sum "$HERE/$t" | cut -d' ' -f1)" ] \
        || die "STALE: aireos-postgres-$os.tar.gz carries a different $t than HEAD — rebuild the companions"
    done
  done
  core="${CORE%.tar.gz}"
  [ "$(tar --force-local -xzOf "$DIST/$CORE" "$core/install.sh" | sha256sum | cut -d' ' -f1)" = "$(sha256sum "$REPO_ROOT/infra/offline/install.sh" | cut -d' ' -f1)" ] \
    || die "STALE: $CORE carries a different install.sh than HEAD — task offline-bundle"
  log "artifacts: $CORE + new companions match HEAD's installers; the v0.16.0 companion hashes to $OLD_SHA"
}

# --- "later" is a checked property -----------------------------------------------------------------------------------
# $1=container $2=companion base_image (floor)  → die unless the host is strictly newer than the build floor
check_later_new() {
  local C="$1" floor host fl; floor="$(XB "$C" "tar -xzOf /artifacts/aireos-postgres-$OS.tar.gz aireos-postgres-$OS/pgsql/share/aireos/COMPONENTS.json" | sed -n 's/.*"base_image": *"\([^"]*\)".*/\1/p' | head -1)"
  [ -n "$floor" ] || die "[$OS] no base_image in the companion's COMPONENTS.json"
  [ "$floor" = "$(lock_get "IMAGE_$OS")" ] || die "[$OS] companion base_image ($floor) is not the lock's IMAGE_$OS — the companion is stale"
  host="$(XB "$C" "$LIBS")"; fl="$(docker run --rm --platform linux/amd64 --entrypoint bash "$floor" -c "$LIBS")"
  log "[$OS] companion build floor : $floor"; echo "$fl" | sed 's/^/      floor /'
  log "[$OS] later host (upgraded)  : $(docker image inspect -f '{{.Id}}' "aireos-later-host-$OS" | cut -c1-19)"; echo "$host" | sed 's/^/      host  /'
  local k newer=0
  for k in $([ "$OS" = rocky9 ] && echo "glibc openssl-libs" || echo "libc6 libssl3t64 libssl3"); do
    [ -n "$(get "$fl" "$k")" ] && [ -n "$(get "$host" "$k")" ] || continue
    if gt "$C" "$(get "$host" "$k")" "$(get "$fl" "$k")"; then newer=$((newer+1)); echo "      NEWER  $k: host $(get "$host" "$k") > floor $(get "$fl" "$k")"
    else echo "      same   $k: host $(get "$host" "$k") vs floor $(get "$fl" "$k")"; fi
  done
  # Rule 12: the comparison must be able to say "not newer" — a floor compared with itself must not count.
  ! gt "$C" "$(get "$fl" "$(echo "$fl" | head -1 | cut -d= -f1)")" "$(get "$fl" "$(echo "$fl" | head -1 | cut -d= -f1)")" || die "[$OS] the version comparison calls a version newer than itself"
  [ "$newer" -ge 1 ] || die "[$OS] the host is NOT strictly newer than the companion's build floor in glibc or OpenSSL — this test is not measuring skew"
  ok "later host is strictly newer than the companion's build in $newer of glibc/OpenSSL (self-test: X is not newer than X)"
}

# --- INS-1 -----------------------------------------------------------------------------------------------------------
function TestINS1LaterHost() {
  local os C pk0 out i
  for os in "${OSES[@]}"; do
    OS="$os"; C="aireos-later-$os"
    start_host "$C" "aireos-later-host-$os"
    check_later_new "$C"
    pk0="$(XB "$C" "$PKGS")"
    # ---- leg A: the new companion on the later host
    log "[$os] leg A: install-postgres.sh from the new companion (--network none)"
    try_install "$C" "/artifacts/aireos-postgres-$os.tar.gz" "aireos-postgres-$os"
    [ "$TI_RC" = 0 ] || { echo "$TI_OUT" | tail -15; die "[$os] leg A: install-postgres.sh failed on the later host (rc=$TI_RC)"; }
    eq "legA unit active" active "$(XB "$C" 'systemctl is-active aireos-postgres')"
    eq "legA TCP password login as cockpit (127.0.0.1:5432, sslmode=disable)" cockpit "$(psql_tcp "$C" 'SELECT current_user')"
    has "legA pgcrypto present" "$(psql_tcp "$C" 'SELECT extname FROM pg_extension')" pgcrypto
    eq "legA pgp_sym round trip" hello "$(psql_tcp "$C" "SELECT pgp_sym_decrypt(pgp_sym_encrypt('hello','k'),'k')")"
    eq "legA INS-2 package database byte-identical across the install" "$pk0" "$(XB "$C" "$PKGS")"
    # ---- leg B: the full stack on the same host
    log "[$os] leg B: the core bundle's real install.sh against that PostgreSQL"
    out="$(docker exec -e AIREOS_DB_URL="postgres://cockpit:$PW@localhost:5432/cockpit?sslmode=disable" -e AIREOS_BOOTSTRAP_OPERATOR_USER=admin \
      -e AIREOS_BOOTSTRAP_OPERATOR_PASSWORD="$OPPW" -e AIREOS_SITE_ADDRESS=localhost -e AIREOS_DEPLOYMENT_PROFILE=lan-only "$C" \
      bash -c "set -e; mkdir -p /root/inst && cd /root/inst && tar xzf /artifacts/$CORE && cd \$(ls -d aireos-offline-*/ | head -1) && ./install.sh" 2>&1)" \
      || { echo "$out" | tail -25; die "[$os] leg B: install.sh FAILED on the later host — a real product finding, not a harness error"; }
    echo "$out" | tail -3
    # T-172c regression: PostgreSQL must survive a CHECKPOINT and a RESTART after install.sh (state-dir traversal).
    XB "$C" "su -s /bin/bash aireos-pg -c '/opt/aireos/pgsql/bin/psql -h /run/aireos-postgres -U aireos-pg -v ON_ERROR_STOP=1 -d postgres -c CHECKPOINT' </dev/null" >/dev/null \
      || { XB "$C" 'journalctl -u aireos-postgres -n 8 --no-pager'; die "[$os] legB T-172c: CHECKPOINT after install.sh failed"; }
    XB "$C" 'systemctl restart aireos-postgres' || die "[$os] legB T-172c: aireos-postgres did not restart after install.sh"
    eq "legB T-172c active after CHECKPOINT + restart" active "$(XB "$C" 'systemctl is-active aireos-postgres')"
    eq "legB T-172c SELECT 1 after the restart" 1 "$(psql_tcp "$C" 'SELECT 1')"
    for i in $(seq 1 30); do [ "$(XB "$C" "systemctl is-active $UNITS | sort -u | tr '\n' ' '")" = "active " ] && break; sleep 1; done   # units settle after the restart
    eq "legB all 8 units active ($UNITS)" "active " "$(XB "$C" "systemctl is-active $UNITS | sort -u | tr '\n' ' '")"
    eq "legB unit count" 8 "$(XB "$C" "systemctl is-active $UNITS | grep -c '^active$'")"
    # Same SPA check and marker as run-wd1.sh; Caddy may still be minting its local CA, so retry a bounded while.
    for i in $(seq 1 30); do XB "$C" 'curl -sk https://localhost/ | grep -qiE "<!doctype html|<html"' && break; sleep 1; done
    XB "$C" 'curl -sk https://localhost/ | grep -qiE "<!doctype html|<html"' || die "[$os] legB: https://localhost/ does not serve the SPA"
    ok "legB Caddy serves the SPA over HTTPS"
    echo "LATER-HOST-TEST PASS os=$os legA legB"
    docker rm -f "$C" >/dev/null
  done
}

# --- SEC-63 ----------------------------------------------------------------------------------------------------------
# CEO ruling 2026-11-01: the negative control is the v0.16.0 RECIPE (fetch-postgres.sh + install-postgres.sh, exactly as
# tagged), rebuilt against Ubuntu's dated archive snapshot of 2026-09-01 — the archive state of the PUB-6 rehearsal's
# failing companion. The SHIPPED v0.16.0 companion pins today's archive head, so it installs today (R1 is latent in it);
# it is still run, and reported as an informational NOTE, never as a gate input.
SNAP=20260901T000000Z; SNAP_LABEL=snapshot-20260901
OLD_BASE=ubuntu:noble-20260810@sha256:33ceb71981b602c1a7443a53469e4dba065f7503eab3078a2d7a57a2ab987517   # perl-base 5.38.2-3.2ubuntu0.3 installed
CTL="$REPO_ROOT/dist/sec63-old-recipe"   # OUTSIDE dist/offline: sign-release / sec45-check glob dist/offline/*.tar.gz
CTL_TAR=aireos-postgres-ubuntu2404.tar.gz

# apt's unmet-dependency signature. A line like "perl : Depends: perl-base (= X) but Y is to be installed" is exact
# version-pin skew; "unmet dependencies" is apt's summary of the same failure.
skew_line() { grep -E -m1 'Depends:.* but .*is to be installed' <<<"$1" || grep -E -m1 'unmet dependencies' <<<"$1" || true; }   # the exact pin line first
perl_pin() { awk '/^Package: perl$/{f=1;next} /^$/{f=0} f&&/^Depends:/{match($0,/perl-base \(= [^)]*\)/); s=substr($0,RSTART,RLENGTH); sub(/^perl-base \(= /,"",s); sub(/\)$/,"",s); print s; exit}' <<<"$1"; }

# Build (once) the v0.16.0 recipe's companion against the 2026-09-01 snapshot. The recipe is extracted from the tag and
# hash-checked against it; only the ENVIRONMENT differs (base image, apt source) — see the comments for packaging.
build_control() {
  local f p tagsha filesha
  git -C "$REPO_ROOT" rev-parse -q --verify v0.16.0 >/dev/null || die "SEC-63 INVALID: cannot build control — tag v0.16.0 is not in this repo"
  mkdir -p "$CTL/src" "$CTL/ca"
  for f in fetch-postgres.sh install-postgres.sh; do
    p="infra/offline/postgres/$f"; tagsha="$(git -C "$REPO_ROOT" rev-parse "v0.16.0:$p")"
    git -C "$REPO_ROOT" show "v0.16.0:$p" > "$CTL/src/$f"
    filesha="$(git -C "$REPO_ROOT" hash-object --no-filters "$CTL/src/$f")"
    [ "$filesha" = "$tagsha" ] || die "SEC-63 INVALID: $f extracted from v0.16.0 is not the tagged blob ($filesha != $tagsha)"
    log "recipe $f = blob $tagsha (v0.16.0:$p)"
  done
  if [ -f "$CTL/$CTL_TAR" ]; then log "control companion already built ($CTL/$CTL_TAR) — reusing; its embedded pins are re-verified below"; return 0; fi
  log "build the control: v0.16.0 recipe, unchanged, on $OLD_BASE with apt pointed at snapshot.ubuntu.com/ubuntu/$SNAP/"
  # TLS stays VERIFIED: the base image has no CA bundle, so a bundle copied from the (ca-certificates-bearing) later-host
  # image is mounted read-only. apt's signed-Release checks are untouched too.
  docker run --rm --platform linux/amd64 -v "$CTL/ca":/o aireos-later-host-ubuntu2404 cp /etc/ssl/certs/ca-certificates.crt /o/ || die "SEC-63 INVALID: cannot build control — no CA bundle"
  docker run --rm --platform linux/amd64 -v "$CTL":/w "$OLD_BASE" bash -c 'rm -rf /w/pkg && mkdir -p /w/pkg/aireos-postgres-ubuntu2404'
  docker run --rm --platform linux/amd64 -v "$CTL/src":/src:ro -v "$CTL/pkg/aireos-postgres-ubuntu2404":/out \
    -v "$CTL/ca/ca-certificates.crt":/etc/ssl/certs/ca-certificates.crt:ro -w /src "$OLD_BASE" bash -c "set -e
      v=\$(dpkg-query -W -f='\${Version}' perl-base); dpkg --compare-versions \"\$v\" le 5.38.2-3.2ubuntu0.4 || { echo \"base perl-base \$v is newer than the 2026-09-01 archive\" >&2; exit 3; }
      echo \"control base: perl-base \$v installed (period-correct: <= 5.38.2-3.2ubuntu0.4)\"
      sed -i 's#^URIs: .*#URIs: https://snapshot.ubuntu.com/ubuntu/$SNAP/#' /etc/apt/sources.list.d/ubuntu.sources
      exec bash /src/fetch-postgres.sh" || die "SEC-63 INVALID: cannot build control (snapshot.ubuntu.com unreachable, or the recipe failed)"
  # Packaging: v0.16.0's build-postgres-companions.sh commands, copied as written (manifest + normalized tar | gzip -n).
  # Differences, all non-functional: run in the same pinned image rather than on the host; no SBOM step (SBOM.cdx.json
  # is not read by install-postgres.sh); output lands in dist/sec63-old-recipe/, never dist/offline/.
  docker run --rm --platform linux/amd64 -v "$CTL/pkg":/pkg -w /pkg "$OLD_BASE" bash -c "set -e
      ( cd aireos-postgres-ubuntu2404 && find . -type f ! -name SHA256_MANIFEST.txt -print0 | sort -z | xargs -0 sha256sum > SHA256_MANIFEST.txt )
      ( cd aireos-postgres-ubuntu2404 && sha256sum -c SHA256_MANIFEST.txt >/dev/null )
      tar -C /pkg --sort=name --mtime='@0' --owner=0 --group=0 --numeric-owner -cf - aireos-postgres-ubuntu2404 | gzip -n > /pkg/$CTL_TAR" \
    || die "SEC-63 INVALID: cannot build control — packaging failed"
  docker run --rm --platform linux/amd64 -v "$CTL":/w "$OLD_BASE" bash -c "mv /w/pkg/$CTL_TAR /w/$CTL_TAR && rm -rf /w/pkg"
  log "control built: $CTL/$CTL_TAR"
}

# $1=container : a fresh later host with the shipped release dir (/old) and the control dir (/ctl) mounted read-only
sec63_host() { start_host "$1" aireos-later-host-ubuntu2404 -v "$OLD_DIR":/old:ro -v "$CTL":/ctl:ro; }

function TestSEC63OldCompanionFailsOnLaterHost() {
  OS=ubuntu2404; local C=aireos-later-sec63 old_tar=aireos-postgres-ubuntu2404.tar.gz pins pin_perl host_perl line sample ctl_pins shipped_rc shipped_pin
  # Rule 12: the signature matcher must match the real rehearsal line and must NOT match a harness-style failure.
  sample='perl : Depends: perl-base (= 5.38.2-3.2ubuntu0.4) but 5.38.2-3.2ubuntu0.6 is to be installed'
  [ -n "$(skew_line "$sample")" ] || die "SEC-63 INVALID: the skew matcher does not recognise the PUB-6 rehearsal's own apt line"
  [ -z "$(skew_line './install-postgres.sh: No such file or directory
E: Unable to locate package postgresql-16')" ] || die "SEC-63 INVALID: the skew matcher accepts a non-skew failure"
  ok "(self-test) the skew matcher recognises the PUB-6 apt line and rejects a harness-style failure"
  build_control
  # Instrument control (independent of any archive): a SYNTHETIC copy of the SHIPPED companion whose `perl` deb pins an
  # OLDER perl-base (made inside the container; dist/ is never touched). The same try_install on the same host image
  # must fail with the skew signature; if not, the harness could not tell skew from anything else.
  sec63_host "$C"
  XB "$C" "set -e; mkdir -p /root/synth && cd /root/synth && tar xzf /old/$old_tar && cd aireos-postgres-ubuntu2404/repo \
    && awk '/^Package: perl\$/{f=1} /^\$/{f=0} f&&/^Depends:/{sub(/perl-base \\(= [^)]*\\)/,\"perl-base (= 5.38.2-3.2ubuntu0.5)\")} {print}' Packages >P && mv P Packages && gzip -9f -k Packages \
    && grep -c 'perl-base (= 5.38.2-3.2ubuntu0.5)' Packages >/dev/null && tar czf /root/synth.tar.gz -C /root/synth aireos-postgres-ubuntu2404"
  try_install "$C" /root/synth.tar.gz aireos-postgres-ubuntu2404
  [ "$TI_RC" != 0 ] || { echo "$TI_OUT" | tail -8; die "SEC-63 INVALID: the synthetic skewed companion INSTALLED — the harness cannot detect skew"; }
  line="$(skew_line "$TI_OUT")"; [ -n "$line" ] || { echo "$TI_OUT" | tail -15; die "SEC-63 INVALID: the synthetic skewed companion failed for the wrong reason"; }
  ok "(instrument control) a synthetic older-pinned companion FAILS through the same try_install: $(sed 's/^[[:space:]]*//' <<<"$line")"
  docker rm -f "$C" >/dev/null
  # ---- the SHIPPED v0.16.0 companion: informational only (not a gate input)
  sec63_host "$C"
  pins="$(XB "$C" "tar -xzOf /old/$old_tar aireos-postgres-ubuntu2404/repo/Packages")"; shipped_pin="$(perl_pin "$pins")"
  host_perl="$(get "$(XB "$C" "$LIBS")" perl-base)"; [ -n "$host_perl" ] || die "SEC-63 INVALID: the later host has no perl-base"
  try_install "$C" "/old/$old_tar" aireos-postgres-ubuntu2404; shipped_rc="$TI_RC"
  docker rm -f "$C" >/dev/null
  # ---- the control: the v0.16.0 recipe at the 2026-09-01 snapshot, through the SAME host image and the SAME try_install
  sec63_host "$C"
  [ "$(XB "$C" "tar -xzOf /ctl/$CTL_TAR aireos-postgres-ubuntu2404/install-postgres.sh | sha256sum | cut -d' ' -f1")" = "$(git -C "$REPO_ROOT" show v0.16.0:infra/offline/postgres/install-postgres.sh | sha256sum | cut -d' ' -f1)" ] \
    || die "SEC-63 INVALID: the control companion's install-postgres.sh is not the v0.16.0 one"   # a cached artifact must carry the tagged installer
  ctl_pins="$(XB "$C" "tar -xzOf /ctl/$CTL_TAR aireos-postgres-ubuntu2404/repo/Packages")"; pin_perl="$(perl_pin "$ctl_pins")"
  [ -n "$pin_perl" ] || die "SEC-63 INVALID: could not read the perl-base pin from the control companion's repo/Packages"
  log "[ubuntu2404] control companion pins perl-base (= $pin_perl) ; later host has perl-base $host_perl"
  gt "$C" "$host_perl" "$pin_perl" || die "SEC-63 INVALID: host perl-base $host_perl is NOT strictly newer than the control's pin $pin_perl (dpkg --compare-versions) — no skew to measure"
  ok "skew is real by version: host perl-base $host_perl > control companion's perl-base $pin_perl (dpkg --compare-versions)"
  log "[ubuntu2404] SEC-63: the v0.16.0-recipe@$SNAP_LABEL companion through the SAME try_install, on the same later host image"
  try_install "$C" "/ctl/$CTL_TAR" aireos-postgres-ubuntu2404
  if [ "$TI_RC" = 0 ]; then
    echo "$TI_OUT" | tail -6
    die "SEC-63 NOT PROVED — the control INSTALLED (rc=0) on a host with real skew. ADR-0077 D7: this test does not measure skew. Do not adjust until green; escalate."
  fi
  line="$(skew_line "$TI_OUT")"
  if [ -z "$line" ]; then echo "$TI_OUT" | tail -20; die "SEC-63 INVALID: failed for the wrong reason (rc=$TI_RC, no apt unmet-dependency signature in the output)"; fi
  line="$(sed 's/^[[:space:]]*//' <<<"$line")"
  echo "SEC-63 PROVED os=ubuntu2404 v0.16.0-recipe@$SNAP_LABEL FAILED on version skew: $line"
  echo "SEC-63 evidence: rc=$TI_RC ; host perl-base $host_perl > control pin $pin_perl (dpkg --compare-versions)"
  if [ "$shipped_rc" = 0 ] && [ "$shipped_pin" = "$host_perl" ]; then
    echo "SEC-63 NOTE shipped v0.16.0 companion installs today (rc=0): pins perl-base $shipped_pin = archive head — R1 latent, triggers on the next perl update"
  else
    echo "SEC-63 NOTE shipped v0.16.0 companion today: rc=$shipped_rc, pins perl-base $shipped_pin, host has $host_perl"
  fi
  docker rm -f "$C" >/dev/null
}

check_artifacts
for os in "${OSES[@]}"; do lock_get "LATEST_$os" >/dev/null; lock_get "IMAGE_$os" >/dev/null; build_host "$os"; done
TestINS1LaterHost
if printf '%s\n' "${OSES[@]}" | grep -qx ubuntu2404; then TestSEC63OldCompanionFailsOnLaterHost; else echo "SEC-63 NOT RUN (ubuntu2404 not in this run's OS set)"; fi
log "done (${OSES[*]})"
