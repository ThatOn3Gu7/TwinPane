package archive

import (
	"archive/zip"
	"bufio"
	"compress/flate"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
)

// ErrExists is returned when the output archive exists and overwriting was
// not authorised.
var ErrExists = errors.New("already exists")

// CreateRequest describes a new ZIP archive.
type CreateRequest struct {
	Sources   []string // absolute paths of files and folders (folders are added recursively)
	Output    string   // absolute path of the archive to write
	Overwrite bool     // replace an existing Output (an explicit user decision)
	// Level is the Deflate level, 1 (fastest) to 9 (best); 0 keeps the
	// default level.
	Level int
}

// CreateResult summarises a finished archive creation.
type CreateResult struct {
	Output   string
	Files    int   // regular files added
	Dirs     int   // folders added
	Links    int   // symbolic links stored as links
	Bytes    int64 // uncompressed bytes added
	Size     int64 // size of the archive written
	Excluded []string
	Errors   []ops.ItemError // items left out of the archive
	Canceled bool
	Fatal    error
}

// OK reports a complete archive with every item included.
func (r CreateResult) OK() bool { return !r.Canceled && r.Fatal == nil && len(r.Errors) == 0 }

// Created reports whether the archive file was written.
func (r CreateResult) Created() bool { return !r.Canceled && r.Fatal == nil }

// Summary returns a one-line description.
func (r CreateResult) Summary() string {
	items := plural(r.Files, "file")
	if r.Dirs > 0 {
		items += ", " + plural(r.Dirs, "folder")
	}
	if r.Links > 0 {
		items += ", " + plural(r.Links, "link")
	}
	switch {
	case r.Fatal != nil:
		return fmt.Sprintf("ZIP not created: %v", r.Fatal)
	case r.Canceled:
		return "ZIP creation cancelled — no archive was written"
	}
	s := fmt.Sprintf("Created %s (%s, %s)", filepath.Base(r.Output), items, humanSize(r.Size))
	if len(r.Errors) > 0 {
		s += fmt.Sprintf(" — %s could not be added", plural(len(r.Errors), "item"))
	}
	return s
}

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

type addItem struct {
	path, name string
	info       fs.FileInfo
}

// Create writes a ZIP archive of req.Sources to req.Output.
//
//   - Each source is stored under its own name, folders recursively (empty
//     folders included); symbolic links are stored as links, never followed;
//     FIFOs, sockets and devices are left out (reported).
//   - The archive is written to a temporary file in the output folder and
//     renamed into place only when complete: cancelling or a failure leaves
//     no archive behind, and an existing file is replaced only with
//     Overwrite (otherwise the rename refuses to replace anything).
//   - The output (and the temporary file) are never added to the archive
//     itself, even when the output folder is inside a source folder.
func Create(ctx context.Context, req CreateRequest, hooks ops.Hooks) CreateResult {
	res := CreateResult{Output: req.Output}
	rep := &reporter{hooks: hooks, p: ops.Progress{Phase: "scanning"}}
	if len(req.Sources) == 0 {
		res.Fatal = errors.New("nothing to add")
		return res
	}
	if req.Level != 0 && (req.Level < flate.BestSpeed || req.Level > flate.BestCompression) {
		res.Fatal = fmt.Errorf("invalid compression level %d", req.Level)
		return res
	}
	if !filepath.IsAbs(req.Output) {
		res.Fatal = fmt.Errorf("%q is not an absolute path", req.Output)
		return res
	}
	out := filepath.Clean(req.Output)
	res.Output = out
	if err := validateOutputName(filepath.Base(out)); err != nil {
		res.Fatal = err
		return res
	}
	dir := filepath.Dir(out)
	if di, err := os.Stat(dir); err != nil {
		res.Fatal = fmt.Errorf("destination folder: %w", err)
		return res
	} else if !di.IsDir() {
		res.Fatal = fmt.Errorf("%s is not a folder", dir)
		return res
	}
	var outInfo fs.FileInfo
	if fi, err := os.Lstat(out); err == nil {
		if fi.IsDir() {
			res.Fatal = fmt.Errorf("%s is a folder", out)
			return res
		}
		if !req.Overwrite {
			res.Fatal = fmt.Errorf("%s %w", filepath.Base(out), ErrExists)
			return res
		}
		outInfo = fi
	} else if !errors.Is(err, fs.ErrNotExist) {
		res.Fatal = err
		return res
	}
	names := map[string]string{}
	for _, s := range req.Sources {
		if !filepath.IsAbs(s) {
			res.Fatal = fmt.Errorf("source %q is not an absolute path", s)
			return res
		}
		b := filepath.Base(filepath.Clean(s))
		if prev, dup := names[b]; dup {
			res.Fatal = fmt.Errorf("%s and %s would both be stored as %q", prev, s, b)
			return res
		}
		names[b] = s
	}

	tmp, err := os.CreateTemp(dir, ops.TempPrefix+"zip-*.tmp")
	if err != nil {
		res.Fatal = err
		return res
	}
	tmpName := tmp.Name()
	placed := false
	defer func() {
		if !placed {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()
	tmpInfo, err := tmp.Stat()
	if err != nil {
		res.Fatal = err
		return res
	}
	isOutput := func(fi fs.FileInfo) bool {
		return os.SameFile(fi, tmpInfo) || (outInfo != nil && os.SameFile(fi, outInfo))
	}

	// scan
	var items []addItem
	var total int64
	for _, src := range req.Sources {
		src = filepath.Clean(src)
		baseDir := filepath.Dir(src)
		err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				res.Errors = append(res.Errors, ops.ItemError{Path: p, Op: "read", Err: err})
				if d != nil && d.IsDir() && p != src {
					return fs.SkipDir
				}
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				res.Errors = append(res.Errors, ops.ItemError{Path: p, Op: "read", Err: err})
				return nil
			}
			if fi.Mode().IsRegular() && isOutput(fi) {
				res.Excluded = append(res.Excluded, p)
				return nil
			}
			rel, err := filepath.Rel(baseDir, p)
			if err != nil {
				res.Errors = append(res.Errors, ops.ItemError{Path: p, Op: "add", Err: err})
				return nil
			}
			items = append(items, addItem{path: p, name: filepath.ToSlash(rel), info: fi})
			if fi.Mode().IsRegular() {
				total += fi.Size()
			}
			rep.p.TotalItems = len(items)
			rep.p.Current = p
			rep.report(false)
			return nil
		})
		if err != nil {
			if errors.Is(err, context.Canceled) {
				res.Canceled = true
				return res
			}
			res.Fatal = err
			return res
		}
	}
	rep.p = ops.Progress{Phase: "compressing", TotalItems: len(items), TotalBytes: total}
	rep.report(true)

	bw := bufio.NewWriterSize(tmp, 256<<10)
	zw := zip.NewWriter(bw)
	if req.Level != 0 {
		level := req.Level
		zw.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) {
			return flate.NewWriter(w, level)
		})
	}
	buf := make([]byte, 256<<10)
	for _, it := range items {
		if ctx.Err() != nil {
			res.Canceled = true
			return res
		}
		rep.p.Current = it.path
		if err := addOne(ctx, zw, it, buf, &res, rep); err != nil {
			if errors.Is(err, context.Canceled) {
				res.Canceled = true
			} else if ops.IsNoSpace(err) {
				res.Fatal = errors.New("the destination is full")
			} else {
				res.Fatal = err
			}
			return res
		}
		rep.p.DoneItems++
		rep.report(false)
	}
	if err := zw.Close(); err != nil {
		res.Fatal = err
		return res
	}
	if err := bw.Flush(); err != nil {
		res.Fatal = err
		return res
	}
	if err := tmp.Sync(); err != nil && !errors.Is(err, os.ErrInvalid) {
		res.Fatal = err
		return res
	}
	if fi, err := tmp.Stat(); err == nil {
		res.Size = fi.Size()
	}
	if err := tmp.Close(); err != nil {
		res.Fatal = err
		return res
	}
	_ = os.Chmod(tmpName, 0o644)
	if err := ops.PlaceFile(tmpName, out, req.Overwrite); err != nil {
		if errors.Is(err, fs.ErrExist) {
			err = fmt.Errorf("%s %w (created meanwhile): nothing was overwritten", filepath.Base(out), ErrExists)
		}
		res.Fatal = err
		return res
	}
	placed = true
	rep.p.Current = ""
	rep.report(true)
	return res
}

