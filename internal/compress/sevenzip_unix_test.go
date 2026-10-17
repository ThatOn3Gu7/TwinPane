//go:build unix

package compress

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
)

func mkfifo(p string) error { return syscall.Mkfifo(p, 0o644) }

// fake7z writes a stand-in for 7-Zip that logs its arguments (one per
// line) and acts according to $FAKE7Z_MODE.
const fake7zScript = `#!/bin/sh
if [ $# -eq 0 ]; then
  echo
  echo "7-Zip 25.01 (x64) : Copyright (c) 1999-2025 Igor Pavlov : 2025-08-03"
  exit 0
fi
printf '%s\n' "$@" > "$FAKE7Z_LOG"
pwd >> "$FAKE7Z_LOG.cwd"
out=""
prev=""
for a in "$@"; do
  if [ "$prev" = "--" ]; then out="$a"; fi
  prev="$a"
done
case "$FAKE7Z_MODE" in
  fail) echo "ERROR: something broke" >&2; exit 2;;
  sleep) printf '  5%%'; sleep 30; exit 0;;
  warn) printf 'data' > "$out"; echo "gone.txt : errno=2 : No such file or directory" >&2; exit 1;;
  nowrite) exit 0;;
esac
printf ' 50%%\b\b\b\b100%%'
printf '7z-data' > "$out"
exit 0
`

func fake7z(t *testing.T, mode string) (tool, log string) {
	t.Helper()
	dir := t.TempDir()
	tool = filepath.Join(dir, "7z")
	if err := os.WriteFile(tool, []byte(fake7zScript), 0o755); err != nil {
		t.Fatal(err)
	}
	log = filepath.Join(dir, "args.log")
	t.Setenv("FAKE7Z_LOG", log)
	t.Setenv("FAKE7Z_MODE", mode)
	return tool, log
}

