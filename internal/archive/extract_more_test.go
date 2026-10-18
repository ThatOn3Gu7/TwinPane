package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	dsbzip2 "github.com/dsnet/compress/bzip2"
	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	"github.com/ulikunitz/xz"

	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
	"github.com/ThatOn3Gu7/TwinPane/internal/proc"
)

type tarItem struct {
	name   string
	typ    byte
	body   string
	mode   int64
	target string
}

func tarBytes(t *testing.T, items []tarItem) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, it := range items {
		h := &tar.Header{Name: it.name, Typeflag: it.typ, Mode: it.mode, Size: int64(len(it.body)), Linkname: it.target, ModTime: time.Unix(1700000000, 0), Format: tar.FormatPAX}
		if h.Mode == 0 {
			h.Mode = 0o644
		}
		if it.typ != tar.TypeReg {
			h.Size = 0
		}
		if err := tw.WriteHeader(h); err != nil {
			t.Fatal(err)
		}
		if it.typ == tar.TypeReg {
			tw.Write([]byte(it.body))
		}
	}
	tw.Close()
	return buf.Bytes()
}

func compressWith(t *testing.T, c Codec, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	var w io.WriteCloser
	var err error
	switch c {
	case CodecNone:
		return data
	case CodecGzip:
		w = gzip.NewWriter(&buf)
	case CodecBzip2:
		w, err = dsbzip2.NewWriter(&buf, nil)
	case CodecXz:
		w, err = xz.NewWriter(&buf)
	case CodecZstd:
		w, err = zstd.NewWriter(&buf)
	case CodecLz4:
		w = lz4.NewWriter(&buf)
	}
	if err != nil {
		t.Fatal(err)
	}
	w.Write(data)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func writeFile(t *testing.T, p string, b []byte) string {
	t.Helper()
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

var basicTar = []tarItem{
	{name: "top/", typ: tar.TypeDir, mode: 0o755},
	{name: "top/a.txt", typ: tar.TypeReg, body: "alpha"},
	{name: "top/run.sh", typ: tar.TypeReg, body: "#!/bin/sh", mode: 0o4777},
	{name: "top/sp ace & 'q' (1).txt", typ: tar.TypeReg, body: "special"},
	{name: "top/link", typ: tar.TypeSymlink, target: "/etc/passwd"},
	{name: "top/hard", typ: tar.TypeLink, target: "top/a.txt"},
	{name: "top/fifo", typ: tar.TypeFifo},
	{name: "../evil.txt", typ: tar.TypeReg, body: "evil"},
	{name: "/abs.txt", typ: tar.TypeReg, body: "abs"},
	{name: "top/a.txt", typ: tar.TypeReg, body: "second copy"},
	{name: "implicit/dir/file.txt", typ: tar.TypeReg, body: "deep"},
}

func TestDetectFormats(t *testing.T) {
	dir := t.TempDir()
	tb := tarBytes(t, basicTar)
	cases := []struct {
		name string
		data []byte
		want Info
	}{
		{"a.tar", tb, Info{FormatTAR, CodecNone}},
		{"a.tar.gz", compressWith(t, CodecGzip, tb), Info{FormatTAR, CodecGzip}},
		{"a.tbz2", compressWith(t, CodecBzip2, tb), Info{FormatTAR, CodecBzip2}},
		{"a.tar.xz", compressWith(t, CodecXz, tb), Info{FormatTAR, CodecXz}},
		{"a.tar.zst", compressWith(t, CodecZstd, tb), Info{FormatTAR, CodecZstd}},
		{"a.tar.lz4", compressWith(t, CodecLz4, tb), Info{FormatTAR, CodecLz4}},
		{"n.txt.gz", compressWith(t, CodecGzip, []byte("hello")), Info{FormatSingle, CodecGzip}},
		{"n.txt.zst", compressWith(t, CodecZstd, []byte("hello")), Info{FormatSingle, CodecZstd}},
		{"x.7z", append([]byte{'7', 'z', 0xbc, 0xaf, 0x27, 0x1c}, make([]byte, 40)...), Info{Format: Format7z}},
		{"x.rar", append([]byte("Rar!\x1a\x07\x01\x00"), make([]byte, 40)...), Info{Format: FormatRAR}},
		{"plain.txt", []byte("just text, not an archive"), Info{}},
		{"misnamed.zip", compressWith(t, CodecGzip, tb), Info{FormatTAR, CodecGzip}}, // content wins over the name
		{"BZh-text.bz2", []byte("BZh is how this text starts"), Info{}},
	}
	for _, c := range cases {
		p := writeFile(t, filepath.Join(dir, c.name), c.data)
		got, err := Detect(p)
		if err != nil || got != c.want {
			t.Errorf("%s: got %+v (%s) err %v, want %+v", c.name, got, got.Name(), err, c.want)
		}
	}
	if got, _ := Detect(dir); got.Format != FormatUnknown {
		t.Error("a folder is not an archive")
	}
	if runtime.GOOS != "windows" {
		fifo := filepath.Join(dir, "fifo")
		if err := mkfifo(fifo); err == nil {
			if got, err := Detect(fifo); err != nil || got.Format != FormatUnknown {
				t.Error("a FIFO must never be opened")
			}
		}
	}
	names := map[Info]string{{FormatTAR, CodecGzip}: "TAR.GZ", {FormatTAR, CodecZstd}: "TAR.ZST", {FormatSingle, CodecXz}: "XZ", {Format: FormatRAR}: "RAR"}
	for in, want := range names {
		if in.Name() != want {
			t.Errorf("%+v: %s, want %s", in, in.Name(), want)
		}
	}
}

func TestExtractTarAllCodecsAndSafety(t *testing.T) {
	tb := tarBytes(t, basicTar)
	for _, c := range []Codec{CodecNone, CodecGzip, CodecBzip2, CodecXz, CodecZstd, CodecLz4} {
		root := t.TempDir()
		arc := writeFile(t, filepath.Join(root, "a.tar.x"), compressWith(t, c, tb))
		dest := filepath.Join(root, "out")
		var progress []ops.Progress
		res := ExtractAny(context.Background(), ExtractRequest{Archive: arc, DestDir: dest}, ops.Hooks{Progress: func(p ops.Progress) { progress = append(progress, p) }})
		if !res.OK() {
			t.Fatalf("codec %d: %s %+v", c, res.Summary(), res.Errors)
		}
		if res.Files != 4 || res.Links != 2 || res.Special != 1 || res.Unsafe != 2 || res.Duplicates != 1 {
			t.Fatalf("codec %d: counts %+v", c, res)
		}
		b, _ := os.ReadFile(filepath.Join(dest, "top", "a.txt"))
		if string(b) != "alpha" {
			t.Fatalf("duplicate names keep the first entry: %q", b)
		}
		if b, _ := os.ReadFile(filepath.Join(dest, "top", "sp ace & 'q' (1).txt")); string(b) != "special" {
			t.Fatal("special characters in names")
		}
		if fi, err := os.Stat(filepath.Join(dest, "top", "run.sh")); err != nil || fi.Mode().Perm() != 0o755 || fi.Mode()&fs.ModeSetuid != 0 {
			t.Fatalf("permissions must be reduced: %v %v", fi.Mode(), err)
		}
		for _, bad := range []string{filepath.Join(root, "evil.txt"), filepath.Join(dest, "top", "link"), filepath.Join(dest, "top", "hard"), filepath.Join(dest, "top", "fifo"), filepath.Join(dest, "abs.txt")} {
			if _, err := os.Lstat(bad); err == nil && !strings.HasSuffix(bad, "abs.txt") {
				t.Fatalf("must not exist: %s", bad)
			}
		}
		if _, err := os.Stat(filepath.Join(dest, "implicit", "dir", "file.txt")); err != nil {
			t.Fatal("folders of a file are created")
		}
		if strings.Join(res.Top, ",") != "top,implicit" {
			t.Fatalf("top: %v", res.Top)
		}
		if len(progress) == 0 || progress[len(progress)-1].DoneBytes == 0 {
			t.Fatal("progress must be reported")
		}
		if !strings.Contains(res.Summary(), "symbolic link") || !strings.Contains(res.Summary(), "special file") {
			t.Fatalf("summary: %s", res.Summary())
		}
	}
}

func TestExtractTarConflictsAndLinks(t *testing.T) {
	root := t.TempDir()
	arc := writeFile(t, filepath.Join(root, "a.tar"), tarBytes(t, []tarItem{
		{name: "x.txt", typ: tar.TypeReg, body: "new x"},
		{name: "y.txt", typ: tar.TypeReg, body: "new y"},
		{name: "via/inside.txt", typ: tar.TypeReg, body: "through a link"},
	}))
	dest := filepath.Join(root, "out")
	os.MkdirAll(dest, 0o755)
	os.WriteFile(filepath.Join(dest, "x.txt"), []byte("old x"), 0o644)
	os.WriteFile(filepath.Join(dest, "y.txt"), []byte("old y"), 0o644)
	elsewhere := filepath.Join(root, "elsewhere")
	os.MkdirAll(elsewhere, 0o755)
	if err := os.Symlink(elsewhere, filepath.Join(dest, "via")); err != nil {
		t.Skip("symlinks not available")
	}
	asked := []string{}
	hooks := ops.Hooks{Conflict: func(c ops.Conflict) ops.Decision {
		asked = append(asked, filepath.Base(c.Dst))
		if filepath.Base(c.Dst) == "x.txt" {
			return ops.Decision{Action: ops.Overwrite}
		}
		return ops.Decision{Action: ops.Skip}
	}}
	res := ExtractAny(context.Background(), ExtractRequest{Archive: arc, DestDir: dest}, hooks)
	if strings.Join(asked, ",") != "x.txt,y.txt" {
		t.Fatalf("asked: %v", asked)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "x.txt")); string(b) != "new x" {
		t.Fatal("overwrite")
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "y.txt")); string(b) != "old y" {
		t.Fatal("skip must keep the file")
	}
	if res.Skipped != 1 || len(res.Errors) != 1 || !strings.Contains(res.Errors[0].Err.Error(), "symbolic link") {
		t.Fatalf("result: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(elsewhere, "inside.txt")); err == nil {
		t.Fatal("written through a symbolic link")
	}
	// nil conflict hook: existing files are skipped
	res = ExtractAny(context.Background(), ExtractRequest{Archive: arc, DestDir: dest}, ops.Hooks{})
	if b, _ := os.ReadFile(filepath.Join(dest, "y.txt")); string(b) != "old y" || res.Skipped != 2 {
		t.Fatalf("no hook: %+v", res)
	}
}

func noTemps(t *testing.T, dir string) {
	t.Helper()
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && strings.HasPrefix(d.Name(), ops.TempPrefix) {
			t.Errorf("temporary file left: %s", p)
		}
		return nil
	})
}

