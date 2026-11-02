package ops

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Trash
//
// Delete is reversible: items move to a trash the application owns and keeps,
// with an index that survives restarts, so a deletion can be undone from the
// Trash screen (T) and old items can be removed on a schedule the user sets.
//
// The layout is deliberately small and self-describing:
//
//	<dir>/data/<id>          the item itself, moved with rename(2)
//	<dir>/info/<id>.json     where it came from, when, how big, file or folder
//
// The id is unique (a timestamp plus a counter), so two items with the same
// name never collide and a restore can never overwrite anything. Only items on
// the same filesystem as the trash are accepted, because the move has to be a
// rename: an item that would need copying is reported as not eligible and the
// confirmation says it will be deleted permanently instead.
//
// Every platform uses this one store, because Android has no system trash and
// Windows' Recycle Bin needs COM. The default location follows the platform's
// conventions (see DefaultTrashDir), so a Termux user finds the trash in the
// usual data directory of their home folder.

// Retention bounds for the trash, mirrored by the Settings screen.
const (
	TrashKeepDaysMin = 1
	TrashKeepDaysDef = 29
	TrashKeepDaysMax = 365
)

// TrashCapShare is the fraction of the filesystem's free space the trash may
// take before the oldest items are removed. A tenth keeps a phone usable while
// still holding weeks of ordinary deletions.
const TrashCapShare = 10

// Trasher moves items to a recoverable trash.
type Trasher interface {
	// Name is a short user-facing description of the trash location.
	Name() string
	// Available reports whether path can be trashed (and why not).
	Available(path string) (bool, string)
	// Trash moves path into the trash. It must never fall back to deleting.
	Trash(path string) error
}

// TrashEntry is one item in the trash.
type TrashEntry struct {
	ID        string    // store-private name (unique)
	Name      string    // the item's name when it was deleted
	Origin    string    // absolute path it was deleted from
	DeletedAt time.Time // when it was deleted
	Bytes     int64     // size of the item (0 when it could not be measured)
	Dir       bool      // it was a folder
}

// TrashStore is the managed trash. The zero value is not usable: call
// OpenTrash (or DefaultTrasher, which opens the platform's location).
type TrashStore struct {
	Dir string
	// Now is the clock (default time.Now); tests pin it.
	Now func() time.Time
	// ID names a new entry (default: timestamp + counter); tests pin it.
	ID func(time.Time) string
}

