package ui

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/scrollbar"
	"github.com/ThatOn3Gu7/TwinPane/internal/textview"
	"github.com/ThatOn3Gu7/TwinPane/internal/theme"
)

// advancedSearchLayout is the responsive geometry of Advanced Search. The
// top section always holds the query; the results and the preview get their
// own bordered panels. Wide terminals show the panes side by side, narrow or
// short ones stack them (results first), and very small terminals fall back to
// a borderless list so the content is never squeezed out.
type advancedSearchLayout struct {
	compact bool
	wide    bool

	width, height int
	searchH       int // height of the bordered search section

	resultsW, resultsH int
	previewW, previewH int
	gap                int

	resultTop  int // body row of the first result row
	resultRows int
	previewTop int // screen row of the preview panel's top border
}

func (a *App) advancedLayout(bodyHeight int) advancedSearchLayout {
	h := max(1, bodyHeight)
	w := max(1, a.width)
	l := advancedSearchLayout{width: w, height: h, searchH: 4, gap: 1}

	if w < 24 || h < 9 {
		l.compact = true
		head := 1 // title
		switch {
		case h >= 5:
			head = 4 // title, root, field, status
		case h == 4:
			head = 3 // title (with the root on its right), field, status
		case h == 3:
			head = 3
		case h == 2:
			head = 2
		}
		l.resultTop = min(head, h)
		l.resultRows = max(0, h-l.resultTop)
		return l
	}

	remaining := h - l.searchH
	if w >= 80 && remaining >= 8 {
		l.wide = true
		left := max(34, (w-1)*44/100)
		if left > w-1-30 {
			left = w - 1 - 30
		}
		l.resultsW, l.previewW = left, w-1-left
		l.resultsH, l.previewH = remaining, remaining
	} else {
		l.resultsW, l.previewW = w, w
		if h >= 14 && remaining >= 10 && w >= 40 {
			l.previewH = max(5, remaining*45/100)
			l.resultsH = remaining - l.previewH
			if l.resultsH < 5 {
				l.resultsH = 5
				l.previewH = remaining - 5
			}
			if l.previewH < 5 {
				l.previewH = 0
				l.resultsH = remaining
			}
		} else {
			l.resultsH = remaining
		}
	}
	l.resultTop = l.searchH + 1
	l.resultRows = max(0, l.resultsH-2)
	if l.previewH > 0 {
		if l.wide {
			// Both panels start directly below the search section.
			l.previewTop = l.searchH + 1
		} else {
			l.previewTop = l.searchH + l.resultsH + 1
		}
	}
	return l
}

func (a *App) advancedSearchResultRows() int {
	return a.advancedLayout(max(1, a.height-3)).resultRows
}

func (a *App) advancedSearchResultTop() int {
	return a.advancedLayout(max(1, a.height-3)).resultTop
}

func (a *App) advancedSearchRender(bodyHeight int) string {
	v := a.advancedSearch
	if v == nil {
		return ""
	}
	layout := a.advancedLayout(bodyHeight)
	a.clampAdvancedSearch()
	var lines []string
	if layout.compact {
		lines = a.advancedCompactLines(layout)
	} else {
		lines = a.advancedSearchSection(layout)
		if layout.wide {
			left := a.advancedResultsBox(layout)
			right := a.advancedPreviewBox(layout)
			lines = append(lines, joinBlocks(left, right, layout.gap)...)
		} else {
			lines = append(lines, a.advancedResultsBox(layout)...)
			if layout.previewH > 0 {
				lines = append(lines, a.advancedPreviewBox(layout)...)
			}
		}
	}
	return strings.Join(fitBlock(lines, layout.height, layout.width), "\n")
}

