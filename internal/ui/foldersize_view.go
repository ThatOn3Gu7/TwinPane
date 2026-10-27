package ui

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/ThatOn3Gu7/TwinPane/internal/foldersize"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/scrollbar"
	"github.com/ThatOn3Gu7/TwinPane/internal/theme"
)

// The folder-size overlay uses the same visual language as search: a compact
// summary at the top, a bordered table with aligned columns, an optional
// details panel and a separate status line. It is inspired by the structured
// tables of Nushell but stays a navigable ncdu-style browser.
//
// The layout is shared with the key and mouse code: listTop/listRows describe
// where the rows of the table start, so cursor movement and taps always agree
// with what is drawn.

type folderSizeLayout struct {
	compact bool

	width, height     int
	summaryH, itemsH  int
	detailsH, statusH int
	listTop, listRows int
}

// folderSizeLayout computes the body geometry for a body of height lines.
func (a *App) folderSizeLayout(height int) folderSizeLayout {
	h := max(1, height)
	w := max(1, a.width)
	l := folderSizeLayout{width: w, height: h}
	if h < 9 || w < 24 {
		l.compact = true
		l.listTop = min(3, h)
		l.listRows = max(0, h-l.listTop)
		return l
	}

	l.summaryH = 4
	if w < 76 {
		l.summaryH = 5
	}
	if h < 14 {
		l.summaryH = 4
	}
	l.statusH = 1
	items := h - l.summaryH - l.statusH
	if w >= 88 && items >= 12 {
		l.detailsH = 5
		items -= l.detailsH
	}
	if items < 5 {
		// The details panel is the first thing to go; the table, summary and
		// status line stay.
		l.detailsH = 0
		items = h - l.summaryH - l.statusH
	}
	l.itemsH = max(3, items)
	if l.itemsH > h-l.summaryH-l.statusH {
		l.summaryH = 3
		l.itemsH = max(3, h-l.summaryH-l.statusH)
	}
	l.listTop = l.summaryH + 2 // border, column header
	l.listRows = max(0, l.itemsH-3)
	return l
}

// folderSizeListRows is how many item rows fit on screen at the current size.
func (a *App) folderSizeListRows(height int) int {
	return a.folderSizeLayout(height).listRows
}

// folderSizeHeaderLines is the number of body rows above the first item; the
// mouse code turns a tap into a row with it.
func (a *App) folderSizeHeaderLines(height int) int {
	return a.folderSizeLayout(height).listTop
}

func (a *App) folderSizeViewRender(height int) string {
	if height <= 0 {
		return ""
	}
	v := a.folderSize
	if v == nil {
		return strings.Repeat("\n", height-1)
	}
	l := a.folderSizeLayout(height)
	var lines []string
	switch {
	case l.compact:
		lines = a.folderSizeCompactLines(v, l)
	case v.loading || v.result == nil:
		lines = a.folderSizeLoadingLines(v, l)
	case v.root == nil:
		lines = a.folderSizeFailedLines(v, l)
	default:
		lines = append(lines, a.folderSizeSummaryBox(v, l)...)
		lines = append(lines, a.folderSizeItemsBox(v, l)...)
		if l.detailsH > 0 {
			lines = append(lines, a.folderSizeDetailsBox(v, l)...)
		}
		if l.statusH > 0 {
			lines = append(lines, a.folderSizeStatusLine(v, l.width))
		}
	}
	return strings.Join(fitBlock(lines, height, a.width), "\n")
}

// ---- summary -----------------------------------------------------------------

