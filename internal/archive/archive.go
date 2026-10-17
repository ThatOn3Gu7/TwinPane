// Package archive lets TwinPane browse ZIP archives as virtual directories
// without extracting them, extract all or part of an archive safely, and
// create ZIP archives from files and folders. It has no UI dependencies.
//
// Browsing only reads the archive's central directory (the table of
// contents at its end): nothing is decompressed or written to disk. The
// archive file is not kept open; operations that need entry data (viewing,
// extraction) reopen it and look entries up by name.
//
// Archive-provided names are never trusted: every name is normalised and
// checked (see cleanName). Names that are absolute, contain "..", NUL bytes
// or drive letters are left out of the virtual tree and are never extracted;
// their number is reported (Archive.Unsafe).
package archive

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
)

// Errors returned for entries that cannot be read.
var (
	ErrEncrypted   = errors.New("the entry is encrypted (password-protected ZIP entries are not supported)")
	ErrUnsupported = errors.New("the entry uses an unsupported compression method")
	ErrNotFound    = errors.New("entry not found in the archive (has the archive changed?)")
	ErrIsDir       = errors.New("the entry is a folder")
	ErrSymlink     = errors.New("the entry is a symbolic link")
)

// Entry is one file or folder in the archive's virtual tree.
type Entry struct {
	Name      string // base name
	Inner     string // path inside the archive, "/"-separated, no leading or trailing slash
	IsDir     bool
	Implicit  bool // a folder that is not stored in the archive (only implied by the paths below it)
	Size      uint64
	Packed    uint64 // compressed size
	Modified  time.Time
	Mode      fs.FileMode // from the archive's attributes (may be zero)
	Method    uint16
	Encrypted bool
	Symlink   bool
	CRC32     uint32
	Comment   string

	raw string // the name exactly as stored (for lookups when reopening)
}

// MethodName describes the compression method.
func (e *Entry) MethodName() string {
	switch {
	case e.IsDir:
		return "—"
	case e.Method == zip.Store:
		return "stored (no compression)"
	case e.Method == zip.Deflate:
		return "deflate"
	}
	return fmt.Sprintf("method %d (unsupported)", e.Method)
}

// Supported reports whether TwinPane can read the entry's data.
func (e *Entry) Supported() error {
	switch {
	case e.IsDir:
		return ErrIsDir
	case e.Encrypted:
		return ErrEncrypted
	case e.Method != zip.Store && e.Method != zip.Deflate:
		return ErrUnsupported
	}
	return nil
}

// Archive is the index of one ZIP file.
type Archive struct {
	Path    string // absolute, clean path of the archive file
	Size    int64
	ModTime time.Time
	Comment string

	Files, Dirs int      // entries in the tree (folders include implicit ones)
	Unsafe      []string // stored names left out (unsafe paths), sanitised for display
	Duplicates  int      // names stored more than once (the first one is used)
	Encrypted   int
	Links       int

	entries  map[string]*Entry   // by Inner; "" is the root
	children map[string][]*Entry // folder Inner → children
}

// Sniff reports whether the file starts with a ZIP signature (local file
// header, or the end record of an empty archive). Only regular files are
// opened, so FIFOs and devices can never block TwinPane.
func Sniff(p string) bool {
	fi, err := os.Stat(p)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() < 22 {
		return false
	}
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	var b [4]byte
	if _, err := io.ReadFull(f, b[:]); err != nil {
		return false
	}
	return bytes.Equal(b[:], []byte("PK\x03\x04")) || bytes.Equal(b[:], []byte("PK\x05\x06"))
}

// Open reads the archive's central directory and builds the virtual tree.
func Open(p string) (*Archive, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return nil, err
	}
	abs = filepath.Clean(abs)
	fi, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", filepath.Base(abs))
	}
	zr, err := zip.OpenReader(abs)
	if err != nil {
		return nil, describeOpenErr(err)
	}
	defer zr.Close()
	a := build(zr.File)
	a.Path, a.Size, a.ModTime, a.Comment = abs, fi.Size(), fi.ModTime(), zr.Comment
	return a, nil
}

func describeOpenErr(err error) error {
	switch {
	case errors.Is(err, zip.ErrFormat):
		return errors.New("not a valid ZIP archive (damaged, incomplete or another format)")
	case errors.Is(err, zip.ErrInsecurePath):
		return err // only with GODEBUG=zipinsecurepath=0; never expected
	}
	return err
}

// Changed reports whether the archive file differs (size or modification
// time) from when it was indexed, or is gone.
func (a *Archive) Changed() bool {
	fi, err := os.Stat(a.Path)
	return err != nil || fi.Size() != a.Size || !fi.ModTime().Equal(a.ModTime)
}

