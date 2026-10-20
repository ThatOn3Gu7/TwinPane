package ui

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ThatOn3Gu7/TwinPane/internal/panel"
	"github.com/ThatOn3Gu7/TwinPane/internal/search"
)

// ---- helpers ---------------------------------------------------------------------

// advOpen opens Advanced Search from the panels (the / then Ctrl+G route).
func advOpen(t *testing.T, h *harness) *advancedSearchView {
	t.Helper()
	h.press("/")
	h.press("ctrl+g")
	v := h.a.advancedSearch
	if v == nil {
		t.Fatal("Advanced Search did not open")
	}
	return v
}

// advOpenIndexed opens Advanced Search and waits for the first index pass.
func advOpenIndexed(t *testing.T, h *harness) *advancedSearchView {
	t.Helper()
	v := advOpen(t, h)
	h.until("advanced index", func() bool {
		return !v.indexing && !v.filtering && len(v.entries) > 0
	})
	return v
}

// advSelect moves the result cursor to the result for path.
func advSelect(t *testing.T, h *harness, path string) {
	t.Helper()
	v := h.a.advancedSearch
	if v == nil {
		t.Fatal("Advanced Search is not open")
	}
	want := filepath.Clean(path)
	for i, index := range v.filenameResults {
		if index < 0 || index >= len(v.entries) {
			continue
		}
		if filepath.Clean(v.entries[index].Path) == want {
			h.a.setAdvancedCursor(i)
			h.until("preview", func() bool { return !v.previewLoading })
			return
		}
	}
	var seen []string
	for _, index := range v.filenameResults {
		if index >= 0 && index < len(v.entries) {
			seen = append(seen, v.entries[index].Path)
		}
	}
	t.Fatalf("result %q is not listed (%d results: %v)", path, len(v.filenameResults), seen)
}

// advMenu presses Ctrl+A and checks the contextual menu opened over the
// still-open results.
func advMenu(t *testing.T, h *harness) string {
	t.Helper()
	h.press("ctrl+a")
	if !h.dialogKind(dlgMenu) {
		t.Fatalf("Ctrl+A did not open the actions menu (dialog %+v)", h.a.dialog)
	}
	if h.a.advancedSearch == nil {
		t.Fatal("the actions menu closed Advanced Search")
	}
	return strings.Join(strings.Fields(h.view()), " ")
}

// advPick opens the menu and picks one entry by its letter.
func advPick(t *testing.T, h *harness, key string) {
	t.Helper()
	advMenu(t, h)
	h.press(key)
}

// advContent builds a content-mode result list. The overlay's own worker is
// stopped so the fixture is stable.
func advContent(t *testing.T, h *harness, query string, results ...search.ContentResult) *advancedSearchView {
	t.Helper()
	v := advOpen(t, h)
	v.mode = advancedContentMode
	v.input = newLineInput(query)
	if v.contentCancel != nil {
		v.contentCancel()
	}
	v.contentGeneration++
	v.contentLoading = false
	v.contentResults = append([]search.ContentResult(nil), results...)
	v.cursor = 0
	h.a.clampAdvancedSearch()
	h.a.refreshAdvancedPreview()
	h.settle()
	return v
}

// captureClipboard swaps the OSC 52 writer for the duration of a test.
func captureClipboard(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := clipboardOut
	clipboardOut = &buf
	t.Cleanup(func() { clipboardOut = old })
	return &buf
}

func (h *harness) otherPanelState() (string, int) {
	p := h.a.panels[1]
	return p.Path(), p.Cursor()
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ---- the menu itself ---------------------------------------------------------------

func TestAdvancedActionsMenuListsEveryActionAndKeepsResults(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	report := filepath.Join(env.src, "report.txt")
	mustWrite(t, report, "one\ntwo\n")
	v := advOpenIndexed(t, h)
	advSelect(t, h, report)

	beforeQuery := v.input.Value()
	beforeCursor := v.cursor
	view := advMenu(t, h)
	for _, want := range []string{
		"Actions · report.txt", "Inspect read-only", "Open with default app", "Jump to location",
		"Edit", "Open with…", "Copy to other panel", "Move to other panel", "Rename…", "Copy path", "Delete…",
		"⏎ or letter: run · Esc close",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("menu lacks %q:\n%s", want, view)
		}
	}
	// the destination panel's folder is the detail of copy/move
	target, ok := h.a.advancedSelectedTarget()
	if !ok {
		t.Fatal("the menu target could not be resolved")
	}
	for _, m := range h.a.advancedActionEntries(target) {
		if (m.label == "Copy to other panel" || m.label == "Move to other panel") && m.detail != shortPath(env.dst) {
			t.Errorf("%s targets %q, want %q", m.label, m.detail, shortPath(env.dst))
		}
	}
	// the menu row itself carries a destination (shortened to the menu width)
	if !strings.Contains(view, "Copy to other panel /") || !strings.Contains(view, "Move to other panel /") {
		t.Errorf("copy/move do not show their destination:\n%s", view)
	}
	// Esc closes only the menu
	h.press("esc")
	if h.a.dialog != nil {
		t.Fatal("Esc did not close the menu")
	}
	if h.a.advancedSearch != v || v.cursor != beforeCursor || v.input.Value() != beforeQuery {
		t.Fatal("closing the menu changed the results or the query")
	}
	if !strings.Contains(h.view(), "Advanced Search · Filename") {
		t.Fatalf("the overlay is gone:\n%s", h.view())
	}
	// q closes the menu too (it never closes Advanced Search from inside it)
	advMenu(t, h)
	h.press("q")
	if h.a.dialog != nil || h.a.advancedSearch != v {
		t.Fatal("q did not close just the menu")
	}
	// an unbound letter does nothing at all
	advMenu(t, h)
	h.press("x", "z")
	if !h.dialogKind(dlgMenu) {
		t.Fatal("an unbound key closed the menu")
	}
	if v.input.Value() != beforeQuery {
		t.Fatalf("menu keys typed into the query: %q", v.input.Value())
	}
	h.press("esc")
	h.press("esc")
	if h.a.advancedSearch != nil {
		t.Fatal("Esc did not close Advanced Search")
	}
}

