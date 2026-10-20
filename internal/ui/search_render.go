package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/scrollbar"
)

// The live filename search uses the same section chrome as Advanced Search:
// a bordered input section (heading, root, field and status) above a bordered
// results panel. Very small terminals fall back to a borderless compact list.

type searchLayout struct {
	compact   bool
	width     int
	height    int
	inputH    int
	inputRows int
	showRoot  bool
	resultH   int
	resultTop int // body row of the first result row
	rows      int // visible result rows
	extras    int // query error and backend-note lines
}

func (a *App) searchLayout(bodyHeight int) searchLayout {
	h := max(1, bodyHeight)
	w := max(1, a.width)
	sv := a.search
	extras := 0
	if sv != nil {
		if sv.qerr != "" {
			extras++
		}
		if sv.last.Note != "" {
			extras++
		}
	}
	l := searchLayout{width: w, height: h, extras: extras}
	if h < 8 || w < 24 {
		l.compact = true
		head := 1 // title
		switch {
		case h >= 6:
			head = 4 // title, root, field, status
		case h >= 3:
			head = 3 // title (with the root on its right), field, status
		case h == 2:
			head = 2
		}
		head += extras
		l.resultTop = min(head, h)
		l.rows = max(0, h-l.resultTop)
		return l
	}
	l.showRoot = true
	l.inputRows = 3 + extras // root, field, status
	l.inputH = l.inputRows + 2
	if h-l.inputH < 4 {
		l.showRoot = false
		l.inputRows = 2 + extras
		l.inputH = l.inputRows + 2
	}
	if h-l.inputH < 3 {
		l.compact = true
		l.resultTop = min(3+extras, h)
		l.rows = max(0, h-l.resultTop)
		return l
	}
	l.resultH = h - l.inputH
	l.resultTop = l.inputH + 1
	l.rows = max(0, l.resultH-2)
	return l
}

func (a *App) searchRows() int {
	if a.search == nil {
		return 1
	}
	return max(1, a.searchLayout(a.height-3).rows)
}

func (a *App) searchViewRender(bodyHeight int) string {
	if a.search == nil {
		return ""
	}
	layout := a.searchLayout(bodyHeight)
	a.search.clamp(layout.rows)
	var lines []string
	if layout.compact {
		lines = a.searchCompactLines(layout)
	} else {
		lines = append(lines, a.searchInputBox(layout)...)
		lines = append(lines, a.searchResultsBox(layout)...)
	}
	return strings.Join(fitBlock(lines, layout.height, layout.width), "\n")
}

func (a *App) searchInputBox(layout searchLayout) []string {
	sv := a.search
	th := a.theme
	inner := max(1, layout.width-2)
	body := make([]string, 0, layout.inputRows)
	if layout.showRoot {
		body = append(body, th.Info.Render(truncRight("in "+fsys.SanitizeName(shortPath(sv.root)), inner)))
	}
	body = append(body, inputLine(th, "Name: ", "Type a filename or path…", sv.input, !sv.list, inner))
	text, style := a.searchStatus(layout.width)
	body = append(body, style.Render(truncRight(text, inner)))
	if sv.qerr != "" {
		body = append(body, th.StatusErr.Render(truncRight("✗ "+sv.qerr, inner)))
	}
	if sv.last.Note != "" {
		body = append(body, th.StatusWarn.Render(truncRight(sv.last.Note+"; using "+sv.last.Backend, inner)))
	}
	right := ""
	if layout.width >= 46 {
		right = sv.last.Backend + " · Ctrl+G advanced"
	}
	return sectionBox(th, "Find files by name", right, body, layout.width, layout.inputH, !sv.list, nil)
}

func (a *App) searchResultsBox(layout searchLayout) []string {
	sv := a.search
	th := a.theme
	inner := max(1, layout.width-2)
	title := fmt.Sprintf("Results · %d", len(sv.matches))
	right := ""
	if layout.width >= 46 {
		right = "of " + plural(len(sv.entries), "entry")
	}
	body := make([]string, layout.rows)
	n := len(sv.matches)
	for row := 0; row < layout.rows; row++ {
		index := sv.offset + row
		switch {
		case index < n:
			body[row] = a.searchResultRow(index, inner)
		case n == 0 && row == 0:
			body[row] = th.Empty.Render(truncRight(a.searchEmptyText(), inner))
		}
	}
	thumb := scrollbar.Cells(n, layout.rows, sv.offset, layout.rows)
	return sectionBox(th, title, right, body, layout.width, layout.resultH, sv.list, thumb)
}

