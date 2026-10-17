package compress

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	dsbzip2 "github.com/dsnet/compress/bzip2"
	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	"github.com/ulikunitz/xz"

	"github.com/ThatOn3Gu7/TwinPane/internal/archive"
	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
)

// ErrExists is returned when the output exists and overwriting was not
// authorised.
var ErrExists = archive.ErrExists

// Request describes an archive (or compressed file) to create.
type Request struct {
	Format ID
	// Level indexes the format's Levels; DefaultLevelIndex (or any value
	// out of range) selects the default. Ignored for formats without levels.
	Level     int
	Sources   []string // absolute paths; folders are added recursively
	Output    string   // absolute path of the file to write
	Overwrite bool     // replace an existing Output (an explicit user decision)
	// Tool is the external program for formats that need one (7Z): the
	// Capability.Tool found by Detect.
	Tool string
}

// Result summarises a finished creation.
type Result struct {
	Format   Format
	Output   string
	Files    int   // regular files added
	Dirs     int   // folders added
	Links    int   // symbolic links stored as links
	Bytes    int64 // uncompressed bytes added
	Size     int64 // size of the file written
	Excluded []string
	Errors   []ops.ItemError // items left out
	Canceled bool
	Fatal    error
}

// OK reports a complete file with every item included.
func (r Result) OK() bool { return !r.Canceled && r.Fatal == nil && len(r.Errors) == 0 }

// Created reports whether the output file was written.
func (r Result) Created() bool { return !r.Canceled && r.Fatal == nil }

// Summary returns a one-line description.
func (r Result) Summary() string {
	name := r.Format.Name
	if name == "" {
		name = "Archive"
	}
	switch {
	case r.Fatal != nil:
		return fmt.Sprintf("%s not created: %v", name, r.Fatal)
	case r.Canceled:
		if r.Format.Kind == Single {
			return name + " compression cancelled — no file was written"
		}
		return name + " creation cancelled — no archive was written"
	}
	var s string
	if r.Format.Kind == Single {
		s = fmt.Sprintf("Created %s (%s → %s)", filepath.Base(r.Output), humanSize(r.Bytes), humanSize(r.Size))
	} else {
		items := plural(r.Files, "file")
		if r.Dirs > 0 {
			items += ", " + plural(r.Dirs, "folder")
		}
		if r.Links > 0 {
			items += ", " + plural(r.Links, "link")
		}
		s = fmt.Sprintf("Created %s (%s, %s)", filepath.Base(r.Output), items, humanSize(r.Size))
	}
	if len(r.Errors) > 0 {
		s += fmt.Sprintf(" — %s could not be added", plural(len(r.Errors), "item"))
	}
	return s
}

// Create writes the archive described by req.
//
// The output is written to a temporary file in the destination folder and
// renamed into place only when complete: cancelling (ctx) or a failure
// leaves nothing behind, and an existing file is replaced only with
// req.Overwrite. The output is never added to itself. The output name must
// carry the format's extension (see CheckName).
func Create(ctx context.Context, req Request, hooks ops.Hooks) Result {
	f, ok := Lookup(req.Format)
	if !ok {
		return Result{Output: req.Output, Fatal: fmt.Errorf("unknown format %q", req.Format)}
	}
	level := 0
	if len(f.Levels) > 0 {
		level = f.Levels[f.LevelIndex(req.Level)].Value
	}
	res := Result{Format: f, Output: req.Output}
	t, err := prepare(f, req)
	if err != nil {
		res.Fatal = err
		return res
	}
	res.Output = t.out
	switch {
	case f.ID == ZIP:
		z := archive.Create(ctx, archive.CreateRequest{Sources: req.Sources, Output: t.out, Overwrite: req.Overwrite, Level: level}, hooks)
		return Result{Format: f, Output: z.Output, Files: z.Files, Dirs: z.Dirs, Links: z.Links, Bytes: z.Bytes,
			Size: z.Size, Excluded: z.Excluded, Errors: z.Errors, Canceled: z.Canceled, Fatal: z.Fatal}
	case f.ID == SevenZ:
		create7z(ctx, req, t, level, hooks, &res)
	case f.Kind == Single:
		createSingle(ctx, req, t, f, level, hooks, &res)
	default:
		createTar(ctx, req, t, f, level, hooks, &res)
	}
	return res
}

