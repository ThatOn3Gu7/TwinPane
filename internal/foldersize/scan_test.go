package foldersize

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func allowSameFilesystem(string, string, fs.FileInfo, fs.FileInfo) (bool, bool) {
	return true, true
}

func scanOptions() Options { return Options{SameFilesystem: allowSameFilesystem} }

func writeSizedFile(t *testing.T, dir, name string, size int) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func findNode(node *Node, name string) *Node {
	for _, child := range node.Children {
		if child.Name == name {
			return child
		}
	}
	return nil
}

func TestNestedTotalsAndEmptyEntries(t *testing.T) {
	root := t.TempDir()
	writeSizedFile(t, root, "root.bin", 3)
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSizedFile(t, nested, "child.bin", 5)
	writeSizedFile(t, nested, "empty", 0)
	if err := os.Mkdir(filepath.Join(root, "empty-dir"), 0o700); err != nil {
		t.Fatal(err)
	}

	result := Scan(context.Background(), root, scanOptions(), nil)
	if result.Status != StatusComplete {
		t.Fatalf("status = %s, error %q", result.Status, result.Error)
	}
	if got := result.Root.ApparentBytes; got != 8 {
		t.Fatalf("apparent bytes = %d, want 8", got)
	}
	if result.Root.Files != 3 || result.Root.Directories != 3 || result.Root.Symlinks != 0 || result.Root.Specials != 0 {
		t.Fatalf("totals: files=%d dirs=%d links=%d special=%d", result.Root.Files, result.Root.Directories, result.Root.Symlinks, result.Root.Specials)
	}
	if got := findNode(result.Root, "nested"); got == nil || got.ApparentBytes != 5 || got.Files != 2 || got.Directories != 1 {
		t.Fatalf("nested totals = %+v", got)
	}
	if got := findNode(result.Root, "empty-dir"); got == nil || got.ApparentBytes != 0 || got.Files != 0 || got.Directories != 1 {
		t.Fatalf("empty directory totals = %+v", got)
	}
}