// DefaultTrashDir is where the trash lives on this platform.
func DefaultTrashDir() string {
	home := homeDirFn()
	if runtime.GOOS == "windows" {
		if base := os.Getenv("LOCALAPPDATA"); base != "" && filepath.IsAbs(base) {
			return filepath.Join(base, "TwinPane", "trash")
		}
		if home != "" {
			return filepath.Join(home, "AppData", "Local", "TwinPane", "trash")
		}
		return ""
	}
	base := os.Getenv("XDG_DATA_HOME")
	if base == "" || !filepath.IsAbs(base) {
		if home == "" {
			return ""
		}
		base = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(base, "twinpane", "trash")
}

// DefaultTrasher returns the trash implementation for this platform: the
// managed store, or NoTrash when no location can be determined.
func DefaultTrasher() Trasher {
	dir := DefaultTrashDir()
	if dir == "" {
		return NoTrash{Reason: "no place for a trash: the home directory is unknown"}
	}
	st, err := OpenTrash(dir)
	if err != nil {
		return NoTrash{Reason: "cannot use the trash at " + dir + ": " + err.Error()}
	}
	return st
}

// OpenTrash prepares the store at dir (creating its folders) and returns it.
func OpenTrash(dir string) (*TrashStore, error) {
	if dir == "" {
		return nil, errors.New("no trash folder given")
	}
	t := &TrashStore{Dir: dir}
	if err := t.prepare(); err != nil {
		return nil, err
	}
	return t, nil
}

func (t *TrashStore) prepare() error {
	for _, sub := range []string{"data", "info"} {
		if err := os.MkdirAll(filepath.Join(t.Dir, sub), 0o700); err != nil {
			return err
		}
	}
	return nil
}

func (t *TrashStore) dataDir() string { return filepath.Join(t.Dir, "data") }
func (t *TrashStore) infoDir() string { return filepath.Join(t.Dir, "info") }

func (t *TrashStore) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

// Name is the trash folder, shown to the user.
func (t *TrashStore) Name() string { return t.Dir }

// Available reports whether path can be moved into the trash: it has to be on
// the same filesystem as the trash folder, and it must not be the trash
// itself.
func (t *TrashStore) Available(path string) (bool, string) {
	if t.Dir == "" {
		return false, "trash location unknown"
	}
	if err := t.prepare(); err != nil {
		return false, fmt.Sprintf("cannot create trash folder: %v", err)
	}
	return checkTrashTarget(path, t.Dir)
}

// checkTrashTarget is the shared eligibility test: the trash has to be on the
// same device (the move is a rename) and the item must not be inside it.
func checkTrashTarget(path, trashDir string) (bool, string) {
	if ti, err := os.Stat(trashDir); err == nil {
		if inside, _ := isInside(filepath.Dir(path), ti); inside {
			return false, "item is already inside the trash"
		}
	}
	same, err := sameDeviceFn(filepath.Dir(path), trashDir)
	if err != nil {
		return false, fmt.Sprintf("cannot check filesystem: %v", err)
	}
	if !same {
		return false, fmt.Sprintf("%s is on a different filesystem than the trash (%s)", filepath.Base(path), trashDir)
	}
	return true, ""
}

// meta is the index record written next to an item.
type meta struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Origin    string    `json:"origin"`
	DeletedAt time.Time `json:"deletedAt"`
	Bytes     int64     `json:"bytes,omitempty"`
	Dir       bool      `json:"dir,omitempty"`
}

var trashCounter int

// newID builds a sortable, unique name for a new entry.
func (t *TrashStore) newID(at time.Time) string {
	if t.ID != nil {
		return t.ID(at)
	}
	trashCounter++
	return fmt.Sprintf("%s-%03d", at.UTC().Format("20060102T150405.000000000"), trashCounter%1000)
}

// Trash moves path into the store. The item is renamed, never copied and never
// deleted; the index record is written before the move and removed again if
// the move fails, so a half-finished attempt leaves no trace.
func (t *TrashStore) Trash(path string) error {
	_, err := t.Put(path)
	return err
}

// Put is Trash with the recorded entry returned.
func (t *TrashStore) Put(path string) (TrashEntry, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return TrashEntry{}, err
	}
	if ok, why := t.Available(abs); !ok {
		return TrashEntry{}, errors.New(why)
	}
	fi, err := os.Lstat(abs)
	if err != nil {
		return TrashEntry{}, err
	}
	if fi.IsDir() {
		if home := homeDirFn(); home != "" {
			if inside, _ := isInside(home, fi); inside {
				return TrashEntry{}, fmt.Errorf("refusing to trash %s: it is (or contains) your home directory", abs)
			}
		}
	}
	at := t.now()
	e := TrashEntry{
		ID:        t.newID(at),
		Name:      filepath.Base(abs),
		Origin:    abs,
		DeletedAt: at,
		Dir:       fi.IsDir(),
	}
	if e.Dir {
		e.Bytes = treeSize(abs)
	} else if fi.Mode().IsRegular() {
		e.Bytes = fi.Size()
	}
	infoPath := filepath.Join(t.infoDir(), e.ID+".json")
	body, err := json.Marshal(meta{ID: e.ID, Name: e.Name, Origin: e.Origin, DeletedAt: e.DeletedAt, Bytes: e.Bytes, Dir: e.Dir})
	if err != nil {
		return TrashEntry{}, err
	}
	f, err := os.OpenFile(infoPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return TrashEntry{}, fmt.Errorf("cannot write the trash index: %w", err)
	}
	_, werr := f.Write(append(body, '\n'))
	cerr := f.Close()
	if werr != nil || cerr != nil {
		os.Remove(infoPath)
		return TrashEntry{}, fmt.Errorf("cannot write the trash index: %v", errors.Join(werr, cerr))
	}
	if err := renameNoReplace(abs, filepath.Join(t.dataDir(), e.ID)); err != nil {
		os.Remove(infoPath)
		return TrashEntry{}, err
	}
	return e, nil
}

