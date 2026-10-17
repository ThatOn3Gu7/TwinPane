package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/editor"
)

// Phase 7: the editor confirmation card never opens a file without an
// explicit decision, and the help screen is a fixed frame with a filter.

// ---- helpers --------------------------------------------------------------------------

// editorEnv is a mock editor setup: $EDITOR=name found at /opt/name.
func editorEnv(name string) *editor.Env {
	return &editor.Env{Getenv: func(k string) string {
		if k == "EDITOR" {
			return name
		}
		return ""
	}, LookPath: func(n string) (string, error) { return "/opt/" + n, nil }, GOOS: "android"}
}

// p7App is p4App plus a Markdown file, a plain-text file and a binary one.
func p7App(t *testing.T, edEnv *editor.Env) (*harness, *opsEnv, *time.Time) {
	t.Helper()
	h, env, clock := p4App(t, edEnv)
	os.WriteFile(filepath.Join(env.src, "doc.md"), []byte("# Notes\n\n- one\n"), 0o644)
	os.WriteFile(filepath.Join(env.src, "e-plain.txt"), []byte("hello\n"), 0o644)
	os.WriteFile(filepath.Join(env.src, "blob.bin"), []byte{0x7f, 'E', 'L', 'F', 0, 0, 1, 0, 0xff, 0}, 0o644)
	h.exec(h.a.panels[0].Refresh())
	h.settle()
	h.a.ActivePanel().Top()
	return h, env, clock
}

// runPending executes cmd (and batches) and returns the messages it
// produces. A command still running after a moment would be a timer (a
// tea.Tick): it fails the test, because nothing may be scheduled behind
// the user's back.
func runPending(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()
	if cmd == nil {
		return nil
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	select {
	case m := <-done:
		if b, ok := m.(tea.BatchMsg); ok {
			var out []tea.Msg
			for _, c := range b {
				out = append(out, runPending(t, c)...)
			}
			return out
		}
		if m == nil {
			return nil
		}
		return []tea.Msg{m}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("a delayed command (timer) is pending")
		return nil
	}
}

// doubleTapRaw double-taps (x, y) through App.Update directly and returns
// the messages of every command the taps produced, without feeding them
// back. That shows exactly what the tap scheduled.
func doubleTapRaw(t *testing.T, h *harness, clock *time.Time, x, y int) []tea.Msg {
	t.Helper()
	var msgs []tea.Msg
	for i, act := range []tea.MouseAction{tea.MouseActionPress, tea.MouseActionRelease, tea.MouseActionPress, tea.MouseActionRelease} {
		if i == 2 {
			*clock = clock.Add(200 * time.Millisecond)
		}
		_, cmd := h.a.Update(tea.MouseMsg{X: x, Y: y, Button: tea.MouseButtonLeft, Action: act})
		msgs = append(msgs, runPending(t, cmd)...)
	}
	return msgs
}

// buttonPos is the screen position of the dialog button whose label
// contains label.
func (h *harness) buttonPos(label string) (int, int) {
	h.t.Helper()
	h.view()
	d := h.a.dialog
	for _, hh := range d.hits {
		if hh.kind == hitButton && strings.Contains(d.buttons[hh.index].label, label) {
			return hh.x0 + 1, hh.y
		}
	}
	h.t.Fatalf("button %q not found", label)
	return 0, 0
}

// ---- 3. the editor confirmation card -------------------------------------------------

func TestDoubleTapAsksAndWaitsForOpen(t *testing.T) {
	for _, ed := range []string{"nvim", "mdhandler", "nano"} { // the name does not matter
		for _, file := range []string{"doc.md", "e-plain.txt"} {
			for _, tips := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/tips=%v", ed, file, tips), func(t *testing.T) {
					edEnv := editorEnv(ed)
					h, env, clock := p7App(t, edEnv)
					h.a.opts.EditorTips = tips
					want, err := editor.Resolve(*edEnv)
					if err != nil {
						t.Fatal(err)
					}
					x, y := h.rowPos(0, file)
					msgs := doubleTapRaw(t, h, clock, x, y)
					if !h.dialogKind(dlgEditTip) {
						t.Fatalf("double tap must show the confirmation card, dialog %v", h.a.dialog)
					}
					if len(env.execs) != 0 {
						t.Fatalf("the editor started without a decision: %+v", env.execs)
					}
					// whatever the taps produced is harmless (no timer: runPending)
					for _, m := range msgs {
						h.send(m)
					}
					h.settle()
					title := fmt.Sprintf("Open %s in %s?", file, want.Hint().Name)
					if v := h.view(); !strings.Contains(v, title) || strings.Contains(v, "automatically") {
						t.Fatalf("card must name the file and editor and not promise an automatic start:\n%s", v)
					}
					// idle for well over 10 s, with unrelated input: still waiting
					*clock = clock.Add(15 * time.Second)
					h.settle()
					h.send(tea.WindowSizeMsg{Width: 90, Height: 26}) // e.g. keyboard shown
					h.wheel(x, y, 3)                                 // swipes
					h.wheel(x, y, -3)
					h.mouse(x, y, tea.MouseButtonLeft, tea.MouseActionRelease) // stray release
					h.press("j", "k", "x", "o", "y")                           // unrelated keys
					h.typeText("\r\r", true)                                   // pasted newlines
					*clock = clock.Add(15 * time.Second)
					h.settle()
					if !h.dialogKind(dlgEditTip) || len(env.execs) != 0 {
						t.Fatalf("idle time or unrelated input launched / closed it: execs %d dialog %v", len(env.execs), h.a.dialog)
					}
					// Open launches exactly once, with that file
					h.tapButton("Open")
					h.settle()
					if len(env.execs) != 1 {
						t.Fatalf("Open must launch exactly once: %d", len(env.execs))
					}
					args := env.execs[0].args
					if args[0] != "/opt/"+ed || args[len(args)-1] != filepath.Join(env.src, file) {
						t.Fatalf("launched %v", args)
					}
					if h.a.dialog != nil {
						t.Fatal("card must close after Open")
					}
					*clock = clock.Add(15 * time.Second)
					h.settle()
					if len(env.execs) != 1 {
						t.Fatal("launched again later")
					}
				})
			}
		}
	}
}

