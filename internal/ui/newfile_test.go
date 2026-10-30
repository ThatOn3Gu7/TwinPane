package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/fileext"
	"github.com/ThatOn3Gu7/TwinPane/internal/panel"
)

// New file and New folder (phase 10).

// chooseFormat opens the format chooser and picks the row that the search
// query leaves on top (a preset extension, "(none)" or a custom one).
func (h *harness) pickFileFormat(query string) {
	h.t.Helper()
	h.press("ctrl+f")
	h.waitDialog(dlgFormat)
	if query == "" {
		h.press("enter")
		return
	}
	h.press("ctrl+u")
	h.typeText(query, false)
	h.press("enter")
	h.waitDialog(dlgNewFile)
}

func TestNewMenuOffersFileAndFolder(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.press("n")
	if !h.dialogKind(dlgNewMenu) {
		t.Fatalf("n must open the New menu:\n%s", h.view())
	}
	v := h.view()
	for _, want := range []string{"New file", "New folder", "f file", "d folder"} {
		if !strings.Contains(v, want) {
			t.Fatalf("New menu misses %q:\n%s", want, v)
		}
	}
	// the hint bar describes the menu as running entries, not opening them
	if f := h.footer(); !strings.Contains(f, "run") {
		t.Fatalf("menu hints: %q", f)
	}
	h.press("esc")
	if h.a.dialog != nil {
		t.Fatal("Esc must close the New menu")
	}
	// F7 still goes straight to the folder prompt
	h.press("f7")
	if !h.dialogKind(dlgMkdir) {
		t.Fatalf("F7 must open the folder prompt:\n%s", h.view())
	}
}

func TestNewFolderThroughMenuKeepsTheFolderFlow(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.press("n", "d")
	if !h.dialogKind(dlgMkdir) {
		t.Fatalf("d must open the folder prompt:\n%s", h.view())
	}
	h.typeText("Docs 2", false)
	h.press("enter")
	h.idle()
	fi, err := os.Stat(filepath.Join(env.src, "Docs 2"))
	if err != nil || !fi.IsDir() {
		t.Fatalf("folder not created: %v (status %q)", err, h.status())
	}
	if e, _ := h.a.ActivePanel().Current(); e.Name != "Docs 2" {
		t.Fatalf("cursor must be on the new folder, got %q", e.Name)
	}
}

func TestNewFileWithoutFormat(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.press("n", "f")
	if !h.dialogKind(dlgNewFile) {
		t.Fatalf("f must open the New file dialog:\n%s", h.view())
	}
	if v := h.view(); !strings.Contains(v, "Creates: —") {
		t.Fatalf("empty dialog should say nothing will be created yet:\n%s", v)
	}
	h.typeText("notes", false)
	if v := h.view(); !strings.Contains(v, "Creates: notes") {
		t.Fatalf("preview must follow the typed name:\n%s", v)
	}
	// a name typed with an extension and no format is used as written
	h.press("ctrl+u")
	h.typeText("notes.txt", false)
	v := h.view()
	if !strings.Contains(v, "Creates: notes.txt") || strings.Contains(v, "(typed") {
		t.Fatalf("without a format nothing is replaced:\n%s", v)
	}
	h.press("ctrl+u")
	h.typeText("notes", false)
	h.press("enter")
	if h.a.dialog != nil {
		t.Fatalf("dialog stayed open: %q", h.a.dialog.errLine)
	}
	path := filepath.Join(env.src, "notes")
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("file not created: %v (status %q)", err, h.status())
	}
	if fi.Size() != 0 {
		t.Fatalf("new file must be empty, got %d bytes", fi.Size())
	}
	if e, _ := h.a.ActivePanel().Current(); e.Name != "notes" {
		t.Fatalf("cursor must be on the new file, got %q", e.Name)
	}
	if !strings.Contains(h.reported(), "Created file") || h.reportedLevel() != lvlOK {
		t.Fatalf("report %q level %d", h.reported(), h.reportedLevel())
	}
}

