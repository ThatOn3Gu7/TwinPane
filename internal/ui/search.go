package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/panel"
	"github.com/ThatOn3Gu7/TwinPane/internal/proc"
	"github.com/ThatOn3Gu7/TwinPane/internal/scrollbar"
	"github.com/ThatOn3Gu7/TwinPane/internal/search"
)

// searchView is the live filename search box. When it opens, the tree below
// the panel's directory is indexed in a goroutine (search.Index with fd,
// find or the built-in walker) and streamed back as searchMsg; every
// keystroke filters the index in memory, so results appear and update as
// the user types. Esc closes the box and cancels the indexing.
type searchView struct {
	id     int
	panel  int // panel the search was started from (and jumps go to)
	root   string
	hidden bool

	input   *lineInput
	query   search.Query // zero value = empty pattern (everything matches)
	qerr    string       // the pattern cannot be used (keeps the previous results)
	entries []search.Entry
	matches []int // indices into entries, best first
	cursor  int
	offset  int
	marks   map[string]bool // marked result paths (Space), for editing several
	list    bool            // keyboard focus on the result list (else the text field)

	indexing bool
	last     search.IndexUpdate // latest counters (Entries nil)
	cancel   context.CancelFunc
	ch       chan search.IndexUpdate
	started  time.Time
	elapsed  time.Duration
}

type searchMsg struct {
	id     int
	u      search.IndexUpdate
	closed bool
}

var searchSeq int

// beginSearch opens the live search box for the active panel's directory.
func (a *App) beginSearch() tea.Cmd {
	if a.refuseInArchive("Find (it searches real folders)") {
		return nil
	}
	p := a.ActivePanel()
	if p.Path() == "" {
		return nil
	}
	a.stopSearch()
	searchSeq++
	ctx, cancel := context.WithCancel(context.Background())
	sv := &searchView{id: searchSeq, panel: a.active, root: p.Path(), hidden: p.Options().ShowHidden,
		input: newLineInput(""), marks: map[string]bool{}, indexing: true,
		cancel: cancel, ch: make(chan search.IndexUpdate, 4), started: a.now()}
	a.search = sv
	backends := a.opts.SearchBackends
	if backends == nil {
		backends = search.DetectBackends(proc.LookPath)
	}
	sv.last.Backend = backends[0].Name
	ch, root, hidden := sv.ch, sv.root, sv.hidden
	go func() {
		defer func() {
			// Never let a bug in the indexer crash TwinPane with the
			// terminal in raw mode: report it as a failed search instead.
			if r := recover(); r != nil {
				select {
				case ch <- search.IndexUpdate{Done: true, Err: fmt.Errorf("internal error: %v", r)}:
				case <-ctx.Done():
				}
			}
			close(ch)
		}()
		search.Index(ctx, root, search.IndexOptions{ShowHidden: hidden, Backends: backends}, ch)
	}()
	a.setStatus("", false)
	return sv.listen()
}

func (sv *searchView) listen() tea.Cmd {
	id, ch := sv.id, sv.ch
	return func() tea.Msg {
		u, ok := <-ch
		return searchMsg{id: id, u: u, closed: !ok}
	}
}

// stopSearch cancels the indexing and closes the box.
func (a *App) stopSearch() {
	if a.search != nil && a.search.cancel != nil {
		a.search.cancel()
	}
	a.search = nil
}

func (a *App) searchUpdate(m searchMsg) tea.Cmd {
	sv := a.search
	if sv == nil || m.id != sv.id || !sv.indexing {
		return nil // stale: the search was closed or replaced
	}
	if m.closed {
		sv.indexing = false
		return nil
	}
	from := len(sv.entries)
	sv.entries = append(sv.entries, m.u.Entries...)
	sv.last = m.u
	sv.last.Entries = nil
	sv.elapsed = a.now().Sub(sv.started)
	if m.u.Done {
		// final order: shallower first, then by path; re-rank everything
		// and keep the cursor on the same item
		sv.indexing = false
		keep := sv.currentPath()
		search.SortEntries(sv.entries)
		sv.refilter()
		sv.focusPath(keep)
		return nil
	}
	// while indexing, new matches are appended (the list does not jump)
	sv.matches = search.Filter(sv.entries, sv.query, from, sv.matches)
	return sv.listen()
}

func (sv *searchView) refilter() {
	sv.matches = search.Filter(sv.entries, sv.query, 0, sv.matches[:0])
	search.Rank(sv.entries, sv.query, sv.matches)
}

func (sv *searchView) currentPath() string {
	if sv.cursor >= 0 && sv.cursor < len(sv.matches) {
		return sv.entries[sv.matches[sv.cursor]].Path
	}
	return ""
}