func TestEditCardCancelAndEsc(t *testing.T) {
	h, env, clock := p7App(t, editorEnv("mdhandler"))
	x, y := h.rowPos(0, "doc.md")
	// Cancel button
	doubleTapRaw(t, h, clock, x, y)
	*clock = clock.Add(time.Second)
	h.tapButton("Cancel")
	if h.a.dialog != nil || len(env.execs) != 0 {
		t.Fatal("Cancel must close without launching")
	}
	if !strings.Contains(h.status(), "not started") {
		t.Fatalf("status %q", h.status())
	}
	// Esc
	*clock = clock.Add(time.Second)
	doubleTapRaw(t, h, clock, x, y)
	h.press("esc")
	*clock = clock.Add(20 * time.Second)
	h.settle()
	if h.a.dialog != nil || len(env.execs) != 0 {
		t.Fatal("Esc must close without launching")
	}
	// Enter on the card (Open is focused) is an explicit decision
	*clock = clock.Add(time.Second)
	doubleTapRaw(t, h, clock, x, y)
	h.press("enter")
	h.settle()
	if len(env.execs) != 1 {
		t.Fatalf("Enter: %d launches", len(env.execs))
	}
	// Tab moves the focus to Cancel: Enter then cancels
	*clock = clock.Add(time.Second)
	doubleTapRaw(t, h, clock, x, y)
	h.press("tab", "enter")
	h.settle()
	if h.a.dialog != nil || len(env.execs) != 1 {
		t.Fatal("Enter on the focused Cancel must not launch")
	}
}

func TestEditCardPendingTouchCannotActivate(t *testing.T) {
	h, env, clock := p7App(t, editorEnv("mdhandler"))
	x, y := h.rowPos(0, "e-plain.txt")
	// tap 1 + the press of tap 2 open the card ...
	h.tap(x, y)
	*clock = clock.Add(150 * time.Millisecond)
	h.mouse(x, y, tea.MouseButtonLeft, tea.MouseActionPress)
	if !h.dialogKind(dlgEditTip) {
		t.Fatal("card expected")
	}
	// ... and the release of tap 2 arrives where Open is drawn
	bx, by := h.buttonPos("Open")
	h.mouse(bx, by, tea.MouseButtonLeft, tea.MouseActionRelease)
	// a quick third tap right on Open (triple tap / bounce) is too early
	*clock = clock.Add(100 * time.Millisecond)
	h.tap(bx, by)
	// a press on Open that is released elsewhere does nothing either
	*clock = clock.Add(time.Second)
	h.mouse(bx, by, tea.MouseButtonLeft, tea.MouseActionPress)
	h.mouse(bx+30, by-3, tea.MouseButtonLeft, tea.MouseActionRelease)
	if len(env.execs) != 0 || !h.dialogKind(dlgEditTip) {
		t.Fatalf("pending touch activated the card: execs %d", len(env.execs))
	}
	// a stale reference to the card's buttons cannot start the editor
	d := h.a.dialog
	h.press("esc")
	if cmd := d.buttons[0].run(h.a); cmd != nil || len(env.execs) != 0 {
		t.Fatal("Open of a closed card must do nothing")
	}
	// another dialog replacing the card: its old Open button is inert
	doubleTapRaw(t, h, clock, x, y)
	old := h.a.dialog
	h.a.dialog = nil
	h.focus("a.txt")
	h.press("d")
	h.settle()
	if cmd := old.buttons[0].run(h.a); cmd != nil || len(env.execs) != 0 {
		t.Fatal("Open of a replaced card must do nothing")
	}
	h.press("esc")
}

