package dircompare

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
)

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setMtime(t *testing.T, path string, when time.Time) {
	t.Helper()
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
}

func scan(t *testing.T, left, right string, opts Options) Result {
	t.Helper()
	result, err := Compare(context.Background(), left, right, opts)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func entryAt(t *testing.T, result Result, rel string) Entry {
	t.Helper()
	for _, entry := range result.Entries {
		if entry.Rel == rel {
			return entry
		}
	}
	t.Fatalf("comparison has no row %q", rel)
	return Entry{}
}

func TestCompareNestedEntriesAndMetadata(t *testing.T) {
	left, right := t.TempDir(), t.TempDir()
	when := time.Date(2026, 4, 5, 6, 7, 8, 0, time.UTC)
	writeFile(t, filepath.Join(left, "same.txt"), "abc")
	writeFile(t, filepath.Join(right, "same.txt"), "abc")
	setMtime(t, filepath.Join(left, "same.txt"), when)
	setMtime(t, filepath.Join(right, "same.txt"), when)
	writeFile(t, filepath.Join(left, "docs", "nested", "left.txt"), "left")
	writeFile(t, filepath.Join(right, "right.txt"), "right")
	writeFile(t, filepath.Join(left, "size.txt"), "x")
	writeFile(t, filepath.Join(right, "size.txt"), "longer")
	writeFile(t, filepath.Join(left, "kind"), "file")
	if err := os.Mkdir(filepath.Join(right, "kind"), 0o755); err != nil {
		t.Fatal(err)
	}

	result := scan(t, left, right, Options{})
	if !result.Complete || !result.CanSync {
		t.Fatalf("complete=%v canSync=%v errors=%v", result.Complete, result.CanSync, result.Errors)
	}
	for i := 1; i < len(result.Entries); i++ {
		if result.Entries[i-1].Rel > result.Entries[i].Rel {
			t.Fatalf("entries are not sorted: %q before %q", result.Entries[i-1].Rel, result.Entries[i].Rel)
		}
	}
	checks := map[string]Status{
		"docs":                 StatusLeftOnly,
		"docs/nested":          StatusLeftOnly,
		"docs/nested/left.txt": StatusLeftOnly,
		"right.txt":            StatusRightOnly,
		"same.txt":             StatusMetadataEqual,
		"size.txt":             StatusMetadataDifferent,
		"kind":                 StatusTypeConflict,
	}
	for rel, want := range checks {
		if got := entryAt(t, result, rel).Status; got != want {
			t.Errorf("%s status = %s, want %s", rel, got, want)
		}
	}
	if result.Totals.LeftOnly != 3 || result.Totals.RightOnly != 1 || result.Totals.MetadataEqual != 1 || result.Totals.MetadataDifferent != 1 || result.Totals.TypeConflicts != 1 {
		t.Fatalf("unexpected totals: %+v", result.Totals)
	}
}

func TestCompareExcludesGitDirectoriesByDefaultIncludingNestedRepositories(t *testing.T) {
	left, right := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(left, ".git", "config"), "root repository metadata")
	writeFile(t, filepath.Join(left, "nested", ".git", "HEAD"), "nested repository metadata")
	writeFile(t, filepath.Join(right, "nested", ".git", "HEAD"), "other nested metadata")
	writeFile(t, filepath.Join(left, ".gitignore"), "build/\n")
	writeFile(t, filepath.Join(left, ".github", "workflows", "ci.yml"), "name: test\n")

	result := scan(t, left, right, Options{})
	for _, entry := range result.Entries {
		for _, component := range strings.Split(entry.Rel, "/") {
			if component == ".git" {
				t.Fatalf("default scan included Git internals: %q", entry.Rel)
			}
		}
	}
	for _, rel := range []string{".gitignore", ".github", ".github/workflows", ".github/workflows/ci.yml"} {
		entryAt(t, result, rel)
	}
}

