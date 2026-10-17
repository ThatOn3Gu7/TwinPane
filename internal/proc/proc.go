// Package proc finds and starts external programs without the system calls
// that Android's seccomp filter forbids.
//
// Android (and so Termux) runs apps under a seccomp filter that answers
// unknown system calls with SIGSYS, which kills a Go program on the spot and
// leaves the terminal in raw mode. Two standard-library paths trip it when
// TwinPane is built for GOOS=linux and run on Android:
//
//   - os/exec.LookPath checks executability with faccessat2 (syscall 439),
//     which no Android version allows;
//   - os.StartProcess (and so exec.Cmd.Start/Run) probes pidfd_open
//     (syscall 434) on first use, which Android 8-10 do not allow.
//
// The Go runtime avoids both only when built with GOOS=android. LookPath and
// Start here avoid them in every build: the executable check uses plain
// faccessat (flags 0) and processes are started with syscall.ForkExec and
// reaped with wait4, which use neither call.
package proc

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ErrNotFound is returned by LookPath when no executable is found.
var ErrNotFound = errors.New("executable file not found in $PATH")

// LookPath searches for an executable named name in the directories of
// $PATH. A name containing a path separator is checked directly. Relative
// $PATH entries (including empty ones) are skipped, as a file manager runs
// in arbitrary directories. The result is an absolute path when name is
// found through $PATH.
func LookPath(name string) (string, error) {
	return lookPathIn(name, os.Getenv("PATH"))
}

func lookPathIn(name, pathEnv string) (string, error) {
	if name == "" {
		return "", &os.PathError{Op: "lookpath", Path: name, Err: ErrNotFound}
	}
	if strings.ContainsRune(name, '/') || strings.ContainsRune(name, filepath.Separator) {
		if err := executable(name); err != nil {
			return "", &os.PathError{Op: "lookpath", Path: name, Err: err}
		}
		return name, nil
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		for _, cand := range candidates(filepath.Join(dir, name)) {
			if executable(cand) == nil {
				return cand, nil
			}
		}
	}
	return "", &os.PathError{Op: "lookpath", Path: name, Err: ErrNotFound}
}
