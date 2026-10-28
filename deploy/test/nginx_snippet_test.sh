#!/usr/bin/env bash
# deploy/test/nginx_snippet_test.sh — deploy/nginx-aapanel.conf inside a copy
# of the server block aaPanel 8.0.7 generates for a Reverse Proxy project
# (template from mod/project/proxy/comMod.py, http defaults from its
# proxy.conf), served by a throw-away Nginx on loopback in front of the real
# binary. Everything (config, pid, logs, temp, cache) lives in a scratch
# folder; the aaPanel configuration is never read or written. Needs
# bin/filemanager and an nginx binary (FM_NGINX, default: nginx on PATH).
set -euo pipefail
# shellcheck source=deploy/test/lib_test.sh
. "$(dirname -- "${BASH_SOURCE[0]}")/lib_test.sh"

BIN=${FM_TEST_BIN:-$REPO/bin/filemanager}
NGINX=${FM_NGINX:-$(command -v nginx || true)}
[[ -x $BIN ]] || { echo "FAIL: $BIN missing — run make build first"; exit 1; }
[[ -x $NGINX ]] || { echo "FAIL: nginx not found (set FM_NGINX)"; exit 1; }
SNIPPET=$REPO/deploy/nginx-aapanel.conf
# Step 23e: the optional second site (separate user-content origin) is
# rendered as a second server block of the same scratch Nginx, in front of
# the same app configured with CONTENT_BASE_URL=https://usercontent.test.
CONTENT_SNIPPET=$REPO/deploy/nginx-usercontent.conf
CONTENT_HOST=usercontent.test

T=$(new_root nginx)
APP_PID="" NGINX_PID=""
cleanup() {
	[[ -n $NGINX_PID ]] && kill "$NGINX_PID" 2>/dev/null && wait "$NGINX_PID" 2>/dev/null
	[[ -n $APP_PID ]] && kill "$APP_PID" 2>/dev/null && wait "$APP_PID" 2>/dev/null
	rm -rf "$T"
	rmdir "$REPO/.deploytest" 2>/dev/null || true
}
trap cleanup EXIT
# Nginx workers run as the build's default user (www on aaPanel): let them read the scratch site.
chmod 0755 "$T" "$REPO/.deploytest"
mkdir -p "$T/site/.well-known/acme-challenge" "$T/logs" "$T/cache" "$T/app" "$T/storage/.system"
echo "acme-token-123" >"$T/site/.well-known/acme-challenge/test"
chmod -R a+rX "$T/site"

APORT=$(free_port)
NPORT=$(free_port)
part() { awk -v b="# --- BEGIN PART $1" -v e="# --- END PART $1" 'index($0,b)==1{on=1;next} index($0,e)==1{on=0} on' "$SNIPPET"; }
PART_A=$(part A)
PART_B=$(part B | sed "s#http://127.0.0.1:8080#http://127.0.0.1:$APORT#")
[[ -n $PART_A && -n $PART_B ]] || { echo "FAIL: PART A/B markers not found in $SNIPPET"; exit 1; }
cpart() { awk -v b="# --- BEGIN PART $1" -v e="# --- END PART $1" 'index($0,b)==1{on=1;next} index($0,e)==1{on=0} on' "$CONTENT_SNIPPET"; }
CONTENT_A=$(cpart A)
CONTENT_B=$(cpart B)
[[ -n $CONTENT_A && -n $CONTENT_B ]] || { echo "FAIL: PART A/B markers not found in $CONTENT_SNIPPET"; exit 1; }

