package ui

import (
	"context"
	"path/filepath"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/foldersize"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
)

// folderSizeView is an on-demand analysis overlay. It owns only scan results
// and UI navigation state; it never changes either panel's directory, cursor,
// or selection.
type folderSizeView struct {
	id         int
	rootPath   string
	rootLabel  string
	root       *foldersize.Node
	current    *foldersize.Node
	result     *foldersize.Result
	progress   foldersize.Progress
	cancel     context.CancelFunc
	events     chan folderSizeEvent
	loading    bool
	cancelling bool
	restoreRel string
	restoreRow string
	cursor     int
	offset     int
	sortMode   folderSizeSort
	sortAlloc  bool
	rows       []folderSizeRow
	rowsFor    *foldersize.Node
	rowsSort   folderSizeSort
	rowsAlloc  bool
}

type folderSizeSort uint8

const (
	folderSizeBySize folderSizeSort = iota
	folderSizeByName
)

type folderSizeRow struct {
	directory *foldersize.Node
	entry     *foldersize.Entry
}

func (r folderSizeRow) name() string {
	if r.directory != nil {
		return r.directory.Name
	}
	if r.entry != nil {
		return r.entry.Name
	}
	return ""
}

func (r folderSizeRow) path() string {
	if r.directory != nil {
		return r.directory.Path
	}
	if r.entry != nil {
		return r.entry.Path
	}
	return ""
}

func (r folderSizeRow) apparent() (uint64, bool, bool) {
	if r.directory != nil {
		return r.directory.ApparentBytes, r.directory.ApparentKnown, r.directory.Overflow
	}
	if r.entry != nil {
		return r.entry.ApparentBytes, r.entry.ApparentKnown, false
	}
	return 0, false, false
}

func (r folderSizeRow) allocated() (uint64, bool, bool) {
	if r.directory != nil {
		return r.directory.AllocatedBytes, r.directory.AllocatedKnown, r.directory.Overflow
	}
	if r.entry != nil {
		return r.entry.AllocatedBytes, r.entry.AllocatedKnown, false
	}
	return 0, false, false
}

type folderSizeEvent struct {
	id       int
	progress foldersize.Progress
	result   *foldersize.Result
	done     bool
}

type folderSizeEventMsg struct{ event folderSizeEvent }

var folderSizeHelp = []Binding{
	{Help: "A", Desc: "scan the cursor/selected local directory, or active folder; neither panel is changed"},
	{Help: "↑/↓, PgUp/PgDn", Desc: "move through the current directory's largest-first entries"},
	{Help: "Enter / → / l", Desc: "drill into a scanned directory"},
	{Help: "Backspace / ←", Desc: "go to the parent in the analysis"},
	{Help: "s / n", Desc: "sort by largest apparent size / name"},
	{Help: "a", Desc: "sort by allocated size where the platform exposes it"},
	{Help: "r", Desc: "rescan the original root (restore the current subdirectory if possible)"},
	{Help: "Esc", Desc: "cancel an active scan, then close the analysis"},
	{Help: "APPARENT", Desc: "sum of Lstat entry lengths per pathname; symlink lengths included, directory metadata excluded"},
	{Help: "ALLOCATED", Desc: "filesystem block estimate (Unix st_blocks × 512); unavailable on some platforms"},
	{Help: "HARD LINKS", Desc: "counted once per pathname; this can overstate unique physical storage"},
	{Help: "SYMLINKS", Desc: "reported as entries but never followed; special files are never opened"},
	{Help: "MOUNTS", Desc: "nested filesystems are skipped where detected; unknown boundaries fail closed"},
	{Help: "PARTIAL", Desc: "lower-bound totals: errors, cancelled scans and safety limits may omit data"},
	{Help: "LIMITS", Desc: "directory results and largest direct-file rows are cached within bounded limits"},
}

