package ui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/archive"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
)

// The action menu (M, or the "M menu" chip in the hint bar).
//
// It lists what can be done with the entry under the cursor (or the
// selection) right now. Every entry either re-sends the shortcut it is
// labelled with — so the menu reuses the existing operations, checks and
// confirmations exactly — or runs a menu-only action (Share, removing the
// folder's bookmark). The letter of an entry picks it directly; Delete is
// shown in the danger colour and always comes last.

type menuEntry struct {
	label, detail string
	key           string         // shortcut shown and accepted in the menu
	send          string         // key re-sent to handleKey ("" = run)
	run           func() tea.Cmd // menu-only action
	danger        bool
}

// menuEntries builds the menu for the current state.
func (a *App) menuEntries() (title string, entries []menuEntry) {
	p := a.ActivePanel()
	e, hasCur := p.Current()
	if hasCur && e.IsParent {
		hasCur = false
	}
	nsel := len(p.Selection())
	targets := p.Targets()
	add := func(m menuEntry) { entries = append(entries, m) }
	single := nsel == 0 && hasCur
	file := single && !e.IsDir

	switch {
	case nsel > 0:
		title = fmt.Sprintf("%d selected", nsel)
	case hasCur:
		title = fsys.SanitizeName(e.Name)
	default:
		title = "This folder"
	}

	if p.InArchive() {
		if file {
			add(menuEntry{label: "View", key: "v", send: "v"})
		} else if single {
			add(menuEntry{label: "Open folder", send: "enter"})
		}
		if len(targets) > 0 {
			add(menuEntry{label: "Extract to folder…", key: "x", send: "x"})
		}
		add(menuEntry{label: "Extract everything", key: "X", send: "X"})
		if hasCur {
			add(menuEntry{label: "Properties", key: "P", send: "P"})
		}
		add(menuEntry{label: "Leave the archive", key: "esc", send: "esc"})
		add(menuEntry{label: "Bookmarks…", key: "b", send: "b"})
		add(menuEntry{label: "Storage info", key: "i", send: "i"})
		return title, entries
	}

	if p.InRemote() {
		// an rclone remote: only what works there (see remoteUnsupported)
		if single && e.IsDir {
			add(menuEntry{label: "Open folder", send: "enter"})
		}
		if len(targets) > 0 {
			dest := a.panelLabel(1 - a.active)
			add(menuEntry{label: "Copy to other panel", detail: dest, key: "c", send: "c"})
			add(menuEntry{label: "Move to other panel", detail: dest, key: "m", send: "m"})
			if len(targets) == 1 {
				add(menuEntry{label: "Rename", key: "r", send: "r"})
			}
		}
		add(menuEntry{label: "New folder", key: "n", send: "n"})
		add(menuEntry{label: "Sources…", detail: "local / cloud", key: "^R", send: "ctrl+r"})
		add(menuEntry{label: "Refresh", key: "^L", send: "ctrl+l"})
		add(menuEntry{label: "Back to local files", detail: shortPath(a.localDirFor(a.active)), key: "l",
			run: func() tea.Cmd { return p.GoLocal(a.localDirFor(a.active), "") }})
		if len(targets) > 0 {
			add(menuEntry{label: "Delete…", key: "d", send: "d", danger: true})
		}
		return title, entries
	}

	if single {
		switch {
		case e.IsDir:
			add(menuEntry{label: "Open folder", send: "enter"})
		case a.isBrowsableArchive(e.Path):
			add(menuEntry{label: "Browse archive (read-only)", send: "enter"})
		default:
			if plat, ok := a.preferExternal(e); ok {
				add(menuEntry{label: "Open (default app)", detail: plat.Name, key: "o", send: "o"})
			} else {
				add(menuEntry{label: "Open (default app)", key: "o", send: "o"})
			}
		}
		if file {
			add(menuEntry{label: "Open with…", key: "O", send: "O"})
			add(menuEntry{label: "View", key: "v", send: "v"})
		}
	}
	if len(targets) > 0 {
		files := true
		for _, t := range targets {
			if t.IsDir {
				files = false
			}
		}
		if files {
			add(menuEntry{label: "Edit", key: "e", send: "e"})
		}
		dest := a.panelLabel(1 - a.active)
		add(menuEntry{label: "Copy to other panel", detail: dest, key: "c", send: "c"})
		add(menuEntry{label: "Move to other panel", detail: dest, key: "m", send: "m"})
		if len(targets) > 1 {
			add(menuEntry{label: "Rename (bulk)", key: "r", send: "r"})
		} else {
			add(menuEntry{label: "Rename", key: "r", send: "r"})
		}
		add(menuEntry{label: "Compress…", detail: "default: " + shortPath(p.Path()), key: "z", send: "z"})
		if file && canExtract(e.Path) {
			add(menuEntry{label: "Extract to folder…", key: "x", send: "x"})
		}
	}
	if hasCur || nsel > 0 {
		add(menuEntry{label: "Properties", key: "P", send: "P"})
	}
	if file {
		if plat := a.platform(); plat.Termux && plat.HasOpener() {
			add(menuEntry{label: "Share…", detail: "Android share sheet", key: "s", run: a.shareFile})
		}
	}
	add(menuEntry{label: "New folder", key: "n", send: "n"})
	if a.canComparePanels() {
		add(menuEntry{label: "Compare directories", key: "D", send: "D"})
	}
	add(menuEntry{label: "Sources…", detail: "local / cloud", key: "^R", send: "ctrl+r"})
	if dir := p.Path(); a.isBookmarked(dir) {
		add(menuEntry{label: "Remove this folder's bookmark", key: "B", run: a.unbookmarkHere})
	} else {
		add(menuEntry{label: "Bookmark this folder", key: "B", send: "B"})
	}
	add(menuEntry{label: "Bookmarks…", key: "b", send: "b"})
	add(menuEntry{label: "Storage info", key: "i", send: "i"})
	add(menuEntry{label: "Analyze folder sizes", key: "A", send: "A"})
	if len(targets) > 0 {
		add(menuEntry{label: "Delete…", key: "d", send: "d", danger: true})
	}
	return title, entries
}

// openMenu shows the action menu (M).
func (a *App) openMenu() tea.Cmd {
	title, entries := a.menuEntries()
	d := &dialog{kind: dlgMenu, title: title, width: 50}
	for _, m := range entries {
		k := m.key
		if k == "esc" {
			k = ""
		}
		d.list = append(d.list, listItem{label: m.label, detail: m.detail, key: k, danger: m.danger})
	}
	d.footer = "⏎ or letter: run · Esc close"
	d.onPick = func(a *App, i int) tea.Cmd {
		m := entries[i]
		a.dialog = nil
		if m.run != nil {
			return m.run()
		}
		_, cmd := a.handleKey(keyFor(m.send))
		return cmd
	}
	a.dialog = d
	return nil
}

// panelLabel describes panel i's folder for menu details: a short local
// path, or "remote:/folder".
func (a *App) panelLabel(i int) string {
	p := a.panels[i]
	if p.InRemote() {
		return fsys.SanitizeName(p.Location().Display())
	}
	return shortPath(p.RealDir())
}

// canExtract reports whether x can extract the file (any supported format).
func canExtract(p string) bool {
	info, err := archive.Detect(p)
	return err == nil && info.Format != archive.FormatUnknown
}
