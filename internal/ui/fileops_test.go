package ui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/editor"
	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
	"github.com/ThatOn3Gu7/TwinPane/internal/panel"
	"github.com/ThatOn3Gu7/TwinPane/internal/proc"
)

// harness emulates the Bubble Tea runtime asynchronously: commands run in
// goroutines and their messages are fed back into Update. This is needed
// because operations block on user answers (conflict dialogs).
type harness struct {
	t    *testing.T
	a    *App
	msgs chan tea.Msg
	quit bool
}

func (h *harness) exec(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() { h.msgs <- cmd() }()
}

func (h *harness) send(msg tea.Msg) {
	_, cmd := h.a.Update(msg)
	h.exec(cmd)
}

func (h *harness) press(keys ...string) {
	for _, k := range keys {
		var m tea.KeyMsg
		switch k {
		case "esc":
			m = tea.KeyMsg{Type: tea.KeyEsc}
		case "ctrl+u":
			m = tea.KeyMsg{Type: tea.KeyCtrlU}
		case "ctrl+k":
			m = tea.KeyMsg{Type: tea.KeyCtrlK}
		case "ctrl+r":
			m = tea.KeyMsg{Type: tea.KeyCtrlR}
		case "ctrl+f":
			m = tea.KeyMsg{Type: tea.KeyCtrlF}
		case "ctrl+l":
			m = tea.KeyMsg{Type: tea.KeyCtrlL}
		case "f7":
			m = tea.KeyMsg{Type: tea.KeyF7}
		case "pgdown":
			m = tea.KeyMsg{Type: tea.KeyPgDown}
		case "pgup":
			m = tea.KeyMsg{Type: tea.KeyPgUp}
		default:
			m = key(k)
		}
		h.send(m)
		h.settle()
	}
}

// typeText sends text as one multi-rune message (like fast typing or paste).
func (h *harness) typeText(s string, paste bool) {
	h.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s), Paste: paste})
	h.settle()
}

func (h *harness) handle(m tea.Msg) {
	switch m := m.(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range m {
			h.exec(c)
		}
	case tea.QuitMsg:
		h.quit = true
	default:
		h.send(m)
	}
}

// until processes messages until cond holds.
func (h *harness) until(what string, cond func() bool) {
	h.t.Helper()
	deadline := time.After(10 * time.Second)
	for !cond() {
		select {
		case m := <-h.msgs:
			h.handle(m)
		case <-deadline:
			st, _ := h.a.Status()
			h.t.Fatalf("timeout waiting for %s (status %q, busy=%v, dialog=%v)", what, st, h.a.busy(), h.a.dialog != nil)
		}
	}
}

// settle processes messages that arrive within a short quiet period.
func (h *harness) settle() {
	for {
		select {
		case m := <-h.msgs:
			h.handle(m)
		case <-time.After(30 * time.Millisecond):
			return
		}
	}
}

func (h *harness) idle() {
	h.t.Helper()
	h.until("operation to finish", func() bool { return !h.a.busy() })
}

func (h *harness) dialogKind(k dialogKind) bool { return h.a.dialog != nil && h.a.dialog.kind == k }

func (h *harness) waitDialog(k dialogKind) {
	h.t.Helper()
	h.until("dialog", func() bool { return h.dialogKind(k) })
}

func (h *harness) view() string { return ansi.Strip(h.a.View()) }

// focus moves the active panel's cursor to the named entry.
func (h *harness) focus(name string) {
	h.t.Helper()
	p := h.a.ActivePanel()
	p.Top()
	for i := 0; i < len(p.Entries()); i++ {
		if e, ok := p.Current(); ok && e.Name == name {
			return
		}
		p.Down()
	}
	h.t.Fatalf("entry %q not found in %s", name, p.Path())
}

func (h *harness) status() string { s, _ := h.a.Status(); return s }

func panelHas(p *panel.Panel, name string) bool {
	for _, e := range p.Entries() {
		if e.Name == name {
			return true
		}
	}
	return false
}

type execCall struct{ args []string }

type opsEnv struct {
	root, src, dst, trash string
	execs                 []execCall
	execErr               error
	editFn                func(args []string) // simulates what the editor does

	// phase 6: the opener environment (no real apps are ever started)
	env      map[string]string         // Getenv
	tools    map[string]bool           // extra programs LookPath finds (as /fake/bin/NAME)
	hide     map[string]bool           // installed programs LookPath must not find
	mu       sync.Mutex                // guards launches (Launch runs in a command goroutine)
	launches [][]string                // argv passed to Launch
	launchFn func(argv []string) error // Launch result (nil = success)
	bmPath   string                    // bookmarks file
}

// launched returns the argv lists passed to Launch so far.
func (e *opsEnv) launched() [][]string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([][]string(nil), e.launches...)
}

