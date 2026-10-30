package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/compress"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/history"
	"github.com/ThatOn3Gu7/TwinPane/internal/scrollbar"
	"github.com/ThatOn3Gu7/TwinPane/internal/settings"
	"github.com/ThatOn3Gu7/TwinPane/internal/theme"
)

// The Settings screen: F9, Ctrl+S and the "Settings…" entry of the TwinPane
// badge menu all open the same window. Unlike help/tasks it is not a
// full-screen view: it floats above the panes like a dialog — the
// directory behind stays visible, dimmed by the backdrop — with a blank
// line between its sections so they breathe. ↑/↓ to move, ←/→ (or h/l,
// Space, Enter) to change a value, Esc to close; every change is applied
// live where a live apply is possible and saved to settings.json
// immediately.
//
// Rows that cannot take effect until a restart or the next start (mouse,
// startup folders, starting panel) say so in a muted note. A damaged or
// newer-version settings file is announced at the top of the list; "r" is
// the intentional recovery path that overwrites it with the defaults.

// settingsScreen is the Settings screen's navigation state. The box
// rectangle is the screen placement of the last drawn window (filled by
// settingsView, same as renderDialog does for dialogs) for tap mapping.
type settingsScreen struct {
	sel      int
	scroll   int
	viewport int
	lineRow  []int // content line → row index, for tap hit-testing
	boxX     int
	boxY     int
	boxW     int
	boxH     int
}

// settingsRowID identifies a row for key and tap dispatch; rows are stable,
// but labels and values change with the preference.
type settingsRowID int

const (
	rowStartLeft settingsRowID = iota
	rowStartRight
	rowStartPane
	rowFocusPreview
	rowShowHidden
	rowSortKey
	rowSortDesc
	rowDirsFirst
	rowCorner
	rowDuration
	rowTheme
	rowEditor
	rowTips
	rowMouse
	rowWrap
	rowNumbers
	rowFormat
	rowLevel
	rowHistorySessions
	rowActivity
	rowActivitySize
	rowActivityDays
)

// settingsRow is one preference in the list. group is printed as a section
// title above the row when non-empty; after is a muted note on the right
// ("next start", "restart to apply").
type settingsRow struct {
	id    settingsRowID
	group string
	label string
	value string
	after string
	text  bool // Enter (or a tap) opens a text-field dialog
}

// noticeCorners are the notification corners in cycle order; the current
// default (bottom-right) must stay first for round-trips through the file.
var noticeCorners = []string{"br", "bl", "tr", "tl"}

var noticeCornerNames = map[string]string{
	"br": "bottom-right",
	"bl": "bottom-left",
	"tr": "top-right",
	"tl": "top-left",
}

