package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/activity"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/history"
	"github.com/ThatOn3Gu7/TwinPane/internal/proc"
	"github.com/ThatOn3Gu7/TwinPane/internal/settings"
)

// The User Activity Log bridge (internal/activity).
//
// The log is written from a handful of central places rather than from every
// action, so it stays cheap and complete:
//
//   - the command trace hook, which sees every external program TwinPane
//     starts (rclone, 7-Zip, the editor's helpers, openers, search tools);
//   - the status line, where every warning and error the user is told about
//     ends up;
//   - a diff of the dialog and screen state taken in Update, which catches
//     every dialog and overlay without a call at each of the ~100 places that
//     assign a.dialog;
//   - the navigation funnel (a panel finishing a load with a new location);
//   - the task, operation and cloud funnels;
//   - the settings screen, for preference changes.
//
// What is never recorded, by design and enforced by the callers: the contents
// of a text field (a new name, a path typed into a prompt), search and filter
// queries, the clipboard, the editor's command input, the environment, and
// per-tick progress updates. Command arguments and error text are recorded,
// but only after internal/activity has redacted them.

// initActivityLog opens the log. Called from New once the preferences and the
// operation history are known: the log's run ID is the history's session ID,
// so an entry and an operation from the same run can be read together.
func (a *App) initActivityLog(path string, errText string, override string) {
	if a.actLog != nil || a.activityPath != "" {
		return // already opened (New only calls this once)
	}
	a.activityPath = path
	if errText != "" {
		a.actErr = fmt.Errorf("%s", errText)
		return
	}
	if path == "" {
		// No path (tests, embedders): logging stays off. Nothing is created.
		a.actLog = nil
		return
	}
	mode := a.actMode
	if override != "" {
		mode = activity.ParseMode(override)
	}
	maxKiB, keepDays := a.actMaxKiB, a.actKeepDays
	if a.opts.ActivityMaxKiB > 0 {
		maxKiB = settings.ClampActivityMaxKiB(a.opts.ActivityMaxKiB)
	}
	if a.opts.ActivityKeepDays > 0 {
		keepDays = settings.ClampActivityKeepDays(a.opts.ActivityKeepDays)
	}
	run := ""
	if a.history != nil {
		run = a.history.CurrentID()
	}
	store, err := activity.Open(path, activity.Options{
		Mode:     mode,
		MaxBytes: int64(maxKiB) << 10,
		Keep:     activity.DefaultKeep,
		KeepDays: keepDays,
		Run:      run,
		Version:  a.opts.Version,
		Now:      a.now,
	})
	if err != nil {
		a.actErr = err
		return
	}
	a.actLog = store
	a.actMode = mode
	a.installTrace()
	a.logActivity(activity.Entry{
		Level: activity.LevelInfo.String(), Cat: activity.CatRecovery, Ev: "activity.start",
		Msg:  fmt.Sprintf("TwinPane %s started · logging %s", versionLabel(a.opts.Version), mode.Label()),
		Data: map[string]string{"mode": mode.String(), "file": filepath.Base(path)},
	})
	if a.actErr != nil {
		a.logActivity(activity.Entry{
			Level: activity.LevelWarn.String(), Cat: activity.CatRecovery, Ev: "activity.warn",
			Msg: "activity log: " + a.actErr.Error(),
		})
	}
}

func versionLabel(v string) string {
	if v == "" {
		return "dev"
	}
	return v
}

// closeActivityLog records the end of the run and closes the file.
func (a *App) closeActivityLog() {
	if a.actLog == nil {
		return
	}
	if a.actRestore != nil {
		a.actRestore()
		a.actRestore = nil
	}
	a.logActivity(activity.Entry{
		Level: activity.LevelInfo.String(), Cat: activity.CatRecovery, Ev: "activity.exit",
		Msg: "TwinPane exited",
	})
	_ = a.actLog.Close()
	a.actLog = nil
}

