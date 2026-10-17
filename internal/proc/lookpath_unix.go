//go:build !windows

package proc

import (
	"errors"
	"os"
	"syscall"
)

func candidates(p string) []string { return []string{p} }

// executable reports whether p is a regular file the current user may
// execute. syscall.Access is faccessat(AT_FDCWD, p, X_OK, 0) - flags 0,
// so it never uses faccessat2.
func executable(p string) error {
	fi, err := os.Stat(p)
	if err != nil {
		return err
	}
	if fi.IsDir() || !fi.Mode().IsRegular() {
		return errors.New("not a regular file")
	}
	if fi.Mode()&0o111 == 0 {
		return os.ErrPermission
	}
	return syscall.Access(p, 0x1 /* X_OK */)
}
