// Package foldersize scans a local directory tree without following
// symlinks. It keeps aggregated directory totals and a bounded set of large
// direct entries so callers can browse results without retaining every file.
package foldersize

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ThatOn3Gu7/TwinPane/internal/storage"
)

const (
	defaultTopEntries       = 20
	defaultStoredEntries    = 20_000
	defaultMaxDirectories   = 100_000
	defaultMaxDepth         = 128
	defaultProgressInterval = 250 * time.Millisecond
	readDirBatchSize        = 128
)

// Status describes how much of a result is trustworthy.
type Status uint8

const (
	StatusScanning Status = iota
	StatusComplete
	StatusPartial
	StatusCancelled
	StatusFailed
	StatusBoundarySkipped
	StatusDuplicateSkipped
)

func (s Status) String() string {
	switch s {
	case StatusScanning:
		return "scanning"
	case StatusComplete:
		return "complete"
	case StatusPartial:
		return "partial"
	case StatusCancelled:
		return "cancelled"
	case StatusFailed:
		return "failed"
	case StatusBoundarySkipped:
		return "mount skipped"
	case StatusDuplicateSkipped:
		return "duplicate skipped"
	default:
		return "unknown"
	}
}

// Kind identifies a leaf directory entry. Directories are represented by
// Node values instead.
type Kind uint8

const (
	KindFile Kind = iota
	KindSymlink
	KindSpecial
)

func (k Kind) String() string {
	switch k {
	case KindFile:
		return "file"
	case KindSymlink:
		return "link"
	case KindSpecial:
		return "special"
	default:
		return "unknown"
	}
}

// Entry is a bounded summary of one direct non-directory entry. Nodes retain
// only the largest entries for their directory; all entries still contribute
// to aggregate totals and counts.
type Entry struct {
	Name           string
	Path           string
	Kind           Kind
	ApparentBytes  uint64
	ApparentKnown  bool
	AllocatedBytes uint64
	AllocatedKnown bool
}

// Node contains aggregate values for a directory. DirectoryCount includes
// this directory; Files counts regular-file paths, and Symlinks and Specials
// count those path entries separately. Directory inode metadata is excluded
// from byte totals.
type Node struct {
	Name   string
	Path   string
	Parent *Node

	ApparentBytes  uint64
	AllocatedBytes uint64
	ApparentKnown  bool
	AllocatedKnown bool
	Overflow       bool

	Files         uint64
	Directories   uint64
	Symlinks      uint64
	Specials      uint64
	DirectEntries uint64
	DirectLeaves  uint64
	ErrorCount    uint64
	FirstError    string
	Status        Status

	// TopEntries and TopAllocatedEntries retain separate largest-first
	// samples. Both caches are bounded per directory and globally by Options;
	// aggregate totals are unaffected by this display cache.
	TopEntries          []Entry
	TopAllocatedEntries []Entry
	TopEntriesLimited   bool
	Children            []*Node
}

// Progress is a coarse-grained snapshot. Callers should batch it rather than
// turn every file into a UI message.
type Progress struct {
	CurrentPath    string
	Files          uint64
	Directories    uint64
	Symlinks       uint64
	Specials       uint64
	Entries        uint64
	Errors         uint64
	ApparentBytes  uint64
	AllocatedBytes uint64
	AllocatedKnown bool
	Overflow       bool
	Elapsed        time.Duration
}

// Result describes one non-transactional view of the filesystem. Complete
// means the traversal encountered no scan errors, excluded mount points, or
// arithmetic overflow; it does not mean the tree was an atomic snapshot.
type Result struct {
	Root              *Node
	Status            Status
	Error             string
	Errors            uint64
	MountsSkipped     uint64
	DuplicatesSkipped uint64
	TopEntriesLimited bool
	Progress          Progress
	Elapsed           time.Duration
}

// Directory is the portion of *os.File used for streaming directory reads.
type Directory interface {
	ReadDir(n int) ([]fs.DirEntry, error)
	Stat() (fs.FileInfo, error)
	Close() error
}

// FileSystem is injectable for deterministic error and cancellation tests.
type FileSystem interface {
	Lstat(path string) (fs.FileInfo, error)
	OpenDir(path string) (Directory, error)
}

