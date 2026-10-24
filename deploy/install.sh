#!/usr/bin/env bash
# deploy/install.sh — idempotent installer for FileManager (SPEC §12.1).
#
#   sudo bash deploy/install.sh [--dry-run] [--yes] [--binary PATH | --skip-build]
#                               [--rebuild] [--with-unrar] [--skip-apt]
#                               [--skip-aapanel] [--no-swap]
#
# Safe to run again: every step checks the current state first. It never
# touches UFW/the aaPanel firewall, Nginx or aaPanel itself; the aaPanel steps
# are printed at the end (docs/DEPLOY-AAPANEL.md).
#
# Overrides for tests and CI (defaults in brackets):
#   FM_APP_DIR [repo of this script]   FM_STORAGE [/www/storage/FileManager]
#   FM_SYSTEMD_DIR [/etc/systemd/system] FM_SVC_USER [filemanager]
#   FM_SKIP_APT=1  FM_SKIP_USER=1 (user must exist)  FM_ALLOW_NONROOT=1
#   FM_LISTEN_ADDR / FM_BASE_URL (only for a newly created .env)
#   FM_READY_TIMEOUT [30]  FM_OS_RELEASE [/etc/os-release]
set -euo pipefail
umask 022

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=deploy/lib.sh
. "$SCRIPT_DIR/lib.sh"

DEFAULT_APP_DIR=/www/wwwroot/file.kppbwi.app
DEFAULT_STORAGE=/www/storage/FileManager
APP_DIR=${FM_APP_DIR:-$(cd -- "$SCRIPT_DIR/.." && pwd)}
STORAGE=${FM_STORAGE:-$DEFAULT_STORAGE}
SYSTEMD_DIR=${FM_SYSTEMD_DIR:-/etc/systemd/system}
SVC_USER=${FM_SVC_USER:-filemanager}
SERVICE=filemanager
READY_TIMEOUT=${FM_READY_TIMEOUT:-30}
OS_RELEASE=${FM_OS_RELEASE:-/etc/os-release}
GO_ROOT=/usr/local/go
SYSTEM_DIRS=(db trash versions uploads thumbs tmp logs backups deleted-users)

BINARY="" SKIP_BUILD=0 REBUILD=0 WITH_UNRAR=0 SKIP_APT=${FM_SKIP_APT:-0} SKIP_AAPANEL=0 NO_SWAP=0
CHANGED_SERVICE=0 NEW_ADMIN_PASSWORD="" SEVENZIP_FOUND="" UNRAR_FOUND=""

usage() { sed -n '2,19p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; }

parse_args() {
	while (($#)); do
		case $1 in
		--dry-run) FM_DRY_RUN=1 ;;
		-y | --yes) FM_YES=1 ;;
		--binary)
			[[ $# -ge 2 ]] || die "--binary needs a path"
			BINARY=$(realpath -- "$2") || die "binary not found: $2"
			shift
			;;
		--skip-build | --no-build) SKIP_BUILD=1 ;;
		--rebuild) REBUILD=1 ;;
		--with-unrar) WITH_UNRAR=1 ;;
		--skip-apt) SKIP_APT=1 ;;
		--skip-aapanel) SKIP_AAPANEL=1 ;;
		--no-swap) NO_SWAP=1 ;;
		-h | --help) usage; exit 0 ;;
		*) die "unknown option: $1 (see --help)" ;;
		esac
		shift
	done
	[[ -z $BINARY || -f $BINARY ]] || die "binary not found: $BINARY"
}

needs_build() { [[ -z $BINARY && $SKIP_BUILD == 0 ]]; }

check_root() {
	if [[ $(id -u) != 0 && ${FM_ALLOW_NONROOT:-0} != 1 ]]; then
		dry && { warn "not root: the dry-run can only report what it can read"; return; }
		die "run as root: sudo bash deploy/install.sh"
	fi
}

