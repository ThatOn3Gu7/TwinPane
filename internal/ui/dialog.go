package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

// dialogKind identifies a dialog (used by tests and for behaviour tweaks).
type dialogKind int

const (
	dlgConfirm dialogKind = iota // copy / move confirmation
	dlgConflict
	dlgLowSpace
	dlgDelete
	dlgRename
	dlgBinary
	dlgReport
	dlgSearch           // search pattern prompt
	dlgBulkEdit         // bulk rename: a text prompt (row edit, find, replace, pattern)
	dlgBulkConfirm      // bulk rename: apply confirmation
	dlgChooser          // pick an item from a list (editor chooser)
	dlgEditTip          // save/quit keys shown just before the editor starts
	dlgExtract          // extraction destination prompt
	dlgZip              // compression format/name prompt (legacy kind)
	dlgZipExists        // overwrite an existing compressed output?
	dlgCompressProgress // retained compression progress / completion modal
	dlgViewFind         // viewer search prompt

	// phase 6
	dlgMenu      // context menu (M)
	dlgBookmarks // bookmark list (b)
	dlgOpenWith  // Open with… (O)
	dlgStorage   // storage overview (i)
	dlgRunWith   // "Run a program…" prompt

	// phase 11 (focus mode)
	dlgGoTo // Ctrl+G: type a local path or a remote location
	dlgDest // copy/move destination chooser (focus mode)

	// phase 9
	dlgSources     // Sources picker (Ctrl+R): local files or an rclone remote
	dlgMkdir       // new folder name prompt
	dlgSyncConfirm // final approval for a reviewed directory plan

	// phase 13
	dlgSettings // settings text field / reset confirmation (F9 screen)
)

// dlgCompress is the compression dialog (z). It kept the kind of the
// phase-5 ZIP dialog it grew from.
const dlgCompress = dlgZip

// lineKind controls how a body line is laid out.
type lineKind int

const (
	lnText lineKind = iota // wrapped
	lnPath                 // single line, truncated on the left ("…/tail")
	lnWarn                 // wrapped, warning style
	lnHint                 // wrapped, muted
	lnBlank
	lnRaw // pre-rendered, inserted as is
)

type dline struct {
	kind lineKind
	text string
}

func text(s string) dline { return dline{lnText, s} }
func path(s string) dline { return dline{lnPath, s} }
func warn(s string) dline { return dline{lnWarn, s} }
func hint(s string) dline { return dline{lnHint, s} }
func blank() dline        { return dline{kind: lnBlank} }

// listItem is one row of a dialog's pick list.
type listItem struct {
	label    string
	detail   string // muted text after the label
	info     string // shown under the list while the item is highlighted
	tip      string // muted line(s) under info (e.g. the editor's save/quit keys)
	disabled bool
	// phase 6 (context menu): key picks the item directly and is shown
	// instead of its number; danger draws it in the warning colour.
	key    string
	danger bool
}

// listAction is an extra key of a pick-list dialog acting on the
// highlighted item (e.g. r = rename a bookmark). needItem: only when the
// list is not empty.
type listAction struct {
	key, label string
	needItem   bool
	run        func(a *App, i int) tea.Cmd
}

// selector is a "◂ value ▸" row of a dialog (e.g. the archive format),
// changed with its keys or by tapping the arrows. prev/next nil: the row
// shows a fixed value.
type selector struct {
	label      string
	value      func() string
	note       func() (text string, warn bool) // line under the row ("" = none)
	prev, next func(a *App)
	keys       [2]string // keys for prev / next
	help       string    // key label for hints, e.g. "↑↓"
	hint       string    // what it changes, for the hint bar
}

type button struct {
	label  string
	key    string // hotkey ("" = none); Enter activates the focused button
	danger bool
	run    func(a *App) tea.Cmd
}

// inputToggle is an on/off switch shown under a dialog's text input.
type inputToggle struct {
	key   string // e.g. "ctrl+r"
	help  string // e.g. "^R"
	label string
	on    bool
}

