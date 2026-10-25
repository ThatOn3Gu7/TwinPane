package ui

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/storage"
)

// Phase 6: the compression dialog, bookmarks, Open / Open with, the action
// menu and the storage overview. No real app is ever started: Launch is
// recorded by the harness (opsEnv.launches), the environment (Termux or
// not) comes from opsEnv.env, and tools can be hidden from LookPath.

func (h *harness) keyType(t tea.KeyType) {
	h.send(tea.KeyMsg{Type: t})
	h.settle()
}

func (h *harness) waitCompressionDone() {
	h.t.Helper()
	h.until("compression summary", func() bool { return h.a.compressProgress != nil && h.a.compressProgress.finished })
	if !h.dialogKind(dlgCompressProgress) {
		h.t.Fatalf("compression completed without retaining its modal: %v", h.a.dialog)
	}
}

func (h *harness) dismissCompression() {
	h.t.Helper()
	if !h.dialogKind(dlgCompressProgress) || h.a.compressProgress == nil || !h.a.compressProgress.finished {
		h.t.Fatalf("no completed compression dialog to dismiss: %v", h.a.dialog)
	}
	h.press("enter")
	if h.a.dialog != nil || h.a.compressProgress != nil {
		h.t.Fatal("compression summary did not dismiss")
	}
}

// chooseFormat moves the compression dialog's format selector to name.
func (h *harness) chooseFormat(name string) {
	h.t.Helper()
	d := h.a.dialog
	if d == nil || len(d.selectors) == 0 {
		h.t.Fatalf("no compression dialog: %v", d)
	}
	for i := 0; i < 20; i++ {
		if strings.HasPrefix(d.selectors[0].value(), name+" (") {
			return
		}
		h.keyType(tea.KeyDown)
	}
	h.t.Fatalf("format %s not offered", name)
}

func assertCompressDefault(t *testing.T, input *lineInput, dir, ext string) {
	t.Helper()
	want := filepath.Join(dir, ext)
	if input.Value() != want {
		t.Fatalf("default output path %q, want %q", input.Value(), want)
	}
	wantPos := len([]rune(want)) - len([]rune(ext))
	if input.pos != wantPos {
		t.Fatalf("extension cursor at %d, want %d for %q", input.pos, wantPos, want)
	}
}

func tarGzNames(t *testing.T, path string) []string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	var out []string
	for {
		hd, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, hd.Name)
	}
	sort.Strings(out)
	return out
}

func TestCompressDialogFormatAndLevel(t *testing.T) {
	h, env, _ := p4App(t, nil)
	h.focus("sub")
	h.press("z")
	h.waitDialog(dlgCompress)
	d := h.a.dialog
	assertCompressDefault(t, d.input, env.src, ".zip")
	v := h.view()
	for _, want := range []string{"Format", "ZIP (.zip)", "Level", "Archive file:", "Default output folder"} {
		if !strings.Contains(v, want) {
			t.Fatalf("dialog lacks %q:\n%s", want, v)
		}
	}
	if f := h.footer(); !strings.Contains(f, "format") || !strings.Contains(f, "level") {
		t.Fatalf("footer lacks the selector keys: %q", f)
	}
	// ↓ changes the format and the extension with it
	h.chooseFormat("TAR.GZ")
	assertCompressDefault(t, d.input, env.src, ".tar.gz")
	lvl := d.selectors[1].value()
	h.keyType(tea.KeyPgDown)
	if d.selectors[1].value() == lvl {
		t.Fatalf("PgDn did not change the level (%s)", lvl)
	}
	// TAR has no levels: shown as such
	h.chooseFormat("TAR")
	assertCompressDefault(t, d.input, env.src, ".tar")
	if !strings.Contains(d.selectors[1].value(), "single setting") {
		t.Fatalf("TAR level %q", d.selectors[1].value())
	}
	// an edited name keeps its stem when the format changes
	h.press("ctrl+u", "ctrl+k")
	h.typeText(filepath.Join(env.dst, "mine.tar"), true)
	h.chooseFormat("TAR.GZ")
	if got := d.input.Value(); got != filepath.Join(env.dst, "mine.tar.gz") {
		t.Fatalf("edited name became %q", got)
	}
	h.press("enter")
	h.waitCompressionDone()
	if !strings.Contains(h.view(), "Final archive size") || !strings.Contains(h.view(), "Elapsed:") || !strings.Contains(h.view(), "mine.tar.gz") {
		t.Fatalf("completion summary lacks metadata:\n%s", h.view())
	}
	if !strings.Contains(h.reported(), "mine.tar.gz") {
		t.Fatalf("report %q", h.reported())
	}
	got := tarGzNames(t, filepath.Join(env.dst, "mine.tar.gz"))
	want := []string{"sub/", "sub/deep/", "sub/deep/c.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("entries %v, want %v", got, want)
	}
	h.dismissCompression()
	// the format is remembered for the next dialog
	h.focus("a.txt")
	h.press("z")
	h.waitDialog(dlgCompress)
	assertCompressDefault(t, h.a.dialog.input, env.src, ".tar.gz")
}

