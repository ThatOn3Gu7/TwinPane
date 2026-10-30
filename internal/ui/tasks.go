package ui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
	"github.com/ThatOn3Gu7/TwinPane/internal/scrollbar"
)

// taskState distinguishes live work from retained outcomes.
type taskState int

const (
	taskPlanning taskState = iota
	taskRunning
	taskCanceling
	taskWaiting
	taskCompleted
	taskCanceled
	taskPartial
	taskFailed
)

// Background tasks keep both running operations and their outcomes until the
// user clears the completed history. Prompts are serialized because the
// terminal can only show one conflict / safety dialog at a time.
type taskManager struct {
	byID         map[int]*opSession
	order        []int // creation order; rendering reverses it
	selected     int
	listOffset   int
	detailOffset int
	showing      bool
	prompts      []taskPrompt
	activePrompt int

	// The list also shows the recorded operations of earlier runs
	// (historyview.go). selKind says what the cursor is on: a live task of
	// this run (selected) or a stored record (histSession + histOp). Historical
	// rows are read-only. cleared remembers the records of finished tasks the
	// user cleared from this run's list, so they do not come back as rows.
	selKind     taskSelKind
	histSession int // index into historySessions() of the selected record
	histOp      int // index into that run's operations (newest first)
	cleared     map[string]bool

	// entries is the merged list. It is rebuilt only when the task list or the
	// recorded history changed, so progress updates redraw for free.
	entries        []taskEntry
	entriesRev     uint64
	entriesLiveRev int
	entriesValid   bool
	liveRev        int
}

const (
	maxTaskHistory = 50
	maxTaskDetails = 100
)

func newTaskManager() taskManager {
	return taskManager{byID: make(map[int]*opSession)}
}

func (m *taskManager) get(id int) *opSession {
	if m == nil {
		return nil
	}
	return m.byID[id]
}

func (m *taskManager) add(s *opSession) {
	if m.byID == nil {
		m.byID = make(map[int]*opSession)
	}
	for len(m.order) >= maxTaskHistory {
		removed := false
		for _, id := range m.order {
			old := m.byID[id]
			if old == nil || !old.active() {
				m.remove(id)
				removed = true
				break
			}
		}
		if !removed { // do not discard running work to enforce a history limit
			break
		}
	}
	m.byID[s.id] = s
	m.order = append(m.order, s.id)
	// New work takes the cursor, as it always did while the list held only
	// tasks: the screen then shows the operation that just started.
	m.selected, m.selKind = s.id, selLiveTask
	m.liveRev++
}

func (m *taskManager) remove(id int) {
	delete(m.byID, id)
	for i, taskID := range m.order {
		if taskID == id {
			m.order = append(m.order[:i], m.order[i+1:]...)
			break
		}
	}
	if m.selected == id {
		m.selected = 0
		if len(m.order) > 0 {
			m.selected = m.order[len(m.order)-1]
		}
	}
	m.liveRev++
}

func (m *taskManager) newest() []*opSession {
	if m == nil {
		return nil
	}
	out := make([]*opSession, 0, len(m.order))
	for i := len(m.order) - 1; i >= 0; i-- {
		if s := m.byID[m.order[i]]; s != nil {
			out = append(out, s)
		}
	}
	return out
}

func (m *taskManager) activeCount() int {
	if m == nil {
		return 0
	}
	n := 0
	for _, s := range m.byID {
		if s.active() {
			n++
		}
	}
	return n
}

func (m *taskManager) latestActive() *opSession {
	for _, s := range m.newest() {
		if s.active() {
			return s
		}
	}
	return nil
}

func (m *taskManager) selectedTask() *opSession {
	if s := m.get(m.selected); s != nil {
		return s
	}
	for _, s := range m.newest() {
		return s
	}
	return nil
}

func (m *taskManager) selectDelta(delta int) {
	items := m.newest()
	if len(items) == 0 {
		m.selected, m.listOffset, m.detailOffset = 0, 0, 0
		return
	}
	idx := 0
	for i, s := range items {
		if s.id == m.selected {
			idx = i
			break
		}
	}
	idx = max(0, min(len(items)-1, idx+delta))
	m.selected = items[idx].id
	m.detailOffset = 0
}

func (m *taskManager) visibleTasks(rows int) []*opSession {
	items := m.newest()
	if len(items) == 0 || rows <= 0 {
		m.listOffset = 0
		return nil
	}
	selectedIndex := 0
	for i, s := range items {
		if s.id == m.selected {
			selectedIndex = i
			break
		}
	}
	if selectedIndex < m.listOffset {
		m.listOffset = selectedIndex
	}
	if selectedIndex >= m.listOffset+rows {
		m.listOffset = selectedIndex - rows + 1
	}
	m.listOffset = max(0, min(m.listOffset, max(0, len(items)-rows)))
	end := min(len(items), m.listOffset+rows)
	return items[m.listOffset:end]
}

