#!/usr/bin/env bash
# docs-rehearsal.sh — DOC-41 / HUM-1 made checkable (T-177): do the server command lines the install guide PRESENTS actually
# take a fresh later host from "files copied" to "the browser shows the setup page", and are there at most three of them?
#
# It reads the guide, not this script: the lines are EXTRACTED from the fenced ```sh blocks between the guide's
# <!-- rehearsal:begin --> / <!-- rehearsal:end --> markers (the user path on the server, "files copied" -> "setup page"),
# counted (HUM-1: at most 3; `cd` counts), the guide's blanks filled in (<version> <fingerprint> <address> <os>; ANY other
# <blank> left in a line is a failure), and run VERBATIM, in order, as root on a fresh later host (the host image
# later-host-test.sh builds: the OS's LATEST image + a full upgrade, no network), with the download folder laid out as the
# guide says. The release key is a THROWAWAY minted here in a temp dir; the artifacts are signed copies of dist/offline/'s.
#
# Asserted: line 1 prints `ALL VERIFIED:` naming BOTH archives; the tar line leaves the unpacked folder; the setup line exits 0
# and prints a setup code, the intermediate-certificate fingerprint line and the address to open; and GET /v1/setup through Caddy
# answers setupRequired=true. It answers setup.sh's two questions the way the guide tells the reader (profile `lan-only`, demo
# Yes) on a real pty. `sudo` is a harness shim (the image has none): it runs its command with the VAR=value words applied, which
# is what sudo does for a user whose sudoers entry allows it.
#
# Negative controls (rule 12), in the same run: (1) the verification line must print NOT VERIFIED for a flipped byte in the
# companion (naming it), a wrong fingerprint and the unreplaced placeholder; (2) a guide copy with a 4th command line must fail the
# count; (3) a guide copy whose `tar` line names the wrong file must fail the rehearsal. If a mutated guide PASSES, this script
# fails: the rehearsal could not have told.
#
# Usage: bash infra/offline/docs-rehearsal.sh [--guide FILE]       (default docs/devops/offline-install-v3.md; ubuntu2404 only)
# Needs Docker, dist/offline/ output of `task offline-bundle` + the companions (same as later-host-test.sh) and openssl on the host.
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$HERE/../.." && pwd)"
DIST="$REPO_ROOT/dist/offline"; OS=ubuntu2404; IMAGE="aireos-later-host-$OS"
GUIDE="$REPO_ROOT/docs/devops/offline-install-v3.md"
[ "${1:-}" != --guide ] || { GUIDE="$2"; shift 2; }
log() { echo "[docs-rehearsal] $*"; }
die() { echo "[docs-rehearsal] FAIL: $*" >&2; exit 1; }
docker() { MSYS_NO_PATHCONV=1 command docker "$@"; }
TMP="$(mktemp -d)"; TMPW="$(cd "$TMP" && { pwd -W 2>/dev/null || pwd; })"   # TMPW: the path Docker can mount (Windows git-bash: C:/...)
cleanup() { docker ps -aq --filter name=aireos-docsreh | xargs -r docker rm -f >/dev/null 2>&1 || true; rm -rf "$TMP"; }   # hosts are made inside subshells: remove by name
trap cleanup EXIT

# ---- the guide's lines -------------------------------------------------------------------------------------------------
extract() { awk '/<!-- rehearsal:begin -->/{on=1;next} /<!-- rehearsal:end -->/{on=0} on && /^```sh$/{f=1;next} on && /^```$/{f=0} on&&f&&NF' "$1"; }
LINES_MAX=3

# ---- the throwaway release key and signed copies of the real artifacts ---------------------------------------------------------
CORE="$(basename "$(ls "$DIST"/aireos-offline-*.tar.gz | head -1)")"; VERSION="${CORE#aireos-offline-}"; VERSION="${VERSION%.tar.gz}"
COMP="aireos-postgres-$OS.tar.gz"
[ -f "$DIST/$CORE" ] && [ -f "$DIST/$COMP" ] || die "need $DIST/$CORE and $DIST/$COMP (task offline-bundle; build-postgres-companions.sh $OS)"
mkdir -p "$TMP/dl"
openssl genpkey -algorithm ed25519 -out "$TMP/throwaway.key" 2>/dev/null   # NEVER the repo's, never the production key
openssl pkey -in "$TMP/throwaway.key" -pubout -out "$TMP/dl/aireos-release.pub"
cp "$DIST/$CORE" "$DIST/$COMP" "$TMP/dl/"
for f in "$CORE" "$COMP"; do openssl pkeyutl -sign -rawin -inkey "$TMP/throwaway.key" -in "$TMP/dl/$f" -out "$TMP/dl/$f.sig"; done
FP="$(openssl pkey -pubin -in "$TMP/dl/aireos-release.pub" -pubout -outform DER | openssl dgst -sha256 | awk '{print $NF}')"