// treeSize adds up the size of the regular files under path. Unreadable
// entries are skipped: the number is for the user and for the cap, not for
// accounting.
func treeSize(path string) int64 {
	var total int64
	filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// Entries lists the trash, newest first. A record whose item is gone is
// skipped (and its index file removed): that can only happen after a crash
// between the two halves of a restore or a purge.
func (t *TrashStore) Entries() ([]TrashEntry, error) {
	if t.Dir == "" {
		return nil, nil
	}
	if err := t.prepare(); err != nil {
		return nil, err
	}
	infos, err := os.ReadDir(t.infoDir())
	if err != nil {
		return nil, err
	}
	out := make([]TrashEntry, 0, len(infos))
	for _, de := range infos {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".json") {
			continue
		}
		path := filepath.Join(t.infoDir(), de.Name())
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var m meta
		if err := json.Unmarshal(body, &m); err != nil || m.ID == "" {
			continue
		}
		if _, err := os.Lstat(filepath.Join(t.dataDir(), m.ID)); err != nil {
			os.Remove(path) // orphan record: the item is gone
			continue
		}
		out = append(out, TrashEntry{ID: m.ID, Name: m.Name, Origin: m.Origin,
			DeletedAt: m.DeletedAt, Bytes: m.Bytes, Dir: m.Dir})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].DeletedAt.Equal(out[j].DeletedAt) {
			return out[i].DeletedAt.After(out[j].DeletedAt)
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

// Total reports how many items the trash holds and how big they are.
func (t *TrashStore) Total() (int, int64) {
	entries, err := t.Entries()
	if err != nil {
		return 0, 0
	}
	var bytes int64
	for _, e := range entries {
		bytes += e.Bytes
	}
	return len(entries), bytes
}

// Restore moves an item back to dest. It never overwrites: if dest exists the
// caller has to choose another name. A restore across filesystems (the trash
// on the home volume, the original path on another one) copies the item and
// only removes it from the trash once the copy is complete.
func (t *TrashStore) Restore(e TrashEntry, dest string) error {
	if dest == "" {
		return errors.New("no destination")
	}
	if !filepath.IsAbs(dest) {
		return fmt.Errorf("refusing to restore to %q: not an absolute path", dest)
	}
	if _, err := os.Lstat(dest); err == nil {
		return fmt.Errorf("%s already exists", dest)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	src := filepath.Join(t.dataDir(), e.ID)
	if _, err := os.Lstat(src); err != nil {
		return fmt.Errorf("the item is no longer in the trash: %w", err)
	}
	if err := renameNoReplace(src, dest); err != nil {
		if !isCrossDevice(err) {
			return err
		}
		if err := copyTree(src, dest); err != nil {
			os.RemoveAll(dest)
			return err
		}
		if err := os.RemoveAll(src); err != nil {
			return fmt.Errorf("restored to %s, but the copy in the trash could not be removed: %w", dest, err)
		}
	}
	os.Remove(filepath.Join(t.infoDir(), e.ID+".json"))
	return nil
}

// copyTree copies a file, symlink or folder (used only for cross-filesystem
// restores).
func copyTree(src, dst string) error {
	fi, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(src)
		if err != nil {
			return err
		}
		return os.Symlink(target, dst)
	case fi.IsDir():
		if err := os.Mkdir(dst, fi.Mode().Perm()); err != nil {
			return err
		}
		entries, err := os.ReadDir(src)
		if err != nil {
			return err
		}
		for _, ent := range entries {
			if err := copyTree(filepath.Join(src, ent.Name()), filepath.Join(dst, ent.Name())); err != nil {
				return err
			}
		}
		return nil
	default:
		return copyFile(src, dst, fi.Mode().Perm())
	}
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		return err
	}
	_, cerr := io.Copy(out, in)
	if err := out.Close(); err != nil || cerr != nil {
		return errors.Join(cerr, err)
	}
	return nil
}

// Remove deletes one item from the trash for good.
func (t *TrashStore) Remove(e TrashEntry) error {
	// The index file is claimed first: if two windows are looking at the same
	// trash, only one of them removes the item.
	if err := os.Remove(filepath.Join(t.infoDir(), e.ID+".json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.RemoveAll(filepath.Join(t.dataDir(), e.ID))
}

// TrashSweep is what a sweep did.
type TrashSweep struct {
	Removed int   // items deleted for good
	Bytes   int64 // how much space that freed
	Expired int   // items removed because they were older than the retention
	Capped  int   // items removed because the trash was over its size cap
	Failed  int   // items that could not be removed
}

// Empty removes everything in the trash and reports what it freed.
func (t *TrashStore) Empty() TrashSweep {
	entries, err := t.Entries()
	if err != nil {
		return TrashSweep{Failed: 1}
	}
	var out TrashSweep
	for _, e := range entries {
		if err := t.Remove(e); err != nil {
			out.Failed++
			continue
		}
		out.Removed++
		out.Bytes += e.Bytes
		out.Expired++
	}
	return out
}

// Sweep removes items older than keepDays and, afterwards, the oldest items
// while the trash is bigger than capBytes (0 = no size cap, negative = no
// limit either). It never looks at anything outside its own index.
func (t *TrashStore) Sweep(keepDays int, capBytes int64) TrashSweep {
	var out TrashSweep
	entries, err := t.Entries()
	if err != nil {
		return TrashSweep{Failed: 1}
	}
	if keepDays < TrashKeepDaysMin {
		keepDays = TrashKeepDaysDef
	}
	now := t.now()
	cutoff := now.Add(-time.Duration(keepDays) * 24 * time.Hour)
	keep := make([]TrashEntry, 0, len(entries))
	for _, e := range entries { // newest first
		if e.DeletedAt.Before(cutoff) {
			if err := t.Remove(e); err != nil {
				out.Failed++
				keep = append(keep, e)
				continue
			}
			out.Removed++
			out.Expired++
			out.Bytes += e.Bytes
			continue
		}
		keep = append(keep, e)
	}
	if capBytes <= 0 {
		return out
	}
	var total int64
	for _, e := range keep {
		total += e.Bytes
	}
	for i := len(keep) - 1; i >= 0 && total > capBytes; i-- { // oldest first
		e := keep[i]
		if err := t.Remove(e); err != nil {
			out.Failed++
			continue
		}
		total -= e.Bytes
		out.Removed++
		out.Capped++
		out.Bytes += e.Bytes
	}
	return out
}

// CapBytes is how large the trash may grow: a tenth of the free space of the
// filesystem it lives on. When the free space cannot be read the historic
// default (1 GiB) is used, so the cap never silently disappears.
func (t *TrashStore) CapBytes() int64 {
	if body := os.Getenv("TWINPANE_TRASH_CAP"); body != "" {
		var n int64
		if _, err := fmt.Sscanf(body, "%d", &n); err == nil && n >= 0 {
			return n
		}
	}
	free, err := freeSpace(t.Dir)
	if err != nil || free == 0 {
		return 1 << 30
	}
	return int64(free / TrashCapShare)
}

// NoTrash is used where no reliable trash exists.
type NoTrash struct{ Reason string }

func (n NoTrash) Name() string                    { return "none" }
func (n NoTrash) Available(string) (bool, string) { return false, n.Reason }
func (n NoTrash) Trash(string) error              { return errors.New(n.Reason) }
