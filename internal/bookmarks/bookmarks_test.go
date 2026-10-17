package bookmarks

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigDir(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	ucd := func() (string, error) { return "/home/u/.config", nil }
	noUCD := func() (string, error) { return "", errors.New("unset") }
	if d, _ := ConfigDir(env(map[string]string{"TWINPANE_CONFIG_DIR": "/x/y/"}), ucd); d != "/x/y" {
		t.Errorf("override: %q", d)
	}
	if _, err := ConfigDir(env(map[string]string{"TWINPANE_CONFIG_DIR": "rel"}), ucd); err == nil {
		t.Error("relative override accepted")
	}
	if d, _ := ConfigDir(env(nil), ucd); d != "/home/u/.config/twinpane" {
		t.Errorf("user config: %q", d)
	}
	// Termux: HOME=/data/data/com.termux/files/home → ~/.config/twinpane
	termuxHome := "/data/data/com.termux/files/home"
	termuxUCD := func() (string, error) { return termuxHome + "/.config", nil }
	if d, _ := ConfigDir(env(map[string]string{"HOME": termuxHome}), termuxUCD); d != termuxHome+"/.config/twinpane" {
		t.Errorf("termux: %q", d)
	}
	if d, _ := ConfigDir(env(map[string]string{"HOME": "/h"}), noUCD); d != "/h/.twinpane" {
		t.Errorf("fallback: %q", d)
	}
	if _, err := ConfigDir(env(nil), noUCD); err == nil {
		t.Error("no folder at all must fail")
	}
}

func TestAddListRenameRemovePersist(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cfg", FileName) // the folder is created on first write
	s, err := Open(path)
	if err != nil || len(s.List()) != 0 {
		t.Fatalf("empty open: %v %v", err, s.List())
	}
	a := filepath.Join(dir, "Projects")
	b := filepath.Join(dir, "ünï cødé $(x) 'q'")
	os.Mkdir(a, 0o755)
	os.Mkdir(b, 0o755)
	if bm, err := s.Add(a, ""); err != nil || bm.Name != "Projects" || bm.Path != a {
		t.Fatalf("add: %+v %v", bm, err)
	}
	if _, err := s.Add(b+"/", "  Odd\tname  "); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode: %v %v", fi, err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	l := s2.List()
	if len(l) != 2 || l[1].Name != "Odd name" || l[1].Path != b {
		t.Fatalf("reloaded: %+v", l)
	}
	if err := s2.Rename(b, "Renamed"); err != nil {
		t.Fatal(err)
	}
	if err := s2.Remove(a); err != nil {
		t.Fatal(err)
	}
	s3, _ := Open(path)
	if l := s3.List(); len(l) != 1 || l[0].Name != "Renamed" {
		t.Fatalf("after rename/remove: %+v", l)
	}
	if err := s3.Remove(a); !errors.Is(err, ErrNotFound) {
		t.Errorf("remove missing: %v", err)
	}
	if err := s3.Rename(b, "   "); err == nil {
		t.Error("empty name accepted")
	}
	// no temporary files left
	ents, _ := os.ReadDir(filepath.Dir(path))
	if len(ents) != 1 {
		t.Errorf("config folder: %v", ents)
	}
	var f file
	raw, _ := os.ReadFile(path)
	if err := json.Unmarshal(raw, &f); err != nil || f.Version != Version {
		t.Errorf("file format: %v %s", err, raw)
	}
}

func TestDuplicates(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(filepath.Join(dir, FileName))
	real := filepath.Join(dir, "real")
	os.Mkdir(real, 0o755)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skip(err)
	}
	if _, err := s.Add(real, "R"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{real, real + "/", real + "/./", filepath.Join(dir, "x", "..", "real"), link} {
		bm, err := s.Add(p, "")
		if !errors.Is(err, ErrDuplicate) || bm.Name != "R" {
			t.Errorf("Add(%q) = %+v, %v; want ErrDuplicate of R", p, bm, err)
		}
	}
	if len(s.List()) != 1 {
		t.Errorf("list %+v", s.List())
	}
	if i, ok := s.Find(link); !ok || i != 0 {
		t.Error("Find through link")
	}
}

func TestCorruptFileNeverOverwritten(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	junk := []byte("{not json")
	os.WriteFile(path, junk, 0o600)
	s, err := Open(path)
	var ce *CorruptError
	if !errors.As(err, &ce) || !strings.Contains(err.Error(), "will not overwrite") {
		t.Fatalf("open corrupt: %v", err)
	}
	if _, err := s.Add(dir, ""); !errors.As(err, &ce) {
		t.Fatalf("add on corrupt: %v", err)
	}
	if b, _ := os.ReadFile(path); string(b) != string(junk) {
		t.Fatal("corrupt file was overwritten")
	}
	// a newer format version is not overwritten either
	os.WriteFile(path, []byte(`{"version":99,"bookmarks":[]}`), 0o600)
	if _, err := s.Add(dir, ""); !errors.As(err, &ce) {
		t.Fatalf("newer version: %v", err)
	}
}

func TestReadSkipsBadEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	os.WriteFile(path, []byte(`{"version":1,"bookmarks":[{"name":"rel","path":"relative/x"},{"name":"","path":"/tmp/a/"},{"name":"dup","path":"/tmp/a"}]}`), 0o600)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if l := s.List(); len(l) != 1 || l[0].Path != "/tmp/a" || l[0].Name != "a" {
		t.Errorf("list %+v", l)
	}
	os.WriteFile(path, []byte("  \n"), 0o600)
	if s, err := Open(path); err != nil || len(s.List()) != 0 {
		t.Errorf("empty file: %v", err)
	}
}

func TestConcurrentInstancesMerge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	s1, _ := Open(path)
	s2, _ := Open(path)
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	os.Mkdir(a, 0o755)
	os.Mkdir(b, 0o755)
	s1.Add(a, "")
	s2.Add(b, "") // re-reads first: a is kept
	s3, _ := Open(path)
	if len(s3.List()) != 2 {
		t.Errorf("lost an update: %+v", s3.List())
	}
}

func TestCheckStates(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "file")
	os.WriteFile(f, nil, 0o644)
	if st, _ := Check(dir); st != OK {
		t.Error("dir")
	}
	if st, _ := Check(f); st != NotFolder {
		t.Error("file")
	}
	if st, _ := Check(filepath.Join(dir, "gone")); st != Missing {
		t.Error("missing")
	}
}

func TestNames(t *testing.T) {
	if DefaultName("/") != "/" || DefaultName("/a/b") != "b" {
		t.Error("DefaultName")
	}
	long := strings.Repeat("é", 100)
	if n := CleanName(long); len([]rune(n)) != MaxName {
		t.Errorf("CleanName length %d", len([]rune(n)))
	}
	if CleanName("a\x1b[31mb\n") != "a [31mb" {
		t.Errorf("CleanName control: %q", CleanName("a\x1b[31mb\n"))
	}
}

func TestUnwritableConfigDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root")
	}
	dir := t.TempDir()
	ro := filepath.Join(dir, "ro")
	os.Mkdir(ro, 0o555)
	defer os.Chmod(ro, 0o755)
	s, _ := Open(filepath.Join(ro, FileName))
	if _, err := s.Add(dir, ""); err == nil || !strings.Contains(err.Error(), "cannot write") {
		t.Errorf("unwritable: %v", err)
	}
	if len(s.List()) != 0 {
		t.Error("in-memory list changed although the save failed")
	}
}