func (a *App) settingsRows() []settingsRow {
	rows := []settingsRow{
		{id: rowStartLeft, group: "Panels", label: "Startup folder — left",
			value: startupLabel(a.startLeft), after: "next start", text: true},
		{id: rowStartRight, label: "Startup folder — right",
			value: startupLabel(a.startRight), after: "next start", text: true},
		{id: rowStartPane, label: "Starting panel",
			value: map[int]string{0: "left", 1: "right"}[a.startPane], after: "next start"},
		{id: rowFocusPreview, label: "Focus-mode preview pane", value: onOff(a.focusPreview)},
		{id: rowShowHidden, label: "Show hidden files", value: onOff(a.showHidden)},

		{id: rowSortKey, group: "Sorting", label: "Sort by", value: upperFirst(a.defSort.String())},
		{id: rowSortDesc, label: "Sort direction",
			value: map[bool]string{true: "descending", false: "ascending"}[a.defDesc]},
		{id: rowDirsFirst, label: "Directories first", value: onOff(a.defDirsFirst)},

		{id: rowCorner, group: "Notifications", label: "Position",
			value: noticeCornerNames[a.noticeCorner]},
		{id: rowDuration, label: "Duration",
			value: formatSeconds(int(a.noticeDur/time.Second)) + " (2–10)", after: "new notices"},

		{id: rowTheme, group: "Appearance", label: "Theme", value: a.themeName},

		{id: rowEditor, group: "Editor & viewer", label: "Preferred editor",
			value: editorLabel(a.editorCmd), text: true},
		{id: rowTips, label: "Editor tips before starting", value: onOff(a.tipsPref)},
		{id: rowMouse, label: "Mouse input", value: onOff(a.mousePref), after: "restart to apply"},
		{id: rowWrap, label: "Line wrapping (viewer)", value: onOff(a.viewerWrap), after: "next view"},
		{id: rowNumbers, label: "Line numbers (viewer)", value: onOff(a.viewerNums), after: "next view"},

		{id: rowFormat, group: "Compression", label: "Default format", value: a.compressFormatLabel()},
		{id: rowLevel, label: a.levelRowLabel(), value: a.levelRowValue()},

		{id: rowHistorySessions, group: "Operation history", label: "Runs kept",
			value: a.historyKeepLabel()},

		{id: rowActivity, group: "Activity log", label: "Record",
			value: a.actMode.Label(), after: "what TwinPane did"},
		{id: rowActivitySize, label: "Rotate at",
			value: fmt.Sprintf("%d KiB", a.actMaxKiB), after: "keeps 3 older files"},
		{id: rowActivityDays, label: "Delete rotated after",
			value: plural(a.actKeepDays, "day")},
	}
	return rows
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func formatSeconds(n int) string {
	return strconv.Itoa(n) + " s"
}

// startupLabel shows a startup folder the way the panels do: empty means
// home, shown as ~.
func startupLabel(dir string) string {
	if strings.TrimSpace(dir) == "" {
		return "~"
	}
	return shortPath(dir)
}

func editorLabel(cmd string) string {
	if strings.TrimSpace(cmd) == "" {
		return "auto ($VISUAL, $EDITOR)"
	}
	return cmd
}

// compressFormatIDs are the values of the Default-format row in cycle order.
func compressFormatIDs() []compress.ID {
	ids := []compress.ID{""} // auto: the dialog picks the first capable format
	for _, f := range compress.Formats() {
		ids = append(ids, f.ID)
	}
	return ids
}

// historyKeepLabel shows the operation-history retention limit with the range
// the +/- keys stay inside.
func (a *App) historyKeepLabel() string {
	return plural(a.historyRetention(), "run") + " (1–200)"
}

func (a *App) compressFormatLabel() string {
	if a.lastFormat == "" {
		return "auto (ZIP)"
	}
	if f, ok := compress.Lookup(a.lastFormat); ok {
		return f.Name
	}
	return string(a.lastFormat)
}

// levelFormat is the format the Level row edits: the remembered default, or
// ZIP when the format is automatic (ZIP is what the dialog usually picks).
func (a *App) levelFormat() (compress.Format, bool) {
	if f, ok := compress.Lookup(a.lastFormat); ok {
		return f, true
	}
	return compress.Lookup(compress.ZIP)
}

func (a *App) levelRowLabel() string {
	if f, ok := a.levelFormat(); ok {
		return "Level for " + f.Name
	}
	return "Level"
}

func (a *App) levelRowValue() string {
	f, ok := a.levelFormat()
	if !ok || !f.HasLevels() {
		return "—"
	}
	return f.Levels[a.levelIndex(f)].Name
}

// ---- opening and closing ------------------------------------------------------

// openSettings shows the Settings screen (F9 / Ctrl+S / the menu entry).
func (a *App) openSettings() tea.Cmd {
	a.showSettings = true
	a.settingsScr = settingsScreen{}
	return nil
}

func (a *App) closeSettings() tea.Cmd {
	a.showSettings = false
	return nil
}

// syncSettingsLayout re-renders the screen into itself so key and tap
// handlers know the current viewport (the help screen does the same).
func (a *App) syncSettingsLayout() {
	if a.width >= minWidth && a.height >= minHeight {
		a.settingsView()
	}
}

// ---- keys --------------------------------------------------------------------

// settingsKey handles keys while the Settings screen is shown. It never
// reaches the panels: Esc / F9 / Ctrl+S close it, ? opens the help on top
// of it, and everything else is navigation.
func (a *App) settingsKey(msg tea.KeyMsg) tea.Cmd {
	a.syncSettingsLayout()
	rows := a.settingsRows()
	if len(rows) == 0 {
		return nil
	}
	if a.settingsScr.sel >= len(rows) {
		a.settingsScr.sel = len(rows) - 1
	}
	s := msg.String()
	page := a.settingsScr.viewport
	if page < 1 {
		page = 1
	}
	switch s {
	case "esc", "f9", "ctrl+s":
		return a.closeSettings()
	case "up", "k":
		a.settingsScr.sel--
	case "down", "j":
		a.settingsScr.sel++
	case "pgup":
		a.settingsScr.sel -= page
	case "pgdown":
		a.settingsScr.sel += page
	case "home", "g":
		a.settingsScr.sel = 0
	case "end", "G":
		a.settingsScr.sel = len(rows) - 1
	case "left", "h":
		return a.settingsAdjust(rows, -1)
	case "right", "l", " ", "enter":
		return a.settingsAdjust(rows, +1)
	case "?":
		a.openHelp()
		return nil
	case "r":
		return a.settingsResetConfirm()
	}
	if a.settingsScr.sel < 0 {
		a.settingsScr.sel = 0
	}
	if a.settingsScr.sel >= len(rows) {
		a.settingsScr.sel = len(rows) - 1
	}
	return nil
}

// settingsAdjust changes the selected row's value by delta (toggles ignore
// the direction) and saves. RowShowHidden keeps its historic path: it flips,
// applies to both panels and reports with its own message.
func (a *App) settingsAdjust(rows []settingsRow, delta int) tea.Cmd {
	i := a.settingsScr.sel
	if i < 0 || i >= len(rows) {
		return nil
	}
	r := rows[i]
	var cmd tea.Cmd
	switch r.id {
	case rowStartLeft, rowStartRight, rowEditor:
		return a.editSettingsText(r) // text rows: change opens the editor
	case rowShowHidden:
		return a.toggleHidden()
	case rowStartPane:
		a.startPane = 1 - a.startPane
	case rowFocusPreview:
		a.focusPreview = !a.focusPreview
	case rowSortKey:
		a.defSort = cycleSortKey(a.defSort, delta)
		cmd = tea.Batch(
			a.panels[0].SetSort(a.defSort, a.defDesc),
			a.panels[1].SetSort(a.defSort, a.defDesc),
		)
	case rowSortDesc:
		a.defDesc = !a.defDesc
		cmd = tea.Batch(
			a.panels[0].SetSort(a.defSort, a.defDesc),
			a.panels[1].SetSort(a.defSort, a.defDesc),
		)
	case rowDirsFirst:
		a.defDirsFirst = !a.defDirsFirst
		cmd = tea.Batch(
			a.panels[0].SetDirsFirst(a.defDirsFirst),
			a.panels[1].SetDirsFirst(a.defDirsFirst),
		)
	case rowCorner:
		a.noticeCorner = cycleString(noticeCorners, a.noticeCorner, delta)
	case rowDuration:
		secs := int(a.noticeDur/time.Second) + delta
		if secs > settings.NoticeDurationMax {
			secs = settings.NoticeDurationMin
		}
		if secs < settings.NoticeDurationMin {
			secs = settings.NoticeDurationMax
		}
		a.noticeDur = time.Duration(secs) * time.Second
	case rowTheme:
		name := cycleString(theme.PresetNames(), a.themeName, delta)
		if th, ok := theme.Preset(name); ok {
			a.themeName, a.theme = name, th
		}
	case rowTips:
		a.tipsPref = !a.tipsPref
	case rowMouse:
		a.mousePref = !a.mousePref
	case rowWrap:
		a.viewerWrap = !a.viewerWrap
	case rowNumbers:
		a.viewerNums = !a.viewerNums
	case rowFormat:
		ids := compressFormatIDs()
		cur := string(a.lastFormat)
		next := cycleString(idsAsStrings(ids), cur, delta)
		a.lastFormat = compress.ID(next)
	case rowLevel:
		f, ok := a.levelFormat()
		if !ok || !f.HasLevels() {
			return nil
		}
		li := a.levelIndex(f) + delta
		if li < 0 {
			li = len(f.Levels) - 1
		}
		if li >= len(f.Levels) {
			li = 0
		}
		a.formatLevels[f.ID] = li
	case rowActivity:
		// The log is written by the same process that edits this preference,
		// so the new mode takes effect at once.
		a.actMode = a.actMode.Next(delta)
		if a.actLog != nil {
			a.actLog.SetMode(a.actMode)
		}
	case rowActivitySize:
		a.actMaxKiB += delta * 256
		if a.actMaxKiB > settings.ActivityMaxKiBMax {
			a.actMaxKiB = settings.ActivityMaxKiBMin
		}
		if a.actMaxKiB < settings.ActivityMaxKiBMin {
			a.actMaxKiB = settings.ActivityMaxKiBMax
		}
	case rowActivityDays:
		a.actKeepDays += delta * 5
		if a.actKeepDays > settings.ActivityDaysMax {
			a.actKeepDays = settings.ActivityDaysMin
		}
		if a.actKeepDays < settings.ActivityDaysMin {
			a.actKeepDays = settings.ActivityDaysMax
		}
	case rowHistorySessions:
		// The history store prunes older runs as soon as the limit drops, so
		// the screen and the store can never disagree.
		if a.history != nil {
			if want := history.ClampLimit(a.history.Limit() + delta); want != a.history.Limit() {
				if err := a.history.SetLimit(want); err != nil {
					a.settingsErr = err
				}
			}
		}
	}
	a.logSetting(a.settingsRows()[i])
	a.savePrefs(true)
	return cmd
}

func cycleSortKey(k fsys.SortKey, delta int) fsys.SortKey {
	n := int(k) + delta
	for n < 0 {
		n += 4
	}
	return fsys.SortKey(n % 4)
}

func cycleString(list []string, cur string, delta int) string {
	if len(list) == 0 {
		return cur
	}
	idx := 0
	for i, v := range list {
		if v == cur {
			idx = i
			break
		}
	}
	idx = (idx + delta) % len(list)
	if idx < 0 {
		idx += len(list)
	}
	return list[idx]
}

func idsAsStrings(ids []compress.ID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = string(id)
	}
	return out
}

