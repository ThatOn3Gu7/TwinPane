package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/history"
	"github.com/ThatOn3Gu7/TwinPane/internal/settings"
)

// The Tasks screen has two views: the live background tasks of this run, and
// the persistent operation history (all recorded runs, newest first). Both
// share the same header, list and detail layout so the screen looks the same
// whichever view is open.

// historySessions returns the recorded runs, newest first.
func (a *App) historySessions() []history.Session {
	if a == nil || a.history == nil {
		return nil
	}
	return a.history.Sessions()
}

// historyRetention is the number of runs the history keeps.
func (a *App) historyRetention() int {
	if a == nil || a.history == nil {
		return settings.DefaultHistorySessions
	}
	return a.history.Limit()
}

// resetHistorySelection selects the newest run that has operations, so the
// screen never opens on an empty block while there is something to inspect.
func (a *App) resetHistorySelection() {
	a.tasks.histSession, a.tasks.histOp, a.tasks.histOffset = 0, 0, 0
	for i, sess := range a.historySessions() {
		if len(sess.Operations()) > 0 {
			a.tasks.histSession = i
			return
		}
	}
}

// clampHistorySelection keeps the run/operation selection inside the
// available records.
func (a *App) clampHistorySelection() ([]history.Session, history.Session) {
	sessions := a.historySessions()
	if len(sessions) == 0 {
		a.tasks.histSession, a.tasks.histOp = 0, 0
		return nil, history.Session{}
	}
	if a.tasks.histSession < 0 || a.tasks.histSession >= len(sessions) {
		a.tasks.histSession = max(0, min(len(sessions)-1, a.tasks.histSession))
	}
	sess := sessions[a.tasks.histSession]
	ops := sess.Operations()
	if len(ops) == 0 {
		a.tasks.histOp = 0
	} else if a.tasks.histOp < 0 || a.tasks.histOp >= len(ops) {
		a.tasks.histOp = max(0, min(len(ops)-1, a.tasks.histOp))
	}
	return sessions, sess
}

// selectedHistoryRecord returns the operation the history view has selected.
func (a *App) selectedHistoryRecord() (history.Record, history.Session, bool) {
	_, sess := a.clampHistorySelection()
	ops := sess.Operations()
	if len(ops) == 0 {
		return history.Record{}, sess, false
	}
	return ops[a.tasks.histOp], sess, true
}

// historyLayout says where each part of the history view starts, so drawing
// and mouse clicks agree. Rows count from the top of the body.
type historyLayout struct {
	sessions   int // first run row
	sessRows   int
	ops        int // first operation row
	opRows     int
	details    int // first detail row
	detailRows int
}

func historyLayoutFor(height, sessionCount, opCount int) historyLayout {
	l := historyLayout{sessions: 1, details: height}
	if height <= 1 {
		l.sessRows, l.ops, l.opRows = 0, height, 0
		return l
	}
	avail := height - 1 // the header takes the first row
	// Runs get up to a third of the screen, operations up to half of the rest.
	l.sessRows = min(sessionCount, max(1, avail/3))
	l.ops = l.sessions + l.sessRows + 1 // one blank line between the blocks
	rest := avail - l.sessRows - 1
	if rest < 2 {
		l.ops, l.opRows = height, 0
		return l
	}
	l.opRows = min(opCount, max(1, rest/2))
	l.details = l.ops + l.opRows + 1 // one blank line before the details
	if l.details > height {
		l.details = height
	}
	l.detailRows = max(0, height-l.details)
	return l
}

