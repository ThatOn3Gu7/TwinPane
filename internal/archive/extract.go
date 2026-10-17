package archive

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
)

// ExtractRequest describes an extraction.
type ExtractRequest struct {
	Archive string   // path of the ZIP file
	Members []string // inner paths to extract (files or folders, recursively); empty = the whole archive
	Base    string   // inner folder the members are relative to: Base/x/y is written to DestDir/x/y
	DestDir string   // absolute path; created if it does not exist
}

// ExtractResult summarises a finished extraction.
type ExtractResult struct {
	Dest     string
	Files    int   // files written
	Dirs     int   // folders created
	Bytes    int64 // bytes written
	Skipped  int   // existing files kept (conflict: skip)
	Links    int   // symbolic links in the selection (never extracted)
	Unsafe   int   // unsafe names in the archive (never extracted)
	Errors   []ops.ItemError
	Canceled bool
	Fatal    error
	Top      []string // top-level names written in Dest, in order
}

// OK reports a complete run without errors (skips and links are not errors).
func (r ExtractResult) OK() bool { return !r.Canceled && r.Fatal == nil && len(r.Errors) == 0 }

// Summary returns a one-line description.
func (r ExtractResult) Summary() string {
	s := fmt.Sprintf("Extracted %s", plural(r.Files, "file"))
	if r.Dirs > 0 {
		s += ", " + plural(r.Dirs, "new folder")
	}
	if r.Skipped > 0 {
		s += fmt.Sprintf(", kept %d existing", r.Skipped)
	}
	if r.Links > 0 {
		s += fmt.Sprintf(", %s not extracted", plural(r.Links, "symbolic link"))
	}
	if len(r.Errors) > 0 {
		s += ", " + plural(len(r.Errors), "error")
	}
	switch {
	case r.Fatal != nil:
		s = fmt.Sprintf("Extraction stopped: %v (%s)", r.Fatal, s)
	case r.Canceled:
		s = fmt.Sprintf("Extraction cancelled (%s; the rest was not extracted)", s)
	}
	return s
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

// progressEvery throttles progress reports.
const progressEvery = 80 * time.Millisecond

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

// Extract writes the requested entries below req.DestDir.
//
// Safety rules:
//   - only names that passed cleanName are extracted, and every target path
//     is checked to lie inside DestDir;
//   - no folder below DestDir is written through a symbolic link: an
//     existing link (or non-folder) where a folder is needed is an error;
//   - existing files are only replaced after Hooks.Conflict answered
//     Overwrite (a nil hook skips), and existing folders are merged into;
//   - every file is written to a temporary name and renamed into place, so
//     damaged data (CRC mismatch, data longer than stored), errors or
//     cancellation never leave a partial file or destroy the file it was
//     to replace;
//   - symbolic links are never created, and permissions are reduced to
//     0644 / 0755 (no set-id bits, no world-writable files).
func Extract(ctx context.Context, req ExtractRequest, hooks ops.Hooks) ExtractResult {
	res := ExtractResult{Dest: req.DestDir}
	rep := &reporter{hooks: hooks, p: ops.Progress{Phase: "scanning"}}
	if !filepath.IsAbs(req.DestDir) {
		res.Fatal = fmt.Errorf("destination %q is not an absolute path", req.DestDir)
		return res
	}
	dest := filepath.Clean(req.DestDir)
	res.Dest = dest
	if fi, err := os.Stat(dest); err == nil {
		if !fi.IsDir() {
			res.Fatal = fmt.Errorf("destination %s exists and is not a folder", dest)
			return res
		}
	} else if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(dest, 0o755); err != nil {
			res.Fatal = fmt.Errorf("cannot create the destination: %w", err)
			return res
		}
	} else {
		res.Fatal = fmt.Errorf("destination: %w", err)
		return res
	}

	zr, err := zip.OpenReader(req.Archive)
	if err != nil {
		res.Fatal = describeOpenErr(err)
		return res
	}
	defer zr.Close()
	a := build(zr.File)
	res.Unsafe = len(a.Unsafe)
	byRaw := map[string]*zip.File{}
	for _, f := range zr.File {
		if _, ok := byRaw[f.Name]; !ok {
			byRaw[f.Name] = f
		}
	}

	base := strings.Trim(req.Base, "/")
	members := req.Members
	if len(members) == 0 {
		members = []string{base}
	}
	var todo []*Entry
	seen := map[string]bool{}
	for _, m := range members {
		es := a.Walk(m)
		if es == nil {
			res.Errors = append(res.Errors, ops.ItemError{Path: m, Op: "extract", Err: ErrNotFound})
			continue
		}
		for _, e := range es {
			if !seen[e.Inner] {
				seen[e.Inner] = true
				todo = append(todo, e)
			}
		}
	}

	var total int64
	files := 0
	for _, e := range todo {
		if !e.IsDir {
			files++
			if !e.Symlink && e.Supported() == nil {
				total += int64(e.Size)
			}
		}
	}
	rep.p = ops.Progress{Phase: "extracting", TotalItems: files, TotalBytes: total}
	rep.report(true)
	if free, err := ops.FreeSpace(dest); err == nil && uint64(total) > free {
		if hooks.LowSpace == nil || !hooks.LowSpace(uint64(total), free) {
			res.Canceled = true
			return res
		}
	}

	x := &extractor{ctx: ctx, dest: dest, res: &res, rep: rep, hooks: hooks, top: map[string]bool{}}
	for _, e := range todo {
		if ctx.Err() != nil {
			res.Canceled = true
			break
		}
		rel := e.Inner
		if base != "" {
			r, ok := strings.CutPrefix(e.Inner, base+"/")
			if !ok {
				x.fail(e.Inner, fmt.Errorf("not inside %s", base))
				continue
			}
			rel = r
		}
		x.entry(e, rel, byRaw[e.raw])
		if res.Canceled || res.Fatal != nil {
			break
		}
	}
	rep.p.Current = ""
	rep.report(true)
	return res
}

