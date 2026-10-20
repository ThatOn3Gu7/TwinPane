package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/proc"
	"github.com/ThatOn3Gu7/TwinPane/internal/search"
	"github.com/ThatOn3Gu7/TwinPane/internal/textview"
)

type advancedSearchMode uint8

const (
	advancedFilenameMode advancedSearchMode = iota
	advancedContentMode
)

type advancedEventKind uint8

const (
	advancedIndexEvent advancedEventKind = iota
	advancedFilterEvent
	advancedContentEvent
	advancedPreviewEvent
)

type advancedSearchEvent struct {
	kind       advancedEventKind
	index      search.IndexUpdate
	content    search.ContentUpdate
	generation int
	indices    []int
	entryCount int
	keepPath   string
	path       string
	doc        *textview.Doc
	syntax     []string // Chroma-coloured doc lines (filename preview)
	err        error
}

type advancedSearchMsg struct {
	id     int
	event  advancedSearchEvent
	closed bool
}

// advancedSearchView owns search-only state and background work. It never
// changes either panel's path, cursor, or selection; inspection overlays the
// existing viewer while this state remains parked underneath it.
type advancedSearchView struct {
	id         int
	panel      int
	root       string
	showHidden bool
	mode       advancedSearchMode
	input      *lineInput

	entries           []search.Entry
	filenameResults   []int
	filenameQuery     string
	filenameFilterLen int
	indexing          bool
	indexStatus       search.IndexUpdate
	indexCancel       context.CancelFunc
	filtering         bool
	filterDirty       bool
	filterGeneration  int
	filterCancel      context.CancelFunc

	contentResults    []search.ContentResult
	contentStatus     search.ContentUpdate
	contentLoading    bool
	contentGeneration int
	contentCtx        context.Context
	contentCancel     context.CancelFunc
	contentKeepPath   string

	cursor int
	offset int

	previewPath       string
	previewLoading    bool
	previewIsDir      bool
	previewDoc        *textview.Doc
	previewSyntax     []string // Chroma-coloured preview lines, nil = plain text
	previewErr        error
	previewGeneration int
	previewCancel     context.CancelFunc

	ctx     context.Context
	cancel  context.CancelFunc
	events  chan advancedSearchEvent
	started time.Time
}

// beginAdvancedSearchFromQuick is the documented, conflict-free bridge from
// the existing / filename workflow: Ctrl+G opens Advanced Search without
// changing the active panel state, and seeds the filename query.
func (a *App) beginAdvancedSearchFromQuick() tea.Cmd {
	sv := a.search
	if sv == nil {
		return nil
	}
	p := a.panels[sv.panel]
	if p.InRemote() {
		a.setStatusLevel("Advanced Search is local-only; the live filename search remains open", lvlWarn)
		return nil
	}
	root, panelIndex, query, showHidden := sv.root, sv.panel, sv.input.Value(), sv.hidden
	a.stopSearch()
	return a.beginAdvancedSearch(root, panelIndex, query, showHidden)
}

func (a *App) beginAdvancedSearch(root string, panelIndex int, query string, showHidden bool) tea.Cmd {
	if panelIndex < 0 || panelIndex > 1 || root == "" {
		a.setStatusLevel("Advanced Search needs a local folder", lvlWarn)
		return nil
	}
	p := a.panels[panelIndex]
	if p.InArchive() {
		a.setStatusLevel("Advanced Search searches local folders; leave the archive first", lvlWarn)
		return nil
	}
	if p.InRemote() {
		a.setStatusLevel("Advanced Search is local-only", lvlWarn)
		return nil
	}
	if a.advancedSearch != nil {
		a.closeAdvancedSearch()
	}
	a.advancedSeq++
	ctx, cancel := context.WithCancel(context.Background())
	sv := &advancedSearchView{
		id: a.advancedSeq, panel: panelIndex, root: root, showHidden: showHidden,
		mode: advancedFilenameMode, input: newLineInput(query), indexing: true,
		ctx: ctx, cancel: cancel, events: make(chan advancedSearchEvent, 16), started: a.now(),
	}
	a.advancedSearch = sv
	a.showHelp = false
	a.setStatus("", false)
	a.startAdvancedIndex(sv)
	if query != "" {
		a.scheduleFilenameFilter(true, "")
	}
	return sv.listen()
}

