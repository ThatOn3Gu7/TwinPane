// Package ui wires two panels into the Bubble Tea application: layout, key
// dispatch, status line, footer, help overlay and (phase 2) the dialogs and
// background sessions for file operations (fileops.go, edit.go, dialog.go).
package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/bookmarks"
	"github.com/ThatOn3Gu7/TwinPane/internal/cloud"
	"github.com/ThatOn3Gu7/TwinPane/internal/compress"
	"github.com/ThatOn3Gu7/TwinPane/internal/editor"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/opener"
	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
	"github.com/ThatOn3Gu7/TwinPane/internal/panel"
	"github.com/ThatOn3Gu7/TwinPane/internal/proc"
	"github.com/ThatOn3Gu7/TwinPane/internal/search"
	"github.com/ThatOn3Gu7/TwinPane/internal/settings"
	"github.com/ThatOn3Gu7/TwinPane/internal/theme"
)

const (
	minWidth  = 30
	minHeight = 7
)

// Options configures a new App.
type Options struct {
	Version string
	Lister  fsys.Lister // nil = local filesystem
	// StartDir overrides the settings' startup folders: both panels open
	// there ("" = saved folders, home by default).
	StartDir string

	// Phase 2 (all optional; nil = real system behaviour)
	Trasher   ops.Trasher // nil = ops.DefaultTrasher()
	EditorEnv *editor.Env // nil = editor.SystemEnv()
	Exec      ExecFunc    // nil = tea.Exec

	// Notice is shown in the status line at start-up (warning level).
	Notice string

	// SearchBackends lists the search backends to try in order (nil =
	// detect: fd/fdfind, find, built-in).
	SearchBackends []search.Backend

	// Phase 4
	// Mouse: mouse/touch reporting is enabled for the program (main.go
	// passes tea.WithMouseCellMotion). Bubble Tea switches it off while an
	// external editor runs and does not switch it back on, so the App
	// re-enables it when the editor returns.
	Mouse bool
	// EditorTips: before the first editor launch of a session (and again
	// after an edit that saved nothing) show the editor's save/quit keys on
	// a confirmation card that waits for Enter (see showEditTip in edit.go).
	// A double tap on a file shows that card every time, regardless.
	EditorTips bool

	// Phase 6 (all optional; nil / "" = real system behaviour)
	// LookPath finds external programs (7-Zip, termux-open, xdg-open):
	// nil = proc.LookPath.
	LookPath func(string) (string, error)
	// Getenv reads the environment for platform detection: nil = os.Getenv.
	Getenv func(string) string
	// GOOS overrides runtime.GOOS for platform detection ("" = runtime).
	GOOS string
	// Launch starts an external opener detached from the terminal and
	// reports its failure: nil = opener.Launch.
	Launch func(argv []string) error
	// BookmarksPath is the bookmarks file: "" = bookmarks.DefaultPath().
	BookmarksPath string
	// SettingsPath is the user preferences file. The command sets this to
	// settings.DefaultPath(); empty disables persistence (useful for tests).
	SettingsPath string
	// SettingsPathError reports a failure resolving the default settings path.
	SettingsPathError string
}

// status levels
const (
	lvlInfo = iota
	lvlOK
	lvlWarn
	lvlErr
)