func (m *taskManager) queuePrompt(p taskPrompt) {
	m.prompts = append(m.prompts, p)
}

func (m *taskManager) removePrompts(id int) {
	kept := m.prompts[:0]
	for _, p := range m.prompts {
		if p.taskID != id {
			kept = append(kept, p)
		}
	}
	m.prompts = kept
}

func (m *taskManager) clearFinished() int {
	kept := m.order[:0]
	removed := 0
	for _, id := range m.order {
		s := m.byID[id]
		if s == nil || !s.active() {
			// The record stays in the history file; only this run's row goes,
			// so it must not reappear as a "recorded" row straight away.
			if s != nil && s.hist.id != "" {
				if m.cleared == nil {
					m.cleared = make(map[string]bool)
				}
				m.cleared[s.hist.id] = true
			}
			delete(m.byID, id)
			removed++
			continue
		}
		kept = append(kept, id)
	}
	m.order = kept
	if m.get(m.selected) == nil {
		m.selected = 0
		if len(m.order) > 0 {
			m.selected = m.order[len(m.order)-1]
		}
	}
	m.listOffset, m.detailOffset = 0, 0
	m.liveRev++
	return removed
}

type taskPromptKind int

const (
	promptConflict taskPromptKind = iota
	promptLowSpace
	promptDeleteConfirm
	promptTransferConfirm
)

type taskPrompt struct {
	taskID   int
	kind     taskPromptKind
	conflict opConflictMsg
	lowSpace opLowSpaceMsg
	plan     ops.DeletePlan
	transfer ops.Request
	targets  []fsys.Entry
	est      ops.Estimate
}

func (a *App) openTasks() {
	// The list says everything the notification says, and more.
	a.dismissNotice()
	a.tasks.showing = true
	a.tasks.listOffset, a.tasks.detailOffset = 0, 0
	// Start on running work; otherwise on the newest row there is, which is
	// this run's last task or — when it did nothing yet — the newest recorded
	// operation of an earlier run.
	if s := a.tasks.latestActive(); s != nil {
		a.tasks.selKind, a.tasks.selected = selLiveTask, s.id
		return
	}
	if s := a.tasks.selectedTask(); s != nil {
		a.tasks.selKind, a.tasks.selected = selLiveTask, s.id
		return
	}
	a.ensureTaskSelection(a.taskEntries())
}

func (a *App) closeTasks() {
	a.tasks.showing = false
}

func (a *App) taskViewKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "t":
		a.closeTasks()
		a.setStatus("", false)
	case "up", "k":
		a.moveTaskSelection(-1)
	case "down", "j":
		a.moveTaskSelection(1)
	case "[":
		a.jumpTaskRun(false) // the previous (older) run section
	case "]":
		a.jumpTaskRun(true) // the next (newer) run section
	case "pgup", "ctrl+b":
		a.tasks.detailOffset = max(0, a.tasks.detailOffset-5)
	case "pgdown", "ctrl+f":
		a.tasks.detailOffset += 5
	case "enter", "w":
		return a.watchSelectedTask() // live.go
	case "c", "ctrl+c":
		return a.cancelSelectedEntry()
	case "x":
		return a.clearFinishedEntries()
	case "+", "=":
		a.changeHistoryRetention(1)
	case "-", "_":
		a.changeHistoryRetention(-1)
	}
	return nil
}

// cancelSelectedEntry cancels the selected task. Only a running task of this
// run can be cancelled: a recorded operation is a read-only row.
func (a *App) cancelSelectedEntry() tea.Cmd {
	e, ok := a.selectedTaskEntry()
	switch {
	case !ok:
		a.setStatusLevel("Nothing is selected", lvlInfo)
	case e.kind == taskEntryRecord:
		a.setStatusLevel("Recorded operations are read-only — nothing to cancel", lvlInfo)
	case e.task == nil || !e.task.active():
		a.setStatusLevel("The selected task has already finished", lvlInfo)
	default:
		a.cancelTask(e.task.id)
	}
	return nil
}

// clearFinishedEntries clears this run's finished tasks. History records are
// never touched, so the action is refused while a record is selected.
func (a *App) clearFinishedEntries() tea.Cmd {
	if e, ok := a.selectedTaskEntry(); ok && e.kind == taskEntryRecord {
		a.setStatusLevel("Recorded operations are read-only — x only clears this run's finished tasks", lvlInfo)
		return nil
	}
	n := a.tasks.clearFinished()
	if n == 0 {
		a.setStatusLevel("No completed tasks to clear", lvlInfo)
	} else {
		a.setStatusLevel(fmt.Sprintf("Cleared %s", plural(n, "completed task")), lvlOK)
	}
	a.ensureTaskSelection(a.taskEntries())
	return nil
}