// SameFilesystem may override platform boundary detection. known=false means
// the scanner cannot safely establish that child is on root's filesystem; it
// then declines to descend and marks the result partial.
type SameFilesystem func(rootPath, childPath string, rootInfo, childInfo fs.FileInfo) (same, known bool)

// Options configures limits and test seams. Zero values use safe defaults.
type Options struct {
	TopEntriesPerDirectory int
	MaxStoredEntries       int
	MaxDirectories         uint64
	MaxDepth               int
	ProgressInterval       time.Duration
	FileSystem             FileSystem
	SameFilesystem         SameFilesystem
}

// Scan recursively calculates folder totals. It never follows a symlink,
// never opens a leaf special file, and checks ctx between directory batches
// and entries. Cancellation returns a StatusCancelled result with the
// accumulated lower-bound totals rather than an error or a false completion.
func Scan(ctx context.Context, root string, options Options, report func(Progress)) Result {
	started := time.Now()
	if ctx == nil {
		ctx = context.Background()
	}
	options = normalizeOptions(options)
	filesystem := options.FileSystem
	if filesystem == nil {
		filesystem = osFileSystem{}
	}
	result := Result{Status: StatusFailed}
	if root == "" {
		result.Error = "no directory selected"
		result.Elapsed = time.Since(started)
		return result
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		result.Error = fmt.Sprintf("cannot resolve directory %q: %v", root, err)
		result.Elapsed = time.Since(started)
		return result
	}
	abs = filepath.Clean(abs)
	info, err := filesystem.Lstat(abs)
	if err != nil {
		result.Error = issueText(abs, err)
		result.Elapsed = time.Since(started)
		return result
	}
	rootNode := newNode(filepath.Base(abs), abs, nil)
	result.Root = rootNode
	if info.Mode()&fs.ModeSymlink != 0 {
		rootNode.Status = StatusFailed
		rootNode.ApparentKnown = false
		rootNode.AllocatedKnown = false
		result.Error = "the selected path is a symbolic link; directory links are not followed"
		rootNode.FirstError = result.Error
		result.Elapsed = time.Since(started)
		return result
	}
	if !info.IsDir() {
		rootNode.Status = StatusFailed
		rootNode.ApparentKnown = false
		rootNode.AllocatedKnown = false
		result.Error = "the selected path is not a directory"
		rootNode.FirstError = result.Error
		result.Elapsed = time.Since(started)
		return result
	}
	rootNode.Directories = 1
	rootNode.Status = StatusScanning

	mounts, mountsKnown := readMounts()
	boundaryCheck := options.SameFilesystem
	if boundaryCheck == nil {
		boundaryCheck = func(rootPath, childPath string, rootInfo, childInfo fs.FileInfo) (bool, bool) {
			return platformSameFilesystem(rootPath, childPath, rootInfo, childInfo, mounts, mountsKnown)
		}
	}
	s := &scanState{
		ctx:             ctx,
		fs:              filesystem,
		options:         options,
		rootPath:        abs,
		rootInfo:        info,
		boundaryCheck:   boundaryCheck,
		visitedDirs:     make(map[string]struct{}),
		started:         started,
		report:          report,
		lastReport:      started,
		progress:        Progress{Directories: 1, AllocatedKnown: platformHasAllocated()},
		storedEntries:   0,
		rootDirectories: 1,
	}
	s.visitedDirs[directoryIdentity(info, abs)] = struct{}{}
	s.publish(true)
	if ctx.Err() != nil {
		s.cancelled = true
		rootNode.Status = StatusCancelled
		rootNode.ApparentKnown = false
		rootNode.AllocatedKnown = false
		result.Status = StatusCancelled
	} else {
		failed := s.walk(rootNode, info, 0, true)
		if failed {
			rootNode.Status = StatusFailed
			result.Status = StatusFailed
			result.Error = rootNode.FirstError
		} else if s.cancelled || ctx.Err() != nil {
			rootNode.Status = StatusCancelled
			rootNode.ApparentKnown = false
			rootNode.AllocatedKnown = false
			result.Status = StatusCancelled
		} else if s.issues > 0 || s.limited || rootNode.Overflow || s.progress.Overflow {
			rootNode.Status = StatusPartial
			rootNode.ApparentKnown = false
			rootNode.AllocatedKnown = false
			result.Status = StatusPartial
		} else {
			rootNode.Status = StatusComplete
			result.Status = StatusComplete
		}
	}
	sortCachedEntries(rootNode)
	result.Errors = s.issues
	result.MountsSkipped = s.mountsSkipped
	result.DuplicatesSkipped = s.duplicatesSkipped
	result.TopEntriesLimited = s.topEntriesLimited
	result.Progress = s.snapshot()
	result.Elapsed = time.Since(started)
	if result.Error == "" {
		result.Error = s.firstError
	}
	if result.Status == StatusPartial && (rootNode.Overflow || s.progress.Overflow) && result.Error == "" {
		result.Error = "byte total overflow; totals are saturated at uint64 maximum"
	}
	if report != nil {
		report(result.Progress)
	}
	return result
}

