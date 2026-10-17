//go:build unix

package ops

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestSpecialFilesAreReportedNotCopied(t *testing.T) {
	root := t.TempDir()
	src := tree(t, root)
	if err := syscall.Mkfifo(filepath.Join(src, "pipe"), 0o644); err != nil {
		t.Skip(err)
	}
	dest := filepath.Join(root, "dest")
	if err := syscall.Mkdir(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	res := run(Copy, dest, Hooks{}, src)
	if res.OK() || len(res.Errors) != 1 || !strings.Contains(res.Errors[0].Error(), "named pipe") {
		t.Fatalf("%+v", res)
	}
	if res.Files != 3 {
		t.Fatalf("regular files should still be copied: %+v", res)
	}
}