func (a *App) startAdvancedIndex(v *advancedSearchView) {
	backends := a.opts.SearchBackends
	if len(backends) == 0 {
		backends = search.DetectBackends(proc.LookPath)
	}
	if len(backends) > 0 {
		v.indexStatus.Backend = backends[0].Name
	}
	workerCtx, cancel := context.WithCancel(v.ctx)
	v.indexCancel = cancel
	updates := make(chan search.IndexUpdate, 4)
	go func() {
		defer close(updates)
		defer func() {
			if r := recover(); r != nil {
				select {
				case updates <- search.IndexUpdate{Done: true, Err: fmt.Errorf("internal error: %v", r)}:
				case <-workerCtx.Done():
				}
			}
		}()
		search.Index(workerCtx, v.root, search.IndexOptions{ShowHidden: v.showHidden, Backends: backends}, updates)
	}()
	go func() {
		for update := range updates {
			if !publishAdvancedEvent(v.ctx, workerCtx, v.events, advancedSearchEvent{kind: advancedIndexEvent, index: update}) {
				return
			}
		}
	}()
}

func publishAdvancedEvent(lifetime, worker context.Context, events chan<- advancedSearchEvent, event advancedSearchEvent) bool {
	select {
	case events <- event:
		return true
	case <-worker.Done():
		return false
	case <-lifetime.Done():
		return false
	}
}

func (v *advancedSearchView) listen() tea.Cmd {
	id, events, ctx := v.id, v.events, v.ctx
	return func() tea.Msg {
		select {
		case event := <-events:
			return advancedSearchMsg{id: id, event: event}
		case <-ctx.Done():
			return advancedSearchMsg{id: id, closed: true}
		}
	}
}

func (a *App) advancedSearchUpdate(msg advancedSearchMsg) tea.Cmd {
	v := a.advancedSearch
	if v == nil || msg.id != v.id || msg.closed {
		return nil
	}
	switch msg.event.kind {
	case advancedIndexEvent:
		u := msg.event.index
		from := len(v.entries)
		v.entries = append(v.entries, u.Entries...)
		v.indexStatus = u
		v.indexStatus.Entries = nil
		v.indexing = !u.Done
		if v.mode == advancedFilenameMode {
			query := v.input.Value()
			if strings.TrimSpace(query) == "" && !v.filtering {
				for i := from; i < len(v.entries); i++ {
					v.filenameResults = append(v.filenameResults, i)
				}
				v.filenameQuery, v.filenameFilterLen = query, len(v.entries)
				if len(v.filenameResults) == len(u.Entries) {
					v.cursor, v.offset = 0, 0
				}
				a.clampAdvancedSearch()
				a.refreshAdvancedPreview()
			} else if v.filtering {
				v.filterDirty = true
			} else {
				a.scheduleFilenameFilter(false, v.currentResultPath())
			}
		}
	case advancedFilterEvent:
		if msg.event.generation != v.filterGeneration {
			return v.listen()
		}
		v.filtering = false
		v.filenameResults = msg.event.indices
		v.filenameFilterLen = msg.event.entryCount
		v.cursor = v.findFilenamePath(msg.event.keepPath)
		a.clampAdvancedSearch()
		if v.filterDirty {
			v.filterDirty = false
			a.scheduleFilenameFilter(false, v.currentResultPath())
		} else {
			a.refreshAdvancedPreview()
		}
	case advancedContentEvent:
		if msg.event.generation != v.contentGeneration {
			return v.listen()
		}
		u := msg.event.content
		oldPath := v.currentResultPath()
		v.contentResults = append(v.contentResults, u.Results...)
		v.contentStatus = u
		v.contentStatus.Results = nil
		v.contentLoading = !u.Done
		if v.contentKeepPath != "" {
			if index, ok := v.findContentPath(v.contentKeepPath); ok {
				v.cursor = index
				v.contentKeepPath = ""
			} else if u.Done {
				v.contentKeepPath = ""
			}
		} else if oldPath != "" {
			if index, ok := v.findContentPath(oldPath); ok {
				v.cursor = index
			} else {
				v.cursor = 0
			}
		}
		a.clampAdvancedSearch()
	case advancedPreviewEvent:
		if msg.event.generation != v.previewGeneration || msg.event.path != v.previewPath {
			return v.listen()
		}
		v.previewLoading = false
		v.previewDoc, v.previewSyntax, v.previewErr = msg.event.doc, msg.event.syntax, msg.event.err
	}
	return v.listen()
}

