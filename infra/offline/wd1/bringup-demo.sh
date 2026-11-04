#!/usr/bin/env bash
# bringup-demo.sh — stand up a PERSISTENT installed v0.4.0 stack for the §human
# WD-4 walkthrough (docs/releases/v0.4.0/verification_runbook.md, Part 4). Same
# bring-up as run-wd1.sh (real install.sh under systemd-in-Docker) but: no teardown,
# Caddy published to https://localhost:8443, and fire-agent.sh staged in the host.
# Reuses the wd1 images built by `task wd1`.
#
#   bash infra/offline/wd1/bringup-demo.sh        # up at https://localhost:8443
#   docker exec aireos-demo-host fire-agent.sh    # drive an agent invocation
#   # teardown:
#   docker rm -f aireos-demo-host aireos-demo-sshd; docker network rm aireos-demo; docker volume rm aireos-demo-keys
#
# Prereqs: a from-HEAD bundle in dist/offline/aireos-offline-*.tar.gz (task
# offline-bundle, or the Dockerized build-bundle) + the postgres companion.
#
# --no-seed-resource (T-133b): skip the "seed the flagged ssh resource" step
# below. DEFAULT OFF — every existing caller (quickstart-shell-gate.sh, the
# §human WD-4 walkthrough) keeps getting a pre-seeded resource, byte-for-byte
# unchanged. This flag exists because that seed makes OnboardingChecklist's
# "Create loopback demo target" button permanently invisible
# (apps/web/src/components/OnboardingChecklist.tsx: the button only renders
# while !resourceExists) — the S-26 browser quickstart gate needs a stack with
# NO resource yet, so it can actually click that button.
set -euo pipefail
export MSYS_NO_PATHCONV=1
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
OUT="$REPO_ROOT/dist/offline"
HOST=aireos-demo-host
SSHD=aireos-demo-sshd
NET=aireos-demo
KEYVOL=aireos-demo-keys
DBPW=demosecret
OPPW=demo-operator-pw
RID=01900000-0000-7000-8000-0000000000e2
SEED_RESOURCE=1
NO_OPERATOR=0
DEMO=0
SETUP_CODE_FILE="${SETUP_CODE_FILE:-$REPO_ROOT/tests/smoke/.setup-code}"   # --no-operator: where the printed setup code is saved (git-ignored)
for arg in "$@"; do
  case "$arg" in
    --no-seed-resource) SEED_RESOURCE=0 ;;
    # T-175d: install through setup.sh with NO operator, so the first operator is made in the BROWSER (ADR-0078). Implies
    # nothing about the resource seed, but the seed's own steps need no operator, so both flags combine.
    --no-operator) NO_OPERATOR=1 ;;
    # T-176d: install through setup.sh WITH an environment operator (admin / $OPPW) and AIREOS_LOOPBACK_DEMO=yes, so the stack is
    # what a real operator has after `sudo ./setup.sh`: the demo account + a pending key prepared, the cockpit's "Create loopback
    # demo target" button ready. Combine with --no-seed-resource. (Without --no-operator the browser setup path is not used.)
    --demo) DEMO=1 ;;
    *) echo "unknown argument: $arg" >&2; exit 1 ;;
  esac
done
log() { printf '\033[1;36m[demo]\033[0m %s\n' "$*"; }
# psql as the cluster superuser (T-172b, ADR-0077): the source-built PostgreSQL has no `postgres` OS user and
# no `psql` on PATH; its service user is aireos-pg and its socket is /run/aireos-postgres (peer auth).
hostpsql() { docker exec -i "$HOST" su -s /bin/bash aireos-pg -c "$(printf '%q ' /opt/aireos/pgsql/bin/psql -h /run/aireos-postgres -U aireos-pg "$@")"; }

