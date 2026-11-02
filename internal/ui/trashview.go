package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
	"github.com/ThatOn3Gu7/TwinPane/internal/scrollbar"
)

// The Trash screen (`T`).
//
// Deleting moves an item to the trash instead of destroying it, and this screen
// is where it comes back from: the list shows what was deleted, when, and where
// it came from, and the actions are restoring it, deleting it for good, or
// emptying the whole trash. Nothing here touches a path outside the store's
// index, and a restore never overwrites: if the original name is taken the item
// goes to the other panel under a numbered name, and the notification says so.
//
// The list is newest first, because the reason to open this screen is almost
// always the deletion that just happened.

// trashView is the screen's state. The entries are a snapshot: every action
// reloads them, so the list can never disagree with the store.
type trashView struct {
	entries []ops.TrashEntry
	sel     int
	offset  int
	err     error
}

// trashRows is how many list rows fit in the drawn screen.
const trashSummaryLines = 2 // the summary line and a blank under it

func (v *trashView) clamp() {
	if len(v.entries) == 0 {
		v.sel, v.offset = 0, 0
		return
	}
	if v.sel < 0 {
		v.sel = 0
	}
	if v.sel >= len(v.entries) {
		v.sel = len(v.entries) - 1
	}
}

func (v *trashView) current() (ops.TrashEntry, bool) {
	if v.sel < 0 || v.sel >= len(v.entries) {
		return ops.TrashEntry{}, false
	}
	return v.entries[v.sel], true
}

// openTrash shows the trash (T, the menu entry, or the Settings row).
func (a *App) openTrash() tea.Cmd {
	if a.trashStore == nil {
		why := "the trash is not available in this session"
		if t, ok := a.trasher.(ops.NoTrash); ok && t.Reason != "" {
			why = "the trash is not available: " + t.Reason
		}
		a.noteStatus(lvlWarn, why)
		return nil
	}
	a.trashScr = &trashView{}
	a.reloadTrash()
	return nil
}

func (a *App) closeTrash() tea.Cmd {
	a.trashScr = nil
	return nil
}

// reloadTrash reads the store and keeps the cursor on a real row.
func (a *App) reloadTrash() {
	v := a.trashScr
	if v == nil || a.trashStore == nil {
		return
	}
	entries, err := a.trashStore.Entries()
	v.entries, v.err = entries, err
	v.clamp()
	a.refreshTrashStats()
}

// trashStatsText reports how much the trash holds, for the screen header and
// the Settings row.
func (a *App) trashStatsText() string {
	if a.trashStore == nil {
		return "unavailable"
	}
	n, bytes := a.trashItems, a.trashBytes
	if n == 0 {
		return "empty"
	}
	return fmt.Sprintf("%s · %s", plural(n, "item"), fsys.HumanSize(bytes))
}

// refreshTrashStats updates the cached count and size.
func (a *App) refreshTrashStats() {
	if a.trashStore == nil {
		a.trashItems, a.trashBytes = 0, 0
		return
	}
	a.trashItems, a.trashBytes = a.trashStore.Total()
}

// trashKeepLabel is the retention in words, used by the Settings row.
func (a *App) trashKeepLabel() string {
	return fmt.Sprintf("%s (1–365)", plural(a.trashKeepDays, "day"))
}

// ---- keys ----------------------------------------------------------------------

func (a *App) trashKey(msg tea.KeyMsg) tea.Cmd {
	v := a.trashScr
	if v == nil {
		return nil
	}
	page := a.trashPageRows()
	switch {
	case a.is("trash.close", msg):
		return a.closeTrash()
	case a.is("trash.up", msg):
		v.sel--
	case a.is("trash.down", msg):
		v.sel++
	case a.is("trash.pageUp", msg):
		v.sel -= page
	case a.is("trash.pageDown", msg):
		v.sel += page
	case a.is("trash.first", msg):
		v.sel = 0
	case a.is("trash.last", msg):
		v.sel = len(v.entries) - 1
	case a.is("trash.restore", msg):
		return a.trashRestore()
	case a.is("trash.delete", msg):
		return a.trashDeleteConfirm()
	case a.is("trash.empty", msg):
		return a.trashEmptyConfirm()
	case a.is("trash.refresh", msg):
		a.reloadTrash()
		a.noteStatus(lvlInfo, "Trash reloaded — "+a.trashStatsText())
		return nil
	case a.is("trash.help", msg):
		a.openHelp()
		return nil
	}
	v.clamp()
	a.trashScrollIntoView()
	return nil
}

