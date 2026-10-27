package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
)

// live progress: a foreground screen that follows one background task.
//
// Large copy, move and delete operations ask how they should run, and "watch
// progress" opens this screen. It is a view of the same opSession the task list
// shows — nothing about the operation changes — so it can be left at any time
// (Esc: keep running in the background, with a notification) and re-opened from
// the task list (⏎). Prompts that need a decision (conflicts, free space) are
// modal dialogs drawn over it as usual, and the screen shows that it is waiting
// for the answer.

// liveTask records which task the foreground screen follows.
type liveTask struct {
	id     int
	scroll int // first detail row shown
}

// watchTask opens the live progress screen for a task.
func (a *App) watchTask(s *opSession) {
	if s == nil {
		return
	}
	a.live = &liveTask{id: s.id}
	a.tasks.selected = s.id
}

// watchSelectedTask opens the live progress screen for the task selected in the
// task list. Finished tasks have nothing to watch: they keep their result in
// the list.
func (a *App) watchSelectedTask() tea.Cmd {
	e, ok := a.selectedTaskEntry()
	if !ok {
		a.setStatusLevel("No task to watch", lvlInfo)
		return nil
	}
	if e.kind == taskEntryRecord {
		// A recorded operation has no live progress to show.
		a.setStatusLevel("Recorded operations are read-only — nothing to watch", lvlInfo)
		return nil
	}
	s := e.task
	if s == nil {
		a.setStatusLevel("No task to watch", lvlInfo)
		return nil
	}
	if !s.active() {
		a.setStatusLevel("This task has finished — its result stays in the task list", lvlInfo)
		return nil
	}
	a.tasks.showing = false
	a.watchTask(s)
	return nil
}

// liveTaskSession returns the task the foreground screen follows, if any.
func (a *App) liveTaskSession() *opSession {
	if a.live == nil {
		return nil
	}
	return a.tasks.get(a.live.id)
}

// closeLive leaves the foreground screen. A task that is still running keeps
// running in the background and announces itself with a notification, which is
// the whole point of leaving early.
func (a *App) closeLive(notify bool) tea.Cmd {
	s := a.liveTaskSession()
	a.live = nil
	if s != nil && s.active() && notify {
		a.notifyBackground(s)
	}
	return nil
}

// liveKey handles the keys of the live progress screen. Panel keys do not reach
// the panels from here: the screen is a view of one operation, not a second
// browser.
func (a *App) liveKey(msg tea.KeyMsg) tea.Cmd {
	s := a.liveTaskSession()
	if s == nil {
		a.live = nil
		return nil
	}
	switch msg.String() {
	case "esc", "q":
		// Leaving does not stop anything: the task keeps running and says so
		// in the notification instead of pretending it was cancelled.
		running := s.active()
		cmd := a.closeLive(running)
		if !running && s.summary != "" {
			a.setStatusLevel(s.summary, taskLevel(s))
		}
		return cmd
	case "t":
		a.live = nil
		a.openTasks()
		return nil
	case "c", "ctrl+c":
		if !s.active() {
			a.setStatusLevel("This task has already finished", lvlInfo)
			return nil
		}
		a.cancelTask(s.id)
		return nil
	case "up", "k", "pgup", "ctrl+b":
		a.live.scroll = max(0, a.live.scroll-1)
		return nil
	case "down", "j", "pgdown", "ctrl+f":
		a.live.scroll++
		return nil
	case "?":
		a.openHelp()
		return nil
	case "enter":
		if !s.active() {
			return a.closeLive(false)
		}
		return nil
	}
	return nil
}

// liveMouse scrolls the details block; taps do nothing (the screen has no
// targets).
func (a *App) liveMouse(ev tea.MouseEvent) tea.Cmd {
	if a.live == nil {
		return nil
	}
	if ev.IsWheel() {
		a.live.scroll = max(0, a.live.scroll+wheelDelta(ev))
	}
	return nil
}

