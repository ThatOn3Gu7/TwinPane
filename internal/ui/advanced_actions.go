package ui

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
	"github.com/ThatOn3Gu7/TwinPane/internal/panel"
	"github.com/ThatOn3Gu7/TwinPane/internal/search"
)

// Advanced Search actions.
//
// The overlay is read-only by itself, so acting on a result goes through a
// contextual menu (Ctrl+A) instead of adding single-letter shortcuts: every
// printable key still belongs to the query, and no shortcut can leak into the
// panels, the live filename search, the editor or the dialogs. The menu
// reuses the panel operations (ops.Run / ops.PlanDelete through an opSession,
// the editor, Open with, the platform opener) so confirmations, conflicts,
// cancellation and the background task list behave exactly as elsewhere.

// advancedTarget is the result the actions apply to.
type advancedTarget struct {
	path   string
	rel    string
	name   string
	line   int    // 1-based content match line (0 = not a content result)
	query  string // content query, used to highlight the match after a jump
	isDir  bool
	isLink bool
}

// entry is the fsys.Entry used by the confirmation dialogs.
func (t advancedTarget) entry() fsys.Entry {
	return fsys.Entry{Name: t.name, Path: t.path, IsDir: t.isDir, IsLink: t.isLink}
}

// parent is the folder Jump to location reveals.
func (t advancedTarget) parent() string { return filepath.Dir(t.path) }

// advancedSelectedTarget resolves the highlighted result. It reports why
// nothing can be done (no results, unreadable entry) through the status line.
func (a *App) advancedSelectedTarget() (advancedTarget, bool) {
	v := a.advancedSearch
	if v == nil {
		return advancedTarget{}, false
	}
	if v.mode == advancedContentMode {
		if v.cursor < 0 || v.cursor >= len(v.contentResults) {
			a.setStatusLevel("No content match selected", lvlWarn)
			return advancedTarget{}, false
		}
		r := v.contentResults[v.cursor]
		t := advancedTarget{path: r.Path, rel: r.Rel, name: filepath.Base(r.Path), query: v.input.Value()}
		if len(r.Matches) > 0 {
			t.line = r.Matches[0].Line // the match the preview shows first
		}
		return t, true
	}
	if v.cursor < 0 || v.cursor >= len(v.filenameResults) {
		a.setStatusLevel("No result selected", lvlWarn)
		return advancedTarget{}, false
	}
	index := v.filenameResults[v.cursor]
	if index < 0 || index >= len(v.entries) {
		return advancedTarget{}, false
	}
	e := &v.entries[index]
	e.Resolve()
	return advancedTarget{
		path: e.Path, rel: e.Rel, name: filepath.Base(e.Path),
		isDir: e.IsDir, isLink: e.IsLink,
	}, true
}

// advancedActionsTitle names the menu target, including the content match
// line when there is one.
func advancedActionsTitle(t advancedTarget) string {
	name := fsys.SanitizeName(t.name)
	if t.line > 0 {
		name += ":" + fmt.Sprint(t.line)
	}
	return "Actions · " + name
}

// openAdvancedActions shows the contextual menu (Ctrl+A). It is the only new
// Advanced Search shortcut; the menu letters are only active while it is
// open.
func (a *App) openAdvancedActions() tea.Cmd {
	t, ok := a.advancedSelectedTarget()
	if !ok {
		return nil
	}
	entries := a.advancedActionEntries(t)
	d := &dialog{kind: dlgMenu, title: advancedActionsTitle(t), width: 52}
	for _, m := range entries {
		d.list = append(d.list, listItem{label: m.label, detail: m.detail, key: m.key, danger: m.danger})
	}
	d.footer = "⏎ or letter: run · Esc close"
	d.onEsc = func(a *App) tea.Cmd {
		a.dialog = nil
		return nil // the results underneath are untouched
	}
	d.onPick = func(app *App, i int) tea.Cmd {
		if i < 0 || i >= len(entries) {
			return nil
		}
		m := entries[i]
		app.dialog = nil
		if m.run == nil {
			return nil
		}
		return m.run()
	}
	a.dialog = d
	return nil
}

