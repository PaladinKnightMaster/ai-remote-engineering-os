#!/usr/bin/env bash
# run-wd1.sh — the WD-2 wedge demo against the INSTALLED stack under REAL systemd
# (T-021d bring-up; walkthrough extended to the governed-egress wedge by T-033b).
# It brings up the real offline install on a systemd-in-Docker host + a test sshd,
# then drives the operator walkthrough through Caddy HTTPS and asserts the audit
# chain host-internally.
#
#   Bring-up (T-021d-a): real install-postgres.sh + install.sh (systemctl enabled);
#     all six units + postgres active; Caddy serves the SPA over HTTPS.
#   Walkthrough (T-021d-b + T-033b): login → resource picker → create against the
#     requires_approval-flagged resource → operator APPROVE → SSH session →
#     command → end → DENY a second session → logout, through Caddy HTTPS; then
#     assert the chain is intact + carries auth.login.success / the policy
#     decision + approval outcomes / credential.leased / opened / input.line /
#     closed:operator / auth.logout. WD-2 supersedes WD-1 (release-criteria §2).
#
# Build host needs Docker (Linux or Windows/WSL2 — systemd-in-Docker verified).
# Prereq artifacts: the core bundle (REBUILT FROM HEAD — dist/ goes stale) + the
# ubuntu2404 postgres companion in dist/offline/ (see the error messages).
set -euo pipefail
export MSYS_NO_PATHCONV=1   # Windows/git-bash: don't mangle -v / cgroup paths

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

# 8. seed the ssh resource into the installed DB. requires_approval=true: the
#    installed walkthrough drives the GOVERNED wedge (WD-2.3) — the ungated path
#    stays covered by the compose e2e (T-033a). Credential provisioning goes
#    through connector-service (the sole custodian, OI-6): the key is read from
#    connector.env — aireos.env no longer carries it.
log "seed the flagged ssh resource in the installed DB"
HOSTKEY="$(docker exec "$HOST" cat /wd1keys/ssh_host_key_pub)"
LABELS="$(printf '{"ssh_host":"%s","ssh_port":"2222","ssh_user":"aireos","ssh_host_key":"%s","requires_approval":"true"}' "$SSHD" "$HOSTKEY")"
docker exec -i "$HOST" su - postgres -c "psql -v ON_ERROR_STOP=1 -d cockpit" <<SQL
INSERT INTO resources (id, type, name, labels)
VALUES ('$RID', 'ssh', 'wd1-target', '$LABELS'::jsonb)
ON CONFLICT (id) DO UPDATE SET labels = EXCLUDED.labels;
SQL
# Read only the two vars set-secret needs (don't source the whole env file — the
# site-address value contains a space and the operator hash contains '$', neither
# of which survives bash `source`; systemd's EnvironmentFile parser handles both).
docker exec "$HOST" bash -c 'DU=$(grep "^DATABASE_URL=" /etc/aireos/aireos.env | cut -d= -f2-); RK=$(grep "^AIREOS_RESOURCE_SECRET_KEY=" /etc/aireos/connector.env | cut -d= -f2-); DATABASE_URL="$DU" AIREOS_RESOURCE_SECRET_KEY="$RK" /opt/aireos/bin/connector-service credentials set-secret '"$RID"' --key-file /wd1keys/client_ed25519'

# 9. bring-up assertions (T-021d-a; six units since OI-6).
log "assert: units active + Caddy serves the SPA over HTTPS"
docker exec "$HOST" systemctl is-active aireos-api aireos-audit aireos-broker aireos-connector aireos-policy aireos-mcp-gateway aireos-caddy postgresql
docker exec "$HOST" bash -c 'curl -sk https://localhost/ | grep -qiE "<!doctype html|<html" && echo "SPA OK"'

# 9b. SEC-20 (v0.4.0/ADR-0060): assert install.sh keyed the agent-facing gateway as
#     KEYLESS of the cluster — gateway.env carries its own sign key + the F3 command
#     key + api's public key, but NOT AIREOS_PASETO_KEY, and the unit loads gateway.env
#     (not aireos.env). This is the installed-stack analogue of the e2e SEC-21 negative
#     test (the gateway has no cluster key, so it can forge no foreign-edge token).
log "assert: the installed gateway is keyless (SEC-20)"
docker exec "$HOST" bash -c '
  set -e
  grep -q "^AIREOS_GATEWAY_SIGN_KEY=" /etc/aireos/gateway.env || { echo "gateway.env missing AIREOS_GATEWAY_SIGN_KEY"; exit 1; }
  grep -q "^AIREOS_GATEWAY_COMMAND_KEY=" /etc/aireos/gateway.env || { echo "gateway.env missing AIREOS_GATEWAY_COMMAND_KEY (F3)"; exit 1; }
  grep -q "^AIREOS_API_AGENT_PUBLIC_KEY=" /etc/aireos/gateway.env || { echo "gateway.env missing AIREOS_API_AGENT_PUBLIC_KEY"; exit 1; }
  grep -q "^AIREOS_API_AGENT_SIGN_KEY=" /etc/aireos/api.env || { echo "api.env missing AIREOS_API_AGENT_SIGN_KEY"; exit 1; }
  if grep -q "^AIREOS_PASETO_KEY" /etc/aireos/gateway.env; then echo "SEC-20 VIOLATION — gateway.env carries the cluster key"; exit 1; fi
  grep -q "^EnvironmentFile=/etc/aireos/gateway.env" /etc/systemd/system/aireos-mcp-gateway.service || { echo "gateway unit does not load gateway.env"; exit 1; }
  if grep -q "^EnvironmentFile=/etc/aireos/aireos.env" /etc/systemd/system/aireos-mcp-gateway.service; then echo "SEC-20 VIOLATION — gateway unit loads aireos.env (cluster key)"; exit 1; fi
  echo "SEC-20 OK — gateway keyless: own sign + F3 command + api-public; no cluster key; loads gateway.env only"
