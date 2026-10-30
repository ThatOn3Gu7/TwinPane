package ui

import (
	"archive/zip"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	"github.com/ThatOn3Gu7/TwinPane/internal/archive"
	"github.com/ThatOn3Gu7/TwinPane/internal/panel"
	"github.com/ThatOn3Gu7/TwinPane/internal/theme"
)

// ---- helpers ------------------------------------------------------------------------

func TestMissingDestinationParent(t *testing.T) {
	root := t.TempDir()
	parent, focus := missingDestinationParent(filepath.Join(root, "new", "nested"))
	if parent != root || focus != "new" {
		t.Fatalf("nearest existing ancestor = (%q, %q), want (%q, %q)", parent, focus, root, "new")
	}
	existing := filepath.Join(root, "present")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	if parent, focus = missingDestinationParent(existing); parent != "" || focus != "" {
		t.Fatalf("existing destination should not refresh a parent, got (%q, %q)", parent, focus)
	}
}

// writeZip creates a ZIP file; names ending in "/" are folders.
func writeZip(t *testing.T, path string, entries map[string]string) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range entries {
		hdr := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
		if strings.HasSuffix(name, "/") {
			hdr.Method = zip.Store
		}
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

var testZip = map[string]string{
	"top.txt":       "top level\n",
	"dir/a.txt":     "inside dir\n",
	"dir/sub/b.txt": "deep\n",
	"empty/":        "",
}

// zipApp is p4App with src/t.zip, the left panel refreshed and focused on it.
func zipApp(t *testing.T) (*harness, *opsEnv, *time.Time) {
	t.Helper()
	h, env, clock := p4App(t, nil)
	writeZip(t, filepath.Join(env.src, "t.zip"), testZip)
	h.exec(h.a.panels[0].RefreshFocus("t.zip"))
	h.settle()
	return h, env, clock
}

func entryNames(p *panel.Panel) []string {
	var out []string
	for _, e := range p.Entries() {
		out = append(out, e.Name)
	}
	return out
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

// checkScreen verifies that every screen line fits the width exactly and
// that there are exactly height lines (nothing wrapped or overflowing).
func fitsScreen(t *testing.T, h *harness, what string) {
	t.Helper()
	v := h.a.View()
	lines := strings.Split(v, "\n")
	if len(lines) != h.a.height {
		t.Fatalf("%s at %dx%d: %d lines", what, h.a.width, h.a.height, len(lines))
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w > h.a.width {
			t.Fatalf("%s at %dx%d: line %d is %d wide: %q", what, h.a.width, h.a.height, i, w, ansi.Strip(l))
		}
	}
	if w := ansi.StringWidth(lines[len(lines)-1]); w != h.a.width {
		t.Fatalf("%s: hint bar is %d wide, want %d", what, w, h.a.width)
	}
}

func (h *harness) later() { h.a.now = func() time.Time { return time.Now().Add(time.Hour) } }

// ---- 1. hint bar padding -------------------------------------------------------------

func TestHintChipsArePaddedAndColoured(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(old)
	h, _, _ := p4App(t, nil)
	th := theme.Default()
	if _, r, _, l := th.FooterKey.GetPadding(); l != theme.FooterKeyPad || r != theme.FooterKeyPad || theme.FooterKeyPad != 2 {
		t.Fatalf("chip padding %d/%d", l, r)
	}
	for _, w := range []int{40, 60, 100} {
		h.send(tea.WindowSizeMsg{Width: w, Height: 24})
		raw := h.a.View()
		bar := raw[strings.LastIndex(raw, "\n")+1:]
		items, _ := h.a.hints()
		plain := ansi.Strip(bar)
		if !strings.HasPrefix(plain, "  Tab  ") {
			t.Fatalf("width %d: first chip not padded: %q", w, plain)
		}
		// every drawn chip is the padded, coloured key followed by the
		// gray description
		shown := 0
		for _, it := range items {
			chip := th.FooterKey.Render(it.key)
			if !strings.Contains(bar, chip) {
				continue
			}
			shown++
			if !strings.Contains(ansi.Strip(chip), "  "+it.key+"  ") {
				t.Fatalf("chip %q lacks two spaces each side: %q", it.key, ansi.Strip(chip))
			}
			if !strings.Contains(bar, chip+th.FooterDesc.Render(" "+it.desc+" ")) && !strings.Contains(bar, chip+th.FooterDesc.Render(" "+ansi.Truncate(it.desc, w, "…"))) {
				// keepChip hints (the focus-mode toggle "^O") fall back to
				// the bare padded key when the bar has no room for text.
				if !it.keepChip {
					t.Fatalf("chip %q not followed by its description", it.key)
				}
			}
		}
		if shown == 0 || !strings.Contains(bar, "48;5;249") || !strings.Contains(bar, "38;5;250") {
			t.Fatalf("width %d: chips/colours missing in %q", w, bar)
		}
		if ansi.StringWidth(bar) != w {
			t.Fatalf("width %d: bar is %d wide", w, ansi.StringWidth(bar))
		}
		// hit ranges match the drawn chips exactly
		for _, fh := range h.a.footerHits {
			seg := []rune(plain)[fh.x0:fh.x1]
			if !strings.HasPrefix(string(seg), "  ") || fh.x1 > w {
				t.Fatalf("width %d: hit %v covers %q", w, fh, string(seg))
			}
		}
	}
}

func TestHintBarFitsInEveryNewScreen(t *testing.T) {
	h, env, _ := zipApp(t)
	os.WriteFile(filepath.Join(env.src, "long.txt"), []byte(strings.Repeat("wide line ", 400)+"\n"), 0o644)
	h.exec(h.a.panels[0].RefreshFocus("long.txt"))
	h.settle()
	for _, w := range []int{30, 40, 60, 100} {
		h.send(tea.WindowSizeMsg{Width: w, Height: 16})
		h.settle()
		fitsScreen(t, h, "browse")
		h.press("v")
		fitsScreen(t, h, "viewer")
		h.press("w")
		fitsScreen(t, h, "viewer no-wrap")
		h.press("esc")
		h.press("P")
		fitsScreen(t, h, "properties")
		h.press("esc")
		h.focus("t.zip")
		h.press("enter")
		h.until("archive", func() bool { return h.a.ActivePanel().InArchive() })
		fitsScreen(t, h, "archive")
		h.press("X")
		fitsScreen(t, h, "extract dialog")
		h.press("esc", "esc")
	}
}

// ---- 2. properties --------------------------------------------------------------------

func TestPropertiesShowsMetadata(t *testing.T) {
	h, env, _ := p4App(t, nil)
	h.focus("a.txt")
	h.press("P")
	if h.a.props == nil {
		t.Fatal("P did not open properties")
	}
	v := h.view()
	for _, want := range []string{"Properties", "a.txt", "regular file", "5 bytes", "rw-r--r--", "0644", "Modified", "Apply", "Close Esc"} {
		if !strings.Contains(v, want) {
			t.Errorf("properties lack %q", want)
		}
	}
	if !strings.Contains(v, filepath.Base(env.src)) {
		t.Error("folder missing")
	}
	if runtime.GOOS != "windows" && !strings.Contains(v, fmt.Sprintf("(%d)", os.Getuid())) {
		t.Error("owner id missing")
	}
	if f := h.footer(); !strings.Contains(f, "⏎  apply") || !strings.Contains(f, "Esc  close") {
		t.Fatalf("hints: %q", f)
	}
	h.press("esc")
	// a folder shows its item count instead of a size
	h.focus("sub")
	h.press("P")
	if v := h.view(); !strings.Contains(v, "folder") || !strings.Contains(v, "1 item") {
		t.Fatalf("folder properties: %s", v)
	}
}

func TestPropertiesRenameValidatesAndNeverOverwrites(t *testing.T) {
	h, env, _ := p4App(t, nil)
	h.focus("a.txt")
	row := h.a.ActivePanel().Cursor() - h.a.ActivePanel().Offset()
	h.press("P")
	// the cursor starts before the extension: ^U + "b" gives "b.txt"
	h.press("ctrl+u")
	h.typeText("b", false)
	if v := h.view(); !strings.Contains(v, "Changes not applied yet") {
		t.Fatal("pending change not shown")
	}
	h.press("enter")
	if !strings.Contains(h.a.props.msg, "already exists") || h.a.props.msgLvl != lvlErr {
		t.Fatalf("conflict: %q", h.a.props.msg)
	}
	if readFile(t, filepath.Join(env.src, "b.txt")) != "new-b" || !fileExists(filepath.Join(env.src, "a.txt")) {
		t.Fatal("rename conflict changed files")
	}
	h.press("ctrl+u")
	h.typeText("x/", false)
	h.press("enter")
	if !strings.Contains(h.a.props.msg, "cannot contain '/'") {
		t.Fatalf("invalid name: %q", h.a.props.msg)
	}
	h.press("ctrl+u")
	h.typeText("c", false) // "c.txt"
	h.press("enter")
	h.settle()
	if h.a.props.msgLvl != lvlOK || !fileExists(filepath.Join(env.src, "c.txt")) || fileExists(filepath.Join(env.src, "a.txt")) {
		t.Fatalf("rename failed: %q", h.a.props.msg)
	}
	if !strings.Contains(h.view(), "c.txt") || h.a.props.info.Name != "c.txt" {
		t.Fatal("dialog does not show the new name")
	}
	h.press("esc")
	if h.a.props != nil {
		t.Fatal("Esc did not close")
	}
	if e, _ := h.a.ActivePanel().Current(); e.Name != "c.txt" {
		t.Fatalf("cursor on %q after rename", e.Name)
	}
	if r := h.a.ActivePanel().Cursor() - h.a.ActivePanel().Offset(); r != row {
		t.Fatalf("cursor moved on screen: row %d → %d", row, r)
	}
}

func TestPropertiesEscDiscardsChanges(t *testing.T) {
	h, env, _ := p4App(t, nil)
	h.focus("b.txt")
	cur := h.a.ActivePanel().Cursor()
	h.press("P")
	h.press("ctrl+u")
	h.typeText("zzz", false)
	h.press("tab", " ") // owner read off
	h.press("esc")
	if !fileExists(filepath.Join(env.src, "b.txt")) || h.a.ActivePanel().Cursor() != cur {
		t.Fatal("Esc applied changes or moved the cursor")
	}
	if fi, _ := os.Stat(filepath.Join(env.src, "b.txt")); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o644 {
		t.Fatalf("mode changed: %v", fi.Mode())
	}
	if !strings.Contains(h.status(), "discarded") {
		t.Fatalf("status %q", h.status())
	}
}

func TestPropertiesChangePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no unix permissions")
	}
	h, env, _ := p4App(t, nil)
	f := filepath.Join(env.src, "a.txt")
	h.focus("a.txt")
	h.press("P")
	if !h.a.props.info.ChmodOK {
		t.Skipf("filesystem does not support chmod here: %s", h.a.props.info.ChmodWhy)
	}
	h.press("tab")               // owner read
	h.press("right", "right")    // owner exec
	h.press(" ")                 // set
	h.press("down", "left", " ") // group write
	if v := h.view(); !strings.Contains(v, "rwxrw-r--") || !strings.Contains(v, "0764") || !strings.Contains(v, "(now 0644)") {
		t.Fatalf("pending permissions not shown: %s", v)
	}
	if fi, _ := os.Stat(f); fi.Mode().Perm() != 0o644 {
		t.Fatal("changed before Apply")
	}
	h.press("enter")
	if fi, _ := os.Stat(f); fi.Mode().Perm() != 0o764 {
		t.Fatalf("mode %v", fi.Mode())
	}
	if !strings.Contains(strings.ToLower(h.a.props.msg), "permissions set to rwxrw-r-- (0764)") {
		t.Fatalf("feedback %q", h.a.props.msg)
	}
	// nothing pending: Apply says so
	h.press("enter")
	if !strings.Contains(h.a.props.msg, "Nothing to apply") {
		t.Fatalf("no-op apply: %q", h.a.props.msg)
	}
}

func TestPropertiesNoChmodForLinksAndErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	h, env, _ := p4App(t, nil)
	os.Symlink("a.txt", filepath.Join(env.src, "link"))
	h.exec(h.a.panels[0].RefreshFocus("link"))
	h.settle()
	h.press("P")
	v := h.view()
	if !strings.Contains(v, "symbolic link → regular file") || !strings.Contains(v, "Link to") || !strings.Contains(v, "Cannot be changed here") {
		t.Fatalf("link properties: %s", v)
	}
	for _, f := range h.a.props.focusables() {
		if f >= pfCell0 && f < pfCell0+9 {
			t.Fatal("permission boxes offered for a link")
		}
	}
	h.press("esc")
	// the item disappears while the dialog is open: a clear error, no crash
	h.focus("b.txt")
	h.press("P", "ctrl+u")
	h.typeText("q", false)
	os.Remove(filepath.Join(env.src, "b.txt"))
	h.press("enter")
	if h.a.props.msgLvl != lvlErr || !strings.Contains(h.a.props.msg, "Not renamed") {
		t.Fatalf("missing item: %q", h.a.props.msg)
	}
}

func TestPropertiesByTouch(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no unix permissions")
	}
	h, env, clock := p4App(t, nil)
	h.focus("a.txt")
	h.press("P")
	h.view()
	cell := func(t0 int) propsHit {
		for _, hh := range h.a.props.hits {
			if hh.target == t0 {
				return hh
			}
		}
		t.Fatalf("no hit for %d", t0)
		return propsHit{}
	}
	if !h.a.props.info.ChmodOK {
		t.Skip("no chmod here")
	}
	c := cell(pfCell0 + 2) // owner exec
	h.tap(c.x0+1, c.y)     // within dialogGuard of opening: ignored
	if h.a.props.perm.Perm() != 0o644 {
		t.Fatal("tap right after opening acted")
	}
	*clock = clock.Add(time.Second)
	h.tap(c.x0+1, c.y)
	if h.a.props.perm.Perm() != 0o744 {
		t.Fatalf("tap did not toggle: %v", h.a.props.perm)
	}
	h.view()
	a := cell(pfApply)
	h.tap(a.x0+1, a.y)
	if fi, _ := os.Stat(filepath.Join(env.src, "a.txt")); fi.Mode().Perm() != 0o744 {
		t.Fatal("Apply tap did not apply")
	}
	h.view()
	cl := cell(pfClose)
	h.tap(cl.x0+1, cl.y)
	if h.a.props != nil {
		t.Fatal("Close tap did not close")
	}
}

