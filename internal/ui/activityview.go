package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/activity"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/panel"
	"github.com/ThatOn3Gu7/TwinPane/internal/scrollbar"
)

// The activity log viewer: a full-body screen on `L`.
//
// One log, several filters. The header names the filter in force and the keys
// change it (`c` cycles the nine categories, `l` the minimum level, `/` types a
// query). The list is the log itself, newest first, so nothing is duplicated
// and nothing is lost by switching filter: it is the same file either way.
//
// The list is grouped by application session — the run ID the log shares with
// the operation history — under a dated rule, so "this session" is separated
// from earlier ones. Headers are drawn lines, not entries: the selection only
// ever points at a real entry, and `[`/`]` jump between the sessions.
//
// Rows are one line when there is room (time, category, message) and two lines
// on a narrow terminal (message above, time/category below). The selected
// entry's details appear under a thin rule instead of a box, so the screen stays
// flat and a short terminal still shows a usable list. A slim scrollbar in the
// last column reports the position in the list and is deliberately separate
// from the selected row's highlight.

const (
	actBoxTitle  = "Activity log"
	actDetailMax = 8 // detail lines at most (below the rule)
	actQueryMax  = 80
	// actNarrow is below which rows take two lines.
	actNarrow = 64
)

// actLineKind is what one drawn line of the list is.
type actLineKind int

const (
	actHeaderLine actLineKind = iota // a session rule (never selectable)
	actEntryLine                     // a line of a log entry
)

// actLine is one line of the laid-out list. Entries carry the index into
// activityView.rows they belong to, so a header can never be mistaken for an
// entry and the selection stays an index into the entries alone.
type actLine struct {
	kind  actLineKind
	row   int          // index into activityView.rows (actEntryLine only)
	part  int          // 0 = first line of the row, 1 = its second line (narrow layout)
	text  string       // the session label (actHeaderLine only)
	t     time.Time    // the session's date (actHeaderLine only)
	span  [2]time.Time // first/last visible entry of the session
	count int          // entries in the session block
}

type activityView struct {
	entries []activity.Entry // snapshot, newest first
	rows    []int            // indices of entries passing the filter

	// lines is the laid-out list: session rules interleaved with entry lines.
	// It is rebuilt when the filter or the width changes, never per frame.
	lines    []actLine
	linesW   int    // width lines was laid out for
	linesKey string // session labels depend on the run and the day
	firstLn  []int  // row index → first line index in lines

	sel int // index into rows; always inside [0, len(rows)-1] (0 when empty)
	top int // first visible line index

	filter   activity.Filter
	catSel   activity.CatFilter
	levelSel activity.LevelFilter
	typing   bool
	typed    string // the query being typed
	query    string // the committed query, for the header

	detail  bool
	listH   int // list lines available in the last frame
	stat    activity.Stat
	readErr string
	rev     uint64
}

// reload takes a snapshot of the log and re-applies the filter.
func (v *activityView) reload(a *App) {
	v.entries, v.stat, v.readErr = nil, activity.Stat{Missing: true}, ""
	path := a.activityPath
	if a.actErr != nil {
		v.readErr = a.actErr.Error()
	}
	if path != "" {
		res := activity.Read(path, 4000)
		v.entries, v.stat = res.Entries, res.Stat
	}
	if a.actLog != nil {
		v.rev = a.actLog.Revision()
		if a.actLog.Err() != nil && v.readErr == "" {
			v.readErr = a.actLog.Err().Error()
		}
	}
	v.rebuild()
}