// liveViewRender draws the foreground progress screen.
func (a *App) liveViewRender(height int) string {
	if height <= 0 {
		return ""
	}
	s := a.liveTaskSession()
	if s == nil {
		a.live = nil
		return strings.Join(fitBlock([]string{""}, height, a.width), "\n")
	}
	th := a.theme
	title := capitalize(s.title)
	right := taskStateWord(s)
	if frac, known := taskFraction(s); known && s.active() {
		right += fmt.Sprintf(" · %.0f%%", frac*100)
	}
	body := a.liveBody(s, max(1, a.width-2))
	rows := max(0, height-2)
	// Scroll the body when it is taller than the box (short terminals).
	maxScroll := max(0, len(body)-rows)
	scroll := min(max(0, a.live.scroll), maxScroll)
	body = body[scroll:]
	var thumb []bool
	if len(body) > rows && rows > 0 {
		thumb = make([]bool, rows)
		thumb[min(rows-1, max(0, scroll*rows/max(1, len(body))))] = true
	}
	lines := sectionBox(th, title, right, body, a.width, height, true, thumb)
	return strings.Join(lines, "\n")
}

// liveBody is the content of the live screen: the big progress bar first, then
// the same facts the details table shows, then the way out.
func (a *App) liveBody(s *opSession, width int) []string {
	th := a.theme
	var out []string
	valueW := max(8, width-taskLabelW-1)
	add := func(label, value string) {
		if value == "" {
			return
		}
		for i, part := range wrapText(fsys.SanitizeName(value), valueW) {
			if i == 0 {
				out = append(out, th.PropLabel.Render(padRight(label, taskLabelW))+th.DialogText.Render(part))
				continue
			}
			out = append(out, strings.Repeat(" ", taskLabelW)+th.DialogText.Render(part))
		}
	}
	frac, known := taskFraction(s)
	if known {
		barW := max(8, min(40, width-10))
		line := progressBar(th, frac, barW) + fmt.Sprintf(" %3.0f%%", frac*100)
		if s.active() {
			out = append(out, line)
			var counts []string
			if s.prog.TotalItems > 0 {
				counts = append(counts, fmt.Sprintf("%d of %d items", s.prog.DoneItems, s.prog.TotalItems))
			}
			if s.prog.TotalBytes > 0 {
				counts = append(counts, taskBytesText(s))
			}
			if len(counts) > 0 {
				out = append(out, th.Summary.Render(strings.Join(counts, " · ")))
			}
		} else {
			// finished: the result is the headline, the bar a summary
			out = append(out, line)
		}
	} else {
		out = append(out, th.Summary.Render(taskFallbackText(s)))
	}
	out = append(out, "")
	if len(s.from) > 0 {
		for i, p := range s.from {
			if i == 3 {
				add("Source", fmt.Sprintf("… %d more", len(s.from)-3))
				break
			}
			label := ""
			if i == 0 {
				label = "Source"
			}
			add(label, p)
		}
	}
	add("Target", s.to)
	if s.prog.Current != "" && s.active() {
		add("Current", s.prog.Current)
	}
	add("Elapsed", taskElapsed(a, s))
	if s.state == taskWaiting && s.waitingFor != "" {
		add("Waiting", s.waitingFor+" (a dialog is open)")
	}
	if s.prog.LastError != "" {
		// keep the reason at the end of the message, like the details table
		out = append(out, th.Error.Render(truncLeft("Last error: "+fsys.SanitizeName(s.prog.LastError), width)))
	}
	if !s.active() {
		if s.summary != "" {
			add("Result", s.summary)
		}
		for _, detail := range s.details {
			if len(out) > 40 {
				break
			}
			add("•", detail)
		}
	}
	out = append(out, "")
	hint := "Esc keeps it running in the background · c cancels · t task list"
	if !s.active() {
		hint = "Enter or Esc closes · the result stays in the task list (t)"
	}
	out = append(out, th.Summary.Render(hint))
	return out
}
