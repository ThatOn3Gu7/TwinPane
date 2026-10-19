package archive

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	dsbzip2 "github.com/dsnet/compress/bzip2"
	"github.com/klauspost/compress/zstd"
	"github.com/pierrec/lz4/v4"
	"github.com/ulikunitz/xz"
)

// Archive formats are recognised primarily from file content. A recognised
// TAR-family extension is retained as a fallback when a compressed stream has
// an invalid/short TAR header, so browsing reports the damaged archive instead
// of treating it as an unrelated single compressed file.

// Format is a recognised extractable format.
type Format int

const (
	FormatUnknown Format = iota
	FormatZIP
	FormatTAR // plain or compressed TAR (see Codec)
	FormatSingle
	Format7z
	FormatRAR
)

// Codec is the compression around a TAR archive or a single file.
type Codec int

const (
	CodecNone Codec = iota
	CodecGzip
	CodecBzip2
	CodecXz
	CodecZstd
	CodecLz4
)

var codecNames = map[Codec]string{CodecNone: "", CodecGzip: "GZIP", CodecBzip2: "BZIP2", CodecXz: "XZ", CodecZstd: "ZSTD", CodecLz4: "LZ4"}

// codecExts are the file extensions of each codec, longest first.
var codecExts = map[Codec][]string{
	CodecGzip:  {".gz", ".gzip"},
	CodecBzip2: {".bz2", ".bzip2"},
	CodecXz:    {".xz"},
	CodecZstd:  {".zst", ".zstd"},
	CodecLz4:   {".lz4"},
}

// Info describes a file's format.
type Info struct {
	Format Format
	Codec  Codec
}

// Name is the format's display name: "ZIP", "TAR.GZ", "GZIP", "7Z", "RAR".
func (i Info) Name() string {
	switch i.Format {
	case FormatZIP:
		return "ZIP"
	case FormatTAR:
		if i.Codec == CodecNone {
			return "TAR"
		}
		n := codecNames[i.Codec]
		if i.Codec == CodecGzip {
			n = "GZ"
		} else if i.Codec == CodecBzip2 {
			n = "BZ2"
		} else if i.Codec == CodecZstd {
			n = "ZST"
		}
		return "TAR." + n
	case FormatSingle:
		return codecNames[i.Codec]
	case Format7z:
		return "7Z"
	case FormatRAR:
		return "RAR"
	}
	return "unknown"
}

// External reports whether the format needs an installed 7-Zip.
func (i Info) External() bool { return i.Format == Format7z || i.Format == FormatRAR }

// Single reports a single compressed file (decompressed to one file).
func (i Info) Single() bool { return i.Format == FormatSingle }

// Supported lists the formats for messages.
const Supported = "ZIP and TAR (TAR.GZ/TGZ, TAR.BZ2/TBZ2, TAR.XZ/TXZ, TAR.ZST/TZST, TAR.LZ4); GZ, BZ2, XZ, ZST, LZ4; 7Z and RAR with 7-Zip"

// ErrNotArchive is returned for files in no supported format.
var ErrNotArchive = errors.New("not an archive or compressed file TwinPane can extract (supported: " + Supported + ")")

var (
	magicGzip  = []byte{0x1f, 0x8b}
	magicBzip2 = []byte("BZh")
	magicXz    = []byte{0xfd, '7', 'z', 'X', 'Z', 0x00}
	magicZstd  = []byte{0x28, 0xb5, 0x2f, 0xfd}
	magicLz4   = []byte{0x04, 0x22, 0x4d, 0x18}
	magic7z    = []byte{'7', 'z', 0xbc, 0xaf, 0x27, 0x1c}
	magicRar   = []byte("Rar!\x1a\x07")
)

// Detect recognises the format of the file at p. Only regular files are
// read, so FIFOs and devices can never block. A file in no supported
// format gives FormatUnknown and a nil error.
func Detect(p string) (Info, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return Info{}, err
	}
	if !fi.Mode().IsRegular() {
		return Info{}, nil
	}
	if Sniff(p) {
		return Info{Format: FormatZIP}, nil
	}
	f, err := os.Open(p)
	if err != nil {
		return Info{}, err
	}
	defer f.Close()
	head := make([]byte, 512)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return Info{}, err
	}
	head = head[:n]
	switch {
	case bytes.HasPrefix(head, magic7z):
		return Info{Format: Format7z}, nil
	case bytes.HasPrefix(head, magicRar):
		return Info{Format: FormatRAR}, nil
	case isTarHeader(head):
		return Info{Format: FormatTAR}, nil
	}
	codec := CodecNone
	switch {
	case bytes.HasPrefix(head, magicGzip):
		codec = CodecGzip
	case bytes.HasPrefix(head, magicBzip2) && len(head) > 3 && head[3] >= '1' && head[3] <= '9':
		codec = CodecBzip2
	case bytes.HasPrefix(head, magicXz):
		codec = CodecXz
	case bytes.HasPrefix(head, magicZstd):
		codec = CodecZstd
	case bytes.HasPrefix(head, magicLz4):
		codec = CodecLz4
	default:
		if hint, ok := tarExtensionHint(p); ok {
			return hint, nil
		}
		return Info{}, nil
	}
	// A compressed TAR or a single compressed file? Peek at the start of
	// the decompressed data. For damaged TAR streams, keep a matching
	// extension hint so the archive reader can return a useful corruption
	// error instead of treating the bytes as a single compressed file.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return Info{}, err
	}
	r, closeFn, err := decompressor(codec, f)
	if err != nil {
		if hint, ok := tarExtensionHint(p); ok && hint.Codec == codec {
			return hint, nil
		}
		return Info{Format: FormatSingle, Codec: codec}, nil
	}
	defer closeFn()
	inner := make([]byte, 512)
	k, _ := io.ReadFull(r, inner)
	if isTarHeader(inner[:k]) {
		return Info{Format: FormatTAR, Codec: codec}, nil
	}
	if hint, ok := tarExtensionHint(p); ok && hint.Codec == codec {
		return hint, nil
	}
	return Info{Format: FormatSingle, Codec: codec}, nil
}

