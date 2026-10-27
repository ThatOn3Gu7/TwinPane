package ui

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/editor"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
)

func names(p interface{ Entries() []fsys.Entry }) []string {
	var out []string
	for _, e := range p.Entries() {
		out = append(out, e.Name)
	}
	return out
}

// ---- editor launch and input safety ------------------------------------------

func TestTypeaheadAfterEditDoesNotQuitOrRelaunch(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.focus("a.txt")
	// "e:wq" arriving in one read (typed ahead before the editor started)
	h.typeText("e:wq", false)
	if h.quit || h.a.quitting || len(env.execs) != 1 {
		t.Fatalf("quit=%v execs=%d", h.quit, len(env.execs))
	}
	// "ee": one editor only
	h.typeText("ee", false)
	if len(env.execs) != 2 {
		t.Fatalf("ee launched %d editors", len(env.execs)-1)
	}
	// escape sequences mis-parsed as alt+runes, and pastes, are dropped
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(":wq"), Alt: true})
	h.typeText("qd", true)
	if h.quit || h.a.dialog != nil {
		t.Fatal("alt burst / paste must be ignored")
	}
	// a late terminal reply ("?62;22c" from a DA1 query) can at most toggle
	// help: the 'c' (copy) must never run
	h.typeText("?62;22c", false)
	if h.a.dialog != nil || h.a.busy() {
		t.Fatal("terminal reply triggered an operation")
	}
	// movement keys still repeat
	h.press("esc")
	h.a.showHelp = false
	h.ActivePanelTop()
	h.typeText("jjj", false)
	if h.a.ActivePanel().Cursor() != 3 {
		t.Fatalf("cursor %d after jjj", h.a.ActivePanel().Cursor())
	}
}

func (h *harness) ActivePanelTop() { h.a.ActivePanel().Top() }

func TestKeysIgnoredWhileEditingAndShortlyAfter(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	var cb tea.ExecCallback
	h.a.execFn = func(c tea.ExecCommand, fn tea.ExecCallback) tea.Cmd { cb = fn; return nil } // editor "running"
	h.focus("a.txt")
	h.press("e")
	if !h.a.editing || cb == nil {
		t.Fatal("editor should be running")
	}
	h.press("q", "e", "d", "c", "r")
	if h.quit || h.a.quitting || h.a.dialog != nil || h.a.busy() {
		t.Fatal("keys while editing must be dropped")
	}
	clock := time.Now()
	h.a.now = func() time.Time { return clock }
	quietAfterEdit = 250 * time.Millisecond
	h.send(cb(nil))
	h.settle()
	if h.a.editing || !strings.HasPrefix(h.status(), "Finished editing a.txt") {
		t.Fatalf("editing=%v status=%q", h.a.editing, h.status())
	}
	h.press("d") // within the quiet period: dropped
	if h.a.dialog != nil {
		t.Fatal("key in the quiet period after the editor must be dropped")
	}
	clock = clock.Add(time.Second)
	h.press("d")
	if !h.dialogKind(dlgDelete) {
		t.Fatal("keys must work again after the quiet period")
	}
}

func TestEditorExitMessages(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.focus("a.txt")
	env.execErr = &editor.ExitError{Signal: syscall.SIGKILL}
	h.press("e")
	h.settle()
	if !h.a.statusErr || !strings.Contains(h.status(), "was terminated (killed): changes may not have been saved") {
		t.Fatalf("%q", h.status())
	}
	env.execErr = &editor.ExitError{Code: 1}
	h.press("e")
	h.settle()
	if !strings.Contains(h.status(), "exited with status 1: changes may not have been saved") {
		t.Fatalf("%q", h.status())
	}
	// resumed after a suspend: success, with an explanation
	env.execErr = nil
	h.a.editorDone(editorDoneMsg{paths: []string{"/x/a.txt"}, editor: "nvim", resumed: 1})
	if h.a.statusLevel != lvlWarn || !strings.Contains(h.status(), "suspended and has been resumed") {
		t.Fatalf("%q", h.status())
	}
}

