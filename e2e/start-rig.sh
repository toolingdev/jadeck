#!/usr/bin/env bash
# Starts a blank jadeck plus the simulator (4 cameras, vMix, Proclaim, WING)
# and waits until the simulated gear is connected. Used by CI; works the
# same on a Mac.
#
#   e2e/start-rig.sh <ptz binary> <ptzsim binary> [data dir]
#
# jadeck answers on $PTZ_PORT (default 8080). Logs go to <data dir>/../*.log.
# Stop both with: kill $(cat <data dir>/../pids)
# SIM_ARGS adds simulator flags (other ports when one is already running).
set -euo pipefail

PTZ="$(cd "$(dirname "$1")" && pwd)/$(basename "$1")"
SIM="$(cd "$(dirname "$2")" && pwd)/$(basename "$2")"
DATA="${3:-$(mktemp -d)/data}"
PORT="${PTZ_PORT:-8080}"
BASE="http://127.0.0.1:$PORT"
mkdir -p "$DATA"
DIR="$(cd "$DATA/.." && pwd)"

wait_for() { # <what> <seconds> <command...>
  local what=$1 secs=$2; shift 2
  for _ in $(seq 1 "$secs"); do "$@" >/dev/null 2>&1 && return 0; sleep 1; done
  echo "timed out waiting for $what"; return 1
}

PTZ_DATA_DIR="$DATA" PTZ_PORT="$PORT" "$PTZ" run >"$DIR/ptz.log" 2>&1 &
echo $! >"$DIR/pids"
wait_for "jadeck" 60 curl -sf "$BASE/api/version"

# jadeck unpacks its MediaMTX on first start; the simulator's cameras use it too.
MTX="$DATA/video/bin/mediamtx"
wait_for "MediaMTX in $MTX" 60 test -x "$MTX"

"$SIM" --cameras 4 --mediamtx "$MTX" --program "" --setup "$BASE" ${SIM_ARGS:-} >"$DIR/ptzsim.log" 2>&1 &
echo $! >>"$DIR/pids"

ready() {
  curl -sf "$BASE/api/connections" | python3 -c '
import json, sys
c = json.load(sys.stdin)
c = c.get("connections", c) if isinstance(c, dict) else c
need = {"vmix", "proclaim", "behringer-wing"}
ok = {x.get("addon") for x in c if x.get("ready")}
sys.exit(0 if need <= ok else 1)'
}
wait_for "simulated vMix, Proclaim and WING to connect" 90 ready
cams=$(curl -sf "$BASE/api/cameras" | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')
echo "rig ready at $BASE: $cams cameras, vMix, Proclaim, WING (data in $DATA)"
