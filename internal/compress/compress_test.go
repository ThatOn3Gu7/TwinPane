package compress

import (
	"archive/tar"
	"bytes"
	stdbzip2 "compress/bzip2"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	"github.com/ulikunitz/xz"

	"github.com/ThatOn3Gu7/TwinPane/internal/archive"
	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
)

// awkward names: spaces, quotes, Unicode and shell metacharacters
var awkward = []string{
	"with space.txt",
	`quote's "double".txt`,
	"ünïcødé 文件.txt",
	"$(echo pwned) `id`;&|.txt",
	"-rf",
	"@list",
	"star*?[x].txt",
}

// tree builds src/ with nested folders, an empty folder, awkward names and
// a symbolic link; it returns the src folder and the expected file contents
// by archive path.
func tree(t *testing.T, root string) (string, map[string]string) {
	t.Helper()
	src := filepath.Join(root, "src")
	want := map[string]string{}
	write := func(rel, content string) {
		p := filepath.Join(src, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		want["src/"+rel] = content
	}
	write("a.txt", strings.Repeat("hello compress ", 200))
	write("sub/deep/b.txt", "bee")
	for i, n := range awkward {
		write("odd/"+n, "content "+string(rune('A'+i)))
	}
	if err := os.MkdirAll(filepath.Join(src, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../a.txt", filepath.Join(src, "sub", "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(src, "sub", "deep", "b.txt"), 0o600); err != nil {
		t.Fatal(err)
	}
	return src, want
}

func decoder(t *testing.T, c Codec, r io.Reader) io.Reader {
	t.Helper()
	switch c {
	case None:
		return r
	case Gzip:
		zr, err := gzip.NewReader(r)
		if err != nil {
			t.Fatal(err)
		}
		return zr
	case Xz:
		zr, err := xz.NewReader(r)
		if err != nil {
			t.Fatal(err)
		}
		return zr
	case Zstd:
		zr, err := zstd.NewReader(r)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(zr.Close)
		return zr
	case Bzip2:
		return stdbzip2.NewReader(r) // the standard library's reader, independent of the writer
	case Lz4:
		return lz4.NewReader(r)
	}
	t.Fatalf("no decoder for %d", c)
	return nil
}

type tarEntry struct {
	typ      byte
	content  string
	linkname string
	mode     int64
}

func readTar(t *testing.T, path string, c Codec) map[string]tarEntry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	tr := tar.NewReader(decoder(t, c, f))
	out := map[string]tarEntry{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("reading %s: %v", path, err)
		}
		b, _ := io.ReadAll(tr)
		out[h.Name] = tarEntry{typ: h.Typeflag, content: string(b), linkname: h.Linkname, mode: h.Mode}
	}
	return out
}

func noLeftovers(t *testing.T, dir string) {
	t.Helper()
	ents, _ := os.ReadDir(dir)
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), ops.TempPrefix) {
			t.Errorf("temporary file left behind: %s", e.Name())
		}
	}
}

func TestFormatTable(t *testing.T) {
	ids, exts := map[ID]bool{}, map[string]bool{}
	for _, f := range Formats() {
		if ids[f.ID] || exts[f.Ext] {
			t.Errorf("duplicate format %s / %s", f.ID, f.Ext)
		}
		ids[f.ID], exts[f.Ext] = true, true
		if len(f.Levels) > 0 && (f.DefaultLevel < 0 || f.DefaultLevel >= len(f.Levels)) {
			t.Errorf("%s: default level %d out of range", f.Name, f.DefaultLevel)
		}
		if len(f.Levels) == 0 && f.HasLevels() {
			t.Errorf("%s: HasLevels without levels", f.Name)
		}
		if !strings.HasPrefix(f.Ext, ".") {
			t.Errorf("%s: extension %q", f.Name, f.Ext)
		}
	}
	for _, id := range []ID{ZIP, SevenZ, TAR, TGZ, TGZ2, TXZ, TZST, TBZ2, TLZ4, GZ, XZ, ZST, BZ2, LZ4} {
		if !ids[id] {
			t.Errorf("format %s missing", id)
		}
	}
	for _, id := range []ID{TXZ, XZ, TAR} {
		if f, _ := Lookup(id); f.HasLevels() {
			t.Errorf("%s must not offer levels (the backend has none)", id)
		}
	}
}

