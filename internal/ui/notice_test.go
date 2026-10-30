package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
)

// noticeText joins what the pop-up currently says (heading, message and body),
// which is what a test wants to assert on: the message, wherever it is wrapped.
func noticeText(h *harness) string {
	n := h.a.notice
	if n == nil {
		return ""
	}
	all := append([]string{n.head, n.msg}, n.body...)
	return strings.Join(all, " | ")
}

// reported is what the app told the user about the last finished operation:
// the notification when one is showing, otherwise the status line. File
// operation results moved from the status line into notifications, so the
// tests that ask "did the app report this?" use this helper; the dedicated
// notification tests below check which surface carries what. The heading is
// left out: it names the operation, while these assertions are about the
// result message.
func (h *harness) reported() string { return reportedText(h.a) }

// reportedText is what the app last told the user, whichever surface carries it:
// the notification when one is up, the status row otherwise. The heading is left
// out, because these assertions are about the message.
func reportedText(a *App) string {
	if n := a.notice; n != nil {
		return strings.Join(append(append([]string{n.msg}, n.body...), a.status), " | ")
	}
	return a.status
}

// reportedLevel is the severity of the last report, in status-line terms.
func (h *harness) reportedLevel() int {
	if n := h.a.notice; n != nil {
		switch n.kind {
		case noticeOK:
			return lvlOK
		case noticeWarn:
			return lvlWarn
		case noticeErr:
			return lvlErr
		}
		return lvlInfo
	}
	return h.a.statusLevel
}

// tick advances the notification countdown by one frame.
func tick(h *harness) {
	h.send(noticeTickMsg{seq: h.a.notice.seq})
}

// runTicks advances the countdown until it ends.
func runTicks(h *harness) {
	h.t.Helper()
	for i := 0; i < 200 && h.a.notice != nil; i++ {
		tick(h)
	}
}

// ---- kinds and styling --------------------------------------------------------

func TestNoticeKindsHaveIconsAndColours(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	th := h.a.theme
	cases := []struct {
		lvl  int
		icon string
		want lipgloss.Style
	}{
		{lvlOK, "✓", th.NoticeOK},
		{lvlWarn, "!", th.NoticeWarn},
		{lvlErr, "✕", th.NoticeErr},
		{lvlInfo, "ℹ", th.NoticeInfo},
	}
	for _, tc := range cases {
		h.a.notice = nil
		h.a.result(tc.lvl, "Note", "message of "+tc.icon)
		if h.a.notice == nil {
			t.Fatalf("%q: no notification", tc.icon)
		}
		if k := resultKind(tc.lvl); noticeIcon(k) != tc.icon {
			t.Errorf("level %d uses icon %q, want %q", tc.lvl, noticeIcon(k), tc.icon)
		}
		if got := noticeStyle(th, resultKind(tc.lvl)); got.GetForeground() != tc.want.GetForeground() {
			t.Errorf("level %d: style foreground = %v, want %v", tc.lvl, got.GetForeground(), tc.want.GetForeground())
		}
		// The icon and the message are both drawn, on the border and inside.
		var lines []string
		x, y, w, _, ok := h.a.noticeRect()
		_ = y
		if !ok {
			t.Fatalf("%q: no room for the notification", tc.icon)
		}
		lines = h.a.noticeBox(w)
		joined := ansi.Strip(strings.Join(lines, "\n"))
		if !strings.Contains(joined, tc.icon) || !strings.Contains(joined, "message of") {
			t.Errorf("level %d: notification lacks its icon or text:\n%s", tc.lvl, joined)
		}
		if !strings.Contains(joined, "╭") || !strings.Contains(joined, "╰") {
			t.Errorf("level %d: notification is not bordered:\n%s", tc.lvl, joined)
		}
		_ = x
	}
}

// ---- results become notifications ----------------------------------------------

