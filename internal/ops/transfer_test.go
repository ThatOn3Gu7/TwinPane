package ops

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCopyFilesAndNestedDirs(t *testing.T) {
	root := t.TempDir()
	src := tree(t, root)
	single := filepath.Join(root, "single.txt")
	write(t, single, "one")
	old := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	os.Chtimes(single, old, old)
	os.Chmod(single, 0o640)
	dest := filepath.Join(root, "dest")
	os.Mkdir(dest, 0o755)

	var last Progress
	res := run(Copy, dest, Hooks{Progress: func(p Progress) { last = p }}, src, single)
	if !res.OK() {
		t.Fatalf("not OK: %+v", res)
	}
	if res.Files != 4 || res.Dirs != 3 || res.Bytes != int64(len("alphabravocharlieone")) {
		t.Fatalf("counts: files=%d dirs=%d bytes=%d", res.Files, res.Dirs, res.Bytes)
	}
	if read(t, filepath.Join(dest, "proj", "sub", "deeper", "c.txt")) != "charlie" {
		t.Fatal("nested content wrong")
	}
	fi, _ := os.Stat(filepath.Join(dest, "single.txt"))
	if fi.Mode().Perm() != 0o640 || !fi.ModTime().Equal(old) {
		t.Fatalf("mode/mtime not preserved: %v %v", fi.Mode(), fi.ModTime())
	}
	if !exists(src) || !exists(single) {
		t.Fatal("copy must not remove sources")
	}
	if last.DoneBytes != last.TotalBytes || last.TotalItems != 7 {
		t.Fatalf("final progress %+v", last)
	}
	noTemps(t, root)
}

func TestConflictDefaultIsSkip(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "src", "f.txt"), "new")
	write(t, filepath.Join(root, "dst", "f.txt"), "old")
	res := run(Copy, filepath.Join(root, "dst"), Hooks{}, filepath.Join(root, "src", "f.txt"))
	if res.Skipped != 1 || res.Files != 0 || !res.OK() {
		t.Fatalf("%+v", res)
	}
	if read(t, filepath.Join(root, "dst", "f.txt")) != "old" {
		t.Fatal("existing file was overwritten without a decision")
	}
}

func TestConflictOverwriteSkipCancelAndApplyAll(t *testing.T) {
	root := t.TempDir()
	var srcs []string
	for _, n := range []string{"1", "2", "3"} {
		write(t, filepath.Join(root, "src", n), "new"+n)
		write(t, filepath.Join(root, "dst", n), "old"+n)
		srcs = append(srcs, filepath.Join(root, "src", n))
	}
	dst := filepath.Join(root, "dst")

	// Overwrite one, then skip.
	answers := []Decision{{Action: Overwrite}, {Action: Skip}, {Action: Overwrite}}
	calls := 0
	res := run(Copy, dst, Hooks{Conflict: func(c Conflict) Decision {
		if c.IsDir || c.DstInfo == nil || c.SrcInfo == nil {
			t.Errorf("bad conflict %+v", c)
		}
		d := answers[calls]
		calls++
		return d
	}}, srcs...)
	if calls != 3 || res.Files != 2 || res.Skipped != 1 {
		t.Fatalf("calls=%d %+v", calls, res)
	}
	if read(t, filepath.Join(dst, "1")) != "new1" || read(t, filepath.Join(dst, "2")) != "old2" {
		t.Fatal("overwrite/skip not applied correctly")
	}

	// Apply-to-all: asked once.
	for _, n := range []string{"1", "2", "3"} {
		write(t, filepath.Join(dst, n), "old"+n)
	}
	calls = 0
	res = run(Copy, dst, Hooks{Conflict: func(Conflict) Decision {
		calls++
		return Decision{Action: Skip, ApplyAll: true}
	}}, srcs...)
	if calls != 1 || res.Skipped != 3 {
		t.Fatalf("apply-all: calls=%d %+v", calls, res)
	}

	// Cancel stops everything that follows.
	calls = 0
	res = run(Copy, dst, Hooks{Conflict: func(Conflict) Decision { calls++; return Decision{Action: Cancel} }}, srcs...)
	if !res.Canceled || res.OK() || calls != 1 || res.Files != 0 {
		t.Fatalf("cancel: %+v", res)
	}
	if !strings.Contains(res.Summary(), "cancelled") {
		t.Fatalf("summary should say cancelled: %s", res.Summary())
	}
}

