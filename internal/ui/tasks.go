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

	// persistent operation history view (history.go / historyview.go)
	histShowing bool
	histSession int // selected run, 0 = newest
	histOp      int // selected operation within that run, 0 = newest
	histOffset  int // first detail line shown
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
	m.selected = s.id
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
}

func (a *App) openTasks() {
	a.tasks.showing = true
	a.tasks.histShowing = false
	if s := a.tasks.latestActive(); s != nil {
		a.tasks.selected = s.id
	} else if s := a.tasks.selectedTask(); s != nil {
		a.tasks.selected = s.id
	}
	a.tasks.listOffset, a.tasks.detailOffset = 0, 0
}

func (a *App) closeTasks() {
	a.tasks.showing = false
}

func (a *App) taskViewKey(msg tea.KeyMsg) tea.Cmd {
	if a.tasks.histShowing {
		if cmd, handled := a.historyViewKey(msg); handled {
			return cmd
		}
	}
	switch msg.String() {
	case "h":
		// Switch between this run's tasks and the recorded history.
		a.tasks.histShowing = !a.tasks.histShowing
		if a.tasks.histShowing {
			a.resetHistorySelection()
		}
		a.tasks.histOffset = 0
	case "esc", "t":
		a.closeTasks()
		a.setStatus("", false)
	case "up", "k":
		a.tasks.selectDelta(-1)
	case "down", "j":
		a.tasks.selectDelta(1)
	case "pgup", "ctrl+b":
		a.tasks.detailOffset = max(0, a.tasks.detailOffset-5)
	case "pgdown", "ctrl+f":
		a.tasks.detailOffset += 5
	case "c", "ctrl+c":
		if s := a.tasks.selectedTask(); s != nil && s.active() {
			a.cancelTask(s.id)
		} else {
			a.setStatusLevel("The selected task has already finished", lvlInfo)
		}
	case "x":
		n := a.tasks.clearFinished()
		if n == 0 {
			a.setStatusLevel("No completed tasks to clear", lvlInfo)
		} else {
			a.setStatusLevel(fmt.Sprintf("Cleared %s", plural(n, "completed task")), lvlOK)
		}
	}
	return nil
}

func (a *App) taskViewMouse(ev tea.MouseEvent) tea.Cmd {
	if a.tasks.histShowing {
		return a.historyViewMouse(ev)
	}
	if ev.IsWheel() {
		delta := wheelDelta(ev)
		listRows := taskListRows(a.height-3, len(a.tasks.order))
		if ev.Y >= 2+listRows {
			a.tasks.detailOffset = max(0, a.tasks.detailOffset+delta)
		} else if delta != 0 {
			a.tasks.selectDelta(delta)
		}
		return nil
	}
	if ev.Action == tea.MouseActionRelease && ev.Button == tea.MouseButtonLeft {
		listRows := taskListRows(a.height-3, len(a.tasks.order))
		row := ev.Y - 2
		if row >= 0 && row < listRows {
			idx := a.tasks.listOffset + row
			items := a.tasks.newest()
			if idx < len(items) {
				a.tasks.selected = items[idx].id
				a.tasks.detailOffset = 0
			}
		}
	}
	return nil
}

func taskListRows(bodyHeight, total int) int {
	if total <= 0 || bodyHeight <= 1 {
		return 0
	}
	return min(total, max(1, (bodyHeight-1)/2))
}

func taskStateLabel(s *opSession) string {
	if s == nil {
		return "UNKNOWN"
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
	default:
		return "UNKNOWN"
	}
}

func taskProgressLabel(s *opSession) string {
	if s == nil {
		return ""
	}
	if s.state == taskWaiting && s.waitingFor != "" {
		return "waiting: " + s.waitingFor
	}
	if !s.active() && s.summary != "" {
		return fsys.SanitizeName(s.summary)
	}
	p := s.prog
	if p.TotalBytes > 0 {
		return fmt.Sprintf("%.0f%% · %s/%s · %s/%s", p.Fraction()*100,
			fsys.HumanSize(p.DoneBytes), fsys.HumanSize(p.TotalBytes),
			plural(p.DoneItems, "item"), plural(p.TotalItems, "item"))
	}
	if p.TotalItems > 0 {
		return fmt.Sprintf("%s · %d/%d items", p.Phase, p.DoneItems, p.TotalItems)
	}
	if p.Phase != "" {
		return p.Phase + "…"
	}
	return "working…"
}