// taskWatchable reports whether the selected row is a running task.
func (a *App) taskWatchable() bool {
	e, ok := a.selectedTaskEntry()
	return ok && e.kind == taskEntryLive && e.task != nil && e.task.active()
}

func (a *App) taskViewMouse(ev tea.MouseEvent) tea.Cmd {
	l := a.taskLayout(a.height - 3)
	// The list starts on screen row 1 (the title bar is row 0) inside its
	// border; the column header, when drawn, is the list's first row.
	listTop := 2
	if l.header {
		listTop++
	}
	listBottom := 1 + l.listH - 1 // the list box's bottom border
	if ev.IsWheel() {
		delta := wheelDelta(ev)
		if ev.Y >= listBottom && l.detailsH > 0 {
			a.tasks.detailOffset = max(0, a.tasks.detailOffset+delta)
		} else if delta != 0 {
			a.moveTaskSelection(delta)
		}
		return nil
	}
	if ev.Action != tea.MouseActionRelease || ev.Button != tea.MouseButtonLeft {
		return nil
	}
	if ev.Y < listTop || ev.Y >= listBottom {
		return nil
	}
	row := ev.Y - listTop
	if row < 0 || row >= l.listRows {
		return nil
	}
	entries := a.taskEntries()
	lines, offset, rows := a.taskListWindow(l, entries)
	idx := offset + row
	if idx < 0 || idx >= len(lines) || idx >= offset+rows {
		return nil
	}
	a.selectTaskEntry(entries, lines[idx].entry)
	return nil
}

// ---- task table geometry ------------------------------------------------------

// The task screen is two bordered sections: the task list (a table under a
// header row) and the details of the selected task (a label/value table). Both
// drop columns instead of overflowing, and a narrow or short terminal gets
// two-line rows instead of a table, so a phone shows the same information as a
// desktop terminal without wrapping into noise.

const (
	taskMarkerW = 2  // "▸ " before the selected row
	taskIDW     = 3  // task number
	taskStateW  = 14 // state glyph + word ("✕ INTERRUPTED") + gap
	taskSizeW   = 13 // "120 M/266 M"
	taskTimeW   = 6  // "  0:08"
	taskMinOpW  = 12 // narrowest operation column still worth drawing
	taskBarW    = 8  // cells of the progress bar in a table row
	taskLabelW  = 11 // details label column ("Operation  ")
)

// taskLayout is how the task screen splits its body at the current size.
type taskLayout struct {
	width    int
	innerW   int  // width inside the list box
	compact  bool // two lines per row: no table
	header   bool // draw the table's column header row
	columns  taskColumns
	listH    int // rows of the list section (borders included)
	detailsH int // rows of the details section (0 = not drawn)
	listRows int // list lines that fit under the header
}

// taskColumns selects which table columns are drawn.
type taskColumns struct {
	stateWord bool
	progress  bool
	size      bool
	elapsed   bool
	opW       int
	progW     int
}

func (a *App) taskLayout(height int) taskLayout {
	l := taskLayout{width: a.width, innerW: max(1, a.width-2)}
	fixed := taskMarkerW + taskIDW + taskStateW
	// The details section needs a few rows to be worth its border; the list
	// always keeps at least three (borders + header + one task).
	switch {
	case height < 7:
		l.listH, l.detailsH = height, 0
	case height < 12:
		l.detailsH = 5
		l.listH = height - l.detailsH
	default:
		l.detailsH = min(12, max(6, height/2))
		l.listH = height - l.detailsH
	}
	// Columns: keep the operation and progress readable first, add the
	// secondary columns only where the width can hold them.
	avail := l.innerW - fixed - 3 // three separating gaps
	l.columns.elapsed = avail >= taskMinOpW+12+taskTimeW
	l.columns.size = avail >= taskMinOpW+16+taskTimeW+taskSizeW
	l.columns.stateWord = l.innerW >= fixed+taskMinOpW+1
	if l.columns.stateWord {
		avail -= 1 // the glyph/word split needs its own space
	} else {
		fixed = taskMarkerW + taskIDW + 2 // only the glyph fits
		avail = l.innerW - fixed - 2
	}
	if l.columns.elapsed {
		avail -= taskTimeW
	}
	if l.columns.size {
		avail -= taskSizeW
	}
	l.columns.progress = l.innerW >= 46
	l.compact = !l.columns.stateWord || l.innerW < 46 || avail < taskMinOpW+10
	if l.compact {
		l.columns = taskColumns{}
	}
	l.columns.progW = 0
	if l.columns.progress {
		l.columns.progW = min(28, max(12, avail/2))
		avail -= l.columns.progW
	}
	l.columns.opW = max(8, avail)
	// Lines that fit, one per row (two in compact mode), minus the pinned
	// column header of the table layout.
	l.header = !l.compact && l.columns.progress
	rows := max(0, l.listH-2) // borders
	if l.header {
		rows-- // pinned header row
	}
	l.listRows = rows
	return l
}

