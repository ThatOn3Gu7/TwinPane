package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/activity"
	"github.com/ThatOn3Gu7/TwinPane/internal/proc"
	"github.com/ThatOn3Gu7/TwinPane/internal/settings"
)

// activityApp is newOpsApp with the activity log switched on in a temp file.
// The app has already run Init, so the store is opened here exactly as New
// would (same run ID, same options) and the trace hook installed.
func activityApp(t *testing.T, mode activity.Mode) (*harness, *opsEnv, string) {
	t.Helper()
	h, env := newOpsApp(t, nil, nil)
	path := filepath.Join(env.root, "config", activity.FileName)
	clock := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	h.a.now = func() time.Time { return clock }
	h.a.activityPath = path
	h.a.actMode = mode
	h.a.actMaxKiB, h.a.actKeepDays = 1024, 30
	store, err := activity.Open(path, activity.Options{
		Mode: mode, Run: "run-test", Now: h.a.now, MaxBytes: 1 << 20, KeepDays: 30,
	})
	if err != nil {
		t.Fatalf("open activity log: %v", err)
	}
	h.a.actLog = store
	h.a.installTrace()
	t.Cleanup(func() { _ = store.Close() })
	return h, env, path
}

// activityEntries reads the log newest first.
func activityEntries(t *testing.T, path string) []activity.Entry {
	t.Helper()
	return activity.Read(path, 5000).Entries
}

// eventNames lists the events in the log, oldest first.
func eventNames(entries []activity.Entry) []string {
	out := make([]string, 0, len(entries))
	for i := len(entries) - 1; i >= 0; i-- {
		out = append(out, entries[i].Ev)
	}
	return out
}

func hasEvent(entries []activity.Entry, ev string) bool {
	for _, e := range entries {
		if e.Ev == ev {
			return true
		}
	}
	return false
}

func countCat(entries []activity.Entry, cat activity.Category) int {
	n := 0
	for _, e := range entries {
		if e.Cat == cat {
			n++
		}
	}
	return n
}

// TestActivityLogRecordsCoverage proves the central hooks really see the
// actions they claim to: a screen, a dialog, navigation, a warning and the
// log being opened.
func TestActivityLogRecordsCoverage(t *testing.T) {
	h, env, path := activityApp(t, activity.ModeActions)
	h.send(tea.WindowSizeMsg{Width: 110, Height: 30})

	h.press("f9") // settings: a screen
	h.press("esc")
	h.press("f7") // new folder: a dialog
	h.press("esc")
	h.press("t") // tasks: a screen
	h.press("esc")
	h.press("L") // the log itself
	h.press("esc")
	h.press("enter") // open the selected entry (a folder or a file)
	h.settle()
	h.press("esc") // a warning or nothing at all; either way it is safe

	entries := activityEntries(t, path)
	if len(entries) == 0 {
		t.Fatal("nothing was logged")
	}
	evs := eventNames(entries)
	for _, want := range []string{"ui.screen", "ui.dialog", "ui.dialog.close", "nav.open", "ui.view"} {
		if !hasEvent(entries, want) {
			t.Errorf("missing event %q in %v", want, evs)
		}
	}
	if countCat(entries, activity.CatNavigation) == 0 {
		t.Error("no navigation entries")
	}
	if countCat(entries, activity.CatUI) == 0 {
		t.Error("no UI entries")
	}
	// Every entry is timestamped and carries its run ID, so the viewer and the
	// operation history can be read together.
	for _, e := range entries {
		if e.Time.IsZero() || e.V != 1 || e.Run != "run-test" {
			t.Fatalf("entry is missing its header fields: %+v", e)
		}
	}
	_ = env
}

// TestActivityLogNeverRecordsTypedText is the privacy rule: what is typed into
// a text field — a file name, a search query — must not reach the file, while
// the fact that the dialog opened is recorded.
func TestActivityLogNeverRecordsTypedText(t *testing.T) {
	h, _, path := activityApp(t, activity.ModeVerbose)
	h.send(tea.WindowSizeMsg{Width: 110, Height: 30})

	const typedName = "diary-taxes-2026"
	const query = "confidential-salary"

	h.press("/") // search overlay
	h.typeText(query, false)
	h.press("esc")
	h.press("f7") // new folder
	h.typeText(typedName, false)
	h.press("esc")
	h.settle()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	body := string(raw)
	if strings.Contains(body, typedName) {
		t.Fatalf("the log recorded a typed file name:\n%s", body)
	}
	if strings.Contains(body, query) {
		t.Fatalf("the log recorded a search query:\n%s", body)
	}
	// The dialogs themselves are recorded (that is the useful part).
	entries := activityEntries(t, path)
	if !hasEvent(entries, "ui.dialog") {
		t.Fatalf("the dialogs were not logged: %v", eventNames(entries))
	}
}