func (a *App) scheduleFilenameFilter(reset bool, keepPath string) {
	v := a.advancedSearch
	if v == nil {
		return
	}
	query := v.input.Value()
	v.filenameQuery = query
	if reset {
		if v.filterCancel != nil {
			v.filterCancel()
		}
		v.filterGeneration++
		v.filtering, v.filterDirty = false, false
		v.filenameResults = nil
		v.cursor, v.offset = 0, 0
		v.previewPath, v.previewDoc, v.previewErr = "", nil, nil
		v.previewSyntax = nil
		v.previewIsDir, v.previewLoading = false, false
		if v.previewCancel != nil {
			v.previewCancel()
		}
	}
	if v.filtering {
		v.filterDirty = true
		return
	}
	v.filterGeneration++
	generation := v.filterGeneration
	v.filtering = true
	entries := append([]search.Entry(nil), v.entries...)
	workerCtx, cancel := context.WithCancel(v.ctx)
	v.filterCancel = cancel
	go func() {
		indices, complete := search.FuzzyRankContext(workerCtx, entries, query)
		if !complete || workerCtx.Err() != nil {
			return
		}
		publishAdvancedEvent(v.ctx, workerCtx, v.events, advancedSearchEvent{
			kind: advancedFilterEvent, generation: generation, indices: indices, entryCount: len(entries), keepPath: keepPath,
		})
	}()
}

func (a *App) advancedQueryChanged() {
	v := a.advancedSearch
	if v == nil {
		return
	}
	if v.mode == advancedFilenameMode {
		a.scheduleFilenameFilter(true, v.currentResultPath())
		return
	}
	a.startAdvancedContentSearch(v.input.Value(), v.currentResultPath())
}

func (a *App) startAdvancedContentSearch(query, keepPath string) {
	v := a.advancedSearch
	if v == nil {
		return
	}
	if v.contentCancel != nil {
		v.contentCancel()
	}
	v.contentGeneration++
	generation := v.contentGeneration
	v.contentResults = nil
	v.contentStatus = search.ContentUpdate{}
	v.cursor, v.offset = 0, 0
	v.contentKeepPath = keepPath
	v.contentLoading = strings.TrimSpace(query) != ""
	if strings.TrimSpace(query) == "" {
		v.contentKeepPath = ""
		return
	}
	workerCtx, cancel := context.WithCancel(v.ctx)
	v.contentCtx, v.contentCancel = workerCtx, cancel
	updates := make(chan search.ContentUpdate, 4)
	go func() {
		defer close(updates)
		defer func() {
			if r := recover(); r != nil {
				select {
				case updates <- search.ContentUpdate{Done: true, Err: fmt.Errorf("internal error: %v", r)}:
				case <-workerCtx.Done():
				}
			}
		}()
		search.RunContent(workerCtx, v.root, query, search.ContentOptions{}, updates)
	}()
	go func() {
		for update := range updates {
			if !publishAdvancedEvent(v.ctx, workerCtx, v.events, advancedSearchEvent{
				kind: advancedContentEvent, generation: generation, content: update,
			}) {
				return
			}
		}
	}()
}

func (a *App) toggleAdvancedSearchMode() {
	v := a.advancedSearch
	if v == nil {
		return
	}
	query := v.input.Value()
	if v.mode == advancedFilenameMode {
		v.mode = advancedContentMode
		if v.previewCancel != nil {
			v.previewCancel()
		}
		v.previewPath, v.previewDoc, v.previewErr = "", nil, nil
		v.previewSyntax = nil
		v.previewIsDir, v.previewLoading = false, false
		a.startAdvancedContentSearch(query, "")
		a.setStatus("content search · exact .git folders skipped", false)
		return
	}
	if v.contentCancel != nil {
		v.contentCancel()
	}
	v.contentGeneration++ // stale content updates are ignored after the toggle
	v.contentLoading = false
	v.mode = advancedFilenameMode
	if v.filenameQuery != query || v.filenameFilterLen < len(v.entries) {
		a.scheduleFilenameFilter(true, "")
	}
	a.refreshAdvancedPreview()
	a.setStatus("filename search · fuzzy path matching", false)
}