fill() {   # one guide line -> the line with the blanks filled; a blank left over is a failure
  local l="$1"
  l="${l//<version>/$VERSION}"; l="${l//<fingerprint>/$FP}"; l="${l//<address>/localhost}"; l="${l//<os>/$OS}"
  printf '%s' "$l"
}

# ---- a fresh later host, laid out as the guide says --------------------------------------------------------------------------
new_host() {   # $1=name
  docker rm -f "$1" >/dev/null 2>&1 || true
  docker run -d --platform linux/amd64 --name "$1" --network none --privileged --cgroupns=host --tmpfs /run --tmpfs /run/lock \
    -v /sys/fs/cgroup:/sys/fs/cgroup:rw -v "$TMPW/dl":/dl:ro "$IMAGE" >/dev/null
  local i; for i in $(seq 1 90); do case "$(docker exec "$1" systemctl is-system-running 2>&1 || true)" in running|degraded) break;; esac; sleep 1; done
  docker exec "$1" bash -c 'mkdir -p /root/dl && cp /dl/* /root/dl/ && printf "#!/bin/sh\nexec env \"\$@\"\n" >/usr/local/bin/sudo && chmod +x /usr/local/bin/sudo' >/dev/null
}
# run <container> <line> : the line as typed, in /root/dl, on a pty with the guide's answers on stdin. Sets OUT (ANSI and CR stripped) and RC.
run() {
  OUT="$(printf 'lan-only\ny\n' | docker exec -i -w /root/dl -e LINE="$2" "$1" script -qec 'bash -c "$LINE"' /dev/null 2>&1)" && RC=0 || RC=$?
  OUT="$(printf '%s' "$OUT" | tr -d '\r' | sed 's/\x1b\[[0-9;]*m//g')"
}
code_re='[0-9A-Z]{5}(-[0-9A-Z]{5}){3}'

# rehearse <guide> <host-name> : 0 = every claim held; 1 = a step failed (printed). Never exits the script.
rehearse() {
  local guide="$1" host="$2" lines n i l
  mapfile -t lines < <(extract "$guide")
  n="${#lines[@]}"; log "the guide presents $n server command line(s) between 'files copied' and 'the browser shows the setup page' (HUM-1: at most $LINES_MAX)"
  [ "$n" -ge 1 ] && [ "$n" -le "$LINES_MAX" ] || { echo "FAIL: $n command lines"; return 1; }
  for l in "${lines[@]}"; do
    l="$(fill "$l")"
    [[ "$l" != *'<'[a-z]*'>'* ]] || { echo "FAIL: a blank is left in a line the guide shows: $l"; return 1; }
  done
  new_host "$host"
  for i in "${!lines[@]}"; do
    l="$(fill "${lines[$i]}")"; log "line $((i+1))/$n: ${l:0:110}$([ ${#l} -gt 110 ] && echo …)"
    run "$host" "$l"
    if [ "$RC" != 0 ]; then echo "$OUT" | tail -15; echo "FAIL: line $((i+1)) exited $RC"; return 1; fi
    case "$i" in
      0) grep -qF "ALL VERIFIED:" <<<"$OUT" && grep -qF "$CORE" <<<"$OUT" && grep -qF "$COMP" <<<"$OUT" || { echo "$OUT"; echo "FAIL: line 1 did not print ALL VERIFIED naming both archives"; return 1; }
         echo "  ok line 1: $(grep ALL <<<"$OUT")" ;;
      1) docker exec "$host" test -d "/root/dl/aireos-offline-$VERSION" && docker exec "$host" test -f "/root/dl/$COMP" || { echo "FAIL: line 2 left no unpacked folder, or the companion is missing"; return 1; }
         echo "  ok line 2: /root/dl/aireos-offline-$VERSION exists; the companion tarball is still beside it, unpacked by nobody" ;;
      2) echo "$OUT" | grep -v '^[[:space:]]*$' | cut -c1-210 | sed 's/^/    | /'   # the whole output, as a reader sees it
         grep -qF 'Create loopback demo target' <<<"$OUT" || { echo "FAIL: line 3 did not offer the demo target (the two answers the guide gives were not taken)"; return 1; }
         grep -Eq "Setup code: +$code_re" <<<"$OUT" || { echo "$OUT" | tail -20; echo "FAIL: line 3 printed no setup code"; return 1; }
         grep -qF 'Caddy Local Authority - ECC Intermediate' <<<"$OUT" || { echo "FAIL: line 3 did not name the intermediate certificate"; return 1; }
         grep -Eq "Open https://localhost/ in your browser" <<<"$OUT" || { echo "FAIL: line 3 did not end with the address to open"; return 1; }
         echo "  ok line 3: setup code, fingerprint line and the address to open were printed" ;;
    esac
    if [ "$i" = 2 ]; then SETUP_OUT="$OUT"; fi
  done
  OUT="$(docker exec "$host" curl -sk https://localhost/v1/setup)" || { echo "FAIL: GET /v1/setup through Caddy did not answer"; return 1; }
  grep -Eq '"setup_?[Rr]equired": *true' <<<"$OUT" || { echo "FAIL: GET /v1/setup did not say setup is required: $OUT"; return 1; }
  echo "  ok GET https://localhost/v1/setup (through Caddy) -> $OUT"
  OUT="$(docker exec "$host" curl -sk https://localhost/ | head -c 200)"; grep -qiE '<!doctype html|<html' <<<"$OUT" || { echo "FAIL: the setup page (SPA) is not served at /"; return 1; }
  echo "  ok https://localhost/ serves the cockpit page the setup form is on"
  return 0
}

