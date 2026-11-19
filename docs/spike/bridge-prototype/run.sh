#!/bin/bash
# usage: run.sh <name> <delaySeconds> <slowSeconds> <timeoutKey|none> <noRefresh 0|1>
set -u
S="$(cd "$(dirname "$0")" && pwd)"
W="$(cygpath -m -l "$S")"
N=$1; DELAY=$2; SLOW=$3; TMO=$4; NOREF=$5
O="$S/out/$N"; rm -rf "$O"; mkdir -p "$O/shared"
NET="bspike-$N"
export MSYS_NO_PATHCONV=1
docker rm -f "$N-gw" "$N-model" "$N-cline" >/dev/null 2>&1
docker network rm "$NET" >/dev/null 2>&1
docker network create --internal "$NET" >/dev/null
docker run -d --name "$N-gw" --network "$NET" --network-alias gateway -e SPIKE_OUT=/o/shared -e SLOW_SECONDS="$SLOW" -v "$W/out:/bin2:ro" -v "$W/out/$N:/o" debian:bookworm-slim sh -c '/bin2/bridgespike >/o/gateway.log 2>&1' >/dev/null
docker run -d --name "$N-model" --network "$NET" --network-alias fakemodel -e DELAY_SECONDS="$DELAY" -e MODEL_LOG=/o/model.log -v "$W/fakemodel:/m:ro" -v "$W/out/$N:/o" node:22-bookworm-slim node /m/server.js >/dev/null
for i in 1 2 3 4 5 6 7 8 9 10; do [ -s "$O/shared/ca.sha256" ] && [ -s "$O/model.log" ] && break; sleep 1; done
if [ "$TMO" = none ]; then TJ=""; else TJ="\"timeout\": $TMO,"; fi
PIN="$(tr -d '\n' < "$O/shared/ca.sha256")"
cat > "$O/cline_mcp_settings.json" <<J
{"mcpServers":{"aireos":{"type":"stdio","command":"/bin2/aireos-mcp-bridge","args":[],$TJ
 "env":{"AIREOS_GATEWAY_URL":"https://gateway:8443/mcp","AIREOS_EXCHANGE_URL":"https://gateway:8444/v1/agent-tokens/exchange",
  "AIREOS_CREDENTIAL_FILE":"/o/shared/credential","AIREOS_CA_FILE":"/o/shared/ca.pem","AIREOS_CA_SHA256":"$PIN",
  "AIREOS_BRIDGE_LOG":"/o/bridge.log","AIREOS_BRIDGE_NO_REFRESH":"$NOREF"},
 "autoApprove":["remote.exec"],"disabled":false}}}
J
date -u +%FT%TZ > "$O/start.txt"
docker run --name "$N-cline" --network "$NET" -v "$W/out:/bin2:ro" -v "$W/out/$N:/o" bridge-spike-cline:3.0.74 sh -c '
  mkdir -p /root/.cline/data/settings && cp /o/cline_mcp_settings.json /root/.cline/data/settings/ &&
  cline auth -p openai-compatible -k fake-key -m fake-model -b http://fakemodel:8000/v1 >/o/cline-auth.log 2>&1; echo auth=$? >>/o/cline-auth.log;
  cline --json -P openai-compatible -m fake-model "Run five commands on the target with the aireos remote exec tool, then finish." >/o/cline.jsonl 2>/o/cline.stderr; echo $? >/o/cline.exit'
date -u +%FT%TZ > "$O/end.txt"
docker logs "$N-model" >"$O/model.stdout" 2>&1
docker rm -f "$N-gw" "$N-model" "$N-cline" >/dev/null 2>&1
docker network rm "$NET" >/dev/null
echo "run $N done: cline exit=$(cat "$O/cline.exit" 2>/dev/null)"