// editSettingsText opens the shared line-input dialog for a text preference
// (startup folders, preferred editor). The Settings screen stays open
// behind it and receives the value when Enter is pressed.
func (a *App) editSettingsText(r settingsRow) tea.Cmd {
	d := &dialog{kind: dlgSettings, title: r.label}
	cur := ""
	switch r.id {
	case rowStartLeft:
		cur = a.startLeft
	case rowStartRight:
		cur = a.startRight
	case rowEditor:
		cur = a.editorCmd
	}
	d.input, d.inputLabel = newLineInput(cur), "Value:"
	switch r.id {
	case rowStartLeft, rowStartRight:
		d.body = []dline{text("Empty or ~ means the home directory; ~ also expands.")}
	default:
		d.body = []dline{text("Empty keeps the automatic choice ($VISUAL, $EDITOR, then a detected editor).")}
	}
	d.footer = "Enter apply · Esc cancel"
	d.onSubmit = func(a *App, v string) tea.Cmd {
		v = strings.TrimSpace(v)
		a.dialog = nil
		switch r.id {
		case rowStartLeft:
			a.startLeft = v
		case rowStartRight:
			a.startRight = v
		case rowEditor:
			a.editorCmd = v
		}
		a.savePrefs(true)
		return nil
	}
	a.dialog = d
	return nil
}