// newOpsApp creates root/src (left panel) and root/dst (right panel).
func newOpsApp(t *testing.T, tr ops.Trasher, edEnv *editor.Env) (*harness, *opsEnv) {
	t.Helper()
	root := t.TempDir()
	env := &opsEnv{root: root, src: filepath.Join(root, "src"), dst: filepath.Join(root, "dst"), trash: filepath.Join(root, "Trash")}
	for _, d := range []string{env.src, env.dst, filepath.Join(env.src, "sub", "deep")} {
		os.MkdirAll(d, 0o755)
	}
	os.WriteFile(filepath.Join(env.src, "a.txt"), []byte("new-a"), 0o644)
	os.WriteFile(filepath.Join(env.src, "b.txt"), []byte("new-b"), 0o644)
	os.WriteFile(filepath.Join(env.src, "sub", "deep", "c.txt"), []byte("c"), 0o644)
	t.Setenv("HOME", root+"/home")
	os.MkdirAll(root+"/home", 0o755)
	if tr == nil {
		tr = &ops.FreeDesktopTrash{Dir: env.trash}
	}
	if edEnv == nil {
		edEnv = &editor.Env{Getenv: func(string) string { return "" }, LookPath: func(n string) (string, error) { return "/bin/" + n, nil }, GOOS: "linux"}
	}
	oldQuiet := quietAfterEdit
	quietAfterEdit = 0
	t.Cleanup(func() { quietAfterEdit = oldQuiet })

	env.env = map[string]string{}
	env.tools = map[string]bool{}
	env.hide = map[string]bool{}
	env.bmPath = filepath.Join(root, "config", "bookmarks.json")
	a := New(Options{Version: "test", StartDir: env.src, Trasher: tr, EditorEnv: edEnv,
		Getenv: func(k string) string { return env.env[k] },
		GOOS:   "linux",
		LookPath: func(n string) (string, error) {
			if env.tools[n] {
				return "/fake/bin/" + n, nil
			}
			if env.hide[n] {
				return "", errors.New("not found")
			}
			return proc.LookPath(n)
		},
		Launch: func(argv []string) error {
			env.mu.Lock()
			env.launches = append(env.launches, argv)
			fn := env.launchFn
			env.mu.Unlock()
			if fn != nil {
				return fn(argv)
			}
			return nil
		},
		BookmarksPath: env.bmPath,
		// Preferences and the operation history live in the temp config dir,
		// so no test touches the real user configuration.
		SettingsPath: filepath.Join(root, "config", "settings.json"),
		HistoryPath:  filepath.Join(root, "config", "history.json"),
		Exec: func(c tea.ExecCommand, fn tea.ExecCallback) tea.Cmd {
			proc, ok := c.(*editor.Process)
			if !ok {
				t.Fatalf("exec got %T, want *editor.Process", c)
			}
			env.execs = append(env.execs, execCall{proc.Cmd.Args})
			if env.editFn != nil {
				env.editFn(proc.Cmd.Args)
			}
			err := env.execErr
			return func() tea.Msg { return fn(err) }
		}})
	h := &harness{t: t, a: a, msgs: make(chan tea.Msg, 256)}
	h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
	h.exec(a.Init())
	h.until("left panel", func() bool { return a.Panel(0).Path() == env.src && !a.Panel(1).Loading() })
	h.exec(a.Panel(1).Load(env.dst, "", panel.LoadNavigate))
	h.until("right panel", func() bool { return a.Panel(1).Path() == env.dst })
	return h, env
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func fileExists(p string) bool { _, err := os.Lstat(p); return err == nil }

// ---- copy / move ---------------------------------------------------------------

func TestCopyWithConflictOverwrite(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	os.WriteFile(filepath.Join(env.dst, "a.txt"), []byte("old-a"), 0o644)
	h.focus("a.txt")
	h.press("c")
	h.waitDialog(dlgConfirm)
	if !strings.Contains(h.view(), `Copy file "a.txt"?`) {
		t.Fatalf("confirm dialog expected:\n%s", h.view())
	}
	h.press("enter")
	h.waitDialog(dlgConflict)
	v := h.view()
	for _, want := range []string{`A file named "a.txt" already exists`, "Existing:", "Incoming:", "Skip (s)", "Overwrite (o)", "Apply to all"} {
		if !strings.Contains(v, want) {
			t.Errorf("conflict dialog lacks %q:\n%s", want, v)
		}
	}
	h.press("o")
	h.idle()
	if got := readFile(t, filepath.Join(env.dst, "a.txt")); got != "new-a" {
		t.Fatalf("not overwritten: %q", got)
	}
	if st := h.status(); st != "Copied 1 file" {
		t.Fatalf("status %q", st)
	}
	if !fileExists(filepath.Join(env.src, "a.txt")) {
		t.Fatal("copy removed source")
	}
}

func TestConflictDefaultSkipAndApplyAll(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	for _, n := range []string{"a.txt", "b.txt"} {
		os.WriteFile(filepath.Join(env.dst, n), []byte("old"), 0o644)
	}
	h.focus("a.txt")
	h.press("space", "space") // select a.txt and b.txt
	h.press("c")
	h.waitDialog(dlgConfirm)
	if !strings.Contains(h.view(), "Copy 2 files?") {
		t.Fatalf("expected count in title:\n%s", h.view())
	}
	h.press("enter")
	h.waitDialog(dlgConflict)
	h.press("a")     // apply to all
	h.press("enter") // default button = Skip
	h.idle()
	if h.dialog() != nil {
		t.Fatal("second conflict should not be asked")
	}
	for _, n := range []string{"a.txt", "b.txt"} {
		if readFile(t, filepath.Join(env.dst, n)) != "old" {
			t.Fatalf("%s overwritten", n)
		}
	}
	if st := h.status(); !strings.Contains(st, "skipped 2") {
		t.Fatalf("status %q", st)
	}
}

func (h *harness) dialog() *dialog { return h.a.dialog }

func TestCancelAtConflictIsNotReportedAsSuccess(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	os.WriteFile(filepath.Join(env.dst, "a.txt"), []byte("old"), 0o644)
	h.focus("a.txt")
	h.press("space", "space", "c")
	h.waitDialog(dlgConfirm)
	h.press("enter")
	h.waitDialog(dlgConflict)
	h.press("esc")
	h.idle()
	st := h.status()
	if !strings.Contains(st, "cancelled") || h.a.statusLevel != lvlWarn {
		t.Fatalf("status %q level %d", st, h.a.statusLevel)
	}
	if fileExists(filepath.Join(env.dst, "b.txt")) {
		t.Fatal("items after cancel must not be copied")
	}
	if len(h.a.ActivePanel().Selection()) != 2 {
		t.Fatal("selection must be kept when the operation did not fully succeed")
	}
}

func TestCopyIntoItselfRejectedWithMessage(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.exec(h.a.Panel(1).Load(filepath.Join(env.src, "sub", "deep"), "", panel.LoadNavigate))
	h.until("right panel", func() bool { return strings.HasSuffix(h.a.Panel(1).Path(), "deep") })
	h.focus("sub")
	h.press("c")
	h.idle()
	if h.a.dialog != nil || !strings.Contains(h.status(), "into itself") || !h.a.statusErr {
		t.Fatalf("status %q dialog %v", h.status(), h.a.dialog != nil)
	}
	h.press("m")
	h.idle()
	if h.a.dialog != nil || !strings.Contains(h.status(), "into itself") {
		t.Fatalf("move status %q", h.status())
	}
}

func TestMoveFolderAndRefresh(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.focus("sub")
	h.press("m")
	h.waitDialog(dlgConfirm)
	if !strings.Contains(h.view(), `Move folder "sub"?`) {
		t.Fatalf("%s", h.view())
	}
	h.press("enter")
	h.idle()
	h.settle()
	if fileExists(filepath.Join(env.src, "sub")) || readFile(t, filepath.Join(env.dst, "sub", "deep", "c.txt")) != "c" {
		t.Fatal("move failed")
	}
	if panelHas(h.a.Panel(0), "sub") || !panelHas(h.a.Panel(1), "sub") {
		t.Fatal("panels not refreshed")
	}
	if e, _ := h.a.Panel(1).Current(); e.Name != "sub" {
		t.Fatalf("destination cursor should be on moved item, got %q", e.Name)
	}
	if !strings.HasPrefix(h.status(), "Moved") {
		t.Fatalf("status %q", h.status())
	}
}

func TestCopyPartialFailureIsRetainedInTaskView(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix permissions")
	}
	h, env := newOpsApp(t, nil, nil)
	locked := filepath.Join(env.src, "b.txt")
	os.Chmod(locked, 0)
	defer os.Chmod(locked, 0o644)
	h.focus("a.txt")
	h.press("space", "space", "c")
	h.waitDialog(dlgConfirm)
	h.press("enter")
	h.idle()
	if h.a.dialog != nil || h.a.statusLevel != lvlErr {
		t.Fatalf("background failure should not interrupt browsing, status %q", h.status())
	}
	if !strings.Contains(h.status(), "Copied 1 file, 1 error") {
		t.Fatalf("status %q", h.status())
	}
	h.press("t")
	v := h.view()
	for _, want := range []string{"PARTIAL", "Copied 1 file, 1 error", "b.txt", "permission denied", "From:", "To:"} {
		if !strings.Contains(v, want) {
			t.Errorf("task view lacks %q:\n%s", want, v)
		}
	}
	h.press("esc")
	if fileExists(filepath.Join(env.dst, "b.txt")) || readFile(t, filepath.Join(env.dst, "a.txt")) != "new-a" {
		t.Fatal("wrong destination state")
	}
}

