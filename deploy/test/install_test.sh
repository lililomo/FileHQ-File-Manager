#!/usr/bin/env bash
# deploy/test/install_test.sh — install.sh in scratch folders (SPEC §12.1):
# --dry-run changes nothing, two runs in a row succeed and the second changes
# nothing, an existing .env with inline comments is cleaned, an unsupported OS
# is refused. Needs bin/filemanager (make build).
set -euo pipefail
# shellcheck source=deploy/test/lib_test.sh
. "$(dirname -- "${BASH_SOURCE[0]}")/lib_test.sh"

BIN=${FM_TEST_BIN:-$REPO/bin/filemanager}
[[ -x $BIN ]] || { echo "FAIL: $BIN missing — run make build first"; exit 1; }

ROOT=$(new_root install)
cleanup() {
	FM_SHIM_STATE=$ROOT/shim FM_APP_DIR=$ROOT/app "$REPO/deploy/test/shim/systemctl" stop >/dev/null 2>&1 || true
	rm -rf "$ROOT"
	rmdir "$REPO/.deploytest" 2>/dev/null || true
}
trap cleanup EXIT

APP=$ROOT/app
mkdir -p "$APP"
cp "$REPO/.env.example" "$APP/.env.example"
fake_os_release "$ROOT/os-release" ubuntu 24.04
PORT=$(free_port)
export FM_APP_DIR=$APP FM_STORAGE=$ROOT/storage FM_SYSTEMD_DIR=$ROOT/systemd FM_SVC_USER
FM_SVC_USER=$(id -un)
export FM_SKIP_USER=1 FM_SKIP_APT=1 FM_ALLOW_NONROOT=1 FM_LISTEN_ADDR=127.0.0.1:$PORT
export FM_OS_RELEASE=$ROOT/os-release FM_SHIM_STATE=$ROOT/shim FM_READY_TIMEOUT=30
# The binary refuses uid 0 in production; only matters when the test runs as root.
export ALLOW_ROOT=true
export PATH=$REPO/deploy/test/shim:$PATH
ARGS=(--yes --binary "$BIN" --skip-aapanel --no-swap)
UNIT=$ROOT/systemd/filemanager.service

snapshot() { (cd "$ROOT" && find . -path ./shim -prune -o -printf '%p %m %s\n' | sort); }

# 1. dry-run on an empty machine: nothing is created.
before=$(snapshot)
out=$("$REPO/deploy/install.sh" --dry-run "${ARGS[@]}" 2>&1) && rc=0 || rc=$?
check "dry-run exits 0" test "$rc" = 0
check "dry-run creates nothing" test "$before" = "$(snapshot)"
check "dry-run prints the plan" grep -q '\[dry-run\] create system user\|\[dry-run\] write .*\.env\|\[dry-run\] install .*filemanager.service' <<<"$out"

# 2. first install.
"$REPO/deploy/install.sh" "${ARGS[@]}" >"$ROOT/run1.log" 2>&1 && rc=0 || rc=$?
check "first run exits 0" test "$rc" = 0 || true; ((rc == 0)) || tail -30 "$ROOT/run1.log"
check ".env mode 0600" test "$(stat -c %a "$APP/.env")" = 600
check "APP_SECRET is 64 hex" grep -qE '^APP_SECRET=[0-9a-f]{64}$' "$APP/.env"
check "ADMIN_INITIAL_PASSWORD is 24 random characters" grep -qE '^ADMIN_INITIAL_PASSWORD=[A-Za-z0-9]{24}$' "$APP/.env"
check ".env has no inline comments" bash -c "! grep -qE '^[A-Za-z_][A-Za-z0-9_]*=[^#]*[[:space:]]#' '$APP/.env'"
check ".env points at the scratch storage" grep -qx "STORAGE_ROOT=$ROOT/storage" "$APP/.env"
pw=$(sed -n 's/^ADMIN_INITIAL_PASSWORD=//p' "$APP/.env")
check "admin password not in the installer output" bash -c "! grep -qF '$pw' '$ROOT/run1.log'"
check "storage mode 0750" test "$(stat -c %a "$ROOT/storage")" = 750
for d in db trash versions uploads thumbs tmp logs backups deleted-users; do
	[[ -d $ROOT/storage/.system/$d && $(stat -c %a "$ROOT/storage/.system/$d") == 750 ]] || fail ".system/$d 0750"