func TestMatchAndCheckName(t *testing.T) {
	cases := map[string]ID{"a.tar.gz": TGZ, "A.TAR.GZ": TGZ, "a.gz": GZ, "a.tgz": TGZ2, "x.tar.zst": TZST, "x.zst": ZST, "b.7z": SevenZ, "c.zip": ZIP, "d.tar": TAR}
	for name, want := range cases {
		if f, ok := MatchExt(name); !ok || f.ID != want {
			t.Errorf("MatchExt(%q) = %s, %v; want %s", name, f.ID, ok, want)
		}
	}
	for _, name := range []string{"notes.txt", ".gz", "tar"} {
		if f, ok := MatchExt(name); ok {
			t.Errorf("MatchExt(%q) = %s", name, f.ID)
		}
	}
	tgz, _ := Lookup(TGZ)
	gz, _ := Lookup(GZ)
	zip, _ := Lookup(ZIP)
	if n, err := CheckName(tgz, "photos"); err != nil || n != "photos.tar.gz" {
		t.Errorf("CheckName append = %q, %v", n, err)
	}
	if n, err := CheckName(zip, "photos.2024"); err != nil || n != "photos.2024.zip" {
		t.Errorf("CheckName other ext = %q, %v", n, err)
	}
	if _, err := CheckName(tgz, "photos.zip"); err == nil || !strings.Contains(err.Error(), ".zip") {
		t.Errorf("CheckName mismatch = %v", err)
	}
	var ee *ExtError
	if _, err := CheckName(tgz, "x.gz"); !errors.As(err, &ee) {
		t.Errorf("x.gz as TAR.GZ must be refused, got %v", err)
	}
	// a single compressed .tar keeps its name: backup.tar.gz is a GZIP file
	if n, err := CheckName(gz, "backup.tar.gz"); err != nil || n != "backup.tar.gz" {
		t.Errorf("CheckName gz of tar = %q, %v", n, err)
	}
	if TrimExt("a.b.tar.xz") != "a.b" || TrimExt("plain.txt") != "plain.txt" {
		t.Error("TrimExt")
	}
}

func TestDetect(t *testing.T) {
	none := func(string) (string, error) { return "", errors.New("not found") }
	caps := Detect(none)
	if len(caps) != len(table) {
		t.Fatalf("%d capabilities", len(caps))
	}
	for _, c := range caps {
		if c.ID == SevenZ {
			if c.Available || !strings.Contains(c.Reason, "7z") {
				t.Errorf("7z without a program: %+v", c)
			}
		} else if !c.Available || c.Backend != "built in" {
			t.Errorf("%s should be built in and available: %+v", c.Name, c)
		}
	}
	if c, _ := Find(Detect(nil), SevenZ); c.Available {
		t.Error("nil lookPath must not find 7z")
	}
	// preference order: 7zz before 7z
	var asked []string
	found := func(name string) (string, error) {
		asked = append(asked, name)
		if name == "7z" || name == "7za" {
			return "/opt/bin/" + name, nil
		}
		return "", errors.New("no")
	}
	c, _ := Find(Detect(found), SevenZ)
	if !c.Available || c.Tool != "/opt/bin/7z" || c.Backend != "7z" {
		t.Errorf("7z detection: %+v", c)
	}
	if len(asked) < 2 || asked[0] != "7zz" || asked[1] != "7z" {
		t.Errorf("lookup order %v", asked)
	}
}

