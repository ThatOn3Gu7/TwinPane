#!/usr/bin/env bash
# Phase 2 end-to-end test: runs the real binary in a tmux pseudo-terminal and
# exercises copy / move / rename / delete / edit by keystroke, checking both
# the rendered screen and the resulting filesystem state. Requires tmux.
#
# usage: scripts/tui_smoke_ops.sh [binary] [other-filesystem-dir]
#   other-filesystem-dir: a directory on a different filesystem than $TMPDIR
#   (default /var/tmp) used to test the "trash unavailable" permanent delete.
set -euo pipefail
BIN="$(realpath "${1:-./twinpane}")"
OTHERFS="${2:-/var/tmp}"
command -v tmux >/dev/null || { echo "tmux not installed"; exit 2; }

H="$(mktemp -d)"; OTHER="$(mktemp -d -p "$OTHERFS")"; TOOLS="$(mktemp -d)"
cleanup() {
  tmux kill-session -t tpops 2>/dev/null || true
  chmod -R u+rwx "$H" "$OTHER" 2>/dev/null || true
  rm -rf "$H" "$OTHER" "$TOOLS"
}
trap cleanup EXIT
tmux set-option -g escape-time 0 2>/dev/null || true

# ---- fixture ------------------------------------------------------------------
# HOME: L/ R/ zz-other -> $OTHER (different filesystem)
mkdir -p "$H"/L/dir1/deep "$H"/R "$H"/L/many
echo new-alpha > "$H/L/alpha.txt"
echo beta > "$H/L/beta.txt"
echo y > "$H/L/dir1/deep/y.txt"
echo old-alpha > "$H/R/alpha.txt"
printf 'BIN\0\1\2\3' > "$H/L/blob.bin"
python3 - "$H/L/many" <<'EOF'
import os, sys
d = sys.argv[1]
for i in range(30000):
    with open(os.path.join(d, f"f{i:05d}"), "w") as f:
        f.write("x" * 64)
EOF
mkdir -p "$OTHER/victim/sub"; echo 1 > "$OTHER/victim/a"; echo 2 > "$OTHER/victim/sub/b"
ln -s "$OTHER" "$H/zz-other"

cat > "$TOOLS/fake-editor" <<'EOF'
#!/bin/sh
printf 'FAKE EDITOR editing %s\n' "$1"
IFS= read -r line
printf '%s\n' "$line" >> "$1"
EOF
chmod +x "$TOOLS/fake-editor"

pass=0; fail=0
cap()   { sleep "${1:-0.5}"; tmux capture-pane -p -t tpops; }
check() { # check <description> <grep-pattern> [screen]
  local scr="${3:-$(cap)}"
  if grep -qE -- "$2" <<<"$scr"; then echo "PASS  $1"; pass=$((pass+1));
  else echo "FAIL  $1  (pattern: $2)"; echo "$scr"; fail=$((fail+1)); fi
}
fs()    { # fs <description> <shell test...>
  local d="$1"; shift
  if "$@"; then echo "PASS  $d"; pass=$((pass+1)); else echo "FAIL  $d"; fail=$((fail+1)); fi
}
keys()  { tmux send-keys -t tpops "$@"; }
lit()   { tmux send-keys -t tpops -l "$1"; }

start() { # start <extra env...>
  tmux kill-session -t tpops 2>/dev/null || true
  tmux new-session -d -s tpops -x 110 -y 32 -c "$H" "env HOME=$H XDG_DATA_HOME= TERM=xterm-256color $* $BIN"
  sleep 1
}

start EDITOR="$TOOLS/fake-editor" VISUAL=
# HOME sorted: .. L R zz-other ; L sorted: .. dir1 many alpha.txt beta.txt blob.bin
keys Down Enter; sleep 0.3          # left -> L
keys Tab Down Down Enter; sleep 0.3 # right -> R
keys Tab
S="$(cap)"
check "both panels set up (L | R)" "/L .*/R " "$S"
check "hint bar on .. offers no file operations" "Tab +switch.*\? +help" "$S"