// validateOutputName checks the archive's file name.
func validateOutputName(name string) error {
	if err := ops.ValidateName(name); err != nil {
		return err
	}
	return nil
}

// addOne writes one item. Problems with the item itself (cannot open it, not
// a regular file) leave it out and are recorded in res; errors while the
// archive is being written (a file failing halfway, the disk filling up)
// are returned and stop the whole archive, because a ZIP stream cannot drop
// an entry that has already been started.
func addOne(ctx context.Context, zw *zip.Writer, it addItem, buf []byte, res *CreateResult, rep *reporter) error {
	mode := it.info.Mode()
	switch {
	case mode.IsDir():
		hdr := &zip.FileHeader{Name: strings.TrimSuffix(it.name, "/") + "/", Method: zip.Store, Modified: it.info.ModTime()}
		hdr.SetMode(mode)
		if _, err := zw.CreateHeader(hdr); err != nil {
			return err
		}
		res.Dirs++
		return nil
	case mode&fs.ModeSymlink != 0:
		target, err := os.Readlink(it.path)
		if err != nil {
			res.Errors = append(res.Errors, ops.ItemError{Path: it.path, Op: "read link", Err: err})
			return nil
		}
		hdr := &zip.FileHeader{Name: it.name, Method: zip.Store, Modified: it.info.ModTime()}
		hdr.SetMode(mode)
		w, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		if _, err := io.WriteString(w, target); err != nil {
			return err
		}
		res.Links++
		return nil
	case !mode.IsRegular():
		res.Errors = append(res.Errors, ops.ItemError{Path: it.path, Op: "add", Err: errors.New("not a regular file (pipe, socket or device): left out")})
		return nil
	}
	f, err := os.Open(it.path)
	if err != nil {
		res.Errors = append(res.Errors, ops.ItemError{Path: it.path, Op: "read", Err: err})
		return nil
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		res.Errors = append(res.Errors, ops.ItemError{Path: it.path, Op: "read", Err: errors.New("changed while reading: left out")})
		return nil
	}
	hdr, err := zip.FileInfoHeader(it.info)
	if err != nil {
		return err
	}
	hdr.Name = it.name
	hdr.Method = zip.Deflate
	w, err := zw.CreateHeader(hdr)
	if err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, rerr := f.Read(buf)
		if n > 0 {
			if _, err := w.Write(buf[:n]); err != nil {
				return err
			}
			res.Bytes += int64(n)
			rep.p.DoneBytes += int64(n)
			rep.report(false)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("reading %s: %w", it.path, rerr)
		}
	}
	res.Files++
	return nil
}
