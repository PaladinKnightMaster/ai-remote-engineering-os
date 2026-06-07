#!/usr/bin/env bash
# run-wd1.sh — the WD-1 wedge demo against the INSTALLED stack under REAL systemd
# (T-021d). It brings up the real offline install on a systemd-in-Docker host + a
# test sshd, then drives the operator walkthrough through Caddy HTTPS and asserts
# the audit chain host-internally.
#
#   T-021d-a (bring-up): real install-postgres.sh + install.sh (systemctl enabled);
#     units + postgres active; Caddy serves the SPA over HTTPS; verify CLI works.
#   T-021d-b (walkthrough): login → resource picker → SSH session → command → end
#     → logout through Caddy HTTPS; then assert the chain is intact + carries
#     auth.login.success / opened / input.line / closed:operator / auth.logout.
#
# Build host needs Docker (Linux or Windows/WSL2 — systemd-in-Docker verified).
# Prereq artifacts: the core bundle + the ubuntu2404 postgres companion in
# dist/offline/ (build them first — see the error messages).
set -euo pipefail
export MSYS_NO_PATHCONV=1   # Windows/git-bash: don't mangle -v / cgroup paths

# --- TEMPORARY GUARD (T-030a → T-033) -----------------------------------------
# T-030a moved target-credential custody to connector-service and DELETED
# `api resources set-secret`, which this harness uses to provision the SSH
# credential on the installed host. WD-1 is therefore non-runnable until T-033 /
# OI-6 re-points offline provisioning to `connector credentials set-secret` (and
# wires CONNECTOR_URL + relocates AIREOS_RESOURCE_SECRET_KEY into the units).
# Fail loud now rather than 5 minutes into a systemd-in-Docker bring-up.
# T-033 REMOVES this guard as part of re-enabling WD-1.
echo "[wd1] ABORT: WD-1 is temporarily non-runnable. Credential provisioning is" >&2
echo "[wd1] migrating from 'api resources set-secret' to 'connector credentials" >&2
echo "[wd1] set-secret' (T-030a done; re-pointed in T-033/OI-6). See docs/tickets/README.md." >&2
exit 1
# --- END TEMPORARY GUARD ------------------------------------------------------

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
OUT="$REPO_ROOT/dist/offline"
HOST=aireos-wd1-host
SSHD=aireos-wd1-sshd
NET=aireos-wd1
KEYVOL=aireos-wd1-keys
DBPW=wd1secret
OPPW=e2e-operator-pw   # must match operatorPass in tests/e2e/harness_test.go
RID=01900000-0000-7000-8000-0000000000e2   # must match tests/e2e/cmd/seed + harness_test.go

log() { printf '\033[1;35m[wd1]\033[0m %s\n' "$*"; }
cleanup() {
  docker rm -f "$HOST" "$SSHD" >/dev/null 2>&1 || true
  docker volume rm "$KEYVOL" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
}
trap cleanup EXIT

# 1. require artifacts.
ls "$OUT"/aireos-offline-*.tar.gz >/dev/null 2>&1 \
  || { echo "missing core bundle — build it (see T-021a)"; exit 1; }
ls "$OUT"/aireos-postgres-ubuntu2404.tar.gz >/dev/null 2>&1 \
  || { echo "missing postgres companion — bash infra/offline/postgres/build-postgres-companions.sh ubuntu2404"; exit 1; }

# 2. images (relative build contexts: portable under MSYS_NO_PATHCONV).
log "build host + sshd images"
( cd "$REPO_ROOT/infra/offline/wd1" && docker build -t aireos-wd1-host:latest -f Dockerfile.host . >/dev/null )
( cd "$REPO_ROOT/infra/docker-compose/e2e" && docker build -t aireos-wd1-sshd:latest -f Dockerfile.sshd . >/dev/null )

cleanup
docker network create "$NET" >/dev/null
docker volume create "$KEYVOL" >/dev/null

# 3. generate the SSH keys (keys-only seed → KEYVOL); the DB insert is skipped.
log "generate ssh keys (keys-only seed)"
docker run --rm -v "$REPO_ROOT":/work -w /work -v aireos_gomodcache:/go/pkg/mod -v "$KEYVOL":/seed-keys \
  -e SEED_KEYS_ONLY=1 -e SEED_KEY_DIR=/seed-keys -e AIREOS_RESOURCE_SECRET_KEY=unused -e DATABASE_URL=unused \
  golang:1.23 bash -c 'go run ./tests/e2e/cmd/seed' >/dev/null

# 4. test sshd target (reads its host key + authorized client key from KEYVOL).
log "start test sshd"
docker run -d --name "$SSHD" --network "$NET" --network-alias "$SSHD" \
  -v "$KEYVOL":/seed-keys:ro aireos-wd1-sshd:latest >/dev/null

# 5. systemd host (privileged, cgroup v2); mount the artifacts + the key volume.
log "start systemd host"
docker run -d --name "$HOST" --network "$NET" --privileged --cgroupns=host \
  --tmpfs /run --tmpfs /run/lock -v /sys/fs/cgroup:/sys/fs/cgroup:rw \
  -v "$OUT":/artifacts:ro -v "$KEYVOL":/wd1keys:ro aireos-wd1-host:latest >/dev/null