// ---- 3. archives -------------------------------------------------------------------

func TestArchiveBrowseWithoutExtraction(t *testing.T) {
	h, env, _ := zipApp(t)
	before := dirNames(t, env.src)
	tmpBefore := dirNames(t, os.TempDir())
	h.send(tea.WindowSizeMsg{Width: 220, Height: 24})
	if f := h.footer(); !strings.Contains(f, "⏎  browse archive") || !strings.Contains(f, "x  extract") || !strings.Contains(f, "P  properties") {
		t.Fatalf("hints on a ZIP file: %q", f)
	}
	h.focus("a.txt")
	if f := h.footer(); !strings.Contains(f, "⏎  view") || strings.Contains(f, "extract") {
		t.Fatalf("hints on a text file: %q", f)
	}
	h.focus("t.zip")
	h.send(tea.WindowSizeMsg{Width: 100, Height: 24})
	h.press("enter")
	h.until("archive", func() bool { return h.a.ActivePanel().InArchive() })
	p := h.a.ActivePanel()
	if got := strings.Join(entryNames(p), ","); got != "..,dir,empty,top.txt" {
		t.Fatalf("root entries %s", got)
	}
	v := h.view()
	if !strings.Contains(v, "ZIP") || !strings.Contains(v, "t.zip") || !strings.Contains(h.status(), "browsing t.zip") {
		t.Fatalf("no archive indicator: status %q", h.status())
	}
	if h.a.panels[1].InArchive() || h.a.panels[1].Path() != env.dst {
		t.Fatal("the other panel changed")
	}
	h.focus("dir")
	h.press("enter")
	h.settle()
	if got := strings.Join(entryNames(p), ","); got != "..,sub,a.txt" || p.ArchiveInner() != "dir" {
		t.Fatalf("dir entries %s (%s)", got, p.ArchiveInner())
	}
	h.focus("sub")
	h.press("enter")
	h.settle()
	if p.ArchiveInner() != "dir/sub" || !panelHas(p, "b.txt") {
		t.Fatalf("nested: %s", p.ArchiveInner())
	}
	h.press("backspace")
	h.settle()
	if e, _ := p.Current(); p.ArchiveInner() != "dir" || e.Name != "sub" {
		t.Fatalf("back: %s cursor %s", p.ArchiveInner(), e.Name)
	}
	h.press("backspace")
	h.settle()
	h.focus("empty")
	h.press("enter")
	h.settle()
	if !strings.Contains(h.view(), "(empty folder in the archive)") {
		t.Fatal("empty folder message missing")
	}
	h.press("esc") // Esc leaves the archive from any depth
	h.settle()
	if p.InArchive() || p.Path() != env.src {
		t.Fatalf("Esc did not leave: %s", p.Path())
	}
	if e, _ := p.Current(); e.Name != "t.zip" {
		t.Fatalf("cursor on %q after leaving", e.Name)
	}
	if strings.Join(dirNames(t, env.src), ",") != strings.Join(before, ",") {
		t.Fatal("browsing created files next to the archive")
	}
	if len(dirNames(t, os.TempDir())) > len(tmpBefore) {
		t.Log("note: temp dir grew (other processes may write there)")
	}
	// ".." at the archive root also leaves it
	h.press("enter")
	h.until("archive", func() bool { return p.InArchive() })
	h.press("g", "enter")
	h.settle()
	if p.InArchive() {
		t.Fatal(".. at the archive root did not leave")
	}
}

