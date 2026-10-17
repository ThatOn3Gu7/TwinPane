package opener

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ThatOn3Gu7/TwinPane/internal/proc"
)

// LaunchWait is how long Launch waits for an opener to report a failure.
const LaunchWait = 3 * time.Second

// Launch starts argv (argv[0] an absolute path) detached from the terminal:
// in a new session, stdin and stdout on /dev/null, so it can neither read
// keys meant for TwinPane nor draw over it. It waits up to wait for the
// program to finish: a non-zero exit is returned as an error with the last
// line it wrote to stderr; a program still running after wait (some
// openers stay until the app closes) is left running and reaped in the
// background.
//
// No shell is involved: every element of argv reaches the program as one
// argument, whatever characters it contains.
func Launch(argv []string, wait time.Duration) error {
	if len(argv) == 0 || !filepath.IsAbs(argv[0]) {
		return errors.New("no program to run")
	}
	r, w, err := os.Pipe()
	if err != nil {
		return err
	}
	child, err := proc.Start(proc.Spec{Path: argv[0], Args: argv, Files: []*os.File{nil, nil, w}, Sys: detached()})
	w.Close()
	if err != nil {
		r.Close()
		return err
	}
	errText := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(io.LimitReader(r, 16<<10))
		io.Copy(io.Discard, r)
		r.Close()
		errText <- string(b)
	}()
	done := make(chan proc.Status, 1)
	go func() {
		st, _ := child.Wait()
		done <- st
	}()
	name := filepath.Base(argv[0])
	select {
	case st := <-done:
		if st.Success() {
			return nil
		}
		var msg string
		select {
		case msg = <-errText:
		case <-time.After(500 * time.Millisecond):
		}
		if l := lastLine(msg); l != "" {
			return fmt.Errorf("%s failed (%s): %s", name, st, l)
		}
		return fmt.Errorf("%s failed (%s)", name, st)
	case <-time.After(wait):
		return nil // still running: the app is up (reaped by the goroutine)
	}
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	l := strings.TrimSpace(lines[len(lines)-1])
	if len(l) > 200 {
		l = l[:200] + "…"
	}
	return l
}