// historyViewRender draws the operation history inside the Tasks screen.
func (a *App) historyViewRender(height int) string {
	if height <= 0 {
		return ""
	}
	width := a.width
	sessions, sess := a.clampHistorySelection()
	ops := sess.Operations()
	layout := historyLayoutFor(height, len(sessions), len(ops))

	lines := []string{a.historyHeader(len(sessions))}
	// Runs
	if layout.sessRows > 0 {
		lines = append(lines, a.historySessionRows(sessions, layout.sessRows, width)...)
	}
	// Operations of the selected run
	if layout.opRows > 0 {
		for len(lines) < layout.ops {
			lines = append(lines, "")
		}
		for _, line := range a.historyOpRows(ops, layout.opRows, width) {
			lines = append(lines, line)
		}
	}
	// Details of the selected operation
	if rec, _, ok := a.selectedHistoryRecord(); ok && layout.details < height {
		for len(lines) < layout.details {
			lines = append(lines, "")
		}
		details := a.historyRecordDetails(rec, width)
		if a.tasks.histOffset >= len(details) {
			a.tasks.histOffset = max(0, len(details)-1)
		}
		for _, line := range details[a.tasks.histOffset:] {
			if len(lines) >= height {
				break
			}
			lines = append(lines, line)
		}
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	return strings.Join(lines, "\n")
}

func (a *App) historyHeader(count int) string {
	ops := 0
	sessions := a.historySessions()
	for _, sess := range sessions {
		ops += len(sess.Operations())
	}
	header := fmt.Sprintf("Operation history · %s · %s · keeping %d",
		plural(count, "run"), plural(ops, "operation"), a.historyRetention())
	if a.history != nil && a.history.ReadOnly() {
		header += " · read-only (file from a newer TwinPane)"
	}
	return a.theme.HelpTitle.Render(fitWidth(header, a.width))
}

// historySessionRows renders the run list, scrolled so the selected run is
// visible.
func (a *App) historySessionRows(sessions []history.Session, rows, width int) []string {
	if len(sessions) == 0 {
		return []string{a.theme.Empty.Render(fitWidth("No operations recorded yet. Copy, move, delete, rename, create or archive something.", width))}
	}
	offset := a.tasks.histSession - rows + 1
	offset = max(0, min(offset, len(sessions)-rows))
	offset = max(0, offset)
	end := min(len(sessions), offset+rows)
	out := make([]string, 0, end-offset)
	for i := offset; i < end; i++ {
		sess := sessions[i]
		marker := "  "
		if i == a.tasks.histSession {
			marker = "> "
		}
		row := marker + history.SessionSummary(sess)
		if sess.Version != "" {
			row += " · " + sess.Version
		}
		style := a.theme.TitleText
		if i == a.tasks.histSession {
			style = a.theme.CursorActive
		}
		out = append(out, style.Render(fitWidth(fsys.SanitizeName(row), width)))
	}
	return out
}

// historyOpRows renders the operations of the selected run, scrolled so the
// selected operation is visible.
func (a *App) historyOpRows(ops []history.Record, rows, width int) []string {
	if len(ops) == 0 {
		return []string{a.theme.Empty.Render(fitWidth("   This run has no operations.", width))}
	}
	offset := a.tasks.histOp - rows + 1
	offset = max(0, min(offset, len(ops)-rows))
	offset = max(0, offset)
	end := min(len(ops), offset+rows)
	out := make([]string, 0, end-offset)
	for i := offset; i < end; i++ {
		r := ops[i]
		marker := "     "
		if i == a.tasks.histOp {
			marker = "   › "
		}
		row := fmt.Sprintf("%s%s %-8s %-16s %s", marker, clockOf(r.Started), r.Status.Label(), r.Op, historyRowTarget(r))
		style := a.theme.TitleText
		if i == a.tasks.histOp {
			style = a.theme.CursorActive
		} else {
			switch r.Status {
			case history.StatusSuccess:
				style = a.theme.StatusOK
			case history.StatusFailed:
				style = a.theme.StatusErr
			case history.StatusPartial, history.StatusCanceled, history.StatusInterrupted:
				style = a.theme.StatusWarn
			}
		}
		out = append(out, style.Render(fitWidth(fsys.SanitizeName(row), width)))
	}
	return out
}

// historyRowTarget is the short path shown at the end of an operation row.
func historyRowTarget(r history.Record) string {
	switch {
	case r.Dest != "":
		return "→ " + r.Dest
	case len(r.Sources) > 0:
		return r.Sources[0]
	case len(r.Items) > 0:
		return r.Items[0]
	}
	return ""
}

func clockOf(t time.Time) string {
	if t.IsZero() {
		return "--:--:--"
	}
	return t.Local().Format("15:04:05")
}

// historyRecordDetails renders one recorded operation as detail lines.
func (a *App) historyRecordDetails(r history.Record, width int) []string {
	label := func(k, v string) []string {
		if v == "" {
			return nil
		}
		parts := wrapText(k+v, max(1, width))
		for i := range parts {
			parts[i] = a.theme.DialogText.Render(parts[i])
		}
		return parts
	}
	lines := []string{a.theme.HelpTitle.Render(fmt.Sprintf("Details · %s", fsys.SanitizeName(r.Op)))}
	lines = append(lines, label("Operation: ", r.Op)...)
	lines = append(lines, label("State: ", r.Status.Label())...)
	if !r.Started.IsZero() {
		lines = append(lines, label("Started: ", r.Started.Local().Format("2006-01-02 15:04:05"))...)
	}
	if d := r.Duration(); d > 0 {
		lines = append(lines, label("Duration: ", d.Round(time.Millisecond).String())...)
	}
	const pathCap = 12
	for i, p := range r.Sources {
		if i == pathCap {
			lines = append(lines, label("       ", fmt.Sprintf("… %d more source paths", len(r.Sources)-pathCap))...)
			break
		}
		prefix := "From: "
		if i > 0 {
			prefix = "       "
		}
		lines = append(lines, label(prefix, fsys.SanitizeName(p))...)
	}
	lines = append(lines, label("To: ", fsys.SanitizeName(r.Dest))...)
	if len(r.Items) > 0 {
		const itemCap = 12
		for i, it := range r.Items {
			if i == itemCap {
				lines = append(lines, label("       ", fmt.Sprintf("… %d more items", len(r.Items)-itemCap))...)
				break
			}
			prefix := "Items: "
			if i > 0 {
				prefix = "       "
			}
			lines = append(lines, label(prefix, fsys.SanitizeName(it))...)
		}
	}
	lines = append(lines, label("Result: ", historyCounts(r))...)
	lines = append(lines, label("Summary: ", fsys.SanitizeName(r.Summary))...)
	lines = append(lines, label("Exit: ", r.Result)...)
	for i, e := range r.Errors {
		prefix := "Error: "
		if i > 0 {
			prefix = "       "
		}
		lines = append(lines, label(prefix, fsys.SanitizeName(e))...)
	}
	for _, detail := range r.Details {
		lines = append(lines, label("• ", fsys.SanitizeName(detail))...)
	}
	return lines
}

// historyCounts summarises the counts an operation reported.
func historyCounts(r history.Record) string {
	var parts []string
	if r.Files > 0 {
		parts = append(parts, plural(r.Files, "file"))
	}
	if r.Dirs > 0 {
		parts = append(parts, plural(r.Dirs, "folder"))
	}
	if r.Links > 0 {
		parts = append(parts, plural(r.Links, "link"))
	}
	if r.Bytes > 0 {
		parts = append(parts, fsys.HumanSize(r.Bytes))
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, ", ")
}

// historyFooterHints returns the actions available in the history view.
func (a *App) historyFooterHints() []barItem {
	return []barItem{
		item("h/Esc", "live tasks", "h"),
		item("↑↓", "operation", ""),
		item("[ ]", "run", ""),
		item("+/-", "keep more/fewer", ""),
		item("PgUp/Dn", "details", "pgup"),
	}
}

// historyViewKey handles keys in the history view. It returns handled=false
// for keys this view does not use, so they keep their usual meaning.
func (a *App) historyViewKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	sessions, _ := a.clampHistorySelection()
	switch msg.String() {
	case "esc", "t":
		a.closeTasks()
		a.setStatus("", false)
		return nil, true
	case "h", "tab":
		a.tasks.histShowing = false
		a.tasks.histOffset = 0
		return nil, true
	case "up", "k":
		if a.tasks.histOp > 0 {
			a.tasks.histOp--
			a.tasks.histOffset = 0
		}
		return nil, true
	case "down", "j":
		if ops := a.selectedHistoryOps(); a.tasks.histOp < len(ops)-1 {
			a.tasks.histOp++
			a.tasks.histOffset = 0
		}
		return nil, true
	case "[":
		if a.tasks.histSession < len(sessions)-1 {
			a.tasks.histSession++
			a.tasks.histOp, a.tasks.histOffset = 0, 0
		}
		return nil, true
	case "]":
		if a.tasks.histSession > 0 {
			a.tasks.histSession--
			a.tasks.histOp, a.tasks.histOffset = 0, 0
		}
		return nil, true
	case "+", "=":
		a.changeHistoryRetention(1)
		return nil, true
	case "-", "_":
		a.changeHistoryRetention(-1)
		return nil, true
	case "pgup", "ctrl+b":
		a.tasks.histOffset = max(0, a.tasks.histOffset-5)
		return nil, true
	case "pgdown", "ctrl+f":
		a.tasks.histOffset += 5
		return nil, true
	}
	return nil, false
}

