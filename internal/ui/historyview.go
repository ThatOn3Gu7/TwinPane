package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/history"
	"github.com/ThatOn3Gu7/TwinPane/internal/settings"
)

// Persistent operation history inside the Tasks screen.
//
// The Tasks screen lists three kinds of rows in one continuous, scrollable
// list, grouped by application run:
//
//	── This run · 06:02 · 1 active · 2 finished ──────────────────
//	▸ 1  ● RUNNING   Copying   ██████░░░░ 40% · 3/8   12 M/30 M  0:04
//	  2  ✓ DONE      Delete    ████████ 100% · 1/1     6 B/6 B   2s
//	── Today 05:03 · 3 operations ───────────────────────────────
//	    05:03:47 ✓ DONE      Copy      → /tmp/photos/2024
//	    05:02:11 ✕ FAILED    Upload    → drive:backup
//
// The first section is this run: its active tasks (pinned first, so running
// work is always on screen with its live progress), then everything else it
// did newest first — finished tasks and the records of operations that never
// had a task row (rename, new file, permissions). The sections below it are
// the earlier runs from the history store, newest first, each with its
// operations newest first.
//
// Live rows are the task manager's sessions, so progress, canceling and watch
// keep working; historical rows are copies from the store and are read-only:
// no row action (cancel, watch, clear) ever touches them.

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

// currentSessionIndex is this run's session in sessions, or -1 when the store
// keeps no session for this run (a read-only file from a newer TwinPane, or
// persistence disabled).
func (a *App) currentSessionIndex(sessions []history.Session) int {
	if a.history == nil {
		return -1
	}
	id := a.history.CurrentID()
	if id == "" {
		return -1
	}
	for i := range sessions {
		if sessions[i].ID == id {
			return i
		}
	}
	return -1
}

// ---- the merged list ---------------------------------------------------------

// taskSelKind says what the cursor is on.
type taskSelKind int

const (
	selLiveTask taskSelKind = iota // a task of this run (running or finished)
	selRecord                      // a recorded operation from the history
)

// taskEntryKind is the kind of one row or heading of the merged list.
type taskEntryKind int

const (
	taskEntryHeading taskEntryKind = iota // a run heading (not selectable)
	taskEntryLive                         // a background task of this run
	taskEntryRecord                       // a recorded operation (read-only)
	taskEntryEmpty                        // "nothing yet" inside a heading
)

// taskEntry is one row of the Tasks screen. Live entries point at their
// session so progress keeps updating; record entries point into the history
// snapshot the list was built from (kept alive by the entries themselves).
type taskEntry struct {
	kind    taskEntryKind
	title   string // heading or placeholder text
	current bool   // heading of this run
	sess    int    // history session index (-1 = this run)
	op      int    // operation index inside that session (newest first)
	task    *opSession
	rec     *history.Record
	run     time.Time // when the record's run started
	started time.Time // when the operation started (ordering)
}

// taskEntries is the list the Tasks screen shows. It is rebuilt only when the
// task list or the recorded history changed, so redrawing during progress
// updates costs nothing: the rows read the live sessions directly.
func (a *App) taskEntries() []taskEntry {
	if a == nil {
		return nil
	}
	rev := uint64(0)
	if a.history != nil {
		rev = a.history.Revision()
	}
	m := &a.tasks
	if m.entriesValid && m.entriesRev == rev && m.entriesLiveRev == m.liveRev {
		return m.entries
	}
	m.entries = a.buildTaskEntries()
	m.entriesRev, m.entriesLiveRev, m.entriesValid = rev, m.liveRev, true
	a.ensureTaskSelection(m.entries)
	return m.entries
}