// advancedActionEntries builds the menu for one result. The letters avoid
// q, l, j, k, g and G, which move the menu cursor or close it, so an entry
// can always be picked by its own letter. Delete is last and in the danger
// colour, exactly like the panel menu.
func (a *App) advancedActionEntries(t advancedTarget) []menuEntry {
	var out []menuEntry
	add := func(m menuEntry) { out = append(out, m) }
	name := fsys.SanitizeName(t.name)

	if t.isDir {
		// Enter already opens a folder; the menu offers the same plus the
		// operations that work on folders.
		add(menuEntry{label: "Open folder", detail: "show its contents in the panel", key: "o",
			run: func() tea.Cmd { return a.advancedEnterDirectory(t) }})
	} else {
		// Inspect is what Enter does; listing it keeps the menu the complete
		// list of actions and shows what Enter will do here.
		add(menuEntry{label: "Inspect read-only", detail: a.advancedInspectDetail(t), key: "i",
			run: func() tea.Cmd { return a.inspectAdvancedResult() }})
		add(menuEntry{label: "Open with default app", key: "o",
			run: func() tea.Cmd { return a.openExternalEntry(t.entry()) }})
	}
	jumpDetail := shortPath(t.parent())
	if t.line > 0 {
		jumpDetail = fmt.Sprintf("line %d in %s", t.line, shortPath(t.parent()))
	}
	add(menuEntry{label: "Jump to location", detail: jumpDetail, key: "t",
		run: func() tea.Cmd { return a.advancedJump(t, false) }})
	if !t.isDir {
		add(menuEntry{label: "Edit", key: "e",
			run: func() tea.Cmd { return a.editPaths([]string{t.path}, false) }})
		add(menuEntry{label: "Open with…", key: "O",
			run: func() tea.Cmd { return a.advancedOpenWith(t) }})
	}
	dest := a.advancedDestination()
	add(menuEntry{label: "Copy to other panel", detail: dest, key: "c",
		run: func() tea.Cmd { return a.advancedTransfer(t, ops.Copy) }})
	add(menuEntry{label: "Move to other panel", detail: dest, key: "m",
		run: func() tea.Cmd { return a.advancedTransfer(t, ops.Move) }})
	add(menuEntry{label: "Rename…", detail: name, key: "r",
		run: func() tea.Cmd { return a.renameAdvancedResult(t) }})
	add(menuEntry{label: "Copy path", detail: "to the terminal clipboard", key: "y",
		run: func() tea.Cmd { return a.copyAdvancedPath(t) }})
	add(menuEntry{label: "Delete…", key: "d", danger: true,
		run: func() tea.Cmd { return a.deleteAdvancedResult(t) }})
	return out
}

// advancedInspectDetail describes what Enter shows for a result.
func (a *App) advancedInspectDetail(t advancedTarget) string {
	if t.line > 0 {
		return fmt.Sprintf("open at line %d", t.line)
	}
	return "read-only viewer"
}

// advancedDestination is the folder copy/move use: the other panel, exactly
// like c and m in the panels.
func (a *App) advancedDestination() string {
	p := a.panels[1-a.active]
	if p == nil {
		return ""
	}
	if p.InArchive() || p.InRemote() {
		return "unavailable"
	}
	return shortPath(p.Path())
}

// advancedJump shows the result in the main interface: the panel the search
// started from navigates to the result's folder with the cursor on it, and
// the overlay closes. A content match also opens in the viewer on its line,
// so the match stays in view; the viewer is the regular one, so editing and
// navigation work from there and Esc leaves the panels on the folder.
// With into set (Enter on a folder) the folder itself is opened.
func (a *App) advancedJump(t advancedTarget, into bool) tea.Cmd {
	v := a.advancedSearch
	if v == nil {
		return nil
	}
	info, err := os.Lstat(t.path)
	if err != nil {
		a.setStatusLevel(fmt.Sprintf("%s is no longer available: %s", fsys.SanitizeName(t.name), errText(err)), lvlWarn)
		return nil
	}
	index := v.panel
	if index < 0 || index >= len(a.panels) || a.panels[index] == nil {
		index = a.active
	}
	a.closeAdvancedSearch()
	a.active = index
	p := a.panels[index]
	dir, focus := t.parent(), t.name
	if into && info.IsDir() {
		dir, focus = t.path, ""
	}
	note := "found " + fsys.SanitizeName(t.rel)
	if t.line > 0 {
		note += fmt.Sprintf(" (line %d)", t.line)
	}
	a.setStatusLevel(note, lvlOK)
	var load tea.Cmd
	if p.InArchive() || p.InRemote() {
		load = p.GoLocal(dir, focus)
	} else {
		load = p.Load(dir, focus, panel.LoadNavigate)
	}
	if t.line > 0 && !info.IsDir() {
		return tea.Batch(load, a.openViewerAtPath(t.path, t.query, t.line))
	}
	return load
}

// advancedEnterDirectory opens the highlighted folder in the main interface.
func (a *App) advancedEnterDirectory(t advancedTarget) tea.Cmd {
	return a.advancedJump(t, true)
}