// taskStateWord is the human name of a task state (the table and the details
// section use it; the glyph only adds colour and shape).
func taskStateWord(s *opSession) string {
	if s == nil {
		return "Unknown"
	}
	switch s.state {
	case taskPlanning:
		return "SCANNING"
	case taskRunning:
		return "RUNNING"
	case taskCanceling:
		return "STOPPING"
	case taskWaiting:
		return "WAITING"
	case taskCompleted:
		return "DONE"
	case taskCanceled:
		return "CANCELED"
	case taskPartial:
		return "PARTIAL"
	case taskFailed:
		return "FAILED"
	}
	return "UNKNOWN"
}

// taskStateGlyph is the one-cell status indicator of a task. Shapes carry the
// state even where colour is unavailable, and every glyph has a word next to it
// in the table header's column (unless the terminal is too narrow).
func taskStateGlyph(s *opSession) string {
	if s == nil {
		return "?"
	}
	switch s.state {
	case taskPlanning:
		return "…"
	case taskRunning:
		return "●"
	case taskCanceling:
		return "■"
	case taskWaiting:
		return "◐"
	case taskCompleted:
		return "✓"
	case taskCanceled:
		return "⊘"
	case taskPartial:
		return "▲"
	case taskFailed:
		return "✕"
	}
	return "?"
}

func taskStateStyle(a *App, s *opSession) lipgloss.Style {
	if s == nil {
		return a.theme.TitleText
	}
	switch s.state {
	case taskCompleted:
		return a.theme.StatusOK
	case taskCanceled, taskPartial:
		return a.theme.StatusWarn
	case taskFailed:
		return a.theme.StatusErr
	case taskWaiting:
		return a.theme.StatusWarn
	default:
		return a.theme.HelpTitle
	}
}

// taskLevel is the status-line severity that matches a task state.
func taskLevel(s *opSession) int {
	if s == nil {
		return lvlInfo
	}
	switch s.state {
	case taskCompleted:
		return lvlOK
	case taskFailed:
		return lvlErr
	case taskCanceled, taskPartial:
		return lvlWarn
	}
	return lvlInfo
}

// taskFraction returns the completion fraction and whether it is known.
func taskFraction(s *opSession) (float64, bool) {
	if s == nil {
		return 0, false
	}
	switch {
	case !s.active() && s.state == taskCompleted:
		return 1, true
	case s.prog.TotalBytes > 0 || s.prog.TotalItems > 0:
		return s.prog.Fraction(), true
	}
	return 0, false
}

// taskElapsed is the running or final duration, rendered compactly.
func taskElapsed(a *App, s *opSession) string {
	if s == nil || s.started.IsZero() {
		return ""
	}
	d := s.finished.Sub(s.started)
	if s.finished.IsZero() {
		d = a.now().Sub(s.started)
	}
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02d", int(d.Hours()), int(d.Minutes())%60)
	}
}

// taskBytesText renders "done/total" (or only the total when nothing has moved
// yet), shortened for the size column.
func taskBytesText(s *opSession) string {
	p := s.prog
	if p.TotalBytes <= 0 {
		return ""
	}
	if p.DoneBytes <= 0 {
		return fsys.HumanSize(p.TotalBytes)
	}
	return fsys.HumanSize(p.DoneBytes) + "/" + fsys.HumanSize(p.TotalBytes)
}

// taskProgressCell is the table's progress cell: a small bar with the
// percentage, plus the item counts where there is room.
func (a *App) taskProgressCell(s *opSession, w int) string {
	if w <= 0 {
		return ""
	}
	frac, known := taskFraction(s)
	// The bar already says "how far", so the text next to it can drop a word
	// and keep the counts that the bar cannot show.
	short := ""
	var parts []string
	if known {
		short = fmt.Sprintf("%.0f%%", frac*100)
		parts = append(parts, short)
	}
	items := ""
	if s.prog.TotalItems > 0 {
		items = fmt.Sprintf("%d/%d items", s.prog.DoneItems, s.prog.TotalItems)
		parts = append(parts, items)
	} else if s.prog.Phase != "" && s.active() {
		parts = append(parts, s.prog.Phase)
	}
	text := strings.Join(parts, " · ")
	if text == "" {
		text = taskFallbackText(s)
	}
	// the bar is the first thing to go when the cell is narrow
	if known && w >= taskBarW+8 {
		if items != "" {
			text = short + " · " + strings.Replace(items, " items", "", 1)
		}
		return progressBar(a.theme, frac, taskBarW) + " " + truncRight(text, w-taskBarW-1)
	}
	return truncRight(text, w)
}

