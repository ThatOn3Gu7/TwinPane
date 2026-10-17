package ops

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
)

const (
	bufSize        = 256 << 10
	tmpPrefix      = ".twinpane-"
	reportInterval = 50 * time.Millisecond
)

// Test seams. moveRename performs the source->destination rename of a move
// (replace selects replace-vs-no-replace semantics) and may be swapped to
// simulate cross-filesystem moves; wrapWriter can inject write failures.
var (
	moveRename = func(src, dst string, replace bool) error {
		if replace {
			return os.Rename(src, dst)
		}
		return renameNoReplace(src, dst)
	}
	wrapWriter  = func(w io.Writer, dst string) io.Writer { return w }
	freeSpaceFn = freeSpace
)

type treeStats struct {
	files, dirs int
	bytes       int64
}

type engine struct {
	ctx        context.Context
	req        Request
	hooks      Hooks
	res        Result
	prog       Progress
	lastReport time.Time
	policy     *Action
	tops       map[string]treeStats
	buf        []byte
}

// Run executes a copy or move. It always returns a Result describing exactly
// what happened; it never panics on filesystem errors.
func Run(ctx context.Context, req Request, hooks Hooks) Result {
	e := &engine{ctx: ctx, req: req, hooks: hooks, res: Result{Kind: req.Kind}, tops: map[string]treeStats{}}
	if err := Validate(req); err != nil {
		e.res.Fatal = err
		return e.res
	}
	e.prog.Phase = "scanning"
	e.report(true)
	need := e.scan()
	if e.checkCanceled() {
		return e.res
	}
	if need > 0 {
		if free, err := freeSpaceFn(req.DestDir); err == nil && uint64(need) > free {
			if hooks.LowSpace == nil || !hooks.LowSpace(uint64(need), free) {
				e.res.Fatal = fmt.Errorf("not enough free space in %s (need %s, %s available)",
					req.DestDir, fsys.HumanSize(need), fsys.HumanSize(int64(free)))
				return e.res
			}
		}
	}
	if req.Kind == Move {
		e.prog.Phase = "moving"
	} else {
		e.prog.Phase = "copying"
	}
	for _, src := range req.Sources {
		if e.stopped() || e.checkCanceled() {
			break
		}
		src = filepath.Clean(src)
		e.transfer(src, filepath.Join(req.DestDir, filepath.Base(src)), true)
	}
	e.prog.Current = ""
	e.report(true)
	return e.res
}

// scan counts items/bytes for progress and returns the number of bytes that
// will actually have to be written (same-filesystem moves need none).
func (e *engine) scan() int64 {
	var need int64
	for _, src := range e.req.Sources {
		src = filepath.Clean(src)
		var st treeStats
		_ = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
			if e.ctx.Err() != nil {
				return e.ctx.Err()
			}
			if err != nil {
				return nil // reported later, during the transfer
			}
			if d.IsDir() {
				st.dirs++
			} else {
				st.files++
				if d.Type().IsRegular() {
					if info, err := d.Info(); err == nil {
						st.bytes += info.Size()
					}
				}
			}
			return nil
		})
		e.tops[src] = st
		e.prog.TotalItems += st.files + st.dirs
		e.prog.TotalBytes += st.bytes
		if e.req.Kind == Copy {
			need += st.bytes
		} else if same, err := sameDeviceFn(filepath.Dir(src), e.req.DestDir); err != nil || !same {
			need += st.bytes
		}
	}
	return need
}

var sameDeviceFn = sameDevice

func (e *engine) report(force bool) {
	if e.hooks.Progress == nil {
		return
	}
	now := time.Now()
	if !force && now.Sub(e.lastReport) < reportInterval {
		return
	}
	e.lastReport = now
	e.hooks.Progress(e.prog)
}

func (e *engine) stopped() bool { return e.res.Canceled || e.res.Fatal != nil }

func (e *engine) checkCanceled() bool {
	if e.ctx.Err() != nil {
		e.res.Canceled = true
	}
	return e.res.Canceled
}

func (e *engine) fail(path, op string, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		e.res.Canceled = true
		return
	}
	if isNoSpace(err) {
		e.res.Fatal = fmt.Errorf("destination is full while writing %s: %w", path, err)
		e.prog.Errors++
		e.prog.LastError = e.res.Fatal.Error()
		return
	}
	ie := ItemError{Path: path, Op: op, Err: err}
	e.res.Errors = append(e.res.Errors, ie)
	e.prog.Errors++
	e.prog.LastError = ie.Error()
	e.report(true)
}

func (e *engine) leftover(src string, err error) {
	ie := ItemError{Path: src, Op: "remove source after verified copy", Err: err}
	e.res.Leftovers = append(e.res.Leftovers, ie)
	e.prog.Errors++
	e.prog.LastError = ie.Error()
}