// ---- delete ----------------------------------------------------------------------

func TestDeleteToTrashDefaultIsNo(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.focus("a.txt")
	h.press("d")
	h.waitDialog(dlgDelete)
	v := h.view()
	for _, want := range []string{"Move 1 file to the trash?", filepath.Join(env.src, "a.txt"), "No, cancel (n)", "Yes, move to trash (y)", "restored from the trash"} {
		if !strings.Contains(v, want) {
			t.Errorf("dialog lacks %q:\n%s", want, v)
		}
	}
	h.press("enter") // default = No
	if h.a.dialog != nil || !fileExists(filepath.Join(env.src, "a.txt")) || !strings.Contains(h.status(), "nothing was deleted") {
		t.Fatalf("Enter must cancel: status %q", h.status())
	}
	h.press("d")
	h.waitDialog(dlgDelete)
	h.press("y")
	h.idle()
	h.settle()
	if fileExists(filepath.Join(env.src, "a.txt")) || readFile(t, filepath.Join(env.trash, "files", "a.txt")) != "new-a" {
		t.Fatal("not moved to trash")
	}
	if h.status() != "Moved 1 item to the trash" || panelHas(h.a.Panel(0), "a.txt") {
		t.Fatalf("status %q", h.status())
	}
	if e, _ := h.a.Panel(0).Current(); e.Name != "b.txt" {
		t.Fatalf("cursor should move to next entry, got %q", e.Name)
	}
}

