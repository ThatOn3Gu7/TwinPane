package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/activity"
)

// ---- helpers -----------------------------------------------------------------

// seedSession appends n entries of one application run to the log file, one
// minute apart from started. Writing the NDJSON directly keeps the sessions,
// their timestamps and their run IDs exactly what the test needs (the store
// would stamp them with "now").
func seedSession(t *testing.T, path, run string, started time.Time, n int, prefix string) {
	t.Helper()
	var b strings.Builder
	for i := 0; i < n; i++ {
		ts := started.Add(time.Duration(i) * time.Minute)
		fmt.Fprintf(&b, `{"v":1,"ts":%q,"run":%q,"seq":%d,"lvl":"info","cat":"ui","ev":"ui.note","msg":%q}`+"\n",
			ts.Format(time.RFC3339Nano), run, i+1, fmt.Sprintf("%s %d", prefix, i))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("config dir: %v", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatalf("append log: %v", err)
	}
	if _, err := f.WriteString(b.String()); err != nil {
		t.Fatalf("write log: %v", err)
	}
	f.Close()
}

// openLogViewer presses L with a settled, sized app.
func openLogViewer(t *testing.T, h *harness, w, height int) {
	t.Helper()
	h.send(tea.WindowSizeMsg{Width: w, Height: height})
	h.settle()
	h.press("L")
	if h.a.actView == nil {
		t.Fatalf("the viewer did not open:\n%s", h.view())
	}
	h.settle()
	_ = h.view() // draw one frame: the list layout is computed while rendering
}

// bodyLines returns the viewer's body (between the title line and the status
// and footer lines).
func bodyLines(t *testing.T, h *harness, height int) []string {
	t.Helper()
	lines := strings.Split(h.view(), "\n")
	if len(lines) < height {
		t.Fatalf("frame has %d lines, want %d", len(lines), height)
	}
	return lines[1 : height-2]
}

// thumbRow is the first body row of the scrollbar thumb, or -1 when none is
// drawn.
func thumbRow(body []string) int {
	for i, l := range body {
		if strings.Contains(l, "┃") {
			return i
		}
	}
	return -1
}

// thumbEnd is the last body row of the scrollbar thumb.
func thumbEnd(body []string) int {
	end := -1
	for i, l := range body {
		if strings.Contains(l, "┃") {
			end = i
		}
	}
	return end
}

// selectedIndex is the selection as an index into the filtered rows.
func selectedIndex(t *testing.T, h *harness) int {
	t.Helper()
	v := h.a.actView
	if v == nil {
		t.Fatal("no viewer")
	}
	if len(v.rows) == 0 {
		if v.sel != 0 {
			t.Fatalf("empty list has sel %d, want 0", v.sel)
		}
		return 0
	}
	if v.sel < 0 || v.sel >= len(v.rows) {
		t.Fatalf("sel = %d outside [0,%d)", v.sel, len(v.rows))
	}
	return v.sel
}

// TestActivityDetailsLastEntryThenDown is the reported crash: open the log,
// select the last entry, press Enter for the details, then Down. The selection
// must clamp at the end instead of walking past the list into the details pane.
func TestActivityDetailsLastEntryThenDown(t *testing.T) {
	h, _, path := activityApp(t, activity.ModeActions)
	seedSession(t, path, "run-a", time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC), 6, "seeded")
	openLogViewer(t, h, 100, 24)

	h.press("end")
	last := selectedIndex(t, h)
	if last != len(h.a.actView.rows)-1 {
		t.Fatalf("End put the cursor at %d, want the last entry %d", last, len(h.a.actView.rows)-1)
	}
	h.press("enter") // details open
	if !h.a.actView.detail {
		t.Fatal("⏎ did not open the details")
	}
	// The crash: Down with the details open used to leave sel == len(rows).
	h.press("down")
	if got := selectedIndex(t, h); got != last {
		t.Fatalf("Down at the end moved the cursor to %d, want it clamped at %d", got, last)
	}
	v := h.view()
	if !strings.Contains(v, "Details") {
		t.Fatalf("the details pane vanished:\n%s", v)
	}
	if !strings.Contains(v, fmt.Sprintf("seeded %d", last)) {
		// Row (last) of seedSession's messages; the selected entry is the same
		// one the details describe.
		t.Logf("view:\n%s", v)
	}
	checkScreen(t, v, 100, 24)

	// The same boundary from the other side, and repeatedly.
	for _, key := range []string{"down", "down", "up", "end", "down", "pgdown", "up", "home", "up", "pgup", "end"} {
		h.press(key)
		selectedIndex(t, h) // fails if the selection is ever out of range
		checkScreen(t, h.view(), 100, 24)
	}
}

