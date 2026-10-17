#!/usr/bin/env bash
# phase7_check.sh - end-to-end checks of the v0.7.0 changes on a raw
# pseudo-terminal (scripts/ptyterm.py), with input sent exactly as Termux
# sends it (a tap = SGR press + release, a double tap = two taps 120 ms
# apart, a swipe = wheel events):
#   * a double tap on a Markdown / text file shows the editor confirmation
#     card, which stays (idle > 10 s, swipes, unrelated keys) until the user
#     chooses: Open starts the editor once, Cancel / Esc start nothing;
#     with a real Neovim and with a mock handler whose name is not Neovim;
#   * the binary-file warning is unchanged;
#   * the help screen keeps its frame while scrolling, filters with /, and
#     fits a minimum-size terminal.
# This exercises the real binary; it is not a test on a phone.
#
# usage: scripts/phase7_check.sh [BIN]      env: LOGDIR (keep screen logs), NVIM
# needs: python3 + pyte, nvim
set -uo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
BIN=${1:-$HERE/../dist/twinpane-linux-amd64}
NVIM=${NVIM:-$(command -v nvim)}
HARNESS="$HERE/ptyterm.py"
WORK=$(mktemp -d "${TMPDIR:-/tmp}/tp-phase7-check.XXXXXX")
H="$WORK/home"
trap 'chmod -R u+rwx "$WORK" 2>/dev/null; rm -rf "$WORK"' EXIT
LOGDIR=${LOGDIR:-$WORK}; mkdir -p "$LOGDIR"

# a mock "editor" with a name that is not Neovim: it records its arguments
FAKE="$WORK/bin/md-handler"
HLOG="$WORK/handler.log"
mkdir -p "$WORK/bin"
cat >"$FAKE" <<EOF
#!/bin/sh
echo "\$@" >> "$HLOG"
echo "MOCK HANDLER OPENED \$*"
sleep 1
EOF
chmod +x "$FAKE"

setup() {
	chmod -R u+rwx "$H" 2>/dev/null; rm -rf "$H"
	mkdir -p "$H/.xdg"
	printf '# Phase seven notes\n\nsome text\n' > "$H/notes.md"
	echo "plain hello" > "$H/plain.txt"
	printf '\177ELF\000\000\001\000\377\000' > "$H/blob.bin"
	rm -f "$HLOG"
}

pass=0 fail=0
ok()  { echo "PASS  $1"; pass=$((pass+1)); }
bad() { echo "FAIL  $1"; fail=$((fail+1)); [ -n "${2:-}" ] && echo "$2" | tail -40 | sed 's/^/      /'; }
run() { # name, editor, cols, rows, steps...
	local name=$1 ed=$2 cols=$3 rows=$4; shift 4
	local out
	if out=$(python3 "$HARNESS" --cols "$cols" --rows "$rows" --log "$LOGDIR/$name.log" \
		--env HOME="$H" --env TERM=xterm-256color --env PATH="$PATH" --env LANG=C.UTF-8 \
		--env EDITOR="$ed" --env VISUAL= --env XDG_CONFIG_HOME="$H/.xdg" --env XDG_DATA_HOME="$H/.local/share" -- \
		"type:$BIN\\r" wait:1.5 expect:'TwinPane' "$@" \
		'type:q' wait:1 expect:'SHELL\$' 2>&1); then ok "$name"
	else bad "$name" "$out"; fi
}
fscheck() { if eval "$2"; then ok "$1"; else bad "$1" "$(ls -la "$H" "$WORK" 2>&1; cat "$HLOG" 2>&1)"; fi; }

# HOME listing (hidden off): ..  blob.bin  notes.md  plain.txt
setup

# 1. Markdown + real Neovim: the card waits through 11 s of idle time,
#    swipes and unrelated keys; Esc cancels; a later double tap + Open
#    starts Neovim exactly once.
run md-neovim-wait-esc-open "$NVIM" 100 30 \
	dclick:'notes.md' wait:0.5 expect:'Open notes\.md in Neovim\?' expect:'Open \(Enter\)' expect:'Cancel \(Esc\)' \
	reject:'automatically' wait:11 expect:'Open notes\.md in Neovim\?' reject:'Phase seven notes' \
	wheel:3:'TwinPane' wheel:-3:'TwinPane' type:j type:x wait:1 \
	expect:'Open notes\.md in Neovim\?' reject:'Phase seven notes' \
	'type:\e' wait:0.5 expect:'editing canceled' reject:'Open notes' wait:3 reject:'Phase seven notes' \
	dclick:'notes.md' wait:1 expect:'Open notes\.md in Neovim\?' \
	click:'Open (Enter)' wait:2 expect:'Phase seven notes' \
	'type:\e:q\r' wait:1.5 expect:'notes\.md' reject:'Open notes' wait:3 reject:'Phase seven notes'

