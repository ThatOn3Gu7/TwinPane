#!/usr/bin/env bash
# End-to-end smoke test: runs the real binary inside a tmux pseudo-terminal,
# sends keystrokes and checks the rendered screen. Requires tmux.
set -euo pipefail
BIN="$(realpath "${1:-./twinpane}")"
command -v tmux >/dev/null || { echo "tmux not installed"; exit 2; }

FAKEHOME="$(mktemp -d)"; LAUNCHDIR="$(mktemp -d)"
trap 'tmux kill-session -t tpsmoke 2>/dev/null || true; chmod -R u+rwx "$FAKEHOME"; rm -rf "$FAKEHOME" "$LAUNCHDIR"' EXIT
mkdir -p "$FAKEHOME"/{Documents,Music,Projects/app,zlocked}
for i in $(seq -w 1 40); do echo "$i" > "$FAKEHOME/Projects/file$i.txt"; done
echo hi > "$FAKEHOME/readme.md"; echo s > "$FAKEHOME/.secret"
ln -s "$FAKEHOME/Projects" "$FAKEHOME/proj-link"
chmod 000 "$FAKEHOME/zlocked"

pass=0; fail=0
cap()   { sleep 0.4; tmux capture-pane -p -t tpsmoke; }
check() { # check <description> <grep-pattern> [screen]
  local scr="${3:-$(cap)}"
  if grep -qE -- "$2" <<<"$scr"; then echo "PASS  $1"; pass=$((pass+1));
  else echo "FAIL  $1  (pattern: $2)"; echo "$scr"; fail=$((fail+1)); fi
}
keys()  { tmux send-keys -t tpsmoke "$@"; }

# Launch from an unrelated directory; panels must open in $HOME.
tmux new-session -d -s tpsmoke -x 110 -y 30 -c "$LAUNCHDIR" "env HOME=$FAKEHOME TERM=xterm-256color $BIN"
sleep 1
S="$(cap)"
check "both panels open in HOME (not launch dir)" "$(basename "$FAKEHOME").*$(basename "$FAKEHOME")" "$S"
check "column headers shown"            "Name +Size +Modified" "$S"
check "footer shows shortcuts"          "Tab +switch.*q +quit" "$S"
check "left panel active in title"      "active: left panel" "$S"
check "hidden files hidden by default"  "\+1 hidden" "$S"
check "symlink marked"                  "proj-link@" "$S"

keys Down Down Down Down Enter       # .. Documents Music proj-link Projects -> Projects
S="$(cap)"; check "enter opens directory (path line)" "│ /.*/Projects +│" "$S"
check "directory contents listed"       "file01.txt +3 B" "$S"
keys Down Down; S="$(cap)"; check "file info line for cursor entry" "│ -rw-r--r-- +3 B +20[0-9-]+ " "$S"
keys End; S="$(cap)"; check "End scrolls to last entry" "file40.txt" "$S"
check "position indicator"              "42/42" "$S"
keys BSpace; S="$(cap)"; check "backspace returns to parent" "Documents/" "$S"

keys Tab; S="$(cap)"; check "tab switches active panel" "active: right panel" "$S"
keys Down Space Space; S="$(cap)"; check "selection summary" "2 selected" "$S"
keys .; S="$(cap)"; check "toggle hidden shows dotfiles" "\.secret" "$S"
keys h; S="$(cap)"; check "h hides dotfiles again" "\+1 hidden" "$S"
keys h; S="$(cap)"; check "h shows dotfiles again" "\.secret" "$S"
keys s; S="$(cap)"; check "s sorts by size (largest first)" "size ↓" "$S"
keys S; S="$(cap)"; check "S reverses the order" "size ↑" "$S"
keys s; S="$(cap)"; check "s cycles to modification time" "time ↓" "$S"
keys s; S="$(cap)"; check "s cycles to type" "type ↑" "$S"
keys s; S="$(cap)"; check "s cycles back to name" "name ↑ .*name ↑" "$S"

# navigate right panel to locked dir -> error in status line, panel stays
keys g; for _ in 1 2 3 4 5; do keys Down; done; S="$(cap)"
check "cursor on locked dir" "drwx------|d---------" "$S"
keys Enter; S="$(cap)"; check "inaccessible dir reported" "permission denied" "$S"

keys ?; S="$(cap)"; check "help overlay" "keyboard controls" "$S"
keys q; S="$(cap)"; check "help closes on key" "Name +Size" "$S"

tmux resize-window -t tpsmoke -x 60 -y 15 2>/dev/null || tmux resize-pane -t tpsmoke -x 60 -y 15
S="$(cap)"; check "resize to 60x15 re-layouts" "Name +Size" "$S"
tmux resize-window -t tpsmoke -x 25 -y 6 2>/dev/null || true
S="$(cap)"; check "tiny terminal message" "too small" "$S"
tmux resize-window -t tpsmoke -x 110 -y 30 2>/dev/null || true
S="$(cap)"; check "recovers after enlarging" "Name +Size +Modified" "$S"

keys q; sleep 0.6
if tmux has-session -t tpsmoke 2>/dev/null; then echo "FAIL  q quits"; fail=$((fail+1)); else echo "PASS  q quits"; pass=$((pass+1)); fi

echo "smoke: $pass passed, $fail failed"
[[ $fail -eq 0 ]]