// advancedSearchSection is the bordered query section: mode and root in the
// heading, the input on the first row and the current search information on
// the second. Keyboard hints stay in the hint bar at the bottom of the screen.
func (a *App) advancedSearchSection(layout advancedSearchLayout) []string {
	v := a.advancedSearch
	inner := max(2, layout.width-2)
	root := "in " + fsys.SanitizeName(shortPath(v.root))
	title := "Advanced Search · " + a.advancedModeName()
	body := []string{
		a.advancedQueryLine(inner),
		a.advancedStatusLine(inner),
	}
	return sectionBox(a.theme, title, root, body, layout.width, layout.searchH, true, nil)
}

func (a *App) advancedResultsBox(layout advancedSearchLayout) []string {
	v := a.advancedSearch
	inner := max(1, layout.resultsW-2)
	title := fmt.Sprintf("Results · %d", a.advancedResultCount())
	right := ""
	if layout.resultsW >= 46 {
		if v.mode == advancedFilenameMode {
			right = "of " + plural(len(v.entries), "entry")
		} else {
			right = plural(int(v.contentStatus.ScannedFiles), "file") + " scanned"
		}
	}
	body := make([]string, layout.resultRows)
	for row := 0; row < layout.resultRows; row++ {
		index := v.offset + row
		switch {
		case index < a.advancedResultCount():
			body[row] = a.advancedResultRow(index, inner)
		case a.advancedResultCount() == 0 && row == 0:
			body[row] = a.theme.Empty.Render(truncRight(a.advancedEmptyText(), inner))
		}
	}
	thumb := scrollbar.Cells(a.advancedResultCount(), layout.resultRows, v.offset, layout.resultRows)
	return sectionBox(a.theme, title, right, body, layout.resultsW, layout.resultsH, true, thumb)
}

func (a *App) advancedPreviewBox(layout advancedSearchLayout) []string {
	title, right := a.advancedPreviewHeading()
	inner := max(1, layout.previewW-2)
	a.clampAdvancedPreview()
	rows := layout.previewH - 2
	body := make([]string, max(0, rows))
	for row := range body {
		body[row] = a.advancedPreviewRow(row, inner, len(body))
	}
	// The scroll indicator lives on the preview's right border, like the
	// file panels and the result lists.
	total, visible := a.advancedPreviewWindow(layout)
	thumb := scrollbar.Cells(total, visible, a.advancedSearch.previewOffset, visible)
	thumbRows := make([]bool, len(body))
	for i := range thumb {
		// Content previews keep their summary row pinned above the matches.
		shift := 0
		if a.advancedSearch.mode == advancedContentMode && rows > 1 {
			shift = 1
		}
		if i+shift < len(thumbRows) {
			thumbRows[i+shift] = thumb[i]
		}
	}
	return sectionBox(a.theme, title, right, body, layout.previewW, layout.previewH, false, thumbRows)
}

// advancedPreviewHeading names the file being previewed, mirroring the
// "Results · N" heading of the list.
func (a *App) advancedPreviewHeading() (title, right string) {
	v := a.advancedSearch
	if v == nil {
		return "Preview", ""
	}
	name := ""
	if v.mode == advancedContentMode {
		if v.cursor >= 0 && v.cursor < len(v.contentResults) {
			result := v.contentResults[v.cursor]
			name = filepath.Base(result.Rel)
			right = plural(len(result.Matches), "match")
		}
	} else if v.previewPath != "" {
		name = filepath.Base(v.previewPath)
	}
	if name == "" {
		return "Preview", ""
	}
	return "Preview · " + fsys.SanitizeName(name), right
}

func (a *App) advancedCompactLines(layout advancedSearchLayout) []string {
	v := a.advancedSearch
	th := a.theme
	head := th.HelpTitle.Render("Advanced Search · " + a.advancedModeName())
	rootText := "in " + fsys.SanitizeName(shortPath(v.root))
	if layout.height >= 5 {
		lines := []string{fitWidth(head, layout.width)}
		lines = append(lines, th.Summary.Render(truncRight(rootText, layout.width)))
		lines = append(lines, a.advancedQueryLine(layout.width))
		lines = append(lines, fitWidth(a.advancedStatusLine(layout.width), layout.width))
		return a.advancedCompactRows(layout, lines)
	}
	if layout.width < 42 {
		name := "Adv. search · Files"
		if v.mode == advancedContentMode {
			name = "Adv. search · Text"
		}
		head = th.HelpTitle.Render(name)
	}
	lines := []string{splitLine(head, th.Summary.Render(" "+rootText), layout.width)}
	if layout.height >= 2 {
		lines = append(lines, a.advancedQueryLine(layout.width))
	}
	if layout.height >= 3 {
		lines = append(lines, fitWidth(a.advancedStatusLine(layout.width), layout.width))
	}
	return a.advancedCompactRows(layout, lines)
}

