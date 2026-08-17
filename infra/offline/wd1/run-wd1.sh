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
# CTXVOL (T-102, OI-17): a harness-owned volume mounted at the SAME path on BOTH
# $HOST and the Go test-runner container, standing in for "a real host directory
# the installed api process can read" — api runs as a systemd SERVICE inside $HOST
# (no bind-mounted repo checkout there, unlike compose e2e's shared x-go anchor), and
# the test-runner container has no other filesystem access to $HOST at all. This is
# wd1 TEST-HARNESS wiring only (this script) — it is never install.sh/systemd/Caddy/
# env, so it does not touch the OI-17 zero-install-wiring-diff claim.
CTXVOL=aireos-wd1-context
CTXPATH=/wd1-context
DBPW=wd1secret
OPPW=e2e-operator-pw   # must match operatorPass in tests/e2e/harness_test.go
RID=01900000-0000-7000-8000-0000000000e2   # must match tests/e2e/cmd/seed + harness_test.go

log() { printf '\033[1;35m[wd1]\033[0m %s\n' "$*"; }
cleanup() {
  docker rm -f "$HOST" "$SSHD" >/dev/null 2>&1 || true
  docker volume rm "$KEYVOL" "$CTXVOL" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
  rm -f "$OUT/wd1-caddy-ca.pem" 2>/dev/null || true
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
docker volume create "$CTXVOL" >/dev/null

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
  -v "$OUT":/artifacts:ro -v "$KEYVOL":/wd1keys:ro -v "$CTXVOL":"$CTXPATH" aireos-wd1-host:latest >/dev/null
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
  AIREOS_SITE_ADDRESS='localhost $HOST' AIREOS_DEPLOYMENT_PROFILE=lan-only ./install.sh"

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

# 9d. E-03 (ADR-0068 §D1; OI-21/OI-23): assert the installed aireos.env
#     persisted the deployment profile — the installed-stack proof that the
#     profile survives past acceptance, not just that install.sh accepted it
#     (that acceptance is proven separately by install_profile_test.sh).
log "assert: the installed aireos.env persisted AIREOS_DEPLOYMENT_PROFILE=lan-only"
docker exec "$HOST" bash -c '
  grep -q "^AIREOS_DEPLOYMENT_PROFILE=lan-only$" /etc/aireos/aireos.env \
    || { echo "aireos.env missing AIREOS_DEPLOYMENT_PROFILE=lan-only"; exit 1; }
  echo "OI-21/OI-23 OK — deployment profile persisted in aireos.env"
'

# 9e. WD-11 (T-122a, ADR-0068 §D10/SEC-41): pairing ships OFF by default. Assert
#     the off state answers 404 (no oracle) on THIS stack rather than assuming
#     compose's proof of the same posture carries over, THEN turn it on the way
#     an operator actually would — a re-run of the real install.sh with
#     AIREOS_PAIRING_ENABLED=1 — and restart aireos-api.service so the new env
#     takes effect (a running process only reads AIREOS_PAIRING_ENABLED at boot,
#     services/api/main.go; install.sh's own `systemctl enable --now` is a no-op
#     on an already-active unit). This is the enable path GATE-4's §human
#     walkthrough will also exercise; proving it here is what lets the WD-11 Go
#     test below assume pairing is live.
log "assert: pairing routes 404 while AIREOS_PAIRING_ENABLED is off (SEC-41)"
docker exec "$HOST" bash -c '
  # redeemPairingRequest declares security:[] (public — the redeeming device has
  # no session yet, ADR-0068 §D6), so it reaches pairingDisabled 404 with NO
  # capability token needed, unlike CreatePairingRequest (gated by
  # RequireCapability first, so an unauthenticated probe there 401s before ever
  # reaching the pairing-off check — that is a DIFFERENT gate, not this one).
  code="$(curl -sk -o /dev/null -w "%{http_code}" -X POST https://localhost/v1/pairing-requests/probe-id/redeem -H "Content-Type: application/json" -d "{\"secret\":\"x\"}")"
  [ "$code" = "404" ] || { echo "SEC-41 VIOLATION — POST /v1/pairing-requests/{id}/redeem with pairing off = $code, want 404"; exit 1; }
  echo "SEC-41 OK — pairing routes 404 while off"
'
log "enable pairing: re-run install.sh with AIREOS_PAIRING_ENABLED=1 (OI-23 idempotent re-run)"
docker exec "$HOST" bash -c "set -e; cd /root/inst && cd \$(ls -d aireos-offline-*/ | head -1) &&
  AIREOS_DB_URL='postgres://cockpit:$DBPW@localhost:5432/cockpit?sslmode=disable' \
  AIREOS_BOOTSTRAP_OPERATOR_USER=admin AIREOS_BOOTSTRAP_OPERATOR_PASSWORD=$OPPW \
  AIREOS_SITE_ADDRESS='localhost $HOST' AIREOS_DEPLOYMENT_PROFILE=lan-only AIREOS_PAIRING_ENABLED=1 ./install.sh"
docker exec "$HOST" systemctl restart aireos-api.service
for i in $(seq 1 30); do
  docker exec "$HOST" systemctl is-active --quiet aireos-api && break
  [ "$i" = 30 ] && { echo "aireos-api did not come back active after the pairing-enable restart"; exit 1; }
  sleep 1
done
log "assert: the re-run preserved the profile + persisted AIREOS_PAIRING_ENABLED=1"
docker exec "$HOST" bash -c '
  grep -q "^AIREOS_DEPLOYMENT_PROFILE=lan-only$" /etc/aireos/aireos.env || { echo "OI-23 VIOLATION — profile lost across the pairing-enable re-run"; exit 1; }
  grep -q "^AIREOS_PAIRING_ENABLED=1$" /etc/aireos/aireos.env || { echo "aireos.env missing AIREOS_PAIRING_ENABLED=1 after enabling"; exit 1; }
  echo "OI-23 OK — deployment profile survived the re-run; AIREOS_PAIRING_ENABLED=1 persisted"
'

# 9c. HYGIENE (T-084 security-hook flag): export Caddy's own local CA (`tls
#     internal`, persisted under XDG_DATA_HOME=/var/lib/aireos/caddy per the
#     aireos-caddy unit) so the go-test harness can REAL-verify the HTTPS
#     handshake instead of InsecureSkipVerify. Discover the root cert path
#     rather than hardcoding it — fail loud if it can't be found.
log "export Caddy's local CA root so the harness can verify (not skip) TLS"
CA_PATH="$(docker exec "$HOST" find / -xdev -name root.crt -path '*caddy*pki*' 2>/dev/null | head -1)"
if [ -z "$CA_PATH" ]; then
  echo "could not locate Caddy's local CA root.crt on $HOST (tls internal storage under /var/lib/aireos/caddy) — refusing to fall back to InsecureSkipVerify"
  exit 1
fi
log "found Caddy local CA at $HOST:$CA_PATH"
CA_FILE="$OUT/wd1-caddy-ca.pem"
docker exec "$HOST" cat "$CA_PATH" > "$CA_FILE"
[ -s "$CA_FILE" ] || { echo "exported Caddy CA file is empty ($CA_FILE)"; exit 1; }

# 10. WD-1 walkthrough driver, through Caddy HTTPS, on the network by host name.
#     WD1_CA_FILE points the harness at the exported CA so it trusts (rather
#     than skips) Caddy's self-signed cert — no InsecureSkipVerify anywhere.
log "run the WD-1 walkthrough driver (Caddy HTTPS, verified)"
docker run --rm --network "$NET" -v "$REPO_ROOT":/work -w /work -v aireos_gomodcache:/go/pkg/mod \
  -v "$CA_FILE":/wd1ca/root.crt:ro -v "$CTXVOL":"$CTXPATH" \
  -e WD1_BASE_URL="https://$HOST" -e WD1_CA_FILE=/wd1ca/root.crt -e WD1_CONTEXT_ROOT="$CTXPATH" \
  golang:1.23 bash -c 'go test -tags wd1 -count=1 -v -timeout 1500s ./tests/e2e/...' 2>&1 | tail -60

# 11. assert the audit chain host-internally (WD-2.4: integrity + the governed
#     wedge events in one chain). Retry: policy/connector relays deliver via
#     LISTEN/NOTIFY and may lag the driver by a beat.
log "assert: audit chain intact + ATTESTED (head signatures) + the WD-2 events are present"
# T-127a: this invocation used to export ONLY DATABASE_URL. T-126a taught
# `audit-service verify` to check Ed25519 head signatures (ADR-0069 D6), but with no
# key in its environment resolveVerifyKey() finds nothing and the CLI falls back to
# ordering-only — still exiting 0. So this gate went on passing while remaining blind
# to head signing, i.e. blind to the mechanism this whole release rests on: the tool
# got stronger and the caller kept invoking it in the weak mode.
#
# Export the chain-head PUBLIC key (it rides shared aireos.env from T-125a; the secret
# lives in audit.env and is deliberately NOT needed here — a verifier must never
# require signing material), and assert the LITERAL attested outcome rather than a bare
# exit 0, because exit 0 alone cannot distinguish an attested run from the
# ordering-only fallback.
# Force one checkpoint FIRST (ADR-0069 D3's explicit operator action), then verify.
# Without it the correct outcome is INTACT+UNATTESTED, not a failure: heads are signed
# on a bounded cadence (100 events / 60s, D2) and a short wd1 run finishes before the
# next tick, so the tail is legitimately unattested. `task e2e` already forces a
# checkpoint for exactly this reason; wd1 must too, or it asserts something the cadence
# design guarantees will not be true.
#
# NOTE THE PRIVILEGE SPLIT, it is deliberate: `checkpoint` SIGNS, so it needs the SECRET
# from audit.env (0600, audit-service's own env file). `verify` only CHECKS, so it gets
# the PUBLIC key from shared aireos.env and must never require signing material.
# EXTRACT the values, never `.`/source the env files: sourcing runs them as shell, so
# any line that is not a clean assignment gets EXECUTED (an earlier cut of this block
# sourced aireos.env and produced `line 8: aireos-wd1-host: command not found`). The
# original DATABASE_URL line used grep|cut for exactly this reason — a config file is
# data, not code, and in a release about evidence integrity we do not hand a config
# file to the shell.
docker exec "$HOST" bash -c '
  DU=$(grep "^DATABASE_URL=" /etc/aireos/aireos.env | head -1 | cut -d= -f2-)
  SK=$(grep "^AIREOS_AUDIT_CHAIN_SIGN_KEY=" /etc/aireos/audit.env | head -1 | cut -d= -f2-)
  DATABASE_URL="$DU" AIREOS_AUDIT_CHAIN_SIGN_KEY="$SK" /opt/aireos/bin/audit-service checkpoint' \
  || echo "wd1: forced checkpoint did not sign (see above) — the verify assertion below will catch it"
VERIFY_OUT="$(docker exec "$HOST" bash -c '
  DU=$(grep "^DATABASE_URL=" /etc/aireos/aireos.env | head -1 | cut -d= -f2-)
  PK=$(grep "^AIREOS_AUDIT_CHAIN_PUBLIC_KEY=" /etc/aireos/aireos.env | head -1 | cut -d= -f2-)
  DATABASE_URL="$DU" AIREOS_AUDIT_CHAIN_PUBLIC_KEY="$PK" /opt/aireos/bin/audit-service verify')"
echo "$VERIFY_OUT"
case "$VERIFY_OUT" in
  *"INTACT+ATTESTED"*) : ;;
  *"INTACT+UNATTESTED"*)
    echo "wd1: verify reported INTACT+UNATTESTED after a forced checkpoint — head signatures ARE being checked, but the signed prefix does not cover the tail. Either the checkpoint above failed (see its output) or events were appended after it. Output above."; exit 1 ;;
  *)
    echo "wd1: audit-service verify did NOT report an attested outcome at all — most likely no chain-head public key reached the CLI, so it fell back to ordering-only (T-126a's resolveVerifyKey). Output above."; exit 1 ;;
