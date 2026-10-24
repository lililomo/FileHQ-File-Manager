# shellcheck shell=bash
# Helpers for the deploy tests (sourced). Tests run in $REPO/.deploytest (git
# ignored, removed afterwards) with the systemctl/journalctl shims from
# deploy/test/shim; nothing outside that folder is touched.
REPO=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
FAILS=0

pass() { printf 'ok   %s\n' "$*"; }
fail() { printf 'FAIL %s\n' "$*"; FAILS=$((FAILS + 1)); }
check() { # check <description> <command...>
	local d=$1
	shift
	if "$@"; then pass "$d"; else fail "$d"; fi
}

free_port() { python3 -I -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1]); s.close()'; }

http_code() { curl -s -o /dev/null -w '%{http_code}' --max-time 3 "$1" || true; }

new_root() {
	mkdir -p "$REPO/.deploytest"
	mktemp -d "$REPO/.deploytest/$1.XXXXXX"
}

fake_os_release() { # fake_os_release <file> <id> <version>
	printf 'ID=%s\nVERSION_ID="%s"\nPRETTY_NAME="test"\n' "$2" "$3" >"$1"
}

finish() {
	if ((FAILS)); then
		printf '%d check(s) failed\n' "$FAILS"
		exit 1
	fi
	echo "all checks passed"
}
