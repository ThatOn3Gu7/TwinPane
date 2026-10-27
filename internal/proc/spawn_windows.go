package proc

import (
	"os"
	"syscall"
	"time"
)

// Child is a started process.
type Child struct {
	Pid    int
	p      *os.Process
	done   bool
	status Status
	// trace records the command's end for the observer installed with
	// SetTrace (see trace.go); nil when nobody is listening.
	trace func(Status, error)
}

// Start starts the process (Windows has no seccomp concerns: os.StartProcess).
func Start(s Spec) (*Child, error) {
	argv, env, files, cleanup, err := s.prepare()
	defer cleanup()
	if err != nil {
		return nil, err
	}
	p, err := os.StartProcess(s.Path, argv, &os.ProcAttr{Dir: s.Dir, Env: env, Files: files, Sys: s.Sys})
	if err != nil {
		return nil, err
	}
	return &Child{Pid: p.Pid, p: p, trace: startTrace(s, argv, time.Now())}, nil
}

func (c *Child) Wait() (Status, error) {
	if c.done {
		return c.status, nil
	}
	st, err := c.p.Wait()
	if err != nil {
		if c.trace != nil {
			c.trace(Status{}, err)
		}
		return Status{}, err
	}
	c.done = true
	c.status = Status{Code: st.ExitCode()}
	if ws, ok := st.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		c.status = Status{Signal: ws.Signal()}
	}
	if c.trace != nil {
		c.trace(c.status, nil)
	}
	return c.status, nil
}

func (c *Child) Kill() error {
	if c.done {
		return nil
	}
	return c.p.Kill()
}

// NewGroup returns nil on Windows (no process groups).
func NewGroup() *syscall.SysProcAttr { return nil }

// KillGroup is Kill on Windows.
func (c *Child) KillGroup() error { return c.Kill() }

// InterruptGroup stops the child. Windows has no SIGINT for a process
// without a console of its own, so this is the same as Kill.
func (c *Child) InterruptGroup() error { return c.Kill() }