type extractor struct {
	ctx      context.Context
	dest     string
	res      *ExtractResult
	rep      *reporter
	hooks    ops.Hooks
	applyAll *ops.Action
	top      map[string]bool
}

func (x *extractor) fail(inner string, err error) {
	x.res.Errors = append(x.res.Errors, ops.ItemError{Path: inner, Op: "extract", Err: err})
	x.rep.p.Errors++
	x.rep.p.LastError = err.Error()
}

// target returns the filesystem path for rel, verified to be inside dest.
func (x *extractor) target(rel string) (string, error) {
	t := filepath.Join(x.dest, filepath.FromSlash(rel))
	r, err := filepath.Rel(x.dest, t)
	if err != nil || r == "." || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) || filepath.IsAbs(r) {
		return "", fmt.Errorf("unsafe path %q refused", rel)
	}
	return t, nil
}

func (x *extractor) noteTop(rel string) {
	first, _, _ := strings.Cut(rel, "/")
	if !x.top[first] {
		x.top[first] = true
		x.res.Top = append(x.res.Top, first)
	}
}

// ensureDirs creates the folders of rel (a "/"-separated path below dest)
// one by one, refusing to pass through anything that is not a real folder.
func (x *extractor) ensureDirs(rel string) error {
	if rel == "" || rel == "." {
		return nil
	}
	cur := x.dest
	for _, part := range strings.Split(rel, "/") {
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if err := os.Mkdir(cur, 0o755); err != nil {
				return err
			}
			x.res.Dirs++
		case err != nil:
			return err
		case fi.Mode()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s is a symbolic link: refusing to extract through it", cur)
		case !fi.IsDir():
			return fmt.Errorf("%s exists and is not a folder", cur)
		}
	}
	return nil
}

