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
	old, oldQuiet := progressDelay, quietAfterEdit
	progressDelay, quietAfterEdit = 20*time.Millisecond, 0
	t.Cleanup(func() { progressDelay, quietAfterEdit = old, oldQuiet })

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
	if !h.dialogKind(dlgConfirm) || !strings.Contains(h.view(), `Copy file "a.txt"?`) {
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
	h.press("space", "space", "c", "enter")
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
	if h.a.dialog != nil || !strings.Contains(h.status(), "into itself") || !h.a.statusErr {
		t.Fatalf("status %q dialog %v", h.status(), h.a.dialog != nil)
	}
	h.press("m")
	if h.a.dialog != nil || !strings.Contains(h.status(), "into itself") {
		t.Fatalf("move status %q", h.status())
	}
}

func TestMoveFolderAndRefresh(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.focus("sub")
	h.press("m")
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

func TestCopyPartialFailureShowsReport(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs unix permissions")
	}
	h, env := newOpsApp(t, nil, nil)
	locked := filepath.Join(env.src, "b.txt")
	os.Chmod(locked, 0)
	defer os.Chmod(locked, 0o644)
	h.focus("a.txt")
	h.press("space", "space", "c", "enter")
	h.idle()
	if !h.dialogKind(dlgReport) || h.a.statusLevel != lvlErr {
		t.Fatalf("report expected, status %q", h.status())
	}
	v := h.view()
	for _, want := range []string{"finished with problems", "Failed (1)", "b.txt", "permission denied"} {
		if !strings.Contains(v, want) {
			t.Errorf("report lacks %q:\n%s", want, v)
		}
	}
	if !strings.Contains(h.status(), "Copied 1 file, 1 error") {
		t.Fatalf("status %q", h.status())
	}
	h.press("enter")
	if h.a.dialog != nil {
		t.Fatal("report should close")
	}
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
	if !h.dialogKind(dlgReport) {
		t.Fatalf("report expected; status %q", h.status())
	}
	v := h.view()
	for _, want := range []string{"Delete finished with problems", "Could not delete (1)", "c.txt", "permission denied"} {
		if !strings.Contains(v, want) {
			t.Errorf("report lacks %q:\n%s", want, v)
		}
	}
	if st := h.status(); !strings.Contains(st, "Delete incomplete: 1 of 2 items removed, 1 error") {
		t.Fatalf("status %q", st)
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

// ---- progress / busy state ----------------------------------------------------------

func TestBusyStateIgnoresKeysAndCancels(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	canceled := false
	h.a.op = &opSession{id: 99, title: "Copying", cancel: func() { canceled = true }, events: make(chan tea.Msg, 1),
		prog: ops.Progress{Phase: "copying", Current: "/x/big.iso", DoneItems: 3, TotalItems: 10, DoneBytes: 50 << 20, TotalBytes: 200 << 20, Errors: 1, LastError: "copy /x/y: permission denied"}}
	h.a.op.visible = true
	v := h.view()
	for _, want := range []string{"Copying…", "25%", "3 / 10 items", "50 M / 200 M", "/x/big.iso", "1 error so far", "Esc cancel"} {
		if !strings.Contains(v, want) {
			t.Errorf("progress lacks %q:\n%s", want, v)
		}
	}
	active := h.a.Active()
	h.a.Update(key("tab"))
	h.a.Update(key("q"))
	if h.a.Active() != active || h.a.quitting {
		t.Fatal("keys must be ignored while an operation runs")
	}
	h.a.Update(key("ctrl+c"))
	if !canceled || h.a.quitting || !h.a.op.canceling {
		t.Fatal("ctrl+c should cancel the operation, not quit")
	}
	if !strings.Contains(h.view(), "Stopping safely") {
		t.Fatal("cancelling state not shown")
	}
	h.a.op = nil
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
	h.focus("a.txt")
	h.a.Update(key("d")) // planning started, result not yet processed
	if h.a.planning == nil {
		t.Fatal("planning expected")
	}
	h.a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if h.a.busy() || !strings.Contains(h.status(), "nothing was deleted") {
		t.Fatal("esc should cancel planning")
	}
	// a late plan result is ignored
	h.a.Update(deletePlannedMsg{id: h.a.opSeq, plan: ops.DeletePlan{Paths: []string{filepath.Join(env.src, "a.txt")}}})
	if h.a.dialog != nil {
		t.Fatal("stale plan opened a dialog")
	}
	_ = context.Background
}

// ---- layout ---------------------------------------------------------------------------

func TestFooterAndHelpShowOperationKeys(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.send(tea.WindowSizeMsg{Width: 130, Height: 30})
	h.focus("a.txt") // the hints follow the cursor: file operations need a target
	v := h.view()
	footer := v[strings.LastIndex(v, "\n")+1:]
	for _, want := range []string{"copy", "move", "rename", "delete", "edit", "select", "help"} {
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
	for _, want := range []string{"Navigation", "File operations", "c/F5", "m/F6", "r/F2", "d/F8/Del", "e/F4", "Esc", "^L", "quit", "close help"} {
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
