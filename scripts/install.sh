#!/bin/sh
# TwinPane installer: downloads an official release binary from GitHub
# Releases, checks its SHA-256 checksum and installs it.
#
#   curl -fsSL https://raw.githubusercontent.com/ThatOn3Gu7/TwinPane/master/scripts/install.sh | sh
#   wget -qO-  https://raw.githubusercontent.com/ThatOn3Gu7/TwinPane/master/scripts/install.sh | sh
#
# Options go after "sh -s --", for example:  ... | sh -s -- --dir "$HOME/bin"
# Run with --help for all options. The installer never uses sudo, never runs
# the downloaded program and leaves an existing installation untouched when
# anything fails.
#
# Supported: Android ARM64 (Termux), Linux ARM64, Linux x86_64, macOS ARM64,
# macOS x86_64. File names follow build.sh ("./build.sh list-assets").

set -u

REPO="ThatOn3Gu7/TwinPane"
# Base URLs; overridable for mirrors and for the installer's own tests.
API_BASE="${TWINPANE_API_BASE:-https://api.github.com}"
DOWNLOAD_BASE="${TWINPANE_DOWNLOAD_BASE:-https://github.com}"
SUPPORTED_TARGETS="android-arm64 linux-arm64 linux-amd64 darwin-arm64 darwin-amd64"

INSTALL_DIR="${TWINPANE_INSTALL_DIR:-}"
REQ_VERSION="${TWINPANE_VERSION:-}"
DOWNLOADER="${TWINPANE_DOWNLOADER:-}"
TARGET=""
DRY_RUN=0

TMP_DIR=""
STAGED=""
STEP=0
STEPS=5

# ---------------------------------------------------------------- output
# Colours: never with NO_COLOR, always with FORCE_COLOR, otherwise only on
# a terminal that is not TERM=dumb. Messages go to stdout, warnings and
# errors to stderr.

setup_colors() {
	C_RESET="" C_BOLD="" C_DIM="" C_RED="" C_GREEN="" C_YELLOW="" C_BLUE="" C_CYAN=""
	[ -n "${NO_COLOR:-}" ] && return
	if [ -n "${FORCE_COLOR:-}" ] || { [ -t 1 ] && [ "${TERM:-}" != dumb ]; }; then
		esc="$(printf '\033')"
		C_RESET="${esc}[0m" C_BOLD="${esc}[1m" C_DIM="${esc}[2m" C_RED="${esc}[31m"
		C_GREEN="${esc}[32m" C_YELLOW="${esc}[33m" C_BLUE="${esc}[34m" C_CYAN="${esc}[36m"
	fi
}
setup_colors

say() { printf '%s\n' "$*"; }
step() { # step TEXT: the next numbered stage
	STEP=$((STEP + 1))
	printf '%s==>%s %s[%d/%d]%s %s\n' "$C_BLUE$C_BOLD" "$C_RESET" "$C_DIM" "$STEP" "$STEPS" "$C_RESET" "$C_BOLD$*$C_RESET"
}
ok() { printf '    %sok%s  %s\n' "$C_GREEN$C_BOLD" "$C_RESET" "$*"; }
info() { printf '    %s--%s  %s\n' "$C_DIM" "$C_RESET" "$*"; }
warn() { printf '    %swarning:%s %s\n' "$C_YELLOW$C_BOLD" "$C_RESET" "$*" >&2; }
die() {
	printf '%stwinpane-install: error:%s %s\n' "$C_RED$C_BOLD" "$C_RESET" "$*" >&2
	if [ "$STEP" -gt 0 ]; then
		printf '%sInstallation failed; nothing was changed.%s\n' "$C_RED" "$C_RESET" >&2
	fi
	exit 1
}

banner() {
	printf '\n'
	printf '  %s+------------+------------+%s\n' "$C_CYAN" "$C_RESET"
	printf '  %s|%s %sTwin%s       %s|%s       %sPane%s %s|%s\n' "$C_CYAN" "$C_RESET" "$C_BOLD" "$C_RESET" "$C_CYAN" "$C_RESET" "$C_BOLD" "$C_RESET" "$C_CYAN" "$C_RESET"
	printf '  %s+------------+------------+%s\n' "$C_CYAN" "$C_RESET"
	printf '  %sdual-panel file manager for the terminal%s\n\n' "$C_DIM" "$C_RESET"
}

