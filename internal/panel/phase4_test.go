package panel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/theme"
)

// thumbRows returns the inner line indices whose right border is the
// scroll thumb, and checks the frame geometry on the way.
func thumbRows(t *testing.T, p *Panel, active bool) []int {
	t.Helper()
	lines := strings.Split(ansi.Strip(p.View(theme.Default(), active)), "\n")
	if len(lines) != p.height {
		t.Fatalf("view has %d lines, want %d", len(lines), p.height)
	}
	var rows []int
	for i, l := range lines {
		if w := ansi.StringWidth(l); w != p.width {
			t.Fatalf("line %d is %d wide, want %d: %q", i, w, p.width, l)
		}
		r := []rune(l)
		if string(r[len(r)-1]) == ScrollThumbChar {
			rows = append(rows, i-1)
		}
		if string(r[0]) == ScrollThumbChar {
			t.Fatal("thumb on the left border")
		}
	}
	return rows
}

func TestScrollIndicatorOnBorder(t *testing.T) {
	dir := tree(t, 40) // .. + 3 dirs + 40 files = 44 entries
	p := newLoaded(t, dir)
	p.SetSize(40, 14) // innerH 12 -> 7 list rows
	l := p.layout()
	rows := thumbRows(t, p, true)
	if len(rows) == 0 {
		t.Fatal("no indicator for an overflowing list")
	}
	first := p.rowStart(l)
	if rows[0] != first {
		t.Fatalf("at the top the thumb starts at inner line %d, want %d", rows[0], first)
	}
	// size: 7 rows * 7/44 -> 1 cell
	if len(rows) != 1 {
		t.Fatalf("thumb %v, want 1 cell", rows)
	}
	// inactive panels show it too (dimmer, on the plain border)
	if got := thumbRows(t, p, false); len(got) != 1 {
		t.Fatalf("inactive panel: %v", got)
	}
	p.Bottom()
	rows = thumbRows(t, p, true)
	if rows[len(rows)-1] != first+l.rows-1 {
		t.Fatalf("at the bottom the thumb ends at %d, want %d", rows[len(rows)-1], first+l.rows-1)
	}
	// the names are untouched: every row keeps its full inner width
	p.Top()
	p.MoveBy(20)
	mid := thumbRows(t, p, true)
	if mid[0] <= first || mid[0] >= first+l.rows-1 {
		t.Fatalf("middle offset drawn at an end: %v", mid)
	}
}

func TestScrollIndicatorHiddenWhenAllFits(t *testing.T) {
	p := newLoaded(t, tree(t, 2)) // 6 entries
	p.SetSize(40, 20)
	if rows := thumbRows(t, p, true); len(rows) != 0 {
		t.Fatalf("indicator shown although everything fits: %v", rows)
	}
	// resizing smaller makes it appear, larger again removes it
	p.SetSize(40, 10) // 3 list rows
	if rows := thumbRows(t, p, true); len(rows) == 0 {
		t.Fatal("no indicator after shrinking")
	}
	p.SetSize(40, 30)
	if rows := thumbRows(t, p, true); len(rows) != 0 {
		t.Fatal("indicator after growing")
	}
	// compact layouts (no header / footer) still align
	for _, h := range []int{3, 4, 5, 6, 9} {
		p.SetSize(30, h)
		thumbRows(t, p, true)
	}
}