func (sv *searchView) focusPath(p string) {
	sv.cursor = 0
	if p == "" {
		return
	}
	for i, m := range sv.matches {
		if sv.entries[m].Path == p {
			sv.cursor = i
			return
		}
	}
}

// queryChanged re-filters after the text field changed.
func (sv *searchView) queryChanged() {
	v := sv.input.Value()
	if strings.TrimSpace(v) == "" {
		sv.query, sv.qerr = search.Query{}, ""
	} else {
		q, err := search.ParseQuery(v)
		if err != nil {
			sv.qerr = err.Error()
			return // keep the previous results
		}
		sv.query, sv.qerr = q, ""
	}
	sv.refilter()
	sv.cursor, sv.offset = 0, 0
}

func (a *App) searchKey(msg tea.KeyMsg) tea.Cmd {
	sv := a.search
	s := msg.String()
	rows := a.searchRows()
	defer func() {
		if a.search == sv {
			sv.clamp(rows)
		}
	}()
	// keys that work the same in the text field and in the list
	switch s {
	case "ctrl+c":
		a.stopSearch()
		a.quitting = true
		return tea.Quit
	case "esc":
		a.stopSearch()
		return nil
	case "ctrl+g":
		return a.beginAdvancedSearchFromQuick()
	case "enter":
		return a.searchJump()
	case "tab", "shift+tab":
		sv.list = !sv.list
		return nil
	case "up", "ctrl+p":
		sv.cursor--
		return nil
	case "down", "ctrl+n":
		sv.cursor++
		return nil
	case "pgup":
		sv.cursor -= rows
		return nil
	case "pgdown":
		sv.cursor += rows
		return nil
	case "f4":
		return a.searchEdit(false)
	}
	if !sv.list {
		if sv.input.Update(msg) {
			sv.queryChanged()
		}
		return nil
	}
	// result list
	if msg.Paste {
		return nil
	}
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 {
		// a burst (key repeat, typeahead): only j/k repeat
		if msg.Alt {
			return nil
		}
		var cmd tea.Cmd
		for i, r := range msg.Runes {
			if i > 0 && (r != 'j' && r != 'k' || a.search != sv || a.dialog != nil) {
				break
			}
			cmd = a.searchKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		}
		return cmd
	}
	switch s {
	case "k":
		sv.cursor--
	case "j":
		sv.cursor++
	case "home", "g":
		sv.cursor = 0
	case "end", "G":
		sv.cursor = len(sv.matches) - 1
	case "right", "l":
		return a.searchJump()
	case " ", "insert":
		if p := sv.currentPath(); p != "" {
			if sv.marks[p] {
				delete(sv.marks, p)
			} else {
				sv.marks[p] = true
			}
			sv.cursor++
		}
	case "e":
		return a.searchEdit(false)
	case "E":
		return a.searchEdit(true)
	case "q":
		a.stopSearch()
	case "/", "i":
		sv.list = false
	case "backspace":
		sv.list = false
		if sv.input.Update(msg) {
			sv.queryChanged()
		}
	default:
		// any other character goes back to the text field
		if msg.Type == tea.KeyRunes && !msg.Alt {
			sv.list = false
			if sv.input.Update(msg) {
				sv.queryChanged()
			}
		}
	}
	return nil
}

func (sv *searchView) clamp(rows int) {
	if sv.cursor >= len(sv.matches) {
		sv.cursor = len(sv.matches) - 1
	}
	if sv.cursor < 0 {
		sv.cursor = 0
	}
	if rows < 1 {
		rows = 1
	}
	if sv.cursor < sv.offset {
		sv.offset = sv.cursor
	}
	if sv.cursor >= sv.offset+rows {
		sv.offset = sv.cursor - rows + 1
	}
}

// searchJump closes the box and shows the highlighted result in its
// directory, with the cursor on it.
func (a *App) searchJump() tea.Cmd {
	sv := a.search
	if len(sv.matches) == 0 {
		return nil
	}
	e := sv.entries[sv.matches[sv.cursor]]
	if _, err := os.Lstat(e.Path); err != nil {
		a.setStatus(fmt.Sprintf("%s no longer exists", fsys.SanitizeName(e.Rel)), true)
		return nil
	}
	a.stopSearch()
	a.active = sv.panel
	p := a.panels[sv.panel]
	a.setStatusLevel("found "+fsys.SanitizeName(e.Rel), lvlOK)
	return p.Load(filepath.Dir(e.Path), filepath.Base(e.Path), panel.LoadNavigate)
}