func TestEditTargetsSelection(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	p := h.a.ActivePanel()
	h.focus("a.txt")
	h.press("space") // selects a.txt, cursor moves to b.txt
	h.focus("b.txt")
	h.press("space")
	h.focus("sub") // cursor elsewhere: the selection wins
	h.press("e")
	h.settle()
	want := "/bin/nvim " + filepath.Join(env.src, "a.txt") + " " + filepath.Join(env.src, "b.txt")
	if len(env.execs) != 1 || strings.Join(env.execs[0].args, " ") != want {
		t.Fatalf("execs %+v, want %q", env.execs, want)
	}
	if !strings.HasPrefix(h.status(), "Finished editing 2 files") {
		t.Fatalf("%q", h.status())
	}
	// a folder in the selection: refused, nothing launched
	h.focus("sub")
	h.press("space")
	h.press("e")
	if !h.a.statusErr || !strings.Contains(h.status(), "the selection contains the folder sub") || len(env.execs) != 1 {
		t.Fatalf("%q", h.status())
	}
	_ = p
}

// ---- targeting -------------------------------------------------------------------

func TestRenameTargetsSelectionOrFocus(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	// no selection: the focused item
	h.focus("b.txt")
	h.press("r")
	if !h.dialogKind(dlgRename) || !strings.Contains(h.view(), "Current name: b.txt") {
		t.Fatalf("focused rename:\n%s", h.view())
	}
	h.press("esc")
	// exactly one selected item that is not under the cursor: that item
	h.focus("a.txt")
	h.press("space")
	h.focus("sub")
	h.press("r")
	if !h.dialogKind(dlgRename) || !strings.Contains(h.view(), "Current name: a.txt") {
		t.Fatalf("selected rename:\n%s", h.view())
	}
	h.press("ctrl+u", "ctrl+k")
	h.typeText("z.txt", false)
	h.press("enter")
	h.settle()
	if !fileExists(filepath.Join(env.src, "z.txt")) || fileExists(filepath.Join(env.src, "a.txt")) {
		t.Fatal("selected item not renamed")
	}
	if e, _ := h.a.ActivePanel().Current(); e.Name != "sub" {
		t.Fatalf("cursor should stay on sub, is on %s", e.Name)
	}
	// the same targeting in the right panel
	os.WriteFile(filepath.Join(env.dst, "r1"), nil, 0o644)
	os.WriteFile(filepath.Join(env.dst, "r2"), nil, 0o644)
	h.press("tab", "ctrl+l")
	h.settle()
	h.press("a") // select all
	h.press("r")
	if h.a.bulk == nil || len(h.a.bulk.items) != 2 || h.a.bulk.dir != env.dst {
		t.Fatalf("bulk in right panel: %+v", h.a.bulk)
	}
}

// ---- sorting and hidden files ------------------------------------------------------