func TestAdvancedActionsHintChipOpensTheMenu(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	path := filepath.Join(env.src, "a.txt")
	advOpenIndexed(t, h)
	advSelect(t, h, path)
	if !strings.Contains(h.footer(), "actions") {
		t.Fatalf("the hint bar does not advertise the actions: %q", h.footer())
	}
	h.tapHint("ctrl+a")
	if !h.dialogKind(dlgMenu) || !strings.Contains(h.view(), "Actions · a.txt") {
		t.Fatalf("tapping the actions hint did not open the menu (dialog %+v)", h.a.dialog)
	}
	h.press("esc")
	h.press("esc")
}

func TestAdvancedActionsMenuSkipsFileOnlyEntriesForFolders(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	advOpenIndexed(t, h)
	advSelect(t, h, filepath.Join(env.src, "sub"))
	view := advMenu(t, h)
	for _, want := range []string{"Open folder", "Jump to location", "Copy to other panel", "Move to other panel", "Rename…", "Copy path", "Delete…"} {
		if !strings.Contains(view, want) {
			t.Errorf("folder menu lacks %q:\n%s", want, view)
		}
	}
	for _, unwanted := range []string{"Inspect read-only", "Open with default app", "Open with…"} {
		if strings.Contains(view, unwanted) {
			t.Errorf("folder menu offers %q:\n%s", unwanted, view)
		}
	}
	h.press("esc")
	h.press("esc")
}

func TestAdvancedActionsMenuTargetsContentMatchLine(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	path := filepath.Join(env.src, "content.txt")
	mustWrite(t, path, "first\nneedle one\nneedle two\n")
	v := advContent(t, h, "needle", search.ContentResult{
		Path: path, Rel: "content.txt",
		Matches: []search.ContentMatch{{Line: 2, Text: "needle one"}, {Line: 3, Text: "needle two"}},
	})
	view := advMenu(t, h)
	if !strings.Contains(view, "Actions · content.txt:2") {
		t.Errorf("menu title does not name the match line:\n%s", view)
	}
	if !strings.Contains(view, "at line 2") || !strings.Contains(view, "line 2 in") {
		t.Errorf("menu does not show the match line in its details:\n%s", view)
	}
	if !strings.Contains(strings.Join(strings.Fields(h.view()), " "), "Advanced Search · Content") {
		t.Fatalf("the menu replaced the content results:\n%s", h.view())
	}
	h.press("esc")
	if h.a.advancedSearch != v {
		t.Fatal("closing the content menu closed the search")
	}
	h.press("esc")
}

// ---- open, inspect, open with, edit -------------------------------------------------

func TestAdvancedActionsInspectAndOpenFilenameResult(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	termux(env)
	path := filepath.Join(env.src, "notes.txt")
	mustWrite(t, path, "hello\n")
	advOpenIndexed(t, h)
	advSelect(t, h, path)

	// i inspects read-only, exactly like Enter
	advPick(t, h, "i")
	h.settle()
	if h.a.viewer == nil || !h.a.viewer.fromAdvanced || h.a.viewer.path != path {
		t.Fatalf("inspect did not open the read-only viewer: %+v", h.a.viewer)
	}
	if h.a.advancedSearch == nil || h.a.dialog != nil {
		t.Fatal("inspection lost the search state")
	}
	h.press("esc")
	if h.a.viewer != nil || h.a.advancedSearch == nil {
		t.Fatal("Esc did not return to the results")
	}

	// o opens it with the platform's default app
	advPick(t, h, "o")
	h.until("launch", func() bool { return len(env.launched()) == 1 })
	if got := env.launched()[0]; len(got) < 2 || got[len(got)-1] != path {
		t.Fatalf("opened %q, want the result path %q", got, path)
	}
	if h.a.advancedSearch == nil {
		t.Fatal("opening the file closed the search")
	}

	// O lists the choices for the same result, without touching the panels
	advPick(t, h, "O")
	if !h.dialogKind(dlgOpenWith) {
		t.Fatalf("O did not open the Open-with list (dialog %+v)", h.a.dialog)
	}
	if !strings.Contains(h.view(), "notes.txt") {
		t.Fatalf("Open-with does not name the result:\n%s", h.view())
	}
	h.press("esc")
	if h.a.dialog != nil || h.a.advancedSearch == nil {
		t.Fatal("closing Open with lost the search")
	}
	h.press("esc")
}

func TestAdvancedActionsEditFilenameResult(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	path := filepath.Join(env.src, "deep name.txt")
	mustWrite(t, path, "x\n")
	advOpenIndexed(t, h)
	advSelect(t, h, path)
	advPick(t, h, "e")
	h.settle()
	if len(env.execs) != 1 {
		t.Fatalf("edit ran %d processes, want 1: %+v", len(env.execs), env.execs)
	}
	args := env.execs[0].args
	if len(args) == 0 || args[len(args)-1] != path {
		t.Fatalf("editor argv %q does not end with %q", args, path)
	}
	if h.a.advancedSearch == nil {
		t.Fatal("editing closed Advanced Search")
	}
	h.press("esc")
}

func TestAdvancedActionEnterOnDirectoryEntersIt(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	p := h.a.ActivePanel()
	otherPath, otherCursor := h.otherPanelState()
	advOpenIndexed(t, h)
	deep := filepath.Join(env.src, "sub", "deep")
	advSelect(t, h, deep)
	h.press("enter") // Enter on a folder opens it in the main interface
	if h.a.advancedSearch != nil {
		t.Fatal("Enter on a folder kept the overlay open")
	}
	h.until("folder", func() bool { return p.Path() == deep && !p.Loading() })
	if got, _ := h.otherPanelState(); got != otherPath || p.Path() != deep {
		t.Fatal("entering the folder changed the wrong panel")
	}
	if _, cur := h.otherPanelState(); cur != otherCursor {
		t.Fatal("the other panel's cursor moved")
	}

	// the menu's Open folder does the same (from the parent folder again)
	h.exec(p.Load(env.src, "", panel.LoadNavigate))
	h.until("back in the root", func() bool { return p.Path() == env.src && !p.Loading() })
	advOpenIndexed(t, h)
	advSelect(t, h, deep)
	advPick(t, h, "o")
	if h.a.advancedSearch != nil {
		t.Fatal("Open folder kept the overlay open")
	}
	h.until("folder again", func() bool { return p.Path() == deep })
}