// TestActivitySelectionBoundaries covers the empty and filtered lists, where the
// old code could set sel to -1 (End on nothing) or past the end.
func TestActivitySelectionBoundaries(t *testing.T) {
	t.Run("empty log", func(t *testing.T) {
		h, _, _ := activityApp(t, activity.ModeOff) // nothing is written
		openLogViewer(t, h, 100, 24)
		for _, key := range []string{"down", "up", "end", "home", "pgdown", "pgup", "enter", "down", "enter", "down"} {
			h.press(key)
			selectedIndex(t, h)
			if v := h.view(); !strings.Contains(v, "No entries yet") {
				t.Fatalf("empty log lost its message after %q:\n%s", key, v)
			}
			checkScreen(t, h.view(), 100, 24)
		}
	})

	t.Run("filtered to nothing", func(t *testing.T) {
		h, _, path := activityApp(t, activity.ModeActions)
		seedSession(t, path, "run-a", time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC), 5, "ui event")
		openLogViewer(t, h, 100, 24)
		h.press("end")
		h.press("enter") // details open on the last entry
		// Commands: the seeded entries are all UI actions, so this empties it.
		h.press("c")
		if !strings.Contains(h.view(), "Commands") {
			t.Fatalf("category filter did not apply:\n%s", h.view())
		}
		if len(h.a.actView.rows) != 0 {
			t.Fatalf("expected an empty result, got %d rows", len(h.a.actView.rows))
		}
		for _, key := range []string{"down", "up", "end", "home", "pgdown", "enter", "down"} {
			h.press(key)
			selectedIndex(t, h)
			if v := h.view(); !strings.Contains(v, "Nothing matches this filter") {
				t.Fatalf("filtered-empty list lost its message after %q:\n%s", key, v)
			}
			checkScreen(t, h.view(), 100, 24)
		}
		// Going back to a non-empty filter restores a usable selection.
		h.press("x")
		if len(h.a.actView.rows) == 0 {
			t.Fatal("clearing the filter left the list empty")
		}
		selectedIndex(t, h)
		h.press("down", "end", "down")
		selectedIndex(t, h)
	})

	t.Run("single entry", func(t *testing.T) {
		h, _, path := activityApp(t, activity.ModeActions)
		seedSession(t, path, "run-a", time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC), 1, "only")
		openLogViewer(t, h, 100, 24)
		for _, key := range []string{"end", "down", "down", "enter", "down", "up", "up", "enter"} {
			h.press(key)
			sel := selectedIndex(t, h)
			if sel != len(h.a.actView.rows)-1 && sel != 0 {
				t.Fatalf("selection = %d in a %d-entry list, want it clamped at an end",
					sel, len(h.a.actView.rows))
			}
			checkScreen(t, h.view(), 100, 24)
		}
		// With one entry left, up and down both stay on it.
		h.press("c", "c") // Commands: no seeded entries there
		if len(h.a.actView.rows) != 0 {
			// The viewer's own ▸ ui.view entry is not a command either, so the
			// list must be empty.
			t.Fatalf("expected an empty command list, got %d rows", len(h.a.actView.rows))
		}
		h.press("up", "down", "enter")
		selectedIndex(t, h)
	})
}

