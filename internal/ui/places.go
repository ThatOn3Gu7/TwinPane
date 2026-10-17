package ui

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/bookmarks"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/storage"
)

// Bookmarks (b, B) and the storage overview (i).
//
// The bookmark list lives in package bookmarks (a JSON file in TwinPane's
// configuration folder). The list dialog opens a bookmark in the active
// panel (Enter / tap / 1-9), renames (r) or removes (d) the highlighted
// one and adds the current folder (a). A bookmark whose folder no longer
// exists is marked "missing"; opening it explains and offers removal — it
// never silently opens some other folder instead.

// bookmarkStore returns the store, re-reading the file (another TwinPane
// may have changed it).
func (a *App) bookmarkStore() (*bookmarks.Store, error) {
	if a.bookmarks == nil {
		path := a.opts.BookmarksPath
		if path == "" {
			p, err := bookmarks.DefaultPath()
			if err != nil {
				return nil, err
			}
			path = p
		}
		s, err := bookmarks.Open(path)
		a.bookmarks = s
		return s, err
	}
	return a.bookmarks, a.bookmarks.Reload()
}

// bookmarkHere bookmarks the active panel's folder (B).
func (a *App) bookmarkHere() tea.Cmd {
	p := a.ActivePanel()
	if p.InArchive() {
		a.setStatus("folders inside a ZIP archive cannot be bookmarked: bookmark the folder that contains it (Esc leaves the archive)", true)
		return nil
	}
	dir := p.Path()
	if dir == "" {
		return nil
	}
	s, err := a.bookmarkStore()
	if err != nil {
		a.setStatus("bookmarks: "+errText(err), true)
		return nil
	}
	bm, err := s.Add(dir, "")
	switch {
	case errors.Is(err, bookmarks.ErrDuplicate):
		a.setStatusLevel(fmt.Sprintf("already bookmarked as %q — b opens the bookmarks (rename, remove)", bm.Name), lvlWarn)
	case err != nil:
		a.setStatus("could not save the bookmark: "+errText(err), true)
	default:
		a.setStatusLevel(fmt.Sprintf("bookmarked %s as %q — b opens the bookmarks", fsys.SanitizeName(dir), bm.Name), lvlOK)
	}
	return nil
}

// unbookmarkHere removes the bookmark of the active panel's folder (menu).
func (a *App) unbookmarkHere() tea.Cmd {
	s, err := a.bookmarkStore()
	if err != nil {
		a.setStatus("bookmarks: "+errText(err), true)
		return nil
	}
	dir := a.ActivePanel().Path()
	if err := s.Remove(dir); err != nil {
		a.setStatus("could not remove the bookmark: "+errText(err), true)
		return nil
	}
	a.setStatusLevel("bookmark of "+fsys.SanitizeName(dir)+" removed (the folder is not touched)", lvlOK)
	return nil
}

// isBookmarked reports whether the active panel's folder is bookmarked
// (without re-reading the file: used to build the menu).
func (a *App) isBookmarked(dir string) bool {
	if a.bookmarks == nil {
		if _, err := a.bookmarkStore(); err != nil || a.bookmarks == nil {
			return false
		}
	}
	_, ok := a.bookmarks.Find(dir)
	return ok
}

// shortPath abbreviates the home folder as ~.
func shortPath(p string) string {
	home := fsys.HomeDir()
	if home != "" && home != "/" && (p == home || strings.HasPrefix(p, home+"/")) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}