// rebuild recomputes the effective filter and the entry list, then clamps the
// selection. It is the only place rows changes, so sel can never outlive the
// list it indexes.
func (v *activityView) rebuild() {
	q := activity.Filter{}.ApplyQuery(v.query)
	if q.Cat != activity.FilterAll {
		v.catSel = q.Cat
	}
	if q.Level != activity.LevelsAll {
		v.levelSel = q.Level
	}
	v.filter = activity.Filter{Cat: v.catSel, Level: v.levelSel, Text: q.Text, EvPrefix: q.EvPrefix}
	v.rows = v.rows[:0]
	for i := range v.entries {
		if v.filter.Match(v.entries[i]) {
			v.rows = append(v.rows, i)
		}
	}
	v.clampSel()
	v.lines = nil // the layout depends on rows
}

// clampSel keeps the selection inside the visible entry list. An empty list
// has no selection (0), never -1.
func (v *activityView) clampSel() {
	if len(v.rows) == 0 {
		v.sel, v.top = 0, 0
		return
	}
	if v.sel >= len(v.rows) {
		v.sel = len(v.rows) - 1
	}
	if v.sel < 0 {
		v.sel = 0
	}
}

// layout builds the drawn lines for contentW columns: a session rule before
// each run's entries, then the entries themselves, one or two lines each.
func (v *activityView) layout(contentW int, currentRun string, now time.Time) {
	// The grouping labels depend on which run is "now" and on the day the
	// viewer is looking at, so a midnight or a reload invalidates them too.
	key := currentRun + "|" + now.Format("2006-01-02")
	if v.linesW == contentW && v.linesKey == key && v.lines != nil {
		return
	}
	v.lines = v.lines[:0]
	v.firstLn = v.firstLn[:0]
	twoLines := contentW < actNarrow
	var lastRun string
	for i, ei := range v.rows {
		e := v.entries[ei]
		if i == 0 || e.Run != lastRun {
			lastRun = e.Run
			v.lines = append(v.lines, v.sessionLine(i, contentW, currentRun, now))
		}
		v.firstLn = append(v.firstLn, len(v.lines))
		v.lines = append(v.lines, actLine{kind: actEntryLine, row: i, part: 0})
		if twoLines {
			v.lines = append(v.lines, actLine{kind: actEntryLine, row: i, part: 1})
		}
	}
	v.linesW, v.linesKey = contentW, key
}

// sessionLine is the rule that starts a session block: its date (or "This
// session"), the span of visible entries and how many there are.
func (v *activityView) sessionLine(row, contentW int, currentRun string, now time.Time) actLine {
	run := v.entries[v.rows[row]].Run
	span := [2]time.Time{v.entries[v.rows[row]].Time, v.entries[v.rows[row]].Time}
	count := 0
	for i := row; i < len(v.rows); i++ {
		e := v.entries[v.rows[i]]
		if e.Run != run {
			break
		}
		count++
		if e.Time.Before(span[0]) {
			span[0] = e.Time
		}
		if e.Time.After(span[1]) {
			span[1] = e.Time
		}
	}
	return actLine{kind: actHeaderLine, row: -1, text: run, t: span[0], span: span, count: count}
}

// rowHeight is how many lines the given row occupies (one, or two when the
// terminal is narrow).
func (v *activityView) rowHeight(row int) int {
	if row < 0 || row >= len(v.firstLn) {
		return 1
	}
	start := v.firstLn[row]
	h := 0
	for i := start; i < len(v.lines); i++ {
		if v.lines[i].kind != actEntryLine || v.lines[i].row != row {
			break
		}
		h++
	}
	if h < 1 {
		h = 1
	}
	return h
}

