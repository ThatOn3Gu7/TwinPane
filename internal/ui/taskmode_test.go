package ui

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
	"github.com/ThatOn3Gu7/TwinPane/internal/panel"
)

// ---- the "large enough to ask" rule ------------------------------------------

func TestLargeOpThresholds(t *testing.T) {
	cases := []struct {
		name string
		est  ops.Estimate
		want bool
	}{
		{"empty", ops.Estimate{}, false},
		{"one file", ops.Estimate{Files: 1, Bytes: 10}, false},
		{"just under the item count", ops.Estimate{Files: 499, Bytes: 1 << 20}, false},
		{"the item count", ops.Estimate{Files: 500}, true},
		{"folders count as items", ops.Estimate{Dirs: 500}, true},
		{"just under the byte size", ops.Estimate{Files: 2, Bytes: largeOpBytes - 1}, false},
		{"the byte size", ops.Estimate{Files: 2, Bytes: largeOpBytes}, true},
		{"a capped walk", ops.Estimate{Files: ops.EstimateEntryCap, Truncated: true}, true},
	}
	for _, tc := range cases {
		if got := largeOp(tc.est); got != tc.want {
			t.Errorf("%s: largeOp(%+v) = %v, want %v", tc.name, tc.est, got, tc.want)
		}
	}
}

func TestEstimateTextMentionsTheReason(t *testing.T) {
	got := estimateText(ops.Estimate{Files: 3, Dirs: 1, Bytes: 3 << 20})
	for _, want := range []string{"3 files", "1 folder", "3.0 M"} {
		if !strings.Contains(got, want) {
			t.Errorf("estimateText = %q, want it to contain %q", got, want)
		}
	}
	if got := estimateText(ops.Estimate{}); !strings.Contains(got, "may take a while") {
		t.Errorf("empty estimate text = %q", got)
	}
	if got := estimateText(ops.Estimate{Files: 4000, Truncated: true}); !strings.Contains(got, "larger") {
		t.Errorf("a truncated estimate should say the totals are minimums: %q", got)
	}
}

// ---- the choice itself -------------------------------------------------------

// modeChoice opens the choice for a large operation and returns the session
// plus a counter of how often the operation was started.
func modeChoice(t *testing.T, h *harness, est ops.Estimate) (*opSession, *int) {
	t.Helper()
	s, _ := h.a.newSession("Copying", 0, []string{"/src/a.iso"})
	s.kind, s.to = ops.Copy, "/dst"
	s.state = taskWaiting
	started := 0
	h.a.confirmTaskMode(s, est, func() tea.Cmd {
		started++
		return nil
	})
	return s, &started
}

func TestSmallOperationRunsWithoutAsking(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	s, started := modeChoice(t, h, ops.Estimate{Files: 2, Bytes: 4096})
	if h.a.dialog != nil {
		t.Fatalf("a small operation must not open a dialog:\n%s", h.view())
	}
	if *started != 1 {
		t.Fatalf("the operation should start at once, started=%d", *started)
	}
	if s.mode != modeBackground {
		t.Fatalf("mode = %v, want background", s.mode)
	}
	if h.a.notice != nil {
		t.Fatal("a small operation must not show a notification")
	}
}

func TestLargeOperationAsksAndCanStayInTheForeground(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	s, started := modeChoice(t, h, ops.Estimate{Files: 900, Bytes: 3 << 30})
	if h.a.dialog == nil {
		t.Fatalf("a large operation should ask how to run:\n%s", h.view())
	}
	v := h.view()
	for _, want := range []string{"large", "watch", "background", "900 files"} {
		if !strings.Contains(v, want) {
			t.Errorf("the choice dialog lacks %q:\n%s", want, v)
		}
	}
	if *started != 0 {
		t.Fatal("the operation must wait for the answer")
	}
	h.press("enter") // the highlighted button: stay and watch
	if *started != 1 || s.mode != modeLive {
		t.Fatalf("started=%d mode=%v, want the live screen", *started, s.mode)
	}
	if h.a.notice != nil {
		t.Fatal("watching needs no notification")
	}
}

