#!/usr/bin/env bash
# deploy/update.sh — update with database backup, migrations and automatic
# rollback when /readyz does not come back (SPEC §12.5).
#
#   sudo bash deploy/update.sh [--dry-run] [--yes] [--ready-timeout S]
#        [--binary PATH | --release FILE.tar.gz [--sha256 HEX] | --ref GIT_REF]
#
# Sources of the new binary: --binary (built elsewhere, e.g. CI), --release
# (CI tarball with bin/filemanager, checked against FILE.tar.gz.sha256 or
# --sha256), --ref (git fetch + checkout, then make build), or by default a
# build of the current checkout. Steps: backup-db --label pre-update → stop →
# migrate up (new binary) → swap binary (old one kept as bin/filemanager.prev)
# → start → /readyz within the timeout. On failure: roll the new migrations
# back with the new binary (or restore the pre-update backup when that fails),
# put the previous binary back, start, exit 1. Log: DATA_DIR/logs/update-<ts>.log.
#
# Overrides (tests): FM_APP_DIR, FM_SVC_USER [filemanager], FM_ALLOW_NONROOT=1,
# FM_READY_TIMEOUT [30].

# Everything lives in main(): bash has then parsed the whole file before a
# "git checkout" can rewrite it underneath the running process.
main() {
	set -euo pipefail
	umask 022
	SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
	# shellcheck source=deploy/lib.sh
	. "$SCRIPT_DIR/lib.sh"

	APP_DIR=${FM_APP_DIR:-$(cd -- "$SCRIPT_DIR/.." && pwd)}
	SVC_USER=${FM_SVC_USER:-filemanager}
	SERVICE=filemanager
	READY_TIMEOUT=${FM_READY_TIMEOUT:-30}
	ENV_FILE=$APP_DIR/.env
	BIN=$APP_DIR/bin/filemanager
	NEW=$APP_DIR/bin/filemanager.new
	PREV=$APP_DIR/bin/filemanager.prev
	SRC_BINARY="" RELEASE="" RELEASE_SHA="" REF=""

	while (($#)); do
		case $1 in
		--dry-run) FM_DRY_RUN=1 ;;
		-y | --yes) FM_YES=1 ;;
		--ready-timeout) READY_TIMEOUT=${2:?}; shift ;;
		--binary) SRC_BINARY=$(realpath -- "${2:?--binary needs a path}"); shift ;;
		--release) RELEASE=$(realpath -- "${2:?--release needs a file}"); shift ;;
		--sha256) RELEASE_SHA=${2:?}; shift ;;
		--ref) REF=${2:?}; shift ;;
		-h | --help) sed -n '2,19p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; return 0 ;;
		*) die "unknown option: $1 (see --help)" ;;
		esac
		shift
	done
	[[ -z $SRC_BINARY || -f $SRC_BINARY ]] || die "binary not found: $SRC_BINARY"
	[[ -z $RELEASE || -f $RELEASE ]] || die "release not found: $RELEASE"
	if [[ $(id -u) != 0 && ${FM_ALLOW_NONROOT:-0} != 1 ]] && ! dry; then
		die "run as root: sudo bash deploy/update.sh"
	fi
	[[ -x $BIN ]] || die "$BIN missing — run deploy/install.sh first"
	[[ -f $ENV_FILE ]] || die "$ENV_FILE missing — run deploy/install.sh first"
	cd "$APP_DIR" # like WorkingDirectory= of the unit (config checks STORAGE_ROOT against it)

	DATA_DIR=$(env_get "$ENV_FILE" DATA_DIR)
	[[ -n $DATA_DIR ]] || DATA_DIR=$(env_get "$ENV_FILE" STORAGE_ROOT)/.system
	DB=$DATA_DIR/db/app.db
	URL=$(listen_url "$(env_get "$ENV_FILE" LISTEN_ADDR)")
	TS=$(date +%Y%m%d-%H%M%S)

	if dry; then
		plan_only
		return 0
	fi

	exec 9>"$APP_DIR/bin/.update.lock"
	flock -n 9 || die "another update is running"
	LOG=$DATA_DIR/logs/update-$TS.log
	mkdir -p "$DATA_DIR/logs"
	exec > >(tee -a "$LOG") 2>&1
	TEE_PID=$!
	trap 'exec 1>&- 2>&-; wait "$TEE_PID" 2>/dev/null || true' EXIT
	say "update started $(date -Is) — log $LOG"
	say "current: $("$BIN" version)"
	if ! curl -fsS -o /dev/null --max-time 3 "$URL/readyz"; then
		warn "$URL/readyz does not answer 200 before the update"
		confirm "Continue anyway?" n || die "aborted (service not ready before the update)"
	fi

	fetch_new_binary
	say "new: $("$NEW" version)"

	BACKUP=$(backup_db)
	say "database backup: $BACKUP"
	install -m 0755 "$BIN" "$PREV"

	local t0=$SECONDS
	V_BEFORE=$(schema_version "$NEW")
	say "stop $SERVICE"
	systemctl stop "$SERVICE"
	if ! as_user "$SVC_USER" "$NEW" migrate up --env-file "$ENV_FILE"; then
		warn "migrate up failed"
		rollback "$t0"
		return 1
	fi
	V_AFTER=$(schema_version "$NEW")
	say "schema version $V_BEFORE → $V_AFTER"
	mv -f "$NEW" "$BIN"
	say "start $SERVICE"
	systemctl start "$SERVICE" 9>&- # the lock must not leak into the service
	if wait_ready "$URL" "$READY_TIMEOUT" "$SERVICE"; then
		say "UPDATE OK in $((SECONDS - t0))s: $("$BIN" version) (previous kept as bin/filemanager.prev)"
		return 0
	fi
	warn "$URL/readyz not 200 within ${READY_TIMEOUT}s"
	journalctl -u "$SERVICE" -n 50 --no-pager 2>/dev/null || true
	install -m 0755 "$BIN" "$NEW" # the failed binary carries the down migrations
	rollback "$t0"
	return 1
}