func (a *App) folderSizeSummaryBox(v *folderSizeView, l folderSizeLayout) []string {
	th := a.theme
	inner := max(1, l.width-2)
	rows := max(0, l.summaryH-2)
	body := make([]string, 0, rows)
	push := func(line string) {
		if len(body) < rows {
			body = append(body, line)
		}
	}
	label, path := v.rootLabel, fsys.SanitizeName(shortPath(v.rootPath))
	target := "Target: "
	switch {
	case label != "" && path != "":
		target += label + " · " + path
	case label != "":
		target += label
	case path != "":
		target += path
	default:
		target += "preparing the scan…"
	}
	if v.root == nil {
		push(th.Info.Render(truncRight(target, inner)))
		push(th.Summary.Render(truncRight(folderSizeProgressSummary(v.progress), inner)))
		push(th.Summary.Render(truncRight(folderSizeProgressCounts(v.progress), inner)))
		return sectionBox(th, "Folder-size analysis", folderSizeStateLabel(v), body, l.width, l.summaryH, true, nil)
	}

	node := v.root
	sizeLine := "Apparent " + formatFolderSize(node.ApparentBytes, node.ApparentKnown, node.Overflow)
	if foldersize.AllocatedSizeSupported() {
		sizeLine += " · Allocated " + formatFolderSize(node.AllocatedBytes, node.AllocatedKnown, node.Overflow)
	}
	countLine := folderSizePlural(node.Directories, "dir") + " · " +
		folderSizePlural(node.Files, "file") + " · " + folderSizePlural(node.Symlinks, "link")
	if node.ErrorCount > 0 {
		countLine += " · " + folderSizePlural(node.ErrorCount, "issue")
	}
	if len(body) == 1 {
		push(th.Info.Render(truncRight(target+" · "+sizeLine+" · "+countLine, inner)))
	} else {
		push(th.Info.Render(truncRight(target, inner)))
		push(th.DialogText.Render(truncRight(sizeLine+" · "+countLine, inner)))
		if node.Specials > 0 {
			push(th.Summary.Render(truncRight(fmt.Sprintf("Current: %s · %d special files",
				fsys.SanitizeName(shortPath(v.current.Path)), node.Specials), inner)))
		} else {
			push(th.Summary.Render(truncRight("Current: "+fsys.SanitizeName(shortPath(v.current.Path)), inner)))
		}
	}
	return sectionBox(th, "Folder-size analysis", folderSizeStateLabel(v), body, l.width, l.summaryH, true, nil)
}

// ---- items table -------------------------------------------------------------

// folderSizeCols is the column geometry of the largest-items table. Name is
// flexible; type, size and share are fixed so numbers line up.
type folderSizeCols struct {
	markerW, nameW, typeW, sizeW, shareW int
	total                                int
}

// folderSizeColumns picks the widest useful column set that still leaves a
// readable name column. The share column only appears when there is room for
// a bar or at least a percentage.
func folderSizeColumns(inner int) folderSizeCols {
	variants := []folderSizeCols{
		{markerW: 2, typeW: 4, sizeW: 9, shareW: 10}, // marker, bar, percent
		{markerW: 2, typeW: 4, sizeW: 9, shareW: 5},  // marker, percent
		{markerW: 2, typeW: 4, sizeW: 9},
		{markerW: 1, typeW: 3, sizeW: 8},
		{markerW: 1, typeW: 0, sizeW: 8},
	}
	for _, c := range variants {
		fixed := c.markerW + 1 + c.typeW + c.sizeW // marker, space, name?, type, size
		if c.typeW > 0 {
			fixed++ // space between name and type
		}
		if c.shareW > 0 {
			fixed++ // space before share
		}
		c.nameW = inner - fixed
		if c.shareW > 0 {
			c.nameW -= c.shareW
		}
		c.total = inner
		if c.nameW >= 12 || (c.typeW == 0 && c.nameW >= 6) {
			return c
		}
	}
	c := folderSizeCols{markerW: 1, typeW: 0, sizeW: 0, total: inner}
	c.nameW = max(1, inner-1)
	return c
}

func folderSizeHeaderLine(th theme.Theme, cols folderSizeCols) string {
	indent := strings.Repeat(" ", cols.markerW)
	if cols.sizeW == 0 {
		return fitWidth(indent+styleCell(th.ColumnHeader, "NAME", cols.nameW, false), cols.total)
	}
	line := indent + styleCell(th.ColumnHeader, "NAME", cols.nameW, false) + " " +
		styleCell(th.ColumnHeader, "TYPE", cols.typeW, false) + " " +
		styleCell(th.ColumnHeader, "SIZE", cols.sizeW, true)
	if cols.shareW > 0 {
		line += " " + styleCell(th.ColumnHeader, "SHARE", cols.shareW, true)
	}
	return fitWidth(line, cols.total)
}

