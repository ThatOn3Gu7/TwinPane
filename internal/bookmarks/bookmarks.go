// Package bookmarks stores the user's bookmarked folders.
//
// The list is a small JSON file ({"version":1,"bookmarks":[{name,path}]})
// in TwinPane's configuration folder:
//
//   - $TWINPANE_CONFIG_DIR when set;
//   - otherwise os.UserConfigDir()/twinpane ($XDG_CONFIG_HOME/twinpane or
//     ~/.config/twinpane; in Termux $HOME is the app's private home, so
//     this is ~/.config/twinpane there too);
//   - otherwise $HOME/.twinpane.
//
// Every change re-reads the file first (another TwinPane may have changed
// it), then writes a temporary file, syncs it and renames it over the old
// one, so the list is never left half-written. A file that cannot be
// parsed is never overwritten: changes are refused until the user fixes or
// removes it.
package bookmarks

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// Version is the file format version written.
const Version = 1

// FileName is the bookmarks file inside the configuration folder.
const FileName = "bookmarks.json"

// MaxName is the longest bookmark name kept (in runes).
const MaxName = 64

// Bookmark is one bookmarked folder.
type Bookmark struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type file struct {
	Version   int        `json:"version"`
	Bookmarks []Bookmark `json:"bookmarks"`
}

// Errors returned by Store methods.
var (
	ErrDuplicate = errors.New("already bookmarked")
	ErrNotFound  = errors.New("no such bookmark")
)

// CorruptError reports a bookmarks file that could not be parsed.
type CorruptError struct {
	Path string
	Err  error
}

func (e *CorruptError) Error() string {
	return fmt.Sprintf("the bookmarks file %s is damaged (%v): fix or delete it — TwinPane will not overwrite it", e.Path, e.Err)
}

func (e *CorruptError) Unwrap() error { return e.Err }

// ConfigDir returns TwinPane's configuration folder (see the package
// documentation). getenv and userConfigDir are os.Getenv and
// os.UserConfigDir in production.
func ConfigDir(getenv func(string) string, userConfigDir func() (string, error)) (string, error) {
	if d := getenv("TWINPANE_CONFIG_DIR"); d != "" {
		if !filepath.IsAbs(d) {
			return "", fmt.Errorf("TWINPANE_CONFIG_DIR=%q is not an absolute path", d)
		}
		return filepath.Clean(d), nil
	}
	if d, err := userConfigDir(); err == nil && filepath.IsAbs(d) {
		return filepath.Join(d, "twinpane"), nil
	}
	if h := getenv("HOME"); h != "" && filepath.IsAbs(h) {
		return filepath.Join(h, ".twinpane"), nil
	}
	return "", errors.New("no configuration folder: set HOME or TWINPANE_CONFIG_DIR")
}

