package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/activity"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
)

// The activity log viewer: a full-body screen on L.
//
// One log, several filters. The header names the filter in force and the keys
// change it (`c` cycles the nine categories, `l` the minimum level, `/` types a
// query). The list is the log itself, newest first, so nothing is duplicated
// and nothing is lost by switching filter: it is the same file either way.
//
// Rows are one line when there is room (time, category, message) and two lines
// on a narrow terminal (message above, time/category below). The selected
// entry's details — event name, program, exit code, duration, error text and
// data — appear under ⏎ instead of beside the list, so a short terminal still
// shows a usable list.

const (
	actBoxTitle  = "Activity log"
	actMaxRows   = 40 // entries shown at once at most (the rest scroll)
	actDetailMax = 9  // detail lines at most
	actQueryMax  = 80
)

type activityView struct {
	entries []activity.Entry // snapshot, newest first
	rows    []int            // indices of entries passing the filter
	sel     int              // index into rows
	offset  int              // first visible row

	filter   activity.Filter
	catSel   activity.CatFilter
	levelSel activity.LevelFilter
	typing   bool
	typed    string // the query being typed
	query    string // the committed query, for the header

	detail  bool
	stat    activity.Stat
	readErr string
	rev     uint64

	// geometry of the last frame, for mouse hit-testing
	lastTop, lastRows int
}

// reload takes a snapshot of the log and re-applies the filter.
func (v *activityView) reload(a *App) {
	v.entries, v.stat, v.readErr = nil, activity.Stat{Missing: true}, ""
	path := a.activityPath
	if a.actErr != nil {
		v.readErr = a.actErr.Error()
	}
	if path != "" && a.actLog != nil {
		res := activity.Read(path, actMaxRows*200)
		v.entries, v.stat = res.Entries, res.Stat
		v.rev = a.actLog.Revision()
		if a.actLog.Err() != nil {
			v.readErr = a.actLog.Err().Error()
		}
	} else if path != "" {
		res := activity.Read(path, actMaxRows*200)
		v.entries, v.stat = res.Entries, res.Stat
	}
	v.rebuild()
}

// rebuild recomputes the effective filter from the category and level the
// user cycled to plus the typed query, and from the filtered rows.
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
	if v.sel >= len(v.rows) {
		v.sel = len(v.rows) - 1
	}
	if v.sel < 0 {
		v.sel = 0
	}
	v.clampOffset()
}

// clampOffset keeps the selection on screen.
func (v *activityView) clampOffset() {
	rows := v.listRows()
	if rows < 1 {
		v.offset = 0
		return
	}
	if v.sel < v.offset {
		v.offset = v.sel
	}
	if v.sel >= v.offset+rows {
		v.offset = v.sel - rows + 1
	}
	if v.offset < 0 {
		v.offset = 0
	}
	if max := len(v.rows) - rows; v.offset > max && max >= 0 {
		v.offset = max
	}
}

// listRows is how many entries the list shows: the body height minus the
// header, the detail box and the scroll indicator.
func (v *activityView) listRows() int {
	rows := v.lastRows
	if rows < 1 {
		rows = actMaxRows
	}
	return max(1, rows)
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
	page := v.listRows()
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
		v.lastRows = 0
		v.clampOffset()
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
	v.clampOffset()
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
		v.clampOffset()
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

// activityJumpRun moves the selection to the previous or next application run
// (the run ID shared with the operation history).
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
			v.clampOffset()
			return
		}
		i += dir
	}
	a.setStatus("activity: no other run in this filter", false)
}

// ---- rendering ---------------------------------------------------------------