// buildTaskEntries merges this run's tasks with the recorded runs.
func (a *App) buildTaskEntries() []taskEntry {
	sessions := a.historySessions()
	cur := a.currentSessionIndex(sessions)
	live := a.tasks.newest()

	out := make([]taskEntry, 0, len(live)+16)

	// Everything this run did that is not an active task, newest first:
	// finished tasks plus the records that never had a task row.
	linked := make(map[string]bool, len(live))
	for _, s := range live {
		if s.hist.id != "" {
			linked[s.hist.id] = true
		}
	}
	active := make([]*opSession, 0, len(live))
	rest := make([]taskEntry, 0, len(live)+8)
	finished := 0
	for _, s := range live {
		if s.active() {
			active = append(active, s)
			continue
		}
		finished++
		rest = append(rest, taskEntry{kind: taskEntryLive, task: s, sess: -1, started: s.started})
	}
	recorded := 0
	if cur >= 0 {
		for i := range sessions[cur].Ops {
			r := &sessions[cur].Ops[i]
			// A task row already shows its own record; records the user
			// cleared from the list stay cleared until the next run.
			if linked[r.ID] || a.tasks.cleared[r.ID] {
				continue
			}
			recorded++
			rest = append(rest, taskEntry{
				kind: taskEntryRecord, rec: r, sess: cur, op: i,
				run: sessions[cur].Started, started: r.Started,
			})
		}
	}
	sort.SliceStable(rest, func(i, j int) bool { return rest[i].started.After(rest[j].started) })

	out = append(out, taskEntry{
		kind: taskEntryHeading, current: true, sess: cur,
		title: a.runHeading(sessions, cur, len(active), finished, recorded),
	})
	// Active tasks stay on top: running work must never scroll out of sight
	// behind a long list of finished rows.
	for _, s := range active {
		out = append(out, taskEntry{kind: taskEntryLive, task: s, sess: -1, started: s.started})
	}
	out = append(out, rest...)

	// Earlier runs, newest first. A run that recorded nothing has nothing to
	// group, so it is skipped instead of leaving an empty heading behind.
	var earlier []taskEntry
	for i := range sessions {
		if i == cur || len(sessions[i].Ops) == 0 {
			continue
		}
		earlier = append(earlier, taskEntry{
			kind: taskEntryHeading, sess: i,
			title: a.sessionHeading(sessions[i]),
		})
		for j := range sessions[i].Ops {
			earlier = append(earlier, taskEntry{
				kind: taskEntryRecord, rec: &sessions[i].Ops[j], sess: i, op: j,
				run: sessions[i].Started, started: sessions[i].Ops[j].Started,
			})
		}
	}
	// The explanation for an empty screen belongs on an empty screen only;
	// with earlier runs below, their rows say what this screen is for.
	if len(active) == 0 && len(rest) == 0 && len(earlier) == 0 {
		out = append(out, taskEntry{kind: taskEntryEmpty,
			title: "Nothing yet — copy, move, rename, create or delete something."})
	}
	out = append(out, earlier...)
	return out
}

// runHeading is the "This run" heading with the counts that fit.
func (a *App) runHeading(sessions []history.Session, cur, active, finished, recorded int) string {
	title := "This run"
	if cur >= 0 && !sessions[cur].Started.IsZero() {
		title += " · " + sessions[cur].Started.Local().Format("15:04")
	}
	var parts []string
	if active > 0 {
		parts = append(parts, fmt.Sprintf("%d active", active))
	}
	if finished > 0 {
		parts = append(parts, fmt.Sprintf("%d finished", finished))
	}
	if recorded > 0 {
		parts = append(parts, fmt.Sprintf("%d recorded", recorded))
	}
	if len(parts) == 0 {
		return title + " · nothing yet"
	}
	return title + " · " + strings.Join(parts, " · ")
}

// sessionHeading describes an earlier run: when it started and what it did.
func (a *App) sessionHeading(sess history.Session) string {
	title := dayLabel(sess.Started, a.now()) + " · " + plural(len(sess.Ops), "operation")
	failed := 0
	for _, r := range sess.Ops {
		if r.Status == history.StatusFailed || r.Status == history.StatusInterrupted {
			failed++
		}
	}
	if failed > 0 {
		title += fmt.Sprintf(" · %d failed", failed)
	}
	return title
}