// dialog is a modal box. While one is open it receives every key.
type dialog struct {
	kind   dialogKind
	title  string
	danger bool
	body   []dline

	buttons []button
	focus   int

	// optional text input: Enter calls onSubmit with its value
	input      *lineInput
	inputLabel string
	onSubmit   func(a *App, value string) tea.Cmd
	errLine    string

	// optional switches for an input dialog (e.g. "regular expression"),
	// flipped with their key while typing
	inToggles []*inputToggle

	// optional checkbox toggled by toggleKey (e.g. "apply to all")
	toggleLabel string
	toggleKey   string
	toggled     bool

	// optional pick list: ↑↓ moves, 1-9 picks directly, Enter calls onPick
	list        []listItem
	listCursor  int
	onPick      func(a *App, i int) tea.Cmd
	listActions []listAction
	listEmpty   string // shown instead of an empty list

	// optional selector rows above the text input (phase 6)
	selectors []*selector

	onEsc  func(a *App) tea.Cmd // nil = just close
	footer string               // key hints shown at the bottom
	scroll int
	width  int // preferred content width

	// Filled in by renderDialog for mouse / touch input (screen coordinates
	// of the last drawn frame).

	scrollable   bool
	hits         []dialogHit
	boxY0, boxY1 int        // screen rows covered by the box
	press        *dialogHit // target under the last left-button press
}

// dialogHit is a clickable region of a drawn dialog.
type dialogHit struct {
	kind   hitKind
	index  int // button / list item / input toggle index
	x0, x1 int // screen columns [x0, x1)
	y      int // screen row
}

type hitKind int

const (
	hitButton hitKind = iota
	hitListItem
	hitToggle   // the "apply to all" checkbox
	hitInToggle // a switch under a text field
	hitSelPrev  // a selector's ◂
	hitSelNext  // a selector's ▸
)

func (d *dialog) cancel(a *App) tea.Cmd {
	if d.onEsc != nil {
		return d.onEsc(a)
	}
	a.dialog = nil
	return nil
}

// dialogKey routes a key to the open dialog.
func (a *App) dialogKey(msg tea.KeyMsg) tea.Cmd {
	d := a.dialog
	s := msg.String()
	if s == "esc" || s == "ctrl+c" {
		return d.cancel(a)
	}
	for _, sel := range d.selectors {
		switch {
		case s == sel.keys[0] && sel.prev != nil:
			sel.prev(a)
			d.errLine = ""
			return nil
		case s == sel.keys[1] && sel.next != nil:
			sel.next(a)
			d.errLine = ""
			return nil
		}
	}
	if d.input != nil {
		if s == "enter" {
			return d.onSubmit(a, d.input.Value())
		}
		for _, t := range d.inToggles {
			if s == t.key {
				t.on = !t.on
				d.errLine = ""
				return nil
			}
		}
		if d.input.Update(msg) {
			d.errLine = ""
		}
		return nil
	}
	if msg.Paste {
		return nil // never let pasted text press buttons
	}
	if d.list != nil {
		return a.listKey(d, msg)
	}
	switch s {
	case "enter":
		if len(d.buttons) > 0 {
			return d.buttons[d.focus].run(a)
		}
		return d.cancel(a)
	case "tab", "right":
		if n := len(d.buttons); n > 0 {
			d.focus = (d.focus + 1) % n
		}
		return nil
	case "shift+tab", "left":
		if n := len(d.buttons); n > 0 {
			d.focus = (d.focus + n - 1) % n
		}
		return nil
	case "up", "k":
		d.scroll--
		return nil
	case "down", "j":
		d.scroll++
		return nil
	case "pgup":
		d.scroll -= 10
		return nil
	case "pgdown", " ":
		d.scroll += 10
		return nil
	}
	if d.toggleKey != "" && s == d.toggleKey {
		d.toggled = !d.toggled
		return nil
	}
	for _, b := range d.buttons {
		if b.key != "" && s == b.key {
			return b.run(a)
		}
	}
	return nil
}

