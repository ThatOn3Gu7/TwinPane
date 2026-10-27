package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/foldersize"
)

// barItem is one item of the context hint bar at the bottom of the screen.
// key is the key label, desc what it does. A hint with send set can be
// tapped / clicked: TwinPane then behaves exactly as if that key had been
// pressed, so the bar never offers an action the keyboard doesn't have.
type barItem struct {
	key, desc string
	send      string // key string (tea.KeyMsg.String()) to synthesise; "" = not clickable
	// keepChip draws the padded key alone (no description) instead of
	// dropping the item when the bar is too full for both. The focus-mode
	// toggle uses it: on a narrow terminal "^O" still tells you how to get
	// back to two panes.
	keepChip bool
}

func item(key, desc, send string) barItem { return barItem{key: key, desc: desc, send: send} }

// footerHit is the screen range of a clickable hint (row = last screen line).
type footerHit struct {
	x0, x1 int
	send   string
}

// hints returns the items for the current screen, most important first
// (items that don't fit are dropped from the end), plus the item pinned at
// the right edge (normal browsing only: "? help").
func (a *App) hints() (items []barItem, pinned *barItem) {
	switch {
	case a.editing:
		return nil, nil // the editor owns the terminal
	case a.dialog != nil:
		return a.dialogHints(a.dialog), nil
	case a.tasks.showing:
		return a.taskFooterHints(), nil
	case a.actView != nil:
		return a.activityHints(), nil
	case a.live != nil:
		return a.liveHints(), nil
	case a.showHelp:
		return a.helpHints(), nil
	case a.showSettings:
		return a.settingsHints(), nil
	case a.props != nil:
		return a.propsHints(), nil
	case a.viewer != nil:
		return a.viewerHints(), nil
	case a.advancedSearch != nil:
		return a.advancedSearchHints(), nil
	case a.search != nil:
		return a.searchHints(), nil
	case a.bulk != nil:
		return a.bulkHints(), nil
	case a.folderSize != nil:
		return a.folderSizeHints(), nil
	case a.comparison != nil:
		return a.comparisonHints(), nil
	}
	help := item("?", "help", "?")
	return a.browseHints(), &help
}

// liveHints describes the live progress window: it is a view of one running
// task, so the chips are about leaving it, not about browsing. Both ⏎ and Esc
// close a finished task, but only one close chip is shown: the same action does
// not need two keys in the bar.
func (a *App) liveHints() []barItem {
	s := a.liveTaskSession()
	out := []barItem{item("↑↓", "scroll", "")}
	if s != nil && s.active() {
		out = append(out, item("c", "cancel", "c"), item("Esc", "background", "esc"))
	} else {
		out = append(out, item("Esc", "close", "esc"))
	}
	return append(out, item("t", "tasks", "t"))
}