// trashPageRows is how many entries one page scrolls: the list rows that fit.
func (a *App) trashPageRows() int {
	if n := a.trashListRows(); n > 0 {
		return n
	}
	return 1
}

// trashListRows is how many entries the screen shows at once.
func (a *App) trashListRows() int {
	body := a.height - 3
	rows := body - 2 - trashSummaryLines // border rows and the summary
	if a.trashTwoLine() {
		return max(1, rows/2)
	}
	return max(1, rows)
}

// trashTwoLine reports the narrow layout: two lines per entry.
func (a *App) trashTwoLine() bool { return a.width < 64 }

// trashScrollIntoView keeps the selected entry inside the window.
func (a *App) trashScrollIntoView() {
	v := a.trashScr
	if v == nil {
		return
	}
	n := a.trashListRows()
	if v.sel < v.offset {
		v.offset = v.sel
	}
	if v.sel >= v.offset+n {
		v.offset = v.sel - n + 1
	}
	if v.offset > len(v.entries)-n {
		v.offset = max(0, len(v.entries)-n)
	}
	if v.offset < 0 {
		v.offset = 0
	}
}

// ---- actions -------------------------------------------------------------------

// trashRestore puts the selected item back. The original path is used when it
// is free; otherwise (or when the folder it came from is gone) the item goes to
// the other panel under a numbered name, so nothing is ever overwritten and a
// restore cannot fail because of a name clash.
func (a *App) trashRestore() tea.Cmd {
	v := a.trashScr
	if v == nil || a.trashStore == nil {
		return nil
	}
	e, ok := v.current()
	if !ok {
		a.noteStatus(lvlInfo, "Nothing to restore: the trash is empty")
		return nil
	}
	dest, moved := a.trashRestoreTarget(e)
	if err := a.trashStore.Restore(e, dest); err != nil {
		a.reloadTrash()
		a.noteResult(lvlErr, "could not restore "+e.Name+": "+errText(err), "Restore failed",
			"Could not restore "+fsys.SanitizeName(e.Name), errText(err))
		return nil
	}
	a.reloadTrash()
	reload := a.refreshPanelsAfterRestore(filepath.Dir(dest), filepath.Base(dest))
	body := []string{"→ " + shortPath(dest)}
	if moved != "" {
		body = append(body, moved)
	}
	a.noteResult(lvlOK, "restored "+e.Name+" to "+dest, "Restored",
		"Restored "+fsys.SanitizeName(e.Name), body...)
	return reload
}

// trashRestoreTarget picks where an entry goes back to.
func (a *App) trashRestoreTarget(e ops.TrashEntry) (dest, note string) {
	if e.Origin != "" {
		parent := filepath.Dir(e.Origin)
		if fi, err := os.Stat(parent); err == nil && fi.IsDir() {
			if _, err := os.Lstat(e.Origin); errors.Is(err, os.ErrNotExist) {
				return e.Origin, ""
			}
		}
	}
	dir, why := a.trashFallbackDir()
	name := freeNameIn(dir, e.Name)
	dest = filepath.Join(dir, name)
	note = "The original folder is gone or the name is taken, so it went to " + shortPath(dir)
	if e.Origin != "" && name != e.Name {
		note = "The original path is taken, so it went to " + shortPath(dir) + " as " + name
	} else if why != "" {
		note = why
	}
	return dest, note
}

// trashFallbackDir is the folder a restored item lands in when it cannot go
// back where it came from: the other panel when it shows a local folder, then
// the active panel, then the home folder.
func (a *App) trashFallbackDir() (string, string) {
	for _, i := range []int{1 - a.active, a.active} {
		p := a.panels[i]
		if p == nil || p.InRemote() || p.InArchive() {
			continue
		}
		if p.Path() != "" && p.Err() == nil {
			return p.Path(), ""
		}
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return home, ""
	}
	return ".", "No local folder is open, so it went to the current directory"
}

