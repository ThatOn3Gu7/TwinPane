package proc

import (
	"fmt"
	"os"
	"syscall"
)

// Spec describes a process to start.
type Spec struct {
	Path  string   // absolute path of the executable
	Args  []string // argv, including argv[0]; nil = [Path]
	Env   []string // nil = os.Environ()
	Dir   string   // "" = current directory
	Files []*os.File
	// Files become fds 0, 1, 2, ... of the child; nil entries are
	// replaced by /dev/null.
	Sys *syscall.SysProcAttr
}

// Status is how a process ended.
type Status struct {
	Code   int            // exit status, valid when Signal == 0
	Signal syscall.Signal // terminating signal, or 0
}

// Success reports exit status 0.
func (s Status) Success() bool { return s.Signal == 0 && s.Code == 0 }

func (s Status) String() string {
	if s.Signal != 0 {
		return "terminated by signal: " + s.Signal.String()
	}
	return fmt.Sprintf("exit status %d", s.Code)
}

func (s Spec) prepare() (argv, env []string, files []*os.File, cleanup func(), err error) {
	argv = s.Args
	if len(argv) == 0 {
		argv = []string{s.Path}
	}
	env = s.Env
	if env == nil {
		env = os.Environ()
	}
	var null *os.File
	cleanup = func() {
		if null != nil {
			null.Close()
		}
	}
	files = make([]*os.File, len(s.Files))
	for i, f := range s.Files {
		if f == nil {
			if null == nil {
				if null, err = os.OpenFile(os.DevNull, os.O_RDWR, 0); err != nil {
					return nil, nil, nil, cleanup, err
				}
			}
			f = null
		}
		files[i] = f
	}
	return argv, env, files, cleanup, nil
}
