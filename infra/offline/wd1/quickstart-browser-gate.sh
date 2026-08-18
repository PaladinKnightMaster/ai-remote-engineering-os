#!/usr/bin/env bash
# quickstart-browser-gate.sh — S-26/T-133b: the BROWSER half of the OI-36
# quickstart gate, completing what T-133a's shell gate deliberately left out
# (see that script's header) and replacing its known ONB-4 gap (a direct DB
# insert standing in for step 3) with the real cockpit flow.
#
# Orchestrates two Playwright runs across the plane boundary the quickstart
# itself crosses (browser -> host shell -> browser, Commander finding B):
#
#   spec A  (quickstart-a.smoke.spec.ts, steps 1+3) -- log in, create the
#           loopback demo target through the real cockpit path, prove ONB-4
#           via the resource.created audit event; writes the new resource id
#           to a state file (Commander finding B's seam).
#   step 4  (host shell) -- setup-loopback-demo.sh bind --resource-id <id>,
#           using the BUNDLE's OWN copy (OI-36), same as quickstart-shell-gate.sh.
#   spec B  (quickstart-b.smoke.spec.ts, steps 5/6/7-cockpit) -- launch a
#           one-step Run, confirm its events in the audit surface, remove the
#           demo target.
#   step 7  (host shell) -- --undo, same as quickstart-shell-gate.sh.
#
# Brings the demo stack up WITHOUT the usual seeded resource
# (bringup-demo.sh --no-seed-resource, Commander finding A) so the "Create
# loopback demo target" button actually renders — every earlier walkthrough
# ran against a pre-seeded stack where that button is permanently hidden.
#
# The Playwright runner is a plain `docker run` on bringup-demo.sh's own
# `aireos-demo` network (Commander finding D: docker-compose.smoke.yml
# `include`s the whole e2e backend and must not be reused here), targeting
# the docker network name (Commander finding E: Windows-host HTTPS to
# published ports is broken on this dev box; in-network clients work).
#
#   bash infra/offline/wd1/quickstart-browser-gate.sh              # happy path
#   bash infra/offline/wd1/quickstart-browser-gate.sh no-bind       # VER-16: skip step 4 on purpose
#
# Prereqs: same as bringup-demo.sh -- a from-HEAD bundle in
# dist/offline/aireos-offline-*.tar.gz + the postgres companion (both already
# on disk for v0.13.0; do NOT rebuild, per the ticket brief).
set -euo pipefail
export MSYS_NO_PATHCONV=1
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
SMOKE_DIR="$REPO_ROOT/tests/smoke"
HOST=aireos-demo-host
SSHD=aireos-demo-sshd
NET=aireos-demo
KEYVOL=aireos-demo-keys
ACCOUNT=aireos-demo
OPPW=demo-operator-pw
PW_IMAGE=mcr.microsoft.com/playwright:v1.49.1-noble
STATE_FILE="$SMOKE_DIR/.quickstart-resource-id"
DONE_FILE="$SMOKE_DIR/.quickstart-b-done"
RUNNER=aireos-quickstart-runner
MODE="${1:-happy}" # "no-bind" is VER-16's negative leg

log() { printf '\033[1;35m[quickstart-browser]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[quickstart-browser] ERROR:\033[0m %s\n' "$*" >&2; exit 1; }

cleanup() {
  log "tear down the runner + demo stack"
  docker rm -f "$RUNNER" >/dev/null 2>&1 || true
  docker rm -f "$HOST" "$SSHD" >/dev/null 2>&1 || true
  docker volume rm "$KEYVOL" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
  rm -f "$STATE_FILE" "$DONE_FILE"
}
trap cleanup EXIT

log "bring-up (bringup-demo.sh --no-seed-resource, finding A) -- real install.sh from the built bundle, systemd-in-Docker, Caddy on :8443, no resource pre-seeded"
bash "$SCRIPT_DIR/bringup-demo.sh" --no-seed-resource

log "locate the BUNDLE's OWN copy of setup-loopback-demo.sh on the installed host (OI-36)"
BUNDLE_DIR="$(docker exec "$HOST" bash -c 'ls -d /root/inst/aireos-offline-*/ | head -1')"
[ -n "$BUNDLE_DIR" ] || die "could not locate the extracted bundle dir on $HOST"
docker exec "$HOST" test -f "${BUNDLE_DIR}setup-loopback-demo.sh" \
  || die "the bundle at $BUNDLE_DIR does not carry setup-loopback-demo.sh -- OI-36 requires driving THIS copy, not infra/offline/'s"

