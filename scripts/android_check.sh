#!/usr/bin/env bash
# android_check.sh — interactive checks under an Android-like seccomp filter.
#
# Android (Termux) kills programs that make system calls outside its seccomp
# allowlist (SIGSYS). TwinPane v0.3.0 died this way when e was pressed: the
# GOOS=linux build looked the editor up with os/exec.LookPath, which uses
# faccessat2. This script runs TwinPane on a raw pseudo-terminal
# (scripts/ptyterm.py) under scripts/android_seccomp.py, which traps the
# same calls Android traps, and drives the editor (real Neovim, set as
# EDITOR=nvim like on the user's phone), the editor chooser and the live
# search with fd and find.
#
# Usage: scripts/android_check.sh [TWINPANE_BINARY] [CONTROL_BINARY]
#   CONTROL_BINARY (optional): a build expected to crash (e.g. v0.3.0); the
#   control scenario then proves the filter really catches the bug.
# Needs: python3 with pyte (PYTHONPATH may point to it), nvim, find; fd
#   (FD_DIR=directory containing fd, optional) for the fd scenario.
# EXTRA_ENV="K=V ..." adds variables (e.g. XDG_CONFIG_HOME for LazyVim).
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
BIN=${1:-$HERE/../dist/twinpane-linux-amd64}
CONTROL=${2:-}
HARNESS="$HERE/ptyterm.py"
SECCOMP="$HERE/android_seccomp.py"
WORK=$(mktemp -d "${TMPDIR:-/tmp}/tp-android-check.XXXXXX")
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/home/notes/deep"
printf 'hello\n' > "$WORK/home/aaa.txt"
printf 'second\n' > "$WORK/home/bbb.txt"
printf 'needle\n' > "$WORK/home/notes/deep/needle-file.md"
LOGDIR=${LOGDIR:-$WORK}; mkdir -p "$LOGDIR"
NVIMDIR=$(dirname "$(command -v nvim)")
BASEPATH="$NVIMDIR:/usr/bin:/bin"

pass=0 fail=0
run() { # name, profile (android8|android11), path, binary, steps...
	local name=$1 profile=$2 path=$3 bin=$4; shift 4
	local out extra=() flag=""
	[ "$profile" = android11 ] && flag="--allow-pidfd"
	for kv in ${EXTRA_ENV:-}; do extra+=(--env "$kv"); done
	if out=$(python3 "$HARNESS" --log "$LOGDIR/$name.log" \
		--env HOME="$WORK/home" --env TERM=xterm-256color --env PATH="$path" \
		--env EDITOR=nvim --env VISUAL= --env LANG=C.UTF-8 "${extra[@]}" -- \
		"type:$SECCOMP $flag -- $bin\\r" wait:1.5 expect:'TwinPane' "$@" 2>&1); then
		echo "PASS  $name"; pass=$((pass+1))
	else
		echo "FAIL  $name"; fail=$((fail+1))
		echo "$out" | tail -40 | sed 's/^/      /'
	fi
}

# TwinPane opens in $HOME: "..", notes/, aaa.txt, bbb.txt
# The first launch of an editor in a session shows its save/quit card, which
# waits for Enter (v0.7.0; before, it started the editor after 2.5 s).
NOCRASH=(reject:'SIGSYS' reject:'bad system call' reject:'0x1b7' reject:'SHELL\$')
QUIT=(type:q wait:1 expect:'SHELL\$')

for profile in android8 android11; do
	run "$profile-e-opens-nvim" $profile "$BASEPATH" "$BIN" \
		type:j wait:0.3 type:j wait:0.3 type:e wait:0.6 expect:'Open aaa\.txt in Neovim\?' 'type:\r' wait:2.5 expect:'hello' "${NOCRASH[@]}" \
		'type:\e:wq\r' wait:1.5 expect:'Finished editing aaa.txt' "${NOCRASH[@]}" "${QUIT[@]}"

	run "$profile-E-chooser" $profile "$BASEPATH" "$BIN" \
		type:j wait:0.3 type:j wait:0.3 type:E wait:1 expect:'Open aaa.txt with' expect:'Neovim' \
		expect:'EDITOR · default' 'type:\r' wait:0.6 expect:'Open aaa\.txt in Neovim\?' 'type:\r' wait:2.5 expect:'hello' "${NOCRASH[@]}" \
		'type:\e:wq\r' wait:1.5 expect:'Finished editing aaa.txt' "${NOCRASH[@]}" "${QUIT[@]}"

	# two selected files, Neovim in tabs (chooser row 2): both tab labels show
	run "$profile-multi-file-tabs" $profile "$BASEPATH" "$BIN" \
		type:j wait:0.3 type:j wait:0.3 'type: ' wait:0.3 'type: ' wait:0.3 expect:'2 sel' \
		type:E wait:1 expect:'Open 2 files with' expect:'Neovim – tabs' type:2 wait:0.6 expect:'Open 2 files in Neovim\?' 'type:\r' wait:2.5 \
		expect:'aaa.txt' expect:'bbb.txt' expect:'hello' "${NOCRASH[@]}" \
		'type:\e:wqa\r' wait:1.5 expect:'Finished editing 2 files' "${NOCRASH[@]}" "${QUIT[@]}"

	run "$profile-ctrl-z-in-editor" $profile "$BASEPATH" "$BIN" \
		type:j wait:0.3 type:j wait:0.3 type:e wait:0.6 expect:'Open aaa\.txt in Neovim\?' 'type:\r' wait:2.5 expect:'hello' 'type:\x1a' wait:2 reject:'Stopped' \
		expect:'hello' 'type:\e:wq\r' wait:1.5 expect:'suspended and has been resumed' "${NOCRASH[@]}" "${QUIT[@]}"
done

# live search: fd (if available) and find run as child processes
if [ -n "${FD_DIR:-}" ] && [ -x "$FD_DIR/fd" ]; then
	run android8-search-fd android8 "$FD_DIR:$BASEPATH" "$BIN" \
		type:/ wait:1.5 expect:'Find files by name' expect:'fd *│' expect:'entries' \
		type:need wait:0.8 expect:'1 match of' expect:'notes/deep/needle-file.md' "${NOCRASH[@]}" \
		'type:\r' wait:1 expect:'found notes/deep/needle-file.md' "${NOCRASH[@]}" "${QUIT[@]}"
else
	echo "SKIP  android8-search-fd (set FD_DIR to a directory containing fd)"
fi
run android8-search-find-edit android8 "$BASEPATH" "$BIN" \
	type:/ wait:1.5 expect:'find *│' type:bbb wait:0.8 expect:'1 match of' \
	'type:\t' wait:0.3 type:e wait:0.6 expect:'Open bbb\.txt in Neovim\?' 'type:\r' wait:2.5 expect:'second' "${NOCRASH[@]}" \
	'type:\e:wq\r' wait:1.5 expect:'Find files by name' expect:'Finished editing bbb.txt' "${NOCRASH[@]}" \
	'type:\e' wait:0.5 "${QUIT[@]}"

# control: a build with the bug must be killed by the filter
if [ -n "$CONTROL" ]; then
	run control-old-build-crashes android11 "$BASEPATH" "$CONTROL" \
		type:j wait:0.3 type:j wait:0.3 type:e wait:2.5 expect:'0x1b7|SIGSYS|bad system call' expect:'SHELL\$'
fi

echo "android checks: $pass passed, $fail failed (binary: $BIN)"
[ "$fail" -eq 0 ]
