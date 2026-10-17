//go:build !windows

package proc

import (
	"errors"
	"os"
	"sync"
	"syscall"
)

// Child is a started process. Wait and Kill may be called from different
// goroutines.
type Child struct {
	Pid    int
	mu     sync.Mutex // guards done/status; held while reaping
	done   bool
	status Status
}

// Start starts the process described by s with syscall.ForkExec (no pidfd,
// no clone3 - see the package documentation).
func Start(s Spec) (*Child, error) {
	argv, env, files, cleanup, err := s.prepare()
	defer cleanup()
	if err != nil {
		return nil, err
	}
	fds := make([]uintptr, len(files))
	for i, f := range files {
		fds[i] = f.Fd()
	}
	pid, err := syscall.ForkExec(s.Path, argv, &syscall.ProcAttr{Dir: s.Dir, Env: env, Files: fds, Sys: s.Sys})
	if err != nil {
		return nil, &os.PathError{Op: "fork/exec", Path: s.Path, Err: err}
	}
	return &Child{Pid: pid}, nil
}

// Wait waits for the process to exit and reaps it. It may be called again
// after it returned; later calls return the same status.
//
// Where the system allows it (Linux/Android: waitid with WNOWAIT), Wait
// first blocks until the child has exited without reaping it and then reaps
// it while holding the lock Kill takes, so Kill can never signal a recycled
// pid. Elsewhere the reaping wait4 runs unlocked (Kill must not block behind
// a running child), leaving the small window os.Process has there too.
func (c *Child) Wait() (Status, error) {
	c.mu.Lock()
	if c.done {
		st := c.status
		c.mu.Unlock()
		return st, nil
	}
	c.mu.Unlock()
	waitable := blockUntilWaitable(c.Pid)
	if waitable {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.done {
			return c.status, nil
		}
	}
	ws, err := wait4(c.Pid)
	if !waitable {
		c.mu.Lock()
		defer c.mu.Unlock()
	}
	if err != nil {
		return Status{}, err
	}
	c.done = true
	if ws.Signaled() {
		c.status = Status{Signal: ws.Signal()}
	} else {
		c.status = Status{Code: ws.ExitStatus()}
	}
	return c.status, nil
}

// wait4 reaps pid, retrying on EINTR and skipping stop/continue reports.
func wait4(pid int) (syscall.WaitStatus, error) {
	for {
		var ws syscall.WaitStatus
		_, err := syscall.Wait4(pid, &ws, 0, nil)
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil {
			return ws, err
		}
		if ws.Stopped() || ws.Continued() {
			continue
		}
		return ws, nil
	}
}

// Kill sends SIGKILL unless the process has already been reaped.
func (c *Child) Kill() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done {
		return nil
	}
	return syscall.Kill(c.Pid, syscall.SIGKILL)
}

// NewGroup returns process attributes that start the child as the leader
// of a new process group, so KillGroup can stop it together with any
// processes it started (a wrapper script and the program it runs).
func NewGroup() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setpgid: true} }

// KillGroup sends SIGKILL to the process group of a child started with
// NewGroup (falling back to the child alone), unless it has been reaped.
func (c *Child) KillGroup() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.done {
		return nil
	}
	if err := syscall.Kill(-c.Pid, syscall.SIGKILL); err == nil {
		return nil
	}
	return syscall.Kill(c.Pid, syscall.SIGKILL)
}
