package ui

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// drain executes cmd (recursively expanding batches) and feeds the resulting
// messages back into the app, emulating the Bubble Tea runtime.
func drain(a *App, cmd tea.Cmd) (quit bool) {
	if cmd == nil {
		return false
	}
	msg := cmd()
	switch m := msg.(type) {
	case tea.BatchMsg:
		for _, c := range m {
			if drain(a, c) {
				quit = true
			}
		}
		return quit
	case tea.QuitMsg:
		return true
	case nil, spinTickMsg:
		// Spinner frames are dropped: drain runs commands one at a time,
		// so a load queued behind the tick would never finish and the
		// spinner would reschedule itself forever. (The real runtime runs
		// commands concurrently; spinner_test.go covers the animation.)
		return false
	default:
		_, next := a.Update(m)
		return drain(a, next)
	}
}

func key(s string) tea.KeyMsg {
	switch s {
	case "tab":
		return tea.KeyMsg{Type: tea.KeyTab}
	case "enter":
		return tea.KeyMsg{Type: tea.KeyEnter}
	case "backspace":
		return tea.KeyMsg{Type: tea.KeyBackspace}
	case "down":
		return tea.KeyMsg{Type: tea.KeyDown}
	case "up":
		return tea.KeyMsg{Type: tea.KeyUp}
	case "space":
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
	case "ctrl+c":
		return tea.KeyMsg{Type: tea.KeyCtrlC}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func press(a *App, keys ...string) (quit bool) {
	for _, k := range keys {
		_, cmd := a.Update(key(k))
		if drain(a, cmd) {
			quit = true
		}
	}
	return quit
}

func setup(t *testing.T) (*App, string) {
	t.Helper()
	home := t.TempDir()
	for _, d := range []string{"docs", "music", "src"} {
		os.MkdirAll(filepath.Join(home, d, "sub"), 0o755)
	}
	os.WriteFile(filepath.Join(home, "notes.txt"), []byte("hi"), 0o644)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // windows

	// Launch from an unrelated working directory: panels must still use HOME.
	wd, _ := os.Getwd()
	os.Chdir(t.TempDir())
	t.Cleanup(func() { os.Chdir(wd) })

	a := New(Options{Version: "test"})
	a.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	drain(a, a.Init())
	return a, home
}

func TestStartsInHomeRegardlessOfCwd(t *testing.T) {
	a, home := setup(t)
	for i := 0; i < 2; i++ {
		if got := a.Panel(i).Path(); got != home {
			t.Fatalf("panel %d path = %s, want %s", i, got, home)
		}
	}
	if a.Active() != 0 {
		t.Fatal("left panel should start active")
	}
}

func TestTabSwitchAndIndependentNavigation(t *testing.T) {
	a, home := setup(t)
	press(a, "down", "enter") // left: open docs
	if a.Panel(0).Path() != filepath.Join(home, "docs") {
		t.Fatalf("left path = %s", a.Panel(0).Path())
	}
	press(a, "tab")
	if a.Active() != 1 {
		t.Fatal("tab did not switch")
	}
	press(a, "j", "j", "l") // right: open music
	if a.Panel(1).Path() != filepath.Join(home, "music") || a.Panel(0).Path() != filepath.Join(home, "docs") {
		t.Fatalf("panels not independent: %s / %s", a.Panel(0).Path(), a.Panel(1).Path())
	}
	press(a, "backspace")
	if a.Panel(1).Path() != home {
		t.Fatal("parent navigation failed")
	}
	if e, _ := a.Panel(1).Current(); e.Name != "music" {
		t.Fatalf("cursor should return to music, got %q", e.Name)
	}
	press(a, "tab")
	if a.Active() != 0 {
		t.Fatal("second tab should return to left")
	}
}

func TestSelectionAndSyncOther(t *testing.T) {
	a, home := setup(t)
	press(a, "down", "space", "space")
	if n := len(a.Panel(0).Selection()); n != 2 {
		t.Fatalf("selected %d", n)
	}
	// both panels show the home directory: the selection is mirrored live
	if n := len(a.Panel(1).Selection()); n != 2 {
		t.Fatalf("selection not mirrored in the other panel showing the same folder: %d", n)
	}
	press(a, "g", "j", "enter") // left into docs: its own, empty selection
	if n := len(a.Panel(0).Selection()); n != 0 {
		t.Fatalf("selection followed the panel into another folder: %d", n)
	}
	if n := len(a.Panel(1).Selection()); n != 2 {
		t.Fatalf("panel staying in home lost its selection: %d", n)
	}
	press(a, "=") // mirror to right: both in docs again
	if a.Panel(1).Path() != filepath.Join(home, "docs") {
		t.Fatalf("= did not sync: %s", a.Panel(1).Path())
	}
	if n := len(a.Panel(1).Selection()); n != 0 {
		t.Fatalf("old selection carried into docs: %d", n)
	}
}

func TestOpenFileShowsStatus(t *testing.T) {
	// Phase 5: Enter on a file opens the read-only viewer (before: a status
	// message announcing it); Esc returns to the same panel state.
	a, _ := setup(t)
	press(a, "G")
	cur := a.ActivePanel().Cursor()
	press(a, "enter")
	if a.viewer == nil {
		t.Fatal("Enter on a file did not open the viewer")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if a.viewer != nil || a.ActivePanel().Cursor() != cur {
		t.Fatal("Esc did not return to the panel")
	}
	a.setStatus("x", false)
	press(a, "k") // any key clears status
	if s, _ := a.Status(); s != "" {
		t.Fatalf("status not cleared: %q", s)
	}
}

func TestInaccessibleDirectoryReportsError(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs non-root unix")
	}
	a, home := setup(t)
	locked := filepath.Join(home, "src")
	os.Chmod(locked, 0)
	defer os.Chmod(locked, 0o755)
	press(a, "j", "j", "j", "enter")
	s, isErr := a.Status()
	if !isErr || !strings.Contains(s, "permission denied") {
		t.Fatalf("status = %q err=%v", s, isErr)
	}
	if a.Panel(0).Path() != home {
		t.Fatal("panel should stay put")
	}
	if !strings.Contains(a.View(), "permission denied") {
		t.Fatal("error not visible in view")
	}
}

func TestHelpOverlay(t *testing.T) {
	a, _ := setup(t)
	press(a, "?")
	if !a.HelpVisible() || !strings.Contains(a.View(), "keyboard controls") {
		t.Fatal("help not shown")
	}
	if press(a, "q") {
		t.Fatal("q should close help, not quit")
	}
	if a.HelpVisible() {
		t.Fatal("help not closed")
	}
}

func TestQuit(t *testing.T) {
	a, _ := setup(t)
	if !press(a, "q") {
		t.Fatal("q should quit")
	}
	b, _ := setup(t)
	if !press(b, "ctrl+c") {
		t.Fatal("ctrl+c should quit")
	}
}

func TestResizeLayout(t *testing.T) {
	a, _ := setup(t)
	sizes := [][2]int{{100, 30}, {80, 24}, {41, 9}, {30, 7}, {200, 60}, {29, 20}, {50, 6}, {1, 1}, {0, 0}}
	for _, s := range sizes {
		w, h := s[0], s[1]
		a.Update(tea.WindowSizeMsg{Width: w, Height: h})
		out := a.View()
		if w == 0 {
			continue
		}
		if w < minWidth || h < minHeight {
			if !strings.Contains(out, "too small") && w > 20 {
				t.Fatalf("%dx%d: expected too-small message", w, h)
			}
			continue
		}
		lines := strings.Split(out, "\n")
		if len(lines) != h {
			t.Fatalf("%dx%d: got %d lines", w, h, len(lines))
		}
		for i, l := range lines {
			if lw := ansi.StringWidth(l); lw > w {
				t.Fatalf("%dx%d: line %d too wide (%d)", w, h, i, lw)
			}
		}
		pw0 := strings.Index(lines[1], "╭") // active panel uses rounded border
		if pw0 != 0 {
			t.Fatalf("%dx%d: left panel border not at column 0: %q", w, h, lines[1])
		}
	}
}

func TestActivePanelVisuallyDistinct(t *testing.T) {
	a, _ := setup(t)
	out := a.View()
	top := strings.Split(out, "\n")[1]
	if !strings.HasPrefix(top, "╭") || !strings.Contains(top, "┌") {
		t.Fatalf("expected rounded active + square inactive borders: %q", top)
	}
	press(a, "tab")
	top = strings.Split(a.View(), "\n")[1]
	if !strings.HasPrefix(top, "┌") || !strings.Contains(top, "╭") {
		t.Fatalf("borders did not swap after tab: %q", top)
	}
	if !strings.Contains(a.View(), "active: right panel") {
		t.Fatal("title should name active panel")
	}
}

func TestKeyMapHasNoConflicts(t *testing.T) {
	k := DefaultKeys()
	seen := map[string]string{}
	v := reflect.ValueOf(k)
	for i := 0; i < v.NumField(); i++ {
		b := v.Field(i).Interface().(Binding)
		for _, s := range b.Keys {
			if prev, ok := seen[s]; ok {
				t.Fatalf("key %q bound to both %s and %s", s, prev, v.Type().Field(i).Name)
			}
			seen[s] = v.Type().Field(i).Name
		}
	}
}

func TestCoalescedRunesAreSplit(t *testing.T) {
	a, _ := setup(t)
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("jjj")})
	drain(a, cmd)
	if a.Panel(0).Cursor() != 3 {
		t.Fatalf("cursor = %d, want 3", a.Panel(0).Cursor())
	}
	// Phase 3: only the first rune of a burst is a command; the rest only
	// while they are movement keys. "qq" closes help but must not quit.
	press(a, "?")
	_, cmd = a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("qq")})
	if drain(a, cmd) || a.quitting || a.showHelp {
		t.Fatal("\"qq\" after help should close help and nothing else")
	}
	b, _ := setup(t)
	_, cmd = b.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("qjj"), Paste: true})
	drain(b, cmd)
	if b.quitting || b.Panel(0).Cursor() != 0 {
		t.Fatal("pasted text must be ignored")
	}
}
