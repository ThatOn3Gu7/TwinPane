//go:build unix

package search

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"

	"github.com/ThatOn3Gu7/TwinPane/internal/textview"
)

// openRegularContentFile opens without following symlinks and without
// blocking on a raced-in FIFO. Fstat is authoritative after the open.
func openRegularContentFile(path string) (*os.File, os.FileInfo, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, nil, err
	}
	f := os.NewFile(uintptr(fd), path)
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, nil, fmt.Errorf("%s: %w", path, textview.ErrNotRegular)
	}
	return f, info, nil
}
