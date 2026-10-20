package ui

import (
	"context"
	"fmt"
	pathpkg "path"
	"path/filepath"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/cloud"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/panel"
	"github.com/ThatOn3Gu7/TwinPane/internal/scrollbar"
	"github.com/ThatOn3Gu7/TwinPane/internal/textview"
)

// The built-in read-only viewer (phase 5): v / F3, or Enter on a file. It
// replaces the panels while open (title, status line and hint bar stay) and
// returns to exactly the same panel state when closed, since the panels are
// not touched. Loading is bounded (textview.Defaults) and happens in the
// background; binary files are detected and not shown.

// viewMaxMatches caps the search result list.
const viewMaxMatches = 100_000

type viewer struct {
	seq     int
	name    string // file name (header)
	where   string // folder or archive location (info line)
	path    string // real file path; "" for archive entries (not editable)
	inArc   bool
	loading bool
	doc     *textview.Doc
	err     error

	// remote previews (rclone): read-only, staged privately and removed as
	// soon as the text is in memory (see viewer_remote.go)
	remote     bool
	remoteLoc  cloud.Path
	remoteSize int64
	loadCancel context.CancelFunc

	fromAdvanced  bool // Advanced Search inspection is read-only and returns to its results
	initialQuery  string
	initialLine   int // one-based content result line to select after loading
	syntaxLines   []string
	syntaxLoading bool
	syntaxCancel  context.CancelFunc

	disp    []string // expanded display lines, filled lazily
	dispOK  []bool
	top     int // first line shown
	sub     int // first wrapped row of that line shown
	xoff    int // horizontal scroll (no-wrap mode)
	wrap    bool
	numbers bool

	query   string
	matches []int // line indices containing query
	mcur    int   // current match (index into matches), -1 = none

	rangeLine  int
	rangeQuery string
	rangeCells [][2]int // cell ranges for the last rendered line/chunk

	// geometry of the last frame (for scrolling and the mouse)
	rows, textW, total, visible int
}

type viewLoadedMsg struct {
	seq int
	doc *textview.Doc
	err error
}

type viewSyntaxMsg struct {
	seq   int
	lines []string
}

// openViewer shows the file e of panel p.
func (a *App) openViewer(p *panel.Panel, e fsys.Entry) tea.Cmd {
	if e.IsParent {
		return nil
	}
	if e.IsDir {
		a.setStatus(fsys.SanitizeName(e.Name)+" is a folder: Enter opens it", true)
		return nil
	}
	if p.InRemote() {
		return a.openRemoteViewer(p, e)
	}
	a.opSeq++
	v := &viewer{seq: a.opSeq, name: e.Name, loading: true, wrap: true, numbers: true, mcur: -1}
	var load func() (*textview.Doc, error)
	if p.InArchive() {
		arc := p.Archive()
		inner, ok := arc.InnerOf(e.Path)
		if !ok {
			return nil
		}
		v.inArc = true
		v.where = arc.Display(innerDir(inner))
		load = func() (*textview.Doc, error) {
			rc, ent, err := arc.OpenEntry(inner)
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return textview.Load(rc, int64(ent.Size), textview.Defaults)
		}
	} else {
		v.path = e.Path
		v.where = filepath.Dir(e.Path)
		path := e.Path
		load = func() (*textview.Doc, error) { return textview.LoadFile(path, textview.Defaults) }
	}
	a.viewer = v
	a.setStatus("", false)
	seq := v.seq
	return func() tea.Msg {
		doc, err := load()
		return viewLoadedMsg{seq, doc, err}
	}
}

// openAdvancedViewer is the read-only inspection path from Advanced Search.
// It deliberately leaves both panels and the search view untouched.
func (a *App) openAdvancedViewer(path, query string, line int) tea.Cmd {
	return a.openViewerAt(path, query, line, true)
}

// openViewerAtPath opens the regular viewer on a file at a one-based line,
// seeding the search so the match is highlighted. Advanced Search uses it
// after jumping to a content result: the panels are already on the folder, so
// closing the viewer leaves the user there and its keys are the full set.
func (a *App) openViewerAtPath(path, query string, line int) tea.Cmd {
	return a.openViewerAt(path, query, line, false)
}

