package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ThatOn3Gu7/TwinPane/internal/dircompare"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func waitComparison(t *testing.T, h *harness) {
	t.Helper()
	h.until("directory comparison", func() bool {
		return h.a.comparison != nil && !h.a.comparison.loading && h.a.comparison.result != nil
	})
}

func comparisonIndex(t *testing.T, h *harness, rel string) int {
	t.Helper()
	for i, entry := range h.a.comparison.result.Entries {
		if entry.Rel == rel {
			return i
		}
	}
	t.Fatalf("comparison row %q not found", rel)
	return -1
}

func TestDirectoryComparisonRequiresReviewedPlanAndExplicitApproval(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	if err := os.WriteFile(filepath.Join(env.dst, "a.txt"), []byte("old-a"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.exec(h.a.panels[1].Refresh())
	h.until("right panel refresh", func() bool { return !h.a.panels[1].Loading() })
	h.press("D")
	waitComparison(t, h)
	if !strings.Contains(h.view(), "Directory comparison") || h.a.comparison.result.Totals.LeftOnly == 0 {
		t.Fatalf("comparison view did not show one-sided entries:\n%s", h.view())
	}

	h.a.comparison.cursor = comparisonIndex(t, h, "a.txt")
	h.press("l")
	if h.a.comparison.choices["a.txt"] != dircompare.LeftToRight {
		t.Fatalf("left-to-right choice not stored: %+v", h.a.comparison.choices)
	}
	h.press("p")
	if !h.a.comparison.reviewing || h.a.comparison.plan.Totals().Files != 1 || h.a.comparison.plan.Totals().Overwrites != 1 {
		t.Fatalf("plan review missing: %+v", h.a.comparison.plan.Totals())
	}
	if !strings.Contains(h.view(), "Review sync plan") || !strings.Contains(h.view(), "a.txt") {
		t.Fatalf("review does not show exact operation:\n%s", h.view())
	}

	h.press("enter")
	if !h.dialogKind(dlgSyncConfirm) {
		t.Fatalf("expected final plan approval dialog, got %v", h.a.dialog)
	}
	if !strings.Contains(h.view(), "Approve 1 overwrite") || readFile(t, filepath.Join(env.dst, "a.txt")) != "old-a" {
		t.Fatal("the overwrite must be disclosed, and nothing may change before approval")
	}
	h.press("esc")
	if readFile(t, filepath.Join(env.dst, "a.txt")) != "old-a" || !h.a.comparison.reviewing {
		t.Fatal("canceling final approval must leave the reviewed plan unexecuted")
	}

	h.press("enter")
	h.press("enter")
	h.waitDialog(dlgConflict)
	if got := readFile(t, filepath.Join(env.dst, "a.txt")); got != "old-a" {
		t.Fatalf("destination changed before overwrite confirmation: %q", got)
	}
	h.press("o")
	h.idle()
	if got := readFile(t, filepath.Join(env.dst, "a.txt")); got != "new-a" {
		t.Fatalf("approved action copied %q", got)
	}
	if fileExists(filepath.Join(env.dst, "b.txt")) {
		t.Fatal("execution ran an action that was not included in the reviewed plan")
	}
	if h.a.tasks.latestActive() != nil {
		t.Fatal("sync task should be complete")
	}
}

func TestDirectoryPlanStopsIfSourceChangesAfterReview(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	h.press("D")
	waitComparison(t, h)
	h.a.comparison.cursor = comparisonIndex(t, h, "a.txt")
	h.press("l")
	h.press("p")
	h.press("enter") // final approval is open, but execution has not begun
	if err := os.WriteFile(filepath.Join(env.src, "a.txt"), []byte("changed after review"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.press("enter")
	h.idle()
	if fileExists(filepath.Join(env.dst, "a.txt")) {
		t.Fatal("stale source was copied after plan review")
	}
	if !strings.Contains(h.status(), "path changed since plan review") {
		t.Fatalf("status does not report stale plan: %q", h.status())
	}
}

func TestDirectoryComparisonRejectsArchivePanels(t *testing.T) {
	h, _, _ := zipApp(t)
	h.focus("t.zip")
	h.press("enter")
	h.until("archive panel", func() bool { return h.a.panels[0].InArchive() })
	h.press("D")
	if h.a.comparison != nil {
		t.Fatal("archive-backed panel must not start local directory comparison")
	}
	if !strings.Contains(h.status(), "local-only") {
		t.Fatalf("local-only refusal not shown: %q", h.status())
	}
}

func TestDirectoryComparisonRejectsRemotePanels(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	fakeRclone(t, `for a in "$@"; do
  case "$a" in
    listremotes) echo "drive:        drive"; exit 0 ;;
    lsjson) echo '[]'; exit 0 ;;
  esac
done
exit 0
`)
	h.openRemote("drive")
	if !h.a.ActivePanel().InRemote() {
		t.Fatal("test setup did not open the fake remote")
	}
	h.press("D")
	if h.a.comparison != nil {
		t.Fatal("remote-backed panel must not start local directory comparison")
	}
	if !strings.Contains(h.status(), "local-only") {
		t.Fatalf("local-only refusal not shown: %q", h.status())
	}
}

func TestDirectoryComparisonFitsSmallTerminal(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.press("D")
	waitComparison(t, h)
	h.send(tea.WindowSizeMsg{Width: 30, Height: 7})
	fitsScreen(t, h, "directory comparison")
	view := h.view()
	if !strings.Contains(view, "Directory comparison") || ansi.StringWidth(strings.Split(view, "\n")[3]) != 30 {
		t.Fatalf("small comparison view is not usable:\n%s", view)
	}
}