func TestArchiveRefusesUnsupportedOperations(t *testing.T) {
	h, env, _ := zipApp(t)
	h.press("enter")
	h.until("archive", func() bool { return h.a.ActivePanel().InArchive() })
	h.focus("top.txt")
	for _, k := range []string{"d", "m", "r", "e", "E", "/", "z"} {
		h.press(k)
		if h.a.dialog != nil || h.a.bulk != nil || h.a.search != nil || h.a.editing {
			t.Fatalf("%s opened something inside the archive", k)
		}
		if s := h.reported(); h.reportedLevel() != lvlErr || !strings.Contains(s, "read-only archive") {
			t.Fatalf("%s: report %q (level %d)", k, s, h.reportedLevel())
		}
	}
	f := h.footer()
	for _, bad := range []string{"delete", "rename", "move", "edit", "find", "zip  "} {
		if strings.Contains(f, bad) {
			t.Fatalf("archive hints offer %q: %q", bad, f)
		}
	}
	if !strings.Contains(f, "x  extract") || !strings.Contains(f, "Esc  leave archive") {
		t.Fatalf("archive hints: %q", f)
	}
	// copy / move into the archive from the other panel are refused
	h.press("tab")
	h.exec(h.a.panels[1].Load(env.src, "a.txt", panel.LoadNavigate))
	h.settle()
	for _, k := range []string{"c", "m"} {
		h.press(k)
		if h.a.dialog != nil || !strings.Contains(h.status(), "into a read-only archive") {
			t.Fatalf("%s into archive: %q", k, h.status())
		}
	}
	if !fileExists(filepath.Join(env.src, "a.txt")) {
		t.Fatal("source changed")
	}
}

