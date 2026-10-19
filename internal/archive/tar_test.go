package archive

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
)

func TestTARFileEntryCannotBecomeAParentDirectory(t *testing.T) {
	root := t.TempDir()
	p := writeFile(t, filepath.Join(root, "conflict.tar"), tarBytes(t, []tarItem{
		{name: "node", typ: tar.TypeReg, body: "file"},
		{name: "node/child.txt", typ: tar.TypeReg, body: "not reachable"},
	}))
	a, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if a.Duplicates != 1 || a.Files != 1 {
		t.Fatalf("conflicting tree counts: files=%d duplicates=%d", a.Files, a.Duplicates)
	}
	if _, ok := a.Lookup("node"); !ok {
		t.Fatal("first file was not kept")
	}
	if _, ok := a.Lookup("node/child.txt"); ok {
		t.Fatal("child below a file was indexed")
	}
}

func TestBrowseLegacyRegularTARFile(t *testing.T) {
	root := t.TempDir()
	var data bytes.Buffer
	tw := tar.NewWriter(&data)
	payload := []byte("legacy regular file")
	if err := tw.WriteHeader(&tar.Header{Name: "legacy.txt", Typeflag: tar.TypeRegA, Mode: 0o644, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	// Go's writer canonicalises TypeRegA to TypeReg, so emulate a V7 header
	// with the NUL regular-file flag and recompute its checksum.
	header := data.Bytes()[:512]
	header[156] = tar.TypeRegA
	var checksum int
	for i, b := range header {
		if i >= 148 && i < 156 {
			b = ' '
		}
		checksum += int(b)
	}
	copy(header[148:156], []byte(fmt.Sprintf("%06o\x00 ", checksum)))
	p := writeFile(t, filepath.Join(root, "legacy.tar"), data.Bytes())
	a, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	e, ok := a.Lookup("legacy.txt")
	if !ok {
		t.Fatal("legacy regular entry missing")
	}
	if !isTARRegular(tar.TypeRegA) || e.TarType != tar.TypeReg || e.Supported() != nil {
		t.Fatalf("legacy regular entry should be browseable: %+v, supported=%v", e, e.Supported())
	}
	rc, _, err := a.OpenEntry("legacy.txt")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(rc)
	if closeErr := rc.Close(); err != nil || closeErr != nil || string(got) != string(payload) {
		t.Fatalf("legacy entry data %q (%v; close %v)", got, err, closeErr)
	}
}

func TestBrowseTARReadOnlyAndNested(t *testing.T) {
	root := t.TempDir()
	plain := tarBytes(t, basicTar)
	archivePath := writeFile(t, filepath.Join(root, "pack.tgz"), compressWith(t, CodecGzip, plain))
	before, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}

	a, err := Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if a.Format != (Info{Format: FormatTAR, Codec: CodecGzip}) || a.Format.Name() != "TAR.GZ" {
		t.Fatalf("format: %+v", a.Format)
	}
	if a.UnsafeCount != 2 || a.Duplicates != 1 || a.Links != 2 || a.UnsafeLinks != 1 {
		t.Fatalf("safety metadata: unsafe=%d duplicates=%d links=%d unsafeLinks=%d", a.UnsafeCount, a.Duplicates, a.Links, a.UnsafeLinks)
	}
	rootList, err := a.List("", fsys.Options{})
	if err != nil || strings.Join(names(rootList.Entries), ",") != "..,implicit,top" {
		t.Fatalf("root listing: %v (%v)", names(rootList.Entries), err)
	}
	deep, err := a.List("implicit/dir", fsys.Options{})
	if err != nil || strings.Join(names(deep.Entries), ",") != "..,file.txt" {
		t.Fatalf("nested listing: %v (%v)", names(deep.Entries), err)
	}
	file, ok := a.Lookup("top/a.txt")
	if !ok || file.Size != uint64(len("alpha")) || file.Modified.IsZero() || file.Format != FormatTAR {
		t.Fatalf("file metadata: %+v, found=%v", file, ok)
	}
	folder, ok := a.Lookup("implicit")
	if !ok || folder.MethodName() != "—" {
		t.Fatalf("directory metadata label: %+v", folder)
	}
	link, ok := a.Lookup("top/link")
	if !ok || !link.Symlink || !link.UnsafeLink || link.LinkTarget != "/etc/passwd" || link.MethodName() != "link (not opened)" {
		t.Fatalf("link metadata: %+v, found=%v", link, ok)
	}
	if _, _, err := a.OpenEntry("top/link"); !errors.Is(err, ErrSymlink) {
		t.Fatalf("link open: %v", err)
	}
	rc, entry, err := a.OpenEntry("top/a.txt")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(rc)
	closeErr := rc.Close()
	if err != nil || closeErr != nil || string(data) != "alpha" || entry.Name != "a.txt" {
		t.Fatalf("stream entry %q (%v, close %v)", data, err, closeErr)
	}
	after, err := os.ReadDir(root)
	if err != nil || len(before) != len(after) {
		t.Fatalf("browsing wrote or removed files: %v (%v)", after, err)
	}
}

func TestBrowseTARRejectsMalformedAndDecompressionErrors(t *testing.T) {
	root := t.TempDir()
	valid := tarBytes(t, []tarItem{{name: "folder/file.txt", typ: tar.TypeReg, body: "payload"}})
	truncated := writeFile(t, filepath.Join(root, "truncated.tar"), valid[:len(valid)-1024])
	if _, err := Open(truncated); err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("missing end marker: %v", err)
	}

	gz := compressWith(t, CodecGzip, valid)
	gz[len(gz)-8] ^= 0xff // keep the TAR intact but corrupt the gzip checksum
	badCompressed := writeFile(t, filepath.Join(root, "checksum.tar.gz"), gz)
	if _, err := Open(badCompressed); err == nil || !strings.Contains(err.Error(), "damaged") {
		t.Fatalf("bad decompressor checksum: %v", err)
	}

	short := writeFile(t, filepath.Join(root, "short.tar.gz"), []byte{0x1f, 0x8b, 0x08})
	info, err := Detect(short)
	if err != nil || info.Format != FormatTAR || info.Codec != CodecGzip {
		t.Fatalf("extension fallback for damaged compressed TAR: %+v, %v", info, err)
	}
	if _, err := Open(short); err == nil {
		t.Fatal("damaged compressed stream opened as a browsable archive")
	}

	// A valid compressor around an invalid TAR header still fails as TAR,
	// rather than being offered as a single compressed file.
	badHeader := compressWith(t, CodecGzip, bytes.Repeat([]byte{'x'}, 1024))
	badTar := writeFile(t, filepath.Join(root, "bad-header.tar.gz"), badHeader)
	info, err = Detect(badTar)
	if err != nil || info.Format != FormatTAR {
		t.Fatalf("TAR extension hint lost for malformed header: %+v, %v", info, err)
	}
	if _, err := Open(badTar); err == nil {
		t.Fatal("malformed TAR header opened")
	}
}