cleanup() {
	if [ -n "$STAGED" ] && [ -e "$STAGED" ]; then rm -f "$STAGED"; fi
	if [ -n "$TMP_DIR" ] && [ -d "$TMP_DIR" ]; then rm -rf "$TMP_DIR"; fi
}
interrupted() {
	cleanup
	printf '\n%sInterrupted: nothing was installed or changed.%s\n' "$C_YELLOW" "$C_RESET" >&2
	exit "$1"
}
trap cleanup EXIT
trap 'interrupted 130' INT
trap 'interrupted 143' TERM HUP

usage() {
	cat <<'EOF'
Install TwinPane from GitHub Releases.

Usage: install.sh [options]

  -d, --dir DIR          install into DIR (created if missing)
                         default: $PREFIX/bin in Termux, $HOME/.local/bin elsewhere
  -v, --version VERSION  install this release (v0.7.0 or 0.7.0) instead of the latest
      --target TARGET    skip detection and install TARGET, one of:
                         android-arm64 linux-arm64 linux-amd64 darwin-arm64 darwin-amd64
      --downloader TOOL  use curl or wget (default: curl if present, else wget)
  -n, --dry-run          show what would be installed, download nothing
  -h, --help             show this help

Environment: TWINPANE_INSTALL_DIR, TWINPANE_VERSION and TWINPANE_DOWNLOADER
set the same things as --dir, --version and --downloader.
EOF
}

need_arg() { [ "$2" -ge 2 ] || die "$1 needs a value (see --help)"; }

parse_args() {
	while [ $# -gt 0 ]; do
		case "$1" in
		-d | --dir) need_arg "$1" $#; INSTALL_DIR="$2"; shift 2 ;;
		--dir=*) INSTALL_DIR="${1#*=}"; shift ;;
		-v | --version) need_arg "$1" $#; REQ_VERSION="$2"; shift 2 ;;
		--version=*) REQ_VERSION="${1#*=}"; shift ;;
		--target) need_arg "$1" $#; TARGET="$2"; shift 2 ;;
		--target=*) TARGET="${1#*=}"; shift ;;
		--downloader) need_arg "$1" $#; DOWNLOADER="$2"; shift 2 ;;
		--downloader=*) DOWNLOADER="${1#*=}"; shift ;;
		-n | --dry-run) DRY_RUN=1; shift ;;
		-h | --help) usage; exit 0 ;;
		*) die "unknown option: $1 (see --help)" ;;
		esac
	done
}

# ---------------------------------------------------------------- platform