// ---- jump to location ----------------------------------------------------------------

func TestAdvancedActionJumpSelectsTheResult(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	// a nested result, so the panel really has to navigate
	target := filepath.Join(env.src, "sub", "deep", "c.txt")
	odd := filepath.Join(env.src, "sub", "odd name 'ü' #1.txt")
	mustWrite(t, odd, "odd\n")
	otherPath, otherCursor := h.otherPanelState()
	advOpenIndexed(t, h)
	advSelect(t, h, target)
	advPick(t, h, "t")
	if h.a.advancedSearch != nil || h.a.viewer != nil {
		t.Fatal("jump did not close the overlay")
	}
	p := h.a.ActivePanel()
	h.until("panel on the result folder", func() bool {
		return p.Path() == filepath.Join(env.src, "sub", "deep") && !p.Loading()
	})
	h.until("cursor on the result", func() bool {
		e, ok := p.Current()
		return ok && e.Path == target
	})
	if !strings.Contains(h.status(), "found") || !strings.Contains(h.status(), "c.txt") {
		t.Fatalf("status %q does not report the jump", h.status())
	}
	if got, cur := h.otherPanelState(); got != otherPath || cur != otherCursor {
		t.Fatal("jump changed the other panel")
	}

	// a result with spaces, quotes and non-ASCII characters is selected too
	h.exec(p.Load(env.src, "", panel.LoadNavigate))
	h.until("back in the root", func() bool { return p.Path() == env.src && !p.Loading() })
	advOpenIndexed(t, h)
	advSelect(t, h, odd)
	advPick(t, h, "t")
	h.until("odd name selected", func() bool {
		e, ok := p.Current()
		return p.Path() == filepath.Join(env.src, "sub") && ok && e.Path == odd
	})
}

func TestAdvancedJumpToFolderSelectsInsteadOfEntering(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	advOpenIndexed(t, h)
	advSelect(t, h, filepath.Join(env.src, "sub"))
	advPick(t, h, "t")
	if h.a.advancedSearch != nil {
		t.Fatal("jump did not close the overlay")
	}
	p := h.a.ActivePanel()
	h.until("cursor on the folder", func() bool {
		e, ok := p.Current()
		return p.Path() == env.src && ok && e.Name == "sub" && e.IsDir
	})
}

func TestAdvancedActionJumpOnContentOpensTheMatchLine(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	path := filepath.Join(env.src, "sub", "deep", "c.txt")
	mustWrite(t, path, "header\nneedle here\nneedle again\n")
	advContent(t, h, "needle", search.ContentResult{
		Path: path, Rel: "sub/deep/c.txt",
		Matches: []search.ContentMatch{{Line: 2, Text: "needle here"}, {Line: 3, Text: "needle again"}},
	})
	if !strings.Contains(strings.Join(strings.Fields(h.view()), " "), "Advanced Search · Content") {
		t.Fatal("content mode did not render")
	}
	advPick(t, h, "t")
	if h.a.advancedSearch != nil {
		t.Fatal("jump kept the overlay open")
	}
	p := h.a.ActivePanel()
	h.until("panel on the file", func() bool { return p.Path() == filepath.Join(env.src, "sub", "deep") })
	h.until("cursor on the file", func() bool {
		e, ok := p.Current()
		return ok && e.Path == path
	})
	h.until("viewer", func() bool { return h.a.viewer != nil && !h.a.viewer.loading })
	view := h.a.viewer
	if view.path != path || view.fromAdvanced {
		t.Fatalf("jump viewer: path %q fromAdvanced %v", view.path, view.fromAdvanced)
	}
	if view.query != "needle" || view.mcur < 0 {
		t.Fatalf("jump did not seed the match: query %q mcur %d", view.query, view.mcur)
	}
	if line := view.doc.Lines[view.matches[view.mcur]]; !strings.Contains(line, "needle here") {
		t.Fatalf("viewer is on %q, want the first match line", line)
	}
	// the regular viewer, so editing works from here
	h.press("e")
	h.settle()
	if len(env.execs) != 1 || env.execs[0].args[len(env.execs[0].args)-1] != path {
		t.Fatalf("editing from the jumped-to viewer failed: %+v", env.execs)
	}
	if h.a.advancedSearch != nil {
		t.Fatal("the search reappeared")
	}
}

func TestAdvancedContentInspectAndActions(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	termux(env)
	path := filepath.Join(env.src, "content.txt")
	mustWrite(t, path, "first\nneedle one\nneedle two\nlast\n")
	v := advContent(t, h, "needle", search.ContentResult{
		Path: path, Rel: "content.txt",
		Matches: []search.ContentMatch{{Line: 2, Text: "needle one"}, {Line: 3, Text: "needle two"}},
	})
	// Enter still inspects, at the match line
	h.press("enter")
	h.settle()
	if h.a.viewer == nil || !h.a.viewer.fromAdvanced || h.a.viewer.mcur < 0 {
		t.Fatalf("Enter did not inspect at the match: %+v", h.a.viewer)
	}
	if got := h.a.viewer.doc.Lines[h.a.viewer.matches[h.a.viewer.mcur]]; !strings.Contains(got, "needle one") {
		t.Fatalf("inspection at %q", got)
	}
	h.press("esc")
	if h.a.advancedSearch == nil {
		t.Fatal("Esc did not return to the results")
	}
	// i does the same from the menu
	advPick(t, h, "i")
	h.settle()
	if h.a.viewer == nil || !h.a.viewer.fromAdvanced || h.a.viewer.mcur < 0 {
		t.Fatal("inspect from the menu lost the line")
	}
	h.press("esc")
	// o opens with the default app, e edits, O lists choices
	advPick(t, h, "o")
	h.until("launch", func() bool { return len(env.launched()) == 1 })
	advPick(t, h, "e")
	h.settle()
	if len(env.execs) != 1 || env.execs[0].args[len(env.execs[0].args)-1] != path {
		t.Fatalf("edit from a content result: %+v", env.execs)
	}
	advPick(t, h, "O")
	if !h.dialogKind(dlgOpenWith) {
		t.Fatal("O did not open the Open-with list for a content result")
	}
	h.press("esc")
	// rename keeps the match (and its line) under the new name
	advPick(t, h, "r")
	if !strings.Contains(h.view(), "content match stays selected") {
		t.Fatalf("the content rename dialog does not mention the match:\n%s", h.view())
	}
	h.press("ctrl+u", "ctrl+k")
	h.typeText("content-renamed.txt", false)
	h.press("enter")
	h.settle()
	renamed := filepath.Join(env.src, "content-renamed.txt")
	if !fileExists(renamed) || fileExists(path) {
		t.Fatal("the content result was not renamed on disk")
	}
	if len(v.contentResults) != 1 || filepath.Clean(v.contentResults[0].Path) != renamed ||
		len(v.contentResults[0].Matches) != 2 || v.contentResults[0].Matches[0].Line != 2 {
		t.Fatalf("the content result did not follow the rename: %+v", v.contentResults)
	}
	// and copy path names the new path
	buf := captureClipboard(t)
	advPick(t, h, "y")
	if want := base64.StdEncoding.EncodeToString([]byte(renamed)); !strings.Contains(buf.String(), want) {
		t.Fatalf("clipboard %q does not contain %q", buf.String(), want)
	}
	h.press("esc")
}