// ActivityLog exposes the log (tests).
func (a *App) ActivityLog() *activity.Store { return a.actLog }

// logEnabled reports whether an entry of this level would be written, so a
// caller can skip building an expensive message.
func (a *App) logEnabled(l activity.Level) bool {
	if a == nil || a.actLog == nil {
		return false
	}
	return a.actLog.Enabled() && activityRecords(a.actMode, l)
}

// activityRecords reports whether mode records at least level l.
func activityRecords(m activity.Mode, l activity.Level) bool {
	switch m {
	case activity.ModeOff:
		return false
	case activity.ModeWarnings:
		return l >= activity.LevelWarn
	case activity.ModeVerbose:
		return true
	}
	return l >= activity.LevelInfo
}

// logActivity writes one entry. It is safe to call with no store, from any
// goroutine, and it never returns an error: logging must not disturb the work
// it describes.
func (a *App) logActivity(e activity.Entry) {
	if a == nil || a.actLog == nil {
		return
	}
	if e.Time.IsZero() {
		e.Time = a.now()
	}
	a.actLog.Log(e)
}

// logf is logActivity with a formatted message.
func (a *App) logf(l activity.Level, cat activity.Category, ev, format string, args ...any) {
	if a == nil || a.actLog == nil {
		return
	}
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	a.logActivity(activity.Entry{Level: l.String(), Cat: cat, Ev: ev, Msg: msg})
}

// activityLevel maps a status-line level to a log level.
func activityLevel(lvl int) activity.Level {
	switch lvl {
	case lvlErr:
		return activity.LevelError
	case lvlWarn:
		return activity.LevelWarn
	}
	return activity.LevelInfo
}

// ---- commands ----------------------------------------------------------------

// installTrace makes every external program TwinPane starts leave two entries
// in the log: a start event (verbose only) and an end event with the exit code
// and the duration. Arguments are passed to the store unredacted on purpose;
// the store redacts them before anything is encoded.
func (a *App) installTrace() {
	if a.actLog == nil {
		return
	}
	hook := func(ev proc.TraceEvent) { a.traceActivity(ev) }
	a.actRestore = proc.SetTrace(hook)
}

// traceActivity turns one trace event into a log entry.
func (a *App) traceActivity(ev proc.TraceEvent) {
	if a == nil || a.actLog == nil || len(ev.Args) == 0 {
		return
	}
	name := commandName(ev.Args)
	if !ev.Done {
		if !a.logEnabled(activity.LevelDebug) {
			return
		}
		a.logActivity(activity.Entry{
			Level: activity.LevelDebug.String(), Cat: activity.CatCommand, Ev: "command.start",
			Msg: commandLine(ev.Args),
		})
		return
	}
	level := activity.LevelInfo
	status := "exit 0"
	switch {
	case ev.Err != nil:
		level, status = activity.LevelError, "not waited for"
	case ev.Status.Signal != 0:
		level, status = activity.LevelError, "signal "+actItoa(int(ev.Status.Signal))
	case ev.Status.Code != 0:
		level, status = activity.LevelError, "exit "+actItoa(ev.Status.Code)
	}
	msg := name
	if a.logEnabled(activity.LevelDebug) {
		msg = commandLine(ev.Args) // verbose: the whole (redacted) command line
	}
	e := activity.Entry{
		Level: level.String(), Cat: activity.CatCommand, Ev: "command.done",
		Msg: msg, Status: status, DurMS: ev.Elapsed.Milliseconds(), Code: ev.Status.Code,
	}
	if ev.Err != nil {
		e.Err = ev.Err.Error()
	}
	a.logActivity(e)
}

// commandName is the executable's base name plus its subcommand, which is what
// identifies a command at a glance: "rclone lsf", "7z a", "xdg-open".
func commandName(args []string) string {
	if len(args) == 0 {
		return ""
	}
	name := filepath.Base(args[0])
	for _, a := range args[1:] {
		if a == "" || strings.HasPrefix(a, "-") {
			continue
		}
		return name + " " + a
	}
	return name
}

