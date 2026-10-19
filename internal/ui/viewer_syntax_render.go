package ui

import (
	"sort"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/textview"
)

// viewerText composes Chroma's syntax colours with TwinPane's search-match
// highlight. Match styling is applied last (a background colour), so it
// remains unmistakable against token foreground colours.
func (a *App) viewerText(line int, text string, startCell int) string {
	v := a.viewer
	if v == nil || v.doc == nil || line < 0 || line >= len(v.doc.Lines) {
		return a.highlight(text, "")
	}
	full := v.line(line)
	source := full
	if line < len(v.syntaxLines) && v.syntaxLines[line] != "" {
		source = v.syntaxLines[line]
	}
	cut := func(left, width int) string {
		if width <= 0 {
			return ""
		}
		return ansi.Cut(source, left, left+width)
	}
	if v.query == "" {
		return cut(startCell, ansi.StringWidth(text))
	}

	if v.rangeLine != line || v.rangeQuery != v.query {
		v.rangeLine, v.rangeQuery = line, v.query
		v.rangeCells = viewerMatchCellRanges(full, v.query)
	}
	ranges := v.rangeCells
	if len(ranges) == 0 {
		return cut(startCell, ansi.StringWidth(text))
	}

	visibleEnd := startCell + ansi.StringWidth(text)
	first := sort.Search(len(ranges), func(i int) bool { return ranges[i][1] > startCell })
	position := startCell
	var out strings.Builder
	for i := first; i < len(ranges) && ranges[i][0] < visibleEnd; i++ {
		matchStart, matchEnd := ranges[i][0], ranges[i][1]
		visibleStart := max(startCell, matchStart)
		visibleMatchEnd := min(visibleEnd, matchEnd)
		if visibleStart > position {
			out.WriteString(cut(position, visibleStart-position))
		}
		matched := ansi.Cut(text, visibleStart-startCell, visibleMatchEnd-startCell)
		if matched != "" {
			out.WriteString(a.theme.Match.Render(matched))
		}
		position = visibleMatchEnd
	}
	if position < visibleEnd {
		out.WriteString(cut(position, visibleEnd-position))
	}
	return out.String()
}

// viewerMatchCellRanges converts byte ranges to terminal-cell offsets once per
// line. The cached ranges let wrapped chunks highlight partial matches without
// rescanning a long line for every chunk.
func viewerMatchCellRanges(line, query string) [][2]int {
	byteRanges := textview.MatchRanges(line, query)
	if len(byteRanges) == 0 {
		return nil
	}
	cells := make([][2]int, 0, len(byteRanges))
	bytePos, cellPos := 0, 0
	for _, match := range byteRanges {
		cellPos += ansi.StringWidth(line[bytePos:match[0]])
		start := cellPos
		cellPos += ansi.StringWidth(line[match[0]:match[1]])
		cells = append(cells, [2]int{start, cellPos})
		bytePos = match[1]
	}
	return cells
}
