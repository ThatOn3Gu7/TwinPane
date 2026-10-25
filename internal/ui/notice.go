package ui

import (
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/theme"
)

// Notifications
//
// A notification is a small, self-erasing pop-up in the bottom-right corner of
// the content area that reports the outcome of something the user asked for:
// "✓ Copied 61 files, 16 folders", "! Move cancelled — nothing was transferred",
// "✕ cannot delete: permission denied". It is deliberately not a dialog: it
// takes no keys, it never blocks the panels, and it goes away by itself.
//
// Which messages become notifications, and which stay in the status line:
//
//   - notifications report finished actions (results): a file operation, an
//     archive extract or compress, a cloud operation, a rename, a bookmark, an
//     open/share, a finished edit, a folder-size scan, a directory plan;
//   - the status line keeps everything that is not a result: progress
//     ("Checking copy…"), prompts and refusals ("nothing to copy: put the
//     cursor on an item"), navigation notes, mode toggles and selection
//     counts. Those are context for what to do next, and they belong next to
//     the panels, not in a pop-up.
//
// Only one notification is shown at a time and a newer result replaces an older
// one, so a burst of operations cannot stack up; repeating the very same result
// restarts its countdown instead of flickering. The countdown pauses while a
// modal dialog is open (the pop-up is not drawn there either), so a result
// cannot expire unseen behind a prompt.

const (
	// noticeLife is how long a notification stays before it fades out.
	noticeLife = 4 * time.Second
	// noticeTick is the countdown frame. The bar drains by width/50 per frame,
	// which reads as a smooth sweep rather than a jumping number.
	noticeTick = 80 * time.Millisecond
	// noticeMaxWidth bounds the pop-up so it stays a corner note on wide
	// terminals instead of becoming a banner.
	noticeMaxWidth = 46
)

// noticeKind selects the colours, the border and the icon of a notification.
type noticeKind int

const (
	noticeInfo noticeKind = iota
	noticeOK
	noticeWarn
	noticeErr
)

// notice is one pop-up.
type notice struct {
	seq   int // identifies the countdown that belongs to this notice
	kind  noticeKind
	title string   // what happened
	body  []string // optional context (destination, reason, hint)
	task  int      // task a tap opens (0 = a tap only dismisses)

	total time.Duration // countdown length, for the drain bar
	left  time.Duration
}

// noticeTickMsg advances the countdown of one notice.
type noticeTickMsg struct{ seq int }

// noticeIcon is the one-cell indicator prefixed to the heading. Shape carries
// the kind even where colours are unavailable.
func noticeIcon(k noticeKind) string {
	switch k {
	case noticeOK:
		return "✓"
	case noticeWarn:
		return "!"
	case noticeErr:
		return "✕"
	}
	return "ℹ"
}

// noticeWord is the heading fallback for messages too long to fit there.
func noticeWord(k noticeKind) string {
	switch k {
	case noticeOK:
		return "Done"
	case noticeWarn:
		return "Warning"
	case noticeErr:
		return "Error"
	}
	return "Note"
}

// noticeStyle is the border/heading style of a kind.
func noticeStyle(th theme.Theme, k noticeKind) lipgloss.Style {
	switch k {
	case noticeOK:
		return th.NoticeOK
	case noticeWarn:
		return th.NoticeWarn
	case noticeErr:
		return th.NoticeErr
	}
	return th.NoticeInfo
}

// notify shows a notification. body holds optional extra lines; they are
// wrapped and capped by the renderer.
func (a *App) notify(kind noticeKind, title string, body ...string) {
	if a.quitting || title == "" {
		return
	}
	if n := a.notice; n != nil && n.kind == kind && n.title == title &&
		strings.Join(n.body, "\n") == strings.Join(body, "\n") {
		// The same result again (a repeated action): keep the pop-up where it
		// is and restart its countdown instead of flickering.
		n.left = n.total
		return
	}
	lines := make([]string, 0, len(body))
	for _, line := range body {
		if line != "" {
			lines = append(lines, line)
		}
	}
	a.noticeSeq++
	a.notice = &notice{
		seq: a.noticeSeq, kind: kind, title: title,
		body:  lines,
		total: noticeLife, left: noticeLife,
	}
}