// openViewerAt loads one file into a new viewer, optionally read-only and at
// a line (with query highlighted when it is not empty).
func (a *App) openViewerAt(path, query string, line int, readOnly bool) tea.Cmd {
	if path == "" {
		return nil
	}
	a.opSeq++
	v := &viewer{
		seq: a.opSeq, name: filepath.Base(path), where: filepath.Dir(path), path: path,
		loading: true, wrap: true, numbers: true, mcur: -1,
		fromAdvanced: readOnly, initialQuery: query, initialLine: line,
	}
	a.viewer = v
	a.setStatus("", false)
	seq := v.seq
	return func() tea.Msg {
		doc, err := textview.LoadFile(path, textview.Defaults)
		return viewLoadedMsg{seq: seq, doc: doc, err: err}
	}
}

// innerDir is the folder of an archive entry ("" = the archive root).
func innerDir(inner string) string {
	if d := pathpkg.Dir(inner); d != "." && d != "/" {
		return d
	}
	return ""
}

// openViewerFor is v / F3: view the file under the cursor.
func (a *App) openViewerFor(p *panel.Panel) tea.Cmd {
	e, ok := p.Current()
	if !ok || e.IsParent {
		a.setStatus("nothing to view: put the cursor on a file", true)
		return nil
	}
	return a.openViewer(p, e)
}

func (a *App) viewerLoaded(m viewLoadedMsg) tea.Cmd {
	v := a.viewer
	if v == nil || v.seq != m.seq {
		return nil // closed or superseded
	}
	a.setStatus("", false)
	v.loading = false
	v.doc, v.err = m.doc, m.err
	if m.doc == nil {
		return nil
	}
	v.disp = make([]string, len(m.doc.Lines))
	v.dispOK = make([]bool, len(m.doc.Lines))
	if v.initialQuery != "" && !m.doc.Binary {
		v.query = v.initialQuery
		v.matches = textview.Find(m.doc.Lines, v.query, viewMaxMatches)
		v.mcur = -1
		for i, line := range v.matches {
			if line >= max(0, v.initialLine-1) {
				v.mcur = i
				break
			}
		}
		if v.mcur < 0 && len(v.matches) > 0 {
			v.mcur = 0
		}
		if v.mcur >= 0 {
			v.gotoLine(v.matches[v.mcur])
			a.viewerMatchStatus()
		}
	}
	if m.doc.Binary || len(m.doc.Lines) == 0 {
		return nil
	}
	v.syntaxLoading = true
	ctx, cancel := context.WithCancel(context.Background())
	v.syntaxCancel = cancel
	seq, name := v.seq, v.name
	lines := append([]string(nil), m.doc.Lines...)
	return func() tea.Msg {
		colored := syntaxHighlightLines(name, lines)
		if ctx.Err() != nil {
			return nil
		}
		return viewSyntaxMsg{seq: seq, lines: colored}
	}
}

func (a *App) viewerSyntaxLoaded(m viewSyntaxMsg) {
	v := a.viewer
	if v == nil || v.seq != m.seq {
		return // closed or superseded
	}
	v.syntaxLoading = false
	v.syntaxLines = m.lines
}

func (a *App) closeViewer() {
	if a.viewer != nil && a.viewer.syntaxCancel != nil {
		a.viewer.syntaxCancel()
	}
	if a.viewer != nil && a.viewer.loadCancel != nil {
		a.viewer.loadCancel() // stops the rclone read and removes the staging
	}
	a.viewer = nil
	a.setStatus("", false)
}

// showsText reports whether there are lines to scroll through.
func (v *viewer) showsText() bool {
	return !v.loading && v.err == nil && v.doc != nil && !v.doc.Binary && len(v.doc.Lines) > 0
}

func (v *viewer) line(i int) string {
	if !v.dispOK[i] {
		v.disp[i] = textview.Expand(v.doc.Lines[i])
		v.dispOK[i] = true
	}
	return v.disp[i]
}

func (v *viewer) lineRows(i int) int {
	if !v.wrap {
		return 1
	}
	return textview.ChunkCount(v.line(i), v.textW)
}

func (v *viewer) scrollDown(n int) {
	for ; n > 0; n-- {
		if v.sub+1 < v.lineRows(v.top) {
			v.sub++
		} else if v.top+1 < len(v.doc.Lines) {
			v.top, v.sub = v.top+1, 0
		} else {
			break
		}
	}
	v.clampEnd()
}