func TestSortReverseHiddenAndCursorIdentity(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	now := time.Now()
	write := func(n string, size int, age time.Duration) {
		p := filepath.Join(env.src, n)
		os.WriteFile(p, []byte(strings.Repeat("x", size)), 0o644)
		os.Chtimes(p, now.Add(-age), now.Add(-age))
	}
	write("a.txt", 10, 3*time.Hour)
	write("b.txt", 300, 1*time.Hour)
	write("c.go", 50, 2*time.Hour)
	write(".dot", 5, 0)
	h.press("ctrl+l")
	h.settle()
	p := h.a.ActivePanel()
	if got := strings.Join(names(p), ","); got != "..,sub,a.txt,b.txt,c.go" {
		t.Fatalf("name ↑: %s", got)
	}
	if !strings.Contains(h.view(), "name ↑") {
		t.Fatal("sort mode not shown")
	}
	h.focus("c.go")
	h.press("s") // size, natural direction: largest first
	h.settle()
	if got := strings.Join(names(p), ","); got != "..,sub,b.txt,c.go,a.txt" || !strings.Contains(h.view(), "size ↓") {
		t.Fatalf("size ↓: %s", got)
	}
	if e, _ := p.Current(); e.Name != "c.go" {
		t.Fatalf("cursor lost its item: %s", e.Name)
	}
	h.press("S") // reverse: smallest first, folders still first
	h.settle()
	if got := strings.Join(names(p), ","); got != "..,sub,a.txt,c.go,b.txt" || !strings.Contains(h.view(), "size ↑") {
		t.Fatalf("size ↑: %s", got)
	}
	if !strings.Contains(h.status(), "ascending") {
		t.Fatalf("status %q", h.status())
	}
	h.press("s") // time: newest first
	h.settle()
	if got := strings.Join(names(p), ","); got != "..,sub,b.txt,c.go,a.txt" || !strings.Contains(h.view(), "time ↓") {
		t.Fatalf("time ↓: %s", got)
	}
	h.press("s") // type (extension) A→Z
	h.settle()
	if got := strings.Join(names(p), ","); got != "..,sub,c.go,a.txt,b.txt" || !strings.Contains(h.view(), "type ↑") {
		t.Fatalf("type ↑: %s", got)
	}
	h.press("S")
	h.settle()
	if got := strings.Join(names(p), ","); got != "..,sub,a.txt,b.txt,c.go" {
		t.Fatalf("type ↓: %s", got)
	}
	// panels sort independently
	if h.a.Panel(1).Options().Sort != fsys.SortName {
		t.Fatal("the other panel's sort changed")
	}

	// h toggles hidden files ("." still works); the mode is shown
	h.press("s") // back to name
	h.settle()
	h.press("h")
	h.settle()
	if !panelHas(p, ".dot") || !strings.Contains(h.view(), "hidden shown") {
		t.Fatalf("h should show hidden files: %v", names(p))
	}
	// select the hidden file and keep the cursor on it, then hide again:
	// it leaves the selection, and the cursor lands on a neighbour
	h.focus(".dot")
	h.press("space")
	h.focus(".dot")
	h.press(".")
	h.settle()
	if panelHas(p, ".dot") || len(p.Selection()) != 0 {
		t.Fatalf("hidden file still listed/selected: %v %v", names(p), p.Selection())
	}
	if e, _ := p.Current(); e.Name == ".." {
		t.Fatal("cursor should land on a neighbour, not jump to the top")
	}
	if !strings.Contains(h.view(), "+1 hidden") {
		t.Fatal("hidden count not shown")
	}
	// h is no longer "parent directory"
	if p.Path() != env.src {
		t.Fatal("h must not navigate")
	}
}

func TestSortUnknownMetadataLast(t *testing.T) {
	es := []fsys.Entry{
		{Name: "big", Size: 100, ModTime: time.Unix(300, 0)},
		{Name: "broken", StatErr: os.ErrPermission},
		{Name: "small", Size: 1, ModTime: time.Unix(100, 0)},
		{Name: "dir", IsDir: true},
	}
	for _, desc := range []bool{false, true} {
		for _, key := range []fsys.SortKey{fsys.SortSize, fsys.SortTime} {
			c := append([]fsys.Entry(nil), es...)
			fsys.SortEntries(c, key, desc, false)
			if c[0].Name != "dir" || c[3].Name != "broken" {
				t.Errorf("key %v desc %v: %v", key, desc, c)
			}
		}
	}
}

// ---- search -------------------------------------------------------------------------

// The live search box is tested in feedback_test.go.

// ---- bulk rename ------------------------------------------------------------------------

func bulkSetup(t *testing.T, files ...string) (*harness, *opsEnv) {
	h, env := newOpsApp(t, nil, nil)
	for _, f := range files {
		os.WriteFile(filepath.Join(env.src, f), []byte("content of "+f), 0o644)
	}
	h.press("ctrl+l")
	h.settle()
	for _, f := range files {
		h.focus(f)
		h.press("space")
	}
	h.press("r")
	if h.a.bulk == nil {
		t.Fatal("bulk rename should open for several selected items")
	}
	return h, env
}

