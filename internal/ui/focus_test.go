package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
	"github.com/ThatOn3Gu7/TwinPane/internal/panel"
)

// ---- helpers --------------------------------------------------------------------

// focusOn toggles focus mode the way the hint chip and the menu do (the
// synthetic key), so the key path is covered too.
func focusOn(t *testing.T, a *App) {
	t.Helper()
	if a.FocusMode() {
		return
	}
	_, cmd := a.Update(tea.KeyMsg{Type: keyFocusMode})
	drain(a, cmd)
	if !a.FocusMode() {
		t.Fatal("focus mode did not turn on")
	}
}

// toggleFocusKey sends the synthetic key the hint chip and menu send; it
// flips the mode (focusOn only ever turns it on).
func toggleFocusKey(t *testing.T, a *App) {
	t.Helper()
	was := a.FocusMode()
	_, cmd := a.Update(tea.KeyMsg{Type: keyFocusMode})
	drain(a, cmd)
	if a.FocusMode() == was {
		t.Fatalf("focus mode did not toggle (still %v)", was)
	}
}

// csiMsg stands in for bubbletea's unknownCSISequenceMsg, which is
// unexported but only needs a String method.
type csiMsg string

func (c csiMsg) String() string { return string(c) }

// panelLineWidth is the rendered width of a panel (all rows are equal).
func panelLineWidth(a *App, i int) int {
	lines := strings.Split(a.Panel(i).View(a.theme, i == a.Active()), "\n")
	return ansi.StringWidth(lines[0])
}

func dialogText(d *dialog) string {
	var b strings.Builder
	for _, ln := range d.body {
		b.WriteString(ln.text)
		b.WriteString("\n")
	}
	return b.String()
}

func entryOf(t *testing.T, a *App, i int) fsys.Entry {
	t.Helper()
	e, ok := a.Panel(i).Current()
	if !ok {
		t.Fatalf("panel %d has no entry under the cursor", i)
	}
	return e
}

// ---- toggling and layout ----------------------------------------------------------

func TestFocusModeKeepsBothPanelsIntact(t *testing.T) {
	a, home := setup(t)
	docs, music := filepath.Join(home, "docs"), filepath.Join(home, "music")
	drain(a, a.panels[0].Load(docs, "", panel.LoadNavigate))
	drain(a, a.panels[1].Load(music, "", panel.LoadNavigate))

	focusOn(t, a)
	if a.width != 100 || a.height != 30 {
		t.Fatalf("unexpected screen size %dx%d", a.width, a.height)
	}
	// The active panel fills the width minus the preview pane; the hidden
	// panel keeps the size it had in two-pane mode.
	if got, want := panelLineWidth(a, 0), a.focusPanelWidth(); got != want {
		t.Fatalf("focused panel width = %d, want %d", got, want)
	}
	if got := panelLineWidth(a, 1); got != a.width/2 {
		t.Fatalf("hidden panel width = %d, want %d (it must not be resized)", got, a.width/2)
	}
	if a.Panel(1).Path() != music || a.Panel(0).Path() != docs {
		t.Fatalf("focus mode moved a panel: %s / %s", a.Panel(0).Path(), a.Panel(1).Path())
	}

	// Tab switches which panel is drawn, nothing else.
	press(a, "tab")
	if a.Active() != 1 {
		t.Fatal("Tab did not switch panels in focus mode")
	}
	if got, want := panelLineWidth(a, 1), a.focusPanelWidth(); got != want {
		t.Fatalf("right panel width after Tab = %d, want %d", got, want)
	}
	if a.Panel(0).Path() != docs {
		t.Fatalf("left panel moved while hidden: %s", a.Panel(0).Path())
	}

	// Back to two panes: half the width each, same folders.
	press(a, "tab")
	toggleFocusKey(t, a)
	if a.FocusMode() {
		t.Fatal("second toggle did not leave focus mode")
	}
	for i := 0; i < 2; i++ {
		if got := panelLineWidth(a, i); got != a.width/2 {
			t.Fatalf("panel %d width after leaving focus mode = %d, want %d", i, got, a.width/2)
		}
	}
	if a.Panel(0).Path() != docs || a.Panel(1).Path() != music {
		t.Fatalf("folders changed: %s / %s", a.Panel(0).Path(), a.Panel(1).Path())
	}
}

