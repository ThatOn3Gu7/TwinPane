package ui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/ThatOn3Gu7/TwinPane/internal/editor"
	"github.com/ThatOn3Gu7/TwinPane/internal/proc"
	"github.com/ThatOn3Gu7/TwinPane/internal/search"
)

// ---- live search ------------------------------------------------------------

// testBackends returns each available backend on its own (fd also from
// $TWINPANE_TEST_FD), so every test runs against all of them.
func testBackends(t *testing.T) [][]search.Backend {
	var out [][]search.Backend
	have := map[string]bool{}
	for _, b := range search.DetectBackends(proc.LookPath) {
		out = append(out, []search.Backend{b})
		have[b.Name] = true
	}
	if p := os.Getenv("TWINPANE_TEST_FD"); p != "" && !have[search.BackendFd] {
		out = append([][]search.Backend{{{Name: search.BackendFd, Path: p}}}, out...)
	}
	return out
}

func (h *harness) searchDone() {
	h.until("index done", func() bool { return h.a.search != nil && !h.a.search.indexing })
}

func matchRels(sv *searchView) []string {
	var out []string
	for _, m := range sv.matches {
		out = append(out, filepath.ToSlash(sv.entries[m].Rel))
	}
	return out
}

func TestLiveSearchBackends(t *testing.T) {
	for _, bs := range testBackends(t) {
		t.Run(bs[0].Name, func(t *testing.T) {
			h, env := newOpsApp(t, nil, nil)
			h.a.opts.SearchBackends = bs
			os.WriteFile(filepath.Join(env.src, "sub", "deep", "Target-File.md"), nil, 0o644)
			os.WriteFile(filepath.Join(env.src, ".hidden-target"), nil, 0o644)
			h.press("/")
			if h.a.search == nil || h.a.dialog != nil {
				t.Fatal("/ should open the search box directly (no prompt dialog)")
			}
			h.searchDone()
			v := h.view()
			if !strings.Contains(v, "Find files by name") || !strings.Contains(v, bs[0].Name) || !strings.Contains(v, "hidden skipped") {
				t.Fatalf("box:\n%s", v)
			}
			// empty pattern: everything (6 entries: a.txt b.txt sub sub/deep c.txt Target)
			if n := len(h.a.search.matches); n != 6 {
				t.Fatalf("empty query: %v", matchRels(h.a.search))
			}
			// results update with every keystroke, without Enter
			h.typeText("t", false)
			if got := matchRels(h.a.search); len(got) != 4 { // a.txt b.txt c.txt Target-File.md
				t.Fatalf("after t: %v", got)
			}
			h.typeText("arg", false)
			if got := matchRels(h.a.search); !reflect.DeepEqual(got, []string{"sub/deep/Target-File.md"}) {
				t.Fatalf("after targ: %v", got)
			}
			if !strings.Contains(h.view(), "1 match of 6 entries") {
				t.Fatalf("count line:\n%s", h.view())
			}
			h.typeText("zz", false)
			if len(h.a.search.matches) != 0 {
				t.Fatal("targzz should match nothing")
			}
			h.press("backspace", "backspace")
			if len(h.a.search.matches) != 1 {
				t.Fatal("backspace should bring the match back")
			}
			// an invalid wildcard keeps the previous results and says why
			h.typeText("[", false)
			if h.a.search.qerr == "" || len(h.a.search.matches) != 1 || !strings.Contains(h.view(), "invalid wildcard") {
				t.Fatalf("invalid pattern: %q %v", h.a.search.qerr, matchRels(h.a.search))
			}
			h.press("backspace")
			// Enter jumps to the result
			h.press("enter")
			h.settle()
			p := h.a.ActivePanel()
			if h.a.search != nil || p.Path() != filepath.Join(env.src, "sub", "deep") {
				t.Fatalf("jump: %s", p.Path())
			}
			if e, _ := p.Current(); e.Name != "Target-File.md" {
				t.Fatalf("cursor on %s", e.Name)
			}
			// with hidden files shown in the panel, the index includes them
			h.exec(p.ChangeDir(env.src))
			h.until("src", func() bool { return h.a.ActivePanel().Path() == env.src })
			h.press("h", "/")
			h.searchDone()
			h.typeText("target", false)
			// name prefix ranks before substring
			if got := matchRels(h.a.search); !reflect.DeepEqual(got, []string{"sub/deep/Target-File.md", ".hidden-target"}) {
				t.Fatalf("hidden included: %v", got)
			}
			// Esc closes; a stale update afterwards is ignored
			id := h.a.search.id
			h.press("esc")
			h.a.Update(searchMsg{id: id, u: search.IndexUpdate{Done: true}})
			if h.a.search != nil {
				t.Fatal("stale update reopened the search")
			}
		})
	}
}

