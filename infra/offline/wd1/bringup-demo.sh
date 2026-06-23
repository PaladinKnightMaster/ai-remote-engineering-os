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
# Prereqs: a from-HEAD bundle in dist/offline/aireos-offline-0.4.0.tar.gz (task
# offline-bundle, or the Dockerized build-bundle) + the postgres companion.
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
log() { printf '\033[1;36m[demo]\033[0m %s\n' "$*"; }

log "tear down any previous demo stack"
docker rm -f "$HOST" "$SSHD" >/dev/null 2>&1 || true
docker volume rm "$KEYVOL" >/dev/null 2>&1 || true
docker network rm "$NET" >/dev/null 2>&1 || true

ls "$OUT"/aireos-offline-0.4.0.tar.gz >/dev/null || { echo "missing 0.4.0 bundle in $OUT — run task offline-bundle (or the Dockerized build-bundle) first"; exit 1; }
ls "$OUT"/aireos-postgres-ubuntu2404.tar.gz >/dev/null || { echo "missing postgres companion in $OUT"; exit 1; }

docker network create "$NET" >/dev/null
docker volume create "$KEYVOL" >/dev/null

log "generate ssh keys (keys-only seed)"
docker run --rm -v "$REPO_ROOT":/work -w /work -v aireos_gomodcache:/go/pkg/mod -v "$KEYVOL":/seed-keys \
  -e SEED_KEYS_ONLY=1 -e SEED_KEY_DIR=/seed-keys -e AIREOS_RESOURCE_SECRET_KEY=unused -e DATABASE_URL=unused \
  golang:1.23 bash -c 'go run ./tests/e2e/cmd/seed' >/dev/null

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
docker exec "$HOST" bash -c "set -e; mkdir -p /root/inst && cd /root/inst &&
  tar xzf /artifacts/aireos-postgres-ubuntu2404.tar.gz && cd aireos-postgres-ubuntu2404 &&
  COCKPIT_PW=$DBPW ./install-postgres.sh"

log "install + start the app stack under systemd (real install.sh)"
docker exec "$HOST" bash -c "set -e; cd /root/inst && tar xzf /artifacts/aireos-offline-0.4.0.tar.gz &&
  cd \$(ls -d aireos-offline-*/ | head -1) &&
  AIREOS_DB_URL='postgres://cockpit:$DBPW@localhost:5432/cockpit?sslmode=disable' \
  AIREOS_BOOTSTRAP_OPERATOR_USER=admin AIREOS_BOOTSTRAP_OPERATOR_PASSWORD=$OPPW \
  AIREOS_SITE_ADDRESS='localhost $HOST' ./install.sh"

log "seed the flagged ssh resource (requires_approval=true)"
HOSTKEY="$(docker exec "$HOST" cat /demokeys/ssh_host_key_pub)"
LABELS="$(printf '{"ssh_host":"%s","ssh_port":"2222","ssh_user":"aireos","ssh_host_key":"%s","requires_approval":"true"}' "$SSHD" "$HOSTKEY")"
docker exec -i "$HOST" su - postgres -c "psql -v ON_ERROR_STOP=1 -d cockpit" <<SQL
INSERT INTO resources (id, type, name, labels)
VALUES ('$RID', 'ssh', 'demo-target', '$LABELS'::jsonb)
ON CONFLICT (id) DO UPDATE SET labels = EXCLUDED.labels;
SQL
docker exec "$HOST" bash -c 'DU=$(grep "^DATABASE_URL=" /etc/aireos/aireos.env | cut -d= -f2-); RK=$(grep "^AIREOS_RESOURCE_SECRET_KEY=" /etc/aireos/connector.env | cut -d= -f2-); DATABASE_URL="$DU" AIREOS_RESOURCE_SECRET_KEY="$RK" /opt/aireos/bin/connector-service credentials set-secret '"$RID"' --key-file /demokeys/client_ed25519'

log "stage fire-agent.sh into the host (via stdin — docker cp mangles Windows paths)"
docker exec -i "$HOST" tee /usr/local/bin/fire-agent.sh >/dev/null < "$SCRIPT_DIR/fire-agent.sh"
docker exec "$HOST" chmod +x /usr/local/bin/fire-agent.sh

log "assert all units active"
docker exec "$HOST" systemctl is-active aireos-api aireos-audit aireos-broker aireos-connector aireos-policy aireos-mcp-gateway aireos-caddy postgresql

log "DEMO STACK UP -> https://localhost:8443  (operator: admin / $OPPW)"
