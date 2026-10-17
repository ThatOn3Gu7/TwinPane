package proc

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
)

func TestLookPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permissions")
	}
	dir := t.TempDir()
	exe := filepath.Join(dir, "myed")
	noexec := filepath.Join(dir, "plain")
	sub := filepath.Join(dir, "sub")
	os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755)
	os.WriteFile(noexec, []byte("x"), 0o644)
	os.Mkdir(sub, 0o755)
	path := "relative:" + "::" + dir
	if p, err := lookPathIn("myed", path); err != nil || p != exe {
		t.Fatalf("myed: %q %v", p, err)
	}
	for _, n := range []string{"plain", "sub", "missing", ""} {
		if p, err := lookPathIn(n, path); err == nil {
			t.Errorf("%s: found %q", n, p)
		}
	}
	// relative entries are skipped even when they would match
	wd, _ := os.Getwd()
	defer os.Chdir(wd)
	os.Chdir(dir)
	if p, err := lookPathIn("myed", ".:"); err == nil {
		t.Errorf("relative PATH entry used: %q", p)
	}
	// names with a slash are checked directly
	if p, err := lookPathIn(exe, ""); err != nil || p != exe {
		t.Fatalf("direct: %q %v", p, err)
	}
	if _, err := lookPathIn(noexec, ""); err == nil {
		t.Fatal("non-executable accepted")
	}
	if _, err := LookPath("sh"); err != nil {
		t.Fatalf("sh not found in $PATH: %v", err)
	}
}

func TestStartWait(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	sh, err := LookPath("sh")
	if err != nil {
		t.Skip(err)
	}
	r, w, _ := os.Pipe()
	c, err := Start(Spec{Path: sh, Args: []string{"sh", "-c", "echo $FOO; pwd; exit 3"}, Env: []string{"FOO=bar"}, Dir: "/", Files: []*os.File{nil, w, nil}})
	w.Close()
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 100)
	n, _ := r.Read(out)
	st, err := c.Wait()
	if err != nil || st.Code != 3 || st.Success() {
		t.Fatalf("%v %v", st, err)
	}
	if got := strings.TrimSpace(string(out[:n])); !strings.HasPrefix(got, "bar") {
		t.Fatalf("output %q", got)
	}
	if st2, _ := c.Wait(); st2 != st {
		t.Fatal("second Wait differs")
	}
	c2, err := Start(Spec{Path: sh, Args: []string{"sh", "-c", "sleep 10"}})
	if err != nil {
		t.Fatal(err)
	}
	c2.Kill()
	if st, _ := c2.Wait(); st.Signal != syscall.SIGKILL {
		t.Fatalf("kill: %v", st)
	}
	if _, err := Start(Spec{Path: "/nonexistent/prog"}); err == nil {
		t.Fatal("start of a missing program succeeded")
	}
}

// Kill from another goroutine while Wait blocks (the search cancels a
// running fd/find this way); Kill after Wait is a no-op.
func TestKillDuringWait(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	sh, err := LookPath("sh")
	if err != nil {
		t.Skip(err)
	}
	for i := 0; i < 20; i++ {
		c, err := Start(Spec{Path: sh, Args: []string{"sh", "-c", "sleep 5"}})
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan Status)
		go func() { st, _ := c.Wait(); done <- st }()
		if err := c.Kill(); err != nil {
			t.Fatal(err)
		}
		st := <-done
		if st.Signal != syscall.SIGKILL {
			t.Fatalf("status %v", st)
		}
		if err := c.Kill(); err != nil {
			t.Fatalf("kill after wait: %v", err)
		}
	}
}