// target is a checked output.
type target struct {
	out     string
	dir     string
	outInfo fs.FileInfo // the existing output (being overwritten), or nil
}

// prepare validates the request before anything is written.
func prepare(f Format, req Request) (target, error) {
	var t target
	if len(req.Sources) == 0 {
		return t, errors.New("nothing to add")
	}
	if !filepath.IsAbs(req.Output) {
		return t, fmt.Errorf("%q is not an absolute path", req.Output)
	}
	t.out = filepath.Clean(req.Output)
	base := filepath.Base(t.out)
	if err := ops.ValidateName(base); err != nil {
		return t, err
	}
	if !f.HasExt(base) {
		return t, fmt.Errorf("%q does not end in %s, the %s extension", base, f.Ext, f.Name)
	}
	if f.Kind == Single && len(req.Sources) != 1 {
		return t, fmt.Errorf("%s compresses a single file (%d items given)", f.Name, len(req.Sources))
	}
	names := map[string]string{}
	for _, s := range req.Sources {
		if !filepath.IsAbs(s) {
			return t, fmt.Errorf("source %q is not an absolute path", s)
		}
		if filepath.Clean(s) == t.out {
			return t, errors.New("the output would replace one of the items being added")
		}
		b := filepath.Base(filepath.Clean(s))
		if prev, dup := names[b]; dup {
			return t, fmt.Errorf("%s and %s would both be stored as %q", prev, s, b)
		}
		names[b] = s
	}
	t.dir = filepath.Dir(t.out)
	if di, err := os.Stat(t.dir); err != nil {
		return t, fmt.Errorf("destination folder: %w", err)
	} else if !di.IsDir() {
		return t, fmt.Errorf("%s is not a folder", t.dir)
	}
	if fi, err := os.Lstat(t.out); err == nil {
		if fi.IsDir() {
			return t, fmt.Errorf("%s is a folder", t.out)
		}
		if !req.Overwrite {
			return t, fmt.Errorf("%s %w", base, ErrExists)
		}
		t.outInfo = fi
	} else if !errors.Is(err, fs.ErrNotExist) {
		return t, err
	}
	return t, nil
}

// ---- progress ------------------------------------------------------------------

const progressEvery = 100 * time.Millisecond

type reporter struct {
	hooks ops.Hooks
	p     ops.Progress
	last  time.Time
}

func (r *reporter) report(force bool) {
	if r.hooks.Progress == nil {
		return
	}
	if now := time.Now(); force || now.Sub(r.last) >= progressEvery {
		r.last = now
		r.hooks.Progress(r.p)
	}
}

// ---- scanning --------------------------------------------------------------------

type item struct {
	path string
	name string // slash-separated path inside the archive
	info fs.FileInfo
}

type scanResult struct {
	items    []item
	total    int64 // bytes of regular files
	special  []item
	excluded []string
	errors   []ops.ItemError
}

