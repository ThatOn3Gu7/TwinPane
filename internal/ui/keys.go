package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/keybinding"
)

// Binding maps one action to one or more key strings (as produced by
// tea.KeyMsg.String()) plus help text.
type Binding struct {
	Keys []string
	Help string // short key label for the footer / help screen
	Desc string
}

// Matches reports whether msg triggers the binding.
func (b Binding) Matches(msg tea.KeyMsg) bool {
	s := msg.String()
	for _, k := range b.Keys {
		if k == s {
			return true
		}
	}
	return false
}

// KeyMap lists every action. Phase 2 added the file operations (Copy, Move,
// Delete, Edit, Rename) and Cancel; phase 3 Reverse and Search (and moved
// Hidden to h). Panel code is unaware of key bindings. File operations act
// on the selection if there is one, otherwise on the entry under the cursor.
type KeyMap struct {
	Up, Down, PageUp, PageDown, Top, Bottom            Binding
	Open, Parent, Home, SyncOther, Compare, FolderSize Binding
	Back, Forward                                      Binding
	Select, SelectAll, Unselect                        Binding
	Switch, Hidden, Sort, Reverse, Refresh             Binding
	Search                                             Binding
	Copy, Move, Rename, Delete, Edit                   Binding
	EditWith                                           Binding
	Cancel, Tasks, Help, Quit, Settings                Binding

	// Phase 5
	Properties, View         Binding
	Extract, ExtractAll, Zip Binding

	// Phase 6
	OpenExt, OpenWith, Menu Binding
	Bookmarks, Bookmark     Binding
	Storage                 Binding

	// Phase 9: Sources took Ctrl+R (Refresh moved to Ctrl+L), Mkdir
	Sources, Mkdir Binding

	// Phase 10: n opens the New file / New folder menu; F7 still creates a
	// folder directly.
	New Binding

	// Phase 15: L opens the activity log (what TwinPane did, for
	// troubleshooting). It is not the operation history, which lives in the
	// Tasks screen (t).
	Activity Binding

	// Focus mode: the toggle, the preview pane and the typed location box.
	// They only reach the panels while focus mode is on.
	Focus, Preview, GoTo Binding
}

// DefaultKeys returns the default key bindings.
func DefaultKeys() KeyMap { return keyMapFrom(keybinding.Default()) }

// keyMapFrom builds the browse key map from the active binding set, so a key
// changed in keybinding.json is what the panels listen for and what the help
// screen and the hint bar show.
func keyMapFrom(s *keybinding.Set) KeyMap {
	b := func(name, desc string) Binding {
		return Binding{Keys: s.Keys(name), Help: s.Label(name), Desc: desc}
	}
	return KeyMap{
		Up:       b("browse.up", "move up"),
		Down:     b("browse.down", "move down"),
		PageUp:   b("browse.pageUp", "page up"),
		PageDown: b("browse.pageDown", "page down"),
		Top:      b("browse.top", "first entry"),
		Bottom:   b("browse.bottom", "last entry"),
		Open:     b("browse.open", "open directory"),

		// One help row for both directions keeps the help screen a page;
		// the keys themselves are separate actions (see handleKey).
		Parent:     b("browse.parent", "parent directory"),
		Back:       b("browse.back", "back / forward (history)"),
		Forward:    b("browse.forward", "back / forward (history)"),
		Home:       b("browse.home", "go to home directory"),
		SyncOther:  b("browse.syncOther", "same dir in other panel"),
		Compare:    b("browse.compare", "compare local directories"),
		FolderSize: b("browse.folderSize", "analyze folder sizes"),
		Select:     b("browse.select", "toggle selection"),
		SelectAll:  b("browse.selectAll", "select all / none"),
		Unselect:   b("browse.unselect", "clear selection"),
		Switch:     b("browse.switch", "switch active panel"),
		Hidden:     b("browse.hidden", "hidden files (both panels)"),
		Sort:       b("browse.sort", "sort by name/size/time/type"),
		Reverse:    b("browse.reverse", "reverse sort order (↑/↓)"),
		Search:     b("browse.search", "find files by name (live)"),
		Refresh:    b("browse.refresh", "refresh both panels"),
		Copy:       b("browse.copy", "copy to other panel"),
		Move:       b("browse.move", "move to other panel"),
		Rename:     b("browse.rename", "rename (several: bulk)"),
		Delete:     b("browse.delete", "delete (trash if available)"),
		Edit:       b("browse.edit", "edit in default editor"),
		EditWith:   b("browse.editWith", "edit with… (choose editor)"),
		Cancel:     b("browse.cancel", "cancel dialog / operation"),
		Tasks:      b("browse.tasks", "tasks & operation history"),
		Help:       b("browse.help", "toggle help"),
		Settings:   b("browse.settings", "settings"),
		Quit:       b("browse.quit", "quit"),

		Properties: b("browse.properties", "properties / permissions"),
		View:       b("browse.view", "view file (read-only)"),
		Extract:    b("browse.extract", "extract archive / selected"),
		ExtractAll: b("browse.extractAll", "extract all (in a ZIP)"),
		Zip:        b("browse.compress", "compress (ZIP/TAR/7Z…)"),

		OpenExt:   b("browse.openExternal", "open in default app"),
		OpenWith:  b("browse.openWith", "open with… (choose)"),
		Menu:      b("browse.menu", "menu of actions"),
		Bookmarks: b("browse.bookmarks", "bookmarks (jump/edit)"),
		Bookmark:  b("browse.bookmark", "bookmark this folder"),
		Storage:   b("browse.storage", "storage / free space"),

		Sources: b("browse.sources", "source: local / cloud"),
		Mkdir:   b("browse.mkdir", "new folder (direct)"),
		New:     b("browse.new", "new file / new folder"),

		Activity: b("browse.activity", "activity log"),

		Focus:   b("browse.focus", "focus mode (one panel)"),
		Preview: b("browse.preview", "preview pane (focus mode)"),
		GoTo:    b("browse.goTo", "go to a folder (focus mode)"),
	}
}

