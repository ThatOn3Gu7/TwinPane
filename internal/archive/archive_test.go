package archive

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
)

type zent struct {
	name    string
	body    string
	mode    fs.FileMode // 0 = default
	raw     *zip.FileHeader
	rawBody []byte
}

// makeZip writes a test archive (in a temp dir) and returns its path.
func makeZip(t *testing.T, dir, name string, ents []zent) string {
	t.Helper()
	p := filepath.Join(dir, name)
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	mt := time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)
	for _, e := range ents {
		if e.raw != nil {
			w, err := zw.CreateRaw(e.raw)
			if err != nil {
				t.Fatal(err)
			}
			w.Write(e.rawBody)
			continue
		}
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate, Modified: mt}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(w, e.body)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()
	return p
}

func names(es []fsys.Entry) []string {
	var out []string
	for _, e := range es {
		out = append(out, e.Name)
	}
	return out
}

func TestBrowseTreeWithoutExtracting(t *testing.T) {
	dir := t.TempDir()
	p := makeZip(t, dir, "t.zip", []zent{
		{name: "top.txt", body: "hello"},
		{name: "docs/"},
		{name: "docs/a.md", body: "# a"},
		{name: "docs/deep/b.txt", body: "bbb"}, // "docs/deep/" is implied
		{name: "empty/"},
		{name: ".hidden", body: "h"},
		{name: `win\style.txt`, body: "w"},
	})
	before, _ := os.ReadDir(dir)
	a, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadDir(dir)
	if len(after) != len(before) {
		t.Fatalf("opening the archive wrote files: %v", after)
	}
	l, err := a.List("", fsys.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(names(l.Entries), ","); got != "..,docs,empty,win,top.txt" {
		t.Fatalf("root listing %q", got)
	}
	if l.Hidden != 1 || l.Path != a.Path || l.Entries[0].Path != dir {
		t.Fatalf("hidden %d path %q parent %q", l.Hidden, l.Path, l.Entries[0].Path)
	}
	l2, _ := a.List("", fsys.Options{ShowHidden: true})
	if len(l2.Entries) != 6 {
		t.Fatalf("with hidden: %v", names(l2.Entries))
	}
	ld, err := a.List("docs", fsys.Options{})
	if err != nil || strings.Join(names(ld.Entries), ",") != "..,deep,a.md" {
		t.Fatalf("docs: %v %v", names(ld.Entries), err)
	}
	if ld.Entries[0].Path != a.VirtualPath("") || ld.Path != a.Path+"/docs" {
		t.Fatalf("docs parent %q path %q", ld.Entries[0].Path, ld.Path)
	}
	deep, _ := a.Lookup("docs/deep")
	if !deep.IsDir || !deep.Implicit {
		t.Fatalf("implied folder %+v", deep)
	}
	lb, _ := a.List("docs/deep", fsys.Options{})
	if e := lb.Entries[1]; e.Name != "b.txt" || e.Size != 3 || e.IsDir || e.Path != a.Path+"/docs/deep/b.txt" {
		t.Fatalf("deep entry %+v", e)
	}
	le, _ := a.List("empty", fsys.Options{})
	if len(le.Entries) != 1 {
		t.Fatalf("empty folder lists %v", names(le.Entries))
	}
	if _, err := a.List("top.txt", fsys.Options{}); err == nil {
		t.Fatal("listing a file must fail")
	}
	if in, ok := a.InnerOf(a.Path + "/docs/deep"); !ok || in != "docs/deep" {
		t.Fatalf("InnerOf %q %v", in, ok)
	}
	if _, ok := a.InnerOf(dir + "/other"); ok {
		t.Fatal("InnerOf outside")
	}
	if a.Files != 5 || a.Dirs != 4 {
		t.Fatalf("counts files %d dirs %d", a.Files, a.Dirs)
	}
	// sorting by size, descending
	ls, _ := a.List("", fsys.Options{Sort: fsys.SortSize, Desc: true, ShowHidden: true})
	if got := strings.Join(names(ls.Entries), ","); got != "..,docs,empty,win,top.txt,.hidden" {
		t.Fatalf("size order %q", got)
	}
	// reading one entry
	rc, e, err := a.OpenEntry("docs/a.md")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "# a" || e.Name != "a.md" {
		t.Fatalf("entry data %q", b)
	}
	if _, _, err := a.OpenEntry("docs"); !errors.Is(err, ErrIsDir) {
		t.Fatalf("open folder: %v", err)
	}
}

func TestUnsafeNamesAreHiddenAndCounted(t *testing.T) {
	dir := t.TempDir()
	p := makeZip(t, dir, "evil.zip", []zent{
		{name: "../escape.txt", body: "x"},
		{name: "a/../../escape2.txt", body: "x"},
		{name: "/abs.txt", body: "x"},
		{name: "C:/win.txt", body: "x"},
		{name: `..\back.txt`, body: "x"},
		{name: "ok/./fine.txt", body: "fine"},
	})
	a, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(a.Unsafe) != 5 {
		t.Fatalf("unsafe %v", a.Unsafe)
	}
	if _, ok := a.Lookup("ok/fine.txt"); !ok {
		t.Fatal("normalised name missing")
	}
	l, _ := a.List("", fsys.Options{ShowHidden: true})
	if got := strings.Join(names(l.Entries), ","); got != "..,ok" {
		t.Fatalf("listing %q", got)
	}
}

func TestDamagedAndUnsupportedArchives(t *testing.T) {
	dir := t.TempDir()
	junk := filepath.Join(dir, "junk.zip")
	os.WriteFile(junk, bytes.Repeat([]byte("not a zip "), 50), 0o644)
	if _, err := Open(junk); err == nil || !strings.Contains(err.Error(), "not a valid ZIP") {
		t.Fatalf("junk: %v", err)
	}
	good := makeZip(t, dir, "good.zip", []zent{{name: "a.txt", body: strings.Repeat("abc", 1000)}})
	data, _ := os.ReadFile(good)
	trunc := filepath.Join(dir, "trunc.zip")
	os.WriteFile(trunc, data[:len(data)/2], 0o644)
	if _, err := Open(trunc); err == nil {
		t.Fatal("truncated archive opened")
	}
	if Sniff(junk) || !Sniff(good) {
		t.Fatal("sniff")
	}
	if Sniff(dir) {
		t.Fatal("sniff on a folder")
	}

	body := []byte("secret!")
	enc := &zip.FileHeader{Name: "enc.txt", Method: zip.Store, Flags: 0x1, CRC32: crc32.ChecksumIEEE(body),
		CompressedSize64: uint64(len(body)), UncompressedSize64: uint64(len(body))}
	odd := &zip.FileHeader{Name: "odd.bin", Method: 99, CRC32: crc32.ChecksumIEEE(body),
		CompressedSize64: uint64(len(body)), UncompressedSize64: uint64(len(body))}
	bad := &zip.FileHeader{Name: "badcrc.txt", Method: zip.Store, CRC32: 12345,
		CompressedSize64: uint64(len(body)), UncompressedSize64: uint64(len(body))}
	p := makeZip(t, dir, "mixed.zip", []zent{
		{raw: enc, rawBody: body}, {raw: odd, rawBody: body}, {raw: bad, rawBody: body},
		{name: "ok.txt", body: "fine"},
	})
	a, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if a.Encrypted != 1 {
		t.Fatalf("encrypted count %d", a.Encrypted)
	}
	if _, _, err := a.OpenEntry("enc.txt"); !errors.Is(err, ErrEncrypted) {
		t.Fatalf("encrypted: %v", err)
	}
	if _, _, err := a.OpenEntry("odd.bin"); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("method 99: %v", err)
	}
	rc, _, err := a.OpenEntry("badcrc.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(rc); !errors.Is(err, zip.ErrChecksum) {
		t.Fatalf("bad crc read: %v", err)
	}
	rc.Close()

	dest := filepath.Join(dir, "out")
	res := Extract(context.Background(), ExtractRequest{Archive: p, DestDir: dest}, ops.Hooks{})
	if res.Files != 1 || len(res.Errors) != 3 || res.OK() {
		t.Fatalf("mixed extraction: %+v", res)
	}
	for _, n := range []string{"enc.txt", "odd.bin", "badcrc.txt"} {
		if _, err := os.Lstat(filepath.Join(dest, n)); err == nil {
			t.Errorf("%s was written", n)
		}
	}
	leftovers, _ := filepath.Glob(filepath.Join(dest, ops.TempPrefix+"*"))
	if len(leftovers) != 0 {
		t.Fatalf("temporary files left: %v", leftovers)
	}
	if got := readFile(t, filepath.Join(dest, "ok.txt")); got != "fine" {
		t.Fatalf("ok.txt %q", got)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func tree(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == root {
			return nil
		}
		r, _ := filepath.Rel(root, p)
		if d.IsDir() {
			r += "/"
		}
		out = append(out, filepath.ToSlash(r))
		return nil
	})
	sort.Strings(out)
	return out
}