# assert_exactly_one_bundle — OI-37's cousin finding (T-133a, Commander audit
# finding B): a bare `aireos-offline-*.tar.gz` glob, and this script's own
# `ls -d aireos-offline-*/ | head -1` further down, silently pick the FIRST
# match if two bundle versions sit in dist/offline at once (e.g. a stale
# build left behind by a prior release). Ambiguous selection that resolves
# quietly is the same family of defect as a stale default — fail loud and
# name every candidate instead.
assert_exactly_one_bundle() {
  local matches=("$OUT"/aireos-offline-*.tar.gz)
  if [ ! -e "${matches[0]}" ]; then
    echo "missing a from-HEAD bundle in $OUT — run task offline-bundle (or the Dockerized build-bundle) first"
    exit 1
  fi
  if [ "${#matches[@]}" -gt 1 ]; then
    echo "ambiguous bundle selection in $OUT — expected exactly ONE aireos-offline-*.tar.gz, found ${#matches[@]}: ${matches[*]} — remove the stale one(s) before running $(basename "$0")"
    exit 1
  fi
}

log "tear down any previous demo stack"
docker rm -f "$HOST" "$SSHD" >/dev/null 2>&1 || true
docker volume rm "$KEYVOL" >/dev/null 2>&1 || true
docker network rm "$NET" >/dev/null 2>&1 || true

assert_exactly_one_bundle
ls "$OUT"/aireos-postgres-ubuntu2404.tar.gz >/dev/null || { echo "missing postgres companion in $OUT"; exit 1; }

docker network create "$NET" >/dev/null
docker volume create "$KEYVOL" >/dev/null

log "generate ssh keys (keys-only seed)"
docker run --rm -v "$REPO_ROOT":/work -w /work -v aireos_gomodcache:/go/pkg/mod -v "$KEYVOL":/seed-keys \
  -e SEED_KEYS_ONLY=1 -e SEED_KEY_DIR=/seed-keys -e AIREOS_RESOURCE_SECRET_KEY=unused -e DATABASE_URL=unused \
  golang:1.26 bash -c 'go run ./tests/e2e/cmd/seed' >/dev/null

log "start test sshd"
docker run -d --name "$SSHD" --network "$NET" --network-alias "$SSHD" \
  -v "$KEYVOL":/seed-keys:ro aireos-wd1-sshd:latest >/dev/null

log "start systemd host (Caddy published -> https://localhost:8443)"
docker run -d --name "$HOST" --network "$NET" --privileged --cgroupns=host \
  --tmpfs /run --tmpfs /run/lock -v /sys/fs/cgroup:/sys/fs/cgroup:rw \
  -p 8443:443 \
  -v "$OUT":/artifacts:ro -v "$KEYVOL":/demokeys:ro aireos-wd1-host:latest >/dev/null
for _ in $(seq 1 30); do
  case "$(docker exec "$HOST" systemctl is-system-running 2>&1 || true)" in running|degraded) break;; esac; sleep 1
done

log "install postgres (offline)"
docker exec "$HOST" bash -c 'rm -f /etc/apt/sources.list /etc/apt/sources.list.d/*.sources /etc/apt/sources.list.d/*.list 2>/dev/null || true'
if [ "$NO_OPERATOR" = "1" ] || [ "$DEMO" = "1" ]; then
  # T-175d: the REAL one-command path with NO operator anywhere -- setup.sh installs the companion and the app, api starts in setup
  # mode, and setup.sh prints the one-time code (which only a root shell on the host ever sees). The code is saved for the browser run.
  # T-176d: --demo adds AIREOS_LOOPBACK_DEMO=yes, and (without --no-operator) an environment operator, so no setup code is issued.
  SETUP_ENV="AIREOS_SITE_ADDRESS='localhost $HOST' AIREOS_DEPLOYMENT_PROFILE=lan-only"
  [ "$DEMO" = "1" ] && SETUP_ENV="$SETUP_ENV AIREOS_LOOPBACK_DEMO=yes"
  [ "$NO_OPERATOR" = "1" ] || SETUP_ENV="$SETUP_ENV AIREOS_BOOTSTRAP_OPERATOR_USER=admin AIREOS_BOOTSTRAP_OPERATOR_PASSWORD=$OPPW"
  log "setup.sh (companion + app; demo=$DEMO, browser-setup-operator=$NO_OPERATOR)"
  SETUP_OUT="$(docker exec "$HOST" bash -c "set -e; mkdir -p /root/inst && cd /root/inst && cp /artifacts/aireos-postgres-ubuntu2404.tar.gz . &&
    tar xzf /artifacts/aireos-offline-*.tar.gz && $SETUP_ENV ./aireos-offline-*/setup.sh" 2>&1)" \
    || { echo "$SETUP_OUT" | tail -20; exit 1; }
  echo "$SETUP_OUT" | tail -8
  if [ "$NO_OPERATOR" = "1" ]; then
    CODE="$(grep -Eo '[0-9A-Z]{5}(-[0-9A-Z]{5}){3}' <<<"$SETUP_OUT" || true)"
    [ -n "$CODE" ] && [ "$(wc -l <<<"$CODE")" = 1 ] || { echo "$SETUP_OUT" | tail -20; echo "expected exactly one setup code in setup.sh's output"; exit 1; }
    mkdir -p "$(dirname "$SETUP_CODE_FILE")"; ( umask 077; printf '%s\n' "$CODE" > "$SETUP_CODE_FILE" )
    log "setup.sh printed a setup code (saved to $SETUP_CODE_FILE for the browser run)"
  fi