func TestCompareCanIncludeRootAndNestedGitDirectories(t *testing.T) {
	left, right := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(left, ".git", "config"), "root repository metadata")
	writeFile(t, filepath.Join(left, "nested", ".git", "HEAD"), "nested repository metadata")

	result := scan(t, left, right, Options{IncludeGitDirectories: true})
	for _, rel := range []string{".git", ".git/config", "nested", "nested/.git", "nested/.git/HEAD"} {
		entryAt(t, result, rel)
	}
}

func TestOptionalContentComparisonFindsSameMetadataDifferentBytes(t *testing.T) {
	left, right := t.TempDir(), t.TempDir()
	when := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	for name, pair := range map[string][2]string{
		"changed": {"aaaa", "bbbb"},
		"equal":   {"same", "same"},
	} {
		writeFile(t, filepath.Join(left, name), pair[0])
		writeFile(t, filepath.Join(right, name), pair[1])
		setMtime(t, filepath.Join(left, name), when)
		setMtime(t, filepath.Join(right, name), when)
	}
	if runtime.GOOS != "windows" {
		writeFile(t, filepath.Join(left, "permissions"), "same")
		writeFile(t, filepath.Join(right, "permissions"), "same")
		if err := os.Chmod(filepath.Join(left, "permissions"), 0o600); err != nil {
			t.Fatal(err)
		}
		setMtime(t, filepath.Join(left, "permissions"), when)
		setMtime(t, filepath.Join(right, "permissions"), when)
	}
	metadata := scan(t, left, right, Options{})
	if got := entryAt(t, metadata, "changed").Status; got != StatusMetadataEqual {
		t.Fatalf("metadata-only status = %s, want same metadata", got)
	}
	hashed := scan(t, left, right, Options{HashMatchingFiles: true})
	if got := entryAt(t, hashed, "changed").Status; got != StatusContentDifferent {
		t.Fatalf("hashed status = %s, want content differs", got)
	}
	if got := entryAt(t, hashed, "equal").Status; got != StatusIdentical {
		t.Fatalf("identical status = %s, want identical", got)
	}
	wantSameContent := 0
	if runtime.GOOS != "windows" {
		wantSameContent = 1
		if got := entryAt(t, hashed, "permissions").Status; got != StatusSameContent {
			t.Fatalf("permissions-only difference status = %s, want same content", got)
		}
	}
	if !hashed.ContentChecked || hashed.Totals.ContentDifferent != 1 || hashed.Totals.Identical != 1 || hashed.Totals.SameContent != wantSameContent || hashed.Totals.MetadataDifferent != wantSameContent {
		t.Fatalf("content totals = %+v (checked=%v)", hashed.Totals, hashed.ContentChecked)
	}
}

func TestCompareDoesNotFollowSymlinkDirectories(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("creating directory symlinks requires elevated privileges on Windows")
	}
	left, right, outside := t.TempDir(), t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(outside, "secret.txt"), "outside")
	if err := os.Symlink(outside, filepath.Join(left, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(right, "linked")); err != nil {
		t.Fatal(err)
	}
	result := scan(t, left, right, Options{})
	link := entryAt(t, result, "linked")
	if link.Left.Snapshot == nil || link.Left.Snapshot.Kind != KindSymlink || link.Left.Snapshot.LinkTarget != outside || link.Right.Snapshot == nil || link.Right.Snapshot.LinkTarget != outside {
		t.Fatalf("link comparison = %+v", link)
	}
	for _, entry := range result.Entries {
		if strings.HasPrefix(entry.Rel, "linked/") {
			t.Fatalf("followed symlink and listed %q", entry.Rel)
		}
	}
}