func TestBulkRenameTemplateApply(t *testing.T) {
	h, env := bulkSetup(t, "x1.jpg", "x2.jpg", "x3.jpg")
	v := h.view()
	if !strings.Contains(v, "Bulk rename: 3 items") || !strings.Contains(v, "0 to rename · 3 unchanged") {
		t.Fatalf("overlay:\n%s", v)
	}
	h.press("n", "ctrl+u")
	h.typeText("photo-{n:2}{ext}", false)
	h.press("enter")
	v = h.view()
	if !strings.Contains(v, "photo-01.jpg") || !strings.Contains(v, "3 to rename") {
		t.Fatalf("preview:\n%s", v)
	}
	if !fileExists(filepath.Join(env.src, "x1.jpg")) {
		t.Fatal("preview must not rename")
	}
	h.press("a")
	if !h.dialogKind(dlgBulkConfirm) || !strings.Contains(h.view(), "x1.jpg → photo-01.jpg") {
		t.Fatalf("confirm:\n%s", h.view())
	}
	h.press("enter") // default = No
	if h.a.dialog != nil || !fileExists(filepath.Join(env.src, "x1.jpg")) {
		t.Fatal("Enter must answer No")
	}
	h.press("a", "y")
	h.until("bulk done", func() bool { return h.a.bulk == nil })
	h.settle()
	for i, f := range []string{"photo-01.jpg", "photo-02.jpg", "photo-03.jpg"} {
		if got := readFile(t, filepath.Join(env.src, f)); got != "content of x"+string(rune('1'+i))+".jpg" {
			t.Fatalf("%s has %q", f, got)
		}
	}
	if !strings.HasPrefix(h.reported(), "Renamed 3 items") || len(h.a.ActivePanel().Selection()) != 0 {
		t.Fatalf("report %q", h.reported())
	}
}

func TestBulkRenameSwapByRowEdits(t *testing.T) {
	h, env := bulkSetup(t, "left", "right")
	h.press("enter", "ctrl+u") // row 0
	h.typeText("right", false)
	h.press("enter", "j", "enter", "ctrl+u")
	h.typeText("left", false)
	h.press("enter")
	if h.a.bulk.plan.Changes != 2 || h.a.bulk.plan.Errors != 0 {
		t.Fatalf("%+v", h.a.bulk.plan)
	}
	h.press("a", "y")
	h.until("bulk done", func() bool { return h.a.bulk == nil })
	if readFile(t, filepath.Join(env.src, "left")) != "content of right" || readFile(t, filepath.Join(env.src, "right")) != "content of left" {
		t.Fatal("swap failed")
	}
	for _, e := range h.a.ActivePanel().Entries() {
		if ops.IsBatchTemp(e.Name) {
			t.Fatal("temporary name left behind")
		}
	}
}

func TestBulkRenameValidationAndCancel(t *testing.T) {
	h, env := bulkSetup(t, "one.txt", "two.txt")
	// duplicate names
	h.press("n", "ctrl+u")
	h.typeText("same.txt", false)
	h.press("enter")
	v := h.view()
	if !strings.Contains(v, "2 problems") || !strings.Contains(v, "another item gets the same name") {
		t.Fatalf("duplicates:\n%s", v)
	}
	h.press("a")
	if h.a.dialog != nil || !strings.Contains(h.status(), "2 problems must be fixed") {
		t.Fatalf("apply with errors: %q", h.status())
	}
	// collision with an existing file outside the batch ("a.txt" exists)
	h.press("R", "enter", "ctrl+u", "ctrl+k")
	h.typeText("a.txt", false)
	h.press("enter")
	if !strings.Contains(h.view(), "an item with this name already exists") {
		t.Fatalf("exists:\n%s", h.view())
	}
	// invalid names
	h.press("enter", "ctrl+u", "ctrl+k")
	h.typeText("bad/name", false)
	h.press("enter")
	if !strings.Contains(h.view(), "✗") || h.a.bulk.plan.Errors != 1 {
		t.Fatalf("invalid:\n%s", h.view())
	}
	// find/replace with a regular expression (^R toggles it)
	h.press("R", "f")
	h.press("ctrl+r")
	h.typeText(`^(\w+)\.txt$`, false)
	h.press("enter", "ctrl+u")
	h.typeText("${1}.md", false)
	h.press("enter")
	if got := strings.Join(h.a.bulk.news(), ","); got != "one.md,two.md" {
		t.Fatalf("regex replace: %s", got)
	}
	// upper case, then cancel: nothing changes on disk
	h.press("U")
	if got := strings.Join(h.a.bulk.news(), ","); got != "ONE.MD,TWO.MD" {
		t.Fatalf("upper: %s", got)
	}
	h.press("esc")
	if h.a.bulk != nil || !strings.Contains(h.status(), "nothing was renamed") {
		t.Fatal("Esc must cancel")
	}
	if !fileExists(filepath.Join(env.src, "one.txt")) || !fileExists(filepath.Join(env.src, "two.txt")) {
		t.Fatal("cancel changed files")
	}
}