func TestExtractAllAndSelected(t *testing.T) {
	dir := t.TempDir()
	p := makeZip(t, dir, "t.zip", []zent{
		{name: "top.txt", body: "hello"},
		{name: "docs/a.md", body: "# a"},
		{name: "docs/deep/b.txt", body: "bbb"},
		{name: "empty/"},
		{name: "run.sh", body: "#!/bin/sh", mode: 0o4777},
	})
	dest := filepath.Join(dir, "all")
	res := Extract(context.Background(), ExtractRequest{Archive: p, DestDir: dest}, ops.Hooks{})
	if !res.OK() || res.Files != 4 {
		t.Fatalf("extract all: %+v", res)
	}
	if got := strings.Join(tree(t, dest), ","); got != "docs/,docs/a.md,docs/deep/,docs/deep/b.txt,empty/,run.sh,top.txt" {
		t.Fatalf("tree %s", got)
	}
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(filepath.Join(dest, "run.sh"))
		if fi.Mode().Perm() != 0o755 || fi.Mode()&(fs.ModeSetuid|fs.ModeSetgid) != 0 {
			t.Fatalf("run.sh mode %v (set-id / world-writable bits must be dropped)", fi.Mode())
		}
		fi, _ = os.Stat(filepath.Join(dest, "top.txt"))
		if fi.Mode().Perm() != 0o644 || !fi.ModTime().Equal(time.Date(2025, 3, 4, 5, 6, 7, 0, time.UTC)) {
			t.Fatalf("top.txt mode %v time %v", fi.Mode(), fi.ModTime())
		}
	}
	if strings.Join(res.Top, ",") != "docs,empty,run.sh,top.txt" {
		t.Fatalf("top %v", res.Top)
	}

	// selected entries relative to the folder being browsed
	sel := filepath.Join(dir, "sel")
	res = Extract(context.Background(), ExtractRequest{Archive: p, Base: "docs", Members: []string{"docs/deep"}, DestDir: sel}, ops.Hooks{})
	if !res.OK() || strings.Join(tree(t, sel), ",") != "deep/,deep/b.txt" {
		t.Fatalf("selected: %+v %v", res, tree(t, sel))
	}
	res = Extract(context.Background(), ExtractRequest{Archive: p, Members: []string{"nope"}, DestDir: sel}, ops.Hooks{})
	if len(res.Errors) != 1 {
		t.Fatalf("missing member: %+v", res)
	}
}