func TestFake7zArguments(t *testing.T) {
	tool, log := fake7z(t, "ok")
	root := t.TempDir()
	dir := filepath.Join(root, "d")
	os.Mkdir(dir, 0o755)
	var sources []string
	for _, n := range awkward {
		p := filepath.Join(dir, n)
		os.WriteFile(p, []byte(n), 0o644)
		sources = append(sources, p)
	}
	mkfifo(filepath.Join(dir, "pipe")) // not selected: must not matter
	out := filepath.Join(root, "res.7z")
	var pct []int64
	res := Create(context.Background(), Request{Format: SevenZ, Level: 4, Sources: sources, Output: out, Tool: tool},
		ops.Hooks{Progress: func(p ops.Progress) { pct = append(pct, p.DoneBytes) }})
	if !res.OK() {
		t.Fatalf("%s", res.Summary())
	}
	if b, _ := os.ReadFile(out); string(b) != "7z-data" {
		t.Fatalf("output %q", b)
	}
	raw, _ := os.ReadFile(log)
	args := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	// each name arrives as exactly one argument, unchanged, after "--"
	sep := -1
	for i, a := range args {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep < 0 || len(args) != sep+2+len(awkward) {
		t.Fatalf("arguments %q", args)
	}
	for i, n := range awkward {
		if args[sep+2+i] != n {
			t.Errorf("name %d: %q, want %q", i, args[sep+2+i], n)
		}
	}
	for _, want := range []string{"a", "-t7z", "-mx=7", "-spd", "-snl", "-bsp1"} {
		if !contains(args[:sep], want) {
			t.Errorf("switch %s missing in %q", want, args[:sep])
		}
	}
	if tmp := args[sep+1]; filepath.Dir(filepath.Dir(tmp)) != root || !strings.HasPrefix(filepath.Base(filepath.Dir(tmp)), ops.TempPrefix) {
		t.Errorf("temporary output %q not in a fresh folder of the destination", tmp)
	}
	cwd, _ := os.ReadFile(log + ".cwd")
	if strings.TrimSpace(string(cwd)) != dir {
		t.Errorf("7z ran in %q, want %q", cwd, dir)
	}
	if len(pct) == 0 {
		t.Error("no progress")
	}
	noLeftovers(t, root)
}

func TestFake7zExcludesOutputAndSpecials(t *testing.T) {
	tool, log := fake7z(t, "ok")
	root := t.TempDir()
	src, _ := tree(t, root)
	if err := mkfifo(filepath.Join(src, "sub", "pipe")); err != nil {
		t.Skip(err)
	}
	out := filepath.Join(src, "sub", "self.7z") // inside the source
	res := Create(context.Background(), Request{Format: SevenZ, Sources: []string{src}, Output: out, Tool: tool}, ops.Hooks{})
	if !res.Created() {
		t.Fatal(res.Summary())
	}
	raw, _ := os.ReadFile(log)
	args := strings.Split(string(raw), "\n")
	var tmpEx, pipeEx bool
	for _, a := range args {
		if strings.HasPrefix(a, "-x!src/sub/"+ops.TempPrefix+"7z-") {
			tmpEx = true
		}
		if a == "-x!src/sub/pipe" {
			pipeEx = true
		}
	}
	if !tmpEx || !pipeEx {
		t.Errorf("excludes missing (temp folder %v, pipe %v): %q", tmpEx, pipeEx, args)
	}
	if len(res.Errors) != 1 {
		t.Errorf("errors %v (want the pipe)", res.Errors)
	}
	noLeftovers(t, filepath.Join(src, "sub"))
}

func TestFake7zFailure(t *testing.T) {
	tool, _ := fake7z(t, "fail")
	root := t.TempDir()
	src, _ := tree(t, root)
	out := filepath.Join(root, "f.7z")
	res := Create(context.Background(), Request{Format: SevenZ, Sources: []string{src}, Output: out, Tool: tool}, ops.Hooks{})
	if res.Fatal == nil || !strings.Contains(res.Fatal.Error(), "something broke") {
		t.Fatalf("failure: %v", res.Fatal)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("output written")
	}
	noLeftovers(t, root)
}

func TestFake7zNoOutput(t *testing.T) {
	tool, _ := fake7z(t, "nowrite")
	root := t.TempDir()
	src, _ := tree(t, root)
	res := Create(context.Background(), Request{Format: SevenZ, Sources: []string{src}, Output: filepath.Join(root, "n.7z"), Tool: tool}, ops.Hooks{})
	if res.Fatal == nil || !strings.Contains(res.Fatal.Error(), "did not write") {
		t.Fatalf("no output: %v", res.Fatal)
	}
	noLeftovers(t, root)
}

func TestFake7zWarnings(t *testing.T) {
	tool, _ := fake7z(t, "warn")
	root := t.TempDir()
	src, _ := tree(t, root)
	out := filepath.Join(root, "w.7z")
	res := Create(context.Background(), Request{Format: SevenZ, Sources: []string{src}, Output: out, Tool: tool}, ops.Hooks{})
	if !res.Created() || len(res.Errors) != 1 || res.Errors[0].Path != "gone.txt" {
		t.Fatalf("warnings: %s %v", res.Summary(), res.Errors)
	}
}

func TestFake7zCancelKills(t *testing.T) {
	tool, _ := fake7z(t, "sleep")
	root := t.TempDir()
	src, _ := tree(t, root)
	out := filepath.Join(root, "c.7z")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { time.Sleep(300 * time.Millisecond); cancel() }()
	start := time.Now()
	res := Create(ctx, Request{Format: SevenZ, Sources: []string{src}, Output: out, Tool: tool}, ops.Hooks{})
	if !res.Canceled {
		t.Fatalf("not cancelled: %s", res.Summary())
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("cancel took %v (process not killed?)", d)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("output exists")
	}
	noLeftovers(t, root)
}

// TestReal7z checks the arguments against an installed 7-Zip, when there
// is one (skipped otherwise).
func TestReal7z(t *testing.T) {
	var tool string
	for _, n := range []string{"7zz", "7z", "7za"} {
		if p, err := exec.LookPath(n); err == nil {
			tool = p
			break
		}
	}
	if tool == "" {
		t.Skip("no 7-Zip installed")
	}
	root := t.TempDir()
	src, want := tree(t, root)
	out := filepath.Join(src, "self.7z")
	res := Create(context.Background(), Request{Format: SevenZ, Level: 1, Sources: []string{src}, Output: out, Tool: tool}, ops.Hooks{})
	if !res.OK() {
		t.Fatalf("%s %v", res.Summary(), res.Errors)
	}
	cmd := exec.Command(tool, "l", "-slt", "-ba", out)
	cmd.Env = append(os.Environ(), "LC_ALL=C.UTF-8")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("7z l: %v\n%s", err, b)
	}
	listing := string(b)
	for name := range want {
		if !strings.Contains(listing, "Path = "+name+"\n") {
			t.Errorf("%q not in the archive", name)
		}
	}
	if strings.Contains(listing, "self.7z") || strings.Contains(listing, ops.TempPrefix) {
		t.Error("archive contains itself")
	}
	if info := probe7z(tool); info.snl && !strings.Contains(listing, "Path = src/sub/link\n") {
		t.Error("link missing")
	}
	noLeftovers(t, src)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
