#!/usr/bin/env bash
# AC Step 8: "THUMB_WORKERS=1: 200 thumbnails processed serially without the
# backend RSS growing by more than +150 MB". Runs the built binary on a scratch
# storage with 200 generated images, requests every thumbnail through the API
# and samples VmRSS of the Go process (tools run as separate processes).
#
#   bash backend/internal/thumbs/testdata/rss_probe.sh   # from the repo root
set -euo pipefail
ROOT=$(cd "$(dirname "$0")/../../../.." && pwd)
BIN="$ROOT/bin/filemanager"
[ -x "$BIN" ] || { echo "build first: make build"; exit 2; }
WORK=$(mktemp -d /tmp/fm-rss-XXXXXX)
STORAGE="$WORK/storage"; DATA="$STORAGE/.system"
mkdir -p "$DATA" "$STORAGE/admin/foto"
PORT=18099
cleanup() { kill "$PID" 2>/dev/null || true; rm -rf "$WORK"; }
trap cleanup EXIT

echo "generating 200 images…"
for i in $(seq 1 200); do
  /usr/bin/vips black "$STORAGE/admin/foto/img$i.png" $((400 + i)) $((300 + i)) 2>/dev/null
done

APP_ENV=development ALLOW_ROOT=true APP_BASE_URL="http://127.0.0.1:$PORT" LISTEN_ADDR="127.0.0.1:$PORT" \
TRUSTED_PROXIES=127.0.0.1 STORAGE_ROOT="$STORAGE" DATA_DIR="$DATA" APP_SECRET=$(openssl rand -hex 32) \
ADMIN_USERNAME=admin ADMIN_INITIAL_PASSWORD='Rss-Probe-Passw0rd!!' THUMB_WORKERS=1 LOG_LEVEL=warn \
"$BIN" serve >"$WORK/server.log" 2>&1 &
PID=$!
for _ in $(seq 1 50); do curl -fs "http://127.0.0.1:$PORT/healthz" >/dev/null 2>&1 && break; sleep 0.2; done
rss() { awk '/VmRSS/ {print $2}' "/proc/$PID/status"; }
BASE=$(rss); echo "baseline RSS: $((BASE / 1024)) MB"

J="$WORK/jar"; H=(-H "Origin: http://127.0.0.1:$PORT")
curl -fs -c "$J" -b "$J" "${H[@]}" -H 'Content-Type: application/json' -d '{"username":"admin","password":"Rss-Probe-Passw0rd!!"}' "http://127.0.0.1:$PORT/api/v1/auth/login" >/dev/null
CSRF=$(curl -fs -c "$J" -b "$J" "http://127.0.0.1:$PORT/api/v1/auth/csrf" | python3 -c 'import sys,json;print(json.load(sys.stdin)["csrfToken"])')
curl -fs -c "$J" -b "$J" "${H[@]}" -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
  -d '{"currentPassword":"Rss-Probe-Passw0rd!!","newPassword":"Rss-Probe-New-Passw0rd-2026"}' "http://127.0.0.1:$PORT/api/v1/auth/change-password" >/dev/null
PATHS=$(python3 -c 'import json;print(json.dumps([f"/foto/img{i}.png" for i in range(1,201)]))')
URLS=$(curl -fs -c "$J" -b "$J" "${H[@]}" -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
  -d "{\"paths\":$PATHS,\"purpose\":\"thumb\",\"size\":256}" "http://127.0.0.1:$PORT/api/v1/files/sign" | python3 -c 'import sys,json;[print(u["url"]) for u in json.load(sys.stdin)["urls"]]')

MAX=$BASE; READY=0; START=$(date +%s)
# fire every request once (queues 200 jobs, serial worker), then poll until all are 200
for u in $URLS; do curl -s -o /dev/null "http://127.0.0.1:$PORT$u"; done
while :; do
  READY=0
  for u in $URLS; do
    code=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT$u")
    [ "$code" = 200 ] && READY=$((READY + 1))
  done
  R=$(rss); [ "$R" -gt "$MAX" ] && MAX=$R
  echo "ready $READY/200, RSS now $((R / 1024)) MB, max $((MAX / 1024)) MB"
  [ "$READY" -ge 200 ] && break
  [ $(( $(date +%s) - START )) -gt 600 ] && { echo "timeout"; exit 1; }
  sleep 3
done
echo "RESULT: 200 thumbnails in $(( $(date +%s) - START )) s; RSS baseline $((BASE / 1024)) MB, max $((MAX / 1024)) MB, growth $(( (MAX - BASE) / 1024 )) MB (limit 150 MB)"
[ $(( (MAX - BASE) / 1024 )) -le 150 ]
