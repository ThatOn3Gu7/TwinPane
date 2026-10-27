package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/compress"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
)

// The compress dialog (z, or Compress in the context menu) creates an
// archive or a compressed file of the targets in any format of package
// compress. It is the phase-5 "create a ZIP" dialog with two selector rows
// added. The output goes in the active panel's folder by default. Archive
// formats prefill only the extension, with the cursor before the dot:
//
//	Format  ◂ TAR.GZ (.tar.gz) ▸   ↑↓       (all formats; unavailable ones say why)
//	Level   ◂ normal ▸             PgUp/PgDn (only formats with levels)
//	Archive file: [/source/folder/.tar.gz]
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
	single string // source filename for single-file formats
	auto   string // the path value set automatically (not edited by the user)
}

func (st *compressState) cur() compress.Capability { return st.caps[st.idx] }

// defaultPath is the suggested output for format f. Archive names start
// blank; single-file formats keep the selected file's name.
func (st *compressState) defaultPath(f compress.Format) string {
	name := f.Ext
	if f.Kind == compress.Single && st.single != "" {
		name = st.single + f.Ext
	}
	return filepath.Join(st.outDir, name)
}

// newCompressPathInput starts archive names immediately before the extension.
func newCompressPathInput(path string, f compress.Format) *lineInput {
	in := newLineInput(path)
	if f.Kind == compress.Archive {
		in.SetCursor(len(in.runes) - len([]rune(f.Ext)))
	}
	return in
}

// isFormatExtensionOnly reports whether name consists solely of a known suffix.
func isFormatExtensionOnly(name string) bool {
	for _, f := range compress.Formats() {
		if strings.EqualFold(name, f.Ext) {
			return true
		}
	}
	return false
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
	outDir := p.Path()
	st := &compressState{caps: compress.Detect(a.lookPath), outDir: outDir}
	st.sel.Items = len(targets)
	if len(targets) == 1 && !targets[0].IsDir && targets[0].Mode.IsRegular() {
		st.sel.SingleFile = true
		st.single = targets[0].Name
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
		hint("Default output folder: "+shortPath(st.outDir)+". Archive names start at the extension; single-file formats keep the source name. Edit the path to choose another folder."),
		hint("Folders are added with all their contents; symbolic links are stored as links. The new file is never added to itself, and an existing file is not replaced without asking."))
	st.auto = st.defaultPath(st.cur().Format)
	d.input = newCompressPathInput(st.auto, st.cur().Format)
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
		base := filepath.Base(out)
		if base == "." || isFormatExtensionOnly(base) {
			a.dialog.errLine = "enter a file name before the extension"
			return nil
		}
		name, err := compress.CheckName(c.Format, base)
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
		a.savePrefs(false) // remember the format for the settings default
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
// name: an untouched default gets the new extension; an edited name keeps
// its stem when it has the old extension; anything else is left alone (Enter
// then checks it).
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
		d.input = newCompressPathInput(nv, to)
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
	a.savePrefs(false) // remember the level for the settings default
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
	a.historyTouch(s)
	a.compressProgress = &compressProgressState{
		id: s.id, req: req, format: f, started: s.started,
		progress: ops.Progress{Phase: "starting"},
	}
	a.dialog = &dialog{kind: dlgCompressProgress, width: 72}
	a.updateCompressionDialog()
	h := a.hooks(ctx, s)
	go func() {
		res := compress.Create(ctx, req, h)
		s.events <- opZipDoneMsg{s.id, res}
	}()
	return tea.Batch(a.startCmds(s), a.compressPulse(s.id))
}

func (a *App) finishZip(s *opSession, res compress.Result) tea.Cmd {
	if cp := a.compressProgress; cp != nil && cp.id == s.id {
		cp.finished, cp.finishedAt, cp.result = true, a.now(), res
		a.updateCompressionDialog()
	}
	focus := ""
	var cmd tea.Cmd
	if res.Created() {
		focus = filepath.Base(res.Output)
		cmd = a.refreshPanelsAt(filepath.Dir(res.Output), focus)
	}
	summary := res.Summary()
	details := appendTaskDetail(nil, "Stopped", res.Fatal)
	details = append(details, itemErrorDetails(res.Errors)...)
	details = appendTaskDetail(details, "Temporary output cleanup", res.CleanupErr)
	for _, excluded := range res.Excluded {
		details = append(details, "Not included: "+excluded)
	}
	cleanupErrors := 0
	if res.CleanupErr != nil {
		cleanupErrors = 1
	}
	state := taskOutcome(res.Canceled, res.Fatal, len(res.Errors)+cleanupErrors, res.Created() && (res.Files+res.Dirs+res.Links > 0))
	level := lvlErr
	switch {
	case res.OK():
		level = lvlOK
		if src := a.panels[s.srcPanel]; !src.InArchive() && a.sourceSelectionUnchanged(s) {
			src.ClearSelection()
		}
	case res.Canceled:
		level = lvlWarn
	default:
		if res.Created() {
			level = lvlWarn
		}
	}
	s.hist.files, s.hist.dirs, s.hist.links, s.hist.bytes = res.Files, res.Dirs, res.Links, res.Bytes
	s.hist.errors = itemErrorTexts(res.Errors)
	if res.Fatal != nil {
		s.hist.errors = append(s.hist.errors, errText(res.Fatal))
	}
	if res.CleanupErr != nil {
		s.hist.errors = append(s.hist.errors, "temporary output cleanup: "+errText(res.CleanupErr))
	}
	if res.Output != "" {
		s.hist.result = "wrote " + res.Output
	}
	body := []string{}
	if res.Output != "" {
		body = append(body, "→ "+fsys.SanitizeName(res.Output))
	}
	// Failures are named in the pop-up itself: the first two details say what
	// went wrong, the task list keeps the rest.
	body = append(body, detailsBody(details)...)
	failed := res.Fatal != nil || len(res.Errors)+cleanupErrors > 0
	a.taskResult(s, level, operationHeading("Compress", failed, res.Canceled, res.Created() && (failed || res.Canceled)), summary, body...)
	a.setStatus("", false)
	a.finishTask(s, state, summary, details...)
	return cmd
}