func (v *viewer) scrollUp(n int) {
	for ; n > 0; n-- {
		if v.sub > 0 {
			v.sub--
		} else if v.top > 0 {
			v.top--
			v.sub = v.lineRows(v.top) - 1
		} else {
			break
		}
	}
}

// endPos is the position that shows the last rows of the document.
func (v *viewer) endPos() (int, int) {
	need := v.rows
	for i := len(v.doc.Lines) - 1; i >= 0; i-- {
		r := v.lineRows(i)
		if r >= need {
			return i, r - need
		}
		need -= r
	}
	return 0, 0
}

// clampEnd keeps the view from scrolling past the end of the document.
func (v *viewer) clampEnd() {
	if v.rows <= 0 {
		return
	}
	if r := v.lineRows(v.top); v.sub >= r {
		v.sub = r - 1
	}
	rows := v.lineRows(v.top) - v.sub
	for i := v.top + 1; i < len(v.doc.Lines) && rows < v.rows; i++ {
		rows += v.lineRows(i)
	}
	if rows < v.rows {
		v.top, v.sub = v.endPos()
	}
}

func (v *viewer) gotoLine(i int) {
	v.top, v.sub = i, 0
	v.scrollUp(min(2, v.rows/3)) // a little context above
	v.clampEnd()
}

// geometry computes the content area for a body of height h.
func (a *App) viewerGeometry(h int) {
	v := a.viewer
	v.rows = max(0, h-2)
	w := a.width - 1 // last column: scroll indicator
	if v.numbers && v.doc != nil {
		w -= len(strconv.Itoa(max(1, len(v.doc.Lines)))) + 1
	}
	v.textW = max(1, w)
}

func (a *App) viewerKey(msg tea.KeyMsg) tea.Cmd {
	v := a.viewer
	s := msg.String()
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 {
		// a burst (key repeat, typeahead): only repeat scrolling keys
		r := msg.Runes[0]
		if r != 'j' && r != 'k' {
			s = string(r)
		} else {
			for range msg.Runes {
				a.viewerKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
			}
			return nil
		}
	}
	switch s {
	case "ctrl+c":
		a.quitting = true
		return tea.Quit
	case "esc", "q", "f3", "f10", "v", "backspace":
		a.closeViewer()
		return nil
	case "?", "f1":
		a.openHelp()
		return nil
	case "e", "f4":
		if v.fromAdvanced {
			a.setStatus("Advanced Search inspection is read-only", true)
			return nil
		}
		if v.remote {
			a.setStatus("remote previews are read-only — copy the file to a local panel (c) to edit it", true)
			return nil
		}
		if v.path == "" {
			a.setStatus("an archive entry cannot be edited: extract it first (x in the panel)", true)
			return nil
		}
		path := v.path
		if v.syntaxCancel != nil {
			v.syntaxCancel()
		}
		a.viewer = nil
		return a.editPaths([]string{path}, false)
	}
	if !v.showsText() {
		return nil
	}
	a.setStatus("", false)
	page := max(1, v.rows-1)
	switch s {
	case "down", "j", "enter":
		v.scrollDown(1)
	case "up", "k":
		v.scrollUp(1)
	case "pgdown", "ctrl+f", " ", "ctrl+d":
		v.scrollDown(page)
	case "pgup", "ctrl+b", "ctrl+u":
		v.scrollUp(page)
	case "home", "g":
		v.top, v.sub, v.xoff = 0, 0, 0
	case "end", "G":
		v.top, v.sub = v.endPos()
	case "right", "l":
		if !v.wrap {
			// never further than needed to show the end of the widest
			// line on screen
			widest := 0
			for i := v.top; i < len(v.doc.Lines) && i < v.top+v.rows; i++ {
				widest = max(widest, ansi.StringWidth(v.line(i)))
			}
			v.xoff = min(v.xoff+max(4, v.textW/2), max(0, widest-v.textW+1))
		}
	case "left", "h":
		if !v.wrap {
			v.xoff = max(0, v.xoff-max(4, v.textW/2))
		}
	case "w":
		v.wrap = !v.wrap
		v.sub, v.xoff = 0, 0
		a.setStatus(map[bool]string{true: "line wrapping on", false: "line wrapping off: ←/→ scroll sideways"}[v.wrap], false)
	case "#":
		v.numbers = !v.numbers
		v.sub = 0
	case "/":
		a.viewerFindDialog()
	case "n":
		a.viewerNextMatch(1)
	case "N":
		a.viewerNextMatch(-1)
	}
	return nil
}