// ---- copy / move ---------------------------------------------------------------------

func TestAdvancedActionCopyKeepsTheResultAndThePanels(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	path := filepath.Join(env.src, "a.txt")
	p := h.a.ActivePanel()
	cursor, sel := p.Cursor(), p.Selection()
	otherPath, _ := h.otherPanelState()
	v := advOpenIndexed(t, h)
	advSelect(t, h, path)
	selected := v.cursor
	advPick(t, h, "c")
	h.waitDialog(dlgConfirm)
	view := strings.Join(strings.Fields(h.view()), " ")
	if !strings.Contains(view, `Copy file "a.txt"?`) || !strings.Contains(view, "From:") || !strings.Contains(view, "To:") ||
		!strings.Contains(view, "/src") || !strings.Contains(view, "/dst") {
		t.Fatalf("copy confirmation is wrong:\n%s", view)
	}
	h.press("enter")
	h.idle()
	h.settle()
	if got := readFile(t, filepath.Join(env.dst, "a.txt")); got != "new-a" {
		t.Fatalf("copy produced %q", got)
	}
	if !fileExists(path) {
		t.Fatal("copy removed the source")
	}
	if h.a.advancedSearch != v || v.cursor != selected {
		t.Fatal("the copy closed or changed the results")
	}
	if p.Cursor() != cursor || len(p.Selection()) != len(sel) {
		t.Fatal("the copy changed the source panel's cursor or selection")
	}
	// the destination panel stays on its folder and, like c in the panels,
	// puts its cursor on the copied file
	other := h.a.panels[1]
	h.until("destination panel", func() bool {
		e, ok := other.Current()
		return other.Path() == otherPath && !other.Loading() && ok && e.Name == "a.txt"
	})
	if st := h.status(); !strings.Contains(st, "Copied 1 file") {
		t.Fatalf("status %q", st)
	}
	// the same result can still be acted on
	advMenu(t, h)
	h.press("esc")
	h.press("esc")
}

func TestAdvancedActionMovePrunesTheResult(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	path := filepath.Join(env.src, "b.txt")
	v := advOpenIndexed(t, h)
	advSelect(t, h, path)
	if v.previewPath != path {
		t.Fatalf("preview path %q, want %q", v.previewPath, path)
	}
	advPick(t, h, "m")
	h.waitDialog(dlgConfirm)
	h.press("enter")
	h.idle()
	h.until("moved", func() bool { return !fileExists(path) && fileExists(filepath.Join(env.dst, "b.txt")) })
	if got := readFile(t, filepath.Join(env.dst, "b.txt")); got != "new-b" {
		t.Fatalf("move produced %q", got)
	}
	h.settle()
	if h.a.advancedSearch != v {
		t.Fatal("the move closed the search")
	}
	for _, index := range v.filenameResults {
		if index >= 0 && index < len(v.entries) && filepath.Clean(v.entries[index].Path) == path {
			t.Fatal("the moved file is still listed")
		}
	}
	if v.previewPath == path {
		t.Fatal("the preview still shows the moved file")
	}
	if v.cursor < 0 || (len(v.contentResults) > 0 && v.cursor >= len(v.contentResults)) {
		t.Fatalf("cursor %d was not clamped", v.cursor)
	}
	h.press("esc")
}

func TestAdvancedActionCopyCancelAndFailure(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	path := filepath.Join(env.src, "a.txt")
	advOpenIndexed(t, h)
	advSelect(t, h, path)
	advPick(t, h, "c")
	h.waitDialog(dlgConfirm)
	h.press("esc") // cancel
	if h.a.dialog != nil {
		t.Fatal("Esc did not close the copy confirmation")
	}
	if fileExists(filepath.Join(env.dst, "a.txt")) {
		t.Fatal("a cancelled copy still wrote the file")
	}
	if h.a.advancedSearch == nil {
		t.Fatal("the cancelled copy closed the search")
	}
	if st := h.status(); !strings.Contains(st, "nothing was transferred") {
		t.Fatalf("status %q", st)
	}

	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only destination would still be writable")
	}
	if err := os.Chmod(env.dst, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(env.dst, 0o755)
	advPick(t, h, "c")
	h.waitDialog(dlgConfirm)
	h.press("enter")
	h.idle()
	h.settle()
	if fileExists(filepath.Join(env.dst, "a.txt")) {
		t.Fatal("the file was copied into a read-only folder")
	}
	if st, isErr := h.a.Status(); !isErr || !strings.Contains(st, "error") {
		t.Fatalf("failure not reported: %q (err=%v)", st, isErr)
	}
	if h.a.advancedSearch == nil || !fileExists(path) {
		t.Fatal("the failed copy damaged the source or the search")
	}
	h.press("esc")
}

// ---- rename ----------------------------------------------------------------------------

