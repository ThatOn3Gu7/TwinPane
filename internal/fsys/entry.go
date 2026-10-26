// Package fsys contains the filesystem abstraction used by TwinPane.
//
// It is deliberately free of any UI code so that later phases (file
// operations, search, archives, remote backends) can reuse or replace the
// listing logic without touching the interface layer.
package fsys

import (
	"io/fs"
	"time"
)

// Entry describes a single item shown in a panel.
type Entry struct {
	Name    string      // base name as stored on disk
	Path    string      // absolute path
	Size    int64       // size in bytes (of the link itself for symlinks)
	Mode    fs.FileMode // mode from Lstat
	ModTime time.Time

	IsDir    bool // directory, or symlink resolving to a directory
	IsParent bool // synthetic ".." entry

	IsLink     bool
	LinkTarget string // raw link target (may be relative)
	LinkBroken bool   // symlink whose target cannot be resolved

	// StatErr records a per-entry error (e.g. Lstat failed because the file
	// vanished between ReadDir and Lstat). The entry is still listed.
	StatErr error
}

// IsExecutable reports whether a regular file has any execute bit set.
func (e Entry) IsExecutable() bool {
	return !e.IsDir && e.Mode.IsRegular() && e.Mode.Perm()&0o111 != 0
}

// IsHidden reports whether the entry is a dot-file.
func (e Entry) IsHidden() bool {
	return !e.IsParent && len(e.Name) > 0 && e.Name[0] == '.'
}

// SortKey selects how listings are ordered.
type SortKey int

const (
	SortName SortKey = iota
	SortSize
	SortTime
	SortType // by extension ("type")
	sortKeyCount
)

// SortExt is the phase 1/2 name of SortType.
const SortExt = SortType

func (k SortKey) String() string {
	switch k {
	case SortSize:
		return "size"
	case SortTime:
		return "time"
	case SortType:
		return "type"
	default:
		return "name"
	}
}

// Next cycles to the following sort key.
func (k SortKey) Next() SortKey { return (k + 1) % sortKeyCount }

// ParseSortKey maps a persisted sort name ("name", "size", "time", "type")
// to its key. Unknown names report false so callers can fall back to the
// default instead of guessing.
func ParseSortKey(s string) (SortKey, bool) {
	switch s {
	case "size":
		return SortSize, true
	case "time":
		return SortTime, true
	case "type":
		return SortType, true
	case "name":
		return SortName, true
	}
	return SortName, false
}

// DefaultDesc is the natural direction of a key, used when switching to it:
// names and types A→Z, sizes largest first, times newest first.
func (k SortKey) DefaultDesc() bool { return k == SortSize || k == SortTime }

// Options controls how a directory is listed. The zero value keeps the
// historical order: directories grouped before files.
type Options struct {
	ShowHidden bool
	Sort       SortKey
	Desc       bool // descending order (see SortEntries)
	// MixedOrder interleaves files and directories by the sort key instead
	// of grouping directories first (the settings screen's
	// "directories first" toggle maps to !MixedOrder).
	MixedOrder bool
}

// SortLabel describes the order for the UI, e.g. "name ↑" or "size ↓".
func (o Options) SortLabel() string {
	if o.Desc {
		return o.Sort.String() + " ↓"
	}
	return o.Sort.String() + " ↑"
}

// Listing is the result of reading one directory.
type Listing struct {
	Path    string
	Entries []Entry // includes a leading ".." entry unless Path is a root
	Dirs    int     // number of real directories (excluding "..")
	Files   int     // number of non-directories
	Hidden  int     // number of hidden entries filtered out
}

// Lister is the backend interface panels use to read directories. The OS
// implementation lives in OSLister; archives or remote filesystems can provide
// their own implementation in later phases.
type Lister interface {
	List(path string, opts Options) (Listing, error)
}