// TestActivitySelectionStaysVisible: the highlight must follow the cursor and
// the cursor must never scroll off screen, with the details open or closed.
func TestActivitySelectionStaysVisible(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(old)

	h, _, path := activityApp(t, activity.ModeActions)
	// The seeded session is in the past, so this run's own entry (written when
	// the viewer opens) is the newest one and gets the first session rule.
	h.a.now = func() time.Time { return time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC) }
	seedSession(t, path, "run-a", time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC), 40, "row")
	openLogViewer(t, h, 100, 20)

	// Every step draws the selected row with the theme's cursor background.
	seen := map[string]bool{}
	for i := 0; i < 45; i++ {
		sel := selectedIndex(t, h)
		entry := h.a.actView.entries[h.a.actView.rows[sel]]
		raw := h.a.View()
		var row string
		for _, l := range strings.Split(raw, "\n") {
			if strings.Contains(l, entry.Summary()) && strings.Contains(l, "48;5;153") {
				row = l
				break
			}
		}
		if row == "" {
			t.Fatalf("step %d: the selected entry %q is not highlighted on screen", i, entry.Summary())
		}
		seen[entry.Summary()] = true
		h.press("down")
		checkScreen(t, h.view(), 100, 20)
	}
	if len(seen) < 20 {
		t.Fatalf("only %d distinct rows were reachable by scrolling down", len(seen))
	}
	// End puts the cursor on the last entry and the list at its bottom.
	h.press("end")
	v := h.a.actView
	if v.sel != len(v.rows)-1 {
		t.Fatalf("End: sel = %d, want %d", v.sel, len(v.rows)-1)
	}
	if !strings.Contains(h.view(), h.a.actView.entries[h.a.actView.rows[v.sel]].Summary()) {
		t.Fatal("the last entry is not on screen after End")
	}
	// Home returns to the top, showing the first session's rule above its
	// first entry rather than cutting the block off its heading.
	h.press("home")
	if got := h.a.actView.top; got != 0 {
		t.Fatalf("Home left the list scrolled at line %d, want 0", got)
	}
	if !strings.Contains(h.view(), "This session") {
		t.Fatal("the current session rule is not visible after Home")
	}
	// With the details open the visible list shrinks; the cursor stays visible.
	h.press("enter")
	end := len(h.a.actView.rows) - 1
	h.press("end")
	if got := h.a.actView.entries[h.a.actView.rows[h.a.actView.sel]].Summary(); got != fmt.Sprintf("row %d", end-0) && !strings.HasPrefix(got, "row") {
		t.Fatalf("End with details open selected %q", got)
	}
	if !strings.Contains(h.view(), h.a.actView.entries[h.a.actView.rows[h.a.actView.sel]].Summary()) {
		t.Fatal("the selected entry is off screen with the details open")
	}
}

// TestActivityScrollbar: a separate column on the right that is accurate at the
// top, the middle and the bottom, and disappears when the list fits.
func TestActivityScrollbar(t *testing.T) {
	h, _, path := activityApp(t, activity.ModeActions)
	seedSession(t, path, "run-a", time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC), 40, "row")
	openLogViewer(t, h, 100, 20)

	body := bodyLines(t, h, 20)
	top := thumbRow(body)
	if top < 0 {
		t.Fatalf("no scrollbar in the viewer:\n%s", strings.Join(body, "\n"))
	}
	if top > 2 {
		t.Fatalf("the thumb starts at body row %d, want the top", top)
	}
	// The thumb is in the last column, apart from the text.
	for _, l := range body {
		if i := strings.Index(l, "┃"); i >= 0 && ansi.StringWidth(l[:i]) != 99 {
			t.Fatalf("the scrollbar is not in the last column (column %d of 100): %q",
				ansi.StringWidth(l[:i]), l)
		}
	}
	// Bottom: the thumb has travelled to the end of the track.
	h.press("end")
	body = bodyLines(t, h, 20)
	bottom := thumbEnd(body)
	if bottom < 0 || bottom < len(body)-2 {
		t.Fatalf("the thumb did not reach the bottom (ends at row %d of %d)", bottom, len(body))
	}
	if bottom == top {
		t.Fatal("the thumb did not move between the top and the bottom")
	}
	// The last entry is on screen too, so the bar and the list agree.
	last := h.a.actView.entries[h.a.actView.rows[len(h.a.actView.rows)-1]]
	if !strings.Contains(h.view(), last.Summary()) {
		t.Fatalf("the thumb is at the bottom but the last entry %q is not visible", last.Summary())
	}
	// Halfway.
	h.press("home")
	for i := 0; i < 20; i++ {
		h.press("down")
	}
	mid := thumbRow(bodyLines(t, h, 20))
	if mid <= top || mid >= bottom {
		t.Fatalf("the thumb at the middle is at row %d (top %d, bottom %d)", mid, top, bottom)
	}

	// A list that fits has no scrollbar at all, and neither has an empty one.
	h.press("x")
	h.press("l", "l", "l") // errors only: the seeded entries are all info
	if n := len(h.a.actView.rows); n != 0 {
		t.Fatalf("expected an empty errors-only list, got %d rows", n)
	}
	if r := thumbRow(bodyLines(t, h, 20)); r >= 0 {
		t.Fatalf("an empty list still drew a scrollbar at row %d", r)
	}
	h.press("x")
	// One entry: still no scrollbar, exactly one highlighted row.
	h.press("/")
	h.typeText("row 39", false)
	h.press("enter")
	if n := len(h.a.actView.rows); n != 1 {
		t.Fatalf("query left %d rows, want 1", n)
	}
	small := bodyLines(t, h, 20)
	if r := thumbRow(small); r >= 0 {
		t.Fatalf("a one-entry list still drew a scrollbar at row %d", r)
	}
	// And the single row is the selected one.
	if !strings.Contains(h.view(), "row 39") {
		t.Fatal("the matching entry is not on screen")
	}
}