func TestPermanentRecursiveDeleteRequiresTypedConfirmation(t *testing.T) {
	h, env := newOpsApp(t, ops.NoTrash{Reason: "no trash in test"}, nil)
	h.focus("sub")
	h.press("d")
	h.waitDialog(dlgDelete)
	v := h.view()
	for _, want := range []string{"Permanently delete 1 folder?", "PERMANENTLY delete", "ALL of their contents", "2 items inside", "CANNOT be undone", "Trash not used: no trash in test", `Type "delete"`} {
		if !strings.Contains(v, want) {
			t.Errorf("dialog lacks %q:\n%s", want, v)
		}
	}
	h.press("y")     // goes into the text field, does not confirm
	h.press("enter") // wrong word
	if !fileExists(filepath.Join(env.src, "sub")) || h.a.dialog == nil || h.a.dialog.errLine == "" {
		t.Fatal("wrong confirmation word must not delete")
	}
	h.press("ctrl+u")
	h.typeText("delete", true) // pasted into the field
	h.press("enter")
	h.idle()
	if fileExists(filepath.Join(env.src, "sub")) {
		t.Fatal("not deleted")
	}
	if st := h.status(); st != "Deleted 1 item (1 file, 2 folders)" {
		t.Fatalf("status %q", st)
	}
}

func TestPermanentDeleteEmptyFieldCancels(t *testing.T) {
	h, env := newOpsApp(t, ops.NoTrash{Reason: "none"}, nil)
	h.focus("sub")
	h.press("d")
	h.waitDialog(dlgDelete)
	h.press("enter")
	if h.a.dialog != nil || !fileExists(filepath.Join(env.src, "sub")) {
		t.Fatal("Enter on empty field must cancel")
	}
}

func TestSinglePermanentFileUsesButtons(t *testing.T) {
	h, env := newOpsApp(t, ops.NoTrash{Reason: "none"}, nil)
	h.focus("a.txt")
	h.press("d")
	h.waitDialog(dlgDelete)
	if h.a.dialog.input != nil || !strings.Contains(h.view(), "Yes, delete permanently (y)") {
		t.Fatalf("%s", h.view())
	}
	h.press("y")
	h.idle()
	if fileExists(filepath.Join(env.src, "a.txt")) {
		t.Fatal("not deleted")
	}
}

func TestCoalescedDeleteKeysDoNotConfirm(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.focus("a.txt")
	h.typeText("dy", false) // "d" then "y" in one terminal read
	h.waitDialog(dlgDelete)
	h.typeText("y", true) // a pasted "y" must not press the button either
	if !fileExists(filepath.Join(env.src, "a.txt")) {
		t.Fatal("keys typed together with d confirmed the deletion")
	}
	h.press("y")
	h.idle()
	if fileExists(filepath.Join(env.src, "a.txt")) {
		t.Fatal("explicit y should delete")
	}
}

func TestDeletePartialFailureReport(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix permissions")
	}
	h, env := newOpsApp(t, ops.NoTrash{Reason: "none"}, nil)
	deep := filepath.Join(env.src, "sub", "deep")
	os.Chmod(deep, 0o555)
	defer os.Chmod(deep, 0o755)
	h.focus("a.txt")
	h.press("space") // a.txt
	h.focus("sub")
	h.press("space")
	h.press("d")
	h.waitDialog(dlgDelete)
	if !strings.Contains(h.view(), "2 items (1 folder, 1 file)") {
		t.Fatalf("%s", h.view())
	}
	h.typeText("delete", false)
	h.press("enter")
	h.idle()
	if h.a.dialog != nil {
		t.Fatalf("background delete failure should not replace the UI; status %q", h.status())
	}
	if st := h.status(); !strings.Contains(st, "Delete incomplete: 1 of 2 items removed, 1 error") {
		t.Fatalf("status %q", st)
	}
	h.press("t")
	v := h.view()
	for _, want := range []string{"PARTIAL", "Delete incomplete: 1 of 2 items removed, 1 error", "c.txt", "permission denied"} {
		if !strings.Contains(v, want) {
			t.Errorf("task view lacks %q:\n%s", want, v)
		}
	}
	if fileExists(filepath.Join(env.src, "a.txt")) || !fileExists(filepath.Join(deep, "c.txt")) {
		t.Fatal("unexpected filesystem state")
	}
}

