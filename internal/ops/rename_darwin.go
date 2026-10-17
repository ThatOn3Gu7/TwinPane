package ops

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// renameNoReplace renames atomically, failing if dst exists
// (renamex_np RENAME_EXCL). A case-only rename on a case-insensitive volume
// (dst is the same file) uses a plain rename; filesystems without
// RENAME_EXCL support fall back to check-then-rename.
func renameNoReplace(src, dst string) error {
	err := unix.RenamexNp(src, dst, unix.RENAME_EXCL)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EEXIST):
		if sameFile(src, dst) {
			return os.Rename(src, dst)
		}
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: os.ErrExist}
	case errors.Is(err, unix.ENOTSUP), errors.Is(err, unix.EINVAL), errors.Is(err, unix.ENOSYS):
		return checkedRename(src, dst)
	default:
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
	}
}