// taskFallbackText describes a task that has no counters yet.
func taskFallbackText(s *opSession) string {
	switch {
	case s.state == taskWaiting && s.waitingFor != "":
		return "waiting: " + s.waitingFor
	case !s.active() && s.summary != "":
		return s.summary
	case s.state == taskPlanning:
		return "scanning…"
	case s.active():
		return "working…"
	}
	return ""
}

// ---- task screen ---------------------------------------------------------------

func (a *App) taskViewRender(height int) string {
	if height <= 0 {
		return ""
	}
	th := a.theme
	entries := a.taskEntries()
	if !a.tasksShowingAnything(entries) {
		body := []string{
			"",
			" " + th.Empty.Render("Nothing here yet."),
			" " + th.Summary.Render("Copy, move or delete something and its progress is kept here."),
		}
		box := sectionBox(th, taskBoxTitle, a.taskHeaderLabel(entries), body, a.width, min(height, 6), true, nil)
		return strings.Join(fitBlock(box, height, a.width), "\n")
	}
	l := a.taskLayout(height)
	if l.listRows == 0 {
		// Too short for the framed list: still show the selected row.
		e, _ := a.selectedTaskEntry()
		lines := a.taskDetailsForEntry(e, max(1, a.width-1))
		return strings.Join(fitBlock(lines, height, a.width), "\n")
	}
	var out []string
	out = append(out, a.taskListBox(l, true)...)
	if l.detailsH > 0 {
		out = append(out, a.taskDetailsBox(l)...)
	}
	return strings.Join(fitBlock(out, height, a.width), "\n")
}

// taskBoxTitle names the screen: tasks and recorded history are one list now.
const taskBoxTitle = "Tasks & history"

// tasksShowingAnything reports whether the list has any row besides the
// "nothing yet" placeholder of this run.
func (a *App) tasksShowingAnything(entries []taskEntry) bool {
	for _, e := range entries {
		if selectable(e) {
			return true
		}
	}
	return false
}

func (a *App) taskListBox(l taskLayout, focused bool) []string {
	th := a.theme
	entries := a.taskEntries()
	body := make([]string, 0, max(0, l.listH-2))
	if l.header {
		body = append(body, th.ColumnHeader.Render(truncRight(a.taskHeaderRow(l), l.innerW)))
	}
	lines, offset, rows := a.taskListWindow(l, entries)
	for i := offset; i < offset+rows && i < len(lines); i++ {
		body = append(body, lines[i].text)
	}
	// Empty rows keep the box tidy when the list is short.
	for len(body) < max(0, l.listH-2) {
		body = append(body, "")
	}
	return sectionBox(th, taskBoxTitle, a.taskHeaderLabel(entries),
		body, a.width, l.listH, focused, taskThumb(len(lines), rows, offset, max(0, l.listH-2), l.header))
}

// taskHeaderLabel is the right-aligned summary of the box's top border.
func (a *App) taskHeaderLabel(entries []taskEntry) string {
	active, finished, recorded := 0, 0, 0
	for _, e := range entries {
		switch e.kind {
		case taskEntryLive:
			if e.task != nil && e.task.active() {
				active++
			} else {
				finished++
			}
		case taskEntryRecord:
			recorded++
		}
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
		// Nothing to count, but a read-only store still has to say so.
		parts = append(parts, "idle")
	}
	if a.history != nil && recorded > 0 {
		parts = append(parts, fmt.Sprintf("keeping %d", a.historyRetention()))
	}
	if a.history != nil && a.history.ReadOnly() {
		parts = append(parts, "read-only")
	}
	return strings.Join(parts, " · ")
}

// taskLine is one rendered line of the list and the entry it belongs to.
type taskLine struct {
	text  string
	entry int
}

// taskListLines renders every row of the merged list: run headings, live
// tasks and recorded operations.
func (a *App) taskListLines(l taskLayout, entries []taskEntry) []taskLine {
	sel := a.selectedTaskEntryIndex(entries)
	out := make([]taskLine, 0, len(entries)*2)
	for i := range entries {
		e := entries[i]
		switch e.kind {
		case taskEntryHeading:
			out = append(out, taskLine{a.taskHeadingLine(e.title, l.innerW, e.current), i})
		case taskEntryEmpty:
			out = append(out, taskLine{a.theme.Empty.Render(fitWidth("  "+e.title, l.innerW)), i})
		case taskEntryLive:
			if e.task == nil {
				continue
			}
			for _, line := range a.taskRowLines(l, e.task, i == sel) {
				out = append(out, taskLine{line, i})
			}
		case taskEntryRecord:
			for _, line := range a.recordRowLines(l, e, i == sel) {
				out = append(out, taskLine{line, i})
			}
		}
	}
	return out
}