// searchStatus returns the search information line and its style. Keyboard
// hints are deliberately absent: they live in the hint bar at the bottom of
// the screen.
func (a *App) searchStatus(width int) (string, lipgloss.Style) {
	sv := a.search
	th := a.theme
	n, total := len(sv.matches), len(sv.entries)
	if sv.last.Err != nil {
		return "Search failed: " + errText(sv.last.Err), th.Error
	}
	var state string
	if sv.query.String() == "" {
		state = plural(total, "entry")
		if sv.indexing {
			state += " so far, indexing…"
		} else {
			state += fmt.Sprintf(" (%.1fs)", sv.elapsed.Seconds())
		}
	} else {
		state = fmt.Sprintf("%s of %s", plural(n, "match"), plural(total, "entry"))
		if sv.indexing {
			state += " · indexing…"
		}
	}
	if sv.last.Truncated {
		state += fmt.Sprintf(" · stopped at %d entries", total)
	}
	if sv.last.Errors > 0 {
		state += fmt.Sprintf(" · %s unreadable", plural(sv.last.Errors, "folder"))
	}
	if len(sv.marks) > 0 {
		state += fmt.Sprintf(" · %d marked", len(sv.marks))
	}
	switch {
	case sv.hidden:
		state += " · hidden shown"
	case width >= 52:
		state += " · hidden skipped"
	default:
		state += " · hidden off"
	}
	return state, th.Info
}

func (a *App) searchEmptyText() string {
	sv := a.search
	switch {
	case sv.last.Err != nil:
		return "Search failed"
	case sv.indexing:
		return "Indexing files… results appear as they are found"
	case sv.query.String() != "":
		return "No matches"
	default:
		return "No files found"
	}
}

// searchResultRow draws one result with a selection marker and, when the
// width allows, a right-aligned type column so names and kinds line up.
func (a *App) searchResultRow(index, width int) string {
	sv := a.search
	th := a.theme
	if index < 0 || index >= len(sv.matches) || width <= 0 {
		return strings.Repeat(" ", max(0, width))
	}
	entry := &sv.entries[sv.matches[index]]
	entry.Resolve()
	name := fsys.SanitizeName(entry.Rel)
	kind := "FILE"
	style := th.File
	switch {
	case entry.IsLink:
		name += "@"
		kind = "LINK"
		style = th.Link
	case entry.IsDir:
		name += "/"
		kind = "DIR"
		style = th.Dir
	case strings.HasPrefix(filepath.Base(entry.Rel), "."):
		style = th.Hidden
	}
	selected := index == sv.cursor
	marker := "  "
	if selected {
		marker = "▸ "
	}
	typeWidth := 0
	if width >= 52 {
		typeWidth = 4
	}
	nameWidth := width - ansi.StringWidth(marker)
	if typeWidth > 0 {
		nameWidth -= typeWidth + 1
	}
	nameCell := cellText(name, max(0, nameWidth), false)

	if selected {
		plain := marker + nameCell
		if typeWidth > 0 {
			plain += " " + cellText(kind, typeWidth, true)
		}
		if sv.list {
			return th.CursorActive.Render(fitWidth(plain, width))
		}
		return th.CursorInactive.Render(fitWidth(plain, width))
	}
	line := marker + styleCell(style, name, max(0, nameWidth), false)
	if typeWidth > 0 {
		line += " " + styleCell(th.ColumnHeader, kind, typeWidth, true)
	}
	if sv.marks[entry.Path] {
		return th.SelectedRow(style).Render(fitWidth(ansi.Strip(line), width))
	}
	return fitWidth(line, width)
}

func (a *App) searchCompactLines(layout searchLayout) []string {
	sv := a.search
	th := a.theme
	rootText := "in " + fsys.SanitizeName(shortPath(sv.root))
	lines := []string{}
	if layout.height >= 6 {
		lines = append(lines, fitWidth(th.HelpTitle.Render("Find files by name"), layout.width))
		lines = append(lines, th.Summary.Render(truncRight(rootText, layout.width)))
	} else {
		lines = append(lines, splitLine(th.HelpTitle.Render("Find files by name"), th.Summary.Render(rootText), layout.width))
	}
	if layout.height >= 2 {
		lines = append(lines, inputLine(th, "Name: ", "Type a filename or path…", sv.input, !sv.list, layout.width))
	}
	if layout.height >= 3 {
		text, style := a.searchStatus(layout.width)
		lines = append(lines, style.Render(truncRight(text, layout.width)))
	}
	if sv.qerr != "" && len(lines) < layout.height {
		lines = append(lines, th.StatusErr.Render(truncRight("✗ "+sv.qerr, layout.width)))
	}
	if sv.last.Note != "" && len(lines) < layout.height {
		lines = append(lines, th.StatusWarn.Render(truncRight(sv.last.Note, layout.width)))
	}
	for row := 0; row < layout.rows; row++ {
		index := sv.offset + row
		if index < len(sv.matches) {
			lines = append(lines, a.searchResultRow(index, layout.width))
			continue
		}
		if len(sv.matches) == 0 && row == 0 && len(lines) < layout.height {
			lines = append(lines, th.Empty.Render(truncRight(a.searchEmptyText(), layout.width)))
		}
	}
	return lines
}