func TestExtractPathTraversalIsRefused(t *testing.T) {
	dir := t.TempDir()
	dest := filepath.Join(dir, "dest")
	os.MkdirAll(dest, 0o755)
	p := makeZip(t, dir, "evil.zip", []zent{
		{name: "../escape.txt", body: "x"},
		{name: "sub/../../escape2.txt", body: "x"},
		{name: "/tmp/abs-twinpane.txt", body: "x"},
		{name: `..\back.txt`, body: "x"},
		{name: "fine.txt", body: "ok"},
	})
	res := Extract(context.Background(), ExtractRequest{Archive: p, DestDir: dest}, ops.Hooks{})
	if res.Unsafe != 4 || res.Files != 1 {
		t.Fatalf("result %+v", res)
	}
	for _, bad := range []string{filepath.Join(dir, "escape.txt"), filepath.Join(dir, "escape2.txt"), filepath.Join(dir, "back.txt")} {
		if _, err := os.Lstat(bad); err == nil {
			t.Fatalf("%s escaped the destination", bad)
		}
	}
	if got := strings.Join(tree(t, dest), ","); got != "fine.txt" {
		t.Fatalf("dest %s", got)
	}
}

func TestExtractNeverWritesThroughSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	dir := t.TempDir()
	outside := filepath.Join(dir, "outside")
	dest := filepath.Join(dir, "dest")
	os.MkdirAll(outside, 0o755)
	os.MkdirAll(dest, 0o755)
	// a link already in the destination pointing outside it
	os.Symlink(outside, filepath.Join(dest, "sub"))
	p := makeZip(t, dir, "t.zip", []zent{
		{name: "sub/payload.txt", body: "x"},
		{name: "link", body: "/etc/passwd", mode: fs.ModeSymlink | 0o777},
		{name: "ok.txt", body: "ok"},
	})
	res := Extract(context.Background(), ExtractRequest{Archive: p, DestDir: dest}, ops.Hooks{})
	if _, err := os.Lstat(filepath.Join(outside, "payload.txt")); err == nil {
		t.Fatal("wrote through a symlink")
	}
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0].Err.Error(), "symbolic link") {
		t.Fatalf("errors %+v", res.Errors)
	}
	if res.Links != 1 {
		t.Fatalf("links %d", res.Links)
	}
	if _, err := os.Lstat(filepath.Join(dest, "link")); err == nil {
		t.Fatal("a symlink was created")
	}
}

