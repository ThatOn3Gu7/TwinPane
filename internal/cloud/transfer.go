package cloud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
)

// TransferItem is one file or folder to copy or move.
type TransferItem struct {
	Src     Path
	IsDir   bool
	Size    int64 // files: size if known (progress only)
	ModTime time.Time
}

// TransferRequest copies or moves Items into the folder Dest. Sources and
// destination may each be local or on a remote (local → local is done by
// package ops, not here).
type TransferRequest struct {
	Kind  ops.Kind
	Items []TransferItem
	Dest  Path
}

// Transfer runs a copy or move with rclone and reports like ops.Run:
// progress through hooks.Progress, conflicts through hooks.Conflict (nil =
// skip), cancellation through ctx.
//
// Safety:
//   - items that do not exist in the destination are transferred with
//     --ignore-existing, so a file that appears there meanwhile is never
//     replaced;
//   - a file is replaced only after an Overwrite decision (then with
//     --ignore-times, so it is really transferred even when size and time
//     happen to match);
//   - a folder that exists in both places is merged after a Merge decision:
//     files that exist in both are asked about one by one and only those
//     approved are replaced;
//   - moves delete a source only after rclone transferred it; items that
//     were skipped or failed stay in the source.
func (c *Client) Transfer(ctx context.Context, req TransferRequest, hooks ops.Hooks) ops.Result {
	x := &xfer{c: c, ctx: ctx, req: req, hooks: hooks, res: ops.Result{Kind: req.Kind}}
	x.run()
	x.prog.Current = ""
	x.report(true)
	return x.res
}

type xfer struct {
	c      *Client
	ctx    context.Context
	req    TransferRequest
	hooks  ops.Hooks
	res    ops.Result
	prog   ops.Progress
	last   time.Time
	policy *ops.Action

	done  int64            // bytes of finished rclone runs
	files map[string]int   // per folder item: files found by the scan
	bytes map[string]int64 // per item: bytes found by the scan
}

const transferReport = 100 * time.Millisecond

func (x *xfer) report(force bool) {
	if x.hooks.Progress == nil {
		return
	}
	if now := time.Now(); force || now.Sub(x.last) >= transferReport {
		x.last = now
		x.hooks.Progress(x.prog)
	}
}

func (x *xfer) verb() string { return x.req.Kind.String() }

func (x *xfer) fail(p Path, err error) {
	x.res.Errors = append(x.res.Errors, ops.ItemError{Path: p.Display(), Op: x.verb(), Err: err})
	x.prog.Errors++
	x.prog.LastError = err.Error()
	if KindOf(err).Fatal() && KindOf(err) != KindCanceled {
		x.res.Fatal = err
	}
}

func (x *xfer) canceled() bool {
	if x.ctx.Err() != nil {
		x.res.Canceled = true
		return true
	}
	return false
}