// ensureVisible scrolls so the selected entry is fully on screen, and keeps
// the window inside the list.
func (v *activityView) ensureVisible() {
	if len(v.rows) == 0 {
		v.top = 0
		return
	}
	v.clampSel()
	rows := max(1, v.listH)
	if v.sel >= len(v.firstLn) {
		v.top = 0
		return
	}
	start := v.firstLn[v.sel]
	h := v.rowHeight(v.sel)
	if start < v.top {
		v.top = start
	}
	if start+h > v.top+rows {
		v.top = start + h - rows
	}
	// Cosmetic nudges, each only when it cannot push the selection off the
	// bottom: never start the window on the second line of a two-line row, and
	// never cut a session rule away from the block it introduces.
	keep := start + h // first line below the selected entry
	nudge := func() {
		if v.top > 0 && v.top-1+rows >= keep {
			v.top--
		}
	}
	if v.top > 0 && v.top < len(v.lines) && v.lines[v.top].kind == actEntryLine && v.lines[v.top].part == 1 {
		nudge()
	}
	if v.top > 0 && v.lines[v.top-1].kind == actHeaderLine {
		nudge()
	}
	if maxTop := len(v.lines) - rows; v.top > maxTop {
		v.top = maxTop
	}
	if v.top < 0 {
		v.top = 0
	}
}

// normalize is called after anything that changes the selection, the filter or
// the geometry: it re-lays the list if needed, clamps the selection and scrolls
// it into view. Every key and mouse path ends here, which is what makes an
// out-of-range selection impossible.
func (a *App) activityNormalize() {
	v := a.actView
	if v == nil {
		return
	}
	contentW := a.activityContentWidth()
	v.layout(contentW, a.activityCurrentRun(), a.now())
	v.clampSel()
	v.ensureVisible()
}

// activityContentWidth is the width of the list without the scrollbar column.
func (a *App) activityContentWidth() int {
	if a.width < 20 {
		return max(1, a.width)
	}
	return max(1, a.width-1) // one column for the scrollbar
}

// activityCurrentRun is this run's session ID, as recorded in the log.
func (a *App) activityCurrentRun() string {
	if a.history == nil {
		return ""
	}
	return a.history.CurrentID()
}

// ---- keys --------------------------------------------------------------------

func (a *App) activityKey(msg tea.KeyMsg) tea.Cmd {
	v := a.actView
	if v == nil {
		return nil
	}
	if v.typing {
		return a.activityTypingKey(msg)
	}
	page := max(1, v.listH-1)
	switch msg.String() {
	case "esc", "L", "q":
		return a.closeActivityView()
	case "up", "k":
		v.sel--
	case "down", "j":
		v.sel++
	case "pgup", "ctrl+b":
		v.sel -= page
	case "pgdown", "ctrl+f":
		v.sel += page
	case "home", "g":
		v.sel = 0
	case "end", "G":
		v.sel = len(v.rows) - 1
	case "c":
		v.catSel = v.catSel.Next(1)
		v.rebuild()
		a.setStatus("activity: "+v.catSel.Label(), false)
	case "l":
		v.levelSel = v.levelSel.Next(1)
		v.rebuild()
		a.setStatus("activity: "+v.levelSel.Label(), false)
	case "[":
		a.activityJumpRun(-1)
	case "]":
		a.activityJumpRun(+1)
	case "enter", " ":
		v.detail = !v.detail
	case "/":
		v.typing = true
		v.typed = v.query
	case "x":
		v.query, v.typed = "", ""
		v.catSel, v.levelSel = activity.FilterAll, activity.LevelsAll
		v.rebuild()
		a.setStatus("activity: all entries", false)
	case "r":
		v.reload(a)
		a.setStatus("activity: reloaded", false)
	case "?":
		a.openHelp()
	}
	a.activityNormalize()
	return nil
}

// activityTypingKey handles the query field. The typed text is a filter, and a
// filter is never written to the log: it stays in this field.
func (a *App) activityTypingKey(msg tea.KeyMsg) tea.Cmd {
	v := a.actView
	switch msg.Type {
	case tea.KeyEsc:
		v.typing = false
		return nil
	case tea.KeyEnter:
		v.typing = false
		v.query = strings.ToLower(strings.TrimSpace(v.typed))
		v.rebuild()
		v.sel = 0
		a.activityNormalize()
		return nil
	case tea.KeyBackspace:
		if v.typed != "" {
			r := []rune(v.typed)
			v.typed = string(r[:len(r)-1])
		}
		return nil
	case tea.KeyCtrlU:
		v.typed = ""
		return nil
	case tea.KeySpace:
		v.typed = clipQuery(v.typed + " ")
		return nil
	case tea.KeyRunes:
		v.typed = clipQuery(v.typed + string(msg.Runes))
		return nil
	}
	return nil
}

