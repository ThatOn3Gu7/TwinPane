// Package opener decides how a file is opened: its type (from the name, or
// from its first bytes when the name says nothing), which external opener
// the platform offers (termux-open in Termux, xdg-open in a graphical Linux
// session, open on macOS), and the alternatives offered by "Open with".
// It also launches external openers (Launch) without a shell: the file is
// always passed as one argument, as an absolute path.
package opener

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ThatOn3Gu7/TwinPane/internal/textview"
)

// Kind is a broad file type.
type Kind int

const (
	Unknown Kind = iota
	Text
	Image
	Audio
	Video
	PDF
	Document
	APK
	Archive
	Program
)

var kindNames = [...]string{"file", "text", "image", "audio", "video", "PDF", "document", "Android package", "archive", "program"}

func (k Kind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return "file"
}

// Media reports types that a dedicated app shows better than a terminal:
// Enter opens them with the external default app where there is one.
func (k Kind) Media() bool {
	switch k {
	case Image, Audio, Video, PDF, Document:
		return true
	}
	return false
}

// Type is a detected file type.
type Type struct {
	Kind Kind
	MIME string // "" when unknown
	// Sniffed is true when the type came from the file's contents.
	Sniffed bool
}

type extInfo struct {
	kind Kind
	mime string
}

// byExt maps lower-case extensions (without the dot) to types.
var byExt = map[string]extInfo{}

func reg(kind Kind, mime string, exts ...string) {
	for _, e := range exts {
		byExt[e] = extInfo{kind, mime}
	}
}

func init() {
	reg(Image, "image/jpeg", "jpg", "jpeg", "jfif")
	reg(Image, "image/png", "png")
	reg(Image, "image/gif", "gif")
	reg(Image, "image/webp", "webp")
	reg(Image, "image/bmp", "bmp")
	reg(Image, "image/svg+xml", "svg")
	reg(Image, "image/heic", "heic", "heif")
	reg(Image, "image/avif", "avif")
	reg(Image, "image/tiff", "tif", "tiff")
	reg(Image, "image/x-icon", "ico")
	reg(Audio, "audio/mpeg", "mp3")
	reg(Audio, "audio/mp4", "m4a", "aac")
	reg(Audio, "audio/ogg", "ogg", "oga", "opus")
	reg(Audio, "audio/flac", "flac")
	reg(Audio, "audio/wav", "wav")
	reg(Audio, "audio/amr", "amr")
	reg(Audio, "audio/midi", "mid", "midi")
	reg(Audio, "audio/x-ms-wma", "wma")
	reg(Video, "video/mp4", "mp4", "m4v")
	reg(Video, "video/x-matroska", "mkv")
	reg(Video, "video/webm", "webm")
	reg(Video, "video/x-msvideo", "avi")
	reg(Video, "video/quicktime", "mov")
	reg(Video, "video/3gpp", "3gp")
	reg(Video, "video/mpeg", "mpeg", "mpg")
	reg(Video, "video/x-ms-wmv", "wmv")
	reg(Video, "video/x-flv", "flv")
	reg(PDF, "application/pdf", "pdf")
	reg(Document, "application/msword", "doc")
	reg(Document, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "docx")
	reg(Document, "application/vnd.ms-excel", "xls")
	reg(Document, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "xlsx")
	reg(Document, "application/vnd.ms-powerpoint", "ppt")
	reg(Document, "application/vnd.openxmlformats-officedocument.presentationml.presentation", "pptx")
	reg(Document, "application/vnd.oasis.opendocument.text", "odt")
	reg(Document, "application/vnd.oasis.opendocument.spreadsheet", "ods")
	reg(Document, "application/vnd.oasis.opendocument.presentation", "odp")
	reg(Document, "application/rtf", "rtf")
	reg(Document, "application/epub+zip", "epub")
	reg(APK, "application/vnd.android.package-archive", "apk")
	reg(Archive, "application/zip", "zip", "jar", "aar", "whl", "cbz", "xpi")
	reg(Archive, "application/x-7z-compressed", "7z")
	reg(Archive, "application/vnd.rar", "rar")
	reg(Archive, "application/x-tar", "tar")
	reg(Archive, "application/gzip", "gz", "tgz")
	reg(Archive, "application/x-xz", "xz", "txz")
	reg(Archive, "application/zstd", "zst", "tzst")
	reg(Archive, "application/x-bzip2", "bz2", "tbz2")
	reg(Archive, "application/x-lz4", "lz4")
	reg(Text, "text/plain", "txt", "text", "log", "md", "markdown", "rst", "org", "csv", "tsv",
		"ini", "conf", "cfg", "toml", "yaml", "yml", "json", "jsonc", "xml", "srt", "vtt", "tex",
		"go", "mod", "sum", "c", "h", "cc", "cpp", "hpp", "rs", "java", "kt", "kts", "py", "rb", "pl",
		"php", "lua", "js", "mjs", "cjs", "jsx", "tsx", "css", "scss", "sh", "bash", "zsh", "fish",
		"sql", "diff", "patch", "gradle", "properties", "mk", "cmake", "vim", "el", "swift", "dart", "nix")
	reg(Text, "text/html", "html", "htm")
}

// ambiguous extensions are decided by the contents (".ts": TypeScript or
// an MPEG transport stream).
var ambiguous = map[string]extInfo{"ts": {Video, "video/mp2t"}}

