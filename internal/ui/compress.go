package ui

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/compress"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
)

// The compress dialog (z, or Compress in the context menu) creates an
// archive or a compressed file of the targets in any format of package
// compress. It is the phase-5 "create a ZIP" dialog with two selector rows
// added, so with the defaults (ZIP, normal level, the other panel's folder)
// z + Enter works exactly as before:
//
//	Format  ◂ TAR.GZ (.tar.gz) ▸   ↑↓       (all formats; unavailable ones say why)
//	Level   ◂ normal ▸             PgUp/PgDn (only formats with levels)
//	Archive file: [/other/panel/name.tar.gz]
//
// Changing the format swaps the file name's extension; a name that ends in
// another format's extension is refused rather than silently corrected,
// and a format that cannot be used (7-Zip missing, a single-file format
// with several items or a folder) is refused with the reason — never
// replaced by another format.

type opZipDoneMsg struct {
	id  int
	res compress.Result
}

// compressState is the state of an open compress dialog.
type compressState struct {
	caps   []compress.Capability
	sel    compress.Selection
	idx    int    // current format (index into caps)
	outDir string // default destination folder
	base   string // default name for archive formats (without extension)
	single string // full file name, for single-file formats ("" = not one file)
	auto   string // the path value set automatically (not edited by the user)
}

func (st *compressState) cur() compress.Capability { return st.caps[st.idx] }

// defaultPath is the suggested output for format f.
func (st *compressState) defaultPath(f compress.Format) string {
	name := st.base
	if f.Kind == compress.Single && st.single != "" {
		name = st.single
	}
	return filepath.Join(st.outDir, name+f.Ext)
}

// beginZip opens the compress dialog for the targets of the active panel.
func (a *App) beginZip() tea.Cmd {
	p := a.ActivePanel()
	if a.refuseInArchive("Compressing archive entries") {
		return nil
	}
	targets := p.Targets()
	if len(targets) == 0 {
		a.setStatus("nothing to compress: put the cursor on an item or select items with Space", true)
		return nil
	}
	other := a.panels[1-a.active]
	outDir := other.Path()
	if other.InArchive() || other.InRemote() || other.Err() != nil || outDir == "" {
		outDir = p.Path()
	}
	st := &compressState{caps: compress.Detect(a.lookPath), outDir: outDir}
	st.sel.Items = len(targets)
	st.base = filepath.Base(p.Path())
	if len(targets) == 1 {
		st.base = targets[0].Name
		if !targets[0].IsDir {
			st.base = stem(st.base)
			if targets[0].Mode.IsRegular() {
				st.sel.SingleFile = true
				st.single = targets[0].Name
			}
		}
	}
	if st.base == "" || st.base == string(filepath.Separator) || st.base == "." {
		st.base = "archive"
	}
	// the format chosen last time, if it can be used for these items
	for i, c := range st.caps {
		if c.ID == compress.ZIP {
			st.idx = i
		}
	}
	if c, ok := compress.Find(st.caps, a.lastFormat); ok && c.Check(st.sel) == nil {
		for i := range st.caps {
			if st.caps[i].ID == c.ID {
				st.idx = i
			}
		}
	}

	d := &dialog{kind: dlgCompress, title: "Compress " + describe(targets)}
	d.body = []dline{text("Add:")}
	d.body = append(d.body, nameList(targets)...)
	d.body = append(d.body, blank(),
		hint("Folders are added with all their contents; symbolic links are stored as links. The new file is never added to itself, and an existing file is not replaced without asking."))
	st.auto = st.defaultPath(st.cur().Format)
	d.input = newLineInput(st.auto)
	a.compressLabel(d, st)
	d.footer = "Enter create · Esc cancel · ↑↓ format · PgUp/PgDn level"
	d.selectors = []*selector{
		{
			label: "Format",
			value: func() string {
				c := st.cur()
				v := fmt.Sprintf("%s (%s)", c.Name, c.Ext)
				switch {
				case !c.Available:
					v += " — unavailable"
				case c.Check(st.sel) != nil:
					v += " — one file only"
				}
				return v
			},
			note: func() (string, bool) {
				c := st.cur()
				if err := c.Check(st.sel); err != nil {
					if !c.Available {
						return c.Reason, true
					}
					return err.Error(), true
				}
				about := c.About
				if c.External() {
					about += " (" + c.Backend + ")"
				}
				return fmt.Sprintf("%d/%d · %s", st.idx+1, len(st.caps), about), false
			},
			prev: func(a *App) { a.compressFormat(d, st, -1) },
			next: func(a *App) { a.compressFormat(d, st, +1) },
			keys: [2]string{"up", "down"},
			help: "↑↓",
			hint: "format",
		},
		{
			label: "Level",
			value: func() string {
				f := st.cur().Format
				if !f.HasLevels() {
					return "— (this format has a single setting)"
				}
				return f.Levels[a.levelIndex(f)].Name
			},
			prev: func(a *App) { a.compressLevel(st, -1) },
			next: func(a *App) { a.compressLevel(st, +1) },
			keys: [2]string{"pgup", "pgdown"},
			help: "PgUp/PgDn",
			hint: "level",
		},
	}
	srcIdx := a.active
	sources := pathsOf(targets)
	d.onSubmit = func(a *App, v string) tea.Cmd {
		c := st.cur()
		if err := c.Check(st.sel); err != nil {
			a.dialog.errLine = err.Error()
			return nil
		}
		out, err := resolveUserPath(v, p.Path())
		if err != nil {
			a.dialog.errLine = err.Error()
			return nil
		}
		name, err := compress.CheckName(c.Format, filepath.Base(out))
		if err != nil {
			a.dialog.errLine = err.Error()
			return nil
		}
		out = filepath.Join(filepath.Dir(out), name)
		if err := ops.ValidateName(name); err != nil {
			a.dialog.errLine = errText(err)
			return nil
		}
		for _, s := range sources {
			if out == s {
				a.dialog.errLine = "the new file cannot replace one of the items being added"
				return nil
			}
		}
		req := compress.Request{Format: c.ID, Level: a.levelIndex(c.Format), Sources: sources, Output: out, Tool: c.Tool}
		a.lastFormat = c.ID
		if fi, err := os.Lstat(out); err == nil {
			if fi.IsDir() {
				a.dialog.errLine = "a folder with that name exists"
				return nil
			}
			a.zipOverwriteDialog(srcIdx, req, fi.Size())
			return nil
		}
		a.dialog = nil
		return a.startZip(srcIdx, req)
	}
	a.dialog = d
	return nil
}