func TestSymlinksAreCountedButNeverFollowed(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	outside := filepath.Join(parent, "outside")
	if err := os.MkdirAll(filepath.Join(root, "real"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSizedFile(t, filepath.Join(root, "real"), "file", 4)
	writeSizedFile(t, outside, "large", 1000)
	links := map[string]string{
		filepath.Join(root, "file-link"):    "real/file",
		filepath.Join(root, "outside-link"): "../outside",
		filepath.Join(root, "cycle"):        ".",
	}
	for path, target := range links {
		if err := os.Symlink(target, path); err != nil {
			t.Skipf("symlinks are unavailable: %v", err)
		}
	}

	result := Scan(context.Background(), root, scanOptions(), nil)
	if result.Status != StatusComplete {
		t.Fatalf("status = %s, error %q", result.Status, result.Error)
	}
	if result.Root.Files != 1 || result.Root.Directories != 2 || result.Root.Symlinks != 3 {
		t.Fatalf("symlink traversal/counts: files=%d dirs=%d links=%d", result.Root.Files, result.Root.Directories, result.Root.Symlinks)
	}
	if result.Root.ApparentBytes >= 1000 {
		t.Fatalf("followed outside or cycle link: apparent total %d", result.Root.ApparentBytes)
	}
	if len(result.Root.Children) != 1 || result.Root.Children[0].Name != "real" {
		t.Fatalf("directory links were traversed: children = %+v", result.Root.Children)
	}

	linkRoot := filepath.Join(parent, "root-link")
	if err := os.Symlink(root, linkRoot); err != nil {
		t.Skipf("root symlinks are unavailable: %v", err)
	}
	linkResult := Scan(context.Background(), linkRoot, scanOptions(), nil)
	if linkResult.Status != StatusFailed || !strings.Contains(linkResult.Error, "symbolic link") {
		t.Fatalf("symlink root status/error = %s / %q", linkResult.Status, linkResult.Error)
	}
}

func TestHardLinksAreCountedOncePerPath(t *testing.T) {
	root := t.TempDir()
	first := writeSizedFile(t, root, "first", 7)
	if err := os.Link(first, filepath.Join(root, "second")); err != nil {
		t.Skipf("hard links are unavailable: %v", err)
	}
	result := Scan(context.Background(), root, scanOptions(), nil)
	if result.Status != StatusComplete || result.Root.Files != 2 || result.Root.ApparentBytes != 14 {
		t.Fatalf("hard-link path semantics: status=%s files=%d bytes=%d", result.Status, result.Root.Files, result.Root.ApparentBytes)
	}
}

func TestMountBoundaryIsVisibleAndNotTraversed(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "mounted")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSizedFile(t, child, "not-included", 23)
	options := Options{SameFilesystem: func(_, childPath string, _, _ fs.FileInfo) (bool, bool) {
		return filepath.Clean(childPath) != filepath.Clean(child), true
	}}
	result := Scan(context.Background(), root, options, nil)
	mounted := findNode(result.Root, "mounted")
	if result.Status != StatusPartial || result.MountsSkipped != 1 || mounted == nil || mounted.Status != StatusBoundarySkipped {
		t.Fatalf("boundary result = status %s, skipped %d, child %+v", result.Status, result.MountsSkipped, mounted)
	}
	if result.Root.ApparentBytes != 0 || mounted.Files != 0 {
		t.Fatalf("crossed skipped boundary: root bytes %d, child files %d", result.Root.ApparentBytes, mounted.Files)
	}
}

func TestDuplicateDirectoryIdentityIsNotTraversedTwice(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	alias := filepath.Join(root, "alias")
	for _, path := range []string{real, alias} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeSizedFile(t, real, "data", 9)
	realInfo, err := os.Lstat(real)
	if err != nil {
		t.Fatal(err)
	}
	if directoryIdentity(realInfo, real) != directoryIdentity(realInfo, alias) {
		t.Skip("directory identity is path-based on this platform")
	}

	// Model two paths to the same directory consistently: both Lstat and
	// OpenDir (including paths below the alias) resolve to the real directory.
	// The filesystem may return either name first, so accept either as the
	// one scanned and require the other to be skipped as a duplicate.
	filesystem := directoryAliasFS{osFileSystem: osFileSystem{}, alias: alias, target: real}
	result := Scan(context.Background(), root, Options{FileSystem: filesystem, SameFilesystem: allowSameFilesystem}, nil)
	realNode, aliasNode := findNode(result.Root, "real"), findNode(result.Root, "alias")
	if result.Status != StatusComplete || result.DuplicatesSkipped != 1 || realNode == nil || aliasNode == nil {
		t.Fatalf("duplicate result = status %s, skipped %d, real %+v, alias %+v", result.Status, result.DuplicatesSkipped, realNode, aliasNode)
	}
	realSkipped := realNode.Status == StatusDuplicateSkipped
	aliasSkipped := aliasNode.Status == StatusDuplicateSkipped
	if realSkipped == aliasSkipped {
		t.Fatalf("exactly one duplicate directory should be skipped: real %+v, alias %+v", realNode, aliasNode)
	}
	if result.Root.Files != 1 || result.Root.ApparentBytes != 9 {
		t.Fatalf("duplicate directory was counted twice: files=%d bytes=%d", result.Root.Files, result.Root.ApparentBytes)
	}
}

func TestUnreadableAndDisappearingEntriesMakePartialResult(t *testing.T) {
	root := t.TempDir()
	blocked := filepath.Join(root, "blocked")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSizedFile(t, blocked, "hidden", 50)
	writeSizedFile(t, root, "vanished", 5)
	writeSizedFile(t, root, "counted", 3)
	filesystem := &overrideFS{
		osFileSystem: osFileSystem{},
		lstatErr:     map[string]error{filepath.Join(root, "vanished"): fs.ErrNotExist},
		openErr:      map[string]error{blocked: fs.ErrPermission},
	}
	result := Scan(context.Background(), root, Options{FileSystem: filesystem, SameFilesystem: allowSameFilesystem}, nil)
	blockedNode := findNode(result.Root, "blocked")
	if result.Status != StatusPartial || result.Errors != 2 || result.Root.ApparentKnown {
		t.Fatalf("partial result = status %s errors %d known %v", result.Status, result.Errors, result.Root.ApparentKnown)
	}
	if result.Root.ApparentBytes != 3 || result.Root.Files != 1 || blockedNode == nil || blockedNode.Status != StatusPartial {
		t.Fatalf("partial totals/status: bytes=%d files=%d blocked=%+v", result.Root.ApparentBytes, result.Root.Files, blockedNode)
	}
	if !strings.Contains(result.Error, "permission denied") && !strings.Contains(result.Root.FirstError, "permission denied") {
		t.Fatalf("permission failure not reported: result=%q root=%q", result.Error, result.Root.FirstError)
	}
}

func TestRootPermissionFailureIsFailed(t *testing.T) {
	root := t.TempDir()
	filesystem := &overrideFS{osFileSystem: osFileSystem{}, openErr: map[string]error{root: fs.ErrPermission}}
	result := Scan(context.Background(), root, Options{FileSystem: filesystem, SameFilesystem: allowSameFilesystem}, nil)
	if result.Status != StatusFailed || !strings.Contains(result.Error, "permission denied") {
		t.Fatalf("root failure = %s / %q", result.Status, result.Error)
	}
}

func TestDirectoryAndDepthLimitsArePartial(t *testing.T) {
	root := t.TempDir()
	one := filepath.Join(root, "one")
	two := filepath.Join(one, "two")
	if err := os.MkdirAll(two, 0o700); err != nil {
		t.Fatal(err)
	}
	writeSizedFile(t, one, "near", 2)
	writeSizedFile(t, two, "deep", 7)

	depth := Scan(context.Background(), root, Options{SameFilesystem: allowSameFilesystem, MaxDepth: 1}, nil)
	deep := findNode(findNode(depth.Root, "one"), "two")
	if depth.Status != StatusPartial || deep == nil || deep.Status != StatusPartial || depth.Root.Files != 1 || depth.Root.ApparentBytes != 2 {
		t.Fatalf("depth limit result: status=%s files=%d bytes=%d deep=%+v", depth.Status, depth.Root.Files, depth.Root.ApparentBytes, deep)
	}

	directoryLimit := Scan(context.Background(), root, Options{SameFilesystem: allowSameFilesystem, MaxDirectories: 1}, nil)
	if directoryLimit.Status != StatusPartial || directoryLimit.Errors == 0 || directoryLimit.Root.ApparentKnown || directoryLimit.Root.Directories != 1 {
		t.Fatalf("directory limit did not produce a partial lower bound: %+v", directoryLimit)
	}
}

func TestCancellationStopsWorkAndClosesDirectory(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 1024; i++ {
		writeSizedFile(t, root, fmt.Sprintf("file-%04d", i), 1)
	}
	filesystem := &trackingFS{osFileSystem: osFileSystem{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const cancelAt = uint64(256)
	result := Scan(ctx, root, Options{FileSystem: filesystem, SameFilesystem: allowSameFilesystem}, func(p Progress) {
		if p.Entries >= cancelAt {
			cancel()
		}
	})
	if result.Status != StatusCancelled || result.Root.ApparentKnown {
		t.Fatalf("cancel result = %s, known %v", result.Status, result.Root.ApparentKnown)
	}
	if result.Progress.Entries > cancelAt {
		t.Fatalf("scanner did not stop promptly: processed %d entries after threshold %d", result.Progress.Entries, cancelAt)
	}
	if filesystem.opened != filesystem.closed {
		t.Fatalf("directory handles: opened %d, closed %d", filesystem.opened, filesystem.closed)
	}
}

func TestRootCloseErrorProducesPartialResult(t *testing.T) {
	root := t.TempDir()
	writeSizedFile(t, root, "readable", 1)
	result := Scan(context.Background(), root, Options{
		FileSystem:     closeErrorFS{osFileSystem: osFileSystem{}},
		SameFilesystem: allowSameFilesystem,
	}, nil)
	if result.Status != StatusPartial || result.Errors == 0 || result.Root.ApparentBytes != 1 {
		t.Fatalf("root close error result = status %s errors=%d bytes=%d", result.Status, result.Errors, result.Root.ApparentBytes)
	}
}

func TestNestedCancellationClosesAllDirectoryHandles(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 512; i++ {
		writeSizedFile(t, child, fmt.Sprintf("file-%04d", i), 1)
	}
	filesystem := &trackingFS{osFileSystem: osFileSystem{}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := Scan(ctx, root, Options{FileSystem: filesystem, SameFilesystem: allowSameFilesystem}, func(p Progress) {
		if p.Entries >= 256 {
			cancel()
		}
	})
	if result.Status != StatusCancelled || filesystem.opened != 2 || filesystem.closed != 2 {
		t.Fatalf("nested cancellation cleanup: status=%s opened=%d closed=%d", result.Status, filesystem.opened, filesystem.closed)
	}
}

func TestLimitsBoundDirectoryAndTopEntryCaches(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 6; i++ {
		writeSizedFile(t, root, fmt.Sprintf("file-%d", i), i+1)
	}
	result := Scan(context.Background(), root, Options{
		SameFilesystem:         allowSameFilesystem,
		TopEntriesPerDirectory: 4,
		MaxStoredEntries:       1,
	}, nil)
	if result.Status != StatusComplete || !result.TopEntriesLimited {
		t.Fatalf("cache limit should not make complete totals partial: %s limited=%v", result.Status, result.TopEntriesLimited)
	}
	if result.Root.Files != 6 || result.Root.ApparentBytes != 21 || len(result.Root.TopEntries) != 1 || !result.Root.TopEntriesLimited {
		t.Fatalf("bounded cache/totals: files=%d bytes=%d top=%d limited=%v", result.Root.Files, result.Root.ApparentBytes, len(result.Root.TopEntries), result.Root.TopEntriesLimited)
	}
}

func TestTopEntryCacheKeepsLargestEntries(t *testing.T) {
	root := t.TempDir()
	writeSizedFile(t, root, "a-small", 1)
	writeSizedFile(t, root, "b-medium", 10)
	writeSizedFile(t, root, "c-large", 100)
	result := Scan(context.Background(), root, Options{
		SameFilesystem:         allowSameFilesystem,
		TopEntriesPerDirectory: 2,
		MaxStoredEntries:       10,
	}, nil)
	entries := result.Root.TopEntries
	if len(entries) != 2 {
		t.Fatalf("cached %d entries, want 2", len(entries))
	}
	got := map[string]uint64{}
	for _, entry := range entries {
		got[entry.Name] = entry.ApparentBytes
	}
	if got["a-small"] != 0 || got["b-medium"] != 10 || got["c-large"] != 100 {
		t.Fatalf("top two were not retained: %+v", got)
	}
}

func TestAllocatedTopEntriesAreRetainedSeparately(t *testing.T) {
	if !AllocatedSizeSupported() {
		t.Skip("allocated-size estimates are not supported on this platform")
	}
	root := t.TempDir()
	for i, size := range []int{1, 3000, 9000} {
		writeSizedFile(t, root, fmt.Sprintf("file-%d", i), size)
	}
	result := Scan(context.Background(), root, Options{SameFilesystem: allowSameFilesystem}, nil)
	entries := result.Root.TopAllocatedEntries
	if len(entries) != 3 {
		t.Fatalf("allocated-size cache has %d entries, want 3", len(entries))
	}
	for i := 1; i < len(entries); i++ {
		if entries[i-1].AllocatedBytes < entries[i].AllocatedBytes {
			t.Fatalf("allocated-size cache not largest-first: %d < %d", entries[i-1].AllocatedBytes, entries[i].AllocatedBytes)
		}
	}
}

func TestOverflowAndLargeByteFormatting(t *testing.T) {
	root := t.TempDir()
	overrides := make(map[string]fs.FileInfo)
	for i := 0; i < 3; i++ {
		path := writeSizedFile(t, root, fmt.Sprintf("large-%d", i), 1)
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		overrides[path] = sizeOverride{FileInfo: info, size: math.MaxInt64}
	}
	filesystem := &overrideFS{osFileSystem: osFileSystem{}, lstat: overrides}
	result := Scan(context.Background(), root, Options{FileSystem: filesystem, SameFilesystem: allowSameFilesystem}, nil)
	if result.Root.ApparentBytes != math.MaxUint64 || !result.Root.Overflow || !result.Progress.Overflow {
		t.Fatalf("overflow handling: bytes=%d node=%v progress=%v", result.Root.ApparentBytes, result.Root.Overflow, result.Progress.Overflow)
	}
	if result.Status != StatusPartial || !strings.Contains(result.Error, "overflow") {
		t.Fatalf("overflow result should be visibly partial: status=%s error=%q", result.Status, result.Error)
	}
	if formatted := FormatBytes(math.MaxUint64); !strings.Contains(formatted, "EiB") {
		t.Fatalf("large size format = %q", formatted)
	}
}

func TestFormatBytesSmallAndBoundaryValues(t *testing.T) {
	for n, want := range map[uint64]string{0: "0 B", 1: "1 B", 1024: "1.0 KiB"} {
		if got := FormatBytes(n); got != want {
			t.Errorf("FormatBytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestAlreadyCancelledScanDoesNotClaimCompleteTotals(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := Scan(ctx, t.TempDir(), scanOptions(), nil)
	if result.Status != StatusCancelled || result.Root == nil || result.Root.ApparentKnown {
		t.Fatalf("already-cancelled scan = status %s root %+v", result.Status, result.Root)
	}
}

func TestCancellationWaitsForInFlightReadAndCleansUp(t *testing.T) {
	root := t.TempDir()
	writeSizedFile(t, root, "file", 1)
	filesystem := &blockingFS{osFileSystem: osFileSystem{}, readStarted: make(chan struct{}, 1), allowRead: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	resultCh := make(chan Result, 1)
	go func() {
		resultCh <- Scan(ctx, root, Options{FileSystem: filesystem, SameFilesystem: allowSameFilesystem}, nil)
	}()
	select {
	case <-filesystem.readStarted:
	case <-time.After(time.Second):
		t.Fatal("scan did not begin its directory read")
	}
	cancel()
	select {
	case <-resultCh:
		t.Fatal("scan returned while its in-flight read was deliberately blocked")
	case <-time.After(20 * time.Millisecond):
	}
	close(filesystem.allowRead)
	select {
	case result := <-resultCh:
		if result.Status != StatusCancelled {
			t.Fatalf("result status = %s", result.Status)
		}
	case <-time.After(time.Second):
		t.Fatal("scan did not stop after the in-flight read completed")
	}
	if filesystem.opened != filesystem.closed {
		t.Fatalf("directory handles: opened %d, closed %d", filesystem.opened, filesystem.closed)
	}
}

type directoryAliasFS struct {
	osFileSystem
	alias  string
	target string
}

func (f directoryAliasFS) resolve(path string) string {
	rel, err := filepath.Rel(f.alias, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}
	return filepath.Join(f.target, rel)
}

func (f directoryAliasFS) Lstat(path string) (fs.FileInfo, error) {
	return f.osFileSystem.Lstat(f.resolve(path))
}

func (f directoryAliasFS) OpenDir(path string) (Directory, error) {
	return f.osFileSystem.OpenDir(f.resolve(path))
}

type overrideFS struct {
	osFileSystem
	lstat    map[string]fs.FileInfo
	lstatErr map[string]error
	openErr  map[string]error
}

func (f *overrideFS) Lstat(path string) (fs.FileInfo, error) {
	if err := f.lstatErr[path]; err != nil {
		return nil, err
	}
	if info := f.lstat[path]; info != nil {
		return info, nil
	}
	return f.osFileSystem.Lstat(path)
}

func (f *overrideFS) OpenDir(path string) (Directory, error) {
	if err := f.openErr[path]; err != nil {
		return nil, err
	}
	return f.osFileSystem.OpenDir(path)
}

type sizeOverride struct {
	fs.FileInfo
	size int64
}

func (i sizeOverride) Size() int64 { return i.size }

type closeErrorFS struct{ osFileSystem }

func (f closeErrorFS) OpenDir(path string) (Directory, error) {
	dir, err := f.osFileSystem.OpenDir(path)
	if err != nil {
		return nil, err
	}
	return closeErrorDir{Directory: dir}, nil
}

type closeErrorDir struct{ Directory }

func (d closeErrorDir) Close() error {
	_ = d.Directory.Close()
	return errors.New("injected close error")
}

type trackingFS struct {
	osFileSystem
	opened int
	closed int
}

func (f *trackingFS) OpenDir(path string) (Directory, error) {
	d, err := f.osFileSystem.OpenDir(path)
	if err != nil {
		return nil, err
	}
	f.opened++
	return &trackingDir{Directory: d, closed: func() { f.closed++ }}, nil
}

type trackingDir struct {
	Directory
	closed func()
}

func (d *trackingDir) Close() error {
	err := d.Directory.Close()
	d.closed()
	return err
}

type blockingFS struct {
	osFileSystem
	readStarted chan struct{}
	allowRead   chan struct{}
	opened      int
	closed      int
}

func (f *blockingFS) OpenDir(path string) (Directory, error) {
	d, err := f.osFileSystem.OpenDir(path)
	if err != nil {
		return nil, err
	}
	f.opened++
	return &blockingDir{Directory: d, started: f.readStarted, allow: f.allowRead, closed: func() { f.closed++ }}, nil
}

type blockingDir struct {
	Directory
	started chan struct{}
	allow   chan struct{}
	closed  func()
}

func (d *blockingDir) ReadDir(n int) ([]fs.DirEntry, error) {
	select {
	case d.started <- struct{}{}:
	default:
	}
	<-d.allow
	return d.Directory.ReadDir(n)
}

func (d *blockingDir) Close() error {
	err := d.Directory.Close()
	d.closed()
	return err
}