func TestKeyboardEditUnchanged(t *testing.T) {
	// tips off: e opens directly, as before
	h, env, clock := p7App(t, editorEnv("mdhandler"))
	h.focus("doc.md")
	h.press("e")
	h.settle()
	if len(env.execs) != 1 || h.a.dialog != nil {
		t.Fatalf("e with tips off: %d launches, dialog %v", len(env.execs), h.a.dialog)
	}
	// tips on: the first e shows the card, which waits for Enter
	h2, env2, clock2 := p7App(t, editorEnv("mdhandler"))
	_ = clock
	h2.a.opts.EditorTips = true
	h2.focus("doc.md")
	h2.press("e")
	*clock2 = clock2.Add(30 * time.Second)
	h2.settle()
	if !h2.dialogKind(dlgEditTip) || len(env2.execs) != 0 {
		t.Fatal("the first e shows the card and waits")
	}
	h2.press("enter")
	h2.settle()
	if len(env2.execs) != 1 {
		t.Fatal("Enter must launch")
	}
}

func TestBinaryConfirmationUnchanged(t *testing.T) {
	h, env, clock := p7App(t, editorEnv("mdhandler"))
	x, y := h.rowPos(0, "blob.bin")
	doubleTapRaw(t, h, clock, x, y)
	if !h.dialogKind(dlgBinary) || len(env.execs) != 0 {
		t.Fatal("binary file: the binary warning comes first")
	}
	v := h.view()
	for _, w := range []string{"looks like a binary file", "Open anyway (o)", "Cancel (Esc)"} {
		if !strings.Contains(v, w) {
			t.Fatalf("binary dialog lacks %q:\n%s", w, v)
		}
	}
	*clock = clock.Add(20 * time.Second)
	h.settle()
	if !h.dialogKind(dlgBinary) || len(env.execs) != 0 {
		t.Fatal("the binary dialog has no timer either")
	}
	h.press("esc")
	if h.a.dialog != nil || len(env.execs) != 0 {
		t.Fatal("Esc cancels")
	}
	// "Open anyway" is the decision: the editor starts (no second card)
	*clock = clock.Add(time.Second)
	doubleTapRaw(t, h, clock, x, y)
	h.press("o")
	h.settle()
	if len(env.execs) != 1 || h.a.dialog != nil {
		t.Fatalf("Open anyway: %d launches, dialog %v", len(env.execs), h.a.dialog)
	}
}

// ---- 1./2. the help screen -----------------------------------------------------------------

// helpFrame locates the help box on the screen and checks that it is
// intact: four corners, both side borders on every row, nothing drawn
// outside it in the body area.
type frame struct{ top, bottom, left, right int }

func cells(line string) []string {
	var out []string
	for _, r := range line {
		s := string(r)
		out = append(out, s)
		for w := ansi.StringWidth(s); w > 1; w-- {
			out = append(out, "")
		}
	}
	return out
}

