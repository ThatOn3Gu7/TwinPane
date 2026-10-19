package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/panel"
	"github.com/ThatOn3Gu7/TwinPane/internal/scrollbar"
)

// The help screen (?) is a fixed frame: the four borders, the title row,
// the filter row and the footer always stay where they are; only the body
// rows between them scroll. The frame size is computed from the screen
// (which shrinks while the on-screen keyboard is up) and from the full,
// unfiltered help, so it does not jump while a filter is typed.
//
//	╭─ TwinPane — keyboard controls ─── / filter ─╮  title in the border
//	│ Filter: nav█                           3/62 │  only while filtering
//	│ Navigation                                  │  ┐
//	│ Tab       switch active panel               ┃  │ body viewport: the only
//	│ ↑/k       move up                           ┃  │ part that scrolls; the
//	│ …                                           │  ┘ thumb shows the position
//	╰───────────────────────────────── 1–20/42 ───╯  position in the border
//
// The frame (outer size, borders) stays the same while filtering: the
// filter row takes the first interior row from the body viewport.
// "/" filters live (case-insensitive, all words must match the key labels,
// key names, descriptions or section titles). The help data is rebuilt on
// every render and never modified: clearing the filter shows everything.

// helpState is the help screen's own state besides showHelp/helpScroll.
type helpState struct {
	filter *lineInput // nil: no filter
	typing bool       // keys go to the filter field
	rows   int        // body viewport height at the last render (paging)
	// screen position of the filter row at the last render (taps)
	filterY, filterX0, filterX1 int
}

// helpEntry is one help row plus its continuation rows (Help == "", e.g.
// the wrapped editor save/quit keys), matched as a unit.
type helpEntry struct {
	section string
	rows    []Binding
	hay     string // lower-case text the filter matches
}

// helpColumns returns the help sections in their two columns. The slices
// are new on every call (helpSections builds them), so appending here
// does not touch shared data.
func (a *App) helpColumns() [2][]helpSection {
	cols := a.keys.helpSections()
	if a.comparison != nil {
		cols[1] = append(cols[1], helpSection{"Directory comparison", comparisonHelp})
	}
	if a.folderSize != nil {
		cols[1] = append(cols[1], helpSection{"Folder-size analysis", folderSizeHelp})
	}
	cols[1] = append(cols[1], helpSection{"Search workflow", searchModeHelp})
	if a.advancedSearch != nil {
		cols[1] = append(cols[1], helpSection{"Advanced Search", advancedSearchHelp})
	}
	cols[0] = append(cols[0], helpSection{"Mouse & touch", mouseHelp})
	editor := a.editorHelp()
	if a.viewer != nil && a.viewer.fromAdvanced {
		editor = helpSection{"Read-only search inspection", advancedInspectionHelp}
	}
	cols[1] = append(cols[1], editor, helpSection{"Help & dialogs", helpScreenHelp})
	return cols
}

// Search bindings live in their own overlays, not in the normal panel key map.
// Document the bridge here so / remains familiar while Advanced Search is
// still easy to discover.
var searchModeHelp = []Binding{
	{Help: "/", Desc: "open the existing live filename search"},
	{Help: "Ctrl+G in /", Desc: "open Advanced Search with the current query"},
	{Help: "Ctrl+T", Desc: "toggle filename / content mode in Advanced Search"},
	{Help: "Esc", Desc: "close search and keep both panels unchanged"},
}

var advancedSearchHelp = []Binding{
	{Help: "type", Desc: "filter relative paths by fuzzy character sequence"},
	{Help: "Ctrl+T", Desc: "switch between filename and content search; query is preserved"},
	{Help: "↑ / ↓", Desc: "move through results; the preview follows the selection"},
	{Help: "Enter", Desc: "inspect a file read-only; Esc returns to the same results"},
	{Help: "CONTENT", Desc: "smart-case search; matching lines and distinct highlights are shown"},
	{Help: ".git", Desc: "exact .git directories are skipped, including nested repositories"},
	{Help: "LIMITS", Desc: "binary files are skipped; traversal, byte and result caps are reported"},
	{Help: "Esc", Desc: "cancel the active scan and return without changing panel state"},
}

