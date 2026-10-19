package panel

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/archive"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/scrollbar"
	"github.com/ThatOn3Gu7/TwinPane/internal/theme"
)

const (
	sizeColW = 8
	timeColW = 12
)

// layout describes which chrome rows fit into the current panel height.
type layout struct {
	innerW, innerH int
	header         bool // column header row
	footer         bool // divider + info + summary rows
	info           bool // info row only (compact mode)
	rows           int  // number of list rows
}

func (p *Panel) layout() layout {
	l := layout{innerW: p.width - 2, innerH: p.height - 2}
	if l.innerW < 1 {
		l.innerW = 1
	}
	switch {
	case l.innerH >= 8:
		l.header, l.footer = true, true
		l.rows = l.innerH - 5 // path, header, divider, info, summary
	case l.innerH >= 3:
		l.info = true
		l.rows = l.innerH - 2 // path, info
	default:
		l.rows = l.innerH - 1 // path only
	}
	if l.rows < 0 {
		l.rows = 0
	}
	return l
}

func (p *Panel) listRows() int { return p.layout().rows }

type columns struct {
	nameW            int
	showSize, showTm bool
}

func makeColumns(innerW int) columns {
	c := columns{showSize: innerW >= 26, showTm: innerW >= 44}
	c.nameW = innerW - 2 // leading mark + trailing space
	if c.showSize {
		c.nameW -= sizeColW + 1
	}
	if c.showTm {
		c.nameW -= timeColW + 1
	}
	if c.nameW < 1 {
		c.nameW = 1
	}
	return c
}

// View renders the panel at its configured size.
func (p *Panel) View(th theme.Theme, active bool) string {
	if p.width < 3 || p.height < 3 {
		return ""
	}
	l := p.layout()
	cols := makeColumns(l.innerW)
	lines := make([]string, 0, l.innerH)

	// Path line
	suffix := ""
	if p.loading {
		suffix = " " + p.spinner()
	}
	pathStyle := th.PathInactive
	if active {
		pathStyle = th.PathActive
	}
	switch {
	case p.rem != nil:
		// On a remote: the remote's name as a badge, then the folder:
		// "[drive:] /Backups".
		loc := p.Location()
		badge := th.CloudBadge.Render(fsys.SanitizeName(loc.Remote) + ":")
		bw := lipgloss.Width(badge)
		disp := "/" + strings.TrimPrefix(loc.Path, "/")
		if bw+6 > l.innerW {
			badge, bw = "", 0
			disp = loc.Remote + ":" + disp
		}
		w := l.innerW - bw
		pathText := " " + fitLeft(fsys.SanitizeName(disp), w-1-ansi.StringWidth(suffix)) + suffix
		lines = append(lines, badge+pathStyle.Render(fit(pathText, w)))
	case p.arc != nil:
		// Inside an archive: its format badge, then "archive ▸ folder".
		badge := th.ArchiveBadge.Render(p.arc.Format.Name())
		bw := lipgloss.Width(badge)
		if bw+4 > l.innerW {
			badge, bw = "", 0
		}
		w := l.innerW - bw
		disp := fsys.SanitizeName(p.arc.Display(p.ArchiveInner()))
		pathText := " " + fitLeft(disp, w-1-ansi.StringWidth(suffix)) + suffix
		lines = append(lines, badge+pathStyle.Render(fit(pathText, w)))
	default:
		badge, bw := "", 0
		if p.localBadge {
			// labelled while the other panel shows a remote
			badge = th.LocalBadge.Render("LOCAL")
			bw = lipgloss.Width(badge)
			if bw+8 > l.innerW {
				badge, bw = "", 0
			}
		}
		w := l.innerW - bw
		pathText := " " + fitLeft(fsys.SanitizeName(p.path), w-1-ansi.StringWidth(suffix)) + suffix
		lines = append(lines, badge+pathStyle.Render(fit(pathText, w)))
	}

	if l.header {
		h := " " + fit("Name", cols.nameW)
		if cols.showSize {
			h += " " + padLeft("Size", sizeColW)
		}
		if cols.showTm {
			h += " " + fit("Modified", timeColW)
		}
		lines = append(lines, th.ColumnHeader.Render(fit(h, l.innerW)))
	}

	lines = append(lines, p.renderRows(th, active, l, cols)...)

	if l.footer {
		lines = append(lines, th.Divider.Render(strings.Repeat("─", l.innerW)))
		lines = append(lines, th.Info.Render(fit(" "+p.infoText(l.innerW-1), l.innerW)))
		lines = append(lines, th.Summary.Render(p.summaryText(l.innerW)))
	} else if l.info {
		lines = append(lines, th.Info.Render(fit(" "+p.infoText(l.innerW-1), l.innerW)))
	}

	return p.frame(th, active, l, lines)
}