func normalizeOptions(o Options) Options {
	if o.TopEntriesPerDirectory <= 0 {
		o.TopEntriesPerDirectory = defaultTopEntries
	}
	if o.MaxStoredEntries <= 0 {
		o.MaxStoredEntries = defaultStoredEntries
	}
	if o.MaxDirectories == 0 {
		o.MaxDirectories = defaultMaxDirectories
	}
	if o.MaxDepth <= 0 {
		o.MaxDepth = defaultMaxDepth
	}
	if o.ProgressInterval <= 0 {
		o.ProgressInterval = defaultProgressInterval
	}
	return o
}

func newNode(name, path string, parent *Node) *Node {
	return &Node{
		Name: name, Path: path, Parent: parent, Status: StatusScanning,
		ApparentKnown: true, AllocatedKnown: platformHasAllocated(),
	}
}

type osFileSystem struct{}

func (osFileSystem) Lstat(path string) (fs.FileInfo, error) { return os.Lstat(path) }
func (osFileSystem) OpenDir(path string) (Directory, error) { return openDirectory(path) }

type scanState struct {
	ctx               context.Context
	fs                FileSystem
	options           Options
	rootPath          string
	rootInfo          fs.FileInfo
	boundaryCheck     SameFilesystem
	visitedDirs       map[string]struct{}
	started           time.Time
	report            func(Progress)
	lastReport        time.Time
	progress          Progress
	issues            uint64
	firstError        string
	mountsSkipped     uint64
	duplicatesSkipped uint64
	storedEntries     int
	rootDirectories   uint64
	cancelled         bool
	limited           bool
	topEntriesLimited bool
}