// dayLabel names a moment relative to now, so a heading reads "Today 06:02"
// instead of an ISO timestamp.
func dayLabel(t, now time.Time) string {
	if t.IsZero() {
		return "Earlier"
	}
	t, now = t.Local(), now.Local()
	switch {
	case sameDay(t, now):
		return "Today " + t.Format("15:04")
	case sameDay(t, now.AddDate(0, 0, -1)):
		return "Yesterday " + t.Format("15:04")
	case t.Year() == now.Year():
		return t.Format("Jan 2 15:04")
	}
	return t.Format("2006-01-02 15:04")
}

func sameDay(a, b time.Time) bool {
	y1, m1, d1 := a.Date()
	y2, m2, d2 := b.Date()
	return y1 == y2 && m1 == m2 && d1 == d2
}

// ---- selection ---------------------------------------------------------------

// selectable reports whether a row can hold the cursor.
func selectable(e taskEntry) bool {
	return e.kind == taskEntryLive || e.kind == taskEntryRecord
}

// selectedTaskEntryIndex is the index of the selected row (-1 when the
// selection does not exist any more).
func (a *App) selectedTaskEntryIndex(entries []taskEntry) int {
	for i := range entries {
		e := entries[i]
		switch {
		case e.kind == taskEntryLive && a.tasks.selKind == selLiveTask &&
			e.task != nil && e.task.id == a.tasks.selected:
			return i
		case e.kind == taskEntryRecord && a.tasks.selKind == selRecord &&
			e.sess == a.tasks.histSession && e.op == a.tasks.histOp:
			return i
		}
	}
	return -1
}

// selectedTaskEntry is the selected row, if any.
func (a *App) selectedTaskEntry() (taskEntry, bool) {
	entries := a.taskEntries()
	if i := a.selectedTaskEntryIndex(entries); i >= 0 {
		return entries[i], true
	}
	return taskEntry{}, false
}

// selectTaskEntry puts the cursor on entries[idx] (headings are ignored).
func (a *App) selectTaskEntry(entries []taskEntry, idx int) {
	if idx < 0 || idx >= len(entries) {
		return
	}
	e := entries[idx]
	switch e.kind {
	case taskEntryLive:
		if e.task == nil {
			return
		}
		if a.tasks.selKind != selLiveTask || a.tasks.selected != e.task.id {
			a.tasks.detailOffset = 0
		}
		a.tasks.selKind, a.tasks.selected = selLiveTask, e.task.id
	case taskEntryRecord:
		if a.tasks.selKind != selRecord || a.tasks.histSession != e.sess || a.tasks.histOp != e.op {
			a.tasks.detailOffset = 0
		}
		a.tasks.selKind, a.tasks.histSession, a.tasks.histOp = selRecord, e.sess, e.op
	}
}

// ensureTaskSelection keeps the cursor on a row that still exists: a cleared
// task, a pruned run or a changed retention limit can remove the old one.
func (a *App) ensureTaskSelection(entries []taskEntry) {
	if a.selectedTaskEntryIndex(entries) >= 0 {
		return
	}
	for i := range entries {
		if selectable(entries[i]) {
			a.selectTaskEntry(entries, i)
			return
		}
	}
}

// moveTaskSelection moves the cursor by delta rows, skipping headings.
func (a *App) moveTaskSelection(delta int) {
	entries := a.taskEntries()
	idx := a.selectedTaskEntryIndex(entries)
	dir := 1
	if delta < 0 {
		dir = -1
	}
	if idx < 0 {
		for i := range entries {
			if selectable(entries[i]) {
				a.selectTaskEntry(entries, i)
				return
			}
		}
		return
	}
	for i := idx + dir; i >= 0 && i < len(entries); i += dir {
		if selectable(entries[i]) {
			a.selectTaskEntry(entries, i)
			return
		}
	}
}