func (e *engine) resolve(c Conflict) Action {
	if e.policy != nil {
		return *e.policy
	}
	if e.hooks.Conflict == nil {
		return Skip
	}
	d := e.hooks.Conflict(c)
	if e.ctx.Err() != nil {
		return Cancel
	}
	if d.ApplyAll && d.Action != Cancel {
		a := d.Action
		e.policy = &a
	}
	return d.Action
}

// transfer copies or moves one entry. It returns true when the entry was
// completely transferred (for moves: the source no longer exists).
func (e *engine) transfer(src, dst string, top bool) bool {
	if e.stopped() || e.checkCanceled() {
		return false
	}
	op := e.req.Kind.String()
	si, err := os.Lstat(src)
	if err != nil {
		e.fail(src, op, err)
		return false
	}
	e.prog.Current = src
	e.report(false)

	overwrite := false
	di, err := os.Lstat(dst)
	switch {
	case err == nil:
		if os.SameFile(si, di) {
			e.fail(src, op, errors.New("source and destination are the same file"))
			return false
		}
		if si.IsDir() != di.IsDir() {
			e.fail(src, op, fmt.Errorf("destination %s exists and is a %s; it was not replaced", dst, typeName(di.Mode())))
			return false
		}
		switch e.resolve(Conflict{Src: src, Dst: dst, SrcInfo: si, DstInfo: di, IsDir: si.IsDir()}) {
		case Overwrite:
			overwrite = true
		case Cancel:
			e.res.Canceled = true
			return false
		default:
			e.res.Skipped++
			e.prog.DoneItems++
			return false
		}
	case errors.Is(err, fs.ErrNotExist):
	default:
		e.fail(dst, "check destination", err)
		return false
	}

	mode := si.Mode()
	switch {
	case mode.IsDir():
		return e.transferDir(src, dst, si, overwrite, top)
	case mode&fs.ModeSymlink != 0:
		return e.transferLink(src, dst, overwrite)
	case mode.IsRegular():
		return e.transferFile(src, dst, si, overwrite)
	default:
		e.fail(src, op, fmt.Errorf("unsupported file type (%s); skipped", typeName(mode)))
		return false
	}
}

func (e *engine) transferDir(src, dst string, si fs.FileInfo, merge, top bool) bool {
	if e.req.Kind == Move && !merge {
		err := moveRename(src, dst, false)
		if err == nil {
			st, ok := e.tops[src]
			if !top || !ok {
				st = treeStats{dirs: 1}
			}
			e.res.Dirs += st.dirs
			e.res.Files += st.files
			e.res.Bytes += st.bytes
			e.prog.DoneItems += st.files + st.dirs
			e.prog.DoneBytes += st.bytes
			e.report(false)
			return true
		}
		if !isCrossDevice(err) {
			e.fail(src, "move", err)
			return false
		}
		// different filesystem: copy, verify, then remove
	}

	created := false
	if !merge {
		if err := os.Mkdir(dst, 0o700); err != nil {
			e.fail(dst, "create folder", err)
			return false
		}
		created = true
		e.res.Dirs++
	}
	e.prog.DoneItems++

	entries, err := os.ReadDir(src)
	if err != nil {
		e.fail(src, "read folder", err)
		e.finishDir(dst, si, created)
		return false
	}
	all := true
	for _, ent := range entries {
		if e.stopped() || e.checkCanceled() {
			all = false
			break
		}
		if !e.transfer(filepath.Join(src, ent.Name()), filepath.Join(dst, ent.Name()), false) {
			all = false
		}
	}
	e.finishDir(dst, si, created)
	if e.stopped() {
		return false
	}
	if e.req.Kind == Move && all {
		// Only an empty directory can be removed: os.Remove never deletes contents.
		if err := os.Remove(src); err != nil {
			e.leftover(src, err)
			return false
		}
	}
	return all
}

func (e *engine) finishDir(dst string, si fs.FileInfo, created bool) {
	if !created {
		return // never alter attributes of a pre-existing folder
	}
	if err := os.Chmod(dst, si.Mode().Perm()); err != nil {
		e.fail(dst, "set folder permissions", err)
	}
	_ = os.Chtimes(dst, si.ModTime(), si.ModTime())
}