// settingsResetConfirm asks before overwriting the file: this is the
// recovery path for a damaged settings file and the way back to the
// defaults. Enter (the focused, non-dangerous button) cancels.
func (a *App) settingsResetConfirm() tea.Cmd {
	d := &dialog{kind: dlgSettings, title: "Reset settings?", width: 46}
	d.body = []dline{
		text("Every preference returns to its default."),
		hint("The settings file is replaced — this also recovers a damaged file."),
	}
	d.buttons = []button{
		{label: "Cancel", run: func(a *App) tea.Cmd { a.dialog = nil; return nil }},
		{label: "Reset (y)", key: "y", danger: true, run: func(a *App) tea.Cmd {
			return a.settingsResetApply()
		}},
	}
	// Focus stays on Cancel (index 0): Enter must not destroy the file;
	// "y" (the Reset button's hotkey) is the explicit action.
	a.dialog = d
	return nil
}

// settingsResetApply installs the defaults live and force-writes the file.
func (a *App) settingsResetApply() tea.Cmd {
	a.dialog = nil
	p := settings.Default()
	a.applyPreferences(p)
	cmd := a.syncPanelsWithPreferences()
	if a.settingsPath != "" {
		if err := settings.Reset(a.settingsPath, p); err != nil {
			a.settingsErr = err
			a.setStatusLevel("settings not reset: "+errText(err), lvlWarn)
			return cmd
		}
	}
	a.settingsErr = nil
	a.setStatusLevel("settings reset to defaults", lvlOK)
	return cmd
}