// Results appear while the index is still being built, and the final
// ranking keeps the cursor on the same item.
func TestLiveSearchWhileIndexing(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	sv := &searchView{id: 4242, root: env.src, input: newLineInput(""), marks: map[string]bool{}, indexing: true}
	h.a.search = sv
	entry := func(rel string) search.Entry {
		e, _ := search.NewEntry(env.src, filepath.Join(env.src, rel))
		return e
	}
	h.typeText("rep", false)
	if len(sv.matches) != 0 {
		t.Fatal("nothing indexed yet")
	}
	h.a.Update(searchMsg{id: 4242, u: search.IndexUpdate{Entries: []search.Entry{entry("x/y/old-report.txt"), entry("a.txt")}, Count: 2}})
	if got := matchRels(sv); !reflect.DeepEqual(got, []string{"x/y/old-report.txt"}) {
		t.Fatalf("first batch: %v", got)
	}
	if !strings.Contains(h.view(), "indexing…") {
		t.Fatal("state should say indexing")
	}
	h.press("down") // cursor stays (one result)
	h.a.Update(searchMsg{id: 4242, u: search.IndexUpdate{Entries: []search.Entry{entry("report.md")}, Count: 3}})
	if got := matchRels(sv); !reflect.DeepEqual(got, []string{"x/y/old-report.txt", "report.md"}) {
		t.Fatalf("second batch appended: %v", got)
	}
	h.a.Update(searchMsg{id: 4242, u: search.IndexUpdate{Count: 3, Done: true}})
	if got := matchRels(sv); !reflect.DeepEqual(got, []string{"report.md", "x/y/old-report.txt"}) {
		t.Fatalf("final ranking (prefix first): %v", got)
	}
	if sv.currentPath() != filepath.Join(env.src, "x/y/old-report.txt") {
		t.Fatalf("cursor moved to %s", sv.currentPath())
	}
}

func TestSearchEditResults(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.a.opts.SearchBackends = []search.Backend{{Name: search.BackendBuiltin}}
	h.press("/")
	h.searchDone()
	h.typeText(".txt", false) // a.txt b.txt sub/deep/c.txt
	if len(h.a.search.matches) != 3 {
		t.Fatalf("%v", matchRels(h.a.search))
	}
	// F4 edits the highlighted result from the text field
	h.press("f4")
	h.settle()
	if len(env.execs) != 1 || !reflect.DeepEqual(env.execs[0].args, []string{"/bin/nvim", filepath.Join(env.src, "a.txt")}) {
		t.Fatalf("F4: %v", env.execs)
	}
	if h.a.search == nil {
		t.Fatal("the search box should stay open after editing")
	}
	// Tab: result list. Space marks, e opens the marked results together
	h.press("tab", " ", "down", " ")
	if !strings.Contains(h.view(), "2 marked") {
		t.Fatalf("marks:\n%s", h.view())
	}
	h.press("e")
	h.settle()
	want := []string{"/bin/nvim", filepath.Join(env.src, "a.txt"), filepath.Join(env.src, "sub", "deep", "c.txt")}
	if len(env.execs) != 2 || !reflect.DeepEqual(env.execs[1].args, want) {
		t.Fatalf("e with marks: %v", env.execs)
	}
	// E: the editor chooser opens over the box
	h.press("E")
	if !h.dialogKind(dlgChooser) || !strings.Contains(h.view(), "Open 2 files with") {
		t.Fatalf("chooser:\n%s", h.view())
	}
	h.press("esc")
	if h.a.dialog != nil || h.a.search == nil {
		t.Fatal("Esc should close only the chooser")
	}
	// a folder result is refused
	h.press("/")
	for range ".txt" {
		h.press("backspace")
	}
	h.typeText("deep", false)
	h.press("tab", "e")
	if !strings.Contains(h.status(), "deep is a folder") || len(env.execs) != 2 {
		t.Fatalf("folder: %q", h.status())
	}
	// typing in the list goes back to the text field
	h.typeText("x", false)
	if h.a.search.list || h.a.search.input.Value() != "deepx" {
		t.Fatalf("typing in list: list=%v value=%q", h.a.search.list, h.a.search.input.Value())
	}
}