// ScrollThumbChar is drawn over the right border where the scroll thumb is.
const ScrollThumbChar = "┃"

// frame draws the panel border around lines (exactly l.innerH lines are
// shown). The border is drawn here rather than by lipgloss so that the
// right edge next to the list rows can double as a zero-width scroll
// indicator: where the thumb is, the border line becomes a heavy, brighter
// "┃". When every entry fits, the border is drawn unchanged.
func (p *Panel) frame(th theme.Theme, active bool, l layout, lines []string) string {
	b := lipgloss.NormalBorder()
	color := th.BorderOff
	thumbSt := th.ScrollThumbOff
	if active {
		b = lipgloss.RoundedBorder()
		color = th.BorderOn
		thumbSt = th.ScrollThumb
	}
	bs := lipgloss.NewStyle().Foreground(color)
	for len(lines) < l.innerH {
		lines = append(lines, strings.Repeat(" ", l.innerW))
	}
	lines = lines[:l.innerH]

	first := p.rowStart(l)
	total, visible, offset := p.ScrollInfo()
	cells := scrollbar.Cells(total, visible, offset, l.rows)

	var sb strings.Builder
	sb.WriteString(bs.Render(b.TopLeft + strings.Repeat(b.Top, l.innerW) + b.TopRight))
	for i, ln := range lines {
		sb.WriteByte('\n')
		right := bs.Render(b.Right)
		if j := i - first; cells != nil && j >= 0 && j < len(cells) && cells[j] {
			right = thumbSt.Render(ScrollThumbChar)
		}
		sb.WriteString(bs.Render(b.Left) + fit(ln, l.innerW) + right)
	}
	sb.WriteByte('\n')
	sb.WriteString(bs.Render(b.BottomLeft + strings.Repeat(b.Bottom, l.innerW) + b.BottomRight))
	return sb.String()
}

// rowStart is the index (within the inner lines) of the first list row.
func (p *Panel) rowStart(l layout) int {
	if l.header {
		return 2 // path, header
	}
	return 1 // path
}

// HitKind says what part of a panel a pointer position is over.
type HitKind int

const (
	HitNone      HitKind = iota // outside the panel
	HitChrome                   // border, path line, header, info/summary
	HitRow                      // a list row showing an entry (Index = entry)
	HitEmpty                    // a list row below the last entry
	HitScrollbar                // the scroll indicator (Index = track cell)
)

// Hit is the result of HitTest.
type Hit struct {
	Kind  HitKind
	Index int // entry index (HitRow) or track cell (HitScrollbar)
	Track int // track length (HitScrollbar)
}

// HitTest maps a position relative to the panel's top-left corner (as
// drawn by View) to what is there. The right border next to the list rows
// is reported as HitScrollbar only while a scroll indicator is shown;
// otherwise it counts as part of the row.
func (p *Panel) HitTest(x, y int) Hit {
	if x < 0 || y < 0 || x >= p.width || y >= p.height || p.width < 3 || p.height < 3 {
		return Hit{Kind: HitNone}
	}
	l := p.layout()
	li := y - 1 // inner line
	first := p.rowStart(l)
	if li < first || li >= first+l.rows || x == 0 {
		return Hit{Kind: HitChrome}
	}
	j := li - first
	total, visible, offset := p.ScrollInfo()
	if x == p.width-1 {
		if _, _, ok := scrollbar.Thumb(total, visible, offset, l.rows); ok {
			return Hit{Kind: HitScrollbar, Index: j, Track: l.rows}
		}
	}
	if i := p.offset + j; i < len(p.entries) {
		return Hit{Kind: HitRow, Index: i}
	}
	return Hit{Kind: HitEmpty}
}