// cleanName normalises a stored name to a safe inner path. ok is false for
// names that must never be used: absolute paths, drive letters, ".."
// components, NUL bytes, or names that are empty after cleaning. Backslashes
// are treated as separators (archives made on Windows sometimes use them).
func cleanName(raw string) (inner string, dir bool, ok bool) {
	if strings.ContainsRune(raw, 0) {
		return "", false, false
	}
	n := strings.ReplaceAll(raw, `\`, "/")
	dir = strings.HasSuffix(n, "/")
	if strings.HasPrefix(n, "/") || (len(n) >= 2 && n[1] == ':') {
		return "", dir, false
	}
	var parts []string
	for _, c := range strings.Split(n, "/") {
		switch c {
		case "", ".":
			continue
		case "..":
			return "", dir, false
		}
		parts = append(parts, c)
	}
	if len(parts) == 0 {
		return "", dir, false
	}
	return strings.Join(parts, "/"), dir, true
}

func build(files []*zip.File) *Archive {
	a := &Archive{entries: map[string]*Entry{"": {Name: "", Inner: "", IsDir: true}}, children: map[string][]*Entry{}}
	var ensureDir func(inner string) *Entry
	ensureDir = func(inner string) *Entry {
		if e, ok := a.entries[inner]; ok {
			return e
		}
		parent := path.Dir(inner)
		if parent == "." {
			parent = ""
		}
		pe := ensureDir(parent)
		if !pe.IsDir {
			return nil // a file with the folder's name came first
		}
		e := &Entry{Name: path.Base(inner), Inner: inner, IsDir: true, Implicit: true}
		a.entries[inner] = e
		a.children[parent] = append(a.children[parent], e)
		a.Dirs++
		return e
	}
	for _, f := range files {
		inner, isDir, ok := cleanName(f.Name)
		if !ok {
			a.Unsafe = append(a.Unsafe, fsys.SanitizeName(f.Name))
			continue
		}
		mode := f.Mode()
		if mode.IsDir() {
			isDir = true
		}
		if ex, dup := a.entries[inner]; dup {
			if ex.IsDir && ex.Implicit && isDir {
				// the folder's own entry after files inside it: fill in its metadata
				ex.Implicit, ex.Modified, ex.Mode, ex.raw = false, f.Modified, mode, f.Name
				continue
			}
			a.Duplicates++
			continue
		}
		parent := path.Dir(inner)
		if parent == "." {
			parent = ""
		}
		pe := ensureDir(parent)
		if pe == nil {
			a.Duplicates++ // "x" is a file and also used as a folder: keep the file
			continue
		}
		e := &Entry{
			Name: path.Base(inner), Inner: inner, IsDir: isDir,
			Size: f.UncompressedSize64, Packed: f.CompressedSize64,
			Modified: f.Modified, Mode: mode, Method: f.Method,
			Encrypted: f.Flags&0x1 != 0, Symlink: mode&fs.ModeSymlink != 0,
			CRC32: f.CRC32, Comment: f.Comment, raw: f.Name,
		}
		if isDir {
			e.Size, e.Packed = 0, 0
			a.Dirs++
		} else {
			a.Files++
			if e.Encrypted {
				a.Encrypted++
			}
			if e.Symlink {
				a.Links++
			}
		}
		a.entries[inner] = e
		a.children[parent] = append(a.children[parent], e)
	}
	return a
}

// Lookup returns the entry at inner ("" = root).
func (a *Archive) Lookup(inner string) (*Entry, bool) {
	e, ok := a.entries[strings.Trim(inner, "/")]
	return e, ok
}

// Children returns the entries directly inside folder inner.
func (a *Archive) Children(inner string) []*Entry { return a.children[strings.Trim(inner, "/")] }

// Walk returns inner itself (if it exists) and, for a folder, everything
// below it, parents before children.
func (a *Archive) Walk(inner string) []*Entry {
	e, ok := a.Lookup(inner)
	if !ok {
		return nil
	}
	var out []*Entry
	var rec func(e *Entry)
	rec = func(e *Entry) {
		if e.Inner != "" {
			out = append(out, e)
		}
		if e.IsDir {
			kids := append([]*Entry(nil), a.children[e.Inner]...)
			sort.Slice(kids, func(i, j int) bool { return kids[i].Inner < kids[j].Inner })
			for _, k := range kids {
				rec(k)
			}
		}
	}
	rec(e)
	return out
}

// Total returns the number of entries in the tree (files and folders).
func (a *Archive) Total() int { return a.Files + a.Dirs }

// ---- virtual paths -----------------------------------------------------------

// Sep separates the archive path from the inner path in virtual paths:
// "/sdcard/x.zip/docs/a.txt". Below the archive file nothing exists on the
// real filesystem (a regular file has no children), so a virtual path that
// leaked into a filesystem call would fail with "not a directory" instead of
// touching anything.
const Sep = "/"

// VirtualPath returns the panel path for inner ("" = the archive root,
// which is the archive's own path).
func (a *Archive) VirtualPath(inner string) string {
	inner = strings.Trim(inner, "/")
	if inner == "" {
		return a.Path
	}
	return a.Path + Sep + inner
}

// InnerOf converts a virtual path back to an inner path.
func (a *Archive) InnerOf(virtual string) (string, bool) {
	if virtual == a.Path {
		return "", true
	}
	if rest, ok := strings.CutPrefix(virtual, a.Path+Sep); ok {
		return strings.Trim(filepath.ToSlash(rest), "/"), true
	}
	return "", false
}

// Display returns a readable location: "/sdcard/x.zip ▸ docs/a.txt".
func (a *Archive) Display(inner string) string {
	inner = strings.Trim(inner, "/")
	if inner == "" {
		return a.Path + " ▸ /"
	}
	return a.Path + " ▸ " + inner
}

// List returns folder inner as a panel listing. Entry paths are virtual
// paths; the ".." entry leads to the parent folder inside the archive or,
// at the archive root, to the folder containing the archive.
func (a *Archive) List(inner string, opts fsys.Options) (fsys.Listing, error) {
	inner = strings.Trim(inner, "/")
	d, ok := a.entries[inner]
	if !ok || !d.IsDir {
		return fsys.Listing{}, fmt.Errorf("%s: folder not found in the archive", a.Display(inner))
	}
	l := fsys.Listing{Path: a.VirtualPath(inner)}
	var es []fsys.Entry
	for _, e := range a.children[inner] {
		if !opts.ShowHidden && strings.HasPrefix(e.Name, ".") {
			l.Hidden++
			continue
		}
		fe := a.FsysEntry(e)
		if e.IsDir {
			l.Dirs++
		} else {
			l.Files++
		}
		es = append(es, fe)
	}
	fsys.SortEntries(es, opts.Sort, opts.Desc)
	up := filepath.Dir(a.Path)
	if inner != "" {
		pi := path.Dir(inner)
		if pi == "." {
			pi = ""
		}
		up = a.VirtualPath(pi)
	}
	parent := fsys.Entry{Name: "..", Path: up, IsDir: true, IsParent: true, Mode: fs.ModeDir}
	l.Entries = append([]fsys.Entry{parent}, es...)
	return l, nil
}

// FsysEntry converts e to a panel entry (with its virtual path).
func (a *Archive) FsysEntry(e *Entry) fsys.Entry {
	mode := e.Mode
	switch {
	case e.IsDir:
		mode = fs.ModeDir | (mode.Perm())
		if mode.Perm() == 0 {
			mode |= 0o755
		}
	case e.Symlink:
		mode = fs.ModeSymlink | mode.Perm()
	default:
		mode = mode.Perm()
		if mode == 0 {
			mode = 0o644
		}
	}
	return fsys.Entry{
		Name: e.Name, Path: a.VirtualPath(e.Inner), Size: int64(e.Size), Mode: mode,
		ModTime: e.Modified, IsDir: e.IsDir, IsLink: e.Symlink,
	}
}

// ---- reading entries -----------------------------------------------------------

// OpenEntry opens the data of the file at inner for reading. The archive is
// reopened; the caller must Close the result (which closes the archive).
// The returned reader fails with zip.ErrChecksum / zip.ErrFormat if the data
// is damaged or longer than the stored size.
func (a *Archive) OpenEntry(inner string) (io.ReadCloser, *Entry, error) {
	e, ok := a.Lookup(inner)
	if !ok {
		return nil, nil, ErrNotFound
	}
	if e.Symlink {
		return nil, e, ErrSymlink
	}
	if err := e.Supported(); err != nil {
		return nil, e, err
	}
	zr, err := zip.OpenReader(a.Path)
	if err != nil {
		return nil, e, describeOpenErr(err)
	}
	f := findFile(zr.File, e.raw)
	if f == nil {
		zr.Close()
		return nil, e, ErrNotFound
	}
	rc, err := f.Open()
	if err != nil {
		zr.Close()
		if errors.Is(err, zip.ErrAlgorithm) {
			return nil, e, ErrUnsupported
		}
		return nil, e, err
	}
	return &entryReader{rc: rc, zr: zr}, e, nil
}

func findFile(files []*zip.File, raw string) *zip.File {
	for _, f := range files {
		if f.Name == raw {
			return f
		}
	}
	return nil
}

type entryReader struct {
	rc io.ReadCloser
	zr *zip.ReadCloser
}

func (r *entryReader) Read(p []byte) (int, error) { return r.rc.Read(p) }
func (r *entryReader) Close() error {
	err := r.rc.Close()
	if err2 := r.zr.Close(); err == nil {
		err = err2
	}
	return err
}
