package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/panel"
	"github.com/ThatOn3Gu7/TwinPane/internal/textview"
	"github.com/ThatOn3Gu7/TwinPane/internal/theme"
)

// Shared chrome for the full-screen overlays (live search, Advanced Search
// and folder-size analysis). Keeping the border drawing, cell alignment and
// proportional bars in one place makes the three screens look like parts of
// the same application and keeps their narrow-terminal behaviour consistent.

// sectionBox renders one bordered section whose top border carries a title and
// an optional right-aligned label. Every returned line is exactly width cells
// wide and exactly height lines are returned. Body rows are clipped (ANSI
// aware) and padded to the inner width. When the space is too small for a
// border the body is returned without one, so narrow terminals keep their
// content instead of losing it to the frame.
//
// thumb, when set, marks body rows that draw the scroll indicator on the right
// border (index 0 is the first body row), the same way the file panels draw it
// on their border.
func sectionBox(th theme.Theme, title, right string, body []string, width, height int, focused bool, thumb []bool) []string {
	if width <= 0 || height <= 0 {
		return nil
	}
	if width < 4 || height < 3 {
		return fitBlock(body, height, width)
	}
	borderColor := th.BorderOff
	if focused {
		borderColor = th.BorderOn
	}
	border := lipgloss.NewStyle().Foreground(borderColor)
	inner := width - 2
	out := make([]string, 0, height)
	out = append(out, sectionTop(th, title, right, width, focused))
	for i := 0; i < height-2; i++ {
		line := ""
		if i < len(body) {
			line = body[i]
		}
		rightBorder := border.Render("│")
		if i < len(thumb) && thumb[i] {
			rightBorder = th.ScrollThumb.Render(panel.ScrollThumbChar)
		}
		out = append(out, border.Render("│")+fitWidth(line, inner)+rightBorder)
	}
	out = append(out, border.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return out
}

// sectionTop draws "╭─ Title ───── Right ─╮" in exactly width cells. The
// labels are dropped or shortened when the width cannot hold them.
func sectionTop(th theme.Theme, title, right string, width int, focused bool) string {
	borderColor := th.BorderOff
	titleSt, rightSt := th.ColumnHeader, th.Summary
	if focused {
		borderColor = th.BorderOn
		titleSt = th.HelpTitle
	}
	border := lipgloss.NewStyle().Foreground(borderColor)
	inner := width - 2
	if title == "" && right == "" {
		return border.Render("╭" + strings.Repeat("─", inner) + "╮")
	}
	leftText, rightText := "", ""
	if title != "" {
		leftText = " " + title + " "
	}
	if right != "" {
		rightText = " " + right + " "
	}
	// One dash after the left corner and one before the right corner leaves
	// inner-2 cells for the labels plus at least one separating dash.
	avail := inner - 2
	if avail < 0 {
		avail = 0
	}
	if ansi.StringWidth(leftText)+ansi.StringWidth(rightText)+1 > avail {
		// Shorten the right label first: it is usually the less important
		// state detail, and dropping it entirely loses useful information.
		// Keep a space between an ellipsis and the right corner.
		room := avail - ansi.StringWidth(leftText) - 1
		if room >= 6 {
			rightText = ansi.Truncate(rightText, room-1, "…") + " "
		} else {
			rightText = ""
		}
	}
	if lw := ansi.StringWidth(leftText); lw > avail-1 {
		leftText = ansi.Truncate(leftText, max(0, avail-1), "…")
	}
	used := ansi.StringWidth(leftText) + ansi.StringWidth(rightText)
	fill := avail - used
	if fill < 1 {
		fill = 1 // only reachable when avail is 0: keep the frame well formed
	}
	if used+fill > avail {
		// avail is 0 or 1: shorten the label further.
		leftText = ansi.Truncate(leftText, max(0, avail-1), "")
		fill = max(0, avail-ansi.StringWidth(leftText)-ansi.StringWidth(rightText))
	}
	return border.Render("╭─") + titleSt.Render(leftText) +
		border.Render(strings.Repeat("─", fill)) + rightSt.Render(rightText) +
		border.Render("─╮")
}

// fitBlock returns exactly height lines of exactly width cells.
func fitBlock(lines []string, height, width int) []string {
	if height <= 0 {
		return nil
	}
	out := make([]string, 0, height)
	for _, line := range lines {
		if len(out) >= height {
			break
		}
		out = append(out, fitWidth(line, width))
	}
	for len(out) < height {
		out = append(out, strings.Repeat(" ", max(0, width)))
	}
	return out
}

// joinBlocks places two already-sized blocks side by side with gap blank
// columns between them. Shorter blocks are padded with blank lines.
func joinBlocks(left, right []string, gap int) []string {
	height := max(len(left), len(right))
	out := make([]string, 0, height)
	for i := 0; i < height; i++ {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out = append(out, l+strings.Repeat(" ", max(0, gap))+r)
	}
	return out
}

// cellText formats text into exactly width cells, truncating with an ellipsis
// when needed. alignRight pads on the left instead of the right.
func cellText(text string, width int, alignRight bool) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(text) > width {
		text = ansi.Truncate(text, width, "…")
	}
	gap := width - ansi.StringWidth(text)
	if gap <= 0 {
		return text
	}
	if alignRight {
		return strings.Repeat(" ", gap) + text
	}
	return text + strings.Repeat(" ", gap)
}

