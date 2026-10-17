// Package compress creates compressed archives and compressed single files
// in the formats TwinPane offers (ZIP, 7Z, TAR and compressed TAR, and the
// single-file formats GZIP, XZ, ZSTD, BZIP2 and LZ4).
//
// Every format except 7Z is written by pure-Go code that is part of the
// binary, so it is always available. 7Z has no maintained pure-Go writer:
// it is created with an installed 7-Zip program (7zz, 7z, 7za or 7zr) when
// one is found on $PATH, and reported as unavailable (with the package to
// install) otherwise. A format is never replaced by another one.
//
// The package knows nothing about the user interface: callers detect what
// is available (Detect), check it against a selection (Capability.Check)
// and create an archive (Create), which reports progress through ops.Hooks.
package compress

import (
	"strings"
)

// ID identifies a format.
type ID string

// The formats, in the order they are offered.
const (
	ZIP    ID = "zip"
	SevenZ ID = "7z"
	TAR    ID = "tar"
	TGZ    ID = "tar.gz"
	TGZ2   ID = "tgz" // TAR.GZ with the short .tgz extension
	TXZ    ID = "tar.xz"
	TZST   ID = "tar.zst"
	TBZ2   ID = "tar.bz2"
	TLZ4   ID = "tar.lz4"
	GZ     ID = "gz"
	XZ     ID = "xz"
	ZST    ID = "zst"
	BZ2    ID = "bz2"
	LZ4    ID = "lz4"
)

// Kind tells what a format can hold.
type Kind int

const (
	// Archive formats hold any number of files and folders.
	Archive Kind = iota
	// Single formats compress exactly one regular file.
	Single
)

// Codec is the compression applied to a stream (TAR or a single file).
type Codec int

const (
	None Codec = iota
	Gzip
	Xz
	Zstd
	Bzip2
	Lz4
)

// Level is one compression level a format offers.
type Level struct {
	Name  string // shown to the user: "fastest", "normal", "best", ...
	Value int    // backend-specific value
}

// Format describes one output format.
type Format struct {
	ID    ID
	Name  string // shown to the user: "TAR.GZ"
	Ext   string // file extension including the dot: ".tar.gz"
	Kind  Kind
	Codec Codec // for TAR and single-file formats
	// Levels are the compression levels the backend supports (none: the
	// format has a single setting). DefaultLevel indexes Levels.
	Levels       []Level
	DefaultLevel int
	// Tools are the external programs that can create the format, in
	// order of preference; empty for built-in formats.
	Tools []string
	// About is a short description shown in the dialog.
	About string
}

// External reports whether the format needs an installed program.
func (f Format) External() bool { return len(f.Tools) > 0 }

// HasLevels reports whether a level can be chosen.
func (f Format) HasLevels() bool { return len(f.Levels) > 1 }

// LevelIndex returns a valid index into Levels for i (DefaultLevel when i
// is out of range, e.g. DefaultLevelIndex).
func (f Format) LevelIndex(i int) int {
	if i < 0 || i >= len(f.Levels) {
		return f.DefaultLevel
	}
	return i
}

// DefaultLevelIndex asks for the format's default level.
const DefaultLevelIndex = -1

var (
	deflateLevels = []Level{{"fastest", 1}, {"fast", 3}, {"normal", 6}, {"best", 9}}
	sevenLevels   = []Level{{"store (no compression)", 0}, {"fastest", 1}, {"fast", 3}, {"normal", 5}, {"maximum", 7}, {"ultra", 9}}
	zstdLevels    = []Level{{"fastest", 1}, {"normal", 2}, {"better", 3}, {"best", 4}} // zstd.EncoderLevel values
	bzip2Levels   = []Level{{"fastest", 1}, {"normal", 6}, {"best", 9}}
	lz4Levels     = []Level{{"fast", 0}, {"high", 5}, {"maximum", 9}} // 0 = lz4.Fast, else lz4.LevelN
)