// walk returns true only when the root could not be opened/read at all. A
// child directory failure marks that node and its ancestors partial, then the
// scan continues with siblings.
func (s *scanState) walk(node *Node, expected fs.FileInfo, depth int, root bool) (rootFailure bool) {
	if s.ctx.Err() != nil {
		s.cancelNode(node)
		return false
	}
	if !root && depth > s.options.MaxDepth {
		s.issue(node, "maximum directory depth reached", nil)
		return false
	}
	dir, err := s.fs.OpenDir(node.Path)
	if err != nil {
		if root {
			s.issue(node, issueText(node.Path, err), nil)
			return true
		}
		s.issue(node, issueText(node.Path, err), nil)
		return false
	}
	defer func() {
		if closeErr := dir.Close(); closeErr != nil {
			s.issue(node, issueText(node.Path, closeErr), nil)
		}
	}()
	currentInfo, err := s.fs.Lstat(node.Path)
	if err != nil {
		s.issue(node, issueText(node.Path, err), nil)
		return root
	}
	if currentInfo.Mode()&fs.ModeSymlink != 0 || !os.SameFile(expected, currentInfo) {
		s.issue(node, issueText(node.Path, errors.New("directory changed or became a symbolic link before it could be scanned")), nil)
		return root
	}
	openedInfo, err := dir.Stat()
	if err != nil {
		if root {
			s.issue(node, issueText(node.Path, err), nil)
			return true
		}
		s.issue(node, issueText(node.Path, err), nil)
		return false
	}
	if !openedInfo.IsDir() || !os.SameFile(expected, openedInfo) {
		err = errors.New("directory changed before it could be scanned")
		if root {
			s.issue(node, issueText(node.Path, err), nil)
			return true
		}
		s.issue(node, issueText(node.Path, err), nil)
		return false
	}

	s.progress.CurrentPath = node.Path
	for {
		if s.ctx.Err() != nil {
			s.cancelNode(node)
			return false
		}
		entries, readErr := dir.ReadDir(readDirBatchSize)
		for _, entry := range entries {
			if s.ctx.Err() != nil {
				s.cancelNode(node)
				return false
			}
			path := filepath.Join(node.Path, entry.Name())
			s.progress.CurrentPath = path
			info, err := s.fs.Lstat(path)
			if err != nil {
				node.DirectEntries++
				s.progress.Entries++
				s.issue(node, issueText(path, err), nil)
				s.publish(false)
				continue
			}
			node.DirectEntries++
			s.progress.Entries++
			mode := info.Mode()
			switch {
			case mode&fs.ModeSymlink != 0:
				apparent, apparentKnown := nonnegativeSize(info.Size())
				if !apparentKnown {
					s.issue(node, path+": filesystem reported a negative symbolic-link size", nil)
				}
				s.addLeaf(node, Entry{Name: entry.Name(), Path: path, Kind: KindSymlink,
					ApparentBytes: apparent, ApparentKnown: apparentKnown,
					AllocatedBytes: allocatedSize(info), AllocatedKnown: allocatedKnown(info)})
				node.Symlinks++
				s.progress.Symlinks++
			case info.IsDir():
				s.progress.Directories++
				if s.rootDirectories >= s.options.MaxDirectories {
					s.issue(node, path+": directory count limit reached; subtree was not scanned", nil)
					s.limited = true
					s.publish(true)
					return false
				}
				child := newNode(entry.Name(), path, node)
				child.Directories = 1
				node.Children = append(node.Children, child)
				s.rootDirectories++
				same, known := s.boundaryCheck(s.rootPath, path, s.rootInfo, info)
				if !known || !same {
					reason := "cannot verify filesystem boundary; not descending"
					if known {
						reason = "different filesystem or mount; not descending"
					}
					s.skipBoundary(child, reason)
					s.mergeChild(node, child)
					s.publish(false)
					continue
				}
				identity := directoryIdentity(info, path)
				if _, exists := s.visitedDirs[identity]; exists {
					s.skipDuplicate(child, "directory identity was already scanned")
					s.mergeChild(node, child)
					s.publish(false)
					continue
				}
				s.visitedDirs[identity] = struct{}{}
				if depth+1 > s.options.MaxDepth {
					s.issue(child, "maximum directory depth reached; subtree was not scanned", nil)
					s.mergeChild(node, child)
					continue
				}
				failed := s.walk(child, info, depth+1, false)
				s.mergeChild(node, child)
				if failed {
					s.limited = true
					return false
				}
				if s.ctx.Err() != nil {
					s.cancelNode(node)
					return false
				}
			case mode.IsRegular():
				apparent, apparentKnown := nonnegativeSize(info.Size())
				if !apparentKnown {
					s.issue(node, path+": filesystem reported a negative file size", nil)
				}
				item := Entry{Name: entry.Name(), Path: path, Kind: KindFile,
					ApparentBytes: apparent, ApparentKnown: apparentKnown,
					AllocatedBytes: allocatedSize(info), AllocatedKnown: allocatedKnown(info)}
				s.addLeaf(node, item)
				node.Files++
				s.progress.Files++
			default:
				// FIFOs, sockets, block/character devices and unknown types
				// are never opened. Their stat-reported size is not meaningful.
				s.addLeaf(node, Entry{Name: entry.Name(), Path: path, Kind: KindSpecial,
					ApparentKnown: true, AllocatedKnown: true})
				node.Specials++
				s.progress.Specials++
			}
			s.publish(false)
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				s.issue(node, issueText(node.Path, readErr), nil)
			}
			break
		}
		if len(entries) == 0 {
			break
		}
	}
	if s.ctx.Err() != nil {
		s.cancelNode(node)
		return false
	}
	if node.Status == StatusScanning {
		if node.Overflow {
			node.Status = StatusPartial
		} else {
			node.Status = StatusComplete
		}
	}
	return false
}