func TestDeleteNothingAndParentEntry(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.a.ActivePanel().Top() // ".."
	for _, k := range []string{"d", "c", "m", "r", "e"} {
		h.press(k)
		if h.a.dialog != nil || h.a.busy() || !h.a.statusErr {
			t.Fatalf("%s on '..' should only show an error (status %q)", k, h.status())
		}
	}
}

// ---- rename ---------------------------------------------------------------------

func TestRenameFlow(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.focus("a.txt")
	h.press("r")
	if !h.dialogKind(dlgRename) || h.a.dialog.input.Value() != "a.txt" || h.a.dialog.input.pos != 1 {
		t.Fatalf("rename dialog should be prefilled with cursor before extension")
	}
	h.press("ctrl+u", "ctrl+k") // clear the whole field
	h.typeText("b.txt", false)
	h.press("enter")
	if h.a.dialog == nil || !strings.Contains(h.a.dialog.errLine, "already exists") {
		t.Fatal("collision must be reported inline")
	}
	h.press("ctrl+u", "ctrl+k")
	h.typeText("../escape.txt", false)
	h.press("enter")
	if h.a.dialog == nil || !strings.Contains(h.a.dialog.errLine, "invalid name") {
		t.Fatalf("traversal must be refused: %q", h.a.dialog.errLine)
	}
	h.press("ctrl+u", "ctrl+k")
	h.typeText("renamed.txt", false)
	h.press("enter")
	h.settle()
	if h.a.dialog != nil || !fileExists(filepath.Join(env.src, "renamed.txt")) || fileExists(filepath.Join(env.src, "a.txt")) {
		t.Fatal("rename failed")
	}
	if e, _ := h.a.ActivePanel().Current(); e.Name != "renamed.txt" {
		t.Fatalf("cursor should follow the renamed item, got %q", e.Name)
	}
	if readFile(t, filepath.Join(env.src, "b.txt")) != "new-b" || fileExists(filepath.Join(env.root, "escape.txt")) {
		t.Fatal("other files affected")
	}
	h.press("r", "esc")
	if h.a.dialog != nil {
		t.Fatal("esc should close rename")
	}
}

// ---- edit -----------------------------------------------------------------------

func TestEditLaunchesEditorAndRefreshes(t *testing.T) {
	edEnv := &editor.Env{
		Getenv:   func(k string) string { return map[string]string{"VISUAL": "myed --wait"}[k] },
		LookPath: func(n string) (string, error) { return "/opt/" + n, nil },
		GOOS:     "linux",
	}
	h, env := newOpsApp(t, nil, edEnv)
	h.focus("a.txt")
	h.press("e")
	h.settle()
	if len(env.execs) != 1 || strings.Join(env.execs[0].args, " ") != "/opt/myed --wait "+filepath.Join(env.src, "a.txt") {
		t.Fatalf("exec calls %+v", env.execs)
	}
	if !strings.HasPrefix(h.status(), "Finished editing a.txt — nothing saved") || !strings.Contains(h.status(), "myed's own help") {
		t.Fatalf("status %q", h.status())
	}

	env.execErr = &exec.ExitError{}
	h.press("e")
	h.settle()
	if !h.a.statusErr || !strings.Contains(h.status(), "editor myed exited") {
		t.Fatalf("failing editor: %q", h.status())
	}
	env.execErr = errors.New("exec: not started")
	h.press("e")
	h.settle()
	if !strings.Contains(h.status(), "could not run editor") {
		t.Fatalf("status %q", h.status())
	}
}

func TestEditRefusesDirsAndWarnsOnBinary(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.focus("sub")
	h.press("e")
	if !h.a.statusErr || !strings.Contains(h.status(), "only files can be edited") || len(env.execs) != 0 {
		t.Fatalf("dir: %q", h.status())
	}
	os.WriteFile(filepath.Join(env.src, "prog.bin"), []byte{0x7f, 'E', 'L', 'F', 0, 1, 2}, 0o644)
	h.press("ctrl+l")
	h.settle()
	h.focus("prog.bin")
	h.press("e")
	if !h.dialogKind(dlgBinary) || !strings.Contains(h.view(), "looks like a binary file") {
		t.Fatalf("binary warning expected:\n%s", h.view())
	}
	h.press("enter") // default = Cancel
	if len(env.execs) != 0 || h.a.dialog != nil {
		t.Fatal("Enter must cancel")
	}
	h.press("e", "o")
	h.settle()
	if len(env.execs) != 1 {
		t.Fatal("Open anyway should launch the editor")
	}
}