esac

# T-127a leftover, closed here (ADR-0069 D4): the assertion above proves the SIGNED
# HEAD (the in-Postgres chain_heads row + its signature). It never checked the
# out-of-Postgres anchor file — the truncation detector that is the whole reason D4
# exists (a deleted tail is invisible to Postgres alone; only the anchor catches it).
# Assert it exists AND is non-empty at the path install.sh actually provisioned into
# aireos.env's AIREOS_AUDIT_ANCHOR_PATH — not a hardcoded path, so this breaks loudly
# if install.sh's provisioning ever drifts from what it tells the operator.
log "assert: the out-of-Postgres audit anchor file exists and is non-empty (ADR-0069 D4)"
docker exec "$HOST" bash -c '
  AP=$(grep "^AIREOS_AUDIT_ANCHOR_PATH=" /etc/aireos/aireos.env | head -1 | cut -d= -f2-)
  [ -n "$AP" ] || { echo "aireos.env missing AIREOS_AUDIT_ANCHOR_PATH"; exit 1; }
  [ -s "$AP" ] || { echo "anchor file $AP does not exist or is empty — the out-of-Postgres truncation detector never wrote anything"; exit 1; }
  echo "anchor OK — $AP exists and is non-empty ($(wc -l < "$AP") line(s))"