plan_only() {
	say "DRY RUN — nothing will be changed"
	say "current: $("$BIN" version) · data $DATA_DIR · ready check $URL/readyz"
	if [[ -n $SRC_BINARY ]]; then
		printf '[dry-run] new binary: %s (%s)\n' "$SRC_BINARY" "$("$SRC_BINARY" version 2>/dev/null || echo 'does not run')"
	elif [[ -n $RELEASE ]]; then
		printf '[dry-run] new binary: bin/filemanager from %s (sha256 checked)\n' "$RELEASE"
	elif [[ -n $REF ]]; then
		printf '[dry-run] git fetch + checkout %s as %s, then make build → bin/filemanager.new\n' "$REF" "$(repo_owner "$APP_DIR")"
	else
		printf '[dry-run] make build of the current checkout (%s) → bin/filemanager.new\n' "$(git_ro rev-parse --short HEAD 2>/dev/null || echo '?')"
	fi
	printf '[dry-run] %s backup-db --label pre-update (as %s) → %s/backups/app-%s-pre-update.db\n' "$BIN" "$SVC_USER" "$DATA_DIR" "$TS"
	printf '[dry-run] copy bin/filemanager → bin/filemanager.prev; systemctl stop %s; new binary migrate up\n' "$SERVICE"
	printf '[dry-run] move the new binary into place; systemctl start %s; wait for /readyz <= %ss\n' "$SERVICE" "$READY_TIMEOUT"
	printf '[dry-run] on failure: migrate down to the old schema (or restore the backup), bin/filemanager.prev back, start; exit 1\n'
}