// jumpTaskRun moves to the next newer (newer = true) or older run section and
// selects its first row. The list is newest-first, so "newer" walks up.
// Sections without rows are skipped.
func (a *App) jumpTaskRun(newer bool) {
	entries := a.taskEntries()
	sel := a.selectedTaskEntryIndex(entries)
	if sel < 0 {
		a.ensureTaskSelection(entries)
		return
	}
	head := -1
	for i := sel; i >= 0; i-- {
		if entries[i].kind == taskEntryHeading {
			head = i
			break
		}
	}
	if head < 0 {
		return
	}
	dir := 1 // older: down the list
	if newer {
		dir = -1
	}
	for i := head + dir; i >= 0 && i < len(entries); i += dir {
		if entries[i].kind != taskEntryHeading {
			continue
		}
		for j := i + 1; j < len(entries) && entries[j].kind != taskEntryHeading; j++ {
			if selectable(entries[j]) {
				a.selectTaskEntry(entries, j)
				return
			}
		}
	}
}

// ---- row rendering -----------------------------------------------------------

// taskHeadingLine draws a run heading as a rule with its title in it, so the
// sections are visibly separated at any width.
func (a *App) taskHeadingLine(title string, innerW int, current bool) string {
	style := a.theme.ColumnHeader
	if current {
		style = a.theme.HelpTitle
	}
	if innerW <= 0 {
		return ""
	}
	label := " " + fsys.SanitizeName(title) + " "
	if ansi.StringWidth(label)+4 > innerW {
		return style.Render(truncRight("──"+label, innerW))
	}
	fill := strings.Repeat("─", innerW-2-ansi.StringWidth(label))
	return style.Render("──" + label + fill)
}

// recordGlyph is the one-cell status mark of a record. It uses the same
// shapes as a live task's state, so one list reads consistently.
func recordGlyph(st history.Status) string {
	switch st {
	case history.StatusSuccess:
		return "✓"
	case history.StatusFailed:
		return "✕"
	case history.StatusPartial:
		return "▲"
	case history.StatusCanceled:
		return "⊘"
	case history.StatusInterrupted:
		return "?"
	case history.StatusRunning:
		return "●"
	}
	return "?"
}

// recordStyle colours a recorded status the same way a task state is coloured.
func (a *App) recordStyle(st history.Status) lipgloss.Style {
	switch st {
	case history.StatusSuccess:
		return a.theme.StatusOK
	case history.StatusFailed:
		return a.theme.StatusErr
	case history.StatusPartial, history.StatusCanceled, history.StatusInterrupted:
		return a.theme.StatusWarn
	}
	return a.theme.HelpTitle
}

// recordRowLines renders one recorded operation: one table row (two lines on
// a narrow terminal), aligned with the live task rows.
func (a *App) recordRowLines(l taskLayout, e taskEntry, selected bool) []string {
	r := e.rec
	if r == nil {
		return nil
	}
	style := a.recordStyle(r.Status)
	glyph := recordGlyph(r.Status)
	op := fsys.SanitizeName(capitalize(r.Op))
	target := recordTarget(r)
	marker := "  "
	if selected {
		marker = a.theme.HelpTitle.Render("▸") + " "
	}
	if l.compact {
		head := marker + style.Render(glyph) + " " +
			a.theme.Info.Render(truncRight(op, max(6, l.innerW-4)))
		detail := "   " + a.theme.Summary.Render(truncRight(
			r.Status.Label()+" · "+clockOf(r.Started)+" "+target, max(6, l.innerW-4)))
		if selected {
			head = a.theme.CursorActive.Render(fitWidth(ansi.Strip(head), l.innerW))
			detail = a.theme.CursorInactive.Render(fitWidth(ansi.Strip(detail), l.innerW))
		}
		return []string{head, detail}
	}
	c := l.columns
	var b strings.Builder
	b.WriteString(marker)
	b.WriteString(strings.Repeat(" ", taskIDW)) // history rows have no task number
	stateW := 2                                 // glyph only, like a narrow live row
	if c.stateWord {
		stateW = taskStateW
		b.WriteString(style.Render(padRight(glyph+" "+r.Status.Label(), taskStateW)))
	} else {
		b.WriteString(style.Render(glyph) + " ")
	}
	b.WriteString(a.theme.Info.Render(padRight(truncRight(op, max(1, c.opW-1)), c.opW)))
	// The progress and size columns carry no meaning for a finished record;
	// the space shows what it acted on instead.
	rest := max(1, l.innerW-taskMarkerW-taskIDW-stateW-c.opW)
	if target != "" {
		target = " " + target
	}
	b.WriteString(a.theme.Summary.Render(truncRight(clockOf(r.Started)+target, rest)))
	row := truncRight(b.String(), l.innerW)
	if selected {
		return []string{a.theme.CursorActive.Render(fitWidth(ansi.Strip(row), l.innerW))}
	}
	return []string{fitWidth(row, l.innerW)}
}

