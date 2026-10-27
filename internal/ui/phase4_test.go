package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/editor"
	"github.com/ThatOn3Gu7/TwinPane/internal/panel"
)

// ---- helpers --------------------------------------------------------------------

// p4App is an ops harness with a controllable clock and many files so the
// panels scroll. Screen 100x24: panels 50 wide, 21 high (14 list rows).
func p4App(t *testing.T, edEnv *editor.Env) (*harness, *opsEnv, *time.Time) {
	t.Helper()
	h, env := newOpsApp(t, nil, edEnv)
	for i := 0; i < 30; i++ {
		os.WriteFile(filepath.Join(env.src, fmt.Sprintf("f%02d.txt", i)), []byte("x"), 0o644)
	}
	clock := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	h.a.now = func() time.Time { return clock }
	h.a.opts.Mouse = true
	h.exec(h.a.panels[0].Refresh())
	h.settle()
	h.send(tea.WindowSizeMsg{Width: 100, Height: 24})
	h.settle()
	return h, env, &clock
}

func (h *harness) mouse(x, y int, b tea.MouseButton, act tea.MouseAction) {
	h.send(tea.MouseMsg{X: x, Y: y, Button: b, Action: act})
	h.settle()
}

// tap sends what Termux sends for a finger tap: press + release.
func (h *harness) tap(x, y int) {
	h.mouse(x, y, tea.MouseButtonLeft, tea.MouseActionPress)
	h.mouse(x, y, tea.MouseButtonLeft, tea.MouseActionRelease)
}

func (h *harness) wheel(x, y, n int) {
	b := tea.MouseButtonWheelDown
	if n < 0 {
		b, n = tea.MouseButtonWheelUp, -n
	}
	for i := 0; i < n; i++ {
		h.mouse(x, y, b, tea.MouseActionPress)
	}
}

// rowPos returns a screen position on the row showing name in panel i.
func (h *harness) rowPos(i int, name string) (int, int) {
	h.t.Helper()
	p := h.a.panels[i]
	x0 := 0
	if i == 1 {
		x0 = h.a.width / 2
	}
	for y := 1; y < h.a.height-2; y++ {
		if hit := p.HitTest(5, y-1); hit.Kind == panel.HitRow && p.Entries()[hit.Index].Name == name {
			return x0 + 5, y
		}
	}
	h.t.Fatalf("%q not visible in panel %d", name, i)
	return 0, 0
}

// footer returns the hint bar text with every run of 2+ spaces collapsed
// to two, so tests can match "Esc  cancel" whatever the chip padding is
// (footerRaw keeps the exact cells).
func (h *harness) footer() string {
	return squashSpaces(h.footerRaw())
}

// footerRaw returns the hint bar text exactly as drawn (ANSI stripped).
func (h *harness) footerRaw() string {
	v := h.view()
	return v[strings.LastIndex(v, "\n")+1:]
}

var multiSpace = regexp.MustCompile(`  +`)

func squashSpaces(s string) string { return multiSpace.ReplaceAllString(s, "  ") }

// tapHint taps the hint-bar item that sends key.
func (h *harness) tapHint(key string) {
	h.t.Helper()
	h.view()
	for _, fh := range h.a.footerHits {
		if fh.send == key {
			h.tap(fh.x0+1, h.a.height-1)
			return
		}
	}
	h.t.Fatalf("no hint for %q in %q", key, h.footer())
}

// tapButton taps the dialog button whose label contains label.
func (h *harness) tapButton(label string) {
	h.t.Helper()
	h.view()
	d := h.a.dialog
	for _, hh := range d.hits {
		if hh.kind == hitButton && strings.Contains(d.buttons[hh.index].label, label) {
			h.tap(hh.x0+1, hh.y)
			return
		}
	}
	h.t.Fatalf("button %q not found", label)
}

// thumbCells returns the screen rows where column x shows the scroll thumb.
func thumbCells(v string, x int) []int {
	var rows []int
	for y, l := range strings.Split(v, "\n") {
		r := []rune(l)
		if x < len(r) && string(r[x]) == panel.ScrollThumbChar {
			rows = append(rows, y)
		}
	}
	return rows
}

// ---- 1. scroll indicators ----------------------------------------------------------