// taskListWindow renders the list and scrolls it so the selected entry is
// fully visible. Rendering and mouse hit-testing share it, so a click always
// lands on the row the user sees.
func (a *App) taskListWindow(l taskLayout, entries []taskEntry) (lines []taskLine, offset, rows int) {
	lines = a.taskListLines(l, entries)
	rows = max(0, l.listRows)
	if rows == 0 {
		return lines, 0, 0
	}
	offset = a.tasks.listOffset
	if sel := a.selectedTaskEntryIndex(entries); sel >= 0 {
		first, last := -1, -1
		for i := range lines {
			if lines[i].entry != sel {
				continue
			}
			if first < 0 {
				first = i
			}
			last = i
		}
		if first >= 0 {
			if first < offset {
				offset = first
			}
			if last >= offset+rows {
				offset = last - rows + 1
			}
		}
	}
	offset = max(0, min(offset, max(0, len(lines)-rows)))
	a.tasks.listOffset = offset
	return lines, offset, rows
}

// taskThumb marks the body rows of the scroll indicator on the list border.
func taskThumb(total, rows, offset, bodyRows int, header bool) []bool {
	start, size, ok := scrollbar.Thumb(total, rows, offset, rows)
	if !ok || bodyRows <= 0 {
		return nil
	}
	thumb := make([]bool, bodyRows)
	for i := start; i < start+size; i++ {
		row := i
		if header {
			row++
		}
		if row >= 0 && row < bodyRows {
			thumb[row] = true
		}
	}
	return thumb
}

func (a *App) taskHeaderRow(l taskLayout) string {
	c := l.columns
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", taskMarkerW+taskIDW))
	if c.stateWord {
		b.WriteString(padRight("State", taskStateW))
	} else {
		b.WriteString(strings.Repeat(" ", 2))
	}
	b.WriteString(padRight("Operation", c.opW))
	if c.progress {
		b.WriteString(padRight("Progress", c.progW))
	}
	if c.size {
		b.WriteString(padRight("Size", taskSizeW))
	}
	if c.elapsed {
		b.WriteString(strings.TrimRight(padRight("Time", taskTimeW), ""))
	}
	return strings.TrimRight(b.String(), " ")
}

// taskRowLines renders one task: a table row, or two lines in compact mode.
func (a *App) taskRowLines(l taskLayout, s *opSession, selected bool) []string {
	marker, glyph := "  ", taskStateGlyph(s)
	if selected {
		marker = a.theme.HelpTitle.Render("▸") + " "
	}
	style := taskStateStyle(a, s)
	if l.compact {
		// status + operation, then the progress underneath
		head := marker + a.theme.Summary.Render(fmt.Sprintf("%-2d ", s.id)) +
			style.Render(glyph) + " " + a.theme.Info.Render(truncRight(capitalize(s.title), max(6, l.innerW-8)))
		frac, known := taskFraction(s)
		detail := "  "
		if known {
			detail += a.theme.Summary.Render(fmt.Sprintf("%3.0f%%", frac*100)) + " "
		}
		detail += a.theme.Summary.Render(taskCompactDetail(s, max(6, l.innerW-6)))
		if selected {
			head = a.theme.CursorActive.Render(fitWidth(ansi.Strip(head), l.innerW))
			detail = a.theme.CursorInactive.Render(fitWidth(ansi.Strip(detail), l.innerW))
		}
		return []string{head, detail}
	}
	c := l.columns
	var b strings.Builder
	b.WriteString(marker)
	b.WriteString(a.theme.Summary.Render(fmt.Sprintf("%-2d ", s.id)))
	if c.stateWord {
		b.WriteString(style.Render(padRight(glyph+" "+taskStateWord(s), taskStateW)))
	} else {
		b.WriteString(style.Render(glyph) + " ")
	}
	b.WriteString(a.theme.Info.Render(padRight(truncRight(capitalize(s.title), c.opW-1), c.opW)))
	if c.progress {
		// one cell of the progress column is the gap before the size column
		b.WriteString(padRight(a.taskProgressCell(s, c.progW-1), c.progW))
	}
	if c.size {
		b.WriteString(padRight(truncRight(taskBytesText(s), taskSizeW-1), taskSizeW))
	}
	if c.elapsed {
		b.WriteString(padRight(taskElapsed(a, s), taskTimeW))
	}
	row := truncRight(b.String(), l.innerW)
	if selected {
		return []string{a.theme.CursorActive.Render(fitWidth(ansi.Strip(row), l.innerW))}
	}
	return []string{fitWidth(row, l.innerW)}
}