func (x *xfer) run() {
	req := x.req
	if len(req.Items) == 0 {
		x.res.Fatal = errors.New("nothing to " + x.verb())
		return
	}
	for _, it := range req.Items {
		if it.Src.IsRoot() {
			x.res.Fatal = fmt.Errorf("the root of %s: cannot be %s as an item: open it and select its contents", it.Src.Remote, strings.ToLower(req.Kind.Past()))
			return
		}
		if it.Src.Dir().Arg() == req.Dest.Arg() {
			x.res.Fatal = fmt.Errorf("%s is already in the destination folder", it.Src.Display())
			return
		}
		if it.IsDir && req.Dest.Within(it.Src) {
			x.res.Fatal = fmt.Errorf("cannot %s the folder %s into itself", x.verb(), it.Src.Display())
			return
		}
	}

	// scan: the destination's contents (for conflicts) and the size of the
	// folders (for the progress total and the summary)
	x.prog = ops.Progress{Phase: "scanning", TotalItems: len(req.Items)}
	x.report(true)
	existing, err := x.destIndex()
	if err != nil {
		if KindOf(err) == KindCanceled {
			x.res.Canceled = true
			return
		}
		if KindOf(err) == KindNotFound {
			err = fmt.Errorf("the destination folder %s no longer exists", req.Dest.Display())
		}
		x.res.Fatal = err
		return
	}
	x.files, x.bytes = map[string]int{}, map[string]int64{}
	var total int64
	for _, it := range req.Items {
		if x.canceled() {
			return
		}
		key := it.Src.Arg()
		if !it.IsDir {
			x.bytes[key] = it.Size
			total += it.Size
			continue
		}
		x.prog.Current = it.Src.Display()
		x.report(false)
		files, err := x.c.ListFiles(x.ctx, it.Src)
		if err != nil {
			if KindOf(err) == KindCanceled {
				x.res.Canceled = true
				return
			}
			x.fail(it.Src, err)
			if x.res.Fatal != nil {
				return
			}
			x.files[key] = -1 // failed: not transferred
			continue
		}
		var b int64
		for _, f := range files {
			b += f.Size
		}
		x.files[key], x.bytes[key] = len(files), b
		total += b
	}

	x.prog = ops.Progress{Phase: map[ops.Kind]string{ops.Copy: "copying", ops.Move: "moving"}[req.Kind],
		TotalItems: len(req.Items), TotalBytes: total}
	x.report(true)
	for _, it := range req.Items {
		if x.canceled() || x.res.Fatal != nil {
			return
		}
		key := it.Src.Arg()
		if x.files[key] < 0 {
			x.prog.DoneItems++
			continue
		}
		name := it.Src.Base()
		dst := req.Dest.Join(name)
		x.prog.Current = it.Src.Display()
		x.report(true)
		ex, clash := existing[name]
		switch {
		case !clash:
			x.transferOne(it, dst, false)
		case ex.IsLink && it.IsDir:
			// A folder is never merged through a symbolic link in the
			// destination: the files would be written where the link
			// points, outside the chosen folder.
			x.fail(it.Src, fmt.Errorf("a symbolic link named %q is in the destination: refusing to merge the folder through it", name))
		case ex.IsDir != it.IsDir:
			what := "file"
			if ex.IsDir {
				what = "folder"
			}
			x.fail(it.Src, fmt.Errorf("a %s named %q already exists in the destination", what, name))
		case it.IsDir:
			switch x.ask(ops.Conflict{Src: it.Src.Display(), Dst: dst.Display(), SrcInfo: info(it.item(name)), DstInfo: info(ex), IsDir: true}) {
			case ops.Cancel:
				x.res.Canceled = true
				return
			case ops.Skip:
				x.res.Skipped++
				x.done += x.bytes[key]
			default:
				x.merge(it, dst)
			}
		default:
			switch x.ask(ops.Conflict{Src: it.Src.Display(), Dst: dst.Display(), SrcInfo: info(it.item(name)), DstInfo: info(ex)}) {
			case ops.Cancel:
				x.res.Canceled = true
				return
			case ops.Skip:
				x.res.Skipped++
				x.done += x.bytes[key]
			default:
				x.transferOne(it, dst, true)
			}
		}
		x.prog.DoneItems++
		x.prog.DoneBytes = x.done
		x.report(true)
	}
}

func (it TransferItem) item(name string) Item {
	return Item{Name: name, Size: it.Size, ModTime: it.ModTime, IsDir: it.IsDir}
}

// ask resolves a conflict (a remembered "apply to all" answer first).
func (x *xfer) ask(c ops.Conflict) ops.Action {
	if x.policy != nil {
		return *x.policy
	}
	if x.hooks.Conflict == nil {
		return ops.Skip
	}
	d := x.hooks.Conflict(c)
	if d.ApplyAll && d.Action != ops.Cancel {
		a := d.Action
		x.policy = &a
	}
	return d.Action
}

// destIndex lists the destination folder by name.
func (x *xfer) destIndex() (map[string]Item, error) {
	idx := map[string]Item{}
	d := x.req.Dest
	if d.IsLocal() {
		des, err := os.ReadDir(d.Path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, &Error{Op: "list", Kind: KindNotFound, Msg: err.Error()}
			}
			return nil, err
		}
		for _, de := range des {
			p := d.Join(de.Name()).Path
			li, lerr := os.Lstat(p) // inspect the entry itself, without following a link
			link := lerr == nil && li.Mode()&fs.ModeSymlink != 0
			fi, err := os.Stat(p) // a link to a folder counts as a folder
			if err != nil {
				fi, err = li, lerr
			}
			if err != nil {
				idx[de.Name()] = Item{Name: de.Name(), IsLink: link}
				continue
			}
			idx[de.Name()] = Item{Name: de.Name(), Size: fi.Size(), ModTime: fi.ModTime(), IsDir: fi.IsDir(), IsLink: link}
		}
		return idx, nil
	}
	items, err := x.c.ListItems(x.ctx, d)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		idx[it.Name] = it
	}
	return idx, nil
}

// transferOne copies or moves one item that does not exist in the
// destination (overwrite=false) or replaces an existing file (true).
func (x *xfer) transferOne(it TransferItem, dst Path, overwrite bool) {
	key := it.Src.Arg()
	var args []string
	if it.IsDir {
		args = []string{map[ops.Kind]string{ops.Copy: "copy", ops.Move: "move"}[x.req.Kind], "--create-empty-src-dirs"}
		if x.req.Kind == ops.Move {
			args = append(args, "--delete-empty-src-dirs")
		}
	} else {
		args = []string{map[ops.Kind]string{ops.Copy: "copyto", ops.Move: "moveto"}[x.req.Kind]}
	}
	if overwrite {
		args = append(args, "--ignore-times")
	} else {
		args = append(args, "--ignore-existing")
	}
	st, err := x.rclone(args, it.Src, dst, x.bytes[key])
	if err != nil {
		if KindOf(err) == KindCanceled {
			x.res.Canceled = true
			return
		}
		x.fail(it.Src, err)
		return
	}
	if it.IsDir {
		x.res.Dirs++
		x.res.Files += x.files[key]
		x.res.Bytes += x.bytes[key]
		return
	}
	if !overwrite && st.moved() == 0 {
		// --ignore-existing kept a file that appeared meanwhile
		x.res.Skipped++
		return
	}
	x.res.Files++
	x.res.Bytes += x.bytes[key]
}

