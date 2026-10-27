//go:build !windows

package activity

import (
	"os"
	"syscall"
)

// lockFile takes an advisory lock so two instances do not rotate the log at
// the same moment. It returns a function that releases it. A filesystem
// without flock support simply runs unlocked: rotation is then done twice at
// worst, and no entry is lost (see rotateLocked).
func lockFile(path string) func() {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return func() {}
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return func() {}
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}
}