// App is the root Bubble Tea model.
type App struct {
	opts   Options
	keys   KeyMap
	theme  theme.Theme
	panels [2]*panel.Panel
	active int

	width, height int
	status        string
	statusErr     bool
	statusLevel   int
	showHelp      bool
	quitting      bool

	// phase 2 / 12: file operations and retained background task history
	dialog           *dialog
	tasks            taskManager
	opSeq            int
	compressProgress *compressProgressState
	comparisonSeq    int
	folderSizeSeq    int
	trasher          ops.Trasher
	edEnv            editor.Env
	execFn           ExecFunc

	// phase 3
	editing        bool      // the external editor owns the terminal
	quietUntil     time.Time // ignore keys until then (late terminal replies)
	now            func() time.Time
	spinning       bool                // a spinner tick is scheduled (only while a panel loads)
	search         *searchView         // recursive search overlay
	advancedSearch *advancedSearchView // FZF-inspired filename/content search
	advancedSeq    int
	bulk           *bulkRename     // bulk rename overlay
	comparison     *comparisonView // local directory comparison and reviewed plan
	folderSize     *folderSizeView // cancellable local folder-size analysis

	// phase 4
	showHidden   bool   // one setting for both panels (h / H / .)
	settingsPath string // persisted user preferences (empty disables persistence)
	settingsErr  error  // latest settings load/save failure

	// phase 13: the Settings screen (settings.go) and the preferences it
	// edits. Fields mirror settings.Preferences; currentPrefs() and
	// applyPreferences() map between the two.
	showSettings   bool
	settingsScr    settingsScreen
	startLeft      string // startup folder, left panel ("" = home; expanded at Init)
	startRight     string
	startPane      int          // panel focused at startup: 0 left, 1 right
	defSort        fsys.SortKey // default sort; s/S keep changing the session only
	defDesc        bool
	defDirsFirst   bool
	noticeCorner   string        // notification corner: "br" | "bl" | "tr" | "tl"
	noticeDur      time.Duration // notification lifetime
	themeName      string        // active theme preset (theme.Preset)
	editorCmd      string        // preferred editor command line ("" = $VISUAL/$EDITOR)
	tipsPref       bool          // editor-tips preference (the flag can still disable)
	mousePref      bool          // mouse preference (flags/env can still disable)
	viewerWrap     bool          // viewer defaults for a newly opened file
	viewerNums     bool
	footerHits     []footerHit // clickable hint-bar items of the last frame
	helpScroll     int         // first line of the help screen shown
	helpScrollable bool        // the help body is taller than its viewport
	help           helpState   // help filter and layout (help.go)
	lastClick      clickState  // double-tap detection
	press          pressState  // hint-bar press awaiting its release
	drag           dragState   // scroll-indicator drag (mouse)
	seenDialog     *dialog     // dialog shown in the previous update
	dialogAt       time.Time   // when the current dialog opened
	tipsShown      map[string]bool

	// phase 5
	viewer *viewer    // built-in read-only file viewer
	props  *propsView // Properties dialog

	// phase 6
	lookPath     func(string) (string, error)
	getenv       func(string) string
	goos         string
	launch       func(argv []string) error
	platCache    *platCache          // the opener platform for the hint bar
	archiveHint  *archiveHintCache   // cached ZIP/TAR browse support for the current file
	lastFormat   compress.ID         // format chosen last in the compress dialog
	formatLevels map[compress.ID]int // level index chosen per format
	bookmarks    *bookmarks.Store
	bookmarksErr error // why the bookmarks could not be loaded

	// phase 9
	sourcesSeq int // the Sources picker's latest remote listing

	// phase 11: focus mode (focus.go, focus_dest.go) — one panel at the full
	// width of the terminal. The two panels themselves keep their state.
	focus           bool
	focusPreview    bool         // show the preview/details pane when there is room
	focusPrevScroll int          // first preview row shown
	focusPrev       focusPreview // cached preview of the selected entry
	recents         []destLoc    // recent transfer destinations (this run)
	destSeq         int          // the destination picker's latest listing

	// phase 11: background task experience (live.go). live follows one task in
	// the foreground.
	live *liveTask
	// phase 12: notifications (notice.go). notice is the pop-up reporting the
	// newest result; noticeSeq identifies its countdown.
	notice        *notice
	noticeSeq     int
	noticeTicking bool
}

// New creates the application model.
func New(opts Options) *App {
	a := &App{opts: opts, keys: DefaultKeys(), theme: theme.Default(), tasks: newTaskManager()}
	a.trasher = opts.Trasher
	if a.trasher == nil {
		a.trasher = ops.DefaultTrasher()
	}
	a.edEnv = editor.SystemEnv()
	if opts.EditorEnv != nil {
		a.edEnv = *opts.EditorEnv
	}
	a.execFn = opts.Exec
	if a.execFn == nil {
		a.execFn = tea.Exec
	}
	a.now = time.Now
	a.tipsShown = map[string]bool{}
	a.lookPath = opts.LookPath
	if a.lookPath == nil {
		a.lookPath = proc.LookPath
	}
	a.getenv = opts.Getenv
	if a.getenv == nil {
		a.getenv = os.Getenv
	}
	a.goos = opts.GOOS
	if a.goos == "" {
		a.goos = runtime.GOOS
	}
	a.launch = opts.Launch
	if a.launch == nil {
		a.launch = func(argv []string) error { return opener.Launch(argv, opener.LaunchWait) }
	}
	a.formatLevels = map[compress.ID]int{}
	// Defaults matching historic behaviour; the settings file overrides them.
	a.focusPreview = true
	a.tipsPref = true
	a.mousePref = true
	a.viewerWrap, a.viewerNums = true, true
	a.noticeCorner, a.noticeDur = "br", noticeLife
	a.themeName = "default"
	a.defSort, a.defDesc, a.defDirsFirst = fsys.SortName, false, true
	a.settingsPath = opts.SettingsPath
	if opts.SettingsPathError != "" {
		a.settingsErr = fmt.Errorf("%s", opts.SettingsPathError)
	} else if a.settingsPath != "" {
		prefs, err := settings.Load(a.settingsPath)
		a.settingsErr = err
		if err == nil {
			a.applyPreferences(prefs)
		}
	}
	if opts.StartDir != "" {
		// Explicit start directory (tests and embedders): both panels use it.
		a.startLeft, a.startRight = opts.StartDir, opts.StartDir
	}
	if a.startPane == 1 {
		a.active = 1
	}
	a.panels[0] = panel.New(0, opts.Lister)
	a.panels[1] = panel.New(1, opts.Lister)
	// Install the effective preferences before Init starts either panel's
	// first load. Each SetX updates the option without reloading an
	// uninitialized panel.
	a.panels[0].SetShowHidden(a.showHidden)
	a.panels[1].SetShowHidden(a.showHidden)
	a.panels[0].SetSort(a.defSort, a.defDesc)
	a.panels[1].SetSort(a.defSort, a.defDesc)
	a.panels[0].SetDirsFirst(a.defDirsFirst)
	a.panels[1].SetDirsFirst(a.defDirsFirst)
	var notices []string
	if opts.Notice != "" {
		notices = append(notices, opts.Notice)
	}
	if a.settingsErr != nil {
		notices = append(notices, "settings: "+a.settingsErr.Error())
	}
	if len(notices) > 0 {
		a.setStatusLevel(strings.Join(notices, " · "), lvlWarn)
	}
	return a
}

