package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ThatOn3Gu7/TwinPane/internal/foldersize"
	tea "github.com/charmbracelet/bubbletea"
)

func waitFolderSize(t *testing.T, h *harness) {
	t.Helper()
	h.until("folder-size scan", func() bool {
		return h.a.folderSize != nil && !h.a.folderSize.loading && h.a.folderSize.result != nil
	})
}

func folderSizeIndex(t *testing.T, v *folderSizeView, name string) int {
	t.Helper()
	for i, row := range folderSizeRows(v) {
		if row.name() == name {
			return i
		}
	}
	t.Fatalf("folder-size row %q not found", name)
	return -1
}

func TestFolderSizeSelectionAndPanelStateArePreserved(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.focus("sub")
	h.press("space")
	leftPath, rightPath := h.a.panels[0].Path(), h.a.panels[1].Path()
	leftCurrent, _ := h.a.panels[0].Current()
	rightCurrent, _ := h.a.panels[1].Current()
	leftSelection := h.a.panels[0].SelectedNames()
	rightSelection := h.a.panels[1].SelectedNames()

	h.press("A")
	waitFolderSize(t, h)
	v := h.a.folderSize
	if v.rootPath != filepath.Join(env.src, "sub") || v.rootLabel != "selected directory" {
		t.Fatalf("scan target = %q (%s), want selected subdirectory", v.rootPath, v.rootLabel)
	}
	if h.a.panels[0].Path() != leftPath || h.a.panels[1].Path() != rightPath {
		t.Fatalf("analysis changed panel paths: %q / %q", h.a.panels[0].Path(), h.a.panels[1].Path())
	}
	if current, _ := h.a.panels[0].Current(); current.Name != leftCurrent.Name {
		t.Fatalf("left cursor changed from %q to %q", leftCurrent.Name, current.Name)
	}
	if current, _ := h.a.panels[1].Current(); current.Name != rightCurrent.Name {
		t.Fatalf("right cursor changed from %q to %q", rightCurrent.Name, current.Name)
	}
	if got := h.a.panels[0].SelectedNames(); !sameStrings(got, leftSelection) {
		t.Fatalf("left selection changed: %v → %v", leftSelection, got)
	}
	if got := h.a.panels[1].SelectedNames(); !sameStrings(got, rightSelection) {
		t.Fatalf("right selection changed: %v → %v", rightSelection, got)
	}
	if !strings.Contains(h.view(), "Folder-size analysis") || !strings.Contains(h.view(), "selected directory") {
		t.Fatalf("analysis overlay is missing its target:\n%s", h.view())
	}
	if _, err := os.Stat(filepath.Join(env.src, "sub", "deep", "c.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestFolderSizeLargestFirstNavigationAndSortModes(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.focus("a.txt") // a file under the cursor falls back to the active folder
	h.press("A")
	waitFolderSize(t, h)
	v := h.a.folderSize
	if v.rootPath != env.src || v.rootLabel != "active folder" {
		t.Fatalf("scan target = %q (%s), want active folder", v.rootPath, v.rootLabel)
	}
	if v.root.Files != 3 || v.root.Directories != 3 || v.root.ApparentBytes != 11 {
		t.Fatalf("root totals: files=%d dirs=%d bytes=%d", v.root.Files, v.root.Directories, v.root.ApparentBytes)
	}
	rows := folderSizeRows(v)
	if len(rows) != 3 || rows[0].name() != "a.txt" || rows[1].name() != "b.txt" || rows[2].name() != "sub" {
		t.Fatalf("largest-first order = %v", folderSizeNames(rows))
	}
	v.cursor = folderSizeIndex(t, v, "sub")
	h.press("enter")
	if v.current.Name != "sub" {
		t.Fatalf("Enter did not drill into sub: current=%q", v.current.Name)
	}
	v.cursor = folderSizeIndex(t, v, "deep")
	h.press("l")
	if v.current.Name != "deep" {
		t.Fatalf("Right did not drill into deep: current=%q", v.current.Name)
	}
	h.press("backspace", "backspace")
	if v.current != v.root {
		t.Fatalf("parent navigation did not return to root: %q", v.current.Path)
	}
	h.press("n")
	rows = folderSizeRows(v)
	if names := folderSizeNames(rows); fmt.Sprint(names) != "[a.txt b.txt sub]" {
		t.Fatalf("name sort = %v", names)
	}
	h.press("s")
	rows = folderSizeRows(v)
	if rows[0].name() != "a.txt" || rows[2].name() != "sub" {
		t.Fatalf("size sort did not restore largest-first order: %v", folderSizeNames(rows))
	}

	if err := os.WriteFile(filepath.Join(env.src, "a.txt"), []byte(strings.Repeat("x", 20)), 0o644); err != nil {
		t.Fatal(err)
	}
	h.press("r")
	waitFolderSize(t, h)
	if v.root.ApparentBytes != 26 {
		t.Fatalf("refresh did not update totals: %d", v.root.ApparentBytes)
	}
	if v.current != v.root {
		t.Fatal("refresh did not restore the root navigation position")
	}
	v.cursor = folderSizeIndex(t, v, "sub")
	h.press("enter")
	if v.current.Name != "sub" {
		t.Fatal("could not return to the subdirectory before refresh")
	}
	if err := os.WriteFile(filepath.Join(env.src, "sub", "deep", "c.txt"), []byte("1234"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.press("r")
	waitFolderSize(t, h)
	if v.current.Name != "sub" || v.current.ApparentBytes != 4 || v.root.ApparentBytes != 29 {
		t.Fatalf("refresh did not restore/update subdirectory: current=%q bytes=%d root=%d", v.current.Name, v.current.ApparentBytes, v.root.ApparentBytes)
	}
}

func folderSizeNames(rows []folderSizeRow) []string {
	out := make([]string, len(rows))
	for i, row := range rows {
		out[i] = row.name()
	}
	return out
}

func TestFolderSizeHelpAndSelectionErrors(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.focus("a.txt")
	h.press("space")
	h.press("A")
	if h.a.folderSize != nil || !strings.Contains(h.status(), "Select a directory") {
		t.Fatalf("file selection should be refused, overlay=%v status=%q", h.a.folderSize, h.status())
	}
	h.press("u")
	h.press("A")
	waitFolderSize(t, h)
	h.press("?")
	if !h.a.showHelp || h.a.folderSize == nil {
		t.Fatal("folder-size help did not open over the analysis")
	}
	h.press("/")
	h.typeText("never followed", false)
	if !strings.Contains(h.view(), "SYMLINKS") || !strings.Contains(h.view(), "never followed") {
		t.Fatalf("folder-size help does not explain symlink handling:\n%s", h.view())
	}
	h.press("esc")
	h.press("/")
	h.typeText("hard links", false)
	if !strings.Contains(h.view(), "HARD LINKS") {
		t.Fatalf("folder-size help does not explain hard-link accounting:\n%s", h.view())
	}
	h.press("esc")
	h.press("esc")
	if h.a.showHelp || h.a.folderSize == nil {
		t.Fatal("closing help should return to folder-size analysis")
	}
	h.press("esc")
	if h.a.folderSize != nil {
		t.Fatal("Esc should close the completed folder-size analysis")
	}
}

func TestFolderSizeCancellationKeepsOverlayResponsive(t *testing.T) {
	a, _ := setup(t)
	cancelled := false
	a.folderSize = &folderSizeView{loading: true, cancelling: false, cancel: func() { cancelled = true }}
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil || !cancelled || !a.folderSize.cancelling {
		t.Fatalf("first Esc did not request cancellation: cmd=%v cancelled=%v view=%+v", cmd, cancelled, a.folderSize)
	}
	_, cmd = a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil || a.folderSize != nil {
		t.Fatalf("second Esc should dismiss a cancelling overlay: cmd=%v view=%+v", cmd, a.folderSize)
	}
}

func TestFolderSizeFailedResultIsVisible(t *testing.T) {
	a, _ := setup(t)
	a.folderSize = &folderSizeView{
		rootPath: "/missing/root",
		result:   &foldersize.Result{Status: foldersize.StatusFailed, Error: "/missing/root: disappeared or no longer exists"},
	}
	view := a.View()
	if !strings.Contains(view, "failed") || !strings.Contains(view, "disappeared") {
		t.Fatalf("failed scan not visible:\n%s", view)
	}
}

func TestFolderSizePartialResultShowsLowerBoundAndIssue(t *testing.T) {
	a, _ := setup(t)
	root := &foldersize.Node{
		Name: "root", Path: "/root", Status: foldersize.StatusPartial,
		ApparentBytes: 3, ApparentKnown: false, AllocatedKnown: false,
		Directories: 1, Files: 1, ErrorCount: 1,
		FirstError: "/root/missing: disappeared or no longer exists",
	}
	a.folderSize = &folderSizeView{
		rootPath: "/root", rootLabel: "active folder", root: root, current: root,
		result: &foldersize.Result{
			Root: root, Status: foldersize.StatusPartial, Errors: 1,
			Error: root.FirstError,
		},
	}
	view := a.View()
	if !strings.Contains(view, "partial") || !strings.Contains(view, "≥3 B") || !strings.Contains(view, "missing") {
		t.Fatalf("partial lower-bound/error details are not visible:\n%s", view)
	}
}

func TestFolderSizeMenuAndHelpBinding(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	if !h.a.keys.FolderSize.Matches(key("A")) {
		t.Fatal("A is not bound to folder-size analysis")
	}
	_, entries := h.a.menuEntries()
	found := false
	for _, entry := range entries {
		if entry.action == "browse.folderSize" && strings.Contains(entry.label, "folder sizes") {
			found = true
		}
	}
	if !found {
		t.Fatal("action menu does not expose folder-size analysis")
	}
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
