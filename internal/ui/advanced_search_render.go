package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/textview"
)

type advancedSearchLayout struct {
	wide                    bool
	compact                 bool
	width, height           int
	leftWidth, rightWidth   int
	resultTop, resultRows   int // resultTop is an absolute screen row
	previewTop, previewRows int
}

func (a *App) advancedLayout(bodyHeight int) advancedSearchLayout {
	h := max(1, bodyHeight)
	w := max(1, a.width)
	layout := advancedSearchLayout{width: w, height: h, wide: w >= 80}
	if layout.wide && h >= 6 {
		inner := max(1, w-2)
		layout.leftWidth = max(28, inner*42/100)
		layout.rightWidth = max(1, inner-layout.leftWidth-1)
		layout.resultTop, layout.resultRows = 6, max(1, h-5)
		return layout
	}
	if !layout.wide && h >= 8 {
		layout.leftWidth = max(1, w-2)
		layout.rightWidth = layout.leftWidth
		available := h - 6 // four shared header rows and two pane headings
		layout.resultRows = max(1, (available+1)/2)
		layout.previewRows = max(0, available-layout.resultRows)
		layout.resultTop = 6
		layout.previewTop = 7 + layout.resultRows
		return layout
	}

	layout.compact = true
	if h <= 4 {
		layout.resultTop, layout.resultRows = 4, max(1, h-3)
	} else {
		layout.resultTop, layout.resultRows = 5, max(1, h-4)
	}
	return layout
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
	} else if layout.wide {
		lines = a.advancedWideLines(layout)
	} else {
		lines = a.advancedStackedLines(layout)
	}
	for len(lines) < layout.height {
		lines = append(lines, strings.Repeat(" ", layout.width))
	}
	if len(lines) > layout.height {
		lines = lines[:layout.height]
	}
	for i := range lines {
		lines[i] = fitWidth(lines[i], layout.width)
	}
	return strings.Join(lines, "\n")
}

func (a *App) advancedCompactLines(layout advancedSearchLayout) []string {
	v := a.advancedSearch
	th := a.theme
	lines := []string{
		fitWidth(th.HelpTitle.Render("Advanced Search · "+a.advancedModeName()), layout.width),
	}
	if layout.height > 4 {
		lines = append(lines, fitWidth(th.Info.Render("in "+fsys.SanitizeName(v.root)), layout.width))
	}
	lines = append(lines,
		a.advancedQueryLine(layout.width),
		fitWidth(a.advancedStatusLine(layout.width), layout.width),
	)
	if len(lines) < layout.height {
		index := v.cursor
		if a.advancedResultCount() == 0 {
			lines = append(lines, fitWidth(th.Empty.Render(a.advancedEmptyText()), layout.width))
		} else {
			lines = append(lines, a.advancedResultRow(index, layout.width))
		}
	}
	return lines
}

func (a *App) advancedWideLines(layout advancedSearchLayout) []string {
	v := a.advancedSearch
	th := a.theme
	lines := []string{
		fitWidth(th.HelpTitle.Render("Advanced Search · "+a.advancedModeName()), layout.width),
		fitWidth(th.Info.Render("in "+fsys.SanitizeName(v.root)), layout.width),
		a.advancedQueryLine(layout.width),
		fitWidth(a.advancedStatusLine(layout.width), layout.width),
	}
	leftHeader := th.HelpTitle.Render(fmt.Sprintf("Results · %s", plural(a.advancedResultCount(), "match")))
	rightHeader := th.HelpTitle.Render("Preview")
	lines = append(lines, a.advancedJoinColumns(leftHeader, rightHeader, layout))
	for row := 0; row < layout.resultRows; row++ {
		index := v.offset + row
		left := strings.Repeat(" ", layout.leftWidth)
		if index < a.advancedResultCount() {
			left = a.advancedResultRow(index, layout.leftWidth)
		} else if a.advancedResultCount() == 0 && row == 0 {
			left = th.Empty.Render(truncRight(a.advancedEmptyText(), layout.leftWidth))
		}
		right := a.advancedPreviewRow(row, layout.rightWidth, layout.resultRows)
		lines = append(lines, a.advancedJoinColumns(left, right, layout))
	}
	return lines
}

