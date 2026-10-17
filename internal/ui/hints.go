package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// barItem is one item of the context hint bar at the bottom of the screen.
// key is the key label, desc what it does. A hint with send set can be
// tapped / clicked: TwinPane then behaves exactly as if that key had been
// pressed, so the bar never offers an action the keyboard doesn't have.
type barItem struct {
	key, desc string
	send      string // key string (tea.KeyMsg.String()) to synthesise; "" = not clickable
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
	case a.planning != nil || a.op != nil:
		return []barItem{item("Esc", "cancel operation", "esc")}, nil
	case a.showHelp:
		return a.helpHints(), nil
	case a.props != nil:
		return a.propsHints(), nil
	case a.viewer != nil:
		return a.viewerHints(), nil
	case a.search != nil:
		return a.searchHints(), nil
	case a.bulk != nil:
		return a.bulkHints(), nil
	}
	help := item("?", "help", "?")
	return a.browseHints(), &help
}

// browseHints adapts the normal-mode hints to the active panel: actions
// that cannot apply to the current target are left out.
func (a *App) browseHints() []barItem {
	p := a.ActivePanel()
	if p.Err() != nil {
		return []barItem{item("⌫", "parent", "backspace"), item("^R", "retry", "ctrl+r"),
			item("Tab", "switch", "tab"), item("~", "home", "~"), item("q", "quit", "q")}
	}
	if p.InArchive() {
		return a.archiveHints()
	}
	cur, ok := p.Current()
	targets := p.Targets()
	nsel := len(p.Selection())

	out := []barItem{item("Tab", "switch", "tab")}
	if ok && cur.IsDir && nsel == 0 {
		out = append(out, item("⏎", "open", "enter"))
	}
	isZip := ok && !cur.IsDir && !cur.IsParent && looksLikeZip(cur.Name)
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
		out = append(out, item("c", "copy", "c"), item("m", "move", "m"), item("r", rename, "r"), item("d", "delete", "d"))
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
	// phase 5: files open in the viewer, ZIP archives are browsed
	if ok && !cur.IsDir && !cur.IsParent && nsel == 0 {
		if isZip {
			out = append(out, item("⏎", "browse ZIP", "enter"), item("x", "extract", "x"))
		} else if a.hintPrefersExternal(cur) {
			out = append(out, item("⏎", "open", "enter"))
		} else {
			out = append(out, item("⏎", "view", "enter"))
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
	if len(targets) > 0 {
		out = append(out, item("z", "compress", "z"))
	}
	out = append(out, item("q", "quit", "q"))
	// phase 6, after everything that was there before so those keep their
	// place: the action menu (also reachable by tapping the TwinPane
	// badge, at any width), Open with, bookmarks, storage
	out = append(out, item("M", "menu", "M"))
	if ok && !cur.IsDir && !cur.IsParent && nsel == 0 && !isZip {
		out = append(out, item("o/O", "open with", "O"))
	}
	out = append(out, item("b", "bookmarks", "b"), item("i", "storage", "i"))
	return out
}

// archiveHints is the hint bar of a panel browsing a ZIP archive: only what
// works on archive entries (they are read-only).
func (a *App) archiveHints() []barItem {
	p := a.ActivePanel()
	cur, ok := p.Current()
	nsel := len(p.Selection())
	out := []barItem{item("Tab", "switch", "tab")}
	if ok && nsel == 0 {
		switch {
		case cur.IsParent && p.ArchiveInner() == "":
			out = append(out, item("⏎", "leave ZIP", "enter"))
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
	if len(p.Targets()) > 0 {
		out = append(out, item("x", "extract", "x"))
	}
	out = append(out, item("Esc", "leave ZIP", "esc"), item("X", "extract all", "X"))
	if ok && !cur.IsParent {
		out = append(out, item("P", "info", "P"))
	}
	hidden := "show hidden"
	if a.showHidden {
		hidden = "hide hidden"
	}
	return append(out, item("h", hidden, "h"), item("s/S", "sort", "s"), item("q", "quit", "q"))
}

func (a *App) searchHints() []barItem {
	sv := a.search
	have := len(sv.matches) > 0
	if sv.list {
		out := []barItem{}
		if have {
			out = append(out, item("⏎", "go to", "enter"), item("e", "edit", "e"), item("E", "edit with…", "E"), item("Space", "mark", " "))
		}
		return append(out, item("↑↓", "move", ""), item("/", "type", "/"), item("Esc", "close", "esc"))
	}
	out := []barItem{item("type", "to filter", "")}
	if have {
		out = append(out, item("⏎", "go to", "enter"), item("Tab", "results", "tab"), item("F4", "edit", "f4"), item("↑↓", "move", ""))
	}
	return append(out, item("Esc", "close", "esc"))
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
	case d.input != nil:
		out := []barItem{item("⏎", "OK", "enter")}
		for _, t := range d.inToggles {
			out = append(out, item(t.help, strings.ToLower(t.label), t.key))
		}
		out = append(out, item("Esc", escDesc, "esc"))
		for _, sel := range d.selectors {
			out = append(out, item(sel.help, sel.hint, sel.keys[1]))
		}
		return out
	case d.list != nil:
		out := []barItem{item("↑↓", "choose", "")}
		if d.listKeyed() {
			out = append(out, item("a-z", "pick", ""))
		} else if len(d.list) > 0 {
			out = append(out, item("1-9", "pick", ""))
		}
		pick := "open"
		if d.kind == dlgMenu {
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
		out = append(out, item("⏎", "close", "enter"))
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
	for i, it := range items {
		s := seg(it)
		w := lipgloss.Width(s)
		if used+w > avail {
			if i > 0 {
				break
			}
			// the first item alone is too wide: shorten its description
			s = a.fitSegment(it, avail)
			w = lipgloss.Width(s)
			if w == 0 || used+w > avail {
				break
			}
		}
		if it.send != "" {
			a.footerHits = append(a.footerHits, footerHit{used, used + w, it.send})
		}
		b.WriteString(s)
		used += w
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
	special := map[string]tea.KeyType{
		"esc": tea.KeyEsc, "enter": tea.KeyEnter, "tab": tea.KeyTab, "backspace": tea.KeyBackspace,
		"ctrl+r": tea.KeyCtrlR, "ctrl+t": tea.KeyCtrlT, "f4": tea.KeyF4, "up": tea.KeyUp, "down": tea.KeyDown,
		"pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown, "left": tea.KeyLeft, "right": tea.KeyRight,
		"shift+tab": tea.KeyShiftTab, "home": tea.KeyHome, "end": tea.KeyEnd,
	}
	if t, ok := special[s]; ok {
		return tea.KeyMsg{Type: t}
	}
	if s == " " {
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}
