package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// Scrolling inside a dialog body (the format chooser and every other long
// list): the view follows the highlighted row and the right border carries
// the scroll indicator, exactly like the panels.

// dialogFrame cuts the dialog's box out of the current frame: one string per
// screen row (top border first) and the screen column of the right border.
// The box is found on its top border row, so the dimmed panels around it can
// never be mistaken for dialog content.
func dialogFrame(t *testing.T, h *harness) (rows []string, right int) {
	t.Helper()
	d := h.a.dialog
	if d == nil {
		t.Fatalf("no dialog")
	}
	lines := strings.Split(ansi.Strip(h.a.View()), "\n")
	if d.boxY0 < 0 || d.boxY0 >= len(lines) {
		t.Fatalf("dialog box row %d outside the frame:\n%s", d.boxY0, h.a.View())
	}
	top := []rune(lines[d.boxY0])
	left, end := -1, -1
	for i, r := range top {
		if r != '╭' {
			continue
		}
		for j := i + 1; j < len(top); j++ {
			if top[j] == '╮' {
				if j-i > end-left {
					left, end = i, j
				}
				break
			}
		}
	}
	if left < 0 || end <= left {
		t.Fatalf("could not locate the dialog box:\n%s", h.a.View())
	}
	for y := d.boxY0; y <= d.boxY1 && y < len(lines); y++ {
		r := []rune(lines[y])
		if left >= len(r) {
			continue
		}
		rows = append(rows, string(r[left:min(end+1, len(r))]))
	}
	return rows, end
}

// thumbRows returns the body rows (0 = first visible body row) where the
// dialog's right border shows the scroll indicator.
func thumbRows(h *harness) []int {
	d := h.a.dialog
	if d == nil {
		return nil
	}
	lines := strings.Split(ansi.Strip(h.a.View()), "\n")
	top := []rune(lines[d.boxY0])
	right := -1
	for i, r := range top {
		if r != '╭' {
			continue
		}
		for j := i + 1; j < len(top); j++ {
			if top[j] == '╮' {
				if j-i > right {
					right = j
				}
				break
			}
		}
	}
	if right < 0 {
		return nil
	}
	var rows []int
	for y := d.boxY0; y <= d.boxY1 && y < len(lines); y++ {
		r := []rune(lines[y])
		if right < len(r) && string(r[right]) == "┃" {
			rows = append(rows, y-d.boxY0-3) // body row under title + gap
		}
	}
	return rows
}

// cursorOnScreen reports whether the highlighted list row is visible in the
// dialog box.
func cursorOnScreen(t *testing.T, h *harness) bool {
	t.Helper()
	d := h.a.dialog
	if d == nil || d.list == nil || d.listCursor >= len(d.list) {
		t.Fatalf("no list cursor to check")
	}
	label := strings.TrimSpace(d.list[d.listCursor].label)
	rows, _ := dialogFrame(t, h)
	for _, row := range rows {
		if strings.Contains(row, label) {
			return true
		}
	}
	return false
}

// openChooser opens the format chooser on a 100x20 screen.
func openChooser(t *testing.T) *harness {
	t.Helper()
	h, _ := newOpsApp(t, nil, nil)
	h.send(tea.WindowSizeMsg{Width: 100, Height: 20})
	h.settle()
	h.press("n", "f", "ctrl+f")
	if !h.dialogKind(dlgFormat) {
		t.Fatalf("no format chooser:\n%s", h.view())
	}
	return h
}

