#!/usr/bin/env bash
# quickstart-shell-gate.sh — S-26/T-133a: the SHELL half of the OI-36
# quickstart gate. Drives docs/devops/quickstart-first-governed-action.md's
# steps 2 (generate), 4 (bind), and 7's host-side half (--undo) against the
# BUILT BUNDLE's OWN shipped copy of setup-loopback-demo.sh — never
# infra/offline/'s copy — on a fresh systemd-in-Docker host installed by the
# real bundled install.sh. That distinction is OI-36 itself ("from the built
# bundle, not from the repo, and not with repo defaults"): no .md ships in
# the bundle, so an operator runs install.sh + setup-loopback-demo.sh, which
# DO ship, and this gate must prove THOSE bytes, not the repo's.
#
# OUT OF SCOPE (T-133b, no browser here): quickstart steps 1 (cockpit login),
# 3 (create the resource via the "Create loopback demo target" cockpit
# action), 5 (compose + launch a Run), 6 (the /audit page), and step 7's
# cockpit half ("Remove the demo target"). See the T-133b SEAM note below for
# exactly where this gate stands in for the missing browser leg.
#
# Reuses infra/offline/wd1/bringup-demo.sh (Commander audit finding E) for
# the bring-up primitive itself, rather than duplicating it: real install.sh
# under systemd-in-Docker, Caddy published on :8443. bringup-demo.sh ALSO
# seeds its own, unrelated fire-agent demo resource (requires_approval=true,
# a separate aireos-demo-sshd container on the docker network) — harmless
# here; this script never touches it and drives the quickstart's OWN
# loopback-demo resource (127.0.0.1, the installed host SSHing to itself,
# ONB-3) independently, using a distinct resource id.
#
#   bash infra/offline/wd1/quickstart-shell-gate.sh
#
# Prereqs: same as bringup-demo.sh — a from-HEAD bundle in
# dist/offline/aireos-offline-*.tar.gz + the postgres companion (both already
# on disk for v0.13.0; do NOT rebuild — see the ticket brief's finding F).
set -euo pipefail
export MSYS_NO_PATHCONV=1
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
HOST=aireos-demo-host
SSHD=aireos-demo-sshd
NET=aireos-demo
KEYVOL=aireos-demo-keys
ACCOUNT=aireos-demo
# Distinct from bringup-demo.sh's own fire-agent resource id
# (...0000000000e2) — two independent resources coexist on the same stack.
RID=01900000-0000-7000-8000-0000000000e3

log() { printf '\033[1;32m[quickstart]\033[0m %s\n' "$*"; }
die() { printf '\033[1;31m[quickstart] ERROR:\033[0m %s\n' "$*" >&2; exit 1; }

cleanup() {
  log "tear down the demo stack"
  docker rm -f "$HOST" "$SSHD" >/dev/null 2>&1 || true
  docker volume rm "$KEYVOL" >/dev/null 2>&1 || true
  docker network rm "$NET" >/dev/null 2>&1 || true
}
trap cleanup EXIT

log "bring-up (reusing bringup-demo.sh, finding E) -- real install.sh from the built bundle, systemd-in-Docker, Caddy on :8443"
bash "$SCRIPT_DIR/bringup-demo.sh"

log "locate the BUNDLE's OWN copy of setup-loopback-demo.sh on the installed host (OI-36)"
BUNDLE_DIR="$(docker exec "$HOST" bash -c 'ls -d /root/inst/aireos-offline-*/ | head -1')"
[ -n "$BUNDLE_DIR" ] || die "could not locate the extracted bundle dir on $HOST"
docker exec "$HOST" test -f "${BUNDLE_DIR}setup-loopback-demo.sh" \
  || die "the bundle at $BUNDLE_DIR does not carry setup-loopback-demo.sh -- OI-36 requires driving THIS copy, not infra/offline/'s"
log "bundle's setup-loopback-demo.sh confirmed at ${BUNDLE_DIR}setup-loopback-demo.sh"