func helpFrame(t *testing.T, v string, w, hgt int) frame {
	t.Helper()
	checkScreen(t, v, w, hgt)
	lines := strings.Split(ansi.Strip(v), "\n")
	f := frame{top: -1, bottom: -1, left: -1, right: -1}
	for i, l := range lines {
		c := cells(l)
		for j, s := range c {
			switch s {
			case "╭":
				f.top, f.left = i, j
			case "╮":
				if i == f.top {
					f.right = j
				}
			case "╰":
				f.bottom = i
			}
		}
	}
	if f.top < 1 || f.bottom <= f.top || f.left < 0 || f.right <= f.left {
		t.Fatalf("help frame not found / not intact (%+v):\n%s", f, ansi.Strip(v))
	}
	if f.bottom > hgt-3 {
		t.Fatalf("help frame overlaps the status/hint rows: %+v", f)
	}
	at := func(row, col int) string {
		c := cells(lines[row])
		if col < len(c) {
			return c[col]
		}
		return ""
	}
	if at(f.bottom, f.left) != "╰" || at(f.bottom, f.right) != "╯" {
		t.Fatalf("bottom corners missing:\n%s", ansi.Strip(v))
	}
	for row := 1; row <= hgt-3; row++ {
		c := cells(lines[row])
		inBox := row >= f.top && row <= f.bottom
		for col, s := range c {
			if inBox && col >= f.left && col <= f.right {
				continue
			}
			if s != " " && s != "" {
				t.Fatalf("drawn outside the help box at row %d col %d (%q):\n%s", row, col, s, ansi.Strip(v))
			}
		}
		if inBox && row > f.top && row < f.bottom {
			if at(row, f.left) != "│" || (at(row, f.right) != "│" && at(row, f.right) != "┃") {
				t.Fatalf("side border broken on row %d:\n%s", row, ansi.Strip(v))
			}
		}
	}
	return f
}

// helpBody returns the text inside the frame (between the side borders).
func helpBody(v string, f frame) string {
	lines := strings.Split(ansi.Strip(v), "\n")
	var b strings.Builder
	for row := f.top + 1; row < f.bottom; row++ {
		c := cells(lines[row])
		b.WriteString(strings.Join(c[f.left+1:min(f.right, len(c))], ""))
		b.WriteString("\n")
	}
	return b.String()
}

func TestHelpFixedFrameScrolls(t *testing.T) {
	for _, size := range [][2]int{{50, 20}, {80, 24}, {60, 14}, {100, 30}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			h, _, _ := p7App(t, nil)
			h.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			h.press("?")
			v := h.view()
			f0 := helpFrame(t, v, size[0], size[1])
			if !h.a.helpScrollable {
				t.Fatalf("help should scroll at %dx%d", size[0], size[1])
			}
			top := helpBody(v, f0)
			// middle and end: the frame stays exactly where it was
			var bodies []string
			for _, keys := range [][]string{{"j", "j", "j"}, {"pgdown"}, {"G"}} {
				h.press(keys...)
				v = h.view()
				if f := helpFrame(t, v, size[0], size[1]); f != f0 {
					t.Fatalf("frame moved while scrolling: %+v -> %+v", f0, f)
				}
				if !strings.Contains(strings.Split(ansi.Strip(v), "\n")[f0.top], "TwinPane") {
					t.Fatal("title must stay in the top border")
				}
				bodies = append(bodies, helpBody(v, f0))
			}
			if bodies[0] == top || bodies[1] == bodies[0] || bodies[2] == top {
				t.Fatal("the content must scroll")
			}
			// the end shows the last rows, with the position in the border
			last := strings.Split(ansi.Strip(v), "\n")[f0.bottom]
			if !strings.Contains(last, "/") || !strings.Contains(bodies[2], "dialogs: cancel") {
				t.Fatalf("end of help: position %q body:\n%s", last, bodies[2])
			}
			// the scroll thumb sits on the right border
			if !strings.Contains(v, "┃") {
				t.Fatal("scroll indicator missing")
			}
			// wheel / swipe scrolls back up inside the same frame
			before := h.a.helpScroll
			h.wheel(size[0]/2, f0.top+2, -3)
			v = h.view()
			if f := helpFrame(t, v, size[0], size[1]); f != f0 || h.a.helpScroll != before-3*wheelStep || !h.a.showHelp {
				t.Fatalf("wheel must scroll inside the same frame: %d -> %d", before, h.a.helpScroll)
			}
			h.press("g")
			if v = h.view(); helpBody(v, f0) != top {
				t.Fatal("g must return to the top")
			}
		})
	}
}