'

# Includes the WD-5 Run lifecycle events (OI-12, T-061): run.launched + run.completed
# land via api's in-process outbox relay, proving the Run plane drives on the install.
# v0.6 (OI-13, T-069) adds the delegation lifecycle: grant.issued/grant.revoked (the
# operator's authorization + its withdrawal) and run.canceled (the revoke cascade,
# DUR-7) — all via the same api relay.
# v0.7 (OI-14, T-077) adds the chartered-Workstream lifecycle: workstream.chartered (the
# ONE consent act, actor=operator) + workstream.retired (the operator kill switch) — the
# AUD-18 events, delivered via the same api relay. The tick's own run.launched/completed
# are already in WANTS; the schedule-fired run's workstream:<id> actor is asserted below.
# v0.8 (OI-15, T-084) adds the Persistent Scoped Memory governance acts (AUD-19, all
# content-free, same api relay): memory.written (accrual on the terminal flip + the
# authored write), memory.redacted (the tombstone flip), memory.expired (the sweep at
# the production 2-min reaper cadence — no AIREOS_MEMORY_SWEEP_SECONDS on the install).
# Per-id actor/exactly-once precision is asserted in-test via the audit read-proxy.
# v0.9 (OI-16, T-093) adds the Adaptive-Workstreams agent-composed lifecycle (ADR-0065,
# same api relay + migration 0013 riding the migrate loop): plan.requested/submitted
# (a tick opening a plan request / a composer answering it), plan.expired (an unanswered
# request's own TTL, never wedging the charter), memory.read/shared/unshared (the
# agent-plane read + the operator's share flip that gates it), run.denied (the deny cycle
# on a submitted plan, distinct from the revoke-cascade's run.canceled). Per-composer/
# per-id exactly-once precision is asserted in-test via the audit read-proxy.
# v0.10 (OI-17, T-102) adds the Layer 1 Live/Context Index vocabulary (ADR-0066, same
# api relay + migrations 0014+0015 riding the migrate loop): context.source.registered
# (the operator's one consent act binding a source to a charter) and context.read (the
# composer's per-search chained read, agent-workspace edge, content-free). The indexer
# rides the already-running api goroutine at the REAL 60s production floor (no
# AIREOS_CONTEXT_INDEX_SECONDS override); context.search rides the already-installed
# gateway binary + Caddy /mcp route — no new unit/route/key/env. Per-composer/per-id
# exactly-once precision is asserted in-test via the audit read-proxy.
# v0.12 (OI-27, T-122a) adds the WD-11 pairing lifecycle (ADR-0068, AUD-32, same
# api relay + migration 0016 riding the migrate loop): pairing.requested (the desk's
# request), pairing.redeemed (the device's redeem), pairing.revoked (the desk's
# revoke) — all content-free per AUD-33 (no pairing.expired). approval.granted is
# already in WANTS from WD-2; the paired-origin approval's distinct actor
# (operator:admin#paired, AUD-34) is asserted separately below.
WANTS="auth.login.success policy.decision:requires_approval approval.granted credential.leased opened input.line closed:operator approval.denied:operator auth.logout tool.invoked remote.exec tool.result run.launched run.completed grant.issued grant.revoked run.canceled workstream.chartered workstream.retired memory.written memory.redacted memory.expired plan.requested plan.submitted plan.expired memory.read memory.shared memory.unshared run.denied context.source.registered context.read pairing.requested pairing.redeemed pairing.revoked"
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