// listKey handles keys for a pick-list dialog.
func (a *App) listKey(d *dialog, msg tea.KeyMsg) tea.Cmd {
	n := len(d.list)
	move := func(delta int) {
		if n == 0 {
			return
		}
		for i := 0; i < n; i++ {
			d.listCursor = (d.listCursor + delta + n) % n
			if !d.list[d.listCursor].disabled {
				return
			}
		}
	}
	s := msg.String()
	switch s {
	case "up", "k", "shift+tab", "ctrl+p":
		move(-1)
	case "down", "j", "tab", "ctrl+n":
		move(1)
	case "home", "g":
		d.listCursor = n - 1
		move(1)
	case "end", "G":
		d.listCursor = 0
		move(-1)
	case "enter", "right", "l":
		if n > 0 && !d.list[d.listCursor].disabled {
			return d.onPick(a, d.listCursor)
		}
	case "q":
		return d.cancel(a)
	default:
		for _, act := range d.listActions {
			if s == act.key && (!act.needItem || n > 0) {
				return act.run(a, d.listCursor)
			}
		}
		if d.listKeyed() {
			for i, it := range d.list {
				if it.key != "" && s == it.key && !it.disabled {
					d.listCursor = i
					return d.onPick(a, i)
				}
			}
			return nil // keyed lists (menus) pick by letter, not number
		}
		if len(msg.Runes) == 1 && msg.Runes[0] >= '1' && msg.Runes[0] <= '9' {
			i := int(msg.Runes[0] - '1')
			if i < n && !d.list[i].disabled {
				d.listCursor = i
				return d.onPick(a, i)
			}
		}
	}
	return nil
}

// listKeyed reports a list whose items have their own keys (a menu).
func (d *dialog) listKeyed() bool {
	for _, it := range d.list {
		if it.key != "" {
			return true
		}
	}
	return false
}

// ---- rendering --------------------------------------------------------------

// boxWidth returns the content width for a dialog on a w-wide screen.
func boxWidth(pref, screenW int) int {
	if pref == 0 {
		pref = 64
	}
	maxW := screenW - 6 // border + padding + margin
	if pref > maxW {
		pref = maxW
	}
	if pref < 10 {
		pref = 10
	}
	return pref
}