// scan lists every source recursively (links are not followed). skip
// reports the output and temporary files, which are left out. Sources are
// stored relative to their parent folder.
func scan(ctx context.Context, sources []string, skip func(fs.FileInfo) bool, rep *reporter) (scanResult, error) {
	var sr scanResult
	for _, src := range sources {
		src = filepath.Clean(src)
		baseDir := filepath.Dir(src)
		err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if err != nil {
				sr.errors = append(sr.errors, ops.ItemError{Path: p, Op: "read", Err: err})
				if d != nil && d.IsDir() && p != src {
					return fs.SkipDir
				}
				return nil
			}
			fi, err := d.Info()
			if err != nil {
				sr.errors = append(sr.errors, ops.ItemError{Path: p, Op: "read", Err: err})
				return nil
			}
			if skip != nil && skip(fi) {
				sr.excluded = append(sr.excluded, p)
				if fi.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			rel, err := filepath.Rel(baseDir, p)
			if err != nil {
				sr.errors = append(sr.errors, ops.ItemError{Path: p, Op: "add", Err: err})
				return nil
			}
			it := item{path: p, name: filepath.ToSlash(rel), info: fi}
			m := fi.Mode()
			if !m.IsRegular() && !m.IsDir() && m&fs.ModeSymlink == 0 {
				sr.special = append(sr.special, it)
				sr.errors = append(sr.errors, ops.ItemError{Path: p, Op: "add", Err: errors.New("not a regular file (pipe, socket or device): left out")})
				return nil
			}
			sr.items = append(sr.items, it)
			if m.IsRegular() {
				sr.total += fi.Size()
			}
			rep.p.TotalItems = len(sr.items)
			rep.p.Current = p
			rep.report(false)
			return nil
		})
		if err != nil {
			return sr, err
		}
	}
	return sr, nil
}

// ---- output file -------------------------------------------------------------------

// tempOut is the temporary file an archive is written to.
type tempOut struct {
	f      *os.File
	name   string
	info   fs.FileInfo
	placed bool
}

func newTempOut(dir string) (*tempOut, error) {
	f, err := os.CreateTemp(dir, ops.TempPrefix+"pack-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("cannot write in %s: %w", dir, unwrapPath(err))
	}
	t := &tempOut{f: f, name: f.Name()}
	if t.info, err = f.Stat(); err != nil {
		t.discard()
		return nil, err
	}
	return t, nil
}

// discard removes the temporary file unless it was placed.
func (t *tempOut) discard() {
	if !t.placed {
		t.f.Close()
		os.Remove(t.name)
	}
}

// finish syncs and closes the file and renames it onto out.
func (t *tempOut) finish(out string, overwrite bool) (int64, error) {
	if err := t.f.Sync(); err != nil && !errors.Is(err, os.ErrInvalid) {
		return 0, err
	}
	var size int64
	if fi, err := t.f.Stat(); err == nil {
		size = fi.Size()
	}
	if err := t.f.Close(); err != nil {
		return 0, err
	}
	_ = os.Chmod(t.name, 0o644)
	if err := ops.PlaceFile(t.name, out, overwrite); err != nil {
		if errors.Is(err, fs.ErrExist) {
			err = fmt.Errorf("%s %w (created meanwhile): nothing was overwritten", filepath.Base(out), ErrExists)
		}
		return 0, err
	}
	t.placed = true
	return size, nil
}

func unwrapPath(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// fatal converts a write error into the reported cause.
func fatal(err error) error {
	if ops.IsNoSpace(err) {
		return errors.New("the destination is full")
	}
	return err
}

// countWriter counts bytes written through it.
type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

// newCodec wraps w in the compressor for codec at the given level value.
// name and mod fill the gzip header of single files ("" leaves it empty).
func newCodec(codec Codec, level int, w io.Writer, name string, mod time.Time) (io.WriteCloser, error) {
	switch codec {
	case None:
		return nopCloser{w}, nil
	case Gzip:
		zw, err := gzip.NewWriterLevel(w, level)
		if err != nil {
			return nil, err
		}
		if latin1(name) {
			zw.Name = name // the gzip header can only hold Latin-1
		}
		if name != "" {
			zw.ModTime = mod
		}
		return zw, nil
	case Xz:
		return xz.NewWriter(w)
	case Zstd:
		conc := runtime.GOMAXPROCS(0)
		if conc > 4 {
			conc = 4 // bounds memory use on phones with many cores
		}
		return zstd.NewWriter(w, zstd.WithEncoderLevel(zstd.EncoderLevel(level)), zstd.WithEncoderConcurrency(conc))
	case Bzip2:
		return dsbzip2.NewWriter(w, &dsbzip2.WriterConfig{Level: level})
	case Lz4:
		zw := lz4.NewWriter(w)
		lv, err := lz4Level(level)
		if err != nil {
			return nil, err
		}
		if err := zw.Apply(lz4.CompressionLevelOption(lv)); err != nil {
			return nil, err
		}
		return zw, nil
	}
	return nil, fmt.Errorf("unknown codec %d", codec)
}

func lz4Level(n int) (lz4.CompressionLevel, error) {
	switch {
	case n == 0:
		return lz4.Fast, nil
	case n >= 1 && n <= 9:
		return lz4.CompressionLevel(1 << (8 + n - 1)), nil // lz4.Level1 … lz4.Level9
	}
	return 0, fmt.Errorf("invalid LZ4 level %d", n)
}

func latin1(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r > 0xff || r == 0 {
			return false
		}
	}
	return true
}

