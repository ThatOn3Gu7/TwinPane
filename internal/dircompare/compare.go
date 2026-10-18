// Package dircompare compares two local directory trees without following
// symlinks. It contains no UI code and performs no writes; callers must build
// and review a Plan before executing any copy actions.
package dircompare

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Kind describes the filesystem object found at a relative path.
type Kind uint8

const (
	KindUnknown Kind = iota
	KindRegular
	KindDirectory
	KindSymlink
	KindSpecial
)

func (k Kind) String() string {
	switch k {
	case KindRegular:
		return "file"
	case KindDirectory:
		return "directory"
	case KindSymlink:
		return "symlink"
	case KindSpecial:
		return "special"
	default:
		return "unknown"
	}
}

// Status is the comparison result for one relative path.
type Status uint8

const (
	StatusUnknown Status = iota
	StatusLeftOnly
	StatusRightOnly
	StatusMetadataEqual
	StatusMetadataDifferent
	StatusContentDifferent
	StatusIdentical
	StatusSameContent
	StatusTypeConflict
	StatusError
)

func (s Status) String() string {
	switch s {
	case StatusLeftOnly:
		return "left only"
	case StatusRightOnly:
		return "right only"
	case StatusMetadataEqual:
		return "same metadata"
	case StatusMetadataDifferent:
		return "metadata differs"
	case StatusContentDifferent:
		return "content differs"
	case StatusIdentical:
		return "identical"
	case StatusSameContent:
		return "same content, metadata differs"
	case StatusTypeConflict:
		return "type conflict"
	case StatusError:
		return "read error"
	default:
		return "unknown"
	}
}

// Snapshot is the no-follow metadata captured for one entry. Its filesystem
// identity is retained privately so execution can reject a path replaced
// after review, even when the replacement happens to have the same size and
// timestamp.
type Snapshot struct {
	Kind         Kind
	Mode         fs.FileMode
	Size         int64
	ModTime      time.Time
	LinkTarget   string
	Hash         [sha256.Size]byte
	HashCompared bool
	info         fs.FileInfo
}

// Side is one panel's view of an entry. Snapshot is nil when the entry was
// absent or unreadable; Error distinguishes an unreadable entry from absence.
type Side struct {
	Snapshot *Snapshot
	Error    string
}

// Entry is one relative path in the union of both trees. Rel always uses
// slash separators so a result is stable across platforms.
type Entry struct {
	Rel    string
	Left   Side
	Right  Side
	Status Status
}

// ScanError records an entry or directory that could not be inspected.
type ScanError struct {
	Side string // "left" or "right"
	Rel  string
	Err  string
}

// Totals summarizes the rows shown by the comparison view.
type Totals struct {
	Entries               int
	LeftOnly              int
	RightOnly             int
	MetadataEqual         int
	MetadataDifferent     int
	ContentDifferent      int
	Identical             int
	SameContent           int
	TypeConflicts         int
	Errors                int
	LeftBytes, RightBytes int64
}

// Result is an immutable-by-convention snapshot of both trees. Entries are
// sorted by relative path. Complete is false if any entry disappeared or
// could not be read; incomplete results may be viewed but cannot be planned.
type Result struct {
	LeftRoot, RightRoot string
	Entries             []Entry
	Errors              []ScanError
	Totals              Totals
	Complete            bool
	CanSync             bool // false for equal or overlapping canonical roots
	SyncNote            string
	ContentChecked      bool
	leftRootInfo        fs.FileInfo
	rightRootInfo       fs.FileInfo
}

// Options controls optional directory-comparison behavior. HashMatchingFiles
// enables streaming SHA-256 checks for regular files whose size and modification
// time match. Files are revalidated before and after hashing; symlinks are never
// opened. Git directories are excluded unless IncludeGitDirectories is true.
type Options struct {
	HashMatchingFiles     bool
	IncludeGitDirectories bool
}

// filesystem seams are intentionally narrow: tests use them to model
// disappearing and unreadable entries deterministically.
var (
	lstatPath = os.Lstat
	openPath  = os.Open
	readDir   = func(f *os.File) ([]os.DirEntry, error) { return f.ReadDir(-1) }
	readLink  = os.Readlink
)