# aaPanel's Nginx has lua-nginx-module compiled in and needs its library path.
LUA_LINE=""
if "$NGINX" -V 2>&1 | grep -q lua_nginx_module; then LUA_LINE='lua_package_path "/www/server/nginx/lib/lua/?.lua;;";'; fi
cat >"$T/nginx.conf" <<EOF
worker_processes 1;
pid $T/nginx.pid;
error_log $T/logs/error.log warn;
events { worker_connections 128; }
http {
    default_type application/octet-stream;
    client_body_temp_path $T/tmp/body;
    proxy_temp_path       $T/tmp/proxy;
    fastcgi_temp_path     $T/tmp/fastcgi;
    uwsgi_temp_path       $T/tmp/uwsgi;
    scgi_temp_path        $T/tmp/scgi;
    log_format fm '\$remote_addr "\$request" \$status';
    # --- what aaPanel's nginx.conf + proxy.conf set for every site ---
    $LUA_LINE
    client_max_body_size 50m;
    proxy_cache_path $T/cache levels=1:2 keys_zone=cache_one:1m inactive=1d max_size=10m;
    proxy_cache cache_one;
    proxy_connect_timeout 60;
    proxy_read_timeout 60;
    proxy_send_timeout 60;
    proxy_next_upstream error timeout invalid_header http_500 http_503 http_404;
    map \$http_upgrade \$connection_upgrade { default upgrade; '' close; }
    # --- server block as rendered by the Reverse Proxy project template ---
    server {
        listen 127.0.0.1:$NPORT;
        server_name file.test;
        index index.php index.html index.htm default.php default.htm default.html;
        root $T/site;
        #WEBSOCKET-SUPPORT START
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
        #WEBSOCKET-SUPPORT END
        #PROXY-CONF-START
        location ^~ / {
            proxy_pass http://127.0.0.1:$APORT;
            proxy_set_header Host \$http_host;
            proxy_set_header X-Real-IP \$remote_addr;
            proxy_set_header X-Real-Port \$remote_port;
            proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
            proxy_set_header REMOTE-HOST \$remote_addr;
            proxy_connect_timeout 10s;
            proxy_send_timeout 3600s;
            proxy_read_timeout 3600s;
            proxy_http_version 1.1;
            proxy_set_header Upgrade \$http_upgrade;
            proxy_set_header Connection "upgrade";
$PART_A
        }
        #PROXY-CONF-END
        #SERVER-BLOCK START
$PART_B
        #SERVER-BLOCK END
        location ~ ^/(\.user.ini|\.htaccess|\.git|\.env|\.svn|\.project|LICENSE|README.md) { return 404; }
        location /.well-known { allow all; root $T/site; }
        if ( \$uri ~ "^/\.well-known/.*\.(php|jsp|py|js|css|lua|ts|go|zip|tar\.gz|rar|7z|sql|bak)$" ) { return 403; }
        access_log $T/logs/site.log fm;
    }
    # --- second Reverse Proxy project: the user-content origin (Step 23e) ---
    server {
        listen 127.0.0.1:$NPORT;
        server_name $CONTENT_HOST;
        index index.php index.html index.htm default.php default.htm default.html;
        root $T/site;
        #PROXY-CONF-START
        location ^~ / {
            proxy_pass http://127.0.0.1:$APORT;
            proxy_set_header Host \$http_host;
            proxy_set_header X-Real-IP \$remote_addr;
            proxy_set_header X-Real-Port \$remote_port;
            proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
            proxy_set_header REMOTE-HOST \$remote_addr;
            proxy_connect_timeout 10s;
            proxy_send_timeout 3600s;
            proxy_read_timeout 3600s;
            proxy_http_version 1.1;
            proxy_set_header Upgrade \$http_upgrade;
            proxy_set_header Connection "upgrade";
$CONTENT_A
        }
        #PROXY-CONF-END
        #SERVER-BLOCK START
$CONTENT_B
        #SERVER-BLOCK END
        location ~ ^/(\.user.ini|\.htaccess|\.git|\.env|\.svn|\.project|LICENSE|README.md) { return 404; }
        location /.well-known { allow all; root $T/site; }
        access_log $T/logs/content.log fm;
    }
}
EOF
mkdir -p "$T/tmp"

out=$("$NGINX" -t -p "$T/" -c "$T/nginx.conf" -e "$T/logs/error.log" 2>&1) && rc=0 || rc=$?
check "nginx -t accepts the snippet inside the aaPanel template" test "$rc" = 0
((rc == 0)) || { echo "$out"; finish; }

(
	cd "$T/app"
	APP_SECRET=$(openssl rand -hex 32)
	ADMIN_INITIAL_PASSWORD=$(openssl rand -hex 12)
	export APP_ENV=production APP_BASE_URL=https://file.kppbwi.app LISTEN_ADDR=127.0.0.1:$APORT \
		STORAGE_ROOT=$T/storage DATA_DIR=$T/storage/.system TRUSTED_PROXIES=127.0.0.1 ALLOW_ROOT=true \
		CONTENT_BASE_URL=https://$CONTENT_HOST \
		APP_SECRET ADMIN_INITIAL_PASSWORD
	exec "$BIN" serve
) >"$T/logs/app.log" 2>&1 &
APP_PID=$!
"$NGINX" -p "$T/" -c "$T/nginx.conf" -e "$T/logs/error.log" -g 'daemon off;' &
NGINX_PID=$!
for _ in $(seq 1 50); do
	[[ $(http_code "http://127.0.0.1:$APORT/readyz") == 200 && $(http_code "http://127.0.0.1:$NPORT/healthz") == 200 ]] && break
	sleep 0.2
done
U=http://127.0.0.1:$NPORT

