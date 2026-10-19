package compress

import (
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
	"sync"
	"time"

	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
	"github.com/ThatOn3Gu7/TwinPane/internal/proc"
)

// 7Z archives are created by an installed 7-Zip program. It is started
// directly (proc.Start: no shell, so file names are never interpreted) with
//
//	7z a -t7z -mx=N -bso0 -bse2 -bsp1 -y -spd [-snl] [-x!excluded…] -- OUT name…
//
// in the folder that holds the selected items, so they are stored under
// their own names as with the other formats:
//
//   - "--" ends the switches, so names starting with "-" or "@" are names
//     (not switches or list files); -spd turns off wildcard matching, so
//     "*" and "?" in names are literal;
//   - OUT is a fresh file inside a new temporary folder in the destination
//     folder (7-Zip would update an existing archive instead of replacing
//     it); the folder is excluded from the archive and removed afterwards;
//   - -snl stores symbolic links as links (7-Zip 21 and later; p7zip does
//     so by default and does not know the switch);
//   - pipes, sockets and devices are excluded and reported, as with TAR.

// sevenProbe caches what the installed program supports.
var sevenProbe = struct {
	sync.Mutex
	m map[string]sevenInfo
}{m: map[string]sevenInfo{}}

type sevenInfo struct {
	banner string
	snl    bool
}

var sevenVersion = regexp.MustCompile(`7-Zip(?: \(\w+\))? (\d+)\.(\d+)`)

// probe7z runs the program without arguments (it prints its banner and
// usage) to learn its version.
func probe7z(tool string) sevenInfo {
	sevenProbe.Lock()
	defer sevenProbe.Unlock()
	if in, ok := sevenProbe.m[tool]; ok {
		return in
	}
	out, _ := runCapture(tool, nil, "", 5*time.Second)
	in := parse7zBanner(out)
	sevenProbe.m[tool] = in
	return in
}

// parse7zBanner reads the version banner of 7z.
func parse7zBanner(out string) sevenInfo {
	var in sevenInfo
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); strings.Contains(l, "7-Zip") {
			in.banner = l
			break
		}
	}
	if strings.Contains(out, "p7zip") {
		return in // p7zip stores links as links and rejects -snl
	}
	if m := sevenVersion.FindStringSubmatch(in.banner); m != nil {
		major, _ := strconv.Atoi(m[1])
		in.snl = major >= 21
	}
	return in
}

// runCapture runs a program with stdin from /dev/null and returns its
// combined output (used for the version probe).
func runCapture(path string, args []string, dir string, timeout time.Duration) (string, error) {
	r, w, err := os.Pipe()
	if err != nil {
		return "", err
	}
	child, err := proc.Start(proc.Spec{Path: path, Args: append([]string{path}, args...), Dir: dir, Env: sevenEnv(), Files: []*os.File{nil, w, w}, Sys: proc.NewGroup()})
	w.Close()
	if err != nil {
		r.Close()
		return "", err
	}
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		io.Copy(&buf, io.LimitReader(r, 64<<10))
		io.Copy(io.Discard, r)
		r.Close()
		close(done)
	}()
	timer := time.AfterFunc(timeout, func() { child.KillGroup() })
	_, err = child.Wait()
	timer.Stop()
	<-done
	return buf.String(), err
}

// sevenEnv is the environment for 7-Zip: p7zip needs a UTF-8 locale to
// store non-ASCII names correctly.
func sevenEnv() []string {
	env := os.Environ()
	for _, k := range []string{"LC_ALL", "LC_CTYPE", "LANG"} {
		v := strings.ToLower(os.Getenv(k))
		if v == "" {
			continue
		}
		if strings.Contains(v, "utf-8") || strings.Contains(v, "utf8") {
			return env
		}
		break // the first one set decides
	}
	return append(env, "LC_ALL=C.UTF-8")
}

// sevenArgs builds the argument list (without argv[0]).
func sevenArgs(level int, snl bool, excludes []string, out string, names []string) []string {
	args := []string{"a", "-t7z", "-mx=" + strconv.Itoa(level), "-bso0", "-bse2", "-bsp1", "-y", "-spd"}
	if snl {
		args = append(args, "-snl")
	}
	for _, x := range excludes {
		args = append(args, "-x!"+x)
	}
	args = append(args, "--", out)
	return append(args, names...)
}

var percentRE = regexp.MustCompile(`(\d{1,3})%`)

