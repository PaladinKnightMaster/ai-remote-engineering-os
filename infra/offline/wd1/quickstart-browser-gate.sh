#!/usr/bin/env bash
# quickstart-browser-gate.sh — the BROWSER half of the OI-36 quickstart gate, in the v0.17.0 no-paste form (T-176d; ADR-0079,
# FRX-4/FRX-5). It replaces the T-133b flow (generate a key and host-key line with the script, paste them into the cockpit, bind
# on the host): the stack is installed by setup.sh with AIREOS_LOOPBACK_DEMO=yes, and from then on EVERYTHING is the browser.
#
#   bring-up  (bringup-demo.sh --demo --no-seed-resource) -- the real setup.sh on a systemd host; the demo account and a pending
#             key are prepared; no resource exists, so the "Create loopback demo target" button renders.
#   spec A    (quickstart-a)          -- log in, ONE click on the button (no paste, no server command), 201 from /v1/demo-target,
#                                        resource.created + credential.bound in the audit surface.
#   spec C    (quickstart-credential) -- a governed Run completes; the credential panel shows the PUBLIC key and the exact
#                                        authorized_keys line; Rotate asks a confirmation that names the consequence; after the
#                                        rotation THE NEXT RUN FAILS (the target still trusts only the old key).
#   host step -- the OPERATOR's action (nothing in the product writes authorized_keys, ADR-0079 D3): install the new line.
#   spec B    (quickstart-b)          -- the same Run completes again, its events are in the audit surface, and the demo target
#                                        is removed through the cockpit. Then setup.sh --remove-demo undoes the host side.
#
# The script path (setup-loopback-demo.sh generate / bind / --undo, FRX-6) is covered by quickstart-shell-gate.sh and is not
# touched here.
#
# The Playwright runner is a plain `docker run` on bringup-demo.sh's own `aireos-demo` network, targeting the docker network
# name (Windows-host HTTPS to published ports is broken on this dev box; in-network clients work). Screenshots go to
# dist/t176d-evidence/.
#
#   bash infra/offline/wd1/quickstart-browser-gate.sh              # happy path
#   bash infra/offline/wd1/quickstart-browser-gate.sh no-install    # VER-16 negative leg: skip the host step; spec B must FAIL
#
# Prereqs: same as bringup-demo.sh -- a from-HEAD bundle in dist/offline/ + the postgres companion.
set -euo pipefail
export MSYS_NO_PATHCONV=1
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../.." && pwd)"
SMOKE_DIR="$REPO_ROOT/tests/smoke"
EVIDENCE="$REPO_ROOT/dist/t176d-evidence"
HOST=aireos-demo-host
SSHD=aireos-demo-sshd
NET=aireos-demo
KEYVOL=aireos-demo-keys
ACCOUNT=aireos-demo
OPPW=demo-operator-pw
PW_IMAGE=mcr.microsoft.com/playwright:v1.49.1-noble
STATE_FILE="$SMOKE_DIR/.quickstart-resource-id"
LINES_FILE="$SMOKE_DIR/.quickstart-lines.json"
DONE_FILE="$SMOKE_DIR/.quickstart-b-done"
RUNNER=aireos-quickstart-runner
MODE="${1:-happy}" # "no-install" is VER-16's negative leg

log() { printf '\033[1;35m[quickstart-browser]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[quickstart-browser] ERROR:\033[0m %s\n' "$*" >&2; exit 1; }

cleanup() {
  log "tear down the runner + demo stack"
  docker rm -f "$RUNNER" >/dev/null 2>&1 || true
  docker rm -f "$HOST" "$SSHD" >/dev/null 2>&1 || true
  docker volume rm "$KEYVOL" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
  rm -f "$STATE_FILE" "$DONE_FILE" "$LINES_FILE"
}
trap cleanup EXIT

# pw <spec-file> [docker -e args...] -- one Playwright run in the pinned image on the demo network.
pw() {
  local spec="$1"; shift
  docker run --rm --name "$RUNNER" --network "$NET" -v "$SMOKE_DIR":/smoke -v "$EVIDENCE":/evidence \
    -e SMOKE_BASE_URL="https://$HOST" -e QUICKSTART_OPERATOR_PASS="$OPPW" -e QUICKSTART_EVIDENCE_DIR=/evidence \
    -e QUICKSTART_STATE_FILE=/smoke/.quickstart-resource-id -e QUICKSTART_LINES_FILE=/smoke/.quickstart-lines.json "$@" \
    -w /smoke "$PW_IMAGE" sh -c "npm install --no-audit --no-fund >/dev/null 2>&1 && npx playwright test $spec --project=chromium"
}

rm -rf "$EVIDENCE"; mkdir -p "$EVIDENCE"; rm -f "$STATE_FILE" "$DONE_FILE" "$LINES_FILE"

log "bring-up (bringup-demo.sh --demo --no-seed-resource) -- real setup.sh from the built bundle, AIREOS_LOOPBACK_DEMO=yes, systemd-in-Docker, no resource"
bash "$SCRIPT_DIR/bringup-demo.sh" --demo --no-seed-resource

log "host plane, before: setup.sh prepared the demo (account + one restricted authorized_keys entry), and no resource exists"
docker exec "$HOST" id "$ACCOUNT" >/dev/null || die "setup.sh did not create the $ACCOUNT account"
PRE_LINE="$(docker exec "$HOST" cat "/home/$ACCOUNT/.ssh/authorized_keys")"
[ "$(wc -l <<<"$PRE_LINE")" = 1 ] && grep -q '^restrict,.* ssh-ed25519 ' <<<"$PRE_LINE" || die "expected exactly one restricted ssh-ed25519 entry in $ACCOUNT's authorized_keys, got: $PRE_LINE"