// styleCell is cellText for one styled cell: the text is clipped before the
// style is applied so the padding stays outside the styled span and columns
// keep their alignment.
func styleCell(st lipgloss.Style, text string, width int, alignRight bool) string {
	if width <= 0 {
		return ""
	}
	if ansi.StringWidth(text) > width {
		text = ansi.Truncate(text, width, "…")
	}
	pad := strings.Repeat(" ", max(0, width-ansi.StringWidth(text)))
	if alignRight {
		return pad + st.Render(text)
	}
	return st.Render(text) + pad
}

// sharePercent is the rounded percentage of total represented by part. ok is
// false when there is no meaningful total.
func sharePercent(part, total uint64) (int, bool) {
	if total == 0 {
		return 0, false
	}
	if part > total {
		part = total
	}
	return int(float64(part)*100/float64(total) + 0.5), true
}

// barText returns a fixed-width proportional bar. A non-zero part always
// shows at least one filled cell, and a part smaller than the total never
// fills the whole bar.
func barText(part, total uint64, cells int) string {
	if cells <= 0 {
		return ""
	}
	fill := 0
	if total > 0 {
		share := float64(part) / float64(total)
		fill = int(share*float64(cells) + 0.5)
		if fill == 0 && part > 0 && share >= 0.01 {
			fill = 1
		}
		if fill >= cells && part < total {
			fill = cells - 1
		}
		if fill > cells {
			fill = cells
		}
	}
	return strings.Repeat("█", fill) + strings.Repeat("░", cells-fill)
}

// inputLine draws "Label: value" with an empty-value placeholder. It returns
// exactly width cells. The placeholder is only shown while the field is
// empty, so it never competes with typed text.
func inputLine(th theme.Theme, label, placeholder string, in *lineInput, focused bool, width int) string {
	labelSt := th.DialogText
	if !focused {
		labelSt = th.DialogHint
	}
	labelText := ""
	labelWidth := 0
	if width >= len(label)+8 {
		labelText = label
		labelWidth = len(label)
	}
	fieldWidth := max(2, width-labelWidth)
	if in.Value() == "" && placeholder != "" {
		shown := th.InputCursor.Render(" ") + th.DialogHint.Render(ansi.Truncate(placeholder, max(0, fieldWidth-1), "…"))
		shown = fitWidth(shown, fieldWidth)
		return labelSt.Render(labelText) + shown
	}
	return labelSt.Render(labelText) + in.View(th, fieldWidth)
}

// splitLine places a left and a right label on one width-cell line, dropping
// the right label when the two cannot share the width. Both labels may be
// styled; widths are measured after the escape sequences are ignored.
func splitLine(left, right string, width int) string {
	if width <= 0 {
		return ""
	}
	lw, rw := ansi.StringWidth(left), ansi.StringWidth(right)
	if rw == 0 {
		return fitWidth(left, width)
	}
	if lw+rw+1 > width {
		// Keep as much of the right label as fits; it is usually the more
		// specific detail (a path or a state).
		room := width - lw - 1
		if room < 6 {
			return fitWidth(left, width)
		}
		right = ansi.Truncate(right, room, "…")
		rw = ansi.StringWidth(right)
	}
	gap := width - lw - rw
	return left + strings.Repeat(" ", max(0, gap)) + right
}

// highlightWith applies TwinPane's match style to every occurrence of query
// in s. It is used where text must be highlighted without a *viewer.
func highlightWith(th theme.Theme, s, query string) string {
	if query == "" || s == "" {
		return s
	}
	ranges := textview.MatchRanges(s, query)
	if len(ranges) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, r := range ranges {
		if r[0] < last || r[1] > len(s) {
			continue
		}
		b.WriteString(s[last:r[0]])
		b.WriteString(th.Match.Render(s[r[0]:r[1]]))
		last = r[1]
	}
	b.WriteString(s[last:])
	return b.String()
}
