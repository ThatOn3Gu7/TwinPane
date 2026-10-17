//go:build !(linux || darwin || freebsd || netbsd || openbsd || dragonfly || windows)

package editor

import "os"

// Platforms without terminal job control support: run the editor plainly.
func (p *Process) run() error { return convertExit(p.Cmd.Run()) }

func isTerminal(*os.File) bool { return false }

func flushInput(*os.File) {}

func SaveTerminal(*os.File) func() { return func() {} }
