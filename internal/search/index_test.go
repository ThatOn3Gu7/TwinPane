package search

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ThatOn3Gu7/TwinPane/internal/proc"
)

func indexTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"docs/reports", "src/ui", ".config/app", "Music"} {
		os.MkdirAll(filepath.Join(root, d), 0o755)
	}
	for _, f := range []string{"README.md", "docs/report.md", "docs/reports/q1.txt", "src/ui/view.go",
		"src/main.go", ".config/app/conf", ".hidden.txt", "Music/Report Song.mp3"} {
		os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644)
	}
	if runtime.GOOS != "windows" {
		os.Symlink("src", filepath.Join(root, "srclink"))
	}
	return root
}

func runIndex(t *testing.T, root string, opts IndexOptions) (es []Entry, last IndexUpdate) {
	t.Helper()
	ch := make(chan IndexUpdate, 4)
	go Index(context.Background(), root, opts, ch)
	timeout := time.After(20 * time.Second)
	for {
		select {
		case u := <-ch:
			es = append(es, u.Entries...)
			if u.Done {
				return es, u
			}
		case <-timeout:
			t.Fatal("index did not finish")
		}
	}
}

func rels(es []Entry) []string {
	var out []string
	for i := range es {
		e := &es[i]
		e.Resolve()
		s := filepath.ToSlash(e.Rel)
		if e.IsLink {
			s += "@"
		} else if e.IsDir {
			s += "/"
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// backends returns the external backends available on this machine plus
// the built-in one. fd is looked up in $PATH and in $TWINPANE_TEST_FD.
func backends(t *testing.T) []Backend {
	bs := DetectBackends(proc.LookPath)
	if p := os.Getenv("TWINPANE_TEST_FD"); p != "" {
		have := false
		for _, b := range bs {
			have = have || b.Name == BackendFd
		}
		if !have {
			bs = append([]Backend{{BackendFd, p}}, bs...)
		}
	}
	names := []string{}
	for _, b := range bs {
		names = append(names, b.Name)
	}
	t.Logf("backends: %v", names)
	return bs
}

func TestIndexBackendsAgree(t *testing.T) {
	root := indexTree(t)
	want := map[bool][]string{
		false: {"Music/", "Music/Report Song.mp3", "README.md", "docs/", "docs/report.md", "docs/reports/",
			"docs/reports/q1.txt", "src/", "src/main.go", "src/ui/", "src/ui/view.go", "srclink@"},
	}
	want[true] = append(append([]string{}, want[false]...), ".config/", ".config/app/", ".config/app/conf", ".hidden.txt")
	sort.Strings(want[true])
	if runtime.GOOS == "windows" {
		t.Skip("symlink fixture")
	}
	for _, b := range backends(t) {
		for _, hidden := range []bool{false, true} {
			es, last := runIndex(t, root, IndexOptions{ShowHidden: hidden, Backends: []Backend{b}})
			if last.Backend != b.Name || last.Note != "" || last.Err != nil || last.Count != len(es) {
				t.Errorf("%s hidden=%v: final update %+v", b.Name, hidden, last)
			}
			if got := rels(es); !reflect.DeepEqual(got, want[hidden]) {
				t.Errorf("%s hidden=%v:\n got %v\nwant %v", b.Name, hidden, got, want[hidden])
			}
			for _, e := range es {
				if !filepath.IsAbs(e.Path) || !strings.HasPrefix(e.Path, root) {
					t.Errorf("%s: bad path %q", b.Name, e.Path)
				}
			}
		}
	}
}

func TestIndexFallsBack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/false")
	}
	f, err := proc.LookPath("false")
	if err != nil {
		t.Skip(err)
	}
	root := indexTree(t)
	es, last := runIndex(t, root, IndexOptions{Backends: []Backend{{BackendFd, f}, {Name: BackendBuiltin}}})
	if last.Backend != BackendBuiltin || !strings.Contains(last.Note, "fd failed") || len(es) != 12 {
		t.Fatalf("fallback: %d entries, %+v", len(es), last)
	}
	// a tool that cannot be started at all
	_, last = runIndex(t, root, IndexOptions{Backends: []Backend{{BackendFind, "/nonexistent/find"}, {Name: BackendBuiltin}}})
	if last.Backend != BackendBuiltin || !strings.Contains(last.Note, "find failed") {
		t.Fatalf("missing tool: %+v", last)
	}
}

func TestIndexSymlinkRootAndErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	root := indexTree(t)
	link := filepath.Join(t.TempDir(), "shared") // like Termux ~/storage/shared
	os.Symlink(root, link)
	for _, b := range backends(t) {
		es, last := runIndex(t, link, IndexOptions{Backends: []Backend{b}})
		if len(es) != 12 || last.Err != nil {
			t.Errorf("%s: symlinked root gave %d entries (%+v)", b.Name, len(es), last)
		}
		for _, e := range es {
			if !strings.HasPrefix(e.Path, link+string(filepath.Separator)) {
				t.Errorf("%s: path %q not below the root as given", b.Name, e.Path)
				break
			}
		}
	}
	_, last := runIndex(t, filepath.Join(root, "missing"), IndexOptions{})
	if last.Err == nil || !last.Done {
		t.Fatalf("missing root: %+v", last)
	}
	if os.Getuid() != 0 {
		locked := filepath.Join(root, "docs", "reports")
		os.Chmod(locked, 0)
		defer os.Chmod(locked, 0o755)
		for _, b := range backends(t) {
			es, last := runIndex(t, root, IndexOptions{Backends: []Backend{b}})
			if last.Errors == 0 || len(es) != 11 || last.Backend != b.Name {
				t.Errorf("%s: unreadable folder: %d entries, %+v", b.Name, len(es), last)
			}
		}
	}
}