# v0.12 (OI-27, T-122a): the PAIRED-session approval must be attributable as such
# (AUD-34) — approval.granted with actor 'operator:admin#paired', distinguishable
# from every desk approval already in the chain (plain 'operator:admin'). Proves
# WD-11's leg 3 (the held invocation was approved FROM the paired session) on the
# real install, not just on compose.
for attempt in 1 2 3; do
  PAIRED_APPROVED="$(docker exec "$HOST" su - postgres -c "psql -tA -d cockpit -c \"SELECT count(*) FROM audit_events WHERE outcome='approval.granted' AND actor='operator:admin#paired'\"")"
  [ "${PAIRED_APPROVED:-0}" -ge 1 ] && break
  [ "$attempt" = 3 ] && { echo "no operator:admin#paired approval.granted in the installed chain — AUD-34's paired-origin attribution missing"; exit 1; }
  sleep 5
done

log "T-053a/T-061/T-069/T-077/T-084/T-093/T-102/T-122a PASS — WD-4 + WD-5 + WD-6 + WD-7 + WD-8 + WD-9 + WD-10 + WD-11 on the installed stack via Caddy HTTPS: WD-2 session approval + governed lease + deny leg, the agent surface (Caddy /mcp → gateway block-and-wait → operator approve → broker exec) driven by a KEYLESS gateway (SEC-20), the WD-5 Run plane (OI-12), the WD-6 delegated-launch plane (OI-13), the WD-7 chartered-Workstream plane (OI-14: operator charter → the in-process ticker fires a governed Run on the schedule with nobody present → the flagged step parks with the workstream context → operator approve → completed → retire; the schedule floor is the 60s production default (no new env; the ticker rides the OI-13 GATEWAY_URL, migration 0011 rides the migrate loop)), the WD-8 Persistent Scoped Memory plane (OI-15: the completed tick ACCRUES a content-free pointer with nobody writing it → author/scoped-read/redact + the N1 idempotent no-op → the agent plane is rejected on /v1/memories* (SEC-31) → unknown-id 404s → the sweep at the PRODUCTION 2-min reaper cadence expires the unpinned row exactly once and spares the pinned one → an operator-canceled Run accrues exactly one :canceled pointer; migration 0012 rides the migrate loop, the sweep rides the unconditional reaper goroutine), the WD-9 Adaptive-Workstreams plane (OI-16: a real external MCP client composes the next tick's plan from governed memory it reads over Caddy /mcp, a human reviews the exact plan before anything runs, the engine executes it, the outcome accrues, and the NEXT tick's read proves the loop closed — at the 60s production floor; cross-grant containment + revoke-cascades-with-zero-run.denied + the share gate + the deny cycle + expiry-never-wedges all proven installed; migration 0013 rides the migrate loop, the three control-plane MCP tools ride the existing gateway binary + /mcp route — NO new unit/route/key/env for either v0.8 or v0.9), the WD-10 Layer 1 Live/Context Index plane (OI-17: a real host directory bound to a charter — via the wd1-harness-owned CTXVOL, standing in for the installed api's own filesystem, since api is a systemd service inside $HOST with no bind-mounted repo checkout — is swept by the in-process indexer at the REAL 60s production floor, the composer's context.search over Caddy /mcp reads it (chained context.read, agent-workspace edge, content-free) alongside memory.read, a human reviews and approves the exact grounded plan, the flagged resource's invocation-level park+approve completes it, it accrues, and a live source edit is reflected on the NEXT sweep with no rebuild call; the confined-root guard (N2) holds installed too; migrations 0014+0015 ride the migrate loop, context.search rides the existing gateway binary + /mcp route — NO new unit/route/key/required-env), AND the WD-11 pairing plane (T-122a, ADR-0068: pairing ships OFF by default — proven 404 live on this stack — then enabled the way an operator actually would, a re-run of the real install.sh with AIREOS_PAIRING_ENABLED=1 + a unit restart, OI-23 idempotence preserved; the desk requests a pairing, a device redeems the fragment-carried secret for a revocable paired session, a held invocation is approved FROM the paired session — not the desk — attributably (AUD-34: approval.granted actor=operator:admin#paired), and the desk revokes it, SEC-34 proving the revoked reuse refused live; migration 0016 rides the migrate loop, no new unit/port/key/Caddyfile change); chain intact + complete (incl. workstream.chartered/workstream.retired + workstream-actor run.launched + memory.written/memory.redacted/memory.expired + plan.requested/plan.submitted/plan.expired + memory.read/memory.shared/memory.unshared + run.denied + context.source.registered/context.read + pairing.requested/pairing.redeemed/pairing.revoked + the paired-actor approval.granted)."