func clipQuery(s string) string {
	if len(s) > actQueryMax {
		return s[:actQueryMax]
	}
	return s
}

// activityJumpRun moves the selection to the previous or next application
// session (the run ID shared with the operation history).
func (a *App) activityJumpRun(dir int) {
	v := a.actView
	if v == nil || len(v.rows) == 0 {
		return
	}
	cur := v.entries[v.rows[v.sel]].Run
	i := v.sel + dir
	for i >= 0 && i < len(v.rows) {
		if v.entries[v.rows[i]].Run != cur {
			// Step to the first row of the neighbouring run, in this direction.
			for dir < 0 && i > 0 && v.entries[v.rows[i-1]].Run == v.entries[v.rows[i]].Run {
				i--
			}
			for dir > 0 && i < len(v.rows)-1 && v.entries[v.rows[i+1]].Run == v.entries[v.rows[i]].Run {
				i++
			}
			v.sel = i
			a.activityNormalize()
			return
		}
		i += dir
	}
	a.setStatus("activity: no other session in this filter", false)
}

// ---- rendering ---------------------------------------------------------------

// activityRender draws the whole body: the header, the grouped list with its
// scrollbar, and — when asked for — the details under a thin rule.
func (a *App) activityRender(height int) string {
	v := a.actView
	if v == nil || height <= 0 {
		return ""
	}
	width := a.width
	contentW := a.activityContentWidth()

	// Geometry: header + list + optional details (rule + lines).
	detailRows := 0
	if v.detail && len(v.rows) > 0 {
		detailRows = min(actDetailMax, max(2, height/3)) + 1
	}
	v.listH = max(1, height-1-detailRows)
	v.layout(contentW, a.activityCurrentRun(), a.now())
	v.clampSel()
	v.ensureVisible()

	lines := make([]string, 0, height)
	lines = append(lines, a.activityHeader(width, len(v.rows)))

	if len(v.rows) == 0 {
		th := a.theme
		msg := "No entries yet."
		switch {
		case v.readErr != "":
			msg = v.readErr
		case !v.filter.Empty():
			msg = "Nothing matches this filter."
		}
		lines = append(lines, "", " "+th.Empty.Render(ansi.Truncate(msg, max(1, width-2), "…")))
		if !v.filter.Empty() {
			lines = append(lines, " "+th.Summary.Render("x clears the filter"))
		}
		return strings.Join(fitBlock(lines, height, width), "\n")
	}

	// The scrollbar geometry for the whole list, in drawn lines.
	thumbStart, thumbSize, scrolls := scrollbar.Thumb(len(v.lines), v.listH, v.top, v.listH)

	end := min(len(v.lines), v.top+v.listH)
	for i := v.top; i < end; i++ {
		l := v.lines[i]
		var content string
		if l.kind == actHeaderLine {
			content = a.activitySessionHeader(l, contentW)
		} else {
			content = a.activityEntryLine(l, width, contentW)
		}
		lines = append(lines, content+a.activityScrollCell(i-v.top, thumbStart, thumbSize, scrolls, width, contentW))
	}
	if v.detail && detailRows > 0 {
		lines = append(lines, a.activityRule("Details", width, a.theme.Divider))
		for _, l := range fitBlock(a.activityDetailLines(v, contentW, a.now()), detailRows-1, contentW) {
			lines = append(lines, l)
		}
	}
	return strings.Join(fitBlock(lines, height, width), "\n")
}

