package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
)

// The trash screen (T): deleting is reversible, restoring puts an item back
// where it came from, and old items are removed on a schedule the user sets.

// deleteToTrash deletes the named entry of the left panel and waits for it.
func (h *harness) deleteToTrash(name string) {
	h.t.Helper()
	h.focus(name)
	h.press("d")
	h.waitDialog(dlgDelete)
	h.press("y")
	h.idle()
}

func TestDeleteGoesToTrashAndTShowsIt(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.deleteToTrash("a.txt")
	if !strings.Contains(h.reported(), "Moved 1 item to the trash") {
		t.Fatalf("report %q", h.reported())
	}
	if !strings.Contains(h.reported(), "T opens the trash") {
		t.Fatalf("the result does not say where it went: %q", h.reported())
	}
	if fileExists(filepath.Join(env.src, "a.txt")) {
		t.Fatal("the file is still in the panel")
	}
	h.press("T")
	if h.a.trashScr == nil {
		t.Fatal("T did not open the trash")
	}
	v := h.view()
	for _, want := range []string{"Trash", "a.txt", "deleted just now", "restore", "empty"} {
		if !strings.Contains(v, want) {
			t.Errorf("trash screen lacks %q:\n%s", want, v)
		}
	}
	h.press("esc")
	if h.a.trashScr != nil {
		t.Fatal("Esc did not close the trash")
	}
}

func TestTrashAndTasksAreSeparateScreens(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.press("t")
	if !h.a.tasks.showing || h.a.trashScr != nil {
		t.Fatal("t must open the tasks, not the trash")
	}
	h.press("esc")
	h.press("T")
	if h.a.trashScr == nil || h.a.tasks.showing {
		t.Fatal("T must open the trash, not the tasks")
	}
	h.press("t") // tasks are not bound inside the trash: it stays open
	if h.a.trashScr == nil {
		t.Fatal("t closed the trash")
	}
	h.press("T")
	if h.a.trashScr != nil {
		t.Fatal("T did not close the trash")
	}
}

func TestTrashRestorePutsItBack(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.deleteToTrash("a.txt")
	h.press("T")
	h.press("r")
	if !fileExists(filepath.Join(env.src, "a.txt")) {
		t.Fatal("restore did not put the file back")
	}
	if !strings.Contains(h.reported(), "Restored a.txt") {
		t.Fatalf("report %q", h.reported())
	}
	if n := h.a.trashItems; n != 0 {
		t.Fatalf("%d items left in the trash", n)
	}
	if !strings.Contains(h.view(), "The trash is empty") {
		t.Fatalf("the empty state is missing:\n%s", h.view())
	}
	// the panel shows the restored file without a manual refresh
	if !panelHas(h.a.Panel(0), "a.txt") {
		t.Fatal("the panel does not show the restored file")
	}
}