func TestMissingToolReasonTermux(t *testing.T) {
	t.Setenv("TERMUX_VERSION", "0.118.1")
	c, _ := Find(Detect(nil), SevenZ)
	if !strings.Contains(c.Reason, "pkg install 7zip") {
		t.Errorf("Termux reason %q", c.Reason)
	}
}

func TestCheckSelection(t *testing.T) {
	caps := Detect(nil)
	gz, _ := Find(caps, GZ)
	tgz, _ := Find(caps, TGZ)
	seven, _ := Find(caps, SevenZ)
	if err := gz.Check(Selection{Items: 1, SingleFile: true}); err != nil {
		t.Errorf("gz one file: %v", err)
	}
	if err := gz.Check(Selection{Items: 3}); err == nil || !strings.Contains(err.Error(), "TAR.GZ") {
		t.Errorf("gz several: %v", err)
	}
	if err := gz.Check(Selection{Items: 1}); err == nil || !strings.Contains(err.Error(), "folder") {
		t.Errorf("gz folder: %v", err)
	}
	if err := tgz.Check(Selection{Items: 3}); err != nil {
		t.Errorf("tar.gz several: %v", err)
	}
	if err := seven.Check(Selection{Items: 1, SingleFile: true}); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Errorf("unavailable 7z: %v", err)
	}
	if err := tgz.Check(Selection{}); err == nil {
		t.Error("empty selection accepted")
	}
}

func TestCreateTarFormats(t *testing.T) {
	for _, id := range []ID{TAR, TGZ, TGZ2, TXZ, TZST, TBZ2, TLZ4} {
		t.Run(string(id), func(t *testing.T) {
			root := t.TempDir()
			src, want := tree(t, root)
			f, _ := Lookup(id)
			out := filepath.Join(root, "out"+f.Ext)
			var progressed bool
			res := Create(context.Background(), Request{Format: id, Level: DefaultLevelIndex, Sources: []string{src}, Output: out},
				ops.Hooks{Progress: func(ops.Progress) { progressed = true }})
			if !res.OK() {
				t.Fatalf("not OK: %s %v", res.Summary(), res.Errors)
			}
			if !progressed {
				t.Error("no progress reported")
			}
			got := readTar(t, out, f.Codec)
			for name, content := range want {
				e, ok := got[name]
				if !ok {
					t.Errorf("missing %q", name)
					continue
				}
				if e.content != content {
					t.Errorf("%q content %q", name, e.content)
				}
			}
			for _, d := range []string{"src/", "src/empty/", "src/sub/deep/", "src/odd/"} {
				if e, ok := got[d]; !ok || e.typ != tar.TypeDir {
					t.Errorf("folder %q missing", d)
				}
			}
			if l := got["src/sub/link"]; l.typ != tar.TypeSymlink || l.linkname != "../a.txt" {
				t.Errorf("link stored as %+v", l)
			}
			if m := got["src/sub/deep/b.txt"].mode & 0o777; m != 0o600 {
				t.Errorf("mode %o, want 600", m)
			}
			if res.Files != len(want) || res.Links != 1 || res.Dirs != 5 {
				t.Errorf("counts files=%d links=%d dirs=%d", res.Files, res.Links, res.Dirs)
			}
			if !strings.HasPrefix(res.Summary(), "Created out"+f.Ext+" (") {
				t.Errorf("summary %q", res.Summary())
			}
			noLeftovers(t, root)
		})
	}
}