// Init loads both panels in their configured startup folders (settings:
// startup left/right, both overridden by Options.StartDir; empty = home).
func (a *App) Init() tea.Cmd {
	return tea.Batch(
		tea.SetWindowTitle("TwinPane"),
		a.panels[0].Load(expandHomeDir(a.startLeft), "", panel.LoadForce),
		a.panels[1].Load(expandHomeDir(a.startRight), "", panel.LoadForce),
	)
}

// applyPreferences installs loaded (or reset) preference values on the App.
// Panel-affecting values are pushed to the panels by the caller through
// syncPanelsWithPreferences; startup folders and the starting pane take
// effect on the next start.
func (a *App) applyPreferences(p settings.Preferences) {
	p = p.Normalized()
	a.showHidden = p.ShowHidden
	a.startLeft, a.startRight = p.Startup.Left, p.Startup.Right
	a.startPane = 0
	if p.Startup.Pane == "right" {
		a.startPane = 1
	}
	key, _ := fsys.ParseSortKey(p.Sort.Key)
	a.defSort, a.defDesc, a.defDirsFirst = key, p.Sort.Desc, p.Sort.DirsFirst
	a.focusPreview = p.FocusPreview
	a.noticeCorner, a.noticeDur = p.Notice.Corner, p.Duration()
	a.themeName = p.Theme
	if th, ok := theme.Preset(a.themeName); ok {
		a.theme = th
	} else {
		a.themeName, a.theme = "default", theme.Default()
	}
	a.editorCmd = p.Editor.Command
	a.tipsPref, a.mousePref = p.Editor.Tips, p.Mouse
	a.viewerWrap, a.viewerNums = p.Viewer.Wrap, p.Viewer.Numbers
	a.lastFormat = compress.ID(p.Compress.Format)
	for id, lv := range p.Compress.Levels {
		a.formatLevels[compress.ID(id)] = lv
	}
}

// syncPanelsWithPreferences applies the panel-affecting preferences to both
// panels. Reloading only happens for panels that have a path (before Init
// the options are simply set).
func (a *App) syncPanelsWithPreferences() tea.Cmd {
	return tea.Batch(
		a.panels[0].SetShowHidden(a.showHidden), a.panels[1].SetShowHidden(a.showHidden),
		a.panels[0].SetSort(a.defSort, a.defDesc), a.panels[1].SetSort(a.defSort, a.defDesc),
		a.panels[0].SetDirsFirst(a.defDirsFirst), a.panels[1].SetDirsFirst(a.defDirsFirst),
	)
}

// currentPrefs snapshots the preference fields as they are now, for saving.
// Session-only state (a sort cycled with s/S, live panel folders) does not
// belong here: only values the settings screen edits are included.
func (a *App) currentPrefs() settings.Preferences {
	pane := "left"
	if a.startPane == 1 {
		pane = "right"
	}
	levels := make(map[string]int, len(a.formatLevels))
	for id, lv := range a.formatLevels {
		if id != "" {
			levels[string(id)] = lv
		}
	}
	return settings.Preferences{
		ShowHidden:   a.showHidden,
		Startup:      settings.StartupPrefs{Left: a.startLeft, Right: a.startRight, Pane: pane},
		Sort:         settings.SortPrefs{Key: a.defSort.String(), Desc: a.defDesc, DirsFirst: a.defDirsFirst},
		FocusPreview: a.focusPreview,
		Notice:       settings.NoticePrefs{Corner: a.noticeCorner, Duration: int(a.noticeDur / time.Second)},
		Theme:        a.themeName,
		Editor:       settings.EditorPrefs{Command: a.editorCmd, Tips: a.tipsPref},
		Mouse:        a.mousePref,
		Viewer:       settings.ViewerPrefs{Wrap: a.viewerWrap, Numbers: a.viewerNums},
		Compress:     settings.CompressPrefs{Format: string(a.lastFormat), Levels: levels},
	}
}

