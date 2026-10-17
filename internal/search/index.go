package search

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ThatOn3Gu7/TwinPane/internal/proc"
)

// Entry is one indexed file or folder.
type Entry struct {
	Path    string // absolute path
	Rel     string // relative to the search root (OS separators)
	lowRel  string // lower-cased, slash-separated Rel
	lowName string // lower-cased base name
	depth   int
	IsDir   bool
	IsLink  bool
	typed   bool // IsDir/IsLink are known (external tools only report names)
}

// NewEntry makes an index entry for path p below root (exported for tests
// and callers that build an index themselves). ok is false for root itself
// and for paths outside it.
func NewEntry(root, p string) (Entry, bool) {
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == "." || rel == "" || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return Entry{}, false
	}
	slash := filepath.ToSlash(rel)
	return Entry{Path: p, Rel: rel, lowRel: strings.ToLower(slash), lowName: strings.ToLower(filepath.Base(p)),
		depth: strings.Count(slash, "/")}, true
}

// Resolve fills in IsDir/IsLink with lstat (and stat for links) if the
// backend did not report them. Cheap enough for the rows on screen.
func (e *Entry) Resolve() {
	if e.typed {
		return
	}
	e.typed = true
	fi, err := os.Lstat(e.Path)
	if err != nil {
		return
	}
	e.IsLink = fi.Mode()&fs.ModeSymlink != 0
	e.IsDir = fi.IsDir()
	if e.IsLink {
		if st, err := os.Stat(e.Path); err == nil && st.IsDir() {
			e.IsDir = true
		}
	}
}

// Backend names.
const (
	BackendFd      = "fd"
	BackendFind    = "find"
	BackendBuiltin = "built-in"
)

// Backend is a way of listing a directory tree.
type Backend struct {
	Name string // BackendFd, BackendFind or BackendBuiltin
	Path string // executable (external backends)
}

// DetectBackends returns the usable backends in order of preference: fd
// (also installed as fdfind on Debian/Ubuntu), find, then the built-in
// walker, which always works.
func DetectBackends(lookPath func(string) (string, error)) []Backend {
	var out []Backend
	if lookPath != nil {
		for _, name := range []string{"fd", "fdfind"} {
			if p, err := lookPath(name); err == nil {
				out = append(out, Backend{BackendFd, p})
				break
			}
		}
		if p, err := lookPath("find"); err == nil {
			out = append(out, Backend{BackendFind, p})
		}
	}
	return append(out, Backend{Name: BackendBuiltin})
}

// IndexOptions control Index.
type IndexOptions struct {
	ShowHidden bool          // include dot-files and descend into dot-folders
	Backends   []Backend     // tried in order; nil = DetectBackends(proc.LookPath)
	MaxEntries int           // stop after this many (0 = DefaultMaxEntries)
	Interval   time.Duration // batch interval (0 = DefaultInterval)
}

// DefaultMaxEntries bounds memory use for huge trees.
const DefaultMaxEntries = 300000

// IndexUpdate is sent while the index is built; the final one has Done set.
// Entries holds only the new entries; counters are cumulative.
type IndexUpdate struct {
	Entries   []Entry
	Count     int
	Errors    int    // unreadable folders (or error lines from the tool)
	Backend   string // backend producing the entries
	Note      string // why a preferred backend was not used
	Done      bool
	Truncated bool  // stopped at MaxEntries
	Canceled  bool  // stopped through the context
	Err       error // the root itself could not be read
}

type indexer struct {
	ctx      context.Context
	root     string
	opts     IndexOptions
	out      chan<- IndexUpdate
	batch    []Entry
	total    IndexUpdate
	lastSend time.Time
}

func (ix *indexer) send(u IndexUpdate) bool {
	select {
	case ix.out <- u:
		return true
	case <-ix.ctx.Done():
		return false
	}
}

func (ix *indexer) flush() bool {
	u := ix.total
	u.Entries, ix.batch = ix.batch, nil
	ix.lastSend = time.Now()
	return ix.send(u)
}

// add records an entry; false means stop (limit reached or canceled).
func (ix *indexer) add(e Entry) bool {
	ix.batch = append(ix.batch, e)
	ix.total.Count++
	if ix.total.Count >= ix.opts.MaxEntries {
		ix.total.Truncated = true
		return false
	}
	if len(ix.batch) >= 2000 || time.Since(ix.lastSend) >= ix.opts.Interval {
		return ix.flush()
	}
	return ix.ctx.Err() == nil
}