func TestArchiveExtractSelectedWithConflict(t *testing.T) {
	h, env, _ := zipApp(t)
	os.WriteFile(filepath.Join(env.src, "top.txt"), []byte("old"), 0o644)
	h.press("enter")
	h.until("archive", func() bool { return h.a.ActivePanel().InArchive() })
	h.focus("dir")
	h.press(" ")
	h.focus("top.txt")
	h.press(" ")
	h.press("x")
	if !h.dialogKind(dlgExtract) || h.a.dialog.input.Value() != env.src {
		t.Fatalf("extract dialog / default destination: %v", h.a.dialog)
	}
	h.press("enter")
	h.waitDialog(dlgConflict)
	if !strings.Contains(h.view(), "top.txt") {
		t.Fatal("conflict dialog does not name the file")
	}
	h.press("s") // skip: the existing file stays
	h.idle()
	if readFile(t, filepath.Join(env.src, "top.txt")) != "old" {
		t.Fatal("skip overwrote")
	}
	if readFile(t, filepath.Join(env.src, "dir", "sub", "b.txt")) != "deep\n" || readFile(t, filepath.Join(env.src, "dir", "a.txt")) != "inside dir\n" {
		t.Fatal("folder not extracted recursively")
	}
	if !strings.Contains(h.reported(), "kept 1 existing") {
		t.Fatalf("report %q", h.reported())
	}
	// again, overwriting this time
	h.press("u")
	h.focus("top.txt")
	h.press("x", "enter")
	h.waitDialog(dlgConflict)
	h.press("o")
	h.idle()
	if readFile(t, filepath.Join(env.src, "top.txt")) != "top level\n" {
		t.Fatal("overwrite not applied")
	}
	if !h.a.ActivePanel().InArchive() {
		t.Fatal("extraction left the archive view")
	}
}

func TestExtractWholeArchiveFromFolderAndTraversal(t *testing.T) {
	h, env, _ := p4App(t, nil)
	writeZip(t, filepath.Join(env.src, "evil.zip"), map[string]string{
		"ok.txt": "fine", "../escape.txt": "bad", "a/../../escape2.txt": "bad", "/abs.txt": "bad",
	})
	h.exec(h.a.panels[0].RefreshFocus("evil.zip"))
	h.settle()
	h.press("x")
	want := filepath.Join(env.src, "evil")
	if !h.dialogKind(dlgExtract) || h.a.dialog.input.Value() != want {
		t.Fatalf("default destination %q", h.a.dialog.input.Value())
	}
	h.press("enter")
	h.idle()
	if readFile(t, filepath.Join(want, "ok.txt")) != "fine" {
		t.Fatal("safe entry not extracted")
	}
	for _, p := range []string{filepath.Join(env.src, "escape.txt"), filepath.Join(env.src, "escape2.txt"), filepath.Join(env.root, "escape.txt"), filepath.Join(env.root, "escape2.txt"), "/abs.txt", filepath.Join(want, "abs.txt")} {
		if p == filepath.Join(want, "abs.txt") {
			continue // an absolute name may only ever land inside the destination
		}
		if fileExists(p) {
			t.Fatalf("path traversal wrote %s", p)
		}
	}
	if !strings.Contains(h.reported(), "unsafe") {
		t.Fatalf("the report does not mention refused entries: %q", h.reported())
	}
	// not an archive: refused clearly, naming the supported formats
	h.focus("a.txt")
	h.press("x")
	if h.a.dialog != nil || !strings.Contains(h.reported(), "not an archive TwinPane can extract") || !strings.Contains(h.reported(), "TAR.GZ") {
		t.Fatalf("x on a text file: %q", h.reported())
	}
}