// activityScrollCell draws the scrollbar column of one list row: a thumb cell,
// the dim track, or nothing when the list fits. It is a single column at the
// right edge, so it can never be mistaken for the selected row's highlight.
func (a *App) activityScrollCell(row, thumbStart, thumbSize int, scrolls bool, width, contentW int) string {
	if width <= contentW {
		return ""
	}
	th := a.theme
	if !scrolls {
		return " "
	}
	if row >= thumbStart && row < thumbStart+thumbSize {
		return th.ScrollThumb.Render(panel.ScrollThumbChar)
	}
	return th.ScrollThumbOff.Render("│")
}

// activityRule draws a labelled horizontal rule, without a box.
func (a *App) activityRule(label string, width int, st lipgloss.Style) string {
	if label == "" {
		return st.Render(fitWidth(strings.Repeat("─", max(1, width)), width))
	}
	head := "── " + label + " "
	fill := max(0, width-lipgloss.Width(head))
	return st.Render(fitWidth(head+strings.Repeat("─", fill), width))
}

// activitySessionHeader draws the rule that separates sessions.
func (a *App) activitySessionHeader(l actLine, width int) string {
	th := a.theme
	name := activitySessionName(l.span, l.count, l.text == a.activityCurrentRun(), a.now())
	head := "── " + name + " "
	fill := max(0, width-lipgloss.Width(head))
	return th.Divider.Render("── ") + th.ColumnHeader.Render(name) +
		th.Divider.Render(" "+strings.Repeat("─", fill))
}

// activitySessionName is the text of a session rule: when it was, how long it
// ran, and how many entries are visible in it.
func activitySessionName(start [2]time.Time, count int, current bool, now time.Time) string {
	label := activityDateLabel(start[0], now)
	if current {
		label = "This session"
	}
	times := start[0].Format("15:04")
	if start[1].Sub(start[0]) >= time.Minute {
		times = start[0].Format("15:04") + "–" + start[1].Format("15:04")
	}
	return fmt.Sprintf("%s · %s · %s", label, times, plural(count, "entry"))
}

// activityDateLabel names the day of t relative to now, with the date when it
// is not today or yesterday.
func activityDateLabel(t, now time.Time) string {
	y, m, d := now.Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, now.Location())
	switch {
	case day.Equal(today):
		return "Today"
	case day.Equal(today.AddDate(0, 0, -1)):
		return "Yesterday"
	case t.Year() == now.Year():
		return t.Format("Mon 2 Jan")
	}
	return t.Format("Mon 2 Jan 2006")
}

// activityHeader is the title line: filter, counts, query field.
func (a *App) activityHeader(width, shown int) string {
	v := a.actView
	th := a.theme
	left := th.HelpTitle.Render(actBoxTitle) + th.Summary.Render(" · "+v.filter.Cat.Label())

	info := fmt.Sprintf("%s shown", plural(shown, "entry"))
	if len(v.entries) != shown {
		info += fmt.Sprintf(" of %d", len(v.entries))
	}
	if v.filter.Level != activity.LevelsAll {
		info += " · " + v.filter.Level.Label()
	}
	if v.stat.Skipped > 0 {
		info += fmt.Sprintf(" · %d unreadable lines skipped", v.stat.Skipped)
	}
	right := th.Summary.Render(info)

	if v.typing {
		left = th.HelpTitle.Render(actBoxTitle) + th.Summary.Render(" · filter: ")
		right = th.StatusInfo.Render(" " + v.typed + "▏ ")
	} else if v.query != "" {
		left += th.Summary.Render(" · /" + v.query)
	}
	gap := width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return ansi.Truncate(left, max(1, width), "…")
	}
	return left + strings.Repeat(" ", gap) + right
}