'

# 10. WD-1 walkthrough driver, through Caddy HTTPS, on the network by host name.
log "run the WD-1 walkthrough driver (Caddy HTTPS)"
docker run --rm --network "$NET" -v "$REPO_ROOT":/work -w /work -v aireos_gomodcache:/go/pkg/mod \
  -e WD1_BASE_URL="https://$HOST" \
  golang:1.23 bash -c 'go test -tags wd1 -count=1 -v -timeout 600s ./tests/e2e/...' 2>&1 | tail -18

# 11. assert the audit chain host-internally (WD-2.4: integrity + the governed
#     wedge events in one chain). Retry: policy/connector relays deliver via
#     LISTEN/NOTIFY and may lag the driver by a beat.
log "assert: audit chain intact + the WD-2 events are present"
docker exec "$HOST" bash -c 'DU=$(grep "^DATABASE_URL=" /etc/aireos/aireos.env | cut -d= -f2-); DATABASE_URL="$DU" /opt/aireos/bin/audit-service verify'
# Includes the WD-5 Run lifecycle events (OI-12, T-061): run.launched + run.completed
# land via api's in-process outbox relay, proving the Run plane drives on the install.
# v0.6 (OI-13, T-069) adds the delegation lifecycle: grant.issued/grant.revoked (the
# operator's authorization + its withdrawal) and run.canceled (the revoke cascade,
# DUR-7) — all via the same api relay.
# v0.7 (OI-14, T-077) adds the chartered-Workstream lifecycle: workstream.chartered (the
# ONE consent act, actor=operator) + workstream.retired (the operator kill switch) — the
# AUD-18 events, delivered via the same api relay. The tick's own run.launched/completed
# are already in WANTS; the schedule-fired run's workstream:<id> actor is asserted below.
WANTS="auth.login.success policy.decision:requires_approval approval.granted credential.leased opened input.line closed:operator approval.denied:operator auth.logout tool.invoked remote.exec tool.result run.launched run.completed grant.issued grant.revoked run.canceled workstream.chartered workstream.retired"
for attempt in 1 2 3 4 5 6; do
  OUTCOMES="$(docker exec "$HOST" su - postgres -c "psql -tA -d cockpit -c \"SELECT outcome FROM audit_events ORDER BY seq\"")"
  MISSING=""
  for want in $WANTS; do
    echo "$OUTCOMES" | grep -qx "$want" || MISSING="$MISSING $want"
  done
  [ -z "$MISSING" ] && break
  [ "$attempt" = 6 ] && { echo "MISSING audit outcome(s):$MISSING"; echo "chain: $OUTCOMES"; exit 1; }
  sleep 5
done

# v0.6 (OI-13): the DELEGATED launch must be attributed to the AGENT actor in the
# installed chain (AUD-17 — distinguishable from WD-5's operator launch). grant.revoked
# rides the same ordered api relay and was waited for above, so the earlier delegated
# run.launched is already delivered; retry a beat anyway.
for attempt in 1 2 3; do
  AGENT_LAUNCHED="$(docker exec "$HOST" su - postgres -c "psql -tA -d cockpit -c \"SELECT count(*) FROM audit_events WHERE outcome='run.launched' AND actor LIKE 'agent:%'\"")"
  [ "${AGENT_LAUNCHED:-0}" -ge 1 ] && break
  [ "$attempt" = 3 ] && { echo "no agent-actor run.launched in the installed chain — delegated attribution (AUD-17) missing"; exit 1; }
  sleep 5
done

# v0.7 (OI-14): the SCHEDULE-fired Run must be attributed to the WORKSTREAM actor in the
# installed chain (AUD-17/AUD-18 — distinguishable from WD-5's operator and WD-6's agent
# launches). Proves the in-process ticker fired a governed Run on the real install.
for attempt in 1 2 3; do
  WS_LAUNCHED="$(docker exec "$HOST" su - postgres -c "psql -tA -d cockpit -c \"SELECT count(*) FROM audit_events WHERE outcome='run.launched' AND actor LIKE 'workstream:%'\"")"
  [ "${WS_LAUNCHED:-0}" -ge 1 ] && break
  [ "$attempt" = 3 ] && { echo "no workstream-actor run.launched in the installed chain — the schedule ticker did not fire (OI-14)"; exit 1; }
  sleep 5
done

log "T-053a/T-061/T-069/T-077 PASS — WD-4 + WD-5 + WD-6 + WD-7 on the installed stack via Caddy HTTPS: WD-2 session approval + governed lease + deny leg, the agent surface (Caddy /mcp → gateway block-and-wait → operator approve → broker exec) driven by a KEYLESS gateway (SEC-20), the WD-5 Run plane (OI-12), the WD-6 delegated-launch plane (OI-13), AND the WD-7 chartered-Workstream plane (OI-14: operator charter → the in-process ticker fires a governed Run on the schedule with nobody present → the flagged step parks with the workstream context → operator approve → completed → retire; the schedule floor is the 60s production default (no new env; the ticker rides the OI-13 GATEWAY_URL, migration 0011 rides the migrate loop); chain intact + complete (incl. workstream.chartered/workstream.retired + workstream-actor run.launched)."