// render draws the dialog box for a screen of the given size.
func (a *App) renderDialog(d *dialog) string {
	th := a.theme
	w := boxWidth(d.width, a.width)

	var body []string
	for _, l := range d.body {
		switch l.kind {
		case lnBlank:
			body = append(body, "")
		case lnRaw:
			body = append(body, l.text)
		case lnPath:
			body = append(body, th.DialogPath.Render(truncLeft(sanitize(l.text), w)))
		default:
			st := th.DialogText
			if l.kind == lnWarn {
				st = th.DialogWarn
			} else if l.kind == lnHint {
				st = th.DialogHint
			}
			for _, wl := range wrapText(sanitize(l.text), w) {
				body = append(body, st.Render(wl))
			}
		}
	}

	type pending struct {
		kind  hitKind
		index int
		line  int // body index (list) or tail index (others)
		x0    int // content column
		w     int
	}
	var bodyHits, tailHits []pending
	if d.list != nil {
		labelW := 0
		for _, it := range d.list {
			labelW = max(labelW, runewidth.StringWidth(it.label))
		}
		keyed := d.listKeyed()
		if len(d.list) == 0 && d.listEmpty != "" {
			for _, wl := range wrapText(sanitize(d.listEmpty), w) {
				body = append(body, th.DialogHint.Render(wl))
			}
		}
		for i, it := range d.list {
			// The key column is always followed by a separating space: a
			// three-cell key ("esc") must never run into its label
			// ("escLeave the archive").
			num := "    "
			if keyed {
				num = runewidth.FillRight(it.key, 3) + " "
			} else if i < 9 {
				num = string(rune('1'+i)) + "   "
			}
			row := num + it.label + strings.Repeat(" ", labelW-runewidth.StringWidth(it.label))
			if it.detail != "" {
				row += "  " + it.detail
			}
			row = truncRight(sanitize(row), w)
			row += strings.Repeat(" ", max(0, w-ansi.StringWidth(row)))
			bodyHits = append(bodyHits, pending{hitListItem, i, len(body), 0, w})
			switch {
			case i == d.listCursor:
				body = append(body, th.ButtonFocus.Padding(0).Render(row))
			case it.disabled:
				body = append(body, th.DialogHint.Render(row))
			case it.danger:
				body = append(body, th.DialogWarn.Render(row))
			default:
				body = append(body, th.DialogText.Render(row))
			}
		}
		if d.listCursor < len(d.list) && d.list[d.listCursor].info != "" {
			body = append(body, "", th.DialogPath.Render(truncLeft(sanitize(d.list[d.listCursor].info), w)))
		}
		// Tips get the height of the tallest one, so the box does not
		// change size (and jump) while the cursor moves.
		tipH := 0
		for _, it := range d.list {
			if it.tip != "" {
				tipH = max(tipH, len(wrapText(sanitize(it.tip), w)))
			}
		}
		if tipH > 0 {
			var tl []string
			if d.listCursor < len(d.list) && d.list[d.listCursor].tip != "" {
				tl = wrapText(sanitize(d.list[d.listCursor].tip), w)
			}
			for i := 0; i < tipH; i++ {
				l := ""
				if i < len(tl) {
					l = tl[i]
				}
				body = append(body, th.DialogHint.Render(l))
			}
		}
	}

	var tail []string
	if d.toggleLabel != "" {
		box := "[ ]"
		if d.toggled {
			box = "[x]"
		}
		tail = append(tail, "")
		tailHits = append(tailHits, pending{hitToggle, 0, len(tail), 0, runewidth.StringWidth(box + " " + d.toggleLabel)})
		tail = append(tail, th.DialogText.Render(box+" "+d.toggleLabel))
	}
	for si, sel := range d.selectors {
		if si == 0 {
			tail = append(tail, "")
		}
		label := runewidth.FillRight(sel.label, 9)
		val := sanitize(sel.value())
		if sel.prev == nil && sel.next == nil {
			tail = append(tail, th.DialogText.Render(truncRight(label+val, w)))
		} else {
			helpS := ""
			if sel.help != "" {
				helpS = "   " + sel.help
			}
			room := max(1, w-runewidth.StringWidth(label)-4) // "◂ " + " ▸"
			if runewidth.StringWidth(val)+runewidth.StringWidth(helpS) > room {
				helpS = ""
			}
			chip := "◂ " + truncRight(val, room) + " ▸"
			lw, cw := runewidth.StringWidth(label), runewidth.StringWidth(chip)
			tailHits = append(tailHits, pending{hitSelPrev, si, len(tail), 0, lw + 2})
			tailHits = append(tailHits, pending{hitSelNext, si, len(tail), lw + 2, cw - 2})
			tail = append(tail, th.DialogText.Render(label)+th.ButtonFocus.Padding(0).Render(chip)+th.DialogHint.Render(helpS))
		}
		if sel.note != nil {
			if note, isWarn := sel.note(); note != "" {
				st := th.DialogHint
				if isWarn {
					st = th.DialogWarn
				}
				for _, wl := range wrapText(sanitize(note), max(1, w-9)) {
					tail = append(tail, st.Render(strings.Repeat(" ", 9)+wl))
				}
			}
		}
	}
	if d.input != nil {
		tail = append(tail, "")
		if d.inputLabel != "" {
			tail = append(tail, th.DialogText.Render(d.inputLabel))
		}
		tail = append(tail, d.input.View(th, w))
		for ti, t := range d.inToggles {
			box := "[ ]"
			if t.on {
				box = "[x]"
			}
			tailHits = append(tailHits, pending{hitInToggle, ti, len(tail), 0, min(w, runewidth.StringWidth(box+" "+t.label+" ("+t.help+")"))})
			tail = append(tail, th.DialogText.Render(truncRight(box+" "+t.label+" ("+t.help+")", w)))
		}
	}
	if d.errLine != "" {
		for _, wl := range wrapText("✗ "+sanitize(d.errLine), w) {
			tail = append(tail, th.StatusErr.Render(wl))
		}
	}
	if len(d.buttons) > 0 {
		var bs []string
		for i, b := range d.buttons {
			st := th.Button
			if i == d.focus && d.input == nil {
				st = th.ButtonFocus
				if b.danger {
					st = th.ButtonDangerFocus
				}
			}
			bs = append(bs, st.Render(b.label))
		}
		tail = append(tail, "")
		if row := strings.Join(bs, "  "); lipgloss.Width(row) <= w {
			x := 0
			for i, b := range bs {
				tailHits = append(tailHits, pending{hitButton, i, len(tail), x, lipgloss.Width(b)})
				x += lipgloss.Width(b) + 2
			}
			tail = append(tail, row)
		} else {
			for i, b := range bs { // stack on narrow screens
				tailHits = append(tailHits, pending{hitButton, i, len(tail), 0, lipgloss.Width(b)})
				tail = append(tail, b)
			}
		}
	}
	if d.footer != "" {
		tail = append(tail, th.DialogHint.Render(truncRight(d.footer, w)))
	}

	// Scroll the body if everything doesn't fit the screen.
	avail := a.height - 4 - 2 - len(tail) // borders, title + gap
	if avail < 1 {
		avail = 1
	}
	if len(body) > avail {
		maxScroll := len(body) - avail
		if d.scroll > maxScroll {
			d.scroll = maxScroll
		}
		if d.scroll < 0 {
			d.scroll = 0
		}
		more := th.DialogHint.Render("  ↑↓ scroll")
		body = append(body[d.scroll:d.scroll+avail-1], more)
		d.scrollable = true
	} else {
		d.scroll = 0
		d.scrollable = false
	}
	shown := len(body)

	titleSt := th.DialogTitle
	boxSt := th.DialogBox
	if d.danger {
		titleSt, boxSt = th.DialogDangerTitle, th.DialogDangerBox
	}
	lines := append([]string{titleSt.Render(truncRight(sanitize(d.title), w)), ""}, body...)
	lines = append(lines, tail...)
	box := boxSt.Width(w + 4).Render(strings.Join(lines, "\n"))

	// Record clickable regions in screen coordinates, using the same
	// placement as overlay(): content line L of the box is at screen row
	// oy+1+L (top border) and content column c at ox+3+c (border+padding).
	ox, oy := overlayOrigin(box, a.width, a.height)
	d.boxY0, d.boxY1 = oy, oy+min(strings.Count(box, "\n")+1, a.height)
	d.hits = d.hits[:0]
	add := func(pd pending, line int) {
		y := oy + 1 + line
		if y < 0 || y >= a.height {
			return
		}
		d.hits = append(d.hits, dialogHit{kind: pd.kind, index: pd.index, x0: ox + 3 + pd.x0, x1: ox + 3 + pd.x0 + pd.w, y: y})
	}
	for _, pd := range bodyHits {
		bi := pd.line
		if d.scrollable {
			if bi < d.scroll || bi >= d.scroll+shown-1 {
				continue // scrolled out of view
			}
			bi -= d.scroll
		}
		add(pd, 2+bi)
	}
	for _, pd := range tailHits {
		add(pd, 2+shown+pd.line)
	}
	return box
}

