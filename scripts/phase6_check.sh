#!/usr/bin/env bash
# phase6_check.sh - end-to-end checks of the v0.6.0 features on a raw
# pseudo-terminal (scripts/ptyterm.py): the compression dialog (format and
# level selectors, TAR.GZ / 7Z / GZIP, unavailable formats), bookmarks,
# the action menu (keyboard and title tap), Open / Open with (a recording
# stand-in for termux-open: no app is started), "Run a program…" and the
# storage overview. This exercises the real binary; it is not a test on a
# phone, and the termux-open used here is a fake that records its
# arguments.
#
# usage: scripts/phase6_check.sh [BIN]      env: LOGDIR (keep screen logs)
# needs: python3 + pyte; tar, gzip; 7z for the 7Z step (skipped otherwise)
set -uo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
BIN=${1:-$HERE/../dist/twinpane-linux-amd64}
HARNESS="$HERE/ptyterm.py"
WORK=$(mktemp -d "${TMPDIR:-/tmp}/tp-phase6-check.XXXXXX")
H="$WORK/home"
trap 'chmod -R u+rwx "$WORK" 2>/dev/null; rm -rf "$WORK"' EXIT
LOGDIR=${LOGDIR:-$WORK}; mkdir -p "$LOGDIR"
PIC="pic 'q' \"d\" \$(touch PWNED);ü.jpg"

setup() {
	chmod -R u+rwx "$H" 2>/dev/null; rm -rf "$H"
	mkdir -p "$H/src/inner" "$H/.xdg" "$WORK/fakebin" "$WORK/nobin"
	echo alpha > "$H/src/a.txt"; echo beta > "$H/src/inner/b.txt"
	for i in $(seq 1 50); do echo "notes line $i"; done > "$H/notes.txt"
	printf '\377\330\377\340fake jpeg' > "$H/$PIC"
	# termux-open stand-in: records its arguments (NUL-separated)
	cat > "$WORK/fakebin/termux-open" <<EOF
#!/bin/sh
for a in "\$@"; do printf '%s\\0' "\$a"; done >> "$WORK/opened"
EOF
	chmod +x "$WORK/fakebin/termux-open"
	# a PATH with the harness's bash (and sh/python3 for wrapper scripts,
	# e.g. the seccomp filter) but no 7-Zip
	for t in bash sh python3; do ln -sf "$(command -v $t)" "$WORK/nobin/$t"; done
	rm -f "$WORK/opened"
}

pass=0 fail=0 skip=0
ok()  { echo "PASS  $1"; pass=$((pass+1)); }
bad() { echo "FAIL  $1"; fail=$((fail+1)); [ -n "${2:-}" ] && echo "$2" | tail -40 | sed 's/^/      /'; }
EXTRA=()
run() { # name, steps...   (extra --env arguments in EXTRA)
	local name=$1; shift
	local out
	if out=$(python3 "$HARNESS" --cols 100 --rows 30 --log "$LOGDIR/$name.log" \
		--env HOME="$H" --env TERM=xterm-256color --env PATH="$PATH" --env LANG=C.UTF-8 \
		--env EDITOR=true --env VISUAL= --env XDG_CONFIG_HOME="$H/.xdg" --env DISPLAY= --env WAYLAND_DISPLAY= \
		"${EXTRA[@]}" -- \
		"type:$BIN\\r" wait:1.5 expect:'TwinPane' "$@" \
		'type:q' wait:1 expect:'SHELL\$' 2>&1); then ok "$name"
	else bad "$name" "$out"; fi
}
fscheck() { if eval "$2"; then ok "$1"; else bad "$1" "$(ls -laR "$H" 2>&1 | head -60)"; fi; }
DOWN='type:\x1b[B'
PGDN='type:\x1b[6~'

# HOME listing: ..  src/  notes.txt  pic…jpg
setup