// TestActivityLogRedactsCommands drives the real trace hook with an argument
// list that carries a credential, exactly as a future flag might.
func TestActivityLogRedactsCommands(t *testing.T) {
	h, _, path := activityApp(t, activity.ModeActions)
	h.send(tea.WindowSizeMsg{Width: 110, Height: 30})

	argv := []string{"/usr/bin/rclone", "lsf", "--password", "hunter2", "--token=abc123secret", "gdrive:/"}
	h.a.traceActivity(proc.TraceEvent{Path: argv[0], Args: argv, Started: time.Now(),
		Elapsed: 1200 * time.Millisecond, Status: proc.Status{Code: 3}, Done: true})

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	for _, secret := range []string{"hunter2", "abc123secret"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("command argument %q leaked into the log:\n%s", secret, raw)
		}
	}
	entries := activityEntries(t, path)
	var cmd *activity.Entry
	for i := range entries {
		if entries[i].Ev == "command.done" {
			cmd = &entries[i]
		}
	}
	if cmd == nil {
		t.Fatalf("no command entry: %v", eventNames(entries))
	}
	if cmd.Code != 3 || cmd.Status != "exit 3" || cmd.DurMS != 1200 {
		t.Fatalf("exit status not recorded: %+v", cmd)
	}
	if cmd.LevelOf() != activity.LevelError {
		t.Fatalf("a failing command should be an error entry: %+v", cmd)
	}
	if !strings.Contains(cmd.Msg, "rclone lsf") {
		t.Fatalf("the command is not identifiable: %q", cmd.Msg)
	}
}

// TestActivityDurationsAndSignals covers the other ways a command can end.
func TestActivityDurationsAndSignals(t *testing.T) {
	h, _, path := activityApp(t, activity.ModeActions)
	h.send(tea.WindowSizeMsg{Width: 110, Height: 30})
	h.a.traceActivity(proc.TraceEvent{Args: []string{"/usr/bin/7z", "a", "out.7z", "src"},
		Elapsed: 250 * time.Millisecond, Status: proc.Status{Signal: 9}, Done: true})
	h.a.traceActivity(proc.TraceEvent{Args: []string{"/usr/bin/xdg-open", "a.pdf"},
		Elapsed: 10 * time.Millisecond, Status: proc.Status{}, Done: true})
	entries := activityEntries(t, path)
	var signaled, ok *activity.Entry
	for i := range entries {
		switch entries[i].Ev {
		case "command.done":
			if strings.Contains(entries[i].Msg, "7z") {
				signaled = &entries[i]
			} else {
				ok = &entries[i]
			}
		}
	}
	if signaled == nil || !strings.Contains(signaled.Status, "signal 9") || signaled.LevelOf() != activity.LevelError {
		t.Fatalf("a killed command: %+v", signaled)
	}
	if ok == nil || ok.Status != "exit 0" || ok.LevelOf() != activity.LevelInfo {
		t.Fatalf("a successful command: %+v", ok)
	}
}

// TestActivityModeWarningsOnly: the "warnings" mode keeps problems and drops
// the ordinary actions, but the viewer still works.
func TestActivityModeWarningsOnly(t *testing.T) {
	h, _, path := activityApp(t, activity.ModeWarnings)
	h.send(tea.WindowSizeMsg{Width: 110, Height: 30})
	h.press("f9")
	h.press("esc")
	h.press("enter") // navigating is not a warning: it must not be stored
	h.settle()
	entries := activityEntries(t, path)
	for _, e := range entries {
		if e.LevelOf() < activity.LevelWarn {
			t.Fatalf("warnings mode stored an %s entry: %+v", e.Level, e)
		}
	}
	h.a.logf(activity.LevelInfo, activity.CatUI, "ui.note", "informational")
	h.a.logf(activity.LevelWarn, activity.CatUI, "ui.warn", "something to look at")
	entries = activityEntries(t, path)
	if hasEvent(entries, "ui.note") {
		t.Fatal("info entry stored in warnings mode")
	}
	if !hasEvent(entries, "ui.warn") {
		t.Fatalf("warning entry not stored: %v", eventNames(entries))
	}
}