func TestExtractConflicts(t *testing.T) {
	dir := t.TempDir()
	p := makeZip(t, dir, "t.zip", []zent{
		{name: "a.txt", body: "from-zip-a"},
		{name: "b.txt", body: "from-zip-b"},
		{name: "c.txt", body: "from-zip-c"},
		{name: "d", body: "file named d"},
	})
	dest := filepath.Join(dir, "dest")
	os.MkdirAll(filepath.Join(dest, "d"), 0o755)
	for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
		os.WriteFile(filepath.Join(dest, n), []byte("mine"), 0o644)
	}
	// nil hook: never overwrite
	res := Extract(context.Background(), ExtractRequest{Archive: p, DestDir: dest}, ops.Hooks{})
	if res.Skipped != 3 || res.Files != 0 || len(res.Errors) != 1 {
		t.Fatalf("nil hook: %+v", res)
	}
	for _, n := range []string{"a.txt", "b.txt", "c.txt"} {
		if readFile(t, filepath.Join(dest, n)) != "mine" {
			t.Fatalf("%s overwritten without permission", n)
		}
	}
	// overwrite a, skip b, cancel at c
	var asked []string
	answers := []ops.Action{ops.Overwrite, ops.Skip, ops.Cancel}
	res = Extract(context.Background(), ExtractRequest{Archive: p, DestDir: dest}, ops.Hooks{Conflict: func(c ops.Conflict) ops.Decision {
		asked = append(asked, filepath.Base(c.Dst))
		if c.SrcInfo == nil || c.DstInfo == nil {
			t.Error("conflict without infos")
		}
		a := answers[0]
		answers = answers[1:]
		return ops.Decision{Action: a}
	}})
	if strings.Join(asked, ",") != "a.txt,b.txt,c.txt" || !res.Canceled || res.Files != 1 || res.Skipped != 1 {
		t.Fatalf("asked %v res %+v", asked, res)
	}
	if readFile(t, filepath.Join(dest, "a.txt")) != "from-zip-a" || readFile(t, filepath.Join(dest, "b.txt")) != "mine" || readFile(t, filepath.Join(dest, "c.txt")) != "mine" {
		t.Fatal("wrong conflict handling")
	}
	// apply to all
	n := 0
	res = Extract(context.Background(), ExtractRequest{Archive: p, DestDir: dest}, ops.Hooks{Conflict: func(c ops.Conflict) ops.Decision {
		n++
		return ops.Decision{Action: ops.Overwrite, ApplyAll: true}
	}})
	if n != 1 || res.Files != 3 || readFile(t, filepath.Join(dest, "c.txt")) != "from-zip-c" {
		t.Fatalf("apply all: asked %d, %+v", n, res)
	}
}