func TestLargeOperationCanBeSentToTheBackground(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	s, started := modeChoice(t, h, ops.Estimate{Files: 900})
	h.press("b")
	if *started != 1 || s.mode != modeBackground {
		t.Fatalf("started=%d mode=%v, want background", *started, s.mode)
	}
	if h.a.notice == nil {
		t.Fatal("backgrounding a task should raise the notification")
	}
	v := h.view()
	for _, want := range []string{"in the background", "t"} {
		if !strings.Contains(v, want) {
			t.Errorf("notification lacks %q:\n%s", want, v)
		}
	}
	// The notification never takes the keyboard: browsing keeps working.
	before := h.a.ActivePanel().Cursor()
	h.press("j", "j")
	if h.a.ActivePanel().Cursor() == before {
		t.Errorf("the cursor should still move while the notification is shown:\n%s", h.view())
	}
	if h.a.notice != nil {
		t.Error("a keypress should dismiss the notification")
	}
}

func TestModeChoiceEscWatchesInsteadOfCancelling(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	s, started := modeChoice(t, h, ops.Estimate{Files: 900})
	h.press("esc")
	if *started != 1 || s.mode != modeLive || h.a.dialog != nil {
		t.Fatalf("Esc should pick the watching default: started=%d mode=%v", *started, s.mode)
	}
}

