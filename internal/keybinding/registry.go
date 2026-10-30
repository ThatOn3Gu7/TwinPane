// Package keybinding defines every key TwinPane understands, loads the user's
// overrides from keybinding.json and answers "does this key mean that action?".
//
// The package knows nothing about Bubble Tea's model: it returns tea.KeyMsg
// values only because that is how the interface identifies a key press. It has
// no I/O beyond reading and writing its own file.
//
// Actions are named "<section>.<action>" (browse.copy, viewer.wrap,
// dialog.cancel). A section is one keyboard context; the same key may mean
// different things in different sections (browse q quits, viewer q closes the
// viewer), which is not a conflict. Within one section a key belongs to one
// action unless the registry marks an action as shadowing (context-dependent
// keys such as compare.retry, which reuses r only after a scan failed).
package keybinding

// Action is one bindable command: its name, the section it belongs to, the
// default keys and the text the help screen and the documentation use.
type Action struct {
	Name    string   // "browse.copy"
	Section string   // "browse"
	Keys    []string // defaults, in display order
	Desc    string   // short description ("copy to the other panel")
	// Label is the compact label the help screen shows while the action still
	// has its default keys. A customized action is labelled from its real
	// keys instead, so the help never shows a key that no longer works.
	Label string
	// Hint, when set, replaces Desc in the comment above the action in
	// keybinding.json: it is written for a reader who does not know the
	// program yet, so it adds the context the short help text leaves out
	// ("selects it and moves down", "only in focus mode").
	Hint string
	// Shadow marks an action whose keys are allowed to repeat another action's
	// keys inside the same section. The dispatch order decides which one runs,
	// exactly as it did when the keys were hard-coded (compare.retry is only
	// consulted when the scan failed).
	Shadow bool
	// Essential actions may never end up with no keys: restoring the defaults
	// is the fallback, so a hand-edited file cannot lock the user inside a
	// screen that only Escape leaves.
	Essential bool
}

// Sections in file order. The order is the registry order, so a generated
// keybinding.json reads like the program: browsing first, then each screen.
const (
	SecBrowse      = "browse"
	SecDialog      = "dialog"
	SecList        = "list"
	SecFilterList  = "filterList"
	SecInput       = "input"
	SecViewer      = "viewer"
	SecSearch      = "search"
	SecSearchList  = "searchList"
	SecAdvanced    = "advanced"
	SecTasks       = "tasks"
	SecActivity    = "activity"
	SecProps       = "props"
	SecBulkRename  = "bulkRename"
	SecHelp        = "help"
	SecSettings    = "settings"
	SecCompare     = "compare"
	SecComparePlan = "comparePlan"
	SecFolderSize  = "folderSize"
	SecLive        = "live"
)

// sectionTitles name the sections in the generated file's "about" text and in
// the documentation.
var sectionTitles = map[string]string{
	SecBrowse:      "the two panels",
	SecDialog:      "dialog boxes (buttons and scrolling)",
	SecList:        "pick lists inside dialogs",
	SecFilterList:  "pick lists with a filter field",
	SecInput:       "text fields",
	SecViewer:      "the file viewer",
	SecSearch:      "the live search box",
	SecSearchList:  "the live search result list",
	SecAdvanced:    "Advanced Search",
	SecTasks:       "the Tasks screen",
	SecActivity:    "the activity log",
	SecProps:       "Properties",
	SecBulkRename:  "bulk rename",
	SecHelp:        "the help screen",
	SecSettings:    "the Settings screen",
	SecCompare:     "directory comparison",
	SecComparePlan: "the reviewed comparison plan",
	SecFolderSize:  "folder-size analysis",
	SecLive:        "the live progress window",
}

