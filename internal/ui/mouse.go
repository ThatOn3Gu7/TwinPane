package ui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ThatOn3Gu7/TwinPane/internal/panel"
	"github.com/ThatOn3Gu7/TwinPane/internal/scrollbar"
)

// Mouse and touch input (phase 4).
//
// TwinPane uses Bubble Tea's mouse reporting (button-event tracking with SGR
// coordinates). In Termux, with mouse reporting on, a tap arrives as a left
// press immediately followed by a release at the tapped cell, a finger swipe
// or fling arrives as wheel events (one per text row travelled, reported at
// the position where the swipe started), and a long press still starts
// Termux's own text selection (TwinPane never sees it). Desktop terminals
// send real press/drag/release and wheel events.
//
// Rules, chosen to be safe with touch:
//   - a tap moves the cursor and focuses the panel; it never selects/marks
//     or runs anything by itself;
//   - a second tap on the same row within doubleTapWindow opens it
//     (directory: enter it; file: the default editor); the click state is
//     then reset so a third tap is not another double tap;
//   - buttons and hint-bar items act on release, only if the press was on
//     the same item, and not during dialogGuard after a dialog opened;
//   - nothing happens while the editor runs or during the quiet period
//     after it; background file operations do not lock panel navigation.

var (
	doubleTapWindow = 450 * time.Millisecond
	dialogGuard     = 400 * time.Millisecond
	wheelStep       = 1 // rows per wheel event (Termux sends one per row swiped)
)

// clickState remembers the last tap on a list row for double-tap detection.
type clickState struct {
	area  int // areaPanel0/1, areaSearch, areaBulk
	index int
	at    time.Time
	valid bool
}

const (
	areaPanel0 = iota
	areaPanel1
	areaSearch
	areaBulk
	areaViewer
	areaFolderSize
	areaAdvancedSearch
)

// pressState remembers a left-button press on a hint-bar item, so that the
// release can be matched against it.
type pressState struct {
	footer int // index into footerHits, -1 = none
	valid  bool
	title  bool // pressed on the TwinPane badge (opens the menu)
}

// dragState tracks dragging a scroll indicator with a mouse (touch screens
// in Termux cannot drag: a swipe becomes wheel events).
type dragState struct {
	area   int
	active bool
}

func (a *App) handleMouse(m tea.MouseMsg) tea.Cmd {
	if a.keyBlocked() || a.quitting {
		return nil
	}
	ev := tea.MouseEvent(m)
	if ev.Action == tea.MouseActionMotion {
		if a.drag.active && ev.Button == tea.MouseButtonLeft {
			a.dragTo(ev.Y)
		}
		return nil
	}
	if ev.Action == tea.MouseActionRelease {
		a.drag.active = false
	}

	// The hint bar (last screen row), unless a dialog box covers it.
	if ev.Y == a.height-1 && (a.dialog == nil || !a.dialogCovers(ev.Y)) && !a.propsCovers(ev.Y) && !ev.IsWheel() {
		return a.footerMouse(ev)
	}
	if ev.Action == tea.MouseActionPress && ev.Button == tea.MouseButtonLeft {
		a.press = pressState{}
	}

	switch {
	case a.dialog != nil:
		return a.dialogMouse(a.dialog, ev)
	case a.tasks.showing:
		return a.taskViewMouse(ev)
	case a.showHelp:
		return a.helpMouse(ev) // help.go: wheel scrolls, a tap closes
	case a.props != nil:
		return a.propsMouse(ev)
	case a.viewer != nil:
		return a.viewerMouse(ev)
	case a.advancedSearch != nil:
		return a.advancedSearchMouse(ev)
	case a.search != nil:
		return a.searchMouse(ev)
	case a.bulk != nil:
		return a.bulkMouse(ev)
	case a.folderSize != nil:
		return a.folderSizeMouse(ev)
	case a.comparison != nil:
		return a.comparisonMouse(ev)
	}
	// A tap on the TwinPane badge (title row) opens the action menu: it
	// is visible at any width, unlike the "M menu" chip.
	if ev.Y == 0 && ev.Button == tea.MouseButtonLeft && ev.X < lipgloss.Width(a.theme.TitleBadge.Render("TwinPane")) {
		switch ev.Action {
		case tea.MouseActionPress:
			a.press = pressState{title: true}
		case tea.MouseActionRelease:
			if a.press.title {
				a.press = pressState{}
				a.lastClick = clickState{}
				return a.openMenu()
			}
		}
		return nil
	}
	if ev.Action == tea.MouseActionRelease {
		a.press.title = false
	}
	return a.panelMouse(ev)
}