// browseHints adapts the normal-mode hints to the active panel: actions
// that cannot apply to the current target are left out.
func (a *App) browseHints() []barItem {
	p := a.ActivePanel()
	if p.Err() != nil {
		if p.InRemote() {
			return []barItem{item("^L", "retry", "ctrl+l"), item("^R", "sources", "ctrl+r"),
				item("Tab", "switch", "tab"), item("t", "tasks", "t"), item("~", "local home", "~"), item("q", "quit", "q")}
		}
		return []barItem{item("⌫", "parent", "backspace"), item("^L", "retry", "ctrl+l"),
			item("Tab", "switch", "tab"), item("t", "tasks", "t"), item("~", "home", "~"), item("q", "quit", "q"), item("^R", "sources", "ctrl+r")}
	}
	if p.InArchive() {
		return a.archiveHints()
	}
	if p.InRemote() {
		return a.remoteHints()
	}
	cur, ok := p.Current()
	targets := p.Targets()
	nsel := len(p.Selection())
	isBrowsable := ok && !cur.IsDir && !cur.IsParent && a.isBrowsableArchive(cur.Path)

	out := []barItem{item("Tab", "switch", "tab")}
	// the mode and history chips come right after Tab: they are contextual,
	// and the end of the bar is what narrow terminals drop
	out = append(out, a.focusHints()...)
	out = append(out, a.navHints(p)...)
	if isBrowsable && nsel == 0 {
		out = append(out, item("⏎", "browse archive", "enter"), item("x", "extract", "x"))
	}
	if ok && cur.IsDir && nsel == 0 {
		out = append(out, item("⏎", "open", "enter"))
	}
	if ok && !cur.IsParent {
		if p.IsSelected(cur.Name) {
			out = append(out, item("Space", "unselect", " "))
		} else {
			out = append(out, item("Space", "select", " "))
		}
	}
	if nsel > 0 {
		out = append(out, item("u", "clear", "u"))
	}
	if len(targets) > 0 {
		rename := "rename"
		if len(targets) > 1 {
			rename = "bulk rename"
		}
		copyDesc, moveDesc := "copy", "move"
		if a.focus {
			// one pane: the destination is chosen first (focus_dest.go)
			copyDesc, moveDesc = "copy to…", "move to…"
		}
		out = append(out, item("c", copyDesc, "c"), item("m", moveDesc, "m"), item("r", rename, "r"), item("d", "delete", "d"))
		files := true
		for _, e := range targets {
			if e.IsDir {
				files = false
				break
			}
		}
		if files {
			out = append(out, item("e/E", "edit", "e"))
		}
	}
	out = append(out, item("t", "tasks", "t"))
	// Files open in the viewer or external app; supported archives were
	// given their browse/extract actions near the front of the hint bar.
	if ok && !cur.IsDir && !cur.IsParent && nsel == 0 && !isBrowsable {
		if a.hintPrefersExternal(cur) {
			out = append(out, item("⏎", "open", "enter"))
		} else {
			out = append(out, item("⏎", "view", "enter"))
		}
		if looksLikeArchive(cur.Name) {
			out = append(out, item("x", "extract", "x")) // TAR, 7Z, RAR, compressed files
		}
	}
	hidden := "show hidden"
	if a.showHidden {
		hidden = "hide hidden"
	}
	out = append(out, item("/", "find", "/"), item("h", hidden, "h"), item("s/S", "sort", "s"))
	if ok && !cur.IsParent {
		out = append(out, item("P", "properties", "P"))
	}
	out = append(out, item("q", "quit", "q"))
	// phase 6, after everything that was there before so those keep their
	// place: the action menu (also reachable by tapping the TwinPane
	// badge, at any width), Open with, bookmarks, storage
	out = append(out, item("M", "menu", "M"))
	if ok && !cur.IsDir && !cur.IsParent && nsel == 0 && !isBrowsable {
		out = append(out, item("o/O", "open with", "O"))
	}
	out = append(out, item("b", "bookmarks", "b"), item("i", "storage", "i"), item("A", "folder sizes", "A"))
	if len(targets) > 0 {
		out = append(out, item("z", "compress", "z"))
	}
	// phase 9, again after everything that was there before
	out = append(out, item("^R", "sources", "ctrl+r"), item("n", "new file / folder", "n"))
	out = append(out, a.focusToggleHint()...)
	// phase 13: the settings screen, last like the other late additions.
	// keepChip (and the position after the focus toggle, which keeps its
	// own keep-chip priority) means a bare "F9" still renders from the
	// leftover cells of a crowded bar, and the full "F9 settings" chip
	// shows once the bar has room.
	return append(out, barItem{key: "F9", desc: "settings", send: "f9", keepChip: true})
}

// remoteHints is the hint bar of a panel showing an rclone remote: only
// what works there.
func (a *App) remoteHints() []barItem {
	p := a.ActivePanel()
	cur, ok := p.Current()
	nsel := len(p.Selection())
	out := []barItem{item("Tab", "switch", "tab"), item("t", "tasks", "t")}
	out = append(out, a.focusHints()...)
	out = append(out, a.navHints(p)...)
	if ok && cur.IsDir && nsel == 0 {
		out = append(out, item("⏎", "open", "enter"))
	}
	if ok && !cur.IsParent {
		if p.IsSelected(cur.Name) {
			out = append(out, item("Space", "unselect", " "))
		} else {
			out = append(out, item("Space", "select", " "))
		}
	}
	if nsel > 0 {
		out = append(out, item("u", "clear", "u"))
	}
	if len(p.Targets()) > 0 {
		out = append(out, item("c", "copy", "c"), item("m", "move", "m"))
		if len(p.Targets()) == 1 {
			out = append(out, item("r", "rename", "r"))
		}
		out = append(out, item("d", "delete", "d"))
	}
	out = append(out, item("n", "new", "n"), item("^R", "sources", "ctrl+r"), item("^L", "refresh", "ctrl+l"))
	hidden := "show hidden"
	if a.showHidden {
		hidden = "hide hidden"
	}
	out = append(out, item("h", hidden, "h"), item("s/S", "sort", "s"))
	out = append(out, item("M", "menu", "M"), item("q", "quit", "q"))
	return append(out, a.focusToggleHint()...)
}