// merge puts a folder into an existing folder of the same name.
func (x *xfer) merge(it TransferItem, dst Path) {
	src, err := x.c.ListFiles(x.ctx, it.Src)
	if err == nil && dst.IsLocal() {
		err = checkLocalMergeDirs(dst, src)
	}
	if err == nil {
		var have []Item
		if have, err = x.c.ListFiles(x.ctx, dst); err == nil {
			x.mergeFiles(it, dst, src, have)
			return
		}
	}
	if KindOf(err) == KindCanceled {
		x.res.Canceled = true
		return
	}
	x.fail(it.Src, err)
}

func checkLocalMergeDirs(dst Path, src []Item) error {
	root, err := os.Lstat(dst.Path)
	if err != nil {
		return fmt.Errorf("cannot safely inspect the local merge destination %s: %w", dst.Display(), err)
	}
	if root.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("the local merge destination %s is a symbolic link: refusing to merge through it", dst.Display())
	}
	for _, f := range src {
		if f.Path == "" {
			return fmt.Errorf("cannot safely merge path %q into the local destination", f.Path)
		}
		for _, part := range strings.Split(f.Path, "/") {
			if part == ".." {
				return fmt.Errorf("cannot safely merge path %q into the local destination", f.Path)
			}
		}
		rel := filepath.Clean(filepath.FromSlash(f.Path))
		if filepath.IsAbs(rel) || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			return fmt.Errorf("cannot safely merge path %q into the local destination", f.Path)
		}
		dir := filepath.Dir(rel)
		if dir == "." {
			continue
		}
		p := dst.Path
		for _, part := range strings.Split(dir, string(os.PathSeparator)) {
			if part == "" || part == "." {
				continue
			}
			p = filepath.Join(p, part)
			fi, err := os.Lstat(p)
			if errors.Is(err, fs.ErrNotExist) {
				break // the remaining directories will be newly created
			}
			if err != nil {
				return fmt.Errorf("cannot safely inspect local merge path %q: %w", p, err)
			}
			if fi.Mode()&fs.ModeSymlink != 0 {
				return fmt.Errorf("a symbolic link directory is in the local merge path %q: refusing to merge through it", p)
			}
			if !fi.IsDir() {
				break // an ordinary file blocks this path; rclone will report the conflict
			}
		}
	}
	return nil
}

func (x *xfer) mergeFiles(it TransferItem, dst Path, src, have []Item) {
	byPath := make(map[string]Item, len(have))
	for _, h := range have {
		byPath[h.Path] = h
	}
	var replace []string
	var replaceBytes, keptBytes int64
	skipped := 0
	for _, f := range src {
		h, ok := byPath[f.Path]
		if !ok {
			continue
		}
		if strings.ContainsAny(f.Path, "\n\r") {
			x.fail(it.Src.Join(f.Path), errors.New("names with line breaks cannot be replaced in a merge; it was kept"))
			skipped++
			continue
		}
		switch x.ask(ops.Conflict{Src: it.Src.Join(f.Path).Display(), Dst: dst.Join(f.Path).Display(), SrcInfo: info(f), DstInfo: info(h)}) {
		case ops.Cancel:
			x.res.Canceled = true
			return
		case ops.Skip:
			skipped++
			keptBytes += f.Size
		default:
			replace = append(replace, f.Path)
			replaceBytes += f.Size
		}
	}
	key := it.Src.Arg()
	verb := map[ops.Kind]string{ops.Copy: "copy", ops.Move: "move"}[x.req.Kind]
	// 1. everything that is not in the destination yet
	args := []string{verb, "--create-empty-src-dirs", "--ignore-existing"}
	if x.req.Kind == ops.Move {
		args = append(args, "--delete-empty-src-dirs")
	}
	if dst.IsLocal() {
		if err := checkLocalMergeDirs(dst, src); err != nil {
			x.fail(it.Src, err)
			return
		}
	}
	fresh := x.bytes[key] - replaceBytes - keptBytes
	if _, err := x.rclone(args, it.Src, dst, fresh); err != nil {
		if KindOf(err) == KindCanceled {
			x.res.Canceled = true
			return
		}
		x.fail(it.Src, err)
		return
	}
	// 2. the files the user chose to replace
	if len(replace) > 0 {
		list, err := os.CreateTemp("", "twinpane-rclone-*.txt")
		if err != nil {
			x.fail(it.Src, err)
			return
		}
		defer os.Remove(list.Name())
		_, werr := list.WriteString(strings.Join(replace, "\n") + "\n")
		if cerr := list.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			x.fail(it.Src, werr)
			return
		}
		args := []string{verb, "--ignore-times", "--files-from-raw", list.Name()}
		if x.req.Kind == ops.Move {
			args = append(args, "--delete-empty-src-dirs")
		}
		if dst.IsLocal() {
			if err := checkLocalMergeDirs(dst, src); err != nil {
				x.fail(it.Src, err)
				return
			}
		}
		if _, err := x.rclone(args, it.Src, dst, replaceBytes); err != nil {
			if KindOf(err) == KindCanceled {
				x.res.Canceled = true
				return
			}
			x.fail(it.Src, err)
			return
		}
	}
	x.res.Dirs++
	x.res.Files += x.files[key] - skipped
	x.res.Skipped += skipped
	x.res.Bytes += x.bytes[key] - keptBytes
	x.done += keptBytes
}

