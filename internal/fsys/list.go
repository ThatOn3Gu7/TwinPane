package fsys

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// OSLister lists directories on the local filesystem.
type OSLister struct{}

// List reads path and returns its entries sorted according to opts.
// Directories always come before files. A synthetic ".." entry is prepended
// unless path is a filesystem root.
func (OSLister) List(path string, opts Options) (Listing, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Listing{}, err
	}
	abs = filepath.Clean(abs)

	info, err := os.Stat(abs)
	if err != nil {
		return Listing{}, wrapErr(abs, err)
	}
	if !info.IsDir() {
		return Listing{}, fmt.Errorf("%s: not a directory", abs)
	}

	dirEntries, err := os.ReadDir(abs)
	if err != nil {
		return Listing{}, wrapErr(abs, err)
	}

	l := Listing{Path: abs}
	entries := make([]Entry, 0, len(dirEntries))
	for _, de := range dirEntries {
		name := de.Name()
		if !opts.ShowHidden && strings.HasPrefix(name, ".") {
			l.Hidden++
			continue
		}
		e := statEntry(abs, de)
		if e.IsDir {
			l.Dirs++
		} else {
			l.Files++
		}
		entries = append(entries, e)
	}
	SortEntries(entries, opts.Sort, opts.Desc, opts.MixedOrder)

	if !IsRoot(abs) {
		parent := Entry{Name: "..", Path: filepath.Dir(abs), IsDir: true, IsParent: true, Mode: fs.ModeDir}
		entries = append([]Entry{parent}, entries...)
	}
	l.Entries = entries
	return l, nil
}

func statEntry(dir string, de fs.DirEntry) Entry {
	e := Entry{Name: de.Name(), Path: filepath.Join(dir, de.Name())}
	info, err := de.Info()
	if err != nil {
		e.StatErr = err
		e.IsDir = de.IsDir()
		e.Mode = de.Type()
		return e
	}
	e.Size = info.Size()
	e.Mode = info.Mode()
	e.ModTime = info.ModTime()
	e.IsDir = info.IsDir()

	if info.Mode()&fs.ModeSymlink != 0 {
		e.IsLink = true
		if target, err := os.Readlink(e.Path); err == nil {
			e.LinkTarget = target
		}
		if ti, err := os.Stat(e.Path); err == nil {
			e.IsDir = ti.IsDir()
		} else {
			e.LinkBroken = true
		}
	}
	return e
}

// SortEntries sorts entries in place for display:
//
//   - ".." first, then (unless mixed) directories, then everything else,
//     whatever the direction, so folders stay grouped and distinguishable;
//   - within each group by key, ascending or descending (desc);
//   - entries whose metadata for the key is unavailable (stat failed, or the
//     size of a broken link) come last in their group, in either direction;
//   - directories are ordered by name when sorting by size, because their
//     sizes are not meaningful;
//   - ties are broken by name, A→Z (case-insensitive, then byte order), so
//     the order is always deterministic.
//
// mixed interleaves directories with files by the sort key instead of
// grouping them first (Options.MixedOrder).
func SortEntries(entries []Entry, key SortKey, desc, mixed bool) {
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.IsParent != b.IsParent {
			return a.IsParent
		}
		if !mixed && a.IsDir != b.IsDir {
			return a.IsDir
		}
		if c := comparePrimary(a, b, key); c != 0 {
			if c == unknownLast || c == -unknownLast {
				return c < 0
			}
			if desc {
				return c > 0
			}
			return c < 0
		}
		return nameLess(a.Name, b.Name)
	})
}

// unknownLast marks a comparison decided by missing metadata; it is not
// affected by the sort direction.
const unknownLast = 2

// comparePrimary compares by key: -1/0/1, or ±unknownLast when exactly one
// side lacks the metadata (the known side sorts first).
func comparePrimary(a, b Entry, key SortKey) int {
	switch key {
	case SortSize:
		if a.IsDir || b.IsDir {
			// Both are directories: by name. Mixed listings also meet
			// here for a directory against a file, where a size would
			// not be meaningful: fall through to the name tie-break.
			return 0
		}
		ka, kb := sizeKnown(a), sizeKnown(b)
		if ka != kb {
			return knownFirst(ka)
		}
		if ka && a.Size != b.Size {
			return cmp3(a.Size < b.Size)
		}
	case SortTime:
		ka, kb := timeKnown(a), timeKnown(b)
		if ka != kb {
			return knownFirst(ka)
		}
		if ka && !a.ModTime.Equal(b.ModTime) {
			return cmp3(a.ModTime.Before(b.ModTime))
		}
	case SortType:
		ea, eb := TypeOf(a.Name), TypeOf(b.Name)
		if ea != eb {
			return cmp3(ea < eb)
		}
	default: // SortName
		la, lb := strings.ToLower(a.Name), strings.ToLower(b.Name)
		if la != lb {
			return cmp3(la < lb)
		}
	}
	return 0
}

func cmp3(less bool) int {
	if less {
		return -1
	}
	return 1
}

func knownFirst(aKnown bool) int {
	if aKnown {
		return -unknownLast
	}
	return unknownLast
}

func nameLess(a, b string) bool {
	la, lb := strings.ToLower(a), strings.ToLower(b)
	if la != lb {
		return la < lb
	}
	return a < b
}

func sizeKnown(e Entry) bool { return e.StatErr == nil && !(e.IsLink && e.LinkBroken) }

func timeKnown(e Entry) bool { return e.StatErr == nil && !e.ModTime.IsZero() }

// TypeOf returns the lower-cased extension used for sorting by type, without
// the dot; "" for names without one. A leading dot does not start an
// extension (".bashrc" has none, ".config.json" has "json").
func TypeOf(name string) string {
	trimmed := strings.TrimLeft(name, ".")
	ext := filepath.Ext(trimmed)
	if ext == "" || ext == trimmed {
		return ""
	}
	return strings.ToLower(ext[1:])
}

// IsRoot reports whether p is a filesystem root ("/" or "C:\").
func IsRoot(p string) bool {
	p = filepath.Clean(p)
	return filepath.Dir(p) == p
}

// Parent returns the parent directory of p (p itself for a root).
func Parent(p string) string { return filepath.Dir(filepath.Clean(p)) }

// NearestExisting walks up from p until it finds an existing directory.
func NearestExisting(p string) string {
	p = filepath.Clean(p)
	for {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			return p
		}
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		p = parent
	}
}

// HomeDir returns the user's home directory, falling back to the working
// directory and finally the filesystem root.
func HomeDir() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return filepath.Clean(h)
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return string(filepath.Separator)
}

// wrapErr converts low-level errors into short, user-facing messages.
func wrapErr(path string, err error) error {
	switch {
	case errors.Is(err, fs.ErrPermission):
		return fmt.Errorf("permission denied: %s", path)
	case errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("no such directory: %s", path)
	default:
		return err
	}
}
