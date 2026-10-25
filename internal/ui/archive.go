package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/archive"
	"github.com/ThatOn3Gu7/TwinPane/internal/compress"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/panel"
)

// Archive support: browsing ZIP and TAR-family archives as virtual,
// read-only folders in one panel (panel.OpenArchive), extracting all or part
// of them (archive.ExtractAny), and creating archives (compress.Create).
// Extraction runs as a background task like copy/move, with panel navigation
// available and conflict / low-space prompts serialized safely. Compression
// keeps its own modal progress and completion dialog above the file manager.
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

// archiveNames are other extensions x can extract; TAR-family extensions
// are also offered for read-only browsing, while extraction checks content.
var archiveNames = []string{
	".tar.gzip", ".tar.bzip2", ".tar.zstd", ".tar.gz", ".tgz", ".tar.bz2", ".tbz2", ".tbz",
	".tar.xz", ".txz", ".tar.zst", ".tzst", ".tar.lz4", ".tar", ".gz", ".gzip", ".bz2", ".bzip2",
	".xz", ".zst", ".zstd", ".lz4", ".7z", ".rar",
}

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

type archiveHintCache struct {
	path      string
	size      int64
	modTime   time.Time
	browsable bool
}

func (a *App) isBrowsableArchive(name string) bool {
	fi, err := os.Stat(name)
	if err != nil || !fi.Mode().IsRegular() {
		a.archiveHint = &archiveHintCache{path: name}
		return false
	}
	if c := a.archiveHint; c != nil && c.path == name && c.size == fi.Size() && c.modTime.Equal(fi.ModTime()) {
		return c.browsable
	}
	info, err := archive.Detect(name)
	ok := err == nil && (info.Format == archive.FormatZIP || info.Format == archive.FormatTAR)
	a.archiveHint = &archiveHintCache{path: name, size: fi.Size(), modTime: fi.ModTime(), browsable: ok}
	return ok
}

// refuseInArchive reports (and returns true) when the active panel shows an
// archive: what cannot be done on archive entries.
func (a *App) refuseInArchive(what string) bool {
	if !a.ActivePanel().InArchive() {
		return false
	}
	a.setStatus(what+" is not possible inside a read-only archive: x extracts, v views, Esc leaves", true)
	return true
}

// refuseArchiveDest reports (and returns true) when the other panel, the
// destination of copy/move, shows an archive.
func (a *App) refuseArchiveDest(what string) bool {
	if !a.panels[1-a.active].InArchive() {
		return false
	}
	a.setStatus("cannot "+what+" into a read-only archive: leave it in the other panel first (Esc there)", true)
	return true
}

// openFile is Enter (and double tap) on a file: an archive entry opens in
// the viewer, a ZIP or TAR-family archive is browsed, other files open in the viewer (or,
// for a double tap, the editor as in phase 4).
func (a *App) openFile(p *panel.Panel, e fsys.Entry, tap bool) tea.Cmd {
	if p.InRemote() {
		// remote files open in the read-only preview (viewer_remote.go): no
		// local app can read them, and nothing on the remote is ever written
		return a.openViewer(p, e)
	}
	if p.InArchive() {
		return a.openViewer(p, e)
	}
	if a.isBrowsableArchive(e.Path) {
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
	msg := fmt.Sprintf("browsing %s (%s, read-only): %s, %s — x extracts, Esc leaves", filepath.Base(arc.Path), arc.Format.Name(),
		plural(arc.Files, "file"), plural(arc.Dirs, "folder"))
	lvl := lvlInfo
	var notes []string
	if n := arc.UnsafeCount; n > 0 {
		notes = append(notes, fmt.Sprintf("%s with unsafe paths hidden (never extracted)", plural(n, "entry")))
	}
	if arc.UnsafeLinks > 0 {
		notes = append(notes, fmt.Sprintf("%s visible but never followed or extracted", plural(arc.UnsafeLinks, "unsafe link")))
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
// filesystem: the supported archive under the cursor (or the one selected).
func (a *App) beginExtract(all bool) tea.Cmd {
	p := a.ActivePanel()
	// Keep extraction beside the source archive by default. The destination
	// field remains editable when the user wants a different folder.
	destDir := p.RealDir()
	var t extractTarget
	defDest := destDir
	if p.InArchive() {
		arc := p.Archive()
		destDir = filepath.Dir(arc.Path)
		defDest = destDir
		t.archive = arc.Path
		if all {
			t.what = fmt.Sprintf("the whole archive (%s, %s)", plural(arc.Files, "file"), plural(arc.Dirs, "folder"))
			folder := stem(filepath.Base(arc.Path))
			if arc.Format.Format == archive.FormatTAR {
				folder = archive.FolderName(filepath.Base(arc.Path))
			}
			defDest = filepath.Join(destDir, folder)
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
		destDir = filepath.Dir(e.Path)
		defDest = destDir
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
		hint("Default destination is the source archive's folder; whole archives use a same-named subfolder. Edit the path to choose another folder. Missing folders are created, and existing files are never overwritten without asking."),
		hint("Unsafe paths and symbolic links in archives are never extracted."))
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
	level := lvlErr
	switch {
	case res.OK():
		level = lvlOK
		if a.sourceSelectionUnchanged(s) {
			a.panels[s.srcPanel].ClearSelection()
		}
	case res.Canceled && res.Fatal == nil && len(res.Errors) == 0:
		level = lvlWarn
	}
	body := []string{"→ " + fsys.SanitizeName(res.Dest)}
	if len(res.Errors) > 0 {
		body = append(body, fmt.Sprintf("%s — details in the task list (t)", plural(len(res.Errors), "entry")+" failed"))
	}
	a.taskResult(s, level, summary, body...)
	a.setStatus("", false)
	a.finishTask(s, state, summary, details...)
	return tea.Batch(cmds...)
}

// Creating archives (z) moved to compress.go in phase 6.
