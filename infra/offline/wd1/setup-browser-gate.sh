#!/usr/bin/env bash
# setup-browser-gate.sh — T-175d: first-run setup in a REAL browser against a REAL stack with NO operator (ADR-0078; FRX-1/2).
#
#   bash infra/offline/wd1/setup-browser-gate.sh
#
# Brings the demo stack up through setup.sh with no operator anywhere (bringup-demo.sh --no-operator --no-seed-resource), takes the
# one-time code setup.sh printed on the host, and runs tests/smoke/setup.smoke.spec.ts in the pinned Playwright image on the
# demo network: the setup page, a wrong code, the real code, the dashboard, then "the page is gone". Screenshots go to
# dist/t175d-evidence/. Before and after, the HOST plane is checked too (the API's own answers; `setup.sh --new-setup-code`
# refused once an operator exists). The existing quickstart browser gate (an env operator) is a separate script and untouched.
#
# Prereqs: same as bringup-demo.sh (a from-HEAD bundle + the ubuntu2404 companion in dist/offline/).
set -euo pipefail
export MSYS_NO_PATHCONV=1
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
SMOKE_DIR="$REPO_ROOT/tests/smoke"
EVIDENCE="$REPO_ROOT/dist/t175d-evidence"
HOST=aireos-demo-host; SSHD=aireos-demo-sshd; NET=aireos-demo; KEYVOL=aireos-demo-keys; RUNNER=aireos-setup-runner
PW_IMAGE=mcr.microsoft.com/playwright:v1.49.1-noble
export SETUP_CODE_FILE="$SMOKE_DIR/.setup-code"; DONE_FILE="$SMOKE_DIR/.setup-done"
log() { printf '\033[1;35m[setup-browser]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[setup-browser] ERROR:\033[0m %s\n' "$*" >&2; exit 1; }
hx() { docker exec "$HOST" curl -sk -o /dev/null -w '%{http_code}' "https://localhost$1"; }
cleanup() {
  docker rm -f "$RUNNER" "$HOST" "$SSHD" >/dev/null 2>&1 || true
  docker volume rm "$KEYVOL" >/dev/null 2>&1 || true; docker network rm "$NET" >/dev/null 2>&1 || true
  rm -f "$SETUP_CODE_FILE" "$DONE_FILE"
}
trap cleanup EXIT

rm -rf "$EVIDENCE"; mkdir -p "$EVIDENCE"; rm -f "$DONE_FILE"
bash "$SCRIPT_DIR/bringup-demo.sh" --no-operator --no-seed-resource
[ -s "$SETUP_CODE_FILE" ] || die "bringup did not save a setup code"

log "host plane, before: setup is open, and nothing else answers"
[ "$(hx /v1/setup)" = 200 ] || die "GET /v1/setup is not 200 on a stack with no operator"
[ "$(hx /v1/auth/me)" = 503 ] || die "another route answered something other than 503 in setup mode"
docker exec "$HOST" bash -c '! grep -q AIREOS_BOOTSTRAP_OPERATOR /etc/aireos/aireos.env' || die "aireos.env carries an operator: this is not the no-operator path"

log "Playwright (chromium, the pinned image) on the demo network -> https://$HOST"
rc=0
docker run --rm --name "$RUNNER" --network "$NET" -v "$SMOKE_DIR":/smoke -v "$EVIDENCE":/evidence \
  -e SMOKE_BASE_URL="https://$HOST" -e SETUP_CODE="$(cat "$SETUP_CODE_FILE")" -e SETUP_EVIDENCE_DIR=/evidence \
  -w /smoke "$PW_IMAGE" \
  sh -c "npm install --no-audit --no-fund >/dev/null 2>&1 && npx playwright test setup.smoke.spec.ts --project=chromium" || rc=$?
log "playwright exit code: $rc"
[ "$rc" = 0 ] || die "the Playwright run failed (exit $rc)"
[ -f "$DONE_FILE" ] || die "no $DONE_FILE: the test body did not run to its end (a skipped Playwright test still exits 0)"
for s in 01-setup-page 02-wrong-code-refused 03-dashboard-after-setup 04-setup-route-lands-on-login; do
  [ "$(wc -c <"$EVIDENCE/$s.png")" -gt 5000 ] || die "screenshot $s.png is missing or empty"
done

log "host plane, after: the setup routes are gone, and a new code is refused"
[ "$(hx /v1/setup)" = 404 ] || die "GET /v1/setup still answers after setup"
if out="$(docker exec "$HOST" bash -c 'cd /root/inst && ./aireos-offline-*/setup.sh --new-setup-code' 2>&1)"; then die "--new-setup-code succeeded after setup"; fi
grep -qF 'Setup is already complete' <<<"$out" || die "--new-setup-code refused, but not with the plain message: $out"

log "T-175d PASS -- playwright exit 0; screenshots:"
ls -1 "$EVIDENCE"/*.png