func TestEditMissingEditor(t *testing.T) {
	edEnv := &editor.Env{
		Getenv:   func(k string) string { return map[string]string{"EDITOR": "nonexistent-ed"}[k] },
		LookPath: func(n string) (string, error) { return "", exec.ErrNotFound },
		GOOS:     "linux",
	}
	h, env := newOpsApp(t, nil, edEnv)
	h.focus("a.txt")
	h.press("e")
	if !h.a.statusErr || !strings.Contains(h.status(), `"nonexistent-ed" from $EDITOR was not found`) || len(env.execs) != 0 {
		t.Fatalf("status %q", h.status())
	}
}

// ---- background task view / responsiveness ---------------------------------------

func TestLowSpaceDeclineIsRetainedAsCanceledTask(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	s, ctx := h.a.newSession("Copying", 0, nil)
	s.kind = ops.Copy
	t.Cleanup(s.cancel)

	decision := make(chan bool, 1)
	go func() { decision <- h.a.hooks(ctx, s).LowSpace(1024, 128) }()
	select {
	case msg := <-s.events:
		if _, ok := h.a.handleOpMsg(msg); !ok {
			t.Fatalf("low-space event was not recognized: %T", msg)
		}
		h.a.showNextTaskPrompt()
	case <-time.After(time.Second):
		t.Fatal("low-space hook did not request a decision")
	}
	h.press("esc")
	select {
	case continued := <-decision:
		if continued || !s.lowSpaceDeclined {
			t.Fatal("declining the free-space warning should stop the operation")
		}
	case <-time.After(time.Second):
		t.Fatal("low-space hook did not resume after the user declined")
	}

	cmd := h.a.finishTransfer(s, ops.Result{Kind: ops.Copy, Fatal: errors.New("not enough free space")})
	h.exec(cmd)
	h.settle()
	if s.state != taskCanceled || !strings.Contains(s.summary, "cancelled") || h.a.statusLevel != lvlWarn {
		t.Fatalf("declined low-space warning should be a canceled task: state=%v summary=%q level=%v", s.state, s.summary, h.a.statusLevel)
	}
	h.press("t")
	v := h.view()
	if !strings.Contains(v, "CANCELED") || !strings.Contains(v, "not enough free space") {
		t.Fatalf("task view lacks the canceled low-space outcome:\n%s", v)
	}
}

func TestBackgroundTasksKeepUIResponsiveAndCancelSelectedTask(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	cancelled := [2]bool{}
	first := &opSession{id: 901, title: "Copying", state: taskRunning, cancel: func() { cancelled[0] = true },
		events: make(chan tea.Msg, 1), from: []string{"/src/first"}, to: "/dst"}
	second := &opSession{id: 902, title: "Moving", state: taskRunning, cancel: func() { cancelled[1] = true },
		events: make(chan tea.Msg, 1), from: []string{"/src/big.iso"}, to: "/dst",
		prog: ops.Progress{Phase: "moving", Current: "/src/big.iso", DoneItems: 3, TotalItems: 10, DoneBytes: 50 << 20, TotalBytes: 200 << 20, Errors: 1, LastError: "move /src/old: permission denied"}}
	h.a.tasks.add(first)
	h.a.tasks.add(second)

	// Background work must not lock normal panel navigation.
	h.a.Update(key("tab"))
	h.a.Update(key("j"))
	if h.a.Active() != 1 || h.a.quitting {
		t.Fatal("panel switching/navigation should continue while tasks run")
	}
	// Quitting is guarded until work has completed or been cancelled.
	h.a.Update(key("q"))
	if !h.a.tasks.showing || h.a.quitting {
		t.Fatal("q should open the task view instead of abandoning active work")
	}
	v := h.view()
	for _, want := range []string{"Background tasks", "RUNNING", "25%", "/src/big.iso", "/dst", "permission denied"} {
		if !strings.Contains(v, want) {
			t.Errorf("task view lacks %q:\n%s", want, v)
		}
	}
	// The newest task is selected; cancellation does not affect its sibling.
	h.a.Update(key("c"))
	if !cancelled[1] || cancelled[0] || second.state != taskCanceling || h.a.quitting {
		t.Fatal("cancel should stop only the selected task and keep the app open")
	}
	if !strings.Contains(h.view(), "STOPPING") || !strings.Contains(h.status(), "Stopping") {
		t.Fatal("stopping state should remain visible in the task view")
	}
	h.a.finishTask(first, taskCompleted, "Copied 1 file")
	h.a.finishTask(second, taskCanceled, "Move cancelled")
	if h.a.busy() {
		t.Fatal("completed history must not count as active work")
	}
	h.a.Update(key("x"))
	if len(h.a.tasks.order) != 0 {
		t.Fatal("x should clear retained completed task history")
	}
}