// Compare reads both local directory trees. It resolves the chosen roots
// once, but never follows a symlink found below either root. Root errors are
// returned directly; entry errors are recorded in Result and make it
// incomplete so no synchronization plan can be built from guessed absences.
func Compare(ctx context.Context, leftRoot, rightRoot string, opts Options) (Result, error) {
	var result Result
	left, leftInfo, err := resolveRoot(leftRoot)
	if err != nil {
		return result, fmt.Errorf("left directory: %w", err)
	}
	right, rightInfo, err := resolveRoot(rightRoot)
	if err != nil {
		return result, fmt.Errorf("right directory: %w", err)
	}
	result.LeftRoot, result.RightRoot = left, right
	result.leftRootInfo, result.rightRootInfo = leftInfo, rightInfo
	result.CanSync = !rootsOverlap(left, right, leftInfo, rightInfo)
	if !result.CanSync {
		result.SyncNote = "The selected directories are the same or overlap; comparison is read-only."
	}
	result.ContentChecked = opts.HashMatchingFiles

	leftEntries := make(map[string]Side)
	rightEntries := make(map[string]Side)
	var scanErrors []ScanError
	if err := scanTree(ctx, left, "left", opts, leftEntries, &scanErrors); err != nil {
		return result, err
	}
	if err := scanTree(ctx, right, "right", opts, rightEntries, &scanErrors); err != nil {
		return result, err
	}

	all := make(map[string]struct{}, len(leftEntries)+len(rightEntries))
	for rel := range leftEntries {
		all[rel] = struct{}{}
	}
	for rel := range rightEntries {
		all[rel] = struct{}{}
	}
	for _, e := range scanErrors {
		if e.Rel != "" {
			all[e.Rel] = struct{}{}
		}
	}
	rels := make([]string, 0, len(all))
	for rel := range all {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	result.Entries = make([]Entry, 0, len(rels))
	rootErrors := map[string]string{}
	for _, scanErr := range scanErrors {
		if scanErr.Rel == "" {
			rootErrors[scanErr.Side] = scanErr.Err
		}
	}
	for _, rel := range rels {
		entry := Entry{Rel: rel, Left: leftEntries[rel], Right: rightEntries[rel]}
		// A failed directory read means all descendants on that side are
		// unknown, not absent. Propagate the error through the subtree so
		// the display never suggests a one-sided copy from an incomplete scan.
		for _, scanErr := range scanErrors {
			if scanErr.Side == "left" && (scanErr.Rel == "" || rel == scanErr.Rel || strings.HasPrefix(rel, scanErr.Rel+"/")) {
				entry.Left.Error = scanErr.Err
			}
			if scanErr.Side == "right" && (scanErr.Rel == "" || rel == scanErr.Rel || strings.HasPrefix(rel, scanErr.Rel+"/")) {
				entry.Right.Error = scanErr.Err
			}
		}
		if entry.Left.Snapshot == nil && entry.Left.Error == "" && rootErrors["left"] != "" {
			entry.Left.Error = rootErrors["left"]
		}
		if entry.Right.Snapshot == nil && entry.Right.Error == "" && rootErrors["right"] != "" {
			entry.Right.Error = rootErrors["right"]
		}
		entry.Status = classify(entry)
		if opts.HashMatchingFiles && entry.Left.Snapshot != nil && entry.Right.Snapshot != nil &&
			entry.Left.Snapshot.Kind == KindRegular && entry.Right.Snapshot.Kind == KindRegular &&
			entry.Left.Snapshot.Size == entry.Right.Snapshot.Size && entry.Left.Snapshot.ModTime.Equal(entry.Right.Snapshot.ModTime) {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			lh, err := hashSnapshot(ctx, left, rel, *entry.Left.Snapshot)
			if err != nil {
				entry.Left.Error = err.Error()
				addScanError(&scanErrors, "left", rel, err)
				entry.Status = StatusError
			} else {
				rh, err := hashSnapshot(ctx, right, rel, *entry.Right.Snapshot)
				if err != nil {
					entry.Right.Error = err.Error()
					addScanError(&scanErrors, "right", rel, err)
					entry.Status = StatusError
				} else {
					entry.Left.Snapshot.Hash, entry.Left.Snapshot.HashCompared = lh, true
					entry.Right.Snapshot.Hash, entry.Right.Snapshot.HashCompared = rh, true
					if lh != rh {
						entry.Status = StatusContentDifferent
					} else if metadataEqual(*entry.Left.Snapshot, *entry.Right.Snapshot) {
						entry.Status = StatusIdentical
					} else {
						entry.Status = StatusSameContent
					}
				}
			}
		}
		result.Entries = append(result.Entries, entry)
	}
	result.Errors = append([]ScanError(nil), scanErrors...)
	sort.Slice(result.Errors, func(i, j int) bool {
		if result.Errors[i].Rel != result.Errors[j].Rel {
			return result.Errors[i].Rel < result.Errors[j].Rel
		}
		return result.Errors[i].Side < result.Errors[j].Side
	})
	result.Complete = len(result.Errors) == 0
	result.Totals = countTotals(result.Entries)
	return result, nil
}

func resolveRoot(path string) (string, fs.FileInfo, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil, errors.New("path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", nil, err
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(abs))
	if err != nil {
		return "", nil, err
	}
	real = filepath.Clean(real)
	info, err := lstatPath(real)
	if err != nil {
		return "", nil, err
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return "", nil, fmt.Errorf("%s is not a real directory", real)
	}
	return real, info, nil
}

func rootsOverlap(left, right string, leftInfo, rightInfo fs.FileInfo) bool {
	if leftInfo != nil && rightInfo != nil && os.SameFile(leftInfo, rightInfo) {
		return true
	}
	return pathContains(left, right) || pathContains(right, left)
}

func pathContains(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}

func scanTree(ctx context.Context, root, side string, opts Options, entries map[string]Side, errs *[]ScanError) error {
	return scanDirectory(ctx, root, root, "", side, opts, entries, errs)
}

func scanDirectory(ctx context.Context, root, path, rel, side string, opts Options, entries map[string]Side, errs *[]ScanError) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	before, err := lstatPath(path)
	if err != nil {
		recordError(entries, errs, side, rel, err)
		return nil
	}
	if before.Mode()&fs.ModeSymlink != 0 || !before.IsDir() {
		recordError(entries, errs, side, rel, errors.New("directory changed or became a symlink during comparison"))
		return nil
	}
	f, err := openPath(path)
	if err != nil {
		recordError(entries, errs, side, rel, err)
		return nil
	}
	opened, statErr := f.Stat()
	if statErr != nil || !opened.IsDir() || !os.SameFile(before, opened) {
		_ = f.Close()
		if statErr == nil {
			statErr = errors.New("directory changed while it was being opened")
		}
		recordError(entries, errs, side, rel, statErr)
		return nil
	}
	children, readErr := readDir(f)
	_ = f.Close()
	if readErr != nil {
		recordError(entries, errs, side, rel, readErr)
		return nil
	}
	for _, child := range children {
		if err := ctx.Err(); err != nil {
			return err
		}
		name := child.Name()
		childRel := name
		if rel != "" {
			childRel = filepath.ToSlash(filepath.Join(filepath.FromSlash(rel), name))
		}
		childPath := filepath.Join(path, name)
		info, err := lstatPath(childPath)
		if err != nil {
			recordError(entries, errs, side, childRel, err)
			continue
		}
		snap := snapshot(info)
		if name == ".git" && snap.Kind == KindDirectory && !opts.IncludeGitDirectories {
			continue
		}
		if snap.Kind == KindSymlink {
			target, err := readLink(childPath)
			if err != nil {
				recordError(entries, errs, side, childRel, err)
				continue
			}
			snap.LinkTarget = target
			after, err := lstatPath(childPath)
			if err != nil || !sameSnapshot(after, snap, childPath) {
				if err == nil {
					err = errors.New("symlink changed while it was being read")
				}
				recordError(entries, errs, side, childRel, err)
				continue
			}
		}
		entries[childRel] = Side{Snapshot: &snap}
		if snap.Kind == KindDirectory {
			if err := scanDirectory(ctx, root, childPath, childRel, side, opts, entries, errs); err != nil {
				return err
			}
		}
	}
	return nil
}