// TestActivityModeOff: nothing is written, and the viewer says so instead of
// failing.
func TestActivityModeOff(t *testing.T) {
	h, _, path := activityApp(t, activity.ModeOff)
	h.send(tea.WindowSizeMsg{Width: 110, Height: 30})
	h.press("f9")
	h.press("esc")
	h.press("L")
	v := h.view()
	if !strings.Contains(v, "Activity log") {
		t.Fatalf("the viewer did not open:\n%s", v)
	}
	if !strings.Contains(v, "No entries yet") {
		t.Fatalf("the empty viewer should say so:\n%s", v)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("off mode created a file: %v", err)
	}
	h.press("esc")
	if h.a.actView != nil {
		t.Fatal("Esc did not close the viewer")
	}
}

// seedLog writes entries through the app's own store so the viewer has
// something to filter, including a command, a navigation and a cloud failure.
func seedLog(t *testing.T, h *harness) {
	t.Helper()
	h.a.logf(activity.LevelInfo, activity.CatNavigation, "nav.open", "opened /home/u/docs")
	h.a.logf(activity.LevelInfo, activity.CatCommand, "command.done", "rclone lsf gdrive:")
	h.a.logf(activity.LevelError, activity.CatCommand, "command.done", "rclone copy failed: exit 3")
	h.a.logf(activity.LevelError, activity.CatCloud, "cloud.done", "uploaded \"backup.tar\": quota exceeded")
	h.a.logf(activity.LevelWarn, activity.CatRecovery, "recovery.settings", "settings were reset")
	h.a.logf(activity.LevelDebug, activity.CatUI, "key.action", "shortcut f7")
	h.a.logf(activity.LevelInfo, activity.CatSettings, "settings.change", "Theme = dark")
}

// TestActivityViewerOpensAndFilters is the UI behaviour test: L opens a full
// screen, the category cycle narrows the same log, the level cycle stacks on
// top, and Esc returns to the screen behind.
func TestActivityViewerOpensAndFilters(t *testing.T) {
	h, _, _ := activityApp(t, activity.ModeActions)
	seedLog(t, h)
	before := h.view()
	h.send(tea.WindowSizeMsg{Width: 110, Height: 30})
	before = h.view()

	h.press("L")
	v := h.view()
	if !strings.Contains(v, "Activity log") || !strings.Contains(v, "All Activity") {
		t.Fatalf("the viewer did not open on All Activity:\n%s", v)
	}
	if !strings.Contains(v, "rclone lsf") || !strings.Contains(v, "quota exceeded") {
		t.Fatalf("entries are missing:\n%s", v)
	}
	if strings.Contains(v, "shortcut f7") {
		t.Fatalf("debug detail must stay out of the actions view:\n%s", v)
	}

	// c cycles: Commands first, and only commands remain.
	h.press("c")
	v = h.view()
	if !strings.Contains(v, "Commands") {
		t.Fatalf("category filter did not apply:\n%s", v)
	}
	if strings.Contains(v, "quota exceeded") || strings.Contains(v, "settings were reset") {
		t.Fatalf("entries from other categories were listed:\n%s", v)
	}
	if !strings.Contains(v, "rclone lsf") {
		t.Fatalf("the command entries were lost:\n%s", v)
	}

	// l narrows further: errors only.
	h.press("l") // info and up
	h.press("l") // warnings and up
	h.press("l") // errors only
	v = h.view()
	if !strings.Contains(v, "errors only") {
		t.Fatalf("level filter did not apply:\n%s", v)
	}
	if !strings.Contains(v, "rclone copy failed") || strings.Contains(v, "rclone lsf gdrive:") {
		t.Fatalf("errors-only listing is wrong:\n%s", v)
	}

	// x clears everything.
	h.press("x")
	v = h.view()
	if !strings.Contains(v, "All Activity") || !strings.Contains(v, "quota exceeded") {
		t.Fatalf("clearing the filter did not restore the full log:\n%s", v)
	}
	checkScreen(t, v, 110, 30)

	h.press("esc")
	if h.a.actView != nil {
		t.Fatal("Esc did not close the viewer")
	}
	if h.view() != before {
		t.Fatalf("the screen behind the viewer did not come back")
	}
}