log "quickstart step 2 precondition: create the dedicated local demo account (doc: useradd -m -s /bin/bash aireos-demo)"
docker exec "$HOST" useradd -m -s /bin/bash "$ACCOUNT"

log "quickstart step 2: generate (bundle copy, loopback target 127.0.0.1)"
GEN_OUT="$(docker exec "$HOST" bash -c "cd '$BUNDLE_DIR' && ./setup-loopback-demo.sh generate --account $ACCOUNT")"
echo "$GEN_OUT"
echo "$GEN_OUT" | grep -qE 'host:[[:space:]]*127\.0\.0\.1' || die "step 2: generate did not print the expected 127.0.0.1 host line -- output above"
echo "$GEN_OUT" | grep -qE "username:[[:space:]]*$ACCOUNT" || die "step 2: generate did not print the expected username line -- output above"
HOSTKEY_LINE="$(echo "$GEN_OUT" | sed -n 's/^ *ssh host key: //p')"
[ -n "$HOSTKEY_LINE" ] || die "step 2: generate did not print an ssh host key line -- output above"
log "step 2 PASSED -- generate produced host=127.0.0.1 port=22 username=$ACCOUNT + a host key line"

# T-133b SEAM: quickstart step 3 is "click 'Create loopback demo target' in
# the cockpit", which drives the ordinary POST /v1/resources path (ONB-4: no
# demo-only code path). This shell-only gate has no browser, so it cannot
# exercise that path or prove ONB-4. It obtains a resource id the cleanest
# way available now instead -- a DIRECT DB INSERT, the SAME pattern
# bringup-demo.sh already uses for its own unrelated resource -- so step 4
# (bind) has a real id to bind against. THIS IS A KNOWN GAP, not a stand-in
# claim of ONB-4 coverage: T-133b must replace this insert with a real
# POST /v1/resources call through the cockpit UI on this same shell chain.
log "T-133b SEAM: step 3 (cockpit 'Create loopback demo target') is out of scope here -- inserting the resource row directly to obtain an id for step 4, NOT via POST /v1/resources. T-133b replaces this with the real UI flow."
LABELS="$(printf '{"ssh_host":"127.0.0.1","ssh_port":"22","ssh_user":"%s","ssh_host_key":"%s"}' "$ACCOUNT" "$HOSTKEY_LINE")"
docker exec -i "$HOST" su - postgres -c "psql -v ON_ERROR_STOP=1 -d cockpit" <<SQL
INSERT INTO resources (id, type, name, labels)
VALUES ('$RID', 'ssh', 'quickstart-loopback-demo', '$LABELS'::jsonb)
ON CONFLICT (id) DO UPDATE SET labels = EXCLUDED.labels;
SQL

log "quickstart step 4: bind (bundle copy)"
BIND_OUT="$(docker exec "$HOST" bash -c "cd '$BUNDLE_DIR' && ./setup-loopback-demo.sh bind --resource-id $RID")"
echo "$BIND_OUT"
echo "$BIND_OUT" | grep -q "bound" || die "step 4: bind did not report success -- output above"
log "step 4 PASSED -- bind reported success"

log "quickstart step 7 (host half): --undo (bundle copy)"
UNDO_OUT="$(docker exec "$HOST" bash -c "cd '$BUNDLE_DIR' && ./setup-loopback-demo.sh --undo")"
echo "$UNDO_OUT"
echo "$UNDO_OUT" | grep -q "undo complete" || die "step 7: --undo did not report completion -- output above"
log "step 7 (host half) PASSED -- undo ran to completion"

log "T-133a PASS -- quickstart shell steps (2 generate, 4 bind, 7 --undo) all ran successfully from the BUNDLE's OWN copy of setup-loopback-demo.sh, on a fresh systemd-in-Docker host installed by the real bundled install.sh. Browser steps 1/3/5/6 and step 7's cockpit half are T-133b's."