func TestHelpSmallAndResize(t *testing.T) {
	h, _, _ := p7App(t, nil)
	h.press("?")
	for _, size := range [][2]int{{30, 7}, {30, 10}, {34, 8}, {40, 12}, {80, 24}, {120, 40}, {31, 9}} {
		h.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for _, keys := range [][]string{{"g"}, {"j"}, {"G"}} {
			h.press(keys...)
			helpFrame(t, h.view(), size[0], size[1])
		}
		if !h.a.showHelp {
			t.Fatalf("help closed at %v", size)
		}
	}
	// with a filter, through resizes (on-screen keyboard up and down)
	h.press("/")
	h.typeText("e", false)
	for _, size := range [][2]int{{80, 24}, {80, 12}, {30, 7}, {50, 20}} {
		h.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		h.press("down", "down", "pgdown")
		v := h.view()
		f := helpFrame(t, v, size[0], size[1])
		if !strings.Contains(helpBody(v, f), "Filter: e") {
			t.Fatalf("filter row missing at %v:\n%s", size, ansi.Strip(v))
		}
	}
}

func TestHelpFilter(t *testing.T) {
	h, env, _ := p7App(t, nil)
	h.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	rowsBefore := DefaultKeys().helpRows()
	mouseBefore := append([]Binding(nil), mouseHelp...)
	h.press("?")
	full := h.view()
	f0 := helpFrame(t, full, 80, 24)
	if !strings.Contains(full, "/ filter") || !strings.Contains(h.footer(), "/  filter") {
		t.Fatal("the filter must be documented on the help screen")
	}
	h.press("/")
	if !h.a.help.typing {
		t.Fatal("/ must start the filter")
	}
	// keys go to the field, never to the panels or global commands
	cursor := h.a.ActivePanel().Cursor()
	h.typeText("QUIT", false)
	v := h.view()
	f := helpFrame(t, v, 80, 24)
	if f != f0 {
		t.Fatalf("the frame must not change size while filtering: %+v -> %+v", f0, f)
	}
	body := helpBody(v, f)
	if !strings.Contains(body, "Filter: QUIT") || !strings.Contains(body, "q         quit") || strings.Contains(body, "copy to other panel") {
		t.Fatalf("case-insensitive filter:\n%s", body)
	}
	if !strings.Contains(v, "filtering") || !strings.Contains(h.footer(), "clear filter") {
		t.Fatal("an active filter must be visible")
	}
	upper := body
	h.press("ctrl+u")
	h.typeText("quit", false)
	if b := helpBody(h.view(), f); strings.Replace(b, "Filter: quit", "Filter: QUIT", 1) != upper {
		t.Fatalf("lower and upper case must match the same rows:\n%s\nvs\n%s", b, upper)
	}
	if h.quit || h.a.dialog != nil || h.a.ActivePanel().Cursor() != cursor || len(env.execs) != 0 {
		t.Fatal("typing in the filter leaked into the app")
	}
	// key names match too: ctrl+r is shown as ^R
	h.press("ctrl+u")
	h.typeText("ctrl+r", false)
	if b := helpBody(h.view(), f); !strings.Contains(b, "refresh both panels") {
		t.Fatalf("key names must match:\n%s", b)
	}
	// several words: all must match (here a section title plus a word)
	h.press("ctrl+u")
	h.typeText("archives zip", false)
	b := helpBody(h.view(), f)
	if !strings.Contains(b, "Archives") || !strings.Contains(b, "browse (no extraction)") || strings.Contains(b, "compress (ZIP") == false {
		t.Fatalf("multi-word filter:\n%s", b)
	}
	// no matches: a message, still inside the frame
	h.press("ctrl+u")
	h.typeText("zzqx", false)
	v = h.view()
	if b := helpBody(v, helpFrame(t, v, 80, 24)); !strings.Contains(b, "No shortcuts match") || !strings.Contains(b, "0/") {
		t.Fatalf("empty result:\n%s", b)
	}
	// pasted text is accepted by the field
	h.press("ctrl+u")
	h.typeText("delete", true)
	if b := helpBody(h.view(), f); !strings.Contains(b, "delete (trash if available)") {
		t.Fatalf("pasted filter:\n%s", b)
	}
	// Enter keeps the results; Esc then shows everything again
	h.press("enter")
	if h.a.help.typing || h.a.help.filter == nil || !strings.Contains(h.footer(), "show all") {
		t.Fatal("Enter keeps the filter and leaves the field")
	}
	h.press("esc")
	if !h.a.showHelp || h.a.help.filter != nil || h.view() != full {
		t.Fatal("clearing the filter must restore the full help exactly")
	}
	// Esc while typing clears too; the next Esc closes the help
	h.press("/")
	h.typeText("sel", false)
	h.press("esc")
	if !h.a.showHelp || h.a.help.filter != nil {
		t.Fatal("Esc in the field clears the filter")
	}
	h.press("esc")
	if h.a.showHelp {
		t.Fatal("the second Esc closes the help")
	}
	// Backspace on an empty field leaves filter mode
	h.press("?", "/", "backspace")
	if !h.a.showHelp || h.a.help.typing || h.a.help.filter != nil {
		t.Fatal("Backspace on an empty filter leaves it")
	}
	// reopening starts unfiltered
	h.press("/")
	h.typeText("copy", false)
	h.press("enter", "x") // any other key closes
	h.press("?")
	if h.a.help.filter != nil || h.view() != full {
		t.Fatal("help must reopen unfiltered")
	}
	if !reflect.DeepEqual(DefaultKeys().helpRows(), rowsBefore) || !reflect.DeepEqual(mouseHelp, mouseBefore) {
		t.Fatal("filtering must not change the help data")
	}
}