// TrackCell converts a row relative to the panel's top edge into a cell of
// the scroll track (may be out of range; ScrollToTrack clamps).
func (p *Panel) TrackCell(y int) int { return y - 1 - p.rowStart(p.layout()) }

// ScrollToTrack scrolls so that the thumb is centred near track cell
// `cell` (scroll-indicator click / drag).
func (p *Panel) ScrollToTrack(cell int) {
	total, visible, _ := p.ScrollInfo()
	p.ScrollTo(scrollbar.OffsetAt(cell, total, visible, p.listRows()))
}

func (p *Panel) renderRows(th theme.Theme, active bool, l layout, cols columns) []string {
	out := make([]string, 0, l.rows)
	end := p.offset + l.rows
	if end > len(p.entries) {
		end = len(p.entries)
	}
	for i := p.offset; i < end; i++ {
		e := p.entries[i]
		text := p.rowText(e, cols, l.innerW)
		var st lipgloss.Style
		switch {
		case i == p.cursor && active:
			st = th.CursorActive
		case i == p.cursor:
			st = th.CursorInactive
		case e.IsParent || (e.IsDir && !e.IsLink):
			st = th.Dir
		case e.IsLink && e.LinkBroken:
			st = th.Broken
		case e.IsLink:
			st = th.Link
		case e.IsHidden():
			st = th.Hidden
		case e.IsExecutable():
			st = th.Exec
		default:
			st = th.File
		}
		if i != p.cursor && p.IsSelected(e.Name) {
			st = th.SelectedRow(st)
		}
		out = append(out, st.Render(text))
	}

	// Messages below the listing: error or empty directory.
	var msgs []string
	var msgStyle lipgloss.Style
	if p.err != nil {
		msgStyle = th.Error
		for _, w := range wrap("⚠ "+fsys.SanitizeName(p.err.Error()), l.innerW-2) {
			msgs = append(msgs, " "+w)
		}
		if p.rem != nil {
			msgs = append(msgs, " ^L: retry · ^R: sources")
		} else {
			msgs = append(msgs, " Backspace: parent · ^L: retry")
		}
	} else if p.loading && p.rem != nil && p.realCount() == 0 {
		msgStyle = th.Empty
		msgs = append(msgs, " "+p.spinner()+" Loading "+fsys.SanitizeName(p.pendingLabel())+" … (Esc cancels)")
	} else if !p.loading && p.path != "" && p.realCount() == 0 {
		msgStyle = th.Empty
		msg := " (empty directory)"
		switch {
		case p.arc != nil:
			msg = " (empty folder in the archive)"
		case p.rem != nil:
			msg = " (empty folder on " + fsys.SanitizeName(p.rem.Remote.Name) + ":)"
		}
		if p.listing.Hidden > 0 {
			msg = fmt.Sprintf(" (%d hidden — press h to show)", p.listing.Hidden)
		}
		msgs = append(msgs, msg)
	}
	for _, m := range msgs {
		if len(out) >= l.rows {
			break
		}
		out = append(out, msgStyle.Render(fit(m, l.innerW)))
	}
	for len(out) < l.rows {
		out = append(out, strings.Repeat(" ", l.innerW))
	}
	return out
}

func (p *Panel) realCount() int {
	n := 0
	for _, e := range p.entries {
		if !e.IsParent {
			n++
		}
	}
	return n
}