func (v *advancedSearchView) currentResultPath() string {
	if v.cursor < 0 {
		return ""
	}
	if v.mode == advancedFilenameMode {
		if v.cursor >= len(v.filenameResults) {
			return ""
		}
		index := v.filenameResults[v.cursor]
		if index < 0 || index >= len(v.entries) {
			return ""
		}
		return v.entries[index].Path
	}
	if v.cursor >= len(v.contentResults) {
		return ""
	}
	return v.contentResults[v.cursor].Path
}

func (v *advancedSearchView) findFilenamePath(path string) int {
	if path == "" {
		return 0
	}
	for i, index := range v.filenameResults {
		if index >= 0 && index < len(v.entries) && v.entries[index].Path == path {
			return i
		}
	}
	return 0
}

func (v *advancedSearchView) findContentPath(path string) (int, bool) {
	if path == "" {
		return 0, false
	}
	for i, result := range v.contentResults {
		if result.Path == path {
			return i, true
		}
	}
	return 0, false
}

func (a *App) closeAdvancedSearch() {
	v := a.advancedSearch
	if v == nil {
		return
	}
	if v.indexCancel != nil {
		v.indexCancel()
	}
	if v.filterCancel != nil {
		v.filterCancel()
	}
	if v.contentCancel != nil {
		v.contentCancel()
	}
	if v.previewCancel != nil {
		v.previewCancel()
	}
	v.cancel()
	a.advancedSearch = nil
	a.setStatus("", false)
}

func (a *App) advancedSearchKey(msg tea.KeyMsg) tea.Cmd {
	v := a.advancedSearch
	if v == nil {
		return nil
	}
	switch msg.String() {
	case "ctrl+c":
		a.closeAdvancedSearch()
		a.quitting = true
		return tea.Quit
	case "esc":
		a.closeAdvancedSearch()
		return nil
	case "ctrl+t":
		a.toggleAdvancedSearchMode()
		return nil
	case "?", "f1":
		a.openHelp()
		return nil
	case "enter":
		return a.inspectAdvancedResult()
	case "up", "ctrl+p":
		a.moveAdvancedCursor(-1)
		return nil
	case "down", "ctrl+n":
		a.moveAdvancedCursor(1)
		return nil
	case "pgup":
		a.moveAdvancedCursor(-a.advancedSearchResultRows())
		return nil
	case "pgdown":
		a.moveAdvancedCursor(a.advancedSearchResultRows())
		return nil
	case "home":
		a.setAdvancedCursor(0)
		return nil
	case "end":
		a.setAdvancedCursor(a.advancedResultCount() - 1)
		return nil
	}
	if v.input.Update(msg) {
		a.advancedQueryChanged()
	}
	return nil
}

func (a *App) moveAdvancedCursor(delta int) {
	if a.advancedSearch == nil || delta == 0 {
		return
	}
	a.setAdvancedCursor(a.advancedSearch.cursor + delta)
}

func (a *App) setAdvancedCursor(index int) {
	v := a.advancedSearch
	if v == nil {
		return
	}
	v.cursor = index
	a.clampAdvancedSearch()
	a.refreshAdvancedPreview()
}

func (a *App) advancedResultCount() int {
	if a.advancedSearch == nil {
		return 0
	}
	if a.advancedSearch.mode == advancedFilenameMode {
		return len(a.advancedSearch.filenameResults)
	}
	return len(a.advancedSearch.contentResults)
}

func (a *App) clampAdvancedSearch() {
	v := a.advancedSearch
	if v == nil {
		return
	}
	count := a.advancedResultCount()
	if count == 0 {
		v.cursor, v.offset = 0, 0
		return
	}
	v.cursor = max(0, min(v.cursor, count-1))
	rows := a.advancedSearchResultRows()
	if rows < 1 {
		rows = 1
	}
	if v.cursor < v.offset {
		v.offset = v.cursor
	}
	if v.cursor >= v.offset+rows {
		v.offset = v.cursor - rows + 1
	}
	v.offset = max(0, min(v.offset, max(0, count-rows)))
}