// advancedCompactRows appends the visible result rows after the compact
// header lines.
func (a *App) advancedCompactRows(layout advancedSearchLayout, lines []string) []string {
	v := a.advancedSearch
	th := a.theme
	for row := 0; row < layout.resultRows; row++ {
		index := v.offset + row
		if index < a.advancedResultCount() {
			lines = append(lines, a.advancedResultRow(index, layout.width))
			continue
		}
		if a.advancedResultCount() == 0 && row == 0 && layout.height > len(lines) {
			lines = append(lines, th.Empty.Render(truncRight(a.advancedEmptyText(), layout.width)))
		}
	}
	return lines
}

func (a *App) advancedModeName() string {
	if a.advancedSearch != nil && a.advancedSearch.mode == advancedContentMode {
		return "Content"
	}
	return "Filename"
}

func (a *App) advancedQueryLine(width int) string {
	v := a.advancedSearch
	if v == nil {
		return ""
	}
	label, placeholder := "Files: ", "Type a filename or path…"
	if v.mode == advancedContentMode {
		label, placeholder = "Text: ", "Search file contents…"
	}
	return fitWidth(inputLine(a.theme, label, placeholder, v.input, true, width), width)
}

func (a *App) advancedStatusLine(width int) string {
	v := a.advancedSearch
	if v == nil {
		return ""
	}
	var message string
	style := a.theme.Info
	if v.mode == advancedFilenameMode {
		switch {
		case v.indexStatus.Err != nil:
			message, style = "Index failed: "+errText(v.indexStatus.Err), a.theme.Error
		case v.filtering:
			message = fmt.Sprintf("Filtering %s indexed entries…", plural(len(v.entries), "entry"))
		default:
			if width < 56 {
				message = fmt.Sprintf("%s · %d indexed", plural(len(v.filenameResults), "match"), len(v.entries))
			} else {
				message = fmt.Sprintf("%s · %s indexed", plural(len(v.filenameResults), "match"), plural(len(v.entries), "entry"))
			}
			if v.indexing {
				message += " · scanning…"
			}
		}
		if v.indexStatus.Truncated {
			if width < 56 {
				message += " · limit"
			} else {
				message += " · index limit reached"
			}
		}
		if v.indexStatus.Errors > 0 {
			message += fmt.Sprintf(" · %d unreadable", v.indexStatus.Errors)
		}
		if !v.showHidden {
			if width < 56 {
				message += " · hidden off"
			} else {
				message += " · hidden names skipped"
			}
		}
	} else {
		query := strings.TrimSpace(v.input.Value())
		switch {
		case query == "":
			if width < 60 {
				message = "Type a phrase · .git skipped"
			} else {
				message = "Type a phrase to search file contents · exact .git directories skipped"
			}
		case v.contentStatus.Err != nil:
			message, style = "Content search failed: "+errText(v.contentStatus.Err), a.theme.Error
		default:
			if width < 56 {
				message = fmt.Sprintf("%s · %d scanned · %s read", plural(len(v.contentResults), "file"),
					v.contentStatus.ScannedFiles, fsys.HumanSize(v.contentStatus.BytesRead))
			} else {
				message = fmt.Sprintf("%s · %s scanned · %s read", plural(len(v.contentResults), "file match"),
					plural(int(v.contentStatus.ScannedFiles), "file"), fsys.HumanSize(v.contentStatus.BytesRead))
			}
			if v.contentLoading {
				message += " · scanning…"
			}
		}
		if v.contentStatus.Errors > 0 {
			message += fmt.Sprintf(" · %d unreadable", v.contentStatus.Errors)
		}
		if v.contentStatus.Binary > 0 {
			message += fmt.Sprintf(" · %d binary skipped", v.contentStatus.Binary)
		}
		if v.contentStatus.TruncatedFiles > 0 {
			message += fmt.Sprintf(" · %d file prefixes only", v.contentStatus.TruncatedFiles)
		}
		if v.contentStatus.Truncated {
			message += " · result/scan limit reached"
		}
	}
	return style.Render(truncRight(message, width))
}