func TestNewFilePresetExtension(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.press("n", "f")
	h.typeText("notes", false)
	h.pickFileFormat("md")
	if v := h.view(); !strings.Contains(v, "Markdown (.md)") {
		t.Fatalf("the chosen format must be shown:\n%s", v)
	}
	if v := h.view(); !strings.Contains(v, "Creates: notes.md") {
		t.Fatalf("preview must include the extension:\n%s", v)
	}
	h.press("enter")
	h.idle()
	if _, err := os.Stat(filepath.Join(env.src, "notes.md")); err != nil {
		t.Fatalf("notes.md not created: %v (status %q)", err, h.status())
	}
	if _, err := os.Stat(filepath.Join(env.src, "notes.md.md")); err == nil {
		t.Fatal("the extension must not be stacked")
	}
}

func TestNewFileFullNameIsNotDuplicated(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.press("n", "f")
	h.typeText("notes.md", false)
	h.pickFileFormat("md")
	if v := h.view(); !strings.Contains(v, "Creates: notes.md") {
		t.Fatalf("notes.md with .md must stay notes.md:\n%s", v)
	}
	h.press("enter")
	h.idle()
	if _, err := os.Stat(filepath.Join(env.src, "notes.md")); err != nil {
		t.Fatalf("notes.md not created: %v", err)
	}
	for _, bad := range []string{"notes.md.md", "notes.md.txt"} {
		if _, err := os.Stat(filepath.Join(env.src, bad)); err == nil {
			t.Fatalf("%s must not exist", bad)
		}
	}
}

func TestNewFileFormatReplacesTypedExtension(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.press("n", "f")
	h.typeText("draft.txt", false)
	h.pickFileFormat("md")
	if v := h.view(); !strings.Contains(v, "Creates: draft.md") || !strings.Contains(v, "typed draft.txt") {
		t.Fatalf("the dialog must explain the replacement:\n%s", v)
	}
	h.press("enter")
	h.idle()
	if _, err := os.Stat(filepath.Join(env.src, "draft.md")); err != nil {
		t.Fatalf("draft.md not created: %v", err)
	}
	if _, err := os.Stat(filepath.Join(env.src, "draft.txt")); err == nil {
		t.Fatal("the typed but replaced extension must not create a second file")
	}
}

func TestNewFileCustomExtension(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.press("n", "f")
	h.typeText("notes", false)
	h.press("ctrl+f")
	h.waitDialog(dlgFormat)
	h.typeText("xyz", false)
	if v := h.view(); !strings.Contains(v, "Use .xyz") {
		t.Fatalf("a typed custom extension must be offered:\n%s", v)
	}
	h.press("enter")
	h.waitDialog(dlgNewFile)
	if v := h.view(); !strings.Contains(v, ".xyz (custom)") || !strings.Contains(v, "Creates: notes.xyz") {
		t.Fatalf("custom format not applied:\n%s", v)
	}
	h.press("enter")
	h.idle()
	if _, err := os.Stat(filepath.Join(env.src, "notes.xyz")); err != nil {
		t.Fatalf("notes.xyz not created: %v (status %q)", err, h.status())
	}
}

func TestNewFileFormatSearchAndNoMatch(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.press("n", "f", "ctrl+f")
	h.waitDialog(dlgFormat)
	// a category name keeps the whole group
	h.typeText("images", false)
	v := h.view()
	if !strings.Contains(v, ".png") || !strings.Contains(v, ".jpg") {
		t.Fatalf("category search must list its formats:\n%s", v)
	}
	// searching for descriptions works too
	h.press("ctrl+u")
	h.typeText("spreadsheet", false)
	if v := h.view(); !strings.Contains(v, ".csv") && !strings.Contains(v, ".xlsx") {
		t.Fatalf("description search must find formats:\n%s", v)
	}
	// nonsense shows an explanation, not an empty box
	h.press("ctrl+u")
	h.typeText("zzz not a format", false)
	if v := h.view(); !strings.Contains(v, "No format matches") {
		t.Fatalf("no-match explanation missing:\n%s", v)
	}
	// Esc returns to the New file dialog, keeping what was typed there
	h.press("esc")
	if !h.dialogKind(dlgNewFile) {
		t.Fatal("Esc in the chooser must go back to the file dialog")
	}
	// choosing "(none)" clears the format
	h.pickFileFormat("")
	if v := h.view(); !strings.Contains(v, "(none)") {
		t.Fatalf("(none) must be selectable:\n%s", v)
	}
}