func TestAdvancedActionRenameUpdatesTheResult(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	path := filepath.Join(env.src, "report.txt")
	mustWrite(t, path, "data\n")
	v := advOpenIndexed(t, h)
	advSelect(t, h, path)
	advPick(t, h, "r")
	if !h.dialogKind(dlgRename) {
		t.Fatalf("rename dialog missing (dialog %+v)", h.a.dialog)
	}
	if got := h.a.dialog.input.Value(); got != "report.txt" {
		t.Fatalf("rename field starts with %q", got)
	}
	view := strings.Join(strings.Fields(h.view()), " ")
	if !strings.Contains(view, "In folder:") || !strings.Contains(view, "Current name: report.txt") {
		t.Fatalf("rename dialog:\n%s", view)
	}
	h.press("ctrl+u", "ctrl+k") // clear the prefilled name (cursor sits before ".txt")
	h.typeText("renamed.txt", false)
	h.press("enter")
	h.settle()
	renamed := filepath.Join(env.src, "renamed.txt")
	if !fileExists(renamed) || fileExists(path) {
		t.Fatal("the file was not renamed on disk")
	}
	if st := h.status(); !strings.Contains(st, "Renamed") {
		t.Fatalf("status %q", st)
	}
	if h.a.advancedSearch != v {
		t.Fatal("renaming closed the search")
	}
	found := false
	for _, index := range v.filenameResults {
		if index >= 0 && index < len(v.entries) && filepath.Clean(v.entries[index].Path) == renamed {
			found = true
		}
		if index >= 0 && index < len(v.entries) && filepath.Clean(v.entries[index].Path) == path {
			t.Fatal("the old name is still listed")
		}
	}
	if !found {
		t.Fatalf("the renamed file is missing from the results: %+v", v.entries)
	}
	if v.previewPath != renamed {
		t.Fatalf("preview path %q, want %q", v.previewPath, renamed)
	}
	h.until("preview", func() bool { return !v.previewLoading })
	if v.previewDoc == nil || len(v.previewDoc.Lines) == 0 || v.previewDoc.Lines[0] != "data" {
		t.Fatalf("preview did not reload the renamed file: %+v", v.previewDoc)
	}
	h.press("esc")
}

func TestAdvancedActionRenameCancellationAndConflict(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	path := filepath.Join(env.src, "a.txt")
	mustWrite(t, filepath.Join(env.src, "taken.txt"), "other\n")
	advOpenIndexed(t, h)
	advSelect(t, h, path)
	// Esc cancels
	advPick(t, h, "r")
	h.press("esc")
	if h.a.dialog != nil || !fileExists(path) {
		t.Fatal("cancelling the rename changed the file")
	}
	if h.a.advancedSearch == nil {
		t.Fatal("cancelling the rename closed the search")
	}
	// an existing name is reported in the dialog and nothing changes
	advPick(t, h, "r")
	h.press("ctrl+u", "ctrl+k")
	h.typeText("taken.txt", false)
	h.press("enter")
	if !h.dialogKind(dlgRename) || h.a.dialog.errLine == "" {
		t.Fatalf("rename conflict not reported: %+v", h.a.dialog)
	}
	if !fileExists(path) || readFile(t, filepath.Join(env.src, "taken.txt")) != "other\n" {
		t.Fatal("a refused rename changed the files")
	}
	h.press("esc")
	// an empty name is refused by the input and keeps the dialog
	advPick(t, h, "r")
	h.press("ctrl+u", "ctrl+k")
	h.press("enter")
	if !h.dialogKind(dlgRename) || !fileExists(path) {
		t.Fatalf("empty rename: dialog=%+v", h.a.dialog)
	}
	h.press("esc")
	h.press("esc")
}

func TestAdvancedActionRenameToTheSameNameIsHarmless(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	path := filepath.Join(env.src, "a.txt")
	advOpenIndexed(t, h)
	advSelect(t, h, path)
	advPick(t, h, "r")
	h.press("enter") // unchanged name
	if h.a.dialog != nil {
		t.Fatal("dialog stayed open")
	}
	if !fileExists(path) || h.status() != "name unchanged" {
		t.Fatalf("status %q", h.status())
	}
	if h.a.advancedSearch == nil {
		t.Fatal("the search closed")
	}
	h.press("esc")
}

// ---- delete ---------------------------------------------------------------------------

func TestAdvancedActionDeleteUsesTheTrashAndPrunesTheResult(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	path := filepath.Join(env.src, "a.txt")
	v := advOpenIndexed(t, h)
	advSelect(t, h, path)
	advPick(t, h, "d")
	h.waitDialog(dlgDelete)
	view := strings.Join(strings.Fields(h.view()), " ")
	if !strings.Contains(view, "Move 1 file to the trash?") || !strings.Contains(view, "a.txt") || !strings.Contains(view, "Size:") {
		t.Fatalf("delete dialog:\n%s", view)
	}
	h.press("y")
	h.idle()
	h.until("deleted", func() bool { return !fileExists(path) })
	h.settle()
	if readFile(t, filepath.Join(env.trash, "files", "a.txt")) != "new-a" {
		t.Fatal("the file did not go to the trash")
	}
	if h.a.advancedSearch != v {
		t.Fatal("the delete closed the search")
	}
	for _, index := range v.filenameResults {
		if index >= 0 && index < len(v.entries) && filepath.Clean(v.entries[index].Path) == path {
			t.Fatal("the deleted file is still listed")
		}
	}
	if st := h.status(); !strings.Contains(st, "trash") {
		t.Fatalf("status %q", st)
	}
	h.press("esc")
}

func TestAdvancedActionDeleteCancelKeepsEverything(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	path := filepath.Join(env.src, "b.txt")
	advOpenIndexed(t, h)
	advSelect(t, h, path)
	advPick(t, h, "d")
	h.waitDialog(dlgDelete)
	h.press("n") // default is no
	if h.a.dialog != nil || !fileExists(path) {
		t.Fatal("cancelling the delete removed the file")
	}
	if !fileExists(path) || h.a.advancedSearch == nil {
		t.Fatal("cancel damaged the state")
	}
	h.press("esc")
}

func TestAdvancedActionDeleteFolderResult(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	dir := filepath.Join(env.src, "sub")
	v := advOpenIndexed(t, h)
	advSelect(t, h, dir)
	advPick(t, h, "d")
	h.waitDialog(dlgDelete)
	view := strings.Join(strings.Fields(h.view()), " ")
	if !strings.Contains(view, "folder") {
		t.Fatalf("folder delete dialog:\n%s", view)
	}
	h.press("y")
	h.idle()
	h.until("folder deleted", func() bool { return !fileExists(dir) })
	h.settle()
	for _, index := range v.filenameResults {
		if index >= 0 && index < len(v.entries) && filepath.Clean(v.entries[index].Path) == dir {
			t.Fatal("the deleted folder is still listed")
		}
	}
	h.press("esc")
}

