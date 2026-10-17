package ops

import (
	"errors"
	"io/fs"
	"os"
)

// sameFile reports whether a and b name the same file (e.g. a case-only
// rename on a case-insensitive filesystem).
func sameFile(a, b string) bool {
	ai, err := os.Lstat(a)
	if err != nil {
		return false
	}
	bi, err := os.Lstat(b)
	return err == nil && os.SameFile(ai, bi)
}

// checkedRename is the non-atomic fallback: it refuses an existing dst
// (unless it is src itself) and renames. A file created at dst between the
// check and the rename could still be replaced; only used where the
// platform or filesystem offers nothing better.
func checkedRename(src, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		if !sameFile(src, dst) {
			return &os.LinkError{Op: "rename", Old: src, New: dst, Err: os.ErrExist}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
	}
	return os.Rename(src, dst)
}