// commandLine is the whole argument list, for verbose mode.
func commandLine(args []string) string {
	if len(args) == 0 {
		return ""
	}
	out := make([]string, 0, len(args))
	out = append(out, filepath.Base(args[0]))
	out = append(out, args[1:]...)
	return strings.Join(out, " ")
}

// ---- status line -------------------------------------------------------------

// logStatus records a warning or an error the user was told about (and, in
// verbose mode, ordinary status messages too). Empty messages are ignored:
// clearing the status line is not an event.
func (a *App) logStatus(msg string, lvl int) {
	if a == nil || a.actLog == nil || msg == "" {
		return
	}
	level := activityLevel(lvl)
	switch lvl {
	case lvlWarn:
		level = activity.LevelWarn
	case lvlErr:
		level = activity.LevelError
	default:
		// Ordinary status messages are frequent; they are verbose detail.
		level = activity.LevelDebug
	}
	if !a.logEnabled(level) {
		return
	}
	ev := "status.info"
	switch level {
	case activity.LevelWarn:
		ev = "status.warn"
	case activity.LevelError:
		ev = "status.error"
	}
	a.logf(level, activity.CatUI, ev, "%s", msg)
}

// ---- navigation --------------------------------------------------------------

// logNavigation records a panel arriving somewhere new. Refreshes (the same
// location again) are not events.
func (a *App) logNavigation(panelID int, from, to string) {
	if a == nil || a.actLog == nil || from == to || to == "" {
		return
	}
	p := a.panels[panelID]
	kind := "local"
	switch {
	case p.InRemote():
		kind = "remote"
	case p.InArchive():
		kind = "archive"
	}
	a.logActivity(activity.Entry{
		Level: activity.LevelInfo.String(), Cat: activity.CatNavigation, Ev: "nav.open",
		Msg: to, Panel: panelLetter(panelID), Kind: kind,
	})
}

func panelLetter(i int) string {
	if i == 1 {
		return "R"
	}
	return "L"
}

// ---- dialogs and screens -----------------------------------------------------

// logUIChanges records dialog and overlay transitions by comparing the current
// state with the previous frame's. Cheap (two pointer/string comparisons),
// complete (no call site can forget to log), and it sees a dialog's title
// without ever looking at its input field's contents.
func (a *App) logUIChanges() {
	if a == nil || a.actLog == nil {
		return
	}
	// Dialogs.
	if a.dialog != a.actDialog {
		prev := a.actDialog
		a.actDialog = a.dialog
		if prev != nil && a.dialog == nil {
			a.logf(activity.LevelInfo, activity.CatUI, "ui.dialog.close", "closed %s", sanitizeTitle(prev.title))
		}
		if a.dialog != nil {
			kind := "confirm"
			switch {
			case a.dialog.input != nil:
				kind = "input" // the value is never recorded
			case a.dialog.filter != nil:
				kind = "pick list"
			}
			a.logActivity(activity.Entry{
				Level: activity.LevelInfo.String(), Cat: activity.CatUI, Ev: "ui.dialog",
				Msg: sanitizeTitle(a.dialog.title), Kind: kind,
			})
		}
	}
	// Screens and overlays.
	if s := a.screenName(); s != a.actScreen {
		prev := a.actScreen
		a.actScreen = s
		if s != "browse" || prev != "" {
			from, to := prev, s
			if prev == "" {
				from = "browse"
			}
			a.logActivity(activity.Entry{
				Level: activity.LevelInfo.String(), Cat: activity.CatUI, Ev: "ui.screen",
				Msg:  fmt.Sprintf("%s → %s", from, to),
				Kind: screenKind(s),
			})
		}
	}
}