func TestCreateMultipleSources(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "d")
	os.MkdirAll(filepath.Join(dir, "x", "y"), 0o755)
	os.WriteFile(filepath.Join(dir, "one.txt"), []byte("1"), 0o644)
	os.WriteFile(filepath.Join(dir, "x", "y", "two.txt"), []byte("2"), 0o644)
	os.WriteFile(filepath.Join(dir, "skip.txt"), []byte("not selected"), 0o644)
	out := filepath.Join(root, "multi.tar.zst")
	res := Create(context.Background(), Request{Format: TZST, Level: 3, Sources: []string{filepath.Join(dir, "one.txt"), filepath.Join(dir, "x")}, Output: out}, ops.Hooks{})
	if !res.OK() {
		t.Fatal(res.Summary())
	}
	got := readTar(t, out, Zstd)
	var names []string
	for n := range got {
		names = append(names, n)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "one.txt,x/,x/y/,x/y/two.txt" {
		t.Errorf("entries %v (structure must be relative to the selection's folder)", names)
	}
}

func TestOutputInsideSourceIsExcluded(t *testing.T) {
	root := t.TempDir()
	src, _ := tree(t, root)
	out := filepath.Join(src, "sub", "self.tar.gz")
	os.WriteFile(out, []byte("old archive"), 0o644)
	res := Create(context.Background(), Request{Format: TGZ, Sources: []string{src}, Output: out, Overwrite: true}, ops.Hooks{})
	if !res.OK() {
		t.Fatal(res.Summary())
	}
	got := readTar(t, out, Gzip)
	for n := range got {
		if strings.Contains(n, "self.tar.gz") || strings.Contains(n, ops.TempPrefix) {
			t.Errorf("archive contains itself or its temp file: %q", n)
		}
	}
	if len(res.Excluded) < 2 {
		t.Errorf("excluded %v (want the old output and the temp file)", res.Excluded)
	}
	noLeftovers(t, filepath.Join(src, "sub"))
}

func TestSpecialFilesReported(t *testing.T) {
	root := t.TempDir()
	src, _ := tree(t, root)
	if err := mkfifo(filepath.Join(src, "pipe")); err != nil {
		t.Skip("no FIFOs here:", err)
	}
	out := filepath.Join(root, "s.tar")
	res := Create(context.Background(), Request{Format: TAR, Sources: []string{src}, Output: out}, ops.Hooks{})
	if !res.Created() || len(res.Errors) != 1 || !strings.Contains(res.Errors[0].Path, "pipe") {
		t.Fatalf("FIFO handling: %s %v", res.Summary(), res.Errors)
	}
	if _, ok := readTar(t, out, None)["src/pipe"]; ok {
		t.Error("FIFO stored")
	}
	if !strings.Contains(res.Summary(), "1 item could not be added") {
		t.Errorf("summary %q", res.Summary())
	}
}

func TestCreateSingleFormats(t *testing.T) {
	data := bytes.Repeat([]byte("single file data — ünïcødé\n"), 500)
	for _, id := range []ID{GZ, XZ, ZST, BZ2, LZ4} {
		t.Run(string(id), func(t *testing.T) {
			root := t.TempDir()
			src := filepath.Join(root, "notes $(x) 'q'.txt")
			os.WriteFile(src, data, 0o644)
			f, _ := Lookup(id)
			out := src + f.Ext
			res := Create(context.Background(), Request{Format: id, Level: DefaultLevelIndex, Sources: []string{src}, Output: out}, ops.Hooks{})
			if !res.OK() {
				t.Fatal(res.Summary())
			}
			fh, err := os.Open(out)
			if err != nil {
				t.Fatal(err)
			}
			defer fh.Close()
			r := decoder(t, f.Codec, fh)
			got, err := io.ReadAll(r)
			if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("round trip failed: %v (%d bytes)", err, len(got))
			}
			if zr, ok := r.(*gzip.Reader); ok && zr.Name != filepath.Base(src) {
				t.Errorf("gzip header name %q", zr.Name)
			}
			if res.Bytes != int64(len(data)) || res.Size <= 0 || res.Size >= res.Bytes {
				t.Errorf("sizes %d → %d", res.Bytes, res.Size)
			}
			if !strings.Contains(res.Summary(), "→") {
				t.Errorf("summary %q", res.Summary())
			}
			noLeftovers(t, root)
		})
	}
}