// recordTarget is the short path shown at the end of a recorded row.
func recordTarget(r *history.Record) string {
	switch {
	case r.Dest != "":
		return "→ " + fsys.SanitizeName(r.Dest)
	case len(r.Sources) > 0:
		return fsys.SanitizeName(r.Sources[0])
	case len(r.Items) > 0:
		return fsys.SanitizeName(r.Items[0])
	}
	return ""
}

func clockOf(t time.Time) string {
	if t.IsZero() {
		return "--:--:--"
	}
	return t.Local().Format("15:04:05")
}

// recordDetailRows is the Details panel of a recorded operation: the same
// label/value layout as a task's details.
func (a *App) recordDetailRows(e taskEntry, width int) []string {
	r := e.rec
	if r == nil {
		return nil
	}
	th := a.theme
	valueW := max(8, width-taskLabelW-1)
	var lines []string
	add := func(label, value string) {
		if value == "" {
			return
		}
		wrapped := wrapText(fsys.SanitizeName(value), valueW)
		for i, part := range wrapped {
			if i == 0 {
				lines = append(lines, th.PropLabel.Render(padRight(label, taskLabelW))+th.DialogText.Render(part))
				continue
			}
			lines = append(lines, strings.Repeat(" ", taskLabelW)+th.DialogText.Render(part))
		}
	}
	addPath := func(label, path string) {
		if path == "" {
			return
		}
		lines = append(lines, th.PropLabel.Render(padRight(label, taskLabelW))+
			th.DialogPath.Render(truncLeft(fsys.SanitizeName(path), valueW)))
	}

	add("Operation", capitalize(r.Op))
	add("State", r.Status.Label())
	if !e.run.IsZero() {
		add("Session", dayLabel(e.run, a.now()))
	}
	if !r.Started.IsZero() {
		add("Started", r.Started.Local().Format("2006-01-02 15:04:05"))
	}
	if d := r.Duration(); d > 0 {
		add("Duration", d.Round(time.Millisecond).String())
	}
	const sourceCap = 6
	for i, p := range r.Sources {
		if i == sourceCap {
			add("Source", fmt.Sprintf("… %d more", len(r.Sources)-sourceCap))
			break
		}
		label := ""
		if i == 0 {
			label = "From:"
		}
		addPath(label, p)
	}
	addPath("To:", r.Dest)
	const itemCap = 6
	for i, it := range r.Items {
		if i == itemCap {
			add("Items", fmt.Sprintf("… %d more", len(r.Items)-itemCap))
			break
		}
		label := ""
		if i == 0 {
			label = "Items:"
		}
		add(label, it)
	}
	add("Result", historyCounts(r))
	add("Summary", r.Summary)
	add("Tool", r.Result)
	for i, errText := range r.Errors {
		label := ""
		if i == 0 {
			label = "Error:"
		}
		add(label, errText)
	}
	for _, detail := range r.Details {
		add("•", detail)
	}
	if width > 0 {
		for i, line := range lines {
			lines[i] = ansi.Truncate(line, width, "…")
		}
	}
	return lines
}

// historyCounts summarises the counts an operation reported.
func historyCounts(r *history.Record) string {
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
	// The selection may have been pruned away with its run.
	a.ensureTaskSelection(a.taskEntries())
}