// DefaultPath returns the bookmarks file of this process's environment.
func DefaultPath() (string, error) {
	d, err := ConfigDir(os.Getenv, os.UserConfigDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(d, FileName), nil
}

// Store is the bookmark list backed by one file.
type Store struct {
	path  string
	items []Bookmark
}

// Open reads the bookmarks at path. A missing file is an empty list. A
// damaged file returns a *CorruptError together with a usable (empty, but
// read-only) store.
func Open(path string) (*Store, error) {
	s := &Store{path: path}
	items, err := read(path)
	s.items = items
	return s, err
}

// Path returns the file the store uses.
func (s *Store) Path() string { return s.path }

// List returns the bookmarks in order.
func (s *Store) List() []Bookmark {
	out := make([]Bookmark, len(s.items))
	copy(out, s.items)
	return out
}

// Reload re-reads the file.
func (s *Store) Reload() error {
	items, err := read(s.path)
	if err != nil {
		return err
	}
	s.items = items
	return nil
}

// Find returns the index of the bookmark for folder dir (by Same).
func (s *Store) Find(dir string) (int, bool) {
	return find(s.items, dir)
}

func find(items []Bookmark, dir string) (int, bool) {
	for i, b := range items {
		if Same(b.Path, dir) {
			return i, true
		}
	}
	return -1, false
}

// Add bookmarks folder dir under name ("" = the folder's name). A folder
// that is already bookmarked (Same) returns ErrDuplicate with that
// bookmark.
func (s *Store) Add(dir, name string) (Bookmark, error) {
	clean, err := Normalize(dir)
	if err != nil {
		return Bookmark{}, err
	}
	if name = CleanName(name); name == "" {
		name = DefaultName(clean)
	}
	var added Bookmark
	err = s.update(func(items []Bookmark) ([]Bookmark, error) {
		if i, ok := find(items, clean); ok {
			added = items[i]
			return nil, ErrDuplicate
		}
		added = Bookmark{Name: name, Path: clean}
		return append(items, added), nil
	})
	return added, err
}

// Rename renames the bookmark for path.
func (s *Store) Rename(path, name string) error {
	if name = CleanName(name); name == "" {
		return errors.New("the name is empty")
	}
	return s.update(func(items []Bookmark) ([]Bookmark, error) {
		i, ok := find(items, path)
		if !ok {
			return nil, ErrNotFound
		}
		items[i].Name = name
		return items, nil
	})
}

// Remove deletes the bookmark for path (the folder itself is not touched).
func (s *Store) Remove(path string) error {
	return s.update(func(items []Bookmark) ([]Bookmark, error) {
		i, ok := find(items, path)
		if !ok {
			return nil, ErrNotFound
		}
		return append(items[:i], items[i+1:]...), nil
	})
}

// update applies change to the current file contents and saves the result.
func (s *Store) update(change func([]Bookmark) ([]Bookmark, error)) error {
	items, err := read(s.path)
	if err != nil {
		return err // a damaged file is never overwritten
	}
	next, err := change(items)
	if err != nil {
		s.items = items
		return err
	}
	if err := write(s.path, next); err != nil {
		s.items = items
		return err
	}
	s.items = next
	return nil
}

func read(path string) ([]Bookmark, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		return nil, nil // an empty file (e.g. created by hand) is an empty list
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, &CorruptError{Path: path, Err: err}
	}
	if f.Version > Version {
		return nil, &CorruptError{Path: path, Err: fmt.Errorf("written by a newer TwinPane (format %d)", f.Version)}
	}
	var out []Bookmark
	for _, bm := range f.Bookmarks {
		if !filepath.IsAbs(bm.Path) {
			continue // ignore entries that are not absolute paths
		}
		bm.Path = filepath.Clean(bm.Path)
		if bm.Name = CleanName(bm.Name); bm.Name == "" {
			bm.Name = DefaultName(bm.Path)
		}
		if _, dup := find(out, bm.Path); dup {
			continue
		}
		out = append(out, bm)
	}
	return out, nil
}

// write saves items atomically: temporary file, fsync, rename (mode 0600).
func write(path string, items []Bookmark) error {
	if items == nil {
		items = []Bookmark{}
	}
	data, err := json.MarshalIndent(file{Version: Version, Bookmarks: items}, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("cannot create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".bookmarks-*.tmp")
	if err != nil {
		return fmt.Errorf("cannot write in %s: %w", dir, err)
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(name)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	if d, err := os.Open(dir); err == nil {
		d.Sync() // make the rename durable where supported
		d.Close()
	}
	return nil
}

// Normalize returns the cleaned absolute form of dir.
func Normalize(dir string) (string, error) {
	if dir == "" {
		return "", errors.New("no folder")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

// Same reports whether a and b are the same folder: equal cleaned paths,
// or paths that resolve (through symbolic links) to the same place.
func Same(a, b string) bool {
	ca, err1 := Normalize(a)
	cb, err2 := Normalize(b)
	if err1 != nil || err2 != nil {
		return false
	}
	if ca == cb {
		return true
	}
	ra, err1 := filepath.EvalSymlinks(ca)
	rb, err2 := filepath.EvalSymlinks(cb)
	return err1 == nil && err2 == nil && ra == rb
}

// DefaultName is the name a new bookmark gets: the folder's own name.
func DefaultName(dir string) string {
	if dir == string(filepath.Separator) {
		return dir
	}
	return CleanName(filepath.Base(dir))
}

// CleanName trims a name, replaces control characters and limits its
// length.
func CleanName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > MaxName {
		name = string(r[:MaxName])
	}
	return name
}

// State describes whether a bookmarked folder can be opened.
type State int

const (
	OK         State = iota
	Missing          // the path does not exist
	NotFolder        // the path exists but is not a folder
	Unreadable       // it could not be checked (e.g. permission denied)
)

// Check reports the state of a bookmarked path.
func Check(path string) (State, error) {
	fi, err := os.Stat(path)
	switch {
	case err == nil && fi.IsDir():
		return OK, nil
	case err == nil:
		return NotFolder, nil
	case errors.Is(err, fs.ErrNotExist):
		return Missing, err
	}
	return Unreadable, err
}