func TestTARBrowseSupportedCompressedExtensions(t *testing.T) {
	root := t.TempDir()
	plain := tarBytes(t, []tarItem{{name: "entry.txt", typ: tar.TypeReg, body: "x"}})
	for _, tc := range []struct {
		name  string
		codec Codec
	}{
		{"pack.tar", CodecNone}, {"pack.tar.gz", CodecGzip}, {"pack.tgz", CodecGzip},
		{"pack.tar.bz2", CodecBzip2}, {"pack.tbz2", CodecBzip2}, {"pack.tar.xz", CodecXz},
		{"pack.txz", CodecXz}, {"pack.tar.zst", CodecZstd}, {"pack.tzst", CodecZstd},
		{"pack.tar.lz4", CodecLz4},
	} {
		p := writeFile(t, filepath.Join(root, tc.name), compressWith(t, tc.codec, plain))
		a, err := Open(p)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if _, ok := a.Lookup("entry.txt"); !ok {
			t.Errorf("%s: entry missing", tc.name)
		}
	}
}

func TestSelectiveTARExtractionRejectsUnsafeMembersAndLinks(t *testing.T) {
	root := t.TempDir()
	p := writeFile(t, filepath.Join(root, "selection.tar"), tarBytes(t, basicTar))
	dest := filepath.Join(root, "selected")

	bad := ExtractAny(context.Background(), ExtractRequest{Archive: p, DestDir: dest, Members: []string{"../evil.txt"}}, ops.Hooks{})
	if bad.Fatal == nil || !strings.Contains(bad.Fatal.Error(), "unsafe archive member") {
		t.Fatalf("unsafe selected path was accepted: %+v", bad)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("invalid selection touched destination: %v", err)
	}

	link := ExtractAny(context.Background(), ExtractRequest{Archive: p, DestDir: dest, Members: []string{"top/link"}}, ops.Hooks{})
	if !link.OK() || link.Links != 1 || link.UnsafeLinks != 1 || link.Files != 0 {
		t.Fatalf("unsafe link was not safely skipped: %+v", link)
	}
	if _, err := os.Lstat(filepath.Join(root, "etc", "passwd")); !os.IsNotExist(err) {
		t.Fatalf("archive link was followed: %v", err)
	}

	selected := ExtractAny(context.Background(), ExtractRequest{Archive: p, DestDir: dest, Members: []string{"top/a.txt"}}, ops.Hooks{})
	if !selected.OK() || selected.Files != 1 || selected.Duplicates != 1 {
		t.Fatalf("selected duplicate path: %+v", selected)
	}
	if b, err := os.ReadFile(filepath.Join(dest, "top", "a.txt")); err != nil || string(b) != "alpha" {
		t.Fatalf("first duplicate was not kept: %q (%v)", b, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "top", "run.sh")); !os.IsNotExist(err) {
		t.Fatalf("unselected member was extracted: %v", err)
	}
}