// archiveHints is the hint bar of a panel browsing a ZIP or TAR archive: only
// operations supported on virtual, read-only entries are shown.
func (a *App) archiveHints() []barItem {
	p := a.ActivePanel()
	cur, ok := p.Current()
	nsel := len(p.Selection())
	out := []barItem{item("Tab", "switch", "tab"), item("t", "tasks", "t"), item("Esc", "leave archive", "esc")}
	out = append(out, a.focusHints()...)
	out = append(out, a.navHints(p)...)
	if len(p.Targets()) > 0 {
		out = append(out, item("x", "extract", "x"))
	}
	if ok && nsel == 0 {
		switch {
		case cur.IsParent && p.ArchiveInner() == "":
			out = append(out, item("⏎", "leave archive", "enter"))
		case cur.IsDir:
			out = append(out, item("⏎", "open", "enter"))
		default:
			out = append(out, item("⏎", "view", "enter"))
		}
	}
	if ok && !cur.IsParent {
		if p.IsSelected(cur.Name) {
			out = append(out, item("Space", "unselect", " "))
		} else {
			out = append(out, item("Space", "select", " "))
		}
	}
	if nsel > 0 {
		out = append(out, item("u", "clear", "u"))
	}
	out = append(out, item("X", "extract all", "X"))
	if ok && !cur.IsParent {
		out = append(out, item("P", "info", "P"))
	}
	hidden := "show hidden"
	if a.showHidden {
		hidden = "hide hidden"
	}
	out = append(out, item("h", hidden, "h"), item("s/S", "sort", "s"))
	return append(append(out, item("q", "quit", "q")), a.focusToggleHint()...)
}

func (a *App) searchHints() []barItem {
	sv := a.search
	have := len(sv.matches) > 0
	if sv.list {
		out := []barItem{}
		if have {
			out = append(out, item("⏎", "go to", "enter"), item("e", "edit", "e"), item("E", "edit with…", "E"), item("Space", "mark", " "))
		}
		return append(out, item("^G", "advanced", "ctrl+g"), item("↑↓", "move", ""), item("/", "type", "/"), item("Esc", "close", "esc"))
	}
	out := []barItem{item("type", "to filter", "")}
	if have {
		out = append(out, item("⏎", "go to", "enter"), item("Tab", "results", "tab"), item("F4", "edit", "f4"), item("↑↓", "move", ""))
	}
	return append(out, item("^G", "advanced", "ctrl+g"), item("Esc", "close", "esc"))
}

func (a *App) bulkHints() []barItem {
	b := a.bulk
	if b.applying {
		return nil
	}
	return []barItem{
		item("a", "apply…", "a"), item("Esc", "cancel", "esc"),
		item("⏎", "edit name", "enter"), item("f", "find/replace", "f"), item("n", "pattern", "n"),
		item("E", "edit in editor", "E"), item("L/U", "case", ""), item("R", "reset", "R"), item("↑↓", "move", ""),
	}
}