func TestGzipNonLatin1NameOmitted(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "文件.txt")
	os.WriteFile(src, []byte("x"), 0o644)
	res := Create(context.Background(), Request{Format: GZ, Sources: []string{src}, Output: src + ".gz"}, ops.Hooks{})
	if !res.OK() {
		t.Fatal(res.Summary())
	}
}

func TestLevels(t *testing.T) {
	data := bytes.Repeat([]byte("levels levels 0123456789 abcdefghij "), 4000)
	for _, f := range Formats() {
		if f.External() || !f.HasLevels() {
			continue
		}
		var sizes []int64
		for i := range f.Levels {
			root := t.TempDir()
			src := filepath.Join(root, "d.bin")
			os.WriteFile(src, data, 0o644)
			out := filepath.Join(root, "o"+f.Ext)
			if f.Kind == Archive && f.ID != ZIP {
				out = filepath.Join(root, "o"+f.Ext)
			}
			res := Create(context.Background(), Request{Format: f.ID, Level: i, Sources: []string{src}, Output: out}, ops.Hooks{})
			if !res.OK() {
				t.Fatalf("%s level %s: %s", f.Name, f.Levels[i].Name, res.Summary())
			}
			// every level must produce readable output
			switch {
			case f.ID == ZIP:
				a, err := archive.Open(out)
				if err != nil || a.Files != 1 {
					t.Fatalf("%s level %d unreadable: %v", f.Name, i, err)
				}
			case f.Kind == Archive:
				if got := readTar(t, out, f.Codec)["d.bin"]; got.content != string(data) {
					t.Fatalf("%s level %d: content differs", f.Name, i)
				}
			default:
				fh, _ := os.Open(out)
				b, err := io.ReadAll(decoder(t, f.Codec, fh))
				fh.Close()
				if err != nil || !bytes.Equal(b, data) {
					t.Fatalf("%s level %d: round trip failed", f.Name, i)
				}
			}
			sizes = append(sizes, res.Size)
		}
		if sizes[len(sizes)-1] > sizes[0] {
			t.Errorf("%s: best level (%d bytes) larger than fastest (%d)", f.Name, sizes[len(sizes)-1], sizes[0])
		}
	}
	// out-of-range level indexes fall back to the default
	f, _ := Lookup(TGZ)
	if f.LevelIndex(99) != f.DefaultLevel || f.LevelIndex(1) != 1 {
		t.Error("LevelIndex")
	}
}

func TestZipThroughCompress(t *testing.T) {
	root := t.TempDir()
	src, want := tree(t, root)
	out := filepath.Join(root, "z.zip")
	res := Create(context.Background(), Request{Format: ZIP, Level: 3, Sources: []string{src}, Output: out}, ops.Hooks{})
	if !res.OK() || res.Format.ID != ZIP {
		t.Fatal(res.Summary())
	}
	a, err := archive.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	if a.Files != len(want)+1 { // + the link, stored as a ZIP entry
		t.Errorf("zip files %d, want %d", a.Files, len(want)+1)
	}
	if !strings.HasPrefix(res.Summary(), "Created z.zip (") {
		t.Errorf("summary %q", res.Summary())
	}
}

func TestSingleRejectsFolderAndMany(t *testing.T) {
	root := t.TempDir()
	src, _ := tree(t, root)
	res := Create(context.Background(), Request{Format: XZ, Sources: []string{src}, Output: filepath.Join(root, "src.xz")}, ops.Hooks{})
	if res.Fatal == nil || !strings.Contains(res.Fatal.Error(), "folder") {
		t.Errorf("folder as XZ: %v", res.Fatal)
	}
	res = Create(context.Background(), Request{Format: GZ, Sources: []string{filepath.Join(src, "a.txt"), filepath.Join(src, "sub")}, Output: filepath.Join(root, "two.gz")}, ops.Hooks{})
	if res.Fatal == nil || !strings.Contains(res.Fatal.Error(), "single file") {
		t.Errorf("two items as GZIP: %v", res.Fatal)
	}
	noLeftovers(t, root)
	if _, err := os.Stat(filepath.Join(root, "src.xz")); err == nil {
		t.Error("output written")
	}
}