// table lists every format in the order offered to the user.
var table = []Format{
	{ID: ZIP, Name: "ZIP", Ext: ".zip", Kind: Archive, Levels: deflateLevels, DefaultLevel: 2,
		About: "opens everywhere · TwinPane can browse and extract it"},
	{ID: SevenZ, Name: "7Z", Ext: ".7z", Kind: Archive, Levels: sevenLevels, DefaultLevel: 3,
		Tools: []string{"7zz", "7z", "7za", "7zr"},
		About: "high compression · made by the installed 7-Zip program"},
	{ID: TAR, Name: "TAR", Ext: ".tar", Kind: Archive, Codec: None,
		About: "no compression · keeps folders, links and permissions"},
	{ID: TGZ, Name: "TAR.GZ", Ext: ".tar.gz", Kind: Archive, Codec: Gzip, Levels: deflateLevels, DefaultLevel: 2,
		About: "TAR + gzip · the common Linux archive"},
	{ID: TGZ2, Name: "TGZ", Ext: ".tgz", Kind: Archive, Codec: Gzip, Levels: deflateLevels, DefaultLevel: 2,
		About: "TAR.GZ with the short .tgz extension"},
	{ID: TXZ, Name: "TAR.XZ", Ext: ".tar.xz", Kind: Archive, Codec: Xz,
		About: "TAR + xz · small, slower (single setting)"},
	{ID: TZST, Name: "TAR.ZST", Ext: ".tar.zst", Kind: Archive, Codec: Zstd, Levels: zstdLevels, DefaultLevel: 1,
		About: "TAR + Zstandard · fast with good compression"},
	{ID: TBZ2, Name: "TAR.BZ2", Ext: ".tar.bz2", Kind: Archive, Codec: Bzip2, Levels: bzip2Levels, DefaultLevel: 2,
		About: "TAR + bzip2 · older, slow"},
	{ID: TLZ4, Name: "TAR.LZ4", Ext: ".tar.lz4", Kind: Archive, Codec: Lz4, Levels: lz4Levels, DefaultLevel: 0,
		About: "TAR + LZ4 · very fast, larger files"},
	{ID: GZ, Name: "GZIP", Ext: ".gz", Kind: Single, Codec: Gzip, Levels: deflateLevels, DefaultLevel: 2,
		About: "compresses one file"},
	{ID: XZ, Name: "XZ", Ext: ".xz", Kind: Single, Codec: Xz,
		About: "compresses one file (single setting)"},
	{ID: ZST, Name: "ZSTD", Ext: ".zst", Kind: Single, Codec: Zstd, Levels: zstdLevels, DefaultLevel: 1,
		About: "compresses one file"},
	{ID: BZ2, Name: "BZIP2", Ext: ".bz2", Kind: Single, Codec: Bzip2, Levels: bzip2Levels, DefaultLevel: 2,
		About: "compresses one file"},
	{ID: LZ4, Name: "LZ4", Ext: ".lz4", Kind: Single, Codec: Lz4, Levels: lz4Levels, DefaultLevel: 0,
		About: "compresses one file"},
}

// Formats returns every format in the order offered.
func Formats() []Format {
	out := make([]Format, len(table))
	copy(out, table)
	return out
}

// Lookup returns the format with the given ID.
func Lookup(id ID) (Format, bool) {
	for _, f := range table {
		if f.ID == id {
			return f, true
		}
	}
	return Format{}, false
}

// HasExt reports whether name ends with f's extension (case-insensitive).
func (f Format) HasExt(name string) bool {
	n := strings.ToLower(name)
	return strings.HasSuffix(n, f.Ext) && len(n) > len(f.Ext)
}

// MatchExt returns the format whose extension is the longest suffix of
// name, if any (".tar.gz" wins over ".gz").
func MatchExt(name string) (Format, bool) {
	n := strings.ToLower(name)
	best, found := Format{}, false
	for _, f := range table {
		if strings.HasSuffix(n, f.Ext) && len(n) > len(f.Ext) && len(f.Ext) > len(best.Ext) {
			best, found = f, true
		}
	}
	return best, found
}

// TrimExt removes a known archive extension from name ("a.tar.gz" → "a").
func TrimExt(name string) string {
	if f, ok := MatchExt(name); ok {
		return name[:len(name)-len(f.Ext)]
	}
	return name
}

// CheckName verifies that name carries f's extension. A name ending in a
// different format's extension is refused (the file would be mislabelled);
// a name without one gets f's extension appended.
func CheckName(f Format, name string) (string, error) {
	if f.HasExt(name) {
		return name, nil
	}
	if g, ok := MatchExt(name); ok {
		return "", &ExtError{Name: name, Ext: g.Ext, Want: f}
	}
	return name + f.Ext, nil
}

// ExtError reports a file name whose extension belongs to another format.
type ExtError struct {
	Name string
	Ext  string
	Want Format
}

func (e *ExtError) Error() string {
	return "the name ends in " + e.Ext + " but the format is " + e.Want.Name + " (" + e.Want.Ext + "): change the name or the format"
}
