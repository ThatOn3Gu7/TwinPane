package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/search"
)

// advancedPreviewApp opens Advanced Search on a generated long text file and
// waits until its preview has loaded.
func advancedPreviewApp(t *testing.T, lines int) (*harness, *advancedSearchView) {
	t.Helper()
	h, env := newOpsApp(t, nil, nil)
	var body strings.Builder
	for i := 1; i <= lines; i++ {
		fmt.Fprintf(&body, "line %03d value\n", i)
	}
	if err := os.WriteFile(filepath.Join(env.src, "long.txt"), []byte(body.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	h.press("/")
	h.press("ctrl+g")
	v := h.a.advancedSearch
	if v == nil {
		t.Fatal("Advanced Search did not open")
	}
	entry, ok := search.NewEntry(v.root, filepath.Join(env.src, "long.txt"))
	if !ok {
		t.Fatal("could not create the preview entry")
	}
	v.entries = []search.Entry{entry}
	v.filenameResults = []int{0}
	v.indexing = false
	v.cursor = 0
	h.a.refreshAdvancedPreview()
	h.until("advanced preview", func() bool { return !v.previewLoading && v.previewDoc != nil })
	return h, v
}

func TestAdvancedPreviewWheelScrollsThePreview(t *testing.T) {
	h, v := advancedPreviewApp(t, 300)
	h.a.width, h.a.height = 100, 30
	layout := h.a.advancedLayout(27)
	if !layout.wide || layout.previewH <= 0 {
		t.Fatalf("expected a side-by-side preview, got %+v", layout)
	}
	plain := ansi.Strip(h.a.View())
	if !strings.Contains(plain, "line 001 value") {
		t.Fatalf("preview does not start at the first line:\n%s", plain)
	}

	// The wheel over the preview scrolls the preview, not the result list.
	h.wheel(layout.resultsW+3, layout.previewTop+2, 6)
	if v.previewOffset != 6 {
		t.Fatalf("preview offset after wheel = %d, want 6", v.previewOffset)
	}
	if v.cursor != 0 || v.offset != 0 {
		t.Fatalf("the result list moved: cursor=%d offset=%d", v.cursor, v.offset)
	}
	plain = ansi.Strip(h.a.View())
	if strings.Contains(plain, "line 001 value") || !strings.Contains(plain, "line 007 value") {
		t.Fatalf("preview did not scroll by six lines:\n%s", plain)
	}
	if len(thumbCells(h.a.View(), h.a.width-1)) == 0 {
		t.Fatalf("the preview has no scroll indicator:\n%s", plain)
	}

	// The wheel over the result list does not disturb the preview when the
	// list cannot move any further.
	h.wheel(2, layout.searchH+3, 2)
	if v.previewOffset != 6 {
		t.Fatalf("scrolling the results reset the preview to %d", v.previewOffset)
	}
}

func TestAdvancedPreviewScrollbarJumpsAndDrags(t *testing.T) {
	h, v := advancedPreviewApp(t, 300)
	h.a.width, h.a.height = 60, 24
	layout := h.a.advancedLayout(21)
	if layout.wide || layout.previewH <= 0 {
		t.Fatalf("expected a stacked preview, got %+v", layout)
	}
	visible := layout.previewH - 2
	maxOff := 300 - visible

	h.wheel(5, layout.previewTop+2, 4)
	if v.previewOffset != 4 {
		t.Fatalf("stacked preview offset = %d, want 4", v.previewOffset)
	}

	// Tap the top of the preview's scroll indicator: first line again.
	h.tap(h.a.width-1, layout.previewTop+1)
	if v.previewOffset != 0 {
		t.Fatalf("track tap at the top left offset %d", v.previewOffset)
	}

	// Drag the indicator to the bottom: the last page.
	h.mouse(h.a.width-1, layout.previewTop+1, tea.MouseButtonLeft, tea.MouseActionPress)
	h.mouse(h.a.width-1, layout.previewTop+visible, tea.MouseButtonLeft, tea.MouseActionMotion)
	h.mouse(h.a.width-1, layout.previewTop+visible, tea.MouseButtonLeft, tea.MouseActionRelease)
	if v.previewOffset != maxOff {
		t.Fatalf("dragging the indicator gave %d, want %d", v.previewOffset, maxOff)
	}
}

func TestAdvancedPreviewScrollResetsForANewResult(t *testing.T) {
	h, v := advancedPreviewApp(t, 300)
	second := filepath.Join(v.root, "second.txt")
	if err := os.WriteFile(second, []byte("second preview\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	entry, ok := search.NewEntry(v.root, second)
	if !ok {
		t.Fatal("could not create the second entry")
	}
	v.entries = append(v.entries, entry)
	v.filenameResults = []int{0, 1}
	h.a.width, h.a.height = 100, 30

	layout := h.a.advancedLayout(27)
	h.wheel(layout.resultsW+3, layout.previewTop+2, 5)
	if v.previewOffset != 5 {
		t.Fatalf("preview offset = %d, want 5", v.previewOffset)
	}
	h.press("down")
	if v.previewOffset != 0 || v.cursor != 1 {
		t.Fatalf("selecting another result kept cursor=%d offset=%d", v.cursor, v.previewOffset)
	}
	h.until("second preview", func() bool {
		return !v.previewLoading && v.previewDoc != nil && strings.HasSuffix(v.previewPath, "second.txt")
	})
	plain := ansi.Strip(h.a.View())
	if !strings.Contains(plain, "Preview · second.txt") || !strings.Contains(plain, "second preview") {
		t.Fatalf("the new result's preview did not replace the old one:\n%s", plain)
	}
}

func TestAdvancedPreviewContentScrollKeepsTheSummary(t *testing.T) {
	h, v := advancedPreviewApp(t, 300)
	v.mode = advancedContentMode
	v.input = newLineInput("line")
	matches := make([]search.ContentMatch, 0, 100)
	for i := 1; i <= 100; i++ {
		matches = append(matches, search.ContentMatch{Line: i, Text: fmt.Sprintf("line %03d value", i)})
	}
	v.contentResults = []search.ContentResult{{Path: filepath.Join(v.root, "long.txt"), Rel: "long.txt", Matches: matches}}
	v.previewOffset = 0
	h.a.width, h.a.height = 100, 30
	layout := h.a.advancedLayout(27)

	h.wheel(layout.resultsW+3, layout.previewTop+2, 3)
	if v.previewOffset != 3 {
		t.Fatalf("content preview offset = %d, want 3", v.previewOffset)
	}
	plain := ansi.Strip(h.a.View())
	if !strings.Contains(plain, "long.txt · 100 matching line(s)") {
		t.Fatalf("the summary row was scrolled away:\n%s", plain)
	}
	if !strings.Contains(plain, "line 004 value") || strings.Contains(plain, "line 001 value") {
		t.Fatalf("content matches did not scroll:\n%s", plain)
	}
}