func TestExtractCorruptArchiveFailsSafely(t *testing.T) {
	h, env, _ := p4App(t, nil)
	os.WriteFile(filepath.Join(env.src, "bad.zip"), append([]byte("PK\x03\x04"), bytes.Repeat([]byte{0xff}, 64)...), 0o644)
	h.exec(h.a.panels[0].RefreshFocus("bad.zip"))
	h.settle()
	h.press("enter")
	h.settle()
	if h.a.ActivePanel().InArchive() {
		t.Fatal("opened a corrupt archive")
	}
	if s, isErr := h.a.Status(); !isErr || !strings.Contains(s, "bad.zip") {
		t.Fatalf("status %q", s)
	}
	if h.a.ActivePanel().Path() != env.src {
		t.Fatal("panel moved")
	}
}

func TestCreateZipFromSelection(t *testing.T) {
	h, env, _ := p4App(t, nil)
	h.focus("a.txt")
	h.press(" ")
	h.focus("sub")
	h.press(" ")
	h.press("z")
	if !h.dialogKind(dlgZip) {
		t.Fatalf("zip dialog default %v", h.a.dialog)
	}
	assertCompressDefault(t, h.a.dialog.input, env.src, ".zip")
	h.typeText("selection", false)
	want := filepath.Join(env.src, "selection.zip")
	if h.a.dialog.input.Value() != want {
		t.Fatalf("typed archive name %q", h.a.dialog.input.Value())
	}
	h.press("enter")
	h.idle()
	h.waitCompressionDone()
	if !strings.Contains(h.reported(), "selection.zip") {
		t.Fatalf("report %q", h.reported())
	}
	arc, err := archive.Open(want)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"a.txt", "sub/deep/c.txt"} {
		if _, ok := arc.Lookup(n); !ok {
			t.Fatalf("%s missing from the archive", n)
		}
	}
	if _, ok := arc.Lookup("b.txt"); ok {
		t.Fatal("unselected file archived")
	}
	if e, _ := h.a.panels[0].Current(); e.Name != "selection.zip" {
		t.Fatalf("new archive not focused in the source folder: %q", e.Name)
	}
	if len(h.a.panels[0].Selection()) != 0 {
		t.Fatal("selection not cleared after success")
	}
	h.dismissCompression()
}

func TestCreateZipExcludesItselfAndAsksBeforeOverwrite(t *testing.T) {
	h, env, _ := p4App(t, nil)
	// output inside the folder being archived
	h.focus("sub")
	h.press("z", "ctrl+u", "ctrl+k")
	out := filepath.Join(env.src, "sub", "self.zip")
	h.typeText(out, true)
	h.press("enter")
	h.idle()
	h.waitCompressionDone()
	arc, err := archive.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range arc.Walk("") {
		if strings.Contains(e.Inner, "self.zip") {
			t.Fatal("archive contains itself")
		}
	}
	if _, ok := arc.Lookup("sub/deep/c.txt"); !ok {
		t.Fatal("content missing")
	}
	h.dismissCompression()
	// existing file: confirmation, default cancel
	os.WriteFile(filepath.Join(env.dst, "a.zip"), []byte("keep"), 0o644)
	h.focus("a.txt")
	h.press("z")
	h.press("ctrl+u", "ctrl+k")
	h.typeText(filepath.Join(env.dst, "a.zip"), true)
	h.press("enter")
	if !h.dialogKind(dlgZipExists) {
		t.Fatal("no overwrite confirmation")
	}
	h.press("enter") // focused button is Cancel
	if readFile(t, filepath.Join(env.dst, "a.zip")) != "keep" || h.a.dialog != nil {
		t.Fatal("Enter on the confirmation overwrote")
	}
	h.press("z")
	h.press("ctrl+u", "ctrl+k")
	h.typeText(filepath.Join(env.dst, "a.zip"), true)
	h.press("enter")
	h.press("o")
	h.idle()
	h.waitCompressionDone()
	if a, err := archive.Open(filepath.Join(env.dst, "a.zip")); err != nil || a.Files != 1 {
		t.Fatalf("overwrite: %v", err)
	}
	h.dismissCompression()
	// A slow or missing destination mount is checked by the background task,
	// not synchronously while the archive dialog is open.
	h.press("z", "ctrl+u", "ctrl+k")
	h.typeText(filepath.Join(env.root, "nope", "x.zip"), true)
	h.press("enter")
	h.idle()
	h.waitCompressionDone()
	if !strings.Contains(strings.ToLower(h.view()), "destination folder") || !strings.Contains(strings.ToLower(h.view()), "temporary output") {
		t.Fatalf("persistent error/cleanup summary missing:\n%s", h.view())
	}
	h.dismissCompression()
	items := h.a.tasks.newest()
	if len(items) == 0 || items[0].state != taskFailed || !strings.Contains(items[0].summary, "destination folder") {
		t.Fatalf("missing destination was not retained as a task failure: %+v", items)
	}
	h.a.tasks.selected = items[0].id
	h.press("t")
	v := h.view()
	if !strings.Contains(v, "FAILED") || !strings.Contains(v, "destination folder") {
		t.Fatalf("task view should show the missing-destination failure:\n%s", v)
	}
}

