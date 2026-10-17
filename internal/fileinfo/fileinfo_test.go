package fileinfo

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStatKinds(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.txt")
	os.WriteFile(f, []byte("hello"), 0o640)
	in, err := Stat(f)
	if err != nil {
		t.Fatal(err)
	}
	if in.Name != "a.txt" || in.Dir != dir || in.Size != 5 || in.Type != "regular file" || in.IsDir || in.Items != -1 {
		t.Fatalf("%+v", in)
	}
	if runtime.GOOS != "windows" {
		if in.Mode.Perm() != 0o640 || in.UID != os.Getuid() || !in.ChmodOK {
			t.Fatalf("perm %v uid %d chmod %v %s", in.Mode, in.UID, in.ChmodOK, in.ChmodWhy)
		}
	}
	os.MkdirAll(filepath.Join(dir, "d", "x"), 0o755)
	os.WriteFile(filepath.Join(dir, "d", "y"), nil, 0o644)
	in, _ = Stat(filepath.Join(dir, "d"))
	if !in.IsDir || in.Type != "folder" || in.Items != 2 {
		t.Fatalf("dir %+v", in)
	}
	if runtime.GOOS == "windows" {
		return
	}
	os.Symlink("a.txt", filepath.Join(dir, "link"))
	in, _ = Stat(filepath.Join(dir, "link"))
	if !in.IsLink || in.LinkTarget != "a.txt" || in.TargetType != "regular file" || in.ChmodOK || in.ChmodWhy == "" {
		t.Fatalf("link %+v", in)
	}
	os.Symlink("missing", filepath.Join(dir, "broken"))
	in, _ = Stat(filepath.Join(dir, "broken"))
	if !in.IsLink || in.TargetType != "" || in.TargetErr == nil {
		t.Fatalf("broken %+v", in)
	}
	if _, err := Stat(filepath.Join(dir, "nope")); err == nil {
		t.Fatal("missing file")
	}
}

func TestPermissionFormatting(t *testing.T) {
	cases := []struct {
		m        fs.FileMode
		sym, oct string
	}{
		{0o644, "rw-r--r--", "0644"},
		{0o755, "rwxr-xr-x", "0755"},
		{0o755 | fs.ModeSetuid, "rwsr-xr-x", "4755"},
		{0o644 | fs.ModeSetgid, "rw-r-Sr--", "2644"},
		{0o777 | fs.ModeSticky, "rwxrwxrwt", "1777"},
		{0, "---------", "0000"},
	}
	for _, c := range cases {
		if Symbolic(c.m) != c.sym || Octal(c.m) != c.oct {
			t.Errorf("%v: %s %s", c.m, Symbolic(c.m), Octal(c.m))
		}
		if m, err := ParseOctal(c.oct); err != nil || m != c.m {
			t.Errorf("parse %s = %v %v", c.oct, m, err)
		}
	}
	for _, bad := range []string{"", "9", "abc", "17777"} {
		if _, err := ParseOctal(bad); err == nil {
			t.Errorf("ParseOctal(%q) accepted", bad)
		}
	}
}

func TestChmod(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no unix permissions")
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "a")
	os.WriteFile(f, nil, 0o644)
	if err := Chmod(f, 0o600); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(f); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v", fi.Mode())
	}
	os.Symlink("a", filepath.Join(dir, "l"))
	if err := Chmod(filepath.Join(dir, "l"), 0o777); err == nil {
		t.Fatal("chmod through a link")
	}
	if fi, _ := os.Stat(f); fi.Mode().Perm() != 0o600 {
		t.Fatal("link chmod changed the target")
	}
	if err := Chmod(filepath.Join(dir, "missing"), 0o644); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
}
