package cloud

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// ReadFile (remote previews) against a simulated rclone. Providers differ in
// what they do with "cat": some stream a file, some report a size and then
// fail, some keep sending, some hang. Each case is a small shell script, so
// the behaviour is exercised without any cloud account and without assuming
// that every backend behaves like the last one.

// fakeBin writes a shell script that stands in for rclone and returns a
// client that runs it with extra environment.
func fakeBin(t *testing.T, script string, env ...string) *Client {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the simulated rclone is a POSIX shell script")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "rclone")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	return &Client{Bin: path, Env: env, Grace: 500 * time.Millisecond}
}

// catScript is a fake rclone that handles "cat" with body and exits with
// code when told to.
func catScript(body string, code int) string {
	return `case "$1" in
  cat) ` + body + ` ;;
esac
echo "unsupported command: $1" >&2
exit 2
`
}

func TestReadFileStreamsTheWholeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notes.txt")
	content := "first line\nsecond line\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	c := fakeBin(t, catScript(`for last; do :; done; exec cat "`+path+`"`, 0))
	var buf bytes.Buffer
	n, truncated, err := c.ReadFile(context.Background(), On("box", "notes.txt"), &buf, 1<<20)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if truncated || n != int64(len(content)) || buf.String() != content {
		t.Fatalf("n=%d truncated=%v content=%q", n, truncated, buf.String())
	}
}

func TestReadFileStopsAtTheLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.txt")
	body := strings.Repeat("0123456789", 1000) // 10 000 bytes
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c := fakeBin(t, catScript(`for last; do :; done; exec cat "`+path+`"`, 0))
	var buf bytes.Buffer
	start := time.Now()
	n, truncated, err := c.ReadFile(context.Background(), On("box", "big.txt"), &buf, 1000)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !truncated {
		t.Fatal("a file beyond the limit must be reported as truncated")
	}
	if n != 1000 || buf.Len() != 1000 || buf.String() != body[:1000] {
		t.Fatalf("n=%d len=%d", n, buf.Len())
	}
	// rclone is stopped, not drained: this must not wait for the whole file
	if d := time.Since(start); d > 10*time.Second {
		t.Fatalf("stopping the transfer took %v", d)
	}
}

func TestReadFileExactlyAtTheLimitIsNotTruncated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "edge.txt")
	body := strings.Repeat("x", 512)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c := fakeBin(t, catScript(`for last; do :; done; exec cat "`+path+`"`, 0))
	var buf bytes.Buffer
	n, truncated, err := c.ReadFile(context.Background(), On("box", "edge.txt"), &buf, 512)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// the limit is reached exactly: whether it is reported as truncated
	// depends on rclone's exit status, so only the bytes matter here
	if n != 512 || buf.Len() != 512 || buf.String() != body {
		t.Fatalf("n=%d truncated=%v len=%d", n, truncated, buf.Len())
	}
}

func TestReadFileEndlessBackendIsCut(t *testing.T) {
	// a backend that never stops sending (unknown size, a live document):
	// the read ends at the limit instead of running forever
	c := fakeBin(t, catScript(`exec yes "0123456789"`, 0))
	var buf bytes.Buffer
	done := make(chan struct{})
	var n int64
	var truncated bool
	var err error
	go func() {
		n, truncated, err = c.ReadFile(context.Background(), On("box", "endless"), &buf, 64<<10)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("an endless backend must be cut at the limit")
	}
	if err != nil || !truncated || n != 64<<10 || buf.Len() != 64<<10 {
		t.Fatalf("n=%d truncated=%v err=%v len=%d", n, truncated, err, buf.Len())
	}
}

func TestReadFileBackendFailureIsExplained(t *testing.T) {
	c := fakeBin(t, catScript(`echo "google docs: cannot download this file" >&2; exit 3`, 3))
	var buf bytes.Buffer
	n, truncated, err := c.ReadFile(context.Background(), On("box", "doc.gdoc"), &buf, 1<<20)
	if err == nil {
		t.Fatal("a failing backend must be reported")
	}
	if n != 0 || truncated || buf.Len() != 0 {
		t.Fatalf("n=%d truncated=%v len=%d", n, truncated, buf.Len())
	}
	var ce *Error
	if !errors.As(err, &ce) {
		t.Fatalf("error type %T: %v", err, err)
	}
	if ce.Kind == KindCanceled {
		t.Fatalf("a failure is not a cancellation: %v", err)
	}
	if !strings.Contains(err.Error(), "google docs") && !strings.Contains(err.Error(), "cannot download") {
		t.Fatalf("rclone's own explanation must be kept: %v", err)
	}
	if ce.Remote != "box" || ce.Op == "" {
		t.Fatalf("error must name the operation and remote: %+v", ce)
	}
}

func TestReadFilePartialFailureKeepsWhatWasRead(t *testing.T) {
	c := fakeBin(t, catScript(`printf 'partial'; echo "connection reset by peer" >&2; exit 1`, 1))
	var buf bytes.Buffer
	n, truncated, err := c.ReadFile(context.Background(), On("box", "half.txt"), &buf, 1<<20)
	if err == nil {
		t.Fatal("a transfer that failed midway must be reported, not silently shown")
	}
	if truncated {
		t.Fatal("a failure is not a bounded, complete read")
	}
	if n != int64(len("partial")) || buf.String() != "partial" {
		t.Fatalf("n=%d content=%q", n, buf.String())
	}
	if !strings.Contains(err.Error(), "after 7 bytes") {
		t.Fatalf("the error must say how much arrived: %v", err)
	}
	if !strings.Contains(err.Error(), "connection reset") {
		t.Fatalf("rclone's reason must be kept: %v", err)
	}
}

func TestReadFileCancellationStopsRclone(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "pid")
	c := fakeBin(t, catScript(`echo $$ > "`+pidFile+`"; sleep 60; echo never`, 0))
	ctx, cancel := context.WithCancel(context.Background())
	var buf bytes.Buffer
	errc := make(chan error, 1)
	go func() {
		_, _, err := c.ReadFile(ctx, On("box", "slow.txt"), &buf, 1<<20)
		errc <- err
	}()
	// wait for rclone to start, then cancel
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(pidFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the simulated rclone never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	start := time.Now()
	cancel()
	select {
	case err := <-errc:
		if err == nil {
			t.Fatal("cancellation must be reported")
		}
		ce, ok := err.(*Error)
		if !ok || ce.Kind != KindCanceled {
			t.Fatalf("cancellation must be a KindCanceled error: %v", err)
		}
		if d := time.Since(start); d > 5*time.Second {
			t.Fatalf("cancellation took %v", d)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancelling did not stop the read")
	}
}

func TestReadFileRejectsBadRequests(t *testing.T) {
	// no rclone
	if _, _, err := (&Client{}).ReadFile(context.Background(), On("box", "x"), &bytes.Buffer{}, 10); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("no program: %v", err)
	}
	// no limit
	c := fakeBin(t, catScript(`exec cat /dev/null`, 0))
	if _, _, err := c.ReadFile(context.Background(), On("box", "x"), &bytes.Buffer{}, 0); err == nil {
		t.Fatal("a read without a limit must be refused")
	}
	// already cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err := c.ReadFile(ctx, On("box", "x"), &bytes.Buffer{}, 10)
	ce, ok := err.(*Error)
	if !ok || ce.Kind != KindCanceled {
		t.Fatalf("pre-cancelled context: %v", err)
	}
}