func TestWrongExtensionAndUnknownFormat(t *testing.T) {
	root := t.TempDir()
	src, _ := tree(t, root)
	res := Create(context.Background(), Request{Format: TXZ, Sources: []string{src}, Output: filepath.Join(root, "x.tar.gz")}, ops.Hooks{})
	if res.Fatal == nil || !strings.Contains(res.Fatal.Error(), ".tar.xz") {
		t.Errorf("wrong extension accepted: %v", res.Fatal)
	}
	res = Create(context.Background(), Request{Format: "rar", Sources: []string{src}, Output: filepath.Join(root, "x.rar")}, ops.Hooks{})
	if res.Fatal == nil || !strings.Contains(res.Fatal.Error(), "unknown format") {
		t.Errorf("unknown format: %v", res.Fatal)
	}
	if ents, _ := os.ReadDir(root); len(ents) != 1 {
		t.Errorf("files written: %v", ents)
	}
}

func TestExistingOutputNeedsOverwrite(t *testing.T) {
	root := t.TempDir()
	src, _ := tree(t, root)
	out := filepath.Join(root, "e.tar.bz2")
	os.WriteFile(out, []byte("precious"), 0o644)
	res := Create(context.Background(), Request{Format: TBZ2, Sources: []string{src}, Output: out}, ops.Hooks{})
	if !errors.Is(res.Fatal, ErrExists) {
		t.Fatalf("want ErrExists, got %v", res.Fatal)
	}
	if b, _ := os.ReadFile(out); string(b) != "precious" {
		t.Fatal("existing file changed")
	}
	res = Create(context.Background(), Request{Format: TBZ2, Sources: []string{src}, Output: out, Overwrite: true}, ops.Hooks{})
	if !res.OK() {
		t.Fatal(res.Summary())
	}
	if _, ok := readTar(t, out, Bzip2)["src/a.txt"]; !ok {
		t.Error("overwritten archive unreadable")
	}
	noLeftovers(t, root)
}

func TestCancelLeavesNothing(t *testing.T) {
	for _, id := range []ID{TZST, XZ, ZIP} {
		t.Run(string(id), func(t *testing.T) {
			root := t.TempDir()
			src := filepath.Join(root, "big.bin")
			os.WriteFile(src, bytes.Repeat([]byte("x"), 8<<20), 0o644)
			f, _ := Lookup(id)
			out := filepath.Join(root, "big"+f.Ext)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			res := Create(ctx, Request{Format: id, Sources: []string{src}, Output: out}, ops.Hooks{Progress: func(p ops.Progress) {
				if p.DoneBytes > 0 || p.Phase == "compressing" {
					cancel()
				}
			}})
			if !res.Canceled || res.Created() {
				t.Fatalf("not cancelled: %s", res.Summary())
			}
			if !strings.Contains(res.Summary(), "cancelled") {
				t.Errorf("summary %q", res.Summary())
			}
			if _, err := os.Stat(out); err == nil {
				t.Error("output exists after cancel")
			}
			noLeftovers(t, root)
		})
	}
}

func TestUnwritableDestination(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}
	root := t.TempDir()
	src, _ := tree(t, root)
	ro := filepath.Join(root, "ro")
	os.Mkdir(ro, 0o555)
	defer os.Chmod(ro, 0o755)
	res := Create(context.Background(), Request{Format: TGZ, Sources: []string{src}, Output: filepath.Join(ro, "x.tar.gz")}, ops.Hooks{})
	if res.Fatal == nil || !strings.Contains(res.Fatal.Error(), "cannot write in") || !errors.Is(res.Fatal, fs.ErrPermission) {
		t.Errorf("unwritable: %v", res.Fatal)
	}
}