check_os() {
	local id="" ver=""
	[[ -r $OS_RELEASE ]] || die "$OS_RELEASE not readable"
	# shellcheck disable=SC1090
	id=$(. "$OS_RELEASE" && printf '%s' "${ID:-}")
	# shellcheck disable=SC1090
	ver=$(. "$OS_RELEASE" && printf '%s' "${VERSION_ID:-}")
	case "$id:$ver" in
	ubuntu:22.04 | ubuntu:24.04) say "OS: Ubuntu $ver" ;;
	*) die "unsupported OS '$id $ver' (Ubuntu 22.04/24.04 only)" ;;
	esac
}

check_paths() {
	local p
	for p in "$APP_DIR" "$STORAGE"; do
		[[ $p == /* ]] || die "path must be absolute: $p"
		case $p in
		/home/* | /root/* | /run/user/*) die "$p is hidden by ProtectHome=true in the unit" ;;
		/tmp/* | /var/tmp/*) warn "$p is hidden by PrivateTmp=true in the unit" ;;
		esac
	done
	[[ -f $APP_DIR/.env.example ]] || die "$APP_DIR/.env.example missing (is FM_APP_DIR the repo?)"
	case "$STORAGE/" in "$APP_DIR"/*) die "STORAGE must not be inside APP_DIR (SPEC §3.4)" ;; esac
}

# --- 2. memory and swap -------------------------------------------------------
check_memory() {
	local mem swap
	mem=$(awk '/^MemTotal:/ {print int($2/1024)}' /proc/meminfo)
	swap=$(awk '/^SwapTotal:/ {print int($2/1024)}' /proc/meminfo)
	say "memory: ${mem} MiB RAM, ${swap} MiB swap"
	if ! needs_build || ((mem >= 3072 || swap >= 2048)); then
		return 0
	fi
	warn "building on this server needs ±1.5 GB+ RAM; RAM < 3 GB and swap < 2 GB"
	info "recommended: build in CI and pass --binary, or add a 2 GB swapfile:"
	info "  fallocate -l 2G /swapfile && chmod 600 /swapfile && mkswap /swapfile && swapon /swapfile"
	info "  echo '/swapfile none swap sw 0 0' >> /etc/fstab"
	[[ $NO_SWAP == 1 || -e /swapfile ]] && return 0
	dry && { printf '[dry-run] would ask to create /swapfile (2 GB, default: no)\n'; return 0; }
	if [[ -t 0 && $FM_YES != 1 ]] && confirm "Create /swapfile (2 GB) now?" n; then
		fallocate -l 2G /swapfile
		chmod 600 /swapfile
		mkswap /swapfile >/dev/null
		swapon /swapfile
		grep -qE '^/swapfile[[:space:]]' /etc/fstab || echo '/swapfile none swap sw 0 0' >>/etc/fstab
		say "swapfile created"
	fi
}

# --- 3. apt packages ----------------------------------------------------------
pkg_installed() { dpkg-query -W -f='${Status}' "$1" 2>/dev/null | grep -q 'install ok installed'; }

install_packages() {
	local pkgs=(ffmpeg libvips-tools poppler-utils ripgrep curl ca-certificates openssl) missing=() p seven=7zip
	if [[ $SKIP_APT == 1 ]]; then
		say "apt: skipped (--skip-apt)"
	else
		# Ubuntu 24.04: package 7zip (23.01) → /usr/lib/7zip/7z (verified). Older
		# releases without the package fall back to p7zip-full (perlu verifikasi).
		if ! pkg_installed 7zip && ! apt-cache show 7zip >/dev/null 2>&1; then seven=p7zip-full; fi
		pkgs+=("$seven")
		((WITH_UNRAR)) && pkgs+=(unrar)
		for p in "${pkgs[@]}"; do pkg_installed "$p" || missing+=("$p"); done
		if ((${#missing[@]} == 0)); then
			say "apt: all packages present (${pkgs[*]})"
		else
			if ((WITH_UNRAR)) && [[ " ${missing[*]} " == *" unrar "* ]] && ! apt-cache show unrar >/dev/null 2>&1; then
				act "enable the multiverse component (unrar is non-free)" add-apt-repository -y multiverse
			fi
			act "apt-get update" apt-get update -q
			act "apt-get install ${missing[*]}" env DEBIAN_FRONTEND=noninteractive apt-get install -y -q "${missing[@]}"
		fi
	fi
	for p in /usr/lib/7zip/7z /usr/bin/7zz /usr/lib/p7zip/7z; do
		[[ -x $p ]] && { SEVENZIP_FOUND=$p; break; }
	done
	[[ -x /usr/bin/unrar ]] && UNRAR_FOUND=/usr/bin/unrar
	[[ -n $SEVENZIP_FOUND ]] || warn "no 7-Zip binary found: 7z archives stay disabled (check-deps)"
}

# --- 4. Go toolchain (build only) ---------------------------------------------
go_min_version() { awk '$1 == "go" {print $2; exit}' "$APP_DIR/backend/go.mod" 2>/dev/null || true; }

current_go() {
	local g
	for g in "$GO_ROOT/bin/go" "$(command -v go 2>/dev/null || true)"; do
		[[ -n $g && -x $g ]] && { "$g" env GOVERSION 2>/dev/null | sed 's/^go//'; return; }
	done
}

ensure_go() {
	local min cur arch json rel url sha tmp
	needs_build || { say "Go: not needed (prebuilt binary)"; return 0; }
	min=$(go_min_version)
	[[ -n $min ]] || die "cannot read the go directive from backend/go.mod"
	cur=$(current_go)
	if [[ -n $cur ]] && version_ge "$cur" "$min"; then
		say "Go: $cur (>= $min from go.mod)"
		return 0
	fi
	arch=$(dpkg --print-architecture 2>/dev/null || uname -m)
	case $arch in amd64 | x86_64) arch=amd64 ;; arm64 | aarch64) arch=arm64 ;; *) die "no Go tarball for $arch" ;; esac
	# Latest stable release and its checksum from the official index — never a
	# version written into this script.
	json=$(curl -fsSL --max-time 30 'https://go.dev/dl/?mode=json') || die "cannot reach go.dev"
	rel=$(printf '%s' "$json" | python3 -I -c '
import json, sys
arch = sys.argv[1]
for r in json.load(sys.stdin):
    if not r.get("stable"):
        continue
    for f in r["files"]:
        if f["os"] == "linux" and f["arch"] == arch and f["kind"] == "archive":
            print(f["filename"], f["sha256"]); sys.exit(0)
sys.exit(1)' "$arch") || die "no stable linux-$arch Go release in the go.dev index"
	url="https://go.dev/dl/${rel%% *}"
	sha=${rel##* }
	say "Go: ${cur:-none} < $min → installing ${rel%% *} (sha256 $sha) into $GO_ROOT"
	dry && { printf '[dry-run] download %s, verify sha256, replace %s, write /etc/profile.d/go.sh\n' "$url" "$GO_ROOT"; return 0; }
	# Download, verify and unpack beside GO_ROOT; the old toolchain is only
	# removed once the new one is complete.
	tmp=$(mktemp -d "$(dirname "$GO_ROOT")/.go-install-XXXXXX")
	curl -fsSL --max-time 600 -o "$tmp/go.tgz" "$url"
	echo "$sha  $tmp/go.tgz" | sha256sum -c --quiet - || { rm -rf "$tmp"; die "Go tarball checksum mismatch"; }
	tar -C "$tmp" -xzf "$tmp/go.tgz" || { rm -rf "$tmp"; die "cannot unpack the Go tarball"; }
	[[ -x $tmp/go/bin/go ]] || { rm -rf "$tmp"; die "unexpected Go tarball layout"; }
	rm -rf "$GO_ROOT"
	mv "$tmp/go" "$GO_ROOT"
	rm -rf "$tmp"
	[[ -f /etc/profile.d/go.sh ]] || echo "export PATH=\$PATH:$GO_ROOT/bin" >/etc/profile.d/go.sh
	"$GO_ROOT/bin/go" version
}

# --- 5. Node + pnpm (build only) ----------------------------------------------
ensure_node() {
	local v
	needs_build || return 0
	v=$(node --version 2>/dev/null | sed 's/^v//' || true)
	if [[ -z $v ]] || ! version_ge "$v" 22.0.0; then
		die "Node >= 22 is required to build the frontend (found '${v:-none}'); install Node 22+/24 or pass --binary from CI"
	fi
	say "Node: $v"
	command -v pnpm >/dev/null 2>&1 || act "enable corepack (pnpm)" corepack enable
}

# --- 6. service user ----------------------------------------------------------
ensure_user() {
	if id -u "$SVC_USER" >/dev/null 2>&1; then
		say "user $SVC_USER exists ($(id "$SVC_USER"))"
		return 0
	fi
	[[ ${FM_SKIP_USER:-0} == 1 ]] && die "user $SVC_USER does not exist (FM_SKIP_USER=1)"
	act "create system user $SVC_USER" useradd --system --user-group --home-dir /nonexistent --no-create-home \
		--shell /usr/sbin/nologin "$SVC_USER"
}

svc_group() { id -gn "$SVC_USER" 2>/dev/null || printf '%s' "$SVC_USER"; }

# --- 7. storage ---------------------------------------------------------------
ensure_storage() {
	local data_dir=$1 owner="" others=() e d grp
	grp=$(svc_group)
	if [[ -d $STORAGE ]]; then
		owner=$(stat -c '%U:%G' "$STORAGE")
		while IFS= read -r e; do
			[[ $e == .system || $e == lost+found ]] || others+=("$e")
		done < <(ls -A "$STORAGE")
		say "storage $STORAGE: owner $owner, mode $(stat -c %a "$STORAGE"), ${#others[@]} entr(y/ies) besides .system"
	else
		act "create $STORAGE" mkdir -p "$STORAGE"
	fi
	if [[ -n $owner && $owner != "$SVC_USER:$grp" ]]; then
		[[ $owner == www:www ]] && warn "$STORAGE belongs to www:www (aaPanel's web user): PHP sites could read user files; it becomes $SVC_USER:$grp 0750"
		if ((${#others[@]})) && dry; then
			printf '[dry-run] would ask whether to chown -R the %s existing item(s) (default: no)\n' "${#others[@]}"
		elif ((${#others[@]})); then
			if confirm "$STORAGE already holds ${#others[@]} item(s); chown them recursively to $SVC_USER?" n; then
				act "chown -R $STORAGE" chown -R "$SVC_USER:$grp" "$STORAGE"
			else
				warn "only the top folder and .system change owner; fix user folders by hand"
			fi
		fi
	fi
	act "owner $SVC_USER:$grp, mode 0750 on $STORAGE" chown "$SVC_USER:$grp" "$STORAGE"
	dry || chmod 0750 "$STORAGE"
	for d in "${SYSTEM_DIRS[@]}"; do
		[[ -d $data_dir/$d ]] || act "create $data_dir/$d" mkdir -p "$data_dir/$d"
	done
	if dry && ! id -u "$SVC_USER" >/dev/null 2>&1; then
		printf '[dry-run] chown -R %s:%s %s\n' "$SVC_USER" "$grp" "$data_dir"
	elif [[ -d $data_dir ]] && [[ -n $(find "$data_dir" \( ! -user "$SVC_USER" -o ! -group "$grp" \) -print -quit) ]]; then
		act "chown -R $data_dir (files left by earlier root runs)" chown -R "$SVC_USER:$grp" "$data_dir"
	fi
	if ! dry; then
		chmod 0750 "$data_dir"
		for d in "${SYSTEM_DIRS[@]}"; do chmod 0750 "$data_dir/$d"; done
	fi
	case "$data_dir/" in "$STORAGE"/*) ;; *) warn "DATA_DIR $data_dir is outside $STORAGE: add it to ReadWritePaths= in the unit" ;; esac
}

# --- 8. application directory -------------------------------------------------
ensure_app_dir() {
	local f d
	# The checkout keeps its owner (git refuses a work tree owned by someone
	# else, and the service must not be able to rewrite its own binary); the
	# service only needs to traverse it, read .env and run bin/filemanager.
	if [[ $(( 0$(stat -c %a "$APP_DIR") & 01 )) == 0 ]]; then
		warn "$APP_DIR is not traversable by others (mode $(stat -c %a "$APP_DIR")); $SVC_USER needs o+x"
	fi
	for d in "$APP_DIR/public" "$APP_DIR/public/.well-known" "$APP_DIR/bin"; do
		[[ -d $d ]] || act "create $d" mkdir -p "$d"
	done
	dry || chmod 0755 "$APP_DIR/public" "$APP_DIR/public/.well-known" "$APP_DIR/bin"
	((SKIP_AAPANEL)) && return 0
	# Default files an aaPanel PHP site writes into its directory (variant B).
	for d in "$APP_DIR" "$APP_DIR/public"; do
		for f in index.html 404.html .htaccess .user.ini; do
			[[ -e $d/$f ]] || continue
			if git_ro ls-files --error-unmatch -- "$d/$f" >/dev/null 2>&1; then
				continue
			fi
			if [[ $f == .user.ini ]] && command -v chattr >/dev/null; then
				act "unlock $d/$f (chattr -i)" chattr -i "$d/$f"
			fi
			act "remove aaPanel default file $d/$f" rm -f -- "$d/$f"
		done
	done
}

# --- 9. .env ------------------------------------------------------------------
random_password() {
	local p
	p=$(openssl rand -base64 64 | tr -dc 'A-Za-z0-9')
	printf '%s' "${p:0:24}"
}

# ask_admin_password — the operator's choice (read without echo), or nothing
# when a random password should be generated.
ask_admin_password() {
	local p1 p2
	if [[ -t 0 && $FM_YES != 1 ]]; then
		while :; do
			read -r -s -p "Initial admin password (>= 12 chars, empty = random): " p1 </dev/tty || p1=
			echo >/dev/tty
			[[ -z $p1 ]] && return 0
			if ((${#p1} < 12)) || [[ $p1 =~ [[:space:]\"\'\\\$\#\`] ]]; then
				echo "  at least 12 characters, no spaces, quotes, \\, \$, # or backtick" >/dev/tty
				continue
			fi
			read -r -s -p "Repeat: " p2 </dev/tty || p2=
			echo >/dev/tty
			[[ $p1 == "$p2" ]] && { printf '%s' "$p1"; return; }
			echo "  passwords differ" >/dev/tty
		done
	fi
}

ensure_env() {
	local env=$APP_DIR/.env cur="" new secret pw grp listen base pending=0
	grp=$(svc_group)
	if [[ -f $env ]]; then
		cur=$(cat "$env")
		new=$(printf '%s\n' "$cur" | env_clean)
		[[ $new == "$cur" ]] || warn ".env had comments after values (systemd keeps them in the value): cleaned"
	else
		say ".env missing → created from .env.example"
		new=$(env_clean <"$APP_DIR/.env.example")
		[[ $STORAGE == "$DEFAULT_STORAGE" ]] || new=$(printf '%s\n' "$new" | env_set STORAGE_ROOT "$STORAGE" | env_set DATA_DIR "$STORAGE/.system")
		[[ -n ${FM_LISTEN_ADDR:-} ]] && new=$(printf '%s\n' "$new" | env_set LISTEN_ADDR "$FM_LISTEN_ADDR")
		[[ -n ${FM_BASE_URL:-} ]] && new=$(printf '%s\n' "$new" | env_set APP_BASE_URL "$FM_BASE_URL")
		if [[ -n $SEVENZIP_FOUND && $SEVENZIP_FOUND != /usr/lib/7zip/7z ]]; then
			new=$(printf '%s\n' "$new" | env_set SEVENZIP_PATH "$SEVENZIP_FOUND")
		fi
	fi
	secret=$(printf '%s\n' "$new" | awk 'index($0,"APP_SECRET=")==1 {v=substr($0,12)} END {print v}')
	if [[ -z $secret || $secret == CHANGE_ME ]]; then
		dry && printf '[dry-run] APP_SECRET is empty: would generate one (openssl rand -hex 32)\n' && pending=1
		dry || new=$(printf '%s\n' "$new" | env_set APP_SECRET "$(openssl rand -hex 32)")
	elif [[ ! $secret =~ ^[0-9a-fA-F]{64}$ ]]; then
		die "APP_SECRET in $env is not 64 hex characters"
	fi
	pw=$(printf '%s\n' "$new" | awk 'index($0,"ADMIN_INITIAL_PASSWORD=")==1 {v=substr($0,24)} END {print v}')
	if [[ $pw == CHANGE_ME || -z $pw ]]; then
		if dry; then
			printf '[dry-run] ADMIN_INITIAL_PASSWORD is not set: would ask for one (or generate 24 random characters)\n'
			pending=1
		else
			pw=$(ask_admin_password)
			NEW_ADMIN_PASSWORD=chosen
			if [[ -z $pw ]]; then
				pw=$(random_password)
				NEW_ADMIN_PASSWORD=generated
				ADMIN_PASSWORD_VALUE=$pw
			fi
			new=$(printf '%s\n' "$new" | env_set ADMIN_INITIAL_PASSWORD "$pw")
		fi
	fi
	[[ $UNRAR_FOUND && -z $(printf '%s\n' "$new" | awk -F= '$1=="UNRAR_PATH"{print $2}') ]] && new=$(printf '%s\n' "$new" | env_set UNRAR_PATH "$UNRAR_FOUND")
	if printf '%s\n' "$new" | grep -qiE '^ALLOW_ROOT=(true|1|yes)'; then
		warn ".env sets ALLOW_ROOT=true — remove it in production (the service runs as $SVC_USER)"
	fi
	if [[ $new != "$cur" || $pending == 1 ]]; then
		if plan "write $env (0600, $SVC_USER)"; then
			[[ -f $env ]] && install -m 0600 -o "$(stat -c %U "$env")" "$env" "$env.bak-$(date +%Y%m%d-%H%M%S)"
			(umask 077 && printf '%s\n' "$new" >"$env.tmp")
			chown "$SVC_USER:$grp" "$env.tmp"
			mv -f "$env.tmp" "$env"
			CHANGED_SERVICE=1
		fi
	else
		say ".env up to date"
	fi
	if [[ -f $env ]] && [[ $(stat -c '%U:%a' "$env") != "$SVC_USER:600" ]]; then
		act "owner $SVC_USER, mode 0600 on .env" chown "$SVC_USER:$grp" "$env"
		dry || chmod 0600 "$env"
	fi
	listen=$(env_get "$env" LISTEN_ADDR)
	base=$(env_get "$env" APP_BASE_URL)
	[[ -z $listen || $listen == 127.0.0.1:* || $listen == "[::1]:"* || $listen == localhost:* ]] ||
		warn "LISTEN_ADDR=$listen is not loopback; Nginx must be the only public entry"
	say "APP_BASE_URL=${base:-?} LISTEN_ADDR=${listen:-127.0.0.1:8080}"
}

# --- 10. binary ---------------------------------------------------------------
build_as() { as_user "$1" env "PATH=$GO_ROOT/bin:$PATH" make -C "$APP_DIR" build; }

binary_commit() { "$1" version 2>/dev/null | sed -n 's/.*(commit \([^,]*\),.*/\1/p'; }

ensure_binary() {
	local bin=$APP_DIR/bin/filemanager owner head
	if [[ -n $BINARY ]]; then
		"$BINARY" version >/dev/null || die "$BINARY does not run"
		if [[ -f $bin ]] && cmp -s "$BINARY" "$bin"; then
			say "binary up to date ($("$bin" version))"
		else
			act "install $BINARY → bin/filemanager" install -m 0755 "$BINARY" "$bin"
			CHANGED_SERVICE=1
		fi
	elif ((SKIP_BUILD)); then
		[[ -x $bin ]] || die "--skip-build but $bin is missing"
		say "binary: using existing $("$bin" version)"
	else
		owner=$(repo_owner "$APP_DIR")
		head=$(git_ro rev-parse --short HEAD 2>/dev/null || echo none)
		if ((REBUILD == 0)) && [[ -x $bin && $(binary_commit "$bin") == "$head" ]] &&
			[[ -z $(git_ro status --porcelain --untracked-files=no 2>/dev/null) ]]; then
			say "binary already built from $head (--rebuild forces a build)"
		else
			act "build bin/filemanager as $owner" build_as "$owner"
			CHANGED_SERVICE=1
		fi
	fi
	[[ -f $bin ]] && ! dry && chmod 0755 "$bin"
	return 0
}

# --- 11. dependency table + migrations ----------------------------------------
check_deps_and_migrate() {
	local bin=$APP_DIR/bin/filemanager env=$APP_DIR/.env
	if [[ ! -x $bin || ! -f $env ]]; then
		dry || die "missing $bin or $env"
		printf '[dry-run] would run: bin/filemanager check-deps and migrate up as %s\n' "$SVC_USER"
		return 0
	fi
	if dry && ! id -u "$SVC_USER" >/dev/null 2>&1; then
		say "check-deps (as $(id -un): $SVC_USER does not exist yet)"
		"$bin" check-deps --env-file "$env" || warn "check-deps reported a problem"
	else
		say "check-deps (as $SVC_USER)"
		as_user "$SVC_USER" "$bin" check-deps --env-file "$env" || warn "check-deps reported a problem"
	fi
	act "migrate up (as $SVC_USER)" as_user "$SVC_USER" "$bin" migrate up --env-file "$env"
}

# --- 12. systemd --------------------------------------------------------------
render_unit() {
	local u
	u=$(cat "$SCRIPT_DIR/filemanager.service")
	u=${u//"$DEFAULT_APP_DIR"/"$APP_DIR"}
	u=${u//"$DEFAULT_STORAGE"/"$STORAGE"}
	u=${u//User=filemanager/User=$SVC_USER}
	u=${u//Group=filemanager/Group=$(svc_group)}
	printf '%s\n' "$u"
}

ensure_service() {
	local target=$SYSTEMD_DIR/$SERVICE.service unit url state
	unit=$(render_unit)
	if [[ -f $target ]] && [[ $(cat "$target") == "$unit" ]]; then
		say "unit $target up to date"
	else
		if plan "install $target + daemon-reload"; then
			mkdir -p "$SYSTEMD_DIR"
			printf '%s\n' "$unit" >"$target.tmp"
			chmod 0644 "$target.tmp"
			mv -f "$target.tmp" "$target"
			systemctl daemon-reload
		fi
		CHANGED_SERVICE=1
	fi
	if dry; then
		printf '[dry-run] systemctl enable %s; start it (or restart when the unit/binary/.env changed); wait for /readyz <= %ss\n' "$SERVICE" "$READY_TIMEOUT"
		return 0
	fi
	systemctl enable --quiet "$SERVICE"
	state=$(systemctl is-active "$SERVICE" 2>/dev/null || true)
	if [[ $state != active ]]; then
		say "start $SERVICE"
		systemctl start "$SERVICE"
	elif ((CHANGED_SERVICE)); then
		say "restart $SERVICE (unit, binary or .env changed)"
		systemctl restart "$SERVICE"
	else
		say "$SERVICE already running, nothing changed"
	fi
	url=$(listen_url "$(env_get "$APP_DIR/.env" LISTEN_ADDR)")
	if ! wait_ready "$url" "$READY_TIMEOUT" "$SERVICE"; then
		journalctl -u "$SERVICE" -n 50 --no-pager 2>/dev/null || true
		die "$url/readyz did not answer 200 within ${READY_TIMEOUT}s"
	fi
	say "ready: $url/readyz → 200"
}

# --- 13. summary --------------------------------------------------------------
print_summary() {
	local url
	url=$(listen_url "$(env_get "$APP_DIR/.env" LISTEN_ADDR)")
	echo
	say "FileManager installed in $APP_DIR (storage $STORAGE, service $SERVICE as $SVC_USER)"
	if [[ -n $NEW_ADMIN_PASSWORD ]]; then
		if [[ $NEW_ADMIN_PASSWORD == generated && -n ${ADMIN_PASSWORD_VALUE:-} ]] && { : >/dev/tty; } 2>/dev/null; then
			printf '\n    Initial admin password (shown once, not logged): %s\n\n' "$ADMIN_PASSWORD_VALUE" >/dev/tty
		else
			info "initial admin password: see ADMIN_INITIAL_PASSWORD in $APP_DIR/.env (0600)"
		fi
	fi
	info "after the first login + password change: delete ADMIN_INITIAL_PASSWORD from .env"
	info "the port stays on loopback; do NOT open it in UFW or the aaPanel firewall"
	((SKIP_AAPANEL)) && return 0
	cat <<EOT

aaPanel checklist (details: docs/DEPLOY-AAPANEL.md):
  1. Website → Reverse Proxy → Add: domain file.kppbwi.app, target http://127.0.0.1:8080,
     send domain \$http_host, remark FileManager (menu names: perlu verifikasi).
  2. Proxy "/" → timeouts: connect 10 s, send 3600 s, read 3600 s;
     custom configuration ← deploy/nginx-aapanel.conf part A.
  3. Config file → server block ← deploy/nginx-aapanel.conf part B; save (aaPanel runs nginx -t).
  4. SSL → Let's Encrypt (file verification) → Apply → Force HTTPS.
  5. Verify:
       curl -sI https://file.kppbwi.app/healthz            # 200 + Strict-Transport-Security
       curl -sI https://file.kppbwi.app/.env               # 404
       curl -sI https://file.kppbwi.app/bin/filemanager    # 404
       curl -sI https://file.kppbwi.app/readyz             # 404 (loopback only: $url/readyz)
EOT
}

main() {
	parse_args "$@"
	dry && say "DRY RUN — nothing will be changed"
	check_root
	check_os
	check_paths
	cd "$APP_DIR" # like WorkingDirectory= of the unit
	say "app dir $APP_DIR · storage $STORAGE · unit dir $SYSTEMD_DIR · user $SVC_USER"
	check_memory
	install_packages
	ensure_go
	ensure_node
	ensure_user
	ensure_env
	local data_dir
	data_dir=$(env_get "$APP_DIR/.env" DATA_DIR)
	data_dir=${data_dir:-$STORAGE/.system}
	# chown -R/chmod below act on this path: never on / or a top-level directory.
	[[ $data_dir == /*/* && $data_dir != *..* ]] || die "DATA_DIR must be an absolute path at least two levels deep: '$data_dir'"
	ensure_storage "$data_dir"
	ensure_app_dir
	ensure_binary
	check_deps_and_migrate
	ensure_service
	print_summary
}

main "$@"