func create7z(ctx context.Context, req Request, t target, level int, hooks ops.Hooks, res *Result) {
	rep := &reporter{hooks: hooks, p: ops.Progress{Phase: "scanning"}}
	if req.Tool == "" || !filepath.IsAbs(req.Tool) {
		res.Fatal = errors.New("7-Zip (7zz, 7z, 7za or 7zr) was not found")
		return
	}
	base := filepath.Dir(filepath.Clean(req.Sources[0]))
	names := make([]string, len(req.Sources))
	for i, s := range req.Sources {
		s = filepath.Clean(s)
		if filepath.Dir(s) != base {
			res.Fatal = errors.New("7Z: the items must all be in the same folder")
			return
		}
		names[i] = filepath.Base(s)
	}
	tmpDir, err := os.MkdirTemp(t.dir, ops.TempPrefix+"7z-*")
	if err != nil {
		res.Fatal = fmt.Errorf("cannot write in %s: %w", t.dir, unwrapPath(err))
		return
	}
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			res.CleanupErr = errors.Join(res.CleanupErr, fmt.Errorf("could not remove temporary 7-Zip output %s: %w", tmpDir, err))
		}
	}()
	tmpInfo, err := os.Stat(tmpDir)
	if err != nil {
		res.Fatal = err
		return
	}
	tmpOut := filepath.Join(tmpDir, "new.7z")

	// scan: for the counts, the progress total, and what to exclude
	skip := func(fi fs.FileInfo) bool {
		return os.SameFile(fi, tmpInfo) || (t.outInfo != nil && fi.Mode().IsRegular() && os.SameFile(fi, t.outInfo))
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
	var excludes []string
	for _, p := range sr.excluded {
		if rel, err := filepath.Rel(base, p); err == nil {
			excludes = append(excludes, rel)
		}
	}
	for _, it := range sr.special {
		excludes = append(excludes, filepath.FromSlash(it.name))
	}
	for _, it := range sr.items {
		switch m := it.info.Mode(); {
		case m.IsDir():
			res.Dirs++
		case m&fs.ModeSymlink != 0:
			res.Links++
		default:
			res.Files++
		}
	}
	res.Bytes = sr.total
	rep.p = ops.Progress{Phase: "compressing", TotalItems: len(sr.items), TotalBytes: sr.total, ByteOnly: true}
	rep.report(true)

	info := probe7z(req.Tool)
	outR, outW, err := os.Pipe()
	if err != nil {
		res.Fatal = err
		return
	}
	errR, errW, err := os.Pipe()
	if err != nil {
		outR.Close()
		outW.Close()
		res.Fatal = err
		return
	}
	args := append([]string{req.Tool}, sevenArgs(level, info.snl, excludes, tmpOut, names)...)
	child, err := proc.Start(proc.Spec{Path: req.Tool, Args: args, Dir: base, Env: sevenEnv(), Files: []*os.File{nil, outW, errW}, Sys: proc.NewGroup()})
	outW.Close()
	errW.Close()
	if err != nil {
		outR.Close()
		errR.Close()
		res.Fatal = fmt.Errorf("cannot start %s: %w", filepath.Base(req.Tool), err)
		return
	}
	// progress: "NN%" updates on stdout (-bsp1)
	outDone := make(chan struct{})
	go func() {
		defer close(outDone)
		buf := make([]byte, 4096)
		for {
			n, err := outR.Read(buf)
			if n > 0 {
				if m := percentRE.FindAllSubmatch(buf[:n], -1); len(m) > 0 {
					pct, _ := strconv.Atoi(string(m[len(m)-1][1]))
					if pct > 100 {
						pct = 100
					}
					p := rep.p
					p.DoneBytes = sr.total * int64(pct) / 100
					if hooks.Progress != nil {
						hooks.Progress(p)
					}
				}
			}
			if err != nil {
				outR.Close()
				return
			}
		}
	}()
	errDone := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(io.LimitReader(errR, 64<<10))
		io.Copy(io.Discard, errR)
		errR.Close()
		errDone <- string(b)
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
	var stderr string
	select {
	case stderr = <-errDone:
	case <-time.After(2 * time.Second):
		// a process the program started still holds its output open
		errR.Close()
	}
	select {
	case <-outDone: // no progress report after Create returns
	case <-time.After(2 * time.Second):
		outR.Close()
		<-outDone
	}
	if ctx.Err() != nil {
		res.Canceled = true
		return
	}
	if werr != nil {
		res.Fatal = werr
		return
	}
	switch {
	case st.Success():
	case st.Signal == 0 && st.Code == 1:
		// warnings: some files could not be read; the archive is complete
		// without them
		for _, l := range sevenWarnings(stderr) {
			res.Errors = append(res.Errors, ops.ItemError{Path: l, Op: "add", Err: errors.New("7-Zip could not read it")})
		}
		if len(res.Errors) == 0 {
			res.Errors = append(res.Errors, ops.ItemError{Path: base, Op: "add", Err: errors.New("7-Zip reported warnings: " + lastLine(stderr))})
		}
	default:
		msg := lastLine(stderr)
		if msg == "" {
			msg = st.String()
		}
		res.Fatal = fmt.Errorf("%s failed (%s): %s", filepath.Base(req.Tool), st, msg)
		return
	}
	if _, err := os.Stat(tmpOut); err != nil {
		res.Fatal = fmt.Errorf("%s did not write the archive: %s", filepath.Base(req.Tool), lastLine(stderr))
		return
	}
	if fi, err := os.Stat(tmpOut); err == nil {
		res.Size = fi.Size()
	}
	_ = os.Chmod(tmpOut, 0o644)
	if err := ops.PlaceFile(tmpOut, t.out, req.Overwrite); err != nil {
		if errors.Is(err, fs.ErrExist) {
			err = fmt.Errorf("%s %w (created meanwhile): nothing was overwritten", filepath.Base(t.out), ErrExists)
		}
		res.Fatal = fatal(err)
		return
	}
	rep.p.DoneBytes, rep.p.Current = sr.total, ""
	rep.report(true)
}

// sevenWarnings extracts the names from 7-Zip's "name : errno=…" warning
// lines.
func sevenWarnings(stderr string) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range strings.Split(stderr, "\n") {
		if i := strings.Index(l, " : "); i > 0 && strings.Contains(l[i:], "errno=") || (i > 0 && strings.Contains(l[i:], "WARNING")) {
			n := strings.TrimSpace(l[:i])
			if n != "" && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" && !strings.HasPrefix(l, "---") {
			return l
		}
	}
	return ""
}
