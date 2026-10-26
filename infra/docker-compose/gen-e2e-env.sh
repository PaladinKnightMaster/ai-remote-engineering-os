#!/usr/bin/env bash
# gen-e2e-env.sh — generate the e2e/smoke stack's keys, FRESH on every run (T-168).
#
# docker-compose.e2e.yml used to carry its cluster keys as committed literals.
# They were throwaway test values, never shipped, but a public repo with
# secret-shaped strings in it raises secret-scanner incidents (GitGuardian) and
# trains readers to ignore real ones. They are now ${VAR} references that this
# script fills in, per run, into a git-ignored env file:
#
#   infra/docker-compose/.e2e.env      (override: E2E_ENV_FILE=/path)
#
# `task e2e` and `task smoke` run this first and pass the file to compose with
# --env-file. Running compose by hand? Run this script, then add
# `--env-file .e2e.env` to the docker compose command. Without it compose
# refuses to start (every key is a required ${VAR:?...} reference — fail loud,
# never a silent empty key).
#
# Every value comes from the repo's OWN primitives, so formats are exactly what
# the services parse (the same authctl install.sh uses):
#   authctl genkey      -> AIREOS_PASETO_KEY, AIREOS_RESOURCE_SECRET_KEY (32-byte hex)
#   authctl genkeypair  -> "<secret-hex> <public-hex>" (Ed25519; secret = seed||public)
#   authctl genaeskey   -> AIREOS_GATEWAY_COMMAND_KEY (AES-256, hex)
# and is checked before the file is moved into place: right length, hex only,
# each public key equals the tail of its secret (the pair really matches), and
# no two values are equal.
#
# No new host dependency: authctl runs in the golang:1.26 container the e2e
# stack already uses (the same builder and module-cache volumes as `task e2e`).
set -euo pipefail
export MSYS_NO_PATHCONV=1   # Windows/git-bash: don't mangle -v paths

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$HERE/../.." && pwd)"
OUT="${E2E_ENV_FILE:-$HERE/.e2e.env}"
# Docker Desktop on a Windows host needs a drive-letter path (D:/...) for -v;
# a git-bash /d/... path mounts as an EMPTY directory. `pwd -W` is git-bash only.
MOUNT="$(cd "$REPO_ROOT" && { pwd -W 2>/dev/null || pwd; })"

command -v docker >/dev/null 2>&1 || { echo "gen-e2e-env: docker not found -- the e2e stack needs it anyway" >&2; exit 1; }

umask 077
tmp="$(mktemp "$OUT.XXXXXX")"
trap 'rm -f "$tmp"' EXIT

docker run --rm -v "$MOUNT":/work -w /work \
  -v aireos-e2e_gomodcache:/go/pkg/mod -v aireos-e2e_gocache:/root/.cache/go-build \
  golang:1.26 bash -ec '
    go build -o /tmp/authctl ./packages/go/auth/cmd/authctl
    a=/tmp/authctl
    echo "AIREOS_PASETO_KEY=$($a genkey)"
    echo "AIREOS_RESOURCE_SECRET_KEY=$($a genkey)"
    echo "AIREOS_GATEWAY_COMMAND_KEY=$($a genaeskey)"
    read -r sk pk < <($a genkeypair); echo "AIREOS_AUDIT_CHAIN_SIGN_KEY=$sk"
    read -r sk pk < <($a genkeypair); echo "AIREOS_API_AGENT_SIGN_KEY=$sk"; echo "AIREOS_API_AGENT_PUBLIC_KEY=$pk"
    read -r sk pk < <($a genkeypair); echo "AIREOS_GATEWAY_SIGN_KEY=$sk"; echo "AIREOS_GATEWAY_PUBLIC_KEY=$pk"
  ' >"$tmp"

# --- validate before publishing the file --------------------------------------
declare -A want=(
  [AIREOS_PASETO_KEY]=64 [AIREOS_RESOURCE_SECRET_KEY]=64 [AIREOS_GATEWAY_COMMAND_KEY]=64
  [AIREOS_AUDIT_CHAIN_SIGN_KEY]=128 [AIREOS_API_AGENT_SIGN_KEY]=128 [AIREOS_API_AGENT_PUBLIC_KEY]=64
  [AIREOS_GATEWAY_SIGN_KEY]=128 [AIREOS_GATEWAY_PUBLIC_KEY]=64
)
declare -A got=()
while IFS='=' read -r k v; do got[$k]="$v"; done <"$tmp"
for k in "${!want[@]}"; do
  v="${got[$k]:-}"
  [[ "$v" =~ ^[0-9a-f]+$ ]] && [ "${#v}" -eq "${want[$k]}" ] \
    || { echo "gen-e2e-env: FAIL $k is missing or malformed (want ${want[$k]} hex chars) -- not writing $OUT" >&2; exit 1; }
done
[ "${#got[@]}" -eq "${#want[@]}" ] || { echo "gen-e2e-env: FAIL unexpected extra/missing keys -- not writing $OUT" >&2; exit 1; }
for pair in "AIREOS_API_AGENT_SIGN_KEY AIREOS_API_AGENT_PUBLIC_KEY" "AIREOS_GATEWAY_SIGN_KEY AIREOS_GATEWAY_PUBLIC_KEY"; do
  set -- $pair
  [ "${got[$1]: -64}" = "${got[$2]}" ] \
    || { echo "gen-e2e-env: FAIL $2 is not the public half of $1 -- not writing $OUT" >&2; exit 1; }
done
uniq_n="$(printf '%s\n' "${got[@]}" | sort -u | wc -l | tr -d ' ')"
[ "$uniq_n" -eq "${#want[@]}" ] || { echo "gen-e2e-env: FAIL two generated values are identical -- not writing $OUT" >&2; exit 1; }

mv -f "$tmp" "$OUT"
trap - EXIT
# A short fingerprint only -- never the values.
echo "gen-e2e-env: wrote ${#want[@]} fresh keys to $OUT (fingerprint: $(sha256sum "$OUT" | cut -c1-12))"