func TestDirectoryMergeAsksForInnerConflicts(t *testing.T) {
	root := t.TempDir()
	src := tree(t, root)
	dest := filepath.Join(root, "dest")
	write(t, filepath.Join(dest, "proj", "a.txt"), "existing")
	write(t, filepath.Join(dest, "proj", "keep.txt"), "untouched")

	var seen []string
	res := run(Copy, dest, Hooks{Conflict: func(c Conflict) Decision {
		seen = append(seen, filepath.Base(c.Dst))
		if c.IsDir {
			return Decision{Action: Overwrite} // merge
		}
		return Decision{Action: Skip}
	}}, src)
	if strings.Join(seen, ",") != "proj,a.txt" {
		t.Fatalf("conflicts asked: %v", seen)
	}
	if read(t, filepath.Join(dest, "proj", "a.txt")) != "existing" || read(t, filepath.Join(dest, "proj", "keep.txt")) != "untouched" {
		t.Fatal("merge damaged existing files")
	}
	if read(t, filepath.Join(dest, "proj", "sub", "deeper", "c.txt")) != "charlie" || res.Skipped != 1 {
		t.Fatalf("merge incomplete: %+v", res)
	}
}

func TestTypeMismatchNeverReplaced(t *testing.T) {
	root := t.TempDir()
	src := tree(t, root)
	dest := filepath.Join(root, "dest")
	write(t, filepath.Join(dest, "proj"), "i am a file")
	res := run(Copy, dest, Hooks{Conflict: func(Conflict) Decision { t.Error("must not ask"); return Decision{Action: Overwrite} }}, src)
	if len(res.Errors) != 1 || res.OK() {
		t.Fatalf("%+v", res)
	}
	if read(t, filepath.Join(dest, "proj")) != "i am a file" {
		t.Fatal("file replaced by folder")
	}
}

func TestCopyIntoItselfRejected(t *testing.T) {
	root := t.TempDir()
	src := tree(t, root)
	for _, dest := range []string{src, filepath.Join(src, "sub"), filepath.Join(src, "sub", "deeper")} {
		for _, k := range []Kind{Copy, Move} {
			res := run(k, dest, Hooks{}, src)
			if res.Fatal == nil || !strings.Contains(res.Fatal.Error(), "into itself") {
				t.Fatalf("%s into %s: %+v", k, dest, res)
			}
		}
	}
	// via a symlinked alias of a descendant
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(filepath.Join(src, "sub"), alias); err != nil {
		t.Skip(err)
	}
	if err := Validate(Request{Kind: Copy, Sources: []string{src}, DestDir: alias}); err == nil {
		t.Fatal("symlinked descendant not detected")
	}
	if exists(filepath.Join(src, "sub", "proj")) || exists(filepath.Join(src, "proj")) {
		t.Fatal("something was created")
	}
}

func TestSameFolderRejected(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "f"), "x")
	if err := Validate(Request{Kind: Copy, Sources: []string{filepath.Join(root, "f")}, DestDir: root}); err == nil || !strings.Contains(err.Error(), "same") {
		t.Fatalf("copy: %v", err)
	}
	if err := Validate(Request{Kind: Move, Sources: []string{filepath.Join(root, "f")}, DestDir: root}); err == nil || !strings.Contains(err.Error(), "already in") {
		t.Fatalf("move: %v", err)
	}
	for _, bad := range []Request{
		{Kind: Copy, Sources: nil, DestDir: root},
		{Kind: Copy, Sources: []string{"relative"}, DestDir: root},
		{Kind: Copy, Sources: []string{filepath.Join(root, "f")}, DestDir: "relative"},
		{Kind: Copy, Sources: []string{filepath.Join(root, "f")}, DestDir: filepath.Join(root, "missing")},
		{Kind: Copy, Sources: []string{filepath.Join(root, "f")}, DestDir: filepath.Join(root, "f")},
		{Kind: Copy, Sources: []string{"/"}, DestDir: root},
	} {
		if Validate(bad) == nil {
			t.Errorf("request should be invalid: %+v", bad)
		}
	}
}