func (a *App) advancedEmptyText() string {
	v := a.advancedSearch
	if v == nil {
		return ""
	}
	if v.mode == advancedFilenameMode {
		if v.indexing {
			return "Indexing files… type to filter as results arrive"
		}
		if v.filtering {
			return "Filtering indexed paths…"
		}
		if strings.TrimSpace(v.input.Value()) != "" {
			return "No filename matches"
		}
		return "No files found"
	}
	if strings.TrimSpace(v.input.Value()) == "" {
		return "Enter text to search file contents"
	}
	if v.contentLoading {
		return "Scanning text files…"
	}
	return "No content matches"
}

// advancedResultRow draws one result in exactly width cells. The cursor row
// is one continuous highlight; other rows keep the type colours (directories,
// links) and a muted line column for content matches so the two columns stay
// aligned.
func (a *App) advancedResultRow(index, width int) string {
	v := a.advancedSearch
	th := a.theme
	if index < 0 || index >= a.advancedResultCount() || width <= 0 {
		return strings.Repeat(" ", max(0, width))
	}
	selected := index == v.cursor
	marker := "  "
	if selected {
		marker = "▸ "
	}
	plain := ""
	style := th.File
	if v.mode == advancedFilenameMode {
		entryIndex := v.filenameResults[index]
		if entryIndex < 0 || entryIndex >= len(v.entries) {
			return strings.Repeat(" ", width)
		}
		entry := &v.entries[entryIndex]
		entry.Resolve()
		label := fsys.SanitizeName(filepath.ToSlash(entry.Rel))
		switch {
		case entry.IsLink:
			label += "@"
			style = th.Link
		case entry.IsDir:
			label += "/"
			style = th.Dir
		}
		plain = marker + cellText(label, max(0, width-ansi.StringWidth(marker)), false)
	} else {
		result := v.contentResults[index]
		line := 0
		if len(result.Matches) > 0 {
			line = result.Matches[0].Line
		}
		label := fsys.SanitizeName(filepath.ToSlash(result.Rel))
		if width >= 26 {
			lineCell := cellText(strconv.Itoa(line), 5, true)
			if !selected {
				return fitWidth(marker+th.MatchCurrent.Render(lineCell)+"  "+styleCell(th.File, label, max(0, width-ansi.StringWidth(marker)-7), false), width)
			}
			plain = marker + lineCell + "  " + cellText(label, max(0, width-ansi.StringWidth(marker)-7), false)
		} else {
			plain = marker + cellText(label, max(0, width-ansi.StringWidth(marker)), false)
		}
	}
	plain = fitWidth(plain, width)
	if selected {
		return th.CursorActive.Render(plain)
	}
	return style.Render(plain)
}