func TestNewFileNeverReplacesExistingEntries(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	before, err := os.ReadFile(filepath.Join(env.src, "a.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", "sub"} {
		h.press("n", "f")
		h.typeText(name, false)
		h.press("enter")
		if !h.dialogKind(dlgNewFile) {
			t.Fatalf("%s: the dialog closed instead of reporting the conflict", name)
		}
		if !strings.Contains(h.a.dialog.errLine, "already exists") {
			t.Fatalf("%s: error %q", name, h.a.dialog.errLine)
		}
		if v := h.view(); !strings.Contains(v, "already exists") {
			t.Fatalf("%s: the conflict must be visible:\n%s", name, v)
		}
		h.press("esc")
	}
	after, err := os.ReadFile(filepath.Join(env.src, "a.txt"))
	if err != nil || string(after) != string(before) {
		t.Fatalf("the existing file was modified: %q, %v", after, err)
	}
	if fi, err := os.Stat(filepath.Join(env.src, "sub")); err != nil || !fi.IsDir() {
		t.Fatalf("the existing folder was disturbed: %v", err)
	}
}

func TestNewFileInvalidNames(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	entries, _ := os.ReadDir(env.src)
	cases := []struct{ typed, want string }{
		{"", "enter a file name"},
		{"   ", "enter a file name"},
		{"..", "reserved"},
		{"bad/name", "cannot contain '/'"},
		{"bad\\name", "path separator"},
		{strings.Repeat("a", 256), "255"},
	}
	for _, c := range cases {
		h.press("n", "f")
		if c.typed != "" {
			h.typeText(c.typed, false)
		}
		h.press("enter")
		if !h.dialogKind(dlgNewFile) {
			t.Fatalf("%q: the dialog must stay open", c.typed)
		}
		if !strings.Contains(h.a.dialog.errLine, c.want) {
			t.Fatalf("%q: error %q, want %q", c.typed, h.a.dialog.errLine, c.want)
		}
		h.press("esc")
	}
	// a hostile paste with a control character is refused by name validation
	h.press("n", "f")
	h.a.dialog.input = newLineInput("bad\x01name")
	h.press("enter")
	if !h.dialogKind(dlgNewFile) || !strings.Contains(h.a.dialog.errLine, "control") {
		t.Fatalf("control characters must be refused: %q", h.a.dialog.errLine)
	}
	h.press("esc")
	// nothing was created by any of the attempts
	now, _ := os.ReadDir(env.src)
	if len(now) != len(entries) {
		t.Fatalf("invalid names created entries: %d before, %d after", len(entries), len(now))
	}
}

func TestNewFileEmptyNameWithFormatIsRefused(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.press("n", "f")
	h.pickFileFormat("md")
	if v := h.view(); !strings.Contains(v, "type a name before .md") {
		t.Fatalf("the preview must ask for a name:\n%s", v)
	}
	h.press("enter")
	if !h.dialogKind(dlgNewFile) || !strings.Contains(h.a.dialog.errLine, "file name") {
		t.Fatalf("an empty name must be refused: %q", h.a.dialog.errLine)
	}
	h.press("esc")
	if _, err := os.Stat(filepath.Join(env.src, ".md")); err == nil {
		t.Fatal("no file may be created without a name")
	}
}

func TestNewFileMissingFolder(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.press("n", "f")
	h.typeText("orphan", false)
	if err := os.RemoveAll(env.src); err != nil {
		t.Fatal(err)
	}
	h.press("enter")
	if !h.dialogKind(dlgNewFile) {
		t.Fatal("the dialog must stay open when the folder disappeared")
	}
	if !strings.Contains(h.a.dialog.errLine, "no longer exists") {
		t.Fatalf("error for a missing folder: %q", h.a.dialog.errLine)
	}
}

func TestNewFilePermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: a read-only folder is still writable")
	}
	h, env := newOpsApp(t, nil, nil)
	ro := filepath.Join(env.root, "readonly")
	if err := os.Mkdir(ro, 0o555); err != nil {
		t.Skip("cannot create a read-only folder:", err)
	}
	t.Cleanup(func() { os.Chmod(ro, 0o755) })
	h.exec(h.a.Panel(0).Load(ro, "", panel.LoadNavigate))
	h.until("read-only folder", func() bool { return h.a.Panel(0).Path() == ro })
	h.press("n", "f")
	h.typeText("nope.txt", false)
	h.press("enter")
	if !h.dialogKind(dlgNewFile) {
		t.Fatal("the dialog must stay open on a permission error")
	}
	if !strings.Contains(strings.ToLower(h.a.dialog.errLine), "permission denied") {
		t.Fatalf("error for a read-only folder: %q", h.a.dialog.errLine)
	}
	if _, err := os.Stat(filepath.Join(ro, "nope.txt")); err == nil {
		t.Fatal("nothing may be created in a read-only folder")
	}
}

