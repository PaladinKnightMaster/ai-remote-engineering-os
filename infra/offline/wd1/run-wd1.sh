#!/usr/bin/env bash
# run-wd1.sh — bring up the INSTALLED AI ReOS stack under REAL systemd and prove
# it comes up (T-021d-a; the WD-1 walkthrough + timeline assertions are T-021d-b).
#
# It runs the actual offline install path on a systemd-in-Docker "host": install
# postgres from the companion (offline — online apt sources are removed first so
# only the companion can satisfy it), then install.sh with systemctl ENABLED
# (not skipped), then asserts the four units + postgres are active, Caddy serves
# the SPA over HTTPS, and the chain-integrity `audit-service verify` CLI runs.
#
# Build host needs Docker (Linux or Windows/WSL2 — systemd-in-Docker verified).
# Prereq artifacts (build once, see the error messages): the core bundle +
# the ubuntu2404 postgres companion in dist/offline/.
set -euo pipefail
export MSYS_NO_PATHCONV=1   # Windows/git-bash: don't mangle -v / cgroup paths

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
OUT="$REPO_ROOT/dist/offline"
HOST=aireos-wd1-host
NET=aireos-wd1
DBPW=wd1secret
OPPW=wd1-operator-pw

log() { printf '\033[1;35m[wd1]\033[0m %s\n' "$*"; }
cleanup() { docker rm -f "$HOST" >/dev/null 2>&1 || true; docker network rm "$NET" >/dev/null 2>&1 || true; }
trap cleanup EXIT

# 1. require the artifacts (built via Docker; see T-021a/T-021c).
ls "$OUT"/aireos-offline-*.tar.gz >/dev/null 2>&1 \
  || { echo "missing core bundle — build it: docker run ... golang:1.23 'bash infra/offline/build-bundle.sh' (see T-021a)"; exit 1; }
ls "$OUT"/aireos-postgres-ubuntu2404.tar.gz >/dev/null 2>&1 \
  || { echo "missing postgres companion — build it: bash infra/offline/postgres/build-postgres-companions.sh ubuntu2404"; exit 1; }

# 2. systemd host image + container.
log "build systemd host image"
# Relative build context (cd into the dir): an absolute MSYS-style path is not a
# valid docker build context under MSYS_NO_PATHCONV on Windows; "." is portable.
( cd "$REPO_ROOT/infra/offline/wd1" && docker build -t aireos-wd1-host:latest -f Dockerfile.host . >/dev/null )
cleanup
docker network create "$NET" >/dev/null
log "start systemd host (privileged, cgroup v2)"
docker run -d --name "$HOST" --network "$NET" --privileged --cgroupns=host \
  --tmpfs /run --tmpfs /run/lock -v /sys/fs/cgroup:/sys/fs/cgroup:rw \
  -v "$OUT":/artifacts:ro aireos-wd1-host:latest >/dev/null
for i in $(seq 1 30); do
  case "$(docker exec "$HOST" systemctl is-system-running 2>&1 || true)" in
    running|degraded) break;;
  esac; sleep 1
done

# 3. install postgres from the companion (offline: drop online apt sources first).
log "install postgres from the companion (offline)"
docker exec "$HOST" bash -c 'rm -f /etc/apt/sources.list /etc/apt/sources.list.d/*.sources /etc/apt/sources.list.d/*.list 2>/dev/null || true'
docker exec "$HOST" bash -c "
  set -e
  mkdir -p /root/inst && cd /root/inst
  tar xzf /artifacts/aireos-postgres-ubuntu2404.tar.gz
  cd aireos-postgres-ubuntu2404
  COCKPIT_PW=$DBPW ./install-postgres.sh"

# 4. install the app stack — REAL install.sh with systemctl ENABLED.
log "install + start the app stack under systemd"
docker exec "$HOST" bash -c "
  set -e
  cd /root/inst && tar xzf /artifacts/aireos-offline-*.tar.gz
  cd \$(ls -d aireos-offline-*/ | head -1)
  AIREOS_DB_URL='postgres://cockpit:$DBPW@localhost:5432/cockpit?sslmode=disable' \
  AIREOS_BOOTSTRAP_OPERATOR_USER=admin AIREOS_BOOTSTRAP_OPERATOR_PASSWORD=$OPPW \
  AIREOS_SITE_ADDRESS=localhost ./install.sh"

# 5. assertions.
log "assert: units active"
docker exec "$HOST" systemctl is-active aireos-api aireos-audit aireos-broker aireos-caddy postgresql
log "assert: Caddy serves the SPA over HTTPS"
docker exec "$HOST" bash -c 'curl -sk https://localhost/ | grep -qiE "<!doctype html|<html" && echo "SPA OK"'
log "assert: chain-integrity verify CLI (WD-1 step 9)"
docker exec "$HOST" bash -c 'set -a; . /etc/aireos/aireos.env; /opt/aireos/bin/audit-service verify'

log "T-021d-a PASS — installed stack is up under real systemd; verify CLI works."