func (a *App) folderSizeItemsBox(v *folderSizeView, l folderSizeLayout) []string {
	th := a.theme
	rows := folderSizeRows(v)
	title := "Largest items"
	right := ""
	if l.width >= 50 {
		where := ""
		if rel, err := filepath.Rel(v.rootPath, v.current.Path); err == nil && rel != "." {
			where = "at " + fsys.SanitizeName(filepath.ToSlash(rel)) + " · "
		}
		right = where + plural(len(rows), "item")
	}
	body := make([]string, max(0, l.itemsH-2))
	cols := folderSizeColumns(max(1, l.width-2))
	dataRows := 0
	if len(body) > 0 {
		if len(rows) == 0 {
			body[0] = th.Empty.Render(truncRight(folderSizeEmptyText(v), l.width-2))
		} else {
			body[0] = folderSizeHeaderLine(th, cols)
			dataRows = len(body) - 1
		}
	}
	for i := 0; i < dataRows; i++ {
		index := v.offset + i
		if index >= len(rows) {
			break
		}
		body[i+1] = a.folderSizeRowLine(v, rows[index], index == v.cursor, cols)
	}
	thumb := scrollbar.Cells(len(rows), max(1, len(body)), v.offset, max(1, len(body)))
	return sectionBox(th, title, right, body, l.width, l.itemsH, true, thumb)
}

// folderSizeRowLine draws one table row in exactly cols.total cells: an
// aligned name, the kind, the size according to the active sort and, when
// there is room, the row's share of the current folder.
func (a *App) folderSizeRowLine(v *folderSizeView, row folderSizeRow, selected bool, cols folderSizeCols) string {
	th := a.theme
	name := fsys.SanitizeName(row.name())
	label, style := name, th.File
	switch {
	case row.directory != nil:
		label += "/"
		style = th.Dir
	case row.entry.Kind == foldersize.KindSymlink:
		label += "@"
		style = th.Link
	case row.entry.Kind == foldersize.KindSpecial:
		style = th.Exec
	}
	size, known, overflow := row.apparent()
	if v.sortAlloc {
		size, known, overflow = row.allocated()
	}
	sizeText := formatFolderSize(size, known, overflow)
	bar, percent := folderSizeShareParts(size, folderSizeShareTotal(v.current, v.sortAlloc), 4)

	if selected {
		line := "▸ " + cellText(label, cols.nameW, false)
		if cols.typeW > 0 {
			line += " " + cellText(folderSizeKindLabel(row), cols.typeW, false)
		}
		if cols.sizeW > 0 {
			line += " " + cellText(sizeText, cols.sizeW, true)
		}
		if cols.shareW > 0 {
			line += " " + folderSizeShareCell(th, bar, percent, cols.shareW, false)
		}
		return th.CursorActive.Render(fitWidth(line, cols.total))
	}

	line := " "
	if cols.markerW == 2 {
		line = "  "
	}
	line += styleCell(style, label, cols.nameW, false)
	if cols.typeW > 0 {
		line += " " + styleCell(th.ColumnHeader, folderSizeKindLabel(row), cols.typeW, false)
	}
	if cols.sizeW > 0 {
		line += " " + styleCell(th.Summary, sizeText, cols.sizeW, true)
	}
	if cols.shareW > 0 {
		line += " " + folderSizeShareCell(th, bar, percent, cols.shareW, true)
	}
	return fitWidth(line, cols.total)
}

// folderSizeKindLabel is the narrow TYPE column: DIR, FILE, LINK or SPEC.
func folderSizeKindLabel(row folderSizeRow) string {
	switch {
	case row.directory != nil:
		return "DIR"
	case row.entry.Kind == foldersize.KindSymlink:
		return "LINK"
	case row.entry.Kind == foldersize.KindSpecial:
		return "SPEC"
	}
	return "FILE"
}

// folderSizeShareParts splits the share cell into a bar and a percentage.
func folderSizeShareParts(part, total uint64, barW int) (bar, percent string) {
	bar = strings.Repeat(" ", max(0, barW))
	if pct, ok := sharePercent(part, total); ok {
		bar = barText(part, total, barW)
		switch {
		case pct == 0 && part > 0:
			percent = "<1%"
		case pct < 10:
			percent = "  " + strconv.Itoa(pct) + "%"
		default:
			percent = strconv.Itoa(pct) + "%"
		}
		return bar, percent
	}
	return bar, "n/a"
}