// Index lists every entry below root with the first backend that works and
// streams them to out. Symbolic links are never followed (except the root
// itself) and .gitignore files are ignored: a file manager should find
// everything that is there. A backend that fails without producing anything
// is replaced by the next one (IndexUpdate.Note says why).
func Index(ctx context.Context, root string, opts IndexOptions, out chan<- IndexUpdate) {
	if opts.MaxEntries <= 0 {
		opts.MaxEntries = DefaultMaxEntries
	}
	if opts.Interval <= 0 {
		opts.Interval = DefaultInterval
	}
	if opts.Backends == nil {
		opts.Backends = DetectBackends(proc.LookPath)
	}
	ix := &indexer{ctx: ctx, root: filepath.Clean(root), opts: opts, out: out, lastSend: time.Now()}
	if fi, err := os.Stat(ix.root); err != nil || !fi.IsDir() {
		if err == nil {
			err = fmt.Errorf("%s is not a folder", root)
		}
		ix.total.Err, ix.total.Done = err, true
		ix.send(ix.total)
		return
	}
	var notes []string
	for _, b := range opts.Backends {
		ix.total.Backend = b.Name
		ix.total.Note = strings.Join(notes, "; ")
		var err error
		if b.Name == BackendBuiltin {
			err = ix.walk()
		} else {
			err = ix.external(b)
		}
		if err == nil || ix.total.Count > 0 || ctx.Err() != nil {
			break
		}
		// nothing produced and the tool failed: try the next backend
		notes = append(notes, fmt.Sprintf("%s failed (%v)", b.Name, err))
		ix.total.Errors = 0
		if b.Name == BackendBuiltin {
			ix.total.Err = err
		}
	}
	if ctx.Err() != nil {
		ix.total.Canceled = true
		u := ix.total
		u.Entries, u.Done = ix.batch, true
		select {
		case out <- u:
		default:
		}
		return
	}
	ix.total.Done = true
	ix.flush()
}

// startRoot is the root as passed to the tools: with a trailing separator
// when it is a symbolic link, so that tools which don't follow links still
// descend into it (e.g. Termux's ~/storage/shared).
func startRoot(root string) string {
	if fi, err := os.Lstat(root); err == nil && fi.Mode()&fs.ModeSymlink != 0 && !strings.HasSuffix(root, string(filepath.Separator)) {
		return root + string(filepath.Separator)
	}
	return root
}

// ToolArgs returns the argv for an external backend (exported for tests).
func ToolArgs(b Backend, start string, showHidden bool) []string {
	switch b.Name {
	case BackendFd:
		args := []string{filepath.Base(b.Path), "--print0", "--color", "never", "--no-ignore", "--show-errors"}
		if showHidden {
			args = append(args, "--hidden")
		}
		return append(args, ".", start)
	case BackendFind:
		args := []string{filepath.Base(b.Path), start, "-mindepth", "1"}
		if !showHidden {
			args = append(args, "-name", ".*", "-prune", "-o")
		}
		return append(args, "-print0")
	}
	return nil
}

