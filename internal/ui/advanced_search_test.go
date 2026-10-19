package ui

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/search"
)

func TestAdvancedSearchOpensFromQuickSearchAndPreservesPanelState(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	p := h.a.ActivePanel()
	h.focus("a.txt")
	p.ToggleSelect()
	other := h.a.panels[1]
	if err := os.WriteFile(filepath.Join(env.dst, "held.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.exec(other.RefreshFocus("held.txt"))
	h.until("right-pane selection", func() bool {
		entry, ok := other.Current()
		return !other.Loading() && ok && entry.Name == "held.txt"
	})
	other.ToggleSelect()
	beforePath, beforeCursor := p.Path(), p.Cursor()
	beforeSelection := p.Selection()
	otherPath, otherCursor, otherSelection := other.Path(), other.Cursor(), other.Selection()
	h.press("/")
	h.typeText("report", false)
	if h.a.search == nil {
		t.Fatal("live filename search did not open")
	}
	h.press("ctrl+g")
	if h.a.search != nil || h.a.advancedSearch == nil {
		t.Fatalf("quick search was not replaced: search=%v advanced=%v", h.a.search, h.a.advancedSearch)
	}
	if got := h.a.advancedSearch.input.Value(); got != "report" {
		t.Fatalf("query %q was not carried to Advanced Search", got)
	}
	if h.a.advancedSearch.root != env.src || h.a.advancedSearch.mode != advancedFilenameMode {
		t.Fatalf("advanced state: root=%q mode=%v", h.a.advancedSearch.root, h.a.advancedSearch.mode)
	}
	if p.Path() != beforePath || p.Cursor() != beforeCursor || !reflect.DeepEqual(p.Selection(), beforeSelection) ||
		other.Path() != otherPath || other.Cursor() != otherCursor || !reflect.DeepEqual(other.Selection(), otherSelection) {
		t.Fatalf("panel state changed on entry: left=%q/%d/%v right=%q/%d/%v", p.Path(), p.Cursor(), p.Selection(), other.Path(), other.Cursor(), other.Selection())
	}
	h.press("esc")
	if h.a.advancedSearch != nil || p.Path() != beforePath || p.Cursor() != beforeCursor || !reflect.DeepEqual(p.Selection(), beforeSelection) ||
		other.Path() != otherPath || other.Cursor() != otherCursor || !reflect.DeepEqual(other.Selection(), otherSelection) {
		t.Fatal("closing Advanced Search did not preserve both panels' state")
	}
}

func TestAdvancedSearchTogglePreservesQueryAndCancelsWorkers(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.press("/")
	h.press("ctrl+g")
	v := h.a.advancedSearch
	if v == nil {
		t.Fatal("advanced search did not open")
	}
	h.typeText("needle", false)
	query := v.input.Value()
	h.press("ctrl+t")
	if v.mode != advancedContentMode || v.input.Value() != query {
		t.Fatalf("content toggle: mode=%v query=%q", v.mode, v.input.Value())
	}
	h.press("ctrl+t")
	if v.mode != advancedFilenameMode || v.input.Value() != query {
		t.Fatalf("filename toggle: mode=%v query=%q", v.mode, v.input.Value())
	}
	if v.contentCtx == nil || v.contentCtx.Err() == nil {
		t.Fatal("switching back to filenames did not cancel the content worker")
	}
	h.press("esc")
	if v.ctx.Err() == nil {
		t.Fatal("closing Advanced Search did not cancel its workers")
	}
}

func TestAdvancedSearchContentInspectionReturnsToResults(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	path := filepath.Join(env.src, "content.txt")
	if err := os.WriteFile(path, []byte("first line\nneedle one\nneedle two\nlast\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	active := h.a.ActivePanel()
	h.focus("a.txt")
	active.ToggleSelect()
	other := h.a.panels[1]
	if err := os.WriteFile(filepath.Join(env.dst, "held.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.exec(other.RefreshFocus("held.txt"))
	h.until("inspection right-pane selection", func() bool {
		entry, ok := other.Current()
		return !other.Loading() && ok && entry.Name == "held.txt"
	})
	other.ToggleSelect()
	h.press("/")
	h.press("ctrl+g")
	v := h.a.advancedSearch
	h.press("ctrl+t")
	h.typeText("needle", false)
	if v.contentCancel != nil {
		v.contentCancel()
	}
	v.contentGeneration++
	v.contentLoading = false
	v.contentResults = []search.ContentResult{{
		Path: path, Rel: "content.txt", Matches: []search.ContentMatch{{Line: 2, Text: "needle one"}, {Line: 3, Text: "needle two"}},
	}}
	v.cursor = 0
	beforePath, beforeCursor, beforeSelection := active.Path(), active.Cursor(), active.Selection()
	otherPath, otherCursor, otherSelection := other.Path(), other.Cursor(), other.Selection()
	h.press("enter")
	if h.a.viewer == nil || !h.a.viewer.fromAdvanced {
		t.Fatal("Enter did not open a read-only search inspection")
	}
	h.settle()
	if h.a.viewer.doc == nil || h.a.viewer.query != "needle" || h.a.viewer.mcur < 0 {
		t.Fatalf("inspection did not seed content matches: %+v", h.a.viewer)
	}
	if got := h.a.viewer.doc.Lines[h.a.viewer.matches[h.a.viewer.mcur]]; got != "needle one" {
		t.Fatalf("inspection started at %q", got)
	}
	h.press("n")
	if got := h.a.viewer.doc.Lines[h.a.viewer.matches[h.a.viewer.mcur]]; got != "needle two" {
		t.Fatalf("n did not advance to next match: %q", got)
	}
	h.press("N")
	if got := h.a.viewer.doc.Lines[h.a.viewer.matches[h.a.viewer.mcur]]; got != "needle one" {
		t.Fatalf("N did not move to previous match: %q", got)
	}
	h.press("?")
	readOnlyHelp := false
	for _, column := range h.a.helpColumns() {
		for _, section := range column {
			if section.Title == "Read-only search inspection" {
				readOnlyHelp = true
			}
		}
	}
	if !readOnlyHelp {
		t.Fatal("viewer help offers editing instead of read-only search-inspection controls")
	}
	h.press("esc")
	h.press("e")
	if h.a.viewer == nil || !h.a.viewer.fromAdvanced {
		// fromAdvanced remains true; e is ignored rather than launching an editor.
		t.Fatal("read-only inspection was unexpectedly closed")
	}
	h.press("esc")
	if h.a.viewer != nil || h.a.advancedSearch != v || v.input.Value() != "needle" {
		t.Fatal("Escape did not return to the same advanced search state")
	}
	if active.Path() != beforePath || active.Cursor() != beforeCursor || !reflect.DeepEqual(active.Selection(), beforeSelection) ||
		other.Path() != otherPath || other.Cursor() != otherCursor || !reflect.DeepEqual(other.Selection(), otherSelection) {
		t.Fatal("inspection changed one of the panels' state")
	}
	h.press("esc")
}

func TestAdvancedSearchEmptyAndNarrowLayouts(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.press("/")
	h.press("ctrl+g")
	for _, tc := range []struct{ width, height int }{{30, 9}, {42, 12}, {79, 10}, {100, 8}, {30, 7}} {
		h.a.width, h.a.height = tc.width, tc.height
		view := h.a.View()
		if view == "" || strings.Contains(view, "<nil>") {
			t.Fatalf("invalid view at %dx%d: %q", tc.width, tc.height, view)
		}
		if got := ansi.StringWidth(strings.Split(view, "\n")[1]); got > tc.width {
			t.Errorf("line width %d exceeds %d at %dx%d", got, tc.width, tc.width, tc.height)
		}
	}
	h.press("esc")
}

func TestAdvancedSearchHelpDocumentsControls(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.press("/")
	h.press("ctrl+g")
	h.press("?")
	h.press("/")
	h.typeText(".git", false)
	view := h.view()
	if !strings.Contains(view, ".git") || !strings.Contains(view, "skipped") {
		t.Fatalf("Advanced Search .git behavior is missing from help:\n%s", view)
	}
	sections := h.a.helpColumns()
	foundRoute := false
	for _, column := range sections {
		for _, section := range column {
			if section.Title == "Search workflow" {
				foundRoute = true
			}
		}
	}
	if !foundRoute {
		t.Fatal("the existing / to Advanced Search route is missing from help")
	}
	h.press("esc", "esc")
	if h.a.showHelp || h.a.advancedSearch == nil {
		t.Fatal("closing help also closed Advanced Search")
	}
	h.press("esc")
}

func TestAdvancedSearchLateEventsAreIgnored(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.press("/")
	h.press("ctrl+g")
	v := h.a.advancedSearch
	id := v.id
	h.press("esc")
	_, cmd := h.a.Update(advancedSearchMsg{id: id, event: advancedSearchEvent{kind: advancedIndexEvent, index: search.IndexUpdate{Done: true}}})
	if cmd != nil || h.a.advancedSearch != nil {
		t.Fatal("stale search event reopened an overlay")
	}
}

func TestAdvancedContentPreviewFollowsSelectionAndHighlightsMatches(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.press("/")
	h.press("ctrl+g")
	v := h.a.advancedSearch
	v.mode = advancedContentMode
	v.input = newLineInput("needle")
	v.contentResults = []search.ContentResult{
		{Path: "/tmp/first.txt", Rel: "first.txt", Matches: []search.ContentMatch{{Line: 4, Text: "first needle match"}}},
		{Path: "/tmp/second.txt", Rel: "second.txt", Matches: []search.ContentMatch{{Line: 9, Text: "second needle match"}}},
	}
	v.cursor = 0
	first := h.a.advancedPreviewRow(1, 40, 3)
	if !strings.Contains(ansi.Strip(first), "first needle match") || !strings.Contains(first, h.a.theme.Match.Render("needle")) {
		t.Fatalf("first selected match was not previewed/highlighted: %q", first)
	}
	h.a.setAdvancedCursor(1)
	second := h.a.advancedPreviewRow(1, 40, 3)
	if !strings.Contains(ansi.Strip(second), "second needle match") || !strings.Contains(second, h.a.theme.Match.Render("needle")) {
		t.Fatalf("preview did not follow selection: %q", second)
	}
	h.press("esc")
}

func TestAdvancedSearchPreviewIsAsynchronousAndBounded(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	path := filepath.Join(env.src, "preview.txt")
	if err := os.WriteFile(path, []byte("preview text\n"+strings.Repeat("x", 1<<20)), 0o644); err != nil {
		t.Fatal(err)
	}
	h.press("/")
	h.press("ctrl+g")
	v := h.a.advancedSearch
	entry, ok := search.NewEntry(v.root, path)
	if !ok {
		t.Fatal("could not create test result")
	}
	v.entries = []search.Entry{entry}
	v.filenameResults = []int{0}
	h.a.refreshAdvancedPreview()
	if !v.previewLoading {
		t.Fatal("preview was not scheduled asynchronously")
	}
	h.until("bounded preview", func() bool { return !v.previewLoading })
	if v.previewDoc == nil || !v.previewDoc.Truncated {
		t.Fatalf("bounded preview did not load/truncate: doc=%+v err=%v", v.previewDoc, v.previewErr)
	}
	h.press("esc")
}

func TestAdvancedSearchKeyDoesNotEditOrChangePanels(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	p := h.a.ActivePanel()
	before := p.Path()
	h.press("/")
	h.press("ctrl+g")
	v := h.a.advancedSearch
	entry, _ := search.NewEntry(v.root, filepath.Join(env.src, "a.txt"))
	v.entries, v.filenameResults = []search.Entry{entry}, []int{0}
	h.press("enter")
	if h.a.viewer == nil {
		t.Fatal("could not open inspection")
	}
	h.settle()
	h.press("e", "d", "c", "m")
	if h.a.dialog != nil || h.a.editing || h.a.viewer == nil || p.Path() != before {
		t.Fatal("inspection triggered a destructive action or navigated a panel")
	}
	h.press("esc", "esc")
}

func TestAdvancedSearchKeyMessages(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.press("/")
	h.press("ctrl+g")
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("abc")})
	if h.a.advancedSearch.input.Value() != "abc" {
		t.Fatalf("burst text did not reach the query: %q", h.a.advancedSearch.input.Value())
	}
	h.press("esc")
}