// activityEntryLine draws one line of an entry: the whole thing on a wide
// terminal, or the message on the first line and time/level on the second one
// when narrow. A selected line is painted with the theme's cursor colour across
// the full content width, so the highlight is visible whatever the row says.
func (a *App) activityEntryLine(l actLine, width, contentW int) string {
	v := a.actView
	if l.row < 0 || l.row >= len(v.rows) {
		return fitWidth("", contentW) // cannot happen; keeps the frame intact
	}
	e := v.entries[v.rows[l.row]]
	th := a.theme
	selected := l.row == v.sel

	glyph, gstyle := " ", th.Summary
	switch e.LevelOf() {
	case activity.LevelWarn:
		glyph, gstyle = "!", th.StatusWarn
	case activity.LevelError:
		glyph, gstyle = "✗", th.StatusErr
	case activity.LevelDebug:
		gstyle = th.Empty
	}
	stamp := e.Time.Format("15:04:05")
	cat := fmt.Sprintf("%-6s", e.Cat.Short())
	msg := fsys.SanitizeName(e.Summary())

	if contentW >= actNarrow {
		head := " " + stamp + " " + glyph + " " + cat + " " + paneTag(e.Panel) + " "
		body := ansi.Truncate(msg, max(1, contentW-lipgloss.Width(head)), "…")
		if selected {
			plain := " " + stamp + " " + glyph + " " + cat + " " + paneTag(e.Panel) + " " + body
			return th.CursorActive.Render(fitWidth(plain, contentW))
		}
		row := th.Summary.Render(" "+stamp+" ") + gstyle.Render(glyph) + " " +
			th.ColumnHeader.Render(cat) + " " + th.Summary.Render(paneTag(e.Panel)+" ") +
			th.Info.Render(body)
		// Padded to the content width, so the scrollbar column stays a column
		// and the highlight (above) covers the whole row.
		return fitWidth(row, contentW)
	}
	if l.part == 0 {
		line := " " + msg
		if selected {
			return th.CursorActive.Render(fitWidth(line, contentW))
		}
		return gstyle.Render(fitWidth(line, contentW))
	}
	// The second line carries what the wide row shows around the message: when
	// it happened, in which category, and which event — plus the level when it
	// is not a plain info entry.
	sub := fmt.Sprintf("   %s · %s · %s", stamp, strings.TrimSpace(e.Cat.Short()), e.Ev)
	if lv := strings.ToLower(e.LevelOf().String()); lv != "info" {
		sub += " · " + lv
	}
	if d := durText(e.DurMS); d != "" {
		sub += " · " + d
	}
	if selected {
		return th.CursorInactive.Render(fitWidth(sub, contentW))
	}
	return th.Empty.Render(fitWidth(sub, contentW))
}

// paneTag marks the panel an entry belongs to (L/R), blank for global events.
func paneTag(p string) string {
	switch p {
	case "L", "R":
		return p
	}
	return "·"
}

// durText formats a duration compactly.
func durText(ms int64) string {
	switch {
	case ms <= 0:
		return ""
	case ms < 1000:
		return fmt.Sprintf("%d ms", ms)
	case ms < 60_000:
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	}
	return fmt.Sprintf("%dm%02ds", ms/60000, (ms/1000)%60)
}