func TestTrashRestoreNeverOverwrites(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.deleteToTrash("a.txt")
	// something else takes the original name
	if err := os.WriteFile(filepath.Join(env.src, "a.txt"), []byte("newer"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.press("T")
	h.press("r")
	if readFile(t, filepath.Join(env.src, "a.txt")) != "newer" {
		t.Fatal("restore overwrote the file that took the name")
	}
	// it went to the other panel instead, which is empty and takes the name
	if !fileExists(filepath.Join(env.dst, "a.txt")) {
		t.Fatalf("the restored copy is not in the other panel: %v", filesIn(env.dst))
	}
	if readFile(t, filepath.Join(env.dst, "a.txt")) != "new-a" {
		t.Fatal("the restored copy has the wrong content")
	}
	if !strings.Contains(h.reported(), env.dst) {
		t.Fatalf("the report does not name the new path: %q", h.reported())
	}
}

// TestFreeNameInNumbersAClash: a restore that cannot use the plain name picks
// "name (2)", then "name (3)", and never overwrites what is there.
func TestFreeNameInNumbersAClash(t *testing.T) {
	dir := t.TempDir()
	if got := freeNameIn(dir, "a.txt"); got != "a.txt" {
		t.Fatalf("free name = %q, want a.txt", got)
	}
	steps := []struct{ taken, want string }{
		{"a.txt", "a (2).txt"},
		{"a (2).txt", "a (3).txt"},
		{"a (3).txt", "a (4).txt"},
	}
	for _, step := range steps {
		if err := os.WriteFile(filepath.Join(dir, step.taken), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := freeNameIn(dir, "a.txt"); got != step.want {
			t.Fatalf("with %s taken the free name is %q, want %q", step.taken, got, step.want)
		}
	}
	// a dot-file has no stem: the number goes at the end, not before ".bashrc"
	if err := os.WriteFile(filepath.Join(dir, ".bashrc"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := freeNameIn(dir, ".bashrc"); got != ".bashrc (2)" {
		t.Fatalf("dot-file = %q, want .bashrc (2)", got)
	}
}

func TestTrashDeleteForGoodAndEmpty(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.deleteToTrash("a.txt")
	h.deleteToTrash("b.txt")
	h.press("T")
	if h.a.trashItems != 2 {
		t.Fatalf("trash has %d items", h.a.trashItems)
	}
	// d asks first, and "n"-like keys do nothing: only y or Enter act
	h.press("d")
	if h.a.dialog == nil || !strings.Contains(h.view(), "Delete from the trash?") {
		t.Fatal("deleting from the trash is not confirmed")
	}
	h.press("esc")
	if h.a.dialog != nil || h.a.trashItems != 2 {
		t.Fatal("cancel removed something")
	}
	h.press("d")
	h.press("y")
	if h.a.trashItems != 1 {
		t.Fatalf("one item should be gone, %d left", h.a.trashItems)
	}
	if !strings.Contains(h.reported(), "Deleted a.txt for good") && !strings.Contains(h.reported(), "Deleted b.txt for good") {
		t.Fatalf("report %q", h.reported())
	}
	// x empties the rest
	h.press("x")
	if h.a.dialog == nil || !strings.Contains(h.view(), "Empty the trash?") {
		t.Fatal("emptying is not confirmed")
	}
	h.press("y")
	if h.a.trashItems != 0 {
		t.Fatalf("%d items survived the empty", h.a.trashItems)
	}
	if !strings.Contains(h.reported(), "Deleted 1 item for good") {
		t.Fatalf("report %q", h.reported())
	}
}

func TestTrashRetentionSweepsOldItemsAtStartup(t *testing.T) {
	root := t.TempDir()
	trashDir := filepath.Join(root, "Trash")
	st, err := ops.OpenTrash(trashDir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	// an item deleted 40 days ago and one deleted yesterday
	old := filepath.Join(root, "old.txt")
	os.WriteFile(old, []byte("old"), 0o644)
	st.Now = func() time.Time { return now.Add(-40 * 24 * time.Hour) }
	if _, err := st.Put(old); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(root, "fresh.txt")
	os.WriteFile(fresh, []byte("fresh"), 0o644)
	st.Now = func() time.Time { return now.Add(-24 * time.Hour) }
	if _, err := st.Put(fresh); err != nil {
		t.Fatal(err)
	}
	st.Now = time.Now

	h, _ := newOpsApp(t, st, nil)
	if !strings.Contains(h.reported(), "Removed 1 item older than 29 days") {
		t.Fatalf("the sweep did not report itself: %q", h.reported())
	}
	entries, err := st.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name != "fresh.txt" {
		t.Fatalf("the wrong items survived: %+v", entries)
	}
}

func TestTrashRetentionSettingIsSaved(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.a.openSettings()
	h.press("f9") // ensure the screen is the settings
	h.a.showSettings = true
	rows := h.a.settingsRows()
	idx := -1
	for i, r := range rows {
		if r.label == "Keep deleted items for" {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("the Settings screen has no trash retention row")
	}
	h.a.settingsScr.sel = idx
	h.press("h") // prev: 29 -> 28
	if h.a.trashKeepDays != 28 {
		t.Fatalf("retention = %d, want 28", h.a.trashKeepDays)
	}
	if got := h.a.currentPrefs().Trash.Days; got != 28 {
		t.Fatalf("saved preference = %d, want 28", got)
	}
	// the row's value and the screen's text agree
	if !strings.Contains(h.view(), "28 days") {
		t.Fatalf("the row does not show the new value:\n%s", h.view())
	}
}

func TestTrashScreenFitsATinyTerminal(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.deleteToTrash("a.txt")
	for _, size := range [][2]int{{30, 9}, {40, 12}, {60, 20}, {120, 40}} {
		h.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		h.a.dismissNotice()
		h.press("T")
		if h.a.trashScr == nil {
			t.Fatalf("%dx%d: the trash did not open", size[0], size[1])
		}
		v := h.view()
		if !strings.Contains(v, "a.txt") {
			t.Fatalf("%dx%d: the item is not listed:\n%s", size[0], size[1], v)
		}
		h.press("esc")
	}
}

func TestTrashUnavailableExplainsWhy(t *testing.T) {
	h, _ := newOpsApp(t, ops.NoTrash{Reason: "no trash in this session"}, nil)
	h.press("T")
	if h.a.trashScr != nil {
		t.Fatal("the screen opened without a store")
	}
	if !strings.Contains(h.reported(), "no trash in this session") {
		t.Fatalf("report %q", h.reported())
	}
}

func TestTrashScreenNamesTheOriginalPath(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.deleteToTrash("sub")
	h.send(tea.WindowSizeMsg{Width: 120, Height: 30})
	h.press("T")
	v := h.view()
	if !strings.Contains(v, "sub/") {
		t.Fatalf("a folder is not marked as one:\n%s", v)
	}
	if !strings.Contains(v, filepath.Base(env.src)) {
		t.Fatalf("the original path is not shown:\n%s", v)
	}
}

// filesIn lists a folder's entries for failure messages.
func filesIn(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}