fetch_new_binary() {
	local owner tmp
	rm -f "$NEW"
	if [[ -n $SRC_BINARY ]]; then
		install -m 0755 "$SRC_BINARY" "$NEW"
	elif [[ -n $RELEASE ]]; then
		if [[ -z $RELEASE_SHA && -f $RELEASE.sha256 ]]; then RELEASE_SHA=$(awk '{print $1; exit}' "$RELEASE.sha256"); fi
		[[ $RELEASE_SHA =~ ^[0-9a-fA-F]{64}$ ]] || die "release checksum missing: pass --sha256 or put $RELEASE.sha256 next to it"
		echo "$RELEASE_SHA  $RELEASE" | sha256sum -c --quiet - || die "release checksum mismatch"
		tmp=$(mktemp -d "$APP_DIR/bin/.release-XXXXXX")
		tar -xzf "$RELEASE" --no-same-owner -C "$tmp" bin/filemanager || { rm -rf "$tmp"; die "bin/filemanager not in $RELEASE"; }
		install -m 0755 "$tmp/bin/filemanager" "$NEW"
		rm -rf "$tmp"
	else
		owner=$(repo_owner "$APP_DIR")
		if [[ -n $REF ]]; then
			[[ -z $(git_ro status --porcelain --untracked-files=no) ]] || die "tracked files are modified in $APP_DIR; commit or stash first"
			if [[ -n $(git_ro remote) ]]; then
				as_user "$owner" git -C "$APP_DIR" fetch --tags --prune --quiet
			fi
			as_user "$owner" git -C "$APP_DIR" checkout --quiet "$REF"
			if git_ro rev-parse --abbrev-ref --symbolic-full-name '@{u}' >/dev/null 2>&1; then
				as_user "$owner" git -C "$APP_DIR" merge --ff-only --quiet '@{u}'
			fi
		fi
		say "build $(git_ro rev-parse --short HEAD) as $owner"
		as_user "$owner" env "PATH=/usr/local/go/bin:$PATH" make -C "$APP_DIR" build BIN=bin/filemanager.new
	fi
	"$NEW" version >/dev/null || die "the new binary does not run"
}

# backup_db — VACUUM INTO via the running version (Step 19b); prints the path.
backup_db() {
	local out name
	out=$(as_user "$SVC_USER" "$BIN" backup-db --label pre-update --env-file "$ENV_FILE") || die "backup-db failed: $out"
	name=$(sed -n 's/^backup written: \([^ ]*\).*/\1/p' <<<"$out")
	[[ -n $name && -f $DATA_DIR/backups/$name ]] || die "backup-db did not report a file: $out"
	printf '%s' "$DATA_DIR/backups/$name"
}

# schema_version <binary> — highest applied goose version known to <binary>.
schema_version() {
	as_user "$SVC_USER" "$1" migrate status --env-file "$ENV_FILE" | awk '$2 == "applied" {v = $1} END {print v + 0}'
}

restore_db() {
	say "restore the database from $BACKUP"
	local f
	for f in "$DB" "$DB-wal" "$DB-shm"; do
		[[ -e $f ]] && mv -f "$f" "$f.failed-$TS"
	done
	install -m 0640 -o "$SVC_USER" -g "$(id -gn "$SVC_USER")" "$BACKUP" "$DB"
}

rollback() {
	local t0=$1 v ok=1
	say "ROLLBACK"
	systemctl stop "$SERVICE" || true
	v=$(schema_version "$NEW" 2>/dev/null || echo "")
	if [[ -n $v && $v -gt $V_BEFORE ]]; then
		while ((v > V_BEFORE)); do
			if ! as_user "$SVC_USER" "$NEW" migrate down --env-file "$ENV_FILE"; then
				ok=0
				break
			fi
			v=$(schema_version "$NEW")
		done
		((ok)) || restore_db
	elif [[ -z $v ]]; then
		restore_db
	fi
	mv -f "$NEW" "$APP_DIR/bin/filemanager.failed"
	install -m 0755 "$PREV" "$BIN"
	systemctl start "$SERVICE" 9>&- # the lock must not leak into the service
	if wait_ready "$URL" "$READY_TIMEOUT" "$SERVICE"; then
		say "ROLLBACK OK in $((SECONDS - t0))s: running $("$BIN" version); failed binary kept as bin/filemanager.failed"
	else
		journalctl -u "$SERVICE" -n 50 --no-pager 2>/dev/null || true
		warn "ROLLBACK FAILED: $URL/readyz still not 200 — see docs/DEPLOY-AAPANEL.md (Troubleshooting)"
		exit 2
	fi
}

main "$@"
exit $?