func TestExtractCancelAndDestinationErrors(t *testing.T) {
	dir := t.TempDir()
	p := makeZip(t, dir, "t.zip", []zent{{name: "a.txt", body: "a"}, {name: "b.txt", body: "b"}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dest := filepath.Join(dir, "dest")
	res := Extract(ctx, ExtractRequest{Archive: p, DestDir: dest}, ops.Hooks{})
	if !res.Canceled || res.Files != 0 {
		t.Fatalf("cancelled: %+v", res)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("cancelled extraction created its destination: %v", err)
	}
	file := filepath.Join(dir, "file")
	os.WriteFile(file, nil, 0o644)
	if res := Extract(context.Background(), ExtractRequest{Archive: p, DestDir: file}, ops.Hooks{}); res.Fatal == nil {
		t.Fatal("destination is a file")
	}
	if res := Extract(context.Background(), ExtractRequest{Archive: p, DestDir: "relative"}, ops.Hooks{}); res.Fatal == nil {
		t.Fatal("relative destination")
	}
	if res := Extract(context.Background(), ExtractRequest{Archive: file, DestDir: dest}, ops.Hooks{}); res.Fatal == nil {
		t.Fatal("not a zip")
	}
	var progress []ops.Progress
	res = Extract(context.Background(), ExtractRequest{Archive: p, DestDir: dest}, ops.Hooks{Progress: func(pr ops.Progress) { progress = append(progress, pr) }})
	if !res.OK() || len(progress) < 2 || progress[len(progress)-1].DoneItems != 2 || progress[len(progress)-1].TotalItems != 2 {
		t.Fatalf("progress %+v", progress)
	}
}

func TestCreateFromFilesDirsAndSelection(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	os.MkdirAll(filepath.Join(src, "folder", "nested"), 0o755)
	os.MkdirAll(filepath.Join(src, "folder", "empty"), 0o755)
	os.WriteFile(filepath.Join(src, "one.txt"), []byte("one"), 0o644)
	os.WriteFile(filepath.Join(src, "two.txt"), []byte("two"), 0o600)
	os.WriteFile(filepath.Join(src, "folder", "nested", "n.txt"), []byte("nnn"), 0o644)
	if runtime.GOOS != "windows" {
		os.Symlink("one.txt", filepath.Join(src, "folder", "link"))
	}
	out := filepath.Join(dir, "out.zip")
	res := Create(context.Background(), CreateRequest{
		Sources: []string{filepath.Join(src, "one.txt"), filepath.Join(src, "two.txt"), filepath.Join(src, "folder")},
		Output:  out}, ops.Hooks{})
	if !res.OK() || res.Files != 3 || res.Dirs != 3 {
		t.Fatalf("create: %+v", res)
	}
	a, err := Open(out)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range a.Walk("") {
		n := e.Inner
		if e.IsDir {
			n += "/"
		}
		got = append(got, n)
	}
	sort.Strings(got)
	want := "folder/,folder/empty/,folder/link,folder/nested/,folder/nested/n.txt,one.txt,two.txt"
	if runtime.GOOS == "windows" {
		want = strings.Replace(want, "folder/link,", "", 1)
	}
	if strings.Join(got, ",") != want {
		t.Fatalf("entries %v", got)
	}
	rc, _, err := a.OpenEntry("folder/nested/n.txt")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(rc)
	rc.Close()
	if string(b) != "nnn" {
		t.Fatalf("content %q", b)
	}
	if e, _ := a.Lookup("folder/empty"); e == nil || !e.IsDir || e.Implicit {
		t.Fatal("empty folder not stored")
	}
	// round trip
	back := filepath.Join(dir, "back")
	if r := Extract(context.Background(), ExtractRequest{Archive: out, DestDir: back}, ops.Hooks{}); !r.OK() {
		t.Fatalf("round trip %+v", r)
	}
	if readFile(t, filepath.Join(back, "two.txt")) != "two" {
		t.Fatal("round trip content")
	}
}

func TestCreateExcludesItsOwnOutputAndNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "a.txt"), []byte("aaaa"), 0o644)
	out := filepath.Join(src, "src.zip") // the output lives inside the folder being archived
	res := Create(context.Background(), CreateRequest{Sources: []string{src}, Output: out}, ops.Hooks{})
	if !res.OK() {
		t.Fatalf("create: %+v", res)
	}
	a, _ := Open(out)
	for _, e := range a.Walk("") {
		if strings.HasSuffix(e.Name, ".zip") || strings.HasPrefix(e.Name, ops.TempPrefix) {
			t.Fatalf("archive contains its own output / temp file: %s", e.Inner)
		}
	}
	if a.Files != 1 {
		t.Fatalf("files %d", a.Files)
	}
	// existing output: refused without Overwrite, untouched
	before := readFile(t, out)
	res = Create(context.Background(), CreateRequest{Sources: []string{src}, Output: out}, ops.Hooks{})
	if !errors.Is(res.Fatal, ErrExists) || readFile(t, out) != before {
		t.Fatalf("overwrite without permission: %+v", res)
	}
	// with Overwrite: replaced, and the old archive is excluded from the new one
	os.WriteFile(filepath.Join(src, "b.txt"), []byte("b"), 0o644)
	res = Create(context.Background(), CreateRequest{Sources: []string{src}, Output: out, Overwrite: true}, ops.Hooks{})
	if !res.OK() || len(res.Excluded) == 0 {
		t.Fatalf("overwrite: %+v", res)
	}
	a, _ = Open(out)
	if a.Files != 2 {
		t.Fatalf("files after overwrite %d", a.Files)
	}
	leftovers, _ := filepath.Glob(filepath.Join(src, ops.TempPrefix+"*"))
	if len(leftovers) != 0 {
		t.Fatalf("temp files left: %v", leftovers)
	}
}