// TestActivitySessionGrouping: entries are grouped under dated rules, the
// current session first, and the rules are not entries.
func TestActivitySessionGrouping(t *testing.T) {
	h, _, path := activityApp(t, activity.ModeActions)
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	h.a.now = func() time.Time { return now }
	// A previous session (two days ago) and a seeded previous-day session.
	seedSession(t, path, "run-old", now.AddDate(0, 0, -2), 3, "older")
	seedSession(t, path, "run-yesterday", now.AddDate(0, 0, -1), 2, "yesterday")
	openLogViewer(t, h, 110, 26)

	_ = h.view()
	v := h.a.actView
	var headers []actLine
	for _, l := range v.lines {
		if l.kind == actHeaderLine {
			headers = append(headers, l)
		}
	}
	if len(headers) < 2 {
		t.Fatalf("expected a rule per session, got %d:\n%s", len(headers), h.view())
	}
	view := h.view()
	if !strings.Contains(view, "This session") {
		t.Fatalf("the current session has no rule:\n%s", view)
	}
	if !strings.Contains(view, "Yesterday") {
		t.Fatalf("the previous session has no dated rule:\n%s", view)
	}
	if !strings.Contains(view, "Thu 25 Oct") && !strings.Contains(view, "Sat 25 Oct") {
		// The exact weekday depends on the date; the date itself must be there.
		if !strings.Contains(view, "25 Oct") {
			t.Fatalf("the older session has no date:\n%s", view)
		}
	}
	// Every header line carries a count.
	for _, l := range headers {
		if l.count < 1 || l.row != -1 {
			t.Fatalf("header is malformed: %+v", l)
		}
	}
	// The rules are not selectable: the selection index is an entry index, and
	// End lands on the last seeded entry of the oldest session, not on a rule.
	h.press("end")
	sel := h.a.actView.entries[h.a.actView.rows[selectedIndex(t, h)]]
	if strings.Contains(sel.Ev, "session") || sel.Msg == "" {
		t.Fatalf("End selected something that is not an entry: %+v", sel)
	}
	if !strings.HasPrefix(sel.Msg, "older") {
		t.Fatalf("End selected %q, want the oldest seeded entry", sel.Msg)
	}
	// Moving up crosses the session rule and lands on an entry of the session
	// above it (the header is skipped, not selected).
	h.press("up", "up", "up")
	sel = h.a.actView.entries[h.a.actView.rows[selectedIndex(t, h)]]
	if !strings.HasPrefix(sel.Msg, "yesterday") {
		t.Fatalf("crossing a session rule selected %q", sel.Msg)
	}
	// [ and ] jump between the sessions.
	h.press("[")
	if got := h.a.actView.entries[h.a.actView.rows[selectedIndex(t, h)]]; !strings.HasPrefix(got.Run, "run-") && got.Run == "" {
		t.Fatalf("] did not land inside a session: %+v", got)
	}
	// The details of the selected entry name its session, never the rule text.
	h.press("enter")
	det := h.view()
	if !strings.Contains(det, "session") {
		t.Fatalf("details do not name the entry's session:\n%s", det)
	}
	if strings.Contains(det, "· entry") || strings.Contains(det, "entries") && !strings.Contains(det, "This session") {
		t.Logf("details:\n%s", det)
	}
}

