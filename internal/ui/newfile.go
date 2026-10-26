package ui

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/fileext"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
)

// New file and New folder (phase 10).
//
// `n` now opens a small menu instead of jumping straight into the folder
// prompt: New file… and New folder…. New folder is the unchanged phase-9
// flow (F7 still opens it directly); New file asks for a name and an
// optional format. Formats are a large predefined catalogue grouped into
// categories and searchable (Ctrl+F in the dialog); any other extension can
// be typed as a custom one. The file is always created empty, and an
// existing file is never replaced.

// newFileState is the state of the New file dialog and its format chooser.
type newFileState struct {
	panelIdx int    // panel whose folder receives the file
	dir      string // absolute local folder
	ext      string // chosen extension ("" = none), e.g. ".md" or a custom one

	dlg     *dialog  // the New file dialog (kept while the chooser is open)
	pick    *dialog  // the open format chooser
	pickExt []string // extension of every chooser row ("" = no format or a heading)
}

// openNewMenu shows the small New menu (n).
func (a *App) openNewMenu() tea.Cmd {
	p := a.ActivePanel()
	fileDetail, fileDisabled := "", false
	switch {
	case p.InArchive():
		fileDetail, fileDisabled = "not inside an archive", true
	case p.InRemote():
		fileDetail, fileDisabled = "local folders only", true
	}
	items := []listItem{
		{label: "New file…", detail: fileDetail, key: "f", disabled: fileDisabled,
			info: "Creates an empty file in the active panel."},
		{label: "New folder…", key: "d",
			info: "Creates a folder in the active panel (same as F7)."},
	}
	d := &dialog{kind: dlgNewMenu, title: "New", width: 44, list: items}
	d.footer = "f file · d folder · ⏎ run · Esc close"
	d.onPick = func(a *App, i int) tea.Cmd {
		a.dialog = nil
		if i == 0 {
			return a.beginNewFile()
		}
		return a.beginMkdir()
	}
	a.dialog = d
	return nil
}

// beginNewFile opens the New file dialog for the active panel.
func (a *App) beginNewFile() tea.Cmd {
	if a.refuseInArchive("Creating files") {
		return nil
	}
	p := a.ActivePanel()
	if p.InRemote() {
		a.setStatus("new files can only be created in local folders — use Ctrl+R → Local files first", true)
		return nil
	}
	if p.Path() == "" || p.Err() != nil {
		a.setStatus("no folder to create the file in", true)
		return nil
	}
	st := &newFileState{panelIdx: a.active, dir: p.Path()}

	d := &dialog{kind: dlgNewFile, title: "New file", width: 68}
	d.body = []dline{
		text("In:"),
		path(st.dir),
		blank(),
		hint("Type a name with or without an extension (notes.md or notes). The chosen format replaces an existing extension instead of adding a second one."),
	}
	st.dlg = d
	d.input, d.inputLabel = newLineInput(""), "File name:"
	d.selectors = []*selector{{
		label: "Format",
		value: func() string {
			if st.ext == "" {
				return "(none)"
			}
			if e, ok := fileext.Find(st.ext); ok {
				return fmt.Sprintf("%s (%s)", e.Name, e.Ext)
			}
			return st.ext + " (custom)"
		},
		note: func() (string, bool) {
			if st.ext == "" {
				return "No format: the name is used exactly as typed.", false
			}
			return fmt.Sprintf("Ctrl+F: search %d formats by category, or type a custom extension.", fileext.Count()), false
		},
		prev: func(a *App) { a.openFormatPicker(st) },
		next: func(a *App) { a.openFormatPicker(st) },
		keys: [2]string{"ctrl+f", "ctrl+f"},
		help: "Ctrl+F",
		hint: "format",
	}}
	// The preview stays under the input (outside the scrolling body), so it
	// is visible even on a short terminal.
	d.note = func() (string, bool) {
		typed := strings.TrimSpace(d.input.Value())
		switch {
		case typed == "" && st.ext == "":
			return "Creates: — (type a file name)", false
		case typed == "":
			return "Creates: — (type a name before " + st.ext + ")", false
		}
		name := fileext.FinalName(typed, st.ext)
		line := "Creates: " + fsys.SanitizeName(name) + " (empty file)"
		// Say so when the format replaced an extension the user typed (not
		// when it simply added the one the name was missing).
		if _, had := fileext.SplitExtension(typed); st.ext != "" && had != "" && !strings.EqualFold(had, st.ext) {
			line += "   (typed " + fsys.SanitizeName(typed) + ")"
		}
		return line, false
	}
	d.onSubmit = func(a *App, v string) tea.Cmd { return a.createNewFile(st, v) }
	d.footer = "Enter create · Ctrl+F format · Esc cancel"
	a.dialog = d
	return nil
}