func TestScrollIndicatorsPerPanelAndUpdates(t *testing.T) {
	h, env, _ := p4App(t, nil)
	right := h.a.width - 1
	leftEdge := h.a.width/2 - 1
	v := h.view()
	if got := thumbCells(v, leftEdge); len(got) == 0 {
		t.Fatalf("left panel (34 entries, 14 rows) has no indicator:\n%s", v)
	}
	if got := thumbCells(v, right); len(got) != 0 {
		t.Fatal("right panel (empty) must not show an indicator")
	}
	top := thumbCells(v, leftEdge)
	// scrolling moves it
	h.press("G")
	if bottom := thumbCells(h.view(), leftEdge); bottom[0] <= top[0] {
		t.Fatalf("indicator did not move: %v -> %v", top, bottom)
	}
	// the inactive panel keeps its own indicator
	h.exec(h.a.panels[1].ChangeDir(env.src))
	h.settle()
	h.press("tab")
	v = h.view()
	l, r := thumbCells(v, leftEdge), thumbCells(v, right)
	if len(l) == 0 || len(r) == 0 || l[0] == r[0] {
		t.Fatalf("independent indicators expected: left %v right %v", l, r)
	}
	// a resize that fits everything removes them; a different panel size
	// (odd width) keeps them aligned with each border
	h.send(tea.WindowSizeMsg{Width: 101, Height: 60})
	v = h.view()
	if len(thumbCells(v, 100)) != 0 || len(thumbCells(v, 49)) != 0 {
		t.Fatal("indicator although everything fits")
	}
	h.send(tea.WindowSizeMsg{Width: 101, Height: 20})
	v = h.view()
	if len(thumbCells(v, 100)) == 0 || len(thumbCells(v, 49)) == 0 {
		t.Fatalf("indicators lost after resize:\n%s", v)
	}
	checkScreen(t, v, 101, 20)
	// hidden toggle and sorting update the content and the indicator
	for i := 0; i < 20; i++ {
		os.WriteFile(filepath.Join(env.src, fmt.Sprintf(".h%02d", i)), nil, 0o644)
	}
	before := thumbCells(h.view(), 100)
	h.press("h")
	after := thumbCells(h.view(), 100)
	if len(after) >= len(before) {
		t.Fatalf("more entries must shrink the thumb: %v -> %v", before, after)
	}
}

func TestSearchResultsIndicator(t *testing.T) {
	h, _, _ := p4App(t, nil)
	h.press("/")
	h.searchDone()
	h.typeText("f", false)
	h.searchDone()
	col := h.a.width - 1 // the indicator is drawn on the results panel's border
	v := h.view()
	if len(thumbCells(v, col)) == 0 {
		t.Fatalf("search results without indicator:\n%s", v)
	}
	first := thumbCells(v, col)[0]
	h.press("tab", "G")
	h.searchDone()
	if v := h.view(); len(thumbCells(v, col)) == 0 || thumbCells(v, col)[0] <= first {
		t.Fatalf("search indicator did not follow: %d -> %v (cursor %d offset %d)\n%s", first, thumbCells(v, col), h.a.search.cursor, h.a.search.offset, v)
	}
	// a narrow query that fits removes it
	h.press("/")
	h.typeText("01", false)
	h.searchDone()
	if got := thumbCells(h.view(), col); len(got) != 0 {
		t.Fatalf("indicator for a list that fits: %v", got)
	}
	checkScreen(t, h.view(), 100, 24)
}

// ---- 2. context hint bar -----------------------------------------------------------

func TestHintBarFollowsContext(t *testing.T) {
	h, _, _ := p4App(t, nil)
	h.send(tea.WindowSizeMsg{Width: 160, Height: 30})
	has := func(what string, want, reject []string) {
		t.Helper()
		f := h.footer()
		for _, w := range want {
			if !strings.Contains(f, w) {
				t.Errorf("%s: hint bar lacks %q: %q", what, w, f)
			}
		}
		for _, r := range reject {
			if strings.Contains(f, r) {
				t.Errorf("%s: hint bar must not show %q: %q", what, r, f)
			}
		}
	}
	h.ActivePanelTop() // on ".."
	has("on ..", []string{"Tab  switch", "⏎  open", "find", "help"}, []string{"copy", "delete", "edit", "Space"})
	h.focus("sub")
	has("on a folder", []string{"open", "copy", "move", "delete", "Space  select"}, []string{"e/E"})
	h.focus("a.txt")
	has("on a file", []string{"copy", "rename", "e/E  edit"}, []string{"⏎  open", "clear"})
	h.press(" ")
	h.focus("b.txt")
	h.press(" ")
	has("selection", []string{"u  clear", "bulk rename"}, []string{"⏎  open"})
	h.press("u")

	h.press("d")
	h.waitDialog(dlgDelete)
	has("delete dialog", []string{"⏎/n  no, cancel", "y  yes, move to trash", "Esc  cancel"}, []string{"help", "copy"})
	h.press("esc")

	h.press("?")
	has("help", []string{"close help"}, []string{"copy"})
	h.press("esc")

	h.press("/")
	h.searchDone()
	has("search (typing)", []string{"to filter", "Esc  close"}, []string{"copy", "help"})
	h.typeText("f0", false)
	h.searchDone()
	h.press("tab")
	has("search (results)", []string{"go to", "e  edit", "Space  mark", "Esc  close"}, []string{"to filter"})
	h.press("esc")

	h.focus("a.txt")
	h.press(" ")
	h.focus("b.txt")
	h.press(" ")
	h.press("r")
	has("bulk rename", []string{"a  apply…", "Esc  cancel", "find/replace"}, []string{"help", "copy"})
	h.press("esc")

	// narrow: compact, the most important first, help pinned
	h.send(tea.WindowSizeMsg{Width: 40, Height: 20})
	h.focus("a.txt")
	f := h.footerRaw()
	if !strings.HasPrefix(f, "  Tab   switch ") || !strings.HasSuffix(strings.TrimRight(f, " "), "?   help") || ansi.StringWidth(f) != 40 {
		t.Fatalf("narrow hint bar %q", f)
	}
	// while editing nothing is offered
	h.a.editing = true
	if strings.TrimSpace(h.footer()) != "" {
		t.Fatalf("hint bar while editing: %q", h.footer())
	}
	h.a.editing = false
}