// textNames are files without a telling extension that are text.
var textNames = map[string]bool{"makefile": true, "dockerfile": true, "readme": true, "license": true,
	"copying": true, "changelog": true, ".bashrc": true, ".zshrc": true, ".profile": true, ".gitignore": true}

// ByName returns the type implied by a file name alone (Unknown when the
// name says nothing).
func ByName(name string) Type {
	base := strings.ToLower(filepath.Base(name))
	if textNames[base] {
		return Type{Kind: Text, MIME: "text/plain"}
	}
	ext := strings.TrimPrefix(filepath.Ext(base), ".")
	if ext == "" || ext == base {
		return Type{}
	}
	if in, ok := byExt[ext]; ok {
		return Type{Kind: in.kind, MIME: in.mime}
	}
	return Type{}
}

// SniffLen is how many bytes Sniff looks at.
const SniffLen = 4096

// Sniff returns the type implied by a file's first bytes.
func Sniff(head []byte) Type {
	has := func(off int, sig string) bool {
		return len(head) >= off+len(sig) && string(head[off:off+len(sig)]) == sig
	}
	t := func(k Kind, mime string) Type { return Type{Kind: k, MIME: mime, Sniffed: true} }
	switch {
	case len(head) == 0:
		return Type{Kind: Text, MIME: "text/plain", Sniffed: true} // an empty file
	case has(0, "%PDF-"):
		return t(PDF, "application/pdf")
	case has(0, "\x89PNG\r\n\x1a\n"):
		return t(Image, "image/png")
	case has(0, "\xff\xd8\xff"):
		return t(Image, "image/jpeg")
	case has(0, "GIF87a"), has(0, "GIF89a"):
		return t(Image, "image/gif")
	case has(0, "RIFF") && has(8, "WEBP"):
		return t(Image, "image/webp")
	case has(0, "RIFF") && has(8, "WAVE"):
		return t(Audio, "audio/wav")
	case has(0, "RIFF") && has(8, "AVI "):
		return t(Video, "video/x-msvideo")
	case has(4, "ftyp"):
		brand := ""
		if len(head) >= 12 {
			brand = string(head[8:12])
		}
		switch brand {
		case "M4A ", "M4B ", "M4P ":
			return t(Audio, "audio/mp4")
		case "heic", "heix", "mif1", "msf1", "hevc":
			return t(Image, "image/heic")
		case "avif":
			return t(Image, "image/avif")
		case "3gp4", "3gp5", "3gp6", "3g2a":
			return t(Video, "video/3gpp")
		}
		return t(Video, "video/mp4")
	case has(0, "\x1a\x45\xdf\xa3"):
		return t(Video, "video/x-matroska")
	case has(0, "OggS"):
		return t(Audio, "audio/ogg")
	case has(0, "fLaC"):
		return t(Audio, "audio/flac")
	case has(0, "ID3"), len(head) >= 2 && head[0] == 0xff && head[1]&0xe0 == 0xe0 && head[1]&0x06 != 0:
		return t(Audio, "audio/mpeg")
	case has(0, "#!AMR"):
		return t(Audio, "audio/amr")
	case has(0, "PK\x03\x04"), has(0, "PK\x05\x06"):
		return t(Archive, "application/zip")
	case has(0, "7z\xbc\xaf\x27\x1c"):
		return t(Archive, "application/x-7z-compressed")
	case has(0, "Rar!\x1a\x07"):
		return t(Archive, "application/vnd.rar")
	case has(0, "\x1f\x8b"):
		return t(Archive, "application/gzip")
	case has(0, "\xfd7zXZ\x00"):
		return t(Archive, "application/x-xz")
	case has(0, "\x28\xb5\x2f\xfd"):
		return t(Archive, "application/zstd")
	case has(0, "BZh") && len(head) > 3 && head[3] >= '1' && head[3] <= '9':
		return t(Archive, "application/x-bzip2")
	case has(0, "\x04\x22\x4d\x18"):
		return t(Archive, "application/x-lz4")
	case has(257, "ustar"):
		return t(Archive, "application/x-tar")
	case has(0, "\x7fELF"):
		return t(Program, "application/x-executable")
	case has(0, "<svg"), has(0, "<?xml") && bytes.Contains(head, []byte("<svg")):
		return t(Image, "image/svg+xml")
	}
	if bin, _ := textview.Classify(head, len(head) >= SniffLen); !bin {
		return t(Text, "text/plain")
	}
	return t(Unknown, "")
}

// Detect returns the type of the file at path: from its name when the
// name is telling, otherwise (and for ambiguous extensions) from its first
// bytes. Unreadable files are Unknown.
func Detect(path string) Type {
	t := ByName(path)
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	amb, isAmb := ambiguous[ext]
	if t.Kind != Unknown && !isAmb {
		return t
	}
	f, err := os.Open(path)
	if err != nil {
		return t
	}
	defer f.Close()
	head := make([]byte, SniffLen)
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return t
	}
	s := Sniff(head[:n])
	if isAmb {
		if s.Kind == Text {
			return Type{Kind: Text, MIME: "text/plain", Sniffed: true}
		}
		return Type{Kind: amb.kind, MIME: amb.mime, Sniffed: true}
	}
	return s
}