// copyData copies src to w, honouring ctx and reporting progress.
func copyData(ctx context.Context, w io.Writer, src io.Reader, limit int64, buf []byte, rep *reporter) (int64, error) {
	var done int64
	for limit < 0 || done < limit {
		if err := ctx.Err(); err != nil {
			return done, err
		}
		b := buf
		if limit >= 0 && int64(len(b)) > limit-done {
			b = b[:limit-done]
		}
		n, rerr := src.Read(b)
		if n > 0 {
			if _, err := w.Write(b[:n]); err != nil {
				return done, err
			}
			done += int64(n)
			rep.p.DoneBytes += int64(n)
			rep.report(false)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return done, rerr
		}
	}
	return done, nil
}

// ---- TAR ---------------------------------------------------------------------------

func createTar(ctx context.Context, req Request, t target, f Format, level int, hooks ops.Hooks, res *Result) {
	rep := &reporter{hooks: hooks, p: ops.Progress{Phase: "scanning"}}
	tmp, err := newTempOut(t.dir)
	if err != nil {
		res.Fatal = err
		return
	}
	defer tmp.discard()
	skip := func(fi fs.FileInfo) bool {
		return fi.Mode().IsRegular() && (os.SameFile(fi, tmp.info) || (t.outInfo != nil && os.SameFile(fi, t.outInfo)))
	}
	sr, err := scan(ctx, req.Sources, skip, rep)
	res.Excluded, res.Errors = sr.excluded, sr.errors
	if err != nil {
		if errors.Is(err, context.Canceled) {
			res.Canceled = true
		} else {
			res.Fatal = err
		}
		return
	}
	rep.p = ops.Progress{Phase: "compressing", TotalItems: len(sr.items), TotalBytes: sr.total}
	rep.report(true)

	bw := bufio.NewWriterSize(tmp.f, 256<<10)
	cw, err := newCodec(f.Codec, level, bw, "", time.Time{})
	if err != nil {
		res.Fatal = err
		return
	}
	counter := &countWriter{w: cw}
	tw := tar.NewWriter(counter)
	buf := make([]byte, 256<<10)
	for _, it := range sr.items {
		if ctx.Err() != nil {
			res.Canceled = true
			return
		}
		rep.p.Current = it.path
		if err := addTar(ctx, tw, counter, it, buf, res, rep); err != nil {
			if errors.Is(err, context.Canceled) {
				res.Canceled = true
			} else {
				res.Fatal = fatal(err)
			}
			return
		}
		rep.p.DoneItems++
		rep.report(false)
	}
	for _, step := range []func() error{tw.Close, cw.Close, bw.Flush} {
		if err := step(); err != nil {
			res.Fatal = fatal(err)
			return
		}
	}
	if res.Size, err = tmp.finish(t.out, req.Overwrite); err != nil {
		res.Fatal = fatal(err)
		return
	}
	rep.p.Current = ""
	rep.report(true)
}

