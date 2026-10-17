//go:build linux

package ops

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// renameNoReplace renames atomically, failing if dst appeared in the
// meantime (RENAME_NOREPLACE). Filesystems without support fall back to
// check-then-rename.
func renameNoReplace(src, dst string) error {
	err := unix.Renameat2(unix.AT_FDCWD, src, unix.AT_FDCWD, dst, unix.RENAME_NOREPLACE)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EEXIST):
		// Case-only rename on a case-folding filesystem: same file.
		if sameFile(src, dst) {
			return os.Rename(src, dst)
		}
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: os.ErrExist}
	case errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EINVAL):
		return checkedRename(src, dst)
	default:
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
	}
}