func (ix *indexer) external(b Backend) error {
	args := ToolArgs(b, startRoot(ix.root), ix.opts.ShowHidden)
	if args == nil {
		return fmt.Errorf("unknown backend %q", b.Name)
	}
	outR, outW, err := os.Pipe()
	if err != nil {
		return err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return err
	}
	// proc.Start, not exec.Cmd: see package proc (Android seccomp).
	child, err := proc.Start(proc.Spec{Path: b.Path, Args: args, Files: []*os.File{nil, outW, errW}})
	outW.Close()
	errW.Close()
	if err != nil {
		outR.Close()
		errR.Close()
		return err
	}
	// error lines (unreadable folders); keep the last one for the report
	type errInfo struct {
		lines int
		last  string
	}
	errDone := make(chan errInfo, 1)
	go func() {
		sc := bufio.NewScanner(errR)
		var ei errInfo
		for sc.Scan() {
			ei.lines++
			ei.last = sc.Text()
		}
		errR.Close()
		errDone <- ei
	}()
	// kill the tool when the search is canceled
	stop := make(chan struct{})
	go func() {
		select {
		case <-ix.ctx.Done():
			child.Kill()
		case <-stop:
		}
	}()

	rd := bufio.NewReaderSize(outR, 64*1024)
	stopped := false
	sep := string(filepath.Separator)
	for {
		rec, err := rd.ReadString(0)
		if len(rec) > 0 && rec[len(rec)-1] == 0 {
			rec = rec[:len(rec)-1]
			isDir := false
			// fd marks folders with a trailing separator (recent versions)
			if len(rec) > 1 && strings.HasSuffix(rec, sep) {
				rec, isDir = strings.TrimRight(rec, sep), true
			}
			if e, ok := NewEntry(ix.root, filepath.Clean(rec)); ok {
				if isDir {
					e.IsDir, e.typed = true, true
					if fi, err := os.Lstat(e.Path); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
						e.IsLink = true
					}
				}
				if !ix.add(e) {
					stopped = true
					break
				}
			}
		}
		if err != nil {
			break
		}
	}
	close(stop)
	if stopped {
		child.Kill()
	}
	outR.Close()
	st, werr := child.Wait()
	ei := <-errDone
	ix.total.Errors += ei.lines
	switch {
	case stopped || ix.ctx.Err() != nil:
		return nil
	case werr != nil:
		return werr
	case !st.Success() && ix.total.Count == 0:
		if ei.last != "" {
			return errors.New(ei.last)
		}
		return errors.New(st.String())
	}
	return nil
}

func (ix *indexer) walk() error {
	walkRoot := startRoot(ix.root)
	stopped := false
	err := filepath.WalkDir(walkRoot, func(p string, d fs.DirEntry, err error) error {
		if ix.ctx.Err() != nil {
			return filepath.SkipAll
		}
		if err != nil {
			if p == walkRoot {
				return err
			}
			ix.total.Errors++
			return nil
		}
		if p == walkRoot {
			return nil
		}
		name := d.Name()
		if !ix.opts.ShowHidden && strings.HasPrefix(name, ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		e, ok := NewEntry(ix.root, filepath.Clean(p))
		if !ok {
			return nil
		}
		e.typed, e.IsDir, e.IsLink = true, d.IsDir(), d.Type()&fs.ModeSymlink != 0
		if e.IsLink {
			if fi, err := os.Stat(p); err == nil && fi.IsDir() {
				e.IsDir = true // shown as a folder, but not descended into
			}
		}
		if !ix.add(e) {
			stopped = true
			return filepath.SkipAll
		}
		return nil
	})
	if stopped || errors.Is(err, filepath.SkipAll) {
		return nil
	}
	return err
}

// ---- matching ----------------------------------------------------------------

// MatchEntry reports whether e matches q (see Query). The zero Query (empty
// pattern) matches everything.
func (q Query) MatchEntry(e *Entry) bool {
	if q.raw == "" {
		return true
	}
	return q.matchLower(e.lowRel, e.lowName)
}

// Score ranks a match: lower is better. Exact name, then name prefix, then
// name substring, then a match only in the folder path. Wildcard and path
// queries rank all matches equally.
func (q Query) Score(e *Entry) int {
	if q.raw == "" || q.glob || q.wantPath {
		return 1
	}
	switch {
	case e.lowName == q.pattern:
		return 0
	case strings.HasPrefix(e.lowName, q.pattern):
		return 1
	case strings.Contains(e.lowName, q.pattern):
		return 2
	}
	return 3
}

// SortEntries orders an index for display: shallower first, then by path.
func SortEntries(es []Entry) {
	sort.SliceStable(es, func(i, j int) bool {
		if es[i].depth != es[j].depth {
			return es[i].depth < es[j].depth
		}
		return es[i].lowRel < es[j].lowRel
	})
}

// Filter appends to dst the indices of es[from:] that match q.
func Filter(es []Entry, q Query, from int, dst []int) []int {
	for i := from; i < len(es); i++ {
		if q.MatchEntry(&es[i]) {
			dst = append(dst, i)
		}
	}
	return dst
}

// Rank sorts matches (indices into es) by Score, keeping the index order
// within a score.
func Rank(es []Entry, q Query, m []int) {
	if q.raw == "" || q.glob || q.wantPath {
		return
	}
	sort.SliceStable(m, func(i, j int) bool { return q.Score(&es[m[i]]) < q.Score(&es[m[j]]) })
}