func TestCopyResultIsANotification(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.focus("a.txt")
	h.press("space", "space", "c")
	h.waitDialog(dlgConfirm)
	h.press("enter")
	h.idle()
	got := h.a.notice
	if got == nil || got.head != "Copy completed" || !strings.Contains(got.msg, "Copied") {
		t.Fatalf("notification = %+v", got)
	}
	if k := h.a.notice.kind; k != noticeOK {
		t.Errorf("kind = %v, want success", k)
	}
	if txt := noticeText(h); !strings.Contains(txt, env.dst) {
		t.Errorf("the notification should say where the files went: %q", txt)
	}
	v := h.view()
	if !strings.Contains(v, "✓ Copy completed") || !strings.Contains(v, "Copied 2 files") {
		t.Fatalf("the pop-up is not on screen:\n%s", v)
	}
	if st := h.status(); strings.Contains(st, "Copied") {
		t.Errorf("the result should not also fill the status line: %q", st)
	}
}

func TestDeleteResultIsANotification(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.focus("a.txt")
	h.press("d")
	h.waitDialog(dlgDelete)
	h.press("y")
	h.idle()
	if got := h.a.notice; got == nil || !strings.Contains(got.msg, "Moved 1 item to the trash") {
		t.Fatalf("notification = %+v (status %q)", got, h.status())
	}
	if h.a.notice.kind != noticeOK {
		t.Errorf("kind = %v, want success", h.a.notice.kind)
	}
	if !fileExists(filepath.Join(env.trash, "files", "a.txt")) {
		t.Fatal("the delete did not happen")
	}
}

func TestFailedResultIsAnErrorNotification(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	locked := filepath.Join(env.src, "b.txt")
	os.Chmod(locked, 0)
	defer os.Chmod(locked, 0o644)
	h.focus("a.txt")
	h.press("space", "space", "c")
	h.waitDialog(dlgConfirm)
	h.press("enter")
	h.idle()
	n := h.a.notice
	if n == nil || !strings.Contains(n.msg, "Copied 1 file, 1 error") {
		t.Fatalf("notification = %+v", n)
	}
	if n.head != "Copy partially completed" {
		t.Errorf("heading = %q, want a partial copy", n.head)
	}
	if n.kind != noticeErr {
		t.Errorf("kind = %v, want error", n.kind)
	}
	if txt := noticeText(h); !strings.Contains(txt, "task list") {
		t.Errorf("a partial result should point at the task list: %q", txt)
	}
}

func TestStatusLineKeepsProgressAndGuidance(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	// A refusal is guidance for the next key, not a result: status line.
	h.focus("..")
	h.a.ActivePanel().ClearSelection()
	h.press("c")
	if h.a.notice != nil {
		t.Fatalf("a refusal must not become a notification: %q", noticeText(h))
	}
	if st := h.status(); st == "" {
		t.Fatal("a refusal belongs in the status line")
	}
	// So does a mode toggle.
	h.press("h")
	if h.a.notice != nil {
		t.Errorf("a mode toggle must not become a notification: %q", noticeText(h))
	}
	if !strings.Contains(strings.ToLower(h.status()), "hidden") {
		t.Errorf("status = %q", h.status())
	}
}

// ---- countdown -----------------------------------------------------------------

func TestNoticeCountdownDrainsFromTheRight(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.a.result(lvlOK, "Copy completed", "Copied 61 files, 16 folders")
	w := min(noticeMaxWidth, h.a.width-4)
	box := h.a.noticeBox(w)
	if len(box) < 2 {
		t.Fatalf("notification = %v", box)
	}
	// The countdown lives in the bottom border, not on a row of its own.
	bottom := func() string { b := h.a.noticeBox(w); return ansi.Strip(b[len(b)-1]) }
	fresh := bottom()
	if !strings.HasPrefix(fresh, "╰") || !strings.HasSuffix(fresh, "╯") {
		t.Fatalf("the bottom row is not the border: %q", fresh)
	}
	if !strings.Contains(fresh, "━") || strings.Contains(fresh, "─") {
		t.Fatalf("a fresh notification should have a full heavy border: %q", fresh)
	}
	if strings.Contains(ansi.Strip(strings.Join(box, "\n")), "█") {
		t.Fatalf("the old blocky bar is still drawn inside the box:\n%s", ansi.Strip(strings.Join(box, "\n")))
	}
	// Advance half the life: the heavy part shrinks from the right, so it stays
	// anchored on the left and the thin gap grows on the right.
	for i := 0; i < 25; i++ { // half of noticeLife / noticeTick
		tick(h)
	}
	half := bottom()
	firstFilled := func(s string) int { return strings.Index(s, "━") }
	lastFilled := func(s string) int { return strings.LastIndex(s, "━") }
	if firstFilled(half) != firstFilled(fresh) {
		t.Errorf("the border should drain from the right: %q → %q", fresh, half)
	}
	if lastFilled(half) >= lastFilled(fresh) {
		t.Errorf("the countdown did not shrink: %q → %q", fresh, half)
	}
	if !strings.Contains(half, "─") {
		t.Errorf("the drained part should be drawn as a thin border: %q", half)
	}
	// It runs out exactly when the notification goes away.
	runTicks(h)
	if h.a.notice != nil {
		t.Fatalf("the notification should expire after %s", noticeLife)
	}
}