func TestUnreadableFileLeftOut(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read anything")
	}
	root := t.TempDir()
	src, _ := tree(t, root)
	secret := filepath.Join(src, "secret.txt")
	os.WriteFile(secret, []byte("s"), 0o000)
	out := filepath.Join(root, "u.tar.lz4")
	res := Create(context.Background(), Request{Format: TLZ4, Sources: []string{src}, Output: out}, ops.Hooks{})
	if !res.Created() || len(res.Errors) != 1 || res.Errors[0].Path != secret {
		t.Fatalf("unreadable file: %s %v", res.Summary(), res.Errors)
	}
	if _, ok := readTar(t, out, Lz4)["src/a.txt"]; !ok {
		t.Error("readable files missing")
	}
}

func TestMissing7zTool(t *testing.T) {
	root := t.TempDir()
	src, _ := tree(t, root)
	res := Create(context.Background(), Request{Format: SevenZ, Sources: []string{src}, Output: filepath.Join(root, "x.7z")}, ops.Hooks{})
	if res.Fatal == nil || !strings.Contains(res.Fatal.Error(), "not found") {
		t.Errorf("missing tool: %v", res.Fatal)
	}
	if !strings.HasPrefix(res.Summary(), "7Z not created") {
		t.Errorf("summary %q", res.Summary())
	}
	noLeftovers(t, root)
}

func TestParse7zBanner(t *testing.T) {
	cases := []struct {
		out  string
		snl  bool
		head string
	}{
		{"\n7-Zip 25.01 (x64) : Copyright (c) 1999-2025 Igor Pavlov : 2025-08-03\n", true, "7-Zip 25.01"},
		{"\n7-Zip (z) 24.08 (arm64) : Copyright (c) 1999-2024 Igor Pavlov\n", true, "7-Zip (z) 24.08"},
		{"\n7-Zip [64] 16.02 : Copyright (c) 1999-2016 Igor Pavlov : 2016-05-21\np7zip Version 16.02 (locale=utf8,Utf16=on)\n", false, "7-Zip [64] 16.02"},
		{"7-Zip 9.20  Copyright (c) 1999-2010 Igor Pavlov\n", false, "7-Zip 9.20"},
		{"garbage", false, ""},
	}
	for _, c := range cases {
		in := parse7zBanner(c.out)
		if in.snl != c.snl || !strings.HasPrefix(in.banner, c.head) {
			t.Errorf("parse7zBanner(%q) = %+v", c.out, in)
		}
	}
}

func TestSevenArgs(t *testing.T) {
	args := sevenArgs(5, true, []string{"src/sub/out.7z"}, "/d/.tp-7z-1/new.7z", []string{"-rf", "@list", "a*b"})
	got := strings.Join(args, "|")
	want := "a|-t7z|-mx=5|-bso0|-bse2|-bsp1|-y|-spd|-snl|-x!src/sub/out.7z|--|/d/.tp-7z-1/new.7z|-rf|@list|a*b"
	if got != want {
		t.Errorf("args\n got %s\nwant %s", got, want)
	}
	if strings.Contains(strings.Join(sevenArgs(0, false, nil, "o", []string{"x"}), " "), "-snl") {
		t.Error("-snl without support")
	}
}

func TestSevenWarnings(t *testing.T) {
	stderr := "\nWARNING: errno=2 : No such file or directory\nnonexist\n\n\nScan WARNINGS for files and folders:\n\nnonexist : errno=2 : No such file or directory\n----------------\nScan WARNINGS: 1\n"
	if w := sevenWarnings(stderr); len(w) != 1 || w[0] != "nonexist" {
		t.Errorf("warnings %v", w)
	}
	if lastLine(stderr) != "Scan WARNINGS: 1" {
		t.Errorf("lastLine %q", lastLine(stderr))
	}
}