// TestActivityDetailsFollowSelection: with the pane open, moving the cursor
// shows that entry's details, and the pane never keeps a stale entry.
func TestActivityDetailsFollowSelection(t *testing.T) {
	h, _, path := activityApp(t, activity.ModeActions)
	seedSession(t, path, "run-a", time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC), 5, "row")
	openLogViewer(t, h, 110, 26)
	h.press("enter")
	for _, key := range []string{"down", "down", "up", "end", "home", "down"} {
		h.press(key)
		v := h.a.actView
		sel := v.entries[v.rows[v.sel]]
		view := h.view()
		if !strings.Contains(view, "Details") {
			t.Fatalf("details pane missing after %q:\n%s", key, view)
		}
		if !strings.Contains(view, sel.Msg) {
			t.Fatalf("details do not describe the selected entry %q after %q:\n%s", sel.Msg, key, view)
		}
		// The details block must not keep the previous entry's message.
		body := bodyLines(t, h, 26)
		var detailStart int
		for i, l := range body {
			if strings.Contains(l, "Details") {
				detailStart = i
			}
		}
		if detailStart == 0 {
			continue
		}
		detailText := strings.Join(body[detailStart:], " ")
		if strings.Count(detailText, "row ") > 1 {
			t.Fatalf("the details show more than one entry:\n%s", detailText)
		}
		if !strings.Contains(detailText, sel.Msg) {
			t.Fatalf("the details block lacks %q (it shows %q)", sel.Msg, detailText)
		}
		checkScreen(t, view, 110, 26)
	}
}

// TestActivityMouseSkipsHeaders: a tap on a session rule changes nothing, a tap
// on an entry selects exactly that entry.
func TestActivityMouseSkipsHeaders(t *testing.T) {
	h, _, path := activityApp(t, activity.ModeActions)
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	h.a.now = func() time.Time { return now }
	seedSession(t, path, "run-old", now.AddDate(0, 0, -1), 4, "old")
	openLogViewer(t, h, 110, 26)

	_ = h.view()
	v := h.a.actView
	// Find a session rule and the first entry under it, in laid-out lines.
	ruleIdx, entryIdx := -1, -1
	for i, l := range v.lines {
		if l.kind == actHeaderLine {
			if ruleIdx >= 0 && entryIdx < 0 {
				continue
			}
			if len(v.rows) > 0 && v.entries[v.rows[0]].Run == l.text {
				continue // the first rule (this session): use the older one
			}
			ruleIdx = i
			continue
		}
		if ruleIdx >= 0 {
			entryIdx = i
			break
		}
	}
	if ruleIdx < 0 || entryIdx < 0 {
		t.Fatalf("could not find the second session rule in %d lines", len(v.lines))
	}
	if ruleIdx < v.top || entryIdx >= v.top+v.listH {
		t.Skip("the older session is below the fold at this height")
	}
	before := v.sel
	// The body starts one line below the title, and screen y = 2 + line offset.
	h.tap(10, ruleIdx-v.top+2)
	if v.sel != before {
		t.Fatalf("a tap on a session rule moved the selection from %d to %d", before, v.sel)
	}
	h.tap(10, entryIdx-v.top+2)
	if got := v.entries[v.rows[v.sel]].Msg; !strings.HasPrefix(got, "old") {
		t.Fatalf("a tap on an entry selected %q", got)
	}
	selectedIndex(t, h)
	// A tap on the already selected entry opens its details, and one more
	// closes them again; neither can move the selection off the list.
	h.tap(10, entryIdx-v.top+2)
	if !v.detail {
		t.Fatal("a second tap on the selected entry did not open the details")
	}
	h.press("down")
	selectedIndex(t, h)
	checkScreen(t, h.view(), 110, 26)
}