func TestBulkRenameInExternalEditor(t *testing.T) {
	h, env := bulkSetup(t, "aa", "bb")
	env.editFn = func(args []string) {
		os.WriteFile(args[len(args)-1], []byte("first\nsecond\n"), 0o644)
	}
	h.press("E")
	h.settle()
	if got := strings.Join(h.a.bulk.news(), ","); got != "first,second" {
		t.Fatalf("editor names: %s (status %q)", got, h.status())
	}
	if fileExists(env.execs[0].args[len(env.execs[0].args)-1]) {
		t.Fatal("temporary list not removed")
	}
	// wrong number of lines: refused, names unchanged
	env.editFn = func(args []string) { os.WriteFile(args[len(args)-1], []byte("only-one\n"), 0o644) }
	h.press("E")
	h.settle()
	if got := strings.Join(h.a.bulk.news(), ","); got != "first,second" || !strings.Contains(h.status(), "exactly 2 lines") {
		t.Fatalf("line count: %s %q", got, h.status())
	}
	// editor failure: names unchanged
	env.editFn = func(args []string) { os.WriteFile(args[len(args)-1], []byte("x\ny\n"), 0o644) }
	env.execErr = &editor.ExitError{Code: 1}
	h.press("E")
	h.settle()
	if got := strings.Join(h.a.bulk.news(), ","); got != "first,second" || !strings.Contains(h.status(), "new names unchanged") {
		t.Fatalf("editor failure: %s %q", got, h.status())
	}
}

func TestBulkRenamePartialFailureReport(t *testing.T) {
	h, env := bulkSetup(t, "p1", "p2")
	h.press("n", "ctrl+u")
	h.typeText("q{n}", false)
	h.press("enter", "a")
	// another program takes one of the target names before applying
	os.WriteFile(filepath.Join(env.src, "q2"), []byte("precious"), 0o644)
	h.press("y")
	h.until("bulk done", func() bool { return h.a.bulk == nil })
	h.settle()
	if !h.dialogKind(dlgReport) || !strings.Contains(h.view(), "Renamed 1 of 2") {
		t.Fatalf("report:\n%s", h.view())
	}
	if readFile(t, filepath.Join(env.src, "q2")) != "precious" || !fileExists(filepath.Join(env.src, "p2")) {
		t.Fatal("existing file overwritten or source lost")
	}
}

func TestHelpAndFooterListPhase3Keys(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.press("?")
	v := h.view()
	for _, want := range []string{"find files by name", "reverse sort order", "h/H/.", "hidden files (both panels)", "⌫/←", "rename (several: bulk)"} {
		if !strings.Contains(v, want) {
			t.Errorf("help lacks %q", want)
		}
	}
	h.press("esc")
	h.send(tea.WindowSizeMsg{Width: 160, Height: 30})
	if v := squashSpaces(h.view()); !strings.Contains(v, "/  find") || !strings.Contains(v, "s/S  sort") || !strings.Contains(v, "h  show hidden") {
		t.Errorf("footer lacks phase 3 keys:\n%s", v)
	}
}

func TestPluralWord(t *testing.T) {
	for in, want := range map[string]string{"file": "files", "match": "matches", "entry": "entries",
		"folder": "folders", "item": "items", "key": "keys", "box": "boxes"} {
		if got := pluralWord(in); got != want {
			t.Errorf("pluralWord(%q) = %q, want %q", in, got, want)
		}
	}
	if got := plural(1, "match"); got != "1 match" {
		t.Errorf("plural(1) = %q", got)
	}
}