# 12. SEC-43/VER-16 (T-127b) — the forged-chain negative proof ADR-0069 §Security
#     posture pins as binding, run on the INSTALLED stack as its own, EXPLICITLY
#     SEQUENCED FINAL STEP — after the WD PASS line above, never folded in by a
#     "runs last" naming convention (one future added assertion away from an
#     order-dependent failure against a chain THIS step destroys). Nothing may
#     run after it: the chain is deliberately left broken.
#
#     LEG A proves the precondition is real, not asserted: an UPDATE against
#     audit_events is actually attempted while audit_events_no_mutate is armed
#     and is REJECTED, then the SAME statement is shown succeeding once the
#     trigger is disabled — confirming the trigger, not some unrelated
#     permission, was what blocked it (ADR-0069 §Context's corrected threat
#     statement: the precondition is OWNER access, not mere write access).
#
#     LEG B proves the forgery is caught anyway: disable the trigger (owner
#     privilege — itself evidence of the precondition), tamper one event's
#     actor field STRICTLY BELOW the last signed head, then repair
#     prev_hash/chain_hash forward from Genesis using a SQL translation of
#     chain.Compute (services/audit-service/internal/chain/chain.go) — pgcrypto's
#     digest() over the identical length-prefixed framing — so a pure
#     ordering/hash walk (chain.Verify, called via the REAL installed
#     audit-service binary in its no-key ordering-only fallback mode, never
#     reimplemented here) reports INTACT. That "intact" result is the
#     vacuous-looking green ADR-0069's test-design constraint warns about — it
#     is the SETUP the proof needs, not the proof itself. What actually catches
#     the forgery is the signature: both the DB-backed `audit-service verify`
#     and the offline, third-party `aireos-verify` binary (T-126b) are run as
#     the REAL INSTALLED BINARIES — first against the clean pre-tamper state
#     (must PASS, VER-16's "shown passing on the untampered input"), then again
#     against the tampered state (must FAIL and report BROKEN).
#
#     The SQL repair formula was validated byte-for-byte against the real Go
#     chain.Compute before this script was written (two independently
#     Go-computed chain hashes, reproduced exactly via this SQL against a
#     scratch Postgres+pgcrypto instance seeded with the same field values —
#     not merely asserted to be equivalent).
log "SEC-43/VER-16: forged-chain negative proof (installed stack, FINAL step — destroys the chain)"
docker exec -i "$HOST" bash <<'SEC43EOF'
say() { printf '\033[1;35m[wd1:sec43]\033[0m %s\n' "$*"; }
fail() { echo "SEC-43 FAILED: $*" >&2; exit 1; }

