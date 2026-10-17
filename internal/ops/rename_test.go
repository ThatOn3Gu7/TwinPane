package ops

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	good := []string{"a", "file.txt", ".hidden", "with space", "ünïcødé 文件", strings.Repeat("x", 255)}
	for _, n := range good {
		if err := ValidateName(n); err != nil {
			t.Errorf("ValidateName(%q) = %v", n, err)
		}
	}
	bad := []string{"", "   ", ".", "..", "a/b", "../escape", "tab\there", "nl\nx", "nul\x00x", strings.Repeat("x", 256)}
	for _, n := range bad {
		if err := ValidateName(n); !errors.Is(err, ErrInvalidName) {
			t.Errorf("ValidateName(%q) should be invalid, got %v", n, err)
		}
	}
	if runtime.GOOS == "windows" {
		for _, n := range []string{"a:b", "CON", "nul.txt", "trailing."} {
			if ValidateName(n) == nil {
				t.Errorf("%q should be invalid on windows", n)
			}
		}
	}
}

func TestRename(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "old.txt"), "data")
	write(t, filepath.Join(dir, "taken.txt"), "other")

	if err := Rename(dir, "old.txt", "new.txt"); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(dir, "new.txt")) != "data" || exists(filepath.Join(dir, "old.txt")) {
		t.Fatal("rename failed")
	}
	if err := Rename(dir, "new.txt", "taken.txt"); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("collision: %v", err)
	}
	if read(t, filepath.Join(dir, "taken.txt")) != "other" {
		t.Fatal("collision overwrote file")
	}
	if err := Rename(dir, "new.txt", "../escape.txt"); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("traversal: %v", err)
	}
	if exists(filepath.Join(filepath.Dir(dir), "escape.txt")) {
		t.Fatal("escaped directory")
	}
	if err := Rename(dir, "new.txt", "new.txt"); err != nil {
		t.Fatal("same name should be a no-op")
	}
	if err := Rename(dir, "missing", "x"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing source: %v", err)
	}
	os.Mkdir(filepath.Join(dir, "folder"), 0o755)
	if err := Rename(dir, "folder", "renamed-folder"); err != nil || !exists(filepath.Join(dir, "renamed-folder")) {
		t.Fatalf("folder rename: %v", err)
	}
}