// overlayOrigin returns where overlay() places box on a w×h screen.
func overlayOrigin(box string, w, h int) (x, y int) {
	lines := strings.Split(box, "\n")
	bw := 0
	for _, l := range lines {
		bw = max(bw, ansi.StringWidth(l))
	}
	bw = min(bw, w)
	bh := min(len(lines), h)
	return (w - bw) / 2, (h - bh) / 2
}

// hitAt returns the clickable region at screen position (x, y).
func (d *dialog) hitAt(x, y int) *dialogHit {
	for i := range d.hits {
		if hh := &d.hits[i]; hh.y == y && x >= hh.x0 && x < hh.x1 {
			return hh
		}
	}
	return nil
}

// overlay centres box over a dimmed, plain-text copy of bg.
func overlay(bg, box string, w, h int, dim lipgloss.Style) string {
	bgLines := strings.Split(bg, "\n")
	for len(bgLines) < h {
		bgLines = append(bgLines, "")
	}
	bgLines = bgLines[:h]
	boxLines := strings.Split(box, "\n")
	bw := 0
	for _, l := range boxLines {
		if lw := ansi.StringWidth(l); lw > bw {
			bw = lw
		}
	}
	if bw > w {
		bw = w
	}
	bh := len(boxLines)
	if bh > h {
		boxLines, bh = boxLines[:h], h
	}
	x, y := (w-bw)/2, (h-bh)/2
	out := make([]string, h)
	for i, l := range bgLines {
		plain := ansi.Strip(l)
		if i < y || i >= y+bh {
			out[i] = dim.Render(cutCols(plain, 0, w))
			continue
		}
		bl := boxLines[i-y]
		if lw := ansi.StringWidth(bl); lw < bw {
			bl += strings.Repeat(" ", bw-lw)
		} else if lw > bw {
			bl = ansi.Truncate(bl, bw, "")
		}
		out[i] = dim.Render(cutCols(plain, 0, x)) + bl + dim.Render(cutCols(plain, x+bw, w))
	}
	return strings.Join(out, "\n")
}