// openBookmarks shows the bookmark list (b). cursor selects the
// highlighted row; errLine is shown under the list.
func (a *App) openBookmarks(cursor int, errLine string) tea.Cmd {
	s, err := a.bookmarkStore()
	d := &dialog{kind: dlgBookmarks, title: "Bookmarks", width: 64}
	if err != nil {
		errLine = errText(err)
	}
	var list []bookmarks.Bookmark
	if s != nil {
		list = s.List()
	}
	d.list = []listItem{}
	for _, bm := range list {
		it := listItem{label: bm.Name, detail: shortPath(bm.Path)}
		if it.detail != bm.Path {
			it.info = bm.Path
		}
		switch st, _ := bookmarks.Check(bm.Path); st {
		case bookmarks.Missing:
			it.detail = "missing · " + it.detail
			it.danger = true
		case bookmarks.NotFolder:
			it.detail = "not a folder · " + it.detail
			it.danger = true
		}
		d.list = append(d.list, it)
	}
	d.listCursor = max(0, min(cursor, len(d.list)-1))
	d.listEmpty = "No bookmarks yet. Press a to add the current folder (or B while browsing)."
	d.errLine = errLine
	if s != nil {
		d.body = []dline{hint("Saved in " + shortPath(s.Path()))}
	}
	d.footer = "⏎ open · a add this folder · r rename · d remove · Esc close"
	d.onPick = func(a *App, i int) tea.Cmd {
		bm := list[i]
		switch st, err := bookmarks.Check(bm.Path); st {
		case bookmarks.OK:
			a.dialog = nil
			a.setStatus(fmt.Sprintf("bookmark %q", bm.Name), false)
			return a.ActivePanel().ChangeDir(bm.Path)
		case bookmarks.Missing:
			a.dialog.errLine = fmt.Sprintf("%q points to a folder that no longer exists (%s) — d removes the bookmark", bm.Name, bm.Path)
		case bookmarks.NotFolder:
			a.dialog.errLine = fmt.Sprintf("%q no longer points to a folder (%s) — d removes the bookmark", bm.Name, bm.Path)
		default:
			a.dialog.errLine = fmt.Sprintf("%q cannot be opened: %s", bm.Name, errText(err))
		}
		return nil
	}
	d.listActions = []listAction{
		{key: "a", label: "add this folder", run: func(a *App, i int) tea.Cmd {
			a.dialog = nil
			a.bookmarkHere()
			msg := ""
			if a.statusLevel == lvlErr || a.statusLevel == lvlWarn {
				msg = a.status
			}
			n := 0
			if a.bookmarks != nil {
				n = len(a.bookmarks.List())
			}
			if idx, ok := a.bookmarkIndex(a.ActivePanel().Path()); ok {
				n = idx + 1
			}
			return a.openBookmarks(n-1, msg)
		}},
		{key: "r", label: "rename", needItem: true, run: func(a *App, i int) tea.Cmd {
			return a.renameBookmark(list[i], i)
		}},
		{key: "d", label: "remove", needItem: true, run: func(a *App, i int) tea.Cmd {
			return a.removeBookmarkDialog(list[i], i)
		}},
	}
	a.dialog = d
	return nil
}

func (a *App) bookmarkIndex(dir string) (int, bool) {
	if a.bookmarks == nil {
		return 0, false
	}
	return a.bookmarks.Find(dir)
}

func (a *App) renameBookmark(bm bookmarks.Bookmark, i int) tea.Cmd {
	d := &dialog{kind: dlgBookmarks, title: "Rename bookmark", width: 56}
	d.body = []dline{path(bm.Path)}
	d.input = newLineInput(bm.Name)
	d.inputLabel = "Name:"
	d.footer = "Enter save · Esc back"
	d.onSubmit = func(a *App, v string) tea.Cmd {
		if bookmarks.CleanName(v) == "" {
			a.dialog.errLine = "the name is empty"
			return nil
		}
		if err := a.bookmarks.Rename(bm.Path, v); err != nil {
			a.dialog.errLine = errText(err)
			return nil
		}
		return a.openBookmarks(i, "")
	}
	d.onEsc = func(a *App) tea.Cmd { return a.openBookmarks(i, "") }
	a.dialog = d
	return nil
}

func (a *App) removeBookmarkDialog(bm bookmarks.Bookmark, i int) tea.Cmd {
	d := &dialog{kind: dlgBookmarks, title: fmt.Sprintf("Remove the bookmark %q?", bm.Name)}
	d.body = []dline{path(bm.Path), blank(), hint("Only the bookmark is removed; the folder itself is not touched.")}
	back := func(a *App) tea.Cmd { return a.openBookmarks(i, "") }
	d.buttons = []button{
		{label: "Cancel (Esc)", run: back},
		{label: "Remove (y)", key: "y", run: func(a *App) tea.Cmd {
			if err := a.bookmarks.Remove(bm.Path); err != nil {
				return a.openBookmarks(i, errText(err))
			}
			a.setStatusLevel(fmt.Sprintf("bookmark %q removed", bm.Name), lvlOK)
			return a.openBookmarks(i, "")
		}},
	}
	d.onEsc = back
	a.dialog = d
	return nil
}

// ---- storage -------------------------------------------------------------------

// storageStat is storage.Stat (replaced in tests).
var storageStat = storage.Stat

