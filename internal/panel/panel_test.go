package panel

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/theme"
)

func apply(t *testing.T, p *Panel, cmd tea.Cmd) error {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a command")
	}
	msg, ok := cmd().(LoadedMsg)
	if !ok {
		t.Fatalf("unexpected msg type %T", cmd())
	}
	return p.Apply(msg)
}

func tree(t *testing.T, nfiles int) string {
	t.Helper()
	dir := t.TempDir()
	for _, d := range []string{"alpha", "beta", "gamma"} {
		if err := os.MkdirAll(filepath.Join(dir, d, "inner"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < nfiles; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("file%03d.txt", i)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func newLoaded(t *testing.T, dir string) *Panel {
	t.Helper()
	p := New(0, nil)
	p.SetSize(60, 12) // innerH 10 -> 5 list rows
	if err := apply(t, p, p.Load(dir, "", LoadForce)); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOpenAndParent(t *testing.T) {
	dir := tree(t, 3)
	p := newLoaded(t, dir)
	if p.Path() != dir || p.Cursor() != 0 {
		t.Fatalf("initial state path=%s cursor=%d", p.Path(), p.Cursor())
	}
	p.Down()
	p.Down() // ".." alpha beta
	if e, _ := p.Current(); e.Name != "beta" {
		t.Fatalf("cursor on %q", e.Name)
	}
	if err := apply(t, p, p.Open()); err != nil {
		t.Fatal(err)
	}
	if p.Path() != filepath.Join(dir, "beta") {
		t.Fatalf("path after open = %s", p.Path())
	}
	if err := apply(t, p, p.Parent()); err != nil {
		t.Fatal(err)
	}
	if e, _ := p.Current(); p.Path() != dir || e.Name != "beta" {
		t.Fatalf("parent should focus beta, got path=%s cursor=%q", p.Path(), e.Name)
	}
	// Enter on ".." behaves like Parent
	if err := apply(t, p, p.Open()); err != nil { // open beta again
		t.Fatal(err)
	}
	p.Top()
	if err := apply(t, p, p.Open()); err != nil {
		t.Fatal(err)
	}
	if p.Path() != dir {
		t.Fatalf("enter on .. went to %s", p.Path())
	}
	// Open on a file returns no command
	p.Bottom()
	if p.Open() != nil {
		t.Fatal("opening a file should be a no-op in phase 1")
	}
}

func TestScrolling(t *testing.T) {
	dir := tree(t, 50) // 1 + 3 + 50 = 54 entries
	p := newLoaded(t, dir)
	if p.listRows() != 5 {
		t.Fatalf("rows = %d", p.listRows())
	}
	for i := 0; i < 10; i++ {
		p.Down()
	}
	if p.Cursor() != 10 || p.Offset() != 6 {
		t.Fatalf("cursor=%d offset=%d", p.Cursor(), p.Offset())
	}
	p.PageDown()
	if p.Cursor() != 14 {
		t.Fatalf("pagedown cursor=%d", p.Cursor())
	}
	p.Bottom()
	if p.Cursor() != 53 || p.Offset() != 49 {
		t.Fatalf("bottom cursor=%d offset=%d", p.Cursor(), p.Offset())
	}
	p.Down()
	if p.Cursor() != 53 {
		t.Fatal("cursor must not pass the end")
	}
	p.PageUp()
	p.Top()
	p.Up()
	if p.Cursor() != 0 || p.Offset() != 0 {
		t.Fatalf("top cursor=%d offset=%d", p.Cursor(), p.Offset())
	}
	// Shrinking keeps cursor visible
	p.Bottom()
	p.SetSize(60, 30)
	p.SetSize(60, 9)
	if p.Cursor() < p.Offset() || p.Cursor() >= p.Offset()+p.listRows() {
		t.Fatalf("cursor %d not visible (offset %d rows %d)", p.Cursor(), p.Offset(), p.listRows())
	}
}

func TestNavigationErrorKeepsState(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs non-root unix")
	}
	dir := tree(t, 1)
	locked := filepath.Join(dir, "gamma")
	os.Chmod(locked, 0)
	defer os.Chmod(locked, 0o755)
	p := newLoaded(t, dir)
	p.Down()
	p.Down()
	p.Down() // gamma
	err := apply(t, p, p.Open())
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("expected permission error, got %v", err)
	}
	if e, _ := p.Current(); p.Path() != dir || e.Name != "gamma" || p.Err() != nil {
		t.Fatalf("state changed after failed navigation: %s %q %v", p.Path(), e.Name, p.Err())
	}
	// Forced load into the locked dir shows the error in the panel
	if err := apply(t, p, p.Load(locked, "", LoadForce)); err != nil {
		t.Fatal(err)
	}
	if p.Err() == nil || p.Path() != locked || len(p.Entries()) != 1 || !p.Entries()[0].IsParent {
		t.Fatalf("forced error state wrong: err=%v entries=%d", p.Err(), len(p.Entries()))
	}
	view := p.View(theme.Default(), true)
	if !strings.Contains(view, "permission denied") {
		t.Fatalf("error not rendered:\n%s", view)
	}
	// ...and parent navigation recovers
	if err := apply(t, p, p.Parent()); err != nil {
		t.Fatal(err)
	}
	if p.Err() != nil || p.Path() != dir {
		t.Fatal("did not recover via parent")
	}
}

func TestStaleLoadIgnored(t *testing.T) {
	dir := tree(t, 1)
	p := newLoaded(t, dir)
	first := p.Load(filepath.Join(dir, "alpha"), "", LoadNavigate)
	second := p.Load(filepath.Join(dir, "beta"), "", LoadNavigate)
	if err := apply(t, p, second); err != nil {
		t.Fatal(err)
	}
	if err := apply(t, p, first); err != nil {
		t.Fatal(err)
	}
	if p.Path() != filepath.Join(dir, "beta") || p.Loading() {
		t.Fatalf("stale result applied: %s", p.Path())
	}
}

func TestSelection(t *testing.T) {
	dir := tree(t, 4)
	p := newLoaded(t, dir)
	p.ToggleSelect() // on "..": not selectable, cursor advances
	if len(p.Selection()) != 0 || p.Cursor() != 1 {
		t.Fatal("parent entry must not be selectable")
	}
	p.ToggleSelect()
	p.ToggleSelect()
	if got := len(p.Selection()); got != 2 {
		t.Fatalf("selected %d", got)
	}
	// Refresh keeps selection
	if err := apply(t, p, p.Refresh()); err != nil {
		t.Fatal(err)
	}
	if len(p.Selection()) != 2 || p.Cursor() != 3 {
		t.Fatalf("refresh lost selection/cursor: %d %d", len(p.Selection()), p.Cursor())
	}
	p.SelectAll()
	if len(p.Selection()) != 7 {
		t.Fatalf("select all = %d", len(p.Selection()))
	}
	p.SelectAll()
	if len(p.Selection()) != 0 {
		t.Fatal("second select-all should clear")
	}
	p.SelectAll()
	p.ClearSelection()
	if len(p.Selection()) != 0 {
		t.Fatal("clear failed")
	}
	p.SelectAll()
	p.Top()
	p.Down()
	if err := apply(t, p, p.Open()); err != nil {
		t.Fatal(err)
	}
	if len(p.Selection()) != 0 {
		t.Fatal("changing directory must clear selection")
	}
}

func TestRefreshDeletedDirClimbs(t *testing.T) {
	dir := tree(t, 1)
	p := newLoaded(t, dir)
	inner := filepath.Join(dir, "alpha", "inner")
	if err := apply(t, p, p.ChangeDir(inner)); err != nil {
		t.Fatal(err)
	}
	os.RemoveAll(filepath.Join(dir, "alpha"))
	if err := apply(t, p, p.Refresh()); err != nil {
		t.Fatal(err)
	}
	if p.Path() != dir || p.Err() != nil {
		t.Fatalf("refresh of deleted dir: path=%s err=%v", p.Path(), p.Err())
	}
}

func TestHiddenAndSortReload(t *testing.T) {
	dir := tree(t, 1)
	os.WriteFile(filepath.Join(dir, ".secret"), nil, 0o644)
	p := newLoaded(t, dir)
	n := len(p.Entries())
	if err := apply(t, p, p.ToggleHidden()); err != nil {
		t.Fatal(err)
	}
	if len(p.Entries()) != n+1 {
		t.Fatal("hidden file not shown")
	}
	if err := apply(t, p, p.CycleSort()); err != nil {
		t.Fatal(err)
	}
	if p.Options().Sort != fsys.SortSize {
		t.Fatal("sort did not cycle")
	}
}

func TestViewDimensions(t *testing.T) {
	dir := tree(t, 30)
	os.WriteFile(filepath.Join(dir, "very-long-file-name-with-unicode-文件名-and-more-and-more.txt"), nil, 0o644)
	os.WriteFile(filepath.Join(dir, "evil\nname\x1b[2J"), nil, 0o644)
	p := newLoaded(t, dir)
	th := theme.Default()
	for w := 3; w <= 120; w += 7 {
		for h := 3; h <= 40; h += 3 {
			p.SetSize(w, h)
			p.Bottom()
			for _, active := range []bool{true, false} {
				out := p.View(th, active)
				lines := strings.Split(out, "\n")
				if len(lines) != h {
					t.Fatalf("%dx%d: %d lines", w, h, len(lines))
				}
				for i, l := range lines {
					if lw := ansi.StringWidth(l); lw != w {
						t.Fatalf("%dx%d line %d width %d: %q", w, h, i, lw, l)
					}
					if strings.Contains(l, "\x1b[2J") {
						t.Fatal("control sequence leaked from filename")
					}
				}
			}
		}
	}
}

func TestEmptyDirMessage(t *testing.T) {
	dir := t.TempDir()
	p := newLoaded(t, dir)
	if !strings.Contains(p.View(theme.Default(), true), "empty directory") {
		t.Fatal("empty message missing")
	}
}

func TestTargetsAndNextFocus(t *testing.T) {
	dir := tree(t, 3) // .., alpha, beta, gamma, file000..002
	p := newLoaded(t, dir)
	if len(p.Targets()) != 0 {
		t.Fatal("'..' must never be a target")
	}
	p.Down() // alpha
	if ts := p.Targets(); len(ts) != 1 || ts[0].Name != "alpha" {
		t.Fatalf("cursor target: %+v", ts)
	}
	p.ToggleSelect() // alpha, cursor -> beta
	p.Down()         // gamma
	p.ToggleSelect() // gamma, cursor -> file000
	if ts := p.Targets(); len(ts) != 2 || ts[0].Name != "alpha" || ts[1].Name != "gamma" {
		t.Fatalf("selection targets: %+v", ts)
	}
	p.Top()
	p.Down() // alpha
	if got := p.NextFocus(map[string]bool{"alpha": true, "beta": true}); got != "gamma" {
		t.Fatalf("NextFocus = %q", got)
	}
	p.Bottom() // file002
	if got := p.NextFocus(map[string]bool{"file002.txt": true}); got != "file001.txt" {
		t.Fatalf("NextFocus at end = %q", got)
	}
	// RefreshFocus places the cursor on the named entry
	if err := apply(t, p, p.RefreshFocus("beta")); err != nil {
		t.Fatal(err)
	}
	if e, _ := p.Current(); e.Name != "beta" {
		t.Fatalf("RefreshFocus cursor on %q", e.Name)
	}
}
