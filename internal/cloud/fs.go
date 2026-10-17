package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
)

// Item is one entry reported by rclone lsjson.
type Item struct {
	Path    string    `json:"Path"` // relative to the listed folder
	Name    string    `json:"Name"`
	Size    int64     `json:"Size"`
	ModTime time.Time `json:"ModTime"`
	IsDir   bool      `json:"IsDir"`
}

// lsjson runs rclone lsjson on p with extra flags.
func (c *Client) lsjson(ctx context.Context, p Path, extra ...string) ([]Item, error) {
	args := append([]string{"lsjson", "--no-mimetype"}, listFlags...)
	args = append(args, extra...)
	args = append(args, "--", p.Arg())
	out, err := c.output(ctx, "list", args, &p)
	if err != nil {
		return nil, err
	}
	var items []Item
	if err := json.Unmarshal(out, &items); err != nil {
		return nil, &Error{Op: "list", Kind: KindOther, Remote: p.Remote, Msg: "rclone's listing could not be read: " + err.Error()}
	}
	return items, nil
}

// ListItems lists the folder p (not recursive).
func (c *Client) ListItems(ctx context.Context, p Path) ([]Item, error) {
	return c.lsjson(ctx, p)
}

// ListFiles lists every file below the folder p (recursive, no folders).
func (c *Client) ListFiles(ctx context.Context, p Path) ([]Item, error) {
	return c.lsjson(ctx, p, "--recursive", "--files-only")
}

// List lists the folder p as a panel listing, filtered and sorted like a
// local folder. Entry paths are rclone arguments ("remote:dir/name").
func (c *Client) List(ctx context.Context, p Path, opts fsys.Options) (fsys.Listing, error) {
	items, err := c.lsjson(ctx, p)
	if err != nil {
		return fsys.Listing{}, err
	}
	l := fsys.Listing{Path: p.Arg()}
	entries := make([]fsys.Entry, 0, len(items))
	for _, it := range items {
		if !validName(it.Name) {
			continue
		}
		if !opts.ShowHidden && strings.HasPrefix(it.Name, ".") {
			l.Hidden++
			continue
		}
		e := it.entry(p)
		if e.IsDir {
			l.Dirs++
		} else {
			l.Files++
		}
		entries = append(entries, e)
	}
	fsys.SortEntries(entries, opts.Sort, opts.Desc)
	if !p.IsRoot() {
		entries = append([]fsys.Entry{{Name: "..", Path: p.Dir().Arg(), IsDir: true, IsParent: true, Mode: fs.ModeDir}}, entries...)
	}
	l.Entries = entries
	return l, nil
}

func (it Item) entry(dir Path) fsys.Entry {
	e := fsys.Entry{Name: it.Name, Path: dir.Join(it.Name).Arg(), Size: it.Size, ModTime: it.ModTime, IsDir: it.IsDir, Mode: 0o644}
	if it.IsDir {
		e.Mode = fs.ModeDir | 0o755
		e.Size = 0
	}
	if e.Size < 0 {
		e.Size = 0
	}
	return e
}

// validName rejects names that cannot be a single path element.
func validName(n string) bool {
	return n != "" && n != "." && n != ".." && !strings.ContainsRune(n, '/')
}

// CheckName validates a name typed for a new folder or a rename.
func CheckName(n string) error {
	switch {
	case strings.TrimSpace(n) == "":
		return errors.New("type a name")
	case n == "." || n == "..":
		return errors.New("that name is reserved")
	case strings.ContainsRune(n, '/'):
		return errors.New("a name cannot contain /")
	case strings.ContainsRune(n, 0):
		return errors.New("a name cannot contain a NUL character")
	}
	return nil
}

// Stat returns the item at p, or an error of KindNotFound.
func (c *Client) Stat(ctx context.Context, p Path) (Item, error) {
	if p.IsLocal() {
		fi, err := os.Lstat(p.Path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return Item{}, &Error{Op: "stat", Kind: KindNotFound, Msg: p.Display() + " does not exist"}
			}
			return Item{}, &Error{Op: "stat", Kind: KindOther, Msg: err.Error()}
		}
		return Item{Name: fi.Name(), Size: fi.Size(), ModTime: fi.ModTime(), IsDir: fi.IsDir()}, nil
	}
	args := append([]string{"lsjson", "--stat", "--no-mimetype"}, listFlags...)
	args = append(args, "--", p.Arg())
	out, err := c.output(ctx, "stat", args, &p)
	if err != nil {
		return Item{}, err
	}
	var it Item
	if err := json.Unmarshal(out, &it); err != nil {
		return Item{}, &Error{Op: "stat", Kind: KindOther, Remote: p.Remote, Msg: "rclone's answer could not be read: " + err.Error()}
	}
	return it, nil
}

// exists reports whether p exists (err only for failures other than "not
// found").
func (c *Client) exists(ctx context.Context, p Path) (bool, error) {
	_, err := c.Stat(ctx, p)
	switch {
	case err == nil:
		return true, nil
	case KindOf(err) == KindNotFound:
		return false, nil
	}
	return false, err
}

