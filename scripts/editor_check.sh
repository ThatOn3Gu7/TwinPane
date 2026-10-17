#!/usr/bin/env bash
# editor_check.sh — interactive checks of the external-editor integration with
# a real editor (Neovim), run on a raw pseudo-terminal by scripts/ptyterm.py
# (a terminal emulator that answers terminal queries and runs an interactive
# bash with job control, so a stopped or crashed TwinPane shows up exactly as
# a user would see it: the shell prompt on top of the TUI).
#
# Usage: scripts/editor_check.sh [TWINPANE_BINARY] [NVIM_BINARY] [LATENCY_MS]
# Needs: python3 with pyte (PYTHONPATH may point to it), nvim, pkill.
# EXTRA_ENV="K=V ..." adds variables for the editor (e.g. XDG_CONFIG_HOME to
# test with a full Neovim configuration such as LazyVim).
# Each scenario reproduces one failure mode found while diagnosing the
# "pressing e breaks the terminal" bug.
set -u
HERE=$(cd "$(dirname "$0")" && pwd)
BIN=${1:-$HERE/../dist/twinpane-linux-amd64}
NVIM=${2:-$(command -v nvim)}
LAT=${3:-0}
HARNESS="$HERE/ptyterm.py"
WORK=$(mktemp -d "${TMPDIR:-/tmp}/tp-editor-check.XXXXXX")
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/home"
printf 'hello\n' > "$WORK/home/aaa.txt"
LOGDIR=${LOGDIR:-$WORK}; mkdir -p "$LOGDIR"

pass=0 fail=0
run() { # name, steps...
	local name=$1; shift
	local out extra=()
	for kv in ${EXTRA_ENV:-}; do extra+=(--env "$kv"); done
	if out=$(python3 "$HARNESS" --latency "$LAT" --log "$LOGDIR/$name.log" \
		--env HOME="$WORK/home" --env TERM=xterm-256color --env PATH="$PATH" \
		--env EDITOR="$NVIM" --env VISUAL= --env LANG=C.UTF-8 "${extra[@]}" -- \
		"type:$BIN\\r" wait:1.5 expect:'TwinPane' "$@" 2>&1); then
		echo "PASS  $name"; pass=$((pass+1))
	else
		echo "FAIL  $name"; fail=$((fail+1))
		echo "$out" | tail -40 | sed 's/^/      /'
	fi
}

# Entering the file: TwinPane opens in $HOME; ".." is first, aaa.txt second.
# The first edit shows the save/quit card, which waits for Enter (Phase 7;
# before v0.7.0 it opened the editor by itself after 2.5 s); then allow for
# the editor's own start-up time.
OPEN=(type:j wait:0.3 type:e wait:0.5 expect:'Open aaa\.txt in Neovim\?' wait:3 expect:'Open aaa\.txt in Neovim\?'
	'type:\r' wait:2 expect:'hello' reject:'Stopped')
# ptyterm's shell prompt is "SHELL$": it must only appear after quitting.
BACK=(reject:'Stopped' reject:'SHELL\$')
QUIT=(type:q wait:1 expect:'SHELL\$')

run normal-edit "${OPEN[@]}" 'type:\e:wq\r' wait:1.5 expect:'Finished editing aaa.txt' "${BACK[@]}" \
	type:j type:k wait:0.3 'type:?' wait:0.5 expect:'keyboard controls' 'type:\e' wait:0.3 "${QUIT[@]}"

run ctrl-z-in-editor "${OPEN[@]}" 'type:\x1a' wait:2 reject:'Stopped' expect:'hello' \
	'type:\e:wq\r' wait:1.5 expect:'suspended and has been resumed' "${BACK[@]}" "${QUIT[@]}"

run editor-cq "${OPEN[@]}" 'type:\e:cq\r' wait:1.5 expect:'exited with status 1' "${BACK[@]}" "${QUIT[@]}"

# kill -9 the editor (children of the TwinPane process under test only)
KILL_EDITOR="sh:pkill -KILL -P \$(pgrep -n -f -- '^$BIN\$')"
run editor-killed "${OPEN[@]}" "$KILL_EDITOR" wait:1.5 \
	expect:'was terminated' "${BACK[@]}" 'type:?' wait:0.5 expect:'keyboard controls' 'type:\e' wait:0.3 "${QUIT[@]}"

# "e:wq" typed ahead in one burst: the editor opens, ":wq" must not quit TwinPane
run typeahead-e-wq type:j wait:0.3 'type:e:wq' wait:1 expect:'Open aaa\.txt in Neovim\?' 'type:\r' wait:2 expect:'hello' reject:'Stopped' \
	'type:\e:wq\r' wait:1.5 expect:'Finished editing' "${BACK[@]}" "${QUIT[@]}"

# "ee": exactly one editor
run double-e type:j wait:0.3 type:ee wait:1 expect:'Open aaa\.txt in Neovim\?' 'type:\r' wait:2 expect:'hello' 'type:\e:wq\r' wait:1.5 \
	expect:'Finished editing' reject:'hello' "${BACK[@]}" "${QUIT[@]}"

echo "editor checks: $pass passed, $fail failed (nvim: $NVIM, latency ${LAT}ms)"
[ "$fail" -eq 0 ]