// selectedHistoryOps returns the operations of the selected run.
func (a *App) selectedHistoryOps() []history.Record {
	_, sess := a.clampHistorySelection()
	return sess.Operations()
}

// changeHistoryRetention raises or lowers the number of runs kept on disk,
// prunes the history and saves the preference.
func (a *App) changeHistoryRetention(delta int) {
	if a.history == nil {
		return
	}
	want := history.ClampLimit(a.history.Limit() + delta)
	if want == a.history.Limit() {
		a.setStatusLevel(fmt.Sprintf("History keeps %s (the limit)", plural(want, "run")), lvlInfo)
		return
	}
	msg := fmt.Sprintf("History keeps the last %s", plural(want, "run"))
	level := lvlOK
	if err := a.history.SetLimit(want); err != nil {
		msg += "; the history file could not be written: " + errText(err)
		level = lvlWarn
	}
	if a.settingsPath != "" {
		// Save the whole preference set: the retention limit is one field of
		// it, never a file of its own.
		if err := settings.Save(a.settingsPath, a.currentPrefs()); err != nil {
			msg += "; not saved as a preference: " + errText(err)
			level = lvlWarn
		} else {
			a.settingsErr = nil
		}
	}
	a.setStatusLevel(msg, level)
	// The selection may have been pruned away.
	a.clampHistorySelection()
}