func taskRowStyle(a *App, s *opSession, selected bool) lipgloss.Style {
	if selected {
		return a.theme.CursorActive
	}
	switch s.state {
	case taskCompleted:
		return a.theme.StatusOK
	case taskCanceled, taskPartial:
		return a.theme.StatusWarn
	case taskFailed:
		return a.theme.StatusErr
	default:
		return a.theme.TitleText
	}
}

func (a *App) taskViewRender(height int) string {
	if height <= 0 {
		return ""
	}
	if a.tasks.histShowing {
		return a.historyViewRender(height)
	}
	width := a.width
	items := a.tasks.newest()
	active := a.tasks.activeCount()
	lines := []string{a.theme.HelpTitle.Render(fmt.Sprintf("Background tasks · %d active · %d retained", active, len(items)))}
	listRows := taskListRows(height, len(items))
	visible := a.tasks.visibleTasks(listRows)
	if len(items) == 0 {
		lines = append(lines, a.theme.Empty.Render(fitWidth("No background tasks yet. Start a copy, move, delete or archive operation.", width)))
	} else {
		for _, s := range visible {
			marker := "  "
			if s.id == a.tasks.selected {
				marker = "> "
			}
			location := s.prog.Current
			if location == "" && len(s.from) > 0 {
				location = s.from[0]
			}
			row := fmt.Sprintf("%s#%-3d %-8s %-22s %s", marker, s.id, taskStateLabel(s), s.title, taskProgressLabel(s))
			if location != "" {
				row += " · " + fsys.SanitizeName(location)
			}
			lines = append(lines, taskRowStyle(a, s, s.id == a.tasks.selected).Render(fitWidth(row, width)))
		}
	}

	remaining := height - len(lines)
	if s := a.tasks.selectedTask(); s != nil && remaining > 0 {
		details := a.taskDetails(s, width)
		if a.tasks.detailOffset >= len(details) {
			a.tasks.detailOffset = max(0, len(details)-1)
		}
		for _, line := range details[a.tasks.detailOffset:] {
			if remaining == 0 {
				break
			}
			lines = append(lines, line)
			remaining--
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

func (a *App) taskDetails(s *opSession, width int) []string {
	if s == nil {
		return nil
	}
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
	lines := []string{a.theme.HelpTitle.Render(fmt.Sprintf("Details · task #%d", s.id))}
	lines = append(lines, label("Operation: ", fsys.SanitizeName(s.title))...)
	lines = append(lines, label("State: ", taskStateLabel(s))...)
	if !s.started.IsZero() {
		lines = append(lines, label("Started: ", s.started.Format("2006-01-02 15:04:05"))...)
		if s.finished.IsZero() {
			lines = append(lines, label("Elapsed: ", a.now().Sub(s.started).Round(time.Second).String())...)
		} else {
			lines = append(lines, label("Duration: ", s.finished.Sub(s.started).Round(time.Millisecond).String())...)
		}
	}
	if len(s.from) > 0 {
		const sourceCap = 20
		for i, p := range s.from {
			if i == sourceCap {
				lines = append(lines, label("       ", fmt.Sprintf("… %d more source paths", len(s.from)-sourceCap))...)
				break
			}
			prefix := "From: "
			if i > 0 {
				prefix = "       "
			}
			lines = append(lines, label(prefix, fsys.SanitizeName(p))...)
		}
	}
	lines = append(lines, label("To: ", fsys.SanitizeName(s.to))...)
	if s.waitingFor != "" && s.active() {
		lines = append(lines, label("Waiting for: ", s.waitingFor)...)
	}
	if s.prog.Current != "" && s.active() {
		lines = append(lines, label("Current: ", fsys.SanitizeName(s.prog.Current))...)
	}
	if s.active() {
		lines = append(lines, label("Progress: ", taskProgressLabel(s))...)
	}
	if s.summary != "" {
		lines = append(lines, label("Result: ", fsys.SanitizeName(s.summary))...)
	}
	if s.prog.LastError != "" {
		lines = append(lines, label("Last error: ", fsys.SanitizeName(s.prog.LastError))...)
	}
	for _, detail := range s.details {
		lines = append(lines, label("• ", fsys.SanitizeName(detail))...)
	}
	if width > 0 {
		for i, line := range lines {
			lines[i] = ansi.Truncate(line, width, "…")
		}
	}
	return lines
}

// taskFooterHints returns the actions available while the task list is open.
func (a *App) taskFooterHints() []barItem {
	if a.tasks.histShowing {
		return a.historyFooterHints()
	}
	return []barItem{
		item("t/Esc", "close tasks", "esc"),
		item("↑↓", "select", ""),
		item("c", "cancel selected", "c"),
		item("PgUp/Dn", "details", ""),
		item("x", "clear finished", "x"),
		item("h", "past runs", "h"),
	}
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