// savePrefs writes the current preferences. feedback reports the outcome on
// the status line (settings screen changes); silent mode is for frequent
// updates that have their own message (h, the compress dialog).
func (a *App) savePrefs(feedback bool) {
	if a.settingsPath == "" {
		if feedback {
			a.setStatus("settings are not persisted in this session", false)
		}
		return
	}
	if err := settings.Save(a.settingsPath, a.currentPrefs()); err != nil {
		a.settingsErr = err
		if feedback {
			a.setStatusLevel("settings not saved: "+errText(err), lvlWarn)
		}
		return
	}
	a.settingsErr = nil
	if feedback {
		a.setStatus("settings saved", false)
	}
}

// Accessors (used by tests and future components).
func (a *App) Active() int               { return a.active }
func (a *App) Panel(i int) *panel.Panel  { return a.panels[i] }
func (a *App) ActivePanel() *panel.Panel { return a.panels[a.active] }
func (a *App) Status() (string, bool)    { return a.status, a.statusErr }
func (a *App) HelpVisible() bool         { return a.showHelp }
func (a *App) ShowHidden() bool          { return a.showHidden }

func (a *App) setStatus(msg string, isErr bool) {
	lvl := lvlInfo
	if isErr {
		lvl = lvlErr
	}
	a.setStatusLevel(msg, lvl)
}

func (a *App) setStatusLevel(msg string, lvl int) {
	a.status, a.statusErr, a.statusLevel = msg, lvl == lvlErr, lvl
}

// Busy reports whether any background file operation is still active.
func (a *App) Busy() bool { return a.busy() }

// DialogOpen reports whether a modal dialog is shown (used by tests).
func (a *App) DialogOpen() bool { return a.dialog != nil }

// Update handles messages.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	m, cmd := a.update(msg)
	if !a.quitting {
		a.showNextTaskPrompt()
	}
	if a.dialog != a.seenDialog {
		// a dialog opened (or was replaced): mouse clicks on it are ignored
		// for a moment so a double tap cannot press one of its buttons
		a.seenDialog = a.dialog
		a.dialogAt = a.now()
		a.press = pressState{}
	}
	a.syncSelections()
	a.syncBadges()
	a.syncMode()
	return m, a.syncNotice(a.syncSpinner(cmd))
}

// spinInterval is the frame time of the loading spinner.
const spinInterval = 100 * time.Millisecond

// spinTickMsg advances the loading spinners.
type spinTickMsg struct{}

// syncSpinner schedules the next spinner frame while a panel is loading
// (a remote listing can take seconds). One tick is pending at a time, and
// none once nothing loads, so an idle TwinPane does no work.
func (a *App) syncSpinner(cmd tea.Cmd) tea.Cmd {
	if a.spinning || !(a.panels[0].Loading() || a.panels[1].Loading()) {
		return cmd
	}
	a.spinning = true
	return tea.Batch(cmd, tea.Tick(spinInterval, func(time.Time) tea.Msg { return spinTickMsg{} }))
}

// syncSelections mirrors the selection while both panels show the same
// directory: they then share one selection set (the union of both when they
// meet), so selecting in one panel shows in the other at once. A panel that
// changes directory gets a fresh selection of its own (panel.Apply).
func (a *App) syncSelections() {
	l, r := a.panels[0], a.panels[1]
	same := l.Path() != "" && l.Path() == r.Path() && l.Err() == nil && r.Err() == nil
	switch {
	case same && !panel.SharesSelection(l, r):
		panel.ShareSelection(l, r)
	case !same && panel.SharesSelection(l, r):
		r.Unshare()
	}
}