DU=$(grep "^DATABASE_URL=" /etc/aireos/aireos.env | head -1 | cut -d= -f2-)
SK=$(grep "^AIREOS_AUDIT_CHAIN_SIGN_KEY=" /etc/aireos/audit.env | head -1 | cut -d= -f2-)
PK=$(grep "^AIREOS_AUDIT_CHAIN_PUBLIC_KEY=" /etc/aireos/aireos.env | head -1 | cut -d= -f2-)
AP=$(grep "^AIREOS_AUDIT_ANCHOR_PATH=" /etc/aireos/aireos.env | head -1 | cut -d= -f2-)
[ -n "$DU" ] && [ -n "$SK" ] && [ -n "$PK" ] || fail "could not resolve DATABASE_URL / chain-head keys from the installed env files"

say "forcing a checkpoint (idempotent — a no-op if T-127a's earlier checkpoint already covers the tip)"
DATABASE_URL="$DU" AIREOS_AUDIT_CHAIN_SIGN_KEY="$SK" /opt/aireos/bin/audit-service checkpoint || fail "checkpoint did not run"

HEAD_SEQ=$(psql "$DU" -tAc "SELECT seq FROM audit_chain_heads ORDER BY seq DESC LIMIT 1")
[ -n "$HEAD_SEQ" ] || fail "no signed chain head exists — nothing to attest around"
say "signed head at seq $HEAD_SEQ"

say "pre-tamper control (VER-16): audit-service verify must PASS on the untampered chain"
PRE_VERIFY=$(DATABASE_URL="$DU" AIREOS_AUDIT_CHAIN_PUBLIC_KEY="$PK" /opt/aireos/bin/audit-service verify 2>&1)
PRE_VERIFY_CODE=$?
echo "$PRE_VERIFY"
[ "$PRE_VERIFY_CODE" = 0 ] || fail "pre-tamper audit-service verify did not exit 0 (exit $PRE_VERIFY_CODE)"
case "$PRE_VERIFY" in *"INTACT+ATTESTED"*) : ;; *) fail "pre-tamper audit-service verify was not INTACT+ATTESTED" ;; esac

say "pre-tamper control (VER-16): aireos-verify (offline export) must PASS on the untampered chain"
DATABASE_URL="$DU" AIREOS_AUDIT_ANCHOR_PATH="$AP" /opt/aireos/bin/audit-service export > /tmp/sec43-clean.ndjson
[ -s /tmp/sec43-clean.ndjson ] || fail "clean export is empty"
PRE_AV=$(/opt/aireos/bin/aireos-verify --pubkey "$PK" /tmp/sec43-clean.ndjson 2>&1)
PRE_AV_CODE=$?
echo "$PRE_AV"
# SEC-43 is a CHAIN-axis proof. aireos-verify exits 0 only when the chain is
# INTACT+ATTESTED *AND* the anchor is IN_SYNC — two deliberately INDEPENDENT axes
# (ADR-0069 D4/D6). On the installed stack the anchor is routinely one head behind
# (BENIGN_LAG -> exit 3), which is correct designed behaviour, not a fault: the
# anchor is appended AFTER the DB head commits, so a lag of one is the ordinary
# crash-window state the classification exists to name.
#
# Requiring exit 0 here therefore conflated the two axes and failed this control for
# a reason that has nothing to do with what SEC-43 proves. Assert the CHAIN verdict,
# and reject only the codes that mean something is genuinely compromised before we
# have touched anything: 1 = chain BROKEN, 2 = anchor TRUNCATION_SIGNATURE.
case "$PRE_AV_CODE" in
  1|2) fail "pre-tamper aireos-verify reported a COMPROMISED chain/anchor before any tampering (exit $PRE_AV_CODE) — the stack was not in a clean state to prove anything from" ;;
esac
case "$PRE_AV" in
  *"INTACT+ATTESTED"*) : ;;
  *) fail "pre-tamper aireos-verify chain verdict was not INTACT+ATTESTED (exit $PRE_AV_CODE)" ;;
esac
say "pre-tamper controls PASSED on both verifiers — a negative proof that could never have passed proves nothing"