func TestNoticeCountdownIsDrivenByUpdate(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.a.result(lvlOK, "Copy completed", "Copied 1 file")
	if h.a.notice == nil {
		t.Fatal("no notification")
	}
	// Update schedules the next frame and stops when nothing is visible.
	_, cmd := h.a.Update(noticeTickMsg{seq: h.a.notice.seq})
	if cmd == nil && h.a.notice != nil {
		t.Fatal("Update should schedule the next countdown frame")
	}
	runTicks(h)
	if cmd := h.a.syncNotice(nil); cmd != nil {
		t.Error("an expired notification must not keep a timer alive")
	}
}

func TestNoticeCountdownPausesBehindDialogs(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.a.result(lvlOK, "Copy completed", "Copied 61 files, 16 folders")
	left := h.a.notice.left
	h.a.dialog = &dialog{kind: dlgConfirm, title: "Something to decide"}
	// While a dialog owns the screen the pop-up is not drawn and its time
	// does not run out: a result must not expire unseen.
	if _, _, _, _, ok := h.a.noticeRect(); ok {
		t.Fatal("the notification must not be drawn over a dialog")
	}
	for i := 0; i < 60; i++ {
		tick(h)
	}
	if h.a.notice == nil || h.a.notice.left != left {
		t.Fatalf("the countdown ran behind a dialog: %+v", h.a.notice)
	}
	if cmd := h.a.syncNotice(nil); cmd != nil {
		t.Error("no countdown frames while a dialog is open")
	}
	// Closing the dialog brings it back with the rest of its time.
	h.a.dialog = nil
	if !strings.Contains(h.view(), "Copied 61 files") {
		t.Fatalf("the notification should reappear after the dialog:\n%s", h.view())
	}
}

// ---- dismissing ------------------------------------------------------------------

func TestEscDismissesTheNotification(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.a.result(lvlOK, "Copy completed", "Copied 1 file")
	h.press("esc")
	if h.a.notice != nil {
		t.Fatalf("Esc should dismiss the notification:\n%s", h.view())
	}
	// The Esc is consumed by the pop-up, so it cannot also cancel something.
	h.press("esc")
	if h.a.notice != nil || h.a.dialog != nil {
		t.Fatal("a second Esc should be harmless")
	}
}

func TestEscKeepsItsMeaningElsewhere(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	// In the task list Esc closes the list; the pop-up waits for its time.
	s, _ := h.a.newSession("Copying", 0, []string{"/src/a"})
	s.state = taskRunning
	h.a.openTasks()
	h.a.result(lvlOK, "Copy completed", "Copied 1 file") // a pop-up over the list
	// A pinned pop-up mentions Esc only where Esc actually closes it.
	h.a.notice.pinned = true
	if v := ansi.Strip(h.view()); strings.Contains(v, "Esc closes") {
		t.Errorf("the focus hint promises Esc where it would close the task list:\n%s", v)
	}
	h.press("esc")
	if h.a.tasks.showing {
		t.Fatal("Esc should still close the task list")
	}
	if h.a.notice == nil {
		t.Error("Esc in the task list must not dismiss the notification")
	}
	// Back on the panels, the running task still owns Esc: pressing it cancels
	// the task, so the note must not promise it.
	if a := h.a; a.noticeEscDismisses() {
		t.Error("a running task should keep Esc for itself")
	}
	if v := ansi.Strip(h.view()); strings.Contains(v, "Esc closes") {
		t.Errorf("the focus hint promises Esc while a task would be cancelled:\n%s", v)
	}
	// Once nothing is left to cancel, Esc is the note's again and says so.
	s.state = taskCompleted
	if !h.a.noticeEscDismisses() {
		t.Fatal("Esc should be free again")
	}
	if v := ansi.Strip(h.view()); !strings.Contains(v, "Esc closes") {
		t.Errorf("the focus hint should name Esc on the panel screen:\n%s", v)
	}
	h.press("esc")
	if h.a.notice != nil {
		t.Fatal("Esc closes the note when it means nothing else")
	}
}

