package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/compress"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/settings"
)

// newSettingsApp boots an App whose preferences live at path ("" disables
// persistence), with HOME pointed at a fresh directory and local defaults.
// The panels are not loaded until the test calls Init.
func newSettingsApp(t *testing.T, path string) *harness {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	if err := os.MkdirAll(filepath.Join(root, "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := New(Options{
		Version:      "test",
		SettingsPath: path,
		Getenv:       func(string) string { return "" },
		GOOS:         "linux",
		LookPath:     exec.LookPath,
		Launch:       func([]string) error { return nil },
	})
	h := &harness{t: t, a: a, msgs: make(chan tea.Msg, 256)}
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	return h
}

// moveRow presses "down" until the settings selection reaches row.
func moveRow(h *harness, row settingsRowID) {
	for h.a.settingsScr.sel < int(row) {
		h.press("down")
	}
	for h.a.settingsScr.sel > int(row) {
		h.press("up")
	}
}

// TestSettingsAccessOpensOneScreen checks the three entry points (F9,
// Ctrl+S, the TwinPane menu's Settings entry) all open the same screen, and
// that Ctrl+S belongs to the settings binding alone.
func TestSettingsAccessOpensOneScreen(t *testing.T) {
	h := newSettingsApp(t, "")

	k := DefaultKeys()
	if !k.Settings.Matches(key("ctrl+s")) {
		t.Fatal("settings binding must answer to Ctrl+S")
	}
	if k.Settings.Matches(key("ctrl+c")) || k.Copy.Matches(key("ctrl+s")) {
		t.Fatal("Ctrl+C/Ctrl+S cross-wired between copy and settings")
	}
	for _, b := range []Binding{
		k.Copy, k.Move, k.Rename, k.Delete, k.Edit, k.Help, k.Tasks,
		k.Search, k.Switch, k.View, k.Quit, k.Cancel, k.Refresh,
	} {
		if b.Matches(key("ctrl+s")) {
			t.Errorf("binding %q claims Ctrl+S as well", b.Desc)
		}
	}

	checkOpen := func(method string) {
		t.Helper()
		if !h.a.showSettings {
			t.Fatalf("%s did not open the settings screen", method)
		}
		if h.a.showHelp || h.a.tasks.showing || h.a.dialog != nil {
			t.Fatalf("%s opened extra UI alongside settings", method)
		}
		v := h.view()
		if !strings.Contains(v, "Show hidden files") || !strings.Contains(v, "Settings") {
			t.Fatalf("%s: settings screen content missing:\n%s", method, v)
		}
	}

	h.press("f9")
	checkOpen("F9")
	h.press("esc")
	if h.a.showSettings {
		t.Fatal("Esc must close settings")
	}

	h.press("ctrl+s")
	checkOpen("Ctrl+S")
	h.press("f9") // F9 toggles it shut as well
	if h.a.showSettings {
		t.Fatal("F9 must close settings")
	}

	h.press("M") // badge menu → Settings…
	if !h.dialogKind(dlgMenu) {
		t.Fatalf("m opens the menu, got %v", h.a.dialog)
	}
	found := -1
	for i, li := range h.a.dialog.list {
		if strings.Contains(li.label, "Settings") {
			found = i
		}
	}
	if found < 0 {
		t.Fatalf("menu lacks a Settings entry: %+v", h.a.dialog.list)
	}
	h.a.dialog.listCursor = found
	h.press("enter")
	checkOpen("menu Settings entry")
	h.press("esc")
}

