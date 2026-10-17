package archive

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
	"github.com/ThatOn3Gu7/TwinPane/internal/proc"
)

// ExtractAny extracts the archive or compressed file req.Archive in
// whatever supported format it is (see Detect): ZIP with Extract, TAR and
// compressed TAR and single compressed files natively, 7Z and RAR with the
// 7-Zip program req.SevenZip. Every format follows the safety rules of
// Extract: nothing is written outside DestDir or through a symbolic link,
// existing files are replaced only after Hooks.Conflict answered Overwrite,
// files appear only when complete, links and special files are never
// created, permissions are reduced to 0644 / 0755.
func ExtractAny(ctx context.Context, req ExtractRequest, hooks ops.Hooks) ExtractResult {
	info, err := Detect(req.Archive)
	if err != nil {
		return ExtractResult{Dest: req.DestDir, Fatal: err}
	}
	switch info.Format {
	case FormatZIP:
		return Extract(ctx, req, hooks)
	case FormatTAR, FormatSingle, Format7z, FormatRAR:
		if len(req.Members) > 0 {
			return ExtractResult{Dest: req.DestDir, Fatal: fmt.Errorf("%s archives can only be extracted as a whole", info.Name())}
		}
	}
	switch info.Format {
	case FormatTAR:
		return extractTar(ctx, req, info.Codec, hooks)
	case FormatSingle:
		return extractSingle(ctx, req, info.Codec, hooks)
	case Format7z, FormatRAR:
		return extract7z(ctx, req, info, hooks)
	}
	return ExtractResult{Dest: req.DestDir, Fatal: ErrNotArchive}
}

// countingReader reports the bytes read from the archive file as progress
// (the unpacked size of a stream is not known in advance).
type countingReader struct {
	r   io.Reader
	rep *reporter
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.rep.p.DoneBytes += int64(n)
	return n, err
}

func damaged(err error) error {
	return fmt.Errorf("the archive is damaged or truncated (%v)", err)
}

// openStream opens the archive file and its decompressor, with progress
// counted on the compressed bytes.
func openStream(req ExtractRequest, codec Codec, rep *reporter) (io.Reader, func(), os.FileInfo, error) {
	f, err := os.Open(req.Archive)
	if err != nil {
		return nil, nil, nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, nil, err
	}
	rep.p.TotalBytes = fi.Size()
	r, closeDec, err := decompressor(codec, &countingReader{r: f, rep: rep})
	if err != nil {
		f.Close()
		return nil, nil, nil, damaged(err)
	}
	return r, func() { closeDec(); f.Close() }, fi, nil
}

func lowSpace(dest string, need int64, hooks ops.Hooks) bool {
	if free, err := ops.FreeSpace(dest); err == nil && uint64(need) > free {
		return hooks.LowSpace == nil || !hooks.LowSpace(uint64(need), free)
	}
	return false
}

// ---- TAR ----------------------------------------------------------------------

func extractTar(ctx context.Context, req ExtractRequest, codec Codec, hooks ops.Hooks) ExtractResult {
	res := ExtractResult{}
	rep := &reporter{hooks: hooks, p: ops.Progress{Phase: "extracting", ByteOnly: true}}
	dest, err := prepareDest(req.DestDir)
	res.Dest = dest
	if err != nil {
		res.Fatal = err
		return res
	}
	r, closeFn, fi, err := openStream(req, codec, rep)
	if err != nil {
		res.Fatal = err
		return res
	}
	defer closeFn()
	if codec == CodecNone && lowSpace(dest, fi.Size(), hooks) {
		res.Canceled = true
		return res
	}
	rep.report(true)
	x := &extractor{ctx: ctx, dest: dest, res: &res, rep: rep, hooks: hooks, top: map[string]bool{}, srcBytes: true}
	tr := tar.NewReader(r)
	seen := map[string]bool{}
	for {
		if ctx.Err() != nil {
			res.Canceled = true
			break
		}
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			res.Fatal = damaged(err)
			break
		}
		inner, _, ok := cleanName(h.Name)
		if !ok {
			res.Unsafe++
			continue
		}
		switch h.Typeflag {
		case tar.TypeDir:
			x.dir(inner)
		case tar.TypeReg, '\x00', tar.TypeGNUSparse:
			if seen[inner] {
				res.Duplicates++
				continue
			}
			seen[inner] = true
			x.file(inner, inner, tr, h.FileInfo(), damaged)
		case tar.TypeSymlink, tar.TypeLink:
			res.Links++
		case tar.TypeXGlobalHeader:
		default:
			res.Special++ // devices, FIFOs, …
		}
		if res.Canceled || res.Fatal != nil {
			break
		}
	}
	rep.p.Current = ""
	rep.report(true)
	return res
}