for i in $(seq 1 30); do
  case "$(docker exec "$HOST" systemctl is-system-running 2>&1 || true)" in running|degraded) break;; esac; sleep 1
done

# 6. install postgres from the companion (offline: drop online apt sources first).
log "install postgres (offline)"
docker exec "$HOST" bash -c 'rm -f /etc/apt/sources.list /etc/apt/sources.list.d/*.sources /etc/apt/sources.list.d/*.list 2>/dev/null || true'
docker exec "$HOST" bash -c "set -e; mkdir -p /root/inst && cd /root/inst &&
  tar xzf /artifacts/aireos-postgres-ubuntu2404.tar.gz && cd aireos-postgres-ubuntu2404 &&
  COCKPIT_PW=$DBPW ./install-postgres.sh"

# 7. install + start the app stack — REAL install.sh, systemctl enabled. Caddy
#    serves both localhost (in-container checks) and the host name (the driver).
log "install + start the app stack under systemd"
docker exec "$HOST" bash -c "set -e; cd /root/inst && tar xzf /artifacts/aireos-offline-*.tar.gz &&
  cd \$(ls -d aireos-offline-*/ | head -1) &&
  AIREOS_DB_URL='postgres://cockpit:$DBPW@localhost:5432/cockpit?sslmode=disable' \
  AIREOS_BOOTSTRAP_OPERATOR_USER=admin AIREOS_BOOTSTRAP_OPERATOR_PASSWORD=$OPPW \
  AIREOS_SITE_ADDRESS='localhost $HOST' ./install.sh"

# 8. seed the ssh resource into the installed DB (psql row + `api set-secret`).
log "seed the ssh resource in the installed DB"
HOSTKEY="$(docker exec "$HOST" cat /wd1keys/ssh_host_key_pub)"
LABELS="$(printf '{"ssh_host":"%s","ssh_port":"2222","ssh_user":"aireos","ssh_host_key":"%s"}' "$SSHD" "$HOSTKEY")"
docker exec -i "$HOST" su - postgres -c "psql -v ON_ERROR_STOP=1 -d cockpit" <<SQL
INSERT INTO resources (id, type, name, labels)
VALUES ('$RID', 'ssh', 'wd1-target', '$LABELS'::jsonb)
ON CONFLICT (id) DO UPDATE SET labels = EXCLUDED.labels;
SQL
# Read only the two vars set-secret needs (don't source the whole env file — the
# site-address value contains a space and the operator hash contains '$', neither
# of which survives bash `source`; systemd's EnvironmentFile parser handles both).
docker exec "$HOST" bash -c 'DU=$(grep "^DATABASE_URL=" /etc/aireos/aireos.env | cut -d= -f2-); RK=$(grep "^AIREOS_RESOURCE_SECRET_KEY=" /etc/aireos/aireos.env | cut -d= -f2-); DATABASE_URL="$DU" AIREOS_RESOURCE_SECRET_KEY="$RK" /opt/aireos/bin/api resources set-secret '"$RID"' --key-file /wd1keys/client_ed25519'

# 9. bring-up assertions (T-021d-a).
log "assert: units active + Caddy serves the SPA over HTTPS"
docker exec "$HOST" systemctl is-active aireos-api aireos-audit aireos-broker aireos-caddy postgresql
docker exec "$HOST" bash -c 'curl -sk https://localhost/ | grep -qiE "<!doctype html|<html" && echo "SPA OK"'

# 10. WD-1 walkthrough driver, through Caddy HTTPS, on the network by host name.
log "run the WD-1 walkthrough driver (Caddy HTTPS)"
docker run --rm --network "$NET" -v "$REPO_ROOT":/work -w /work -v aireos_gomodcache:/go/pkg/mod \
  -e WD1_BASE_URL="https://$HOST" \
  golang:1.23 bash -c 'go test -tags wd1 -count=1 -v -timeout 300s ./tests/e2e/...' 2>&1 | tail -15

# 11. assert the audit chain host-internally (WD-1 step 8 events + step 9 integrity).
log "assert: audit chain intact + the WD-1 events are present"
docker exec "$HOST" bash -c 'DU=$(grep "^DATABASE_URL=" /etc/aireos/aireos.env | cut -d= -f2-); DATABASE_URL="$DU" /opt/aireos/bin/audit-service verify'
OUTCOMES="$(docker exec "$HOST" su - postgres -c "psql -tA -d cockpit -c \"SELECT outcome FROM audit_events ORDER BY seq\"")"
for want in auth.login.success opened input.line closed:operator auth.logout; do
  echo "$OUTCOMES" | grep -qx "$want" || { echo "MISSING audit outcome: $want"; echo "chain: $OUTCOMES"; exit 1; }
done

log "T-021d-b PASS — WD-1 wedge demo end-to-end on the installed stack via Caddy HTTPS; chain intact + complete."