// cutCols returns the cells [from, to) of a plain (ANSI-free) string, padded
// with spaces; wide characters straddling a boundary become spaces.
func cutCols(s string, from, to int) string {
	if to <= from {
		return ""
	}
	var b strings.Builder
	col := 0
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if rw == 0 {
			continue
		}
		if col >= to {
			break
		}
		switch {
		case col >= from && col+rw <= to:
			b.WriteRune(r)
		case col+rw > from && col < to:
			// partially visible wide rune
			for c := col; c < col+rw; c++ {
				if c >= from && c < to {
					b.WriteByte(' ')
				}
			}
		}
		col += rw
	}
	if col < from {
		col = from
	}
	if col < to {
		b.WriteString(strings.Repeat(" ", to-col))
	}
	return b.String()
}

// sanitize replaces control characters (e.g. in hostile file names) so they
// cannot corrupt the terminal.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return '?'
		}
		return r
	}, s)
}

func truncRight(s string, w int) string {
	if ansi.StringWidth(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

// truncLeft keeps the end of s (the most specific part of a path).
func truncLeft(s string, w int) string {
	if runewidth.StringWidth(s) <= w {
		return s
	}
	rs := []rune(s)
	used := 1 // "…"
	i := len(rs)
	for i > 0 {
		rw := runewidth.RuneWidth(rs[i-1])
		if used+rw > w {
			break
		}
		used += rw
		i--
	}
	return "…" + string(rs[i:])
}

// wrapText wraps plain text at spaces to width w (hard-breaking long words).
func wrapText(s string, w int) []string {
	if s == "" {
		return []string{""}
	}
	var out []string
	for _, para := range strings.Split(s, "\n") {
		var line []rune
		lineW := 0
		for _, word := range strings.Fields(para) {
			ww := runewidth.StringWidth(word)
			if lineW > 0 && lineW+1+ww > w {
				out = append(out, string(line))
				line, lineW = nil, 0
			}
			for ww > w { // hard-break very long words (paths)
				cut := []rune(word)
				n, cw := 0, 0
				for n < len(cut) && cw+runewidth.RuneWidth(cut[n]) <= w-lineW {
					cw += runewidth.RuneWidth(cut[n])
					n++
				}
				if n == 0 {
					if lineW == 0 {
						n = 1 // a single rune wider than w: emit it anyway
					} else {
						out = append(out, string(line))
						line, lineW = nil, 0
						continue
					}
				}
				out = append(out, string(append(line, cut[:n]...)))
				line, lineW = nil, 0
				word = string(cut[n:])
				ww = runewidth.StringWidth(word)
			}
			if lineW > 0 {
				line = append(line, ' ')
				lineW++
			}
			line = append(line, []rune(word)...)
			lineW += ww
		}
		out = append(out, string(line))
	}
	return out
}
