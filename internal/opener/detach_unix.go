//go:build unix

package opener

import "syscall"

func detached() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