# 1. compression dialog: the format selector changes format and extension
#    (ZIP → 7Z → TAR → TAR.GZ), the level selector works, TAR.GZ created.
run compress-targz \
	click:'src/' wait:0.3 type:z wait:0.5 expect:'Format +◂ ZIP \(\.zip\) ▸' expect:'Level +◂ normal ▸' \
	expect:'/src\.zip *│' expect:'↑↓ +format +PgUp/PgDn +level' \
	"$DOWN" wait:0.2 "$DOWN" wait:0.2 "$DOWN" wait:0.3 expect:'TAR\.GZ \(\.tar\.gz\)' expect:'/src\.tar\.gz *│' \
	"$PGDN" wait:0.2 expect:'Level +◂ (best|maximum|high)' \
	'type:\r' wait:1.5 expect:'Created src\.tar\.gz'
fscheck "tar.gz: src/a.txt and src/inner/b.txt" \
	'tar tzf "$H/src.tar.gz" | grep -qx "src/a.txt" && tar tzf "$H/src.tar.gz" | grep -qx "src/inner/b.txt"'

# 2. 7Z through the installed 7-Zip (skipped without it).
if command -v 7z >/dev/null 2>&1 || command -v 7zz >/dev/null 2>&1; then
	run compress-7z \
		click:'src/' wait:0.3 type:z wait:0.5 \
		"$DOWN" wait:0.3 expect:'7Z \(\.7z\)' reject:'unavailable' 'type:\r' wait:3 expect:'Created src\.7z'
	SZ=$(command -v 7z || command -v 7zz)
	fscheck "7z: src/inner/b.txt listed" '"$SZ" l "$H/src.7z" | grep -q "src/inner/b.txt"'
else
	echo "SKIP  compress-7z (no 7z installed)"; skip=$((skip+1))
fi

# 3. without 7-Zip on PATH: 7Z shown as unavailable with the reason and
#    refused; nothing created.
EXTRA=(--env PATH="$WORK/nobin")
run compress-unavailable \
	click:'notes.txt' wait:0.3 type:z wait:0.5 "$DOWN" wait:0.3 \
	expect:'7Z \(\.7z\) — unavailable' expect:'needs 7-Zip' expect:'install 7-Zip or p7zip' \
	'type:\r' wait:0.5 expect:'7Z is not available' 'type:\e' wait:0.3
EXTRA=(--env PATH="$WORK/nobin" --env TERMUX_VERSION=0.118.0)
run compress-unavailable-termux \
	click:'notes.txt' wait:0.3 type:z wait:0.5 "$DOWN" wait:0.3 \
	expect:'7Z \(\.7z\) — unavailable' expect:'not found — Termux:' expect:'pkg install 7zip' 'type:\e' wait:0.3
EXTRA=()
fscheck "unavailable: no notes.7z" '[ ! -e "$H/notes.7z" ]'

# 4. a single-file format keeps the file name (notes.txt.gz).
G=(); for i in $(seq 1 9); do G+=("$DOWN" wait:0.15); done
run compress-gzip \
	click:'notes.txt' wait:0.3 type:z wait:0.5 "${G[@]}" wait:0.2 \
	expect:'GZIP \(\.gz\)' expect:'Compressed file:' expect:'/notes\.txt\.gz *│' \
	'type:\r' wait:1.5 expect:'Created notes\.txt\.gz'
fscheck "gzip: content round-trips" 'gzip -dc "$H/notes.txt.gz" | cmp -s - "$H/notes.txt"'

# 5. bookmarks: add HOME, go into src, jump back with the list, rename,
#    remove (with confirmation).
run bookmarks \
	type:B wait:0.4 expect:'bookmarked .* — b opens the bookmarks' \
	type:B wait:0.4 expect:'already bookmarked' \
	dclick:'src/' wait:0.6 expect:'inner/' \
	type:b wait:0.5 expect:'Bookmarks' expect:'1  home' expect:'a +add this folder' \
	'type:\r' wait:0.6 expect:'notes\.txt' \
	type:b wait:0.5 type:r wait:0.3 'type:\x15' 'type:My home' 'type:\r' wait:0.4 expect:'1  My home' \
	type:d wait:0.3 expect:'Remove the bookmark "My home"\?' expect:'folder itself is not touched' \
	type:y wait:0.4 expect:'No bookmarks yet' 'type:\e' wait:0.3