// advancedPreviewRow draws one preview row: syntax-coloured code for filename
// search, and only the matching lines (with the query highlighted) for
// content search. Row 0 of a content preview summarises the file; the
// remaining rows are its matches.
func (a *App) advancedPreviewRow(row, width, rows int) string {
	v := a.advancedSearch
	th := a.theme
	blank := strings.Repeat(" ", max(0, width))
	if v == nil || row < 0 || row >= rows || width <= 0 {
		return blank
	}
	if v.mode == advancedContentMode {
		if v.cursor < 0 || v.cursor >= len(v.contentResults) {
			if row == 0 {
				return fitWidth(th.Empty.Render(a.advancedEmptyText()), width)
			}
			return blank
		}
		result := v.contentResults[v.cursor]
		if row == 0 && rows > 1 {
			label := fmt.Sprintf("%s · %d matching line(s)", fsys.SanitizeName(filepath.ToSlash(result.Rel)), len(result.Matches))
			return fitWidth(th.Summary.Render(truncRight(label, width)), width)
		}
		lineIndex := row - 1
		if rows == 1 {
			lineIndex = 0
		}
		lineIndex += v.previewOffset
		if lineIndex < 0 || lineIndex >= len(result.Matches) {
			return blank
		}
		match := result.Matches[lineIndex]
		return contentMatchRow(th, match.Line, textview.Expand(match.Text), v.input.Value(), width)
	}

	if v.previewLoading {
		if row == 0 {
			return fitWidth(th.Summary.Render("Loading preview…"), width)
		}
		return blank
	}
	if v.previewIsDir {
		if row == 0 {
			return fitWidth(th.Dir.Render("Directory · no text preview"), width)
		}
		return blank
	}
	if v.previewErr != nil {
		if row == 0 {
			return fitWidth(th.Error.Render(truncRight("Cannot preview: "+errText(v.previewErr), width)), width)
		}
		return blank
	}
	doc := v.previewDoc
	if doc == nil {
		if row == 0 {
			return fitWidth(th.Empty.Render("No preview yet"), width)
		}
		return blank
	}
	if doc.Binary {
		if row == 0 {
			return fitWidth(th.Empty.Render("Binary file · not displayed"), width)
		}
		if row == 1 && doc.BinaryReason != "" {
			return fitWidth(th.Summary.Render(truncRight(doc.BinaryReason, width)), width)
		}
		return blank
	}
	if len(doc.Lines) == 0 {
		if row == 0 {
			return fitWidth(th.Empty.Render("(empty file)"), width)
		}
		return blank
	}
	line := row + v.previewOffset
	if line < 0 || line >= len(doc.Lines) {
		return blank
	}
	return codePreviewRow(th, line+1, doc.Lines[line], previewSyntaxLine(v, line), len(doc.Lines), width)
}

// previewSyntaxLine returns the Chroma-rendered line when it is available.
func previewSyntaxLine(v *advancedSearchView, line int) string {
	if v == nil || line < 0 || line >= len(v.previewSyntax) {
		return ""
	}
	return v.previewSyntax[line]
}

// codePreviewRow renders a line-number gutter followed by syntax-coloured
// text, clipped to the available width.
func codePreviewRow(th theme.Theme, line int, plain, syntax string, total, width int) string {
	if width <= 0 {
		return ""
	}
	gutter := fmt.Sprintf("%*d ", gutterWidth(total), line)
	gutter += "│ "
	remain := width - ansi.StringWidth(gutter)
	if remain < 0 {
		remain = 0
	}
	text := syntax
	if text == "" {
		text = textview.Expand(plain)
	}
	text = ansi.Truncate(text, remain, "…")
	return th.LineNumber.Render(gutter) + fitWidth(text, remain)
}

// contentMatchRow renders one content match with a highlighted line number
// gutter, so matches stand apart from ordinary syntax colours.
func contentMatchRow(th theme.Theme, line int, text, query string, width int) string {
	if width <= 0 {
		return ""
	}
	gutter := cellText(strconv.Itoa(line), 5, true) + " │ "
	remain := max(0, width-ansi.StringWidth(gutter))
	rendered := truncRight(text, remain)
	rendered = highlightWith(th, rendered, query)
	return th.MatchCurrent.Render(gutter) + fitWidth(rendered, remain)
}

// gutterWidth is the line-number column width for a document of total lines.
func gutterWidth(total int) int {
	digits := len(strconv.Itoa(max(1, total)))
	return max(3, digits)
}