// historyViewMouse handles clicks and the wheel in the history view.
func (a *App) historyViewMouse(ev tea.MouseEvent) tea.Cmd {
	sessions, _ := a.clampHistorySelection()
	ops := a.selectedHistoryOps()
	layout := historyLayoutFor(a.height-3, len(sessions), len(ops))
	if ev.IsWheel() {
		a.tasks.histOffset = max(0, a.tasks.histOffset+wheelDelta(ev))
		return nil
	}
	if ev.Action != tea.MouseActionRelease || ev.Button != tea.MouseButtonLeft {
		return nil
	}
	row := ev.Y - 2 // the body starts under the status line
	if row >= layout.sessions && layout.sessRows > 0 && row < layout.sessions+layout.sessRows {
		offset := max(0, min(a.tasks.histSession-layout.sessRows+1, len(sessions)-layout.sessRows))
		if idx := offset + row - layout.sessions; idx >= 0 && idx < len(sessions) {
			a.tasks.histSession, a.tasks.histOp, a.tasks.histOffset = idx, 0, 0
		}
		return nil
	}
	if row >= layout.ops && layout.opRows > 0 && row < layout.ops+layout.opRows {
		offset := max(0, min(a.tasks.histOp-layout.opRows+1, len(ops)-layout.opRows))
		if idx := offset + row - layout.ops; idx >= 0 && idx < len(ops) {
			a.tasks.histOp, a.tasks.histOffset = idx, 0
		}
	}
	return nil
}