func TestIndexLimitAndCancel(t *testing.T) {
	root := indexTree(t)
	for _, b := range backends(t) {
		es, last := runIndex(t, root, IndexOptions{MaxEntries: 5, Backends: []Backend{b}})
		if !last.Truncated || len(es) != 5 {
			t.Errorf("%s: limit: %d entries, %+v", b.Name, len(es), last)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ch := make(chan IndexUpdate, 1)
	done := make(chan struct{})
	go func() { Index(ctx, root, IndexOptions{}, ch); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("canceled index did not return")
	}
}

func TestFilterRank(t *testing.T) {
	root := "/r"
	var es []Entry
	for _, p := range []string{"docs/reports/q1.txt", "README.md", "Music/Report Song.mp3", "docs/report.md", "docs/reports", "src/view.go"} {
		e, _ := NewEntry(root, filepath.Join(root, p))
		es = append(es, e)
	}
	SortEntries(es)
	got := func(q string) []string {
		var query Query
		if q != "" {
			var err error
			if query, err = ParseQuery(q); err != nil {
				t.Fatal(err)
			}
		}
		m := Filter(es, query, 0, nil)
		Rank(es, query, m)
		var out []string
		for _, i := range m {
			out = append(out, filepath.ToSlash(es[i].Rel))
		}
		return out
	}
	if g := got(""); len(g) != 6 || g[0] != "README.md" {
		t.Fatalf("empty query: %v", g)
	}
	// names only (no / in the query): prefix matches, shallower first, then
	// by path; reports/q1.txt does not match (its name has no "report")
	if g := got("report"); !reflect.DeepEqual(g, []string{"docs/report.md", "docs/reports", "Music/Report Song.mp3"}) {
		t.Fatalf("report: %v", g)
	}
	if g := got("song"); !reflect.DeepEqual(g, []string{"Music/Report Song.mp3"}) {
		t.Fatalf("song (substring): %v", g)
	}
	// exact name before prefix before substring
	es2 := []Entry{}
	for _, p := range []string{"a/xmain.go", "main", "b/main.go"} {
		e, _ := NewEntry("/r", "/r/"+p)
		es2 = append(es2, e)
	}
	q, _ := ParseQuery("main")
	m := Filter(es2, q, 0, nil)
	Rank(es2, q, m)
	var order []string
	for _, i := range m {
		order = append(order, es2[i].Rel)
	}
	if !reflect.DeepEqual(order, []string{"main", "b/main.go", "a/xmain.go"}) {
		t.Fatalf("rank: %v", order)
	}
	if g := got("reports/"); !reflect.DeepEqual(g, []string{"docs/reports", "docs/reports/q1.txt"}) {
		t.Fatalf("path query: %v", g)
	}
	if g := got("*.md"); !reflect.DeepEqual(g, []string{"README.md", "docs/report.md"}) {
		t.Fatalf("*.md: %v", g)
	}
	if g := got("reports/*.txt"); !reflect.DeepEqual(g, []string{"docs/reports/q1.txt"}) || len(got("docs/*.txt")) != 0 {
		t.Fatalf("reports/*.txt: %v", g)
	}
}
