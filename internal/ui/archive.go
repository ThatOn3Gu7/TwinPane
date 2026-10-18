package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/archive"
	"github.com/ThatOn3Gu7/TwinPane/internal/compress"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/panel"
)

// Archive support (phase 5): browsing a ZIP archive as a virtual, read-only
// folder in one panel (panel.OpenArchive), extracting all or part of it
// (archive.Extract) and creating archives (archive.Create). Extraction and
// creation run as background tasks like copy/move: progress and outcomes stay
// in the task view, panel navigation remains available, and conflict / low-
// space prompts are serialized safely.
//
// Inside an archive, panel paths are virtual (".../x.zip/inner/path"), so
// every operation that would treat them as real paths is refused here with
// an explanation (refuseInArchive / refuseArchiveDest), before anything is
// touched.

type opExtractDoneMsg struct {
	id          int
	res         archive.ExtractResult
	parent      string
	parentFocus string
}

// zipNames are file extensions offered as archives in the hint bar (any
// file with a ZIP signature can be opened).
var zipNames = []string{".zip", ".jar", ".apk", ".aar", ".whl", ".epub", ".cbz", ".xpi"}

// archiveNames are other extensions x can extract (phase 9); the hint bar
// goes by the name, extraction checks the content.
var archiveNames = []string{".tar", ".tgz", ".tbz2", ".txz", ".gz", ".bz2", ".xz", ".zst", ".lz4", ".7z", ".rar"}

func looksLikeArchive(name string) bool {
	n := strings.ToLower(name)
	for _, ext := range archiveNames {
		if strings.HasSuffix(n, ext) && len(n) > len(ext) {
			return true
		}
	}
	return false
}

func looksLikeZip(name string) bool {
	n := strings.ToLower(name)
	for _, ext := range zipNames {
		if strings.HasSuffix(n, ext) {
			return true
		}
	}
	return false
}

// refuseInArchive reports (and returns true) when the active panel shows an
// archive: what cannot be done on archive entries.
func (a *App) refuseInArchive(what string) bool {
	if !a.ActivePanel().InArchive() {
		return false
	}
	a.setStatus(what+" is not possible inside a ZIP archive (it is read-only): x extracts, v views, Esc leaves the archive", true)
	return true
}

// refuseArchiveDest reports (and returns true) when the other panel, the
// destination of copy/move, shows an archive.
func (a *App) refuseArchiveDest(what string) bool {
	if !a.panels[1-a.active].InArchive() {
		return false
	}
	a.setStatus("cannot "+what+" into a ZIP archive (archives are read-only here): leave it in the other panel first (Esc there)", true)
	return true
}

// openFile is Enter (and double tap) on a file: an archive entry opens in
// the viewer, a ZIP archive is browsed, other files open in the viewer (or,
// for a double tap, the editor as in phase 4).
func (a *App) openFile(p *panel.Panel, e fsys.Entry, tap bool) tea.Cmd {
	if p.InRemote() {
		a.setStatus("files on a remote cannot be opened directly — copy it to a local panel first (c)", true)
		return nil
	}
	if p.InArchive() {
		return a.openViewer(p, e)
	}
	if archive.Sniff(e.Path) {
		a.setStatus("opening "+fsys.SanitizeName(e.Name)+"…", false)
		return p.OpenArchive(e.Path)
	}
	// Media and documents open in their app when the platform has an
	// opener (termux-open, xdg-open); everything else as before.
	if plat, ok := a.preferExternal(e); ok {
		if argv, ok := plat.DefaultArgv(e.Path); ok {
			return a.launchCmd(argv, e.Name, plat, "")
		}
	}
	if tap {
		// a double tap always asks before the editor starts (editPathsAsk)
		return a.editPathsAsk([]string{e.Path}, false, true)
	}
	return a.openViewer(p, e)
}

// archiveOpened reports a panel that just entered an archive.
func (a *App) archiveOpened(p *panel.Panel) {
	arc := p.Archive()
	msg := fmt.Sprintf("browsing %s (read-only): %s, %s — x extracts, Esc leaves", filepath.Base(arc.Path),
		plural(arc.Files, "file"), plural(arc.Dirs, "folder"))
	lvl := lvlInfo
	var notes []string
	if n := len(arc.Unsafe); n > 0 {
		notes = append(notes, fmt.Sprintf("%s with unsafe paths hidden (never extracted)", plural(n, "entry")))
	}
	if arc.Encrypted > 0 {
		notes = append(notes, fmt.Sprintf("%d encrypted (cannot be read)", arc.Encrypted))
	}
	if arc.Duplicates > 0 {
		notes = append(notes, fmt.Sprintf("%d duplicate names ignored", arc.Duplicates))
	}
	if len(notes) > 0 {
		msg += " · " + strings.Join(notes, " · ")
		lvl = lvlWarn
	}
	a.setStatusLevel(msg, lvl)
}

// ---- extract -------------------------------------------------------------------