func TestFocusModeKeepsCursorAndHistory(t *testing.T) {
	a, home := setup(t)
	docs := filepath.Join(home, "docs")
	drain(a, a.panels[0].Load(docs, "", panel.LoadNavigate))
	drain(a, a.panels[0].RefreshFocus("sub"))
	cur, off := a.Panel(0).Cursor(), a.Panel(0).Offset()
	back, _ := a.Panel(0).NavDepth()

	focusOn(t, a)
	if a.Panel(0).Cursor() != cur || a.Panel(0).Offset() != off {
		t.Fatalf("focus mode moved the cursor (%d/%d → %d/%d)", cur, off, a.Panel(0).Cursor(), a.Panel(0).Offset())
	}
	if now, _ := a.Panel(0).NavDepth(); now != back || back == 0 {
		t.Fatalf("history changed: %d → %d", back, now)
	}
	// back/forward keep working with one panel visible
	press(a, "backspace")
	if got := a.Panel(0).Path(); got != home {
		t.Fatalf("Backspace in focus mode went to %s", got)
	}
}

func TestFocusPreviewThresholds(t *testing.T) {
	cases := []struct {
		w, h           int
		avail          bool
		pane, listWant int
	}{
		{100, 30, true, 38, 62},
		{99, 40, false, 0, 99},
		{120, 17, false, 0, 120},
		{100, 18, true, 38, 62},
		{150, 40, true, 57, 93},
		{300, 60, true, 64, 236}, // the pane is capped
	}
	for _, c := range cases {
		a, _ := setup(t)
		a.Update(tea.WindowSizeMsg{Width: c.w, Height: c.h})
		focusOn(t, a)
		if got := a.focusPreviewAvail(); got != c.avail {
			t.Fatalf("%dx%d: avail = %v, want %v", c.w, c.h, got, c.avail)
		}
		if got := a.focusPreviewWidth(); got != c.pane {
			t.Fatalf("%dx%d: preview width = %d, want %d", c.w, c.h, got, c.pane)
		}
		if got := a.focusPanelWidth(); got != c.listWant {
			t.Fatalf("%dx%d: list width = %d, want %d", c.w, c.h, got, c.listWant)
		}
	}
}

func TestFocusPreviewToggle(t *testing.T) {
	a, _ := setup(t)
	focusOn(t, a)
	if !a.FocusPreview() {
		t.Fatal("preview should be on by default in focus mode")
	}
	press(a, "p")
	if a.FocusPreview() {
		t.Fatal("p did not hide the preview pane")
	}
	if a.focusPanelWidth() != a.width {
		t.Fatalf("panel width with the preview off = %d, want %d", a.focusPanelWidth(), a.width)
	}
	press(a, "p")
	if !a.FocusPreview() {
		t.Fatal("p did not show the preview pane again")
	}
	// p means nothing in two-pane mode: no mode change, no dialog.
	toggleFocusKey(t, a)
	press(a, "p")
	if a.FocusMode() || a.dialog != nil {
		t.Fatal("p did something outside focus mode")
	}
}

func TestFocusModeWithoutRoomForPreview(t *testing.T) {
	a, _ := setup(t)
	a.Update(tea.WindowSizeMsg{Width: 84, Height: 30})
	focusOn(t, a)
	if a.focusPreviewWidth() != 0 {
		t.Fatal("narrow screen should have no preview pane")
	}
	if got := panelLineWidth(a, 0); got != 84 {
		t.Fatalf("narrow focus panel width = %d, want 84", got)
	}
	press(a, "p") // must explain, not crash
	if a.statusLevel != lvlWarn {
		t.Fatalf("status level = %v, want a warning", a.statusLevel)
	}
	if !a.FocusMode() {
		t.Fatal("a failed preview toggle left focus mode")
	}
}

// ---- the Ctrl+Shift+F sequence ----------------------------------------------------