func (s *scanState) addLeaf(node *Node, item Entry) {
	node.DirectLeaves++
	s.progress.ApparentBytes = saturatingAdd(s.progress.ApparentBytes, item.ApparentBytes, &s.progress.Overflow)
	node.ApparentBytes = saturatingAdd(node.ApparentBytes, item.ApparentBytes, &node.Overflow)
	if !item.ApparentKnown {
		node.ApparentKnown = false
	}
	if item.AllocatedKnown {
		s.progress.AllocatedBytes = saturatingAdd(s.progress.AllocatedBytes, item.AllocatedBytes, &s.progress.Overflow)
		node.AllocatedBytes = saturatingAdd(node.AllocatedBytes, item.AllocatedBytes, &node.Overflow)
	} else {
		node.AllocatedKnown = false
		s.progress.AllocatedKnown = false
	}
	s.retainTop(node, &node.TopEntries, item, false)
	if item.AllocatedKnown {
		s.retainTop(node, &node.TopAllocatedEntries, item, true)
	}
}

func (s *scanState) retainTop(node *Node, entries *[]Entry, item Entry, byAllocated bool) {
	limit := s.options.TopEntriesPerDirectory
	list := *entries
	if len(list) < limit {
		if s.storedEntries < s.options.MaxStoredEntries {
			*entries = append(list, item)
			s.storedEntries++
		} else {
			node.TopEntriesLimited = true
			s.topEntriesLimited = true
		}
		return
	}
	worst := 0
	for i := 1; i < len(list); i++ {
		if entryLessMetric(list[worst], list[i], byAllocated) {
			worst = i
		}
	}
	if entryLessMetric(item, list[worst], byAllocated) {
		list[worst] = item
	}
}

func sortCachedEntries(root *Node) {
	if root == nil {
		return
	}
	stack := []*Node{root}
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		sort.Slice(node.TopEntries, func(i, j int) bool {
			return entryLessMetric(node.TopEntries[i], node.TopEntries[j], false)
		})
		sort.Slice(node.TopAllocatedEntries, func(i, j int) bool {
			return entryLessMetric(node.TopAllocatedEntries[i], node.TopAllocatedEntries[j], true)
		})
		stack = append(stack, node.Children...)
	}
}

// entryLessMetric orders a metric largest-first and uses a stable name
// tie-break so the retained sample is deterministic for a given traversal.
func entryLessMetric(a, b Entry, byAllocated bool) bool {
	as, bs := a.ApparentBytes, b.ApparentBytes
	if byAllocated {
		as, bs = a.AllocatedBytes, b.AllocatedBytes
	}
	if as != bs {
		return as > bs
	}
	al, bl := strings.ToLower(a.Name), strings.ToLower(b.Name)
	if al != bl {
		return al < bl
	}
	return a.Name < b.Name
}

func (s *scanState) mergeChild(parent, child *Node) {
	parent.Overflow = parent.Overflow || child.Overflow
	parent.Directories = saturatingAdd(parent.Directories, child.Directories, &parent.Overflow)
	parent.Files = saturatingAdd(parent.Files, child.Files, &parent.Overflow)
	parent.Symlinks = saturatingAdd(parent.Symlinks, child.Symlinks, &parent.Overflow)
	parent.Specials = saturatingAdd(parent.Specials, child.Specials, &parent.Overflow)
	if child.Status != StatusDuplicateSkipped {
		parent.ApparentBytes = saturatingAdd(parent.ApparentBytes, child.ApparentBytes, &parent.Overflow)
		parent.AllocatedBytes = saturatingAdd(parent.AllocatedBytes, child.AllocatedBytes, &parent.Overflow)
		if !child.ApparentKnown {
			parent.ApparentKnown = false
		}
		if !child.AllocatedKnown {
			parent.AllocatedKnown = false
		}
	}
	parent.ErrorCount = saturatingAdd(parent.ErrorCount, child.ErrorCount, &parent.Overflow)
	if parent.FirstError == "" {
		parent.FirstError = child.FirstError
	}
	switch child.Status {
	case StatusPartial, StatusBoundarySkipped:
		if parent.Status != StatusCancelled {
			parent.Status = StatusPartial
		}
	case StatusCancelled:
		parent.Status = StatusCancelled
	}
}