say "LEG A: UPDATE against audit_events while audit_events_no_mutate is ARMED must be REJECTED"
TIP_SEQ=$(psql "$DU" -tAc "SELECT max(seq) FROM audit_events")
[ -n "$TIP_SEQ" ] || fail "audit_events is empty — nothing to tamper"

cat > /tmp/sec43-lega.sql <<'SQL'
UPDATE audit_events SET actor = 'sec43-leg-a-should-be-rejected' WHERE seq = :tip;
SQL
LEGA_OUT=$(psql "$DU" -v ON_ERROR_STOP=1 -v tip="$TIP_SEQ" -f /tmp/sec43-lega.sql 2>&1)
LEGA_CODE=$?
echo "$LEGA_OUT"
[ "$LEGA_CODE" != 0 ] || fail "UPDATE against audit_events (seq $TIP_SEQ) succeeded while audit_events_no_mutate should be armed — the append-only barrier did not hold"
echo "$LEGA_OUT" | grep -qi "append-only" || fail "the UPDATE was rejected, but not by the expected trigger"
say "LEG A PASSED: UPDATE against audit_events (seq $TIP_SEQ) REJECTED while armed"

say "LEG B step 1: disable audit_events_no_mutate (owner privilege — itself evidence of the ADR-0069 precondition; the migration file is untouched, this is a runtime-only change on this disposable stack)"
cat > /tmp/sec43-disable.sql <<'SQL'
ALTER TABLE audit_events DISABLE TRIGGER audit_events_no_mutate;
SQL
psql "$DU" -v ON_ERROR_STOP=1 -f /tmp/sec43-disable.sql || fail "disabling audit_events_no_mutate failed"

say "LEG A control: the SAME UPDATE now succeeds with the trigger disabled"
cat > /tmp/sec43-legactrl.sql <<'SQL'
UPDATE audit_events SET actor = actor WHERE seq = :tip;
SQL
psql "$DU" -v ON_ERROR_STOP=1 -v tip="$TIP_SEQ" -f /tmp/sec43-legactrl.sql || fail "the identical UPDATE still fails after disabling the trigger — LEG A's earlier rejection was not actually caused by audit_events_no_mutate"
say "LEG A control PASSED — confirms the earlier rejection was the trigger, not an unrelated permission"

FIRST_SEQ=$(psql "$DU" -tAc "SELECT min(seq) FROM audit_events")
[ -n "$FIRST_SEQ" ] || fail "could not read the lowest event seq"
[ "$FIRST_SEQ" -lt "$HEAD_SEQ" ] || fail "test precondition violated: the lowest event seq ($FIRST_SEQ) is not STRICTLY BELOW the signed head ($HEAD_SEQ) — tampering at or above the head would be a vacuous proof (ADR-0069 §Security posture)"

ORIG_ACTOR=$(psql "$DU" -tAc "SELECT actor FROM audit_events WHERE seq = $FIRST_SEQ")
FORGED_ACTOR="${ORIG_ACTOR}-SEC43-FORGED-BELOW-HEAD"
say "LEG B step 2: tamper actor at seq $FIRST_SEQ (below signed head $HEAD_SEQ) -- $ORIG_ACTOR -> $FORGED_ACTOR"
cat > /tmp/sec43-tamper.sql <<'SQL'
UPDATE audit_events SET actor = :'forged' WHERE seq = :seq;
SQL
psql "$DU" -v ON_ERROR_STOP=1 -v seq="$FIRST_SEQ" -v forged="$FORGED_ACTOR" -f /tmp/sec43-tamper.sql || fail "tamper UPDATE failed"

say "LEG B step 3: repair prev_hash/chain_hash forward from Genesis (chain.Compute-equivalent SQL)"
cat > /tmp/sec43-repair.sql <<'SQL'
CREATE OR REPLACE FUNCTION sec43_compute(prevh text, p_id text, p_ts text, p_actor text, p_edge text, p_target text, p_outcome text, p_evid text, p_idem text)
RETURNS text AS $$
DECLARE
  payload bytea;
BEGIN
  payload := convert_to(prevh, 'UTF8');
  payload := payload || int4send(octet_length(p_id)::int4)      || convert_to(p_id, 'UTF8');
  payload := payload || int4send(octet_length(p_ts)::int4)      || convert_to(p_ts, 'UTF8');
  payload := payload || int4send(octet_length(p_actor)::int4)   || convert_to(p_actor, 'UTF8');
  payload := payload || int4send(octet_length(p_edge)::int4)    || convert_to(p_edge, 'UTF8');
  payload := payload || int4send(octet_length(p_target)::int4)  || convert_to(p_target, 'UTF8');
  payload := payload || int4send(octet_length(p_outcome)::int4) || convert_to(p_outcome, 'UTF8');
  payload := payload || int4send(octet_length(p_evid)::int4)    || convert_to(p_evid, 'UTF8');
  payload := payload || int4send(octet_length(p_idem)::int4)    || convert_to(p_idem, 'UTF8');
  RETURN encode(digest(payload, 'sha256'), 'hex');