func TestTappingTheNotificationPinsIt(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.a.result(lvlOK, "Copy completed", "Copied 1 file")
	x, y, _, _, ok := h.a.noticeRect()
	if !ok {
		t.Fatalf("no notification rect:\n%s", h.view())
	}
	h.tap(x+1, y+1)
	n := h.a.notice
	if n == nil || !n.pinned {
		t.Fatalf("a tap should focus the notification, not dismiss it: %+v", n)
	}
	if h.a.tasks.showing {
		t.Fatal("focusing a result must not open the task list")
	}
	// A focused notification is explicit about it and no longer runs on its
	// own: it survives any number of countdown frames.
	if !strings.Contains(ansi.Strip(h.view()), "pinned") {
		t.Fatalf("a focused notification should say so:\n%s", ansi.Strip(h.view()))
	}
	left := n.left
	for i := 0; i < 200; i++ {
		tick(h)
	}
	if h.a.notice != n || n.left != left {
		t.Fatalf("the countdown kept running while focused: %+v", h.a.notice)
	}
	if cmd := h.a.syncNotice(nil); cmd != nil {
		t.Error("a focused notification must not keep a timer alive")
	}
	// Binding does not take the keyboard: browsing keeps working.
	before := h.a.ActivePanel().Cursor()
	h.press("j")
	if h.a.ActivePanel().Cursor() == before {
		t.Error("ordinary keys must keep working while a notification is focused")
	}
	// Esc closes it.
	h.press("esc")
	if h.a.notice != nil {
		t.Fatal("Esc should close a focused notification")
	}
}

func TestTappingOutsideReleasesTheNotification(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.a.result(lvlOK, "Copy completed", "Copied 1 file")
	for i := 0; i < 25; i++ {
		tick(h)
	}
	x, y, w, _, _ := h.a.noticeRect()
	h.tap(x+1, y+1)
	if !h.a.notice.pinned {
		t.Fatal("a tap should focus the notification")
	}
	left := h.a.notice.left
	// A tap outside releases it: the countdown continues where it stopped, it
	// does not start over, and the tap does its normal job.
	h.tap(x-2, y)
	if h.a.notice == nil || h.a.notice.pinned {
		t.Fatalf("a tap outside should release the notification: %+v", h.a.notice)
	}
	if h.a.notice.left != left {
		t.Errorf("countdown = %v, want the paused value %v", h.a.notice.left, left)
	}
	if strings.Contains(ansi.Strip(h.view()), "pinned") {
		t.Error("a released notification should drop the focus hint")
	}
	if h.a.noticeAt(x-2, y) {
		t.Error("the tap outside should not have landed on the pop-up")
	}
	_ = w
	h.press("j") // still alive after the tap and a normal key
	if h.a.notice == nil {
		t.Fatal("releasing must not dismiss the notification")
	}
	runTicks(h)
	if h.a.notice != nil {
		t.Fatal("a released notification should expire as usual")
	}
}

func TestFocusedTaskNotificationOpensTheTask(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	s1, _ := h.a.newSession("Copying", 0, []string{"/src/a"})
	s1.state = taskRunning
	s2, _ := h.a.newSession("Moving", 0, []string{"/src/b"})
	s2.state = taskRunning
	h.a.notifyBackground(s1)
	x, y, _, _, _ := h.a.noticeRect()
	// First tap focuses, the second opens the list on that task.
	h.tap(x+1, y+1)
	if h.a.tasks.showing || h.a.notice == nil || !h.a.notice.pinned {
		t.Fatalf("first tap: showing=%v notice=%+v", h.a.tasks.showing, h.a.notice)
	}
	h.tap(x+1, y+1)
	if !h.a.tasks.showing || h.a.tasks.selected != s1.id {
		t.Fatalf("tapping the focused task notification should open the list on task %d (showing=%v selected=%d)",
			s1.id, h.a.tasks.showing, h.a.tasks.selected)
	}
	if h.a.notice != nil {
		t.Error("the notification should be gone after opening its task")
	}
}

