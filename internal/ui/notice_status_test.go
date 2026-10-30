package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Messages that are results, warnings or refusals are written for the status
// line, which is a single row and cuts whatever does not fit. These tests cover
// what happens to the tail: it is repeated in a notification, and a tap on that
// notification unfolds the whole text.

// TestLongStatusMessagePopsUp: a message the row cannot show becomes a
// notification as well; one that fits stays where it is.
func TestLongStatusMessagePopsUp(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.send(tea.WindowSizeMsg{Width: 40, Height: 20})

	h.a.setStatusLevel("short note", lvlOK)
	if h.a.notice != nil {
		t.Fatalf("a message that fits the row must not pop up: %q", noticeText(h))
	}

	long := "cannot rename here: the file is a symbolic link pointing outside this folder — use P to look at it, or C to copy it to the other panel first"
	h.a.setStatusLevel(long, lvlErr)
	n := h.a.notice
	if n == nil {
		t.Fatalf("a message the row cuts must pop up:\n%s", h.view())
	}
	if n.msg != long {
		t.Fatalf("the pop-up lost the message: %q", n.msg)
	}
	if n.kind != noticeErr {
		t.Fatalf("kind = %v, want the error kind", n.kind)
	}
	// The row does not repeat it: one message, one place.
	if h.a.status != "" {
		t.Fatalf("the row repeats the message: %q", h.a.status)
	}
	if !strings.Contains(ansi.Strip(h.view()), "cannot rename here") {
		t.Fatalf("the message is on neither surface:\n%s", h.view())
	}
}

// TestQuietStatusMessagesStillFit: progress text is short by construction, so
// the common case must not raise pop-ups on every frame.
func TestQuietStatusMessagesStillFit(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	for _, msg := range []string{
		"Checking copy…",
		"nothing to copy: put the cursor on an item",
		"sort by name (a-z, directories first)",
		"key bindings reloaded: every default in place",
	} {
		h.a.notice = nil
		h.a.setStatusLevel(msg, lvlInfo)
		if h.a.notice != nil {
			t.Errorf("%q fits a 100-wide row and must stay there", msg)
		}
	}
}

// TestTappingALongMessageUnfoldsIt is the point of the tap on a notification
// that was cut: the whole message is drawn, not the first lines and "…".
func TestTappingALongMessageUnfoldsIt(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.send(tea.WindowSizeMsg{Width: 70, Height: 24})
	letters := "abcdefghijkl"
	var parts []string
	for _, c := range letters {
		parts = append(parts, strings.Repeat("word ", 3)+"line "+string(c))
	}
	long := strings.Join(parts, " ")

	h.a.result(lvlErr, "Key bindings not reloaded", long)
	if h.a.notice == nil {
		t.Fatal("no notification")
	}
	compact := ansi.Strip(h.view())
	if strings.Contains(compact, "line l") {
		t.Fatalf("the small pop-up was supposed to be cut:\n%s", compact)
	}
	if !strings.Contains(compact, "line a") {
		t.Fatalf("the small pop-up lost its first line:\n%s", compact)
	}
	x, y, w, hgt, ok := h.a.noticeRect()
	if !ok {
		t.Fatalf("no pop-up rect:\n%s", compact)
	}
	h.tap(x+1, y+1)
	if !h.a.notice.pinned {
		t.Fatal("the tap did not focus the notification")
	}
	// It is wider than the corner note, and every word is on the screen.
	_, _, w2, h2, ok := h.a.noticeRect()
	if !ok {
		t.Fatal("the focused pop-up disappeared")
	}
	if w2 <= w {
		t.Errorf("the focused pop-up did not widen: %d → %d", w, w2)
	}
	if h2 < hgt {
		t.Errorf("the focused pop-up shrank: %d → %d", hgt, h2)
	}
	flat := strings.Join(strings.Fields(ansi.Strip(h.view())), " ")
	for _, c := range letters {
		want := "line " + string(c)
		if !strings.Contains(flat, want) {
			t.Errorf("the unfolded message lost %q:\n%s", want, ansi.Strip(h.view()))
		}
	}
	checkScreen(t, h.a.View(), 70, 24)
}