func (a *App) advancedStackedLines(layout advancedSearchLayout) []string {
	v := a.advancedSearch
	th := a.theme
	lines := []string{
		fitWidth(th.HelpTitle.Render("Advanced Search · "+a.advancedModeName()), layout.width),
		fitWidth(th.Info.Render("in "+fsys.SanitizeName(v.root)), layout.width),
		a.advancedQueryLine(layout.width),
		fitWidth(a.advancedStatusLine(layout.width), layout.width),
		fitWidth(th.HelpTitle.Render(fmt.Sprintf("Results · %s", plural(a.advancedResultCount(), "match"))), layout.width),
	}
	for row := 0; row < layout.resultRows; row++ {
		index := v.offset + row
		if index >= a.advancedResultCount() {
			if a.advancedResultCount() == 0 && row == 0 {
				lines = append(lines, fitWidth(th.Empty.Render(a.advancedEmptyText()), layout.width))
				continue
			}
			lines = append(lines, strings.Repeat(" ", layout.width))
			continue
		}
		lines = append(lines, a.advancedResultRow(index, layout.width))
	}
	lines = append(lines, fitWidth(th.HelpTitle.Render("Preview"), layout.width))
	for row := 0; row < layout.previewRows; row++ {
		lines = append(lines, a.advancedPreviewRow(row, layout.width, layout.previewRows))
	}
	return lines
}

func (a *App) advancedJoinColumns(left, right string, layout advancedSearchLayout) string {
	divider := a.theme.Divider.Render("│")
	left = fitWidth(left, layout.leftWidth)
	right = fitWidth(right, layout.rightWidth)
	return left + divider + right
}

func (a *App) advancedModeName() string {
	if a.advancedSearch != nil && a.advancedSearch.mode == advancedContentMode {
		return "CONTENT"
	}
	return "FILENAME"
}

