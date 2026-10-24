#!/usr/bin/env bash
# deploy/test/update_rollback_test.sh — update.sh against a scratch install
# (install.sh + systemctl shim): a good binary updates; a binary that never
# answers /readyz is rolled back to the previous binary within 60 s (PRD
# E14-US4); a new migration is rolled back with "migrate down"; when the down
# migration fails the pre-update backup is restored. Needs bin/filemanager.
set -euo pipefail
# shellcheck source=deploy/test/lib_test.sh
. "$(dirname -- "${BASH_SOURCE[0]}")/lib_test.sh"

REAL=${FM_TEST_BIN:-$REPO/bin/filemanager}
[[ -x $REAL ]] || { echo "FAIL: $REAL missing — run make build first"; exit 1; }

ROOT=$(new_root update)
cleanup() {
	FM_SHIM_STATE=$ROOT/shim FM_APP_DIR=$ROOT/app "$REPO/deploy/test/shim/systemctl" stop >/dev/null 2>&1 || true
	[[ ${FM_TEST_KEEP:-0} == 1 ]] && { echo "kept $ROOT"; return; }
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
export FM_OS_RELEASE=$ROOT/os-release FM_SHIM_STATE=$ROOT/shim ALLOW_ROOT=true
export PATH=$REPO/deploy/test/shim:$PATH
READY=http://127.0.0.1:$PORT/readyz
BACKUPS=$ROOT/storage/.system/backups
DB=$ROOT/storage/.system/db/app.db

"$REPO/deploy/install.sh" --yes --binary "$REAL" --skip-aapanel --no-swap >"$ROOT/install.log" 2>&1 ||
	{ tail -30 "$ROOT/install.log"; echo "FAIL: install.sh"; exit 1; }
check "installed service ready" test "$(http_code "$READY")" = 200

# fake_binary <path> <mode>: plain = never ready; migration = also applies a
# fake migration 9999; brokendown = like migration but its down step fails.
fake_binary() {
	cat >"$1" <<EOT
#!/usr/bin/env bash
REAL='$REAL' STATE='$ROOT' MODE='$2'
EOT
	cat >>"$1" <<'EOT'
case $1 in
version) echo "filemanager v9.9.9-broken (commit broken, built test)" ;;
serve) exec sleep 600 ;; # runs, never answers /readyz
migrate)
	case $2 in
	up)
		"$REAL" "$@" || exit
		if [[ $MODE != plain ]]; then touch "$STATE/m9999"; echo "applied 1 migration(s): [9999]"; fi
		;;
	status)
		"$REAL" "$@" || exit
		if [[ -f $STATE/m9999 ]]; then echo "9999     applied  2026-10-25T00:00:00Z     9999_fake.sql"; fi
		;;
	down)
		if [[ -f $STATE/m9999 ]]; then
			if [[ $MODE == brokendown ]]; then echo "error: down 9999 failed" >&2; exit 1; fi
			rm -f "$STATE/m9999"; echo "rolled back migration 9999"
		else
			exec "$REAL" "$@"
		fi
		;;
	*) exec "$REAL" "$@" ;;
	esac
	;;
*) exec "$REAL" "$@" ;;
esac
EOT
	chmod 0755 "$1"
}

# run_update <name> <args...> — runs update.sh, sets RC and ELAPSED.
run_update() {
	local name=$1 t0
	shift
	sleep 1 # backups are named per second
	t0=$(date +%s%N)
	"$REPO/deploy/update.sh" "$@" >"$ROOT/$name.log" 2>&1 && RC=0 || RC=$?
	ELAPSED_MS=$((($(date +%s%N) - t0) / 1000000))
	ELAPSED=$((ELAPSED_MS / 1000)).$((ELAPSED_MS % 1000 / 100))
	printf '     %s: exit %s after %ss\n' "$name" "$RC" "$ELAPSED"
}

after_rollback() { # after_rollback <name>
	check "$1: exit 1" test "$RC" = 1
	check "$1: previous binary back in place" cmp -s "$REAL" "$APP/bin/filemanager"
	check "$1: failed binary kept" test -x "$APP/bin/filemanager.failed"
	check "$1: /readyz 200 after rollback" test "$(http_code "$READY")" = 200
	check "$1: output says ROLLBACK OK" grep -q 'ROLLBACK OK' "$ROOT/$1.log"
	check "$1: update log in DATA_DIR/logs" grep -lq 'ROLLBACK OK' "$ROOT"/storage/.system/logs/update-*.log
}

# 1. dry-run changes nothing.
sum=$(sha256sum "$APP/bin/filemanager")
run_update dry --dry-run --binary "$REAL"
check "dry-run: exit 0" test "$RC" = 0
check "dry-run: binary untouched" test "$sum" = "$(sha256sum "$APP/bin/filemanager")"
check "dry-run: no backup" bash -c "! compgen -G '$BACKUPS/*pre-update.db' >/dev/null"

# 2. good binary.
cp "$REAL" "$ROOT/good"
run_update good --binary "$ROOT/good"
check "good: exit 0" test "$RC" = 0 || true
((RC == 0)) || tail -30 "$ROOT/good.log"
check "good: UPDATE OK" grep -q 'UPDATE OK' "$ROOT/good.log"
check "good: previous binary kept as .prev" test -x "$APP/bin/filemanager.prev"
check "good: pre-update backup written" compgen -G "$BACKUPS/app-*-pre-update.db" >/dev/null
check "good: /readyz 200" test "$(http_code "$READY")" = 200

# 3. binary that never becomes ready, default 30 s timeout: rollback <= 60 s.
fake_binary "$ROOT/broken" plain
run_update broken --binary "$ROOT/broken"
after_rollback broken
check "broken: rollback finished within 60 s (took ${ELAPSED}s)" test "$ELAPSED_MS" -le 60000

# 4. new migration applied, then rolled back with migrate down.
fake_binary "$ROOT/migr" migration
run_update migration --binary "$ROOT/migr" --ready-timeout 5
after_rollback migration
check "migration: down step ran" grep -q 'rolled back migration 9999' "$ROOT/migration.log"
check "migration: fake migration gone" test ! -e "$ROOT/m9999"

# 5. down migration fails: the pre-update backup replaces the database.
fake_binary "$ROOT/brokendown" brokendown
run_update brokendown --binary "$ROOT/brokendown" --ready-timeout 5
after_rollback brokendown
latest=$(printf '%s\n' "$BACKUPS"/app-*-pre-update.db | sort | tail -1)
check "brokendown: database restored from $(basename "$latest")" grep -q "restore the database from $latest" "$ROOT/brokendown.log"
check "brokendown: failed database kept aside" compgen -G "$DB.failed-*" >/dev/null
check "brokendown: restored file serves /readyz" test "$(http_code "$READY")" = 200

finish