func (a *App) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		a.layout()
		return a, nil

	case spinTickMsg:
		a.spinning = false
		for _, p := range a.panels {
			p.SpinnerTick()
		}
		return a, nil // Update schedules the next frame if still loading

	case noticeTickMsg:
		a.noticeAdvance(msg)
		return a, nil

	case compressPulseMsg:
		if cp := a.compressProgress; cp != nil && cp.id == msg.id && !cp.finished {
			cp.frame++
			a.updateCompressionDialog()
			return a, a.compressPulse(msg.id)
		}
		return a, nil

	case openDoneMsg:
		a.openDone(msg)
		return a, nil
	case runDoneMsg:
		return a, a.runDone(msg)
	case editorDoneMsg:
		return a, a.editorDone(msg)

	case tea.MouseMsg:
		return a, a.handleMouse(msg)

	case searchMsg:
		return a, a.searchUpdate(msg)

	case advancedSearchMsg:
		return a, a.advancedSearchUpdate(msg)

	case compareDoneMsg:
		a.comparisonDone(msg)
		return a, nil

	case folderSizeEventMsg:
		return a, a.folderSizeEvent(msg)

	case bulkDoneMsg:
		return a, a.bulkDone(msg)

	case panel.LoadedMsg:
		if msg.PanelID < 0 || msg.PanelID > 1 {
			return a, nil
		}
		p := a.panels[msg.PanelID]
		wasArc, wasRem := p.Archive(), p.Source()
		if err := p.Apply(msg); err != nil {
			switch {
			case cloud.KindOf(err) == cloud.KindCanceled:
				a.setStatusLevel("listing cancelled — the panel stays where it was", lvlWarn)
			case msg.Rem != nil:
				a.setStatus(msg.Rem.Remote.Name+": "+err.Error(), true)
			default:
				a.setStatus(err.Error(), true)
			}
		} else if p.Archive() != nil && wasArc == nil {
			a.archiveOpened(p)
		} else if src := p.Source(); src != nil && (wasRem == nil || wasRem.Remote != src.Remote) {
			a.remoteOpened(p)
		}
		return a, nil

	case remotesMsg:
		a.remotesLoaded(msg)
		return a, nil

	case viewLoadedMsg:
		return a, a.viewerLoaded(msg)
	case viewSyntaxMsg:
		a.viewerSyntaxLoaded(msg)
		return a, nil

	case tea.KeyMsg:
		if a.keyBlocked() {
			return a, nil
		}
		a.lastClick = clickState{} // a key between two taps is no double tap
		// A text field in a dialog receives the raw message, including
		// multi-rune input and bracketed paste.
		if a.dialog != nil && a.dialog.input != nil {
			return a, a.dialogKey(msg)
		}
		if a.dialog == nil && a.tasks.showing {
			return a, a.taskViewKey(msg)
		}
		// A notification is dismissed with Esc where Esc means nothing else.
		// Everywhere else Esc keeps its own meaning (closing the task list, a
		// dialog, leaving the live screen), so no shortcut is taken away.
		if a.notice != nil && msg.String() == "esc" && a.noticeEscDismisses() {
			a.dismissNotice()
			return a, nil
		}
		if a.dialog == nil && a.live != nil && !a.showHelp {
			return a, a.liveKey(msg)
		}
		if a.dialog == nil && a.folderSize != nil && !a.showHelp {
			return a, a.folderSizeKey(msg)
		}
		if a.dialog == nil && a.comparison != nil && !a.showHelp {
			return a, a.comparisonKey(msg)
		}
		if a.dialog == nil && msg.String() == "ctrl+c" {
			if s := a.tasks.latestActive(); s != nil {
				a.cancelTask(s.id)
				return a, nil
			}
		}
		// So does the help screen's filter field while it has the focus.
		if a.showHelp && a.help.typing && a.dialog == nil && msg.String() != "ctrl+c" {
			return a, a.helpKey(msg)
		}
		// Overlays with their own key handling (search results, bulk
		// rename) get the raw message too; they handle bursts themselves.
		// Phase 5 overlays: the Properties dialog and the viewer.
		if a.dialog == nil && a.props != nil && !a.showHelp {
			return a, a.propsKey(msg)
		}
		if a.dialog == nil && a.viewer != nil && !a.showHelp {
			return a, a.viewerKey(msg)
		}
		if a.dialog == nil && a.advancedSearch != nil && !a.showHelp {
			return a, a.advancedSearchKey(msg)
		}
		if a.dialog == nil && a.search != nil {
			return a, a.searchKey(msg)
		}
		if a.dialog == nil && a.bulk != nil {
			return a, a.bulkKey(msg)
		}
		// Several runes in one KeyMsg come from key auto-repeat ("jjj"),
		// very fast typing, typeahead buffered while TwinPane was busy, or
		// terminal replies that arrived late (e.g. after the editor exited).
		// Only the first rune is treated as a command; the rest are applied
		// only while they are movement/selection keys, and the burst stops at
		// the first other rune. So "e:wq" typed ahead opens the editor but
		// never quits, and "dy" cannot answer the dialog that "d" opened.
		// Alt-prefixed bursts are unparsed escape sequences and pastes have
		// no target outside text fields: both are dropped.
		if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 {
			if msg.Paste || msg.Alt {
				return a, nil
			}
			var cmds []tea.Cmd
			before := a.uiState()
			for i, r := range msg.Runes {
				// Movement keys may continue the burst only while nothing
				// else changed: no dialog opened or closed, no overlay,
				// operation, help screen or editor started, not quitting.
				if i > 0 && (!burstSafe(r) || a.uiState() != before) {
					break
				}
				_, cmd := a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
				cmds = append(cmds, cmd)
			}
			return a, tea.Batch(cmds...) // per-panel load sequencing drops stale results
		}
		return a.handleKey(msg)
	}
	if cmd, ok := a.handleOpMsg(msg); ok {
		return a, cmd
	}
	return a, nil
}