func TestCreateErrorsAndCancel(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644)
	if r := Create(context.Background(), CreateRequest{Sources: []string{filepath.Join(dir, "a.txt")}, Output: filepath.Join(dir, "nope", "x.zip")}, ops.Hooks{}); r.Fatal == nil {
		t.Fatal("missing destination folder")
	}
	if r := Create(context.Background(), CreateRequest{Sources: []string{filepath.Join(dir, "a.txt")}, Output: dir}, ops.Hooks{}); r.Fatal == nil {
		t.Fatal("output is a folder")
	}
	if r := Create(context.Background(), CreateRequest{Sources: nil, Output: filepath.Join(dir, "x.zip")}, ops.Hooks{}); r.Fatal == nil {
		t.Fatal("no sources")
	}
	other := filepath.Join(dir, "other")
	os.MkdirAll(other, 0o755)
	os.WriteFile(filepath.Join(other, "a.txt"), []byte("a"), 0o644)
	if r := Create(context.Background(), CreateRequest{Sources: []string{filepath.Join(dir, "a.txt"), filepath.Join(other, "a.txt")}, Output: filepath.Join(dir, "x.zip")}, ops.Hooks{}); r.Fatal == nil {
		t.Fatal("duplicate names")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	out := filepath.Join(dir, "c.zip")
	if r := Create(ctx, CreateRequest{Sources: []string{filepath.Join(dir, "a.txt")}, Output: out}, ops.Hooks{}); !r.Canceled {
		t.Fatalf("cancel: %+v", r)
	}
	if _, err := os.Lstat(out); err == nil {
		t.Fatal("cancelled archive was written")
	}
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		locked := filepath.Join(dir, "locked.txt")
		os.WriteFile(locked, []byte("x"), 0o000)
		r := Create(context.Background(), CreateRequest{Sources: []string{filepath.Join(dir, "a.txt"), locked}, Output: filepath.Join(dir, "l.zip")}, ops.Hooks{})
		if !r.Created() || len(r.Errors) != 1 || r.Files != 1 {
			t.Fatalf("unreadable file: %+v", r)
		}
	}
}