var advancedInspectionHelp = []Binding{
	{Help: "n / N", Desc: "next / previous content match"},
	{Help: "e / F4", Desc: "editing is disabled during search inspection"},
	{Help: "Esc", Desc: "return to the same Advanced Search results"},
}

// helpScreenHelp documents the help screen itself and the dialog keys
// (help-only rows, like mouseHelp).
var helpScreenHelp = []Binding{
	{Help: "/", Desc: "filter this help (live)"},
	{Help: "⏎ / Esc", Desc: "keep / clear the filter"},
	{Help: "↑↓ PgDn", Desc: "scroll the help (swipe too)"},
	{Help: "Esc", Desc: "close help (or any key)"},
	{Help: "⏎", Desc: "dialogs: highlighted button"},
	{Help: "Esc", Desc: "dialogs: cancel"},
}

func helpEntries(cols [2][]helpSection) []helpEntry {
	var out []helpEntry
	for _, col := range cols {
		for _, sec := range col {
			first := len(out)
			for _, b := range sec.Bindings {
				if b.Help == "" && len(out) > first {
					e := &out[len(out)-1]
					e.rows = append(e.rows, b)
					e.hay += " " + strings.ToLower(b.Desc+" "+strings.Join(b.Keys, " "))
					continue
				}
				out = append(out, helpEntry{section: sec.Title, rows: []Binding{b},
					hay: strings.ToLower(sec.Title + " " + b.Help + " " + b.Desc + " " + strings.Join(b.Keys, " "))})
			}
		}
	}
	return out
}

// helpMatch reports whether every word of query occurs in e (case-
// insensitive). An empty query matches everything.
func helpMatch(e helpEntry, query string) bool {
	for _, term := range strings.Fields(strings.ToLower(query)) {
		if !strings.Contains(e.hay, term) {
			return false
		}
	}
	return true
}

// helpRowLines renders one help row, wrapping the description below its
// key label when the row is wider than w.
func helpRowLines(b Binding, w int) []string {
	label := fmt.Sprintf("%-9s ", b.Help)
	line := label + b.Desc
	if w <= 0 || ansi.StringWidth(line) <= w {
		return []string{line}
	}
	lw := ansi.StringWidth(label)
	if w-lw < 8 { // label too wide for the rest: wrap the whole line
		return wrapText(line, w)
	}
	var out []string
	for i, part := range wrapText(b.Desc, w-lw) {
		if i == 0 {
			out = append(out, label+part)
		} else {
			out = append(out, strings.Repeat(" ", lw)+part)
		}
	}
	return out
}

// sectionLines renders sections one below the other (w <= 0: no wrapping).
func (a *App) sectionLines(secs []helpSection, w int) []string {
	var lines []string
	for i, sec := range secs {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, a.theme.HelpTitle.Render(sec.Title))
		for _, b := range sec.Bindings {
			lines = append(lines, helpRowLines(b, w)...)
		}
	}
	return lines
}

func maxWidth(lines []string) int {
	w := 0
	for _, l := range lines {
		w = max(w, ansi.StringWidth(l))
	}
	return w
}

// helpFull lays out the unfiltered help for a screen maxInner cells wide
// (inside the frame): two columns when they fit (an 80-column terminal),
// otherwise stacked with long rows wrapped. It returns the lines and the
// natural content width.
func (a *App) helpFull(cols [2][]helpSection, maxInner int) ([]string, int) {
	if a.width >= 80 {
		left, right := a.sectionLines(cols[0], 0), a.sectionLines(cols[1], 0)
		lw, rw := maxWidth(left), maxWidth(right)
		if lw+3+rw <= maxInner {
			n := max(len(left), len(right))
			lines := make([]string, n)
			for i := range lines {
				var l, r string
				if i < len(left) {
					l = left[i]
				}
				if i < len(right) {
					r = right[i]
				}
				lines[i] = l + strings.Repeat(" ", lw-ansi.StringWidth(l)+3) + r
			}
			return lines, lw + 3 + rw
		}
	}
	all := append(append([]helpSection(nil), cols[0]...), cols[1]...)
	natural := maxWidth(a.sectionLines(all, 0))
	w := min(natural, maxInner)
	return a.sectionLines(all, w), w
}

