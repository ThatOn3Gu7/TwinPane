#!/usr/bin/env bash
# phase4_check.sh - end-to-end checks of the v0.4.0 features on a raw
# pseudo-terminal (scripts/ptyterm.py): mouse/touch input sent exactly as
# Termux sends it (SGR press+release for a tap, wheel events for a swipe),
# the context hint bar, the shared hidden-files toggle, clickable dialog
# buttons, the bulk-rename guidance and the editor save/quit card with a
# real Neovim. This exercises the real binary; it is not a test on a phone.
#
# usage: scripts/phase4_check.sh [BIN]      env: LOGDIR (keep screen logs), NVIM
# needs: python3 + pyte, nvim
set -uo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
BIN=${1:-$HERE/../dist/twinpane-linux-amd64}
NVIM=${NVIM:-$(command -v nvim)}
HARNESS="$HERE/ptyterm.py"
WORK=$(mktemp -d "${TMPDIR:-/tmp}/tp-phase4-check.XXXXXX")
H="$WORK/home"
trap 'chmod -R u+rwx "$WORK" 2>/dev/null; rm -rf "$WORK"' EXIT
LOGDIR=${LOGDIR:-$WORK}; mkdir -p "$LOGDIR"

setup() {
	chmod -R u+rwx "$H" 2>/dev/null; rm -rf "$H"
	mkdir -p "$H/many" "$H/ren" "$H/.xdg"
	for i in $(seq -w 1 80); do echo "$i" > "$H/many/m0$i.txt"; done
	echo one > "$H/ren/x1.txt"; echo two > "$H/ren/x2.txt"
	echo hello > "$H/notes.txt"
	echo junk > "$H/junk.txt"
	echo dot > "$H/.hidden-a"
}

pass=0 fail=0
ok()  { echo "PASS  $1"; pass=$((pass+1)); }
bad() { echo "FAIL  $1"; fail=$((fail+1)); [ -n "${2:-}" ] && echo "$2" | tail -40 | sed 's/^/      /'; }
run() { # name, extra-args, steps...
	local name=$1 args=$2; shift 2
	local out
	if out=$(python3 "$HARNESS" --cols 100 --rows 30 --log "$LOGDIR/$name.log" \
		--env HOME="$H" --env TERM=xterm-256color --env PATH="$PATH" --env LANG=C.UTF-8 \
		--env EDITOR="$NVIM" --env VISUAL= --env XDG_CONFIG_HOME="$H/.xdg" --env XDG_DATA_HOME="$H/.local/share" -- \
		"type:$BIN $args\\r" wait:1.5 expect:'TwinPane' "$@" \
		'type:q' wait:1 expect:'SHELL\$' 2>&1); then ok "$name"
	else bad "$name" "$out"; fi
}
fscheck() { if eval "$2"; then ok "$1"; else bad "$1" "$(ls -la "$H" "$H/ren" 2>&1)"; fi; }

# HOME listing (hidden off): ..  many/  ren/  junk.txt  notes.txt
setup

# 1. mouse reporting is on; a tap focuses the tapped panel and moves the
#    cursor there (the status line shows the entry under the cursor) but
#    selects nothing; the hint bar follows the cursor.
run tap-focus "" \
	mode:1002 mode:1006 \
	'type:\t' wait:0.3 expect:'active: right panel' \
	click:'notes.txt' wait:0.3 expect:'active: left panel' expect:'^ \S*/notes\.txt *$' \
	expect:'2 dirs +2 files' reject:'selected' \
	expect:'c +copy +m +move' \
	click:'..' wait:0.3 reject:'c +copy +m +move'

# 2. double tap opens a folder; swipes (wheel events) scroll the list under
#    the finger; the scroll indicator is drawn; a tap on the track jumps.
run doubletap-wheel-scrollbar "" \
	dclick:'many/' wait:0.6 expect:'m001\.txt' expect:'┃' \
	wheel:10:m003.txt wait:0.3 expect:'m011\.txt' reject:'m001\.txt' \
	wheel:-10:m013.txt wait:0.3 expect:'m001\.txt' \
	clickat:50,24 wait:0.3 expect:'m080\.txt' reject:'m001\.txt'