func TestCompressRequiresNameBeforeExtension(t *testing.T) {
	h, env, _ := p4App(t, nil)
	h.focus("a.txt")
	h.press("z")
	h.waitDialog(dlgCompress)
	assertCompressDefault(t, h.a.dialog.input, env.src, ".zip")
	h.press("enter")
	if !h.dialogKind(dlgCompress) || !strings.Contains(h.a.dialog.errLine, "file name") {
		t.Fatalf("extension-only name was accepted: %v", h.a.dialog)
	}
	for _, name := range []string{".zip", ".zip.zip"} {
		if fileExists(filepath.Join(env.src, name)) {
			t.Fatalf("extension-only default created a hidden archive: %s", name)
		}
	}
	h.press("esc")
}

func TestCompressMultipleItemsKeepStructure(t *testing.T) {
	h, env, _ := p4App(t, nil)
	h.focus("a.txt")
	h.press("space")
	h.focus("sub")
	h.press("space")
	h.press("z")
	h.waitDialog(dlgCompress)
	h.chooseFormat("TAR.GZ")
	assertCompressDefault(t, h.a.dialog.input, env.src, ".tar.gz")
	h.typeText("selection", false)
	out := filepath.Join(env.src, "selection.tar.gz")
	if got := h.a.dialog.input.Value(); got != out {
		t.Fatalf("typed name %q", got)
	}
	h.press("enter")
	h.waitCompressionDone()
	got := tarGzNames(t, out)
	want := []string{"a.txt", "sub/", "sub/deep/", "sub/deep/c.txt"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("entries %v, want %v", got, want)
	}
	// The result is created and focused in the active source folder.
	h.until("source panel refreshed", func() bool {
		for _, n := range entryNames(h.a.panels[0]) {
			if n == "selection.tar.gz" {
				return true
			}
		}
		return false
	})
	h.dismissCompression()
}

