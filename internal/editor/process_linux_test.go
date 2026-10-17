package editor

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// These tests run the editor on a real pseudo-terminal. A helper process
// (this test binary, TestHelperProcess) becomes a session leader that owns
// the pty as its controlling terminal — like TwinPane owning the user's
// terminal — and runs an "editor" shell script through Process.

func openPTY(t *testing.T) (master, slave *os.File) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pty support: %v", err)
	}
	fd := int(m.Fd())
	if err := unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); err != nil {
		t.Skipf("unlockpt: %v", err)
	}
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err != nil {
		t.Skipf("ptsname: %v", err)
	}
	s, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|syscall.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("open slave: %v", err)
	}
	return m, s
}

type helperRun struct {
	result map[string]string
	output string // everything written to the terminal
}

// runHelper runs TestHelperProcess in mode on a fresh pty. input, if set, is
// typed into the terminal after inputDelay.
func runHelper(t *testing.T, mode, input string, inputDelay time.Duration) helperRun {
	t.Helper()
	m, s := openPTY(t)
	defer m.Close()
	dir := t.TempDir()
	res := filepath.Join(dir, "result")
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	cmd.Env = append(os.Environ(), "TP_HELPER=shell", "TP_MODE="+mode, "TP_RESULT="+res, "TP_MARK="+filepath.Join(dir, "mark"))
	cmd.Stdin, cmd.Stdout, cmd.Stderr = s, s, s
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s.Close()

	var mu sync.Mutex
	var out bytes.Buffer
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := m.Read(buf)
			mu.Lock()
			out.Write(buf[:n])
			mu.Unlock()
			if err != nil {
				return
			}
		}
	}()
	if input != "" {
		go func() {
			time.Sleep(inputDelay)
			m.WriteString(input)
		}()
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		syscall.Kill(-cmd.Process.Pid, syscall.SIGCONT)
		<-done
		t.Fatalf("helper (mode %s) hung — it was probably stopped together with the editor", mode)
	}
	time.Sleep(50 * time.Millisecond)
	data, err := os.ReadFile(res)
	if err == nil && strings.TrimSpace(string(data)) == "app_stopped=true" {
		return helperRun{result: map[string]string{"app_stopped": "true"}}
	}
	if err != nil {
		mu.Lock()
		defer mu.Unlock()
		t.Fatalf("helper wrote no result: %v; terminal output:\n%q", err, out.String())
	}
	r := helperRun{result: map[string]string{}}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		k, v, _ := strings.Cut(line, "=")
		r.result[k] = v
	}
	mu.Lock()
	r.output = out.String()
	mu.Unlock()
	return r
}

// TestHelperProcess is not a real test: it is the helper started by
// runHelper.
func TestHelperProcess(t *testing.T) {
	switch os.Getenv("TP_HELPER") {
	case "":
		return
	case "shell":
		helperShell()
	default:
		helperApp(os.Getenv("TP_MODE"))
	}
}