func snapshot(info fs.FileInfo) Snapshot {
	kind := KindSpecial
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		kind = KindSymlink
	case info.IsDir():
		kind = KindDirectory
	case info.Mode().IsRegular():
		kind = KindRegular
	}
	return Snapshot{Kind: kind, Mode: info.Mode(), Size: info.Size(), ModTime: info.ModTime(), info: info}
}

func classify(entry Entry) Status {
	if entry.Left.Error != "" || entry.Right.Error != "" {
		return StatusError
	}
	left, right := entry.Left.Snapshot, entry.Right.Snapshot
	switch {
	case left == nil && right == nil:
		return StatusError
	case right == nil:
		return StatusLeftOnly
	case left == nil:
		return StatusRightOnly
	case left.Kind != right.Kind:
		return StatusTypeConflict
	case metadataEqual(*left, *right):
		return StatusMetadataEqual
	default:
		return StatusMetadataDifferent
	}
}

func metadataEqual(left, right Snapshot) bool {
	if left.Kind != right.Kind || left.Mode != right.Mode || !left.ModTime.Equal(right.ModTime) {
		return false
	}
	switch left.Kind {
	case KindDirectory:
		// Directory size is filesystem-dependent and doesn't describe its
		// children; the child paths are compared independently.
		return true
	case KindSymlink:
		return left.Size == right.Size && left.LinkTarget == right.LinkTarget
	default:
		return left.Size == right.Size
	}
}