// folderSizeShareCell composes the optional bar and the percentage in exactly
// shareW cells. The styled variant colours the bar with the progress palette
// and keeps the percentage muted.
func folderSizeShareCell(th theme.Theme, bar, percent string, shareW int, styled bool) string {
	if shareW <= 0 {
		return ""
	}
	pctW := min(5, shareW)
	barW := shareW - pctW
	if barW > 0 {
		barW--
	}
	barW = max(0, barW)
	if styled && strings.ContainsAny(bar, "█░") {
		filled := strings.Count(bar, "█")
		bar = th.ProgressFill.Render(strings.Repeat("█", filled)) +
			th.ProgressEmpty.Render(strings.Repeat("░", max(0, barW-filled)))
		return bar + " " + styleCell(th.Summary, percent, pctW, true)
	}
	return cellText(bar, barW, false) + " " + styleCell(th.Summary, percent, pctW, true)
}

// folderSizeShareTotal is the denominator of the SHARE column: the current
// directory's own total, using the metric the table is sorted by.
func folderSizeShareTotal(node *foldersize.Node, allocated bool) uint64 {
	if node == nil {
		return 0
	}
	if allocated && node.AllocatedKnown {
		return node.AllocatedBytes
	}
	return node.ApparentBytes
}

func folderSizeEmptyText(v *folderSizeView) string {
	if v.current == nil {
		return "No data"
	}
	if v.current.DirectEntries > 0 && len(folderSizeLeafEntries(v)) == 0 {
		return "No files cached; directories remain navigable"
	}
	return "No child entries"
}

// ---- details -----------------------------------------------------------------

func (a *App) folderSizeDetailsBox(v *folderSizeView, l folderSizeLayout) []string {
	th := a.theme
	inner := max(1, l.width-2)
	rows := folderSizeRows(v)
	if len(rows) == 0 {
		body := []string{th.Empty.Render("No item selected")}
		return sectionBox(th, "Selected", "", body, l.width, l.detailsH, false, nil)
	}
	index := max(0, min(v.cursor, len(rows)-1))
	row := rows[index]
	kind := "file"
	style := th.File
	if row.directory != nil {
		kind, style = "directory", th.Dir
	} else if row.entry.Kind == foldersize.KindSymlink {
		kind, style = "link", th.Link
	}

	path := fsys.SanitizeName(shortPath(row.path()))
	body := []string{style.Render("Path: " + truncLeft(path, max(0, inner-6)))}

	size, known, overflow := row.apparent()
	allocated, allocKnown, allocOverflow := row.allocated()
	if v.sortAlloc && row.directory != nil {
		size, known, overflow = allocated, allocKnown, allocOverflow
	}
	total := folderSizeShareTotal(v.current, v.sortAlloc)
	share := ""
	if pct, ok := sharePercent(size, total); ok {
		share = fmt.Sprintf(" · %d%% of this folder", pct)
	}
	sizes := formatFolderSize(size, known, overflow) + " apparent"
	if foldersize.AllocatedSizeSupported() {
		sizes += " · " + formatFolderSize(allocated, allocKnown, allocOverflow) + " allocated"
	}
	if row.directory != nil {
		body = append(body, th.DialogText.Render(truncRight("Subtree: "+sizes, inner)))
		body = append(body, th.Summary.Render(truncRight(kind+" · "+row.directory.Status.String()+
			" · "+plural(int(row.directory.Directories), "dir")+" · "+
			plural(int(row.directory.Files), "file")+" · "+
			plural(int(row.directory.Symlinks), "link"), inner)))
	} else {
		body = append(body, th.DialogText.Render(truncRight("Size:    "+sizes, inner)))
		body = append(body, th.Summary.Render(truncRight(kind+share, inner)))
	}
	return sectionBox(th, "Selected · "+fsys.SanitizeName(row.name()), kind, body, l.width, l.detailsH, false, nil)
}

// ---- status ------------------------------------------------------------------