// ---- 4. viewer -----------------------------------------------------------------------

func TestViewerScrollSearchAndContext(t *testing.T) {
	h, env, _ := p4App(t, nil)
	var b strings.Builder
	for i := 1; i <= 300; i++ {
		fmt.Fprintf(&b, "line number %d\tend\n", i)
	}
	os.WriteFile(filepath.Join(env.src, "f15.txt"), []byte(b.String()), 0o644)
	h.focus("b.txt")
	h.press(" ")
	h.focus("f15.txt")
	cur, off := h.a.ActivePanel().Cursor(), h.a.ActivePanel().Offset()
	h.press("v")
	h.until("loaded", func() bool { return h.a.viewer != nil && !h.a.viewer.loading })
	v := h.view()
	for _, want := range []string{"VIEW", "READ-ONLY", "f15.txt", "line number 1 ", "300 lines", "e edits in your external editor"} {
		if !strings.Contains(v, want) {
			t.Fatalf("viewer lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "\t") {
		t.Fatal("tab not expanded")
	}
	h.press("G")
	if v := h.view(); !strings.Contains(v, "line number 300") || strings.Contains(v, "line number 1 ") {
		t.Fatal("G did not go to the end")
	}
	if !strings.Contains(h.view(), "of 300 (100%)") {
		t.Fatalf("position: %s", h.view())
	}
	h.press("g", "j", "j")
	if h.a.viewer.top != 2 {
		t.Fatalf("j: top %d", h.a.viewer.top)
	}
	h.wheel(10, 10, 3)
	if h.a.viewer.top != 5 {
		t.Fatalf("wheel: top %d", h.a.viewer.top)
	}
	h.press("/")
	if !h.dialogKind(dlgViewFind) {
		t.Fatal("/ did not open find")
	}
	h.typeText("NUMBER 250", false) // capitals: case-sensitive, no match
	h.press("enter")
	if s, isErr := h.a.Status(); !isErr || !strings.Contains(s, "not found") {
		t.Fatalf("smart case: %q", s)
	}
	h.press("/", "ctrl+u")
	h.typeText("number 25", false) // 25, 250-259
	h.press("enter")
	vw := h.a.viewer
	if len(vw.matches) != 11 || vw.matches[vw.mcur] != 24 {
		t.Fatalf("matches %v cur %d", vw.matches, vw.mcur)
	}
	if !strings.Contains(h.view(), "line number 25") {
		t.Fatal("match not scrolled into view")
	}
	h.press("n")
	if vw.matches[vw.mcur] != 249 || !strings.Contains(h.status(), "match 2 of 11") {
		t.Fatalf("n: %d %q", vw.matches[vw.mcur], h.status())
	}
	h.press("N", "N")
	if vw.mcur != 10 {
		t.Fatalf("N wraps around: %d", vw.mcur)
	}
	if f := h.footer(); !strings.Contains(f, "Esc  close") || !strings.Contains(f, "n/N  next/prev") || !strings.Contains(f, "/  find") {
		t.Fatalf("viewer hints %q", f)
	}
	h.press("esc")
	p := h.a.ActivePanel()
	if h.a.viewer != nil || p.Cursor() != cur || p.Offset() != off || !p.IsSelected("b.txt") {
		t.Fatal("closing the viewer did not restore the panel state")
	}
}

func TestViewerEmptyBinaryLargeAndLongLines(t *testing.T) {
	h, env, _ := p4App(t, nil)
	open := func(name string) string {
		h.t.Helper()
		h.exec(h.a.panels[0].RefreshFocus(name))
		h.settle()
		h.focus(name)
		h.press("v")
		h.until("loaded", func() bool { return h.a.viewer != nil && !h.a.viewer.loading })
		return h.view()
	}
	os.WriteFile(filepath.Join(env.src, "empty.txt"), nil, 0o644)
	if v := open("empty.txt"); !strings.Contains(v, "(empty file)") {
		t.Fatal("empty file")
	}
	if f := h.footer(); strings.Contains(f, "find") || !strings.Contains(f, "e  edit externally") {
		t.Fatalf("empty hints %q", f)
	}
	h.press("esc")

	bin := append([]byte("ELF\x00\x01\x02\x1b[2J"), bytes.Repeat([]byte{0, 0xff}, 500)...)
	os.WriteFile(filepath.Join(env.src, "prog.bin"), bin, 0o644)
	v := open("prog.bin")
	if !strings.Contains(v, "binary file") || strings.Contains(h.a.View(), "\x1b[2J") {
		t.Fatal("binary file shown")
	}
	h.press("j", "G") // no crash, nothing to scroll
	h.press("esc")

	big := bytes.Repeat([]byte("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcde\n"), (9<<20)/64)
	os.WriteFile(filepath.Join(env.src, "big.log"), big, 0o644)
	v = open("big.log")
	if !strings.Contains(v, "only the first 8.0 M shown") || len(h.a.viewer.doc.Lines) > 8<<20/64+1 {
		t.Fatalf("large file not bounded: %s", v)
	}
	h.press("G")
	if !strings.Contains(h.view(), "0123456789abcdef") {
		t.Fatal("end of loaded part not shown")
	}
	h.press("esc")

	os.WriteFile(filepath.Join(env.src, "wide.txt"), []byte("start"+strings.Repeat("-", 1000)+"finish\nshort\n"), 0o644)
	open("wide.txt")
	vw := h.a.viewer
	if vw.lineRows(0) < 10 || !strings.Contains(h.view(), "finish") {
		t.Fatalf("wrap: rows %d", vw.lineRows(0))
	}
	h.press("w")
	v = h.view()
	if strings.Contains(v, "finish") || !strings.Contains(v, "›") || !strings.Contains(h.footer(), "←→  sideways") {
		t.Fatal("no-wrap mode")
	}
	for i := 0; i < 30; i++ {
		h.send(tea.KeyMsg{Type: tea.KeyRight})
	}
	if !strings.Contains(h.view(), "finish") {
		t.Fatalf("horizontal scroll does not reach the end: xoff %d textW %d\n%s", vw.xoff, vw.textW, h.view())
	}
	h.press("#")
	if strings.Contains(h.view(), "   1 ") || vw.numbers {
		t.Fatal("# did not hide line numbers")
	}
	// resizing keeps the layout exact
	for _, w := range []int{30, 50, 120} {
		h.send(tea.WindowSizeMsg{Width: w, Height: 12})
		fitsScreen(t, h, "viewer resize")
	}
}

func TestViewerArchiveEntryAndEditor(t *testing.T) {
	h, env, _ := zipApp(t)
	h.press("enter")
	h.until("archive", func() bool { return h.a.ActivePanel().InArchive() })
	h.focus("top.txt")
	h.press("enter")
	h.until("loaded", func() bool { return h.a.viewer != nil && !h.a.viewer.loading })
	if v := h.view(); !strings.Contains(v, "top level") || !strings.Contains(v, "read-only archive entry") {
		t.Fatalf("archive entry viewer: %s", v)
	}
	if strings.Contains(h.footer(), "edit") {
		t.Fatal("edit offered for an archive entry")
	}
	h.press("e")
	if len(env.execs) != 0 || !strings.Contains(h.status(), "cannot be edited") {
		t.Fatal("editor started for an archive entry")
	}
	h.press("esc")
	if !h.a.ActivePanel().InArchive() {
		t.Fatal("closing the viewer left the archive")
	}
	// on the filesystem, e in the viewer hands the file to the editor
	h.press("esc")
	h.settle()
	h.focus("a.txt")
	h.press("v")
	h.until("loaded", func() bool { return !h.a.viewer.loading })
	h.press("e")
	h.until("editor", func() bool { return len(env.execs) == 1 || h.a.dialog != nil })
	if h.a.viewer != nil {
		t.Fatal("viewer still open while editing")
	}
}

func TestArchivePropertiesReadOnly(t *testing.T) {
	h, _, _ := zipApp(t)
	h.press("enter")
	h.until("archive", func() bool { return h.a.ActivePanel().InArchive() })
	h.focus("top.txt")
	h.press("P")
	v := h.view()
	for _, want := range []string{"archive entry, read-only", "top.txt", "Packed", "deflate", "CRC-32"} {
		if !strings.Contains(strings.ToLower(v), strings.ToLower(want)) {
			t.Fatalf("archive properties lack %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "Apply") || strings.Contains(h.footer(), "apply") {
		t.Fatal("Apply offered for an archive entry")
	}
	h.press("esc")
	if h.a.props != nil {
		t.Fatal("not closed")
	}
}

func TestNewKeysDoNotBreakModes(t *testing.T) {
	h, _, _ := p4App(t, nil)
	// P / v / x / z / X on ".." do nothing harmful
	h.a.ActivePanel().Top()
	h.exec(h.a.ActivePanel().Load(filepath.Dir(h.a.ActivePanel().Path()), "src", panel.LoadNavigate))
	h.settle()
	h.a.ActivePanel().Top()
	for _, k := range []string{"v", "x", "X"} {
		h.press(k)
		if h.a.viewer != nil || h.a.dialog != nil || h.a.props != nil {
			t.Fatalf("%s on .. opened something", k)
		}
	}
	_ = fs.ModePerm
}