// helperShell plays the user's interactive shell: it is the session leader
// owning the terminal and runs the "app" (standing in for TwinPane) as a job
// in its own foreground process group, so that the app's group is not
// orphaned and stop signals really stop it. Like bash, it notices when the
// job is stopped — which is exactly the bug: bash then printed "Stopped" and
// took over the terminal.
func helperShell() {
	signal.Ignore(syscall.SIGTTOU, syscall.SIGTTIN)
	app := exec.Command(os.Args[0], "-test.run=^TestHelperProcess$")
	app.Env = append(os.Environ(), "TP_HELPER=app")
	app.Stdin, app.Stdout, app.Stderr = os.Stdin, os.Stdout, os.Stderr
	app.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Foreground: true, Ctty: 0}
	stopped := false
	if err := app.Start(); err == nil {
		for {
			var ws unix.WaitStatus
			_, err := unix.Wait4(app.Process.Pid, &ws, unix.WUNTRACED, nil)
			if errors.Is(err, unix.EINTR) {
				continue
			}
			if err != nil || ws.Exited() || ws.Signaled() {
				break
			}
			if ws.Stopped() { // the job control bug: the app itself was stopped
				stopped = true
				unix.Kill(-app.Process.Pid, unix.SIGKILL)
				unix.Kill(-app.Process.Pid, unix.SIGCONT)
			}
		}
	}
	unix.IoctlSetPointerInt(0, unix.TIOCSPGRP, unix.Getpgrp())
	if stopped {
		f, _ := os.OpenFile(os.Getenv("TP_RESULT"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		fmt.Fprintln(f, "app_stopped=true")
		f.Close()
	}
	os.Exit(0)
}

// helperApp stands in for TwinPane: it runs the editor through Process.
func helperApp(mode string) {
	scripts := map[string]string{
		"suspend": `kill -TSTP 0; echo resumed > "$TP_MARK"; exit 0`,
		"status3": `exit 3`,
		"killed":  `kill -KILL $$`,
		"modes":   `printf '\033[?1049h\033[?1002h\033[?1006h\033[>1u\033[>4;2m'; exit 0`,
		"flush":   `sleep 0.6`,
	}
	var cmd *exec.Cmd
	if mode == "missing" {
		cmd = exec.Command("/nonexistent/editor-binary", "file.txt")
	} else {
		cmd = exec.Command("/bin/sh", "-c", scripts[mode])
	}
	p := NewProcess(cmd)
	p.SetStdin(os.Stdin)
	p.SetStdout(os.Stdout)
	p.SetStderr(os.Stderr)
	err := p.Run()

	fg, _ := unix.IoctlGetInt(0, unix.TIOCGPGRP)
	lines := []string{
		fmt.Sprintf("err=%v", err),
		fmt.Sprintf("jobcontrol=%v", p.JobControl),
		fmt.Sprintf("resumed=%d", p.Resumed),
		fmt.Sprintf("foreground_restored=%v", fg == unix.Getpgrp()),
	}
	var xe *ExitError
	if errors.As(err, &xe) {
		lines = append(lines, fmt.Sprintf("code=%d", xe.Code), fmt.Sprintf("signal=%d", int(xe.Signal)))
	}
	if _, e := os.Stat(os.Getenv("TP_MARK")); e == nil {
		lines = append(lines, "mark=yes")
	}
	// Is any input left unread? (poll with a short timeout)
	fds := []unix.PollFd{{Fd: 0, Events: unix.POLLIN}}
	n, _ := unix.Poll(fds, 300)
	lines = append(lines, fmt.Sprintf("pending_input=%v", n > 0))
	f, _ := os.OpenFile(os.Getenv("TP_RESULT"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	fmt.Fprintln(f, strings.Join(lines, "\n"))
	f.Close()
	os.Exit(0)
}

func TestProcessResumesSuspendedEditor(t *testing.T) {
	r := runHelper(t, "suspend", "", 0)
	if r.result["app_stopped"] == "true" {
		t.Fatalf("the editor's suspend stopped the app too (the original bug): %v", r.result)
	}
	want := map[string]string{"err": "<nil>", "jobcontrol": "true", "resumed": "1", "foreground_restored": "true", "mark": "yes"}
	for k, v := range want {
		if r.result[k] != v {
			t.Errorf("%s = %q, want %q (all: %v)", k, r.result[k], v, r.result)
		}
	}
}

func TestProcessExitStatusSignalAndStartFailure(t *testing.T) {
	r := runHelper(t, "status3", "", 0)
	if r.result["code"] != "3" || r.result["foreground_restored"] != "true" || r.result["jobcontrol"] != "true" {
		t.Errorf("status3: %v", r.result)
	}
	r = runHelper(t, "killed", "", 0)
	if r.result["signal"] != fmt.Sprint(int(syscall.SIGKILL)) || r.result["foreground_restored"] != "true" {
		t.Errorf("killed: %v", r.result)
	}
	// The child makes itself the foreground group before exec; when exec
	// fails, the terminal must still be taken back.
	r = runHelper(t, "missing", "", 0)
	if !strings.Contains(r.result["err"], "no such file") || r.result["foreground_restored"] != "true" {
		t.Errorf("missing: %v", r.result)
	}
}

func TestProcessResetsTerminalModes(t *testing.T) {
	r := runHelper(t, "modes", "", 0)
	i := strings.Index(r.output, "\x1b[?1002h")
	j := strings.LastIndex(r.output, ResetSequence)
	if i < 0 || j < i {
		t.Fatalf("reset sequence must follow the editor's output: %q", r.output)
	}
}

func TestProcessFlushesLeftoverInput(t *testing.T) {
	// Typed while the editor runs but never read by it (or a late terminal
	// reply): must not reach TwinPane afterwards.
	r := runHelper(t, "flush", "e:wq\n", 200*time.Millisecond)
	if r.result["pending_input"] != "false" || r.result["err"] != "<nil>" {
		t.Fatalf("%v", r.result)
	}
}

func TestProcessWithoutTerminal(t *testing.T) {
	// No terminal (e.g. tests, pipes): plain run, statuses still mapped.
	p := NewProcess(exec.Command("/bin/sh", "-c", "exit 4"))
	var out bytes.Buffer
	p.SetStdin(strings.NewReader(""))
	p.SetStdout(&out)
	p.SetStderr(&out)
	err := p.Run()
	var xe *ExitError
	if !errors.As(err, &xe) || xe.Code != 4 || p.JobControl {
		t.Fatalf("%v %+v", err, p)
	}
	if out.Len() != 0 {
		t.Fatal("no reset sequence may be written to a non-terminal")
	}
	if !strings.Contains(DescribeExit("vi", err), "status 4: changes may not have been saved") {
		t.Fatal(DescribeExit("vi", err))
	}
	if !strings.Contains(DescribeExit("vi", &ExitError{Signal: syscall.SIGKILL}), "was terminated (killed)") {
		t.Fatal(DescribeExit("vi", &ExitError{Signal: syscall.SIGKILL}))
	}
}