// activityRender draws the whole body.
func (a *App) activityRender(height int) string {
	v := a.actView
	if v == nil || height <= 0 {
		return ""
	}
	th := a.theme
	width := a.width

	// Geometry: header (1) + list + optional detail box.
	detailRows := 0
	if v.detail {
		detailRows = min(actDetailMax, max(3, height/3))
	}
	v.lastRows = max(1, height-1-detailRows)
	v.clampOffset()

	rows := v.lastRows
	lines := make([]string, 0, height)
	lines = append(lines, a.activityHeader(width, len(v.rows)))

	if len(v.rows) == 0 {
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

	// The visible slice, newest first.
	end := min(len(v.rows), v.offset+rows)
	for i := v.offset; i < end; i++ {
		e := v.entries[v.rows[i]]
		selected := i == v.sel
		lines = append(lines, a.activityRow(e, selected, width)...)
		if len(lines) >= height-1-detailRows+1 {
			// two-line rows can overshoot; the detail box gets what is left
			detailRows = max(0, height-1-(len(lines)-1))
			break
		}
	}
	if v.detail {
		for _, l := range fitBlock(a.activityDetailLines(v, width), detailRows, width) {
			lines = append(lines, l)
		}
	}
	return strings.Join(fitBlock(lines, height, width), "\n")
}

// activityHeader is the title line: filter, counts, query field.
func (a *App) activityHeader(width, shown int) string {
	v := a.actView
	th := a.theme
	title := th.HelpTitle.Render(actBoxTitle)
	left := title + th.Summary.Render(" · "+v.filter.Cat.Label())

	info := fmt.Sprintf("%d shown", shown)
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
		left = title + th.Summary.Render(" · filter: ")
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

// activityRow draws one entry: one line when wide, two when narrow.
func (a *App) activityRow(e activity.Entry, selected bool, width int) []string {
	th := a.theme
	level := e.LevelOf()
	stamp := e.Time.Format("15:04:05")
	cat := fmt.Sprintf("%-6s", e.Cat.Short())
	msg := fsys.SanitizeName(e.Summary())

	// A level glyph makes warnings and errors findable by eye.
	glyph := " "
	style := th.Summary
	switch level {
	case activity.LevelWarn:
		glyph, style = "!", th.StatusWarn
	case activity.LevelError:
		glyph, style = "✗", th.StatusErr
	case activity.LevelDebug:
		style = th.Empty
	}
	if selected {
		style = th.CursorActive
	}

	var out []string
	if width >= 64 {
		head := fmt.Sprintf("%s %s %s %s ", stamp, glyph, cat, paneTag(e.Panel))
		body := ansi.Truncate(msg, max(1, width-lipgloss.Width(head)-1), "…")
		out = append(out, style.Render(fitWidth(head+body, width)))
	} else {
		out = append(out, style.Render(fitWidth(" "+fmt.Sprintf("%s %s %s", glyph, cat, msg), width)))
		sub := fmt.Sprintf("   %s %s", stamp, strings.ToLower(level.String()))
		if dur := durText(e.DurMS); dur != "" {
			sub += " · " + dur
		}
		out = append(out, th.Empty.Render(fitWidth(sub, width)))
	}
	return out
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

// activityDetailLines is the box under the list describing the selected entry.
func (a *App) activityDetailLines(v *activityView, width int) []string {
	if len(v.rows) == 0 {
		return nil
	}
	e := v.entries[v.rows[v.sel]]
	th := a.theme
	avail := max(8, width-2)
	var out []string
	out = append(out, th.Summary.Render(ansi.Truncate(
		e.Time.Format("2006-01-02 15:04:05"), avail, "…"))+th.Empty.Render("  "+e.LevelOf().Label()+" · "+e.Ev))
	if e.Msg != "" {
		out = append(out, th.Info.Render(ansi.Truncate(fsys.SanitizeName(e.Msg), avail, "…")))
	}
	if e.Err != "" && e.Err != e.Msg {
		out = append(out, th.Error.Render(ansi.Truncate(fsys.SanitizeName(e.Err), avail, "…")))
	}
	var facts []string
	if e.Cat != "" && e.Ev != "" {
		facts = append(facts, e.Cat.Label())
	}
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
		facts = append(facts, "run "+shortRun(e.Run))
	}
	if len(facts) > 0 {
		out = append(out, th.Empty.Render(ansi.Truncate(strings.Join(facts, " · "), avail, "…")))
	}
	// Data keys, sorted for a stable frame.
	for _, k := range sortedKeys(e.Data) {
		out = append(out, th.Empty.Render(ansi.Truncate(fsys.SanitizeName(k)+": "+fsys.SanitizeName(e.Data[k]), avail, "…")))
	}
	if len(out) > actDetailMax {
		out = out[:actDetailMax]
	}
	return out
}

// shortRun abbreviates a run ID (20261027T101500.123456789) for display.
func shortRun(id string) string {
	if len(id) > 15 {
		return id[:15]
	}
	return id
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
	out := []barItem{item("↑↓", "move", ""), item("c", "category", "c"), item("l", "level", "l")}
	if v != nil && !v.filter.Empty() {
		out = append(out, item("x", "clear", "x"))
	}
	out = append(out, item("/", "filter", "/"), item("[ ]", "run", "["),
		item("⏎", "details", "enter"), item("r", "reload", "r"))
	return append(out, item("Esc", "close", "esc"))
}

// activityMouse handles the wheel over the list, and a tap to select a row.
func (a *App) activityMouse(ev tea.MouseEvent) tea.Cmd {
	v := a.actView
	if v == nil {
		return nil
	}
	// The wheel first: it arrives as a press, but it must scroll rather than
	// select the row under the pointer.
	if ev.IsWheel() {
		a.activityScroll(wheelDelta(ev))
		return nil
	}
	if ev.Action != tea.MouseActionPress || ev.Button != tea.MouseButtonLeft {
		return nil
	}
	// A tap on a list row selects it; a second tap on the selected row opens
	// the details.
	const bodyTop = 2 // title line + header line (see View)
	row := ev.Y - bodyTop
	if row < 0 {
		return nil
	}
	if a.width < 64 {
		row /= 2 // two-line rows
	}
	i := v.offset + row
	if i < 0 || i >= len(v.rows) {
		return nil
	}
	if i == v.sel {
		v.detail = !v.detail
		v.lastRows = 0
	} else {
		v.sel = i
	}
	v.clampOffset()
	return nil
}

// activityScroll moves the list by delta rows (keyboard or wheel).
func (a *App) activityScroll(delta int) {
	v := a.actView
	if v == nil {
		return
	}
	v.sel += delta
	if v.sel < 0 {
		v.sel = 0
	}
	if v.sel >= len(v.rows) {
		v.sel = len(v.rows) - 1
	}
	v.clampOffset()
}
