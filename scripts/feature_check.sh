#!/usr/bin/env bash
# feature_check.sh - interactive end-to-end checks of the Phase-3 features
# and the v0.3.1 feedback round (live search, bulk rename, targeting) on a raw pseudo-terminal driven
# by scripts/ptyterm.py, i.e. the real binary with real keystrokes.
#
# usage: scripts/feature_check.sh [BIN]      env: LOGDIR (keep screen logs)
# needs: python3 + pyte (pip install pyte)
set -uo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
BIN=${1:-$HERE/../dist/twinpane-linux-amd64}
HARNESS="$HERE/ptyterm.py"
WORK=$(mktemp -d "${TMPDIR:-/tmp}/tp-feature-check.XXXXXX")
H="$WORK/home"
trap 'chmod -R u+rwx "$WORK" 2>/dev/null; rm -rf "$WORK"' EXIT
LOGDIR=${LOGDIR:-$WORK}; mkdir -p "$LOGDIR"

setup() {
	chmod -R u+rwx "$H" 2>/dev/null; rm -rf "$H"
	mkdir -p "$H/proj/src/deep" "$H/proj/docs" "$H/locked/inner" "$H/photos" "$H/big"
	echo r > "$H/proj/src/deep/Report-Final.TXT"
	echo a > "$H/proj/src/deep/aaa.txt"
	echo n > "$H/proj/docs/notes.md"
	echo s > "$H/locked/inner/final-secret.txt"
	chmod 000 "$H/locked"
	for i in 1 2 3; do echo "$i" > "$H/photos/IMG_$i.jpg"; done
	echo B > "$H/photos/b.txt"; echo C > "$H/photos/c.txt"
	# a large tree so a search is still running when it is canceled
	python3 - "$H/big" <<-'EOF'
		import os, sys
		for d in range(1500):
		    for s in range(4):
		        p = os.path.join(sys.argv[1], "d%04d" % d, "s%d" % s, "x")
		        os.makedirs(p)
		        for f in range(40):
		            open(os.path.join(p, "f%03d.dat" % f), "w").close()
	EOF
}

pass=0 fail=0
ok()  { echo "PASS  $1"; pass=$((pass+1)); }
bad() { echo "FAIL  $1"; fail=$((fail+1)); [ -n "${2:-}" ] && echo "$2" | tail -40 | sed 's/^/      /'; }
run() { # name, steps...
	local name=$1; shift
	local out
	if out=$(python3 "$HARNESS" --cols 110 --rows 32 --log "$LOGDIR/$name.log" \
		--env HOME="$H" --env TERM=xterm-256color --env PATH="$PATH" --env LANG=C.UTF-8 --env EDITOR= --env VISUAL= -- \
		"type:$BIN\\r" wait:1.5 expect:'TwinPane' "$@" \
		'type:q' wait:1 expect:'SHELL\$' 2>&1); then ok "$name"
	else bad "$name" "$out"; fi
}
fscheck() { if eval "$2"; then ok "$1"; else bad "$1" "$(ls -la "$H/photos")"; fi; }

# HOME listing (hidden off, case-insensitive): ..  big  locked  photos  proj
setup

# 1. live search: results appear while typing (no Enter), partial and
#    case-insensitive; the unreadable folder is reported; Enter jumps to the
#    file's folder with the cursor on it (r shows the focused name). The
#    backend is whatever is installed (fd, find or built-in).
run search-jump \
	'type:/' wait:3 expect:'Find files by name' expect:'type part of a name' 'type:fINaL' wait:0.6 \
	expect:'1 match of' expect:'Report-Final.TXT' expect:'proj/src/deep' \
	expect:'1 folder unreadable' reject:'final-secret' \
	'type:\r' wait:0.8 expect:'deep' reject:'Find files by name' \
	'type:r' wait:0.5 expect:'Rename' expect:'Report-Final\.TXT' 'type:\e' wait:0.3

# 2. indexing a large tree (240k files, 9k folders) shows progress; typing
#    while it runs filters what is there so far; Esc while indexing closes
#    the box and stops the external tool (no find/fd process left).
run search-progress \
	'type:/' wait:0.15 expect:'so far, indexing' 'type:nomatch' wait:0.1 expect:'0 matches of [0-9]+ entries · indexing' \
	wait:4 expect:'0 matches of [0-9]+ entries' reject:'indexing' 'type:\e' wait:0.4 expect:'Name +Size'
run search-cancel \
	'type:/' wait:0.1 'type:\e' wait:0.6 expect:'Name +Size' reject:'Find files' \
	"sh:sleep 0.3; pgrep -af -- '([f]ind|[f]d) .*$H' > '$WORK/leftover' || true"
fscheck "search-cancel left no indexing process" '[ ! -s "$WORK/leftover" ] || { cat "$WORK/leftover"; false; }'

# 2b. a broad pattern on the same tree: every match is kept, best first
run search-broad \
	'type:/' wait:4 'type:dat' wait:1 expect:'[0-9]{6} matches of' expect:'big/d0000/s0/x/f000.dat' \
	'type:\e' wait:0.4 expect:'Name +Size'

# 3. no match, then Esc closes; open + Esc changes nothing
run search-none \
	'type:/' wait:3 'type:zzzq' wait:0.5 expect:'0 matches of' 'type:\e' wait:0.4 \
	expect:'Name +Size' 'type:/' wait:0.4 'type:\e' wait:0.4 expect:'Name +Size' reject:'Find files'