func TestHintBarPanelErrorAndBusy(t *testing.T) {
	h, env, _ := p4App(t, nil)
	h.send(tea.WindowSizeMsg{Width: 160, Height: 30})
	gone := filepath.Join(env.root, "gone")
	os.MkdirAll(gone, 0o755)
	h.exec(h.a.panels[0].Load(gone, "", panel.LoadForce))
	h.settle()
	os.RemoveAll(gone)
	h.exec(h.a.panels[0].Load(gone, "", panel.LoadForce))
	h.settle()
	if f := h.footer(); !strings.Contains(f, "⌫  parent") || !strings.Contains(f, "^L  retry") || strings.Contains(f, "copy") {
		t.Fatalf("error hints: %q", f)
	}
	task := &opSession{id: 990, title: "Checking delete", state: taskPlanning, events: make(chan tea.Msg, 1)}
	h.a.tasks.add(task)
	if f := h.footer(); !strings.Contains(f, "tasks") || !strings.Contains(f, "retry") {
		t.Fatalf("background work should keep normal error-panel hints: %q", f)
	}
	h.a.tasks.showing = true
	// The hints follow the selected row: an active task can be cancelled, but
	// there is nothing finished to clear yet.
	if f := h.footer(); !strings.Contains(f, "cancel selected") || strings.Contains(f, "clear finished") {
		t.Fatalf("active task hints: %q", f)
	}
	h.a.finishTask(task, taskCanceled, "cancelled")
	if f := h.footer(); !strings.Contains(f, "clear finished") || strings.Contains(f, "cancel") {
		t.Fatalf("finished task hints: %q", f)
	}
	h.a.tasks.showing = false
	h.a.tasks.clearFinished()
}

func TestHelpMatchesBindings(t *testing.T) {
	h, _, _ := p4App(t, nil)
	h.send(tea.WindowSizeMsg{Width: 80, Height: 60})
	h.press("?")
	v := h.view()
	k := DefaultKeys()
	// The help is taller than the screen once every section is listed, so all
	// pages are collected: a binding that lands on the last page still counts.
	all := v
	for i := 0; i < 40 && h.a.helpScrollable; i++ {
		prev := h.view()
		h.press("pgdown")
		next := h.view()
		if next == prev {
			break
		}
		all += "\n" + next
	}
	for _, b := range k.helpRows() {
		if !strings.Contains(all, b.Help) || !strings.Contains(all, b.Desc) {
			t.Errorf("help lacks %q %q", b.Help, b.Desc)
		}
	}
	for _, w := range []string{"Mouse & touch", "2× tap", "swipe", "External editor", "Neovim", ":wq"} {
		if !strings.Contains(all, w) {
			t.Errorf("help lacks %q", w)
		}
	}
	checkScreen(t, v, 80, 60)
	// on a short screen the help scrolls instead of being cut off
	h.press("esc")
	h.send(tea.WindowSizeMsg{Width: 50, Height: 20})
	h.press("?")
	if h.view(); !h.a.helpScrollable || !strings.Contains(h.footer(), "scroll") {
		t.Fatal("tall help must be scrollable")
	}
	first := h.view()
	h.press("j")
	if !h.a.showHelp || h.view() == first {
		t.Fatal("j must scroll the help, not close it")
	}
	checkScreen(t, h.view(), 50, 20)
	h.press("x")
	if h.a.showHelp {
		t.Fatal("other keys close the help")
	}
}

// ---- 3. mouse / touch --------------------------------------------------------------