// openFormatPicker opens the searchable extension list (Ctrl+F or the ◂ ▸
// arrows of the Format row). Choosing a row returns to the New file dialog.
func (a *App) openFormatPicker(st *newFileState) {
	if st.dlg == nil {
		return
	}
	d := &dialog{kind: dlgFormat, title: "Format", width: 54, plainRows: true}
	st.pick = d
	d.filter = newLineInput("")
	d.filterLabel = "Search formats, or type a custom extension:"
	d.onFilter = func(a *App) { st.fillFormatPicker(false) }
	d.footer = "Enter choose · ↑↓ move · Esc back"
	d.onPick = func(a *App, i int) tea.Cmd {
		if i < 0 || i >= len(st.pickExt) {
			return nil
		}
		st.ext = st.pickExt[i]
		st.pick = nil
		a.dialog = st.dlg
		return nil
	}
	d.onEsc = func(a *App) tea.Cmd {
		st.pick = nil
		a.dialog = st.dlg
		return nil
	}
	st.fillFormatPicker(true)
	a.dialog = d
}

// fillFormatPicker rebuilds the chooser rows for the current filter text.
func (st *newFileState) fillFormatPicker(initial bool) {
	d := st.pick
	if d == nil {
		return
	}
	q := strings.TrimSpace(d.filter.Value())
	d.list = []listItem{} // non-nil: the dialog shows listEmpty when nothing matches
	st.pickExt = nil
	add := func(it listItem, ext string) {
		d.list = append(d.list, it)
		st.pickExt = append(st.pickExt, ext)
	}
	if q == "" {
		add(listItem{label: "(none)", detail: "no format — use the name as typed"}, "")
	}
	if q != "" {
		if ext, err := fileext.NormalizeExtension(q); err == nil && ext != "" {
			if _, preset := fileext.Find(ext); !preset {
				add(listItem{label: "Use " + ext, detail: "custom extension"}, ext)
			}
		}
	}
	for _, c := range fileext.Search(q) {
		add(listItem{label: c.Name, header: true, disabled: true}, "")
		for _, e := range c.Extensions {
			detail := e.Name
			if e.Desc != "" {
				detail += " — " + e.Desc
			}
			add(listItem{label: e.Ext, detail: detail}, e.Ext)
		}
	}
	if len(d.list) == 0 {
		d.listEmpty = fmt.Sprintf("No format matches %q. Extensions use letters, digits and . + - _ (for example .xyz); type one to use it as a custom extension.", q)
	} else {
		d.listEmpty = ""
	}
	// Start on the first selectable row, or on the current format.
	d.listCursor = 0
	for i, it := range d.list {
		if !it.disabled {
			d.listCursor = i
			break
		}
	}
	if initial {
		for i, ext := range st.pickExt {
			if ext != "" && strings.EqualFold(ext, st.ext) && !d.list[i].disabled {
				d.listCursor = i
				break
			}
		}
	}
	d.scroll = 0
}

// createNewFile validates the name and creates an empty file. A file that
// already exists is never replaced; filesystem failures stay in the dialog
// with an explanation, so nothing has to be retyped.
func (a *App) createNewFile(st *newFileState, typed string) tea.Cmd {
	d := a.dialog
	typed = strings.TrimSpace(typed)
	if typed == "" {
		d.errLine = "enter a file name"
		return nil
	}
	name := fileext.FinalName(typed, st.ext)
	if err := validateNewFileName(name); err != nil {
		d.errLine = errText(err)
		return nil
	}
	if strings.Trim(typed, ". ") == "" {
		d.errLine = "enter a file name before the extension"
		return nil
	}
	target := filepath.Join(st.dir, name)
	// O_EXCL: create only; an existing file or folder is never replaced.
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		d.errLine = newFileError(name, err)
		return nil
	}
	if err := f.Close(); err != nil {
		// The empty file was created but could not be finished: remove it so
		// the dialog still means "nothing was changed".
		os.Remove(target)
		d.errLine = newFileError(name, err)
		return nil
	}
	a.dialog = nil
	st.dlg = nil
	a.setStatusLevel(fmt.Sprintf("Created file %q", fsys.SanitizeName(name)), lvlOK)
	p := a.panels[st.panelIdx]
	return tea.Batch(p.RefreshFocus(name), a.panels[1-st.panelIdx].Refresh())
}

// validateNewFileName applies the same rules as rename plus a check that the
// name cannot reach outside the dialog's folder.
func validateNewFileName(name string) error {
	if err := ops.ValidateName(name); err != nil {
		return err
	}
	if filepath.Base(name) != name || strings.ContainsAny(name, `/\`) {
		return fmt.Errorf("%w: name cannot contain a path separator", ops.ErrInvalidName)
	}
	return nil
}

// newFileError phrases a creation failure for the dialog.
func newFileError(name string, err error) string {
	switch {
	case errors.Is(err, fs.ErrExist):
		return fmt.Sprintf("%q already exists — nothing was changed", fsys.SanitizeName(name))
	case errors.Is(err, fs.ErrPermission):
		return fmt.Sprintf("cannot create %q: permission denied (this folder is not writable)", fsys.SanitizeName(name))
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Sprintf("cannot create %q: the folder no longer exists", fsys.SanitizeName(name))
	default:
		return fmt.Sprintf("cannot create %q: %s", fsys.SanitizeName(name), errText(err))
	}
}
