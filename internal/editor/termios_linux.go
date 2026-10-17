package editor

import "golang.org/x/sys/unix"

const (
	ioctlGetTermios      = unix.TCGETS
	ioctlSetTermios      = unix.TCSETS
	ioctlSetTermiosFlush = unix.TCSETSF // set attributes after discarding pending input
)