// ---- copy path -----------------------------------------------------------------------

func TestAdvancedActionCopyPathWritesOSC52(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	buf := captureClipboard(t)
	path := filepath.Join(env.src, "a.txt")
	advOpenIndexed(t, h)
	advSelect(t, h, path)
	advPick(t, h, "y")
	if h.a.dialog != nil {
		t.Fatal("copy path left a dialog open")
	}
	want := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(path)) + "\x07"
	if got := buf.String(); got != want {
		t.Fatalf("clipboard sequence %q, want %q", got, want)
	}
	if st := h.status(); !strings.Contains(st, "copied") || !strings.Contains(st, "a.txt") || !strings.Contains(st, "clipboard") {
		t.Fatalf("status %q does not report the copy", st)
	}
	if h.a.advancedSearch == nil {
		t.Fatal("copy path closed the search")
	}
	// a broken terminal writer is reported, not fatal
	clipboardOut = failingWriter{}
	advPick(t, h, "y")
	if st, isErr := h.a.Status(); !isErr || !strings.Contains(st, "could not copy") {
		t.Fatalf("write failure not reported: %q (err=%v)", st, isErr)
	}
	h.press("esc")
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("terminal gone") }

// ---- failures and unusual results -----------------------------------------------------

func TestAdvancedActionsOnVanishedResult(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	path := filepath.Join(env.src, "a.txt")
	v := advOpenIndexed(t, h)
	advSelect(t, h, path)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	// jump, copy, move and delete check the result before acting and leave
	// the results and the panels alone
	for _, key := range []string{"t", "c", "m", "d"} {
		advMenu(t, h)
		h.press(key)
		if h.a.dialog != nil {
			t.Fatalf("%q opened a dialog for a vanished file", key)
		}
		if h.a.advancedSearch != v {
			t.Fatalf("%q closed the search", key)
		}
		if st := h.status(); !strings.Contains(st, "no longer available") {
			t.Fatalf("%q: status %q", key, st)
		}
		if p := h.a.ActivePanel(); p.Path() != env.src {
			t.Fatalf("%q navigated the panel to %q", key, p.Path())
		}
	}
	// inspecting and renaming report it too, without crashing
	h.press("enter")
	if h.a.viewer != nil || !strings.Contains(h.status(), "no longer available") {
		t.Fatalf("inspect on a vanished file: viewer=%v status=%q", h.a.viewer, h.status())
	}
	advPick(t, h, "r")
	if !h.dialogKind(dlgRename) {
		t.Fatalf("rename dialog missing: %+v", h.a.dialog)
	}
	h.press("ctrl+u", "ctrl+k")
	h.typeText("other.txt", false)
	h.press("enter")
	if !h.dialogKind(dlgRename) || h.a.dialog.errLine == "" {
		t.Fatalf("rename of a vanished file was accepted: %+v", h.a.dialog)
	}
	if fileExists(filepath.Join(env.src, "other.txt")) {
		t.Fatal("renaming a vanished file created something")
	}
	h.press("esc")
	if h.a.advancedSearch != v {
		t.Fatal("the search closed")
	}
	h.press("esc")
}

func TestAdvancedActionsOnUnreadableResult(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read anything")
	}
	h, env := newOpsApp(t, nil, nil)
	path := filepath.Join(env.src, "secret.txt")
	mustWrite(t, path, "hidden\n")
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(path, 0o644)
	advOpenIndexed(t, h)
	advSelect(t, h, path)
	// inspect shows the read error inside the viewer instead of crashing
	h.press("enter")
	h.settle()
	if h.a.viewer == nil {
		t.Fatal("inspect did not open for an unreadable file")
	}
	if h.a.viewer.doc != nil || h.a.viewer.err == nil {
		t.Fatalf("unreadable file loaded without an error: %+v", h.a.viewer)
	}
	h.press("esc")
	// the metadata actions still work: jump selects it in its folder
	advPick(t, h, "t")
	p := h.a.ActivePanel()
	h.until("parent folder", func() bool { return p.Path() == env.src && !p.Loading() })
	h.until("selection", func() bool {
		e, ok := p.Current()
		return ok && e.Path == path
	})
	if h.status() == "" {
		t.Fatal("jump reported nothing")
	}
}

// ---- scope: the shortcuts only exist inside Advanced Search ----------------------------