// tarExtensionHint returns the TAR codec implied by a recognised TAR-family
// extension. It is a fallback when content inspection cannot identify a valid
// TAR header, allowing damaged named archives to fail with a corruption error.
func tarExtensionHint(name string) (Info, bool) {
	n := strings.ToLower(name)
	for _, x := range []struct {
		ext   string
		codec Codec
	}{
		{".tar.gzip", CodecGzip}, {".tar.bzip2", CodecBzip2}, {".tar.zstd", CodecZstd},
		{".tar.gz", CodecGzip}, {".tgz", CodecGzip},
		{".tar.bz2", CodecBzip2}, {".tbz2", CodecBzip2}, {".tbz", CodecBzip2},
		{".tar.xz", CodecXz}, {".txz", CodecXz},
		{".tar.zst", CodecZstd}, {".tzst", CodecZstd},
		{".tar.lz4", CodecLz4}, {".tar", CodecNone},
	} {
		if strings.HasSuffix(n, x.ext) && len(n) > len(x.ext) {
			return Info{Format: FormatTAR, Codec: x.codec}, true
		}
	}
	return Info{}, false
}

// isTarHeader reports whether b starts with a TAR header: the "ustar"
// magic (POSIX and GNU), or a pre-POSIX header with a valid checksum.
func isTarHeader(b []byte) bool {
	if len(b) < 512 {
		return false
	}
	if bytes.Equal(b[257:262], []byte("ustar")) {
		return true
	}
	// v7 TAR: the checksum field (148..156) is the octal sum of the header
	// bytes with the field itself counted as spaces
	field := strings.TrimRight(strings.TrimLeft(string(b[148:156]), " "), " \x00")
	if field == "" || b[0] == 0 {
		return false
	}
	var want int64
	if _, err := fmt.Sscanf(field, "%o", &want); err != nil {
		return false
	}
	var sum int64
	for i, c := range b[:512] {
		if i >= 148 && i < 156 {
			c = ' '
		}
		sum += int64(c)
	}
	return sum == want
}

// decompressor wraps r in the codec's reader.
func decompressor(c Codec, r io.Reader) (io.Reader, func(), error) {
	nop := func() {}
	switch c {
	case CodecNone:
		return r, nop, nil
	case CodecGzip:
		zr, err := gzip.NewReader(r)
		if err != nil {
			return nil, nop, err
		}
		return zr, func() { zr.Close() }, nil
	case CodecBzip2:
		zr, err := dsbzip2.NewReader(r, nil)
		if err != nil {
			return nil, nop, err
		}
		return zr, func() { zr.Close() }, nil
	case CodecXz:
		zr, err := xz.NewReader(r)
		if err != nil {
			return nil, nop, err
		}
		return zr, nop, nil
	case CodecZstd:
		zr, err := zstd.NewReader(r, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(256<<20), zstd.WithDecoderMaxWindow(256<<20))
		if err != nil {
			return nil, nop, err
		}
		return zr, zr.Close, nil
	case CodecLz4:
		return lz4.NewReader(r), nop, nil
	}
	return nil, nop, fmt.Errorf("unknown compression")
}

// SingleOutputName is the name a single compressed file decompresses to:
// the name without the codec's extension ("notes.txt.gz" → "notes.txt").
// A name without that extension gets ".out" appended rather than a guessed
// extension.
func SingleOutputName(name string, c Codec) string {
	lower := strings.ToLower(name)
	for _, ext := range codecExts[c] {
		if strings.HasSuffix(lower, ext) && len(name) > len(ext) {
			return name[:len(name)-len(ext)]
		}
	}
	return name + ".out"
}

// FolderName is the default folder an archive extracts into: the name
// without its archive extension ("photos.tar.gz" → "photos").
func FolderName(name string) string {
	lower := strings.ToLower(name)
	for _, ext := range []string{".tar.gzip", ".tar.bzip2", ".tar.zstd", ".tar.gz", ".tar.bz2", ".tar.xz", ".tar.zst", ".tar.lz4", ".tgz", ".tbz2", ".tbz", ".txz", ".tzst", ".tar", ".zip", ".7z", ".rar"} {
		if strings.HasSuffix(lower, ext) && len(name) > len(ext) {
			return name[:len(name)-len(ext)]
		}
	}
	if ext := filepath.Ext(name); ext != "" && ext != name {
		return strings.TrimSuffix(name, ext)
	}
	return name + ".extracted"
}
