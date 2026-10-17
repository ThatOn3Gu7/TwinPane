//go:build unix

package editor

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestCheckRefusesSpecialFiles(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Skip(err)
	}
	if _, err := Check(fifo); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("fifo: %v", err)
	}
}