// addTar writes one item. Problems with the item itself leave it out (and
// are recorded); errors while writing the stream are returned and stop the
// archive, as a started TAR entry cannot be taken back.
func addTar(ctx context.Context, tw *tar.Writer, counter *countWriter, it item, buf []byte, res *Result, rep *reporter) error {
	mode := it.info.Mode()
	link := ""
	if mode&fs.ModeSymlink != 0 {
		target, err := os.Readlink(it.path)
		if err != nil {
			res.Errors = append(res.Errors, ops.ItemError{Path: it.path, Op: "read link", Err: err})
			return nil
		}
		link = target
	}
	var f *os.File
	if mode.IsRegular() {
		var err error
		if f, err = os.Open(it.path); err != nil {
			res.Errors = append(res.Errors, ops.ItemError{Path: it.path, Op: "read", Err: err})
			return nil
		}
		defer f.Close()
		if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
			res.Errors = append(res.Errors, ops.ItemError{Path: it.path, Op: "read", Err: errors.New("changed while reading: left out")})
			return nil
		}
	}
	hdr, err := tar.FileInfoHeader(it.info, link)
	if err != nil {
		res.Errors = append(res.Errors, ops.ItemError{Path: it.path, Op: "add", Err: err})
		return nil
	}
	hdr.Name = it.name
	if mode.IsDir() {
		hdr.Name = strings.TrimSuffix(it.name, "/") + "/"
	}
	before := counter.n
	if err := tw.WriteHeader(hdr); err != nil {
		if counter.n == before {
			// refused before anything was written (a name or field the
			// TAR format cannot hold): leave the item out
			res.Errors = append(res.Errors, ops.ItemError{Path: it.path, Op: "add", Err: err})
			return nil
		}
		return err
	}
	switch {
	case mode.IsDir():
		res.Dirs++
	case link != "":
		res.Links++
	default:
		n, err := copyData(ctx, tw, f, hdr.Size, buf, rep)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			return fmt.Errorf("reading %s: %w", it.path, err)
		}
		if n < hdr.Size {
			return fmt.Errorf("%s became shorter while it was being added", it.path)
		}
		res.Bytes += n
		res.Files++
	}
	return nil
}

// ---- single file ---------------------------------------------------------------

func createSingle(ctx context.Context, req Request, t target, f Format, level int, hooks ops.Hooks, res *Result) {
	rep := &reporter{hooks: hooks}
	src := filepath.Clean(req.Sources[0])
	in, err := os.Open(src)
	if err != nil {
		res.Fatal = err
		return
	}
	defer in.Close()
	fi, err := in.Stat()
	if err != nil {
		res.Fatal = err
		return
	}
	if !fi.Mode().IsRegular() {
		if fi.IsDir() {
			res.Fatal = fmt.Errorf("%s compresses a single file, and %s is a folder", f.Name, filepath.Base(src))
		} else {
			res.Fatal = fmt.Errorf("%s is not a regular file", filepath.Base(src))
		}
		return
	}
	if t.outInfo != nil && os.SameFile(fi, t.outInfo) {
		res.Fatal = errors.New("the output would replace the file being compressed")
		return
	}
	tmp, err := newTempOut(t.dir)
	if err != nil {
		res.Fatal = err
		return
	}
	defer tmp.discard()
	rep.p = ops.Progress{Phase: "compressing", TotalItems: 1, TotalBytes: fi.Size(), Current: src}
	rep.report(true)
	bw := bufio.NewWriterSize(tmp.f, 256<<10)
	cw, err := newCodec(f.Codec, level, bw, filepath.Base(src), fi.ModTime())
	if err != nil {
		res.Fatal = err
		return
	}
	n, err := copyData(ctx, cw, in, -1, make([]byte, 256<<10), rep)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			res.Canceled = true
		} else {
			res.Fatal = fatal(err)
		}
		return
	}
	for _, step := range []func() error{cw.Close, bw.Flush} {
		if err := step(); err != nil {
			res.Fatal = fatal(err)
			return
		}
	}
	if res.Size, err = tmp.finish(t.out, req.Overwrite); err != nil {
		res.Fatal = fatal(err)
		return
	}
	res.Files, res.Bytes = 1, n
	rep.p.DoneItems = 1
	rep.p.Current = ""
	rep.report(true)
}

// ---- helpers ---------------------------------------------------------------------

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
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
