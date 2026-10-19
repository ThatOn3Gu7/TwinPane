package archive

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
)

// TAR browsing streams the table of contents and discards file payloads as
// tar.Reader advances. It never extracts data or keeps file contents in RAM.
const (
	maxTARIndexEntries   = 100_000
	maxTARIndexPathBytes = int64(32 << 20)
	maxTARExpanded       = int64(8 << 30) // bound decompression work and archive bombs
)

func isTARRegular(t byte) bool {
	return t == tar.TypeReg || t == tar.TypeRegA || t == tar.TypeGNUSparse
}

var (
	ErrTARLimit    = errors.New("TAR archive exceeds TwinPane's safe indexing limit")
	ErrTARTrailer  = errors.New("TAR archive is truncated (missing its end marker)")
	ErrTARTrailing = errors.New("TAR archive has unexpected data after its end marker")
)

type tarLimitReader struct {
	r io.Reader
	n int64
}

func (r *tarLimitReader) Read(p []byte) (int, error) {
	if r.n >= maxTARExpanded {
		var one [1]byte
		n, err := r.r.Read(one[:])
		if n > 0 {
			return 0, ErrTARLimit
		}
		return 0, err
	}
	left := maxTARExpanded - r.n
	if int64(len(p)) > left+1 {
		p = p[:left+1]
	}
	n, err := r.r.Read(p)
	r.n += int64(n)
	if r.n > maxTARExpanded {
		return n, ErrTARLimit
	}
	return n, err
}

// tarProbeReader keeps only a bounded rolling window of the decompressed
// stream so we can distinguish a valid two-block TAR end marker from EOF on
// a truncated archive.
type tarProbeReader struct {
	r     io.Reader
	tail  [1024]byte
	n     int
	start int
}

func (r *tarProbeReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	for _, b := range p[:n] {
		if r.n < len(r.tail) {
			r.tail[r.n] = b
			r.n++
			continue
		}
		r.tail[r.start] = b
		r.start = (r.start + 1) % len(r.tail)
	}
	return n, err
}

func (r *tarProbeReader) hasEndMarker() bool {
	if r.n < len(r.tail) {
		return false
	}
	for i := 0; i < len(r.tail); i++ {
		if r.tail[(r.start+i)%len(r.tail)] != 0 {
			return false
		}
	}
	return true
}

type zeroOnlyWriter struct{ bad bool }

func (w *zeroOnlyWriter) Write(p []byte) (int, error) {
	for _, b := range p {
		if b != 0 {
			w.bad = true
		}
	}
	return len(p), nil
}

// verifyTAREnd requires the standard two zero-block end marker, consumes
// codec trailers/checksums, and rejects non-zero bytes following the marker.
func verifyTAREnd(r *tarProbeReader) error {
	if !r.hasEndMarker() {
		return ErrTARTrailer
	}
	w := &zeroOnlyWriter{}
	if _, err := io.Copy(w, r); err != nil {
		return fmt.Errorf("TAR stream: %w", err)
	}
	if w.bad {
		return ErrTARTrailing
	}
	return nil
}

func openTARStream(name string, codec Codec) (io.Reader, func(), error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, nil, err
	}
	r, closeDec, err := decompressor(codec, f)
	if err != nil {
		f.Close()
		return nil, nil, damaged(err)
	}
	return r, func() { closeDec(); _ = f.Close() }, nil
}

// ensureTARDir builds implicit parents while enforcing a bound on both tree
// entries and retained path strings. A nil entry with no error means a file
// already occupies one of the required parent paths.
func (a *Archive) ensureTARDir(inner string, pathBytes *int64) (*Entry, error) {
	if e, ok := a.entries[inner]; ok {
		if e.IsDir {
			return e, nil
		}
		return nil, nil
	}
	parent := path.Dir(inner)
	if parent == "." {
		parent = ""
	}
	pe, err := a.ensureTARDir(parent, pathBytes)
	if err != nil || pe == nil || !pe.IsDir {
		return nil, err
	}
	if a.Total() >= maxTARIndexEntries || int64(len(inner)) > maxTARIndexPathBytes-*pathBytes {
		return nil, ErrTARLimit
	}
	*pathBytes += int64(len(inner))
	e := &Entry{Name: path.Base(inner), Inner: inner, IsDir: true, Implicit: true}
	a.entries[inner] = e
	a.children[parent] = append(a.children[parent], e)
	a.Dirs++
	return e, nil
}

