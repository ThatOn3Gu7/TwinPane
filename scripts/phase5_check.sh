#!/usr/bin/env bash
# phase5_check.sh - end-to-end checks of the v0.5.0 features on a raw
# pseudo-terminal (scripts/ptyterm.py): padded hint chips, the read-only
# viewer, browsing / extracting / creating ZIP archives, the Properties
# dialog (permissions, rename, tappable buttons) and the refusals inside an
# archive. This exercises the real binary; it is not a test on a phone.
#
# usage: scripts/phase5_check.sh [BIN]      env: LOGDIR (keep screen logs)
# needs: python3 + pyte
set -uo pipefail
HERE=$(cd "$(dirname "$0")" && pwd)
BIN=${1:-$HERE/../dist/twinpane-linux-amd64}
HARNESS="$HERE/ptyterm.py"
WORK=$(mktemp -d "${TMPDIR:-/tmp}/tp-phase5-check.XXXXXX")
H="$WORK/home"
trap 'chmod -R u+rwx "$WORK" 2>/dev/null; rm -rf "$WORK"' EXIT
LOGDIR=${LOGDIR:-$WORK}; mkdir -p "$LOGDIR"

setup() {
	chmod -R u+rwx "$H" 2>/dev/null; rm -rf "$H"
	mkdir -p "$H/src/inner" "$H/.xdg"
	echo alpha > "$H/src/a.txt"; echo beta > "$H/src/inner/b.txt"
	for i in $(seq 1 120); do echo "notes line $i"; done > "$H/notes.txt"
	printf 'BIN\000\001\002\033[2J\377\376' > "$H/data.bin"
	echo perm > "$H/perm.txt"; chmod 644 "$H/perm.txt"
	python3 - "$H" <<'EOF'
import sys, zipfile
h = sys.argv[1]
with zipfile.ZipFile(h + "/pack.zip", "w", zipfile.ZIP_DEFLATED) as z:
    z.writestr("readme.md", "# pack readme\n")
    z.writestr("docs/", "")
    z.writestr("docs/guide/intro.txt", "intro text\n")
    z.writestr("empty/", "")
with zipfile.ZipFile(h + "/evil.zip", "w") as z:
    z.writestr("ok.txt", "fine\n")
    z.writestr("../escape.txt", "bad\n")
    z.writestr("/abs.txt", "bad\n")
EOF
}

pass=0 fail=0
ok()  { echo "PASS  $1"; pass=$((pass+1)); }
bad() { echo "FAIL  $1"; fail=$((fail+1)); [ -n "${2:-}" ] && echo "$2" | tail -40 | sed 's/^/      /'; }
run() { # name, steps...
	local name=$1; shift
	local out
	if out=$(python3 "$HARNESS" --cols 100 --rows 30 --log "$LOGDIR/$name.log" \
		--env HOME="$H" --env TERM=xterm-256color --env PATH="$PATH" --env LANG=C.UTF-8 \
		--env EDITOR=true --env VISUAL= --env XDG_CONFIG_HOME="$H/.xdg" -- \
		"type:$BIN\\r" wait:1.5 expect:'TwinPane' "$@" \
		'type:q' wait:1 expect:'SHELL\$' 2>&1); then ok "$name"
	else bad "$name" "$out"; fi
}
fscheck() { if eval "$2"; then ok "$1"; else bad "$1" "$(ls -laR "$H" 2>&1 | head -60)"; fi; }

# HOME listing: ..  src/  data.bin  evil.zip  notes.txt  pack.zip  perm.txt
setup

# 1. hint chips: two cells of padding on each side of every key label.
run hint-padding \
	expect:'^  Tab   switch   ⏎   open ' expect:'  \?   help *$' \
	click:'notes.txt' wait:0.3 expect:'  Space   select   c   copy   m   move   r   rename   d   delete '

# 2. viewer: header with VIEW / READ-ONLY badges, line numbers, end of file,
#    search with match count, unwrap, Esc back to the same cursor.
run viewer \
	click:'notes.txt' wait:0.3 type:v wait:0.6 \
	expect:'VIEW +notes\.txt +READ-ONLY' expect:'^ +1 notes line 1\b' expect:'120 lines' \
	expect:'e edits in your external editor' expect:'Esc +close' \
	type:G wait:0.3 expect:'^120 notes line 120' expect:'of 120 \(100%\)' \
	type:/ wait:0.3 expect:'Find text:' 'type:line 77\r' wait:0.3 \
	expect:'"line 77": line 77 — match 1 of 1' expect:'^ 77 notes line 77' \
	type:w wait:0.3 expect:'line wrapping off' \
	'type:\e' wait:0.3 reject:'READ-ONLY' expect:'^ \S*/notes\.txt *$'

# 3. binary file: detected and not dumped to the terminal.
run viewer-binary \
	click:'data.bin' wait:0.3 type:v wait:0.6 \
	expect:'looks like a binary file' reject:'BIN' 'type:\e' wait:0.3