func TestTapFocusesAndMovesCursorOnly(t *testing.T) {
	h, env, clock := p4App(t, nil)
	h.exec(h.a.panels[1].ChangeDir(env.src))
	h.settle()
	x, y := h.rowPos(1, "b.txt")
	h.tap(x, y)
	p := h.a.panels[1]
	if h.a.active != 1 || p.Entries()[p.Cursor()].Name != "b.txt" {
		t.Fatalf("active %d cursor on %q", h.a.active, p.Entries()[p.Cursor()].Name)
	}
	if len(p.Selection()) != 0 || len(env.execs) != 0 || h.a.dialog != nil {
		t.Fatal("a single tap must not select, open or edit")
	}
	// tapping the other panel's header/path area focuses it too
	*clock = clock.Add(time.Second)
	h.tap(5, 2)
	if h.a.active != 0 {
		t.Fatal("tap on the left panel's path line must focus it")
	}
}

func TestDoubleTapOpensOnce(t *testing.T) {
	h, env, clock := p4App(t, nil)
	x, y := h.rowPos(0, "sub")
	h.tap(x, y)
	*clock = clock.Add(200 * time.Millisecond)
	h.tap(x, y)
	if got := h.a.panels[0].Path(); got != filepath.Join(env.src, "sub") {
		t.Fatalf("double tap on a folder: path %s", got)
	}
	// the third tap of a triple tap lands in the new folder: no second open
	*clock = clock.Add(100 * time.Millisecond)
	h.tap(x, y)
	if got := h.a.panels[0].Path(); got != filepath.Join(env.src, "sub") {
		t.Fatalf("triple tap opened twice: %s", got)
	}
	h.press("backspace")
	*clock = clock.Add(time.Second)

	// file: exactly one editor run with that file only (even with a selection)
	h.focus("b.txt")
	h.press(" ")
	x, y = h.rowPos(0, "a.txt")
	h.tap(x, y)
	*clock = clock.Add(300 * time.Millisecond)
	h.tap(x, y)
	h.settle()
	// Phase 7: a double tap always asks first; Enter (Open) starts it once
	if !h.dialogKind(dlgEditTip) || len(env.execs) != 0 {
		t.Fatalf("double tap on a file must show the confirmation card first: %+v", env.execs)
	}
	*clock = clock.Add(time.Second)
	h.press("enter")
	h.settle()
	if len(env.execs) != 1 || !strings.HasSuffix(strings.Join(env.execs[0].args, " "), "nvim "+filepath.Join(env.src, "a.txt")) {
		t.Fatalf("double tap on a file: %+v", env.execs)
	}
	// too slow is two single taps; different rows are no double tap
	*clock = clock.Add(time.Second)
	h.tap(x, y)
	*clock = clock.Add(time.Second)
	h.tap(x, y)
	x2, y2 := h.rowPos(0, "f00.txt")
	*clock = clock.Add(100 * time.Millisecond)
	h.tap(x2, y2)
	if len(env.execs) != 1 {
		t.Fatalf("slow taps / other rows opened: %d runs", len(env.execs))
	}
	// a key between two taps cancels the double tap
	h.tap(x, y)
	h.press("j", "k")
	h.tap(x, y)
	if len(env.execs) != 1 {
		t.Fatal("key between taps must reset the double tap")
	}
}

func TestWheelScrollsPanelUnderPointer(t *testing.T) {
	h, env, _ := p4App(t, nil)
	h.exec(h.a.panels[1].ChangeDir(env.src))
	h.settle()
	h.wheel(70, 10, 5) // over the right panel; the left one is active
	if h.a.active != 0 || h.a.panels[1].Offset() != 5 || h.a.panels[0].Offset() != 0 {
		t.Fatalf("active %d right offset %d left offset %d", h.a.active, h.a.panels[1].Offset(), h.a.panels[0].Offset())
	}
	p := h.a.panels[1]
	if c := p.Cursor(); c < p.Offset() {
		t.Fatalf("cursor %d left the view (offset %d)", c, p.Offset())
	}
	h.wheel(70, 10, -2)
	if p.Offset() != 3 {
		t.Fatalf("wheel up: offset %d", p.Offset())
	}
	h.wheel(70, 10, 500)
	total, visible, off := p.ScrollInfo()
	if off != total-visible {
		t.Fatalf("clamped at the end: %d of %d/%d", off, total, visible)
	}
	// wheel over the title, status or hint bar does nothing
	h.wheel(70, 0, 3)
	h.wheel(70, 23, 3)
	if p.Offset() != off {
		t.Fatal("wheel outside the panels scrolled")
	}
}

