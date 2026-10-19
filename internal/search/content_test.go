package search

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func runContentForTest(ctx context.Context, root, query string, opt ContentOptions) []ContentUpdate {
	out := make(chan ContentUpdate, 4)
	done := make(chan struct{})
	go func() {
		RunContent(ctx, root, query, opt, out)
		close(out)
		close(done)
	}()
	var updates []ContentUpdate
	for u := range out {
		updates = append(updates, u)
	}
	<-done
	return updates
}

func lastContentUpdate(t *testing.T, updates []ContentUpdate) ContentUpdate {
	t.Helper()
	if len(updates) == 0 || !updates[len(updates)-1].Done {
		t.Fatalf("missing final content-search update: %+v", updates)
	}
	return updates[len(updates)-1]
}

func allContentResults(updates []ContentUpdate) []ContentResult {
	var results []ContentResult
	for _, u := range updates {
		results = append(results, u.Results...)
	}
	return results
}

func TestRunContentSkipsExactGitDirectoriesOnly(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"visible.txt":                      "needle outside git\n",
		".git/config":                      "needle in repository metadata\n",
		"vendor/.git/objects/pack":         "needle in nested metadata\n",
		".gitignore":                       "needle in ignore file\n",
		".github/workflows/ci.yml":         "needle in workflow\n",
		"some.git/lookalike.txt":           "needle in lookalike directory\n",
		"nested/.github/metadata/keep.txt": "needle in nested lookalike\n",
	}
	for name, contents := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	updates := runContentForTest(context.Background(), root, "needle", ContentOptions{Interval: time.Nanosecond})
	final := lastContentUpdate(t, updates)
	if final.Err != nil || final.Canceled {
		t.Fatalf("search failed: %+v", final)
	}
	got := map[string]bool{}
	for _, result := range allContentResults(updates) {
		got[filepath.ToSlash(result.Rel)] = true
		if len(result.Matches) == 0 || result.Matches[0].Line != 1 || !strings.Contains(result.Matches[0].Text, "needle") {
			t.Errorf("bad match excerpt: %+v", result)
		}
	}
	for _, excluded := range []string{".git/config", "vendor/.git/objects/pack"} {
		if got[excluded] {
			t.Errorf("exact .git directory was searched: %s", excluded)
		}
	}
	for _, included := range []string{"visible.txt", ".gitignore", ".github/workflows/ci.yml", "some.git/lookalike.txt", "nested/.github/metadata/keep.txt"} {
		if !got[included] {
			t.Errorf("lookalike or ordinary path was incorrectly excluded: %s", included)
		}
	}
}

func TestRunContentSkipsBinaryAndBoundsLargeFiles(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "binary.dat")
	if err := os.WriteFile(binary, []byte("needle\x00binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	large := filepath.Join(root, "large.txt")
	if err := os.WriteFile(large, []byte("needle at the beginning\n"+strings.Repeat("x", 256)), 0o644); err != nil {
		t.Fatal(err)
	}
	updates := runContentForTest(context.Background(), root, "needle", ContentOptions{
		MaxBytesPerFile: 32, MaxBytesTotal: 128, Interval: time.Nanosecond,
	})
	final := lastContentUpdate(t, updates)
	if final.Binary != 1 || final.TruncatedFiles != 1 || final.BytesRead > 128 || final.Err != nil {
		t.Fatalf("binary/large accounting: %+v", final)
	}
	results := allContentResults(updates)
	if len(results) != 1 || filepath.Base(results[0].Path) != "large.txt" || len(results[0].Matches) != 1 {
		t.Fatalf("results: %+v", results)
	}
	if results[0].Matches[0].Line != 1 || !strings.Contains(results[0].Matches[0].Text, "needle") {
		t.Fatalf("excerpt: %+v", results[0].Matches[0])
	}
}

func TestRunContentResultLimit(t *testing.T) {
	root := t.TempDir()
	for i := range 4 {
		path := filepath.Join(root, fmt.Sprintf("file-%d.txt", i))
		if err := os.WriteFile(path, []byte("needle\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	updates := runContentForTest(context.Background(), root, "needle", ContentOptions{MaxResults: 1})
	final := lastContentUpdate(t, updates)
	if !final.Truncated || len(allContentResults(updates)) != 1 {
		t.Fatalf("result cap: %+v; results=%d", final, len(allContentResults(updates)))
	}
}

func TestRunContentBoundsVisitedEntries(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "a-dir"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a-dir/inside.txt", "z.txt"} {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.WriteFile(path, []byte("needle\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	updates := runContentForTest(context.Background(), root, "needle", ContentOptions{MaxEntries: 1})
	final := lastContentUpdate(t, updates)
	if !final.Truncated || final.ScannedDirs+final.ScannedFiles > 1 {
		t.Fatalf("path-entry cap was not enforced: %+v", final)
	}
}

func TestRunContentCancellationAndRootErrors(t *testing.T) {
	root := t.TempDir()
	for i := range 64 {
		name := filepath.Join(root, fmt.Sprintf("file-%03d.txt", i))
		if err := os.WriteFile(name, []byte("needle\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	out := make(chan ContentUpdate, 1)
	done := make(chan struct{})
	go func() {
		RunContent(ctx, root, "needle", ContentOptions{Interval: time.Hour}, out)
		close(out)
		close(done)
	}()
	first := <-out // the 32-result batch guarantees a progress event
	if first.Done {
		t.Fatalf("search completed before cancellation: %+v", first)
	}
	cancel()
	var final *ContentUpdate
	for u := range out {
		if u.Done {
			copy := u
			final = &copy
		}
	}
	<-done
	if final == nil || (!final.Canceled && final.ScannedFiles < 64) {
		t.Fatalf("scan did not stop or complete cleanly after cancellation: %+v (first update %+v)", final, first)
	}

	missing := filepath.Join(t.TempDir(), "not-a-directory")
	updates := runContentForTest(context.Background(), missing, "needle", ContentOptions{})
	finalUpdate := lastContentUpdate(t, updates)
	if finalUpdate.Err == nil {
		t.Fatalf("missing root was not reported: %+v", finalUpdate)
	}
}

func TestRunContentCancellationBeforeStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	updates := runContentForTest(ctx, t.TempDir(), "needle", ContentOptions{})
	final := lastContentUpdate(t, updates)
	if !final.Canceled {
		t.Fatalf("pre-cancelled search: %+v", final)
	}
}

func TestRunContentUnreadableFileIsReported(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not enforced consistently on Windows")
	}
	root := t.TempDir()
	path := filepath.Join(root, "private.txt")
	if err := os.WriteFile(path, []byte("needle"), 0o000); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(path, 0o600)
	updates := runContentForTest(context.Background(), root, "needle", ContentOptions{})
	final := lastContentUpdate(t, updates)
	if final.Errors == 0 && os.Geteuid() == 0 {
		t.Skip("the test process is privileged and can read mode-000 files")
	}
	if final.Errors == 0 || final.LastError == "" {
		t.Fatalf("unreadable file was not reported: %+v", final)
	}
}