func (a *App) viewerFindDialog() {
	v := a.viewer
	d := &dialog{kind: dlgViewFind, title: "Find in " + fsys.SanitizeName(v.name)}
	d.body = []dline{hint("Case is ignored unless the text has capitals. n / N go to the next / previous line found.")}
	d.input = newLineInput(v.query)
	d.inputLabel = "Find text:"
	d.footer = "Enter find · Esc cancel"
	d.onSubmit = func(a *App, q string) tea.Cmd {
		a.dialog = nil
		v := a.viewer
		if v == nil {
			return nil
		}
		v.query = q
		v.matches, v.mcur = nil, -1
		if q == "" {
			a.setStatus("search cleared", false)
			return nil
		}
		v.matches = textview.Find(v.doc.Lines, q, viewMaxMatches)
		if len(v.matches) == 0 {
			a.setStatus(fmt.Sprintf("%q not found", q), true)
			return nil
		}
		// the first match at or after the top of the view (wrapping around)
		v.mcur = 0
		for i, l := range v.matches {
			if l >= v.top {
				v.mcur = i
				break
			}
		}
		v.gotoLine(v.matches[v.mcur])
		a.viewerMatchStatus()
		return nil
	}
	a.dialog = d
}

func (a *App) viewerNextMatch(dir int) {
	v := a.viewer
	if len(v.matches) == 0 {
		if v.query == "" {
			a.setStatus("nothing to repeat: / finds text", false)
		} else {
			a.setStatus(fmt.Sprintf("%q not found", v.query), true)
		}
		return
	}
	n := len(v.matches)
	v.mcur = ((v.mcur+dir)%n + n) % n
	v.gotoLine(v.matches[v.mcur])
	a.viewerMatchStatus()
}

func (a *App) viewerMatchStatus() {
	v := a.viewer
	more := ""
	if len(v.matches) >= viewMaxMatches {
		more = "+"
	}
	a.setStatus(fmt.Sprintf("%q: line %d — match %d of %d%s (n / N for next / previous)",
		v.query, v.matches[v.mcur]+1, v.mcur+1, len(v.matches), more), false)
}

// viewerPosition is shown in the status line when it has nothing else.
func (a *App) viewerPosition() string {
	v := a.viewer
	if !v.showsText() {
		return v.where
	}
	n := len(v.doc.Lines)
	last := min(n, v.top+max(1, v.visible))
	pct := 100
	if n > 0 && v.total > v.visible {
		pct = v.top * 100 / max(1, n-v.visible)
		pct = min(100, pct)
	}
	s := fmt.Sprintf("lines %d–%d of %d (%d%%)", v.top+1, last, n, pct)
	if !v.wrap && v.xoff > 0 {
		s += fmt.Sprintf(" · column %d", v.xoff+1)
	}
	if v.query != "" && len(v.matches) > 0 {
		s += fmt.Sprintf(" · %d lines match %q", len(v.matches), v.query)
	}
	return s
}