done
pass ".system subfolders checked"
check "unit installed" test -f "$UNIT"
check "unit paths rewritten" grep -qx "ReadWritePaths=$ROOT/storage" "$UNIT"
check "unit WorkingDirectory" grep -qx "WorkingDirectory=$APP" "$UNIT"
check "unit has no default storage path left" bash -c "! grep -q '/www/storage/FileManager' '$UNIT'"
check "unit EnvironmentFile" grep -qx "EnvironmentFile=$APP/.env" "$UNIT"
check "unit ExecStart" grep -qx "ExecStart=$APP/bin/filemanager serve" "$UNIT"
check "unit hardening kept" grep -qx 'ProtectSystem=strict' "$UNIT"
check "check-deps table printed" grep -q 'ffmpeg' "$ROOT/run1.log"
check "/readyz 200" test "$(http_code "http://127.0.0.1:$PORT/readyz")" = 200
check "/healthz 200" test "$(http_code "http://127.0.0.1:$PORT/healthz")" = 200
check "binary installed 0755" test "$(stat -c %a "$APP/bin/filemanager")" = 755

# 3. second run: idempotent, nothing restarted.
env_sum=$(sha256sum <"$APP/.env")
unit_sum=$(sha256sum <"$UNIT")
"$REPO/deploy/install.sh" "${ARGS[@]}" >"$ROOT/run2.log" 2>&1 && rc=0 || rc=$?
check "second run exits 0" test "$rc" = 0 || true; ((rc == 0)) || tail -30 "$ROOT/run2.log"
check "second run keeps .env" test "$env_sum" = "$(sha256sum <"$APP/.env")"
check "second run keeps the unit" test "$unit_sum" = "$(sha256sum <"$UNIT")"
check "second run does not restart the service" test "$(wc -l <"$ROOT/shim/starts.log")" = 1
check "second run reports no change" grep -q 'nothing changed' "$ROOT/run2.log"
check "/readyz still 200" test "$(http_code "http://127.0.0.1:$PORT/readyz")" = 200

# 4. an existing .env with comments after values (state of the server's .env
# before Step 22) is cleaned, backed up, and keeps its secret.
secret=$(sed -n 's/^APP_SECRET=//p' "$APP/.env")
sed -e "s/^APP_SECRET=CHANGE_ME/APP_SECRET=$secret/" -e "s|^STORAGE_ROOT=.*|STORAGE_ROOT=$ROOT/storage   # comment|" \
	-e "s|^DATA_DIR=.*|DATA_DIR=$ROOT/storage/.system   # comment|" -e "s|^LISTEN_ADDR=.*|LISTEN_ADDR=127.0.0.1:$PORT   # loopback|" \
	-e "s/^ADMIN_INITIAL_PASSWORD=CHANGE_ME/ADMIN_INITIAL_PASSWORD=$pw/" "$REPO/.env.example" >"$APP/.env"
chmod 0644 "$APP/.env"
"$REPO/deploy/install.sh" "${ARGS[@]}" >"$ROOT/run3.log" 2>&1 && rc=0 || rc=$?
check "run with a commented .env exits 0" test "$rc" = 0 || true; ((rc == 0)) || tail -30 "$ROOT/run3.log"
check "inline comments removed" bash -c "! grep -qE '^[A-Za-z_][A-Za-z0-9_]*=[^#]*[[:space:]]#' '$APP/.env'"
check "secret kept" grep -qx "APP_SECRET=$secret" "$APP/.env"
check "mode fixed to 0600" test "$(stat -c %a "$APP/.env")" = 600
check "original .env backed up" compgen -G "$APP/.env.bak-*" >/dev/null
check "service restarted after the .env change" test "$(wc -l <"$ROOT/shim/starts.log")" = 2
check "/readyz 200 after restart" test "$(http_code "http://127.0.0.1:$PORT/readyz")" = 200

# 5. unsupported OS.
fake_os_release "$ROOT/os-debian" debian 12
out=$(FM_OS_RELEASE=$ROOT/os-debian "$REPO/deploy/install.sh" "${ARGS[@]}" 2>&1) && rc=0 || rc=$?
check "Debian is refused" test "$rc" != 0
check "refusal names the OS" grep -q 'unsupported OS' <<<"$out"

# 6. a DATA_DIR that would make chown -R/chmod act on / or a top-level
# directory is refused before anything changes.
cp "$APP/.env" "$ROOT/env.good"
sed -i 's|^DATA_DIR=.*|DATA_DIR=/|' "$APP/.env"
out=$("$REPO/deploy/install.sh" "${ARGS[@]}" 2>&1) && rc=0 || rc=$?
check "DATA_DIR=/ is refused" test "$rc" != 0
check "refusal names DATA_DIR" grep -q 'DATA_DIR must be' <<<"$out"
cp "$ROOT/env.good" "$APP/.env"

finish