// TestSettingsChangesApplyAndPersist walks the screen with the keyboard:
// every toggle flips live, the panel rows hit both panels immediately, and
// settings.json carries the new values.
func TestSettingsChangesApplyAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	h := newSettingsApp(t, path)
	h.a.settingsPath = path
	h.press("f9")

	// Show hidden files (row 4): flips via the same path as h.
	moveRow(h, rowShowHidden)
	h.press("space")
	if !h.a.showHidden {
		t.Fatal("show-hidden row must toggle")
	}
	if !h.a.ActivePanel().Options().ShowHidden {
		t.Fatal("show-hidden must reach the panels live")
	}

	// Focus preview (row 3).
	moveRow(h, rowFocusPreview)
	h.press("space")
	if h.a.focusPreview {
		t.Fatal("focus preview must toggle")
	}

	// Sorting: rows 5–7 apply to both panels immediately.
	moveRow(h, rowSortKey)
	h.press("space")
	if h.a.defSort != fsys.SortSize || h.a.ActivePanel().Options().Sort != fsys.SortSize {
		t.Fatalf("sort key %v (defSort %v) not applied live",
			h.a.ActivePanel().Options().Sort, h.a.defSort)
	}
	moveRow(h, rowSortDesc)
	h.press("space")
	if !h.a.defDesc || !h.a.ActivePanel().Options().Desc {
		t.Fatal("sort direction not applied live")
	}
	moveRow(h, rowDirsFirst)
	h.press("space")
	if !h.a.ActivePanel().Options().MixedOrder { // dirs-first off ⇔ mixed
		t.Fatal("directories-first not applied live")
	}

	// Notifications: corner and duration.
	moveRow(h, rowCorner)
	h.press("space")
	if h.a.noticeCorner != "bl" {
		t.Fatalf("corner %q, want bl", h.a.noticeCorner)
	}
	moveRow(h, rowDuration)
	h.press("space")
	if h.a.noticeDur != 5*time.Second {
		t.Fatalf("duration %v, want 5s", h.a.noticeDur)
	}

	// Theme preset.
	moveRow(h, rowTheme)
	h.press("space")
	if h.a.themeName == "default" {
		t.Fatal("theme row must cycle away from default")
	}

	// Editor tips, viewer defaults.
	moveRow(h, rowTips)
	h.press("space")
	if h.a.tipsPref {
		t.Fatal("tips must toggle off")
	}
	moveRow(h, rowWrap)
	h.press("space")
	if h.a.viewerWrap {
		t.Fatal("viewer wrap must toggle off")
	}
	moveRow(h, rowNumbers)
	h.press("space")
	if h.a.viewerNums {
		t.Fatal("viewer numbers must toggle off")
	}

	// Compression defaults: format row leaves auto, level row walks the list.
	moveRow(h, rowFormat)
	h.press("space")
	if h.a.lastFormat != compress.ZIP {
		t.Fatalf("format %q, want zip", h.a.lastFormat)
	}
	moveRow(h, rowLevel)
	h.press("space")
	if h.a.formatLevels[compress.ZIP] == 0 {
		t.Fatal("level row must change the ZIP level")
	}

	// Persistence.
	prefs, err := settings.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	switch {
	case !prefs.ShowHidden:
		t.Error("showHidden not saved")
	case prefs.FocusPreview:
		t.Error("focusPreview not saved")
	case prefs.Sort.Key != "size" || !prefs.Sort.Desc || prefs.Sort.DirsFirst:
		t.Errorf("sort not saved: %+v", prefs.Sort)
	case prefs.Notice.Corner != "bl" || prefs.Notice.Duration != 5:
		t.Errorf("notice not saved: %+v", prefs.Notice)
	case prefs.Theme == "default":
		t.Errorf("theme not saved: %q", prefs.Theme)
	case prefs.Editor.Tips:
		t.Error("tips not saved")
	case prefs.Viewer.Wrap || prefs.Viewer.Numbers:
		t.Errorf("viewer not saved: %+v", prefs.Viewer)
	case prefs.Compress.Format != "zip" || prefs.Compress.Levels["zip"] == 0:
		t.Errorf("compress not saved: %+v", prefs.Compress)
	case !prefs.Mouse: // untouched rows keep their defaults
		t.Error("mouse default lost")
	}

	// The preferences must boot into a fresh App too.
	b := newSettingsApp(t, path).a
	if !b.showHidden || b.focusPreview || b.tipsPref || b.viewerWrap || b.viewerNums ||
		b.defSort != fsys.SortSize || b.noticeCorner != "bl" || b.themeName != prefs.Theme {
		t.Fatalf("reloaded app: hidden=%v focus=%v tips=%v wrap=%v nums=%v sort=%v corner=%v theme=%q",
			b.showHidden, b.focusPreview, b.tipsPref, b.viewerWrap, b.viewerNums,
			b.defSort, b.noticeCorner, b.themeName)
	}
}