// beginFolderSize selects a local directory without mutating panel state.
// A single explicitly marked directory wins; otherwise a real directory under
// the cursor is analyzed, falling back to the active panel's current folder.
func (a *App) beginFolderSize() tea.Cmd {
	p := a.ActivePanel()
	switch {
	case p.InArchive():
		a.setStatusLevel("Folder-size analysis is local-only; leave the archive first", lvlWarn)
		return nil
	case p.InRemote():
		a.refuseOnRemote("Folder-size analysis")
		return nil
	case p.Loading():
		a.setStatusLevel("Wait for the active panel to finish loading before scanning folder sizes", lvlWarn)
		return nil
	case p.Err() != nil:
		a.setStatusLevel("Refresh the active panel before scanning folder sizes: "+p.Err().Error(), lvlWarn)
		return nil
	case p.Path() == "":
		a.setStatusLevel("The active panel has no local directory to scan", lvlWarn)
		return nil
	case a.busy():
		a.setStatusLevel("Wait for active file operations to finish before scanning folder sizes", lvlWarn)
		return nil
	}

	root, label, reason := folderSizeTarget(p)
	if reason != "" {
		a.setStatusLevel(reason, lvlWarn)
		return nil
	}
	a.folderSizeSeq++
	a.folderSize = &folderSizeView{
		id:        a.folderSizeSeq,
		rootPath:  root,
		rootLabel: label,
		sortMode:  folderSizeBySize,
	}
	a.tasks.showing = false
	a.showHelp = false
	a.setStatusLevel("Scanning folder sizes…", lvlInfo)
	return a.startFolderSizeScan()
}

func folderSizeTarget(p interface {
	Selection() []fsys.Entry
	Current() (fsys.Entry, bool)
	Path() string
}) (path, label, reason string) {
	selected := p.Selection()
	if len(selected) > 0 {
		if len(selected) != 1 {
			return "", "", "Select exactly one directory, or clear the selection to scan the active folder"
		}
		entry := selected[0]
		if entry.StatErr != nil {
			return "", "", "The selected entry has a listing error; refresh the panel before scanning"
		}
		if entry.IsLink && entry.IsDir {
			return "", "", "Folder-size analysis does not follow directory symlinks; choose a real directory"
		}
		if !entry.IsDir {
			return "", "", "Select a directory, or clear the selection to scan the active folder"
		}
		return entry.Path, "selected directory", ""
	}
	if current, ok := p.Current(); ok && !current.IsParent && current.IsDir {
		if current.IsLink {
			return "", "", "Folder-size analysis does not follow directory symlinks; choose a real directory"
		}
		if current.StatErr != nil {
			return "", "", "The cursor entry has a listing error; refresh the panel before scanning"
		}
		return current.Path, "cursor directory", ""
	}
	return p.Path(), "active folder", ""
}

func (a *App) startFolderSizeScan() tea.Cmd {
	v := a.folderSize
	if v == nil {
		return nil
	}
	if v.cancel != nil {
		v.cancel()
	}
	a.folderSizeSeq++
	v.id = a.folderSizeSeq
	id := v.id
	ctx, cancel := context.WithCancel(context.Background())
	v.cancel = cancel
	v.events = make(chan folderSizeEvent, 1)
	v.loading, v.cancelling = true, false
	v.root, v.current, v.result = nil, nil, nil
	v.rows, v.rowsFor = nil, nil
	events := v.events
	rootPath := v.rootPath
	a.setStatusLevel("Scanning folder sizes…", lvlInfo)
	go func() {
		result := foldersize.Scan(ctx, rootPath, foldersize.Options{}, func(progress foldersize.Progress) {
			publishFolderSizeEvent(events, folderSizeEvent{id: id, progress: progress})
		})
		publishFolderSizeEvent(events, folderSizeEvent{id: id, progress: result.Progress, result: &result, done: true})
	}()
	return waitFolderSizeEvent(events)
}

func publishFolderSizeEvent(events chan folderSizeEvent, event folderSizeEvent) {
	select {
	case events <- event:
		return
	default:
	}
	// Coalesce progress rather than allowing a slow UI to back up the scan.
	select {
	case <-events:
	default:
	}
	select {
	case events <- event:
	default:
	}
}

func waitFolderSizeEvent(events <-chan folderSizeEvent) tea.Cmd {
	return func() tea.Msg { return folderSizeEventMsg{event: <-events} }
}