// dir creates folder rel below dest.
func (x *extractor) dir(rel string) {
	if _, err := x.target(rel); err != nil {
		x.fail(rel, err)
		return
	}
	if err := x.ensureDirs(rel); err != nil {
		x.fail(rel, err)
		return
	}
	x.noteTop(rel)
}

// prepareFile checks target rel for a new file: inside dest, its folders
// created (never through a link), an existing file only after Overwrite.
// ok is false when the file must not be written (the reason is recorded).
func (x *extractor) prepareFile(inner, rel string, info fs.FileInfo) (t string, overwrite, ok bool) {
	t, err := x.target(rel)
	if err != nil {
		x.fail(inner, err)
		return "", false, false
	}
	x.rep.p.Current = t
	parentRel := ""
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		parentRel = rel[:i]
	}
	if err := x.ensureDirs(parentRel); err != nil {
		x.fail(inner, err)
		return "", false, false
	}
	if di, err := os.Lstat(t); err == nil {
		if di.IsDir() {
			x.fail(inner, fmt.Errorf("a folder named %q already exists there", filepath.Base(t)))
			return "", false, false
		}
		switch x.resolve(ops.Conflict{Src: inner, Dst: t, SrcInfo: info, DstInfo: di}) {
		case ops.Skip:
			x.res.Skipped++
			return "", false, false
		case ops.Cancel:
			x.res.Canceled = true
			return "", false, false
		}
		return t, true, true
	} else if !errors.Is(err, fs.ErrNotExist) {
		x.fail(inner, err)
		return "", false, false
	}
	return t, false, true
}

// file writes the data of one file from r to rel below dest.
func (x *extractor) file(inner, rel string, r io.Reader, info fs.FileInfo, readErr func(error) error) {
	defer func() { x.rep.p.DoneItems++; x.rep.report(false) }()
	t, overwrite, ok := x.prepareFile(inner, rel, info)
	if !ok {
		return
	}
	n, err := x.place(r, info.Mode(), info.ModTime(), t, overwrite, readErr)
	x.res.Bytes += n
	if err != nil {
		x.placeFailed(inner, err)
		return
	}
	x.res.Files++
	x.noteTop(rel)
}

func (x *extractor) placeFailed(inner string, err error) {
	switch {
	case errors.Is(err, context.Canceled):
		x.res.Canceled = true
	case ops.IsNoSpace(err):
		x.res.Fatal = errors.New("the destination is full")
	default:
		x.fail(inner, err)
	}
}

// ---- single compressed files ---------------------------------------------------

// singleInfo describes the decompressed file for the conflict dialog.
type singleInfo struct {
	name  string
	mode  fs.FileMode
	mtime time.Time
}

func (s singleInfo) Name() string       { return s.name }
func (s singleInfo) Size() int64        { return -1 } // unknown until decompressed
func (s singleInfo) Mode() fs.FileMode  { return s.mode }
func (s singleInfo) ModTime() time.Time { return s.mtime }
func (s singleInfo) IsDir() bool        { return false }
func (s singleInfo) Sys() any           { return nil }