// searchEdit opens the marked results (or the highlighted one) in the
// default editor, or in a chosen one. The box stays open.
func (a *App) searchEdit(choose bool) tea.Cmd {
	sv := a.search
	var paths []string
	for _, m := range sv.matches {
		if p := sv.entries[m].Path; sv.marks[p] {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		if p := sv.currentPath(); p != "" {
			paths = []string{p}
		}
	}
	if len(paths) == 0 {
		a.setStatus("nothing to edit: no result highlighted", true)
		return nil
	}
	return a.editPaths(paths, choose)
}

// searchHeader returns the lines above the result list.
func (a *App) searchHeader(innerW int) []string {
	sv := a.search
	th := a.theme
	backend := sv.last.Backend
	right := th.TitleText.Render(truncRight(" "+backend+" · Ctrl+G advanced", max(0, innerW)))
	rightWidth := lipgloss.Width(right)
	headWidth := max(0, innerW-rightWidth)
	head := th.HelpTitle.Render(truncRight("Find files by name", headWidth))
	head += strings.Repeat(" ", max(0, innerW-lipgloss.Width(head)-rightWidth)) + right
	where := th.Info.Render(truncLeft("in "+fsys.SanitizeName(sv.root), innerW))

	label := "Name: "
	field := sv.input.View(th, innerW-len(label))
	labelSt := th.DialogText
	if sv.list {
		labelSt = th.DialogHint
	}
	lines := []string{head, where, labelSt.Render(label) + field}

	n, total := len(sv.matches), len(sv.entries)
	var state string
	style := th.Info
	switch {
	case sv.last.Err != nil:
		state, style = "Search failed: "+errText(sv.last.Err), th.Error
	case sv.query.String() == "":
		state = plural(total, "entry")
		if sv.indexing {
			state += " so far, indexing…"
		} else {
			state += fmt.Sprintf(" (%.1fs) · type part of a name", sv.elapsed.Seconds())
		}
	default:
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
	if !sv.hidden {
		state += " · hidden skipped"
	}
	lines = append(lines, style.Render(truncRight(state, innerW)))
	if sv.qerr != "" {
		lines = append(lines, th.StatusErr.Render(truncRight("✗ "+sv.qerr, innerW)))
	}
	if sv.last.Note != "" {
		lines = append(lines, th.StatusWarn.Render(truncRight(sv.last.Note+"; using "+backend, innerW)))
	}
	return lines
}

// searchRows is the number of result rows that fit on screen.
func (a *App) searchRows() int {
	if a.search == nil {
		return 1
	}
	n := a.height - 3 - 2 - len(a.searchHeader(max(10, a.width-4))) // title/status/footer, border, header (keys: hint bar)
	if n < 1 {
		n = 1
	}
	return n
}

func (a *App) searchViewRender(h int) string {
	sv := a.search
	th := a.theme
	innerW := max(10, a.width-4)
	rows := a.searchRows()
	sv.clamp(rows)

	lines := a.searchHeader(innerW)
	n := len(sv.matches)
	for i := sv.offset; i < sv.offset+rows; i++ {
		if i >= n {
			lines = append(lines, strings.Repeat(" ", innerW))
			continue
		}
		e := &sv.entries[sv.matches[i]]
		e.Resolve() // type of the rows on screen (fd/find report names only)
		name := fsys.SanitizeName(e.Rel)
		st := th.File
		switch {
		case e.IsLink:
			name += "@"
			st = th.Link
		case e.IsDir:
			name += "/"
			st = th.Dir
		}
		line := truncLeft(name, innerW)
		line += strings.Repeat(" ", max(0, innerW-lipgloss.Width(line)))
		switch {
		case i == sv.cursor && sv.list:
			st = th.CursorActive
		case i == sv.cursor:
			st = th.CursorInactive
		case sv.marks[e.Path]:
			st = th.SelectedRow(st)
		}
		lines = append(lines, st.Render(line))
	}
	// The keys are listed in the hint bar at the bottom of the screen. A
	// scroll indicator uses the right padding column of the result rows.
	hdr := len(lines) - rows
	cells := scrollbar.Cells(n, rows, sv.offset, rows)
	for i := range lines {
		col := " "
		if j := i - hdr; j >= 0 && j < len(cells) && cells[j] {
			col = th.ScrollThumb.Render(panel.ScrollThumbChar)
		}
		lines[i] = padRight(lines[i], innerW) + col
	}
	box := th.HelpBox.Padding(0, 0, 0, 1).Width(a.width - 2).Render(strings.Join(lines, "\n"))
	return lipgloss.NewStyle().MaxHeight(h).MaxWidth(a.width).Render(box)
}