check "/healthz proxied → 200" test "$(http_code "$U/healthz")" = 200
check "/healthz body from the app" grep -q '"status":"ok"' <<<"$(curl -s "$U/healthz")"
from_nginx_404() { # the 404 page is Nginx's own, not the app's
	local body code
	code=$(curl -s -o "$T/body" -w '%{http_code}' --path-as-is "$U$1")
	body=$(cat "$T/body")
	[[ $code == 404 && $body == *"<center>nginx"* ]]
}
for p in /readyz /metrics /.env /.git/config /.git /bin/filemanager /bin /BIN/filemanager /backend/go.mod \
	/web/package.json /deploy/install.sh /docs/SECURITY.md /SPEC.md /PROGRESS.md /Makefile /go.mod \
	/proxy_cache_dir/x /%2e%65nv /assets/../.env /x/.htaccess /.user.ini; do
	check "$p → 404 from Nginx" from_nginx_404 "$p"
done
check "ACME challenge served from the document root" test "$(curl -s "$U/.well-known/acme-challenge/test")" = acme-token-123
check "SPA route still proxied (/files/a)" test "$(http_code "$U/files/a")" = 200
check "prefix lookalike not blocked (/binder)" test "$(http_code "$U/binder")" = 200
check "prefix lookalike not blocked (/documents)" test "$(http_code "$U/documents")" = 200

token=Tok3nShouldNotBeLogged
check "/s/<token> proxied" test "$(http_code "$U/s/$token")" = 200
check "/api/v1/public/... proxied" test "$(http_code "$U/api/v1/public/shares/$token")" != 502
sleep 0.3
check "share token absent from the Nginx access log" bash -c "! grep -q '$token' '$T/logs/site.log'"
check "other requests still logged" grep -q 'GET /healthz' "$T/logs/site.log"