func extractSingle(ctx context.Context, req ExtractRequest, codec Codec, hooks ops.Hooks) ExtractResult {
	res := ExtractResult{}
	rep := &reporter{hooks: hooks, p: ops.Progress{Phase: "decompressing", TotalItems: 1, ByteOnly: true}}
	dest, err := prepareDest(req.DestDir)
	res.Dest = dest
	if err != nil {
		res.Fatal = err
		return res
	}
	name := req.OutputName
	if name == "" {
		name = SingleOutputName(filepath.Base(req.Archive), codec)
	}
	if name == "." || name == ".." || strings.ContainsAny(name, `/\`) || strings.ContainsRune(name, 0) {
		res.Fatal = fmt.Errorf("invalid output name %q", name)
		return res
	}
	r, closeFn, fi, err := openStream(req, codec, rep)
	if err != nil {
		res.Fatal = err
		return res
	}
	defer closeFn()
	rep.report(true)
	x := &extractor{ctx: ctx, dest: dest, res: &res, rep: rep, hooks: hooks, top: map[string]bool{}, srcBytes: true}
	x.file(name, name, r, singleInfo{name: name, mode: fi.Mode().Perm(), mtime: fi.ModTime()}, damaged)
	if len(res.Errors) == 1 && res.Files == 0 && strings.Contains(res.Errors[0].Err.Error(), "damaged") {
		res.Fatal, res.Errors = res.Errors[0].Err, nil
	}
	rep.p.Current = ""
	rep.report(true)
	return res
}

// ---- 7Z and RAR (7-Zip) --------------------------------------------------------

// 7-Zip is started directly (proc.Start: no shell, so names are never
// interpreted), first to list the archive, then as
//
//	7z x -y -bso0 -bse2 -bsp1 -o<staging> [-spd -x!LINK ...] -- ARCHIVE
//
// with stdin closed (an encrypted archive fails instead of prompting).
// Link entries are left out by name (see sevenzip_list.go). 7-Zip unpacks
// into a fresh private folder inside the destination; when it succeeded,
// regular files and folders are moved into place with the same checks as
// the other formats (conflicts asked, links and special files skipped).
// When it fails, nothing is moved and the folder is removed.

var percent7z = regexp.MustCompile(`(\d{1,3})%`)

// ErrNeeds7zip is returned for 7Z and RAR without an installed 7-Zip.
var ErrNeeds7zip = errors.New("extracting 7Z and RAR archives needs 7-Zip (7zz, 7z, 7za or 7zr)")

func extract7z(ctx context.Context, req ExtractRequest, info Info, hooks ops.Hooks) ExtractResult {
	res := ExtractResult{}
	rep := &reporter{hooks: hooks, p: ops.Progress{Phase: "unpacking", ByteOnly: true}}
	if req.SevenZip == "" || !filepath.IsAbs(req.SevenZip) {
		res.Dest, res.Fatal = req.DestDir, ErrNeeds7zip
		return res
	}
	dest, err := prepareDest(req.DestDir)
	res.Dest = dest
	if err != nil {
		res.Fatal = err
		return res
	}
	src, err := filepath.Abs(req.Archive)
	if err != nil {
		res.Fatal = err
		return res
	}
	fi, err := os.Stat(src)
	if err != nil {
		res.Fatal = err
		return res
	}
	rep.p.TotalBytes = fi.Size()
	if lowSpace(dest, fi.Size(), hooks) {
		res.Canceled = true
		return res
	}
	staging, err := os.MkdirTemp(dest, ops.TempPrefix+"x-*")
	if err != nil {
		res.Fatal = fmt.Errorf("cannot write in %s: %w", dest, err)
		return res
	}
	defer os.RemoveAll(staging)
	rep.report(true)

	listed, err := run7z(ctx, req.SevenZip, src, staging, info, rep)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			res.Canceled = true
		} else {
			res.Fatal = err
		}
		return res
	}

	// links were left out by 7-Zip (and what was stored below them)
	res.Links, res.Unsafe = len(listed.links), listed.below
	excluded := make(map[string]bool, len(listed.links))
	for _, p := range listed.links {
		excluded[p] = true
	}

	// move into place
	x := &extractor{ctx: ctx, dest: dest, res: &res, rep: rep, hooks: hooks, top: map[string]bool{}, srcBytes: true}
	rep.p = ops.Progress{Phase: "placing"}
	walkErr := filepath.WalkDir(staging, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			x.fail(p, err)
			return nil
		}
		if p == staging {
			return nil
		}
		if ctx.Err() != nil {
			res.Canceled = true
			return fs.SkipAll
		}
		r, err := filepath.Rel(staging, p)
		if err != nil {
			return err
		}
		rel, _, ok := cleanName(filepath.ToSlash(r))
		if !ok {
			res.Unsafe++
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		switch t := d.Type(); {
		case t&fs.ModeSymlink != 0:
			if !excluded[filepath.ToSlash(r)] { // a link 7-Zip made anyway: never moved
				res.Links++
			}
		case d.IsDir():
			x.dir(rel)
		case t.IsRegular():
			x.moveFile(rel, p)
		default:
			res.Special++
		}
		if res.Canceled || res.Fatal != nil {
			return fs.SkipAll
		}
		return nil
	})
	if walkErr != nil && res.Fatal == nil {
		res.Fatal = walkErr
	}
	rep.p.Current = ""
	rep.report(true)
	return res
}

// moveFile moves an unpacked file from staging to rel below dest.
func (x *extractor) moveFile(rel, staged string) {
	defer func() { x.rep.p.DoneItems++; x.rep.report(false) }()
	info, err := os.Lstat(staged)
	if err != nil {
		x.fail(rel, err)
		return
	}
	t, overwrite, ok := x.prepareFile(rel, rel, info)
	if !ok {
		return
	}
	perm := fs.FileMode(0o644)
	if info.Mode().Perm()&0o100 != 0 {
		perm = 0o755
	}
	if err := os.Chmod(staged, perm); err != nil {
		x.fail(rel, err)
		return
	}
	if err := ops.PlaceFile(staged, t, overwrite); err != nil {
		x.placeFailed(rel, err)
		return
	}
	x.res.Files++
	x.res.Bytes += info.Size()
	x.noteTop(rel)
}

// run7z lists the archive, then extracts it into staging without the
// links (see sevenzip_list.go).
func run7z(ctx context.Context, tool, src, staging string, info Info, rep *reporter) (listing7z, error) {
	var (
		l    listing7z
		perr error
	)
	err := exec7z(ctx, tool, []string{"l", "-slt", "--", src}, staging, info, func(r io.Reader) {
		l, perr = parseList7z(r)
	})
	if err != nil {
		return l, err
	}
	if perr != nil {
		return l, fmt.Errorf("cannot read the list of files in the %s archive (%v); nothing was extracted", info.Name(), perr)
	}
	if len(l.links) > 0 && l.version < min7zipForLinks {
		return l, fmt.Errorf("the %s archive contains %s, and this 7-Zip (%s) is too old to leave them out safely; "+
			"nothing was extracted. Install 7-Zip %d or newer (Termux: pkg install 7zip)",
			info.Name(), plural(len(l.links), "link"), version7zName(l.version), min7zipForLinks)
	}
	args := []string{"x", "-y", "-bso0", "-bse2", "-bsp1", "-o" + staging}
	args = append(args, l.exclusions()...)
	args = append(args, "--", src)
	return l, exec7z(ctx, tool, args, staging, info, func(r io.Reader) {
		buf := make([]byte, 4096)
		for {
			n, err := r.Read(buf)
			if n > 0 {
				if m := percent7z.FindAllSubmatch(buf[:n], -1); len(m) > 0 {
					if pct, _ := strconv.Atoi(string(m[len(m)-1][1])); pct <= 100 {
						rep.p.DoneBytes = rep.p.TotalBytes * int64(pct) / 100
						rep.report(false)
					}
				}
			}
			if err != nil {
				return
			}
		}
	})
}

func version7zName(v int) string {
	if v == 0 {
		return "unknown version"
	}
	return fmt.Sprintf("version %d", v)
}

// exec7z runs 7-Zip with args (no shell; stdin closed so an encrypted
// archive fails instead of prompting), passes its output to consume and
// explains a failure. Cancelling ctx kills it.
func exec7z(ctx context.Context, tool string, args []string, dir string, info Info, consume func(io.Reader)) error {
	outR, outW, err := os.Pipe()
	if err != nil {
		return err
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		return err
	}
	argv := append([]string{tool}, args...)
	child, err := proc.Start(proc.Spec{Path: tool, Args: argv, Dir: dir, Env: utf8Env(), Files: []*os.File{nil, outW, errW}, Sys: proc.NewGroup()})
	outW.Close()
	errW.Close()
	if err != nil {
		outR.Close()
		errR.Close()
		return fmt.Errorf("cannot start %s: %w", filepath.Base(tool), err)
	}
	outDone := make(chan struct{})
	go func() {
		defer close(outDone)
		defer outR.Close()
		consume(outR)
		io.Copy(io.Discard, outR) // whatever consume left: never block 7-Zip
	}()
	var stderr bytes.Buffer
	errDone := make(chan struct{})
	go func() {
		defer close(errDone)
		io.Copy(&stderr, io.LimitReader(errR, 64<<10))
		io.Copy(io.Discard, errR)
		errR.Close()
	}()
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			child.KillGroup()
		case <-stop:
		}
	}()
	st, werr := child.Wait()
	close(stop)
	<-outDone
	<-errDone
	if ctx.Err() != nil {
		return context.Canceled
	}
	if werr != nil {
		return werr
	}
	if st.Code != 0 || st.Signal != 0 {
		return explain7z(info, st.Code, stderr.String())
	}
	return nil
}

// explain7z turns 7-Zip's error output into a message.
func explain7z(info Info, code int, stderr string) error {
	low := strings.ToLower(stderr)
	last := ""
	for _, l := range strings.Split(stderr, "\n") {
		if l = strings.TrimSpace(l); l != "" && l != "ERRORS:" {
			last = l
		}
	}
	switch {
	case strings.Contains(low, "wrong password"), strings.Contains(low, "break signaled"), strings.Contains(low, "enter password"):
		return fmt.Errorf("the %s archive is password-protected: encrypted archives are not supported (nothing was extracted)", info.Name())
	case strings.Contains(low, "unsupported method"), strings.Contains(low, "unsupported archive"):
		return fmt.Errorf("this 7-Zip cannot read the %s archive (%s); nothing was extracted", info.Name(), last)
	case strings.Contains(low, "cannot open the file as"), strings.Contains(low, "can not open the file as"), strings.Contains(low, "unexpected end"),
		strings.Contains(low, "data error"), strings.Contains(low, "crc failed"), strings.Contains(low, "headers error"):
		return fmt.Errorf("the %s archive is damaged or truncated (%s); nothing was extracted", info.Name(), last)
	case strings.Contains(low, "dangerous link"):
		return fmt.Errorf("the %s archive contains a link pointing outside the destination, which 7-Zip refused (%s); nothing was extracted", info.Name(), last)
	case strings.Contains(low, "no space left"), strings.Contains(low, "disk full"):
		return errors.New("the destination is full; nothing was extracted")
	}
	if last == "" {
		last = fmt.Sprintf("exit status %d", code)
	}
	return fmt.Errorf("7-Zip failed (%s); nothing was extracted", last)
}

// utf8Env makes 7-Zip (p7zip) handle non-ASCII names correctly.
func utf8Env() []string {
	env := os.Environ()
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		v := strings.ToLower(os.Getenv(k))
		if v == "" {
			continue
		}
		if strings.Contains(v, "utf-8") || strings.Contains(v, "utf8") {
			return env
		}
		break
	}
	return append(env, "LC_ALL=C.UTF-8")
}