func (a *App) folderSizeStatusLine(v *folderSizeView, width int) string {
	th := a.theme
	message := ""
	style := th.Info
	switch {
	case v.loading && v.cancelling:
		message, style = "Stopping the scan…", th.StatusWarn
	case v.loading:
		message = "Scanning… " + folderSizeProgressCounts(v.progress) + " · Esc cancels"
	case v.result == nil:
		message, style = "No scan results · r retries · Esc closes", th.StatusWarn
	default:
		r := v.result
		switch r.Status {
		case foldersize.StatusComplete:
			style = th.StatusOK
		case foldersize.StatusPartial, foldersize.StatusCancelled:
			style = th.StatusWarn
		case foldersize.StatusFailed:
			style = th.StatusErr
		}
		message = r.Status.String()
		if r.Errors > 0 {
			message += " · " + folderSizePlural(r.Errors, "issue")
			if r.Error != "" {
				message += ": " + r.Error
			}
		}
		message += fmt.Sprintf(" · %s scanned in %.1fs · sort: %s",
			plural(int(r.Progress.Entries), "entry"), r.Elapsed.Seconds(), folderSizeSortLabel(v))
		if r.MountsSkipped > 0 {
			message += fmt.Sprintf(" · %d mounts skipped", r.MountsSkipped)
		}
		if r.DuplicatesSkipped > 0 {
			message += fmt.Sprintf(" · %d duplicates skipped", r.DuplicatesSkipped)
		}
		if r.TopEntriesLimited {
			message += " · per-directory lists limited"
		}
	}
	return style.Render(truncRight(message, width))
}

// ---- loading, failure and compact layouts ------------------------------------

func (a *App) folderSizeLoadingLines(v *folderSizeView, l folderSizeLayout) []string {
	th := a.theme
	inner := max(1, l.width-2)
	title := "Scanning"
	right := ""
	switch {
	case v.result != nil:
		title, right = "Scan failed", folderSizeStateLabel(v)
	case !v.loading:
		title = "No scan results"
	}
	rows := max(0, l.itemsH-2)
	body := make([]string, 0, rows)
	push := func(line string) {
		if len(body) < rows {
			body = append(body, line)
		}
	}
	if v.result != nil && v.result.Error != "" {
		push(th.Error.Render(truncRight(v.result.Error, inner)))
	} else {
		current := v.progress.CurrentPath
		if current == "" {
			current = v.rootPath
		}
		if current != "" {
			push(th.Summary.Render(truncRight("Current: "+fsys.SanitizeName(shortPath(current)), inner)))
		}
		push(th.Info.Render(truncRight("Counting directories and files in the background…", inner)))
		push(th.DialogHint.Render(truncRight("Esc cancels the scan; results appear when it finishes", inner)))
	}
	lines := a.folderSizeSummaryBox(v, l)
	// No table or details panel while scanning: they use their full share.
	lines = append(lines, sectionBox(th, title, right, body, l.width, l.height-l.summaryH-l.statusH, true, nil)...)
	if l.statusH > 0 {
		lines = append(lines, a.folderSizeStatusLine(v, l.width))
	}
	return lines
}

func (a *App) folderSizeFailedLines(v *folderSizeView, l folderSizeLayout) []string {
	th := a.theme
	inner := max(1, l.width-2)
	body := make([]string, max(0, l.itemsH-2))
	if len(body) > 0 {
		body[0] = th.Error.Render(truncRight(v.result.Error, inner))
	}
	if len(body) > 1 {
		body[1] = th.DialogHint.Render(truncRight("r retries the scan; Esc closes this view", inner))
	}
	lines := a.folderSizeSummaryBox(v, l)
	lines = append(lines, sectionBox(th, "Scan failed", "", body, l.width, l.height-l.summaryH-l.statusH, true, nil)...)
	if l.statusH > 0 {
		lines = append(lines, a.folderSizeStatusLine(v, l.width))
	}
	return lines
}