// compressLabel names the path field after the kind of format.
func (a *App) compressLabel(d *dialog, st *compressState) {
	if st.cur().Kind == compress.Single {
		d.inputLabel = "Compressed file:"
	} else {
		d.inputLabel = "Archive file:"
	}
}

// levelIndex is the level chosen for f (its default until changed).
func (a *App) levelIndex(f compress.Format) int {
	if i, ok := a.formatLevels[f.ID]; ok {
		return f.LevelIndex(i)
	}
	return f.DefaultLevel
}

// compressFormat moves to the next/previous format and updates the file
// name: a suggested name is regenerated; an edited one gets the new
// extension if it had the old one; anything else is left alone (Enter then
// checks it).
func (a *App) compressFormat(d *dialog, st *compressState, delta int) {
	from := st.cur().Format
	n := len(st.caps)
	st.idx = (st.idx + delta + n) % n
	to := st.cur().Format
	v := d.input.Value()
	nv := v
	switch {
	case v == st.auto:
		nv = st.defaultPath(to)
		st.auto = nv
	case from.HasExt(v):
		nv = v[:len(v)-len(from.Ext)] + to.Ext
	}
	if nv != v {
		d.input = newLineInput(nv)
	}
	a.compressLabel(d, st)
}

func (a *App) compressLevel(st *compressState, delta int) {
	f := st.cur().Format
	if !f.HasLevels() {
		return
	}
	i := a.levelIndex(f) + delta
	i = max(0, min(i, len(f.Levels)-1))
	a.formatLevels[f.ID] = i
}

// zipOverwriteDialog asks before replacing an existing file with the new
// one. The default (Enter) is Cancel.
func (a *App) zipOverwriteDialog(srcIdx int, req compress.Request, size int64) {
	f, _ := compress.Lookup(req.Format)
	d := &dialog{kind: dlgZipExists, title: fmt.Sprintf("%q already exists", filepath.Base(req.Output)), danger: true}
	d.body = []dline{path(req.Output), text("Existing file: " + fsys.HumanSize(size)), blank(),
		warn("Overwrite replaces it with the new " + f.Name + " file (only once the new file is complete).")}
	d.buttons = []button{
		{label: "Cancel (Esc)", run: func(a *App) tea.Cmd {
			a.dialog = nil
			a.setStatusLevel(f.Name+" not created — nothing was changed", lvlWarn)
			return nil
		}},
		{label: "Overwrite (o)", key: "o", danger: true, run: func(a *App) tea.Cmd {
			a.dialog = nil
			req.Overwrite = true
			return a.startZip(srcIdx, req)
		}},
	}
	d.onEsc = d.buttons[0].run
	a.dialog = d
}

func (a *App) startZip(srcIdx int, req compress.Request) tea.Cmd {
	req.Sources = append([]string(nil), req.Sources...)
	f, _ := compress.Lookup(req.Format)
	title := "Creating " + f.Name
	if f.Kind == compress.Single {
		title = "Compressing (" + f.Name + ")"
	}
	s, ctx := a.newSession(title, srcIdx, req.Sources)
	s.to = req.Output
	h := a.hooks(ctx, s)
	go func() {
		res := compress.Create(ctx, req, h)
		s.events <- opZipDoneMsg{s.id, res}
	}()
	return a.startCmds(s)
}

func (a *App) finishZip(s *opSession, res compress.Result) tea.Cmd {
	focus := ""
	var cmd tea.Cmd
	if res.Created() {
		focus = filepath.Base(res.Output)
		cmd = a.refreshPanelsAt(filepath.Dir(res.Output), focus)
	}
	summary := res.Summary()
	details := appendTaskDetail(nil, "Stopped", res.Fatal)
	details = append(details, itemErrorDetails(res.Errors)...)
	for _, excluded := range res.Excluded {
		details = append(details, "Not included: "+excluded)
	}
	state := taskOutcome(res.Canceled, res.Fatal, len(res.Errors), res.Created() && (res.Files+res.Dirs+res.Links > 0))
	switch {
	case res.OK():
		a.setStatusLevel(summary, lvlOK)
		if src := a.panels[s.srcPanel]; !src.InArchive() && a.sourceSelectionUnchanged(s) {
			src.ClearSelection()
		}
	case res.Canceled:
		a.setStatusLevel(summary, lvlWarn)
	default:
		level := lvlErr
		if res.Created() {
			level = lvlWarn
		}
		a.setStatusLevel(summary, level)
	}
	a.finishTask(s, state, summary, details...)
	return cmd
}