func TestConcurrentTasksSerializeConflictPromptsAndRetainResults(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	s1, ctx1 := h.a.newSession("Copying", 0, nil)
	s2, ctx2 := h.a.newSession("Moving", 0, nil)
	t.Cleanup(func() {
		if s1.cancel != nil {
			s1.cancel()
		}
		if s2.cancel != nil {
			s2.cancel()
		}
	})
	h.exec(h.a.startCmds(s1))
	h.exec(h.a.startCmds(s2))
	decisions := []chan ops.Decision{make(chan ops.Decision, 1), make(chan ops.Decision, 1)}
	go func() {
		decisions[0] <- h.a.hooks(ctx1, s1).Conflict(ops.Conflict{Src: "/src/one", Dst: "/dst/one"})
	}()
	go func() {
		decisions[1] <- h.a.hooks(ctx2, s2).Conflict(ops.Conflict{Src: "/src/two", Dst: "/dst/two"})
	}()
	h.until("two concurrent conflict decisions", func() bool {
		return h.a.dialog != nil && h.a.dialog.kind == dlgConflict && h.a.tasks.activePrompt != 0 && len(h.a.tasks.prompts) == 1
	})
	firstPrompt := h.a.tasks.activePrompt
	if firstPrompt != s1.id && firstPrompt != s2.id {
		t.Fatalf("unexpected active task prompt #%d", firstPrompt)
	}
	if h.a.tasks.activeCount() != 2 {
		t.Fatalf("both tasks should remain active, got %d", h.a.tasks.activeCount())
	}

	h.press("s") // safely skip the first task's conflict
	h.until("second conflict prompt", func() bool {
		return h.a.dialog != nil && h.a.dialog.kind == dlgConflict && h.a.tasks.activePrompt != firstPrompt
	})
	h.press("s") // and the other task's prompt
	for i := range decisions {
		select {
		case d := <-decisions[i]:
			if d.Action != ops.Skip {
				t.Fatalf("task %d decision = %v, want Skip", i+1, d.Action)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("task %d conflict hook did not resume", i+1)
		}
	}

	// Completion events are independent: neither task replaces the other's
	// record, and finished results remain available in the task view.
	s1.events <- opTransferDoneMsg{id: s1.id, res: ops.Result{Kind: ops.Copy, Files: 1}}
	s2.events <- opTransferDoneMsg{id: s2.id, res: ops.Result{Kind: ops.Move, Files: 1}}
	h.until("both tasks to finish", func() bool { return !s1.active() && !s2.active() })
	if len(h.a.tasks.order) != 2 || h.a.tasks.activeCount() != 0 {
		t.Fatalf("task history lost a result: order=%v active=%d", h.a.tasks.order, h.a.tasks.activeCount())
	}
	h.a.openTasks()
	v := h.view()
	if !strings.Contains(v, "DONE") || !strings.Contains(v, "Moved 1 file") {
		t.Fatalf("completed tasks should remain visible:\n%s", v)
	}
}

func TestTaskRefreshUsesCapturedPanelPaths(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	s, _ := h.a.newSession("Copying", 0, []string{filepath.Join(env.src, "a.txt")})
	s.kind, s.to = ops.Copy, env.dst
	h.exec(h.a.startCmds(s))

	// While the copy runs, browsing remains available and the source panel
	// can leave the directory captured by the task.
	elsewhere := filepath.Join(env.root, "elsewhere")
	if err := os.Mkdir(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}
	h.exec(h.a.panels[0].Load(elsewhere, "", panel.LoadNavigate))
	h.until("source panel navigation", func() bool { return h.a.panels[0].Path() == elsewhere && !h.a.panels[0].Loading() })
	if err := os.WriteFile(filepath.Join(env.dst, "arrived.txt"), []byte("done"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.events <- opTransferDoneMsg{id: s.id, res: ops.Result{Kind: ops.Copy, Files: 1}}
	h.until("destination refresh without resetting the source panel", func() bool {
		return !s.active() && panelHas(h.a.panels[1], "arrived.txt")
	})
	if h.a.panels[0].Path() != elsewhere || h.a.panels[0].Loading() {
		t.Fatalf("completion refreshed a panel that no longer shows the source: path=%q loading=%v", h.a.panels[0].Path(), h.a.panels[0].Loading())
	}
}

func TestBackgroundCompletionPreservesNewPanelSelection(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.focus("a.txt")
	s, _ := h.a.newSession("Copying", 0, []string{filepath.Join(env.src, "a.txt")})
	s.kind, s.to = ops.Copy, env.dst
	h.exec(h.a.startCmds(s))

	// A selection made while the background copy is running belongs to the
	// current browsing session and must not be cleared by its completion.
	h.a.panels[0].ToggleSelect()
	if !h.a.panels[0].IsSelected("a.txt") {
		t.Fatal("test setup: expected a selection made after task start")
	}
	s.events <- opTransferDoneMsg{id: s.id, res: ops.Result{Kind: ops.Copy, Files: 1}}
	h.until("task completion", func() bool { return !s.active() })
	if !h.a.panels[0].IsSelected("a.txt") {
		t.Fatal("background completion cleared a selection created after the task started")
	}
}

func TestRealCancelDuringLargeCopy(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	big := filepath.Join(env.src, "big")
	os.Mkdir(big, 0o755)
	data := strings.Repeat("x", 1<<20)
	for i := 0; i < 40; i++ {
		os.WriteFile(filepath.Join(big, "f"+string(rune('A'+i))), []byte(data), 0o644)
	}
	h.press("ctrl+l")
	h.focus("big")
	h.press("c")
	h.waitDialog(dlgConfirm)
	h.send(key("enter"))                 // starts the copy
	h.send(tea.KeyMsg{Type: tea.KeyEsc}) // cancel immediately, before it can finish
	h.idle()
	st := h.status()
	if strings.HasPrefix(st, "Copied") && fileExists(filepath.Join(env.dst, "big", "f"+string(rune('A'+39)))) {
		t.Skip("copy finished before cancel could take effect")
	}
	if !strings.Contains(st, "cancelled") {
		t.Fatalf("status %q", st)
	}
	// no partial temp files anywhere
	filepath.Walk(env.dst, func(p string, fi os.FileInfo, err error) error {
		if err == nil && strings.HasPrefix(fi.Name(), ".twinpane-") {
			t.Errorf("temp file left: %s", p)
		}
		if err == nil && !fi.IsDir() && fi.Size() != int64(len(data)) {
			t.Errorf("partial file at destination: %s (%d bytes)", p, fi.Size())
		}
		return nil
	})
}

func TestDeletePlanningCanBeCancelled(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	path := filepath.Join(env.src, "a.txt")
	s, _ := h.a.newSession("Checking delete", 0, []string{path})
	s.isDelete, s.state = true, taskPlanning
	h.a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !s.canceling || !h.a.busy() {
		t.Fatal("Esc should cancel the active delete scan without locking the UI")
	}
	h.a.Update(deletePlannedMsg{id: s.id, err: context.Canceled})
	if s.active() || h.a.busy() || !strings.Contains(h.status(), "nothing was deleted") {
		t.Fatal("canceled scan should finish as a retained task")
	}
	// A late plan result must not resurrect the cancelled confirmation.
	h.a.Update(deletePlannedMsg{id: s.id, plan: ops.DeletePlan{Paths: []string{path}}})
	if h.a.dialog != nil {
		t.Fatal("stale plan opened a dialog")
	}
}

// ---- layout ---------------------------------------------------------------------------

func TestFooterAndHelpShowOperationKeys(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.send(tea.WindowSizeMsg{Width: 130, Height: 30})
	h.focus("a.txt") // the hints follow the cursor: file operations need a target
	v := h.view()
	footer := v[strings.LastIndex(v, "\n")+1:]
	for _, want := range []string{"copy", "move", "rename", "delete", "edit", "select", "tasks", "help"} {
		if !strings.Contains(footer, want) {
			t.Errorf("footer lacks %q: %s", want, footer)
		}
	}
	h.send(tea.WindowSizeMsg{Width: 50, Height: 20})
	v = h.view()
	if footer = v[strings.LastIndex(v, "\n")+1:]; !strings.Contains(footer, "help") {
		t.Errorf("help hint must stay visible on narrow screens: %q", footer)
	}

	h.send(tea.WindowSizeMsg{Width: 80, Height: 24})
	h.press("?")
	v = h.view()
	for _, want := range []string{"Navigation", "File operations", "c/F5", "m/F6", "r/F2", "d/F8/Del", "e/F4", "tasks & operation history", "Esc", "^L", "quit", "close help"} {
		if !strings.Contains(v, want) {
			t.Errorf("help at 80x24 lacks %q:\n%s", want, v)
		}
	}
	checkScreen(t, v, 80, 24)
}

func checkScreen(t *testing.T, v string, w, hgt int) {
	t.Helper()
	lines := strings.Split(v, "\n")
	if len(lines) != hgt {
		t.Errorf("screen has %d lines, want %d", len(lines), hgt)
	}
	for i, l := range lines {
		if ansi.StringWidth(l) > w {
			t.Errorf("line %d is %d wide (> %d): %q", i, ansi.StringWidth(l), w, l)
		}
	}
}

func TestDialogsFitSmallScreens(t *testing.T) {
	h, _ := newOpsApp(t, ops.NoTrash{Reason: "none"}, nil)
	for _, size := range [][2]int{{80, 24}, {60, 16}, {40, 12}} {
		h.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		h.focus("sub")
		h.press("space")
		h.focus("a.txt")
		h.press("space")
		h.press("d")
		h.waitDialog(dlgDelete)
		checkScreen(t, h.a.View(), size[0], size[1])
		h.press("esc")
		h.a.ActivePanel().ClearSelection()
	}
}