func TestSymlinksCopiedAsLinksIncludingLoops(t *testing.T) {
	root := t.TempDir()
	src := tree(t, root)
	if err := os.Symlink(".", filepath.Join(src, "loop")); err != nil {
		t.Skip(err)
	}
	os.Symlink("/etc/passwd", filepath.Join(src, "abs"))
	dest := filepath.Join(root, "dest")
	os.Mkdir(dest, 0o755)
	done := make(chan Result)
	go func() { done <- run(Copy, dest, Hooks{}, src) }()
	select {
	case res := <-done:
		if !res.OK() {
			t.Fatalf("%+v", res)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("symlink loop caused runaway recursion")
	}
	if tgt, err := os.Readlink(filepath.Join(dest, "proj", "loop")); err != nil || tgt != "." {
		t.Fatalf("loop link not preserved: %q %v", tgt, err)
	}
	if tgt, _ := os.Readlink(filepath.Join(dest, "proj", "abs")); tgt != "/etc/passwd" {
		t.Fatal("absolute link changed")
	}
}

func TestUnreadableFilePartialFailure(t *testing.T) {
	needUnixNonRoot(t)
	root := t.TempDir()
	makeRemovable(t, root)
	src := tree(t, root)
	chmodRestore(t, filepath.Join(src, "sub", "b.txt"), 0)
	dest := filepath.Join(root, "dest")
	os.Mkdir(dest, 0o755)
	res := run(Copy, dest, Hooks{}, src)
	if res.OK() || len(res.Errors) != 1 || !errors.Is(res.Errors[0], os.ErrPermission) {
		t.Fatalf("%+v", res)
	}
	if !exists(filepath.Join(dest, "proj", "a.txt")) || !exists(filepath.Join(dest, "proj", "sub", "deeper", "c.txt")) {
		t.Fatal("other files should still be copied")
	}
	if exists(filepath.Join(dest, "proj", "sub", "b.txt")) {
		t.Fatal("failed file must not exist at destination")
	}
	noTemps(t, root)
}

func TestUnreadableDirectory(t *testing.T) {
	needUnixNonRoot(t)
	root := t.TempDir()
	makeRemovable(t, root)
	src := tree(t, root)
	chmodRestore(t, filepath.Join(src, "sub"), 0o300) // writable/traversable but not listable
	dest := filepath.Join(root, "dest")
	os.Mkdir(dest, 0o755)
	res := run(Copy, dest, Hooks{}, src)
	if res.OK() || len(res.Errors) != 1 || !strings.Contains(res.Errors[0].Error(), "read folder") {
		t.Fatalf("copy should report the unreadable folder: %+v", res)
	}
	if read(t, filepath.Join(dest, "proj", "a.txt")) != "alpha" {
		t.Fatal("readable siblings should be copied")
	}
	// A same-filesystem move is a single rename: it succeeds without reading.
	dest2 := filepath.Join(root, "dest2")
	os.Mkdir(dest2, 0o755)
	if res := run(Move, dest2, Hooks{}, src); !res.OK() || exists(src) || !exists(filepath.Join(dest2, "proj", "sub")) {
		t.Fatalf("move: %+v", res)
	}
	// Cross-filesystem move cannot read it: report, and keep the source.
	dest3 := filepath.Join(root, "dest3")
	os.Mkdir(dest3, 0o755)
	forceCrossDevice(t)
	moved := filepath.Join(dest2, "proj")
	res = run(Move, dest3, Hooks{}, moved)
	if res.OK() || !exists(filepath.Join(moved, "sub")) {
		t.Fatalf("xdev move of unreadable folder: %+v", res)
	}
	if exists(filepath.Join(moved, "a.txt")) {
		t.Fatal("readable verified file should have been moved")
	}
	os.Chmod(filepath.Join(moved, "sub"), 0o755)
	if read(t, filepath.Join(moved, "sub", "b.txt")) != "bravo" {
		t.Fatal("unreadable source content must be untouched")
	}
}

type failingWriter struct {
	w       io.Writer
	after   int
	written int
	err     error
}

func (f *failingWriter) Write(p []byte) (int, error) {
	if f.written+len(p) > f.after {
		return 0, f.err
	}
	f.written += len(p)
	return f.w.Write(p)
}

func TestDiskFullAbortsAndKeepsExistingDestination(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "src", "big"), strings.Repeat("x", 1000))
	write(t, filepath.Join(root, "src", "zz-later"), "later")
	write(t, filepath.Join(root, "dst", "big"), "precious old data")
	setSeam(t, &wrapWriter, func(w io.Writer, dst string) io.Writer {
		return &failingWriter{w: w, after: 100, err: &os.PathError{Op: "write", Path: dst, Err: syscall.ENOSPC}}
	})
	res := run(Copy, filepath.Join(root, "dst"), Hooks{Conflict: func(Conflict) Decision { return Decision{Action: Overwrite} }},
		filepath.Join(root, "src", "big"), filepath.Join(root, "src", "zz-later"))
	if res.Fatal == nil || !strings.Contains(res.Fatal.Error(), "full") || res.OK() || res.Files != 0 {
		t.Fatalf("%+v", res)
	}
	if read(t, filepath.Join(root, "dst", "big")) != "precious old data" {
		t.Fatal("failed overwrite destroyed the existing destination")
	}
	if exists(filepath.Join(root, "dst", "zz-later")) {
		t.Fatal("operation should stop after disk full")
	}
	noTemps(t, root)
}