// stem returns name without its extension ("photos.zip" → "photos").
func stem(name string) string {
	if ext := filepath.Ext(name); ext != "" && ext != name {
		return strings.TrimSuffix(name, ext)
	}
	return name
}

// extractTarget is the archive to extract and what from it.
type extractTarget struct {
	archive string   // path of the archive file
	tool    string   // 7-Zip, for 7Z and RAR
	single  string   // output name when decompressing a single compressed file
	base    string   // inner folder the members are relative to
	members []string // inner paths; nil = whole archive
	what    string   // description for the dialog
	names   []string // listed in the dialog
}

// beginExtract opens the extraction dialog. In an archive: the selected
// entries (or the one under the cursor; all = the whole archive). On the
// filesystem: the ZIP archive under the cursor (or the one selected).
func (a *App) beginExtract(all bool) tea.Cmd {
	p := a.ActivePanel()
	other := a.panels[1-a.active]
	destDir := other.Path()
	if other.InArchive() || other.InRemote() || other.Err() != nil || destDir == "" {
		destDir = p.RealDir()
	}
	var t extractTarget
	defDest := destDir
	if p.InArchive() {
		arc := p.Archive()
		t.archive = arc.Path
		if all {
			t.what = fmt.Sprintf("the whole archive (%s, %s)", plural(arc.Files, "file"), plural(arc.Dirs, "folder"))
			defDest = filepath.Join(destDir, stem(filepath.Base(arc.Path)))
		} else {
			targets := p.Targets()
			if len(targets) == 0 {
				a.setStatus("nothing to extract: put the cursor on an entry or select entries with Space (X extracts everything)", true)
				return nil
			}
			t.base = p.ArchiveInner()
			for _, e := range targets {
				in, ok := arc.InnerOf(e.Path)
				if !ok {
					continue
				}
				t.members = append(t.members, in)
				n := e.Name
				if e.IsDir {
					n += "/"
				}
				t.names = append(t.names, n)
			}
			t.what = describe(targets) + " from the archive"
		}
	} else {
		targets := p.Targets()
		if len(targets) != 1 {
			if len(targets) == 0 {
				a.setStatus("nothing to extract: put the cursor on an archive", true)
			} else {
				a.setStatus("select a single archive to extract (or clear the selection with u)", true)
			}
			return nil
		}
		e := targets[0]
		if e.IsDir {
			a.setStatus(fsys.SanitizeName(e.Name)+" is a folder, not an archive", true)
			return nil
		}
		info, err := archive.Detect(e.Path)
		switch {
		case err != nil:
			a.setStatus("cannot read "+fsys.SanitizeName(e.Name)+": "+errText(err), true)
			return nil
		case info.Format == archive.FormatUnknown:
			a.setStatus(fsys.SanitizeName(e.Name)+" is not an archive TwinPane can extract — supported: "+archive.Supported, true)
			return nil
		case info.External():
			c, _ := compress.Find(compress.Detect(a.lookPath), compress.SevenZ)
			if !c.Available {
				hint := "install 7-Zip or p7zip"
				if a.platform().Termux {
					hint = "Termux: pkg install 7zip"
				}
				a.setStatus(fmt.Sprintf("extracting %s archives needs 7-Zip (7zz, 7z, 7za or 7zr), not found — %s", info.Name(), hint), true)
				return nil
			}
			t.tool = c.Tool
		}
		t.archive = e.Path
		if info.Single() {
			t.single = archive.SingleOutputName(e.Name, info.Codec)
			t.what = fmt.Sprintf("%s file %q", info.Name(), e.Name)
			defDest = destDir
		} else {
			t.what = fmt.Sprintf("the %s archive %q", info.Name(), e.Name)
			defDest = filepath.Join(destDir, archive.FolderName(e.Name))
		}
	}

	verb := "Extract"
	if t.single != "" {
		verb = "Decompress"
	}
	d := &dialog{kind: dlgExtract, title: verb + " " + t.what + "?"}
	d.body = []dline{text("From:"), path(t.archive)}
	if t.single != "" {
		d.body = append(d.body, text("Writes the file: "+fsys.SanitizeName(t.single)))
	}
	if t.tool != "" {
		d.body = append(d.body, hint("Unpacked with "+filepath.Base(t.tool)+" into a temporary folder first; nothing is moved into place if it fails."))
	}
	if len(t.names) > 0 {
		d.body = append(d.body, blank())
		for i, n := range t.names {
			if i == listCap {
				d.body = append(d.body, hint(fmt.Sprintf("… and %d more", len(t.names)-listCap)))
				break
			}
			d.body = append(d.body, path("  "+n))
		}
	}
	d.body = append(d.body, blank(),
		hint("Missing folders are created. Existing files are never overwritten without asking; unsafe paths and symbolic links in the archive are never extracted."))
	d.input = newLineInput(defDest)
	d.inputLabel = verb + " into folder:"
	d.footer = "Enter " + strings.ToLower(verb) + " · Esc cancel"
	srcIdx := a.active
	d.onSubmit = func(a *App, v string) tea.Cmd {
		dest, err := resolveUserPath(v, p.RealDir())
		if err != nil {
			a.dialog.errLine = err.Error()
			return nil
		}
		if strings.HasPrefix(dest+string(filepath.Separator), t.archive+string(filepath.Separator)) {
			a.dialog.errLine = "the destination cannot be inside the archive file"
			return nil
		}
		a.dialog = nil
		return a.startExtract(srcIdx, archive.ExtractRequest{Archive: t.archive, Members: t.members, Base: t.base, DestDir: dest,
			SevenZip: t.tool, OutputName: t.single})
	}
	a.dialog = d
	return nil
}