func TestScrollByMovesViewportNotCursor(t *testing.T) {
	p := newLoaded(t, tree(t, 40)) // 5 list rows
	p.MoveBy(2)
	p.ScrollBy(3) // cursor 2 would leave the view: pulled to the top row
	if p.Offset() != 3 || p.Cursor() != 3 {
		t.Fatalf("offset %d cursor %d", p.Offset(), p.Cursor())
	}
	p.SetCursor(5)
	p.ScrollBy(1) // cursor 5 still visible (rows 4..8): unchanged
	if p.Offset() != 4 || p.Cursor() != 5 {
		t.Fatalf("offset %d cursor %d", p.Offset(), p.Cursor())
	}
	p.ScrollBy(-100)
	if p.Offset() != 0 || p.Cursor() != 4 {
		t.Fatalf("scroll to top: offset %d cursor %d", p.Offset(), p.Cursor())
	}
	p.ScrollBy(1000)
	total, visible, off := p.ScrollInfo()
	if off != total-visible || p.Cursor() < off {
		t.Fatalf("scroll to bottom: offset %d of %d/%d cursor %d", off, total, visible, p.Cursor())
	}
	p.ScrollToTrack(0)
	if p.Offset() != 0 {
		t.Fatal("track top")
	}
	p.ScrollToTrack(1 << 20)
	if _, _, off := p.ScrollInfo(); off != total-visible {
		t.Fatal("track bottom")
	}
}

func TestHitTest(t *testing.T) {
	p := newLoaded(t, tree(t, 40)) // 60x12: border, path, header, rows at y=3..7
	p.ScrollBy(2)
	if h := p.HitTest(5, 3); h.Kind != HitRow || h.Index != 2 {
		t.Fatalf("first row: %+v", h)
	}
	if h := p.HitTest(5, 7); h.Kind != HitRow || h.Index != 6 {
		t.Fatalf("last row: %+v", h)
	}
	for _, xy := range [][2]int{{5, 0}, {5, 1}, {5, 2}, {0, 4}, {5, 8}, {5, 11}} {
		if h := p.HitTest(xy[0], xy[1]); h.Kind != HitChrome {
			t.Fatalf("%v: %+v", xy, h)
		}
	}
	if h := p.HitTest(59, 5); h.Kind != HitScrollbar || h.Index != 2 || h.Track != 5 {
		t.Fatalf("scrollbar: %+v", h)
	}
	if h := p.HitTest(60, 5); h.Kind != HitNone {
		t.Fatalf("outside: %+v", h)
	}
	// short list: the border is part of the row, rows below the end are empty
	q := newLoaded(t, tree(t, 0)) // 4 entries, 5 rows
	if h := q.HitTest(59, 4); h.Kind != HitRow || h.Index != 1 {
		t.Fatalf("no indicator: %+v", h)
	}
	if h := q.HitTest(5, 7); h.Kind != HitEmpty {
		t.Fatalf("below the list: %+v", h)
	}
	if q.TrackCell(3) != 0 || q.TrackCell(7) != 4 {
		t.Fatal("TrackCell")
	}
}

func TestSetShowHiddenKeepsEntryAndScreenRow(t *testing.T) {
	dir := tree(t, 40)
	for _, n := range []string{".a", ".b", ".c"} {
		os.WriteFile(filepath.Join(dir, n), nil, 0o644)
	}
	p := newLoaded(t, dir)
	p.SetCursor(20)
	p.ScrollBy(-2) // cursor on the 3rd visible row
	name, row := p.entries[p.cursor].Name, p.cursor-p.offset
	if err := apply(t, p, p.SetShowHidden(true)); err != nil {
		t.Fatal(err)
	}
	if !p.Options().ShowHidden || p.entries[p.cursor].Name != name || p.cursor-p.offset != row {
		t.Fatalf("after showing: on %q row %d, want %q row %d", p.entries[p.cursor].Name, p.cursor-p.offset, name, row)
	}
	// the cursor on a hidden file: hiding lands on its neighbour
	p.Top()
	for p.entries[p.cursor].Name != ".b" {
		p.Down()
	}
	if err := apply(t, p, p.SetShowHidden(false)); err != nil {
		t.Fatal(err)
	}
	if got := p.entries[p.cursor].Name; strings.HasPrefix(got, ".") && got != ".." {
		t.Fatalf("cursor on hidden %q after hiding", got)
	}
	// ToggleHidden still works (compatibility)
	if err := apply(t, p, p.ToggleHidden()); err != nil || !p.Options().ShowHidden {
		t.Fatal("ToggleHidden")
	}
}