// ---- spam control ----------------------------------------------------------------

func TestRepeatedResultsDoNotStackUp(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.a.result(lvlOK, "Copy completed", "Copied 1 file")
	first := h.a.notice
	for i := 0; i < 5; i++ {
		for j := 0; j < 10; j++ {
			tick(h)
		}
		h.a.result(lvlOK, "Copy completed", "Copied 1 file")
	}
	if h.a.notice != first {
		t.Fatal("the same result must restart the countdown, not replace the pop-up")
	}
	if h.a.notice.left != noticeLife {
		t.Errorf("countdown = %v, want a restart to %v", h.a.notice.left, noticeLife)
	}
	// There is only ever one pop-up: a newer result replaces the older one.
	h.a.result(lvlWarn, "Move cancelled", "Move cancelled — nothing was transferred")
	if h.a.notice == first || !strings.Contains(h.a.notice.msg, "Move cancelled") {
		t.Fatalf("a new result should replace the old pop-up: %+v", h.a.notice)
	}
	if strings.Count(ansi.Strip(h.view()), "╭─") < 1 {
		t.Fatal("the pop-up is not drawn")
	}
}

func TestNoNotificationWhileQuitting(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.a.quitting = true
	h.a.result(lvlOK, "Copy completed", "Copied 1 file")
	if h.a.notice != nil {
		t.Fatal("nothing should be announced while quitting")
	}
}

// ---- long messages and the heading ------------------------------------------------

func TestHeadingNamesTheOperationAndOutcome(t *testing.T) {
	cases := []struct {
		op                        string
		failed, canceled, partial bool
		want                      string
	}{
		{"Copy", false, false, false, "Copy completed"},
		{"Copy", false, false, true, "Copy partially completed"},
		{"Copy", true, false, true, "Copy partially completed"},
		{"Copy", true, false, false, "Copy failed"},
		{"Delete", false, true, false, "Delete cancelled"},
		{"Extract", false, true, true, "Extract cancelled"},
	}
	for _, tc := range cases {
		if got := operationHeading(tc.op, tc.failed, tc.canceled, tc.partial); got != tc.want {
			t.Errorf("operationHeading(%q, %v, %v, %v) = %q, want %q", tc.op, tc.failed, tc.canceled, tc.partial, got, tc.want)
		}
	}
}

func TestTheResultMessageSitsInsideTheBox(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.send(tea.WindowSizeMsg{Width: 46, Height: 16})
	h.a.result(lvlOK, "Copy completed", "Copied 61 files, 16 folders", "→ /mnt/backup/photos")
	box := ansi.Strip(strings.Join(h.a.noticeBox(min(noticeMaxWidth, h.a.width-4)), "\n"))
	lines := strings.Split(box, "\n")
	// The heading names the operation; the message is the first line inside.
	if !strings.Contains(lines[0], "Copy completed") {
		t.Errorf("the heading is not in the border: %q", lines[0])
	}
	if strings.Contains(lines[0], "Copied 61 files") {
		t.Errorf("the result message should not be in the heading: %q", lines[0])
	}
	if !strings.Contains(lines[1], "Copied 61 files, 16 folders") {
		t.Errorf("the message should be the first body line: %q", lines[1])
	}
	if !strings.Contains(lines[2], "/mnt/backup/photos") {
		t.Errorf("the detail should follow the message: %q", lines[2])
	}
}

func TestLongMessageKeepsItsReasonOnScreen(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.send(tea.WindowSizeMsg{Width: 46, Height: 16})
	h.a.result(lvlErr, "Delete partially completed", "Delete incomplete: 20629 files removed, 2149 errors")
	v := ansi.Strip(h.view())
	for _, want := range []string{"Delete partially completed", "Delete incomplete", "20629 files", "2149 errors"} {
		if !strings.Contains(v, want) {
			t.Errorf("the pop-up lost %q:\n%s", want, v)
		}
	}
}