func TestFormatChooserViewFollowsCursor(t *testing.T) {
	h := openChooser(t)
	h.view() // the dialog records whether it scrolls when it is drawn
	d := h.a.dialog
	if !d.scrollable {
		t.Fatalf("the full catalogue must not fit on a 100x20 screen (%d rows)", len(d.list))
	}
	h.view()
	if got := thumbRows(h); len(got) == 0 {
		t.Fatalf("no scroll indicator on the chooser's right border:\n%s", h.view())
	}
	// Walk down the whole first category and past it: the highlighted row
	// must stay on screen and the view must actually move.
	for i := 0; i < 40; i++ {
		h.press("down")
		if d.list[d.listCursor].header || d.list[d.listCursor].disabled {
			continue
		}
		if !cursorOnScreen(t, h) {
			t.Fatalf("after %d downs the highlighted row %q (%d) is off screen (scroll %d):\n%s",
				i+1, d.list[d.listCursor].label, d.listCursor, d.scroll, h.view())
		}
		if i > 15 && d.scroll == 0 {
			t.Fatalf("after %d downs the body never scrolled (cursor %d):\n%s", i+1, d.listCursor, h.view())
		}
	}
	// Scrolling up brings the view back to the top and keeps the cursor
	// visible on the way. (One more step up at the top wraps to the end,
	// which is tested separately.)
	first := d.firstSelectableRow()
	for i := 0; i < 4*len(d.list); i++ {
		if d.scroll == 0 && d.listCursor == first {
			break
		}
		h.press("up")
		if !cursorOnScreen(t, h) {
			t.Fatalf("up %d: highlighted row %q off screen (scroll %d):\n%s",
				i+1, d.list[d.listCursor].label, d.scroll, h.view())
		}
	}
	h.view()
	if d.scroll != 0 || d.listCursor != first {
		t.Fatalf("at the top: scroll %d cursor %d", d.scroll, d.listCursor)
	}
	if !strings.Contains(h.view(), "(none)") {
		t.Fatalf("the first row is not visible again:\n%s", h.view())
	}
}

// firstSelectableRow is the cursor position at the top of a fresh chooser.
func (d *dialog) firstSelectableRow() int {
	for i, it := range d.list {
		if !it.disabled {
			return i
		}
	}
	return 0
}

func TestFormatChooserScrollWrapsAround(t *testing.T) {
	h := openChooser(t)
	h.view()
	d := h.a.dialog
	first := d.firstSelectableRow()
	// Walk down until the list wraps back to its first row. Headings are
	// skipped, so this takes more steps than there are rows.
	steps := 0
	for {
		h.press("down")
		steps++
		if d.listCursor == first && steps > 1 {
			break
		}
		if steps > 4*len(d.list) {
			t.Fatalf("the list never wrapped (cursor %d)", d.listCursor)
		}
	}
	// From the first row, one step up wraps to the end, and that row must be
	// drawn: this is where the old code left the highlight off screen.
	h.press("up")
	if !cursorOnScreen(t, h) {
		t.Fatalf("wrapping to the last row left it off screen (cursor %d, scroll %d):\n%s",
			d.listCursor, d.scroll, h.view())
	}
	// And one step down from there wraps back to the top, which is drawn too.
	h.press("down")
	if d.listCursor != first {
		t.Fatalf("down from the last row: cursor %d, want %d", d.listCursor, first)
	}
	if !cursorOnScreen(t, h) || d.scroll != 0 {
		t.Fatalf("wrapping back to the top left scroll %d:\n%s", d.scroll, h.view())
	}
}

// The user-visible symptom: scrolling past the bottom and back must move the
// view, not lose the highlighted row or the indicator.
func TestFormatChooserScrollsDownAndBackUp(t *testing.T) {
	h := openChooser(t)
	h.view()
	d := h.a.dialog
	if d.scroll != 0 {
		t.Fatalf("a fresh chooser starts at scroll %d", d.scroll)
	}
	for i := 0; i < 12; i++ {
		h.press("down")
	}
	if v, _ := dialogFrame(t, h); strings.Contains(strings.Join(v, "\n"), "(none)") {
		t.Fatalf("the view did not move after 12 downs:\n%s", h.view())
	}
	back := d.scroll
	if back == 0 {
		t.Fatalf("scrolling never moved the body")
	}
	if !cursorOnScreen(t, h) {
		t.Fatalf("highlighted row off screen after scrolling down:\n%s", h.view())
	}
	for i := 0; i < 12; i++ {
		h.press("up")
	}
	h.view() // the body offset is applied when the dialog is drawn
	if d.scroll != 0 {
		t.Fatalf("scrolling back up left scroll at %d (was %d)", d.scroll, back)
	}
	if rows := thumbRows(h); len(rows) == 0 || rows[0] != 0 {
		t.Fatalf("the indicator is not back at the top of its track (rows %v):\n%s", rows, h.view())
	}
	if !strings.Contains(strings.Join(mustFrame(t, h), "\n"), "(none)") {
		t.Fatalf("the top row is not back:\n%s", h.view())
	}
}