func (p *Panel) rowText(e fsys.Entry, cols columns, innerW int) string {
	mark := " " // selection is shown by the row background (theme.SelectedRow)
	name := fsys.SanitizeName(e.Name)
	switch {
	case e.IsParent:
	case e.IsLink:
		name += "@"
	case e.IsDir:
		name += "/"
	case e.IsExecutable():
		name += "*"
	}
	s := mark + fit(name, cols.nameW)
	if cols.showSize {
		s += " " + padLeft(sizeLabel(e), sizeColW)
	}
	if cols.showTm {
		tm := ""
		if !e.IsParent {
			tm = fsys.FormatTime(e.ModTime, p.now())
		}
		s += " " + fit(tm, timeColW)
	}
	return fit(s, innerW)
}

func sizeLabel(e fsys.Entry) string {
	switch {
	case e.IsParent:
		return "<UP>"
	case e.IsLink && e.LinkBroken:
		return "<BROKEN>"
	case e.IsLink && e.IsDir:
		return "<LINK>"
	case e.IsDir:
		return "<DIR>"
	case e.StatErr != nil:
		return "?"
	default:
		return fsys.HumanSize(e.Size)
	}
}

// spinnerFrames animate a load in progress (braille dots: one cell wide,
// in Termux's and most terminals' default fonts).
var spinnerFrames = [...]string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

func (p *Panel) spinner() string { return spinnerFrames[p.spinFrame%len(spinnerFrames)] }

// loadingText is the info line while a load runs: what is being loaded,
// for how long (from one second on), and whether Esc cancels it. In a
// narrow panel the Esc hint goes first, then the seconds; the spinner and
// the folder name (shortened from the left, so its end stays visible)
// always remain.
func (p *Panel) loadingText(w int) string {
	head := p.spinner() + " Loading "
	secs := ""
	if p.spinFor >= time.Second {
		secs = fmt.Sprintf(" %ds", int(p.spinFor/time.Second))
	}
	tails := []string{" …" + secs, " …"}
	if p.loadCancel != nil {
		tails = append([]string{" …" + secs + " · Esc cancels"}, tails...)
	}
	label := fsys.SanitizeName(p.pendingLabel())
	const minName = 6 // e.g. "…ckups"
	tail := tails[len(tails)-1]
	for _, t := range tails {
		if w-ansi.StringWidth(head)-ansi.StringWidth(t) >= min(minName, ansi.StringWidth(label)) {
			tail = t
			break
		}
	}
	room := w - ansi.StringWidth(head) - ansi.StringWidth(tail)
	if room < 1 {
		return head
	}
	return head + fitLeft(label, room) + tail
}

func (p *Panel) infoText(w int) string {
	// While loading: at once in an empty panel, otherwise after the first
	// spinner tick, so loads that finish at once don't flash the line.
	if p.loading && (len(p.entries) == 0 || p.spinFrame > 0) {
		return p.loadingText(w)
	}
	e, ok := p.Current()
	if !ok {
		return ""
	}
	if e.IsParent {
		return "↑ parent directory"
	}
	if e.StatErr != nil {
		return "error: " + e.StatErr.Error()
	}
	if p.arc != nil {
		return p.archiveInfo(e)
	}
	if p.rem != nil {
		s := "folder"
		if !e.IsDir {
			s = fsys.HumanSize(e.Size)
		}
		if !e.ModTime.IsZero() {
			s += "  " + e.ModTime.Local().Format("2006-01-02 15:04")
		}
		return s + "  · " + p.rem.Remote.Type
	}
	s := e.Mode.String()
	if !e.IsDir || e.IsLink {
		s += "  " + fsys.HumanSize(e.Size)
	}
	if !e.ModTime.IsZero() {
		s += "  " + e.ModTime.Format("2006-01-02 15:04")
	}
	if e.IsLink {
		s += "  → " + fsys.SanitizeName(e.LinkTarget)
		if e.LinkBroken {
			s += " (broken)"
		}
	}
	return s
}