func countTotals(entries []Entry) Totals {
	out := Totals{Entries: len(entries)}
	for _, entry := range entries {
		if entry.Left.Snapshot != nil && entry.Left.Snapshot.Kind == KindRegular {
			out.LeftBytes += entry.Left.Snapshot.Size
		}
		if entry.Right.Snapshot != nil && entry.Right.Snapshot.Kind == KindRegular {
			out.RightBytes += entry.Right.Snapshot.Size
		}
		switch entry.Status {
		case StatusLeftOnly:
			out.LeftOnly++
		case StatusRightOnly:
			out.RightOnly++
		case StatusMetadataEqual:
			out.MetadataEqual++
		case StatusMetadataDifferent:
			out.MetadataDifferent++
		case StatusContentDifferent:
			out.ContentDifferent++
		case StatusIdentical:
			out.Identical++
		case StatusSameContent:
			out.SameContent++
			out.MetadataDifferent++
		case StatusTypeConflict:
			out.TypeConflicts++
		case StatusError:
			out.Errors++
		}
	}
	return out
}

func addScanError(errs *[]ScanError, side, rel string, err error) {
	*errs = append(*errs, ScanError{Side: side, Rel: rel, Err: err.Error()})
}

func recordError(entries map[string]Side, errs *[]ScanError, side, rel string, err error) {
	if rel != "" {
		entry := entries[rel]
		entry.Error = err.Error()
		entries[rel] = entry
	}
	addScanError(errs, side, rel, err)
}

func hashSnapshot(ctx context.Context, root, rel string, expected Snapshot) ([sha256.Size]byte, error) {
	var zero [sha256.Size]byte
	path, err := pathFor(root, rel)
	if err != nil {
		return zero, err
	}
	if err := validateAncestors(root, rel); err != nil {
		return zero, err
	}
	before, err := lstatPath(path)
	if err != nil {
		return zero, err
	}
	if !sameSnapshot(before, expected, path) || expected.Kind != KindRegular {
		return zero, errors.New("file changed before content comparison")
	}
	f, err := openPath(path)
	if err != nil {
		return zero, err
	}
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		_ = f.Close()
		if err == nil {
			err = errors.New("file changed while it was being opened")
		}
		return zero, err
	}
	h := sha256.New()
	_, copyErr := io.Copy(h, contextReader{ctx: ctx, r: f})
	afterOpen, statErr := f.Stat()
	closeErr := f.Close()
	if copyErr != nil {
		return zero, copyErr
	}
	if statErr != nil {
		return zero, statErr
	}
	if closeErr != nil {
		return zero, closeErr
	}
	after, err := lstatPath(path)
	if err != nil {
		return zero, err
	}
	if !sameSnapshot(after, expected, path) || !os.SameFile(opened, afterOpen) || !os.SameFile(opened, after) {
		return zero, errors.New("file changed during content comparison")
	}
	copy(zero[:], h.Sum(nil))
	return zero, nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func pathFor(root, rel string) (string, error) {
	if err := validateRel(rel); err != nil {
		return "", err
	}
	return filepath.Join(root, filepath.FromSlash(rel)), nil
}