// sanitizeTitle keeps a dialog title printable and short.
func sanitizeTitle(s string) string {
	s = fsys.SanitizeName(strings.TrimSpace(s))
	if s == "" {
		return "dialog"
	}
	return s
}

// screenName is the name of the screen on top. It mirrors the order of View
// (the first match wins), so it changes exactly when the visible screen does.
func (a *App) screenName() string {
	switch {
	case a.editing:
		return "editor"
	case a.showHelp:
		return "help"
	case a.tasks.showing:
		return "tasks"
	case a.viewer != nil:
		return "viewer"
	case a.advancedSearch != nil:
		return "search"
	case a.search != nil:
		return "search"
	case a.bulk != nil:
		return "bulk rename"
	case a.folderSize != nil:
		return "folder sizes"
	case a.comparison != nil:
		return "compare"
	case a.props != nil:
		return "properties"
	case a.live != nil:
		return "live progress"
	case a.actView != nil:
		return "activity log"
	case a.showSettings:
		return "settings"
	case a.focus:
		return "focus mode"
	}
	return "browse"
}

// screenKind groups screens for the viewer.
func screenKind(s string) string {
	switch s {
	case "editor", "bulk rename":
		return "work"
	case "viewer", "search", "properties", "folder sizes", "compare", "live progress":
		return "overlay"
	case "help", "tasks", "settings", "activity log":
		return "screen"
	case "browse":
		return "browse"
	}
	return "mode"
}

// ---- keyboard shortcuts ------------------------------------------------------

// logKeyAction records a keyboard shortcut in verbose mode only. It is a
// different event from a command: "shortcut f7" is the user asking for a new
// folder, "command.done" is a program TwinPane ran.
func (a *App) logKeyAction(msg tea.KeyMsg) {
	if !a.logEnabled(activity.LevelDebug) {
		return
	}
	for _, k := range keyActions {
		if a.is(k.action, msg) {
			a.logf(activity.LevelDebug, activity.CatUI, "key.action", "%s: %s", k.label, a.label(k.action))
			return
		}
	}
}

// keyAction is one named binding, for the verbose shortcut log.
type keyAction struct {
	label  string
	action string
}

// keyActions names the bindings that the verbose log reports. It is a static
// list (not built from the registry by reflection) so a new binding has to be
// named here deliberately rather than silently flooding the log. The keys come
// from the active key map, so a rebound shortcut is logged under its real key.
var keyActions = []keyAction{
	{"switch panel", "browse.switch"}, {"open", "browse.open"}, {"parent", "browse.parent"},
	{"copy", "browse.copy"}, {"move", "browse.move"}, {"rename", "browse.rename"},
	{"delete", "browse.delete"}, {"edit", "browse.edit"}, {"view", "browse.view"},
	{"new menu", "browse.new"}, {"new folder", "browse.mkdir"}, {"compress", "browse.compress"},
	{"extract", "browse.extract"}, {"properties", "browse.properties"}, {"search", "browse.search"},
	{"tasks", "browse.tasks"}, {"settings", "browse.settings"}, {"sources", "browse.sources"},
	{"refresh", "browse.refresh"}, {"hidden files", "browse.hidden"}, {"sort", "browse.sort"},
	{"bookmarks", "browse.bookmarks"}, {"storage", "browse.storage"}, {"menu", "browse.menu"},
}

// ---- tasks, operations, cloud ------------------------------------------------

// logTaskStart records a background task starting.
func (a *App) logTaskStart(s *opSession) {
	if a == nil || s == nil {
		return
	}
	msg := s.title
	if len(s.from) > 0 {
		msg += ": " + strings.Join(s.from, ", ")
	}
	if s.to != "" {
		msg += " → " + s.to
	}
	a.logActivity(activity.Entry{
		Level: activity.LevelInfo.String(), Cat: activity.CatTask, Ev: "task.start",
		Msg: msg, Op: s.title, Status: "running",
	})
}