func TestLowSpacePrecheck(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "src", "f"), strings.Repeat("x", 500))
	os.Mkdir(filepath.Join(root, "dst"), 0o755)
	setSeam(t, &freeSpaceFn, func(string) (uint64, error) { return 10, nil })
	res := run(Copy, filepath.Join(root, "dst"), Hooks{}, filepath.Join(root, "src", "f"))
	if res.Fatal == nil || !strings.Contains(res.Fatal.Error(), "not enough free space") || exists(filepath.Join(root, "dst", "f")) {
		t.Fatalf("nil LowSpace hook must abort: %+v", res)
	}
	var need, free uint64
	res = run(Copy, filepath.Join(root, "dst"), Hooks{LowSpace: func(n, f uint64) bool { need, free = n, f; return true }}, filepath.Join(root, "src", "f"))
	if !res.OK() || need != 500 || free != 10 {
		t.Fatalf("continue after warning: %+v need=%d free=%d", res, need, free)
	}
}

func TestCancelMidwayLeavesNoPartialFiles(t *testing.T) {
	root := t.TempDir()
	var srcs []string
	for i := 0; i < 5; i++ {
		p := filepath.Join(root, "src", string(rune('a'+i)))
		write(t, p, strings.Repeat("z", 300<<10)) // > one buffer
		srcs = append(srcs, p)
	}
	os.Mkdir(filepath.Join(root, "dst"), 0o755)
	ctx, cancel := context.WithCancel(context.Background())
	setSeam(t, &wrapWriter, func(w io.Writer, dst string) io.Writer {
		if filepath.Base(dst) == "c" {
			cancel() // interrupt while the third file is being written
		}
		return w
	})
	res := Run(ctx, Request{Kind: Copy, Sources: srcs, DestDir: filepath.Join(root, "dst")}, Hooks{})
	if !res.Canceled || res.OK() || res.Files != 2 {
		t.Fatalf("%+v", res)
	}
	if exists(filepath.Join(root, "dst", "c")) || exists(filepath.Join(root, "dst", "d")) {
		t.Fatal("interrupted/remaining files must not exist")
	}
	noTemps(t, root)
}

func TestMoveSameFilesystem(t *testing.T) {
	root := t.TempDir()
	src := tree(t, root)
	dest := filepath.Join(root, "dest")
	os.Mkdir(dest, 0o755)
	res := run(Move, dest, Hooks{}, src)
	if !res.OK() || exists(src) || read(t, filepath.Join(dest, "proj", "sub", "b.txt")) != "bravo" {
		t.Fatalf("%+v", res)
	}
	if res.Files != 3 || res.Dirs != 3 {
		t.Fatalf("counts %+v", res)
	}
}

func forceCrossDevice(t *testing.T) {
	setSeam(t, &moveRename, func(src, dst string, replace bool) error {
		return &os.LinkError{Op: "rename", Old: src, New: dst, Err: syscall.EXDEV}
	})
	setSeam(t, &sameDeviceFn, func(a, b string) (bool, error) { return false, nil })
}

func TestMoveCrossDeviceCopiesVerifiesAndRemoves(t *testing.T) {
	root := t.TempDir()
	src := tree(t, root)
	os.Symlink("a.txt", filepath.Join(src, "link"))
	dest := filepath.Join(root, "dest")
	os.Mkdir(dest, 0o755)
	forceCrossDevice(t)
	res := run(Move, dest, Hooks{}, src)
	if !res.OK() || exists(src) {
		t.Fatalf("%+v (src exists=%v)", res, exists(src))
	}
	if read(t, filepath.Join(dest, "proj", "sub", "deeper", "c.txt")) != "charlie" {
		t.Fatal("content")
	}
	if tgt, _ := os.Readlink(filepath.Join(dest, "proj", "link")); tgt != "a.txt" {
		t.Fatal("link not moved")
	}
	noTemps(t, root)
}

func TestMoveVerificationFailureKeepsSource(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "src", "f"), "important")
	os.Mkdir(filepath.Join(root, "dst"), 0o755)
	forceCrossDevice(t)
	setSeam(t, &wrapWriter, func(w io.Writer, dst string) io.Writer { return corruptWriter{w} })
	res := run(Move, filepath.Join(root, "dst"), Hooks{}, filepath.Join(root, "src", "f"))
	if res.OK() || len(res.Errors) != 1 || !strings.Contains(res.Errors[0].Error(), "verification failed") {
		t.Fatalf("%+v", res)
	}
	if read(t, filepath.Join(root, "src", "f")) != "important" {
		t.Fatal("source removed although verification failed")
	}
	if exists(filepath.Join(root, "dst", "f")) {
		t.Fatal("unverified copy left at destination")
	}
	noTemps(t, root)
}

