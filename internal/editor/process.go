package editor

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
)

// Process runs an interactive editor while the TUI is suspended. It
// implements Bubble Tea's tea.ExecCommand interface (Run, SetStdin,
// SetStdout, SetStderr) and is used with tea.Exec instead of
// tea.ExecProcess because it adds the terminal hygiene a full-screen editor
// needs:
//
//   - Job control (Linux, macOS, BSD): the editor runs in its own process
//     group, which is made the terminal's foreground group while it runs.
//     Editors suspend themselves with kill(0, SIGTSTP) (Ctrl-Z, :suspend);
//     with a shared process group that also stopped TwinPane, and the shell
//     then took over the terminal underneath the half-drawn editor screen.
//     Now only the editor stops; TwinPane resumes it (there is no shell to
//     return to) and always takes the terminal back when it exits, even if
//     it failed to start.
//   - Terminal reset: after the editor exits, modes it may have left on
//     (mouse tracking, kitty keyboard flags, modifyOtherKeys, application
//     keypad, cursor shape, focus events) are switched off, so a crashed or
//     killed editor cannot leave TwinPane unable to read keys.
//   - Input flush: unread input left over from the editor session (late
//     replies to the editor's terminal queries, mouse reports) is discarded
//     instead of being delivered to TwinPane as keystrokes.
type Process struct {
	Cmd *exec.Cmd

	// Results, valid after Run returns.
	JobControl bool // the editor ran in its own foreground process group
	Resumed    int  // times the editor stopped itself and was resumed
}

// NewProcess wraps cmd. Stdin/Stdout/Stderr left nil are filled in by
// Bubble Tea with the program's terminal.
func NewProcess(cmd *exec.Cmd) *Process { return &Process{Cmd: cmd} }

func (p *Process) SetStdin(r io.Reader) {
	if p.Cmd.Stdin == nil {
		p.Cmd.Stdin = r
	}
}

func (p *Process) SetStdout(w io.Writer) {
	if p.Cmd.Stdout == nil {
		p.Cmd.Stdout = w
	}
}

func (p *Process) SetStderr(w io.Writer) {
	if p.Cmd.Stderr == nil {
		p.Cmd.Stderr = w
	}
}

// Run starts the editor, waits for it and restores the terminal. The error
// is nil for exit status 0, an *ExitError for a non-zero status or a fatal
// signal, and a start error otherwise.
func (p *Process) Run() error {
	err := p.run() // platform specific (process_jobctl.go / process_other.go)
	if out := terminalOf(p.Cmd.Stdout); out != nil {
		_, _ = io.WriteString(out, ResetSequence)
	}
	if in := terminalOf(p.Cmd.Stdin); in != nil {
		flushInput(in)
	}
	return err
}

// ResetSequence switches off terminal modes a full-screen program may leave
// enabled if it crashes or is killed. Each sequence is ignored by terminals
// that don't support it. Bubble Tea re-enables what TwinPane itself uses
// (alternate screen, bracketed paste, hidden cursor) right afterwards.
const ResetSequence = "" +
	"\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1005l\x1b[?1006l\x1b[?1015l\x1b[?1016l" + // mouse tracking
	"\x1b[?1004l" + // focus in/out reports
	"\x1b[?2004l" + // bracketed paste
	"\x1b[=0;1u\x1b[<99u" + // kitty keyboard protocol: clear flags, empty the stack
	"\x1b[>4;0m" + // xterm modifyOtherKeys off
	"\x1b[?1l\x1b>" + // normal cursor keys, numeric keypad
	"\x1b[0 q\x1b]112\x07" + // default cursor shape and colour
	"\x1b[0m\x1b[?25h" // reset attributes, show cursor

// ExitError reports an editor that exited unsuccessfully.
type ExitError struct {
	Code   int            // exit status (when Signal == 0)
	Signal syscall.Signal // terminating signal, 0 if it exited normally
}

func (e *ExitError) Error() string {
	if e.Signal != 0 {
		return "terminated by signal: " + e.Signal.String()
	}
	return fmt.Sprintf("exit status %d", e.Code)
}

// terminalOf returns v as an *os.File if it is a terminal, else nil.
func terminalOf(v any) *os.File {
	f, ok := v.(*os.File)
	if !ok || f == nil || !isTerminal(f) {
		return nil
	}
	return f
}

// convertExit maps an os/exec error to *ExitError where possible.
func convertExit(err error) error {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		return err
	}
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return &ExitError{Signal: ws.Signal()}
	}
	return &ExitError{Code: ee.ExitCode()}
}

// RescueTerminal is the last-resort cleanup when TwinPane exits abnormally:
// it switches off stray modes and leaves the alternate screen, if out is a
// terminal.
func RescueTerminal(out *os.File) {
	if terminalOf(out) != nil {
		_, _ = io.WriteString(out, ResetSequence+"\x1b[?1049l\r\n")
	}
}