func TestCtrlShiftFSequenceDecoding(t *testing.T) {
	good := []string{"27;6;102~", "27;6;70~", "102;6u", "70;6u", "102;6;1u", "70;6;1u"}
	for _, p := range good {
		if !ctrlShiftF(p) {
			t.Fatalf("%q should be Ctrl+Shift+F", p)
		}
	}
	bad := []string{"", "27;5;102~", "27;6;99~", "102;5u", "102;6;3u", "102;6;0u",
		"<27;6;102~", "?27;6;102~", "27;6;102", "102", "x;y;z~"}
	for _, p := range bad {
		if ctrlShiftF(p) {
			t.Fatalf("%q must not toggle focus mode", p)
		}
	}
	// Whole messages: only the "?CSI[...]?" form bubbletea uses for
	// sequences it does not recognise; a key never matches.
	if !focusSequence(csiMsg("?CSI[27;6;102~]?")) {
		t.Fatal("xterm modifyOtherKeys Ctrl+Shift+F not recognised")
	}
	if !focusSequence(csiMsg("?CSI[102;6u]?")) {
		t.Fatal("kitty keyboard Ctrl+Shift+F not recognised")
	}
	if focusSequence(csiMsg("?CSI[27;5;102~]?")) {
		t.Fatal("Ctrl+F must stay page down")
	}
	if focusSequence(tea.KeyMsg{Type: tea.KeyCtrlF}) {
		t.Fatal("a plain key was treated as the sequence")
	}
	if focusSequence(nil) {
		t.Fatal("nil message must not toggle focus mode")
	}
}

func TestFocusSequenceIgnoredWhileOverlayOwnsKeyboard(t *testing.T) {
	a, _ := setup(t)
	a.showHelp = true
	a.Update(csiMsg("?CSI[102;6u]?"))
	if a.FocusMode() {
		t.Fatal("focus mode toggled while help was open")
	}
	a.showHelp = false
	a.Update(csiMsg("?CSI[102;6u]?"))
	if !a.FocusMode() {
		t.Fatal("focus mode did not toggle after help closed")
	}
	// A dialog swallows the sequence too (it is not a KeyMsg, so it can
	// never press a button).
	a.dialog = &dialog{kind: dlgConfirm, title: "x"}
	a.Update(csiMsg("?CSI[102;6u]?"))
	if !a.FocusMode() {
		t.Fatal("a dialog changed focus mode")
	}
	a.dialog = nil
}

// ---- preview pane -----------------------------------------------------------------