func TestExtractDamagedAndCanceled(t *testing.T) {
	root := t.TempDir()
	big := strings.Repeat("0123456789", 50000)
	tb := tarBytes(t, []tarItem{{name: "ok.txt", typ: tar.TypeReg, body: "fine"}, {name: "big.bin", typ: tar.TypeReg, body: big}})
	gz := compressWith(t, CodecGzip, tb)
	arc := writeFile(t, filepath.Join(root, "cut.tar.gz"), gz[:len(gz)*2/3])
	dest := filepath.Join(root, "out")
	os.MkdirAll(dest, 0o755)
	os.WriteFile(filepath.Join(dest, "big.bin"), []byte("keep me"), 0o644)
	res := ExtractAny(context.Background(), ExtractRequest{Archive: arc, DestDir: dest}, ops.Hooks{Conflict: func(ops.Conflict) ops.Decision {
		return ops.Decision{Action: ops.Overwrite}
	}})
	if res.OK() || (res.Fatal == nil && len(res.Errors) == 0) {
		t.Fatalf("a truncated archive must fail: %+v", res)
	}
	if msg := res.Summary(); !strings.Contains(msg, "damaged") {
		t.Fatalf("summary: %s", msg)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "big.bin")); string(b) != "keep me" {
		t.Fatal("a damaged entry must not replace the existing file")
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "ok.txt")); string(b) != "fine" {
		t.Fatal("complete entries before the damage are kept")
	}
	noTemps(t, dest)

	// garbage after a valid magic
	bad := writeFile(t, filepath.Join(root, "bad.gz"), append([]byte{0x1f, 0x8b, 8, 0}, bytes.Repeat([]byte{0xff}, 64)...))
	res = ExtractAny(context.Background(), ExtractRequest{Archive: bad, DestDir: dest}, ops.Hooks{})
	if res.Fatal == nil || !strings.Contains(res.Fatal.Error(), "damaged") {
		t.Fatalf("garbage: %+v", res)
	}
	noTemps(t, dest)

	// cancelled
	ok := writeFile(t, filepath.Join(root, "ok.tar"), tb)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res = ExtractAny(ctx, ExtractRequest{Archive: ok, DestDir: filepath.Join(root, "c")}, ops.Hooks{})
	if !res.Canceled || res.Files != 0 {
		t.Fatalf("cancelled: %+v", res)
	}
	if _, err := os.Stat(filepath.Join(root, "c")); !os.IsNotExist(err) {
		t.Fatalf("cancelled extraction created its destination: %v", err)
	}

	// unsupported
	txt := writeFile(t, filepath.Join(root, "t.txt"), []byte("text"))
	res = ExtractAny(context.Background(), ExtractRequest{Archive: txt, DestDir: dest}, ops.Hooks{})
	if !errors.Is(res.Fatal, ErrNotArchive) || !strings.Contains(res.Fatal.Error(), "TAR.GZ") {
		t.Fatalf("unsupported: %v", res.Fatal)
	}
	// members are ZIP-only
	res = ExtractAny(context.Background(), ExtractRequest{Archive: ok, Members: []string{"ok.txt"}, DestDir: dest}, ops.Hooks{})
	if res.Fatal == nil || !strings.Contains(res.Fatal.Error(), "as a whole") {
		t.Fatalf("members: %v", res.Fatal)
	}
}