// folderSizeCompactLines is the borderless fallback for very small terminals:
// a title, the target and one statistics line, then the aligned table.
func (a *App) folderSizeCompactLines(v *folderSizeView, l folderSizeLayout) []string {
	th := a.theme
	width := l.width
	state := folderSizeStateLabel(v)
	title := "Folder-size analysis"
	if width < 34 {
		title = "Folder size"
	}
	lines := []string{th.HelpTitle.Render(truncRight(title+" · "+state, width))}
	if l.height <= 1 {
		return lines
	}
	if v.loading || v.result == nil {
		if v.rootPath != "" {
			lines = append(lines, th.Info.Render(truncRight("Scanning "+fsys.SanitizeName(shortPath(v.rootPath)), width)))
			if l.height > 2 {
				lines = append(lines, th.Summary.Render(truncRight(folderSizeProgressSummary(v.progress), width)))
			}
		} else {
			lines = append(lines, th.Info.Render(truncRight("Scanning… Esc cancels", width)))
		}
		return lines
	}
	if !v.loading && v.result == nil {
		if len(lines) < l.height {
			lines = append(lines, th.Error.Render(truncRight("No scan results · r retries", width)))
		}
		return lines
	}
	if v.root == nil {
		lines = append(lines, th.Error.Render(truncRight("Scan failed: "+v.result.Error, width)))
		return lines
	}
	if l.height > 2 {
		line := "At: " + fsys.SanitizeName(shortPath(v.current.Path)) + " · " + folderSizeCompactStats(v.current)
		lines = append(lines, th.Summary.Render(truncRight(line, width)))
	}
	rows := folderSizeRows(v)
	if len(rows) == 0 {
		if len(lines) < l.height {
			lines = append(lines, th.Empty.Render(truncRight(folderSizeEmptyText(v), width)))
		}
		return lines
	}
	cols := folderSizeColumns(max(1, width))
	if l.height > 3 {
		lines = append(lines, folderSizeHeaderLine(th, cols))
	}
	for i := 0; i < l.listRows; i++ {
		index := v.offset + i
		if index >= len(rows) {
			break
		}
		lines = append(lines, a.folderSizeRowLine(v, rows[index], index == v.cursor, cols))
	}
	return lines
}

// ---- small labels and helpers ------------------------------------------------

func folderSizeStateLabel(v *folderSizeView) string {
	if v == nil {
		return ""
	}
	switch {
	case v.loading && v.cancelling:
		return "cancelling"
	case v.loading:
		return "scanning"
	case v.result != nil:
		return v.result.Status.String()
	}
	return "no results"
}

func folderSizeSortLabel(v *folderSizeView) string {
	switch {
	case v == nil:
		return ""
	case v.sortMode == folderSizeByName:
		return "name"
	case v.sortAlloc:
		return "allocated"
	}
	return "apparent size"
}

// folderSizePlural is plural for uint64 counts.
func folderSizePlural(n uint64, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return strconv.FormatUint(n, 10) + " " + pluralWord(word)
}

func folderSizeProgressSummary(p foldersize.Progress) string {
	text := formatFolderSize(p.ApparentBytes, true, false) + " apparent"
	if foldersize.AllocatedSizeSupported() {
		text += " · " + formatFolderSize(p.AllocatedBytes, p.AllocatedKnown, p.Overflow) + " allocated"
	}
	return text + " · " + folderSizeProgressCounts(p)
}

func folderSizeProgressCounts(p foldersize.Progress) string {
	parts := []string{
		folderSizePlural(p.Entries, "entry"),
		folderSizePlural(p.Directories, "dir"),
		folderSizePlural(p.Files, "file"),
	}
	if p.Errors > 0 {
		parts = append(parts, folderSizePlural(p.Errors, "issue"))
	}
	return strings.Join(parts, " · ")
}

func folderSizeCompactStats(node *foldersize.Node) string {
	if node == nil {
		return ""
	}
	return fmt.Sprintf("%s apparent · %d dirs · %d files · %d links",
		formatFolderSize(node.ApparentBytes, node.ApparentKnown, node.Overflow),
		node.Directories, node.Files, node.Symlinks)
}

// formatFolderSize renders a byte count, marking lower bounds (unknown or
// overflowing totals) with "≥" so a partial scan is never presented as exact.
func formatFolderSize(size uint64, known, overflow bool) string {
	text := foldersize.FormatBytes(size)
	if !known || overflow {
		return "≥" + text
	}
	return text
}