func TestScrollbarClickAndDrag(t *testing.T) {
	h, _, _ := p4App(t, nil)
	edge := h.a.width/2 - 1
	p := h.a.panels[0]
	// list rows are screen rows 4..17 (title, border, path, header)
	h.mouse(edge, 17, tea.MouseButtonLeft, tea.MouseActionPress)
	total, visible, off := p.ScrollInfo()
	if off != total-visible {
		t.Fatalf("click at the bottom of the track: offset %d", off)
	}
	h.mouse(edge, 10, tea.MouseButtonLeft, tea.MouseActionMotion)
	if _, _, off := p.ScrollInfo(); off == 0 || off == total-visible {
		t.Fatalf("drag to the middle: offset %d", off)
	}
	h.mouse(edge, 2, tea.MouseButtonLeft, tea.MouseActionMotion) // above the track
	if p.Offset() != 0 {
		t.Fatalf("drag above the track: offset %d", p.Offset())
	}
	h.mouse(edge, 2, tea.MouseButtonLeft, tea.MouseActionRelease)
	h.mouse(edge, 17, tea.MouseButtonLeft, tea.MouseActionMotion) // no button held: ignored
	if p.Offset() != 0 {
		t.Fatal("motion after release must not scroll")
	}
	if len(p.Selection()) != 0 {
		t.Fatal("scrollbar use must not select")
	}
}

func TestRightClickTogglesMark(t *testing.T) {
	h, _, _ := p4App(t, nil)
	x, y := h.rowPos(0, "a.txt")
	h.mouse(x, y, tea.MouseButtonRight, tea.MouseActionPress)
	p := h.a.panels[0]
	if !p.IsSelected("a.txt") || p.Entries()[p.Cursor()].Name != "a.txt" {
		t.Fatal("right-click must mark the row and keep the cursor on it")
	}
	h.mouse(x, y, tea.MouseButtonRight, tea.MouseActionPress)
	if p.IsSelected("a.txt") {
		t.Fatal("second right-click must unmark")
	}
}

func TestDialogButtonsByTapAreSafe(t *testing.T) {
	h, env, clock := p4App(t, nil)
	h.focus("a.txt")
	h.press("d")
	h.waitDialog(dlgDelete)
	// within the guard period after opening, taps do nothing
	h.tapButton("Yes")
	if !h.dialogKind(dlgDelete) {
		t.Fatal("a tap right after the dialog opened must be ignored")
	}
	*clock = clock.Add(time.Second)
	// press on one button, release on the other: nothing happens
	h.view()
	var yes, no dialogHit
	for _, hh := range h.a.dialog.hits {
		if hh.kind == hitButton {
			if strings.Contains(h.a.dialog.buttons[hh.index].label, "Yes") {
				yes = hh
			} else {
				no = hh
			}
		}
	}
	h.mouse(yes.x0+1, yes.y, tea.MouseButtonLeft, tea.MouseActionPress)
	h.mouse(no.x0+1, no.y, tea.MouseButtonLeft, tea.MouseActionRelease)
	if !h.dialogKind(dlgDelete) {
		t.Fatal("release on another button must not activate anything")
	}
	// tap outside the box: nothing
	h.tap(1, 1)
	if !h.dialogKind(dlgDelete) {
		t.Fatal("tap outside must not cancel or confirm")
	}
	h.tapButton("No, cancel")
	if h.a.dialog != nil {
		t.Fatal("tap on No must close the dialog")
	}
	if _, err := os.Stat(filepath.Join(env.src, "a.txt")); err != nil {
		t.Fatal("file deleted although cancelled")
	}
	// confirming by tap works like y
	h.press("d")
	h.waitDialog(dlgDelete)
	*clock = clock.Add(time.Second)
	h.tapButton("Yes")
	h.idle()
	h.settle()
	if _, err := os.Stat(filepath.Join(env.src, "a.txt")); !os.IsNotExist(err) {
		t.Fatal("tap on Yes did not delete")
	}
}

func TestHintBarTapsActAsKeys(t *testing.T) {
	h, env, clock := p4App(t, nil)
	h.focus("a.txt")
	h.tapHint("c")
	h.waitDialog(dlgConfirm)
	if !h.dialogKind(dlgConfirm) {
		t.Fatal("tapping 'c copy' must open the copy confirmation")
	}
	// the dialog's hint bar: Esc cancel, guarded right after opening
	h.tapHint("esc")
	if h.a.dialog == nil {
		t.Fatal("hint-bar tap right after the dialog opened must be ignored")
	}
	*clock = clock.Add(time.Second)
	h.tapHint("esc")
	if h.a.dialog != nil {
		t.Fatal("tapping 'Esc cancel' must cancel")
	}
	if _, err := os.Stat(filepath.Join(env.dst, "a.txt")); err == nil {
		t.Fatal("copied although cancelled")
	}
	h.tapHint("?")
	if !h.a.showHelp {
		t.Fatal("'? help' tap")
	}
	h.tap(50, 10) // a tap closes help
	if h.a.showHelp {
		t.Fatal("tap must close help")
	}
	h.send(tea.WindowSizeMsg{Width: 160, Height: 24})
	h.tapHint("h")
	if !h.a.showHidden {
		t.Fatal("'h' tap must toggle hidden files")
	}
}