// TestUnfoldedMessageFitsEverySize: the wide box is still a window, not a
// banner, and on a terminal too small to hold the text it is cut instead of
// overflowing the screen.
func TestUnfoldedMessageFitsEverySize(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	long := strings.Repeat("a sentence that keeps going and going. ", 12)
	for _, size := range []struct{ w, h int }{
		{30, 8}, {40, 12}, {60, 16}, {80, 24}, {120, 40},
	} {
		h.send(tea.WindowSizeMsg{Width: size.w, Height: size.h})
		h.a.result(lvlErr, "Key bindings not reloaded", long)
		if h.a.notice == nil {
			t.Fatalf("%dx%d: no notification", size.w, size.h)
		}
		h.a.notice.pinned = true
		v := h.a.View()
		lines := strings.Split(v, "\n")
		if len(lines) != size.h {
			t.Fatalf("%dx%d: view has %d lines", size.w, size.h, len(lines))
		}
		for i, line := range lines {
			if w := ansi.StringWidth(line); w > size.w {
				t.Fatalf("%dx%d: line %d is %d cells: %q", size.w, size.h, i, w, ansi.Strip(line))
			}
		}
		h.a.notice = nil
	}
}

// TestShortFocusDoesNotUnfold: tapping a result that already fits stops the
// countdown, it does not send a corner note across the screen.
func TestShortFocusDoesNotUnfold(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.a.result(lvlOK, "Copy completed", "Copied 1 file")
	_, _, w, _, ok := h.a.noticeRect()
	if !ok {
		t.Fatal("no notification")
	}
	x, y, _, _, _ := h.a.noticeRect()
	h.tap(x+1, y+1)
	_, _, w2, _, ok := h.a.noticeRect()
	if !ok {
		t.Fatal("the focused pop-up disappeared")
	}
	if w2 != w {
		t.Fatalf("a message that fits must keep its width: %d → %d", w, w2)
	}
}