func TestExtractSingleFiles(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "out")
	for _, c := range []Codec{CodecGzip, CodecBzip2, CodecXz, CodecZstd, CodecLz4} {
		ext := codecExts[c][0]
		arc := writeFile(t, filepath.Join(root, "notes.txt"+ext), compressWith(t, c, []byte("note body "+ext)))
		res := ExtractAny(context.Background(), ExtractRequest{Archive: arc, DestDir: dest}, ops.Hooks{})
		if !res.OK() || res.Files != 1 {
			t.Fatalf("%s: %s", ext, res.Summary())
		}
		if b, _ := os.ReadFile(filepath.Join(dest, "notes.txt")); string(b) != "note body "+ext {
			t.Fatalf("%s: %q", ext, b)
		}
		os.Remove(filepath.Join(dest, "notes.txt"))
	}
	if n := SingleOutputName("data", CodecGzip); n != "data.out" {
		t.Fatalf("no extension: %s", n)
	}
	if n := SingleOutputName("A.TXT.GZ", CodecGzip); n != "A.TXT" {
		t.Fatalf("case: %s", n)
	}
	// existing output: asked; skip keeps it
	arc := writeFile(t, filepath.Join(root, "x.gz"), compressWith(t, CodecGzip, []byte("new")))
	os.WriteFile(filepath.Join(dest, "x"), []byte("old"), 0o644)
	asked := 0
	res := ExtractAny(context.Background(), ExtractRequest{Archive: arc, DestDir: dest}, ops.Hooks{Conflict: func(ops.Conflict) ops.Decision {
		asked++
		return ops.Decision{Action: ops.Skip}
	}})
	if asked != 1 || res.Skipped != 1 {
		t.Fatalf("conflict: %+v", res)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "x")); string(b) != "old" {
		t.Fatal("skip")
	}
	// explicit output name; an unsafe one is refused
	res = ExtractAny(context.Background(), ExtractRequest{Archive: arc, DestDir: dest, OutputName: "chosen.txt"}, ops.Hooks{})
	if b, _ := os.ReadFile(filepath.Join(dest, "chosen.txt")); !res.OK() || string(b) != "new" {
		t.Fatalf("output name: %+v", res)
	}
	res = ExtractAny(context.Background(), ExtractRequest{Archive: arc, DestDir: dest, OutputName: "../up"}, ops.Hooks{})
	if res.Fatal == nil {
		t.Fatal("an output name with a separator must be refused")
	}
	if FolderName("photos.tar.gz") != "photos" || FolderName("b.7z") != "b" || FolderName("raw") != "raw.extracted" {
		t.Fatal("FolderName")
	}
}