// activityDetailLines describes the selected entry, without a box: the time and
// level, the message, the error, then the fields that exist.
func (a *App) activityDetailLines(v *activityView, width int, now time.Time) []string {
	// Defensive: a stale selection must never index past the list (this was a
	// crash: select the last entry, open the details, press Down).
	if v == nil || len(v.rows) == 0 || v.sel < 0 || v.sel >= len(v.rows) {
		return nil
	}
	e := v.entries[v.rows[v.sel]]
	th := a.theme
	avail := max(8, width-2)
	pad := " "
	var out []string
	out = append(out, pad+th.Summary.Render(e.Time.Format("2006-01-02 15:04:05"))+
		th.Divider.Render(" · ")+th.ColumnHeader.Render(e.LevelOf().Label())+
		th.Divider.Render(" · ")+th.ColumnHeader.Render(e.Ev))
	if e.Msg != "" {
		out = append(out, pad+th.Info.Render(ansi.Truncate(fsys.SanitizeName(e.Msg), avail-1, "…")))
	}
	if e.Err != "" && e.Err != e.Msg {
		out = append(out, pad+th.Error.Render(ansi.Truncate(fsys.SanitizeName(e.Err), avail-1, "…")))
	}
	var facts []string
	if e.Kind != "" {
		facts = append(facts, e.Kind)
	}
	if e.Op != "" {
		facts = append(facts, "op "+e.Op)
	}
	if e.Status != "" {
		facts = append(facts, e.Status)
	}
	if e.Code != 0 {
		facts = append(facts, "code "+actItoa(e.Code))
	}
	if d := durText(e.DurMS); d != "" {
		facts = append(facts, d)
	}
	if e.Panel != "" {
		facts = append(facts, "panel "+e.Panel)
	}
	if e.Run != "" {
		when := "this session"
		if e.Run != a.activityCurrentRun() {
			when = activityDateLabel(e.Time, now)
		}
		facts = append(facts, "session "+when)
	}
	if len(facts) > 0 {
		out = append(out, pad+th.Summary.Render(ansi.Truncate(strings.Join(facts, " · "), avail-1, "…")))
	}
	// Data keys, sorted for a stable frame.
	for _, k := range sortedKeys(e.Data) {
		out = append(out, pad+th.Empty.Render(ansi.Truncate(
			fsys.SanitizeName(k)+": "+fsys.SanitizeName(e.Data[k]), avail-1, "…")))
	}
	if len(out) > actDetailMax {
		out = out[:actDetailMax]
	}
	return out
}

func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// insertion sort: at most eight keys
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// activityHints is the hint bar of the viewer.
func (a *App) activityHints() []barItem {
	v := a.actView
	if v != nil && v.typing {
		return []barItem{
			item("⏎", "apply", "enter"), item("Esc", "cancel", "esc"), item("^U", "clear", "ctrl+u"),
			item("cat:/lvl:/ev:", "filters", ""),
		}
	}
	// No "↑↓ move" chip: the list is the whole screen and the arrows are
	// obvious, while the room it frees keeps "Esc close" visible at 100
	// columns. Everything below is in the order a narrow terminal drops it.
	out := []barItem{item("c", "category", "c"), item("l", "level", "l")}
	if v != nil && !v.filter.Empty() {
		out = append(out, item("x", "clear", "x"))
	}
	// Closing comes before the rarer actions so even a 60-column terminal
	// still shows the one key that always works, and the last chips are the
	// ones a narrow terminal drops.
	out = append(out, item("Esc", "close", "esc"), item("/", "filter", "/"),
		item("[ ]", "session", "["), item("⏎", "details", "enter"),
		item("r", "reload", "r"))
	return out
}

// activityMouse handles the wheel over the list, and a tap to select a row.
func (a *App) activityMouse(ev tea.MouseEvent) tea.Cmd {
	v := a.actView
	if v == nil {
		return nil
	}
	// The wheel first: it arrives as a press, but it must scroll rather than
	// select the line under the pointer.
	if ev.IsWheel() {
		a.activityScroll(wheelDelta(ev) * 3)
		return nil
	}
	if ev.Action != tea.MouseActionPress || ev.Button != tea.MouseButtonLeft {
		return nil
	}
	// The body starts below the title line, and its first line is the header:
	// screen y = 1 (title) + 1 (header) + list line.
	line := v.top + (ev.Y - 2)
	if line < 0 || line >= len(v.lines) {
		return nil
	}
	l := v.lines[line]
	if l.kind == actHeaderLine {
		return nil // a session rule is not an entry
	}
	if l.row == v.sel {
		v.detail = !v.detail
	} else {
		v.sel = l.row
	}
	a.activityNormalize()
	return nil
}

// activityScroll moves the selection by delta entries (keyboard or wheel) and
// keeps it in view.
func (a *App) activityScroll(delta int) {
	v := a.actView
	if v == nil {
		return
	}
	v.sel += delta
	a.activityNormalize()
}