// uiState captures what decides how the next key is interpreted (see the
// burst handling in Update).
type uiState struct {
	dialog                                  *dialog
	settings                                bool
	search                                  *searchView
	advancedSearch                          *advancedSearchView
	bulk                                    *bulkRename
	tasks                                   bool
	busy, quitting, editing, help, inputBox bool
	viewer                                  *viewer
	props                                   *propsView
	comparison                              *comparisonView
	folderSize                              *folderSizeView
}

func (a *App) uiState() uiState {
	return uiState{
		dialog: a.dialog, search: a.search, advancedSearch: a.advancedSearch, bulk: a.bulk, tasks: a.tasks.showing,
		settings: a.showSettings,
		busy:     a.busy(), quitting: a.quitting, editing: a.editing, help: a.showHelp,
		inputBox: a.dialog != nil && a.dialog.input != nil, viewer: a.viewer, props: a.props,
		comparison: a.comparison, folderSize: a.folderSize,
	}
}

func (a *App) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	k := a.keys
	if a.dialog != nil {
		return a, a.dialogKey(msg)
	}
	if a.tasks.showing {
		return a, a.taskViewKey(msg)
	}
	if msg.String() == "ctrl+c" {
		if s := a.tasks.latestActive(); s != nil {
			a.cancelTask(s.id)
			return a, nil
		}
		a.quitting = true
		return a, tea.Quit
	}
	if a.showHelp {
		return a, a.helpKey(msg) // help.go
	}
	if a.showSettings {
		return a, a.settingsKey(msg) // settings.go
	}
	if a.quitting {
		return a, nil
	}
	if msg.Type == keyFocusMode || msg.String() == focusKey {
		// The focus-mode toggle. Ctrl+O is a plain Ctrl-only key event, so
		// it works in terminals and on-screen keyboards (Termux, Gboard)
		// that cannot report modified keys without an enhanced protocol.
		// Every overlay (dialog, tasks, help, viewer, search, properties,
		// bulk rename, folder sizes, comparison) returned above, so the
		// toggle can never fire from inside one.
		return a, a.toggleFocus()
	}

	a.setStatus("", false)
	p := a.panels[a.active]
	if p.InRemote() {
		if what := a.remoteUnsupported(msg); what != "" {
			a.refuseOnRemote(what)
			return a, nil
		}
	}
	switch {
	case k.Tasks.Matches(msg):
		a.openTasks()
	case k.Quit.Matches(msg):
		if s := a.tasks.latestActive(); s != nil {
			a.openTasks()
			a.setStatusLevel("Background operations are still running; wait or cancel them in the task view before quitting", lvlWarn)
			return a, nil
		}
		a.quitting = true
		return a, tea.Quit
	case k.Help.Matches(msg):
		a.openHelp()
	case k.Settings.Matches(msg):
		return a, a.openSettings()
	case k.Switch.Matches(msg):
		a.active = 1 - a.active
	case k.Up.Matches(msg):
		p.Up()
	case k.Down.Matches(msg):
		p.Down()
	case k.PageUp.Matches(msg):
		p.PageUp()
	case k.PageDown.Matches(msg):
		p.PageDown()
	case k.Top.Matches(msg):
		p.Top()
	case k.Bottom.Matches(msg):
		p.Bottom()
	case k.Open.Matches(msg):
		if cmd := p.Open(); cmd != nil {
			return a, cmd
		}
		if e, ok := p.Current(); ok && !e.IsDir {
			return a, a.openFile(p, e, false)
		}
	case k.Parent.Matches(msg):
		return a, p.Parent()
	case k.Back.Matches(msg):
		return a, a.navMove(p, true)
	case k.Forward.Matches(msg):
		return a, a.navMove(p, false)
	case k.Home.Matches(msg):
		if p.InRemote() {
			return a, p.GoLocal(fsys.HomeDir(), "")
		}
		return a, p.ChangeDir(fsys.HomeDir())
	case k.SyncOther.Matches(msg):
		other := a.panels[1-a.active]
		focus := ""
		if e, ok := p.Current(); ok && !e.IsParent {
			focus = e.Name
		}
		switch {
		case p.InRemote():
			// the other panel shows the same remote folder
			return a, other.OpenRemoteAt(p.Source(), p.Location(), focus)
		case other.InRemote() && !p.InArchive():
			return a, other.GoLocal(p.Path(), focus)
		}
		if p.InArchive() {
			// the other panel shows the folder containing the archive
			if other.InRemote() {
				return a, other.GoLocal(p.RealDir(), filepath.Base(p.Archive().Path))
			}
			return a, other.Load(p.RealDir(), filepath.Base(p.Archive().Path), panel.LoadNavigate)
		}
		return a, other.Load(p.Path(), focus, panel.LoadNavigate)
	case k.Compare.Matches(msg):
		return a, a.beginDirectoryCompare()
	case k.FolderSize.Matches(msg):
		return a, a.beginFolderSize()
	case k.Select.Matches(msg):
		p.ToggleSelect()
	case k.SelectAll.Matches(msg):
		p.SelectAll()
	case k.Unselect.Matches(msg):
		p.ClearSelection()
	case k.Hidden.Matches(msg):
		return a, a.toggleHidden()
	case k.Sort.Matches(msg):
		cmd := p.CycleSort()
		a.setStatus("sort by "+p.Options().SortLabel()+" (S reverses)", false)
		return a, cmd
	case k.Reverse.Matches(msg):
		cmd := p.ReverseSort()
		dir := map[bool]string{true: "descending", false: "ascending"}[p.Options().Desc]
		a.setStatus("sort by "+p.Options().SortLabel()+" ("+dir+")", false)
		return a, cmd
	case k.Search.Matches(msg):
		return a, a.beginSearch()
	case k.Refresh.Matches(msg):
		return a, a.refreshBoth()
	case k.Sources.Matches(msg):
		return a, a.openSources()
	case k.Mkdir.Matches(msg):
		return a, a.beginMkdir()
	case k.Copy.Matches(msg):
		if p.InArchive() {
			return a, a.beginExtract(false) // copying out of an archive = extracting
		}
		return a, a.beginTransfer(ops.Copy)
	case k.Move.Matches(msg):
		return a, a.beginTransfer(ops.Move)
	case k.Rename.Matches(msg):
		return a, a.beginRename()
	case k.Delete.Matches(msg):
		return a, a.beginDelete()
	case k.Edit.Matches(msg):
		return a, a.beginEdit()
	case k.EditWith.Matches(msg):
		return a, a.beginEditWith()
	case k.Properties.Matches(msg):
		return a, a.openProps()
	case k.View.Matches(msg):
		return a, a.openViewerFor(p)
	case k.Extract.Matches(msg):
		return a, a.beginExtract(false)
	case k.ExtractAll.Matches(msg):
		return a, a.beginExtract(p.InArchive())
	case k.Zip.Matches(msg):
		return a, a.beginZip()
	case k.OpenExt.Matches(msg):
		return a, a.openExternal()
	case k.OpenWith.Matches(msg):
		return a, a.openWith()
	case k.Menu.Matches(msg):
		return a, a.openMenu()
	case k.Bookmarks.Matches(msg):
		return a, a.openBookmarks(0, "")
	case k.Bookmark.Matches(msg):
		return a, a.bookmarkHere()
	case k.Storage.Matches(msg):
		return a, a.showStorage()
	case a.focus && msg.String() == "p":
		// focus mode only: the preview pane (the panels have no p key)
		return a, a.togglePreview()
	case a.focus && msg.String() == "ctrl+g":
		// focus mode only: the panels' Ctrl+G equivalent, a typed
		// local folder or remote location (see focus.go)
		return a, a.openGoTo()
	case k.Cancel.Matches(msg):
		if p.Loading() && p.CancelLoad() {
			return a, nil // the cancelled load reports itself
		}
		if s := a.tasks.latestActive(); s != nil {
			a.cancelTask(s.id)
			return a, nil
		}
		if p.InArchive() {
			return a, p.LeaveArchive()
		}
		// nothing to cancel: just clear the status line (done above)
	}
	return a, nil
}

