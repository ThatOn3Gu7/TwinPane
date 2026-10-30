package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/keybinding"
)

// useBindings swaps the session's binding set, like a keybinding.json other
// than the defaults.
func useBindings(t *testing.T, h *harness, custom map[string][]string) {
	t.Helper()
	h.a.opts.Bindings = keybinding.New(custom)
	h.a.loadBindings()
}

// newBindingsApp boots an App whose key bindings come from path.
func newBindingsApp(t *testing.T, path string) *harness {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	if err := os.MkdirAll(filepath.Join(root, "home"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := New(Options{
		Version:        "test",
		Getenv:         func(string) string { return "" },
		GOOS:           "linux",
		SettingsPath:   filepath.Join(root, "settings.json"),
		HistoryPath:    filepath.Join(root, "history.json"),
		KeybindingPath: path,
	})
	h := &harness{t: t, a: a, msgs: make(chan tea.Msg, 64)}
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	return h
}

// TestBrowseRebindingDrivesKeysAndHints: a changed browse key is the one the
// panel listens for, the old key does nothing, and every label follows it.
func TestBrowseRebindingDrivesKeysAndHints(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	useBindings(t, h, map[string][]string{"browse.copy": {"y"}})
	h.press("down") // leave "..": a file has copy/move/delete hints

	footer := lastLine(h.view())
	if !strings.Contains(footer, "y") || !strings.Contains(footer, "copy") {
		t.Fatalf("footer does not show the rebound copy key: %q", footer)
	}

	h.press("c")
	if h.a.dialog != nil {
		t.Fatal("the old copy key still opens the copy dialog")
	}
	h.press("y")
	if h.a.dialog == nil {
		t.Fatal("the rebound key does not open the copy dialog")
	}
}

// TestHelpShowsReboundKeys: the help screen is generated from the active set.
func TestHelpShowsReboundKeys(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	useBindings(t, h, map[string][]string{"browse.copy": {"y"}})
	h.press("?")
	v := h.view()
	if !strings.Contains(v, "copy to other panel") {
		t.Fatalf("help lost the copy row:\n%s", v)
	}
	if !strings.Contains(v, "y ") || strings.Contains(v, "c/F5") {
		t.Fatalf("help still shows the old copy label:\n%s", v)
	}
}

// TestOverlayRebinding: overlay screens read the same set.
func TestOverlayRebinding(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	useBindings(t, h, map[string][]string{"tasks.close": {"z"}})

	h.press("t")
	if !h.a.tasks.showing {
		t.Fatal("t does not open the task view")
	}
	h.press("esc")
	if !h.a.tasks.showing {
		t.Fatal("the old close key still closes the task view")
	}
	h.press("z")
	if h.a.tasks.showing {
		t.Fatal("the rebound key does not close the task view")
	}
}

// TestKeybindingFileLoadAndReload: Settings shows the state of the file and
// reloads it without a restart.
func TestKeybindingFileLoadAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keybinding.json")
	if err := os.WriteFile(path, []byte(`{"version":1,"bindings":{"browse.copy":["y"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newBindingsApp(t, path)
	if got := h.a.bindingsSummary(); got != "1 changed" {
		t.Fatalf("summary = %q, want 1 changed", got)
	}
	if got := h.a.binds.Primary("browse.copy"); got != "y" {
		t.Fatalf("copy key = %q, want y", got)
	}
	// The Settings screen carries the row (and the path as its note).
	rows := h.a.settingsRows()
	found := false
	for _, r := range rows {
		if r.id == rowKeybindings {
			found = true
			if r.value != "1 changed" || !strings.Contains(r.after, "keybinding.json") {
				t.Fatalf("key bindings row = %+v", r)
			}
		}
	}
	if !found {
		t.Fatal("the Settings screen has no key bindings row")
	}

	// Change the file and reload: the new key applies at once.
	if err := os.WriteFile(path, []byte(`{"version":1,"bindings":{"browse.copy":["z"],"browse.move":["v"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h.a.reloadBindings()
	if got := h.a.binds.Primary("browse.copy"); got != "z" {
		t.Fatalf("after reload copy = %q, want z", got)
	}
	if got := h.a.binds.Primary("browse.move"); got != "v" {
		t.Fatalf("after reload move = %q, want v", got)
	}
	if !strings.Contains(lastLine(h.view()), "key bindings reloaded") &&
		!strings.Contains(h.status(), "key bindings reloaded") {
		t.Fatalf("reload is not reported: status %q", h.status())
	}
}

// TestBadKeybindingFileWarnsAndStarts: a damaged file never stops the start.
func TestBadKeybindingFileWarnsAndStarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keybinding.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	h := newBindingsApp(t, path)
	if h.a.keybindErr == nil {
		t.Fatal("a damaged file must be reported")
	}
	if got := h.a.binds.Primary("browse.copy"); got != "c" {
		t.Fatalf("copy key = %q, the defaults must be used", got)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the damaged file was removed: %v", err)
	}
	if got := h.a.bindingsSummary(); !strings.Contains(got, "could not") {
		t.Fatalf("summary = %q", got)
	}
}

// lastLine is the status/hint line of a rendered screen.
func lastLine(v string) string {
	return v[strings.LastIndex(v, "\n")+1:]
}
