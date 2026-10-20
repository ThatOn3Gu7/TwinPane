package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/search"
	"github.com/ThatOn3Gu7/TwinPane/internal/theme"
)

// Visual layout tests for the shared section chrome and the three redesigned
// screens (live search, Advanced Search, folder-size analysis).

func TestSectionBoxGeometry(t *testing.T) {
	th := theme.Default()
	body := []string{"one", "two words", "a third body line"}
	for width := 1; width <= 90; width++ {
		for height := 1; height <= 7; height++ {
			lines := sectionBox(th, "Results · 12", "of 3 entries", body, width, height, true, []bool{true, false})
			if len(lines) != height {
				t.Fatalf("%dx%d: got %d lines", width, height, len(lines))
			}
			for i, line := range lines {
				if got := ansi.StringWidth(line); got != width {
					t.Fatalf("%dx%d line %d is %d cells wide: %q", width, height, i, got, line)
				}
			}
			if width >= 28 && height >= 3 && !strings.Contains(ansi.Strip(lines[0]), "Results · 12") {
				t.Fatalf("%dx%d lost its title: %q", width, height, ansi.Strip(lines[0]))
			}
			if width >= 4 && height >= 4 && !strings.Contains(ansi.Strip(lines[1]), "┃") {
				t.Fatalf("%dx%d lost the scroll thumb: %q", width, height, ansi.Strip(lines[1]))
			}
		}
	}
	// Too small for a frame: content (or blank space) is returned instead.
	out := sectionBox(th, "Results", "", []string{"row"}, 3, 2, true, nil)
	if len(out) != 2 || ansi.StringWidth(out[0]) != 3 || ansi.Strip(out[0]) != "row" {
		t.Fatalf("borderless fallback = %#v", out)
	}
}

func advancedAppWithPreview(t *testing.T) (*harness, *opsEnv) {
	t.Helper()
	h, env := newOpsApp(t, nil, nil)
	source := "import os\n\ndef hello(name):\n    # greeter\n    return name\n"
	if err := os.WriteFile(filepath.Join(env.src, "tls.py"), []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	h.press("/")
	h.press("ctrl+g")
	v := h.a.advancedSearch
	if v == nil {
		t.Fatal("Advanced Search did not open")
	}
	entry, ok := search.NewEntry(v.root, filepath.Join(env.src, "tls.py"))
	if !ok {
		t.Fatal("could not create the preview entry")
	}
	v.entries = []search.Entry{entry}
	v.filenameResults = []int{0}
	v.indexing = false
	v.cursor = 0
	h.a.refreshAdvancedPreview()
	h.until("advanced preview", func() bool { return !v.previewLoading && v.previewDoc != nil })
	return h, env
}

func TestAdvancedSearchSectionsAndSyntaxPreview(t *testing.T) {
	h, _ := advancedAppWithPreview(t)
	v := h.a.advancedSearch
	if len(v.previewSyntax) == 0 || !strings.Contains(v.previewSyntax[0], "\x1b[") {
		t.Fatalf("preview lines are not syntax coloured: %#v", v.previewSyntax)
	}
	h.a.width, h.a.height = 100, 30
	view := h.a.View()
	plain := ansi.Strip(view)
	for _, want := range []string{
		"Advanced Search · Filename", "Results · 1", "Preview · tls.py",
		"Type a filename or path…", "indexed",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("Advanced Search is missing %q:\n%s", want, plain)
		}
	}
	if !strings.Contains(plain, "  1 │ ") || !strings.Contains(plain, "def") {
		t.Fatalf("preview lost its line numbers or content:\n%s", plain)
	}
	checkScreen(t, view, 100, 30)

	// Content mode highlights matches and labels the preview with the count.
	v.mode = advancedContentMode
	v.input = newLineInput("hello")
	v.contentResults = []search.ContentResult{{
		Path: filepath.Join(v.root, "tls.py"), Rel: "tls.py",
		Matches: []search.ContentMatch{{Line: 3, Text: "def hello(name):"}},
	}}
	v.cursor = 0
	v.contentStatus.ScannedFiles = 4
	plain = ansi.Strip(h.a.View())
	if !strings.Contains(plain, "Preview · tls.py") || !strings.Contains(plain, "1 match") {
		t.Fatalf("content preview heading:\n%s", plain)
	}
	row := h.a.advancedPreviewRow(1, 60, 4)
	if !strings.Contains(ansi.Strip(row), "def hello(name):") {
		t.Fatalf("content preview lost the match text: %q", ansi.Strip(row))
	}
	if !strings.Contains(row, h.a.theme.Match.Render("hello")) {
		t.Fatalf("content match is not highlighted: %q", row)
	}
	if !strings.Contains(ansi.Strip(row), "  3 │ ") {
		t.Fatalf("content match lost its line number: %q", ansi.Strip(row))
	}
}