// helpSections returns the help screen content: two columns of titled
// groups of bindings.
func (k KeyMap) helpSections() [2][]helpSection {
	return [2][]helpSection{
		{
			{"Navigation", []Binding{k.Switch, k.Up, k.Down, k.PageUp, k.PageDown, k.Top, k.Bottom,
				k.Open, k.Parent, k.Home, k.SyncOther, k.Back}},
			{"Selection", []Binding{k.Select, k.SelectAll, k.Unselect}},
			{"Archives", []Binding{
				{Help: "⏎ on .zip", Desc: "browse (no extraction)"},
				{Help: "⌫ / Esc", Desc: "leave the archive"},
				k.Extract, k.ExtractAll, k.Zip}},
			{"Open & places", []Binding{k.Menu, k.OpenExt, k.OpenWith, k.Bookmarks, k.Bookmark, k.Storage}},
			{"Tasks & history (t)", []Binding{
				{Help: "⏎ / w", Desc: "watch live progress"},
				{Help: "c", Desc: "cancel the selected task"},
				{Help: "[ ]", Desc: "previous / next run"},
				k.Activity}},
		},
		{
			{"File operations", []Binding{k.Copy, k.Move, k.Rename, k.Delete, k.Edit, k.EditWith, k.Properties, k.Cancel}},
			{"View & general", []Binding{k.View, k.Search, k.Hidden, k.Sort, k.Reverse, k.Refresh, k.Tasks, k.Quit, k.Settings, k.Help}},
			{"Folders & cloud (rclone)", []Binding{k.New, k.Mkdir, k.Sources,
				{Help: "c / m", Desc: "upload / download"},
				{Help: "Esc", Desc: "stop a slow listing"}}},
			{"Directory analysis", []Binding{k.FolderSize, k.Compare}},
		},
	}
}

type helpSection struct {
	Title    string
	Bindings []Binding
}

// helpRows returns every binding in help-screen order.
func (k KeyMap) helpRows() []Binding {
	var out []Binding
	for _, col := range k.helpSections() {
		for _, sec := range col {
			out = append(out, sec.Bindings...)
		}
	}
	return out
}

// mouseHelp documents the mouse / touch gestures (see mouse.go). The rows
// are not key bindings; they only appear on the help screen.
var mouseHelp = []Binding{
	{Help: "tap", Desc: "focus panel, move cursor"},
	{Help: "2× tap", Desc: "open folder, ZIP or file"},
	{Help: "swipe", Desc: "scroll the list under it"},
	{Help: "tap ┃", Desc: "jump (scroll indicator)"},
	{Help: "tap bar", Desc: "bottom hints act as keys"},
	{Help: "buttons", Desc: "tap to press (dialogs)"},
	{Help: "r-click", Desc: "mark/unmark (mouse only)"},
	{Help: "tap title", Desc: "action menu (TwinPane)"},
}