# 3. hint bar taps act as keys (? help); a tap closes help; H toggles hidden
#    files in BOTH panels at once (same row in both panels).
run hints-help-hidden "" \
	click:'help' wait:0.3 expect:'keyboard controls' expect:'close help' \
	click:'Navigation' wait:0.3 reject:'keyboard controls' \
	type:H wait:0.6 expect:'hidden: shown' expect:'\.hidden-a.*\.hidden-a' expect:'showing hidden files in both panels' \
	type:. wait:0.6 expect:'hidden: off' reject:'\.hidden-a'

# 4. dialog buttons are tappable: No keeps the file, Yes moves it to trash.
run dialog-tap "" \
	click:'junk.txt' wait:0.2 type:d wait:0.3 expect:'Move 1 file to the trash\?' \
	expect:'⏎/n +no, cancel +y +yes, move to trash +Esc +cancel' \
	wait:0.6 click:'No, cancel' wait:0.3 reject:'to the trash\?' expect:'junk\.txt' \
	type:d wait:0.3 wait:0.6 click:'Yes, move to trash' wait:1 reject:'junk\.txt'
fscheck "dialog tap: junk.txt trashed, notes.txt kept" '[ ! -e "$H/junk.txt" ] && [ -f "$H/notes.txt" ]'

# 5. editor card with a real Neovim: the keys are shown before it starts
#    and the card waits for Enter (Phase 7: no automatic start); after
#    saving the status says so and mouse reporting is back on; a double tap
#    on a file always asks first and opens only on a tap on Open; quitting
#    without saving gives the save hint and the card returns next time
#    (Esc cancels it).
run editor-card "" \
	click:'notes.txt' wait:0.2 type:e wait:0.4 \
	expect:'Open notes\.txt in Neovim\?' expect:'Esc, then :w save' expect:':wq save & quit' \
	wait:3.2 expect:'Open notes\.txt in Neovim\?' 'type:\r' wait:1.5 reject:'Open notes' expect:'hello' \
	'type:ihi \e' wait:0.3 'type::wq\r' wait:1.5 \
	expect:'Finished editing notes\.txt — saved' mode:1002 mode:1006 \
	dclick:'notes.txt' wait:3 expect:'Open notes\.txt in Neovim\?' reject:'hi hello' \
	click:'Open (Enter)' wait:1.5 reject:'Open notes' expect:'hi hello' \
	'type::q\r' wait:1.5 expect:'nothing saved \(unchanged\)\. To save in Neovim: Esc, then :w' \
	type:e wait:0.4 expect:'Open notes\.txt in Neovim\?' 'type:\e' wait:0.3 expect:'editing canceled'
fscheck "editor card: notes.txt saved by Neovim" 'grep -qx "hi hello" "$H/notes.txt"'

# 6. bulk rename: the commit guidance names the keys; the confirmation's
#    Rename button can be tapped.
run bulk-guidance "" \
	dclick:'ren/' wait:0.6 expect:'x1\.txt' type:a wait:0.2 type:r wait:0.4 \
	expect:'No new names yet' type:U wait:0.3 \
	expect:'Apply: +a +then +y +to confirm' expect:'Cancel: +Esc +— nothing is renamed until you confirm' \
	expect:'a +apply… +Esc +cancel' \
	type:a wait:0.3 expect:'y = rename now' wait:0.6 click:'Rename (y)' wait:1 expect:'X1\.TXT'
fscheck "bulk: renamed by tapping Rename" '[ -f "$H/ren/X1.TXT" ] && [ -f "$H/ren/X2.TXT" ]'

# 7. --no-mouse / TWINPANE_NO_MOUSE=1 leave mouse reporting off.
run no-mouse-flag "--no-mouse" nomode:1002 nomode:1006

python3 "$HARNESS" --cols 100 --rows 30 --env HOME="$H" --env TWINPANE_NO_MOUSE=1 --env PATH="$PATH" -- \
	"type:$BIN\\r" wait:1.5 expect:'TwinPane' nomode:1002 'type:q' wait:1 expect:'SHELL\$' >/dev/null 2>&1 \
	&& ok "no-mouse-env (TWINPANE_NO_MOUSE=1)" || bad "no-mouse-env (TWINPANE_NO_MOUSE=1)"

echo "phase4_check: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