// actions is the registry. Order matters twice: it is the order of the file
// and the precedence when two actions of a section somehow share a key.
var actions = []Action{
	// ---- the two panels ----------------------------------------------------
	{Name: "browse.up", Section: SecBrowse, Keys: []string{"up", "k"}, Desc: "move up", Label: "↑/k", Hint: "moves the cursor to the entry above"},
	{Name: "browse.down", Section: SecBrowse, Keys: []string{"down", "j"}, Desc: "move down", Label: "↓/j", Hint: "moves the cursor to the entry below"},
	{Name: "browse.pageUp", Section: SecBrowse, Keys: []string{"pgup", "ctrl+b"}, Desc: "page up", Label: "PgUp", Hint: "moves the cursor up by one screenful"},
	{Name: "browse.pageDown", Section: SecBrowse, Keys: []string{"pgdown", "ctrl+f"}, Desc: "page down", Label: "PgDn", Hint: "moves the cursor down by one screenful"},
	{Name: "browse.top", Section: SecBrowse, Keys: []string{"home", "g"}, Desc: "first entry", Label: "Home/g", Hint: "jumps to the first entry of the folder"},
	{Name: "browse.bottom", Section: SecBrowse, Keys: []string{"end", "G"}, Desc: "last entry", Label: "End/G", Hint: "jumps to the last entry of the folder"},
	{Name: "browse.open", Section: SecBrowse, Keys: []string{"enter", "right", "l"}, Desc: "open directory", Label: "⏎/→/l", Hint: "enters a folder, opens a ZIP/TAR archive read-only, or views a file"},
	{Name: "browse.parent", Section: SecBrowse, Keys: []string{"backspace", "left"}, Desc: "parent directory", Label: "⌫/←", Hint: "goes up to the folder that contains the current one"},
	{Name: "browse.back", Section: SecBrowse, Keys: []string{"[", "alt+left"}, Desc: "back (history)", Label: "[ / ]", Hint: "goes back to the folder you were in before, in this panel only"},
	{Name: "browse.forward", Section: SecBrowse, Keys: []string{"]", "alt+right"}, Desc: "forward (history)", Label: "[ / ]", Hint: "goes forward again after Back, in this panel only"},
	{Name: "browse.home", Section: SecBrowse, Keys: []string{"~"}, Desc: "go to home directory", Label: "~", Hint: "jumps to your home folder"},
	{Name: "browse.syncOther", Section: SecBrowse, Keys: []string{"="}, Desc: "same dir in other panel", Label: "=", Hint: "shows the focused panel's folder in the other panel"},
	{Name: "browse.compare", Section: SecBrowse, Keys: []string{"D"}, Desc: "compare local directories", Label: "D", Hint: "compares the two folders side by side, without changing anything"},
	{Name: "browse.folderSize", Section: SecBrowse, Keys: []string{"A"}, Desc: "analyze folder sizes", Label: "A", Hint: "measures how much space the folder (or the selection) uses"},
	{Name: "browse.select", Section: SecBrowse, Keys: []string{" ", "insert"}, Desc: "toggle selection", Label: "Space/Ins", Hint: "marks the entry for copy/move/delete and moves down; press again to unmark"},
	{Name: "browse.selectAll", Section: SecBrowse, Keys: []string{"a"}, Desc: "select all / none", Label: "a", Hint: "marks every entry, or unmarks them all when something is marked"},
	{Name: "browse.unselect", Section: SecBrowse, Keys: []string{"u"}, Desc: "clear selection", Label: "u", Hint: "unmarks everything that was marked"},
	{Name: "browse.switch", Section: SecBrowse, Keys: []string{"tab", "shift+tab"}, Desc: "switch active panel", Label: "Tab", Hint: "gives the keyboard to the other panel"},
	{Name: "browse.hidden", Section: SecBrowse, Keys: []string{"h", "H", "."}, Desc: "hidden files (both panels)", Label: "h/H/.", Hint: "shows or hides dot-files in both panels"},
	{Name: "browse.sort", Section: SecBrowse, Keys: []string{"s"}, Desc: "sort by name/size/time/type", Label: "s", Hint: "changes what the list is sorted by (name, size, time, type)"},
	{Name: "browse.reverse", Section: SecBrowse, Keys: []string{"S"}, Desc: "reverse sort order (↑/↓)", Label: "S", Hint: "flips the sort order between ascending and descending"},
	{Name: "browse.search", Section: SecBrowse, Keys: []string{"/"}, Desc: "find files by name (live)", Label: "/", Hint: "searches file names in this folder and below, while you type"},
	{Name: "browse.refresh", Section: SecBrowse, Keys: []string{"ctrl+l"}, Desc: "refresh both panels", Label: "^L", Hint: "reads both folders again from disk"},
	{Name: "browse.copy", Section: SecBrowse, Keys: []string{"c", "f5"}, Desc: "copy to the other panel", Label: "c/F5", Hint: "copies the marked entries (or the cursor's) to the other panel; nothing is overwritten without asking"},
	{Name: "browse.move", Section: SecBrowse, Keys: []string{"m", "f6"}, Desc: "move to the other panel", Label: "m/F6", Hint: "moves the marked entries (or the one under the cursor) to the other panel"},
	{Name: "browse.rename", Section: SecBrowse, Keys: []string{"r", "f2"}, Desc: "rename", Label: "r/F2", Hint: "renames the entry under the cursor; with several marked it opens bulk rename"},
	{Name: "browse.delete", Section: SecBrowse, Keys: []string{"d", "f8", "delete"}, Desc: "delete", Label: "d/F8/Del", Hint: "deletes the marked entries (or the cursor's) after a confirmation, using the trash when there is one"},
	{Name: "browse.edit", Section: SecBrowse, Keys: []string{"e", "f4"}, Desc: "edit in the default editor", Label: "e/F4", Hint: "opens the file in your text editor"},
	{Name: "browse.editWith", Section: SecBrowse, Keys: []string{"E"}, Desc: "edit with (choose editor)", Label: "E", Hint: "asks which installed editor to open the file with"},
	{Name: "browse.cancel", Section: SecBrowse, Keys: []string{"esc"}, Desc: "cancel dialog / operation", Label: "Esc", Essential: true, Hint: "closes the dialog or stops the operation that is open; it never closes the panels"},
	{Name: "browse.tasks", Section: SecBrowse, Keys: []string{"t"}, Desc: "tasks & operation history", Label: "t", Hint: "shows the background operations and their history"},
	{Name: "browse.help", Section: SecBrowse, Keys: []string{"?", "f1"}, Desc: "toggle help", Label: "?", Hint: "shows the key reference you are reading now"},
	{Name: "browse.settings", Section: SecBrowse, Keys: []string{"f9", "ctrl+s"}, Desc: "settings", Label: "F9 / ^S", Hint: "opens the settings window (theme, sort, editor, keys, log, ...)"},
	{Name: "browse.quit", Section: SecBrowse, Keys: []string{"q", "f10"}, Desc: "quit", Label: "q", Essential: true, Hint: "leaves TwinPane when nothing is running"},
	{Name: "browse.interrupt", Section: SecBrowse, Keys: []string{"ctrl+c"}, Desc: "cancel the running task / quit", Label: "^C", Essential: true, Hint: "cancels the latest background operation; with none running it quits"},
	{Name: "browse.properties", Section: SecBrowse, Keys: []string{"P"}, Desc: "properties / permissions", Label: "P", Hint: "shows size, dates, owner and permissions and lets you change them"},
	{Name: "browse.view", Section: SecBrowse, Keys: []string{"v", "f3"}, Desc: "view file (read-only)", Label: "v/F3", Hint: "shows the file read-only inside TwinPane"},
	{Name: "browse.extract", Section: SecBrowse, Keys: []string{"x"}, Desc: "extract archive / selected", Label: "x", Hint: "unpacks the archive under the cursor, or the marked entries of an open archive"},
	{Name: "browse.extractAll", Section: SecBrowse, Keys: []string{"X"}, Desc: "extract all (in an archive)", Label: "X", Hint: "unpacks everything in the archive that is open"},
	{Name: "browse.compress", Section: SecBrowse, Keys: []string{"z"}, Desc: "compress (ZIP/TAR/7Z)", Label: "z", Hint: "packs the marked entries (or the one under the cursor) into an archive"},
	{Name: "browse.openExternal", Section: SecBrowse, Keys: []string{"o"}, Desc: "open in default app", Label: "o", Hint: "opens the file in the app that handles it outside TwinPane"},
	{Name: "browse.openWith", Section: SecBrowse, Keys: []string{"O"}, Desc: "open with (choose)", Label: "O", Hint: "asks which installed program to open the file with"},
	{Name: "browse.menu", Section: SecBrowse, Keys: []string{"M"}, Desc: "menu of actions", Label: "M", Hint: "shows the action menu for the entry under the cursor"},
	{Name: "browse.bookmarks", Section: SecBrowse, Keys: []string{"b"}, Desc: "bookmarks (jump/edit)", Label: "b", Hint: "lists your saved folders so you can jump to one"},
	{Name: "browse.bookmark", Section: SecBrowse, Keys: []string{"B"}, Desc: "bookmark this folder", Label: "B", Hint: "saves the current folder to your bookmarks"},
	{Name: "browse.storage", Section: SecBrowse, Keys: []string{"i"}, Desc: "storage / free space", Label: "i", Hint: "shows free space and the mounted disks"},
	{Name: "browse.sources", Section: SecBrowse, Keys: []string{"ctrl+r"}, Desc: "source: local / cloud", Label: "^R", Hint: "switches the focused panel between local files and cloud (rclone) remotes"},
	{Name: "browse.mkdir", Section: SecBrowse, Keys: []string{"f7"}, Desc: "new folder (direct)", Label: "F7", Hint: "creates a folder after asking for its name"},
	{Name: "browse.new", Section: SecBrowse, Keys: []string{"n"}, Desc: "new file / new folder", Label: "n", Hint: "opens the New file / New folder menu"},
	{Name: "browse.activity", Section: SecBrowse, Keys: []string{"L"}, Desc: "activity log", Label: "L", Hint: "shows what TwinPane did, for troubleshooting"},
	{Name: "browse.focus", Section: SecBrowse, Keys: []string{"ctrl+o"}, Desc: "focus mode (one panel)", Label: "Ctrl+O", Hint: "shows one panel at full width, and back to two panels"},
	{Name: "browse.preview", Section: SecBrowse, Keys: []string{"p"}, Desc: "preview pane (focus mode)", Label: "p", Hint: "turns the details preview pane on or off (only in focus mode)"},
	{Name: "browse.goTo", Section: SecBrowse, Keys: []string{"ctrl+g"}, Desc: "go to a folder (focus mode)", Label: "Ctrl+G", Hint: "asks for a folder or a remote location to jump to (only in focus mode)"},

	// ---- dialogs -----------------------------------------------------------
	{Name: "dialog.cancel", Section: SecDialog, Keys: []string{"esc", "ctrl+c"}, Desc: "cancel / close", Essential: true, Hint: "closes the dialog without doing anything (or stops the operation it reports)"},
	{Name: "dialog.confirm", Section: SecDialog, Keys: []string{"enter"}, Desc: "press the highlighted button", Hint: "presses the highlighted button"},
	{Name: "dialog.next", Section: SecDialog, Keys: []string{"tab", "right"}, Desc: "next button", Hint: "moves the highlight to the next button"},
	{Name: "dialog.prev", Section: SecDialog, Keys: []string{"shift+tab", "left"}, Desc: "previous button", Hint: "moves the highlight to the previous button"},
	{Name: "dialog.scrollUp", Section: SecDialog, Keys: []string{"up", "k"}, Desc: "scroll up", Hint: "scrolls a long dialog text up"},
	{Name: "dialog.scrollDown", Section: SecDialog, Keys: []string{"down", "j"}, Desc: "scroll down", Hint: "scrolls a long dialog text down"},
	{Name: "dialog.pageUp", Section: SecDialog, Keys: []string{"pgup"}, Desc: "page up", Hint: "scrolls a long dialog text up by a page"},
	{Name: "dialog.pageDown", Section: SecDialog, Keys: []string{"pgdown", " "}, Desc: "page down", Hint: "scrolls a long dialog text down by a page"},

	// ---- pick lists --------------------------------------------------------
	{Name: "list.up", Section: SecList, Keys: []string{"up", "k", "shift+tab", "ctrl+p"}, Desc: "previous item", Hint: "moves the highlight to the previous row"},
	{Name: "list.down", Section: SecList, Keys: []string{"down", "j", "tab", "ctrl+n"}, Desc: "next item", Hint: "moves the highlight to the next row"},
	{Name: "list.first", Section: SecList, Keys: []string{"home", "g"}, Desc: "first item", Hint: "jumps to the first row"},
	{Name: "list.last", Section: SecList, Keys: []string{"end", "G"}, Desc: "last item", Hint: "jumps to the last row"},
	{Name: "list.open", Section: SecList, Keys: []string{"enter", "right", "l"}, Desc: "choose the item", Hint: "chooses the highlighted row"},
	{Name: "list.close", Section: SecList, Keys: []string{"q"}, Desc: "close the list", Hint: "closes the list without choosing anything"},
	{Name: "filterList.up", Section: SecFilterList, Keys: []string{"up", "ctrl+p"}, Desc: "previous item", Hint: "moves the highlight to the previous row"},
	{Name: "filterList.down", Section: SecFilterList, Keys: []string{"down", "ctrl+n", "tab"}, Desc: "next item", Hint: "moves the highlight to the next row"},
	{Name: "filterList.pageUp", Section: SecFilterList, Keys: []string{"pgup"}, Desc: "page up", Hint: "moves the highlight up by a page"},
	{Name: "filterList.pageDown", Section: SecFilterList, Keys: []string{"pgdown"}, Desc: "page down", Hint: "moves the highlight down by a page"},
	{Name: "filterList.first", Section: SecFilterList, Keys: []string{"home"}, Desc: "first item", Hint: "jumps to the first row"},
	{Name: "filterList.last", Section: SecFilterList, Keys: []string{"end"}, Desc: "last item", Hint: "jumps to the last row"},
	{Name: "filterList.pick", Section: SecFilterList, Keys: []string{"enter"}, Desc: "choose the item", Hint: "chooses the highlighted row"},

	// ---- text fields -------------------------------------------------------
	{Name: "input.backspace", Section: SecInput, Keys: []string{"backspace", "ctrl+h"}, Desc: "delete the previous character", Hint: "deletes the character before the cursor"},
	{Name: "input.delete", Section: SecInput, Keys: []string{"delete", "ctrl+d"}, Desc: "delete the next character", Hint: "deletes the character after the cursor"},
	{Name: "input.left", Section: SecInput, Keys: []string{"left", "ctrl+b"}, Desc: "cursor left", Hint: "moves the text cursor one character left"},
	{Name: "input.right", Section: SecInput, Keys: []string{"right", "ctrl+f"}, Desc: "cursor right", Hint: "moves the text cursor one character right"},
	{Name: "input.home", Section: SecInput, Keys: []string{"home", "ctrl+a"}, Desc: "start of the field", Hint: "moves the text cursor to the start of the line"},
	{Name: "input.end", Section: SecInput, Keys: []string{"end", "ctrl+e"}, Desc: "end of the field", Hint: "moves the text cursor to the end of the line"},
	{Name: "input.deleteToStart", Section: SecInput, Keys: []string{"ctrl+u"}, Desc: "delete to the start", Hint: "deletes everything before the cursor"},
	{Name: "input.deleteToEnd", Section: SecInput, Keys: []string{"ctrl+k"}, Desc: "delete to the end", Hint: "deletes everything after the cursor"},
	{Name: "input.deleteWord", Section: SecInput, Keys: []string{"ctrl+w", "alt+backspace"}, Desc: "delete the previous word", Hint: "deletes the word before the cursor"},

	// ---- viewer ------------------------------------------------------------
	{Name: "viewer.close", Section: SecViewer, Keys: []string{"esc", "q", "f3", "f10", "v", "backspace"}, Desc: "close the viewer", Essential: true, Hint: "closes the viewer and returns to the panels"},
	{Name: "viewer.help", Section: SecViewer, Keys: []string{"?", "f1"}, Desc: "help", Hint: "shows the key reference"},
	{Name: "viewer.edit", Section: SecViewer, Keys: []string{"e", "f4"}, Desc: "edit the file", Hint: "opens the file in your editor"},
	{Name: "viewer.scrollDown", Section: SecViewer, Keys: []string{"down", "j", "enter"}, Desc: "scroll down", Hint: "scrolls down one line"},
	{Name: "viewer.scrollUp", Section: SecViewer, Keys: []string{"up", "k"}, Desc: "scroll up", Hint: "scrolls up one line"},
	{Name: "viewer.pageDown", Section: SecViewer, Keys: []string{"pgdown", "ctrl+f", " ", "ctrl+d"}, Desc: "page down", Hint: "scrolls down one screen"},
	{Name: "viewer.pageUp", Section: SecViewer, Keys: []string{"pgup", "ctrl+b", "ctrl+u"}, Desc: "page up", Hint: "scrolls up one screen"},
	{Name: "viewer.top", Section: SecViewer, Keys: []string{"home", "g"}, Desc: "first line", Hint: "jumps to the start of the file"},
	{Name: "viewer.bottom", Section: SecViewer, Keys: []string{"end", "G"}, Desc: "last line", Hint: "jumps to the end of the file"},
	{Name: "viewer.right", Section: SecViewer, Keys: []string{"right", "l"}, Desc: "scroll right (no wrap)", Hint: "scrolls sideways to the right when wrapping is off"},
	{Name: "viewer.left", Section: SecViewer, Keys: []string{"left", "h"}, Desc: "scroll left (no wrap)", Hint: "scrolls sideways to the left when wrapping is off"},
	{Name: "viewer.wrap", Section: SecViewer, Keys: []string{"w"}, Desc: "line wrapping", Hint: "turns line wrapping on or off"},
	{Name: "viewer.numbers", Section: SecViewer, Keys: []string{"#"}, Desc: "line numbers", Hint: "shows or hides line numbers"},
	{Name: "viewer.find", Section: SecViewer, Keys: []string{"/"}, Desc: "find in the file", Hint: "asks for text to find in the file"},
	{Name: "viewer.nextMatch", Section: SecViewer, Keys: []string{"n"}, Desc: "next match", Hint: "jumps to the next place the text was found"},
	{Name: "viewer.prevMatch", Section: SecViewer, Keys: []string{"N"}, Desc: "previous match", Hint: "jumps to the previous place the text was found"},

	// ---- live search -------------------------------------------------------
	{Name: "search.close", Section: SecSearch, Keys: []string{"esc"}, Desc: "close the search", Essential: true, Hint: "closes the search box and stops the search"},
	{Name: "search.quit", Section: SecSearch, Keys: []string{"ctrl+c"}, Desc: "quit", Hint: "leaves TwinPane"},
	{Name: "search.advanced", Section: SecSearch, Keys: []string{"ctrl+g"}, Desc: "Advanced Search", Hint: "opens Advanced Search for names or file contents"},
	{Name: "search.jump", Section: SecSearch, Keys: []string{"enter"}, Desc: "jump to the result", Hint: "closes the search and puts the cursor on the result"},
	{Name: "search.toggleFocus", Section: SecSearch, Keys: []string{"tab", "shift+tab"}, Desc: "text field / result list", Hint: "switches between the text field and the result list"},
	{Name: "search.up", Section: SecSearch, Keys: []string{"up", "ctrl+p"}, Desc: "previous result", Hint: "moves the highlight to the previous result"},
	{Name: "search.down", Section: SecSearch, Keys: []string{"down", "ctrl+n"}, Desc: "next result", Hint: "moves the highlight to the next result"},
	{Name: "search.pageUp", Section: SecSearch, Keys: []string{"pgup"}, Desc: "page up", Hint: "moves the highlight up by a page"},
	{Name: "search.pageDown", Section: SecSearch, Keys: []string{"pgdown"}, Desc: "page down", Hint: "moves the highlight down by a page"},
	{Name: "search.edit", Section: SecSearch, Keys: []string{"f4"}, Desc: "edit the selected file", Hint: "edits the highlighted file"},

	{Name: "searchList.up", Section: SecSearchList, Keys: []string{"k"}, Desc: "previous result", Hint: "moves the highlight to the previous result"},
	{Name: "searchList.down", Section: SecSearchList, Keys: []string{"j"}, Desc: "next result", Hint: "moves the highlight to the next result"},
	{Name: "searchList.first", Section: SecSearchList, Keys: []string{"home", "g"}, Desc: "first result", Hint: "jumps to the first result"},
	{Name: "searchList.last", Section: SecSearchList, Keys: []string{"end", "G"}, Desc: "last result", Hint: "jumps to the last result"},
	{Name: "searchList.open", Section: SecSearchList, Keys: []string{"right", "l"}, Desc: "open the result", Hint: "jumps to the highlighted result"},
	{Name: "searchList.select", Section: SecSearchList, Keys: []string{" ", "insert"}, Desc: "mark the result", Hint: "marks the highlighted result, for editing several at once"},
	{Name: "searchList.edit", Section: SecSearchList, Keys: []string{"e"}, Desc: "edit the result", Hint: "edits the highlighted file"},
	{Name: "searchList.editWith", Section: SecSearchList, Keys: []string{"E"}, Desc: "edit with", Hint: "asks which installed editor to open the file with"},
	{Name: "searchList.close", Section: SecSearchList, Keys: []string{"q"}, Desc: "close the search", Hint: "closes the search box"},
	{Name: "searchList.back", Section: SecSearchList, Keys: []string{"/", "i"}, Desc: "back to the text field", Hint: "goes back to the text field to keep typing"},
	{Name: "searchList.backspace", Section: SecSearchList, Keys: []string{"backspace"}, Desc: "back to the text field and delete", Hint: "goes back to the text field and deletes a character"},

	// ---- advanced search ---------------------------------------------------
	{Name: "advanced.close", Section: SecAdvanced, Keys: []string{"esc"}, Desc: "close Advanced Search", Essential: true, Hint: "closes Advanced Search and stops the scan"},
	{Name: "advanced.quit", Section: SecAdvanced, Keys: []string{"ctrl+c"}, Desc: "quit", Hint: "leaves TwinPane"},
	{Name: "advanced.mode", Section: SecAdvanced, Keys: []string{"ctrl+t"}, Desc: "filename / content", Hint: "switches between searching names and searching file contents"},
	{Name: "advanced.actions", Section: SecAdvanced, Keys: []string{"ctrl+a"}, Desc: "actions for the result", Hint: "shows what can be done with the highlighted result"},
	{Name: "advanced.help", Section: SecAdvanced, Keys: []string{"?", "f1"}, Desc: "help", Hint: "shows the key reference"},
	{Name: "advanced.open", Section: SecAdvanced, Keys: []string{"enter"}, Desc: "inspect the result", Hint: "inspects the highlighted result"},
	{Name: "advanced.up", Section: SecAdvanced, Keys: []string{"up", "ctrl+p"}, Desc: "previous result", Hint: "moves the highlight to the previous result"},
	{Name: "advanced.down", Section: SecAdvanced, Keys: []string{"down", "ctrl+n"}, Desc: "next result", Hint: "moves the highlight to the next result"},
	{Name: "advanced.pageUp", Section: SecAdvanced, Keys: []string{"pgup"}, Desc: "page up", Hint: "moves the highlight up by a page"},
	{Name: "advanced.pageDown", Section: SecAdvanced, Keys: []string{"pgdown"}, Desc: "page down", Hint: "moves the highlight down by a page"},
	{Name: "advanced.first", Section: SecAdvanced, Keys: []string{"home"}, Desc: "first result", Hint: "jumps to the first result"},
	{Name: "advanced.last", Section: SecAdvanced, Keys: []string{"end"}, Desc: "last result", Hint: "jumps to the last result"},

	// ---- tasks -------------------------------------------------------------
	{Name: "tasks.close", Section: SecTasks, Keys: []string{"esc", "t"}, Desc: "close the task view", Essential: true, Hint: "leaves the task view; running operations keep running"},
	{Name: "tasks.up", Section: SecTasks, Keys: []string{"up", "k"}, Desc: "previous task", Hint: "moves the highlight to the previous task"},
	{Name: "tasks.down", Section: SecTasks, Keys: []string{"down", "j"}, Desc: "next task", Hint: "moves the highlight to the next task"},
	{Name: "tasks.prevRun", Section: SecTasks, Keys: []string{"["}, Desc: "previous run", Hint: "jumps to the previous TwinPane run in the history"},
	{Name: "tasks.nextRun", Section: SecTasks, Keys: []string{"]"}, Desc: "next run", Hint: "jumps to the next TwinPane run in the history"},
	{Name: "tasks.pageUp", Section: SecTasks, Keys: []string{"pgup", "ctrl+b"}, Desc: "scroll the details up", Hint: "scrolls the details of the selected task up"},
	{Name: "tasks.pageDown", Section: SecTasks, Keys: []string{"pgdown", "ctrl+f"}, Desc: "scroll the details down", Hint: "scrolls the details of the selected task down"},
	{Name: "tasks.watch", Section: SecTasks, Keys: []string{"enter", "w"}, Desc: "watch the task live", Hint: "follows the selected operation as it runs"},
	{Name: "tasks.cancel", Section: SecTasks, Keys: []string{"c", "ctrl+c"}, Desc: "cancel the task", Hint: "cancels the selected operation"},
	{Name: "tasks.forget", Section: SecTasks, Keys: []string{"x"}, Desc: "clear finished tasks", Hint: "clears the finished operations of this run"},
	{Name: "tasks.moreRuns", Section: SecTasks, Keys: []string{"+", "="}, Desc: "keep more runs", Hint: "keeps more runs in the operation history"},
	{Name: "tasks.fewerRuns", Section: SecTasks, Keys: []string{"-", "_"}, Desc: "keep fewer runs", Hint: "keeps fewer runs in the operation history"},

	// ---- activity log ------------------------------------------------------
	{Name: "activity.close", Section: SecActivity, Keys: []string{"esc", "L", "q"}, Desc: "close the log", Essential: true, Hint: "leaves the activity log"},
	{Name: "activity.up", Section: SecActivity, Keys: []string{"up", "k"}, Desc: "previous entry", Hint: "moves the highlight to the previous entry"},
	{Name: "activity.down", Section: SecActivity, Keys: []string{"down", "j"}, Desc: "next entry", Hint: "moves the highlight to the next entry"},
	{Name: "activity.pageUp", Section: SecActivity, Keys: []string{"pgup", "ctrl+b"}, Desc: "page up", Hint: "moves the highlight up by a page"},
	{Name: "activity.pageDown", Section: SecActivity, Keys: []string{"pgdown", "ctrl+f"}, Desc: "page down", Hint: "moves the highlight down by a page"},
	{Name: "activity.first", Section: SecActivity, Keys: []string{"home", "g"}, Desc: "first entry", Hint: "jumps to the oldest entry"},
	{Name: "activity.last", Section: SecActivity, Keys: []string{"end", "G"}, Desc: "last entry", Hint: "jumps to the newest entry"},
	{Name: "activity.category", Section: SecActivity, Keys: []string{"c"}, Desc: "category filter", Hint: "filters the log by category (navigation, files, cloud, ...)"},
	{Name: "activity.level", Section: SecActivity, Keys: []string{"l"}, Desc: "level filter", Hint: "filters the log by level (info, warning, error)"},
	{Name: "activity.prevRun", Section: SecActivity, Keys: []string{"["}, Desc: "previous run", Hint: "jumps to the previous TwinPane run"},
	{Name: "activity.nextRun", Section: SecActivity, Keys: []string{"]"}, Desc: "next run", Hint: "jumps to the next TwinPane run"},
	{Name: "activity.details", Section: SecActivity, Keys: []string{"enter", " "}, Desc: "show the details", Hint: "shows or hides the details of the selected entry"},
	{Name: "activity.filter", Section: SecActivity, Keys: []string{"/"}, Desc: "filter the log", Hint: "types text to filter the log by"},
	{Name: "activity.clear", Section: SecActivity, Keys: []string{"x"}, Desc: "clear the filters", Hint: "removes the filters and shows everything again"},
	{Name: "activity.reload", Section: SecActivity, Keys: []string{"r"}, Desc: "reload the log", Hint: "reads the log file again"},
	{Name: "activity.help", Section: SecActivity, Keys: []string{"?"}, Desc: "help", Hint: "shows the key reference"},

	// ---- properties --------------------------------------------------------
	{Name: "props.close", Section: SecProps, Keys: []string{"esc"}, Desc: "close without applying", Essential: true, Hint: "closes Properties without saving the changes"},
	{Name: "props.quit", Section: SecProps, Keys: []string{"ctrl+c"}, Desc: "quit", Hint: "leaves TwinPane"},
	{Name: "props.nextField", Section: SecProps, Keys: []string{"tab"}, Desc: "next field", Hint: "moves to the next field (the name, the permissions, Apply, Close)"},
	{Name: "props.prevField", Section: SecProps, Keys: []string{"shift+tab"}, Desc: "previous field", Hint: "moves to the previous field"},
	{Name: "props.apply", Section: SecProps, Keys: []string{"enter"}, Desc: "apply the changes", Shadow: true, Hint: "saves the new name and permissions"},
	{Name: "props.press", Section: SecProps, Keys: []string{"enter", " "}, Desc: "press the focused button", Shadow: true, Hint: "presses the highlighted button (Apply or Close)"},
	{Name: "props.toggle", Section: SecProps, Keys: []string{" ", "x"}, Desc: "toggle a permission", Shadow: true, Hint: "allows or forbids that permission"},
	{Name: "props.left", Section: SecProps, Keys: []string{"left", "h"}, Desc: "move left", Hint: "moves one cell left in the permission grid"},
	{Name: "props.right", Section: SecProps, Keys: []string{"right", "l"}, Desc: "move right", Hint: "moves one cell right in the permission grid"},
	{Name: "props.up", Section: SecProps, Keys: []string{"up", "k"}, Desc: "move up", Hint: "moves one cell up in the permission grid"},
	{Name: "props.down", Section: SecProps, Keys: []string{"down", "j"}, Desc: "move down", Hint: "moves one cell down in the permission grid"},
	{Name: "props.closeReadOnly", Section: SecProps, Keys: []string{"q"}, Desc: "close a read-only view", Hint: "closes a Properties window you may not change"},

	// ---- bulk rename -------------------------------------------------------
	{Name: "bulkRename.cancel", Section: SecBulkRename, Keys: []string{"esc", "q"}, Desc: "cancel (nothing is renamed)", Essential: true, Hint: "closes bulk rename; nothing is renamed"},
	{Name: "bulkRename.quit", Section: SecBulkRename, Keys: []string{"ctrl+c"}, Desc: "quit", Hint: "leaves TwinPane"},
	{Name: "bulkRename.up", Section: SecBulkRename, Keys: []string{"up", "k"}, Desc: "previous row", Hint: "moves to the previous file"},
	{Name: "bulkRename.down", Section: SecBulkRename, Keys: []string{"down", "j"}, Desc: "next row", Hint: "moves to the next file"},
	{Name: "bulkRename.pageUp", Section: SecBulkRename, Keys: []string{"pgup", "ctrl+b"}, Desc: "page up", Hint: "moves up by a page"},
	{Name: "bulkRename.pageDown", Section: SecBulkRename, Keys: []string{"pgdown", "ctrl+f"}, Desc: "page down", Hint: "moves down by a page"},
	{Name: "bulkRename.first", Section: SecBulkRename, Keys: []string{"home", "g"}, Desc: "first row", Hint: "jumps to the first file"},
	{Name: "bulkRename.last", Section: SecBulkRename, Keys: []string{"end", "G"}, Desc: "last row", Hint: "jumps to the last file"},
	{Name: "bulkRename.editRow", Section: SecBulkRename, Keys: []string{"enter", "r", "f2"}, Desc: "edit the new name", Hint: "types a new name for the highlighted file"},
	{Name: "bulkRename.find", Section: SecBulkRename, Keys: []string{"f"}, Desc: "find and replace", Hint: "replaces text in all new names at once"},
	{Name: "bulkRename.pattern", Section: SecBulkRename, Keys: []string{"n"}, Desc: "rename pattern", Hint: "builds new names from a pattern with numbering"},
	{Name: "bulkRename.lower", Section: SecBulkRename, Keys: []string{"L"}, Desc: "lower-case all names", Hint: "makes all new names lower-case"},
	{Name: "bulkRename.upper", Section: SecBulkRename, Keys: []string{"U"}, Desc: "upper-case all names", Hint: "makes all new names upper-case"},
	{Name: "bulkRename.reset", Section: SecBulkRename, Keys: []string{"R"}, Desc: "reset to the current names", Hint: "puts the current names back"},
	{Name: "bulkRename.editAll", Section: SecBulkRename, Keys: []string{"E"}, Desc: "edit all names in the editor", Hint: "edits the whole list of new names in your editor"},
	{Name: "bulkRename.apply", Section: SecBulkRename, Keys: []string{"a"}, Desc: "apply the rename", Hint: "renames the files after a confirmation"},

	// ---- help --------------------------------------------------------------
	{Name: "help.close", Section: SecHelp, Keys: []string{"esc"}, Desc: "close the help", Essential: true, Hint: "closes the help (with a filter typed, the first press only clears it)"},
	{Name: "help.filter", Section: SecHelp, Keys: []string{"/"}, Desc: "filter the help", Hint: "types text to show only the rows that match"},
	{Name: "help.confirm", Section: SecHelp, Keys: []string{"enter"}, Desc: "keep the filter", Hint: "keeps the filtered rows and leaves the text field"},
	{Name: "help.up", Section: SecHelp, Keys: []string{"up", "k"}, Desc: "scroll up", Hint: "scrolls the help up one row"},
	{Name: "help.down", Section: SecHelp, Keys: []string{"down", "j"}, Desc: "scroll down", Hint: "scrolls the help down one row"},
	{Name: "help.pageUp", Section: SecHelp, Keys: []string{"pgup", "ctrl+b"}, Desc: "page up", Hint: "scrolls the help up one page"},
	{Name: "help.pageDown", Section: SecHelp, Keys: []string{"pgdown", "ctrl+f", " "}, Desc: "page down", Hint: "scrolls the help down one page"},
	{Name: "help.first", Section: SecHelp, Keys: []string{"home", "g"}, Desc: "first row", Hint: "jumps to the top of the help"},
	{Name: "help.last", Section: SecHelp, Keys: []string{"end", "G"}, Desc: "last row", Hint: "jumps to the end of the help"},

	// ---- settings ----------------------------------------------------------
	{Name: "settings.close", Section: SecSettings, Keys: []string{"esc", "f9", "ctrl+s"}, Desc: "close the settings", Essential: true, Hint: "closes the settings window (every change is already saved)"},
	{Name: "settings.up", Section: SecSettings, Keys: []string{"up", "k"}, Desc: "previous row", Hint: "moves to the previous setting"},
	{Name: "settings.down", Section: SecSettings, Keys: []string{"down", "j"}, Desc: "next row", Hint: "moves to the next setting"},
	{Name: "settings.pageUp", Section: SecSettings, Keys: []string{"pgup"}, Desc: "page up", Hint: "moves up by a page"},
	{Name: "settings.pageDown", Section: SecSettings, Keys: []string{"pgdown"}, Desc: "page down", Hint: "moves down by a page"},
	{Name: "settings.first", Section: SecSettings, Keys: []string{"home", "g"}, Desc: "first row", Hint: "jumps to the first setting"},
	{Name: "settings.last", Section: SecSettings, Keys: []string{"end", "G"}, Desc: "last row", Hint: "jumps to the last setting"},
	{Name: "settings.prev", Section: SecSettings, Keys: []string{"left", "h"}, Desc: "previous value", Hint: "chooses the previous value, or turns the setting off"},
	{Name: "settings.next", Section: SecSettings, Keys: []string{"right", "l", " ", "enter"}, Desc: "next value", Hint: "chooses the next value, or turns the setting on; on the Key bindings row it reads keybinding.json again"},
	{Name: "settings.help", Section: SecSettings, Keys: []string{"?"}, Desc: "help", Hint: "shows the key reference over the window"},
	{Name: "settings.reset", Section: SecSettings, Keys: []string{"r"}, Desc: "reset all preferences", Hint: "puts every preference back to its default after a confirmation"},

	// ---- directory comparison ---------------------------------------------
	{Name: "compare.close", Section: SecCompare, Keys: []string{"esc", "ctrl+c", "q"}, Desc: "close the comparison", Essential: true, Hint: "closes the comparison (or stops a scan that is running)"},
	{Name: "compare.help", Section: SecCompare, Keys: []string{"?"}, Desc: "help", Hint: "shows the key reference"},
	{Name: "compare.differences", Section: SecCompare, Keys: []string{"f"}, Desc: "only differences", Hint: "shows only the entries that differ"},
	{Name: "compare.git", Section: SecCompare, Keys: []string{"g"}, Desc: "include .git folders", Shadow: true, Hint: "includes or skips .git folders and scans again"},
	{Name: "compare.leftToRight", Section: SecCompare, Keys: []string{"l"}, Desc: "propose copy left to right", Hint: "proposes copying the highlighted entry to the right panel's folder"},
	{Name: "compare.rightToLeft", Section: SecCompare, Keys: []string{"r"}, Desc: "propose copy right to left", Hint: "proposes copying the highlighted entry to the left panel's folder"},
	{Name: "compare.auto", Section: SecCompare, Keys: []string{"a"}, Desc: "propose all one-sided entries", Hint: "proposes every entry that exists on one side only"},
	{Name: "compare.clear", Section: SecCompare, Keys: []string{"u"}, Desc: "clear the proposals", Hint: "removes all the proposals"},
	{Name: "compare.hash", Section: SecCompare, Keys: []string{"h"}, Desc: "compare contents (SHA-256)", Hint: "compares the contents of the files (SHA-256)"},
	{Name: "compare.plan", Section: SecCompare, Keys: []string{"p"}, Desc: "review the plan", Hint: "reviews the proposed copies before anything is done"},
	{Name: "compare.retry", Section: SecCompare, Keys: []string{"r"}, Desc: "retry after an error", Shadow: true, Hint: "scans again after an error"},
	{Name: "compare.up", Section: SecCompare, Keys: []string{"up", "k"}, Desc: "previous row", Hint: "moves to the previous entry"},
	{Name: "compare.down", Section: SecCompare, Keys: []string{"down", "j"}, Desc: "next row", Hint: "moves to the next entry"},
	{Name: "compare.pageUp", Section: SecCompare, Keys: []string{"pgup", "ctrl+b"}, Desc: "page up", Hint: "moves up by a page"},
	{Name: "compare.pageDown", Section: SecCompare, Keys: []string{"pgdown", "ctrl+f"}, Desc: "page down", Hint: "moves down by a page"},
	{Name: "compare.top", Section: SecCompare, Keys: []string{"home", "g"}, Desc: "first row", Hint: "jumps to the first entry"},
	{Name: "compare.bottom", Section: SecCompare, Keys: []string{"end", "G"}, Desc: "last row", Hint: "jumps to the last entry"},
	{Name: "compare.select", Section: SecCompare, Keys: []string{" ", "insert"}, Desc: "toggle the proposal", Hint: "adds or removes the proposal for the highlighted entry"},
	{Name: "comparePlan.back", Section: SecComparePlan, Keys: []string{"esc", "backspace"}, Desc: "back to the list", Essential: true, Hint: "goes back to the comparison list"},
	{Name: "comparePlan.confirm", Section: SecComparePlan, Keys: []string{"enter", "e"}, Desc: "execute the plan", Hint: "runs the reviewed copies"},
	{Name: "comparePlan.up", Section: SecComparePlan, Keys: []string{"up", "k"}, Desc: "previous action", Hint: "moves to the previous action"},
	{Name: "comparePlan.down", Section: SecComparePlan, Keys: []string{"down", "j"}, Desc: "next action", Hint: "moves to the next action"},
	{Name: "comparePlan.pageUp", Section: SecComparePlan, Keys: []string{"pgup", "ctrl+b"}, Desc: "page up", Hint: "moves up by a page"},
	{Name: "comparePlan.pageDown", Section: SecComparePlan, Keys: []string{"pgdown", "ctrl+f"}, Desc: "page down", Hint: "moves down by a page"},

	// ---- folder sizes ------------------------------------------------------
	{Name: "folderSize.close", Section: SecFolderSize, Keys: []string{"esc", "ctrl+c", "q"}, Desc: "stop / close", Essential: true, Hint: "closes the analysis (or stops the scan that is running)"},
	{Name: "folderSize.help", Section: SecFolderSize, Keys: []string{"?"}, Desc: "help", Hint: "shows the key reference"},
	{Name: "folderSize.retry", Section: SecFolderSize, Keys: []string{"r"}, Desc: "rescan", Shadow: true, Hint: "scans the folder again"},
	{Name: "folderSize.up", Section: SecFolderSize, Keys: []string{"up", "k"}, Desc: "previous row", Hint: "moves to the previous folder"},
	{Name: "folderSize.down", Section: SecFolderSize, Keys: []string{"down", "j"}, Desc: "next row", Hint: "moves to the next folder"},
	{Name: "folderSize.pageUp", Section: SecFolderSize, Keys: []string{"pgup", "ctrl+b"}, Desc: "page up", Hint: "moves up by a page"},
	{Name: "folderSize.pageDown", Section: SecFolderSize, Keys: []string{"pgdown", "ctrl+f"}, Desc: "page down", Hint: "moves down by a page"},
	{Name: "folderSize.top", Section: SecFolderSize, Keys: []string{"home", "g"}, Desc: "first row", Hint: "jumps to the first folder"},
	{Name: "folderSize.bottom", Section: SecFolderSize, Keys: []string{"end", "G"}, Desc: "last row", Hint: "jumps to the last folder"},
	{Name: "folderSize.open", Section: SecFolderSize, Keys: []string{"enter", "right", "l"}, Desc: "open the folder", Hint: "looks inside the highlighted folder"},
	{Name: "folderSize.parent", Section: SecFolderSize, Keys: []string{"backspace", "left"}, Desc: "parent folder", Hint: "goes back to the folder above"},
	{Name: "folderSize.sortSize", Section: SecFolderSize, Keys: []string{"s"}, Desc: "sort by size", Hint: "sorts by size"},
	{Name: "folderSize.sortName", Section: SecFolderSize, Keys: []string{"n"}, Desc: "sort by name", Hint: "sorts by name"},
	{Name: "folderSize.sortAllocated", Section: SecFolderSize, Keys: []string{"a"}, Desc: "sort by allocated size", Hint: "sorts by the space actually used on disk"},

	// ---- live progress -----------------------------------------------------
	{Name: "live.close", Section: SecLive, Keys: []string{"esc", "q"}, Desc: "leave the window", Essential: true, Hint: "closes the window; the operation keeps running"},
	{Name: "live.tasks", Section: SecLive, Keys: []string{"t"}, Desc: "open the task list", Hint: "opens the task list"},
	{Name: "live.cancel", Section: SecLive, Keys: []string{"c", "ctrl+c"}, Desc: "cancel the task", Hint: "cancels the operation"},
	{Name: "live.up", Section: SecLive, Keys: []string{"up", "k", "pgup", "ctrl+b"}, Desc: "scroll up", Hint: "scrolls the output up"},
	{Name: "live.down", Section: SecLive, Keys: []string{"down", "j", "pgdown", "ctrl+f"}, Desc: "scroll down", Hint: "scrolls the output down"},
	{Name: "live.help", Section: SecLive, Keys: []string{"?"}, Desc: "help", Hint: "shows the key reference"},
	{Name: "live.confirm", Section: SecLive, Keys: []string{"enter"}, Desc: "close a finished task", Hint: "closes the window of a finished operation"},
}

// hint is the text written above the action in keybinding.json.
func (a Action) hint() string {
	if a.Hint != "" {
		return a.Hint
	}
	return a.Desc
}

// Sections returns the section names in registry order.
func Sections() []string {
	var out []string
	seen := map[string]bool{}
	for _, a := range actions {
		if !seen[a.Section] {
			seen[a.Section] = true
			out = append(out, a.Section)
		}
	}
	return out
}

// SectionTitle describes a section in one phrase.
func SectionTitle(section string) string {
	if s, ok := sectionTitles[section]; ok {
		return s
	}
	return section
}

// Actions returns the registry in file order.
func Actions() []Action {
	out := make([]Action, len(actions))
	copy(out, actions)
	return out
}

// Lookup returns the action with this name.
func Lookup(name string) (Action, bool) {
	for _, a := range actions {
		if a.Name == name {
			return a, true
		}
	}
	return Action{}, false
}