func TestCompareErrorsAreUnknownNotOneSided(t *testing.T) {
	left, right := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(left, "gone.txt"), "left")
	writeFile(t, filepath.Join(right, "gone.txt"), "right")
	oldLstat := lstatPath
	lstatPath = func(path string) (fs.FileInfo, error) {
		if filepath.Clean(path) == filepath.Join(right, "gone.txt") {
			return nil, os.ErrNotExist
		}
		return oldLstat(path)
	}
	t.Cleanup(func() { lstatPath = oldLstat })

	result := scan(t, left, right, Options{})
	if result.Complete {
		t.Fatal("disappearing entry must make the scan incomplete")
	}
	if got := entryAt(t, result, "gone.txt").Status; got != StatusError {
		t.Fatalf("vanished file status = %s, want read error", got)
	}
	if _, err := BuildPlan(result, []Choice{{Rel: "gone.txt", Direction: LeftToRight}}); err == nil {
		t.Fatal("incomplete comparison must not produce a plan")
	}
}

func TestUnreadableSubtreeDoesNotBecomeAOneSidedCopy(t *testing.T) {
	left, right := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(left, "blocked", "nested.txt"), "left")
	writeFile(t, filepath.Join(right, "blocked", "nested.txt"), "right")
	blocked := filepath.Join(left, "blocked")
	oldReadDir := readDir
	readDir = func(f *os.File) ([]os.DirEntry, error) {
		if filepath.Clean(f.Name()) == blocked {
			return nil, fs.ErrPermission
		}
		return oldReadDir(f)
	}
	t.Cleanup(func() { readDir = oldReadDir })

	result := scan(t, left, right, Options{})
	if result.Complete {
		t.Fatal("unreadable directory must make the scan incomplete")
	}
	if got := entryAt(t, result, "blocked/nested.txt").Status; got != StatusError {
		t.Fatalf("unreadable subtree child status = %s, want read error", got)
	}
}

func TestCompareRejectsSameOrOverlappingRootsForSync(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0o755); err != nil {
		t.Fatal(err)
	}
	result := scan(t, root, child, Options{})
	if result.CanSync {
		t.Fatal("nested roots must remain read-only")
	}
	if _, err := BuildPlan(result, []Choice{{Rel: "child", Direction: LeftToRight}}); err == nil {
		t.Fatal("overlapping roots must not produce a sync plan")
	}
}