# is_android: Termux and other Android shells. Several independent signs,
# because none of them is present in every setup.
is_android() {
	[ -n "${TERMUX_VERSION:-}" ] && return 0
	case "${PREFIX:-}" in */com.termux/*) return 0 ;; esac
	[ "$(uname -o 2>/dev/null)" = "Android" ] && return 0
	[ -n "${ANDROID_ROOT:-}" ] && [ -n "${ANDROID_DATA:-}" ] && return 0
	if command -v getprop >/dev/null 2>&1; then
		[ -n "$(getprop ro.build.version.sdk 2>/dev/null)" ] && return 0
	fi
	return 1
}

detect_target() {
	os_raw="$(uname -s 2>/dev/null)" || die "cannot run uname to detect the system"
	arch_raw="$(uname -m 2>/dev/null)" || die "cannot run uname to detect the CPU"
	case "$os_raw" in
	Linux)
		if is_android; then os=android; else os=linux; fi
		;;
	Darwin) os=darwin ;;
	MINGW* | MSYS* | CYGWIN* | Windows_NT)
		die "Windows is not supported by this installer. Download twinpane-<version>-windows-amd64.zip (untested) from https://github.com/$REPO/releases"
		;;
	*) die "unsupported operating system: $os_raw (supported: Android/Termux, Linux, macOS)" ;;
	esac
	case "$arch_raw" in
	aarch64 | arm64 | armv8*) arch=arm64 ;;
	x86_64 | amd64) arch=amd64 ;;
	armv7* | armv6* | armv5* | arm | i386 | i486 | i586 | i686 | x86)
		die "32-bit CPUs ($arch_raw) are not supported; TwinPane is built for 64-bit ARM and x86_64 only"
		;;
	*) die "unsupported CPU architecture: $arch_raw" ;;
	esac
	# An x86_64 shell on Apple silicon (Rosetta) still gets the native build.
	if [ "$os" = darwin ] && [ "$arch" = amd64 ]; then
		if [ "$(sysctl -n sysctl.proc_translated 2>/dev/null)" = "1" ]; then arch=arm64; fi
	fi
	if [ "$os" = android ] && [ "$arch" != arm64 ]; then
		die "Android is supported on ARM64 only (this device reports $arch_raw)"
	fi
	TARGET="$os-$arch"
}

check_target() {
	for t in $SUPPORTED_TARGETS; do
		[ "$t" = "$1" ] && return 0
	done
	die "unknown target '$1' (choose one of: $SUPPORTED_TARGETS)"
}

# ---------------------------------------------------------------- download

pick_downloader() {
	if [ -n "$DOWNLOADER" ]; then
		case "$DOWNLOADER" in
		curl | wget) ;;
		*) die "--downloader must be curl or wget (got '$DOWNLOADER')" ;;
		esac
		command -v "$DOWNLOADER" >/dev/null 2>&1 || die "$DOWNLOADER was requested but is not installed"
		return
	fi
	if command -v curl >/dev/null 2>&1; then
		DOWNLOADER=curl
	elif command -v wget >/dev/null 2>&1; then
		DOWNLOADER=wget
	else
		die "neither curl nor wget is installed. Install one (Termux: pkg install curl; Debian/Ubuntu: sudo apt install curl), or download the release manually from https://github.com/$REPO/releases"
	fi
}

# fetch URL OUTPUT [api|progress]: download URL to OUTPUT. Sets HTTP_CODE
# when known. "progress" shows a progress bar on an interactive terminal.
fetch() {
	# POSIX sh has no local variables: f_* names only, so callers keep theirs.
	f_url="$1"
	f_out="$2"
	f_mode="${3:-}"
	f_bar=0
	[ "$f_mode" = progress ] && [ -t 2 ] && [ "${TERM:-}" != dumb ] && f_bar=1
	HTTP_CODE=""
	rm -f "$f_out"
	if [ "$DOWNLOADER" = curl ]; then
		set -- -fSL --retry 2 --connect-timeout 20 -o "$f_out" -w '%{http_code}'
		[ $f_bar -eq 1 ] || set -- "$@" -s
		case "$f_url" in https://*) set -- "$@" --proto '=https' --tlsv1.2 ;; esac
		[ "$f_mode" = api ] && set -- "$@" -H 'Accept: application/vnd.github+json'
		if [ $f_bar -eq 1 ]; then
			HTTP_CODE="$(curl "$@" --progress-bar "$f_url")"
		else
			HTTP_CODE="$(curl "$@" "$f_url" 2>/dev/null)"
		fi
		f_rc=$?
	else
		set -- -q -T 30 -O "$f_out"
		[ "$f_mode" = api ] && set -- "$@" --header='Accept: application/vnd.github+json'
		if [ $f_bar -eq 1 ] && wget --help 2>&1 | grep -q -- '--show-progress'; then
			wget "$@" --show-progress "$f_url"
		else
			wget "$@" "$f_url" 2>/dev/null
		fi
		f_rc=$?
		[ $f_rc -eq 8 ] && HTTP_CODE="error"
	fi
	if [ $f_rc -ne 0 ]; then
		rm -f "$f_out"
		return 1
	fi
	if [ ! -s "$f_out" ]; then
		rm -f "$f_out"
		HTTP_CODE="empty"
		return 1
	fi
	return 0
}

# explain WHAT URL: a readable reason for the last failed fetch.
explain() {
	case "$HTTP_CODE" in
	404) reason="not found (HTTP 404)" ;;
	403 | 429) reason="refused (HTTP $HTTP_CODE): GitHub limits anonymous API use to 60 requests per hour; wait and retry, or pass --version" ;;
	000 | "") reason="network error (no connection, DNS or TLS failure)" ;;
	error) reason="the server returned an error (for example 404 not found or 403 rate limit)" ;;
	empty) reason="the server returned an empty file" ;;
	*) reason="HTTP $HTTP_CODE" ;;
	esac
	die "could not download $1: $reason
  URL: $2"
}

json_strings() { # json_strings KEY FILE: every "KEY": "value" value, one per line
	tr -d '\r\n' <"$2" | grep -o "\"$1\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" | sed 's/.*"\([^"]*\)"$/\1/'
}

valid_tag() { printf '%s\n' "$1" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; }

resolve_version() {
	if [ -n "$REQ_VERSION" ]; then
		case "$REQ_VERSION" in v*) TAG="$REQ_VERSION" ;; *) TAG="v$REQ_VERSION" ;; esac
		valid_tag "$TAG" || die "invalid version '$REQ_VERSION' (expected MAJOR.MINOR.PATCH, e.g. v0.7.0)"
		RELEASE_JSON=""
		return
	fi
	url="$API_BASE/repos/$REPO/releases/latest"
	RELEASE_JSON="$TMP_DIR/release.json"
	fetch "$url" "$RELEASE_JSON" api || explain "the latest release information" "$url"
	TAG="$(json_strings tag_name "$RELEASE_JSON" | head -n 1)"
	[ -n "$TAG" ] || die "unexpected answer from GitHub (no tag_name in the release information): $url"
	valid_tag "$TAG" || die "the latest release has an unexpected tag '$TAG' (expected vMAJOR.MINOR.PATCH)"
}

# ---------------------------------------------------------------- checks

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
	elif command -v openssl >/dev/null 2>&1; then
		openssl dgst -sha256 "$1" | awk '{print $NF}'
	else
		return 1
	fi
}

verify_checksum() { # verify_checksum FILE NAME SUMS
	expected="$(awk -v f="$2" '$2 == f || $2 == "*" f { print $1; exit }' "$3")"
	[ -n "$expected" ] || die "SHA256SUMS of $TAG has no entry for $2"
	printf '%s\n' "$expected" | grep -Eq '^[0-9a-fA-F]{64}$' || die "SHA256SUMS of $TAG has a malformed entry for $2"
	actual="$(sha256_of "$1")" || die "no SHA-256 tool found (need sha256sum, shasum or openssl; Termux: pkg install coreutils)"
	expected="$(printf '%s' "$expected" | tr 'A-F' 'a-f')"
	[ "$actual" = "$expected" ] || die "checksum mismatch for $2: the download is corrupt or was altered; nothing was installed
  expected $expected
  got      $actual"
}

check_executable_format() { # the file must look like a program for the target
	magic="$(od -An -tx1 -N4 "$1" 2>/dev/null | tr -d ' \n')"
	case "$TARGET" in
	darwin-*)
		case "$magic" in cffaedfe | cefaedfe | cafebabe) return 0 ;; esac ;;
	*)
		[ "$magic" = "7f454c46" ] && return 0 ;;
	esac
	die "the downloaded file is not a $TARGET program; nothing was installed"
}

default_dir() {
	case "$TARGET" in
	android-*)
		if [ -n "${PREFIX:-}" ] && [ -d "$PREFIX/bin" ]; then
			INSTALL_DIR="$PREFIX/bin"
		else
			die "cannot find Termux's \$PREFIX/bin; pass --dir DIR"
		fi
		;;
	*)
		[ -n "${HOME:-}" ] || die "HOME is not set; pass --dir DIR"
		INSTALL_DIR="$HOME/.local/bin"
		;;
	esac
}

# ---------------------------------------------------------------- dependencies

# check_required: the tools the installer itself needs, all reported at once.
check_required() {
	missing=""
	for t in uname tar gzip mktemp od awk; do
		command -v "$t" >/dev/null 2>&1 || missing="$missing $t"
	done
	if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1 &&
		! command -v openssl >/dev/null 2>&1; then
		missing="$missing sha256sum"
	fi
	if [ -n "$missing" ]; then
		if is_android; then hint="pkg install coreutils tar gzip"; else hint="install them with your package manager"; fi
		die "missing required tools:$missing ($hint)"
	fi
}

# report_optional: programs TwinPane uses when present (never required).
report_optional() {
	termux=0
	case "$TARGET" in android-*) termux=1 ;; esac
	opt_line() { # opt_line NAME FOUND WHAT TERMUX_PKG OTHER_HINT
		if [ -n "$2" ]; then
			ok "$1: $2"
		elif [ $termux -eq 1 ]; then
			info "$1: not installed ($3) - pkg install $4"
		else
			info "$1: not installed ($3) - $5"
		fi
	}
	opt_line rclone "$(command -v rclone 2>/dev/null)" "optional, for cloud remotes" rclone "https://rclone.org/install/"
	f="$(command -v fd 2>/dev/null || command -v fdfind 2>/dev/null)"
	opt_line fd "$f" "optional, faster find" fd "package fd-find or fd"
	f="$(command -v 7zz 2>/dev/null || command -v 7z 2>/dev/null || command -v 7za 2>/dev/null || command -v 7zr 2>/dev/null)"
	opt_line 7-Zip "$f" "optional, for 7Z archives" 7zip "package 7zip or p7zip"
	if [ -n "${VISUAL:-}${EDITOR:-}" ]; then
		ok "editor: ${VISUAL:-$EDITOR}"
	else
		info "editor: \$EDITOR is not set (TwinPane looks for an installed editor)"
	fi
	if [ -n "$(command -v rclone 2>/dev/null)" ]; then
		info "rclone: remotes are set up with 'rclone config'; TwinPane never stores logins"
	fi
}

# ---------------------------------------------------------------- main

main() {
	parse_args "$@"
	banner

	step "Checking the system"
	check_required
	if [ -n "$TARGET" ]; then check_target "$TARGET"; else detect_target; fi
	[ -n "$INSTALL_DIR" ] || default_dir
	pick_downloader
	ok "platform $TARGET, downloads with $DOWNLOADER"

	TMP_DIR="$(mktemp -d 2>/dev/null || mktemp -d -t twinpane)" || die "cannot create a temporary folder"
	[ -d "$TMP_DIR" ] || die "cannot create a temporary folder"

	step "Finding the release"
	resolve_version
	base="twinpane-$TAG-$TARGET"
	asset="$base.tar.gz"
	if [ -n "$RELEASE_JSON" ]; then
		names="$(json_strings name "$RELEASE_JSON")"
		printf '%s\n' "$names" | grep -Fqx "$asset" ||
			die "release $TAG has no file $asset (no build for $TARGET in this release?): https://github.com/$REPO/releases/tag/$TAG"
		printf '%s\n' "$names" | grep -Fqx "SHA256SUMS" ||
			die "release $TAG has no SHA256SUMS file, so the download cannot be verified"
	fi
	ok "TwinPane $TAG ($asset)"

	if [ "$DRY_RUN" -eq 1 ]; then
		say "target:      $TARGET"
		say "version:     $TAG"
		say "file:        $asset"
		say "install to:  $INSTALL_DIR/twinpane"
		say "downloader:  $DOWNLOADER"
		say "(dry run: nothing downloaded or installed)"
		return 0
	fi

	if [ ! -d "$INSTALL_DIR" ]; then
		mkdir -p "$INSTALL_DIR" 2>/dev/null || die "cannot create $INSTALL_DIR; choose another folder with --dir"
	fi
	[ -w "$INSTALL_DIR" ] || die "$INSTALL_DIR is not writable; choose another folder with --dir (this installer never uses sudo)"
	dest="$INSTALL_DIR/twinpane"
	[ -d "$dest" ] && die "$dest is a folder; remove it or choose another --dir"

	step "Downloading"
	say "    Downloading TwinPane $TAG for $TARGET with $DOWNLOADER..."
	url="$DOWNLOAD_BASE/$REPO/releases/download/$TAG"
	fetch "$url/$asset" "$TMP_DIR/$asset" progress || explain "$asset" "$url/$asset"
	fetch "$url/SHA256SUMS" "$TMP_DIR/SHA256SUMS" || explain "SHA256SUMS" "$url/SHA256SUMS"
	verify_checksum "$TMP_DIR/$asset" "$asset" "$TMP_DIR/SHA256SUMS"
	ok "Checksum OK."

	step "Installing"
	mkdir "$TMP_DIR/x" || die "cannot use the temporary folder"
	tar -xzf "$TMP_DIR/$asset" -C "$TMP_DIR/x" "$base/twinpane" 2>/dev/null ||
		die "$asset does not contain $base/twinpane; nothing was installed"
	bin="$TMP_DIR/x/$base/twinpane"
	if [ -L "$bin" ] || [ ! -f "$bin" ] || [ ! -s "$bin" ]; then
		die "$asset contains no usable twinpane program; nothing was installed"
	fi
	check_executable_format "$bin"

	# Copy next to the destination, then rename: the switch is atomic and an
	# old twinpane stays in place if anything before the rename fails.
	existed=0
	[ -e "$dest" ] && existed=1
	STAGED="$INSTALL_DIR/.twinpane.install.$$"
	cp "$bin" "$STAGED" || die "cannot write to $INSTALL_DIR"
	chmod 0755 "$STAGED" || die "cannot make $STAGED executable"
	mv -f "$STAGED" "$dest" || die "cannot move the new program to $dest"
	STAGED=""

	if [ $existed -eq 1 ]; then
		ok "Updated $dest to TwinPane $TAG ($TARGET)."
	else
		ok "Installed TwinPane $TAG ($TARGET) to $dest."
	fi

	step "Optional companions"
	report_optional
	printf '\n%sDone.%s ' "$C_GREEN$C_BOLD" "$C_RESET"
	case ":${PATH:-}:" in
	*":$INSTALL_DIR:"*)
		found="$(command -v twinpane 2>/dev/null || true)"
		if [ -n "$found" ] && [ "$found" != "$dest" ]; then
			warn "another twinpane comes first in PATH: $found"
		fi
		say "Run: twinpane"
		;;
	*)
		say "$INSTALL_DIR is not in your PATH. Add it, for example:"
		say "  echo 'export PATH=\"$INSTALL_DIR:\$PATH\"' >> ~/.profile   (zsh: ~/.zshrc)"
		say "or run: $dest"
		;;
	esac
}

main "$@"