func (s *scanState) issue(node *Node, message string, err error) {
	if err != nil {
		message = issueText(message, err)
	}
	if message == "" {
		message = "scan could not include an entry"
	}
	s.issues = saturatingAdd(s.issues, 1, &s.progress.Overflow)
	node.ErrorCount = saturatingAdd(node.ErrorCount, 1, &node.Overflow)
	if node.FirstError == "" {
		node.FirstError = message
	}
	node.ApparentKnown = false
	node.AllocatedKnown = false
	if node.Status != StatusCancelled && node.Status != StatusFailed {
		node.Status = StatusPartial
	}
	if s.progress.Errors < math.MaxUint64 {
		s.progress.Errors++
	}
	if s.firstError == "" {
		s.firstError = message
	}
}

func (s *scanState) skipBoundary(node *Node, reason string) {
	node.Status = StatusBoundarySkipped
	node.ApparentKnown = false
	node.AllocatedKnown = false
	node.FirstError = reason
	node.ErrorCount = 1
	s.issues++
	s.mountsSkipped++
	s.progress.Errors++
	if s.firstError == "" {
		s.firstError = reason + ": " + node.Path
	}
}

func (s *scanState) skipDuplicate(node *Node, reason string) {
	node.Status = StatusDuplicateSkipped
	node.ApparentKnown = false
	node.AllocatedKnown = false
	node.FirstError = reason
	s.duplicatesSkipped++
}

func (s *scanState) cancelNode(node *Node) {
	s.cancelled = true
	node.Status = StatusCancelled
	node.ApparentKnown = false
	node.AllocatedKnown = false
}

func (s *scanState) publish(force bool) {
	if s.report == nil {
		return
	}
	now := time.Now()
	if !force && s.progress.Entries%256 != 0 && now.Sub(s.lastReport) < s.options.ProgressInterval {
		return
	}
	s.lastReport = now
	s.report(s.snapshotAt(now))
}

func (s *scanState) snapshot() Progress { return s.snapshotAt(time.Now()) }

func (s *scanState) snapshotAt(now time.Time) Progress {
	p := s.progress
	p.Elapsed = now.Sub(s.started)
	return p
}

func nonnegativeSize(n int64) (uint64, bool) {
	if n < 0 {
		return 0, false
	}
	return uint64(n), true
}

func saturatingAdd(a, b uint64, overflow *bool) uint64 {
	if math.MaxUint64-a < b {
		if overflow != nil {
			*overflow = true
		}
		return math.MaxUint64
	}
	return a + b
}

func issueText(path string, err error) string {
	if errors.Is(err, fs.ErrPermission) {
		return path + ": permission denied"
	}
	if errors.Is(err, fs.ErrNotExist) {
		return path + ": disappeared or no longer exists"
	}
	return path + ": " + err.Error()
}

// AllocatedSizeSupported reports whether this platform exposes the scanner's
// filesystem-allocation estimate. Per-entry values may still be unavailable.
func AllocatedSizeSupported() bool { return platformHasAllocated() }

func allocatedKnown(info fs.FileInfo) bool {
	_, ok := platformAllocated(info)
	return ok
}

func allocatedSize(info fs.FileInfo) uint64 {
	n, _ := platformAllocated(info)
	return n
}

// FormatBytes formats an unsigned byte count using binary units without
// converting through a signed integer (which would overflow above MaxInt64).
func FormatBytes(n uint64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}
	v := float64(n)
	i := -1
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if v < 10 {
		return fmt.Sprintf("%.1f %s", v, units[i])
	}
	return fmt.Sprintf("%.0f %s", v, units[i])
}

// readMounts uses the Linux mount table when available; other systems fall
// back to device or volume identification in their platform implementation.
func readMounts() ([]storage.Mount, bool) {
	f, err := os.Open(storage.MountInfoPath)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	mounts, err := storage.ParseMountinfo(f)
	return mounts, err == nil && len(mounts) > 0
}
