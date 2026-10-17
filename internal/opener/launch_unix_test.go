//go:build unix

package opener

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeOpener writes a stand-in for termux-open / xdg-open that records
// its arguments one per line.
func fakeOpener(t *testing.T, body string) (string, string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	tool := filepath.Join(dir, "termux-open")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + log + "'\n" + body
	if err := os.WriteFile(tool, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return tool, log
}

func TestLaunchPassesArgumentsUnchanged(t *testing.T) {
	tool, log := fakeOpener(t, "exit 0\n")
	p := Platform{Termux: true, Opener: tool, Name: "termux-open"}
	for _, name := range []string{"/s/with space.jpg", `/s/q'"uote.png`, "/s/$(touch pwned);`id`|&.pdf", "/s/ünï 文件.mp3", "/s/-x --chooser"} {
		argv, _ := p.DefaultArgv(name)
		if err := Launch(argv, 5*time.Second); err != nil {
			t.Fatalf("launch: %v", err)
		}
		b, _ := os.ReadFile(log)
		if got := strings.TrimSuffix(string(b), "\n"); got != "--\n"+name {
			t.Errorf("arguments %q, want -- and %q", got, name)
		}
	}
	if _, err := os.Stat("pwned"); err == nil {
		os.Remove("pwned")
		t.Fatal("a file name was executed by a shell")
	}
}

func TestLaunchFailureReported(t *testing.T) {
	tool, _ := fakeOpener(t, "echo 'am: no such activity' >&2\nexit 3\n")
	err := Launch([]string{tool, "--", "/x.jpg"}, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "exit status 3") || !strings.Contains(err.Error(), "no such activity") {
		t.Errorf("failure: %v", err)
	}
}

func TestLaunchLongRunningDetaches(t *testing.T) {
	tool, _ := fakeOpener(t, "sleep 5\n")
	start := time.Now()
	if err := Launch([]string{tool, "/x.jpg"}, 300*time.Millisecond); err != nil {
		t.Errorf("long-running opener: %v", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("Launch waited %v", d)
	}
}

func TestLaunchRejectsRelative(t *testing.T) {
	if err := Launch([]string{"termux-open", "/x"}, time.Second); err == nil {
		t.Error("relative program accepted")
	}
	if err := Launch(nil, time.Second); err == nil {
		t.Error("empty argv accepted")
	}
}
