// Package archive lets TwinPane browse ZIP and TAR-family archives as virtual
// directories without extracting them, safely extract all or selected entries,
// and create ZIP archives from files and folders. It has no UI dependencies.
//
// ZIP browsing reads the central directory; TAR browsing streams headers and
// discards payloads. Neither format is unpacked to disk or kept open. Operations
// that need entry data (viewing, extraction) reopen the archive and look up
// entries by name.
//
// Archive-provided names are never trusted: every name is normalized and
// checked (see cleanName). Unsafe names are left out of the virtual tree and
// never extracted; their count is reported in Archive.UnsafeCount.
package archive

import (
	"archive/tar"
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
	ErrEncrypted       = errors.New("the entry is encrypted (password-protected ZIP entries are not supported)")
	ErrUnsupported     = errors.New("the entry uses an unsupported compression method")
	ErrUnsupportedType = errors.New("the archive entry has an unsupported type")
	ErrNotFound        = errors.New("entry not found in the archive (has the archive changed?)")
	ErrIsDir           = errors.New("the entry is a folder")
	ErrSymlink         = errors.New("the entry is a link")
)

// Entry is one file or folder in the archive's virtual tree.
type Entry struct {
	Name       string // base name
	Inner      string // path inside the archive, "/"-separated, no leading or trailing slash
	IsDir      bool
	Implicit   bool // a folder that is not stored in the archive (only implied by the paths below it)
	Size       uint64
	Packed     uint64 // compressed size (ZIP only)
	Modified   time.Time
	Mode       fs.FileMode // from the archive's attributes (may be zero)
	Method     uint16
	Encrypted  bool
	Symlink    bool
	LinkTarget string // TAR link target; inert metadata, never followed or extracted
	UnsafeLink bool   // TAR link target escapes the archive or is absolute
	TarType    byte   // TAR type flag (zero for ZIP entries)
	Format     Format
	CRC32      uint32
	Comment    string

	raw string // the name exactly as stored (for lookups when reopening)
}

// MethodName describes the compression method.
func (e *Entry) MethodName() string {
	if e.IsDir {
		return "—"
	}
	if e.Symlink {
		return "link (not opened)"
	}
	if e.Format == FormatTAR {
		switch {
		case isTARRegular(e.TarType) && e.TarType != tar.TypeGNUSparse:
			return "TAR file"
		case e.TarType == tar.TypeGNUSparse:
			return "TAR sparse file"
		default:
			return "TAR special entry (not opened)"
		}
	}
	switch {
	case e.Method == zip.Store:
		return "stored (no compression)"
	case e.Method == zip.Deflate:
		return "deflate"
	}
	return fmt.Sprintf("method %d (unsupported)", e.Method)
}

// Supported reports whether TwinPane can read the entry's data.
func (e *Entry) Supported() error {
	if e.Format == FormatTAR {
		switch {
		case e.IsDir:
			return ErrIsDir
		case e.Symlink:
			return ErrSymlink
		case !isTARRegular(e.TarType):
			return ErrUnsupportedType
		default:
			return nil
		}
	}
	switch {
	case e.IsDir:
		return ErrIsDir
	case e.Symlink:
		return ErrSymlink
	case e.Encrypted:
		return ErrEncrypted
	case e.Method != zip.Store && e.Method != zip.Deflate:
		return ErrUnsupported
	}
	return nil
}

// Archive is the read-only index of one ZIP or TAR-family archive.
type Archive struct {
	Path    string // absolute, clean path of the archive file
	Size    int64
	ModTime time.Time
	Comment string
	Format  Info // ZIP or TAR (plain or compressed)

	Files, Dirs int      // entries in the tree (folders include implicit ones)
	Unsafe      []string // stored names left out (unsafe paths), sanitised for display
	UnsafeCount int      // total unsafe names (Unsafe is capped for bounded memory)
	Duplicates  int      // names stored more than once (the first one is used)
	Encrypted   int
	Links       int
	UnsafeLinks int // TAR links with absolute or escaping targets; never followed

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

// Open reads a ZIP central directory or streams a TAR-family listing into
// the virtual tree. Payloads are not extracted or retained in memory.
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
	info, err := Detect(abs)
	if err != nil {
		return nil, err
	}
	switch info.Format {
	case FormatZIP:
		zr, err := zip.OpenReader(abs)
		if err != nil {
			return nil, describeOpenErr(err)
		}
		defer zr.Close()
		a := build(zr.File)
		a.Path, a.Size, a.ModTime, a.Comment, a.Format = abs, fi.Size(), fi.ModTime(), zr.Comment, info
		return a, nil
	case FormatTAR:
		return openTarArchive(abs, fi, info)
	case FormatUnknown:
		if looksLikeZipName(filepath.Base(abs)) {
			return nil, describeOpenErr(zip.ErrFormat)
		}
		return nil, ErrNotArchive
	default:
		return nil, fmt.Errorf("%s can be extracted but not browsed (read-only browsing supports ZIP and TAR)", info.Name())
	}
}