func mustFrame(t *testing.T, h *harness) []string {
	t.Helper()
	rows, _ := dialogFrame(t, h)
	return rows
}

func TestFormatChooserWheelScrolls(t *testing.T) {
	h := openChooser(t)
	h.view()
	d := h.a.dialog
	x, y := d.boxY0+3, 10
	start := d.listCursor
	for i := 0; i < 8; i++ {
		h.wheel(x, y, 1)
	}
	h.view() // scrolling is applied when the dialog is drawn
	if d.listCursor <= start {
		t.Fatalf("wheel down did not move the list (cursor %d, was %d)", d.listCursor, start)
	}
	if !cursorOnScreen(t, h) {
		t.Fatalf("wheel scrolling left the highlighted row off screen:\n%s", h.view())
	}
	if len(thumbRows(h)) == 0 {
		t.Fatalf("the wheel scrolled away the scroll indicator:\n%s", h.view())
	}
	after := d.listCursor
	for i := 0; i < 4; i++ {
		h.wheel(x, y, -1)
	}
	h.view()
	if d.listCursor >= after {
		t.Fatalf("wheel up did not move back (cursor %d, was %d)", d.listCursor, after)
	}
	if !cursorOnScreen(t, h) {
		t.Fatalf("after wheel up the highlighted row is off screen:\n%s", h.view())
	}
	if len(thumbRows(h)) == 0 {
		t.Fatalf("the indicator vanished after wheel up:\n%s", h.view())
	}
}

func TestFormatChooserIndicatorMovesWithTheView(t *testing.T) {
	h := openChooser(t)
	d := h.a.dialog
	h.view()
	top := thumbRows(h)
	if len(top) == 0 {
		t.Fatal("no indicator")
	}
	for i := 0; i < len(d.list)/2; i++ {
		h.press("down")
	}
	mid := thumbRows(h)
	h.view()
	if len(mid) == 0 {
		t.Fatal("the indicator disappeared while scrolling")
	}
	if top[0] == mid[0] {
		t.Fatalf("the indicator did not move with the view (body row %d before and after, scroll %d)", top[0], d.scroll)
	}
	// It stays inside the body: never on the title row or the borders.
	for _, r := range mid {
		if r < 0 || r >= d.boxY1-d.boxY0 {
			t.Fatalf("indicator body row %d outside the body", r)
		}
	}
}

func TestFormatChooserTapAfterScrollingPicksTheVisibleRow(t *testing.T) {
	h, _, clock := p4App(t, nil)
	h.send(tea.WindowSizeMsg{Width: 100, Height: 20})
	h.settle()
	h.press("n", "f", "ctrl+f")
	if !h.dialogKind(dlgFormat) {
		t.Fatalf("no chooser:\n%s", h.view())
	}
	*clock = clock.Add(2 * time.Second)
	for i := 0; i < 20; i++ {
		h.press("down")
	}
	h.view()
	d := h.a.dialog
	var row *dialogHit
	for i := range d.hits {
		hh := &d.hits[i]
		if hh.kind == hitListItem && hh.index == d.listCursor {
			row = hh
		}
	}
	if row == nil {
		t.Fatalf("the highlighted row has no click target after scrolling:\n%s", h.view())
	}
	want := d.list[d.listCursor].label
	h.tap(row.x0+2, row.y)
	if !h.dialogKind(dlgNewFile) {
		t.Fatalf("tapping a scrolled row did not choose it:\n%s", h.view())
	}
	if v := h.view(); !strings.Contains(v, want+" ") && !strings.Contains(v, strings.TrimPrefix(want, ".")) {
		t.Fatalf("chosen format %q not shown in the file dialog:\n%s", want, v)
	}
}