// taskCompactDetail is the second line of a compact task row.
func taskCompactDetail(s *opSession, w int) string {
	var parts []string
	if s.prog.TotalItems > 0 {
		parts = append(parts, fmt.Sprintf("%d/%d items", s.prog.DoneItems, s.prog.TotalItems))
	}
	if s.prog.TotalBytes > 0 {
		parts = append(parts, taskBytesText(s))
	}
	if s.state == taskWaiting && s.waitingFor != "" {
		parts = append(parts, "waiting: "+s.waitingFor)
	} else if !s.active() && s.summary != "" {
		parts = append(parts, fsys.SanitizeName(s.summary))
	} else if s.prog.Current != "" && s.active() {
		parts = append(parts, fsys.SanitizeName(s.prog.Current))
	}
	if len(parts) == 0 {
		parts = append(parts, taskFallbackText(s))
	}
	return truncRight(strings.Join(parts, " · "), w)
}

func (a *App) taskDetailsBox(l taskLayout) []string {
	e, ok := a.selectedTaskEntry()
	th := a.theme
	rows := max(0, l.detailsH-2)
	var details []string
	right := ""
	if ok {
		right, details = a.taskDetailsHeading(e), a.taskDetailsForEntry(e, max(1, l.innerW))
	}
	if a.tasks.detailOffset > max(0, len(details)-rows) {
		a.tasks.detailOffset = max(0, len(details)-rows)
	}
	body := make([]string, 0, rows)
	visible := details[min(a.tasks.detailOffset, len(details)):]
	for _, line := range visible {
		if len(body) == rows {
			break
		}
		body = append(body, line)
	}
	var thumb []bool
	if len(visible) > rows && rows > 0 {
		thumb = make([]bool, rows)
		thumb[max(0, min(rows-1, a.tasks.detailOffset*rows/max(1, len(visible)+a.tasks.detailOffset)))] = true
	}
	return sectionBox(th, "Details", right, body, a.width, l.detailsH, false, thumb)
}

// taskDetailsHeading is the label on the Details box's right border.
func (a *App) taskDetailsHeading(e taskEntry) string {
	switch e.kind {
	case taskEntryLive:
		if e.task == nil {
			return ""
		}
		right := fmt.Sprintf("#%d · %s", e.task.id, taskStateWord(e.task))
		if frac, known := taskFraction(e.task); known && e.task.active() {
			right += fmt.Sprintf(" · %.0f%%", frac*100)
		}
		return right
	case taskEntryRecord:
		if e.rec == nil {
			return ""
		}
		return fmt.Sprintf("%s · %s", fsys.SanitizeName(e.rec.Op), e.rec.Status.Label())
	}
	return ""
}

// taskDetailsForEntry renders the details of whichever kind of row is
// selected: a live task (with its progress) or a recorded operation.
func (a *App) taskDetailsForEntry(e taskEntry, width int) []string {
	switch e.kind {
	case taskEntryLive:
		return a.taskDetailsRows(e.task, width)
	case taskEntryRecord:
		return a.recordDetailRows(e, width)
	}
	return nil
}