# 4. archive browsing: Enter opens the ZIP in the panel (badge, status),
#    nested folders, empty folder, Backspace up, Esc leaves; nothing extracted.
run archive-browse \
	click:'pack.zip' wait:0.3 'type:\r' wait:0.6 \
	expect:'ZIP' expect:'browsing pack\.zip \(read-only\)' expect:'docs/' expect:'readme\.md' \
	expect:'Esc +leave ZIP' reject:'d +delete' \
	dclick:'docs/' wait:0.6 expect:'guide/' dclick:'guide/' wait:0.6 expect:'intro\.txt' \
	'type:\x7f' wait:0.4 'type:\x7f' wait:0.4 dclick:'empty/' wait:0.6 expect:'empty folder in the archive' \
	'type:\e' wait:0.6 reject:'browsing' expect:'pack\.zip' \
	click:'pack.zip' wait:0.3 'type:\r' wait:0.6 click:'readme.md' wait:0.3 'type:\r' wait:0.6 \
	expect:'# pack readme' expect:'read-only archive entry' 'type:\e' wait:0.3 \
	type:d wait:0.3 expect:'not possible inside a ZIP archive' \
	type:r wait:0.3 expect:'Renaming is not possible' 'type:\e' wait:0.5
fscheck "archive browse: nothing extracted" '[ ! -e "$H/readme.md" ] && [ ! -e "$H/docs" ]'

# 5. extract the selected entry (into the other panel's folder = HOME),
#    then the whole archive from the folder view (into HOME/pack).
run archive-extract \
	click:'pack.zip' wait:0.3 'type:\r' wait:0.6 click:'readme.md' wait:0.3 \
	type:x wait:0.4 expect:'Extract into folder:' 'type:\r' wait:1 expect:'Extracted 1 file' \
	'type:\e' wait:0.6 click:'pack.zip' wait:0.3 type:x wait:0.4 \
	expect:'Extract the ZIP archive "pack.zip"' 'type:\r' wait:1 expect:'Extracted 2 files'
fscheck "extract: readme.md and pack/docs/guide/intro.txt" \
	'grep -q "pack readme" "$H/readme.md" && grep -q "intro text" "$H/pack/docs/guide/intro.txt" && [ -d "$H/pack/empty" ]'

# 6. unsafe paths are refused.
run archive-traversal \
	click:'evil.zip' wait:0.3 type:x wait:0.4 'type:\r' wait:1 expect:'unsafe archive paths refused'
fscheck "traversal: only ok.txt written" \
	'[ -f "$H/evil/ok.txt" ] && [ ! -e "$WORK/escape.txt" ] && [ ! -e "$H/escape.txt" ] && [ ! -e /abs.txt ]'

# 7. create a ZIP of a folder; an existing file is only replaced after the
#    confirmation.
run zip-create \
	click:'src/' wait:0.3 type:z wait:0.4 expect:'Archive file:' 'type:\r' wait:1 \
	expect:'Created src\.zip \(2 files, 2 folders' \
	click:'src/' wait:0.3 type:z wait:0.4 'type:\r' wait:0.4 expect:'"src\.zip" already exists' \
	'type:\r' wait:0.4 expect:'ZIP not created — nothing was changed'
fscheck "zip: src.zip contains src/a.txt and src/inner/b.txt" \
	'python3 -c "import zipfile,sys; n=zipfile.ZipFile(sys.argv[1]).namelist(); sys.exit(0 if \"src/a.txt\" in n and \"src/inner/b.txt\" in n else 1)" "$H/src.zip"'

# 8. Properties: metadata, permission grid, Apply; rename; tap Close.
run properties \
	click:'perm.txt' wait:0.3 type:P wait:0.4 \
	expect:'Properties' expect:'regular file' expect:'rw-r--r-- +0644' expect:'owner +\[x\]' \
	'type:\t' wait:0.1 'type:\x1b[C' wait:0.1 'type:\x1b[C' wait:0.1 'type: ' wait:0.2 \
	expect:'rwxr--r-- +0744 +\(now 0644\)' expect:'Changes not applied yet' \
	'type:\r' wait:0.4 expect:'Permissions set to rwxr--r-- \(0744\)' \
	'type:\t' wait:0.1 'type:\t' wait:0.1 'type:\t' wait:0.1 \
	'type:\e' wait:0.3 reject:'Properties' \
	type:P wait:0.4 'type:\x15' 'type:renamed' wait:0.2 'type:\r' wait:0.5 \
	expect:'Renamed to "renamed\.txt"' wait:0.5 click:'Close Esc' wait:0.4 reject:'Properties' \
	expect:'renamed\.txt'
fscheck "properties: mode 744 and renamed" \
	'[ "$(stat -c %a "$H/renamed.txt")" = 744 ] && [ ! -e "$H/perm.txt" ]'

echo "phase5_check: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