// result reports the outcome of a finished action with a status level. It is
// the notification counterpart of setStatusLevel: call it where the message
// describes what happened, and leave setStatusLevel for progress, prompts and
// guidance.
func (a *App) result(lvl int, msg string, body ...string) {
	a.notify(resultKind(lvl), msg, body...)
}

// taskResult reports the outcome of a finished background task. Tapping the
// notification opens the task list, where the full details are kept.
func (a *App) taskResult(s *opSession, lvl int, msg string, body ...string) {
	if s != nil && a.live != nil && a.live.id == s.id {
		// The live progress screen is already showing this result; a pop-up on
		// top of it would only repeat what the user is reading.
		return
	}
	a.result(lvl, msg, body...)
	if s != nil && a.notice != nil {
		a.notice.task = s.id
	}
}

func resultKind(lvl int) noticeKind {
	switch lvl {
	case lvlOK:
		return noticeOK
	case lvlWarn:
		return noticeWarn
	case lvlErr:
		return noticeErr
	}
	return noticeInfo
}

// notifyBackground shows the notification for a task that just went to the
// background: what is running, how far it is, and that "t" shows the rest.
func (a *App) notifyBackground(s *opSession) {
	if s == nil {
		return
	}
	a.notify(noticeInfo, capitalize(s.title)+" in the background", noticeBody(s), noticeTaskHint)
	if a.notice != nil {
		a.notice.task = s.id
	}
}

// detailsBody turns the first few detail lines of a finished task into
// notification body lines: the pop-up stays small and points at the task list
// for the rest.
func detailsBody(details []string) []string {
	const maxLines = 2
	out := make([]string, 0, maxLines+1)
	for i, d := range details {
		if i == maxLines {
			out = append(out, "… and "+strconv.Itoa(len(details)-maxLines)+" more in the task list (t)")
			break
		}
		out = append(out, fsys.SanitizeName(d))
	}
	return out
}

// formatPercent renders a progress fraction the way notifications and the task
// table do ("29%").
func formatPercent(frac float64) string {
	return strconv.Itoa(int(frac*100+0.5)) + "%"
}

// formatItems renders "1284/4310 items".
func formatItems(done, total int) string {
	return strconv.Itoa(done) + "/" + strconv.Itoa(total) + " items"
}

// noticeTaskHint is the second line of a background-task notification.
const noticeTaskHint = "t opens the task list"

func noticeBody(s *opSession) string {
	var parts []string
	if s.prog.TotalBytes == 0 && s.prog.TotalItems == 0 && s.est.Items() > 0 {
		// The operation has just started and has not counted anything yet:
		// report what it was estimated to be instead of a made-up 0%.
		parts = append(parts, strings.TrimSuffix(estimateText(s.est), "."))
	} else {
		if frac, known := taskFraction(s); known {
			parts = append(parts, formatPercent(frac))
		}
		if s.prog.TotalBytes > 0 {
			parts = append(parts, taskBytesText(s))
		} else if s.prog.TotalItems > 0 {
			parts = append(parts, formatItems(s.prog.DoneItems, s.prog.TotalItems))
		}
	}
	if s.state == taskWaiting && s.waitingFor != "" {
		parts = append(parts, "waiting for you")
	}
	if len(parts) == 0 {
		parts = append(parts, "starting")
	}
	return strings.Join(parts, " · ")
}

// dismissNotice removes the notification (a key, a tap, or its countdown).
func (a *App) dismissNotice() {
	a.notice = nil
}

// syncNotice keeps the countdown running while a notification is visible and
// returns the command that drives it. It is called at the end of Update, the
// same way the spinner is scheduled, so no call site has to thread a command.
func (a *App) syncNotice(cmd tea.Cmd) tea.Cmd {
	if a.notice == nil {
		a.noticeTicking = false
		return cmd
	}
	if a.noticeTicking {
		return cmd
	}
	if a.noticePaused() {
		return cmd // do not let a result expire while a dialog covers it
	}
	a.noticeTicking = true
	seq := a.notice.seq
	return tea.Batch(cmd, tea.Tick(noticeTick, func(time.Time) tea.Msg {
		return noticeTickMsg{seq: seq}
	}))
}

// noticePaused reports whether the countdown has to wait. The pop-up is not
// drawn over a modal dialog or while the editor owns the terminal, so its time
// must not run out there either.
func (a *App) noticePaused() bool {
	return a.dialog != nil || a.editing
}

