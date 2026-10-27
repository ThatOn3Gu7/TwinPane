package ui

import tea "github.com/charmbracelet/bubbletea"

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
}

// DefaultKeys returns the default key bindings.
func DefaultKeys() KeyMap {
	return KeyMap{
		Up:       Binding{[]string{"up", "k"}, "↑/k", "move up"},
		Down:     Binding{[]string{"down", "j"}, "↓/j", "move down"},
		PageUp:   Binding{[]string{"pgup", "ctrl+b"}, "PgUp", "page up"},
		PageDown: Binding{[]string{"pgdown", "ctrl+f"}, "PgDn", "page down"},
		Top:      Binding{[]string{"home", "g"}, "Home/g", "first entry"},
		Bottom:   Binding{[]string{"end", "G"}, "End/G", "last entry"},
		Open:     Binding{[]string{"enter", "right", "l"}, "⏎/→/l", "open directory"},
		Parent:   Binding{[]string{"backspace", "left"}, "⌫/←", "parent directory"},
		// One help row for both directions keeps the help screen a page;
		// the keys themselves are separate bindings (see handleKey).
		Back:       Binding{[]string{"alt+left", "["}, "[ / ]", "back / forward (history)"},
		Forward:    Binding{[]string{"alt+right", "]"}, "[ / ]", "back / forward (history)"},
		Home:       Binding{[]string{"~"}, "~", "go to home directory"},
		SyncOther:  Binding{[]string{"="}, "=", "same dir in other panel"},
		Compare:    Binding{[]string{"D"}, "D", "compare local directories"},
		FolderSize: Binding{[]string{"A"}, "A", "analyze folder sizes"},
		Select:     Binding{[]string{" ", "insert"}, "Space/Ins", "toggle selection"},
		SelectAll:  Binding{[]string{"a"}, "a", "select all / none"},
		Unselect:   Binding{[]string{"u"}, "u", "clear selection"},
		Switch:     Binding{[]string{"tab", "shift+tab"}, "Tab", "switch active panel"},
		Hidden:     Binding{[]string{"h", "H", "."}, "h/H/.", "hidden files (both panels)"},
		Sort:       Binding{[]string{"s"}, "s", "sort by name/size/time/type"},
		Reverse:    Binding{[]string{"S"}, "S", "reverse sort order (↑/↓)"},
		Search:     Binding{[]string{"/"}, "/", "find files by name (live)"},
		Refresh:    Binding{[]string{"ctrl+l"}, "^L", "refresh both panels"},
		Copy:       Binding{[]string{"c", "f5"}, "c/F5", "copy to other panel"},
		Move:       Binding{[]string{"m", "f6"}, "m/F6", "move to other panel"},
		Rename:     Binding{[]string{"r", "f2"}, "r/F2", "rename (several: bulk)"},
		Delete:     Binding{[]string{"d", "f8", "delete"}, "d/F8/Del", "delete (trash if available)"},
		Edit:       Binding{[]string{"e", "f4"}, "e/F4", "edit in default editor"},
		EditWith:   Binding{[]string{"E"}, "E", "edit with… (choose editor)"},
		Cancel:     Binding{[]string{"esc"}, "Esc", "cancel dialog / operation"},
		Tasks:      Binding{[]string{"t"}, "t", "tasks & operation history"},
		Help:       Binding{[]string{"?", "f1"}, "?", "toggle help"},
		Settings:   Binding{[]string{"f9", "ctrl+s"}, "F9 / ^S", "settings"},
		Quit:       Binding{[]string{"q", "ctrl+c", "f10"}, "q", "quit"},

		Properties: Binding{[]string{"P"}, "P", "properties / permissions"},
		View:       Binding{[]string{"v", "f3"}, "v/F3", "view file (read-only)"},
		Extract:    Binding{[]string{"x"}, "x", "extract archive / selected"},
		ExtractAll: Binding{[]string{"X"}, "X", "extract all (in a ZIP)"},
		Zip:        Binding{[]string{"z"}, "z", "compress (ZIP/TAR/7Z…)"},

		OpenExt:   Binding{[]string{"o"}, "o", "open in default app"},
		OpenWith:  Binding{[]string{"O"}, "O", "open with… (choose)"},
		Menu:      Binding{[]string{"M"}, "M", "menu of actions"},
		Bookmarks: Binding{[]string{"b"}, "b", "bookmarks (jump/edit)"},
		Bookmark:  Binding{[]string{"B"}, "B", "bookmark this folder"},
		Storage:   Binding{[]string{"i"}, "i", "storage / free space"},

		Sources: Binding{[]string{"ctrl+r"}, "^R", "source: local / cloud"},
		Mkdir:   Binding{[]string{"f7"}, "F7", "new folder (direct)"},
		New:     Binding{[]string{"n"}, "n", "new file / new folder"},
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
			{"Background tasks (t)", []Binding{
				{Help: "⏎ / w", Desc: "watch live progress"},
				{Help: "c", Desc: "cancel the selected task"},
				{Help: "x", Desc: "clear finished tasks"},
				{Help: "h", Desc: "operation history"}}},
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
