//go:build unix

package foldersize

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestSpecialFileIsCountedWithoutOpeningOrBlocking(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(root, "named-pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("named pipes are unavailable: %v", err)
	}
	started := time.Now()
	result := Scan(context.Background(), root, scanOptions(), nil)
	if time.Since(started) > time.Second {
		t.Fatal("scanning a special file blocked")
	}
	if result.Status != StatusComplete || result.Root.Specials != 1 || result.Root.Files != 0 || result.Root.ApparentBytes != 0 {
		t.Fatalf("special-file result: status=%s files=%d special=%d bytes=%d", result.Status, result.Root.Files, result.Root.Specials, result.Root.ApparentBytes)
	}
	if _, err := os.Lstat(fifo); err != nil {
		t.Fatalf("scanner changed the named pipe: %v", err)
	}
}

func TestDirectoryReplacementWithFIFODoesNotBlock(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "changed")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	dirInfo, err := os.Lstat(child)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(child); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(child, 0o600); err != nil {
		t.Skipf("named pipes are unavailable: %v", err)
	}
	filesystem := &overrideFS{osFileSystem: osFileSystem{}, lstat: map[string]fs.FileInfo{child: dirInfo}}
	started := time.Now()
	result := Scan(context.Background(), root, Options{FileSystem: filesystem, SameFilesystem: allowSameFilesystem}, nil)
	if time.Since(started) > time.Second {
		t.Fatal("opening a replaced directory blocked on a FIFO")
	}
	if result.Status != StatusPartial || result.Errors == 0 {
		t.Fatalf("replaced directory result = %s errors=%d", result.Status, result.Errors)
	}
}