// TestLargeCopyAsksEndToEnd walks the real panel path: a folder with enough
// entries to count as large is copied, and the confirmation is followed by the
// background / watch question instead of the copy starting at once.
func TestLargeCopyAsksEndToEnd(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	big := filepath.Join(env.src, "many")
	if err := os.MkdirAll(big, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < largeOpItems+20; i++ {
		if err := os.WriteFile(filepath.Join(big, "f"+strconv.Itoa(i)+".txt"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h.exec(h.a.Panel(0).Load(env.src, "", panel.LoadForce))
	h.until("panel reload", func() bool { return !h.a.Panel(0).Loading() })
	h.focus("many")
	h.press("c")
	h.waitDialog(dlgConfirm)
	h.press("enter")
	h.until("the background / watch question", func() bool {
		return h.a.dialog != nil && strings.Contains(h.a.dialog.title, "large")
	})
	if _, err := os.Stat(filepath.Join(env.dst, "many")); err == nil {
		t.Fatal("the copy must not start before the question is answered")
	}
	h.press("b")
	if h.a.notice == nil {
		t.Fatal("answering background should show the notification")
	}
	h.idle()
	entries, err := os.ReadDir(filepath.Join(env.dst, "many"))
	if err != nil {
		t.Fatalf("the copy did not reach the destination: %v", err)
	}
	if len(entries) != largeOpItems+20 {
		t.Fatalf("copied %d entries, want %d", len(entries), largeOpItems+20)
	}
	if st := h.status(); !strings.Contains(st, "Copied") {
		t.Fatalf("status after the copy = %q", st)
	}
}

// ---- the notification --------------------------------------------------------

func TestNotificationIsSmallAndNonBlocking(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	s, _ := h.a.newSession("Copying", 0, []string{"/src/big.iso"})
	s.state = taskRunning
	s.prog = ops.Progress{Phase: "copying", DoneItems: 5, TotalItems: 40, DoneBytes: 50 << 20, TotalBytes: 400 << 20}
	h.a.notifyBackground(s)
	v := h.view()
	if !strings.Contains(v, "Copying in the background") {
		t.Fatalf("no notification:\n%s", v)
	}
	if !strings.Contains(v, "12%") || !strings.Contains(v, "t to see progress") {
		t.Errorf("the notification should show progress and how to see it:\n%s", v)
	}
	// Non-blocking: it is not a dialog, it does not own the keyboard and the
	// panels behind it still respond.
	if h.a.dialog != nil {
		t.Fatal("the notification must not be a dialog")
	}
	if !h.a.browseVisible() {
		t.Fatal("the notification must leave the browser on screen")
	}
	h.press("tab")
	if h.a.Active() != 1 {
		t.Error("Tab should still switch panels while the notification is shown")
	}
}

func TestNotificationFitsNarrowTerminals(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	s, _ := h.a.newSession("Copying", 0, []string{"/src/big.iso"})
	s.state = taskRunning
	for _, size := range []struct{ w, h int }{{30, 10}, {34, 8}, {40, 12}, {60, 20}, {120, 40}} {
		h.send(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		h.a.notifyBackground(s)
		v := ansi.Strip(h.a.View())
		lines := strings.Split(v, "\n")
		if len(lines) > size.h {
			t.Errorf("%dx%d: view has %d lines", size.w, size.h, len(lines))
		}
		for i, line := range lines {
			if w := lipgloss.Width(line); w > size.w {
				t.Errorf("%dx%d: line %d is %d cells wide: %q", size.w, size.h, i, w, line)
			}
		}
		if size.w >= 30 && size.h >= 10 && !strings.Contains(v, "t ") && !strings.Contains(v, "progress") {
			t.Errorf("%dx%d: notification lost its hint:\n%s", size.w, size.h, v)
		}
	}
}

func TestNotificationExpiresOnItsTimer(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	s, _ := h.a.newSession("Copying", 0, []string{"/src/a"})
	s.state = taskRunning
	cmd := h.a.notifyBackground(s)
	seq := h.a.notice.seq
	if cmd == nil {
		t.Fatal("showing a notification should schedule its dismissal")
	}
	// A stale timer must not remove a newer notification.
	h.a.notifyBackground(s)
	h.send(noticeTimeoutMsg{seq: seq})
	if h.a.notice == nil {
		t.Fatal("an old timer removed the current notification")
	}
	h.send(noticeTimeoutMsg{seq: h.a.notice.seq})
	if h.a.notice != nil {
		t.Fatal("the notification should expire on its own")
	}
}

// ---- the live progress screen ------------------------------------------------

// liveHarness returns a harness with one running task on the live screen.
func liveHarness(t *testing.T) (*harness, *opSession, *bool) {
	t.Helper()
	h, _ := newOpsApp(t, nil, nil)
	cancelled := false
	s, _ := h.a.newSession("Copying", 0, []string{"/src/big.iso"})
	s.kind, s.to = ops.Copy, "/dst"
	s.state = taskRunning
	s.cancel = func() { cancelled = true }
	s.prog = ops.Progress{Phase: "copying", Current: "/src/big.iso", DoneItems: 5, TotalItems: 10,
		DoneBytes: 50 << 20, TotalBytes: 100 << 20}
	h.a.watchTask(s)
	return h, s, &cancelled
}

func TestLiveScreenShowsProgressAndLeavesTheTaskRunning(t *testing.T) {
	h, s, cancelled := liveHarness(t)
	v := h.view()
	for _, want := range []string{"Copying", "RUNNING", "50%", "5 of 10 items", "/src/big.iso", "/dst", "Esc"} {
		if !strings.Contains(v, want) {
			t.Errorf("live screen lacks %q:\n%s", want, v)
		}
	}
	if !strings.Contains(h.footer(), "cancel") {
		t.Errorf("live hints should offer cancel: %q", h.footer())
	}
	// Leaving the screen backgrounds the task and says so.
	h.press("esc")
	if h.a.live != nil {
		t.Fatal("Esc should close the live screen")
	}
	if *cancelled || s.state != taskRunning {
		t.Fatal("leaving the live screen must not stop the task")
	}
	if h.a.notice == nil || !strings.Contains(h.view(), "in the background") {
		t.Fatalf("leaving should raise the notification:\n%s", h.view())
	}
	// The task is still in the task list, where it can be watched again.
	h.press("t")
	if !strings.Contains(h.view(), "RUNNING") {
		t.Fatalf("the task should still be listed:\n%s", h.view())
	}
	h.press("enter")
	if h.a.live == nil || h.a.tasks.showing {
		t.Fatal("Enter should reopen the live screen from the task list")
	}
}

func TestLiveScreenCancel(t *testing.T) {
	h, s, cancelled := liveHarness(t)
	h.press("c")
	if !*cancelled || s.state != taskCanceling {
		t.Fatalf("c should cancel the watched task: cancelled=%v state=%v", *cancelled, s.state)
	}
	if h.a.live == nil {
		t.Fatal("cancelling should keep the user on the progress screen")
	}
}

func TestLiveScreenShowsFinishedResult(t *testing.T) {
	h, s, _ := liveHarness(t)
	h.a.finishTask(s, taskCompleted, "Copied 1 file")
	v := h.view()
	for _, want := range []string{"DONE", "Copied 1 file", "100%"} {
		if !strings.Contains(v, want) {
			t.Errorf("finished live screen lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "keeps it running") {
		t.Error("a finished task should not offer to keep it running")
	}
	h.press("enter")
	if h.a.live != nil || h.a.notice != nil {
		t.Fatal("closing a finished task needs no notification")
	}
}

func TestLiveScreenHandlesNarrowTerminals(t *testing.T) {
	h, _, _ := liveHarness(t)
	for _, size := range []struct{ w, h int }{{30, 8}, {34, 10}, {46, 12}, {80, 24}} {
		h.send(tea.WindowSizeMsg{Width: size.w, Height: size.h})
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
	}
}

func TestWatchingAFinishedTaskIsRefused(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	s, _ := h.a.newSession("Copying", 0, []string{"/src/a"})
	h.a.finishTask(s, taskCompleted, "Copied 1 file")
	h.a.openTasks()
	h.press("enter")
	if h.a.live != nil {
		t.Fatal("a finished task has no progress to watch")
	}
	if !strings.Contains(h.status(), "finished") {
		t.Errorf("status = %q", h.status())
	}
}

func TestLiveScreenSurvivesDialogsAndTaskList(t *testing.T) {
	h, s, _ := liveHarness(t)
	// A conflict dialog drawn over the live screen keeps the screen behind it.
	h.a.dialog = &dialog{kind: dlgConflict, title: "A file named \"x\" already exists"}
	if !strings.Contains(h.view(), "already exists") {
		t.Fatalf("the dialog should be drawn over the live screen:\n%s", h.view())
	}
	h.a.dialog = nil
	// t swaps to the list and back without disturbing the operation.
	h.press("t")
	if h.a.live != nil || !h.a.tasks.showing {
		t.Fatal("t should show the task list")
	}
	if s.state != taskRunning {
		t.Fatal("switching views must not change the task")
	}
}

// ---- mouse and touch ---------------------------------------------------------

func TestTappingTheNotificationOpensTheTaskList(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	s, _ := h.a.newSession("Copying", 0, []string{"/src/big.iso"})
	s.state = taskRunning
	s.prog = ops.Progress{TotalBytes: 10, DoneBytes: 5}
	h.a.notifyBackground(s)
	x, y := -1, -1
	for yy := 0; yy < h.a.height && x < 0; yy++ {
		for xx := 0; xx < h.a.width; xx++ {
			if h.a.noticeAt(xx, yy) {
				x, y = xx, yy
				break
			}
		}
	}
	if x < 0 {
		t.Fatalf("the notification is not on screen:\n%s", h.view())
	}
	h.tap(x, y)
	if !h.a.tasks.showing || h.a.notice != nil {
		t.Fatal("tapping the notification should open the task list")
	}
}

func TestLiveScreenWheelScrollsTheDetails(t *testing.T) {
	h, _, _ := liveHarness(t)
	h.wheel(h.a.width/2, 4, 3)
	if h.a.live == nil || h.a.live.scroll == 0 {
		t.Fatalf("the wheel should scroll the live screen (scroll=%d)", h.a.live.scroll)
	}
}