log "quickstart step 2 precondition + step 2 itself (bundle copy, loopback target 127.0.0.1)"
docker exec "$HOST" useradd -m -s /bin/bash "$ACCOUNT"
GEN_OUT="$(docker exec "$HOST" bash -c "cd '$BUNDLE_DIR' && ./setup-loopback-demo.sh generate --account $ACCOUNT")"
echo "$GEN_OUT"
HOSTKEY_LINE="$(echo "$GEN_OUT" | sed -n 's/^ *ssh host key: //p')"
[ -n "$HOSTKEY_LINE" ] || die "step 2: generate did not print an ssh host key line -- output above"
log "step 2 PASSED -- generate produced a host key line for $ACCOUNT"

rm -f "$STATE_FILE"

log "spec A -- quickstart steps 1+3 (log in, create the loopback demo target through the real cockpit path, ONB-4)"
docker run --rm --name "$RUNNER" --network "$NET" \
  -v "$SMOKE_DIR":/smoke \
  -e SMOKE_BASE_URL="https://$HOST" \
  -e QUICKSTART_OPERATOR_PASS="$OPPW" \
  -e QUICKSTART_DEMO_ACCOUNT="$ACCOUNT" \
  -e QUICKSTART_HOST_KEY="$HOSTKEY_LINE" \
  -e QUICKSTART_STATE_FILE=/smoke/.quickstart-resource-id \
  -w /smoke "$PW_IMAGE" \
  sh -c "npm install --no-audit --no-fund && npx playwright test quickstart-a.smoke.spec.ts"

[ -f "$STATE_FILE" ] || die "spec A did not write a resource id to $STATE_FILE"
RID="$(cat "$STATE_FILE")"
[ -n "$RID" ] || die "resource id state file is empty"
log "spec A PASSED -- created resource id=$RID (proven via a resource.created audit event, ONB-4)"

if [ "$MODE" = "no-bind" ]; then
  log "VER-16 negative leg: SKIPPING step 4 (bind) on purpose -- spec B's Run must NOT reach completed"
else
  log "quickstart step 4: bind (bundle copy)"
  BIND_OUT="$(docker exec "$HOST" bash -c "cd '$BUNDLE_DIR' && ./setup-loopback-demo.sh bind --resource-id $RID")"
  echo "$BIND_OUT"
  echo "$BIND_OUT" | grep -q "bound" || die "step 4: bind did not report success -- output above"
  log "step 4 PASSED -- bind reported success"
fi

# A SKIPPED Playwright test still exits 0, so the exit code alone cannot
# distinguish "spec B passed" from "spec B never ran" (its own test.skip fires
# on an empty QUICKSTART_RESOURCE_ID). Clear the marker, then require it back:
# a positive marker, not the absence of a failure.
rm -f "$DONE_FILE"
log "spec B -- quickstart steps 5/6/7-cockpit (launch a Run, audit events, remove the demo target)"
docker run --rm --name "$RUNNER" --network "$NET" \
  -v "$SMOKE_DIR":/smoke \
  -e SMOKE_BASE_URL="https://$HOST" \
  -e QUICKSTART_OPERATOR_PASS="$OPPW" \
  -e QUICKSTART_RESOURCE_ID="$RID" \
  -w /smoke "$PW_IMAGE" \
  sh -c "npm install --no-audit --no-fund && npx playwright test quickstart-b.smoke.spec.ts"

[ -f "$DONE_FILE" ] || die "spec B did not run to completion -- no $DONE_FILE marker. A skipped Playwright test exits 0, so this asserts the test BODY actually executed rather than trusting the exit code."
log "spec B PASSED -- completion marker present"

log "quickstart step 7 (host half): --undo (bundle copy)"
UNDO_OUT="$(docker exec "$HOST" bash -c "cd '$BUNDLE_DIR' && ./setup-loopback-demo.sh --undo")"
echo "$UNDO_OUT"
echo "$UNDO_OUT" | grep -q "undo complete" || die "step 7: --undo did not report completion -- output above"
log "step 7 (host half) PASSED"

log "T-133b PASS -- quickstart browser gate (steps 1,3,5,6,7-cockpit) all ran successfully against the built bundle on a fresh host. OI-36 fully closed (shell half T-133a + browser half T-133b)."