// layout distributes the terminal size between the panels.
func (a *App) layout() {
	ph := a.height - 3 // title, status, footer
	if ph < 0 {
		ph = 0
	}
	if a.focus {
		// Only the visible panel is resized: the hidden one keeps the size
		// it has in two-pane mode, so its cursor and scroll offset are not
		// clamped and it looks exactly as before when both are shown again.
		a.panels[a.active].SetSize(a.focusPanelWidth(), ph)
		return
	}
	lw := a.width / 2
	a.panels[0].SetSize(lw, ph)
	a.panels[1].SetSize(a.width-lw, ph)
}

// View renders the whole screen.
func (a *App) View() string {
	if a.quitting {
		return ""
	}
	if a.width == 0 || a.height == 0 {
		return "Starting TwinPane…"
	}
	if a.width < minWidth || a.height < minHeight {
		msg := fmt.Sprintf("Terminal too small (%dx%d)\nneed at least %dx%d", a.width, a.height, minWidth, minHeight)
		return lipgloss.Place(a.width, a.height, lipgloss.Center, lipgloss.Center, msg)
	}

	var body string
	switch {
	case a.showHelp:
		body = a.helpView(a.height - 3)
	case a.tasks.showing:
		body = a.taskViewRender(a.height - 3)
	case a.live != nil:
		body = a.liveViewRender(a.height - 3)
	case a.viewer != nil:
		body = a.viewerRender(a.height - 3)
	case a.advancedSearch != nil:
		body = a.advancedSearchRender(a.height - 3)
	case a.search != nil:
		body = a.searchViewRender(a.height - 3)
	case a.bulk != nil:
		body = a.bulkView(a.height - 3)
	case a.folderSize != nil:
		body = a.folderSizeViewRender(a.height - 3)
	case a.comparison != nil:
		body = a.comparisonViewRender(a.height - 3)
	case a.focus:
		body = a.focusView(a.height - 3)
	default:
		body = lipgloss.JoinHorizontal(lipgloss.Top,
			a.panels[0].View(a.theme, a.active == 0),
			a.panels[1].View(a.theme, a.active == 1))
	}
	screen := strings.Join([]string{a.titleView(), body, a.statusView(), a.footerView()}, "\n")
	screen = a.withNotice(screen)
	if a.showSettings && !a.showHelp {
		// The settings window floats above the panes (and anything else
		// behind): the directory stays visible, dimmed by the backdrop,
		// exactly like the dialogs. A dialog opened from the screen is
		// composited on top below.
		screen = overlay(screen, a.settingsView(), a.width, a.height, a.theme.Backdrop)
	}
	if a.props != nil && !a.showHelp {
		screen = overlay(screen, a.renderProps(), a.width, a.height, a.theme.Backdrop)
	}
	if a.dialog != nil {
		return overlay(screen, a.renderDialog(a.dialog), a.width, a.height, a.theme.Backdrop)
	}
	return screen
}