// ---- rendering ---------------------------------------------------------------

// settingsBody renders the corruption banner (when any), the group titles
// and the preference rows — with one blank line between groups so the
// sections have room to breathe — and returns the content-line → row
// mapping for taps (-1: banner, header or spacer). inner is the usable
// width of the window (box width minus its borders), so values right-align
// to the window instead of the screen.
func (a *App) settingsBody(inner int) ([]string, []int) {
	scr := &a.settingsScr
	rows := a.settingsRows()
	if scr.sel >= len(rows) {
		scr.sel = max(0, len(rows)-1)
	}
	if scr.sel < 0 {
		scr.sel = 0
	}

	var body []string
	var lineRow []int
	if a.settingsErr != nil {
		// Action first: long error text truncates on narrow screens.
		msg := "! settings damaged — press r to reset: " + errText(a.settingsErr)
		body = append(body, a.theme.Error.Render(msg))
		lineRow = append(lineRow, -1)
	}
	for i, r := range rows {
		if r.group != "" {
			if len(body) > 0 {
				// a blank line separates the sections; the very first
				// group starts at the top of the window
				body = append(body, "")
				lineRow = append(lineRow, -1)
			}
			body = append(body, a.theme.HelpTitle.Render(r.group))
			lineRow = append(lineRow, -1)
		}
		body = append(body, a.settingsRowLine(r, inner, i == scr.sel))
		lineRow = append(lineRow, i)
	}
	return body, lineRow
}

// settingsView renders the floating Settings window: a dialog-style box,
// centred over the dimmed panes (View composites it with overlay()), with
// the banner, the spaced-out groups and the rows, scrolled so the selected
// row stays visible. The screen rectangle of the box is recorded for tap
// hit-testing.
func (a *App) settingsView() string {
	scr := &a.settingsScr

	// Floating size: leave a margin of background on every side so the
	// directory behind the window stays visible.
	winW := min(a.width-6, 78)
	if winW < 16 {
		winW = max(4, a.width-2)
	}
	body, lineRow := a.settingsBody(winW - 2)
	winH := min(len(body)+2, max(3, a.height-4))

	viewport := winH - 2
	if viewport < 1 {
		viewport = 1
	}
	scr.viewport = viewport
	scr.lineRow = lineRow
	selLine := 0
	for li, ri := range lineRow {
		if ri == scr.sel {
			selLine = li
			break
		}
	}
	if scr.scroll > selLine {
		scr.scroll = selLine
	}
	if selLine >= scr.scroll+viewport {
		scr.scroll = selLine - viewport + 1
	}
	maxScroll := len(body) - viewport
	if maxScroll < 0 {
		maxScroll = 0
	}
	if scr.scroll > maxScroll {
		scr.scroll = maxScroll
	}
	if scr.scroll < 0 {
		scr.scroll = 0
	}
	visible := body[scr.scroll:min(scr.scroll+viewport, len(body))]
	thumb := scrollbar.Cells(len(body), viewport, scr.scroll, viewport)
	right := "settings.json"
	if a.settingsPath != "" {
		right = shortPath(a.settingsPath)
	} else {
		right = "not persisted"
	}
	lines := sectionBox(a.theme, "Settings", right, visible, winW, winH, true, thumb)
	box := strings.Join(lines, "\n")

	// Where overlay() will place this box on the screen (same maths as
	// renderDialog uses for dialogs).
	scr.boxX, scr.boxY = overlayOrigin(box, a.width, a.height)
	scr.boxW, scr.boxH = 0, 0
	for _, l := range lines {
		scr.boxW = max(scr.boxW, ansi.StringWidth(l))
	}
	scr.boxW = min(scr.boxW, a.width)
	scr.boxH = min(len(lines), a.height)
	return box
}

