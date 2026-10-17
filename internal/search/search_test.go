package search

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestParseAndMatch(t *testing.T) {
	cases := []struct {
		q, rel string
		want   bool
	}{
		{"rep", "docs/Report.PDF", true},
		{"REP", "docs/report.pdf", true},
		{"docs", "docs/report.pdf", false}, // names only without a slash
		{"port.p", "a/report.pdf", true},
		{"*.go", "src/main.go", true},
		{"*.go", "src/main.go.bak", false}, // wildcards match the whole name
		{"m?in.*", "src/MAIN.GO", true},
		{"[ab]*", "x/beta", true},
		{"src/", "src/main.go", true}, // slash: match the relative path
		{"c/ma", "src/main.go", true},
		{"docs/*.md", "a/docs/x.md", true},
		{"docs/*.md", "docs/sub/x.md", false},
		{"ü", "x/Über", true},
	}
	for _, c := range cases {
		q, err := ParseQuery(c.q)
		if err != nil {
			t.Fatalf("%q: %v", c.q, err)
		}
		if got := q.Match(c.rel, filepath.Base(c.rel)); got != c.want {
			t.Errorf("%q vs %q = %v", c.q, c.rel, got)
		}
	}
	for _, bad := range []string{"", "   ", "[", "//"} {
		if _, err := ParseQuery(bad); err == nil {
			t.Errorf("query %q accepted", bad)
		}
	}
}

func tree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, p := range []string{"a/b/c", ".hidden/inner", "docs"} {
		os.MkdirAll(filepath.Join(root, p), 0o755)
	}
	for _, f := range []string{"a/report.txt", "a/b/Report-2.md", "a/b/c/deep_report", ".hidden/inner/report.secret",
		".report.dot", "docs/readme.md"} {
		os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644)
	}
	return root
}

func collect(t *testing.T, root, q string, opts Options) ([]string, Update) {
	t.Helper()
	query, err := ParseQuery(q)
	if err != nil {
		t.Fatal(err)
	}
	ch := make(chan Update)
	go func() { Run(context.Background(), root, query, opts, ch); close(ch) }()
	var rels []string
	var last Update
	for u := range ch {
		for _, r := range u.Results {
			rels = append(rels, filepath.ToSlash(r.Rel))
		}
		last = u
	}
	sort.Strings(rels)
	return rels, last
}

func TestRunFindsAndRespectsHidden(t *testing.T) {
	root := tree(t)
	got, last := collect(t, root, "report", Options{})
	if strings.Join(got, ",") != "a/b/Report-2.md,a/b/c/deep_report,a/report.txt" || !last.Done || last.Errors != 0 {
		t.Fatalf("hidden excluded: %v %+v", got, last)
	}
	got, _ = collect(t, root, "report", Options{ShowHidden: true})
	if len(got) != 5 {
		t.Fatalf("hidden included: %v", got)
	}
	got, _ = collect(t, root, "b/", Options{})
	if strings.Join(got, ",") != "a/b,a/b/Report-2.md,a/b/c,a/b/c/deep_report" {
		t.Fatalf("path match: %v", got)
	}
}

func TestRunDoesNotFollowSymlinkLoops(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	root := tree(t)
	os.Symlink(root, filepath.Join(root, "a", "loop")) // a/loop -> root
	done := make(chan []string)
	go func() { got, _ := collect(t, root, "report", Options{}); done <- got }()
	select {
	case got := <-done:
		if len(got) != 3 {
			t.Fatalf("link was followed: %v", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("search did not finish: symlink loop followed")
	}
	got, _ := collect(t, root, "loop", Options{})
	if len(got) != 1 {
		t.Fatalf("link itself should be reported: %v", got)
	}
}

func TestRunCountsUnreadableDirs(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX permissions and a non-root user")
	}
	root := tree(t)
	locked := filepath.Join(root, "a", "b")
	os.Chmod(locked, 0)
	defer os.Chmod(locked, 0o755)
	got, last := collect(t, root, "report", Options{})
	if strings.Join(got, ",") != "a/report.txt" || last.Errors != 1 || !strings.Contains(last.LastError, "permission denied") || !last.Done || last.Err != nil {
		t.Fatalf("%v %+v", got, last)
	}
	// An unreadable root is an error.
	os.Chmod(root, 0)
	defer os.Chmod(root, 0o755)
	_, last = collect(t, root, "x", Options{})
	if last.Err == nil || !last.Done {
		t.Fatalf("unreadable root: %+v", last)
	}
}

func TestRunTruncatesAndBatches(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 250; i++ {
		os.WriteFile(filepath.Join(root, fmt.Sprintf("f%03d.txt", i)), nil, 0o644)
	}
	got, last := collect(t, root, "f", Options{MaxResults: 100})
	if len(got) != 100 || !last.Truncated || !last.Done {
		t.Fatalf("got %d, %+v", len(got), last)
	}
}

func TestRunCancel(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 50; i++ {
		d := filepath.Join(root, fmt.Sprintf("d%02d", i))
		os.Mkdir(d, 0o755)
		for j := 0; j < 50; j++ {
			os.WriteFile(filepath.Join(d, fmt.Sprintf("x%02d", j)), nil, 0o644)
		}
	}
	q, _ := ParseQuery("x")
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan Update) // unbuffered and nobody reads after the first update
	finished := make(chan struct{})
	go func() { Run(ctx, root, q, Options{Interval: time.Nanosecond}, ch); close(finished) }()
	first := <-ch
	if first.Done {
		t.Fatal("search finished too fast for the test")
	}
	cancel()
	select {
	case <-finished: // Run must return even though nobody reads the channel
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}