// freeNameIn returns name, or "name (2)", "name (3)"… when dir already holds it.
func freeNameIn(dir, name string) string {
	if _, err := os.Lstat(filepath.Join(dir, name)); errors.Is(err, os.ErrNotExist) {
		return name
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if stem == "" { // dot-files: ".bashrc" has no stem
		stem, ext = name, ""
	}
	for i := 2; i < 10000; i++ {
		candidate := fmt.Sprintf("%s (%d)%s", stem, i, ext)
		if _, err := os.Lstat(filepath.Join(dir, candidate)); errors.Is(err, os.ErrNotExist) {
			return candidate
		}
	}
	return name
}

// refreshPanelsAfterRestore reloads the panel that shows the destination, so
// the restored item is visible without pressing Ctrl+L.
func (a *App) refreshPanelsAfterRestore(dir, focus string) tea.Cmd {
	var cmds []tea.Cmd
	for i := 0; i < 2; i++ {
		p := a.panels[i]
		if p == nil || p.InRemote() || p.InArchive() || p.Path() != dir {
			continue
		}
		cmds = append(cmds, p.RefreshFocus(focus))
	}
	return tea.Batch(cmds...)
}

// trashDeleteConfirm asks before an item is removed from the trash for good.
func (a *App) trashDeleteConfirm() tea.Cmd {
	v := a.trashScr
	if v == nil || a.trashStore == nil {
		return nil
	}
	e, ok := v.current()
	if !ok {
		a.noteStatus(lvlInfo, "Nothing to delete: the trash is empty")
		return nil
	}
	what := "item"
	if e.Dir {
		what = "folder"
	}
	body := []dline{
		text(fmt.Sprintf("%s %s (%s) will be deleted for good.", capitalize(what), fsys.SanitizeName(e.Name), fsys.HumanSize(e.Bytes))),
		path("from " + e.Origin),
		hint("It was deleted " + trashAgeText(e.DeletedAt, a.trashNow()) + ". This cannot be undone."),
	}
	a.dialog = &dialog{
		kind: dlgReport, title: "Delete from the trash?", danger: true, body: body, width: 70,
		buttons: []button{
			{label: "Delete for good (y)", key: "y", danger: true, run: func(a *App) tea.Cmd {
				a.dialog = nil
				return a.trashDelete(e)
			}},
			{label: "Cancel (Esc)", run: func(a *App) tea.Cmd { a.dialog = nil; return nil }},
		},
		onEsc: func(a *App) tea.Cmd { a.dialog = nil; return nil },
	}
	return nil
}

func (a *App) trashDelete(e ops.TrashEntry) tea.Cmd {
	if a.trashStore == nil {
		return nil
	}
	if err := a.trashStore.Remove(e); err != nil {
		a.reloadTrash()
		a.noteResult(lvlErr, "could not delete "+e.Name+" from the trash: "+errText(err),
			"Delete failed", "Could not delete "+fsys.SanitizeName(e.Name)+" from the trash", errText(err))
		return nil
	}
	a.reloadTrash()
	a.noteResult(lvlOK, "deleted "+e.Name+" from the trash", "Deleted from the trash",
		"Deleted "+fsys.SanitizeName(e.Name)+" for good")
	return nil
}

// trashEmptyConfirm asks before the whole trash is emptied.
func (a *App) trashEmptyConfirm() tea.Cmd {
	if a.trashScr == nil || a.trashStore == nil {
		return nil
	}
	a.refreshTrashStats()
	if a.trashItems == 0 {
		a.noteStatus(lvlInfo, "The trash is already empty")
		return nil
	}
	body := []dline{
		text(fmt.Sprintf("%s (%s) will be deleted for good.", plural(a.trashItems, "item"), fsys.HumanSize(a.trashBytes))),
		path("in " + a.trashStore.Name()),
		hint("Nothing can be restored afterwards. Keeping items for " + plural(a.trashKeepDays, "day") +
			" lets TwinPane do this on its own."),
	}
	a.dialog = &dialog{
		kind: dlgReport, title: "Empty the trash?", danger: true, body: body, width: 70,
		buttons: []button{
			{label: "Empty the trash (y)", key: "y", danger: true, run: func(a *App) tea.Cmd {
				a.dialog = nil
				return a.trashEmpty()
			}},
			{label: "Cancel (Esc)", run: func(a *App) tea.Cmd { a.dialog = nil; return nil }},
		},
		onEsc: func(a *App) tea.Cmd { a.dialog = nil; return nil },
	}
	return nil
}

func (a *App) trashEmpty() tea.Cmd {
	if a.trashStore == nil {
		return nil
	}
	res := a.trashStore.Empty()
	a.reloadTrash()
	if res.Removed == 0 {
		a.noteResult(lvlWarn, "nothing could be removed from the trash",
			"Trash not emptied", "Nothing could be removed from the trash")
		return nil
	}
	a.noteResult(lvlOK, fmt.Sprintf("emptied the trash (%s, %s)", plural(res.Removed, "item"), fsys.HumanSize(res.Bytes)),
		"Trash emptied", fmt.Sprintf("Deleted %s for good", plural(res.Removed, "item")),
		"Freed "+fsys.HumanSize(res.Bytes))
	return nil
}

// ---- sweep ---------------------------------------------------------------------

// trashSweepEvery is how often a running TwinPane looks for expired items in
// the trash. Start-up always sweeps; this keeps a session that stays open for
// days honest without needing a daemon or a timer that would fire into a
// terminal nobody is looking at.
const trashSweepEvery = 6 * time.Hour

// trashMaybeSweep schedules a sweep when the last one is old enough. It runs off
// key presses rather than a timer, so an idle TwinPane does nothing and a
// running one tidies up at the next interaction.
func (a *App) trashMaybeSweep() tea.Cmd {
	if a.trashStore == nil || a.trashLastSweep.IsZero() {
		return nil
	}
	if a.trashNow().Sub(a.trashLastSweep) < trashSweepEvery {
		return nil
	}
	return a.trashSweepCmd()
}

// trashKeepDays is the retention the sweep uses (the Settings value, clamped).
func (a *App) effectiveTrashKeepDays() int {
	d := a.trashKeepDays
	if d < ops.TrashKeepDaysMin {
		d = ops.TrashKeepDaysDef
	}
	if d > ops.TrashKeepDaysMax {
		d = ops.TrashKeepDaysMax
	}
	return d
}

// trashSweepCmd removes expired items in the background. It reports only when
// something was actually removed: a sweep that finds nothing is not news.
func (a *App) trashSweepCmd() tea.Cmd {
	st := a.trashStore
	if st == nil {
		return nil
	}
	keep := a.effectiveTrashKeepDays()
	cap := st.CapBytes()
	return func() tea.Msg {
		done := make(chan ops.TrashSweep, 1)
		go func() { done <- st.Sweep(keep, cap) }()
		return trashSweptMsg{sweep: <-done, keepDays: keep}
	}
}

// trashSweptMsg carries the result of a sweep back to the update loop.
type trashSweptMsg struct {
	sweep    ops.TrashSweep
	keepDays int
	err      error
}

// trashSwept applies a sweep: refresh the numbers and tell the user if
// something was removed. Nothing is reported when the trash had nothing to do.
func (a *App) trashSwept(msg trashSweptMsg) tea.Cmd {
	a.trashLastSweep = a.trashNow()
	a.refreshTrashStats()
	if a.trashScr != nil {
		a.reloadTrash()
	}
	if msg.sweep.Removed == 0 {
		if msg.sweep.Failed > 0 {
			a.noteResult(lvlWarn, "the trash could not be tidied",
				"Trash not tidied", fmt.Sprintf("%d item(s) in the trash could not be removed", msg.sweep.Failed))
		}
		return nil
	}
	what := fmt.Sprintf("Removed %s older than %s", plural(msg.sweep.Removed, "item"), plural(msg.keepDays, "day"))
	body := []string{"Freed " + fsys.HumanSize(msg.sweep.Bytes)}
	if msg.sweep.Capped > 0 {
		body = append(body, fmt.Sprintf("%d of them because the trash was over its size limit", msg.sweep.Capped))
	}
	a.noteResult(lvlOK, "trash tidy: removed "+plural(msg.sweep.Removed, "item"), "Trash tidied", what, body...)
	return nil
}

// trashNow is the clock the screen uses for "deleted 3 days ago".
func (a *App) trashNow() time.Time {
	if a.now != nil {
		return a.now()
	}
	return time.Now()
}

// trashAgeText renders how long ago something was deleted.
func trashAgeText(at, now time.Time) string {
	if at.IsZero() {
		return "a while ago"
	}
	d := now.Sub(at)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return plural(int(d/time.Minute), "minute") + " ago"
	case d < 24*time.Hour:
		return plural(int(d/time.Hour), "hour") + " ago"
	default:
		return plural(int(d/(24*time.Hour)), "day") + " ago"
	}
}