func TestChooserListTap(t *testing.T) {
	h, env, clock := p4App(t, nil)
	h.focus("a.txt")
	h.press("E")
	if !h.dialogKind(dlgChooser) {
		t.Fatal("no chooser")
	}
	*clock = clock.Add(time.Second)
	h.view()
	d := h.a.dialog
	var target *dialogHit
	for i := range d.hits {
		if d.hits[i].kind == hitListItem && strings.Contains(d.list[d.hits[i].index].label, "nano") {
			target = &d.hits[i]
		}
	}
	if target == nil {
		t.Fatal("nano row not clickable")
	}
	if !strings.Contains(d.list[d.listCursor].tip, ":wq") {
		t.Fatalf("chooser tip for the default (Neovim): %q", d.list[d.listCursor].tip)
	}
	h.tap(target.x0+2, target.y)
	h.settle()
	if len(env.execs) != 1 || !strings.Contains(env.execs[0].args[0], "nano") {
		t.Fatalf("tap on nano: %+v", env.execs)
	}
}

func TestMouseIgnoredWhileEditingAndAfter(t *testing.T) {
	h, env, clock := p4App(t, nil)
	var cb tea.ExecCallback
	h.a.execFn = func(c tea.ExecCommand, fn tea.ExecCallback) tea.Cmd { cb = fn; return nil }
	h.focus("a.txt")
	h.press("e")
	x, y := h.rowPos(0, "sub")
	h.tap(x, y)
	h.tap(x, y)
	h.wheel(x, y, 3)
	if h.a.panels[0].Path() != env.src || h.a.panels[0].Offset() != 0 {
		t.Fatal("mouse input while editing must be dropped")
	}
	quietAfterEdit = 250 * time.Millisecond
	defer func() { quietAfterEdit = 0 }()
	h.send(cb(nil))
	h.settle()
	h.tap(x, y)
	h.tap(x, y)
	if h.a.panels[0].Path() != env.src {
		t.Fatal("mouse input in the quiet period after editing must be dropped")
	}
	*clock = clock.Add(time.Second)
	h.tap(x, y)
	*clock = clock.Add(100 * time.Millisecond)
	h.tap(x, y)
	if h.a.panels[0].Path() == env.src {
		t.Fatal("mouse must work again after the quiet period")
	}
}

func TestMouseReenabledAfterEditor(t *testing.T) {
	h, _, _ := p4App(t, nil)
	found := func(cmd tea.Cmd) bool {
		if cmd == nil {
			return false
		}
		msg := cmd()
		batch, ok := msg.(tea.BatchMsg)
		if !ok {
			batch = tea.BatchMsg{func() tea.Msg { return msg }}
		}
		for _, c := range batch {
			if c != nil && fmt.Sprintf("%T", c()) == "tea.enableMouseCellMotionMsg" {
				return true
			}
		}
		return false
	}
	h.a.editing = true
	if !found(h.a.editorDone(editorDoneMsg{paths: []string{"x"}, editor: "nvim"})) {
		t.Fatal("mouse reporting must be switched back on after the editor")
	}
	h.a.opts.Mouse = false
	if found(h.a.editorDone(editorDoneMsg{paths: []string{"x"}, editor: "nvim"})) {
		t.Fatal("with --no-mouse it must stay off")
	}
}

func TestSearchAndBulkByTouch(t *testing.T) {
	h, env, clock := p4App(t, nil)
	h.press("/")
	h.searchDone()
	h.typeText("f1", false)
	h.searchDone()
	top, _ := h.a.searchGeom()
	h.tap(10, top+2)
	sv := h.a.search
	if !sv.list || sv.cursor != 2 {
		t.Fatalf("tap on result 3: list=%v cursor=%d", sv.list, sv.cursor)
	}
	want := sv.entries[sv.matches[2]].Path
	*clock = clock.Add(100 * time.Millisecond)
	h.tap(10, top+2)
	h.settle()
	if h.a.search != nil {
		t.Fatal("double tap must go to the result")
	}
	p := h.a.ActivePanel()
	if e, _ := p.Current(); e.Path != want {
		t.Fatalf("went to %s, want %s", e.Path, want)
	}

	// bulk: tap selects a row, double tap edits its name, wheel scrolls
	*clock = clock.Add(time.Second)
	h.exec(p.ChangeDir(env.src))
	h.settle()
	h.press("a", "r")
	if h.a.bulk == nil {
		t.Fatal("no bulk rename")
	}
	h.tap(10, bulkTop+3)
	if h.a.bulk.cursor != 3 {
		t.Fatalf("bulk tap: cursor %d", h.a.bulk.cursor)
	}
	*clock = clock.Add(100 * time.Millisecond)
	h.tap(10, bulkTop+3)
	if !h.dialogKind(dlgBulkEdit) {
		t.Fatal("double tap must edit the row")
	}
	h.press("esc")
	h.wheel(10, bulkTop+1, 4)
	if h.a.bulk.offset != 4 {
		t.Fatalf("bulk wheel: offset %d", h.a.bulk.offset)
	}
	checkScreen(t, h.view(), 100, 24)
}

