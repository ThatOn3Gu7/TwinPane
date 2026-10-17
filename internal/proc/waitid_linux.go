package proc

import (
	"errors"

	"golang.org/x/sys/unix"
)

// blockUntilWaitable waits until pid has exited, leaving it unreaped
// (waitid with WNOWAIT, as os.Process does; allowed by Android's seccomp
// filter). It reports whether it did so.
func blockUntilWaitable(pid int) bool {
	for {
		var info unix.Siginfo
		err := unix.Waitid(unix.P_PID, pid, &info, unix.WEXITED|unix.WNOWAIT, nil)
		if !errors.Is(err, unix.EINTR) {
			return err == nil
		}
	}
}