func (a *App) refreshAdvancedPreview() {
	v := a.advancedSearch
	if v == nil {
		return
	}
	if v.mode == advancedContentMode {
		if v.previewCancel != nil {
			v.previewCancel()
		}
		v.previewPath, v.previewDoc, v.previewErr = "", nil, nil
		v.previewSyntax = nil
		v.previewIsDir, v.previewLoading = false, false
		return
	}
	path := v.currentResultPath()
	if path == v.previewPath && (v.previewLoading || v.previewDoc != nil || v.previewErr != nil || v.previewIsDir) {
		return
	}
	if v.previewCancel != nil {
		v.previewCancel()
	}
	v.previewGeneration++
	generation := v.previewGeneration
	v.previewPath, v.previewDoc, v.previewErr = path, nil, nil
	v.previewSyntax = nil
	v.previewIsDir, v.previewLoading = false, false
	if path == "" {
		return
	}
	index := v.filenameResults[v.cursor]
	if index >= 0 && index < len(v.entries) {
		entry := &v.entries[index]
		entry.Resolve()
		if entry.IsDir {
			v.previewIsDir = true
			return
		}
	}
	workerCtx, cancel := context.WithCancel(v.ctx)
	v.previewCancel = cancel
	v.previewLoading = true
	go func() {
		doc, err := textview.LoadFile(path, textview.Options{MaxBytes: 256 << 10, MaxLines: 10_000, MaxLineBytes: 8 << 10})
		// Syntax colouring is computed off the UI goroutine with the same
		// bounded document the preview draws, so painting a frame never
		// tokenises a file.
		var syntax []string
		if err == nil && doc != nil && !doc.Binary && len(doc.Lines) > 0 {
			syntax = syntaxHighlightLines(path, doc.Lines)
		}
		publishAdvancedEvent(v.ctx, workerCtx, v.events, advancedSearchEvent{
			kind: advancedPreviewEvent, generation: generation, path: path, doc: doc, syntax: syntax, err: err,
		})
	}()
}

func (a *App) inspectAdvancedResult() tea.Cmd {
	v := a.advancedSearch
	if v == nil {
		return nil
	}
	path, query, line := "", "", 0
	if v.mode == advancedFilenameMode {
		if v.cursor < 0 || v.cursor >= len(v.filenameResults) {
			a.setStatusLevel("No result to inspect", lvlWarn)
			return nil
		}
		index := v.filenameResults[v.cursor]
		if index < 0 || index >= len(v.entries) {
			return nil
		}
		entry := &v.entries[index]
		entry.Resolve()
		if entry.IsDir {
			a.setStatusLevel("Advanced Search inspection opens files only", lvlWarn)
			return nil
		}
		path = entry.Path
	} else {
		if v.cursor < 0 || v.cursor >= len(v.contentResults) {
			a.setStatusLevel("No content match to inspect", lvlWarn)
			return nil
		}
		result := v.contentResults[v.cursor]
		path, query = result.Path, v.input.Value()
		if len(result.Matches) > 0 {
			line = result.Matches[0].Line
		}
	}
	if _, err := os.Lstat(path); err != nil {
		a.setStatusLevel(fmt.Sprintf("%s is no longer available: %s", fsys.SanitizeName(filepath.Base(path)), err), lvlWarn)
		return nil
	}
	return a.openAdvancedViewer(path, query, line)
}

func (a *App) advancedSearchElapsed() time.Duration {
	if a.advancedSearch == nil {
		return 0
	}
	return a.now().Sub(a.advancedSearch.started)
}

func (a *App) advancedSearchMouse(ev tea.MouseEvent) tea.Cmd {
	v := a.advancedSearch
	if v == nil {
		return nil
	}
	if ev.IsWheel() {
		delta := wheelDelta(ev) * wheelStep
		a.moveAdvancedCursor(delta)
		return nil
	}
	if ev.Action != tea.MouseActionPress || ev.Button != tea.MouseButtonLeft {
		return nil
	}
	top, rows := a.advancedSearchGeom()
	row := ev.Y - top
	if row < 0 || row >= rows {
		a.lastClick = clickState{}
		return nil
	}
	index := v.offset + row
	if index >= a.advancedResultCount() {
		return nil
	}
	a.setAdvancedCursor(index)
	if a.doubleTap(areaAdvancedSearch, index) {
		return a.inspectAdvancedResult()
	}
	return nil
}

// advancedSearchGeom returns the screen row of the first result and the
// number of result rows, as drawn by the responsive layout renderer.
func (a *App) advancedSearchGeom() (top, rows int) {
	return a.advancedSearchResultTop() + 1, a.advancedSearchResultRows()
}