// ---- 4. hidden files: one setting for both panels -------------------------------------

func TestHiddenToggleBothPanels(t *testing.T) {
	h, env, _ := p4App(t, nil)
	for _, n := range []string{".a", ".b"} {
		os.WriteFile(filepath.Join(env.src, n), nil, 0o644)
	}
	h.exec(h.a.panels[1].ChangeDir(env.src))
	h.settle()
	h.exec(h.a.panels[0].Refresh())
	h.settle()
	h.focus("f10.txt")
	l, r := h.a.panels[0], h.a.panels[1]
	row := l.Cursor() - l.Offset()
	for i, k := range []string{"h", "H", "."} {
		h.press(k)
		want := i%2 == 0
		if h.a.showHidden != want || l.Options().ShowHidden != want || r.Options().ShowHidden != want {
			t.Fatalf("%s: global %v left %v right %v", k, h.a.showHidden, l.Options().ShowHidden, r.Options().ShowHidden)
		}
		if l.Entries()[l.Cursor()].Name != "f10.txt" || l.Cursor()-l.Offset() != row {
			t.Fatalf("%s: cursor on %q row %d, want f10.txt row %d", k, l.Entries()[l.Cursor()].Name, l.Cursor()-l.Offset(), row)
		}
		shown := func(p *panel.Panel) bool {
			for _, e := range p.Entries() {
				if e.Name == ".a" {
					return true
				}
			}
			return false
		}
		if shown(l) != want || shown(r) != want {
			t.Fatalf("%s: listings not refreshed (left %v right %v)", k, shown(l), shown(r))
		}
		st := h.status()
		if !strings.Contains(st, "both panels") || strings.Contains(st, "showing") != want {
			t.Fatalf("%s: status %q", k, st)
		}
		title := strings.SplitN(h.view(), "\n", 2)[0]
		if strings.Contains(title, "hidden: shown") != want {
			t.Fatalf("%s: title %q", k, title)
		}
	}
	// the search uses the same setting (h, H, . left it on)
	h.press("/")
	h.searchDone()
	if !h.a.search.hidden {
		t.Fatal("search must include hidden files when they are shown")
	}
	h.press("esc", "h")
	h.press("/")
	h.searchDone()
	if h.a.search.hidden {
		t.Fatal("search must skip hidden files when they are hidden")
	}
	h.press("esc")
	// a panel that changes directory keeps the global setting
	h.press("h")
	h.focus("sub")
	h.press("enter")
	h.settle()
	if !l.Options().ShowHidden {
		t.Fatal("setting lost on navigation")
	}
}

// ---- 5A. bulk rename guidance -------------------------------------------------------

func TestBulkRenameCommitGuidance(t *testing.T) {
	h, env, _ := p4App(t, nil)
	h.focus("a.txt")
	h.press(" ")
	h.focus("b.txt")
	h.press(" ")
	h.press("r")
	if v := h.view(); !strings.Contains(v, "No new names yet") || !strings.Contains(v, "Esc") {
		t.Fatalf("guidance without changes:\n%s", v)
	}
	h.press("U") // upper-case all: 2 changes
	v := h.view()
	for _, w := range []string{"Apply:", " a ", " then ", " y ", "to confirm", "Cancel:", " Esc ", "nothing is renamed until you confirm"} {
		if !strings.Contains(v, w) {
			t.Fatalf("guidance lacks %q:\n%s", w, v)
		}
	}
	if f := h.footer(); !strings.HasPrefix(strings.TrimSpace(f), "a  apply…  Esc  cancel") {
		t.Fatalf("hint bar must lead with apply and cancel: %q", f)
	}
	// a opens the confirmation; Enter there presses the default "No"
	h.press("a")
	if !h.dialogKind(dlgBulkConfirm) || !strings.Contains(h.view(), "y = rename now") {
		t.Fatalf("confirmation:\n%s", h.view())
	}
	if f := h.footer(); !strings.Contains(f, "y  rename") || !strings.Contains(f, "Esc  no") {
		t.Fatalf("confirmation hints: %q", f)
	}
	h.press("enter")
	if _, err := os.Stat(filepath.Join(env.src, "a.txt")); err != nil || h.a.bulk == nil {
		t.Fatal("Enter on the default No must not rename")
	}
	h.press("a", "y")
	h.until("renamed", func() bool { return h.a.bulk == nil })
	if _, err := os.Stat(filepath.Join(env.src, "A.TXT")); err != nil {
		t.Fatal("a then y must rename")
	}
	// Esc cancels without renaming
	h.exec(h.a.panels[0].Refresh())
	h.settle()
	h.focus("f01.txt")
	h.press(" ")
	h.focus("f00.txt")
	h.press(" ", "r", "U", "esc")
	if _, err := os.Stat(filepath.Join(env.src, "f00.txt")); err != nil || h.a.bulk != nil {
		t.Fatal("Esc must cancel without renaming")
	}
}