END;
$$ LANGUAGE plpgsql;

DO $$
DECLARE
  r RECORD;
  prevh TEXT := repeat('0', 64);
  newhash TEXT;
BEGIN
  FOR r IN
    SELECT seq, id::text AS id, "timestamp" AS ts, actor, edge, target,
           COALESCE(outcome, '') AS outcome,
           COALESCE(evidence_packet_id, '') AS evidence_packet_id,
           idempotency_key::text AS idempotency_key
    FROM audit_events ORDER BY seq ASC
  LOOP
    newhash := sec43_compute(prevh, r.id, r.ts, r.actor, r.edge, r.target, r.outcome, r.evidence_packet_id, r.idempotency_key);
    UPDATE audit_events SET prev_hash = prevh, chain_hash = newhash WHERE seq = r.seq;
    prevh := newhash;
  END LOOP;
  RAISE NOTICE 'sec43: chain repaired through seq %, final chain_hash %', (SELECT max(seq) FROM audit_events), prevh;
END $$;
SQL
psql "$DU" -v ON_ERROR_STOP=1 -f /tmp/sec43-repair.sql || fail "chain repair failed"

say "LEG B step 4: chain.Verify (ordering + hash recompute ONLY, no signatures) must return INTACT on the forged chain — the SETUP the proof needs, not the proof itself. If this does not hold, STOP rather than adjust until it goes green (ADR-0069 §Security posture)."
POST_ORDERING=$(DATABASE_URL="$DU" /opt/aireos/bin/audit-service verify 2>&1)
POST_ORDERING_CODE=$?
echo "$POST_ORDERING"
if [ "$POST_ORDERING_CODE" != 0 ] || ! echo "$POST_ORDERING" | grep -q "ORDERING-ONLY"; then
  fail "test setup FAILED: chain.Verify (the real function, invoked with no key so audit-service verify falls back to its ordering-only mode) does not report intact on the forged chain — the repair did not fully satisfy the ordering walk, so this test cannot prove what it claims."
fi
say "chain.Verify (real ordering-only walk, no signatures) reports INTACT on the forged chain — the forgery is invisible to that check alone"

say "LEG B step 5: BOTH real verifiers must still catch it"
POST_VERIFY=$(DATABASE_URL="$DU" AIREOS_AUDIT_CHAIN_PUBLIC_KEY="$PK" /opt/aireos/bin/audit-service verify 2>&1)
POST_VERIFY_CODE=$?
echo "$POST_VERIFY"
[ "$POST_VERIFY_CODE" != 0 ] || fail "audit-service verify exited 0 against the forged chain"
echo "$POST_VERIFY" | grep -q "BROKEN" || fail "audit-service verify did not report BROKEN (exit $POST_VERIFY_CODE)"
say "LEG B PASSED: audit-service verify (DB-backed) reports BROKEN, exit $POST_VERIFY_CODE"

DATABASE_URL="$DU" AIREOS_AUDIT_ANCHOR_PATH="$AP" /opt/aireos/bin/audit-service export > /tmp/sec43-tampered.ndjson
POST_AV=$(/opt/aireos/bin/aireos-verify --pubkey "$PK" /tmp/sec43-tampered.ndjson 2>&1)
POST_AV_CODE=$?
echo "$POST_AV"
[ "$POST_AV_CODE" != 0 ] || fail "aireos-verify exited 0 against the tampered export"
echo "$POST_AV" | grep -q "BROKEN" || fail "aireos-verify did not report BROKEN (exit $POST_AV_CODE)"
say "LEG B PASSED: aireos-verify (offline, third-party, exported bytes only, NO database access) reports BROKEN, exit $POST_AV_CODE"

say "SEC-43 SUMMARY -- signed head seq=$HEAD_SEQ, tampered event seq=$FIRST_SEQ, chain.Verify=INTACT (ordering-only walk, real function) on the forged chain, audit-service verify=BROKEN (exit $POST_VERIFY_CODE), aireos-verify=BROKEN (exit $POST_AV_CODE)"
SEC43EOF

log "T-127b PASS — SEC-43/VER-16 on the installed stack: the append-only trigger is a real barrier (LEG A, shown rejecting an UPDATE while armed then succeeding once disabled — confirming the trigger, not an unrelated permission), and a chain tampered below a signed head then repaired end to end to satisfy a pure ordering/hash walk is still caught by BOTH the DB-backed audit-service verify and the offline, third-party aireos-verify (LEG B). This was the FINAL step by design — the chain is now deliberately broken."
