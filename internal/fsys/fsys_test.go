package fsys

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func mkTree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, d := range []string{"zdir", "adir"} {
		if err := os.Mkdir(filepath.Join(dir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]int{"b.txt": 10, "A.txt": 300, ".hidden": 1, "c.go": 5}
	for name, n := range files {
		if err := os.WriteFile(filepath.Join(dir, name), make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func names(es []Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Name)
	}
	return out
}

func TestListOrderAndCounts(t *testing.T) {
	dir := mkTree(t)
	l, err := OSLister{}.List(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(names(l.Entries), ",")
	want := "..,adir,zdir,A.txt,b.txt,c.go"
	if got != want {
		t.Fatalf("order = %s, want %s", got, want)
	}
	if l.Dirs != 2 || l.Files != 3 || l.Hidden != 1 {
		t.Fatalf("counts dirs=%d files=%d hidden=%d", l.Dirs, l.Files, l.Hidden)
	}
	if !l.Entries[0].IsParent || l.Entries[0].Path != filepath.Dir(dir) {
		t.Fatalf("bad parent entry: %+v", l.Entries[0])
	}
}

func TestListShowHidden(t *testing.T) {
	dir := mkTree(t)
	l, err := OSLister{}.List(dir, Options{ShowHidden: true})
	if err != nil {
		t.Fatal(err)
	}
	if l.Hidden != 0 || l.Files != 4 || names(l.Entries)[3] != ".hidden" {
		t.Fatalf("hidden handling wrong: %v", names(l.Entries))
	}
}

func TestSortKeys(t *testing.T) {
	dir := mkTree(t)
	old := time.Now().Add(-48 * time.Hour)
	_ = os.Chtimes(filepath.Join(dir, "A.txt"), old, old)

	l, _ := OSLister{}.List(dir, Options{Sort: SortSize, Desc: true})
	if got := strings.Join(names(l.Entries[3:]), ","); got != "A.txt,b.txt,c.go" {
		t.Fatalf("size sort = %s", got)
	}
	l, _ = OSLister{}.List(dir, Options{Sort: SortTime, Desc: true})
	if got := names(l.Entries)[len(l.Entries)-1]; got != "A.txt" {
		t.Fatalf("time sort: oldest should be last, got %s", got)
	}
	l, _ = OSLister{}.List(dir, Options{Sort: SortExt})
	if got := strings.Join(names(l.Entries[3:]), ","); got != "c.go,A.txt,b.txt" {
		t.Fatalf("ext sort = %s", got)
	}
	if SortExt.Next() != SortName {
		t.Fatal("sort key should cycle")
	}
}

func TestSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	dir := mkTree(t)
	if err := os.Symlink(filepath.Join(dir, "adir"), filepath.Join(dir, "linkdir")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/nonexistent/target", filepath.Join(dir, "broken")); err != nil {
		t.Fatal(err)
	}
	l, err := OSLister{}.List(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Entry{}
	for _, e := range l.Entries {
		byName[e.Name] = e
	}
	ld := byName["linkdir"]
	if !ld.IsLink || !ld.IsDir || ld.LinkBroken {
		t.Fatalf("linkdir: %+v", ld)
	}
	br := byName["broken"]
	if !br.IsLink || br.IsDir || !br.LinkBroken || br.LinkTarget != "/nonexistent/target" {
		t.Fatalf("broken: %+v", br)
	}
}

func TestListErrors(t *testing.T) {
	dir := mkTree(t)
	if _, err := (OSLister{}).List(filepath.Join(dir, "missing"), Options{}); err == nil || !strings.Contains(err.Error(), "no such directory") {
		t.Fatalf("missing dir err = %v", err)
	}
	if _, err := (OSLister{}).List(filepath.Join(dir, "b.txt"), Options{}); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("file err = %v", err)
	}
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission test requires non-root unix")
	}
	locked := filepath.Join(dir, "zdir")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(locked, 0o755)
	if _, err := (OSLister{}).List(locked, Options{}); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("locked dir err = %v", err)
	}
}

func TestRootHasNoParent(t *testing.T) {
	root := string(filepath.Separator)
	if runtime.GOOS == "windows" {
		root = filepath.VolumeName(os.TempDir()) + `\`
	}
	l, err := OSLister{}.List(root, Options{})
	if err != nil {
		t.Skipf("cannot list root: %v", err)
	}
	if len(l.Entries) > 0 && l.Entries[0].IsParent {
		t.Fatal("root listing must not contain ..")
	}
	if !IsRoot(root) || IsRoot(os.TempDir()) {
		t.Fatal("IsRoot wrong")
	}
}

func TestNearestExisting(t *testing.T) {
	dir := t.TempDir()
	if got := NearestExisting(filepath.Join(dir, "a", "b", "c")); got != dir {
		t.Fatalf("NearestExisting = %s, want %s", got, dir)
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 K", 1536: "1.5 K", 10 * 1024: "10 K", 5 << 30: "5.0 G", -1: "?"}
	for in, want := range cases {
		if got := HumanSize(in); got != want {
			t.Errorf("HumanSize(%d) = %q, want %q", in, got, want)
		}
	}
}

func TestSanitizeAndTime(t *testing.T) {
	if got := SanitizeName("a\nb\x1b[31m"); got != "a?b?[31m" {
		t.Fatalf("SanitizeName = %q", got)
	}
	if got := SanitizeName("ok файл 文件"); got != "ok файл 文件" {
		t.Fatalf("unicode altered: %q", got)
	}
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	if got := FormatTime(now.Add(-time.Hour), now); got != "Oct 15 11:00" {
		t.Fatalf("recent = %q", got)
	}
	if got := FormatTime(now.AddDate(-2, 0, 0), now); got != "Oct 15  2024" {
		t.Fatalf("old = %q", got)
	}
	if FormatTime(time.Time{}, now) != "" {
		t.Fatal("zero time should be empty")
	}
}