// advancedOpenWith shows the Open-with list for the result, using the same
// choices (and archive handling) as O in the panels.
func (a *App) advancedOpenWith(t advancedTarget) tea.Cmd {
	if t.isDir {
		a.setStatusLevel("Open with is for files", lvlWarn)
		return nil
	}
	p := a.panels[a.active]
	if p == nil {
		return nil
	}
	return a.openWithEntry(p, t.entry())
}

// advancedTransfer copies or moves the result to the other panel through the
// regular transfer session: validation, the confirmation dialog, conflict
// questions, progress, cancellation and the task list are the existing ones.
func (a *App) advancedTransfer(t advancedTarget, kind ops.Kind) tea.Cmd {
	what := map[ops.Kind]string{ops.Copy: "copy", ops.Move: "move"}[kind]
	p := a.panels[1-a.active]
	if p == nil || p.InArchive() || p.InRemote() {
		a.setStatusLevel(fmt.Sprintf("cannot %s: the other panel is not a local folder", what), lvlWarn)
		return nil
	}
	if _, err := os.Lstat(t.path); err != nil {
		a.setStatusLevel(fmt.Sprintf("%s is no longer available: %s", fsys.SanitizeName(t.name), errText(err)), lvlWarn)
		return nil
	}
	dest := filepath.Clean(p.Path())
	if dest == t.parent() && kind == ops.Move {
		a.setStatusLevel("source and destination are the same folder", lvlWarn)
		return nil
	}
	paths := []string{t.path}
	req := ops.Request{Kind: kind, Sources: paths, DestDir: dest}
	s, _ := a.newSession("Checking "+kind.String(), -1, paths)
	s.dstPanel = 1 - a.active
	s.kind, s.to = kind, dest
	s.fromLabel = t.parent()
	s.state = taskPlanning
	s.prog = ops.Progress{Phase: "validating", TotalItems: 1}
	if kind == ops.Move {
		// A completed move removes the source: drop it from the results.
		s.after = func(app *App) { app.pruneAdvancedResults(paths) }
	}
	a.setStatus("Checking "+what+" paths…", false)
	targets := []fsys.Entry{t.entry()}
	go func() {
		err := ops.Validate(req)
		est := ops.EstimateTransfer(req)
		s.events <- opTransferValidatedMsg{s.id, req, targets, est, err}
	}()
	return a.startCmds(s)
}

// renameAdvancedResult renames the result in place. The search list keeps the
// renamed entry (the query and the other results stay as they are).
func (a *App) renameAdvancedResult(t advancedTarget) tea.Cmd {
	dir, old := t.parent(), t.name
	in := a.newInput(old)
	if ext := filepath.Ext(old); !t.isDir && ext != "" && ext != old {
		in.SetCursor(len([]rune(old)) - len([]rune(ext))) // cursor before the extension
	}
	d := &dialog{kind: dlgRename, title: "Rename"}
	d.body = []dline{text("In folder:"), path(dir), blank(), text("Current name: " + old)}
	if t.line > 0 {
		d.body = append(d.body, blank(), hint(fmt.Sprintf("The content match stays selected; it moves with the file (line %d).", t.line)))
	}
	d.input, d.inputLabel = in, "New name:"
	d.footer = "Enter rename · Esc cancel"
	d.onSubmit = func(a *App, v string) tea.Cmd {
		if v == old {
			a.dialog = nil
			a.setStatus("name unchanged", false)
			return nil
		}
		if err := ops.Rename(dir, old, v); err != nil {
			a.dialog.errLine = errText(err)
			return nil
		}
		a.dialog = nil
		newPath := filepath.Join(dir, v)
		a.advancedResultRenamed(t.path, newPath)
		a.refreshAdvancedPreview() // the new name may change the syntax highlighting
		a.result(lvlOK, "Rename completed", fmt.Sprintf("Renamed %q to %q", old, v), "in "+fsys.SanitizeName(t.parent()))
		return tea.Batch(a.refreshPanelsAt(dir, v), a.otherPanelRefresh())
	}
	a.dialog = d
	return nil
}

// otherPanelRefresh refreshes the inactive panel after a rename, matching
// renameOne's behaviour for the panel the file is not in.
func (a *App) otherPanelRefresh() tea.Cmd {
	p := a.panels[1-a.active]
	if p == nil {
		return nil
	}
	return p.Refresh()
}