func TestCompressSingleFileFormats(t *testing.T) {
	h, env, _ := p4App(t, nil)
	h.focus("sub")
	h.press("z")
	h.waitDialog(dlgCompress)
	h.chooseFormat("GZIP")
	// a folder cannot be a .gz: refused with the reason, nothing created
	if !strings.Contains(h.a.dialog.selectors[0].value(), "one file only") {
		t.Fatalf("GZIP for a folder: %q", h.a.dialog.selectors[0].value())
	}
	h.press("enter")
	if !h.dialogKind(dlgCompress) || h.a.dialog.errLine == "" {
		t.Fatalf("GZIP of a folder was not refused: %v", h.a.dialog)
	}
	h.press("esc")
	h.focus("a.txt")
	h.press("z")
	h.waitDialog(dlgCompress)
	h.chooseFormat("GZIP")
	wantDefault := filepath.Join(env.src, "a.txt.gz")
	if got := h.a.dialog.input.Value(); got != wantDefault || h.a.dialog.input.pos != len([]rune(wantDefault)) {
		t.Fatalf("single-file default %q at cursor %d", got, h.a.dialog.input.pos)
	}
	if !strings.Contains(h.view(), "Compressed file:") {
		t.Fatalf("label:\n%s", h.view())
	}
	// a wrong extension is refused, never "fixed" silently
	h.press("ctrl+u", "ctrl+k")
	h.typeText(filepath.Join(env.dst, "a.txt.zip"), true)
	h.press("enter")
	if !h.dialogKind(dlgCompress) || h.a.dialog.errLine == "" {
		t.Fatalf("wrong extension accepted: %v", h.a.dialog)
	}
	if _, err := os.Stat(filepath.Join(env.dst, "a.txt.zip")); err == nil {
		t.Fatal("a file with the wrong extension was created")
	}
	h.press("ctrl+u", "ctrl+k")
	h.typeText(filepath.Join(env.dst, "a.txt.gz"), true)
	h.press("enter")
	h.waitCompressionDone()
	if !strings.Contains(h.view(), "Compressed file size") {
		t.Fatalf("single-file summary lacks output size:\n%s", h.view())
	}
	f, err := os.Open(filepath.Join(env.dst, "a.txt.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := io.ReadAll(zr); string(b) != "new-a" {
		t.Fatalf("content %q", b)
	}
	h.dismissCompression()
}

func TestCompressUnavailableFormatExplained(t *testing.T) {
	h, env, _ := p4App(t, nil)
	for _, n := range []string{"7z", "7za", "7zr", "7zz"} {
		env.hide[n] = true
	}
	h.focus("a.txt")
	h.press("z")
	h.waitDialog(dlgCompress)
	h.chooseFormat("7Z")
	d := h.a.dialog
	if !strings.Contains(d.selectors[0].value(), "unavailable") {
		t.Fatalf("7Z without 7z: %q", d.selectors[0].value())
	}
	if !strings.Contains(h.view(), "7z") {
		t.Fatalf("no reason shown:\n%s", h.view())
	}
	if got := d.input.Value(); !strings.HasSuffix(got, ".7z") {
		t.Fatalf("extension not kept for the unavailable format: %q", got)
	}
	h.press("enter")
	if !h.dialogKind(dlgCompress) || d.errLine == "" {
		t.Fatalf("unavailable format not refused: %v", h.a.dialog)
	}
	h.press("esc")
	if fileExists(filepath.Join(env.src, ".7z")) || fileExists(filepath.Join(env.src, ".7z.7z")) {
		t.Fatal("unavailable format created an output")
	}
}

func TestCompressOverwriteNeedsConfirmation(t *testing.T) {
	h, env, _ := p4App(t, nil)
	out := filepath.Join(env.dst, "a.txt.gz")
	os.WriteFile(out, []byte("keep"), 0o644)
	h.focus("a.txt")
	h.press("z")
	h.waitDialog(dlgCompress)
	h.chooseFormat("GZIP")
	h.press("ctrl+u", "ctrl+k")
	h.typeText(out, true)
	h.press("enter")
	h.waitDialog(dlgZipExists)
	h.press("esc")
	if !strings.Contains(h.status(), "GZIP not created — nothing was changed") {
		t.Fatalf("status %q", h.status())
	}
	if b, _ := os.ReadFile(out); string(b) != "keep" {
		t.Fatalf("file changed: %q", b)
	}
}

func TestCompressAwkwardNames(t *testing.T) {
	h, env, _ := p4App(t, nil)
	name := `it's "odd" $(rm -rf x); ü 名.txt`
	os.WriteFile(filepath.Join(env.src, name), []byte("odd"), 0o644)
	h.exec(h.a.panels[0].Refresh())
	h.settle()
	h.focus(name)
	h.press("z")
	h.waitDialog(dlgCompress)
	h.chooseFormat("TAR.GZ")
	h.typeText("odd-archive", false)
	out := filepath.Join(env.src, "odd-archive.tar.gz")
	h.press("enter")
	h.waitCompressionDone()
	if !strings.Contains(h.reported(), "Created") {
		t.Fatalf("report %q", h.reported())
	}
	if !fileExists(out) {
		t.Fatalf("default source folder does not contain %q", out)
	}
	if got := tarGzNames(t, out); !reflect.DeepEqual(got, []string{name}) {
		t.Fatalf("entries %q", got)
	}
	h.dismissCompression()
}

// ---- bookmarks -------------------------------------------------------------------

func TestBookmarksAddOpenRenameRemove(t *testing.T) {
	h, env, _ := p4App(t, nil)
	h.press("B")
	if !strings.Contains(h.status(), "bookmarked") {
		t.Fatalf("status %q", h.status())
	}
	h.press("B")
	if !strings.Contains(h.status(), "already bookmarked") {
		t.Fatalf("duplicate: %q", h.status())
	}
	// the file is written (atomically, JSON)
	b, err := os.ReadFile(env.bmPath)
	if err != nil || !json.Valid(b) || !strings.Contains(string(b), env.src) {
		t.Fatalf("bookmarks file %q, %v", b, err)
	}
	// open it from the other panel
	h.press("tab")
	h.press("b")
	h.waitDialog(dlgBookmarks)
	if v := h.view(); !strings.Contains(v, "src") || !strings.Contains(v, "Bookmarks") {
		t.Fatalf("list:\n%s", v)
	}
	if f := h.footer(); !strings.Contains(f, "rename") || !strings.Contains(f, "remove") {
		t.Fatalf("footer %q", f)
	}
	h.press("enter")
	h.until("jumped", func() bool { return h.a.panels[1].Path() == env.src })
	if h.a.dialog != nil {
		t.Fatal("dialog still open")
	}
	// rename
	h.press("b")
	h.waitDialog(dlgBookmarks)
	h.press("r")
	if h.a.dialog.input == nil {
		t.Fatal("no rename prompt")
	}
	h.press("ctrl+u")
	h.typeText("Projects ü", true)
	h.press("enter")
	if !h.dialogKind(dlgBookmarks) || h.a.dialog.list == nil || h.a.dialog.list[0].label != "Projects ü" {
		t.Fatalf("after rename: %+v", h.a.dialog)
	}
	// remove: asks first, Esc keeps it
	h.press("d")
	h.press("esc")
	if len(h.a.dialog.list) != 1 {
		t.Fatal("Esc removed the bookmark")
	}
	h.press("d")
	h.press("y")
	if !h.dialogKind(dlgBookmarks) || len(h.a.dialog.list) != 0 {
		t.Fatalf("after remove: %+v", h.a.dialog)
	}
	if !strings.Contains(h.view(), "No bookmarks yet") {
		t.Fatalf("empty list:\n%s", h.view())
	}
	// keys on an empty list are harmless
	h.press("down", "up", "enter", "r", "d", "1")
	if !h.dialogKind(dlgBookmarks) {
		t.Fatalf("dialog %v", h.a.dialog)
	}
	if _, err := os.Stat(env.src); err != nil {
		t.Fatal("removing a bookmark touched the folder")
	}
	h.press("esc")
	if h.a.dialog != nil {
		t.Fatal("Esc did not close")
	}
}

func TestBookmarkMissingFolderIsNotRedirected(t *testing.T) {
	h, env, _ := p4App(t, nil)
	gone := filepath.Join(env.root, "gone")
	os.Mkdir(gone, 0o755)
	h.exec(h.a.ActivePanel().ChangeDir(gone))
	h.until("in gone", func() bool { return h.a.ActivePanel().Path() == gone })
	h.press("B")
	h.exec(h.a.ActivePanel().ChangeDir(env.src))
	h.until("back", func() bool { return h.a.ActivePanel().Path() == env.src })
	os.Remove(gone)
	h.press("b")
	h.waitDialog(dlgBookmarks)
	if !strings.Contains(h.view(), "missing") {
		t.Fatalf("not marked missing:\n%s", h.view())
	}
	h.press("enter")
	if !h.dialogKind(dlgBookmarks) || !strings.Contains(h.a.dialog.errLine, "no longer exists") {
		t.Fatalf("missing folder: %+v", h.a.dialog)
	}
	h.settle()
	if p := h.a.ActivePanel().Path(); p != env.src {
		t.Fatalf("panel moved to %s", p)
	}
}

func TestBookmarkRefusedInsideArchive(t *testing.T) {
	h, env, _ := zipApp(t)
	h.focus("t.zip")
	h.press("enter")
	h.until("in archive", func() bool { return h.a.ActivePanel().InArchive() })
	h.press("B")
	if !strings.Contains(h.status(), "cannot be bookmarked") {
		t.Fatalf("status %q", h.status())
	}
	if _, err := os.Stat(env.bmPath); err == nil {
		t.Fatal("bookmarks file written")
	}
}

func TestCorruptBookmarksFileIsKept(t *testing.T) {
	h, env, _ := p4App(t, nil)
	os.MkdirAll(filepath.Dir(env.bmPath), 0o755)
	os.WriteFile(env.bmPath, []byte("{not json"), 0o600)
	h.press("b")
	h.waitDialog(dlgBookmarks)
	if h.a.dialog.errLine == "" {
		t.Fatal("corrupt file not reported")
	}
	h.press("a")
	if b, _ := os.ReadFile(env.bmPath); string(b) != "{not json" {
		t.Fatalf("corrupt file overwritten: %q", b)
	}
}

// ---- open / open with ----------------------------------------------------------

func termux(env *opsEnv) {
	env.env["TERMUX_VERSION"] = "0.118.0"
	env.env["PREFIX"] = "/data/data/com.termux/files/usr"
	env.tools["termux-open"] = true
}

func TestOpenInDefaultAppOnTermux(t *testing.T) {
	h, env, _ := p4App(t, nil)
	termux(env)
	name := `pic 'x' "y" $(z);ü.jpg`
	os.WriteFile(filepath.Join(env.src, name), []byte{0xff, 0xd8, 0xff, 0xe0}, 0o644)
	h.exec(h.a.panels[0].Refresh())
	h.settle()
	h.focus(name)
	h.send(tea.WindowSizeMsg{Width: 260, Height: 24})
	if f := h.footer(); !strings.Contains(f, "⏎  open") || strings.Contains(f, "view") {
		t.Fatalf("footer %q", f)
	}
	h.send(tea.WindowSizeMsg{Width: 100, Height: 24})
	// Enter on an image hands it to Android; the name is one argument
	h.press("enter")
	h.until("launched", func() bool { return len(env.launched()) == 1 })
	want := []string{"/fake/bin/termux-open", "--", filepath.Join(env.src, name)}
	if got := env.launched()[0]; !reflect.DeepEqual(got, want) {
		t.Fatalf("argv %q, want %q", got, want)
	}
	h.until("status", func() bool { return strings.Contains(h.status(), "handed") })
	if h.a.viewer != nil {
		t.Fatal("the viewer opened too")
	}
	// o does the same for any file
	h.focus("a.txt")
	h.press("o")
	h.until("launched", func() bool { return len(env.launched()) == 2 })
	// a failing launch is reported
	env.mu.Lock()
	env.launchFn = func([]string) error { return errors.New("exit status 1") }
	env.mu.Unlock()
	h.press("o")
	h.until("error", func() bool { return strings.Contains(h.status(), "could not open") })
	// text files still open in the viewer with Enter
	h.press("enter")
	if h.a.viewer == nil {
		t.Fatal("Enter on a text file did not open the viewer")
	}
}

func TestOpenWithoutOpener(t *testing.T) {
	h, env, _ := p4App(t, nil) // linux, no $DISPLAY
	os.WriteFile(filepath.Join(env.src, "photo.png"), []byte("\x89PNG\r\n\x1a\nxx"), 0o644)
	h.exec(h.a.panels[0].Refresh())
	h.settle()
	h.focus("photo.png")
	h.press("o")
	if s := h.status(); !strings.Contains(s, "cannot open") || !strings.Contains(s, "DISPLAY") {
		t.Fatalf("status %q", s)
	}
	h.press("enter") // no opener: as before (the viewer)
	if h.a.viewer == nil {
		t.Fatal("no viewer")
	}
	if len(env.launched()) != 0 {
		t.Fatal("something was launched")
	}
}

func TestOpenWithList(t *testing.T) {
	h, env, _ := p4App(t, nil)
	termux(env)
	h.focus("a.txt")
	h.press("O")
	h.waitDialog(dlgOpenWith)
	labels := []string{}
	for _, it := range h.a.dialog.list {
		labels = append(labels, it.label)
	}
	for _, want := range []string{"Built-in viewer", "Text editor", "Share…", "Run a program…"} {
		found := false
		for _, l := range labels {
			found = found || l == want
		}
		if !found {
			t.Fatalf("Open with lacks %q: %q", want, labels)
		}
	}
	if len(labels) > 9 {
		t.Fatalf("%d entries (1-9 must reach all)", len(labels))
	}
	// pick "Share…" by number: termux-open --send
	for i, l := range labels {
		if l == "Share…" {
			h.press(string(rune('1' + i)))
		}
	}
	h.until("shared", func() bool { return len(env.launched()) == 1 })
	if got := env.launched()[0]; !reflect.DeepEqual(got, []string{"/fake/bin/termux-open", "--send", "--", filepath.Join(env.src, "a.txt")}) {
		t.Fatalf("share argv %q", got)
	}
	h.until("status", func() bool { return strings.Contains(h.status(), "share") })
	// the viewer entry opens the viewer
	h.press("O")
	h.waitDialog(dlgOpenWith)
	for i, it := range h.a.dialog.list {
		if it.label == "Built-in viewer" {
			h.press(string(rune('1' + i)))
		}
	}
	if h.a.viewer == nil {
		t.Fatal("viewer not opened")
	}
}

func TestOpenRefusedForFoldersAndArchives(t *testing.T) {
	h, env, _ := zipApp(t)
	termux(env)
	h.focus("sub")
	h.press("o")
	if !strings.Contains(h.status(), "folder") {
		t.Fatalf("o on a folder: %q", h.status())
	}
	h.focus("t.zip")
	h.press("enter") // ZIP archives are still browsed
	h.until("in archive", func() bool { return h.a.ActivePanel().InArchive() })
	h.press("down")
	h.press("o")
	if !strings.Contains(h.status(), "extract it first") {
		t.Fatalf("o in an archive: %q", h.status())
	}
	h.press("O")
	if h.a.dialog != nil {
		t.Fatal("Open with opened inside an archive")
	}
	if len(env.launched()) != 0 {
		t.Fatal("launched")
	}
}

func TestRunAProgramWithoutShell(t *testing.T) {
	h, env, _ := p4App(t, nil)
	h.focus("a.txt")
	h.press("O")
	h.waitDialog(dlgOpenWith)
	for i, it := range h.a.dialog.list {
		if it.label == "Run a program…" {
			h.press(string(rune('1' + i)))
		}
	}
	h.waitDialog(dlgRunWith)
	h.typeText("no-such-program-xyz", true)
	h.press("enter")
	if !h.dialogKind(dlgRunWith) || !strings.Contains(h.a.dialog.errLine, "not found") {
		t.Fatalf("unknown program: %+v", h.a.dialog)
	}
	env.tools["viewer-x"] = true
	h.press("ctrl+u")
	h.typeText(`viewer-x -R "two words"`, true)
	h.press("enter")
	h.until("exec", func() bool { return len(env.execs) == 1 && !h.a.editing })
	want := []string{"/fake/bin/viewer-x", "-R", "two words", filepath.Join(env.src, "a.txt")}
	if got := env.execs[0].args; !reflect.DeepEqual(got, want) {
		t.Fatalf("args %q, want %q", got, want)
	}
	if !strings.Contains(h.status(), "viewer-x finished") {
		t.Fatalf("status %q", h.status())
	}
}

// ---- menu ------------------------------------------------------------------------

func menuLabels(d *dialog) []string {
	var out []string
	for _, it := range d.list {
		out = append(out, it.label)
	}
	return out
}

func TestMenuActionsAndDispatch(t *testing.T) {
	h, env, _ := p4App(t, nil)
	termux(env)
	h.focus("a.txt")
	h.press("M")
	h.waitDialog(dlgMenu)
	d := h.a.dialog
	labels := menuLabels(d)
	for _, want := range []string{"Open (default app)", "Open with…", "View", "Copy to other panel", "Move to other panel",
		"Rename", "Compress…", "Properties", "Share…", "Bookmark this folder", "Bookmarks…", "Storage info", "Delete…"} {
		found := false
		for _, l := range labels {
			found = found || l == want
		}
		if !found {
			t.Fatalf("menu lacks %q: %q", want, labels)
		}
	}
	foundCompressLocation := false
	for _, it := range d.list {
		if it.label == "Compress…" {
			foundCompressLocation = it.detail == "default: "+shortPath(env.src)
		}
	}
	if !foundCompressLocation {
		t.Fatalf("Compress menu item should show the default folder %q", env.src)
	}
	last := d.list[len(d.list)-1]
	if last.label != "Delete…" || !last.danger {
		t.Fatalf("Delete is not last/danger: %+v", last)
	}
	for _, it := range d.list[:len(d.list)-1] {
		if it.danger {
			t.Fatalf("%q marked dangerous", it.label)
		}
	}
	if !strings.Contains(h.view(), "a.txt") {
		t.Fatalf("title:\n%s", h.view())
	}
	// digits do nothing in a menu; letters run the entry
	h.press("1")
	if !h.dialogKind(dlgMenu) {
		t.Fatal("digit picked an entry")
	}
	h.press("d")
	h.waitDialog(dlgDelete)
	h.press("esc")
	if _, err := os.Stat(filepath.Join(env.src, "a.txt")); err != nil {
		t.Fatal("deleted without confirmation")
	}
	// arrows + Enter: Copy opens the usual confirmation
	h.press("M")
	h.waitDialog(dlgMenu)
	for i, it := range h.a.dialog.list {
		if it.label == "Copy to other panel" {
			h.a.dialog.listCursor = i
		}
	}
	h.press("enter")
	h.waitDialog(dlgConfirm)
	h.press("esc")
	// Share is a menu-only action
	h.press("M")
	h.waitDialog(dlgMenu)
	h.press("s")
	h.until("shared", func() bool { return len(env.launched()) == 1 })
	if got := env.launched()[0]; got[1] != "--send" {
		t.Fatalf("share argv %q", got)
	}
	// bookmark from the menu, then the menu offers removal
	h.press("M")
	h.waitDialog(dlgMenu)
	h.press("B")
	h.press("M")
	h.waitDialog(dlgMenu)
	if l := menuLabels(h.a.dialog); !strings.Contains(strings.Join(l, "|"), "Remove this folder's bookmark") {
		t.Fatalf("after bookmarking: %q", l)
	}
	h.press("B")
	if !strings.Contains(h.status(), "removed") {
		t.Fatalf("status %q", h.status())
	}
}

func TestMenuOnlyRelevantActions(t *testing.T) {
	h, env, _ := zipApp(t)
	// desktop without a display: no Share
	h.focus("a.txt")
	h.press("M")
	h.waitDialog(dlgMenu)
	if l := strings.Join(menuLabels(h.a.dialog), "|"); strings.Contains(l, "Share") {
		t.Fatalf("Share offered without Termux: %s", l)
	}
	h.press("esc")
	// a folder: no Open with / View / Edit
	h.focus("sub")
	h.press("M")
	h.waitDialog(dlgMenu)
	l := strings.Join(menuLabels(h.a.dialog), "|")
	for _, bad := range []string{"Open with", "View", "Edit"} {
		if strings.Contains(l, bad) {
			t.Fatalf("folder menu has %q: %s", bad, l)
		}
	}
	h.press("esc")
	// inside an archive: read-only actions only
	h.focus("t.zip")
	h.press("enter")
	h.until("in archive", func() bool { return h.a.ActivePanel().InArchive() })
	h.press("M")
	h.waitDialog(dlgMenu)
	l = strings.Join(menuLabels(h.a.dialog), "|")
	for _, bad := range []string{"Delete", "Rename", "Move", "Copy", "Compress", "Share", "Open with", "Bookmark this"} {
		if strings.Contains(l, bad) {
			t.Fatalf("archive menu has %q: %s", bad, l)
		}
	}
	if !strings.Contains(l, "Extract") {
		t.Fatalf("archive menu lacks Extract: %s", l)
	}
	_ = env
}

func TestMenuChipIsTappable(t *testing.T) {
	h, _, _ := p4App(t, nil)
	h.send(tea.WindowSizeMsg{Width: 260, Height: 24})
	h.focus("a.txt")
	h.tapHint("M")
	if !h.dialogKind(dlgMenu) {
		t.Fatalf("tapping the M chip: %v", h.a.dialog)
	}
	// tap an entry: View
	h.view()
	for i, it := range h.a.dialog.list {
		if it.label == "View" {
			h.a.dialog.listCursor = i
		}
	}
	h.press("enter")
	if h.a.viewer == nil {
		t.Fatal("View from the menu did not open the viewer")
	}
}

// ---- storage ---------------------------------------------------------------------

func fakeStorage(t *testing.T, fn func(string) storage.Info) {
	old := storageStat
	storageStat = fn
	t.Cleanup(func() { storageStat = old })
}

func TestStorageOverview(t *testing.T) {
	h, env, _ := p4App(t, nil)
	fakeStorage(t, func(dir string) storage.Info {
		return storage.Info{Path: dir, Resolved: dir, Total: 100 << 30, Free: 50 << 30, Avail: 40 << 30,
			Mount: storage.Mount{Point: "/data", FSType: "f2fs", Source: "/dev/block/dm-5", Options: "rw"}}
	})
	h.press("i")
	h.waitDialog(dlgStorage)
	v := h.view()
	// used 50 of 90 usable (like df): 56%
	for _, want := range []string{"Storage — /data", "f2fs", "read-write", "Total", "100 G", "50 G (56%)", "40 G available", "+10 G reserved", "56% used", "same filesystem"} {
		if !strings.Contains(v, want) {
			t.Fatalf("storage lacks %q:\n%s", want, v)
		}
	}
	h.press("enter")
	if h.a.dialog != nil {
		t.Fatal("not closed")
	}
	_ = env
}

func TestStorageUnavailableValues(t *testing.T) {
	h, _, _ := p4App(t, nil)
	fakeStorage(t, func(dir string) storage.Info {
		return storage.Info{Path: dir, SizeErr: errors.New("statfs: permission denied"), MountErr: storage.ErrUnsupported}
	})
	h.press("i")
	h.waitDialog(dlgStorage)
	v := h.view()
	if !strings.Contains(v, "unavailable") || !strings.Contains(v, "permission denied") {
		t.Fatalf("unavailable values:\n%s", v)
	}
	if strings.Contains(v, "% used") || strings.Contains(v, "Total") {
		t.Fatalf("invented values:\n%s", v)
	}
}

// ---- help and hints ------------------------------------------------------------

func TestPhase6KeysInHelpAndHints(t *testing.T) {
	h, _, _ := p4App(t, nil)
	h.send(tea.WindowSizeMsg{Width: 260, Height: 60})
	h.press("?")
	v := h.view()
	for _, want := range []string{"menu of actions", "open in default app", "open with…", "bookmarks (jump/edit)", "bookmark this folder", "storage / free space", "compress (ZIP/TAR/7Z…)", "action menu (TwinPane)"} {
		if !strings.Contains(v, want) {
			t.Fatalf("help lacks %q:\n%s", want, v)
		}
	}
	h.press("esc")
	h.focus("a.txt")
	f := h.footer()
	for _, want := range []string{"menu", "open with", "bookmarks", "storage"} {
		if !strings.Contains(f, want) {
			t.Fatalf("footer at 260 lacks %q: %q", want, f)
		}
	}
	// at 100 columns the new chips do not push out the phase-4 ones
	h.send(tea.WindowSizeMsg{Width: 100, Height: 24})
	if f := h.footer(); !strings.Contains(f, "d  delete") {
		t.Fatalf("footer at 100: %q", f)
	}
	h.focus("..")
	if f := h.footer(); !strings.Contains(f, "show hidden") {
		t.Fatalf("footer at 100 on ..: %q", f)
	}
	// the new chips come after everything that was there before
	h.send(tea.WindowSizeMsg{Width: 110, Height: 24})
	if f := h.footer(); !strings.Contains(f, "s/S  sort  q  quit") {
		t.Fatalf("footer at 110 on ..: %q", f)
	}
}

func TestTitleTapOpensMenu(t *testing.T) {
	h, _, _ := p4App(t, nil)
	h.send(tea.WindowSizeMsg{Width: 44, Height: 20}) // a phone in portrait
	h.focus("a.txt")
	if strings.Contains(h.footer(), "menu") {
		t.Skip("the chip fits: nothing to check")
	}
	h.tap(2, 0)
	if !h.dialogKind(dlgMenu) {
		t.Fatalf("tap on the title: %v", h.a.dialog)
	}
	h.press("esc")
	h.tap(30, 0) // elsewhere on the title row: nothing
	if h.a.dialog != nil {
		t.Fatal("menu opened from the right side of the title")
	}
}

func TestCompressCancelLeavesNothing(t *testing.T) {
	h, env, _ := p4App(t, nil)
	big := filepath.Join(env.src, "big")
	os.Mkdir(big, 0o755)
	// incompressible data so XZ is slow enough to be cancelled
	buf := make([]byte, 4<<20)
	seed := uint32(7)
	for i := range buf {
		seed = seed*1664525 + 1013904223
		buf[i] = byte(seed >> 24)
	}
	for i := 0; i < 6; i++ {
		os.WriteFile(filepath.Join(big, "f"+string(rune('A'+i))), buf, 0o644)
	}
	h.exec(h.a.panels[0].Refresh())
	h.settle()
	h.focus("big")
	h.press("z")
	h.waitDialog(dlgCompress)
	h.chooseFormat("TAR.XZ")
	h.typeText("cancel-test", false)
	out := filepath.Join(env.src, "cancel-test.tar.xz")
	h.send(key("enter"))                 // starts
	h.send(tea.KeyMsg{Type: tea.KeyEsc}) // request cancellation; the modal stays open
	if h.a.compressProgress == nil {
		t.Skip("compression finished before the cancel took effect")
	}
	h.idle()
	h.waitCompressionDone()
	if !h.a.compressProgress.result.Canceled {
		h.dismissCompression()
		t.Skip("compression finished before the cancel took effect")
	}
	if !strings.Contains(strings.ToLower(h.reported()), "cancel") || h.reportedLevel() != lvlWarn {
		t.Fatalf("report %q level %d", h.reported(), h.reportedLevel())
	}
	if !strings.Contains(h.view(), "No temporary output remains") {
		t.Fatalf("cancel summary does not report cleanup:\n%s", h.view())
	}
	if fileExists(out) {
		t.Fatalf("final output remains after cancel: %s", out)
	}
	h.dismissCompression()
}