func looksLikeZipName(name string) bool {
	n := strings.ToLower(name)
	for _, ext := range []string{".zip", ".jar", ".apk", ".aar", ".whl", ".epub", ".cbz", ".xpi"} {
		if strings.HasSuffix(n, ext) {
			return true
		}
	}
	return false
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

const (
	maxArchivePathBytes = 4096
	maxArchivePathDepth = 256
)

// cleanName normalises a stored name to a safe inner path. It rejects
// absolute paths, drive letters, traversal, NULs, Windows path aliases/device
// names, overlong paths and names that are empty after cleaning. Backslashes
// are treated as separators so archive paths are validated portably.
func cleanName(raw string) (inner string, dir bool, ok bool) {
	if strings.ContainsRune(raw, 0) {
		return "", false, false
	}
	n := strings.ReplaceAll(raw, `\`, "/")
	dir = strings.HasSuffix(n, "/")
	if len(n) > maxArchivePathBytes || strings.HasPrefix(n, "/") || (len(n) >= 2 && n[1] == ':') {
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
		if !safeArchiveComponent(c) || len(parts) >= maxArchivePathDepth {
			return "", dir, false
		}
		parts = append(parts, c)
	}
	if len(parts) == 0 {
		return "", dir, false
	}
	return strings.Join(parts, "/"), dir, true
}

// Windows treats colons as alternate-data-stream separators, trims trailing
// dots/spaces and reserves DOS device names even when they have extensions.
// Reject those components on every platform to prevent aliasing and devices.
func safeArchiveComponent(c string) bool {
	if strings.ContainsRune(c, ':') || strings.HasSuffix(c, ".") || strings.HasSuffix(c, " ") {
		return false
	}
	stem := strings.ToUpper(strings.TrimRight(strings.SplitN(c, ".", 2)[0], " ."))
	switch stem {
	case "CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$", "CLOCK$":
		return false
	}
	for _, prefix := range []string{"COM", "LPT"} {
		if strings.HasPrefix(stem, prefix) {
			switch strings.TrimPrefix(stem, prefix) {
			case "1", "2", "3", "4", "5", "6", "7", "8", "9", "¹", "²", "³":
				return false
			}
		}
	}
	return true
}

func newIndex(info Info) *Archive {
	return &Archive{Format: info, entries: map[string]*Entry{"": {Name: "", Inner: "", IsDir: true}}, children: map[string][]*Entry{}}
}

// ensureDir creates missing virtual parents. A file that already occupies a
// parent path makes the child structurally unsafe and returns nil.
const unsafeNameLimit = 1000

func (a *Archive) noteUnsafe(raw string) {
	a.UnsafeCount++
	if len(a.Unsafe) < unsafeNameLimit {
		if len(raw) > maxArchivePathBytes {
			raw = raw[:maxArchivePathBytes] + "…"
		}
		a.Unsafe = append(a.Unsafe, fsys.SanitizeName(raw))
	}
}

func (a *Archive) ensureDir(inner string) *Entry {
	if e, ok := a.entries[inner]; ok {
		if e.IsDir {
			return e
		}
		return nil
	}
	parent := path.Dir(inner)
	if parent == "." {
		parent = ""
	}
	pe := a.ensureDir(parent)
	if pe == nil || !pe.IsDir {
		return nil
	}
	e := &Entry{Name: path.Base(inner), Inner: inner, IsDir: true, Implicit: true}
	a.entries[inner] = e
	a.children[parent] = append(a.children[parent], e)
	a.Dirs++
	return e
}

func build(files []*zip.File) *Archive {
	a := newIndex(Info{Format: FormatZIP})
	for _, f := range files {
		inner, isDir, ok := cleanName(f.Name)
		if !ok {
			a.noteUnsafe(f.Name)
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
		pe := a.ensureDir(parent)
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
	case e.Format == FormatTAR && !isTARRegular(e.TarType):
		// Preserve device/FIFO and other special type bits so the panel does
		// not misrepresent an unsupported archive entry as a regular file.
	default:
		mode = mode.Perm()
		if mode == 0 {
			mode = 0o644
		}
	}
	return fsys.Entry{
		Name: e.Name, Path: a.VirtualPath(e.Inner), Size: int64(e.Size), Mode: mode,
		ModTime: e.Modified, IsDir: e.IsDir, IsLink: e.Symlink, LinkTarget: e.LinkTarget,
	}
}

// ---- reading entries -----------------------------------------------------------

// OpenEntry opens the data of the file at inner for reading. The archive is
// reopened; the caller must Close the result (which closes the archive).
// The returned reader reports format-specific errors when the entry is damaged.
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
	if a.Format.Format == FormatTAR {
		r, err := a.openTarEntry(e)
		return r, e, err
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