func TestNewFileIgnoresPastedTextForTheMenu(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.press("n")
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("f"), Paste: true})
	if !h.dialogKind(dlgNewMenu) {
		t.Fatal("pasted text must not press menu entries")
	}
	h.press("esc")
	// name validation still rejects pasted separators
	h.press("n", "f")
	h.typeText("x/y", true)
	h.press("enter")
	if !h.dialogKind(dlgNewFile) || !strings.Contains(h.a.dialog.errLine, "cannot contain '/'") {
		t.Fatalf("a pasted name with a separator must be refused: %q", h.a.dialog.errLine)
	}
}

func TestNewFileMenusWorkWithMouse(t *testing.T) {
	h, _, clock := p4App(t, nil)
	h.press("n")
	if !h.dialogKind(dlgNewMenu) {
		t.Fatal("no New menu")
	}
	*clock = clock.Add(2 * time.Second)
	h.view()
	var row *dialogHit
	for i := range h.a.dialog.hits {
		hh := &h.a.dialog.hits[i]
		if hh.kind == hitListItem && strings.Contains(h.a.dialog.list[hh.index].label, "New folder") {
			row = hh
		}
	}
	if row == nil {
		t.Fatal("New folder row is not clickable")
	}
	h.tap(row.x0+2, row.y)
	if !h.dialogKind(dlgMkdir) {
		t.Fatalf("tapping New folder must open the folder prompt:\n%s", h.view())
	}
	h.press("esc")

	// the ◂ arrow of the Format row opens the chooser
	h.press("n", "f")
	*clock = clock.Add(2 * time.Second)
	h.view()
	var arrow *dialogHit
	for i := range h.a.dialog.hits {
		if h.a.dialog.hits[i].kind == hitSelPrev {
			arrow = &h.a.dialog.hits[i]
		}
	}
	if arrow == nil {
		t.Fatal("the Format selector is not clickable")
	}
	h.tap(arrow.x0+1, arrow.y)
	if !h.dialogKind(dlgFormat) {
		t.Fatalf("tapping the Format arrow must open the chooser:\n%s", h.view())
	}
	// the filter field narrows the rows down with the keyboard
	h.typeText("png", false)
	if v := h.view(); !strings.Contains(v, ".png") {
		t.Fatalf("filtered chooser:\n%s", v)
	}
	h.press("enter")
	if !h.dialogKind(dlgNewFile) {
		t.Fatal("choosing a format must return to the file dialog")
	}
	if v := h.view(); !strings.Contains(v, "PNG (.png)") {
		t.Fatalf("chosen format missing:\n%s", v)
	}
}

func TestNewFileFormatHintIsClickable(t *testing.T) {
	h, _, clock := p4App(t, nil)
	h.press("n", "f")
	if !h.dialogKind(dlgNewFile) {
		t.Fatal("no New file dialog")
	}
	*clock = clock.Add(2 * time.Second)
	h.tapHint("ctrl+f")
	if !h.dialogKind(dlgFormat) {
		t.Fatalf("the Ctrl+F hint must open the chooser:\n%s", h.view())
	}
	if strings.Contains(h.a.dialog.filter.Value(), "ctrl") {
		t.Fatalf("the hint was typed into the filter: %q", h.a.dialog.filter.Value())
	}
}