func (e *engine) transferLink(src, dst string, overwrite bool) bool {
	target, err := os.Readlink(src)
	if err != nil {
		e.fail(src, "read link", err)
		return false
	}
	if e.req.Kind == Move {
		err := moveRename(src, dst, overwrite)
		if err == nil {
			e.res.Files++
			e.prog.DoneItems++
			return true
		}
		if !isCrossDevice(err) {
			e.fail(src, "move", err)
			return false
		}
	}
	tmp, err := tempPath(filepath.Dir(dst))
	if err != nil {
		e.fail(dst, "create link", err)
		return false
	}
	if err := os.Symlink(target, tmp); err != nil {
		e.fail(dst, "create link", err)
		return false
	}
	if err := placeFile(tmp, dst, overwrite); err != nil {
		os.Remove(tmp)
		e.fail(dst, "create link", err)
		return false
	}
	e.res.Files++
	e.prog.DoneItems++
	if e.req.Kind == Move {
		if got, err := os.Readlink(dst); err != nil || got != target {
			e.fail(src, "verify link", fmt.Errorf("copied link does not match source; source kept"))
			return false
		}
		if err := os.Remove(src); err != nil {
			e.leftover(src, err)
			return false
		}
	}
	return true
}

func (e *engine) transferFile(src, dst string, si fs.FileInfo, overwrite bool) bool {
	if e.req.Kind == Move {
		err := moveRename(src, dst, overwrite)
		if err == nil {
			e.res.Files++
			e.res.Bytes += si.Size()
			e.prog.DoneItems++
			e.prog.DoneBytes += si.Size()
			e.report(false)
			return true
		}
		if !isCrossDevice(err) {
			e.fail(src, "move", err)
			return false
		}
	}
	n, err := e.copyFile(src, dst, si, overwrite, e.req.Kind == Move)
	if err != nil {
		e.fail(src, e.req.Kind.String(), err)
		return false
	}
	e.res.Files++
	e.res.Bytes += n
	e.prog.DoneItems++
	if e.req.Kind == Move {
		if err := os.Remove(src); err != nil {
			e.leftover(src, err)
			return false
		}
	}
	return true
}

// copyFile writes src to a temporary file next to dst and renames it into
// place only after everything succeeded. With verify, the data is synced to
// disk and re-read to compare a SHA-256 digest with the bytes read from src.
func (e *engine) copyFile(src, dst string, si fs.FileInfo, overwrite, verify bool) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dst), tmpPrefix+"*.tmp")
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

	var h hash.Hash
	var w io.Writer = tmp
	if verify {
		h = sha256.New()
	}
	w = wrapWriter(w, dst)
	if e.buf == nil {
		e.buf = make([]byte, bufSize)
	}
	var n int64
	for {
		if err := e.ctx.Err(); err != nil {
			return n, err
		}
		r, rerr := in.Read(e.buf)
		if r > 0 {
			if _, werr := w.Write(e.buf[:r]); werr != nil {
				return n, werr
			}
			if h != nil {
				h.Write(e.buf[:r])
			}
			n += int64(r)
			e.prog.DoneBytes += int64(r)
			e.report(false)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			return n, rerr
		}
	}
	if n != si.Size() {
		return n, fmt.Errorf("source changed during copy (expected %d bytes, read %d)", si.Size(), n)
	}
	_ = tmp.Chmod(si.Mode().Perm())
	if verify {
		if err := tmp.Sync(); err != nil {
			return n, err
		}
	}
	if err := tmp.Close(); err != nil {
		return n, err
	}
	_ = os.Chtimes(tmpName, si.ModTime(), si.ModTime())
	if verify {
		got, err := hashFile(tmpName)
		if err != nil {
			return n, fmt.Errorf("verify: %w", err)
		}
		if !bytes.Equal(got, h.Sum(nil)) {
			return n, errors.New("verification failed: the copy differs from the source")
		}
	}
	if err := placeFile(tmpName, dst, overwrite); err != nil {
		return n, err
	}
	done = true
	return n, nil
}

// placeFile renames a finished temporary file onto dst. Without overwrite it
// refuses to replace anything that appeared at dst in the meantime.
func placeFile(tmp, dst string, overwrite bool) error {
	if overwrite {
		return os.Rename(tmp, dst)
	}
	return renameNoReplace(tmp, dst)
}

func tempPath(dir string) (string, error) {
	f, err := os.CreateTemp(dir, tmpPrefix+"*.tmp")
	if err != nil {
		return "", err
	}
	name := f.Name()
	f.Close()
	if err := os.Remove(name); err != nil {
		return "", err
	}
	return name, nil
}

func hashFile(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

func typeName(m fs.FileMode) string {
	switch {
	case m.IsDir():
		return "folder"
	case m&fs.ModeSymlink != 0:
		return "symlink"
	case m.IsRegular():
		return "file"
	case m&fs.ModeNamedPipe != 0:
		return "named pipe"
	case m&fs.ModeSocket != 0:
		return "socket"
	case m&fs.ModeDevice != 0:
		return "device"
	default:
		return "special file"
	}
}
