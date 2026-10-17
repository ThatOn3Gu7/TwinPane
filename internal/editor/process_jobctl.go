//go:build linux || darwin || freebsd || netbsd || openbsd || dragonfly

package editor

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/ThatOn3Gu7/TwinPane/internal/proc"
)

// maxQuickStops bounds automatic resumption: an editor that is stopped more
// often than this within stopWindow is assumed to be stuck in a stop loop
// and is terminated instead of being resumed forever.
const (
	maxQuickStops = 10
	stopWindow    = 2 * time.Second
)

func isTerminal(f *os.File) bool {
	_, err := unix.IoctlGetTermios(int(f.Fd()), ioctlGetTermios)
	return err == nil
}

// flushInput discards input that has been received but not read.
func flushInput(f *os.File) {
	fd := int(f.Fd())
	if t, err := unix.IoctlGetTermios(fd, ioctlGetTermios); err == nil {
		_ = unix.IoctlSetTermios(fd, ioctlSetTermiosFlush, t) // same settings, input flushed
	}
}

// SaveTerminal records the terminal attributes of f and returns a function
// that restores them (a no-op if f is not a terminal).
func SaveTerminal(f *os.File) func() {
	fd := int(f.Fd())
	t, err := unix.IoctlGetTermios(fd, ioctlGetTermios)
	if err != nil {
		return func() {}
	}
	return func() { _ = unix.IoctlSetTermios(fd, ioctlSetTermios, t) }
}

func foregroundPgrp(fd int) (int, error) { return unix.IoctlGetInt(fd, unix.TIOCGPGRP) }

func setForeground(fd, pgrp int) error { return unix.IoctlSetPointerInt(fd, unix.TIOCSPGRP, pgrp) }

// run starts the editor in its own foreground process group (see Process).
// It falls back to a plain run when TwinPane does not own the terminal
// (e.g. stdin is not a terminal or TwinPane itself runs in the background).
//
// The editor is started with proc.Start (syscall.ForkExec) and reaped with
// wait4 rather than through exec.Cmd.Start: os.StartProcess probes
// pidfd_open, which Android 8-10 forbid (SIGSYS, see package proc).
func (p *Process) run() error {
	if p.Cmd.Err != nil {
		return p.Cmd.Err
	}
	files, finish, err := stdFiles(p.Cmd)
	if err != nil {
		return err
	}
	defer finish()
	spec := proc.Spec{Path: p.Cmd.Path, Args: p.Cmd.Args, Env: p.Cmd.Env, Dir: p.Cmd.Dir, Files: files, Sys: p.Cmd.SysProcAttr}

	tty := terminalOf(p.Cmd.Stdin)
	_, outIsFile := p.Cmd.Stdout.(*os.File)
	_, errIsFile := p.Cmd.Stderr.(*os.File)
	jobctl := tty != nil && outIsFile && errIsFile && p.Cmd.SysProcAttr == nil
	var fd, ours int
	if jobctl {
		fd = int(tty.Fd())
		ours = unix.Getpgrp()
		if fg, err := foregroundPgrp(fd); err != nil || fg != ours {
			jobctl = false
		}
	}
	if !jobctl {
		child, err := proc.Start(spec)
		if err != nil {
			return err
		}
		st, err := child.Wait()
		if err != nil {
			return err
		}
		return exitError(st)
	}

	// The child puts itself in a new process group and makes it the
	// foreground group before exec (signals are blocked in the child while
	// it does so, so it cannot be stopped by SIGTTOU).
	spec.Sys = &syscall.SysProcAttr{Setpgid: true, Foreground: true, Ctty: fd}
	child, startErr := proc.Start(spec)

	// From here on TwinPane is a background process on its own terminal:
	// ignore SIGTTOU/SIGTTIN so that taking the terminal back (and any stray
	// terminal access) cannot stop it. Ignoring only after Start keeps the
	// editor from inheriting the ignored dispositions.
	signal.Ignore(syscall.SIGTTOU, syscall.SIGTTIN)
	defer signal.Reset(syscall.SIGTTOU, syscall.SIGTTIN)
	defer func() { _ = setForeground(fd, ours) }() // always take the terminal back

	if startErr != nil {
		return startErr
	}
	p.JobControl = true
	pid := child.Pid

	var stops []time.Time
	for {
		var ws unix.WaitStatus
		_, err := unix.Wait4(pid, &ws, unix.WUNTRACED, nil)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return err
		}
		switch {
		case ws.Exited():
			if ws.ExitStatus() != 0 {
				return &ExitError{Code: ws.ExitStatus()}
			}
			return nil
		case ws.Signaled():
			return &ExitError{Signal: syscall.Signal(ws.Signal())}
		case ws.Stopped():
			now := time.Now()
			stops = append(stops, now)
			for len(stops) > 0 && now.Sub(stops[0]) > stopWindow {
				stops = stops[1:]
			}
			if len(stops) > maxQuickStops {
				_ = unix.Kill(-pid, unix.SIGTERM)
				_ = unix.Kill(-pid, unix.SIGCONT)
				continue // the next Wait4 reports its exit
			}
			// Suspended (Ctrl-Z, :suspend) or stopped by job control:
			// TwinPane has no shell to return to, so give the editor the
			// terminal back and let it continue.
			p.Resumed++
			_ = setForeground(fd, pid)
			_ = unix.Kill(-pid, unix.SIGCONT)
		}
	}
}