func wheelDelta(ev tea.MouseEvent) int {
	switch ev.Button {
	case tea.MouseButtonWheelUp:
		return -1
	case tea.MouseButtonWheelDown:
		return 1
	}
	return 0
}

// doubleTap records a tap on (area, index) and reports whether it completes
// a double tap.
func (a *App) doubleTap(area, index int) bool {
	now := a.now()
	c := a.lastClick
	if c.valid && c.area == area && c.index == index && now.Sub(c.at) <= doubleTapWindow {
		a.lastClick = clickState{}
		return true
	}
	a.lastClick = clickState{area: area, index: index, at: now, valid: true}
	return false
}

// ---- hint bar -----------------------------------------------------------------

func (a *App) footerMouse(ev tea.MouseEvent) tea.Cmd {
	if ev.Button != tea.MouseButtonLeft && ev.Action != tea.MouseActionRelease {
		return nil
	}
	idx := -1
	for i, hh := range a.footerHits {
		if ev.X >= hh.x0 && ev.X < hh.x1 {
			idx = i
		}
	}
	switch ev.Action {
	case tea.MouseActionPress:
		a.press = pressState{footer: idx, valid: idx >= 0}
		return nil
	case tea.MouseActionRelease:
		p := a.press
		a.press = pressState{}
		if !p.valid || p.footer != idx || idx < 0 {
			return nil
		}
		if a.dialog != nil && a.now().Sub(a.dialogAt) < dialogGuard {
			return nil
		}
		send := a.footerHits[idx].send
		a.lastClick = clickState{}
		_, cmd := a.update(keyFor(send))
		return cmd
	}
	return nil
}

// ---- dialogs ------------------------------------------------------------------

func (a *App) dialogCovers(y int) bool {
	d := a.dialog
	return d != nil && y >= d.boxY0 && y < d.boxY1
}

func (a *App) dialogMouse(d *dialog, ev tea.MouseEvent) tea.Cmd {
	if ev.IsWheel() {
		delta := wheelDelta(ev)
		if d.list != nil {
			key := "down"
			if delta < 0 {
				key = "up"
			}
			// A filtered list (the format chooser) moves like its arrow
			// keys; the view follows the cursor in both cases.
			if d.filter != nil {
				return a.filterKey(d, keyFor(key))
			}
			return a.listKey(d, keyFor(key))
		}
		d.scroll += delta * wheelStep
		return nil
	}
	if ev.Button != tea.MouseButtonLeft && ev.Action != tea.MouseActionRelease {
		return nil
	}
	hit := d.hitAt(ev.X, ev.Y)
	switch ev.Action {
	case tea.MouseActionPress:
		d.press = nil
		if hit == nil || a.now().Sub(a.dialogAt) < dialogGuard {
			return nil
		}
		hh := *hit
		d.press = &hh
		switch hit.kind {
		case hitButton:
			if d.input == nil {
				d.focus = hit.index // show which button will act
			}
		case hitListItem:
			if !d.list[hit.index].disabled {
				d.listCursor = hit.index
			}
		}
		return nil
	case tea.MouseActionRelease:
		p := d.press
		d.press = nil
		if p == nil || hit == nil || p.kind != hit.kind || p.index != hit.index {
			return nil // released elsewhere: nothing happens
		}
		switch hit.kind {
		case hitButton:
			if hit.index < len(d.buttons) {
				return d.buttons[hit.index].run(a)
			}
		case hitListItem:
			if hit.index < len(d.list) && !d.list[hit.index].disabled {
				d.listCursor = hit.index
				return d.onPick(a, hit.index)
			}
		case hitToggle:
			d.toggled = !d.toggled
		case hitInToggle:
			if hit.index < len(d.inToggles) {
				d.inToggles[hit.index].on = !d.inToggles[hit.index].on
				d.errLine = ""
			}
		case hitSelPrev, hitSelNext:
			if hit.index < len(d.selectors) {
				sel := d.selectors[hit.index]
				f := sel.next
				if hit.kind == hitSelPrev {
					f = sel.prev
				}
				if f != nil {
					f(a)
					d.errLine = ""
				}
			}
		}
	}
	return nil
}