func TestAdvancedActionsNeverFireOutsideAdvancedSearch(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	p := h.a.ActivePanel()
	// 1. normal panels: Ctrl+A does nothing at all
	path, cursor := p.Path(), p.Cursor()
	h.press("ctrl+a")
	if h.a.dialog != nil || h.a.advancedSearch != nil || h.a.search != nil {
		t.Fatalf("Ctrl+A in the panels did something: dialog=%v advanced=%v", h.a.dialog, h.a.advancedSearch)
	}
	if p.Path() != path || p.Cursor() != cursor || len(p.Selection()) != 0 {
		t.Fatal("Ctrl+A changed the panel")
	}
	// the normal panel shortcuts still work
	h.focus("a.txt")
	h.press("c")
	if !h.dialogKind(dlgConfirm) {
		t.Fatal("c no longer copies from the panels")
	}
	h.press("esc")
	h.press("M")
	if !h.dialogKind(dlgMenu) || strings.Contains(h.view(), "Actions ·") {
		t.Fatalf("M no longer opens the panel menu:\n%s", h.view())
	}
	h.press("esc")

	// 2. the live filename search
	h.press("/")
	if h.a.search == nil {
		t.Fatal("/ no longer opens the live search")
	}
	h.typeText("a", false)
	before := h.a.search.input.Value()
	h.press("ctrl+a")
	if h.a.dialog != nil || h.a.advancedSearch != nil {
		t.Fatal("Ctrl+A inside the live search opened the Advanced Search menu")
	}
	if got := h.a.search.input.Value(); got != before {
		t.Fatalf("Ctrl+A changed the live query: %q", got)
	}
	h.press("esc")
	if h.a.search != nil {
		t.Fatal("Esc no longer closes the live search")
	}

	// 3. the viewer
	h.focus("a.txt")
	h.press("v")
	if h.a.viewer == nil {
		t.Fatal("v no longer opens the viewer")
	}
	h.press("ctrl+a")
	if h.a.dialog != nil || h.a.advancedSearch != nil || h.a.viewer == nil {
		t.Fatal("Ctrl+A inside the viewer did something")
	}
	h.press("esc")

	// 4. a dialog (rename) keeps its own keys
	h.press("r")
	if !h.dialogKind(dlgRename) {
		t.Fatal("r no longer renames from the panels")
	}
	h.press("ctrl+a")
	if !h.dialogKind(dlgRename) || strings.Contains(h.view(), "Actions ·") {
		t.Fatal("Ctrl+A replaced the rename dialog")
	}
	h.press("esc")

	// 5. the help screen
	h.press("?")
	if !h.a.showHelp {
		t.Fatal("? no longer opens help")
	}
	h.press("ctrl+a")
	if h.a.dialog != nil || h.a.advancedSearch != nil {
		t.Fatal("Ctrl+A inside help opened the actions menu")
	}
	h.press("esc")

	// 6. the editor is an external program: no key reaches the app
	h.focus("a.txt")
	h.press("e")
	h.settle()
	if len(env.execs) != 1 || h.a.dialog != nil || h.a.advancedSearch != nil {
		t.Fatalf("the editor shortcut no longer works: %+v", env.execs)
	}
}

func TestAdvancedActionsKeysDoNotLeakIntoTheQuery(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	v := advOpenIndexed(t, h)
	// every action letter is still a plain character while the menu is closed
	h.typeText("cedmtroyi", false)
	if got := v.input.Value(); got != "cedmtroyi" {
		t.Fatalf("action letters were intercepted as shortcuts: %q", got)
	}
	if h.a.dialog != nil {
		t.Fatal("typing opened a dialog")
	}
	if !strings.Contains(h.view(), "Advanced Search · Filename") {
		t.Fatal("typing closed the overlay")
	}
	h.press("esc")
}

func TestAdvancedSearchExistingShortcutsStillWorkWithActions(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	path := filepath.Join(env.src, "content.txt")
	mustWrite(t, path, strings.Repeat("line\n", 40)+"needle\n"+strings.Repeat("tail\n", 40))
	v := advOpenIndexed(t, h)
	query := v.input.Value()
	if query != "" {
		t.Fatalf("unexpected query %q", query)
	}
	// typing still goes to the query
	h.typeText("rep", false)
	if v.input.Value() != "rep" {
		t.Fatalf("typing broke: %q", v.input.Value())
	}
	// cursor movement still works
	h.until("filter", func() bool { return !v.filtering })
	h.press("down", "up")
	// and the query can be cleared again
	h.press("ctrl+u", "ctrl+k")
	h.until("unfiltered", func() bool { return !v.filtering && v.input.Value() == "" })
	// Ctrl+T still switches mode and back
	h.press("ctrl+t")
	if v.mode != advancedContentMode {
		t.Fatal("Ctrl+T no longer switches to content search")
	}
	h.press("ctrl+t")
	if v.mode != advancedFilenameMode {
		t.Fatal("Ctrl+T no longer switches back")
	}
	h.until("back to filenames", func() bool { return !v.filtering && v.input.Value() == "" })
	// Help still opens over the overlay
	h.press("?")
	if !h.a.showHelp || h.a.advancedSearch == nil {
		t.Fatal("? no longer opens help from Advanced Search")
	}
	h.press("esc")
	if h.a.showHelp || h.a.advancedSearch != v {
		t.Fatal("Esc did not close help and keep the results")
	}
	// preview scrolling with the wheel still works
	h.a.height = 30
	h.a.width = 100
	if v.previewDoc != nil {
		v.previewOffset = 0
	}
	h.wheel(90, 10, 3) // over the preview column in the wide layout
	if v.previewOffset < 0 || v.previewOffset > 1<<20 {
		t.Fatalf("preview offset %d", v.previewOffset)
	}
	// inspection, match navigation and returning still work
	advSelect(t, h, path)
	h.press("enter")
	h.settle()
	if h.a.viewer == nil {
		t.Fatal("Enter no longer inspects")
	}
	h.press("n")
	h.press("esc")
	if h.a.viewer != nil || h.a.advancedSearch != v {
		t.Fatal("returning from inspection broke")
	}
	// Esc still closes and cancels the workers
	h.press("esc")
	if h.a.advancedSearch != nil || v.ctx.Err() == nil {
		t.Fatal("Esc no longer closes Advanced Search and cancels its workers")
	}
}

func TestAdvancedActionContentMoveAndDeletePruneMatches(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	first := filepath.Join(env.src, "first.txt")
	second := filepath.Join(env.src, "second.txt")
	mustWrite(t, first, "needle one\n")
	mustWrite(t, second, "needle two\n")
	v := advContent(t, h, "needle",
		search.ContentResult{Path: first, Rel: "first.txt", Matches: []search.ContentMatch{{Line: 1, Text: "needle one"}}},
		search.ContentResult{Path: second, Rel: "second.txt", Matches: []search.ContentMatch{{Line: 1, Text: "needle two"}}},
	)
	// move the first match to the other panel: it leaves the list
	advPick(t, h, "m")
	h.waitDialog(dlgConfirm)
	view := strings.Join(strings.Fields(h.view()), " ")
	if !strings.Contains(view, "From:") || !strings.Contains(view, "/src") {
		t.Fatalf("content move confirmation:\n%s", view)
	}
	h.press("enter")
	h.idle()
	h.until("moved", func() bool { return !fileExists(first) && fileExists(filepath.Join(env.dst, "first.txt")) })
	h.settle()
	if h.a.advancedSearch != v || len(v.contentResults) != 1 || filepath.Clean(v.contentResults[0].Path) != second {
		t.Fatalf("the moved match is still listed: %+v", v.contentResults)
	}
	// delete the remaining match: the list becomes empty, not broken
	advPick(t, h, "d")
	h.waitDialog(dlgDelete)
	h.press("y")
	h.idle()
	h.until("deleted", func() bool { return !fileExists(second) })
	h.settle()
	if h.a.advancedSearch != v || len(v.contentResults) != 0 {
		t.Fatalf("the deleted match is still listed: %+v", v.contentResults)
	}
	if v.previewPath != "" {
		t.Fatalf("the empty result list still previews %q", v.previewPath)
	}
	if view := h.view(); !strings.Contains(view, "Advanced Search · Content") || strings.Contains(view, "<nil>") {
		t.Fatalf("the empty content list does not render:\n%s", view)
	}
	// Ctrl+A on an empty list refuses politely instead of opening a menu
	h.press("ctrl+a")
	if h.a.dialog != nil {
		t.Fatalf("Ctrl+A on an empty list opened %+v", h.a.dialog)
	}
	if st := h.status(); h.a.statusLevel != lvlWarn || !strings.Contains(st, "No content match selected") {
		t.Fatalf("status %q level %v", st, h.a.statusLevel)
	}
	if h.a.advancedSearch != v {
		t.Fatal("the refused action closed the search")
	}
	h.press("esc")
}