# ---- 0. the instrument can fail: a 4th line, a wrong tar name ---------------------------------------------------------------
GUIDE_SRC="$(cat "$GUIDE")"
printf '%s\n' "$GUIDE_SRC" | awk '/^tar -xzf aireos-offline-<version>.tar.gz$/{print "cd /root/dl"} {print}' >"$TMP/guide-4lines.md"
printf '%s\n' "$GUIDE_SRC" | sed 's/^tar -xzf aireos-offline-<version>.tar.gz$/tar -xzf aireos-offline-<version>.tar.gz.old/' >"$TMP/guide-badtar.md"
[ "$(diff <(extract "$GUIDE") <(extract "$TMP/guide-badtar.md") | grep -c '^[<>]')" = 2 ] || die "(self-test) the bad-tar mutation did not change exactly one extracted line"
[ "$(extract "$TMP/guide-4lines.md" | wc -l)" = "$(( $(extract "$GUIDE" | wc -l) + 1 ))" ] || die "(self-test) the 4-line mutation did not add one extracted line"

log "NEGATIVE CONTROL 1: a guide with a 4th command line must fail the HUM-1 count"
if (rehearse "$TMP/guide-4lines.md" aireos-docsreh-n1) >"$TMP/n1.out" 2>&1; then cat "$TMP/n1.out"; die "NEGATIVE CONTROL 1 FAILED: a 4-line guide passed the rehearsal"; fi
tail -2 "$TMP/n1.out"; echo "  ok negative control 1: the 4-line guide was refused"

log "NEGATIVE CONTROL 2: a guide whose tar line names the wrong file must fail the rehearsal"
if (rehearse "$TMP/guide-badtar.md" aireos-docsreh-n2) >"$TMP/n2.out" 2>&1; then cat "$TMP/n2.out"; die "NEGATIVE CONTROL 2 FAILED: a guide with a wrong tar name passed"; fi
tail -6 "$TMP/n2.out"; echo "  ok negative control 2: the wrong tar name was refused"

# ---- 1. the verification line's failure modes, on copies, with the line EXTRACTED from the guide ------------------------------
V1="$(extract "$GUIDE" | head -1)"
new_host aireos-docsreh-v
log "the verification line, as extracted from the guide, on the host:"
verify_case() {   # $1=label $2=expected substring $3=a shell prefix that prepares the copy $4=fingerprint to fill in
  local line="${V1//<fingerprint>/$4}"
  run aireos-docsreh-v "rm -rf /root/vc && mkdir /root/vc && cp /root/dl/aireos-* /root/vc/ && cd /root/vc && $3 && $line"
  grep -qF "$2" <<<"$OUT" || { echo "$OUT"; die "verification case '$1': expected '$2' in the output"; }
  if [ "$2" != "ALL VERIFIED:" ]; then [ "$RC" != 0 ] || die "verification case '$1': expected a non-zero exit"; ! grep -qF "ALL VERIFIED" <<<"$OUT" || die "verification case '$1': printed ALL VERIFIED too"; fi
  echo "  ok $1 -> $(tail -1 <<<"$OUT")  (exit $RC)"
}
verify_case "the real fingerprint, untouched copies" "ALL VERIFIED:" true "$FP"
verify_case "one byte flipped in the companion" "NOT VERIFIED: $COMP" "printf '\\x00' | dd of=$COMP bs=1 seek=1000 conv=notrunc 2>/dev/null" "$FP"
verify_case "one byte flipped in the core" "NOT VERIFIED: $CORE" "printf '\\x00' | dd of=$CORE bs=1 seek=1000 conv=notrunc 2>/dev/null" "$FP"
verify_case "a missing .sig" "NOT VERIFIED: $CORE" "rm $CORE.sig" "$FP"
verify_case "a wrong fingerprint" "NOT VERIFIED: aireos-release.pub" true "$(printf '%s' "$FP" | tr 0-9a-f 1-9a-f0)"
verify_case "the unreplaced placeholder" "NOT VERIFIED: aireos-release.pub" true "<fingerprint>"

# ---- 2. the real rehearsal ----------------------------------------------------------------------------------------------
log "THE REHEARSAL: the guide's lines, verbatim, on a fresh later host ($IMAGE)"
if ! (rehearse "$GUIDE" aireos-docsreh-main); then die "the rehearsal failed (see above)"; fi
echo "DOCS-REHEARSAL PASS: $(extract "$GUIDE" | wc -l) command lines (limit $LINES_MAX); ALL VERIFIED; setup code printed; /v1/setup answers setup-required through Caddy; both negative controls and the verification failure modes held"