// ---- panels -------------------------------------------------------------------

// panelAt returns the panel under screen position (x, y) and the position
// relative to that panel's top-left corner.
func (a *App) panelAt(x, y int) (int, int, int, bool) {
	ph := a.height - 3
	if y < 1 || y >= 1+ph || x < 0 || x >= a.width {
		return 0, 0, 0, false
	}
	lw := a.width / 2
	if x < lw {
		return 0, x, y - 1, true
	}
	return 1, x - lw, y - 1, true
}

func (a *App) panelMouse(ev tea.MouseEvent) tea.Cmd {
	i, px, py, ok := a.panelAt(ev.X, ev.Y)
	if !ok {
		return nil
	}
	p := a.panels[i]
	if ev.IsWheel() {
		// Scroll the panel under the pointer; focus stays where it is.
		p.ScrollBy(wheelDelta(ev) * wheelStep)
		return nil
	}
	if ev.Action != tea.MouseActionPress {
		return nil
	}
	hit := p.HitTest(px, py)
	switch ev.Button {
	case tea.MouseButtonLeft:
		if a.active != i {
			a.active = i
		}
		a.setStatus("", false)
		switch hit.Kind {
		case panel.HitRow:
			p.SetCursor(hit.Index)
			if a.doubleTap(areaPanel0+i, hit.Index) {
				return a.openEntry(p)
			}
		case panel.HitScrollbar:
			a.lastClick = clickState{}
			p.ScrollToTrack(hit.Index)
			a.drag = dragState{area: areaPanel0 + i, active: true}
		default:
			a.lastClick = clickState{}
		}
	case tea.MouseButtonRight:
		// Desktop mice: right-click toggles the mark of the row (Termux
		// has no right button for touch).
		if hit.Kind == panel.HitRow {
			a.active = i
			p.SetCursor(hit.Index)
			p.ToggleSelect()
			p.SetCursor(hit.Index)
		}
	}
	return nil
}

// openEntry is the double-tap action: enter a directory (as Enter does),
// browse a supported archive, view an archive entry, or open the file under the
// cursor alone in the default editor (as e does for one file).
func (a *App) openEntry(p *panel.Panel) tea.Cmd {
	e, ok := p.Current()
	if !ok {
		return nil
	}
	if e.IsDir {
		return p.Open()
	}
	return a.openFile(p, e, true)
}

// dragTo continues dragging a scroll indicator to screen row y.
func (a *App) dragTo(y int) {
	switch a.drag.area {
	case areaPanel0, areaPanel1:
		p := a.panels[a.drag.area-areaPanel0]
		p.ScrollToTrack(p.TrackCell(y - 1)) // panels start below the title row
	case areaSearch:
		if a.search != nil {
			a.searchScrollTrack(y)
		}
	case areaBulk:
		if a.bulk != nil {
			a.bulkScrollTrack(y)
		}
	case areaViewer:
		if a.viewer != nil {
			a.viewerScrollTrack(y)
		}
	}
}

// ---- search box ---------------------------------------------------------------

// searchGeom returns the screen row of the first result and the number of
// result rows, as drawn by searchViewRender.
func (a *App) searchGeom() (top, rows int) {
	return 2 + len(a.searchHeader(max(10, a.width-4))), a.searchRows()
}

