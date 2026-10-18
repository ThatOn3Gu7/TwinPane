package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if !strings.Contains(view, "D:scan") || !strings.Contains(view, "l/r") || !strings.Contains(view, "plan") || ansi.StringWidth(strings.Split(view, "\n")[3]) != 30 {
		t.Fatalf("small comparison view is not usable or lacks workflow hints:\n%s", view)
	}
}

func visibleHas(c *comparisonView, rel string) bool {
	for _, entry := range visibleComparisonEntries(c) {
		if entry.Rel == rel {
			return true
		}
	}
	return false
}

func resultHas(c *comparisonView, rel string) bool {
	if c == nil || c.result == nil {
		return false
	}
	for _, entry := range c.result.Entries {
		if entry.Rel == rel {
			return true
		}
	}
	return false
}

func TestDirectoryComparisonDifferencesFilterHidesMatchesButKeepsProposals(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	when := time.Date(2026, 6, 7, 8, 9, 10, 0, time.UTC)
	for _, path := range []string{filepath.Join(env.src, "match.txt"), filepath.Join(env.dst, "match.txt")} {
		if err := os.WriteFile(path, []byte("same bytes"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
	}

	h.press("D")
	waitComparison(t, h)
	if !visibleHas(h.a.comparison, "match.txt") {
		t.Fatal("unfiltered comparison should show matching entries")
	}
	allRows := h.a.comparison.result.Totals.Entries
	h.press("f")
	if !h.a.comparison.differencesOnly || visibleHas(h.a.comparison, "match.txt") {
		t.Fatal("differences-only filter should hide a matching metadata row")
	}
	if !visibleHas(h.a.comparison, "a.txt") || h.a.comparison.result.Totals.Entries != allRows {
		t.Fatal("filter should preserve actionable rows and full comparison totals")
	}
	view := h.view()
	if !strings.Contains(view, "f: diff") || !strings.Contains(view, "show all") {
		t.Fatalf("active filter or its toggle hint is not visible:\n%s", view)
	}

	h.press("f") // restore all rows
	h.a.comparison.cursor = comparisonIndex(t, h, "match.txt")
	h.press("l") // a matching file may still be explicitly proposed for copying
	h.press("f")
	if !visibleHas(h.a.comparison, "match.txt") {
		t.Fatal("differences-only mode must keep an explicitly proposed matching row visible")
	}
}

func TestDirectoryComparisonGitToggleRescansAndClearsProposals(t *testing.T) {
	h, env := newOpsApp(t, nil, nil)
	for _, path := range []string{
		filepath.Join(env.src, ".git", "config"),
		filepath.Join(env.src, "nested", ".git", "HEAD"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("metadata"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	h.press("D")
	waitComparison(t, h)
	if resultHas(h.a.comparison, ".git") || resultHas(h.a.comparison, ".git/config") || resultHas(h.a.comparison, "nested/.git/HEAD") {
		t.Fatal("Git directories should be excluded by default")
	}
	h.a.comparison.cursor = comparisonIndex(t, h, "a.txt")
	h.press("l")
	if len(h.a.comparison.choices) != 1 {
		t.Fatal("expected a proposal before toggling Git metadata")
	}
	h.press("g")
	waitComparison(t, h)
	if !h.a.comparison.includeGitDirectories {
		t.Fatal("g should enable Git directory scanning")
	}
	if len(h.a.comparison.choices) != 0 {
		t.Fatal("changing comparison scope must clear prior proposals")
	}
	for _, rel := range []string{".git", ".git/config", "nested/.git", "nested/.git/HEAD"} {
		if !resultHas(h.a.comparison, rel) {
			t.Errorf("including Git metadata omitted %q", rel)
		}
	}
	if !strings.Contains(h.view(), "g: .git included") {
		t.Fatalf("active Git scope is not visible:\n%s", h.view())
	}
}

func TestDirectoryComparisonHelpExplainsWorkflowAndStatuses(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	h.press("D")
	waitComparison(t, h)
	h.press("?")
	if !h.a.showHelp || h.a.comparison == nil {
		t.Fatal("? should open comparison help without closing the comparison")
	}
	h.press("/")
	h.typeText("SAME META", false)
	if !strings.Contains(h.view(), "contents may differ") {
		t.Fatalf("help does not explain metadata-only matches:\n%s", h.view())
	}
	h.press("esc")
	h.press("esc")
	if h.a.showHelp || h.a.comparison == nil {
		t.Fatal("closing help should return to the comparison view")
	}
}