// logTaskDone records a task's final state.
func (a *App) logTaskDone(s *opSession) {
	if a == nil || s == nil {
		return
	}
	l := activity.LevelInfo
	switch s.state {
	case taskFailed:
		l = activity.LevelError
	case taskPartial, taskCanceled:
		l = activity.LevelWarn
	}
	msg := s.title
	if s.summary != "" {
		msg += ": " + s.summary
	}
	dur := int64(0)
	if !s.started.IsZero() && !s.finished.IsZero() {
		dur = s.finished.Sub(s.started).Milliseconds()
	}
	a.logActivity(activity.Entry{
		Level: l.String(), Cat: activity.CatTask, Ev: "task.done",
		Msg: msg, Op: s.title, Status: taskStateWord(s), DurMS: dur,
		Err: firstLine(s.summary),
	})
}

// logTaskCancel records the user stopping a task.
func (a *App) logTaskCancel(s *opSession) {
	if a == nil || s == nil {
		return
	}
	a.logActivity(activity.Entry{
		Level: activity.LevelWarn.String(), Cat: activity.CatTask, Ev: "task.cancel",
		Msg: "cancelled " + s.title, Op: s.title, Status: "cancelling",
	})
}

// logOperation records an operation that went into the history (file work).
func (a *App) logOperation(r history.Record) {
	if a == nil || a.actLog == nil {
		return
	}
	l := activity.LevelInfo
	switch r.Status {
	case history.StatusFailed:
		l = activity.LevelError
	case history.StatusPartial, history.StatusCanceled:
		l = activity.LevelWarn
	}
	msg := r.Op
	if r.Dest != "" {
		msg += " → " + r.Dest
	} else if len(r.Sources) > 0 {
		msg += " " + strings.Join(r.Sources, ", ")
	}
	if len(r.Errors) > 0 {
		msg += ": " + firstLine(r.Errors[0])
	} else if r.Summary != "" {
		msg += ": " + firstLine(r.Summary)
	}
	a.logActivity(activity.Entry{
		Level: l.String(), Cat: activity.CatOperation, Ev: "op.done",
		Msg: msg, Op: r.Op, Status: string(r.Status),
		Data: map[string]string{"files": actItoa(r.Files), "bytes": fmt.Sprintf("%d", r.Bytes)},
	})
}

// logSetting records a preference change from the Settings screen.
func (a *App) logSetting(r settingsRow) {
	if a == nil || a.actLog == nil {
		return
	}
	msg := r.label
	if r.text {
		// A text row: the label says what changed, the typed value is not
		// recorded (it may be a path the user would rather not write down).
		msg += ": changed"
	} else if r.value != "" {
		msg += " = " + r.value
	}
	a.logActivity(activity.Entry{
		Level: activity.LevelInfo.String(), Cat: activity.CatSettings, Ev: "settings.change", Msg: msg,
	})
}

// logRecovery records something TwinPane recovered from: a damaged file, a
// fallback, an unusable log.
func (a *App) logRecovery(ev, msg string) {
	a.logf(activity.LevelWarn, activity.CatRecovery, ev, "%s", msg)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// itoa is a tiny integer formatter used in short messages.
func actItoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// ---- the viewer --------------------------------------------------------------

// openActivityView shows the log. The snapshot is taken once and refreshed on
// demand: the log is append-only, so nothing already listed changes.
func (a *App) openActivityView() tea.Cmd {
	if a.actView != nil {
		return nil
	}
	v := &activityView{}
	a.actView = v
	// Log first, then snapshot: the viewer's own action belongs to this
	// session's block, so opening it does not look like an earlier run.
	a.logf(activity.LevelInfo, activity.CatUI, "ui.view", "opened the activity log")
	v.reload(a)
	return nil
}

func (a *App) closeActivityView() tea.Cmd {
	if a.actView == nil {
		return nil
	}
	a.actView = nil
	a.setStatus("", false)
	return nil
}