func TestBuildAndExecuteNestedDirectoryPlan(t *testing.T) {
	left, right := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(left, "tree", "sub", "file.txt"), "contents")
	if err := os.Mkdir(filepath.Join(left, "tree", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	result := scan(t, left, right, Options{})
	plan, err := BuildPlan(result, []Choice{{Rel: "tree", Direction: LeftToRight}})
	if err != nil {
		t.Fatal(err)
	}
	actions := plan.Actions()
	if len(actions) != 4 || actions[0].Kind != ActionCreateDirectory || actions[0].Rel != "tree" || actions[1].Rel != "tree/empty" || actions[2].Rel != "tree/sub" || actions[3].Kind != ActionCopyEntry {
		t.Fatalf("unexpected expanded plan: %+v", actions)
	}
	if got := plan.Totals(); got.Directories != 3 || got.Files != 1 || got.Overwrites != 0 {
		t.Fatalf("plan totals = %+v", got)
	}
	// The public action accessor must not expose the executor's backing slice.
	actions[0].Rel = "../../outside"
	if plan.Actions()[0].Rel != "tree" {
		t.Fatal("caller mutation changed the reviewed plan")
	}

	execution := Execute(context.Background(), plan, ExecutionHooks{Operation: ops.Hooks{
		Conflict: func(ops.Conflict) ops.Decision { return ops.Decision{Action: ops.Overwrite} },
	}})
	if execution.Canceled || execution.Changed != nil || len(execution.Errors) != 0 || execution.CopiedFiles != 1 || execution.CreatedDirs != 3 {
		t.Fatalf("execution = %+v", execution)
	}
	if got, err := os.ReadFile(filepath.Join(right, "tree", "sub", "file.txt")); err != nil || string(got) != "contents" {
		t.Fatalf("copied nested file = %q, %v", got, err)
	}
	if mode := fileMode(t, filepath.Join(right, "tree", "empty")); mode&fs.ModeDir == 0 {
		t.Fatalf("empty directory was not copied: mode %v", mode)
	}
}

func TestExecutionStopsWhenReviewedSourceChanges(t *testing.T) {
	left, right := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(left, "a.txt"), "before")
	writeFile(t, filepath.Join(left, "b.txt"), "untouched")
	result := scan(t, left, right, Options{})
	plan, err := BuildPlan(result, []Choice{
		{Rel: "a.txt", Direction: LeftToRight},
		{Rel: "b.txt", Direction: LeftToRight},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(left, "a.txt"), "changed after review")
	execution := Execute(context.Background(), plan, ExecutionHooks{})
	if execution.Changed == nil || !errors.Is(execution.Changed, ErrChangedSinceReview) {
		t.Fatalf("changed result = %+v, want stop for stale plan", execution)
	}
	if _, err := os.Stat(filepath.Join(right, "a.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale source was copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(right, "b.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("later plan action ran after failure: %v", err)
	}
}

func TestExecutionStopsWhenOverwriteTargetChanges(t *testing.T) {
	left, right := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(left, "same.txt"), "incoming")
	writeFile(t, filepath.Join(right, "same.txt"), "reviewed destination")
	result := scan(t, left, right, Options{})
	plan, err := BuildPlan(result, []Choice{{Rel: "same.txt", Direction: LeftToRight}})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Totals().Overwrites != 1 {
		t.Fatalf("overwrite count = %d, want 1", plan.Totals().Overwrites)
	}
	writeFile(t, filepath.Join(right, "same.txt"), "changed destination")
	execution := Execute(context.Background(), plan, ExecutionHooks{Operation: ops.Hooks{
		Conflict: func(ops.Conflict) ops.Decision { return ops.Decision{Action: ops.Overwrite} },
	}})
	if execution.Changed == nil || !errors.Is(execution.Changed, ErrChangedSinceReview) {
		t.Fatalf("changed result = %+v, want stale destination", execution)
	}
	if got, err := os.ReadFile(filepath.Join(right, "same.txt")); err != nil || string(got) != "changed destination" {
		t.Fatalf("changed destination was overwritten: %q, %v", got, err)
	}
}

func TestExecutionCancellationDoesNotRunPlan(t *testing.T) {
	left, right := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(left, "a.txt"), "source")
	result := scan(t, left, right, Options{})
	plan, err := BuildPlan(result, []Choice{{Rel: "a.txt", Direction: LeftToRight}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	execution := Execute(ctx, plan, ExecutionHooks{})
	if !execution.Canceled || len(execution.Completed) != 0 {
		t.Fatalf("canceled execution = %+v", execution)
	}
	if _, err := os.Stat(filepath.Join(right, "a.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("canceled plan changed destination: %v", err)
	}
}

func TestPlanCopyPreservesSymlinkAsLink(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("creating symlinks requires elevated privileges on Windows")
	}
	left, right, outside := t.TempDir(), t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(outside, "private.txt"), "not copied")
	if err := os.Symlink(filepath.Join(outside, "private.txt"), filepath.Join(left, "link")); err != nil {
		t.Fatal(err)
	}
	result := scan(t, left, right, Options{})
	plan, err := BuildPlan(result, []Choice{{Rel: "link", Direction: LeftToRight}})
	if err != nil {
		t.Fatal(err)
	}
	execution := Execute(context.Background(), plan, ExecutionHooks{Operation: ops.Hooks{
		Conflict: func(ops.Conflict) ops.Decision { return ops.Decision{Action: ops.Overwrite} },
	}})
	if len(execution.Errors) != 0 || execution.Changed != nil || execution.Canceled {
		t.Fatalf("symlink copy execution = %+v", execution)
	}
	got, err := os.Readlink(filepath.Join(right, "link"))
	if err != nil || got != filepath.Join(outside, "private.txt") {
		t.Fatalf("copied link target = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(right, "link")); err != nil {
		t.Fatalf("copied link did not resolve to the original target: %v", err)
	}
}

func fileMode(t *testing.T, path string) fs.FileMode {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode()
}