# 2. plain text, real Neovim: Cancel starts nothing; Enter on the card opens.
run txt-neovim-cancel-enter "$NVIM" 100 30 \
	dclick:'plain.txt' wait:1 expect:'Open plain\.txt in Neovim\?' \
	click:'Cancel (Esc)' wait:0.5 expect:'editing canceled' wait:3 reject:'plain hello' \
	dclick:'plain.txt' wait:1 expect:'Open plain\.txt in Neovim\?' 'type:\r' wait:2 expect:'plain hello' \
	'type:\e:q\r' wait:1.5 reject:'plain hello'

# 3. a mock handler that is not Neovim: same card with its own name; it is
#    not run while the card waits (11 s idle, then Esc); Open runs it once.
run mock-handler-idle "$FAKE" 100 30 \
	dclick:'notes.md' wait:1 expect:'Open notes\.md in md-handler\?' wait:11 \
	expect:'Open notes\.md in md-handler\?' wheel:2:'TwinPane' type:k wait:0.5 \
	expect:'Open notes\.md in md-handler\?' 'type:\e' wait:0.5 expect:'editing canceled'
fscheck "mock handler not run without a decision" '[ ! -e "$HLOG" ]'
run mock-handler-open "$FAKE" 100 30 \
	dclick:'notes.md' wait:1 expect:'Open notes\.md in md-handler\?' \
	click:'Open (Enter)' wait:2.5 expect:'notes\.md' reject:'Open notes'
fscheck "mock handler ran exactly once, with notes.md" '[ "$(wc -l < "$HLOG")" -eq 1 ] && grep -q "notes.md" "$HLOG"'

# 4. binary file: the binary warning is unchanged and has no timer either.
run binary-unchanged "$NVIM" 100 30 \
	dclick:'blob.bin' wait:1 expect:'looks like a binary file' expect:'Open anyway \(o\)' wait:4 \
	expect:'looks like a binary file' 'type:\e' wait:0.5 reject:'binary file'

# 5. help: frame intact at the top, after scrolling and at the end; the
#    filter narrows the list, Esc clears it, the next Esc closes the help.
run help-frame-filter "$NVIM" 80 24 \
	'type:?' wait:0.5 expect:'╭─ TwinPane — keyboard controls' expect:'/ filter ─╮' expect:'╰─.* 1–19/[0-9]+ ───╯' \
	type:j type:j type:j wait:0.3 expect:'╭─ TwinPane — keyboard controls' expect:'╰─.* 4–22/' \
	type:G wait:0.3 expect:'╭─ TwinPane — keyboard controls' expect:'dialogs: cancel' expect:'───╯' \
	type:/ wait:0.2 type:QUIT wait:0.3 expect:'Filter: QUIT' expect:'q +quit' reject:'copy to other panel' \
	expect:'filtering ─╮' \
	type:zz wait:0.3 expect:'No shortcuts match' \
	'type:\e' wait:0.3 reject:'Filter:' expect:'keyboard controls' expect:'copy to other panel' \
	'type:\e' wait:0.3 reject:'keyboard controls' expect:'notes\.md'

# 6. help on a minimum-size terminal (30x7 and 30x10): all four borders.
run help-tiny-30x7 "$NVIM" 30 7 \
	'type:?' wait:0.5 expect:'^╭─ TwinPane help' expect:'─╮$' expect:'^╰─' expect:'─╯$' \
	type:G wait:0.3 expect:'^╭─' expect:'─╯$' 'type:\e' wait:0.3
run help-small-30x10-filter "$NVIM" 30 10 \
	'type:?' wait:0.5 type:/ type:quit wait:0.3 expect:'Filter: quit' expect:'^╭─' expect:'─╯$' \
	'type:\e' wait:0.3 'type:\e' wait:0.3

echo "phase7_check: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