// TestActivityViewerWarningsFilter is the "Warnings & Errors" filter: it cuts
// across categories, which is why it is a filter and not a log of its own.
func TestActivityViewerWarningsFilter(t *testing.T) {
	h, _, _ := activityApp(t, activity.ModeActions)
	seedLog(t, h)
	h.send(tea.WindowSizeMsg{Width: 110, Height: 30})
	h.press("L")
	for i := 0; i < len(activity.CatFilters); i++ {
		if strings.Contains(h.view(), "Warnings & Errors") {
			break
		}
		h.press("c")
	}
	v := h.view()
	if !strings.Contains(v, "Warnings & Errors") {
		t.Fatalf("the warnings filter is not reachable by cycling:\n%s", v)
	}
	for _, want := range []string{"rclone copy failed", "quota exceeded", "settings were reset"} {
		if !strings.Contains(v, want) {
			t.Errorf("warnings filter lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "opened /home/u/docs") {
		t.Errorf("warnings filter lists ordinary actions:\n%s", v)
	}
}

// TestActivityViewerQuery: the typed query narrows the list and is not itself
// written to the log.
func TestActivityViewerQuery(t *testing.T) {
	h, _, path := activityApp(t, activity.ModeActions)
	seedLog(t, h)
	h.send(tea.WindowSizeMsg{Width: 110, Height: 30})
	h.press("L")
	h.press("/")
	h.typeText("quota", false)
	v := h.view()
	if !strings.Contains(v, "quota") {
		t.Fatalf("the query is not shown while typing:\n%s", v)
	}
	h.press("enter")
	v = h.view()
	if !strings.Contains(v, "quota exceeded") {
		t.Fatalf("query result missing:\n%s", v)
	}
	if strings.Contains(v, "rclone lsf") {
		t.Fatalf("query did not narrow the list:\n%s", v)
	}
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "quota\"") || strings.Contains(string(raw), `"/quota`) {
		t.Fatalf("the query itself was logged:\n%s", raw)
	}
}

// TestActivityViewerDetails: ⏎ shows the entry's fields, and the details fit.
func TestActivityViewerDetails(t *testing.T) {
	h, _, _ := activityApp(t, activity.ModeActions)
	seedLog(t, h)
	h.send(tea.WindowSizeMsg{Width: 110, Height: 30})
	h.press("L")
	h.press("c") // Commands
	h.press("l")
	h.press("l")
	h.press("l") // errors only, so the selected entry is the failed command
	h.press("enter")
	v := h.view()
	for _, want := range []string{"command.done", "ERROR", "rclone"} {
		if !strings.Contains(v, want) {
			t.Errorf("details lack %q:\n%s", want, v)
		}
	}
	checkScreen(t, v, 110, 30)
	h.press("enter")
	if strings.Contains(h.view(), "command.done") {
		t.Fatal("a second ⏎ should hide the details again")
	}
}

// TestActivityViewerNarrowAndShort: the same screen has to work on a phone and
// on a short terminal.
func TestActivityViewerNarrowAndShort(t *testing.T) {
	h, _, _ := activityApp(t, activity.ModeActions)
	seedLog(t, h)

	h.send(tea.WindowSizeMsg{Width: 40, Height: 20})
	h.settle()
	h.press("L")
	v := h.view()
	if !strings.Contains(v, "Activity log") {
		t.Fatalf("narrow viewer:\n%s", v)
	}
	if !strings.Contains(v, "quota") {
		t.Fatalf("narrow viewer lost the message:\n%s", v)
	}
	checkScreen(t, v, 40, 20)

	h.send(tea.WindowSizeMsg{Width: 100, Height: 8})
	h.settle()
	v = h.view()
	if !strings.Contains(v, "Activity log") {
		t.Fatalf("short viewer:\n%s", v)
	}
	checkScreen(t, v, 100, 8)
	h.press("esc")
}

// TestActivityViewerHintsAndMouse: the hint bar describes the viewer, and the
// wheel moves the selection.
func TestActivityViewerHintsAndMouse(t *testing.T) {
	h, _, _ := activityApp(t, activity.ModeActions)
	for i := 0; i < 20; i++ {
		h.a.logf(activity.LevelInfo, activity.CatUI, "ui.note", "entry %d", i)
	}
	h.send(tea.WindowSizeMsg{Width: 110, Height: 24})
	h.press("L")
	footer := h.view()[strings.LastIndex(h.view(), "\n")+1:]
	for _, want := range []string{"category", "level", "filter", "close"} {
		if !strings.Contains(footer, want) {
			t.Errorf("viewer hints lack %q: %q", want, footer)
		}
	}
	h.press("down", "down")
	if h.a.actView.sel != 2 {
		t.Fatalf("sel = %d, want 2", h.a.actView.sel)
	}
	h.wheel(50, 8, -3)
	if got := h.a.actView.sel; got >= 2 {
		t.Fatalf("wheel up did not move the selection: sel = %d", got)
	}
	// Esc closes, and does not dismiss a notification while the viewer is up.
	h.press("esc")
	if h.a.actView != nil {
		t.Fatal("Esc did not close the viewer")
	}
}

// TestActivityViewerRunJumps: [ and ] jump between application runs.
func TestActivityViewerRunJumps(t *testing.T) {
	h, _, _ := activityApp(t, activity.ModeActions)
	h.a.logf(activity.LevelInfo, activity.CatUI, "ui.note", "current run")
	h.send(tea.WindowSizeMsg{Width: 110, Height: 24})
	// A second, older run written directly to the file.
	path := h.a.activityPath
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString(`{"v":1,"ts":"2026-10-26T08:00:00Z","run":"run-old","lvl":"info","cat":"ui","ev":"ui.note","msg":"previous run"}` + "\n")
	f.Close()
	h.press("L")
	h.a.actView.reload(h.a) // pick up the appended line
	if len(h.a.actView.rows) < 2 {
		t.Fatalf("rows = %d, want at least 2", len(h.a.actView.rows))
	}
	h.press("]")
	if got := h.a.actView.entries[h.a.actView.rows[h.a.actView.sel]].Run; got != "run-old" {
		t.Fatalf("] landed on run %q, want run-old", got)
	}
	h.press("[")
	if got := h.a.actView.entries[h.a.actView.rows[h.a.actView.sel]].Run; got == "run-old" {
		t.Fatal("[ did not go back to the newer run")
	}
}

// TestActivitySettingsRows: the Settings screen edits the log's mode and
// retention, and the values are persisted.
func TestActivitySettingsRows(t *testing.T) {
	h, _, _ := activityApp(t, activity.ModeActions)
	h.send(tea.WindowSizeMsg{Width: 110, Height: 30})
	h.press("f9")
	a := h.a
	rows := a.settingsRows()
	idx := map[settingsRowID]int{}
	for i, r := range rows {
		idx[r.id] = i
	}
	for _, id := range []settingsRowID{rowActivity, rowActivitySize, rowActivityDays} {
		if _, ok := idx[id]; !ok {
			t.Fatalf("settings row %d missing", id)
		}
	}
	a.settingsScr.sel = idx[rowActivity]
	h.press("l")
	if a.actMode != activity.ModeVerbose {
		t.Fatalf("activity mode = %v, want verbose", a.actMode)
	}
	a.settingsScr.sel = idx[rowActivitySize]
	before := a.actMaxKiB
	h.press("l")
	if a.actMaxKiB == before {
		t.Fatal("rotation size did not change")
	}
	a.settingsScr.sel = idx[rowActivityDays]
	beforeDays := a.actKeepDays
	h.press("h")
	if a.actKeepDays == beforeDays {
		t.Fatal("retention days did not change")
	}
	// The change is logged (settings has its own category) and saved.
	entries := activityEntries(t, a.activityPath)
	if !hasEvent(entries, "settings.change") {
		t.Fatalf("settings changes are not logged: %v", eventNames(entries))
	}
	prefs, err := settings.Load(a.settingsPath)
	if err != nil {
		t.Fatalf("settings not saved: %v", err)
	}
	if prefs.Activity.Mode != "verbose" {
		t.Fatalf("saved activity mode = %q", prefs.Activity.Mode)
	}
}

// TestActivityLogFilePermissions: the log is private.
func TestActivityLogFilePermissions(t *testing.T) {
	h, _, path := activityApp(t, activity.ModeActions)
	h.a.logf(activity.LevelInfo, activity.CatUI, "ui.note", "hello")
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}
}

// TestActivityViewerDoesNotStealKeys: while a dialog is open, L types into it
// instead of opening the log.
func TestActivityViewerDoesNotStealKeys(t *testing.T) {
	h, _, _ := activityApp(t, activity.ModeActions)
	h.send(tea.WindowSizeMsg{Width: 110, Height: 30})
	h.press("f7") // new folder: a text field
	h.press("L")
	if h.a.actView != nil {
		t.Fatal("L opened the viewer from a text field")
	}
	h.press("esc")
}