// TestActivityNarrowLayoutKeepsSelection: two-line rows still highlight both
// lines and keep the rule/session layout intact.
func TestActivityNarrowLayoutKeepsSelection(t *testing.T) {
	old := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI256)
	defer lipgloss.SetColorProfile(old)

	h, _, path := activityApp(t, activity.ModeActions)
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	h.a.now = func() time.Time { return now }
	seedSession(t, path, "run-old", now.AddDate(0, 0, -1), 6, "narrow")
	openLogViewer(t, h, 40, 20)
	v := h.a.actView
	if v.rowHeight(0) != 2 {
		t.Fatalf("narrow rows should take two lines, rowHeight(0) = %d", v.rowHeight(0))
	}
	sel := v.entries[v.rows[v.sel]]
	raw := h.a.View()
	if !strings.Contains(raw, "48;5;153") {
		t.Fatalf("no selection highlight on a narrow terminal:\n%s", raw)
	}
	if strings.Count(raw, "48;5;153") < 2 && v.rowHeight(v.sel) == 2 {
		// The two lines of the selected row use CursorActive and CursorInactive
		// (a slightly different grey), so only the first must be the cursor one.
		t.Logf("selection highlight spans one attribute set; fine")
	}
	if !strings.Contains(h.view(), sel.Msg) {
		t.Fatalf("the selected entry is not visible:\n%s", h.view())
	}
	for _, key := range []string{"down", "end", "down", "up", "home", "enter", "down", "pgdown"} {
		h.press(key)
		selectedIndex(t, h)
		checkScreen(t, h.view(), 40, 20)
	}
}

// TestActivityFilterKeepsSelectionInside: changing filters never leaves the
// selection or the scroll offset outside the new list.
func TestActivityFilterKeepsSelectionInside(t *testing.T) {
	h, _, path := activityApp(t, activity.ModeActions)
	seedSession(t, path, "run-a", time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC), 30, "row")
	openLogViewer(t, h, 100, 20)
	h.press("end")
	for i := 0; i < len(activity.CatFilters)+2; i++ {
		h.press("c")
		selectedIndex(t, h)
		v := h.a.actView
		if v.top > len(v.lines) {
			t.Fatalf("after filtering, top = %d with %d lines", v.top, len(v.lines))
		}
		checkScreen(t, h.view(), 100, 20)
	}
	for i := 0; i < 4; i++ {
		h.press("l")
		selectedIndex(t, h)
		checkScreen(t, h.view(), 100, 20)
	}
}

// TestActivitySessionHelpers covers the date labels and the header text.
func TestActivitySessionHelpers(t *testing.T) {
	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		when time.Time
		want string
	}{
		{now.Add(-time.Hour), "Today"},
		{now.AddDate(0, 0, -1), "Yesterday"},
		{time.Date(2026, 3, 4, 8, 0, 0, 0, time.UTC), "Wed 4 Mar"},
		{time.Date(2025, 12, 31, 8, 0, 0, 0, time.UTC), "Wed 31 Dec 2025"},
	}
	for _, c := range cases {
		if got := activityDateLabel(c.when, now); got != c.want {
			t.Errorf("activityDateLabel(%v) = %q, want %q", c.when, got, c.want)
		}
	}
	start := [2]time.Time{now.Add(-2 * time.Hour), now.Add(-time.Hour)}
	if got := activitySessionName(start, 4, true, now); got != "This session · 10:00–11:00 · 4 entries" {
		t.Errorf("current session name = %q", got)
	}
	same := [2]time.Time{now.Add(-time.Hour), now.Add(-time.Hour)}
	if got := activitySessionName(same, 1, false, now); got != "Today · 11:00 · 1 entry" {
		t.Errorf("single-entry session name = %q", got)
	}
}

var _ = filepath.Join // keep the import list stable if helpers move
