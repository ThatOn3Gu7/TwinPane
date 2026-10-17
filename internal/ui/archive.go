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
// creation run as background operations like copy/move: progress box, Esc
// cancels, the conflict and low-space dialogs are shared.
//
// Inside an archive, panel paths are virtual (".../x.zip/inner/path"), so
// every operation that would treat them as real paths is refused here with
// an explanation (refuseInArchive / refuseArchiveDest), before anything is
// touched.

type opExtractDoneMsg struct {
	id  int
	res archive.ExtractResult
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
		if fi, err := os.Stat(dest); err == nil && !fi.IsDir() {
			a.dialog.errLine = "that is a file, not a folder"
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
	s, ctx := a.newSession("Extracting", srcIdx, nil)
	h := a.hooks(ctx, s)
	go func() {
		res := archive.ExtractAny(ctx, req, h) // ZIP: archive.Extract
		s.events <- opExtractDoneMsg{s.id, res}
	}()
	return a.startCmds(s)
}

// refreshReal refreshes the panels showing the real filesystem, putting the
// cursor on focus in the panel that shows dir.
func (a *App) refreshReal(dir, focus string) tea.Cmd {
	var cmds []tea.Cmd
	for _, p := range a.panels {
		switch {
		case p.InArchive():
			cmds = append(cmds, p.Refresh()) // the archive may have been replaced
		case p.Path() == dir && focus != "":
			cmds = append(cmds, p.RefreshFocus(focus))
		default:
			cmds = append(cmds, p.Refresh())
		}
	}
	return tea.Batch(cmds...)
}

func (a *App) finishExtract(s *opSession, res archive.ExtractResult) tea.Cmd {
	a.op = nil
	a.dialog = nil
	focus := ""
	if len(res.Top) > 0 {
		focus = res.Top[0]
	}
	cmds := []tea.Cmd{a.refreshReal(res.Dest, focus)}
	if dir := filepath.Dir(res.Dest); focus == "" || dir != res.Dest {
		// extracting into a new folder: show it selected in its parent
		cmds = append(cmds, a.refreshReal(dir, filepath.Base(res.Dest)))
	}
	summary := res.Summary()
	if res.Unsafe > 0 {
		summary += fmt.Sprintf(" · %d unsafe archive paths refused", res.Unsafe)
	}
	switch {
	case res.OK():
		a.setStatusLevel(summary+" → "+res.Dest, lvlOK)
		a.panels[s.srcPanel].ClearSelection()
	case res.Canceled && res.Fatal == nil && len(res.Errors) == 0:
		a.setStatusLevel(summary, lvlWarn)
	default:
		a.setStatusLevel(summary, lvlErr)
		var body []dline
		body = append(body, text(summary), text("Into: "+res.Dest))
		if res.Fatal != nil {
			body = append(body, blank(), warn("Stopped: "+errText(res.Fatal)))
		}
		if len(res.Errors) > 0 {
			body = append(body, blank(), warn(fmt.Sprintf("Not extracted (%d):", len(res.Errors))))
			body = append(body, itemLines(res.Errors)...)
		}
		body = append(body, blank(), hint("Files that failed were not written (no partial files are left); everything else was extracted."))
		a.showReport("Extraction finished with problems", body)
	}
	return tea.Batch(cmds...)
}

// Creating archives (z) moved to compress.go in phase 6.