// TestSettingsStartupPrefsApplyOnBoot: startup folders, starting panel and
// defaults land on the panels at Init; Options.StartDir still overrides.
func TestSettingsStartupPrefsApplyOnBoot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	left := filepath.Join(t.TempDir(), "left")
	right := filepath.Join(t.TempDir(), "right")
	override := filepath.Join(t.TempDir(), "override")
	for _, d := range []string{left, right, override} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	p := settings.Default()
	p.Startup.Left, p.Startup.Right, p.Startup.Pane = left, right, "right"
	p.Sort.Key, p.Sort.Desc, p.Sort.DirsFirst = "size", true, false
	if err := settings.Save(path, p); err != nil {
		t.Fatal(err)
	}

	h := newSettingsApp(t, path)
	h.exec(h.a.Init())
	h.until("left panel", func() bool { return h.a.Panel(0).Path() == left })
	h.until("right panel", func() bool { return h.a.Panel(1).Path() == right })
	if h.a.ActivePanel() != h.a.Panel(1) {
		t.Fatal("startup pane 'right' must focus the right panel")
	}
	if got := h.a.ActivePanel().Options(); got.Sort != fsys.SortSize || !got.Desc || !got.MixedOrder {
		t.Fatalf("startup sort options %+v", got)
	}

	// Explicit start directory wins over the saved folders.
	a := New(Options{Version: "test", SettingsPath: path, StartDir: override,
		Getenv: func(string) string { return "" }, GOOS: "linux",
		LookPath: exec.LookPath, Launch: func([]string) error { return nil }})
	if a.startLeft != override || a.startRight != override {
		t.Fatalf("StartDir override ignored: left=%q right=%q", a.startLeft, a.startRight)
	}
}

// TestSettingsEditorCommandRow: the text row edits through the shared input
// dialog and saves the preferred editor.
func TestSettingsEditorCommandRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	h := newSettingsApp(t, path)
	h.a.settingsPath = path
	h.press("f9")

	moveRow(h, rowEditor)
	h.press("enter")
	h.waitDialog(dlgSettings)
	if got := h.a.dialog.input.Value(); got != "" {
		t.Fatalf("prefill %q, want empty (automatic)", got)
	}
	h.typeText("nano --multi", false)
	h.press("enter")
	if h.a.dialog != nil {
		t.Fatal("dialog must close on Enter")
	}
	if h.a.editorCmd != "nano --multi" {
		t.Fatalf("editorCmd %q", h.a.editorCmd)
	}
	prefs, err := settings.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if prefs.Editor.Command != "nano --multi" {
		t.Fatalf("saved editor %q", prefs.Editor.Command)
	}

	// The field reopens prefilled; clearing it restores the automatic chain.
	moveRow(h, rowEditor)
	h.press("enter")
	h.waitDialog(dlgSettings)
	if got := h.a.dialog.input.Value(); got != "nano --multi" {
		t.Fatalf("prefill %q, want the saved command", got)
	}
	h.press("ctrl+u")
	h.press("enter")
	if h.a.editorCmd != "" {
		t.Fatalf("cleared editorCmd %q", h.a.editorCmd)
	}
	if prefs, err = settings.Load(path); err != nil || prefs.Editor.Command != "" {
		t.Fatalf("cleared command not saved: prefs=%+v err=%v", prefs.Editor, err)
	}
}

