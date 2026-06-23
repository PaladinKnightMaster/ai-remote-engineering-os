#!/usr/bin/env bash
# fire-agent.sh — drive ONE agent remote.exec that PARKS for operator approval, for
# the §human WD-4 walkthrough on the persistent demo stack (bringup-demo.sh stages
# this into the host at /usr/local/bin/fire-agent.sh). It runs INSIDE the host
# container, which has curl + Caddy on localhost. Usage on the demo host:
#
#   docker exec aireos-demo-host fire-agent.sh ["echo your command"]
#
# Then APPROVE (or deny) the parked invocation in the browser at
# https://localhost:8443/invocations — the held call below returns the result.
# Approve within ~2 min: the gateway block-and-waits ~125s then fail-closes.
set -uo pipefail
BASE="https://localhost"
OP_USER="admin"
OP_PW="${DEMO_OP_PW:-demo-operator-pw}"
RID="${DEMO_RID:-01900000-0000-7000-8000-0000000000e2}"
CMD="${1:-echo HELLO-FROM-THE-AGENT-$(date +%H%M%S)}"
JAR="$(mktemp)"

echo "[fire-agent] operator login..."
curl -sk -c "$JAR" -o /dev/null -X POST "$BASE/v1/auth/login" \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"$OP_USER\",\"password\":\"$OP_PW\"}"
CSRF="$(awk '/aireos_csrf/{v=$7} END{print v}' "$JAR")"
[ -n "$CSRF" ] || { echo "login failed (no csrf cookie)"; exit 1; }

echo "[fire-agent] minting a scoped agent-tool token (asymmetric, ADR-0060)..."
TOK="$(curl -sk -b "$JAR" -X POST "$BASE/v1/agent-tokens" \
  -H 'Content-Type: application/json' -H "X-CSRF-Token: $CSRF" \
  -d "{\"agentId\":\"demo\",\"resourceId\":\"$RID\",\"operations\":[\"exec\"]}" \
  | sed -E 's/.*"token":"([^"]+)".*/\1/')"
case "$TOK" in v4.public.*) ;; *) echo "agent-token mint failed: $TOK"; exit 1;; esac
echo "[fire-agent] OK — token is v4.public (api signed it with the agent-plane key)."

echo
echo "  >>> The agent now calls remote.exec; it will PARK and block for approval. <<<"
echo "  >>> Open  https://localhost:8443/invocations  and APPROVE/deny it (command: $CMD) <<<"
echo
REQ="{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"tools/call\",\"params\":{\"name\":\"remote.exec\",\"arguments\":{\"resourceId\":\"$RID\",\"command\":\"$CMD\"}}}"
RESP="$(curl -sk --max-time 180 "$BASE/mcp" -H "Authorization: Bearer $TOK" -H 'Content-Type: application/json' -d "$REQ")"
echo "[fire-agent] gateway returned:"
echo "$RESP"
echo
if printf '%s' "$RESP" | grep -q '"isError":true'; then
  echo "[fire-agent] → denied or timed out: NO command executed (the fail-closed path)."
else
  echo "[fire-agent] → the stdout above was approved + executed by the KEYLESS gateway."
fi