type corruptWriter struct{ w io.Writer }

func (c corruptWriter) Write(p []byte) (int, error) {
	q := append([]byte(nil), p...)
	q[0] ^= 0xff
	return c.w.Write(q)
}

func TestMoveCrossDeviceSourceRemovalFailsIsReported(t *testing.T) {
	needUnixNonRoot(t)
	root := t.TempDir()
	makeRemovable(t, root)
	src := tree(t, root)
	chmodRestore(t, filepath.Join(src, "sub"), 0o555) // files inside can't be unlinked
	dest := filepath.Join(root, "dest")
	os.Mkdir(dest, 0o755)
	forceCrossDevice(t)
	res := run(Move, dest, Hooks{}, src)
	if res.OK() || len(res.Leftovers) == 0 || len(res.Errors) != 0 {
		t.Fatalf("%+v", res)
	}
	if !strings.Contains(res.Summary(), "NOT removed") {
		t.Fatalf("summary must flag leftovers: %s", res.Summary())
	}
	// data must exist at the destination, and the unremovable source must still exist
	if read(t, filepath.Join(dest, "proj", "sub", "b.txt")) != "bravo" || !exists(filepath.Join(src, "sub", "b.txt")) {
		t.Fatal("unexpected state")
	}
	if exists(filepath.Join(src, "a.txt")) {
		t.Fatal("removable verified source should have been removed")
	}
}

func TestMoveMergeSkipKeepsSkippedSource(t *testing.T) {
	root := t.TempDir()
	src := tree(t, root)
	dest := filepath.Join(root, "dest")
	write(t, filepath.Join(dest, "proj", "a.txt"), "existing")
	res := run(Move, dest, Hooks{Conflict: func(c Conflict) Decision {
		if c.IsDir {
			return Decision{Action: Overwrite}
		}
		return Decision{Action: Skip}
	}}, src)
	if !res.OK() || res.Skipped != 1 {
		t.Fatalf("%+v", res)
	}
	if read(t, filepath.Join(src, "a.txt")) != "alpha" || read(t, filepath.Join(dest, "proj", "a.txt")) != "existing" {
		t.Fatal("skipped file handling wrong")
	}
	if exists(filepath.Join(src, "sub")) || !exists(filepath.Join(dest, "proj", "sub", "deeper", "c.txt")) {
		t.Fatal("non-conflicting content should be moved")
	}
}

func TestMoveOverwriteFile(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "src", "f"), "new")
	write(t, filepath.Join(root, "dst", "f"), "old")
	res := run(Move, filepath.Join(root, "dst"), Hooks{Conflict: func(Conflict) Decision { return Decision{Action: Overwrite} }}, filepath.Join(root, "src", "f"))
	if !res.OK() || read(t, filepath.Join(root, "dst", "f")) != "new" || exists(filepath.Join(root, "src", "f")) {
		t.Fatalf("%+v", res)
	}
}

// TestRealCrossDeviceMove uses a directory on a different filesystem when
// TWINPANE_XDEV_DIR is set (e.g. /var/tmp while TMPDIR is a tmpfs).
func TestRealCrossDeviceMove(t *testing.T) {
	other := os.Getenv("TWINPANE_XDEV_DIR")
	if other == "" {
		t.Skip("set TWINPANE_XDEV_DIR to a directory on another filesystem")
	}
	root := t.TempDir()
	dest, err := os.MkdirTemp(other, "twinpane-xdev-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dest)
	if same, _ := sameDevice(root, dest); same {
		t.Skip("TWINPANE_XDEV_DIR is on the same filesystem as TMPDIR")
	}
	src := tree(t, root)
	res := run(Move, dest, Hooks{}, src)
	if !res.OK() || exists(src) || read(t, filepath.Join(dest, "proj", "sub", "deeper", "c.txt")) != "charlie" {
		t.Fatalf("%+v", res)
	}
	noTemps(t, dest)
}

func TestSummary(t *testing.T) {
	r := Result{Kind: Copy, Files: 1, Dirs: 2, Skipped: 1}
	if s := r.Summary(); s != "Copied 1 file, 2 folders, skipped 1" {
		t.Fatal(s)
	}
	r = Result{Kind: Move, Fatal: errors.New("boom")}
	if !strings.HasPrefix(r.Summary(), "Move stopped: boom") {
		t.Fatal(r.Summary())
	}
}