// stats is the part of rclone's statistics TwinPane uses.
type stats struct {
	Bytes            int64  `json:"bytes"`
	TotalBytes       int64  `json:"totalBytes"`
	Transfers        int    `json:"transfers"`
	Renames          int    `json:"renames"`
	ServerSideCopies int    `json:"serverSideCopies"`
	ServerSideMoves  int    `json:"serverSideMoves"`
	Errors           int    `json:"errors"`
	LastError        string `json:"lastError"`
	Transferring     []struct {
		Name string `json:"name"`
	} `json:"transferring"`
}

func (s stats) moved() int { return s.Transfers + s.Renames + s.ServerSideCopies + s.ServerSideMoves }

// transferFlags make rclone report statistics as JSON on stderr.
var transferFlags = []string{"--use-json-log", "--stats=500ms", "--stats-log-level=NOTICE", "--retries=2"}

// rclone runs one transfer command (args: subcommand and its flags) from
// src to dst; expect is the number of bytes it should move (progress).
func (x *xfer) rclone(args []string, src, dst Path, expect int64) (stats, error) {
	full := append([]string{args[0]}, transferFlags...)
	full = append(full, args[1:]...)
	full = append(full, "--", src.Arg(), dst.Arg())
	var last stats
	base := x.done
	folder := args[0] == "copy" || args[0] == "move" // else copyto / moveto of one file
	stderr, st, err := x.c.run(x.ctx, full, nil, func(l string) {
		if !strings.HasPrefix(l, "{") {
			return
		}
		var j struct {
			Level string `json:"level"`
			Msg   string `json:"msg"`
			Stats *stats `json:"stats"`
		}
		if json.Unmarshal([]byte(l), &j) != nil {
			return
		}
		if j.Stats != nil {
			last = *j.Stats
			p := x.prog
			p.DoneBytes = base + j.Stats.Bytes
			if extra := j.Stats.TotalBytes - expect; extra > 0 {
				p.TotalBytes += extra // more than the scan found (files added meanwhile)
			}
			if len(j.Stats.Transferring) > 0 && folder {
				p.Current = src.Join(j.Stats.Transferring[0].Name).Display()
			}
			x.prog.DoneBytes, x.prog.TotalBytes = p.DoneBytes, p.TotalBytes
			if x.hooks.Progress != nil && time.Since(x.last) >= transferReport {
				x.last = time.Now()
				x.hooks.Progress(p)
			}
		}
	})
	x.done = base + expect
	if last.Bytes > expect {
		x.done = base + last.Bytes
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return last, &Error{Op: x.verb(), Kind: KindCanceled, Msg: "cancelled"}
		}
		return last, &Error{Op: x.verb(), Kind: KindOther, Msg: err.Error()}
	}
	if !st.Success() {
		remote := src.Remote
		if remote == "" {
			remote = dst.Remote
		}
		return last, explain(x.verb(), remote, st, stderr)
	}
	return last, nil
}

// info adapts an Item to fs.FileInfo for ops.Conflict.
func info(it Item) fs.FileInfo { return itemInfo{it} }

type itemInfo struct{ it Item }

func (i itemInfo) Name() string       { return i.it.Name }
func (i itemInfo) Size() int64        { return i.it.Size }
func (i itemInfo) ModTime() time.Time { return i.it.ModTime }
func (i itemInfo) IsDir() bool        { return i.it.IsDir }
func (i itemInfo) Sys() any           { return nil }
func (i itemInfo) Mode() fs.FileMode {
	if i.it.IsDir {
		return fs.ModeDir | 0o755
	}
	return 0o644
}