// taskDetailsRows is the label/value table of one task: a tidy two-column
// layout whose labels line up, with paths shortened on the left (the file name
// is the useful part) and everything else on the right.
func (a *App) taskDetailsRows(s *opSession, width int) []string {
	if s == nil {
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
	add("Operation", capitalize(s.title))
	state := taskStateWord(s)
	if s.state == taskWaiting && s.waitingFor != "" {
		state += " · " + s.waitingFor
	}
	add("State", state+" · "+taskElapsed(a, s)+" elapsed")
	if frac, known := taskFraction(s); known {
		barW := max(6, min(30, valueW-8))
		line := progressBar(th, frac, barW) + fmt.Sprintf(" %3.0f%%", frac*100)
		lines = append(lines, th.PropLabel.Render(padRight("Progress", taskLabelW))+line)
		if s.prog.TotalItems > 0 || s.prog.DoneItems > 0 {
			items := fmt.Sprintf("%d of %d items", s.prog.DoneItems, s.prog.TotalItems)
			if s.prog.TotalBytes > 0 {
				items += " · " + taskBytesText(s)
			}
			lines = append(lines, strings.Repeat(" ", taskLabelW)+th.Summary.Render(truncRight(items, valueW)))
		}
	} else {
		add("Progress", taskFallbackText(s))
	}
	if len(s.from) > 0 {
		const sourceCap = 6
		for i, p := range s.from {
			if i == sourceCap {
				lines = append(lines, th.PropLabel.Render(padRight("Source", taskLabelW))+
					th.Summary.Render(fmt.Sprintf("… %d more", len(s.from)-sourceCap)))
				break
			}
			label := ""
			if i == 0 {
				label = "From:"
			}
			lines = append(lines, th.PropLabel.Render(padRight(label, taskLabelW))+
				th.DialogPath.Render(truncLeft(fsys.SanitizeName(p), valueW)))
		}
	}
	if s.to != "" {
		lines = append(lines, th.PropLabel.Render(padRight("To:", taskLabelW))+
			th.DialogPath.Render(truncLeft(fsys.SanitizeName(s.to), valueW)))
	}
	if s.prog.Current != "" && s.active() {
		lines = append(lines, th.PropLabel.Render(padRight("Current", taskLabelW))+
			th.DialogPath.Render(truncLeft(fsys.SanitizeName(s.prog.Current), valueW)))
	}
	if s.summary != "" {
		add("Result", s.summary)
	}
	if s.prog.LastError != "" {
		// Errors start with the item and end with the reason. Keeping the end
		// ("permission denied", "no space left") matters more than the path,
		// and one line keeps the details table short on phones.
		lines = append(lines, th.PropLabel.Render(padRight("Last error", taskLabelW))+
			th.Error.Render(truncLeft(fsys.SanitizeName(s.prog.LastError), valueW)))
	}
	if s.finished.IsZero() && !s.started.IsZero() {
		add("Started", s.started.Format("15:04:05"))
	} else if !s.finished.IsZero() {
		add("Finished", s.finished.Format("15:04:05"))
	}
	for i, detail := range s.details {
		if i == 12 {
			lines = append(lines, th.PropLabel.Render(padRight("", taskLabelW))+
				th.Summary.Render(fmt.Sprintf("… %d more", len(s.details)-12)))
			break
		}
		add("•", detail)
	}
	if width > 0 {
		for i, line := range lines {
			lines[i] = ansi.Truncate(line, width, "…")
		}
	}
	return lines
}

// taskFooterHints returns the actions available while the task list is open.
// They follow the selected row: a recorded operation offers no actions of its
// own, and only a running task can be watched or cancelled.
func (a *App) taskFooterHints() []barItem {
	out := []barItem{
		a.bindAll("close", "tasks.close"),
		item("↑↓", "select", ""),
	}
	if a.taskWatchable() {
		out = append(out, a.bind("tasks.watch", "watch live"))
	}
	switch e, ok := a.selectedTaskEntry(); {
	case ok && e.kind == taskEntryRecord:
		// read-only row: no cancel, no watch, no clear
	case ok && e.task != nil && e.task.active():
		out = append(out, a.bind("tasks.cancel", "cancel selected"))
	case ok:
		out = append(out, a.bind("tasks.forget", "clear finished"))
	}
	if len(a.taskSectionsWithRows()) > 1 {
		out = append(out, a.bindAll("run", "tasks.prevRun", "tasks.nextRun"))
	}
	if a.hasRecordedOps() {
		out = append(out, item("+/-", "keep more/fewer", ""))
	}
	return append(out, item("PgUp/Dn", "details", ""))
}

// taskSectionsWithRows lists the run sections that have at least one row.
func (a *App) taskSectionsWithRows() []int {
	var out []int
	for i, e := range a.taskEntries() {
		if e.kind == taskEntryHeading {
			out = append(out, i)
		}
	}
	return out
}

// hasRecordedOps reports whether anything from an earlier run is listed.
func (a *App) hasRecordedOps() bool {
	cur := a.currentSessionIndex(a.historySessions())
	for _, e := range a.taskEntries() {
		if e.kind == taskEntryRecord && e.sess != cur {
			return true
		}
	}
	return false
}

// taskCountText is used in the ordinary status line without obscuring more
// useful messages set by the operation result handlers.
func (a *App) taskCountText() string {
	if n := a.tasks.activeCount(); n > 0 {
		return fmt.Sprintf("%s · %d background task(s) active (t to view)", a.tasks.latestActive().title, n)
	}
	return ""
}

func (a *App) refreshPanelAt(index int, expectedPath, focus string) tea.Cmd {
	if index < 0 || index >= len(a.panels) || expectedPath == "" {
		return nil
	}
	p := a.panels[index]
	if p == nil || p.Path() != expectedPath {
		return nil
	}
	if focus != "" {
		return p.RefreshFocus(focus)
	}
	return p.Refresh()
}

func (a *App) sourceSelectionUnchanged(s *opSession) bool {
	if s == nil || s.srcPanel < 0 || s.srcPanel >= len(a.panels) {
		return false
	}
	p := a.panels[s.srcPanel]
	if p == nil || p.Path() != s.panelPaths[s.srcPanel] {
		return false
	}
	current := p.SelectedNames()
	if len(current) != len(s.selection) {
		return false
	}
	for _, name := range current {
		if !s.selection[name] {
			return false
		}
	}
	return true
}

func (a *App) refreshPanelsAt(dir, focus string) tea.Cmd {
	if dir == "" {
		return nil
	}
	var cmds []tea.Cmd
	for i, p := range a.panels {
		if p != nil && p.Path() == dir {
			f := ""
			if focus != "" {
				f = focus
			}
			cmds = append(cmds, a.refreshPanelAt(i, dir, f))
		}
	}
	return tea.Batch(cmds...)
}