func TestHelpFilterScrollEsc(t *testing.T) {
	h, env, _ := p7App(t, nil)
	h.send(tea.WindowSizeMsg{Width: 44, Height: 14})
	h.press("?", "/")
	h.typeText("e", false) // most rows
	v := h.view()
	f := helpFrame(t, v, 44, 14)
	if !h.a.helpScrollable {
		t.Fatal("many results must scroll")
	}
	first := helpBody(v, f)
	h.press("down", "down", "pgdown")
	v = h.view()
	if g := helpFrame(t, v, 44, 14); g != f {
		t.Fatal("frame moved")
	}
	scrolled := helpBody(v, f)
	if scrolled == first || !strings.Contains(scrolled, "Filter: e") {
		t.Fatalf("results must scroll below the fixed filter row:\n%s", scrolled)
	}
	// typing more resets to the top of the new results
	h.typeText("d", false)
	if h.a.helpScroll != 0 {
		t.Fatal("new results start at the top")
	}
	// Enter, scroll with j/k (field not focused), Esc clears, Esc closes
	h.press("backspace", "enter", "j", "j")
	if h.a.helpScroll != 2 || !h.a.showHelp {
		t.Fatalf("j scrolls the filtered results: %d", h.a.helpScroll)
	}
	h.press("esc", "esc")
	if h.a.showHelp || h.a.dialog != nil || len(env.execs) != 0 || h.quit {
		t.Fatal("Esc, Esc: clear filter, close help, nothing else")
	}
}

func TestHelpTouch(t *testing.T) {
	h, _, clock := p7App(t, nil)
	h.send(tea.WindowSizeMsg{Width: 60, Height: 20})
	h.tapHint("?")
	if !h.a.showHelp {
		t.Fatal("? hint")
	}
	*clock = clock.Add(time.Second)
	h.tapHint("/")
	if !h.a.help.typing {
		t.Fatal("the / hint starts the filter")
	}
	h.press("esc")
	// a tap on the top border (where "/ filter" is shown) starts it too
	f := helpFrame(t, h.view(), 60, 20)
	h.tap(f.left+5, f.top)
	if !h.a.showHelp || !h.a.help.typing {
		t.Fatal("tap on the filter hint starts the filter")
	}
	h.typeText("swipe", false)
	// a swipe scrolls, a tap elsewhere closes as before
	h.wheel(30, 10, 2)
	if !h.a.showHelp {
		t.Fatal("wheel must not close the help")
	}
	h.tap(30, 10)
	if h.a.showHelp {
		t.Fatal("tap closes help")
	}
}

// Bubble Tea renders on a timer: keys can arrive before the help was ever
// drawn, or right after a resize. Scroll keys must still scroll.
func TestHelpKeysBeforeRender(t *testing.T) {
	h, _, _ := p7App(t, nil)
	h.send(tea.WindowSizeMsg{Width: 50, Height: 16})
	h.press("?", "j", "j") // no View() in between
	if !h.a.showHelp || h.a.helpScroll != 2 {
		t.Fatalf("j right after ? must scroll: help %v scroll %d", h.a.showHelp, h.a.helpScroll)
	}
	h.send(tea.WindowSizeMsg{Width: 120, Height: 60}) // now everything fits
	h.press("j")
	if h.a.showHelp {
		t.Fatal("when everything fits, j closes the help as before")
	}
	h.press("?")
	h.send(tea.WindowSizeMsg{Width: 40, Height: 10}) // keyboard up: scrolls again
	h.press("G")
	if !h.a.showHelp || h.a.helpScroll == 0 {
		t.Fatal("G after a resize must scroll")
	}
}