func (a *App) titleView() string {
	badge := a.theme.TitleBadge.Render("TwinPane")
	side := "left"
	if a.active == 1 {
		side = "right"
	}
	ver := ""
	if a.opts.Version != "" {
		ver = " " + a.opts.Version
	}
	hidden := "hidden: off"
	if a.showHidden {
		hidden = "hidden: shown"
	}
	where := "active: " + side + " panel"
	if a.focus {
		where = "focus · " + side + " panel"
	}
	right := a.theme.TitleText.Render(fmt.Sprintf("%s · %s ", hidden, where))
	if lipgloss.Width(badge+a.theme.TitleText.Render(ver))+lipgloss.Width(right) >= a.width {
		right = a.theme.TitleText.Render(fmt.Sprintf("active: %s ", side))
	}
	left := badge + a.theme.TitleText.Render(ver)
	gap := a.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return ansi.Truncate(left, a.width, "")
	}
	return left + strings.Repeat(" ", gap) + right
}

func (a *App) statusView() string {
	text, st := a.status, a.theme.StatusInfo
	switch {
	case text == "":
	case a.statusErr:
		st = a.theme.StatusErr
		text = "✗ " + text
	case a.statusLevel == lvlOK:
		st = a.theme.StatusOK
		text = "✓ " + text
	case a.statusLevel == lvlWarn:
		st = a.theme.StatusWarn
		text = "! " + text
	}
	if text == "" {
		text = a.taskCountText()
	}
	if a.viewer != nil && text == "" {
		text = a.viewerPosition()
	}
	if a.props != nil && text == "" && a.props.info != nil {
		text = a.props.info.Path
	}
	if text == "" {
		if e, ok := a.ActivePanel().Current(); ok && !e.IsParent {
			text = e.Path
		} else {
			text = a.ActivePanel().Path()
		}
	}
	text = fsys.SanitizeName(text)
	return st.Render(fitWidth(" "+text, a.width))
}

// toggleHidden flips the one hidden-files setting and applies it to both
// panels at once. Each panel keeps its cursor entry (or the nearest
// neighbour if that entry is now hidden) on the same screen row.
func (a *App) toggleHidden() tea.Cmd {
	a.showHidden = !a.showHidden
	message := "hiding hidden files in both panels (h shows them)"
	if a.showHidden {
		message = "showing hidden files in both panels (h hides them)"
	}
	level := lvlInfo
	if a.settingsPath != "" {
		if err := settings.Save(a.settingsPath, a.currentPrefs()); err != nil {
			a.settingsErr = err
			message += "; not saved: " + errText(err)
			level = lvlWarn
		} else {
			a.settingsErr = nil
		}
	} else if a.settingsErr != nil {
		message += "; not saved: " + errText(a.settingsErr)
		level = lvlWarn
	}
	a.setStatusLevel(message, level)
	return tea.Batch(a.panels[0].SetShowHidden(a.showHidden), a.panels[1].SetShowHidden(a.showHidden))
}

func fitWidth(s string, w int) string {
	if ansi.StringWidth(s) > w {
		return ansi.Truncate(s, w, "…")
	}
	return s + strings.Repeat(" ", w-ansi.StringWidth(s))
}