// ErrNoEmptyFolders is returned (wrapped) by Mkdir when the remote accepted
// the folder but does not keep empty folders (S3, B2, …).
var ErrNoEmptyFolders = errors.New("this remote does not keep empty folders: the folder appears once a file is put into it")

// Mkdir creates the folder name in dir. An existing item is never touched.
func (c *Client) Mkdir(ctx context.Context, dir Path, name string) error {
	if err := CheckName(name); err != nil {
		return &Error{Op: "create folder", Kind: KindInvalid, Msg: err.Error()}
	}
	p := dir.Join(name)
	if ok, err := c.exists(ctx, p); err != nil {
		return err
	} else if ok {
		return &Error{Op: "create folder", Kind: KindExists, Remote: p.Remote, Msg: fmt.Sprintf("%q already exists", name)}
	}
	if _, err := c.output(ctx, "create folder", append([]string{"mkdir"}, "--", p.Arg()), &p); err != nil {
		return err
	}
	if ok, err := c.exists(ctx, p); err == nil && !ok {
		return ErrNoEmptyFolders
	}
	return nil
}

// Rename renames the item at p to name in the same folder. An existing item
// with the new name is never replaced.
//
// Whether the new name is taken is decided from the folder's listing, which
// has the names as stored, not from a lookup of the new name: on a remote
// that ignores letter case a lookup of "A.txt" finds "a.txt" itself, and on
// one that keeps case "a.txt" and "A.txt" are two items. So:
//
//   - an item named exactly name (other than p) always blocks the rename;
//   - another item whose name differs from name only in letter case blocks
//     it too, since on a case-insensitive remote that is the same name
//     (on a case-sensitive one this refuses a rename that would have been
//     safe: TwinPane cannot tell reliably, and refusing loses nothing);
//   - changing only the case of p's own name is allowed.
//
// A file is moved with --ignore-existing, so a file that appears with the
// new name after the check is not replaced either (rclone then exits with
// 9 under --error-on-no-transfer). That does not work for case-only renames
// (the file itself would count as existing) or folders (rclone merges
// folders and reports no transfer even when it moved one); those rely on
// the check alone.
func (c *Client) Rename(ctx context.Context, p Path, name string) error {
	if err := CheckName(name); err != nil {
		return &Error{Op: "rename", Kind: KindInvalid, Msg: err.Error()}
	}
	if p.IsRoot() {
		return &Error{Op: "rename", Kind: KindInvalid, Msg: "the root of a remote cannot be renamed"}
	}
	dst := p.Dir().Join(name)
	if dst == p {
		return nil
	}
	items, err := c.ListItems(ctx, p.Dir())
	if err != nil {
		return err
	}
	src, err := renameCheck(items, p, name)
	if err != nil {
		return err
	}
	args := []string{"moveto"}
	guarded := !src.IsDir && !strings.EqualFold(p.Base(), name)
	if guarded {
		args = append(args, "--ignore-existing", "--error-on-no-transfer")
	}
	args = append(args, "--", p.Arg(), dst.Arg())
	if _, err := c.output(ctx, "rename", args, &p); err != nil {
		var e *Error
		if guarded && errors.As(err, &e) && e.Code == 9 {
			return &Error{Op: "rename", Kind: KindExists, Remote: p.Remote, Code: 9,
				Msg: fmt.Sprintf("%q appeared while renaming; nothing was renamed", name)}
		}
		return err
	}
	return nil
}

// renameCheck finds p in its folder's listing and refuses a rename to name
// that could replace another item (see Rename).
func renameCheck(items []Item, p Path, name string) (Item, error) {
	var (
		src   Item
		found int
		clash string
	)
	for _, it := range items {
		switch {
		case it.Name == p.Base():
			src = it
			found++
		case it.Name == name:
			return Item{}, &Error{Op: "rename", Kind: KindExists, Remote: p.Remote, Msg: fmt.Sprintf("%q already exists", name)}
		case strings.EqualFold(it.Name, name):
			clash = it.Name
		}
	}
	switch {
	case found == 0:
		return Item{}, &Error{Op: "rename", Kind: KindNotFound, Remote: p.Remote, Msg: fmt.Sprintf("%q is no longer there (refresh with Ctrl+L)", p.Base())}
	case found > 1:
		return Item{}, &Error{Op: "rename", Kind: KindInvalid, Remote: p.Remote,
			Msg: fmt.Sprintf("there are %d items named %q in this folder (some providers allow that); TwinPane cannot tell which one to rename", found, p.Base())}
	case clash != "":
		return Item{}, &Error{Op: "rename", Kind: KindExists, Remote: p.Remote,
			Msg: fmt.Sprintf("%q already exists, and the names differ only in letter case (the same name on many remotes)", clash)}
	}
	return src, nil
}

// Delete deletes the file or folder (with everything in it) at p. Some
// providers (Google Drive by default) keep deleted items in their trash.
func (c *Client) Delete(ctx context.Context, p Path, isDir bool) error {
	if p.IsRoot() {
		return &Error{Op: "delete", Kind: KindInvalid, Msg: "the root of a remote cannot be deleted"}
	}
	cmd := "deletefile"
	if isDir {
		cmd = "purge"
	}
	_, err := c.output(ctx, "delete", []string{cmd, "--", p.Arg()}, &p)
	return err
}