// ---- 7-Zip (REAL, skipped when not installed) ------------------------------------

func find7z(t *testing.T) string {
	t.Helper()
	for _, n := range []string{"7zz", "7z", "7za"} {
		if p, err := proc.LookPath(n); err == nil {
			return p
		}
	}
	if os.Getenv("TWINPANE_REQUIRE_TOOLS") != "" {
		t.Fatal("REAL 7-Zip tests: 7-Zip is not installed, and TWINPANE_REQUIRE_TOOLS is set")
	}
	t.Skip("REAL 7-Zip tests: 7-Zip is not installed")
	return ""
}

func run7zCreate(t *testing.T, tool, dir string, args ...string) {
	t.Helper()
	if !try7z(tool, dir, args...) {
		t.Fatalf("7z %v failed", args)
	}
}

func try7z(tool, dir string, args ...string) bool {
	child, err := proc.Start(proc.Spec{Path: tool, Args: append([]string{tool}, args...), Dir: dir, Env: utf8Env(), Files: []*os.File{nil, nil, nil}})
	if err != nil {
		return false
	}
	st, err := child.Wait()
	return err == nil && st.Code == 0
}

// add7zLinks adds names to archive keeping symbolic links as links: -snl
// on 7-Zip 21 and newer; p7zip 16.02 has no -snl and always keeps them.
func add7zLinks(t *testing.T, tool, dir, archive string, names ...string) {
	t.Helper()
	if !try7z(tool, dir, append([]string{"a", "-bso0", "-bse0", "-snl", archive}, names...)...) {
		run7zCreate(t, tool, dir, append([]string{"a", "-bso0", "-bse0", archive}, names...)...)
	}
}