func TestAdvancedPreviewScrollingStillWorksWithActions(t *testing.T) {
	h, v := advancedPreviewApp(t, 300)
	h.a.width, h.a.height = 100, 30
	layout := h.a.advancedLayout(27)
	if !layout.wide || layout.previewH <= 0 {
		t.Fatalf("expected a side-by-side preview, got %+v", layout)
	}
	before := v.previewOffset
	h.wheel(layout.resultsW+3, layout.previewTop+2, 6)
	if v.previewOffset <= before {
		t.Fatalf("the wheel did not scroll the preview (%d → %d)", before, v.previewOffset)
	}
	// the actions menu neither scrolls the preview nor closes the search
	offset, cursor := v.previewOffset, v.cursor
	advMenu(t, h)
	h.press("esc")
	if v.previewOffset != offset || v.cursor != cursor {
		t.Fatalf("closing the menu moved the preview (%d → %d) or cursor", offset, v.previewOffset)
	}
	// and the wheel still scrolls after the menu was used for an inspection
	advPick(t, h, "i")
	h.settle()
	h.press("esc")
	offset = v.previewOffset
	h.wheel(layout.resultsW+3, layout.previewTop+2, 6)
	if v.previewOffset <= offset {
		t.Fatalf("the wheel stopped scrolling after inspection (%d → %d)", offset, v.previewOffset)
	}
	h.press("esc")
	if h.a.advancedSearch != nil {
		t.Fatal("Esc did not close the search")
	}
}

// ---- cancellation and focus hand-off ---------------------------------------------------

func TestAdvancedActionsMenuKeepsFocusInsideTheOverlay(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	path := filepath.Join(env.src, "a.txt")
	v := advOpenIndexed(t, h)
	advSelect(t, h, path)
	selected := v.cursor
	// a dialog opened by an action hands the keyboard to the dialog, and
	// closing it returns to the same results
	advPick(t, h, "c")
	h.waitDialog(dlgConfirm)
	h.press("esc")
	if h.a.advancedSearch != v || v.cursor != selected {
		t.Fatal("cancelling the action lost the results")
	}
	// the menu can be opened from the content mode as well, and Esc from the
	// viewer returns to the cards, not to the panels
	advPick(t, h, "i")
	h.settle()
	h.press("esc")
	if h.a.viewer != nil || h.a.advancedSearch != v {
		t.Fatal("inspection did not return to the results")
	}
	if h.a.ActivePanel().Path() != env.src {
		t.Fatal("the panel moved during inspection")
	}
	// Ctrl+C still quits from the overlay
	h.press("ctrl+c")
	if !h.quit && !h.a.quitting {
		t.Fatal("Ctrl+C no longer quits from Advanced Search")
	}
}

func TestAdvancedActionsWorkAfterMultipleSearches(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	path := filepath.Join(env.src, "a.txt")
	// first search: close it
	v1 := advOpenIndexed(t, h)
	advSelect(t, h, path)
	h.press("esc")
	if h.a.advancedSearch != nil || v1.ctx.Err() == nil {
		t.Fatal("the first search did not close")
	}
	// second search: the actions belong to it
	v2 := advOpenIndexed(t, h)
	if v2 == v1 {
		t.Fatal("the second search reused the first view")
	}
	advSelect(t, h, path)
	capture := captureClipboard(t)
	advPick(t, h, "y")
	if !strings.Contains(capture.String(), base64.StdEncoding.EncodeToString([]byte(path))) {
		t.Fatal("copy path did not run on the second search")
	}
	if h.a.advancedSearch != v2 {
		t.Fatal("the action attached to a stale search")
	}
	h.press("esc")
}

// ---- the previous Advanced Search behaviour is untouched -------------------------------

func TestAdvancedSearchPanelStateStillUntouchedByActions(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	p := h.a.ActivePanel()
	h.focus("a.txt")
	p.ToggleSelect()
	other := h.a.panels[1]
	mustWrite(t, filepath.Join(env.dst, "held.txt"), "keep")
	h.exec(other.RefreshFocus("held.txt"))
	h.until("right selection", func() bool {
		e, ok := other.Current()
		return !other.Loading() && ok && e.Name == "held.txt"
	})
	other.ToggleSelect()
	beforePath, beforeCursor := p.Path(), p.Cursor()
	beforeSel := p.Selection()
	otherPath, otherCursor, otherSel := other.Path(), other.Cursor(), other.Selection()

	advOpenIndexed(t, h)
	advSelect(t, h, filepath.Join(env.src, "b.txt"))
	// inspect, then return; then open and close the menu
	h.press("enter")
	h.settle()
	h.press("esc")
	advMenu(t, h)
	h.press("esc")
	h.press("esc")
	if h.a.advancedSearch != nil {
		t.Fatal("the overlay did not close")
	}
	if p.Path() != beforePath || p.Cursor() != beforeCursor || len(p.Selection()) != len(beforeSel) {
		t.Fatal("the left panel state changed")
	}
	if other.Path() != otherPath || other.Cursor() != otherCursor || len(other.Selection()) != len(otherSel) {
		t.Fatal("the right panel state changed")
	}
}
