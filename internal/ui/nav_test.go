package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// Back / forward (phase: per-panel history). panel/history_test.go covers the
// state itself; these tests cover the keys, hints and menu that reach it.

// enterFolder opens the named folder and waits for the panel to land.
func enterFolder(h *harness, name string) {
	h.t.Helper()
	h.focus(name)
	h.press("enter")
	h.until("folder "+name, func() bool { return !h.a.ActivePanel().Loading() })
}

func TestBackForwardKeysAndHints(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	p := h.a.Panel(0)
	start := p.Path()
	if f := h.footer(); strings.Contains(f, "back") && !strings.Contains(f, "backspace") {
		t.Fatalf("no history yet, so no back chip: %q", f)
	}
	enterFolder(h, "sub")
	enterFolder(h, "deep")
	if p.Path() == start {
		t.Fatal("setup: the panel did not move")
	}

	// "[" goes back one step, with its cursor restored
	h.press("[")
	h.until("back", func() bool { return p.Path() != env.src+"/sub/deep" })
	if p.Path() != env.src+"/sub" {
		t.Fatalf("after [: %s", p.Path())
	}
	if st := h.status(); !strings.Contains(st, "back to") {
		t.Fatalf("status: %q", st)
	}
	// a forward chip appears once there is somewhere to go forward to
	if f := h.footer(); !strings.Contains(f, "forward") {
		t.Fatalf("forward chip missing after going back: %q", f)
	}
	// Alt+→ is the same action as ]
	h.send(tea.KeyMsg{Type: tea.KeyRight, Alt: true})
	h.until("alt+right", func() bool { return p.Path() == env.src+"/sub/deep" })
	h.press("]")
	h.settle() // already at the end of the history: nothing happens
	if p.Path() != env.src+"/sub/deep" {
		t.Fatalf("] at the end of the history moved: %s", p.Path())
	}
	// Alt+← goes back
	h.send(tea.KeyMsg{Type: tea.KeyLeft, Alt: true})
	h.until("alt+left", func() bool { return p.Path() == env.src+"/sub" })
	if f := h.footer(); !strings.Contains(f, "]") || !strings.Contains(f, "[") {
		t.Fatalf("history chips: %q", f)
	}
	h.press("[")
	h.until("root", func() bool { return p.Path() == start })
	// and back again with an empty history: a warning, no change
	h.press("[")
	if p.Path() != start {
		t.Fatalf("a step into the past: %s", p.Path())
	}
	if st := h.status(); st == "" {
		t.Fatal("an unavailable step must be explained")
	}
	if h.a.statusLevel != lvlWarn {
		t.Fatalf("status level %v", h.a.statusLevel)
	}
}

func TestHistoryIsPerPanelInTheUI(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	left, right := h.a.Panel(0), h.a.Panel(1)
	rightBack, rightFwd := right.NavDepth()
	enterFolder(h, "sub")
	if !left.CanBack() {
		t.Fatal("the navigating panel must gain history")
	}
	if b, f := right.NavDepth(); b != rightBack || f != rightFwd {
		t.Fatalf("the other panel's history changed: %d/%d -> %d/%d", rightBack, rightFwd, b, f)
	}
	// each panel's chips describe its own history: the right panel keeps the
	// one entry the harness gave it when it moved to its folder
	h.press("tab")
	if b, f := right.NavDepth(); b != rightBack || f != rightFwd {
		t.Fatalf("right panel history: %d/%d, want %d/%d", b, f, rightBack, rightFwd)
	}
	if !strings.Contains(h.footer(), "[") {
		t.Fatalf("the right panel's own back chip: %q", h.footer())
	}
	h.press("tab")
	// going back in the left panel leaves the right panel where it was
	h.press("[")
	h.until("back", func() bool { return left.Path() == env.src })
	if right.Path() != env.dst {
		t.Fatalf("the other panel moved: %s", right.Path())
	}
	// the other panel's own history works on its own
	h.press("tab")
	h.press("]")
	if right.Path() != env.dst {
		t.Fatalf("the right panel has no forward entry: %s", right.Path())
	}
	if left.Path() != env.src || !left.CanForward() {
		t.Fatalf("left panel: %s forward=%v", left.Path(), left.CanForward())
	}
}

func TestHistoryMenuEntries(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	enterFolder(h, "sub")
	enterFolder(h, "deep")
	h.press("[")
	h.until("back", func() bool { return !h.a.ActivePanel().Loading() })
	h.press("M")
	h.waitDialog(dlgMenu)
	v := h.view()
	if !strings.Contains(v, "Back") || !strings.Contains(v, "1 step") {
		t.Fatalf("menu lacks Back:\n%s", v)
	}
	if !strings.Contains(v, "Forward") || !strings.Contains(v, "1 step") {
		t.Fatalf("menu lacks Forward with its count:\n%s", v)
	}
	h.press("esc")
	if h.a.dialog != nil {
		t.Fatal("Esc closes the menu")
	}
}

func TestHistoryKeysStayOutOfDialogs(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	enterFolder(h, "sub")
	before := h.a.ActivePanel().Path()
	h.press("n") // new-folder dialog: text input
	if !h.dialogKind(dlgMkdir) {
		t.Fatalf("expected the new-folder dialog, got %v", h.a.dialog.kind)
	}
	h.typeText("x[y]z", true)
	h.send(tea.KeyMsg{Type: tea.KeyLeft, Alt: true})
	h.press("esc")
	if h.a.ActivePanel().Path() != before {
		t.Fatalf("a dialog key moved the panel: %s", h.a.ActivePanel().Path())
	}
	if h.a.ActivePanel().CanForward() {
		t.Fatal("dialog keys must not touch the history")
	}
}