// ---- 5B. editor instructions --------------------------------------------------------

func TestEditorTipCard(t *testing.T) {
	h, env, _ := p4App(t, nil)
	h.a.opts.EditorTips = true
	h.focus("a.txt")
	h.press("e")
	if !h.dialogKind(dlgEditTip) || len(env.execs) != 0 {
		t.Fatal("the first launch shows the save/quit card first")
	}
	v := h.view()
	for _, w := range []string{"Open a.txt in Neovim?", "Esc, then :w save", ":q quit", ":wq save & quit", "starts when you choose Open"} {
		if !strings.Contains(v, w) {
			t.Fatalf("card lacks %q:\n%s", w, v)
		}
	}
	// Esc cancels: no editor
	h.press("esc")
	if h.a.dialog != nil || len(env.execs) != 0 {
		t.Fatal("Esc must cancel the launch")
	}
	// Phase 7: there is no timer; the card waits for a decision
	h.press("e")
	if !h.dialogKind(dlgEditTip) {
		t.Fatal("card expected")
	}
	h.settle()
	if len(env.execs) != 0 || !h.dialogKind(dlgEditTip) {
		t.Fatal("the card must wait for Enter")
	}
	h.press("enter")
	h.settle()
	if len(env.execs) != 1 {
		t.Fatalf("Enter did not launch: %d", len(env.execs))
	}
	// nothing saved: the status says how to save, and the card returns
	if st := h.status(); !strings.Contains(st, "nothing saved") || !strings.Contains(st, "Esc, then :w") {
		t.Fatalf("status %q", st)
	}
	h.press("e")
	if !h.dialogKind(dlgEditTip) {
		t.Fatal("card must return after an edit that saved nothing")
	}
	// Enter starts at once; this time the file is saved
	env.editFn = func(args []string) { os.WriteFile(args[len(args)-1], []byte("changed!"), 0o644) }
	h.press("enter")
	h.settle()
	if len(env.execs) != 2 || !strings.Contains(h.reported(), "— saved") {
		t.Fatalf("execs %d report %q", len(env.execs), h.reported())
	}
	// saved once: later launches go straight to the editor
	h.press("e")
	h.settle()
	if h.a.dialog != nil || len(env.execs) != 3 {
		t.Fatal("no card once the user has saved")
	}
}

func TestEditorTipOtherEditors(t *testing.T) {
	for _, c := range []struct{ editor, want, reject string }{
		{"nano", "Ctrl+O then Enter save", ":wq"},
		{"micro", "Ctrl+S save", ":wq"},
		{"myed", "own help", ":wq"},
	} {
		ed := c.editor
		env := &editor.Env{Getenv: func(k string) string {
			if k == "EDITOR" {
				return ed
			}
			return ""
		}, LookPath: func(n string) (string, error) { return "/opt/" + n, nil }, GOOS: "android"}
		h, _, _ := p4App(t, env)
		h.a.opts.EditorTips = true
		h.focus("a.txt")
		h.press("e")
		v := h.view()
		if !h.dialogKind(dlgEditTip) || !strings.Contains(v, c.want) || strings.Contains(v, c.reject) {
			t.Fatalf("%s card:\n%s", ed, v)
		}
		if !strings.Contains(v, "Termux") {
			t.Fatalf("%s: Android card should mention the Termux extra keys", ed)
		}
		h.press("esc")
	}
}

func TestSavedDetection(t *testing.T) {
	h, env, _ := p4App(t, nil)
	h.focus("a.txt")
	h.press(" ")
	h.focus("b.txt")
	h.press(" ")
	env.editFn = func(args []string) { os.WriteFile(filepath.Join(env.src, "a.txt"), []byte("other content"), 0o644) }
	h.press("e")
	h.settle()
	if st := h.reported(); !strings.Contains(st, "Finished editing 2 files — 1 of 2 saved") {
		t.Fatalf("report %q", st)
	}
	h.press("u")
	h.focus("b.txt")
	env.editFn = func(args []string) { os.WriteFile(filepath.Join(env.src, "b.txt"), []byte("other content"), 0o644) }
	h.press("e")
	h.settle()
	if st := h.reported(); !strings.HasPrefix(st, "Finished editing b.txt — saved") {
		t.Fatalf("report %q", st)
	}
}
