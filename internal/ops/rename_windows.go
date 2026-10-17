package ops

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// renameNoReplace renames without replacing an existing dst: MoveFileEx
// without MOVEFILE_REPLACE_EXISTING (os.Rename would replace it). A
// case-only rename (dst is the same file) uses os.Rename.
func renameNoReplace(src, dst string) error {
	from, err := windows.UTF16PtrFromString(src)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
	}
	to, err := windows.UTF16PtrFromString(dst)
	if err != nil {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
	}
	err = windows.MoveFileEx(from, to, 0)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, windows.ERROR_ALREADY_EXISTS), errors.Is(err, windows.ERROR_FILE_EXISTS):
		if sameFile(src, dst) {
			return os.Rename(src, dst)
		}
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: os.ErrExist}
	default:
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: err}
	}
}