// ---- rendering -----------------------------------------------------------------

func (a *App) trashViewRender(height int) string {
	v := a.trashScr
	if v == nil || height <= 0 {
		return ""
	}
	th := a.theme
	innerW := max(1, a.width-3) // two border cells and the cursor column
	lines := []string{
		th.Summary.Render(fitWidth(" "+a.trashStatsText()+
			" · items older than "+plural(a.effectiveTrashKeepDays(), "day")+" are removed automatically", innerW)),
		"",
	}
	if v.err != nil {
		lines = append(lines, " "+th.Error.Render(fitWidth("cannot read the trash: "+errText(v.err), innerW-1)))
	} else if len(v.entries) == 0 {
		lines = append(lines,
			" "+th.Empty.Render("The trash is empty."),
			" "+th.Summary.Render(fitWidth("Deleting something (d) moves it here, so it can be restored with T.", innerW-1)))
	} else {
		a.trashScrollIntoView()
		rows := a.trashListRows()
		for i := v.offset; i < len(v.entries) && i < v.offset+rows; i++ {
			lines = append(lines, a.trashEntryLines(v.entries[i], i == v.sel, innerW)...)
		}
	}
	thumb := make([]bool, max(0, height-2))
	if n := len(v.entries); n > a.trashListRows() && len(thumb) > trashSummaryLines {
		cells := scrollbar.Cells(n, a.trashListRows(), v.offset, len(thumb)-trashSummaryLines)
		for i, on := range cells {
			if on && trashSummaryLines+i < len(thumb) {
				thumb[trashSummaryLines+i] = true
			}
		}
	}
	box := sectionBox(th, "Trash", "kept "+plural(a.effectiveTrashKeepDays(), "day"), lines, a.width, height, true, thumb)
	return strings.Join(fitBlock(box, height, a.width), "\n")
}

