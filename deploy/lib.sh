# shellcheck shell=bash
# Shared helpers for deploy/install.sh and deploy/update.sh (sourced, never
# executed). Every helper that changes the system goes through act() so that
# --dry-run prints the plan instead of running it.

FM_DRY_RUN=${FM_DRY_RUN:-0}
FM_YES=${FM_YES:-0}

say()  { printf '==> %s\n' "$*"; }
info() { printf '    %s\n' "$*"; }
warn() { printf 'WARN: %s\n' "$*" >&2; }
die()  { printf 'ERROR: %s\n' "$*" >&2; exit 1; }
dry()  { [[ $FM_DRY_RUN == 1 ]]; }

quote_cmd() {
	local out=() a
	for a in "$@"; do out+=("$(printf '%q' "$a")"); done
	printf '%s' "${out[*]}"
}

# act <description> <command> [args...] — run a mutating command, or only
# describe it under --dry-run.
act() {
	local desc=$1
	shift
	if dry; then
		printf '[dry-run] %s: %s\n' "$desc" "$(quote_cmd "$@")"
		return 0
	fi
	say "$desc"
	"$@"
}

# plan <description> — announce a mutation that is implemented inline.
# Returns 1 under --dry-run so callers can write: plan "x" && do_x.
plan() {
	if dry; then
		printf '[dry-run] %s\n' "$1"
		return 1
	fi
	say "$1"
}

# confirm <question> <default y|n> — --yes answers with the default only when
# the default is "y"; risky questions therefore default to "n".
confirm() {
	local q=$1 def=${2:-n} ans
	if [[ $FM_YES == 1 || ! -t 0 ]]; then
		[[ $def == y ]]
		return
	fi
	read -r -p "$q [$([[ $def == y ]] && echo Y/n || echo y/N)] " ans </dev/tty || ans=
	ans=${ans:-$def}
	[[ $ans == [yY]* ]]
}

# version_ge A B — true when version A >= B (sort -V).
version_ge() { printf '%s\n%s\n' "$2" "$1" | sort -V -C; }

# env_get <file> <KEY> — value of the last KEY= line, without an inline
# comment or surrounding quotes. Empty when absent.
env_get() {
	local v
	[[ -r $1 ]] || return 0
	v=$(FM_K=$2 awk 'index($0, ENVIRON["FM_K"] "=") == 1 { v = substr($0, length(ENVIRON["FM_K"]) + 2) } END { print v }' "$1")
	v=$(strip_value "$v")
	printf '%s' "$v"
}

strip_value() {
	local v=$1
	case $v in
	\"*\") v=${v#\"}; v=${v%\"} ;;
	\'*\') v=${v#\'}; v=${v%\'} ;;
	*)
		v=$(printf '%s' "$v" | sed -E 's/[[:space:]]+#.*$//; s/[[:space:]]+$//')
		;;
	esac
	printf '%s' "$v"
}

# env_clean — stdin → stdout: drop inline comments after unquoted values.
# systemd EnvironmentFile= keeps "value   # comment" verbatim (PROGRESS,
# Step 1), so the installed .env must not contain them.
env_clean() {
	awk -v sq="'" '
	match($0, /^[A-Za-z_][A-Za-z0-9_]*=/) {
		key = substr($0, 1, RLENGTH); val = substr($0, RLENGTH + 1); c = substr(val, 1, 1)
		if (c != sq && c != "\"") {
			if (match(val, /[ \t]+#/)) val = substr(val, 1, RSTART - 1)
			sub(/[ \t]+$/, "", val)
		}
		print key val; next
	}
	{ print }'
}

# env_set <KEY> <VALUE> — stdin → stdout: replace the first KEY= line (later
# duplicates are dropped) or append it.
env_set() {
	FM_K=$1 FM_V=$2 awk '
	index($0, ENVIRON["FM_K"] "=") == 1 { if (!done) print ENVIRON["FM_K"] "=" ENVIRON["FM_V"]; done = 1; next }
	{ print }
	END { if (!done) print ENVIRON["FM_K"] "=" ENVIRON["FM_V"] }'
}

# as_user <user> <command...> — run as the service user (runuser when root).
as_user() {
	local u=$1
	shift
	if [[ $u == "$(id -un)" ]]; then
		"$@"
	elif [[ $(id -u) == 0 ]]; then
		runuser -u "$u" -- "$@"
	else
		die "cannot run as $u without root"
	fi
}

# listen_url <LISTEN_ADDR> — http://host:port usable by curl.
listen_url() {
	local a=${1:-127.0.0.1:8080}
	[[ $a == :* ]] && a="127.0.0.1$a"
	[[ $a == 0.0.0.0:* ]] && a="127.0.0.1:${a#0.0.0.0:}"
	printf 'http://%s' "$a"
}

# wait_ready <url> <timeout-seconds> <service> — poll /readyz once per second;
# gives up early when systemd reports the unit as failed.
wait_ready() {
	local url=$1 timeout=$2 svc=$3 start=$SECONDS state
	while ((SECONDS - start < timeout)); do
		if curl -fsS -o /dev/null --max-time 2 "$url/readyz" 2>/dev/null; then
			return 0
		fi
		state=$(systemctl is-active "$svc" 2>/dev/null || true)
		if [[ $state == failed ]]; then
			warn "$svc is in state 'failed'"
			return 1
		fi
		sleep 1
	done
	return 1
}

# repo_owner <dir> — owner of the git checkout (keeps git and builds out of
# the service account; see SPEC §12.1 step 8).
repo_owner() {
	if [[ -e $1/.git ]]; then stat -c %U "$1/.git"; else stat -c %U "$1"; fi
}