func (a *App) folderSizeEvent(msg folderSizeEventMsg) tea.Cmd {
	v := a.folderSize
	if v == nil || v.id != msg.event.id {
		return nil
	}
	if !msg.event.done {
		v.progress = msg.event.progress
		return waitFolderSizeEvent(v.events)
	}
	v.loading, v.cancelling = false, false
	v.cancel = nil
	result := *msg.event.result
	v.result, v.root = &result, result.Root
	v.progress = result.Progress
	if result.Root != nil {
		v.current = findFolderSizeNode(result.Root, v.restoreRel)
		if v.current == nil {
			v.current = result.Root
		}
		v.rows, v.rowsFor = nil, nil
		v.cursor, v.offset = 0, 0
		if v.restoreRow != "" {
			for i, row := range folderSizeRows(v) {
				if row.path() == v.restoreRow {
					v.cursor = i
					break
				}
			}
		}
	}
	v.restoreRel, v.restoreRow = "", ""
	a.keepFolderSizeCursorVisible()
	switch result.Status {
	case foldersize.StatusComplete:
		a.result(lvlOK, "Folder-size scan complete")
		a.setStatus("", false)
	case foldersize.StatusPartial:
		a.result(lvlWarn, "Folder-size scan is partial", "totals are lower bounds")
		a.setStatus("", false)
	case foldersize.StatusCancelled:
		a.result(lvlWarn, "Folder-size scan cancelled", "accumulated totals are incomplete")
		a.setStatus("", false)
	case foldersize.StatusFailed:
		a.result(lvlErr, "Folder-size scan failed", result.Error)
		a.setStatus("", false)
	}
	return nil
}

func (a *App) refreshFolderSize() tea.Cmd {
	v := a.folderSize
	if v == nil || v.loading {
		return nil
	}
	if v.current != nil {
		if rel, err := filepath.Rel(v.rootPath, v.current.Path); err == nil {
			v.restoreRel = rel
		}
	}
	rows := folderSizeRows(v)
	if v.cursor >= 0 && v.cursor < len(rows) {
		v.restoreRow = rows[v.cursor].path()
	}
	return a.startFolderSizeScan()
}

func findFolderSizeNode(root *foldersize.Node, relative string) *foldersize.Node {
	if root == nil || relative == "" || relative == "." {
		return root
	}
	current := root
	for _, part := range strings.Split(filepath.Clean(relative), string(filepath.Separator)) {
		if part == "" || part == "." {
			continue
		}
		found := false
		for _, child := range current.Children {
			if child.Name == part {
				current, found = child, true
				break
			}
		}
		if !found {
			return nil
		}
	}
	return current
}

func folderSizeRows(v *folderSizeView) []folderSizeRow {
	if v == nil || v.current == nil {
		return nil
	}
	if v.rowsFor == v.current && v.rowsSort == v.sortMode && v.rowsAlloc == v.sortAlloc {
		return v.rows
	}
	leaves := folderSizeLeafEntries(v)
	rows := make([]folderSizeRow, 0, len(v.current.Children)+len(leaves))
	for _, child := range v.current.Children {
		rows = append(rows, folderSizeRow{directory: child})
	}
	for i := range leaves {
		rows = append(rows, folderSizeRow{entry: &leaves[i]})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if v.sortMode == folderSizeByName {
			return folderSizeNameLess(a, b)
		}
		as, ak, _ := a.apparent()
		bs, bk, _ := b.apparent()
		if v.sortAlloc {
			as, ak, _ = a.allocated()
			bs, bk, _ = b.allocated()
		}
		if ak != bk {
			return ak
		}
		if as != bs {
			return as > bs // largest first
		}
		if (a.directory != nil) != (b.directory != nil) {
			return a.directory != nil
		}
		return folderSizeNameLess(a, b)
	})
	v.rows, v.rowsFor, v.rowsSort, v.rowsAlloc = rows, v.current, v.sortMode, v.sortAlloc
	return v.rows
}

func folderSizeLeafEntries(v *folderSizeView) []foldersize.Entry {
	if v == nil || v.current == nil {
		return nil
	}
	if v.sortAlloc {
		return v.current.TopAllocatedEntries
	}
	return v.current.TopEntries
}

func folderSizeNameLess(a, b folderSizeRow) bool {
	an, bn := strings.ToLower(a.name()), strings.ToLower(b.name())
	if an != bn {
		return an < bn
	}
	return a.name() < b.name()
}