// Plain text dialogs keep the "↑↓ scroll" row (they do not have a cursor),
// and still get the indicator.
func TestTextDialogScrollsWithKeys(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.send(tea.WindowSizeMsg{Width: 80, Height: 18})
	h.settle()
	d := &dialog{kind: dlgReport, title: "Report"}
	for i := 0; i < 40; i++ {
		d.body = append(d.body, text(fmt.Sprintf("line %02d", i)))
	}
	h.a.dialog = d
	h.settle()
	v := h.view()
	if !strings.Contains(v, "↑↓ scroll") {
		t.Fatalf("no scroll hint on a long text dialog:\n%s", v)
	}
	if !strings.Contains(v, "line 00") {
		t.Fatalf("body not drawn:\n%s", v)
	}
	if len(thumbRows(h)) == 0 {
		t.Fatalf("no scroll indicator:\n%s", v)
	}
	for i := 0; i < 5; i++ {
		h.press("down")
	}
	if d.scroll != 5 {
		t.Fatalf("scroll = %d, want 5", d.scroll)
	}
	v = h.view()
	if strings.Contains(v, "line 00") {
		t.Fatalf("the body did not scroll:\n%s", v)
	}
	if !strings.Contains(v, "line 05") {
		t.Fatalf("the next line did not appear:\n%s", v)
	}
	if len(thumbRows(h)) == 0 {
		t.Fatalf("the indicator vanished while scrolling:\n%s", v)
	}
	// scrolling past the end clamps, and does not lose the indicator
	for i := 0; i < 100; i++ {
		h.press("down")
	}
	v = h.view()
	if len(thumbRows(h)) == 0 {
		t.Fatalf("indicator lost at the end:\n%s", v)
	}
	if !strings.Contains(v, "line 39") {
		t.Fatalf("the last line is not shown at the end:\n%s", v)
	}
	for i := 0; i < 200; i++ {
		h.press("up")
	}
	v = h.view()
	if d.scroll != 0 || !strings.Contains(v, "line 00") || len(thumbRows(h)) == 0 {
		t.Fatalf("up returned to scroll %d:\n%s", d.scroll, v)
	}
}

// A short dialog that fits keeps neither the hint row nor the indicator.
func TestShortDialogHasNoIndicator(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	h.settle()
	h.press("n")
	if !h.dialogKind(dlgNewMenu) {
		t.Fatalf("no New menu:\n%s", h.view())
	}
	if h.a.dialog.scrollable {
		t.Fatal("the two-entry menu must not scroll")
	}
	if rows := thumbRows(h); len(rows) != 0 {
		t.Fatalf("a fitting dialog drew %d indicator cells:\n%s", len(rows), h.view())
	}
	if strings.Contains(h.view(), "↑↓ scroll") {
		t.Fatalf("a fitting dialog shows the scroll hint:\n%s", h.view())
	}
}

// Long non-format lists (bookmarks) follow the cursor too.
func TestListDialogFollowsCursor(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	// many bookmarks so the list is taller than the screen
	var names []string
	for i := 0; i < 30; i++ {
		dir := filepath.Join(env.root, fmt.Sprintf("bm%02d", i))
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		names = append(names, dir)
	}
	store, err := h.a.bookmarkStore()
	if err != nil {
		t.Fatalf("bookmark store: %v", err)
	}
	for i, n := range names {
		if _, err := store.Add(n, fmt.Sprintf("bm%02d", i)); err != nil {
			t.Fatalf("add bookmark %q: %v", n, err)
		}
	}
	h.send(tea.WindowSizeMsg{Width: 90, Height: 16})
	h.settle()
	h.exec(h.a.openBookmarks(0, ""))
	h.waitDialog(dlgBookmarks)
	h.view() // the dialog records its scroll state when it is drawn
	d := h.a.dialog
	if !d.scrollable {
		t.Fatalf("bookmark dialog not scrollable (%d entries):\n%s", len(d.list), h.view())
	}
	h.view()
	if len(thumbRows(h)) == 0 {
		t.Fatalf("no indicator on the bookmark list:\n%s", h.view())
	}
	for i := 0; i < len(d.list)-1; i++ {
		h.press("down")
		if !cursorOnScreen(t, h) {
			t.Fatalf("bookmark %d (%q) off screen (scroll %d):\n%s",
				d.listCursor, d.list[d.listCursor].label, d.scroll, h.view())
		}
	}
}