// settingsRowLine lays one preference out as label left, value right, with
// the selected row in the cursor style. avail is the window's inner width.
func (a *App) settingsRowLine(r settingsRow, avail int, selected bool) string {
	avail = max(4, avail)
	note := ""
	if r.after != "" {
		note = "  " + r.after
	}
	vw := ansi.StringWidth(r.value) + ansi.StringWidth(note)
	label := ansi.Truncate(r.label, max(4, avail-vw-1), "…")
	pad := avail - ansi.StringWidth(label) - vw
	if pad < 1 {
		pad = 1
	}
	if selected {
		plain := ansi.Truncate(label+strings.Repeat(" ", pad)+r.value+note, avail, "…")
		// Pad with spaces before styling so the cursor background reaches
		// the right border of the box.
		plain += strings.Repeat(" ", max(0, avail-ansi.StringWidth(plain)))
		return a.theme.CursorActive.Render(plain)
	}
	return a.theme.DialogText.Render(label) + strings.Repeat(" ", pad) +
		a.theme.HelpTitle.Render(r.value) + a.theme.DialogHint.Render(note)
}

// ---- mouse -------------------------------------------------------------------

// settingsMouse: the wheel scrolls; a tap selects a row, a second tap on the
// selected row changes its value (text rows open their field). Taps outside
// the list do nothing — Esc, F9 or Ctrl+S close the screen.
func (a *App) settingsMouse(ev tea.MouseEvent) tea.Cmd {
	if ev.IsWheel() {
		// The wheel moves the selection (dialogs do the same); the view
		// keeps the selected row visible.
		rows := a.settingsRows()
		a.settingsScr.sel += wheelDelta(ev) * wheelStep
		if a.settingsScr.sel < 0 {
			a.settingsScr.sel = 0
		}
		if a.settingsScr.sel >= len(rows) {
			a.settingsScr.sel = len(rows) - 1
		}
		return nil
	}
	if ev.Action != tea.MouseActionPress || ev.Button != tea.MouseButtonLeft {
		return nil
	}
	a.syncSettingsLayout()
	scr := &a.settingsScr
	if scr.boxW <= 0 || scr.boxH <= 0 {
		return nil // nothing drawn yet
	}
	// Map through the recorded rectangle: content line i of the box sits
	// at screen row boxY+1+i (one row for the top border).
	if ev.X < scr.boxX || ev.X >= scr.boxX+scr.boxW {
		return nil
	}
	content := ev.Y - scr.boxY - 1
	if content < 0 || content >= len(scr.lineRow) {
		return nil
	}
	li := scr.scroll + content
	if li < 0 || li >= len(scr.lineRow) {
		return nil
	}
	row := scr.lineRow[li]
	if row < 0 {
		return nil
	}
	rows := a.settingsRows()
	if scr.sel == row {
		return a.settingsAdjust(rows, +1)
	}
	scr.sel = row
	return nil
}

// ---- hints -------------------------------------------------------------------

// settingsHints is the hint bar of the Settings screen.
func (a *App) settingsHints() []barItem {
	return []barItem{
		item("↑↓", "move", ""),
		item("←→", "change", ""),
		a.bind("settings.next", "open / toggle"),
		a.bind("settings.reset", "reset defaults"),
		a.bind("settings.close", "close"),
	}
}