# 3b. edit a search result: $EDITOR is unset here, so the first detected
#     editor (Neovim) is used without asking; the box stays open afterwards.
#     (The first launch shows the save/quit card, which waits for Enter.)
run search-edit \
	'type:/' wait:3 'type:notes' wait:0.5 expect:'1 match of' expect:'proj/docs/notes.md' \
	'type:\t' wait:0.3 'type:e' wait:0.6 expect:'Open notes\.md in Neovim\?' 'type:\r' wait:2.5 expect:'notes.md' reject:'Find files by name' \
	'type:\e:q\r' wait:1.5 expect:'Find files by name' expect:'Finished editing notes.md' 'type:\e' wait:0.4

# 4. bulk rename with a pattern: select the 3 IMG files (Space marks and moves
#    down), r opens the overlay, n edits the pattern (pre-filled with
#    {name}{ext}; ctrl+u ctrl+k clear it), a + y apply.
#    photos listing: ..  b.txt  c.txt  IMG_1.jpg  IMG_2.jpg  IMG_3.jpg
run bulk-pattern \
	type:jjj wait:0.3 'type:\r' wait:0.6 type:j wait:0.1 type:j wait:0.1 type:j wait:0.1 \
	'type: ' wait:0.1 'type: ' wait:0.1 'type: ' wait:0.3 expect:'3 selected' \
	type:r wait:0.6 expect:'IMG_1.jpg' expect:'IMG_3.jpg' \
	type:n wait:0.5 expect:'\{name\}\{ext\}' 'type:\x15\x0bphoto-{n:2}{ext}\r' wait:0.6 expect:'photo-01.jpg' expect:'photo-03.jpg' \
	type:a wait:0.6 expect:'Rename 3 items\?' type:y wait:1.2 expect:'Renamed 3 items'
fscheck "bulk-pattern renamed on disk" \
	'[ "$(cat "$H/photos/photo-01.jpg" "$H/photos/photo-02.jpg" "$H/photos/photo-03.jpg" | tr -d "\n")" = 123 ] && ! ls "$H"/photos/IMG_* >/dev/null 2>&1'

# 5. swap b.txt <-> c.txt (a dependency cycle) by editing rows in the overlay.
#    photos listing: ..  b.txt  c.txt  photo-01.jpg ...
run bulk-swap \
	type:jjj wait:0.3 'type:\r' wait:0.6 type:j wait:0.1 'type: ' wait:0.1 'type: ' wait:0.3 expect:'2 selected' \
	type:r wait:0.6 'type:\r' wait:0.4 'type:\x15\x0bc.txt\r' wait:0.4 \
	type:j wait:0.2 'type:\r' wait:0.4 'type:\x15\x0bb.txt\r' wait:0.4 \
	type:a wait:0.6 expect:'Rename 2 items\?' type:y wait:1.2 expect:'Renamed 2 items'
fscheck "bulk-swap contents swapped on disk" \
	'[ "$(cat "$H/photos/b.txt")" = C ] && [ "$(cat "$H/photos/c.txt")" = B ] && [ "$(ls "$H/photos" | wc -l)" = 5 ]'

# 6. names already taken are refused and Esc cancels without renaming anything.
#    6a: b.txt + c.txt selected, b.txt -> photo-01.jpg (exists outside the batch)
#    6b: b.txt + photo-01.jpg selected, b.txt -> photo-01.jpg (kept by the batch)
#    photos listing: ..  b.txt  c.txt  photo-01.jpg  photo-02.jpg  photo-03.jpg
run bulk-exists-cancel \
	type:jjj wait:0.3 'type:\r' wait:0.6 type:j wait:0.1 'type: ' wait:0.1 'type: ' wait:0.3 expect:'2 selected' \
	type:r wait:0.6 'type:\r' wait:0.4 'type:\x15\x0bphoto-01.jpg\r' wait:0.5 \
	expect:'already exists' expect:'1 problem' type:a wait:0.5 expect:'must be fixed before renaming' \
	reject:'Rename 2 items\?' 'type:\e' wait:0.5 expect:'nothing was renamed'
run bulk-kept-cancel \
	type:jjj wait:0.3 'type:\r' wait:0.6 type:j wait:0.1 'type: ' wait:0.3 \
	type:j wait:0.1 'type: ' wait:0.3 expect:'2 selected' \
	type:r wait:0.6 'type:\r' wait:0.4 'type:\x15\x0bphoto-01.jpg\r' wait:0.5 \
	expect:'keeps this name' type:a wait:0.5 expect:'must be fixed before renaming' \
	reject:'Rename 2 items\?' 'type:\e' wait:0.5 expect:'nothing was renamed'
fscheck "bulk cancels left files untouched" \
	'[ "$(cat "$H/photos/b.txt")" = C ] && [ "$(cat "$H/photos/c.txt")" = B ] && [ "$(cat "$H/photos/photo-01.jpg")" = 1 ] && [ "$(ls "$H/photos" | wc -l)" = 5 ]'

# 7. targeting: with nothing selected r renames the focused item only
run rename-focused \
	type:jjj wait:0.3 'type:\r' wait:0.6 type:jj wait:0.3 type:r wait:0.5 \
	expect:'Rename' expect:'c\.txt' reject:'IMG' 'type:\x15\x0bc2.txt\r' wait:0.6 expect:'c2.txt'
fscheck "rename-focused renamed exactly one file" \
	'[ -f "$H/photos/c2.txt" ] && [ ! -e "$H/photos/c.txt" ] && [ "$(ls "$H/photos" | wc -l)" = 5 ]'

echo "feature checks: $pass passed, $fail failed"
[ "$fail" = 0 ]