fscheck "bookmarks: file written and emptied" \
	'[ -f "$H/.xdg/twinpane/bookmarks.json" ] && ! grep -q "My home" "$H/.xdg/twinpane/bookmarks.json"'

# 6. action menu: keyboard (letters run entries, Delete last) and a tap on
#    the TwinPane badge.
run menu \
	click:'notes.txt' wait:0.3 type:M wait:0.4 expect:'o  Open \(default app\)' expect:'O  Open with…' \
	expect:'c  Copy to other panel' expect:'d  Delete…' expect:'a-z +pick' \
	type:P wait:0.5 expect:'Properties' expect:'regular file' 'type:\e' wait:0.3 \
	click:'TwinPane' wait:0.4 expect:'v  View' type:v wait:0.5 expect:'VIEW +notes\.txt' 'type:\e' wait:0.3

# 7. Open in Termux (stand-in termux-open): Enter on an image hands it
#    over with the name as one argument; Share from the menu uses --send.
EXTRA=(--env PATH="$WORK/fakebin:$PATH" --env TERMUX_VERSION=0.118.0 --env PREFIX=/data/data/com.termux/files/usr)
run open-termux \
	click:'pic ' wait:0.3 'type:\r' wait:1 expect:'handed pic .* to Android \(termux-open\)' \
	type:M wait:0.4 expect:'s  Share…' type:s wait:1 expect:'sent pic .* share sheet' \
	type:O wait:0.4 expect:'Android: choose app…' expect:'Type: image \(image/jpeg\)' 'type:\e' wait:0.3
EXTRA=()
fscheck "open: argv intact, no shell (no PWNED file)" \
	'python3 - "$WORK/opened" "$H/$PIC" <<EOF
import sys
a = open(sys.argv[1], "rb").read().decode().split("\0")[:-1]
p = sys.argv[2]
sys.exit(0 if a == ["--", p, "--send", "--", p] else 1)
EOF
[ ! -e "$H/PWNED" ] && [ ! -e "$WORK/PWNED" ]'

# 8. no opener (desktop without a display): o explains, Enter views.
run open-none \
	click:'pic ' wait:0.3 type:o wait:0.4 expect:'cannot open .*no graphical session' \
	type:O wait:0.4 expect:'unavailable' expect:'Built-in viewer' 'type:\e' wait:0.3

# 9. Run a program… (no shell): wc -l on notes.txt; TwinPane comes back.
run run-with \
	click:'notes.txt' wait:0.3 type:O wait:0.4 click:'Run a program' wait:0.5 \
	expect:'Program and options:' 'type:wc -l\r' wait:1.5 expect:'wc finished \(notes\.txt\)'

# 10. storage overview of HOME's filesystem.
run storage \
	type:i wait:0.5 expect:'Storage — ' expect:'Folder +~' expect:'Total +[0-9.]+ [KMGT]' \
	expect:'Used +[0-9.]+ [KMGT] \([0-9]+%\)' expect:'[0-9]+% used' 'type:\r' wait:0.3 reject:'Storage — '

# 11. phase-5 regression: z still makes a ZIP by default.
run zip-default \
	click:'src/' wait:0.3 type:z wait:0.5 expect:'Archive file:' 'type:\r' wait:1.2 \
	expect:'Created src\.zip \(2 files, 2 folders'
fscheck "zip: src.zip contains src/inner/b.txt" \
	'python3 -c "import zipfile,sys; sys.exit(0 if \"src/inner/b.txt\" in zipfile.ZipFile(sys.argv[1]).namelist() else 1)" "$H/src.zip"'

echo "phase6_check: $pass passed, $fail failed, $skip skipped"
[ "$fail" -eq 0 ]