func (a *App) folderSizeKey(msg tea.KeyMsg) tea.Cmd {
	v := a.folderSize
	if v == nil {
		return nil
	}
	s := msg.String()
	if v.loading {
		if s == "esc" || s == "ctrl+c" || s == "q" {
			if v.cancelling {
				// The scan checks cancellation between batched reads, but an
				// in-flight filesystem call can itself take time. Let the user
				// dismiss the overlay while the worker winds down.
				a.folderSize = nil
				return nil
			}
			if v.cancel != nil {
				v.cancelling = true
				v.cancel()
				a.setStatusLevel("Stopping folder-size scan…", lvlWarn)
			}
		}
		return nil
	}
	if s == "esc" || s == "ctrl+c" || s == "q" {
		a.folderSize = nil
		a.setStatus("", false)
		return nil
	}
	if s == "?" {
		a.openHelp()
		return nil
	}
	if v.result == nil || v.root == nil {
		if s == "r" {
			return a.refreshFolderSize()
		}
		return nil
	}
	rows := folderSizeRows(v)
	switch {
	case a.keys.Up.Matches(msg):
		v.cursor = max(0, v.cursor-1)
	case a.keys.Down.Matches(msg):
		v.cursor = min(max(0, len(rows)-1), v.cursor+1)
	case a.keys.PageUp.Matches(msg):
		v.cursor = max(0, v.cursor-max(1, a.folderSizeListRows(a.height-3)))
	case a.keys.PageDown.Matches(msg):
		v.cursor = min(max(0, len(rows)-1), v.cursor+max(1, a.folderSizeListRows(a.height-3)))
	case a.keys.Top.Matches(msg):
		v.cursor = 0
	case a.keys.Bottom.Matches(msg):
		v.cursor = max(0, len(rows)-1)
	case a.keys.Open.Matches(msg):
		a.openFolderSizeRow(rows)
	case a.keys.Parent.Matches(msg):
		if v.current.Parent != nil {
			v.current = v.current.Parent
			v.cursor, v.offset = 0, 0
			v.rows, v.rowsFor = nil, nil
		}
	case s == "s":
		v.sortMode, v.sortAlloc = folderSizeBySize, false
		v.rowsFor = nil
		v.cursor, v.offset = 0, 0
	case s == "n":
		v.sortMode, v.sortAlloc = folderSizeByName, false
		v.rowsFor = nil
		v.cursor, v.offset = 0, 0
	case s == "a":
		if !foldersize.AllocatedSizeSupported() {
			a.setStatusLevel("Allocated-size estimates are unavailable on this platform; sort by apparent size instead", lvlWarn)
		} else {
			v.sortMode, v.sortAlloc = folderSizeBySize, true
			v.rowsFor = nil
			v.cursor, v.offset = 0, 0
		}
	case s == "r":
		return a.refreshFolderSize()
	}
	a.keepFolderSizeCursorVisible()
	return nil
}

func (a *App) openFolderSizeRow(rows []folderSizeRow) {
	v := a.folderSize
	if v == nil || v.cursor < 0 || v.cursor >= len(rows) {
		return
	}
	row := rows[v.cursor]
	if row.directory == nil {
		return
	}
	switch row.directory.Status {
	case foldersize.StatusBoundarySkipped:
		a.setStatusLevel(row.directory.FirstError+": "+row.directory.Path, lvlWarn)
		return
	case foldersize.StatusDuplicateSkipped:
		a.setStatusLevel("This directory was skipped because its identity was already scanned", lvlWarn)
		return
	case foldersize.StatusFailed:
		a.setStatusLevel(row.directory.FirstError, lvlErr)
		return
	}
	v.current = row.directory
	v.cursor, v.offset = 0, 0
	v.rows, v.rowsFor = nil, nil
}

func (a *App) keepFolderSizeCursorVisible() {
	v := a.folderSize
	if v == nil {
		return
	}
	rows := folderSizeRows(v)
	if len(rows) == 0 {
		v.cursor, v.offset = 0, 0
		return
	}
	visible := max(1, a.folderSizeListRows(a.height-3))
	v.cursor = max(0, min(v.cursor, len(rows)-1))
	if v.cursor < v.offset {
		v.offset = v.cursor
	}
	if v.cursor >= v.offset+visible {
		v.offset = v.cursor - visible + 1
	}
	v.offset = max(0, min(v.offset, max(0, len(rows)-visible)))
}