// deleteAdvancedResult deletes the result through the regular delete session:
// the plan, the confirmation (including the typed confirmation for permanent
// deletes), conflicts, progress and cancellation are unchanged.
func (a *App) deleteAdvancedResult(t advancedTarget) tea.Cmd {
	if _, err := os.Lstat(t.path); err != nil {
		a.setStatusLevel(fmt.Sprintf("%s is no longer available: %s", fsys.SanitizeName(t.name), errText(err)), lvlWarn)
		return nil
	}
	paths := []string{t.path}
	s, ctx := a.newSession("Checking delete", -1, paths)
	s.isDelete = true
	s.fromLabel = t.parent()
	s.state = taskPlanning
	s.prog = ops.Progress{Phase: "scanning", TotalItems: 1}
	s.after = func(app *App) { app.pruneAdvancedResults(paths) }
	a.setStatus("Checking what will be deleted…", false)
	tr := a.trasher
	go func() {
		plan, err := ops.PlanDelete(ctx, paths, tr)
		s.events <- deletePlannedMsg{s.id, plan, err}
	}()
	return a.startCmds(s)
}

// copyAdvancedPath puts the absolute path on the terminal clipboard.
func (a *App) copyAdvancedPath(t advancedTarget) tea.Cmd {
	if err := copyToClipboard(t.path); err != nil {
		a.noteResult(lvlErr, "could not copy the path: "+errText(err),
			"Not copied", "could not copy the path: "+errText(err))
		return nil
	}
	a.noteResult(lvlOK, "copied "+trimClipboardPath(t.path)+" to the "+clipboardNote,
		"Path copied", "copied "+trimClipboardPath(t.path)+" to the "+clipboardNote)
	return nil
}

// ---- keeping the results in sync ---------------------------------------------

// pruneAdvancedResults drops results whose paths no longer exist, so a
// deleted or moved entry does not stay in the list. It only looks at the
// affected paths, never at the whole index.
func (a *App) pruneAdvancedResults(paths []string) {
	v := a.advancedSearch
	if v == nil || len(paths) == 0 {
		return
	}
	gone := map[string]bool{}
	for _, p := range paths {
		if _, err := os.Lstat(p); err != nil {
			gone[filepath.Clean(p)] = true
		}
	}
	if len(gone) == 0 {
		return
	}

	if len(v.contentResults) > 0 {
		kept := v.contentResults[:0]
		for _, r := range v.contentResults {
			if !gone[filepath.Clean(r.Path)] {
				kept = append(kept, r)
			}
		}
		v.contentResults = kept
	}

	// Filename results index into v.entries, so removing an entry means
	// rebuilding both slices with a remap.
	remap := make([]int, len(v.entries))
	entries := make([]search.Entry, 0, len(v.entries))
	for i, e := range v.entries {
		if gone[filepath.Clean(e.Path)] {
			remap[i] = -1
			continue
		}
		remap[i] = len(entries)
		entries = append(entries, e)
	}
	if len(entries) != len(v.entries) {
		results := make([]int, 0, len(v.filenameResults))
		for _, index := range v.filenameResults {
			if index >= 0 && index < len(remap) && remap[index] >= 0 {
				results = append(results, remap[index])
			}
		}
		v.entries = entries
		v.filenameResults = results
		v.filenameFilterLen = len(v.entries)
	}

	if v.previewPath != "" {
		if _, err := os.Lstat(v.previewPath); err != nil {
			v.previewPath, v.previewDoc, v.previewErr = "", nil, nil
			v.previewSyntax = nil
			v.previewIsDir, v.previewLoading = false, false
		}
	}
	a.clampAdvancedSearch()
	a.refreshAdvancedPreview()
}

// advancedResultRenamed moves the recorded path of one result after a rename.
func (a *App) advancedResultRenamed(oldPath, newPath string) {
	v := a.advancedSearch
	if v == nil {
		return
	}
	oldPath, newPath = filepath.Clean(oldPath), filepath.Clean(newPath)
	rel := ""
	if v.root != "" {
		if r, err := filepath.Rel(v.root, newPath); err == nil {
			rel = r
		}
	}
	for i := range v.entries {
		if filepath.Clean(v.entries[i].Path) != oldPath {
			continue
		}
		v.entries[i].Path = newPath
		if rel != "" {
			v.entries[i].Rel = rel
		}
	}
	for i := range v.contentResults {
		if filepath.Clean(v.contentResults[i].Path) != oldPath {
			continue
		}
		v.contentResults[i].Path = newPath
		if rel != "" {
			v.contentResults[i].Rel = rel
		}
	}
	if v.previewPath != "" && filepath.Clean(v.previewPath) == oldPath {
		v.previewPath = newPath
	}
}