// version7zOf returns the major version of a 7-Zip program (0 if unknown).
func version7zOf(t *testing.T, tool string) int {
	t.Helper()
	var out bytes.Buffer
	exec7z(context.Background(), tool, []string{"i"}, t.TempDir(), Info{Format: Format7z}, func(r io.Reader) {
		io.Copy(&out, io.LimitReader(r, 4096))
	})
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.Contains(line, "7-Zip") {
			if m := version7z.FindStringSubmatch(line); m != nil {
				v, _ := strconv.Atoi(m[1])
				return v
			}
		}
	}
	return 0
}

func TestExtract7zReal(t *testing.T) {
	tool := find7z(t)
	modern := version7zOf(t, tool) >= min7zipForLinks
	root := t.TempDir()
	src := filepath.Join(root, "src")
	os.MkdirAll(filepath.Join(src, "d", "e"), 0o755)
	os.WriteFile(filepath.Join(src, "d", "a.txt"), []byte("seven"), 0o644)
	os.WriteFile(filepath.Join(src, "d", "e", "ü n'i (c).txt"), []byte("unicode"), 0o644)
	run7zCreate(t, tool, src, "a", "-bso0", "-bse0", "../nolink.7z", "d")
	run7zCreate(t, tool, src, "a", "-bso0", "-bse0", "-psecret", "-mhe=on", "../enc.7z", "d")
	// the same with a link to a file outside: 7-Zip 21+ itself refuses to
	// create it (23.01) or points it inside the output folder (25.01);
	// TwinPane leaves it out before 7-Zip sees it, on every version
	os.Symlink("/etc/passwd", filepath.Join(src, "d", "lnk"))
	add7zLinks(t, tool, src, "../links.7z", "d")
	plain := filepath.Join(root, "nolink.7z")

	dest := filepath.Join(root, "out")
	var last ops.Progress
	if modern {
		res := ExtractAny(context.Background(), ExtractRequest{Archive: filepath.Join(root, "links.7z"), DestDir: dest, SevenZip: tool}, ops.Hooks{Progress: func(p ops.Progress) { last = p }})
		if !res.OK() || res.Files != 2 || res.Links != 1 || res.Unsafe != 0 {
			t.Fatalf("7z with a link: %s %+v %v", res.Summary(), res.Errors, res.Fatal)
		}
		if _, err := os.Lstat(filepath.Join(dest, "d", "lnk")); err == nil {
			t.Fatal("links must not be created")
		}
		os.RemoveAll(dest)
	} else {
		// an old 7-Zip has no link checks of its own: refused, nothing written
		res := ExtractAny(context.Background(), ExtractRequest{Archive: filepath.Join(root, "links.7z"), DestDir: dest, SevenZip: tool}, ops.Hooks{})
		if res.Fatal == nil || !strings.Contains(res.Fatal.Error(), "too old") {
			t.Fatalf("old 7-Zip with a link: %v", res.Fatal)
		}
		if ents, _ := os.ReadDir(dest); len(ents) != 0 {
			t.Fatalf("nothing may be extracted: %v", ents)
		}
	}
	res := ExtractAny(context.Background(), ExtractRequest{Archive: plain, DestDir: dest, SevenZip: tool}, ops.Hooks{Progress: func(p ops.Progress) { last = p }})
	if !res.OK() || res.Files != 2 || res.Links != 0 {
		t.Fatalf("7z: %s %+v %v", res.Summary(), res.Errors, res.Fatal)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "d", "e", "ü n'i (c).txt")); string(b) != "unicode" {
		t.Fatal("unicode name")
	}
	if last.Phase == "" {
		t.Fatalf("progress %+v", last)
	}
	noTemps(t, dest)

	// again: conflicts are asked, skip keeps
	os.WriteFile(filepath.Join(dest, "d", "a.txt"), []byte("mine"), 0o644)
	asked := 0
	res = ExtractAny(context.Background(), ExtractRequest{Archive: plain, DestDir: dest, SevenZip: tool}, ops.Hooks{Conflict: func(ops.Conflict) ops.Decision {
		asked++
		return ops.Decision{Action: ops.Skip}
	}})
	if asked != 2 || res.Skipped != 2 {
		t.Fatalf("conflicts: asked %d, %+v", asked, res)
	}
	if b, _ := os.ReadFile(filepath.Join(dest, "d", "a.txt")); string(b) != "mine" {
		t.Fatal("skip")
	}
	noTemps(t, dest)

	// encrypted: clear message, nothing extracted, never a prompt
	encDest := filepath.Join(root, "enc-out")
	res = ExtractAny(context.Background(), ExtractRequest{Archive: filepath.Join(root, "enc.7z"), DestDir: encDest, SevenZip: tool}, ops.Hooks{})
	if res.Fatal == nil || !strings.Contains(res.Fatal.Error(), "password-protected") {
		t.Fatalf("encrypted: %v", res.Fatal)
	}
	if ents, _ := os.ReadDir(encDest); len(ents) != 0 {
		t.Fatalf("nothing may be extracted: %v", ents)
	}
	// damaged
	b, _ := os.ReadFile(plain)
	cut := writeFile(t, filepath.Join(root, "cut.7z"), b[:40])
	res = ExtractAny(context.Background(), ExtractRequest{Archive: cut, DestDir: filepath.Join(root, "cut-out"), SevenZip: tool}, ops.Hooks{})
	if res.Fatal == nil || !strings.Contains(res.Fatal.Error(), "damaged") {
		t.Fatalf("damaged: %v", res.Fatal)
	}
	// no 7-Zip
	res = ExtractAny(context.Background(), ExtractRequest{Archive: plain, DestDir: dest}, ops.Hooks{})
	if !errors.Is(res.Fatal, ErrNeeds7zip) {
		t.Fatalf("no tool: %v", res.Fatal)
	}
}