# ---- copy with conflict -> overwrite ------------------------------------------------
keys Down Down Down                 # alpha.txt
S="$(cap)"
check "hint bar on a file lists file operation keys" "c +copy +m +move +r +rename +d +delete +e/E +edit.*\? +help" "$S"
keys c
check "copy confirmation names item" 'Copy file "alpha.txt"\?'
keys Enter
S="$(cap)"
check "conflict dialog shown" 'A file named "alpha.txt" already exists' "$S"
check "conflict offers overwrite/skip/apply-all" "Skip \(s\).*Overwrite \(o\).*Cancel all|Apply to all" "$S"
keys o
check "status after overwrite" "Copied 1 file"
fs "R/alpha.txt overwritten with new content" grep -qx new-alpha "$H/R/alpha.txt"

# ---- multi-select copy, apply-to-all skip -----------------------------------------
keys Space Space                     # select alpha.txt, beta.txt
check "two items selected" "2 selected"
keys c
check "confirmation shows count" "Copy 2 files\?"
keys Enter
check "conflict for alpha again" 'A file named "alpha.txt" already exists'
# Separate keystrokes: TwinPane treats keys arriving in one burst (typeahead,
# terminal replies) as one command plus movement keys only.
keys a; sleep 0.15; keys s
check "skip reported in summary" "Copied 1 file, skipped 1"
fs "beta.txt copied to R" test -f "$H/R/beta.txt"

# ---- same-folder copy rejected -----------------------------------------------------
keys =                               # right panel shows L too
keys c
check "copy into same folder refused" "cannot copy: source and destination folder are the same"
keys Tab BSpace; sleep 0.3; keys Down Enter Tab   # right back to R (cursor was on L)
check "right panel back on R" "/R "

# ---- rename ---------------------------------------------------------------------------
keys Home Down Down Down Down        # beta.txt
keys r
check "rename dialog prefilled" "Current name: beta.txt"
keys C-u C-k; lit "gamma.txt"; keys Enter
check "rename status" 'Renamed "beta.txt" to "gamma.txt"'
fs "gamma.txt exists, beta.txt gone" test -f "$H/L/gamma.txt" -a ! -e "$H/L/beta.txt"
keys r C-u C-k; lit "alpha.txt"; keys Enter
check "rename collision reported inline" '"alpha.txt" already exists'
keys C-u C-k; lit "../escape"; keys Enter
check "rename path traversal refused" "invalid name"
keys Escape
fs "no file escaped the folder" test ! -e "$H/escape"

# ---- move folder ------------------------------------------------------------------------
keys Home Down                       # dir1
keys m
check "move confirmation" 'Move folder "dir1"\?'
keys Enter
check "move status" "Moved"
fs "dir1 moved with contents" test -f "$H/R/dir1/deep/y.txt" -a ! -e "$H/L/dir1"

# ---- delete to trash (default No) -----------------------------------------------------------
# L now: .. many alpha.txt blob.bin gamma.txt
keys End                             # gamma.txt
keys d
S="$(cap)"
check "trash dialog title" "Move 1 file to the trash\?" "$S"
check "trash dialog shows full path" "$H/L/gamma.txt" "$S"
check "default button is No" "No, cancel \(n\)" "$S"
keys Enter
check "Enter cancels (default No)" "nothing was deleted"
fs "gamma.txt still exists" test -f "$H/L/gamma.txt"
keys d; sleep 0.4; keys y
check "trash status" "Moved 1 item to the trash"
fs "gamma.txt in freedesktop trash" test -f "$H/.local/share/Trash/files/gamma.txt" -a -f "$H/.local/share/Trash/info/gamma.txt.trashinfo" -a ! -e "$H/L/gamma.txt"

# ---- permanent delete on another filesystem (trash unavailable) ---------------------------------
keys Tab BSpace; sleep 0.3           # right -> HOME (cursor on R)
keys Down Enter; sleep 0.3           # zz-other
keys Down                            # victim
keys DC                              # Delete key
S="$(cap)"
check "permanent delete title" "Permanently delete 1 folder\?" "$S"
check "contents warning" "ALL of their contents" "$S"
check "cannot be undone warning" "CANNOT be undone" "$S"
check "reason trash not used" "different filesystem" "$S"
check "typed confirmation required" 'Type "delete"' "$S"
lit "y"; keys Enter
check "wrong word rejected" 'type "delete" exactly'
fs "victim still exists after wrong word" test -d "$OTHER/victim"
keys C-u C-k; lit "delete"; keys Enter
check "permanent delete status" "Deleted 1 item \(2 files, 2 folders\)"
fs "victim permanently deleted" test ! -e "$OTHER/victim"
keys BSpace; sleep 0.3; keys Up Enter Tab   # right back to R