func exitError(st proc.Status) error {
	switch {
	case st.Signal != 0:
		return &ExitError{Signal: st.Signal}
	case st.Code != 0:
		return &ExitError{Code: st.Code}
	}
	return nil
}

// stdFiles returns the child's stdin/stdout/stderr. *os.File streams are
// passed directly; other readers/writers are connected through pipes that
// are copied until the child closes them (finish waits for the copies).
func stdFiles(c *exec.Cmd) (files []*os.File, finish func(), err error) {
	var closeAfter []*os.File // child ends, closed in the parent after start
	var wg sync.WaitGroup
	var parentEnds []*os.File
	fail := func(e error) ([]*os.File, func(), error) {
		for _, f := range append(closeAfter, parentEnds...) {
			f.Close()
		}
		return nil, func() {}, e
	}
	files = make([]*os.File, 3)
	if f, ok := c.Stdin.(*os.File); ok {
		files[0] = f
	} else if c.Stdin != nil {
		r, w, err := os.Pipe()
		if err != nil {
			return fail(err)
		}
		files[0], closeAfter, parentEnds = r, append(closeAfter, r), append(parentEnds, w)
		src := c.Stdin
		// not waited for: the source may never reach EOF (a terminal)
		go func() { _, _ = io.Copy(w, src); w.Close() }()
	}
	for i, dst := range []io.Writer{c.Stdout, c.Stderr} {
		if f, ok := dst.(*os.File); ok {
			files[i+1] = f
			continue
		}
		if dst == nil {
			continue
		}
		// the same writer for stdout and stderr: one pipe (as os/exec
		// does), so the writer is never used by two goroutines
		if i == 1 && sameWriter(c.Stdout, c.Stderr) {
			files[2] = files[1]
			continue
		}
		r, w, err := os.Pipe()
		if err != nil {
			return fail(err)
		}
		files[i+1], closeAfter, parentEnds = w, append(closeAfter, w), append(parentEnds, r)
		wg.Add(1)
		go func(dst io.Writer) { defer wg.Done(); _, _ = io.Copy(dst, r); r.Close() }(dst)
	}
	finish = func() {
		for _, f := range closeAfter {
			f.Close()
		}
		wg.Wait()
	}
	return files, finish, nil
}

// sameWriter reports whether a and b are the same (comparable) writer.
func sameWriter(a, b io.Writer) (same bool) {
	defer func() {
		if recover() != nil {
			same = false // uncomparable dynamic types
		}
	}()
	return a != nil && a == b
}
