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
	{Name: "browse.up", Section: SecBrowse, Keys: []string{"up", "k"}, Desc: "move up", Label: "↑/k"},
	{Name: "browse.down", Section: SecBrowse, Keys: []string{"down", "j"}, Desc: "move down", Label: "↓/j"},
	{Name: "browse.pageUp", Section: SecBrowse, Keys: []string{"pgup", "ctrl+b"}, Desc: "page up", Label: "PgUp"},
	{Name: "browse.pageDown", Section: SecBrowse, Keys: []string{"pgdown", "ctrl+f"}, Desc: "page down", Label: "PgDn"},
	{Name: "browse.top", Section: SecBrowse, Keys: []string{"home", "g"}, Desc: "first entry", Label: "Home/g"},
	{Name: "browse.bottom", Section: SecBrowse, Keys: []string{"end", "G"}, Desc: "last entry", Label: "End/G"},
	{Name: "browse.open", Section: SecBrowse, Keys: []string{"enter", "right", "l"}, Desc: "open directory", Label: "⏎/→/l"},
	{Name: "browse.parent", Section: SecBrowse, Keys: []string{"backspace", "left"}, Desc: "parent directory", Label: "⌫/←"},
	{Name: "browse.back", Section: SecBrowse, Keys: []string{"[", "alt+left"}, Desc: "back (history)", Label: "[ / ]"},
	{Name: "browse.forward", Section: SecBrowse, Keys: []string{"]", "alt+right"}, Desc: "forward (history)", Label: "[ / ]"},
	{Name: "browse.home", Section: SecBrowse, Keys: []string{"~"}, Desc: "go to home directory", Label: "~"},
	{Name: "browse.syncOther", Section: SecBrowse, Keys: []string{"="}, Desc: "same dir in other panel", Label: "="},
	{Name: "browse.compare", Section: SecBrowse, Keys: []string{"D"}, Desc: "compare local directories", Label: "D"},
	{Name: "browse.folderSize", Section: SecBrowse, Keys: []string{"A"}, Desc: "analyze folder sizes", Label: "A"},
	{Name: "browse.select", Section: SecBrowse, Keys: []string{" ", "insert"}, Desc: "toggle selection", Label: "Space/Ins"},
	{Name: "browse.selectAll", Section: SecBrowse, Keys: []string{"a"}, Desc: "select all / none", Label: "a"},
	{Name: "browse.unselect", Section: SecBrowse, Keys: []string{"u"}, Desc: "clear selection", Label: "u"},
	{Name: "browse.switch", Section: SecBrowse, Keys: []string{"tab", "shift+tab"}, Desc: "switch active panel", Label: "Tab"},
	{Name: "browse.hidden", Section: SecBrowse, Keys: []string{"h", "H", "."}, Desc: "hidden files (both panels)", Label: "h/H/."},
	{Name: "browse.sort", Section: SecBrowse, Keys: []string{"s"}, Desc: "sort by name/size/time/type", Label: "s"},
	{Name: "browse.reverse", Section: SecBrowse, Keys: []string{"S"}, Desc: "reverse sort order (↑/↓)", Label: "S"},
	{Name: "browse.search", Section: SecBrowse, Keys: []string{"/"}, Desc: "find files by name (live)", Label: "/"},
	{Name: "browse.refresh", Section: SecBrowse, Keys: []string{"ctrl+l"}, Desc: "refresh both panels", Label: "^L"},
	{Name: "browse.copy", Section: SecBrowse, Keys: []string{"c", "f5"}, Desc: "copy to the other panel", Label: "c/F5"},
	{Name: "browse.move", Section: SecBrowse, Keys: []string{"m", "f6"}, Desc: "move to the other panel", Label: "m/F6"},
	{Name: "browse.rename", Section: SecBrowse, Keys: []string{"r", "f2"}, Desc: "rename", Label: "r/F2"},
	{Name: "browse.delete", Section: SecBrowse, Keys: []string{"d", "f8", "delete"}, Desc: "delete", Label: "d/F8/Del"},
	{Name: "browse.edit", Section: SecBrowse, Keys: []string{"e", "f4"}, Desc: "edit in the default editor", Label: "e/F4"},
	{Name: "browse.editWith", Section: SecBrowse, Keys: []string{"E"}, Desc: "edit with (choose editor)", Label: "E"},
	{Name: "browse.cancel", Section: SecBrowse, Keys: []string{"esc"}, Desc: "cancel dialog / operation", Label: "Esc", Essential: true},
	{Name: "browse.tasks", Section: SecBrowse, Keys: []string{"t"}, Desc: "tasks & operation history", Label: "t"},
	{Name: "browse.help", Section: SecBrowse, Keys: []string{"?", "f1"}, Desc: "toggle help", Label: "?"},
	{Name: "browse.settings", Section: SecBrowse, Keys: []string{"f9", "ctrl+s"}, Desc: "settings", Label: "F9 / ^S"},
	{Name: "browse.quit", Section: SecBrowse, Keys: []string{"q", "f10"}, Desc: "quit", Label: "q", Essential: true},
	{Name: "browse.interrupt", Section: SecBrowse, Keys: []string{"ctrl+c"}, Desc: "cancel the running task / quit", Label: "^C", Essential: true},
	{Name: "browse.properties", Section: SecBrowse, Keys: []string{"P"}, Desc: "properties / permissions", Label: "P"},
	{Name: "browse.view", Section: SecBrowse, Keys: []string{"v", "f3"}, Desc: "view file (read-only)", Label: "v/F3"},
	{Name: "browse.extract", Section: SecBrowse, Keys: []string{"x"}, Desc: "extract archive / selected", Label: "x"},
	{Name: "browse.extractAll", Section: SecBrowse, Keys: []string{"X"}, Desc: "extract all (in an archive)", Label: "X"},
	{Name: "browse.compress", Section: SecBrowse, Keys: []string{"z"}, Desc: "compress (ZIP/TAR/7Z)", Label: "z"},
	{Name: "browse.openExternal", Section: SecBrowse, Keys: []string{"o"}, Desc: "open in default app", Label: "o"},
	{Name: "browse.openWith", Section: SecBrowse, Keys: []string{"O"}, Desc: "open with (choose)", Label: "O"},
	{Name: "browse.menu", Section: SecBrowse, Keys: []string{"M"}, Desc: "menu of actions", Label: "M"},
	{Name: "browse.bookmarks", Section: SecBrowse, Keys: []string{"b"}, Desc: "bookmarks (jump/edit)", Label: "b"},
	{Name: "browse.bookmark", Section: SecBrowse, Keys: []string{"B"}, Desc: "bookmark this folder", Label: "B"},
	{Name: "browse.storage", Section: SecBrowse, Keys: []string{"i"}, Desc: "storage / free space", Label: "i"},
	{Name: "browse.sources", Section: SecBrowse, Keys: []string{"ctrl+r"}, Desc: "source: local / cloud", Label: "^R"},
	{Name: "browse.mkdir", Section: SecBrowse, Keys: []string{"f7"}, Desc: "new folder (direct)", Label: "F7"},
	{Name: "browse.new", Section: SecBrowse, Keys: []string{"n"}, Desc: "new file / new folder", Label: "n"},
	{Name: "browse.activity", Section: SecBrowse, Keys: []string{"L"}, Desc: "activity log", Label: "L"},
	{Name: "browse.focus", Section: SecBrowse, Keys: []string{"ctrl+o"}, Desc: "focus mode (one panel)", Label: "Ctrl+O"},
	{Name: "browse.preview", Section: SecBrowse, Keys: []string{"p"}, Desc: "preview pane (focus mode)", Label: "p"},
	{Name: "browse.goTo", Section: SecBrowse, Keys: []string{"ctrl+g"}, Desc: "go to a folder (focus mode)", Label: "Ctrl+G"},

	// ---- dialogs -----------------------------------------------------------
	{Name: "dialog.cancel", Section: SecDialog, Keys: []string{"esc", "ctrl+c"}, Desc: "cancel / close", Essential: true},
	{Name: "dialog.confirm", Section: SecDialog, Keys: []string{"enter"}, Desc: "press the highlighted button"},
	{Name: "dialog.next", Section: SecDialog, Keys: []string{"tab", "right"}, Desc: "next button"},
	{Name: "dialog.prev", Section: SecDialog, Keys: []string{"shift+tab", "left"}, Desc: "previous button"},
	{Name: "dialog.scrollUp", Section: SecDialog, Keys: []string{"up", "k"}, Desc: "scroll up"},
	{Name: "dialog.scrollDown", Section: SecDialog, Keys: []string{"down", "j"}, Desc: "scroll down"},
	{Name: "dialog.pageUp", Section: SecDialog, Keys: []string{"pgup"}, Desc: "page up"},
	{Name: "dialog.pageDown", Section: SecDialog, Keys: []string{"pgdown", " "}, Desc: "page down"},

	// ---- pick lists --------------------------------------------------------
	{Name: "list.up", Section: SecList, Keys: []string{"up", "k", "shift+tab", "ctrl+p"}, Desc: "previous item"},
	{Name: "list.down", Section: SecList, Keys: []string{"down", "j", "tab", "ctrl+n"}, Desc: "next item"},
	{Name: "list.first", Section: SecList, Keys: []string{"home", "g"}, Desc: "first item"},
	{Name: "list.last", Section: SecList, Keys: []string{"end", "G"}, Desc: "last item"},
	{Name: "list.open", Section: SecList, Keys: []string{"enter", "right", "l"}, Desc: "choose the item"},
	{Name: "list.close", Section: SecList, Keys: []string{"q"}, Desc: "close the list"},
	{Name: "filterList.up", Section: SecFilterList, Keys: []string{"up", "ctrl+p"}, Desc: "previous item"},
	{Name: "filterList.down", Section: SecFilterList, Keys: []string{"down", "ctrl+n", "tab"}, Desc: "next item"},
	{Name: "filterList.pageUp", Section: SecFilterList, Keys: []string{"pgup"}, Desc: "page up"},
	{Name: "filterList.pageDown", Section: SecFilterList, Keys: []string{"pgdown"}, Desc: "page down"},
	{Name: "filterList.first", Section: SecFilterList, Keys: []string{"home"}, Desc: "first item"},
	{Name: "filterList.last", Section: SecFilterList, Keys: []string{"end"}, Desc: "last item"},
	{Name: "filterList.pick", Section: SecFilterList, Keys: []string{"enter"}, Desc: "choose the item"},

	// ---- text fields -------------------------------------------------------
	{Name: "input.backspace", Section: SecInput, Keys: []string{"backspace", "ctrl+h"}, Desc: "delete the previous character"},
	{Name: "input.delete", Section: SecInput, Keys: []string{"delete", "ctrl+d"}, Desc: "delete the next character"},
	{Name: "input.left", Section: SecInput, Keys: []string{"left", "ctrl+b"}, Desc: "cursor left"},
	{Name: "input.right", Section: SecInput, Keys: []string{"right", "ctrl+f"}, Desc: "cursor right"},
	{Name: "input.home", Section: SecInput, Keys: []string{"home", "ctrl+a"}, Desc: "start of the field"},
	{Name: "input.end", Section: SecInput, Keys: []string{"end", "ctrl+e"}, Desc: "end of the field"},
	{Name: "input.deleteToStart", Section: SecInput, Keys: []string{"ctrl+u"}, Desc: "delete to the start"},
	{Name: "input.deleteToEnd", Section: SecInput, Keys: []string{"ctrl+k"}, Desc: "delete to the end"},
	{Name: "input.deleteWord", Section: SecInput, Keys: []string{"ctrl+w", "alt+backspace"}, Desc: "delete the previous word"},

	// ---- viewer ------------------------------------------------------------
	{Name: "viewer.close", Section: SecViewer, Keys: []string{"esc", "q", "f3", "f10", "v", "backspace"}, Desc: "close the viewer", Essential: true},
	{Name: "viewer.help", Section: SecViewer, Keys: []string{"?", "f1"}, Desc: "help"},
	{Name: "viewer.edit", Section: SecViewer, Keys: []string{"e", "f4"}, Desc: "edit the file"},
	{Name: "viewer.scrollDown", Section: SecViewer, Keys: []string{"down", "j", "enter"}, Desc: "scroll down"},
	{Name: "viewer.scrollUp", Section: SecViewer, Keys: []string{"up", "k"}, Desc: "scroll up"},
	{Name: "viewer.pageDown", Section: SecViewer, Keys: []string{"pgdown", "ctrl+f", " ", "ctrl+d"}, Desc: "page down"},
	{Name: "viewer.pageUp", Section: SecViewer, Keys: []string{"pgup", "ctrl+b", "ctrl+u"}, Desc: "page up"},
	{Name: "viewer.top", Section: SecViewer, Keys: []string{"home", "g"}, Desc: "first line"},
	{Name: "viewer.bottom", Section: SecViewer, Keys: []string{"end", "G"}, Desc: "last line"},
	{Name: "viewer.right", Section: SecViewer, Keys: []string{"right", "l"}, Desc: "scroll right (no wrap)"},
	{Name: "viewer.left", Section: SecViewer, Keys: []string{"left", "h"}, Desc: "scroll left (no wrap)"},
	{Name: "viewer.wrap", Section: SecViewer, Keys: []string{"w"}, Desc: "line wrapping"},
	{Name: "viewer.numbers", Section: SecViewer, Keys: []string{"#"}, Desc: "line numbers"},
	{Name: "viewer.find", Section: SecViewer, Keys: []string{"/"}, Desc: "find in the file"},
	{Name: "viewer.nextMatch", Section: SecViewer, Keys: []string{"n"}, Desc: "next match"},
	{Name: "viewer.prevMatch", Section: SecViewer, Keys: []string{"N"}, Desc: "previous match"},

	// ---- live search -------------------------------------------------------
	{Name: "search.close", Section: SecSearch, Keys: []string{"esc"}, Desc: "close the search", Essential: true},
	{Name: "search.quit", Section: SecSearch, Keys: []string{"ctrl+c"}, Desc: "quit"},
	{Name: "search.advanced", Section: SecSearch, Keys: []string{"ctrl+g"}, Desc: "Advanced Search"},
	{Name: "search.jump", Section: SecSearch, Keys: []string{"enter"}, Desc: "jump to the result"},
	{Name: "search.toggleFocus", Section: SecSearch, Keys: []string{"tab", "shift+tab"}, Desc: "text field / result list"},
	{Name: "search.up", Section: SecSearch, Keys: []string{"up", "ctrl+p"}, Desc: "previous result"},
	{Name: "search.down", Section: SecSearch, Keys: []string{"down", "ctrl+n"}, Desc: "next result"},
	{Name: "search.pageUp", Section: SecSearch, Keys: []string{"pgup"}, Desc: "page up"},
	{Name: "search.pageDown", Section: SecSearch, Keys: []string{"pgdown"}, Desc: "page down"},
	{Name: "search.edit", Section: SecSearch, Keys: []string{"f4"}, Desc: "edit the selected file"},

	{Name: "searchList.up", Section: SecSearchList, Keys: []string{"k"}, Desc: "previous result"},
	{Name: "searchList.down", Section: SecSearchList, Keys: []string{"j"}, Desc: "next result"},
	{Name: "searchList.first", Section: SecSearchList, Keys: []string{"home", "g"}, Desc: "first result"},
	{Name: "searchList.last", Section: SecSearchList, Keys: []string{"end", "G"}, Desc: "last result"},
	{Name: "searchList.open", Section: SecSearchList, Keys: []string{"right", "l"}, Desc: "open the result"},
	{Name: "searchList.select", Section: SecSearchList, Keys: []string{" ", "insert"}, Desc: "mark the result"},
	{Name: "searchList.edit", Section: SecSearchList, Keys: []string{"e"}, Desc: "edit the result"},
	{Name: "searchList.editWith", Section: SecSearchList, Keys: []string{"E"}, Desc: "edit with"},
	{Name: "searchList.close", Section: SecSearchList, Keys: []string{"q"}, Desc: "close the search"},
	{Name: "searchList.back", Section: SecSearchList, Keys: []string{"/", "i"}, Desc: "back to the text field"},
	{Name: "searchList.backspace", Section: SecSearchList, Keys: []string{"backspace"}, Desc: "back to the text field and delete"},

	// ---- advanced search ---------------------------------------------------
	{Name: "advanced.close", Section: SecAdvanced, Keys: []string{"esc"}, Desc: "close Advanced Search", Essential: true},
	{Name: "advanced.quit", Section: SecAdvanced, Keys: []string{"ctrl+c"}, Desc: "quit"},
	{Name: "advanced.mode", Section: SecAdvanced, Keys: []string{"ctrl+t"}, Desc: "filename / content"},
	{Name: "advanced.actions", Section: SecAdvanced, Keys: []string{"ctrl+a"}, Desc: "actions for the result"},
	{Name: "advanced.help", Section: SecAdvanced, Keys: []string{"?", "f1"}, Desc: "help"},
	{Name: "advanced.open", Section: SecAdvanced, Keys: []string{"enter"}, Desc: "inspect the result"},
	{Name: "advanced.up", Section: SecAdvanced, Keys: []string{"up", "ctrl+p"}, Desc: "previous result"},
	{Name: "advanced.down", Section: SecAdvanced, Keys: []string{"down", "ctrl+n"}, Desc: "next result"},
	{Name: "advanced.pageUp", Section: SecAdvanced, Keys: []string{"pgup"}, Desc: "page up"},
	{Name: "advanced.pageDown", Section: SecAdvanced, Keys: []string{"pgdown"}, Desc: "page down"},
	{Name: "advanced.first", Section: SecAdvanced, Keys: []string{"home"}, Desc: "first result"},
	{Name: "advanced.last", Section: SecAdvanced, Keys: []string{"end"}, Desc: "last result"},

	// ---- tasks -------------------------------------------------------------
	{Name: "tasks.close", Section: SecTasks, Keys: []string{"esc", "t"}, Desc: "close the task view", Essential: true},
	{Name: "tasks.up", Section: SecTasks, Keys: []string{"up", "k"}, Desc: "previous task"},
	{Name: "tasks.down", Section: SecTasks, Keys: []string{"down", "j"}, Desc: "next task"},
	{Name: "tasks.prevRun", Section: SecTasks, Keys: []string{"["}, Desc: "previous run"},
	{Name: "tasks.nextRun", Section: SecTasks, Keys: []string{"]"}, Desc: "next run"},
	{Name: "tasks.pageUp", Section: SecTasks, Keys: []string{"pgup", "ctrl+b"}, Desc: "scroll the details up"},
	{Name: "tasks.pageDown", Section: SecTasks, Keys: []string{"pgdown", "ctrl+f"}, Desc: "scroll the details down"},
	{Name: "tasks.watch", Section: SecTasks, Keys: []string{"enter", "w"}, Desc: "watch the task live"},
	{Name: "tasks.cancel", Section: SecTasks, Keys: []string{"c", "ctrl+c"}, Desc: "cancel the task"},
	{Name: "tasks.forget", Section: SecTasks, Keys: []string{"x"}, Desc: "clear finished tasks"},
	{Name: "tasks.moreRuns", Section: SecTasks, Keys: []string{"+", "="}, Desc: "keep more runs"},
	{Name: "tasks.fewerRuns", Section: SecTasks, Keys: []string{"-", "_"}, Desc: "keep fewer runs"},

	// ---- activity log ------------------------------------------------------
	{Name: "activity.close", Section: SecActivity, Keys: []string{"esc", "L", "q"}, Desc: "close the log", Essential: true},
	{Name: "activity.up", Section: SecActivity, Keys: []string{"up", "k"}, Desc: "previous entry"},
	{Name: "activity.down", Section: SecActivity, Keys: []string{"down", "j"}, Desc: "next entry"},
	{Name: "activity.pageUp", Section: SecActivity, Keys: []string{"pgup", "ctrl+b"}, Desc: "page up"},
	{Name: "activity.pageDown", Section: SecActivity, Keys: []string{"pgdown", "ctrl+f"}, Desc: "page down"},
	{Name: "activity.first", Section: SecActivity, Keys: []string{"home", "g"}, Desc: "first entry"},
	{Name: "activity.last", Section: SecActivity, Keys: []string{"end", "G"}, Desc: "last entry"},
	{Name: "activity.category", Section: SecActivity, Keys: []string{"c"}, Desc: "category filter"},
	{Name: "activity.level", Section: SecActivity, Keys: []string{"l"}, Desc: "level filter"},
	{Name: "activity.prevRun", Section: SecActivity, Keys: []string{"["}, Desc: "previous run"},
	{Name: "activity.nextRun", Section: SecActivity, Keys: []string{"]"}, Desc: "next run"},
	{Name: "activity.details", Section: SecActivity, Keys: []string{"enter", " "}, Desc: "show the details"},
	{Name: "activity.filter", Section: SecActivity, Keys: []string{"/"}, Desc: "filter the log"},
	{Name: "activity.clear", Section: SecActivity, Keys: []string{"x"}, Desc: "clear the filters"},
	{Name: "activity.reload", Section: SecActivity, Keys: []string{"r"}, Desc: "reload the log"},
	{Name: "activity.help", Section: SecActivity, Keys: []string{"?"}, Desc: "help"},

	// ---- properties --------------------------------------------------------
	{Name: "props.close", Section: SecProps, Keys: []string{"esc"}, Desc: "close without applying", Essential: true},
	{Name: "props.quit", Section: SecProps, Keys: []string{"ctrl+c"}, Desc: "quit"},
	{Name: "props.nextField", Section: SecProps, Keys: []string{"tab"}, Desc: "next field"},
	{Name: "props.prevField", Section: SecProps, Keys: []string{"shift+tab"}, Desc: "previous field"},
	{Name: "props.apply", Section: SecProps, Keys: []string{"enter"}, Desc: "apply the changes", Shadow: true},
	{Name: "props.press", Section: SecProps, Keys: []string{"enter", " "}, Desc: "press the focused button", Shadow: true},
	{Name: "props.toggle", Section: SecProps, Keys: []string{" ", "x"}, Desc: "toggle a permission", Shadow: true},
	{Name: "props.left", Section: SecProps, Keys: []string{"left", "h"}, Desc: "move left"},
	{Name: "props.right", Section: SecProps, Keys: []string{"right", "l"}, Desc: "move right"},
	{Name: "props.up", Section: SecProps, Keys: []string{"up", "k"}, Desc: "move up"},
	{Name: "props.down", Section: SecProps, Keys: []string{"down", "j"}, Desc: "move down"},
	{Name: "props.closeReadOnly", Section: SecProps, Keys: []string{"q"}, Desc: "close a read-only view"},

	// ---- bulk rename -------------------------------------------------------
	{Name: "bulkRename.cancel", Section: SecBulkRename, Keys: []string{"esc", "q"}, Desc: "cancel (nothing is renamed)", Essential: true},
	{Name: "bulkRename.quit", Section: SecBulkRename, Keys: []string{"ctrl+c"}, Desc: "quit"},
	{Name: "bulkRename.up", Section: SecBulkRename, Keys: []string{"up", "k"}, Desc: "previous row"},
	{Name: "bulkRename.down", Section: SecBulkRename, Keys: []string{"down", "j"}, Desc: "next row"},
	{Name: "bulkRename.pageUp", Section: SecBulkRename, Keys: []string{"pgup", "ctrl+b"}, Desc: "page up"},
	{Name: "bulkRename.pageDown", Section: SecBulkRename, Keys: []string{"pgdown", "ctrl+f"}, Desc: "page down"},
	{Name: "bulkRename.first", Section: SecBulkRename, Keys: []string{"home", "g"}, Desc: "first row"},
	{Name: "bulkRename.last", Section: SecBulkRename, Keys: []string{"end", "G"}, Desc: "last row"},
	{Name: "bulkRename.editRow", Section: SecBulkRename, Keys: []string{"enter", "r", "f2"}, Desc: "edit the new name"},
	{Name: "bulkRename.find", Section: SecBulkRename, Keys: []string{"f"}, Desc: "find and replace"},
	{Name: "bulkRename.pattern", Section: SecBulkRename, Keys: []string{"n"}, Desc: "rename pattern"},
	{Name: "bulkRename.lower", Section: SecBulkRename, Keys: []string{"L"}, Desc: "lower-case all names"},
	{Name: "bulkRename.upper", Section: SecBulkRename, Keys: []string{"U"}, Desc: "upper-case all names"},
	{Name: "bulkRename.reset", Section: SecBulkRename, Keys: []string{"R"}, Desc: "reset to the current names"},
	{Name: "bulkRename.editAll", Section: SecBulkRename, Keys: []string{"E"}, Desc: "edit all names in the editor"},
	{Name: "bulkRename.apply", Section: SecBulkRename, Keys: []string{"a"}, Desc: "apply the rename"},

	// ---- help --------------------------------------------------------------
	{Name: "help.close", Section: SecHelp, Keys: []string{"esc"}, Desc: "close the help", Essential: true},
	{Name: "help.filter", Section: SecHelp, Keys: []string{"/"}, Desc: "filter the help"},
	{Name: "help.confirm", Section: SecHelp, Keys: []string{"enter"}, Desc: "keep the filter"},
	{Name: "help.up", Section: SecHelp, Keys: []string{"up", "k"}, Desc: "scroll up"},
	{Name: "help.down", Section: SecHelp, Keys: []string{"down", "j"}, Desc: "scroll down"},
	{Name: "help.pageUp", Section: SecHelp, Keys: []string{"pgup", "ctrl+b"}, Desc: "page up"},
	{Name: "help.pageDown", Section: SecHelp, Keys: []string{"pgdown", "ctrl+f", " "}, Desc: "page down"},
	{Name: "help.first", Section: SecHelp, Keys: []string{"home", "g"}, Desc: "first row"},
	{Name: "help.last", Section: SecHelp, Keys: []string{"end", "G"}, Desc: "last row"},

	// ---- settings ----------------------------------------------------------
	{Name: "settings.close", Section: SecSettings, Keys: []string{"esc", "f9", "ctrl+s"}, Desc: "close the settings", Essential: true},
	{Name: "settings.up", Section: SecSettings, Keys: []string{"up", "k"}, Desc: "previous row"},
	{Name: "settings.down", Section: SecSettings, Keys: []string{"down", "j"}, Desc: "next row"},
	{Name: "settings.pageUp", Section: SecSettings, Keys: []string{"pgup"}, Desc: "page up"},
	{Name: "settings.pageDown", Section: SecSettings, Keys: []string{"pgdown"}, Desc: "page down"},
	{Name: "settings.first", Section: SecSettings, Keys: []string{"home", "g"}, Desc: "first row"},
	{Name: "settings.last", Section: SecSettings, Keys: []string{"end", "G"}, Desc: "last row"},
	{Name: "settings.prev", Section: SecSettings, Keys: []string{"left", "h"}, Desc: "previous value"},
	{Name: "settings.next", Section: SecSettings, Keys: []string{"right", "l", " ", "enter"}, Desc: "next value"},
	{Name: "settings.help", Section: SecSettings, Keys: []string{"?"}, Desc: "help"},
	{Name: "settings.reset", Section: SecSettings, Keys: []string{"r"}, Desc: "reset all preferences"},

	// ---- directory comparison ---------------------------------------------
	{Name: "compare.close", Section: SecCompare, Keys: []string{"esc", "ctrl+c", "q"}, Desc: "close the comparison", Essential: true},
	{Name: "compare.help", Section: SecCompare, Keys: []string{"?"}, Desc: "help"},
	{Name: "compare.differences", Section: SecCompare, Keys: []string{"f"}, Desc: "only differences"},
	{Name: "compare.git", Section: SecCompare, Keys: []string{"g"}, Desc: "include .git folders", Shadow: true},
	{Name: "compare.leftToRight", Section: SecCompare, Keys: []string{"l"}, Desc: "propose copy left to right"},
	{Name: "compare.rightToLeft", Section: SecCompare, Keys: []string{"r"}, Desc: "propose copy right to left"},
	{Name: "compare.auto", Section: SecCompare, Keys: []string{"a"}, Desc: "propose all one-sided entries"},
	{Name: "compare.clear", Section: SecCompare, Keys: []string{"u"}, Desc: "clear the proposals"},
	{Name: "compare.hash", Section: SecCompare, Keys: []string{"h"}, Desc: "compare contents (SHA-256)"},
	{Name: "compare.plan", Section: SecCompare, Keys: []string{"p"}, Desc: "review the plan"},
	{Name: "compare.retry", Section: SecCompare, Keys: []string{"r"}, Desc: "retry after an error", Shadow: true},
	{Name: "compare.up", Section: SecCompare, Keys: []string{"up", "k"}, Desc: "previous row"},
	{Name: "compare.down", Section: SecCompare, Keys: []string{"down", "j"}, Desc: "next row"},
	{Name: "compare.pageUp", Section: SecCompare, Keys: []string{"pgup", "ctrl+b"}, Desc: "page up"},
	{Name: "compare.pageDown", Section: SecCompare, Keys: []string{"pgdown", "ctrl+f"}, Desc: "page down"},
	{Name: "compare.top", Section: SecCompare, Keys: []string{"home", "g"}, Desc: "first row"},
	{Name: "compare.bottom", Section: SecCompare, Keys: []string{"end", "G"}, Desc: "last row"},
	{Name: "compare.select", Section: SecCompare, Keys: []string{" ", "insert"}, Desc: "toggle the proposal"},
	{Name: "comparePlan.back", Section: SecComparePlan, Keys: []string{"esc", "backspace"}, Desc: "back to the list", Essential: true},
	{Name: "comparePlan.confirm", Section: SecComparePlan, Keys: []string{"enter", "e"}, Desc: "execute the plan"},
	{Name: "comparePlan.up", Section: SecComparePlan, Keys: []string{"up", "k"}, Desc: "previous action"},
	{Name: "comparePlan.down", Section: SecComparePlan, Keys: []string{"down", "j"}, Desc: "next action"},
	{Name: "comparePlan.pageUp", Section: SecComparePlan, Keys: []string{"pgup", "ctrl+b"}, Desc: "page up"},
	{Name: "comparePlan.pageDown", Section: SecComparePlan, Keys: []string{"pgdown", "ctrl+f"}, Desc: "page down"},

	// ---- folder sizes ------------------------------------------------------
	{Name: "folderSize.close", Section: SecFolderSize, Keys: []string{"esc", "ctrl+c", "q"}, Desc: "stop / close", Essential: true},
	{Name: "folderSize.help", Section: SecFolderSize, Keys: []string{"?"}, Desc: "help"},
	{Name: "folderSize.retry", Section: SecFolderSize, Keys: []string{"r"}, Desc: "rescan", Shadow: true},
	{Name: "folderSize.up", Section: SecFolderSize, Keys: []string{"up", "k"}, Desc: "previous row"},
	{Name: "folderSize.down", Section: SecFolderSize, Keys: []string{"down", "j"}, Desc: "next row"},
	{Name: "folderSize.pageUp", Section: SecFolderSize, Keys: []string{"pgup", "ctrl+b"}, Desc: "page up"},
	{Name: "folderSize.pageDown", Section: SecFolderSize, Keys: []string{"pgdown", "ctrl+f"}, Desc: "page down"},
	{Name: "folderSize.top", Section: SecFolderSize, Keys: []string{"home", "g"}, Desc: "first row"},
	{Name: "folderSize.bottom", Section: SecFolderSize, Keys: []string{"end", "G"}, Desc: "last row"},
	{Name: "folderSize.open", Section: SecFolderSize, Keys: []string{"enter", "right", "l"}, Desc: "open the folder"},
	{Name: "folderSize.parent", Section: SecFolderSize, Keys: []string{"backspace", "left"}, Desc: "parent folder"},
	{Name: "folderSize.sortSize", Section: SecFolderSize, Keys: []string{"s"}, Desc: "sort by size"},
	{Name: "folderSize.sortName", Section: SecFolderSize, Keys: []string{"n"}, Desc: "sort by name"},
	{Name: "folderSize.sortAllocated", Section: SecFolderSize, Keys: []string{"a"}, Desc: "sort by allocated size"},

	// ---- live progress -----------------------------------------------------
	{Name: "live.close", Section: SecLive, Keys: []string{"esc", "q"}, Desc: "leave the window", Essential: true},
	{Name: "live.tasks", Section: SecLive, Keys: []string{"t"}, Desc: "open the task list"},
	{Name: "live.cancel", Section: SecLive, Keys: []string{"c", "ctrl+c"}, Desc: "cancel the task"},
	{Name: "live.up", Section: SecLive, Keys: []string{"up", "k", "pgup", "ctrl+b"}, Desc: "scroll up"},
	{Name: "live.down", Section: SecLive, Keys: []string{"down", "j", "pgdown", "ctrl+f"}, Desc: "scroll down"},
	{Name: "live.help", Section: SecLive, Keys: []string{"?"}, Desc: "help"},
	{Name: "live.confirm", Section: SecLive, Keys: []string{"enter"}, Desc: "close a finished task"},
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