else
docker exec "$HOST" bash -c "set -e; mkdir -p /root/inst && cd /root/inst &&
  tar xzf /artifacts/aireos-postgres-ubuntu2404.tar.gz && cd aireos-postgres-ubuntu2404 &&
  COCKPIT_PW=$DBPW ./install-postgres.sh"

log "install + start the app stack under systemd (real install.sh)"
docker exec "$HOST" bash -c "set -e; cd /root/inst && tar xzf /artifacts/aireos-offline-*.tar.gz &&
  cd \$(ls -d aireos-offline-*/ | head -1) &&
  AIREOS_DB_URL='postgres://cockpit:$DBPW@localhost:5432/cockpit?sslmode=disable' \
  AIREOS_BOOTSTRAP_OPERATOR_USER=admin AIREOS_BOOTSTRAP_OPERATOR_PASSWORD=$OPPW \
  AIREOS_SITE_ADDRESS='localhost $HOST' AIREOS_DEPLOYMENT_PROFILE=lan-only ./install.sh"
fi

if [ "$SEED_RESOURCE" = "1" ]; then
  log "seed the flagged ssh resource (requires_approval=true)"
  HOSTKEY="$(docker exec "$HOST" cat /demokeys/ssh_host_key_pub)"
  LABELS="$(printf '{"ssh_host":"%s","ssh_port":"2222","ssh_user":"aireos","ssh_host_key":"%s","requires_approval":"true"}' "$SSHD" "$HOSTKEY")"
  hostpsql -v ON_ERROR_STOP=1 -d cockpit <<SQL
INSERT INTO resources (id, type, name, labels)
VALUES ('$RID', 'ssh', 'demo-target', '$LABELS'::jsonb)
ON CONFLICT (id) DO UPDATE SET labels = EXCLUDED.labels;
SQL
  docker exec "$HOST" bash -c 'DU=$(grep "^DATABASE_URL=" /etc/aireos/aireos.env | cut -d= -f2-); RK=$(grep "^AIREOS_RESOURCE_SECRET_KEY=" /etc/aireos/connector.env | cut -d= -f2-); DATABASE_URL="$DU" AIREOS_RESOURCE_SECRET_KEY="$RK" /opt/aireos/bin/connector-service credentials set-secret '"$RID"' --key-file /demokeys/client_ed25519'
else
  log "--no-seed-resource: skipping the flagged ssh resource seed (T-133b: leaves the stack with NO resource, so the cockpit's own create action can be exercised)"
fi

log "stage fire-agent.sh into the host (via stdin — docker cp mangles Windows paths)"
docker exec -i "$HOST" tee /usr/local/bin/fire-agent.sh >/dev/null < "$SCRIPT_DIR/fire-agent.sh"
docker exec "$HOST" chmod +x /usr/local/bin/fire-agent.sh

log "assert all units active"
docker exec "$HOST" systemctl is-active aireos-api aireos-audit aireos-broker aireos-connector aireos-policy aireos-mcp-gateway aireos-caddy aireos-postgres

if [ "$NO_OPERATOR" = "1" ]; then log "DEMO STACK UP, NO OPERATOR (setup mode) -> https://localhost:8443  (setup code in $SETUP_CODE_FILE)"; else log "DEMO STACK UP -> https://localhost:8443  (operator: admin / $OPPW)"; fi
