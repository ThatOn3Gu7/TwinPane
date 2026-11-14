package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// tick runs one marquee frame through the real update path.
func (a *App) tick() {
	if m, _ := a.Update(pathScrollMsg{}); m != nil {
		*a = *(m.(*App))
	}
}

// row returns the status row as the terminal shows it.
func row(a *App) string {
	return ansi.Strip(a.statusView())
}

// A path that does not fit scrolls instead of being cut: the window walks one
// column per frame and wraps around to the beginning of the path.
func TestStatusPathScrollsWhenItDoesNotFit(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	a := h.a
	a.width = 30

	text, ok := a.pathRowText()
	if !ok {
		t.Fatal("the status row should fall back to the path")
	}
	if len(text) <= 29 {
		t.Fatalf("test path is too short to scroll: %q", text)
	}
	if cmd := a.syncPathScroll(nil); cmd == nil || !a.pathScrolling {
		t.Fatal("an overflowing path must schedule a marquee frame")
	}

	first := row(a)
	if got := ansi.StringWidth(first); got != a.width {
		t.Fatalf("row is %d cells wide, want %d: %q", got, a.width, first)
	}
	if want := " " + string([]rune(text)[:a.width-1]); first != want {
		t.Fatalf("first frame = %q, want %q", first, want)
	}

	// A new path rests at its beginning for a moment, then walks.
	for i := 0; i < pathScrollHold; i++ {
		a.tick()
		if a.pathScrollOff != 0 {
			t.Fatalf("the path moved while it should be resting (tick %d)", i)
		}
	}

	// Each frame after that moves the window one column left.
	for i := 1; i <= 3; i++ {
		a.tick()
		want := " " + strings.Repeat("", 0) + string([]rune(text + strings.Repeat(" ", pathScrollGap))[i:i+a.width-1])
		if got := row(a); got != want {
			t.Fatalf("frame %d = %q, want %q", i, got, want)
		}
	}

	// Wrapping: the frame after the gap is the path's beginning again.
	a.pathScrollOff = len([]rune(text)) + pathScrollGap - 1
	a.tick()
	if a.pathScrollOff != 0 {
		t.Fatalf("offset after wrap = %d, want 0", a.pathScrollOff)
	}
	if got := row(a); got != first {
		t.Fatalf("wrapped frame = %q, want the first frame %q", got, first)
	}
}

// A path that fits is drawn whole, and no frame is scheduled for it.
func TestStatusPathFitsWithoutScrolling(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	a := h.a
	a.width = 200

	text, ok := a.pathRowText()
	if !ok {
		t.Fatal("the status row should fall back to the path")
	}
	if cmd := a.syncPathScroll(nil); cmd != nil || a.pathScrolling {
		t.Fatal("a path that fits must not schedule frames")
	}
	if got, want := strings.TrimRight(row(a), " "), " "+text; got != want {
		t.Fatalf("row = %q, want %q", got, want)
	}
}

// Only the path scrolls. A message, a task count or the viewer's position owns
// the row when it is there, and it is shown from its start like before.
func TestStatusMessageStopsTheMarquee(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	a := h.a
	a.width = 30
	a.syncPathScroll(nil)

	a.setStatusRow("copying", lvlInfo)
	if _, ok := a.pathRowText(); ok {
		t.Fatal("a status message must take the row from the path")
	}
	if cmd := a.syncPathScroll(nil); cmd != nil || a.pathScrolling {
		t.Fatal("the marquee must stop while a message is shown")
	}
	if got := row(a); !strings.HasPrefix(got, " copying") {
		t.Fatalf("message row = %q, want it to start at the message", got)
	}

	// Clearing the message brings the path back, starting from its beginning.
	a.setStatusRow("", lvlInfo)
	if _, ok := a.pathRowText(); !ok {
		t.Fatal("the path should return when the message clears")
	}
	if cmd := a.syncPathScroll(nil); cmd == nil {
		t.Fatal("the marquee should resume for an overflowing path")
	}
	if a.pathScrollOff != 0 {
		t.Fatalf("offset = %d, want the restarted marquee at 0", a.pathScrollOff)
	}
}

// Nothing walks behind a window that has the keyboard: the row is drawn but
// nobody is reading it, so the marquee stops until the window closes.
func TestMarqueeStopsUnderAnOverlay(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	a := h.a
	a.width = 30
	if cmd := a.syncPathScroll(nil); cmd == nil {
		t.Fatal("the long test path should scroll")
	}
	a.showHelp = true
	if cmd := a.syncPathScroll(nil); cmd != nil || a.pathScrolling {
		t.Fatal("the marquee kept running under the help screen")
	}
	a.showHelp = false
	if cmd := a.syncPathScroll(nil); cmd == nil {
		t.Fatal("the marquee should resume once the help screen closes")
	}
}
