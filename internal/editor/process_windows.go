package editor

import (
	"os"

	"golang.org/x/sys/windows"
)

var procFlushConsoleInputBuffer = windows.NewLazySystemDLL("kernel32.dll").NewProc("FlushConsoleInputBuffer")

// On Windows there is no job control to manage: the console is shared and
// the editor simply runs to completion.
func (p *Process) run() error { return convertExit(p.Cmd.Run()) }

func isTerminal(f *os.File) bool {
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(f.Fd()), &mode) == nil
}

func flushInput(f *os.File) {
	if procFlushConsoleInputBuffer.Find() == nil {
		_, _, _ = procFlushConsoleInputBuffer.Call(f.Fd())
	}
}

// SaveTerminal records the console mode of f and returns a function that
// restores it.
func SaveTerminal(f *os.File) func() {
	var mode uint32
	h := windows.Handle(f.Fd())
	if windows.GetConsoleMode(h, &mode) != nil {
		return func() {}
	}
	return func() { _ = windows.SetConsoleMode(h, mode) }
}
