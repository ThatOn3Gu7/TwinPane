//go:build darwin || freebsd || netbsd || openbsd || dragonfly

package editor

import "golang.org/x/sys/unix"

const (
	ioctlGetTermios      = unix.TIOCGETA
	ioctlSetTermios      = unix.TIOCSETA
	ioctlSetTermiosFlush = unix.TIOCSETAF // set attributes after discarding pending input
)