// dialogHints derives the hints from the dialog itself (its buttons, list,
// text field and toggles), so they always match what the dialog accepts.
func (a *App) dialogHints(d *dialog) []barItem {
	escDesc := "cancel"
	for _, b := range d.buttons {
		if strings.Contains(b.label, "(Esc)") {
			escDesc = shortLabel(b.label)
		}
	}
	switch {
	case d.filter != nil:
		out := []barItem{item("type", "to filter", "")}
		if len(d.list) > 0 {
			out = append(out, item("⏎", "choose", "enter"))
		}
		return append(out, item("↑↓", "move", ""), item("Esc", "back", "esc"))
	case d.input != nil:
		out := []barItem{item("⏎", "OK", "enter")}
		for _, t := range d.inToggles {
			out = append(out, item(t.help, strings.ToLower(t.label), t.key))
		}
		for _, sel := range d.selectors {
			out = append(out, item(sel.help, sel.hint, sel.keys[1]))
		}
		return append(out, item("Esc", escDesc, "esc"))
	case d.list != nil:
		out := []barItem{item("↑↓", "choose", "")}
		if d.listKeyed() {
			out = append(out, item("a-z", "pick", ""))
		} else if len(d.list) > 0 {
			out = append(out, item("1-9", "pick", ""))
		}
		pick := "open"
		if d.kind == dlgMenu || d.kind == dlgNewMenu {
			pick = "run"
		}
		if len(d.list) > 0 {
			out = append(out, item("⏎", pick, "enter"))
		}
		for _, act := range d.listActions {
			if act.needItem && len(d.list) == 0 {
				continue
			}
			out = append(out, item(act.key, act.label, act.key))
		}
		return append(out, item("Esc", escDesc, "esc"))
	}
	var out []barItem
	if len(d.buttons) == 1 && d.buttons[0].key == "" {
		if d.kind == dlgCompressProgress {
			action := "cancel"
			if a.compressProgress != nil && a.compressProgress.finished {
				action = "dismiss"
			}
			out = append(out, item("⏎", action, "enter"), item("Esc", action, "esc"))
		} else {
			out = append(out, item("⏎", "close", "enter"))
		}
		if d.scrollable {
			out = append(out, item("↑↓", "scroll", ""))
		}
		return out
	}
	if len(d.buttons) > 0 && d.focus < len(d.buttons) {
		fb := d.buttons[d.focus]
		k := "⏎"
		if fb.key != "" {
			k += "/" + fb.key
		}
		out = append(out, item(k, shortLabel(fb.label), "enter"))
	}
	for i, b := range d.buttons {
		if b.key != "" && i != d.focus {
			out = append(out, item(b.key, shortLabel(b.label), b.key))
		}
	}
	if d.toggleKey != "" {
		out = append(out, item(d.toggleKey, "apply to all", d.toggleKey))
	}
	out = append(out, item("Esc", escDesc, "esc"))
	if len(d.buttons) > 1 {
		out = append(out, item("←→", "choose", ""))
	}
	if d.scrollable {
		out = append(out, item("↑↓", "scroll", ""))
	}
	return out
}

