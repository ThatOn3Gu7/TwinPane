//go:build unix

package archive

import "golang.org/x/sys/unix"

func mkfifo(p string) error { return unix.Mkfifo(p, 0o644) }