func openTarArchive(name string, fi os.FileInfo, info Info) (*Archive, error) {
	r, closeFn, err := openTARStream(name, info.Codec)
	if err != nil {
		return nil, err
	}
	defer closeFn()
	probe := &tarProbeReader{r: &tarLimitReader{r: r}}
	tr := tar.NewReader(probe)
	a := newIndex(info)
	a.Path, a.Size, a.ModTime = name, fi.Size(), fi.ModTime()
	var indexedPathBytes int64
	for n := 0; ; n++ {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			if err := verifyTAREnd(probe); err != nil {
				return nil, damaged(err)
			}
			return a, nil
		}
		if errors.Is(err, tar.ErrInsecurePath) && h != nil {
			// cleanName below performs portable validation (including Windows
			// separators) even when the Go runtime reports this advisory error.
			err = nil
		}
		if err != nil {
			return nil, damaged(err)
		}
		if n >= maxTARIndexEntries {
			return nil, ErrTARLimit
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue // global PAX metadata is not a user-visible path
		}
		name := strings.ReplaceAll(h.Name, `\`, "/")
		if len(name) > maxArchivePathBytes || strings.Count(name, "/") > maxArchivePathDepth {
			a.noteUnsafe(h.Name)
			continue
		}
		inner, _, ok := cleanName(h.Name)
		if !ok {
			a.noteUnsafe(h.Name)
			continue
		}
		isDir := h.Typeflag == tar.TypeDir
		isLink := h.Typeflag == tar.TypeSymlink || h.Typeflag == tar.TypeLink
		if existing, dup := a.entries[inner]; dup {
			if existing.IsDir && existing.Implicit && isDir {
				fi := h.FileInfo()
				existing.Implicit, existing.Modified, existing.Mode, existing.raw, existing.TarType, existing.Format = false, h.ModTime, fi.Mode(), h.Name, h.Typeflag, FormatTAR
				continue
			}
			a.Duplicates++
			continue
		}
		if a.Total() >= maxTARIndexEntries || int64(len(inner)) > maxTARIndexPathBytes-indexedPathBytes {
			return nil, ErrTARLimit
		}
		parent := path.Dir(inner)
		if parent == "." {
			parent = ""
		}
		parentEntry, err := a.ensureTARDir(parent, &indexedPathBytes)
		if err != nil {
			return nil, err
		}
		if parentEntry == nil {
			a.Duplicates++ // a file already occupies this parent path
			continue
		}
		if a.Total() >= maxTARIndexEntries {
			return nil, ErrTARLimit
		}
		indexedPathBytes += int64(len(inner))
		mode := h.FileInfo().Mode()
		if isDir {
			mode |= fs.ModeDir
		}
		if isLink {
			mode = fs.ModeSymlink | mode.Perm()
		}
		linkTarget := h.Linkname
		unsafeLink := isLink && (len(linkTarget) > maxArchivePathBytes || !safeTARLink(inner, linkTarget, h.Typeflag == tar.TypeLink))
		if len(linkTarget) > maxArchivePathBytes {
			linkTarget = "(link target exceeds the safe display limit)"
		}
		if int64(len(linkTarget)) > maxTARIndexPathBytes-indexedPathBytes {
			return nil, ErrTARLimit
		}
		indexedPathBytes += int64(len(linkTarget))
		e := &Entry{
			Name: path.Base(inner), Inner: inner, IsDir: isDir,
			Size: uint64(max(int64(0), h.Size)), Modified: h.ModTime,
			Mode: mode, Symlink: isLink, Format: FormatTAR, TarType: h.Typeflag,
			LinkTarget: linkTarget, UnsafeLink: unsafeLink, raw: h.Name,
		}
		if isDir {
			e.Size = 0
			a.Dirs++
		} else {
			a.Files++
		}
		if isLink {
			a.Links++
			if unsafeLink {
				a.UnsafeLinks++
			}
		}
		a.entries[inner] = e
		a.children[parent] = append(a.children[parent], e)
	}
}

func safeTARLink(inner, target string, hard bool) bool {
	if target == "" || strings.ContainsRune(target, 0) {
		return false
	}
	n := strings.ReplaceAll(target, `\`, "/")
	if strings.HasPrefix(n, "/") || (len(n) >= 2 && n[1] == ':') {
		return false
	}
	if hard {
		_, _, ok := cleanName(n)
		return ok
	}
	resolved := path.Clean(path.Join(path.Dir(inner), n))
	return resolved != ".." && !strings.HasPrefix(resolved, "../") && !path.IsAbs(resolved)
}

type tarEntryReader struct {
	r       io.Reader
	closeFn func()
}

func (r *tarEntryReader) Read(p []byte) (int, error) { return r.r.Read(p) }
func (r *tarEntryReader) Close() error {
	r.closeFn()
	return nil
}

func (a *Archive) openTarEntry(e *Entry) (io.ReadCloser, error) {
	r, closeFn, err := openTARStream(a.Path, a.Format.Codec)
	if err != nil {
		return nil, err
	}
	probe := &tarProbeReader{r: &tarLimitReader{r: r}}
	tr := tar.NewReader(probe)
	for n := 0; ; n++ {
		if n >= maxTARIndexEntries {
			closeFn()
			return nil, ErrTARLimit
		}
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			closeFn()
			return nil, ErrNotFound
		}
		if errors.Is(err, tar.ErrInsecurePath) && h != nil {
			err = nil
		}
		if err != nil {
			closeFn()
			return nil, damaged(err)
		}
		if h.Name != e.raw {
			continue
		}
		inner, _, ok := cleanName(h.Name)
		if !ok || inner != e.Inner {
			closeFn()
			return nil, ErrNotFound
		}
		return &tarEntryReader{r: tr, closeFn: closeFn}, nil
	}
}