// TestKeybindingReloadIsANotification: the Settings row's answer arrives in the
// pop-up with a heading, so it is readable while the window is still open.
func TestKeybindingReloadIsANotification(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keybinding.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"bindings":{"browse.copy":["y"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newBindingsApp(t, path)
	h.a.reloadBindings()
	if n := h.a.notice; n == nil || n.head != "Key bindings reloaded" || !strings.Contains(n.msg, "1") {
		t.Fatalf("reload did not report itself: %+v", n)
	}
	// Only the pop-up says it: the row is not a second copy of the result.
	if h.status() != "" {
		t.Fatalf("the status row repeats the notification: %q", h.status())
	}
	if !strings.Contains(ansi.Strip(h.view()), "1 of the defaults changed") {
		t.Fatalf("the reload result is not on screen:\n%s", h.view())
	}

	// A file with an action TwinPane does not know is reported, with the name
	// the user has to fix.
	if err := os.WriteFile(path, []byte(`{"version":1,"bindings":{"browse.copyy":["y"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h.a.reloadBindings()
	n := h.a.notice
	if n == nil || n.kind != noticeWarn {
		t.Fatalf("a warning is not a warning notification: %+v", n)
	}
	if !strings.Contains(strings.Join(append([]string{n.msg}, n.body...), " "), "browse.copyy") {
		t.Fatalf("the pop-up does not name the bad action: %+v", n)
	}
}

// TestDamagedKeybindingFilePopsUpAtStartup: the explanation of what could not be
// loaded is one tap away even on the first screen, where the status row would
// have cut it.
func TestDamagedKeybindingFilePopsUpAtStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keybinding.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newBindingsApp(t, path)
	n := h.a.notice
	if n == nil {
		t.Fatalf("the damaged file was not reported in the pop-up:\n%s", h.view())
	}
	if n.head != "Startup" || n.kind != noticeWarn {
		t.Fatalf("startup pop-up = %+v", n)
	}
	text := strings.Join(append([]string{n.msg}, n.body...), " ")
	if !strings.Contains(text, "key bindings") {
		t.Fatalf("the pop-up does not say what failed: %+v", n)
	}
	if h.status() != "" {
		t.Fatalf("the status row repeats the notification: %q", h.status())
	}
}

// TestNoticeIsDrawnOverSettings: the key bindings row reloads the file from
// inside the Settings window, so the answer has to be visible there and the tap
// has to reach it.
func TestNoticeIsDrawnOverSettings(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.a.showSettings = true
	h.a.result(lvlOK, "Key bindings reloaded", "Every default in place")
	if !strings.Contains(ansi.Strip(h.view()), "Every default in place") {
		t.Fatalf("the pop-up is hidden behind the Settings window:\n%s", ansi.Strip(h.view()))
	}
	x, y, _, _, ok := h.a.noticeRect()
	if !ok {
		t.Fatal("no pop-up rect over the Settings window")
	}
	h.tap(x+1, y+1)
	if h.a.notice == nil || !h.a.notice.pinned {
		t.Fatal("the tap did not reach the pop-up")
	}
}

// TestNoticeStillWaitsBehindDialogs: a modal dialog owns the keyboard and the
// screen, so the corner note must not be drawn over it (or count down unseen).
func TestNoticeStillWaitsBehindDialogs(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.a.result(lvlOK, "Copy completed", "Copied 1 file")
	h.a.dialog = &dialog{kind: dlgConfirm}
	// The dialog is drawn over the panes; the pop-up is not on top of it.
	if _, _, _, _, ok := h.a.noticeRect(); !ok && h.a.notice == nil {
		t.Fatal("the notice should still be pending")
	}
	if !h.a.noticePaused() {
		t.Fatal("the countdown must wait behind a dialog")
	}
	if strings.Contains(ansi.Strip(h.view()), "Copied 1 file") {
		t.Fatalf("the pop-up covered a dialog:\n%s", ansi.Strip(h.view()))
	}
}

// TestResultPopsUpOnAWideTerminal is the "it depends on how far I zoomed"
// half of the bug: a result used to reach the notification only when it was
// wider than the status row, so on a wide terminal the very same message
// appeared as a bare line at the bottom and never as a box.
func TestResultPopsUpOnAWideTerminal(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.send(tea.WindowSizeMsg{Width: 200, Height: 40})
	h.a.openDone(openDoneMsg{name: "photo.png", how: "android"})
	if _, _, _, _, ok := h.a.noticeRect(); !ok {
		t.Fatalf("no pop-up on a 200-column terminal:\n%s", h.view())
	}
	if !strings.Contains(ansi.Strip(h.view()), "Handed over") {
		t.Fatalf("the pop-up is not drawn:\n%s", h.view())
	}
	if h.a.status != "" {
		t.Fatalf("the row repeats the result: %q", h.a.status)
	}
}

// TestResultIsReportedExactlyOnce: one outcome, one message. Counting the
// message on the screen is the direct way to say "no duplicate" — the pop-up
// wraps, so the token is a single word that cannot be split.
func TestResultIsReportedExactlyOnce(t *testing.T) {
	cases := []struct {
		name  string
		w, h  int
		run   func(h *harness)
		token string
	}{
		{"handed to Android", 60, 20, func(h *harness) {
			h.a.openDone(openDoneMsg{name: "photo.png", how: "android"})
		}, "termux-open"},
		{"handed to Android, narrow", 34, 12, func(h *harness) {
			h.a.openDone(openDoneMsg{name: "photo.png", how: "android"})
		}, "termux-open"},
		{"handed to Android, wide", 200, 40, func(h *harness) {
			h.a.openDone(openDoneMsg{name: "photo.png", how: "android"})
		}, "termux-open"},
		{"bookmark added", 60, 20, func(h *harness) { h.press("M", "B") }, "bookmarked"},
		{"new file", 60, 20, func(h *harness) {
			h.press("n", "f")
			h.typeText("notes", false)
			h.press("enter")
		}, "Created"},
	}
	for _, c := range cases {
		h, _ := newOpsApp(t, nil, nil)
		h.send(tea.WindowSizeMsg{Width: c.w, Height: c.h})
		c.run(h)
		v := ansi.Strip(h.view())
		if got := strings.Count(v, c.token); got != 1 {
			t.Errorf("%s at %dx%d: %q appears %d times, want once:\n%s", c.name, c.w, c.h, c.token, got, v)
		}
	}
}

// TestRowNeverRepeatsThePendingPopup: whatever the order of the two calls, a
// message that the pop-up is showing is not printed under it as well. This is
// the guard that keeps a dialog-paused result from coming back as a duplicate
// once the dialog closes.
func TestRowNeverRepeatsThePendingPopup(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	msg := "handed photo.png to Android (termux-open)"
	h.a.notify(noticeOK, "Handed over", msg)
	h.a.setStatusOnly(msg, lvlOK)
	if v := ansi.Strip(h.view()); strings.Count(strings.ReplaceAll(v, "\n", " "), msg) != 1 {
		t.Fatalf("the row repeated the pop-up:\n%s", v)
	}
	// With the pop-up gone, the row is free to say it again.
	h.a.dismissNotice()
	if v := ansi.Strip(h.view()); !strings.Contains(v, msg) {
		t.Fatalf("the message vanished with the pop-up:\n%s", v)
	}
}