// ---- rendering on small terminals --------------------------------------------------

func TestNotificationFitsEverySize(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	long := "Deleted 1 item (20629 files, 2149 folders)"
	for _, size := range []struct{ w, h int }{
		{30, 7}, {30, 9}, {34, 8}, {40, 12}, {46, 14}, {60, 20}, {100, 30}, {160, 50},
	} {
		h.send(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		h.a.result(lvlErr, "Delete partially completed", long, "some/really/long/path/that/does/not/fit/anywhere/at/all.txt", "second line")
		v := ansi.Strip(h.a.View())
		lines := strings.Split(v, "\n")
		if len(lines) > size.h {
			t.Errorf("%dx%d: view has %d lines:\n%s", size.w, size.h, len(lines), v)
		}
		for i, line := range lines {
			if w := lipgloss.Width(line); w > size.w {
				t.Errorf("%dx%d: line %d is %d cells wide: %q", size.w, size.h, i, w, line)
			}
		}
		// Where there is room, the text is there in some form.
		if size.w >= 30 && size.h >= 10 && !strings.Contains(v, "Deleted") && !strings.Contains(v, "Error") {
			t.Errorf("%dx%d: the notification lost its message:\n%s", size.w, size.h, v)
		}
	}
}

func TestNotificationIsSkippedWhenThereIsNoRoom(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	for _, size := range []struct{ w, h int }{{30, 7}, {40, 9}, {24, 12}} {
		h.send(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		h.a.result(lvlOK, "Copy completed", "Copied 61 files, 16 folders")
		if _, _, _, _, ok := h.a.noticeRect(); ok && (size.h < 10 || size.w < 26) {
			t.Errorf("%dx%d: a pop-up would cover the whole screen", size.w, size.h)
		}
		if h.a.notice == nil {
			t.Errorf("%dx%d: the result should still be pending", size.w, size.h)
		}
		// Growing the terminal brings it back, countdown intact.
		h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
		if !strings.Contains(ansi.Strip(h.view()), "Copied 61 files") {
			t.Errorf("%dx%d: the pending result did not appear after growing", size.w, size.h)
		}
		h.a.notice = nil
	}
}

func TestNotificationDoesNotCoverTheChrome(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.a.result(lvlOK, "Copy completed", "Copied 61 files, 16 folders")
	_, y, _, height, ok := h.a.noticeRect()
	if !ok {
		t.Fatal("no notification")
	}
	// Title row is 0, the hint bar is the last row and the status line the
	// one above it: the pop-up stays inside the content area.
	if y < 1 || y+height > h.a.height-2 {
		t.Fatalf("the notification covers the chrome: y=%d h=%d height=%d", y, height, h.a.height)
	}
}

// ---- background tasks stay connected ------------------------------------------------

func TestBackgroundTaskNotificationShowsProgress(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	s, _ := h.a.newSession("Copying", 0, []string{"/src/big.iso"})
	s.state = taskRunning
	s.prog = ops.Progress{Phase: "copying", DoneItems: 5, TotalItems: 40, DoneBytes: 50 << 20, TotalBytes: 400 << 20}
	h.a.notifyBackground(s)
	txt := noticeText(h)
	for _, want := range []string{"in the background", "13%", "t opens the task list"} {
		if !strings.Contains(txt, want) {
			t.Errorf("notification %q lacks %q", txt, want)
		}
	}
	if h.a.notice.task != s.id {
		t.Errorf("task = %d, want %d", h.a.notice.task, s.id)
	}
	if h.a.notice.kind != noticeInfo {
		t.Errorf("kind = %v, want info", h.a.notice.kind)
	}
}

func TestWatchedTaskResultDoesNotRepeatItself(t *testing.T) {
	h, s, _ := liveHarness(t)
	a := h.a
	a.finishTask(s, taskCompleted, "Copied 1 file")
	a.taskResult(s, lvlOK, "Copy completed", s.summary)
	if a.notice != nil {
		t.Fatal("the live screen already shows the result; no pop-up on top of it")
	}
}