// shortLabel turns a button label into a hint description:
// "Move to trash (y)" → "move to trash".
func shortLabel(s string) string {
	if i := strings.LastIndex(s, " ("); i > 0 && strings.HasSuffix(s, ")") {
		s = s[:i]
	}
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

// footerView renders the hint bar and records where the clickable items are.
// Every shortcut is a chip with theme.FooterKeyPad cells of coloured padding
// on both sides (the padding is part of theme.FooterKey, so it shares the
// chip's background), followed by its description in the muted style with
// one space on each side, which also separates it from the next chip. The
// same layout is used in every context (browsing, dialogs, search, bulk
// rename, viewer, properties, operations). Items that don't fit are dropped
// from the end (least important last) — an item is never cut in half — and
// the pinned item stays at the right edge. On very narrow screens a
// description may be shortened so at least the first item stays readable.
func (a *App) footerView() string {
	items, pinned := a.hints()
	a.footerHits = a.footerHits[:0]
	if len(items) == 0 && pinned == nil {
		return strings.Repeat(" ", max(0, a.width))
	}
	seg := func(it barItem) string { return a.hintSegment(it) }
	pin, pinW := "", 0
	if pinned != nil {
		pin = seg(*pinned)
		pinW = lipgloss.Width(pin)
		if pinW > a.width/2 {
			pin, pinW, pinned = "", 0, nil // no room for it: drop it
		}
	}
	avail := a.width - pinW
	var b strings.Builder
	used := 0
	draw := func(it barItem, s string) {
		w := lipgloss.Width(s)
		if it.send != "" {
			a.footerHits = append(a.footerHits, footerHit{used, used + w, it.send})
		}
		b.WriteString(s)
		used += w
	}
	rest := items
	for i, it := range items {
		s := seg(it)
		w := lipgloss.Width(s)
		if used+w > avail {
			if i > 0 {
				rest = items[i:]
				break
			}
			// the first item alone is too wide: shorten its description
			s = a.fitSegment(it, avail)
			w = lipgloss.Width(s)
			if w == 0 || used+w > avail {
				rest = items[i:]
				break
			}
		}
		draw(it, s)
		rest = items[i+1:]
	}
	// Chips that must stay reachable (keepChip: the focus-mode toggle) are
	// drawn into whatever room is left, with their description when it fits
	// and as a bare padded key otherwise. They are the lowest priority, so
	// nothing that fits today moves: the chip only takes leftover cells.
	for _, it := range rest {
		if !it.keepChip {
			continue
		}
		s := seg(it)
		if used+lipgloss.Width(s) > avail {
			s = a.theme.FooterKey.Render(it.key)
		}
		if used+lipgloss.Width(s) > avail {
			continue
		}
		draw(it, s)
	}
	if pinned != nil && avail >= 0 {
		// right-align the pinned item
		if gap := avail - used; gap > 0 {
			b.WriteString(strings.Repeat(" ", gap))
			used += gap
		}
		a.footerHits = append(a.footerHits, footerHit{used, used + pinW, pinned.send})
		b.WriteString(pin)
		used += pinW
	}
	out := b.String()
	if used < a.width {
		out += strings.Repeat(" ", a.width-used)
	}
	return ansi.Truncate(out, a.width, "")
}

// hintSegment renders one hint: the padded shortcut chip and its
// description.
func (a *App) hintSegment(it barItem) string {
	return a.theme.FooterKey.Render(it.key) + a.theme.FooterDesc.Render(" "+it.desc+" ")
}

// fitSegment renders it within w cells by truncating the description
// (never the shortcut chip). It returns "" if not even the chip fits.
func (a *App) fitSegment(it barItem, w int) string {
	chip := a.theme.FooterKey.Render(it.key)
	cw := lipgloss.Width(chip)
	if cw > w {
		return ""
	}
	if room := w - cw; room >= 2 {
		return chip + a.theme.FooterDesc.Render(" "+ansi.Truncate(it.desc, room-1, "…"))
	}
	return chip
}

// keyFor builds the key message that a clickable hint stands for.
func keyFor(s string) tea.KeyMsg {
	if s == "focus" {
		// the hint chip / menu action of the focus-mode toggle (focus.go)
		return tea.KeyMsg{Type: keyFocusMode}
	}
	special := map[string]tea.KeyType{
		"esc": tea.KeyEsc, "enter": tea.KeyEnter, "tab": tea.KeyTab, "backspace": tea.KeyBackspace,
		"ctrl+r": tea.KeyCtrlR, "ctrl+l": tea.KeyCtrlL, "ctrl+f": tea.KeyCtrlF, "ctrl+a": tea.KeyCtrlA,
		"f7": tea.KeyF7, "ctrl+t": tea.KeyCtrlT, "f4": tea.KeyF4, "up": tea.KeyUp, "down": tea.KeyDown,
		"pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown, "left": tea.KeyLeft, "right": tea.KeyRight,
		"shift+tab": tea.KeyShiftTab, "home": tea.KeyHome, "end": tea.KeyEnd,
		"f9": tea.KeyF9, "ctrl+s": tea.KeyCtrlS,
	}
	if t, ok := special[s]; ok {
		return tea.KeyMsg{Type: t}
	}
	if s == " " {
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// advancedSearchHints is the hint bar of Advanced Search: read-only result
// navigation, plus the mode switch and help.
func (a *App) advancedSearchHints() []barItem {
	return []barItem{
		item("↑↓", "move", ""),
		item("^A", "actions", "ctrl+a"),
		item("Enter", "open / inspect", "enter"),
		item("^T", "toggle files/content", "ctrl+t"),
		item("?", "help", "?"),
		item("Esc", "close", "esc"),
	}
}

// folderSizeHints is the hint bar of folder-size analysis. It follows the
// scan state: only Esc while loading, retry after a failure, and the sort /
// navigation keys once there is a result.
func (a *App) folderSizeHints() []barItem {
	v := a.folderSize
	if v == nil {
		return nil
	}
	if v.loading {
		return []barItem{item("Esc", "cancel scan", "esc")}
	}
	if v.result == nil || v.root == nil {
		return []barItem{item("r", "retry scan", "r"), item("?", "keys/semantics", "?"), item("Esc", "close", "esc")}
	}
	out := []barItem{
		item("Enter", "open directory", "enter"), item("←", "parent", "left"),
		item("s", "size", "s"), item("n", "name", "n"), item("r", "refresh", "r"),
	}
	if foldersize.AllocatedSizeSupported() {
		out = append(out, item("a", "allocated sort", "a"))
	}
	out = append(out, item("?", "keys/semantics", "?"), item("Esc", "close", "esc"))
	return out
}