func (a *App) searchMouse(ev tea.MouseEvent) tea.Cmd {
	sv := a.search
	top, rows := a.searchGeom()
	if ev.IsWheel() {
		a.searchScroll(wheelDelta(ev) * wheelStep)
		return nil
	}
	if ev.Action != tea.MouseActionPress || ev.Button != tea.MouseButtonLeft {
		return nil
	}
	r := ev.Y - top
	if r < 0 || r >= rows {
		if ev.Y >= 1 && ev.Y < top {
			sv.list = false // tap on the header / query: type again
		}
		a.lastClick = clickState{}
		return nil
	}
	if ev.X == a.width-2 && len(sv.matches) > rows {
		a.lastClick = clickState{}
		a.searchScrollTrack(ev.Y)
		a.drag = dragState{area: areaSearch, active: true}
		return nil
	}
	i := sv.offset + r
	if i >= len(sv.matches) {
		a.lastClick = clickState{}
		return nil
	}
	sv.cursor = i
	sv.list = true
	if a.doubleTap(areaSearch, i) {
		return a.searchJump()
	}
	return nil
}

// searchScroll moves the result viewport, pulling the cursor along.
func (a *App) searchScroll(delta int) {
	sv := a.search
	rows := a.searchRows()
	sv.offset += delta
	if maxOff := len(sv.matches) - rows; sv.offset > maxOff {
		sv.offset = maxOff
	}
	if sv.offset < 0 {
		sv.offset = 0
	}
	if sv.cursor < sv.offset {
		sv.cursor = sv.offset
	}
	if sv.cursor >= sv.offset+rows {
		sv.cursor = sv.offset + rows - 1
	}
	sv.clamp(rows)
}

func (a *App) searchScrollTrack(y int) {
	top, rows := a.searchGeom()
	off := scrollbar.OffsetAt(y-top, len(a.search.matches), rows, rows)
	a.searchScroll(off - a.search.offset)
}

// ---- bulk rename --------------------------------------------------------------

// bulkTop is the screen row of the first bulk-rename list row: body top (1),
// box border, title, folder, summary, column header.
const bulkTop = 1 + 1 + 4

func (a *App) bulkMouse(ev tea.MouseEvent) tea.Cmd {
	b := a.bulk
	if b.applying {
		return nil
	}
	rows := a.bulkRows()
	if ev.IsWheel() {
		a.bulkScroll(wheelDelta(ev) * wheelStep)
		return nil
	}
	if ev.Action != tea.MouseActionPress || ev.Button != tea.MouseButtonLeft {
		return nil
	}
	r := ev.Y - bulkTop
	if r < 0 || r >= rows {
		a.lastClick = clickState{}
		return nil
	}
	if ev.X == a.width-2 && len(b.items) > rows {
		a.lastClick = clickState{}
		a.bulkScrollTrack(ev.Y)
		a.drag = dragState{area: areaBulk, active: true}
		return nil
	}
	i := b.offset + r
	if i >= len(b.items) {
		a.lastClick = clickState{}
		return nil
	}
	b.cursor = i
	if a.doubleTap(areaBulk, i) {
		a.bulkEditRow() // same as Enter: edit the new name
	}
	return nil
}

func (a *App) bulkScroll(delta int) {
	b := a.bulk
	rows := a.bulkRows()
	b.offset += delta
	if maxOff := len(b.items) - rows; b.offset > maxOff {
		b.offset = maxOff
	}
	if b.offset < 0 {
		b.offset = 0
	}
	if b.cursor < b.offset {
		b.cursor = b.offset
	}
	if b.cursor >= b.offset+rows {
		b.cursor = b.offset + rows - 1
	}
	b.clamp(rows)
}

func (a *App) bulkScrollTrack(y int) {
	rows := a.bulkRows()
	off := scrollbar.OffsetAt(y-bulkTop, len(a.bulk.items), rows, rows)
	a.bulkScroll(off - a.bulk.offset)
}