func TestFocusPreviewShowsFileText(t *testing.T) {
	a, home := setup(t)
	body := strings.Repeat("line of text\n", 40)
	if err := os.WriteFile(filepath.Join(home, "notes.txt"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	drain(a, a.panels[0].RefreshFocus("notes.txt"))
	focusOn(t, a)
	pane := a.focusView(a.height - 3)
	if !strings.Contains(pane, "line of text") {
		t.Fatal("preview pane did not show the file text")
	}
	if a.focusPrev.title != "notes.txt" {
		t.Fatalf("preview title = %q", a.focusPrev.title)
	}
}

func TestFocusPreviewCachesPerSelection(t *testing.T) {
	a, home := setup(t)
	f := filepath.Join(home, "notes.txt")
	if err := os.WriteFile(f, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	drain(a, a.panels[0].RefreshFocus("notes.txt"))
	focusOn(t, a)
	pv1 := a.previewFor(a.Panel(0), entryOf(t, a, 0))
	if !strings.Contains(strings.Join(pv1.lines, ""), "first") {
		t.Fatalf("preview missing content: %q", strings.Join(pv1.lines, ""))
	}
	if err := os.WriteFile(f, []byte("second"), 0o644); err != nil {
		t.Fatal(err)
	}
	drain(a, a.panels[0].RefreshFocus("notes.txt")) // new size/time reaches the panel
	pv2 := a.previewFor(a.Panel(0), entryOf(t, a, 0))
	if strings.Join(pv2.lines, "") == strings.Join(pv1.lines, "") {
		t.Fatal("preview was not rebuilt after the file changed")
	}
	if !strings.Contains(strings.Join(pv2.lines, ""), "second") {
		t.Fatalf("preview has the old content: %q", strings.Join(pv2.lines, ""))
	}
}

func TestFocusPreviewSkipsBinaryAndHugeFiles(t *testing.T) {
	a, home := setup(t)
	bin := filepath.Join(home, "blob.bin")
	if err := os.WriteFile(bin, []byte{0, 1, 2, 3, 0, 5}, 0o644); err != nil {
		t.Fatal(err)
	}
	lines := strings.Join(a.previewTextLines(fsys.Entry{Path: bin, Name: "blob.bin", Size: 6}), "")
	if !strings.Contains(lines, "Binary") {
		t.Fatalf("binary file preview = %q", lines)
	}
	big := strings.Join(a.previewTextLines(fsys.Entry{Path: bin, Name: "big", Size: 9 << 20}), "")
	if !strings.Contains(big, "Too large") {
		t.Fatalf("oversized file preview = %q", big)
	}
}

func TestFocusPreviewBoundsText(t *testing.T) {
	a, home := setup(t)
	f := filepath.Join(home, "long.txt")
	var b strings.Builder
	for i := 0; i < 2000; i++ {
		b.WriteString("0123456789\n")
	}
	if err := os.WriteFile(f, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	lines := a.previewTextLines(fsys.Entry{Path: f, Name: "long.txt", Size: int64(b.Len())})
	if len(lines) > focusPreviewMaxLines+1 {
		t.Fatalf("preview returned %d lines, cap is %d", len(lines), focusPreviewMaxLines)
	}
	if !strings.Contains(strings.Join(lines, ""), "shown") {
		t.Fatal("truncation was not reported")
	}
}

// ---- mouse -------------------------------------------------------------------------

func TestFocusMouseMapsEveryColumnToTheActivePanel(t *testing.T) {
	h, env, _ := p4App(t, nil)
	focusOn(t, h.a)
	h.settle()
	before := h.a.Panel(1).Cursor()
	// A tap well past the middle of the screen: in two-pane mode that is
	// the right panel, in focus mode it is the visible left panel.
	x := h.a.width/2 + 4
	if h.a.focusPreviewAt(x, 5) {
		t.Fatalf("test point %d is inside the preview pane", x)
	}
	h.tap(x, 5)
	if h.a.Active() != 0 {
		t.Fatalf("tap at column %d switched to panel %d", x, h.a.Active())
	}
	if h.a.Panel(1).Cursor() != before {
		t.Fatal("the hidden panel moved")
	}
	if h.a.Panel(0).Cursor() == 0 {
		t.Fatal("the tap did not move the visible panel's cursor")
	}
	if h.a.Panel(0).Path() != env.src {
		t.Fatalf("visible panel left %s for %s", env.src, h.a.Panel(0).Path())
	}
}

func TestFocusPreviewWheelScrollsThePane(t *testing.T) {
	h, env, _ := p4App(t, nil)
	long := filepath.Join(env.src, "long.txt")
	var b strings.Builder
	for i := 0; i < 300; i++ {
		b.WriteString("row\n")
	}
	if err := os.WriteFile(long, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	h.exec(h.a.panels[0].RefreshFocus("long.txt"))
	h.settle()
	focusOn(t, h.a)
	h.settle()
	h.a.View() // build the preview
	if h.a.focusPrevScroll != 0 {
		t.Fatal("preview started scrolled")
	}
	cur := h.a.Panel(0).Cursor()
	x := h.a.width - 2 // inside the preview pane
	h.wheel(x, 5, 3)
	if h.a.focusPrevScroll == 0 {
		t.Fatal("wheel over the preview pane did not scroll it")
	}
	if h.a.Panel(0).Cursor() != cur {
		t.Fatal("wheel over the preview pane moved the list cursor")
	}
	// The list itself still scrolls with the wheel once the cursor is far
	// enough down for the list to have somewhere to scroll (the wheel
	// scrolls the list, it never moves the cursor).
	press(h.a, "G") // last entry: the list is scrolled to the end
	off := h.a.Panel(0).Offset()
	if off == 0 {
		t.Fatal("the list did not scroll")
	}
	prevScroll := h.a.focusPrevScroll
	h.wheel(2, 5, -3) // wheel up
	if h.a.Panel(0).Offset() == off {
		t.Fatalf("wheel over the list did not scroll it (offset %d)", off)
	}
	if h.a.focusPrevScroll != prevScroll {
		t.Fatal("wheel over the list scrolled the preview pane")
	}
}

// ---- hints, menu and help -----------------------------------------------------------

func TestFocusHintsAndMenu(t *testing.T) {
	a, _ := setup(t)
	two := tickLabels(a)
	if !strings.Contains(two, "^⇧F") {
		t.Fatalf("two-pane hints do not offer focus mode: %q", two)
	}
	focusOn(t, a)
	focused := tickLabels(a)
	if !strings.Contains(focused, "p") || !strings.Contains(focused, "preview") {
		t.Fatalf("focus hints lack the preview toggle: %q", focused)
	}
	if !strings.Contains(focused, "^G") {
		t.Fatalf("focus hints lack go to path: %q", focused)
	}
	var labels []string
	for _, e := range a.focusMenuEntries() {
		labels = append(labels, e.label)
	}
	joined := strings.Join(labels, " | ")
	for _, want := range []string{"Two panes", "Preview pane", "Go to path"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("menu lacks %q: %s", want, joined)
		}
	}
	// The toggle entry works through the menu, and none of them opens a
	// dialog (the go-to entry re-sends its key, so it is not run here).
	for _, e := range a.focusMenuEntries() {
		if e.label != "Two panes" {
			continue
		}
		drain(a, e.run())
	}
	if a.FocusMode() {
		t.Fatal("the menu's Two panes entry did not leave focus mode")
	}
	if a.dialog != nil {
		t.Fatal("a menu entry opened a dialog")
	}
}

func TestFocusModeInHelp(t *testing.T) {
	a, _ := setup(t)
	help := ""
	for _, cols := range [][2][]helpSection{a.helpColumns()} {
		for _, col := range cols {
			for _, sec := range col {
				if sec.Title != "Focus mode" {
					continue
				}
				for _, b := range sec.Bindings {
					help += b.Help + " " + b.Desc + "\n"
				}
			}
		}
	}
	for _, want := range []string{"focus mode", "preview", "go to a local folder", "copy / move"} {
		if !strings.Contains(strings.ToLower(help), want) {
			t.Fatalf("help lacks %q:\n%s", want, help)
		}
	}
}

func tickLabels(a *App) string {
	items, _ := a.hints()
	var b strings.Builder
	for _, it := range items {
		b.WriteString(it.key)
		b.WriteString(" ")
		b.WriteString(it.desc)
		b.WriteString("  ")
	}
	return b.String()
}

// ---- copy / move destination chooser ------------------------------------------------

func TestFocusDestChooserDefaultsToTheHiddenPanel(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	a := h.a
	h.focus("a.txt")
	focusOn(t, a)
	h.press("c")
	if !h.dialogKind(dlgDest) {
		t.Fatalf("copy in focus mode opened %v, want the destination chooser", a.dialog)
	}
	items := a.dialog.list
	if len(items) < 2 {
		t.Fatalf("chooser has %d entries: %+v", len(items), items)
	}
	if items[0].detail != "other panel" {
		t.Fatalf("first choice is %q, want the hidden panel", items[0].detail)
	}
	if !strings.Contains(items[0].label, filepath.Base(env.dst)) {
		t.Fatalf("first choice = %q, want %s", items[0].label, env.dst)
	}
	// Picking it leads to the normal confirmation for that folder.
	h.press("enter")
	if !h.dialogKind(dlgConfirm) {
		t.Fatalf("choosing a destination opened %v, want the confirmation", a.dialog)
	}
	if body := dialogText(a.dialog); !strings.Contains(body, env.dst) {
		t.Fatalf("confirmation does not name the destination:\n%s", body)
	}
	h.press("enter") // confirm
	if !fileExists(filepath.Join(env.dst, "a.txt")) {
		t.Fatalf("copy in focus mode did not write %s", filepath.Join(env.dst, "a.txt"))
	}
	if len(a.recents) == 0 || a.recents[len(a.recents)-1].path != env.dst {
		t.Fatalf("destination not remembered: %+v", a.recents)
	}
}

func TestFocusDestChooserOffersRecents(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	a := h.a
	h.focus("a.txt")
	focusOn(t, a)
	h.press("c")
	h.press("enter") // other panel -> dst
	h.press("enter") // confirm
	if !fileExists(filepath.Join(env.dst, "a.txt")) {
		t.Fatal("first copy failed")
	}
	// Move the hidden panel elsewhere: the folder used before stays a choice.
	h.exec(a.panels[1].Load(filepath.Join(env.src, "sub"), "", panel.LoadNavigate))
	h.settle()
	h.focus("b.txt")
	h.press("c")
	if !h.dialogKind(dlgDest) {
		t.Fatal("second copy did not open the chooser")
	}
	var labels []string
	for _, it := range a.dialog.list {
		labels = append(labels, it.label+" ("+it.detail+")")
	}
	joined := strings.Join(labels, " | ")
	if !strings.Contains(joined, "recent") {
		t.Fatalf("the earlier destination is not offered: %s", joined)
	}
	if !strings.Contains(joined, filepath.Base(env.dst)) {
		t.Fatalf("the earlier destination is missing: %s", joined)
	}
	// Pick the recorded destination, not the hidden panel.
	for i, it := range a.dialog.list {
		if it.detail != "recent" {
			continue
		}
		a.dialog.listCursor = i
		break
	}
	h.press("enter")
	h.press("enter")
	if !fileExists(filepath.Join(env.dst, "b.txt")) {
		t.Fatalf("copy to a remembered destination failed: %v", a.recents)
	}
	if fileExists(filepath.Join(env.src, "sub", "b.txt")) {
		t.Fatal("the file landed in the hidden panel instead")
	}
}

func TestFocusDestChooserCancelLeavesNothingBehind(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	a := h.a
	h.focus("a.txt")
	focusOn(t, a)
	h.press("c")
	h.press("esc")
	if a.dialog != nil {
		t.Fatal("Esc did not close the chooser")
	}
	if a.tasks.activeCount() != 0 {
		t.Fatal("cancelling the chooser started a task")
	}
	if fileExists(filepath.Join(env.dst, "a.txt")) {
		t.Fatal("cancelling the chooser copied the file")
	}
	if s, _ := a.Status(); s == "" {
		t.Fatal("cancelling the chooser did not report it")
	}
	// Cancelling the confirmation must not record the destination either.
	h.press("c")
	h.press("enter") // pick the hidden panel
	h.press("esc")   // cancel at the confirmation
	if len(a.recents) != 0 {
		t.Fatalf("a cancelled transfer was remembered as a destination: %+v", a.recents)
	}
}

func TestFocusDestChooserMissingFolder(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	a := h.a
	h.focus("a.txt")
	focusOn(t, a)
	gone := filepath.Join(env.root, "gone")
	cmd := a.beginTransferTo(ops.Copy, 0, gone)
	if cmd != nil {
		t.Fatal("a missing destination must not start a task")
	}
	if a.dialog != nil {
		t.Fatal("a missing destination must not open the confirmation")
	}
	if s, _ := a.Status(); !strings.Contains(s, "not a folder") && !strings.Contains(s, "no such") {
		t.Fatalf("status = %q", s)
	}
}

func TestDestChoicesSkipSourceAndDedupe(t *testing.T) {
	a, home := setup(t)
	drain(a, a.panels[0].Load(home, "", panel.LoadNavigate))
	drain(a, a.panels[1].Load(home, "", panel.LoadNavigate))
	for _, c := range a.destChoices(0) {
		if c.path == home {
			t.Fatalf("the source folder was offered as a destination: %+v", c)
		}
	}
	dst := filepath.Join(home, "docs")
	a.noteDest(dst, "recent")
	a.noteDest(filepath.Join(home, "music"), "recent")
	a.noteDest(dst, "other panel") // moves to the front, no duplicate
	seen := map[string]int{}
	for _, r := range a.recents {
		seen[r.path]++
	}
	if seen[dst] != 1 {
		t.Fatalf("destination recorded twice: %+v", a.recents)
	}
	if got := a.recentDests()[0]; got.path != dst || got.label != "other panel" {
		t.Fatalf("re-used destination was not refreshed: %+v", got)
	}
	for i := 0; i < 20; i++ {
		a.noteDest(filepath.Join(home, "d", string(rune('a'+i))), "recent")
	}
	if len(a.recents) > maxRecents {
		t.Fatalf("%d destinations kept, cap is %d", len(a.recents), maxRecents)
	}
}

// ---- wide panel rendering -----------------------------------------------------------

func TestFocusPanelShowsTypeColumnAndPath(t *testing.T) {
	a, home := setup(t)
	if err := os.WriteFile(filepath.Join(home, "notes.txt"), []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	drain(a, a.panels[0].Load(filepath.Join(home, "docs"), "", panel.LoadNavigate))
	drain(a, a.panels[0].RefreshFocus("sub"))
	focusOn(t, a)
	view := ansi.Strip(a.Panel(0).View(a.theme, true))
	head := ""
	for _, ln := range strings.Split(view, "\n") {
		if strings.Contains(ln, "Name") {
			head = ln
			break
		}
	}
	if !strings.Contains(head, "Type") {
		t.Fatalf("wide panel header lacks the type column (%q):\n%s", head, view)
	}
	for _, want := range []string{"docs", "sub"} {
		if !strings.Contains(view, want) {
			t.Fatalf("wide panel lacks %q:\n%s", want, view)
		}
	}
	if !strings.Contains(view, " › ") {
		t.Fatalf("wide panel lacks the breadcrumb separator:\n%s", view)
	}
	// Two-pane mode keeps the compact columns.
	narrow := ""
	for _, ln := range strings.Split(ansi.Strip(a.Panel(1).View(a.theme, false)), "\n") {
		if strings.Contains(ln, "Name") {
			narrow = ln
			break
		}
	}
	if narrow == "" || strings.Contains(narrow, "Type") {
		t.Fatalf("the two-pane panel header = %q", narrow)
	}
}