func validateRel(rel string) error {
	if rel == "" || filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		return errors.New("invalid relative path")
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return errors.New("relative path escapes its directory")
	}
	return nil
}

func validateAncestors(root, rel string) error {
	if err := validateRel(rel); err != nil {
		return err
	}
	parts := strings.Split(filepath.FromSlash(rel), string(filepath.Separator))
	current := root
	for _, part := range parts[:len(parts)-1] {
		current = filepath.Join(current, part)
		info, err := lstatPath(current)
		if err != nil {
			return err
		}
		if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("parent %s is not a real directory", current)
		}
	}
	return nil
}

func sameSnapshot(info fs.FileInfo, expected Snapshot, path string) bool {
	if info == nil || expected.info == nil || !os.SameFile(info, expected.info) {
		return false
	}
	current := snapshot(info)
	if current.Kind != expected.Kind || current.Mode != expected.Mode || current.Size != expected.Size || !current.ModTime.Equal(expected.ModTime) {
		return false
	}
	if expected.Kind == KindSymlink {
		target, err := readLink(path)
		return err == nil && target == expected.LinkTarget
	}
	return true
}

// ValidateSnapshot checks that root/rel still refers to the exact no-follow
// entry captured by a comparison. It also rejects symlinked parent folders.
// The check is intended to run immediately before each reviewed operation.
func ValidateSnapshot(root, rel string, expected Snapshot) error {
	return ValidateSnapshotContext(context.Background(), root, rel, expected)
}

// ValidateSnapshotContext is ValidateSnapshot with a cancellable content
// recheck for snapshots produced with HashMatchingFiles enabled.
func ValidateSnapshotContext(ctx context.Context, root, rel string, expected Snapshot) error {
	path, err := pathFor(root, rel)
	if err != nil {
		return err
	}
	if err := validateAncestors(root, rel); err != nil {
		if errors.Is(err, fs.ErrNotExist) || strings.Contains(err.Error(), "not a real directory") {
			return fmt.Errorf("%w: %v", ErrChangedSinceReview, err)
		}
		return err
	}
	info, err := lstatPath(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: %s disappeared", ErrChangedSinceReview, rel)
		}
		return err
	}
	if !sameSnapshot(info, expected, path) {
		return fmt.Errorf("%w: %s changed since comparison", ErrChangedSinceReview, rel)
	}
	if expected.HashCompared {
		got, err := hashSnapshot(ctx, root, rel, expected)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			if errors.Is(err, fs.ErrNotExist) || strings.Contains(err.Error(), "changed") || strings.Contains(err.Error(), "not a real directory") {
				return fmt.Errorf("%w: %v", ErrChangedSinceReview, err)
			}
			return err
		}
		if got != expected.Hash {
			return fmt.Errorf("%w: %s content changed since comparison", ErrChangedSinceReview, rel)
		}
	}
	return nil
}

// validateRootIdentity rejects a root replaced after comparison.
func validateRootIdentity(root string, expected fs.FileInfo) error {
	if expected == nil {
		return errors.New("comparison has no root identity")
	}
	info, err := lstatPath(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("%w: directory %s disappeared", ErrChangedSinceReview, root)
		}
		return err
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() || !os.SameFile(info, expected) {
		return fmt.Errorf("%w: directory %s changed since comparison", ErrChangedSinceReview, root)
	}
	return nil
}