// viewerRender draws the viewer in a body of height h.
func (a *App) viewerRender(h int) string {
	v := a.viewer
	th := a.theme
	a.viewerGeometry(h)
	w := a.width
	out := make([]string, 0, h)

	// header: badges and file name
	badge := th.ViewerBadge.Render("VIEW")
	ro := th.ViewerBadge.Render("READ-ONLY")
	name := " " + fsys.SanitizeName(v.name) + " "
	room := w - lipgloss.Width(badge) - lipgloss.Width(ro)
	if room < 4 {
		ro, room = "", w-lipgloss.Width(badge)
	}
	name = ansi.Truncate(name, max(0, room), "…")
	gap := max(0, room-lipgloss.Width(name))
	out = append(out, fitWidth(badge+th.PathActive.Render(name+strings.Repeat(" ", gap))+ro, w))

	// info line
	out = append(out, th.Summary.Render(fitWidth(" "+a.viewerInfo(), w)))

	if v.rows <= 0 {
		return strings.Join(out[:min(len(out), h)], "\n")
	}
	if !v.showsText() {
		msgs := a.viewerMessage()
		pad := max(0, (v.rows-len(msgs))/2)
		for i := 0; i < v.rows; i++ {
			l := ""
			if j := i - pad; j >= 0 && j < len(msgs) {
				l = msgs[j]
				lw := lipgloss.Width(l)
				if lw < w {
					l = strings.Repeat(" ", (w-lw)/2) + l
				}
			}
			out = append(out, fitWidth(l, w))
		}
		return strings.Join(out, "\n")
	}

	if r := v.lineRows(v.top); v.sub >= r { // after a resize
		v.sub = r - 1
	}
	numW := 0
	if v.numbers {
		numW = len(strconv.Itoa(len(v.doc.Lines)))
	}
	curLine := -1
	if v.mcur >= 0 && v.mcur < len(v.matches) {
		curLine = v.matches[v.mcur]
	}
	var rows []string
	shown := 0
	for i := v.top; i < len(v.doc.Lines) && len(rows) < v.rows; i++ {
		shown++
		var chunks []string
		var chunkStarts []int
		cut := false
		if v.wrap {
			chunks = textview.Chunks(v.line(i), v.textW)
			chunkStarts = make([]int, len(chunks))
			for j := range chunks {
				if j > 0 {
					chunkStarts[j] = chunkStarts[j-1] + ansi.StringWidth(chunks[j-1])
				}
			}
		} else {
			full := v.line(i)
			chunks = []string{textview.Cut(full, v.xoff, v.textW)}
			chunkStarts = []int{v.xoff}
			cut = ansi.StringWidth(full) > v.xoff+v.textW
		}
		start := 0
		if i == v.top {
			start = v.sub
		}
		for j := start; j < len(chunks) && len(rows) < v.rows; j++ {
			var b strings.Builder
			if v.numbers {
				num := strings.Repeat(" ", numW)
				st := th.LineNumber
				if j == 0 {
					num = fmt.Sprintf("%*d", numW, i+1)
				} else {
					num = strings.Repeat(" ", numW-1) + "↪"
				}
				if i == curLine {
					st = th.MatchCurrent
				}
				b.WriteString(st.Render(num) + " ")
			}
			text := chunks[j]
			if cut {
				text = ansi.Truncate(text, v.textW-1, "") // room for the marker
			}
			b.WriteString(a.viewerText(i, text, chunkStarts[j]))
			tw := ansi.StringWidth(text)
			if cut {
				b.WriteString(strings.Repeat(" ", max(0, v.textW-1-tw)) + th.Control.Render("›"))
			} else if tw < v.textW {
				b.WriteString(strings.Repeat(" ", v.textW-tw))
			}
			rows = append(rows, b.String())
		}
	}
	for len(rows) < v.rows {
		rows = append(rows, strings.Repeat(" ", w-1))
	}
	v.total, v.visible = len(v.doc.Lines), shown
	if v.top+shown >= len(v.doc.Lines) && v.top == 0 {
		v.visible = len(v.doc.Lines)
	}
	cells := scrollbar.Cells(v.total, v.visible, v.top, v.rows)
	for i, r := range rows {
		mark := " "
		if cells != nil && cells[i] {
			mark = th.ScrollThumb.Render("┃")
		}
		out = append(out, fitWidth(r, w-1)+mark)
	}
	return strings.Join(out, "\n")
}

