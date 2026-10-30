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
	key           string         // menu-local letter (not a global shortcut)
	action        string         // bindable action re-sent to handleKey
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
			add(menuEntry{label: "View", action: "browse.view"})
		} else if single {
			add(menuEntry{label: "Open folder", action: "browse.open"})
		}
		if len(targets) > 0 {
			add(menuEntry{label: "Extract to folder…", action: "browse.extract"})
		}
		add(menuEntry{label: "Extract everything", action: "browse.extractAll"})
		if hasCur {
			add(menuEntry{label: "Properties", action: "browse.properties"})
		}
		add(menuEntry{label: "Leave the archive", action: "browse.cancel"})
		add(menuEntry{label: "Bookmarks…", action: "browse.bookmarks"})
		add(menuEntry{label: "Storage info", action: "browse.storage"})
		add(settingsMenuEntry())
		entries = append(entries, a.navMenuEntries(p)...)
		entries = append(entries, a.focusMenuEntries()...)
		return title, entries
	}

	if p.InRemote() {
		// an rclone remote: only what works there (see remoteUnsupported)
		if single && e.IsDir {
			add(menuEntry{label: "Open folder", action: "browse.open"})
		}
		if len(targets) > 0 {
			dest := a.panelLabel(1 - a.active)
			label, mdl := "Copy to other panel", "Move to other panel"
			if a.focus {
				label, mdl = "Copy to…", "Move to…"
				dest = "choose a destination · default " + dest
			}
			add(menuEntry{label: label, detail: dest, action: "browse.copy"})
			add(menuEntry{label: mdl, detail: dest, action: "browse.move"})
			if len(targets) == 1 {
				add(menuEntry{label: "Rename", action: "browse.rename"})
			}
		}
		add(menuEntry{label: "New folder…", run: func() tea.Cmd { return a.beginMkdir() }})
		add(menuEntry{label: "Sources…", detail: "local / cloud", action: "browse.sources"})
		add(menuEntry{label: "Refresh", action: "browse.refresh"})
		add(menuEntry{label: "Back to local files", detail: shortPath(a.localDirFor(a.active)), key: "l",
			run: func() tea.Cmd { return p.GoLocal(a.localDirFor(a.active), "") }})
		if len(targets) > 0 {
			add(menuEntry{label: "Delete…", action: "browse.delete", danger: true})
		}
		add(settingsMenuEntry())
		entries = append(entries, a.focusMenuEntries()...)
		return title, entries
	}

	if single {
		switch {
		case e.IsDir:
			add(menuEntry{label: "Open folder", action: "browse.open"})
		case a.isBrowsableArchive(e.Path):
			add(menuEntry{label: "Browse archive (read-only)", action: "browse.open"})
		default:
			if plat, ok := a.preferExternal(e); ok {
				add(menuEntry{label: "Open (default app)", detail: plat.Name, action: "browse.openExternal"})
			} else {
				add(menuEntry{label: "Open (default app)", action: "browse.openExternal"})
			}
		}
		if file {
			add(menuEntry{label: "Open with…", action: "browse.openWith"})
			add(menuEntry{label: "View", action: "browse.view"})
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
			add(menuEntry{label: "Edit", action: "browse.edit"})
		}
		dest := a.panelLabel(1 - a.active)
		copyLabel, moveLabel := "Copy to other panel", "Move to other panel"
		if a.focus {
			copyLabel, moveLabel = "Copy to…", "Move to…"
			dest = "choose a destination · default " + dest
		}
		add(menuEntry{label: copyLabel, detail: dest, action: "browse.copy"})
		add(menuEntry{label: moveLabel, detail: dest, action: "browse.move"})
		if len(targets) > 1 {
			add(menuEntry{label: "Rename (bulk)", action: "browse.rename"})
		} else {
			add(menuEntry{label: "Rename", action: "browse.rename"})
		}
		add(menuEntry{label: "Compress…", detail: "default: " + shortPath(p.Path()), action: "browse.compress"})
		if file && canExtract(e.Path) {
			add(menuEntry{label: "Extract to folder…", action: "browse.extract"})
		}
	}
	if hasCur || nsel > 0 {
		add(menuEntry{label: "Properties", action: "browse.properties"})
	}
	if file {
		if plat := a.platform(); plat.Termux && plat.HasOpener() {
			add(menuEntry{label: "Share…", detail: "Android share sheet", key: "s", run: a.shareFile})
		}
	}
	add(menuEntry{label: "New file…", run: func() tea.Cmd { return a.beginNewFile() }})
	add(menuEntry{label: "New folder…", run: func() tea.Cmd { return a.beginMkdir() }})
	if a.canComparePanels() {
		add(menuEntry{label: "Compare directories", action: "browse.compare"})
	}
	add(menuEntry{label: "Sources…", detail: "local / cloud", action: "browse.sources"})
	if dir := p.Path(); a.isBookmarked(dir) {
		add(menuEntry{label: "Remove this folder's bookmark", key: "B", run: a.unbookmarkHere})
	} else {
		add(menuEntry{label: "Bookmark this folder", action: "browse.bookmark"})
	}
	add(menuEntry{label: "Bookmarks…", action: "browse.bookmarks"})
	add(menuEntry{label: "Storage info", action: "browse.storage"})
	add(menuEntry{label: "Analyze folder sizes", action: "browse.folderSize"})
	add(settingsMenuEntry())
	add(menuEntry{label: "Activity log", detail: "what TwinPane did", action: "browse.activity"})
	entries = append(entries, a.navMenuEntries(p)...)
	entries = append(entries, a.focusMenuEntries()...)
	if len(targets) > 0 {
		add(menuEntry{label: "Delete…", action: "browse.delete", danger: true})
	}
	return title, entries
}

// settingsMenuEntry is the Settings row of the action menu (M or a tap on
// the TwinPane badge). It re-sends F9, so the menu opens the same screen as
// the keyboard shortcut.
func settingsMenuEntry() menuEntry {
	return menuEntry{label: "Settings…", detail: "F9", action: "browse.settings"}
}

// openMenu shows the action menu (M).
func (a *App) openMenu() tea.Cmd {
	title, entries := a.menuEntries()
	d := &dialog{kind: dlgMenu, title: title, width: 50}
	for _, m := range entries {
		k, send := m.key, ""
		if m.action != "" {
			k, send = a.primaryLabel(m.action), a.binds.Primary(m.action)
		}
		if send == "esc" {
			// Esc closed menus of old; keep the row free of a shortcut it
			// never needed to show (the footer says Esc closes).
			k = ""
		}
		d.list = append(d.list, listItem{label: m.label, detail: m.detail, key: k, send: send, danger: m.danger})
	}
	d.footer = "⏎ or letter: run · Esc close"
	d.onPick = func(a *App, i int) tea.Cmd {
		m := entries[i]
		a.dialog = nil
		if m.run != nil {
			return m.run()
		}
		if m.action != "" {
			_, cmd := a.handleKey(a.actionMsg(m.action))
			return cmd
		}
		return nil
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