func (x *extractor) entry(e *Entry, rel string, zf *zip.File) {
	t, err := x.target(rel)
	if err != nil {
		x.fail(e.Inner, err)
		return
	}
	x.rep.p.Current = t
	if e.IsDir {
		if e.Implicit {
			return // created (or refused) with the entries below it
		}
		if err := x.ensureDirs(rel); err != nil {
			x.fail(e.Inner, err)
			return
		}
		x.noteTop(rel)
		return
	}
	defer func() { x.rep.p.DoneItems++; x.rep.report(false) }()
	if e.Symlink {
		x.res.Links++
		return
	}
	if err := e.Supported(); err != nil {
		x.fail(e.Inner, err)
		return
	}
	if zf == nil {
		x.fail(e.Inner, ErrNotFound)
		return
	}
	parentRel := ""
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		parentRel = rel[:i]
	}
	if err := x.ensureDirs(parentRel); err != nil {
		x.fail(e.Inner, err)
		return
	}
	overwrite := false
	if di, err := os.Lstat(t); err == nil {
		if di.IsDir() {
			x.fail(e.Inner, fmt.Errorf("a folder named %q already exists there", filepath.Base(t)))
			return
		}
		switch x.resolve(ops.Conflict{Src: e.Inner, Dst: t, SrcInfo: zf.FileInfo(), DstInfo: di}) {
		case ops.Skip:
			x.res.Skipped++
			return
		case ops.Cancel:
			x.res.Canceled = true
			return
		}
		overwrite = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		x.fail(e.Inner, err)
		return
	}
	n, err := x.write(zf, e, t, overwrite)
	x.res.Bytes += n
	if err != nil {
		if errors.Is(err, context.Canceled) {
			x.res.Canceled = true
			return
		}
		if ops.IsNoSpace(err) {
			x.res.Fatal = errors.New("the destination is full")
			return
		}
		x.fail(e.Inner, err)
		return
	}
	x.res.Files++
	x.noteTop(rel)
}

func (x *extractor) resolve(c ops.Conflict) ops.Action {
	if x.applyAll != nil {
		return *x.applyAll
	}
	if x.hooks.Conflict == nil {
		return ops.Skip
	}
	d := x.hooks.Conflict(c)
	if d.ApplyAll && d.Action != ops.Cancel {
		act := d.Action
		x.applyAll = &act
	}
	return d.Action
}

// write decompresses zf to a temporary file next to t and renames it onto t.
func (x *extractor) write(zf *zip.File, e *Entry, t string, overwrite bool) (int64, error) {
	rc, err := zf.Open()
	if err != nil {
		if errors.Is(err, zip.ErrAlgorithm) {
			return 0, ErrUnsupported
		}
		return 0, err
	}
	defer rc.Close()
	tmp, err := os.CreateTemp(filepath.Dir(t), ops.TempPrefix+"*.tmp")
	if err != nil {
		return 0, err
	}
	tmpName := tmp.Name()
	done := false
	defer func() {
		if !done {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()
	var n int64
	buf := make([]byte, 256<<10)
	for {
		if err := x.ctx.Err(); err != nil {
			return n, err
		}
		k, rerr := rc.Read(buf)
		if k > 0 {
			if _, werr := tmp.Write(buf[:k]); werr != nil {
				return n, werr
			}
			n += int64(k)
			x.rep.p.DoneBytes += int64(k)
			x.rep.report(false)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			if errors.Is(rerr, zip.ErrChecksum) || errors.Is(rerr, zip.ErrFormat) {
				return n, fmt.Errorf("damaged data (%v): nothing written", rerr)
			}
			return n, rerr
		}
	}
	if err := tmp.Close(); err != nil {
		return n, err
	}
	perm := fs.FileMode(0o644)
	if e.Mode.Perm()&0o100 != 0 {
		perm = 0o755
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return n, err
	}
	if !e.Modified.IsZero() {
		_ = os.Chtimes(tmpName, e.Modified, e.Modified) // best effort
	}
	if err := ops.PlaceFile(tmpName, t, overwrite); err != nil {
		return n, err
	}
	done = true
	return n, nil
}