func TestNewFileCatalogueIsLargeAndGrouped(t *testing.T) {
	if n := fileext.Count(); n < 150 {
		t.Fatalf("only %d formats offered", n)
	}
	h, _ := newOpsApp(t, nil, nil)
	h.press("n", "f", "ctrl+f")
	h.waitDialog(dlgFormat)
	// groups are headings, not selectable rows
	var headers int
	for _, it := range h.a.dialog.list {
		if it.header {
			headers++
			if !it.disabled {
				t.Fatalf("heading %q must not be selectable", it.label)
			}
		}
	}
	if headers < 10 {
		t.Fatalf("only %d headings in the chooser", headers)
	}
	// the cursor never rests on a heading
	h.press("down", "down", "down")
	if d := h.a.dialog; d.list[d.listCursor].disabled || d.list[d.listCursor].header {
		t.Fatalf("cursor on a heading: %+v", d.list[d.listCursor])
	}
}

func TestNewFileAndFolderInsideArchiveAreRefused(t *testing.T) {
	h, _, _ := zipApp(t)
	h.press("enter")
	h.until("archive", func() bool { return h.a.ActivePanel().InArchive() })
	h.press("n")
	if !h.dialogKind(dlgNewMenu) {
		t.Fatalf("n inside an archive:\n%s", h.view())
	}
	if !h.a.dialog.list[0].disabled || h.a.dialog.list[0].detail == "" {
		t.Fatalf("New file must be disabled inside an archive: %+v", h.a.dialog.list[0])
	}
	h.press("f") // disabled entries do not run
	if !h.dialogKind(dlgNewMenu) {
		t.Fatal("a disabled menu entry must not open a dialog")
	}
	h.press("esc")
	h.press("n", "d")
	if h.a.dialog != nil {
		t.Fatal("no folder dialog inside a read-only archive")
	}
	if s, isErr := h.a.Status(); !isErr || !strings.Contains(s, "read-only archive") {
		t.Fatalf("status %q", s)
	}
}

func TestActionMenuNewEntries(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	pick := func(want string) {
		h.t.Helper()
		h.press("M")
		if !h.dialogKind(dlgMenu) {
			t.Fatalf("action menu:\n%s", h.view())
		}
		found := false
		for i, it := range h.a.dialog.list {
			if strings.Contains(it.label, want) {
				h.a.dialog.listCursor = i
				found = true
			}
		}
		if !found {
			t.Fatalf("no %q entry in the action menu", want)
		}
		h.press("enter")
	}
	pick("New file")
	if !h.dialogKind(dlgNewFile) {
		t.Fatalf("menu → New file:\n%s", h.view())
	}
	h.press("esc")
	pick("New folder")
	if !h.dialogKind(dlgMkdir) {
		t.Fatalf("menu → New folder:\n%s", h.view())
	}
}

func TestNewFileInNarrowTerminal(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.send(tea.WindowSizeMsg{Width: 44, Height: 18})
	h.settle()
	h.press("n")
	if !h.dialogKind(dlgNewMenu) {
		t.Fatalf("narrow New menu:\n%s", h.view())
	}
	h.press("f")
	if !h.dialogKind(dlgNewFile) {
		t.Fatalf("narrow New file dialog:\n%s", h.view())
	}
	h.typeText("narrow", false)
	v := h.view()
	for _, want := range []string{"Creates: narrow (empty file)", "File name:", "Format"} {
		if !strings.Contains(v, want) {
			t.Fatalf("narrow dialog misses %q:\n%s", want, v)
		}
	}
	if n := len(strings.Split(v, "\n")); n != h.a.height {
		t.Fatalf("narrow dialog view has %d lines, want %d:\n%s", n, h.a.height, v)
	}
	h.press("ctrl+f")
	if !h.dialogKind(dlgFormat) {
		t.Fatalf("narrow format chooser:\n%s", h.view())
	}
	h.typeText("md", false)
	h.press("enter")
	if !h.dialogKind(dlgNewFile) {
		t.Fatal("the narrow chooser did not return")
	}
	h.press("enter")
	h.idle()
	if _, err := os.Stat(filepath.Join(env.src, "narrow.md")); err != nil {
		t.Fatalf("narrow terminal creation: %v (status %q)", err, h.status())
	}
}