// ---- editor: e and E ----------------------------------------------------------

func chooserEnv(vars map[string]string, have ...string) *editor.Env {
	return &editor.Env{
		Getenv: func(k string) string { return vars[k] },
		LookPath: func(n string) (string, error) {
			for _, h := range have {
				if h == n {
					return "/bin/" + n, nil
				}
			}
			return "", proc.ErrNotFound
		},
		GOOS: "linux",
	}
}

func TestEditorChooser(t *testing.T) {
	h, env := newOpsApp(t, nil, chooserEnv(map[string]string{"EDITOR": "nvim"}, "nvim", "nano", "mcedit"))
	h.focus("a.txt")
	h.press(" ", " ") // a.txt and b.txt
	h.press("E")
	if !h.dialogKind(dlgChooser) {
		t.Fatal("E should open the editor chooser")
	}
	var labels []string
	for _, it := range h.a.dialog.list {
		l := it.label
		if it.disabled {
			l += " (disabled)"
		}
		labels = append(labels, l)
	}
	want := []string{"Neovim", "Neovim – tabs", "Neovim – side by side", "GNU nano", "mcedit (disabled)"}
	if !reflect.DeepEqual(labels, want) {
		t.Fatalf("chooser rows %v", labels)
	}
	v := h.view()
	if !strings.Contains(v, "Open 2 files with") || !strings.Contains(v, "$EDITOR · default") || !strings.Contains(v, "one file only") {
		t.Fatalf("chooser view:\n%s", v)
	}
	if h.a.dialog.listCursor != 0 {
		t.Fatal("cursor should start on the default editor")
	}
	h.press("down", "down", "down", "down") // wraps over the disabled mcedit row
	if h.a.dialog.listCursor != 0 {
		t.Fatalf("cursor %d: disabled rows must be skipped", h.a.dialog.listCursor)
	}
	h.press("2") // Neovim in tabs
	h.settle()
	a, b := filepath.Join(env.src, "a.txt"), filepath.Join(env.src, "b.txt")
	if len(env.execs) != 1 || !reflect.DeepEqual(env.execs[0].args, []string{"/bin/nvim", "-p", a, b}) {
		t.Fatalf("exec %v", env.execs)
	}
	h.press("E", "down", "down", "down", "enter") // GNU nano
	h.settle()
	if len(env.execs) != 2 || !reflect.DeepEqual(env.execs[1].args, []string{"/bin/nano", a, b}) {
		t.Fatalf("exec %v", env.execs)
	}
	// a single file: no tab variants, mcedit enabled
	h.press("u")
	h.focus("a.txt")
	h.press("E")
	if n := len(h.a.dialog.list); n != 3 || h.a.dialog.list[2].disabled {
		t.Fatalf("single-file chooser: %+v", h.a.dialog.list)
	}
	h.press("esc")
	if h.a.dialog != nil || len(env.execs) != 2 {
		t.Fatal("Esc must cancel without editing")
	}
	// folders are refused before the chooser opens
	h.focus("sub")
	h.press("E")
	if h.a.dialog != nil || !strings.Contains(h.status(), "sub is a folder") {
		t.Fatalf("folder: %q", h.status())
	}
}

func TestEditDefaultEditorSilentFallback(t *testing.T) {
	// $EDITOR unset: the first detected editor, no question asked
	h, env := newOpsApp(t, nil, chooserEnv(nil, "vi", "nano"))
	h.focus("a.txt")
	h.press("e")
	h.settle()
	if h.a.dialog != nil || len(env.execs) != 1 || env.execs[0].args[0] != "/bin/nano" {
		t.Fatalf("unset $EDITOR: dialog=%v execs=%v", h.a.dialog != nil, env.execs)
	}
	// $EDITOR set but missing: a detected editor is used and the status says so
	h2, env2 := newOpsApp(t, nil, chooserEnv(map[string]string{"EDITOR": "nvim-missing"}, "nano"))
	h2.focus("a.txt")
	h2.press("e")
	h2.settle()
	if len(env2.execs) != 1 || env2.execs[0].args[0] != "/bin/nano" {
		t.Fatalf("missing $EDITOR: %v", env2.execs)
	}
	if s := h2.reported(); !strings.Contains(s, `"nvim-missing" from $EDITOR was not found`) || !strings.Contains(s, "used nano") {
		t.Fatalf("report %q", s)
	}
	// no editor at all
	h3, env3 := newOpsApp(t, nil, chooserEnv(nil))
	h3.focus("a.txt")
	h3.press("E")
	if h3.a.dialog != nil || len(env3.execs) != 0 || !strings.Contains(h3.reported(), "no editor found") {
		t.Fatalf("no editors: %q", h3.reported())
	}
}