# Cloudflare real IP: CF-Connecting-IP is honoured only when the peer is a
# Cloudflare range; from loopback (a direct hit on the origin) it is ignored.
spoof=203.0.113.9
curl -s -o /dev/null -H "CF-Connecting-IP: $spoof" "$U/healthz"
sleep 0.3
check "real_ip_header CF-Connecting-IP present in PART B" grep -q '^real_ip_header CF-Connecting-IP;' <<<"$PART_B"
check "CF-Connecting-IP from a non-Cloudflare peer is ignored" bash -c "! grep -q '^$spoof ' '$T/logs/site.log'"
live=$({ curl -s --max-time 5 https://www.cloudflare.com/ips-v4; echo; curl -s --max-time 5 https://www.cloudflare.com/ips-v6; echo; } 2>/dev/null | sed '/^$/d' | sort || true)
if [[ -n $live ]]; then
	ours=$(grep -oE '^set_real_ip_from [^;]+' <<<"$PART_B" | awk '{print $2}' | sort)
	[[ $live == "$ours" ]] || echo "WARN: Cloudflare ranges in PART B differ from www.cloudflare.com/ips-v4|v6 — refresh them"
fi

# Upload body size: 60 MiB passes Nginx (the app answers, without a session
# not 2xx), 70 MiB is refused by Nginx with 413 (client_max_body_size 64m).
code60=$(head -c 62914560 /dev/zero | curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/offset+octet-stream' --data-binary @- "$U/api/v1/upload/tus/x" || true)
code70=$(head -c 73400320 /dev/zero | curl -s -o /dev/null -w '%{http_code}' -X POST -H 'Content-Type: application/offset+octet-stream' --data-binary @- "$U/api/v1/upload/tus/x" || true)
check "60 MiB body not refused by Nginx (app answered $code60)" bash -c "[[ '$code60' != 413 && '$code60' != 000 ]]"
check "70 MiB body → 413 from Nginx" test "$code70" = 413

asset=$(curl -s "$U/" | grep -o '/assets/[^"]*\.js' | head -1)
check "asset found in index.html ($asset)" test -n "$asset"
check "Range request through the proxy → 206" test "$(curl -s -o /dev/null -w '%{http_code}' -r 0-99 "$U$asset")" = 206
check "Range length 100" test "$(curl -s -r 0-99 "$U$asset" | wc -c)" = 100
curl -s -o /dev/null "$U$asset"
curl -s -o /dev/null "$U$asset"
check "aaPanel's global proxy_cache stays unused (proxy_cache off)" test -z "$(find "$T/cache" -type f -print -quit)"
# --- the user-content origin (Step 23e, deploy/nginx-usercontent.conf) ---
C() { curl -s -o "$T/cbody" -w '%{http_code}' --max-time 5 --path-as-is -H "Host: $CONTENT_HOST" "$@"; }
content_nginx_404() { [[ $(C "$U$1") == 404 && $(cat "$T/cbody") == *"<center>nginx"* ]]; }
for p in / /login /files /s/token123 /healthz /readyz /metrics /.env /api/v1/auth/me /api/v1/version /api/v1/files/list \
	/api/v1/public/shares/tok /api/v1/public/shares/tok/list /api/v1/files/hls /api/v1/files/hls/x /api/v1/files/raw/x \
	/api/v1/files/rawx /api/v1/filesraw /assets/index.js; do
	check "content host: $p → 404 from Nginx" content_nginx_404 "$p"
done
app_json() { # proxied: the app answers with its JSON envelope (never Nginx's HTML page)
	local code
	code=$(C "$U$1")
	[[ $code == "$2" && $(cat "$T/cbody") == *'"error"'* ]]
}
check "content host: /api/v1/files/raw proxied (app 403 unsigned)" app_json "/api/v1/files/raw?path=%2Fa&purpose=media" 403
check "content host: /api/v1/files/download proxied" app_json "/api/v1/files/download?path=%2Fa" 403
check "content host: /api/v1/files/zip proxied" app_json "/api/v1/files/zip?token=x" 403
check "content host: /api/v1/files/thumb proxied" app_json "/api/v1/files/thumb?path=%2Fa&size=128" 403
check "content host: /api/v1/archives/entry-content proxied" app_json "/api/v1/archives/entry-content?path=%2Fa&inner=b" 403
check "content host: HLS playlist proxied" app_json "/api/v1/files/hls/0123456789abcdef/playlist.m3u8" 403
check "content host: HLS segment proxied" app_json "/api/v1/files/hls/0123456789abcdef/seg_00000.ts" 403
check "content host: public raw proxied (app 404 unknown token, JSON)" app_json "/api/v1/public/shares/tok/raw?path=%2F" 404
check "content host: public thumb proxied" app_json "/api/v1/public/shares/tok/thumb?path=%2F" 404
check "content host: public download proxied" app_json "/api/v1/public/shares/tok/download?path=%2F" 404
check "content host: public zip proxied" app_json "/api/v1/public/shares/tok/zip?id=x" 404
content_method_404() { [[ $(C -X "$1" "$U$2") == 404 && $(cat "$T/cbody") == *"<center>nginx"* ]]; }
check "content host: POST on a content route → 404 from Nginx" content_method_404 POST /api/v1/files/raw
check "content host: PUT → 404 from Nginx" content_method_404 PUT /api/v1/files/zip
check "content host: DELETE → 404 from Nginx" content_method_404 DELETE /api/v1/files/raw
pre=$(curl -s -D - -o /dev/null --max-time 5 -X OPTIONS -H "Host: $CONTENT_HOST" -H "Origin: https://file.kppbwi.app" -H "Access-Control-Request-Method: GET" -H "Access-Control-Request-Headers: range" "$U/api/v1/files/raw?path=%2Fa")
check "content host: OPTIONS preflight → 204" grep -q '^HTTP/1.1 204' <<<"$pre"
check "content host: Access-Control-Allow-Origin = app origin" grep -qi '^access-control-allow-origin: https://file.kppbwi.app' <<<"$pre"
check "content host: Access-Control-Allow-Headers: Range" grep -qi '^access-control-allow-headers: Range' <<<"$pre"
check "content host: no Set-Cookie" bash -c "! grep -qi '^set-cookie' <<<'$pre'"
content_acme() { [[ $(C "$U/.well-known/acme-challenge/test") == 200 && $(cat "$T/cbody") == acme-token-123 ]]; }
check "content host: ACME challenge still served from the document root" content_acme
check "app host: content route → 302 to the content origin" test "$(curl -s -o /dev/null -w '%{http_code} %{redirect_url}' --max-time 5 "$U/api/v1/files/raw?path=%2Fa&purpose=media")" = "302 https://$CONTENT_HOST/api/v1/files/raw?path=%2Fa&purpose=media"
check "app host: SPA still served (no redirect)" test "$(http_code "$U/files/a")" = 200
sleep 0.3
# proxied requests (the ones that carry sig=) are not logged; the server-level
# 404s above are (no signature in them)
check "content host: proxied content requests absent from the Nginx access log (access_log off)" bash -c "! grep -q 'GET /api/v1/files/raw?' '$T/logs/content.log' 2>/dev/null"
check "content host: the Nginx 404s are still logged" grep -q 'GET /api/v1/auth/me' "$T/logs/content.log"
check "no Nginx errors logged" bash -c "! grep -E '\[(emerg|alert|crit)\]' '$T/logs/error.log'"

finish