// TestSettingsMouseTaps: a tap selects a row, the second tap on the selected
// row changes it, taps on group titles do nothing.
func TestSettingsMouseTaps(t *testing.T) {
	h := newSettingsApp(t, "")
	h.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	h.press("f9")

	// Content line i sits at screen y = i+2 (title row, box border).
	// Group title (content 0) is not selectable.
	h.tap(5, 2)
	if h.a.settingsScr.sel != 0 {
		t.Fatalf("title tap moved selection to %d", h.a.settingsScr.sel)
	}
	// Focus-preview row: content 4 → y=6.
	h.tap(5, 6)
	if h.a.settingsScr.sel != int(rowFocusPreview) {
		t.Fatalf("first tap must select, sel=%d", h.a.settingsScr.sel)
	}
	focus := h.a.focusPreview
	h.tap(5, 6)
	if h.a.focusPreview == focus {
		t.Fatal("second tap on the selected row must change it")
	}
	// Startup-left row (content 1 → y=3): first tap selects, second opens.
	h.tap(5, 3)
	if h.a.settingsScr.sel != int(rowStartLeft) {
		t.Fatalf("tap must select startup-left, sel=%d", h.a.settingsScr.sel)
	}
	h.tap(5, 3)
	if !h.dialogKind(dlgSettings) {
		t.Fatal("tap-tap on a text row opens its field")
	}
	h.press("esc")
	// Wheel moves the selection.
	sel := h.a.settingsScr.sel
	h.wheel(5, 6, 1)
	if h.a.settingsScr.sel == sel {
		t.Fatal("wheel must move the selection")
	}
}

// TestSettingsResetRecoversCorruptFile: a damaged file shows the banner on
// the screen; Enter on the confirmation cancels, "y" rewrites the defaults.
func TestSettingsResetRecoversCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := newSettingsApp(t, path)
	if h.a.settingsErr == nil {
		t.Fatal("corrupt file must surface an error")
	}
	h.press("f9")
	v := h.view()
	if !strings.Contains(v, "press r to reset") {
		t.Fatalf("corruption banner missing: show=%v err=%v v=%q", h.a.showSettings, h.a.settingsErr, v)
	}

	// Enter (focused Cancel) must not destroy anything.
	h.press("r")
	if !h.dialogKind(dlgSettings) {
		t.Fatal("r opens the reset confirmation")
	}
	h.press("enter")
	if h.a.dialog != nil || h.a.settingsErr == nil {
		t.Fatal("cancelling must leave the error in place")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "{not json" {
		t.Fatalf("cancel rewrote the file: %q", b)
	}

	// The hotkey y performs the reset: file valid, defaults, no banner.
	h.press("r")
	h.press("y")
	if h.a.dialog != nil || h.a.settingsErr != nil {
		t.Fatalf("reset must clear dialog and error (err=%v)", h.a.settingsErr)
	}
	prefs, err := settings.Load(path)
	if err != nil {
		t.Fatalf("file must be valid after reset: %v", err)
	}
	if prefs.ShowHidden || prefs.Theme != "default" || !prefs.Mouse {
		t.Fatalf("defaults not written: %+v", prefs)
	}
	if !strings.Contains(h.status(), "reset") {
		t.Fatalf("status %q", h.status())
	}
	if strings.Contains(h.view(), "press r to reset") {
		t.Fatal("banner must disappear after reset")
	}
}

// TestSettingsSessionSortNotPersisted: s in the browse UI only changes the
// session; the screen keeps and saves its own default.
func TestSettingsSessionSortNotPersisted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	h := newSettingsApp(t, path)
	h.a.settingsPath = path
	h.press("f9")
	moveRow(h, rowSortKey)
	h.press("space")
	h.press("space") // name → size → time
	h.press("esc")

	h.press("s") // session cycle on the active panel
	h.press("s")
	prefs, err := settings.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if prefs.Sort.Key != "time" {
		t.Fatalf("session sort leaked into settings: %q", prefs.Sort.Key)
	}
	h.press("f9")
	moveRow(h, rowSortKey)
	if v := h.a.settingsRows()[rowSortKey].value; v != "Time" {
		t.Fatalf("screen sort row %q, want Time", v)
	}
}