// showStorage shows the filesystem of the active panel (and of the other
// panel when it is on a different one): mount point, type, total, used,
// free and a usage bar. Values the system does not report are shown as
// unavailable.
func (a *App) showStorage() tea.Cmd {
	p := a.ActivePanel()
	dir := p.RealDir()
	if dir == "" {
		return nil
	}
	in := storageStat(dir)
	d := &dialog{kind: dlgStorage, title: "Storage", width: 60}
	if in.MountErr == nil {
		d.title = "Storage — " + in.Mount.Point
	}
	w := boxWidth(d.width, a.width)
	d.body = a.storageLines(in, w)
	other := a.panels[1-a.active]
	if od := other.RealDir(); od != "" && od != dir {
		oin := storageStat(od)
		if oin.MountErr != nil || in.MountErr != nil || oin.Mount.Point != in.Mount.Point || oin.Mount.Source != in.Mount.Source {
			d.body = append(d.body, blank(), text("Other panel:"))
			d.body = append(d.body, a.storageLines(oin, w)...)
		} else {
			d.body = append(d.body, blank(), hint("The other panel ("+shortPath(od)+") is on the same filesystem."))
		}
	}
	d.buttons = []button{{label: "Close", run: func(a *App) tea.Cmd { a.dialog = nil; return nil }}}
	a.dialog = d
	return nil
}

func (a *App) storageLines(in storage.Info, w int) []dline {
	th := a.theme
	const lw = 12 // label column
	vw := max(8, w-lw)
	var out []dline
	// row lays out "Label       value": values wrap under the value column;
	// paths are shortened on the left instead.
	row := func(label, value string, isPath bool) {
		lines := []string{truncLeft(sanitize(value), vw)}
		if !isPath {
			lines = wrapText(sanitize(value), vw)
		}
		for i, l := range lines {
			lab := ""
			if i == 0 {
				lab = label
			}
			out = append(out, dline{kind: lnRaw, text: th.DialogHint.Render(fmt.Sprintf("%-*s", lw, lab)) + th.DialogText.Render(l)})
		}
	}
	unavailable := func(err error) string {
		if err == nil {
			return "unavailable"
		}
		return "unavailable (" + errText(err) + ")"
	}
	row("Folder", shortPath(in.Path), true)
	if in.MountErr == nil {
		m := in.Mount
		mode := "read-write"
		if in.ReadOnly() {
			mode = "read-only"
		}
		row("Mounted at", m.Point, true)
		row("Filesystem", fmt.Sprintf("%s · %s", orUnknown(in.FSType()), mode), false)
		if m.Source != "" && m.Source != "none" {
			row("Device", m.Source, true)
		}
	} else {
		row("Mounted at", unavailable(in.MountErr), false)
		ft := "unavailable"
		if in.FSType() != "" {
			ft = in.FSType() + " (from the filesystem's type number)"
		}
		if in.ReadOnlyFlag {
			ft += " · read-only"
		}
		row("Filesystem", ft, false)
	}
	if !in.HasSizes() {
		why := in.SizeErr
		if why == nil {
			why = errors.New("this filesystem reports no capacity")
		}
		row("Size", unavailable(why), false)
		return out
	}
	used := in.Used()
	row("Total", fsys.HumanSize(int64(in.Total)), false)
	pct, ok := in.Percent()
	usedS := fsys.HumanSize(int64(used))
	if ok {
		usedS += fmt.Sprintf(" (%.0f%%)", pct)
	}
	row("Used", usedS, false)
	free := fsys.HumanSize(int64(in.Avail)) + " available"
	if r := in.Reserved(); r > 0 {
		free += fmt.Sprintf(" (+%s reserved for the system)", fsys.HumanSize(int64(r)))
	}
	row("Free", free, false)
	if ok {
		label := fmt.Sprintf(" %.0f%% used", pct)
		bw := max(4, min(vw-len(label), 40))
		filled := int(pct / 100 * float64(bw))
		filled = max(0, min(filled, bw))
		st := th.ProgressFill
		if pct >= 90 {
			st = th.DialogWarn
		}
		bar := strings.Repeat(" ", lw) + st.Render(strings.Repeat("█", filled)) + th.ProgressEmpty.Render(strings.Repeat("░", bw-filled)) + th.DialogHint.Render(label)
		out = append(out, dline{kind: lnRaw, text: bar})
	}
	if in.Resolved != "" && in.Resolved != in.Path {
		out = append(out, hint("(the folder is a link to "+filepath.Clean(in.Resolved)+")"))
	}
	return out
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown type"
	}
	return s
}