log "spec A -- steps 1+3: log in, ONE click on 'Create loopback demo target' (no paste), audit events"
pw quickstart-a.smoke.spec.ts
[ -s "$STATE_FILE" ] || die "spec A did not write a resource id to $STATE_FILE"
RID="$(cat "$STATE_FILE")"
log "spec A PASSED -- created resource id=$RID via POST /v1/demo-target"

log "host plane: the pending key's line is still the one setup.sh installed (the cockpit writes nothing to the target)"
[ "$(docker exec "$HOST" cat "/home/$ACCOUNT/.ssh/authorized_keys")" = "$PRE_LINE" ] || die "authorized_keys changed without the operator doing it"

log "spec C -- the credential panel: Run completes, panel shows the public line, rotate with confirmation, the next Run FAILS"
pw quickstart-credential.smoke.spec.ts -e QUICKSTART_RESOURCE_ID="$RID"
[ -s "$LINES_FILE" ] || die "spec C did not write $LINES_FILE"
OLD_LINE="$(sed -n 's/.*"oldLine":"\([^"]*\)".*/\1/p' "$LINES_FILE")"
NEW_LINE="$(sed -n 's/.*"newLine":"\([^"]*\)".*/\1/p' "$LINES_FILE")"
[ -n "$OLD_LINE" ] && [ -n "$NEW_LINE" ] && [ "$OLD_LINE" != "$NEW_LINE" ] || die "spec C's lines are missing or identical"
case "$PRE_LINE" in "$OLD_LINE"|"$OLD_LINE "*) ;; *) false ;; esac || die "the line the panel showed before rotation is not the one setup.sh installed on the target (setup.sh appends a trailing comment; the options and key must match)"
log "spec C PASSED -- the panel showed the installed line; after rotation the Run failed"

if [ "$MODE" = "no-install" ]; then
  log "VER-16 negative leg: SKIPPING the host step on purpose -- spec B's Run must NOT complete"
else
  log "host step (the OPERATOR's action, ADR-0079 D3): install the new line as the demo account"
  docker exec -i "$HOST" su -s /bin/bash "$ACCOUNT" -c 'umask 077; cat > ~/.ssh/authorized_keys.new && mv ~/.ssh/authorized_keys.new ~/.ssh/authorized_keys' <<<"$NEW_LINE"
  [ "$(docker exec "$HOST" cat "/home/$ACCOUNT/.ssh/authorized_keys")" = "$NEW_LINE" ] || die "the new line was not installed"
  log "host step PASSED -- the target now trusts the new key only"
fi

# A SKIPPED Playwright test still exits 0, so the exit code alone cannot distinguish "spec B passed" from "spec B never ran":
# clear the marker, then require it back (a positive marker, not the absence of a failure).
rm -f "$DONE_FILE"
log "spec B -- steps 5/6/7-cockpit: the Run completes again, audit events, remove the demo target"
if [ "$MODE" = "no-install" ]; then
  rc=0; pw quickstart-b.smoke.spec.ts -e QUICKSTART_RESOURCE_ID="$RID" >/dev/null 2>&1 || rc=$?
  [ "$rc" != 0 ] && [ ! -f "$DONE_FILE" ] || die "negative leg: spec B passed (rc=$rc) although the target trusts only the OLD key -- the gate cannot fail"
  log "T-176d NEGATIVE LEG PASS -- without the operator's host step, spec B failed (rc=$rc) and wrote no completion marker"
  exit 0
fi
pw quickstart-b.smoke.spec.ts -e QUICKSTART_RESOURCE_ID="$RID"
[ -f "$DONE_FILE" ] || die "spec B did not run to completion -- no $DONE_FILE marker (a skipped Playwright test exits 0)"
log "spec B PASSED -- completion marker present"

log "host step: setup.sh --remove-demo exits 0 after a rotation (it removes only the entry IT installed, marked by its comment)"
BUNDLE_DIR="$(docker exec "$HOST" bash -c 'ls -d /root/inst/aireos-offline-*/ | head -1')"
RM_OUT="$(docker exec -e AIREOS_REMOVE_DEMO_ACCOUNT=no "$HOST" bash -c "cd '$BUNDLE_DIR' && ./setup.sh --remove-demo" 2>&1)" || { echo "$RM_OUT"; die "setup.sh --remove-demo failed"; }
echo "$RM_OUT" | tail -5
# A rotation is the OPERATOR installing a new, unmarked line (ADR-0079 D3), so --remove-demo correctly finds none of its own to remove
# and leaves the operator's line alone. Say so out loud: the line is the operator's to delete.
grep -qF 'nothing to remove there' <<<"$RM_OUT" || die "--remove-demo did not report that the operator-installed rotated line is not its own"
docker exec "$HOST" grep -qF "$NEW_LINE" "/home/$ACCOUNT/.ssh/authorized_keys" || die "--remove-demo touched the operator-installed line"

for s in 01-demo-button-before 02-demo-target-created 03-run-completes-before-rotation 04-credential-panel 05-rotate-confirmation 06-credential-rotated-new-line 07-run-fails-after-rotation 08-run-passes-after-new-line-installed; do
  [ "$(wc -c <"$EVIDENCE/$s.png")" -gt 5000 ] || die "screenshot $s.png is missing or empty"
done
log "T-176d PASS -- no-paste quickstart gate: one click, credential panel, rotation consequence shown and recovered. Screenshots:"
ls -1 "$EVIDENCE"/*.png