// TestSettingsNarrowScreen: the screen renders and navigates in a small
// terminal.
func TestSettingsNarrowScreen(t *testing.T) {
	h := newSettingsApp(t, "")
	for _, size := range [][2]int{{40, 12}, {60, 16}, {30, 10}} {
		h.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		h.press("f9")
		checkScreen(t, h.view(), size[0], size[1])
		for i := 0; i < 6; i++ {
			h.press("down")
		}
		checkScreen(t, h.view(), size[0], size[1])
		h.press("space")
		checkScreen(t, h.view(), size[0], size[1])
		h.press("esc")
		if h.a.showSettings {
			t.Fatalf("esc at %dx%d", size[0], size[1])
		}
	}
}

// TestSettingsHintsAndHelp: the screen has its own hint bar, the browse bar
// advertises the shortcut, and help lists the binding.
func TestSettingsHintsAndHelp(t *testing.T) {
	h := newSettingsApp(t, "")
	// The footer follows the cursor on a loaded panel: give the home folder
	// something to point at and boot the panels.
	if err := os.MkdirAll(filepath.Join(os.Getenv("HOME"), "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.exec(h.a.Init())
	h.until("panels", func() bool { return h.a.Panel(0).Path() != "" && h.a.Panel(1).Path() != "" })
	h.send(tea.WindowSizeMsg{Width: 130, Height: 30})
	h.focus("sub")
	// The chip rides at the end of the bar (like the phase-6 menu chips),
	// so it renders once the bar has room; the list must always carry it.
	found := false
	for _, it := range h.a.browseHints() {
		if it.key == "F9" && it.desc == "settings" && it.send == "f9" {
			found = true
		}
	}
	if !found {
		t.Fatal("browse hints lack the settings chip")
	}
	h.send(tea.WindowSizeMsg{Width: 360, Height: 30})
	if f := h.footer(); !strings.Contains(f, "settings") {
		t.Fatalf("wide browse footer lacks the settings chip: %q", f)
	}
	h.send(tea.WindowSizeMsg{Width: 130, Height: 30})
	h.press("f9")
	f := h.footer()
	for _, want := range []string{"move", "change", "close"} {
		if !strings.Contains(f, want) {
			t.Errorf("settings hints lack %q: %q", want, f)
		}
	}
	// ? over settings opens the help on top; Esc returns to the screen.
	h.press("?")
	if !h.a.showHelp || !h.a.showSettings {
		t.Fatalf("help+settings states: help=%v settings=%v", h.a.showHelp, h.a.showSettings)
	}
	if !strings.Contains(h.view(), "F9 / ^S") {
		t.Fatal("help must list the settings binding")
	}
	h.press("esc")
	if h.a.showHelp || !h.a.showSettings {
		t.Fatal("Esc must close help but keep settings")
	}
}

// TestSettingsFlagsStillOverride: tips keep the flag kill-switch; the mouse
// preference round-trips through New (main.go applies it at startup).
func TestSettingsFlagsStillOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	p := settings.Default()
	p.Editor.Tips, p.Mouse = false, false
	if err := settings.Save(path, p); err != nil {
		t.Fatal(err)
	}
	a := New(Options{Version: "test", SettingsPath: path, EditorTips: true,
		Getenv: func(string) string { return "" }, GOOS: "linux",
		LookPath: exec.LookPath, Launch: func([]string) error { return nil }})
	if a.tipsPref {
		t.Fatal("saved tips=false must win")
	}
	if a.mousePref {
		t.Fatal("saved mouse=false must win")
	}
	// The flag only ever disables tips: saved true + flag false ⇒ off.
	p.Editor.Tips = true
	if err := settings.Save(path, p); err != nil {
		t.Fatal(err)
	}
	b := New(Options{Version: "test", SettingsPath: path, EditorTips: false,
		Getenv: func(string) string { return "" }, GOOS: "linux",
		LookPath: exec.LookPath, Launch: func([]string) error { return nil }})
	if !b.tipsPref || b.opts.EditorTips {
		t.Fatalf("pref=%v flag=%v: effective tips must be off", b.tipsPref, b.opts.EditorTips)
	}
}