func TestAdvancedSearchLayoutsFitTheirScreen(t *testing.T) {
	h, _ := advancedAppWithPreview(t)
	for _, size := range [][2]int{{100, 30}, {79, 20}, {60, 24}, {42, 14}, {30, 9}, {100, 10}, {30, 7}} {
		h.a.width, h.a.height = size[0], size[1]
		view := h.a.View()
		checkScreen(t, view, size[0], size[1])
		plain := ansi.Strip(view)
		if !strings.Contains(plain, "Advanced Search · Filename") && !strings.Contains(plain, "Adv. search · Files") {
			t.Fatalf("%dx%d lost the heading:\n%s", size[0], size[1], plain)
		}
		switch size[1] {
		case 30, 24, 20:
			if !strings.Contains(plain, "Results · ") || !strings.Contains(plain, "Preview · tls.py") {
				t.Fatalf("%dx%d should show both panes:\n%s", size[0], size[1], plain)
			}
		}
	}
}

func TestLiveSearchSectionChrome(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	if err := os.WriteFile(filepath.Join(env.src, "Target-File.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	h.press("/")
	h.searchDone()
	h.a.width, h.a.height = 100, 30
	plain := ansi.Strip(h.a.View())
	for _, want := range []string{
		"Find files by name", "in " + env.src, "Type a filename or path…",
		"Results · 6", "of 6 entries", "hidden skipped",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("filename search is missing %q:\n%s", want, plain)
		}
	}
	checkScreen(t, h.a.View(), 100, 30)

	// The placeholder is only shown while the field is empty; the root and
	// the counts must survive every narrow layout.
	for _, size := range [][2]int{{60, 14}, {30, 9}, {30, 7}} {
		h.a.width, h.a.height = size[0], size[1]
		view := h.a.View()
		checkScreen(t, view, size[0], size[1])
		plain = ansi.Strip(view)
		if !strings.Contains(plain, "Find files by name") || !strings.Contains(plain, "Type a filename") {
			t.Fatalf("%dx%d lost the heading or placeholder:\n%s", size[0], size[1], plain)
		}
		if size[1] > 7 && !strings.Contains(plain, "in ") {
			// At 30x7 there is no room next to the heading; the root is
			// still shown in the status bar.
			t.Fatalf("%dx%d lost the search root:\n%s", size[0], size[1], plain)
		}
	}

	h.a.width, h.a.height = 100, 30
	h.typeText("Target", false)
	plain = ansi.Strip(h.a.View())
	if !strings.Contains(plain, "1 match of 6 entries") || !strings.Contains(plain, "Results · 1") {
		t.Fatalf("filtered counts are missing:\n%s", plain)
	}
	checkScreen(t, h.a.View(), 100, 30)
}

func TestFolderSizeStructuredSections(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	longName := strings.Repeat("very-long-file-name-", 6) + "report.txt"
	if err := os.WriteFile(filepath.Join(env.src, longName), []byte(strings.Repeat("z", 4096)), 0o644); err != nil {
		t.Fatal(err)
	}
	h.press("A")
	waitFolderSize(t, h)

	h.a.width, h.a.height = 100, 30
	view := h.a.View()
	plain := ansi.Strip(view)
	for _, want := range []string{
		"Folder-size analysis", "Target: active folder", "Apparent ", "Allocated ",
		"irector", "files", "links", "Largest items", "NAME", "TYPE", "SIZE", "SHARE",
		"Selected · ", "Path:", "apparent",
	} {
		if !strings.Contains(plain, want) {
			t.Fatalf("folder-size analysis is missing %q:\n%s", want, plain)
		}
	}
	if !strings.Contains(plain, "█") && !strings.Contains(plain, "░") {
		t.Fatalf("the share column has no proportional bar:\n%s", plain)
	}
	if !strings.Contains(plain, "…") {
		t.Fatalf("the long file name was not truncated:\n%s", plain)
	}
	checkScreen(t, view, 100, 30)

	// Narrow terminals drop the details panel instead of squeezing the table.
	h.a.width, h.a.height = 42, 12
	plain = ansi.Strip(h.a.View())
	checkScreen(t, h.a.View(), 42, 12)
	if strings.Contains(plain, "Selected · ") || !strings.Contains(plain, "Largest items") {
		t.Fatalf("42x12 should keep the table and drop the details panel:\n%s", plain)
	}

	// Very short terminals fall back to a borderless compact table.
	h.a.width, h.a.height = 30, 8
	plain = ansi.Strip(h.a.View())
	checkScreen(t, h.a.View(), 30, 8)
	if !strings.Contains(plain, "Folder") || !strings.Contains(plain, "NAME") {
		t.Fatalf("30x8 compact layout lost its heading or columns:\n%s", plain)
	}
}