// ---- selection look and mirroring ----------------------------------------------

func TestSelectionIsGrayBackground(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(old)
	h, _ := newOpsApp(t, nil, nil)
	h.focus("a.txt")
	h.press(" ") // select a.txt, cursor moves to b.txt
	raw := h.a.View()
	plain := h.view()
	if strings.Contains(plain, "✓") {
		t.Fatal("no check mark for selected rows")
	}
	if strings.Contains(raw, "38;5;220") {
		t.Fatal("no yellow text for selected rows")
	}
	var row string
	for _, l := range strings.Split(raw, "\n") {
		if strings.Contains(l, "a.txt") {
			row = l
			break
		}
	}
	if !strings.Contains(row, "48;5;240") {
		t.Fatalf("selected row should have the gray background: %q", row)
	}
	if !strings.Contains(plain, "1 selected") {
		t.Fatalf("summary:\n%s", plain)
	}
	// unselected rows have no background
	for _, l := range strings.Split(raw, "\n") {
		if strings.Contains(l, "sub/") && strings.Contains(l, "48;5;240") {
			t.Fatal("unselected row has the selection background")
		}
	}
}

func TestMirroredSelection(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	left, right := h.a.Panel(0), h.a.Panel(1)
	// right panel into the same folder as the left one
	h.exec(right.ChangeDir(env.src))
	h.until("right in src", func() bool { return right.Path() == env.src })
	h.focus("a.txt")
	h.press(" ")
	if !right.IsSelected("a.txt") {
		t.Fatal("selection not shown in the other panel showing the same folder")
	}
	// selecting in the right panel shows in the left one
	h.press("tab")
	right.Top()
	for e, _ := right.Current(); e.Name != "b.txt"; e, _ = right.Current() {
		right.Down()
	}
	h.press(" ")
	if !left.IsSelected("b.txt") || len(left.Selection()) != 2 {
		t.Fatalf("left selection %v", left.Selection())
	}
	h.press("u") // clearing is mirrored too
	if len(left.Selection()) != 0 {
		t.Fatal("clear not mirrored")
	}
	// a panel that leaves gets its own selection; the other keeps its own
	h.press("a") // select all in right (shared)
	h.exec(right.ChangeDir(env.dst))
	h.until("right in dst", func() bool { return right.Path() == env.dst })
	if len(right.Selection()) != 0 || len(left.Selection()) != 3 {
		t.Fatalf("after leaving: left %d right %d", len(left.Selection()), len(right.Selection()))
	}
	// coming back merges again
	h.exec(right.ChangeDir(env.src))
	h.until("right back", func() bool { return right.Path() == env.src })
	if len(right.Selection()) != 3 {
		t.Fatalf("back in the same folder: right %d", len(right.Selection()))
	}
	// hidden files: shown and selected in the left panel, hidden in the
	// right one; a refresh of the right panel must not drop them. Since
	// v0.4.0 the h key sets both panels at once, so the differing settings
	// are made through the panel API (the invariant still matters for any
	// refresh racing a toggle).
	os.WriteFile(filepath.Join(env.src, ".dot"), nil, 0o644)
	h.press("u", "tab") // left active
	h.exec(left.SetShowHidden(true))
	h.settle()
	h.focus(".dot")
	h.press(" ")
	h.exec(right.Refresh())
	h.settle()
	if !left.IsSelected(".dot") {
		t.Fatal("refresh of the panel hiding dot-files dropped the other panel's selection")
	}
	// the right panel does not show .dot, so it never acts on it
	for _, e := range append(right.Selection(), right.Targets()...) {
		if e.Name == ".dot" {
			t.Fatal("a panel hiding dot-files targets the hidden selected file")
		}
	}
	if len(left.Selection()) != 1 || left.Selection()[0].Name != ".dot" {
		t.Fatalf("left selection %v", left.Selection())
	}
}