// highlight marks the occurrences of q in an (expanded, plain) text.
func (a *App) highlight(s, q string) string {
	if q == "" {
		return s
	}
	rs := textview.MatchRanges(s, q)
	if len(rs) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, r := range rs {
		b.WriteString(s[last:r[0]])
		b.WriteString(a.theme.Match.Render(s[r[0]:r[1]]))
		last = r[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

// viewerInfo is the line under the header: where the file is and what was
// loaded.
func (a *App) viewerInfo() string {
	v := a.viewer
	var parts []string
	if v.fromAdvanced {
		parts = append(parts, "read-only search inspection")
	} else if v.remote {
		// the location is appended below, like for every other document
		parts = append(parts, "read-only remote preview")
	} else if v.path != "" {
		parts = append(parts, "e edits in your external editor")
	} else {
		parts = append(parts, "read-only archive entry")
	}
	if d := v.doc; d != nil && !v.loading && v.err == nil {
		if d.Truncated {
			parts = append(parts, "only the first "+fsys.HumanSize(d.Read)+" shown")
		}
		if d.Size >= 0 {
			parts = append(parts, fsys.HumanSize(d.Size))
		}
		if !d.Binary && d.Read > 0 {
			parts = append(parts, plural(len(d.Lines), "line"), d.Encoding)
			if d.CRLF {
				parts = append(parts, "CRLF")
			}
			if d.Invalid > 0 {
				parts = append(parts, fmt.Sprintf("%d invalid bytes shown as �", d.Invalid))
			}
			if d.LongLines > 0 {
				parts = append(parts, fmt.Sprintf("%d very long lines cut", d.LongLines))
			}
			if !v.wrap {
				parts = append(parts, "no wrap")
			}
			if v.syntaxLoading {
				parts = append(parts, "syntax highlighting…")
			} else if len(v.syntaxLines) > 0 {
				parts = append(parts, "syntax highlighted")
			}
		}
	}
	parts = append(parts, fsys.SanitizeName(v.where))
	return strings.Join(parts, " · ")
}

// viewerMessage explains why no text is shown.
func (a *App) viewerMessage() []string {
	v := a.viewer
	th := a.theme
	switch {
	case v.loading:
		if v.remote {
			return []string{th.Empty.Render("Reading from " + remoteLabel(v) + "…"), th.Summary.Render("Esc cancels the transfer")}
		}
		return []string{th.Empty.Render("Loading…")}
	case v.err != nil:
		return []string{th.Error.Render("Cannot show this file"), th.Summary.Render(errText(v.err))}
	case v.doc.Binary:
		out := []string{th.Empty.Render("This looks like a binary file, so it is not shown."),
			th.Summary.Render("(" + v.doc.BinaryReason + ")")}
		if v.path != "" && !v.fromAdvanced && !v.remote {
			out = append(out, th.Summary.Render("e opens it in your external editor anyway."))
		}
		if v.remote {
			out = append(out, th.Summary.Render("Remote previews are read-only: copy the file to a local panel (c) to work with it."))
		}
		return out
	default:
		out := []string{th.Empty.Render("(empty file)")}
		if v.path != "" && !v.fromAdvanced && !v.remote {
			out = append(out, th.Summary.Render("e opens it in your external editor."))
		}
		if v.remote {
			out = append(out, th.Summary.Render("Remote previews are read-only: copy the file to a local panel (c) to work with it."))
		}
		return out
	}
}

func (a *App) viewerHints() []barItem {
	v := a.viewer
	out := []barItem{item("Esc", "close", "esc")}
	if !v.showsText() {
		if v.path != "" && !v.fromAdvanced && !v.remote && v.err == nil && !v.loading {
			out = append(out, item("e", "edit externally", "e"))
		}
		return out
	}
	out = append(out, item("↑↓", "scroll", ""), item("/", "find", "/"))
	if len(v.matches) > 0 {
		out = append(out, item("n/N", "next/prev", "n"))
	}
	wrap := "no wrap"
	if !v.wrap {
		wrap = "wrap"
		out = append(out, item("←→", "sideways", ""))
	}
	out = append(out, item("w", wrap, "w"))
	if v.path != "" && !v.fromAdvanced {
		out = append(out, item("e", "edit externally", "e"))
	}
	nums := "hide numbers"
	if !v.numbers {
		nums = "line numbers"
	}
	out = append(out, item("PgDn", "page", "pgdown"), item("g/G", "top/end", "g"), item("#", nums, "#"))
	return out
}

// viewerMouse: wheel / swipe scrolls, a tap on the scroll column jumps.
func (a *App) viewerMouse(ev tea.MouseEvent) tea.Cmd {
	v := a.viewer
	if !v.showsText() {
		return nil
	}
	if ev.IsWheel() {
		if d := wheelDelta(ev) * wheelStep; d > 0 {
			v.scrollDown(d)
		} else {
			v.scrollUp(-d)
		}
		return nil
	}
	if ev.Action != tea.MouseActionPress || ev.Button != tea.MouseButtonLeft {
		return nil
	}
	a.lastClick = clickState{}
	if ev.X == a.width-1 && v.total > v.visible {
		a.viewerScrollTrack(ev.Y)
		a.drag = dragState{area: areaViewer, active: true}
	}
	return nil
}

// viewerTop is the screen row of the first content row: title, header, info.
const viewerTop = 3

func (a *App) viewerScrollTrack(y int) {
	v := a.viewer
	if v == nil || !v.showsText() {
		return
	}
	off := scrollbar.OffsetAt(y-viewerTop, v.total, v.visible, v.rows)
	v.top, v.sub = off, 0
	v.clampEnd()
}