// helpFiltered lays out the entries matching query in one column, grouped
// under their section titles. n is the number of matching entries.
func (a *App) helpFiltered(entries []helpEntry, query string, w int) (lines []string, n int) {
	section := ""
	for _, e := range entries {
		if !helpMatch(e, query) {
			continue
		}
		n++
		if e.section != section {
			if section != "" {
				lines = append(lines, "")
			}
			section = e.section
			lines = append(lines, a.theme.HelpTitle.Render(e.section))
		}
		for _, b := range e.rows {
			lines = append(lines, helpRowLines(b, w)...)
		}
	}
	if n == 0 {
		for _, l := range wrapText(fmt.Sprintf("No shortcuts match %q.", strings.TrimSpace(query)), w) {
			lines = append(lines, a.theme.DialogWarn.Render(l))
		}
		lines = append(lines, "")
		for _, l := range wrapText("Backspace edits the filter · Esc shows all shortcuts again.", w) {
			lines = append(lines, a.theme.DialogHint.Render(l))
		}
	}
	return lines, n
}

// fitCells truncates s (which may contain styles) to w cells and pads it.
func fitCells(s string, w int) string {
	if ansi.StringWidth(s) > w {
		s = ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", max(0, w-ansi.StringWidth(s)))
}

// helpView renders the help screen into exactly h rows of a.width cells.
func (a *App) helpView(h int) string {
	th := a.theme
	hs := &a.help
	hs.filterY = -1
	if h < 3 || a.width < 8 {
		return strings.Repeat("\n", max(0, h-1))
	}
	maxInner := a.width - 4 // border + one padding cell on each side
	cols := a.helpColumns()
	full, natural := a.helpFull(cols, maxInner)
	inner := min(maxInner, max(natural, min(40, maxInner)))

	// Interior rows: fixed, from the unfiltered help and the screen; at
	// least 2 when possible so the filter row and one body row fit.
	interior := min(len(full), h-2)
	interior = max(interior, min(2, h-2))
	filtering := hs.filter != nil
	rows := interior // body viewport
	if filtering && interior >= 2 {
		rows = interior - 1
	}

	// The body: everything, or the filter results.
	body := full
	matches, total := 0, 0
	if filtering {
		entries := helpEntries(cols)
		total = len(entries)
		body, matches = a.helpFiltered(entries, hs.filter.Value(), inner)
	}
	maxScroll := max(0, len(body)-rows)
	a.helpScroll = max(0, min(a.helpScroll, maxScroll))
	a.helpScrollable = len(body) > rows
	hs.rows = rows

	bst := lipgloss.NewStyle().Foreground(th.HelpBox.GetBorderTopForeground())
	bd := lipgloss.RoundedBorder()
	edge := bst.Render(bd.Left)
	rightEdge := bst.Render(bd.Right)
	row := func(content, right string) string {
		return edge + " " + fitCells(content, inner) + " " + right
	}
	var box []string
	box = append(box, a.helpTopBorder(bst, inner))
	filterRow := -1
	if filtering && interior >= 2 {
		filterRow = len(box)
		box = append(box, row(a.helpFilterLine(inner, matches, total), rightEdge))
	}
	var thumb []bool
	if a.helpScrollable {
		thumb = scrollbar.Cells(len(body), rows, a.helpScroll, rows)
	}
	for i := 0; i < rows; i++ {
		line := ""
		if j := a.helpScroll + i; j < len(body) {
			line = body[j]
		}
		right := rightEdge
		if i < len(thumb) && thumb[i] {
			right = th.ScrollThumb.Render(panel.ScrollThumbChar)
		}
		box = append(box, row(line, right))
	}
	// bottom border, with the scroll position when there is more
	bottom := bd.BottomLeft + strings.Repeat(bd.Bottom, inner+2) + bd.BottomRight
	if a.helpScrollable {
		pos := fmt.Sprintf(" %d–%d/%d ", a.helpScroll+1, min(a.helpScroll+rows, len(body)), len(body))
		if pw := ansi.StringWidth(pos); pw+4 <= inner+2 {
			bottom = bd.BottomLeft + strings.Repeat(bd.Bottom, inner+2-pw-3) + pos + strings.Repeat(bd.Bottom, 3) + bd.BottomRight
		}
	}
	box = append(box, bst.Render(bottom))

	// Place the box: centred, never larger than the area.
	boxW := inner + 4
	left := max(0, (a.width-boxW)/2)
	top := max(0, (h-len(box))/2)
	out := make([]string, 0, h)
	for i := 0; i < top; i++ {
		out = append(out, "")
	}
	pad := strings.Repeat(" ", left)
	for _, l := range box {
		out = append(out, pad+l)
	}
	for len(out) < h {
		out = append(out, "")
	}
	// Tap target for starting the filter: the filter row, or the top
	// border (where "/ filter" is shown). The title bar is screen row 0.
	hs.filterY = 1 + top
	if filterRow >= 0 {
		hs.filterY = 1 + top + filterRow
	}
	hs.filterX0, hs.filterX1 = left, left+boxW
	return strings.Join(out[:h], "\n")
}

// helpTopBorder is the top border with the title on the left and the
// filter hint on the right, shortened to what fits.
func (a *App) helpTopBorder(bst lipgloss.Style, inner int) string {
	th := a.theme
	bd := lipgloss.RoundedBorder()
	span := inner + 2 // border cells between the corners
	title := " TwinPane — keyboard controls "
	tag := " / filter "
	if a.help.filter != nil {
		tag = " filtering "
	}
	// "─" + title + at least one "─" + tag + "─"
	fits := func(title, tag string) bool { return 3+ansi.StringWidth(title)+ansi.StringWidth(tag) <= span }
	switch {
	case fits(title, tag):
	case fits(" TwinPane help ", tag):
		title = " TwinPane help "
	case fits(title, ""):
		tag = ""
	default:
		tag = ""
		title = ansi.Truncate(title, max(0, span-2), "…")
	}
	fill := span - 1 - ansi.StringWidth(title) - ansi.StringWidth(tag)
	if tag != "" {
		fill--
	}
	if fill < 0 {
		return bst.Render(bd.TopLeft + strings.Repeat(bd.Top, span) + bd.TopRight)
	}
	var b strings.Builder
	b.WriteString(bst.Render(bd.TopLeft + bd.Top))
	b.WriteString(th.HelpTitle.Render(title))
	b.WriteString(bst.Render(strings.Repeat(bd.Top, fill)))
	if tag != "" {
		b.WriteString(th.DialogHint.Render(tag))
		b.WriteString(bst.Render(bd.Top))
	}
	b.WriteString(bst.Render(bd.TopRight))
	return b.String()
}

// helpFilterLine is the filter row: a hint when no filter is set, else
// the field (with a cursor while typing) and the number of matches.
func (a *App) helpFilterLine(w, matches, total int) string {
	th := a.theme
	hs := &a.help
	label := "Filter: "
	count := fmt.Sprintf(" %d/%d", matches, total)
	fw := w - ansi.StringWidth(label) - ansi.StringWidth(count)
	if fw < 4 { // narrow: drop the count first
		count = ""
		fw = w - ansi.StringWidth(label)
	}
	var field string
	if hs.typing {
		field = hs.filter.View(th, max(2, fw))
	} else {
		field = th.Input.Render(fitCells(hs.filter.Value(), max(2, fw)))
	}
	return th.HelpTitle.Render(label) + field + th.DialogHint.Render(count)
}

// openHelp shows the help screen, unfiltered and scrolled to the top.
func (a *App) openHelp() {
	a.showHelp = true
	a.helpScroll = 0
	a.help.filter, a.help.typing = nil, false
}

// closeHelp hides the help screen and forgets its filter.
func (a *App) closeHelp() {
	a.showHelp = false
	a.help.filter, a.help.typing = nil, false
}

// clearHelpFilter removes the filter: the full help is shown again.
func (a *App) clearHelpFilter() {
	a.help.filter, a.help.typing = nil, false
	a.helpScroll = 0
}

// startHelpFilter puts the keyboard focus on the filter field.
func (a *App) startHelpFilter() {
	if a.help.filter == nil {
		a.help.filter = newLineInput("")
	}
	a.help.typing = true
}

func (a *App) helpPage() int { return max(1, a.help.rows-1) }

// syncHelpLayout recomputes the help layout state (scrollable, viewport
// rows, tap targets) for the current screen and filter. Key and mouse
// handling call it first, so their decisions never depend on whether a
// frame was rendered since the last resize or key (Bubble Tea renders on
// a timer, several messages can arrive in between).
func (a *App) syncHelpLayout() {
	if a.width >= minWidth && a.height >= minHeight {
		a.helpView(a.height - 3)
	}
}

// helpKey handles a key while the help screen is shown. While the filter
// field has the focus it receives the raw message (bursts and pastes
// included, see Update); keys never reach the panels.
func (a *App) helpKey(msg tea.KeyMsg) tea.Cmd {
	a.syncHelpLayout()
	hs := &a.help
	s := msg.String()
	if hs.typing {
		switch s {
		case "esc":
			a.clearHelpFilter()
		case "enter":
			hs.typing = false
			if strings.TrimSpace(hs.filter.Value()) == "" {
				a.clearHelpFilter()
			}
		case "up":
			a.helpScroll--
		case "down":
			a.helpScroll++
		case "pgup":
			a.helpScroll -= a.helpPage()
		case "pgdown":
			a.helpScroll += a.helpPage()
		case "backspace", "ctrl+h":
			if hs.filter.Value() == "" {
				a.clearHelpFilter() // Backspace on an empty field leaves it
				return nil
			}
			fallthrough
		default:
			before := hs.filter.Value()
			hs.filter.Update(msg)
			if hs.filter.Value() != before {
				a.helpScroll = 0 // new results: start at the top
			}
		}
		return nil
	}
	switch {
	case s == "/":
		a.startHelpFilter()
		return nil
	case s == "esc" && hs.filter != nil:
		a.clearHelpFilter() // the first Esc clears the filter, the next closes
		return nil
	}
	// The scroll keys scroll when there is more to see; any other key
	// closes the help.
	if a.helpScrollable {
		switch s {
		case "up", "k":
			a.helpScroll--
			return nil
		case "down", "j":
			a.helpScroll++
			return nil
		case "pgup", "ctrl+b":
			a.helpScroll -= a.helpPage()
			return nil
		case "pgdown", "ctrl+f", " ":
			a.helpScroll += a.helpPage()
			return nil
		case "home", "g":
			a.helpScroll = 0
			return nil
		case "end", "G":
			a.helpScroll = 1 << 20 // clamped by helpView
			return nil
		}
	}
	a.closeHelp()
	return nil
}

// helpMouse: the wheel (a swipe in Termux) scrolls the body, a tap on the
// filter row starts filtering, any other tap closes the help.
func (a *App) helpMouse(ev tea.MouseEvent) tea.Cmd {
	if ev.IsWheel() {
		a.helpScroll += wheelDelta(ev) * wheelStep
		return nil
	}
	if ev.Action != tea.MouseActionPress {
		return nil
	}
	a.syncHelpLayout()
	hs := &a.help
	if ev.Button == tea.MouseButtonLeft && hs.filterY >= 0 && ev.Y == hs.filterY && ev.X >= hs.filterX0 && ev.X < hs.filterX1 {
		a.startHelpFilter()
		return nil
	}
	a.closeHelp()
	return nil
}

// helpHints are the hint bar items of the help screen.
func (a *App) helpHints() []barItem {
	hs := &a.help
	switch {
	case hs.typing:
		return []barItem{item("⏎", "keep results", "enter"), item("Esc", "clear filter", "esc"), item("↑↓", "scroll", "")}
	case hs.filter != nil:
		items := []barItem{item("/", "edit filter", "/"), item("Esc", "show all", "esc")}
		if a.helpScrollable {
			items = append(items, item("↑↓", "scroll", ""))
		}
		return items
	case a.helpScrollable:
		return []barItem{item("/", "filter", "/"), item("↑↓", "scroll", ""), item("Esc", "close help (any other key too)", "esc")}
	}
	return []barItem{item("/", "filter", "/"), item("Esc", "close help (any key)", "esc")}
}