// resolveUserPath turns a typed path into an absolute, clean one: "~" is the
// home folder, relative paths are relative to base.
func resolveUserPath(v, base string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", fmt.Errorf("type a folder or file path")
	}
	if v == "~" || strings.HasPrefix(v, "~/") {
		v = filepath.Join(fsys.HomeDir(), strings.TrimPrefix(v, "~"))
	}
	if !filepath.IsAbs(v) {
		v = filepath.Join(base, v)
	}
	return filepath.Clean(v), nil
}

func (a *App) startExtract(srcIdx int, req archive.ExtractRequest) tea.Cmd {
	req.Members = append([]string(nil), req.Members...)
	req.DestDir = filepath.Clean(req.DestDir)
	s, ctx := a.newSession("Extracting", srcIdx, []string{req.Archive})
	s.to = req.DestDir
	h := a.hooks(ctx, s)
	go func() {
		if ctx.Err() != nil {
			s.events <- opExtractDoneMsg{s.id, archive.ExtractResult{Dest: req.DestDir, Canceled: true}, "", ""}
			return
		}
		parent, parentFocus := missingDestinationParent(req.DestDir)
		if ctx.Err() != nil {
			s.events <- opExtractDoneMsg{s.id, archive.ExtractResult{Dest: req.DestDir, Canceled: true}, "", ""}
			return
		}
		res := archive.ExtractAny(ctx, req, h) // ZIP: archive.Extract
		if parent != "" {
			if fi, err := os.Stat(req.DestDir); err != nil || !fi.IsDir() {
				parent, parentFocus = "", ""
			}
		}
		s.events <- opExtractDoneMsg{s.id, res, parent, parentFocus}
	}()
	return a.startCmds(s)
}

// missingDestinationParent returns the nearest existing directory that would
// gain a child if extraction creates req.DestDir. Filesystem checks run in the
// task goroutine so a slow mount never blocks the UI.
func missingDestinationParent(dest string) (parent, focus string) {
	if _, err := os.Lstat(dest); err == nil || !os.IsNotExist(err) {
		return "", ""
	}
	candidate := dest
	for {
		up := filepath.Dir(candidate)
		if up == candidate {
			return "", ""
		}
		if fi, err := os.Stat(up); err == nil && fi.IsDir() {
			return up, filepath.Base(candidate)
		} else if err != nil && !os.IsNotExist(err) {
			return "", ""
		}
		candidate = up
	}
}

func (a *App) finishExtract(s *opSession, res archive.ExtractResult, parent, parentFocus string) tea.Cmd {
	focus := ""
	if len(res.Top) > 0 {
		focus = res.Top[0]
	}
	var cmds []tea.Cmd
	cmds = append(cmds, a.refreshPanelsAt(res.Dest, focus))
	if parent != "" {
		cmds = append(cmds, a.refreshPanelsAt(parent, parentFocus))
	}
	summary := res.Summary()
	if res.Unsafe > 0 {
		summary += fmt.Sprintf(" · %d unsafe archive paths refused", res.Unsafe)
	}
	partial := res.Files+res.Dirs > 0
	details := []string{"Into: " + res.Dest}
	details = appendTaskDetail(details, "Stopped", res.Fatal)
	details = append(details, itemErrorDetails(res.Errors)...)
	if res.Unsafe > 0 {
		details = append(details, fmt.Sprintf("Refused %d unsafe archive path(s)", res.Unsafe))
	}
	if res.Canceled {
		details = append(details, "Canceled; completed entries were kept, the destination folder may have been created, and the remaining entries were not extracted")
	}
	state := taskOutcome(res.Canceled, res.Fatal, len(res.Errors), partial)
	switch {
	case res.OK():
		a.setStatusLevel(summary+" → "+res.Dest, lvlOK)
		if a.sourceSelectionUnchanged(s) {
			a.panels[s.srcPanel].ClearSelection()
		}
	case res.Canceled && res.Fatal == nil && len(res.Errors) == 0:
		a.setStatusLevel(summary, lvlWarn)
	default:
		a.setStatusLevel(summary, lvlErr)
	}
	a.finishTask(s, state, summary, details...)
	return tea.Batch(cmds...)
}

// Creating archives (z) moved to compress.go in phase 6.