// trashEntryLines draws one entry (one or two lines, depending on the width).
func (a *App) trashEntryLines(e ops.TrashEntry, selected bool, width int) []string {
	th := a.theme
	name := fsys.SanitizeName(e.Name)
	if e.Dir {
		name += "/"
	}
	age := "deleted " + trashAgeText(e.DeletedAt, a.trashNow())
	size := fsys.HumanSize(e.Bytes)
	style := th.File
	if selected {
		style = th.CursorActive
	}
	render := func(text string) string {
		text = fitWidth(text, width)
		if selected {
			return style.Render(padRight(text, width))
		}
		return text
	}
	if a.trashTwoLine() {
		return []string{
			render(" " + name + "  " + size),
			render(" " + th.Summary.Render(fitWidth(age+" · "+shortPath(e.Origin), width-1))),
		}
	}
	nameW := max(10, min(28, width/3))
	right := age + " · " + size
	rightW := min(30, len([]rune(right)))
	originW := max(0, width-2-nameW-rightW-4)
	line := " " + padRight(truncRight(name, nameW), nameW) + "  " +
		padRight(fitWidth(shortPath(e.Origin), originW), originW) + "  " +
		padLeft(truncRight(right, rightW), rightW) + " "
	return []string{render(line)}
}

// trashHints is the hint bar for the screen.
func (a *App) trashHints() []barItem {
	out := []barItem{item("↑↓", "move", "")}
	if a.trashItems > 0 {
		out = append(out, a.bind("trash.restore", "restore"), a.bind("trash.delete", "delete for good"), a.bind("trash.empty", "empty"))
	}
	return append(out, a.bind("trash.close", "close"))
}

// trashMouse selects a row on a tap and scrolls with the wheel.
func (a *App) trashMouse(ev tea.MouseEvent) tea.Cmd {
	v := a.trashScr
	if v == nil {
		return nil
	}
	if ev.IsWheel() {
		v.offset -= wheelDelta(ev)
		if v.offset < 0 {
			v.offset = 0
		}
		return nil
	}
	if ev.Action != tea.MouseActionPress || ev.Button != tea.MouseButtonLeft {
		return nil
	}
	// The list starts under the border, the summary line and the blank line.
	row := ev.Y - 1 - trashSummaryLines
	if row < 0 {
		return nil
	}
	per := 1
	if a.trashTwoLine() {
		per = 2
	}
	idx := v.offset + row/per
	if idx < 0 || idx >= len(v.entries) {
		return nil
	}
	v.sel = idx
	return nil
}

// padLeft right-aligns s in w cells (the age column of the wide layout).
func padLeft(s string, w int) string {
	if n := w - lipgloss.Width(s); n > 0 {
		return strings.Repeat(" ", n) + s
	}
	return s
}