func (a *App) advancedQueryLine(width int) string {
	v := a.advancedSearch
	if v == nil {
		return ""
	}
	label := "Files: "
	if v.mode == advancedContentMode {
		label = "Text: "
	}
	if width < ansi.StringWidth(label)+2 {
		label = ""
	}
	fieldWidth := max(2, width-ansi.StringWidth(label))
	return fitWidth(a.theme.DialogText.Render(label)+v.input.View(a.theme, fieldWidth), width)
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
			message = fmt.Sprintf("%s · %s indexed", plural(len(v.filenameResults), "match"), plural(len(v.entries), "entry"))
			if v.indexing {
				message += " · scanning…"
			}
		}
		if v.indexStatus.Truncated {
			message += " · index limit reached"
		}
		if v.indexStatus.Errors > 0 {
			message += fmt.Sprintf(" · %d unreadable folders", v.indexStatus.Errors)
		}
		if !v.showHidden {
			message += " · hidden names skipped"
		}
	} else {
		query := strings.TrimSpace(v.input.Value())
		switch {
		case query == "":
			message = "Type a phrase to search file contents · exact .git directories skipped"
		case v.contentStatus.Err != nil:
			message, style = "Content search failed: "+errText(v.contentStatus.Err), a.theme.Error
		default:
			message = fmt.Sprintf("%s · %s scanned · %s read", plural(len(v.contentResults), "file match"),
				plural(v.contentStatus.ScannedFiles, "file"), fsys.HumanSize(v.contentStatus.BytesRead))
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

func (a *App) advancedResultRow(index, width int) string {
	v := a.advancedSearch
	th := a.theme
	if index < 0 || index >= a.advancedResultCount() {
		return strings.Repeat(" ", max(0, width))
	}
	label := ""
	style := th.File
	if v.mode == advancedFilenameMode {
		entryIndex := v.filenameResults[index]
		if entryIndex < 0 || entryIndex >= len(v.entries) {
			return strings.Repeat(" ", max(0, width))
		}
		entry := &v.entries[entryIndex]
		entry.Resolve()
		label = fsys.SanitizeName(filepath.ToSlash(entry.Rel))
		switch {
		case entry.IsLink:
			label += "@"
			style = th.Link
		case entry.IsDir:
			label += "/"
			style = th.Dir
		}
	} else {
		result := v.contentResults[index]
		line := 0
		if len(result.Matches) > 0 {
			line = result.Matches[0].Line
		}
		label = fmt.Sprintf("%s:%d", fsys.SanitizeName(filepath.ToSlash(result.Rel)), line)
		style = th.File
	}
	label = truncLeft(label, width)
	label += strings.Repeat(" ", max(0, width-ansi.StringWidth(label)))
	if index == v.cursor {
		return th.CursorActive.Render(label)
	}
	return style.Render(label)
}

func (a *App) advancedPreviewRow(row, width, rows int) string {
	v := a.advancedSearch
	th := a.theme
	blank := strings.Repeat(" ", max(0, width))
	if v == nil || row < 0 || row >= rows {
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
		if row == 0 {
			if rows == 1 && len(result.Matches) > 0 {
				match := result.Matches[0]
				label := fmt.Sprintf("%d: %s", match.Line, textview.Expand(match.Text))
				return fitWidth(a.highlight(ansi.Truncate(label, width, "…"), v.input.Value()), width)
			}
			label := fmt.Sprintf("%s · %d matching line(s)", fsys.SanitizeName(filepath.ToSlash(result.Rel)), len(result.Matches))
			return fitWidth(th.Info.Render(truncRight(label, width)), width)
		}
		lineIndex := row - 1
		if lineIndex >= len(result.Matches) {
			return blank
		}
		match := result.Matches[lineIndex]
		text := fmt.Sprintf("%6d  %s", match.Line, textview.Expand(match.Text))
		text = ansi.Truncate(text, width, "…")
		return fitWidth(a.highlight(text, v.input.Value()), width)
	}

	if v.previewPath == "" {
		if row == 0 {
			return fitWidth(th.Empty.Render(a.advancedEmptyText()), width)
		}
		return blank
	}
	if row == 0 {
		label := filepath.ToSlash(v.previewPath)
		if v.cursor >= 0 && v.cursor < len(v.filenameResults) {
			entryIndex := v.filenameResults[v.cursor]
			if entryIndex >= 0 && entryIndex < len(v.entries) {
				label = filepath.ToSlash(v.entries[entryIndex].Rel)
			}
		}
		return fitWidth(th.Info.Render(truncLeft(fsys.SanitizeName(label), width)), width)
	}
	if v.previewLoading {
		if row == 1 {
			return fitWidth(th.Summary.Render("Loading preview…"), width)
		}
		return blank
	}
	if v.previewIsDir {
		if row == 1 {
			return fitWidth(th.Dir.Render("Directory · no text preview"), width)
		}
		return blank
	}
	if v.previewErr != nil {
		if row == 1 {
			return fitWidth(th.Error.Render("Cannot preview: "+errText(v.previewErr)), width)
		}
		return blank
	}
	doc := v.previewDoc
	if doc == nil {
		if row == 1 {
			return fitWidth(th.Summary.Render("Preview unavailable"), width)
		}
		return blank
	}
	if doc.Binary {
		if row == 1 {
			return fitWidth(th.Empty.Render("Binary file · not displayed"), width)
		}
		if row == 2 {
			return fitWidth(th.Summary.Render(doc.BinaryReason), width)
		}
		return blank
	}
	if len(doc.Lines) == 0 {
		if row == 1 {
			return fitWidth(th.Empty.Render("(empty file)"), width)
		}
		return blank
	}
	lineIndex := row - 1
	if lineIndex >= len(doc.Lines) {
		return blank
	}
	line := fmt.Sprintf("%5d  %s", lineIndex+1, textview.Expand(doc.Lines[lineIndex]))
	return fitWidth(th.File.Render(ansi.Truncate(line, width, "…")), width)
}

func (a *App) advancedSearchHints() []barItem {
	return []barItem{
		item("↑↓", "move", ""),
		item("Enter", "inspect read-only", "enter"),
		item("^T", "toggle files/content", "ctrl+t"),
		item("?", "help", "?"),
		item("Esc", "close", "esc"),
	}
}