// noticeAdvance applies one countdown frame and reports whether the notice is
// still alive.
func (a *App) noticeAdvance(msg noticeTickMsg) bool {
	n := a.notice
	a.noticeTicking = false
	if n == nil || n.seq != msg.seq {
		return n != nil
	}
	if a.noticePaused() {
		return true
	}
	n.left -= noticeTick
	if n.left <= 0 {
		a.dismissNotice()
		return false
	}
	return true
}

// ---- rendering ------------------------------------------------------------------

// noticeBox builds the pop-up: a heading with the kind's icon and colour, the
// message (or the wrapped message when it does not fit the heading), the
// optional context lines and a countdown bar that drains from right to left.
// Width is the box width including its border.
func (a *App) noticeBox(width int) []string {
	return a.noticeBoxWithin(width, 0)
}

// noticeBoxWithin is noticeBox limited to maxLines rows. A short terminal gets
// the heading and the countdown first and loses body lines, so the pop-up is
// always drawn whole instead of disappearing when the content area is small.
func (a *App) noticeBoxWithin(width, maxLines int) []string {
	n := a.notice
	if n == nil || width < 16 {
		return nil
	}
	th := a.theme
	st := noticeStyle(th, n.kind)
	inner := width - 2
	icon := noticeIcon(n.kind)

	// The heading carries the whole message when it fits in the border. A long
	// message (an error reason usually) keeps its first clause there and moves
	// the rest into the body, so nothing important is cut off and the heading
	// stays meaningful instead of degrading to a bare "Error".
	headText := icon + " " + n.title
	var carried []string
	if ansi.StringWidth(headText) > inner-4 {
		// "╭─" + " icon heading " + at least one dash + "╮"
		head, rest := splitNoticeTitle(n.title, inner-7)
		headText = icon + " " + head
		if rest != "" {
			carried = append(carried, rest)
		}
	}
	body := carried
	for _, line := range n.body {
		for _, part := range wrapText(line, inner-2) {
			body = append(body, th.DialogHint.Render(part))
		}
	}
	// Keep the pop-up small: the task list has the full record.
	if limit := 5; len(body) > limit {
		body = body[:limit]
		body[limit-1] = th.DialogHint.Render("…")
	}
	if maxLines > 0 {
		// heading + countdown + border rows take three lines
		if room := maxLines - 3; room < len(body) {
			if room < 0 {
				room = 0
			}
			body = body[:room]
		}
	}

	lines := make([]string, 0, len(body)+3)
	lines = append(lines, noticeTop(st, headText, width))
	for _, line := range body {
		lines = append(lines, st.Render("│")+" "+fitWidth(line, inner-2)+" "+st.Render("│"))
	}
	frac := 0.0
	if n.total > 0 {
		frac = float64(n.left) / float64(n.total)
	}
	bar := noticeCountdown(st, frac, max(1, inner-2))
	lines = append(lines, st.Render("│")+" "+bar+" "+st.Render("│"))
	lines = append(lines, st.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return lines
}

// splitNoticeTitle splits a long message at its first clause boundary so the
// heading keeps its meaning instead of degrading to a bare "Error". It returns
// the heading and the remainder, which becomes the first body line.
func splitNoticeTitle(title string, room int) (string, string) {
	if ansi.StringWidth(title) <= room {
		return title, ""
	}
	// The longest prefix that still fits wins; the separator decides whether
	// the remainder keeps its bracket or reads as a continuation.
	seps := []struct{ sep, keep string }{
		{": ", ""},
		{" — ", "— "},
		{" (", "("},
	}
	head, rest := "", ""
	for _, s := range seps {
		i := strings.Index(title, s.sep)
		if i <= 0 || ansi.StringWidth(title[:i]) > room {
			continue
		}
		if i > len(head) {
			head = title[:i]
			rest = s.keep + strings.TrimSpace(title[i+len(s.sep):])
		}
	}
	if head != "" {
		return head, rest
	}
	return ansi.Truncate(title, max(4, room), "…"), ""
}

// noticeTop draws the heading row "╭─ ✓ Copied 61 files ───╮".
func noticeTop(st lipgloss.Style, head string, width int) string {
	inner := width - 2
	text := " " + head + " "
	maxW := inner - 3
	if ansi.StringWidth(text) > maxW {
		text = ansi.Truncate(text, max(0, maxW), "…")
	}
	fill := inner - 1 - ansi.StringWidth(text)
	if fill < 1 {
		fill = 1
	}
	return st.Render("╭─") + st.Bold(true).Render(text) + st.Render(strings.Repeat("─", fill)+"╮")
}

// noticeCountdown draws the drain bar. The filled part starts full and shrinks
// from the right, so the bar visibly runs out just before the pop-up closes.
func noticeCountdown(st lipgloss.Style, frac float64, width int) string {
	filled := int(frac*float64(width) + 0.5)
	filled = max(0, min(width, filled))
	return st.Render(strings.Repeat("█", filled)) +
		lipgloss.NewStyle().Foreground(lipgloss.Color("238")).Render(strings.Repeat("░", width-filled))
}

// noticeRect is where the pop-up is drawn: the bottom-right corner of the
// content area, just above the status line. ok is false when there is no room
// for a legible box.
func (a *App) noticeRect() (x, y, w, h int, ok bool) {
	if a.notice == nil || !a.noticeVisible() {
		return 0, 0, 0, 0, false
	}
	w = min(noticeMaxWidth, a.width-4)
	if w < 20 {
		return 0, 0, 0, 0, false
	}
	if a.width < minWidth || a.height < minHeight {
		return 0, 0, 0, 0, false // the "terminal too small" screen owns the view
	}
	areaH := a.height - 3 // title + status + hint rows belong to the chrome
	if areaH < 7 {
		// Below this the pop-up would cover most of the screen and hide the
		// very thing it reports about; the status line and the task list still
		// carry the result.
		return 0, 0, 0, 0, false
	}
	box := a.noticeBoxWithin(w, areaH-1)
	if len(box) == 0 || len(box)+1 > areaH {
		return 0, 0, 0, 0, false
	}
	return a.width - w - 1, 1 + areaH - len(box), w, len(box), true
}

// noticeVisible reports whether anything is drawn on the screen that a
// notification may appear over. A modal dialog owns the screen and the
// keyboard, so a pop-up waits behind it; the editor owns the terminal, so
// there is nothing to draw at all.
func (a *App) noticeVisible() bool {
	return !a.quitting && !a.editing && a.dialog == nil
}

// withNotice places the notification over the screen it was given.
func (a *App) withNotice(screen string) string {
	x, y, w, h, ok := a.noticeRect()
	if !ok {
		return screen
	}
	box := a.noticeBoxWithin(w, h)
	if len(box) != h {
		return screen
	}
	lines := strings.Split(screen, "\n")
	for i, bl := range box {
		row := y + i
		if row < 0 || row >= len(lines) {
			continue
		}
		lines[row] = ansi.Cut(lines[row], 0, x) + bl + ansi.Cut(lines[row], x+w, a.width)
	}
	return strings.Join(lines, "\n")
}

// noticeAt reports whether (x, y) is inside the drawn notification.
func (a *App) noticeAt(x, y int) bool {
	nx, ny, w, h, ok := a.noticeRect()
	if !ok {
		return false
	}
	return x >= nx && x < nx+w && y >= ny && y < ny+h
}

// noticeEscDismisses reports whether Esc is free to dismiss the notification:
// only on the plain browsing screen, where Esc has no other meaning. Overlays,
// the task list, the live screen and dialogs keep their own Esc behaviour.
func (a *App) noticeEscDismisses() bool {
	return a.dialog == nil && !a.quitting && !a.editing && !a.showHelp && !a.tasks.showing &&
		a.live == nil && a.viewer == nil && a.search == nil && a.advancedSearch == nil &&
		a.bulk == nil && a.folderSize == nil && a.comparison == nil && a.props == nil
}

// tapNotice handles a tap on the notification: it opens the task list when the
// pop-up describes a task, and otherwise just dismisses it.
func (a *App) tapNotice() tea.Cmd {
	n := a.notice
	a.dismissNotice()
	if n == nil || n.task == 0 {
		return nil
	}
	a.openTasks()
	if a.tasks.get(n.task) != nil {
		a.tasks.selected = n.task
	}
	return nil
}