// archiveInfo describes an archive entry for the info line.
func (p *Panel) archiveInfo(e fsys.Entry) string {
	inner, _ := p.arc.InnerOf(e.Path)
	ae, ok := p.arc.Lookup(inner)
	if !ok {
		return ""
	}
	var s string
	switch {
	case ae.IsDir && ae.Implicit:
		s = "folder (implied by its contents)"
	case ae.IsDir:
		s = "folder"
	case ae.Symlink:
		s = "link (never followed or extracted)"
		if ae.LinkTarget != "" {
			s += " → " + fsys.SanitizeName(ae.LinkTarget)
		}
	default:
		if p.arc.Format.Format == archive.FormatTAR {
			s = fsys.HumanSize(int64(ae.Size)) + " (" + ae.MethodName() + ")"
		} else {
			s = fsys.HumanSize(int64(ae.Size)) + " (" + fsys.HumanSize(int64(ae.Packed)) + " packed, " + ae.MethodName() + ")"
		}
	}
	if !ae.Modified.IsZero() {
		s += "  " + ae.Modified.Format("2006-01-02 15:04")
	}
	if ae.Encrypted {
		s += "  encrypted"
	}
	return s
}

func (p *Panel) summaryText(w int) string {
	left := fmt.Sprintf(" %d dirs  %d files", p.listing.Dirs, p.listing.Files)
	if p.arc != nil {
		left = " " + p.arc.Format.Name() + ", read-only ·" + left[1:]
	}
	if p.rem != nil {
		left = " " + p.rem.Remote.Name + ": ·" + left[1:]
	}
	if !p.opts.ShowHidden && p.listing.Hidden > 0 {
		left += fmt.Sprintf("  +%d hidden", p.listing.Hidden)
	}
	if sel := p.Selection(); len(sel) > 0 {
		var total int64
		for _, e := range sel {
			if !e.IsDir {
				total += e.Size
			}
		}
		left += fmt.Sprintf("  %d selected (%s)", len(sel), fsys.HumanSize(total))
	}
	pos := 0
	if len(p.entries) > 0 {
		pos = p.cursor + 1
	}
	// The sort mode and hidden filter are always shown, in shorter forms
	// when the panel is narrow (the selection summary gives way first).
	mode := p.opts.SortLabel()
	full, short := mode, mode
	if p.opts.ShowHidden {
		full += " · hidden shown"
		short += " ·h"
	}
	for _, right := range []string{
		fmt.Sprintf("%s  %d/%d ", full, pos, len(p.entries)),
		fmt.Sprintf("%s  %d/%d ", short, pos, len(p.entries)),
		short + " ",
	} {
		if gap := w - ansi.StringWidth(left) - ansi.StringWidth(right); gap >= 1 {
			return left + strings.Repeat(" ", gap) + right
		}
		if lw := w - ansi.StringWidth(right) - 1; lw >= 12 {
			return fit(left, lw) + " " + right
		}
	}
	return fit(left, w)
}

// ---- text helpers ------------------------------------------------------------

// fit truncates s to exactly w cells (with an ellipsis) or pads it with spaces.
func fit(s string, w int) string {
	if w <= 0 {
		return ""
	}
	sw := ansi.StringWidth(s)
	if sw > w {
		s = ansi.Truncate(s, w, "…")
		sw = ansi.StringWidth(s)
	}
	if sw < w {
		s += strings.Repeat(" ", w-sw)
	}
	return s
}

// fitLeft keeps the tail of s, prefixing "…" when it has to cut.
func fitLeft(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if ansi.StringWidth(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && ansi.StringWidth(string(r))+1 > w {
		r = r[1:]
	}
	return "…" + string(r)
}

func padLeft(s string, w int) string {
	sw := ansi.StringWidth(s)
	if sw >= w {
		return ansi.Truncate(s, w, "")
	}
	return strings.Repeat(" ", w-sw) + s
}

// wrap breaks s into chunks of at most w cells.
func wrap(s string, w int) []string {
	if w < 1 {
		w = 1
	}
	var out []string
	var cur []rune
	for _, r := range s {
		if ansi.StringWidth(string(append(cur, r))) > w {
			out = append(out, string(cur))
			cur = cur[:0]
		}
		cur = append(cur, r)
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}