# ---- progress + cancel: cross-filesystem move of 30000 files ---------------------------------
# L/many is on $TMPDIR, zz-other on $OTHERFS: each file is copied, fsynced,
# SHA-256 verified and only then removed from the source.
keys Tab BSpace; sleep 0.3; keys Down Enter; sleep 0.3; keys Tab   # right -> zz-other
keys Home Down                       # many
keys m Enter
S="$(cap 0.8)"
if grep -q "Moved 30000 files" <<<"$S"; then
  echo "SKIP  progress/cancel: move finished in under 0.8s"
else
  check "progress box visible during move" "Moving…|Scanning…" "$S"
  check "progress shows counts and cancel hint" "items.*|Esc cancel" "$S"
  keys Escape
  S="$(cap 2)"
  check "cancel reported as cancelled, not success" "Move cancelled" "$S"
  src_n=$( (find "$H/L/many" -type f 2>/dev/null || true) | wc -l)
  dst_n=$( (find "$OTHER/many" -type f 2>/dev/null || true) | wc -l)
  fs "partial move: $dst_n moved, $src_n remain in source" test "$dst_n" -lt 30000 -a "$src_n" -gt 0
  fs "nothing lost or duplicated: $src_n + $dst_n = 30000" test $((src_n + dst_n)) -eq 30000
  fs "no temporary files left" test -z "$(find "$OTHER" "$H" -name '.twinpane-*')"
fi
keys Tab BSpace; sleep 0.3; keys Up Enter; sleep 0.3; keys Tab     # right -> R

# ---- edit -----------------------------------------------------------------------------------------
keys Home Down Down                  # alpha.txt
keys e
S="$(cap)"
check "editor tip card before first edit (unknown editor: generic text)" "Open alpha.txt in fake-editor\?" "$S"
check "generic card has no Neovim keys" "own help for how to save and quit" "$S"
keys Enter                           # Open (the card waits for it)
check "TUI suspended, external editor running" "FAKE EDITOR editing .*/L/alpha.txt"
lit "added-by-editor"; keys Enter
S="$(cap)"
check "TUI restored after editor" "Finished editing alpha.txt" "$S"
check "panels redrawn after editor" "Name +Size +Modified" "$S"
fs "editor change saved" grep -qx added-by-editor "$H/L/alpha.txt"
keys Down e                          # blob.bin
check "binary warning" 'looks like a binary file'
keys Escape
keys Home Down e                     # many (directory)
check "directory refused for edit" "only files can be edited"

# ---- F-keys + help --------------------------------------------------------------------------------
keys End F5;  check "F5 opens copy" "Copy file"; keys Escape
keys F6;      check "F6 opens move" "Move file"; keys Escape
keys F2;      check "F2 opens rename" "Rename"; keys Escape
keys F8;      check "F8 opens delete" "to the trash\?"; keys Escape
keys '?'
S="$(cap)"
check "help lists operations" "File operations" "$S"
check "help lists F-keys" "c/F5.*|d/F8/Del" "$S"
keys q
check "help closed" "Name +Size"
keys q; sleep 0.5

# ---- missing editor: a detected editor is used instead, and the status says why ----------------------
start EDITOR=nonexistent-editor-xyz VISUAL=
keys Down Enter; sleep 0.3; keys Down Down
keys e
check "fallback editor (Neovim) gets the Neovim card" "Open alpha.txt in Neovim\?.*|Esc, then :w"
keys Enter; sleep 2
check "fallback editor opened the file" "new-alpha"
keys Escape; keys ":q" Enter; sleep 1.5
check "missing \$EDITOR reported after editing" 'Finished editing alpha.txt .* from \$EDITOR was not'
keys q; sleep 0.5

echo
echo "phase-2 smoke: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
