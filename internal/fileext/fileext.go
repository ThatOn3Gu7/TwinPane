// Package fileext holds the file-extension catalogue used by the "New file"
// dialog: predefined extensions grouped into categories, a search over them,
// and the name/extension rules that decide the name of the file to create.
//
// The catalogue is deliberately organised in groups rather than one long
// list: the dialog shows the groups in order and can filter every extension
// by typing. Any extension that is not listed can be typed as a custom one.
package fileext

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// Extension is one predefined file extension.
type Extension struct {
	Ext  string // with the leading dot, lower case: ".md"
	Name string // human name: "Markdown"
	Desc string // what it is used for: "notes, READMEs, documentation"
}

// Category groups related extensions; the dialog shows the groups in this
// order.
type Category struct {
	Name       string
	Extensions []Extension
}

// maxExtLen is the longest extension accepted from the user (without the
// leading dot), so a crafted name cannot abuse the dialog or the filesystem.
const maxExtLen = 32

// catalog is the predefined extension list. Order inside a category is the
// display order; keep the most common formats first.
var catalog = []Category{
	{Name: "Text & notes", Extensions: []Extension{
		{".txt", "Plain text", "notes, README fragments"},
		{".md", "Markdown", "notes, READMEs, documentation"},
		{".markdown", "Markdown (long)", "same as .md"},
		{".mdown", "Markdown (short)", "same as .md"},
		{".rst", "reStructuredText", "Python documentation"},
		{".adoc", "AsciiDoc", "technical documentation"},
		{".org", "Org mode", "notes and outlines"},
		{".tex", "LaTeX", "typesetting source"},
		{".bib", "BibTeX", "bibliography entries"},
		{".log", "Log file", "program or service output"},
		{".nfo", "Info file", "plain text info screens"},
		{".me", "Read me", "plain text read-me"},
	}},
	{Name: "Documents", Extensions: []Extension{
		{".pdf", "PDF", "portable document"},
		{".doc", "Word (legacy)", "Microsoft Word document"},
		{".docx", "Word", "Microsoft Word document"},
		{".odt", "OpenDocument text", "LibreOffice Writer"},
		{".rtf", "Rich text", "formatted text"},
		{".xls", "Excel (legacy)", "spreadsheet"},
		{".xlsx", "Excel", "spreadsheet"},
		{".ods", "OpenDocument sheet", "LibreOffice Calc"},
		{".ppt", "PowerPoint (legacy)", "presentation"},
		{".pptx", "PowerPoint", "presentation"},
		{".odp", "OpenDocument slides", "LibreOffice Impress"},
		{".epub", "EPUB", "e-book"},
		{".mobi", "Mobi", "e-book (Kindle)"},
		{".djvu", "DjVu", "scanned documents"},
		{".pages", "Pages", "Apple Pages document"},
		{".numbers", "Numbers", "Apple Numbers sheet"},
		{".key", "Keynote", "Apple Keynote presentation"},
	}},
	{Name: "Code", Extensions: []Extension{
		{".go", "Go", "Go source"},
		{".py", "Python", "Python source"},
		{".js", "JavaScript", "JavaScript source"},
		{".mjs", "JavaScript module", "ES module"},
		{".cjs", "CommonJS", "CommonJS module"},
		{".ts", "TypeScript", "TypeScript source"},
		{".jsx", "React JSX", "React components"},
		{".tsx", "React TSX", "React components in TypeScript"},
		{".java", "Java", "Java source"},
		{".kt", "Kotlin", "Kotlin source"},
		{".kts", "Kotlin script", "Kotlin build scripts"},
		{".c", "C", "C source"},
		{".h", "C header", "C declarations"},
		{".cpp", "C++", "C++ source"},
		{".cc", "C++ (cc)", "C++ source"},
		{".hpp", "C++ header", "C++ declarations"},
		{".cs", "C#", "C# source"},
		{".rs", "Rust", "Rust source"},
		{".rb", "Ruby", "Ruby source"},
		{".php", "PHP", "PHP source"},
		{".swift", "Swift", "Swift source"},
		{".m", "Objective-C", "Objective-C source"},
		{".mm", "Objective-C++", "Objective-C++ source"},
		{".scala", "Scala", "Scala source"},
		{".clj", "Clojure", "Clojure source"},
		{".ex", "Elixir", "Elixir source"},
		{".exs", "Elixir script", "Elixir scripts and config"},
		{".erl", "Erlang", "Erlang source"},
		{".hs", "Haskell", "Haskell source"},
		{".lua", "Lua", "Lua source"},
		{".pl", "Perl", "Perl source"},
		{".r", "R", "R source"},
		{".jl", "Julia", "Julia source"},
		{".dart", "Dart", "Dart source"},
		{".groovy", "Groovy", "Groovy source"},
		{".zig", "Zig", "Zig source"},
		{".nim", "Nim", "Nim source"},
		{".d", "D", "D source"},
		{".pas", "Pascal", "Pascal source"},
		{".f90", "Fortran", "Fortran source"},
		{".cob", "COBOL", "COBOL source"},
		{".asm", "Assembly", "assembler source"},
		{".v", "V", "V source"},
		{".tcl", "Tcl", "Tcl source"},
		{".awk", "AWK", "AWK programs"},
		{".vue", "Vue", "Vue single-file component"},
		{".svelte", "Svelte", "Svelte component"},
	}},
	{Name: "Web", Extensions: []Extension{
		{".html", "HTML", "web page"},
		{".htm", "HTML (legacy)", "web page"},
		{".xhtml", "XHTML", "strict XML web page"},
		{".css", "CSS", "stylesheets"},
		{".scss", "SCSS", "Sass stylesheets"},
		{".sass", "Sass", "indented Sass"},
		{".less", "Less", "Less stylesheets"},
		{".map", "Source map", "generated asset maps"},
		{".webmanifest", "Web app manifest", "PWA metadata"},
		{".wasm", "WebAssembly", "compiled modules"},
	}},
	{Name: "Data & config", Extensions: []Extension{
		{".json", "JSON", "structured data"},
		{".jsonl", "JSON Lines", "one JSON value per line"},
		{".ndjson", "NDJSON", "one JSON value per line"},
		{".csv", "CSV", "comma-separated values"},
		{".tsv", "TSV", "tab-separated values"},
		{".xml", "XML", "structured markup"},
		{".yaml", "YAML", "configuration and data"},
		{".yml", "YAML (short)", "same as .yaml"},
		{".toml", "TOML", "configuration"},
		{".ini", "INI", "configuration"},
		{".cfg", "Config", "configuration"},
		{".conf", "Conf", "configuration"},
		{".env", "Environment", "environment variables"},
		{".properties", "Properties", "Java-style key/value"},
		{".plist", "Property list", "Apple configuration"},
		{".hcl", "HCL", "Terraform/HashiCorp config"},
		{".tf", "Terraform", "infrastructure definitions"},
		{".tfvars", "Terraform vars", "Terraform variables"},
		{".lock", "Lock file", "pinned dependency versions"},
	}},
	{Name: "Databases & datasets", Extensions: []Extension{
		{".sql", "SQL", "database queries and schemas"},
		{".db", "Database", "generic database file"},
		{".sqlite", "SQLite", "SQLite database"},
		{".sqlite3", "SQLite 3", "SQLite database"},
		{".mdb", "Access (legacy)", "Microsoft Access database"},
		{".accdb", "Access", "Microsoft Access database"},
		{".dbf", "dBase", "dBase table"},
		{".parquet", "Parquet", "columnar dataset"},
		{".avro", "Avro", "row-based dataset"},
		{".orc", "ORC", "columnar dataset"},
		{".feather", "Feather", "Arrow-based dataset"},
		{".h5", "HDF5", "scientific dataset"},
		{".hdf5", "HDF5 (long)", "scientific dataset"},
		{".pkl", "Pickle", "Python serialised object"},
	}},
	{Name: "Images", Extensions: []Extension{
		{".png", "PNG", "lossless raster image"},
		{".jpg", "JPEG", "photo"},
		{".jpeg", "JPEG (long)", "photo"},
		{".gif", "GIF", "animated or indexed image"},
		{".webp", "WebP", "web image"},
		{".avif", "AVIF", "modern web image"},
		{".bmp", "BMP", "uncompressed bitmap"},
		{".tif", "TIFF", "high-quality raster"},
		{".tiff", "TIFF (long)", "high-quality raster"},
		{".ico", "Icon", "favicon or app icon"},
		{".svg", "SVG", "vector image"},
		{".heic", "HEIC", "Apple photo"},
		{".heif", "HEIF", "photo container"},
		{".psd", "Photoshop", "layered image"},
		{".xcf", "GIMP", "layered image"},
		{".ai", "Illustrator", "vector artwork"},
		{".eps", "EPS", "encapsulated PostScript"},
		{".raw", "RAW photo", "camera raw data"},
		{".cr2", "Canon RAW", "camera raw data"},
		{".nef", "Nikon RAW", "camera raw data"},
	}},
	{Name: "Audio", Extensions: []Extension{
		{".mp3", "MP3", "compressed audio"},
		{".wav", "WAV", "uncompressed audio"},
		{".flac", "FLAC", "lossless audio"},
		{".ogg", "Ogg Vorbis", "compressed audio"},
		{".oga", "Ogg audio", "Ogg audio stream"},
		{".opus", "Opus", "speech and music"},
		{".m4a", "M4A", "AAC audio"},
		{".aac", "AAC", "compressed audio"},
		{".wma", "WMA", "Windows Media audio"},
		{".aif", "AIFF", "uncompressed audio"},
		{".aiff", "AIFF (long)", "uncompressed audio"},
		{".mid", "MIDI", "instrument data"},
		{".midi", "MIDI (long)", "instrument data"},
		{".amr", "AMR", "voice recording"},
		{".ape", "Monkey's Audio", "lossless audio"},
	}},
	{Name: "Video", Extensions: []Extension{
		{".mp4", "MP4", "video"},
		{".mkv", "Matroska", "video container"},
		{".webm", "WebM", "web video"},
		{".mov", "QuickTime", "Apple video"},
		{".avi", "AVI", "legacy video"},
		{".wmv", "WMV", "Windows Media video"},
		{".flv", "Flash video", "legacy web video"},
		{".m4v", "M4V", "Apple video"},
		{".mpg", "MPEG", "legacy video"},
		{".mpeg", "MPEG (long)", "legacy video"},
		{".3gp", "3GP", "phone video"},
		{".ogv", "Ogg video", "Theora video"},
		{".m2ts", "M2TS", "camcorder video"},
	}},
	{Name: "Subtitles", Extensions: []Extension{
		{".srt", "SubRip", "subtitles"},
		{".vtt", "WebVTT", "web subtitles"},
		{".ass", "ASS", "styled subtitles"},
		{".ssa", "SSA", "styled subtitles"},
		{".sub", "MicroDVD", "subtitles"},
		{".lrc", "Lyrics", "timed lyrics"},
	}},
	{Name: "Archives & disk images", Extensions: []Extension{
		{".zip", "ZIP", "compressed archive"},
		{".tar", "TAR", "uncompressed archive"},
		{".gz", "Gzip", "single compressed file"},
		{".tgz", "TAR.GZ", "compressed archive"},
		{".bz2", "Bzip2", "single compressed file"},
		{".tbz2", "TAR.BZ2", "compressed archive"},
		{".xz", "XZ", "single compressed file"},
		{".txz", "TAR.XZ", "compressed archive"},
		{".zst", "Zstandard", "single compressed file"},
		{".lz4", "LZ4", "single compressed file"},
		{".lz", "Lzip", "single compressed file"},
		{".7z", "7-Zip", "compressed archive"},
		{".rar", "RAR", "compressed archive"},
		{".cab", "Cabinet", "Windows archive"},
		{".jar", "Java archive", "packaged Java program"},
		{".war", "Web archive", "packaged Java web app"},
		{".apk", "Android package", "Android app"},
		{".deb", "Debian package", "Debian/Ubuntu package"},
		{".rpm", "RPM package", "Fedora/RHEL package"},
		{".msi", "Windows installer", "MSI package"},
		{".pkg", "macOS package", "installer"},
		{".iso", "ISO image", "optical disc image"},
		{".img", "Disk image", "raw disk image"},
		{".dmg", "Apple disk image", "macOS disk image"},
	}},
	{Name: "Scripts & automation", Extensions: []Extension{
		{".sh", "Shell", "POSIX shell script"},
		{".bash", "Bash", "Bash script"},
		{".zsh", "Zsh", "Zsh script"},
		{".fish", "Fish", "Fish script"},
		{".ps1", "PowerShell", "PowerShell script"},
		{".psm1", "PowerShell module", "PowerShell module"},
		{".bat", "Batch", "Windows batch file"},
		{".cmd", "Command", "Windows command script"},
		{".vbs", "VBScript", "Windows scripting"},
		{".sed", "sed", "stream editor script"},
		{".dockerfile", "Dockerfile", "container build recipe"},
		{".service", "systemd unit", "system service"},
		{".desktop", "Desktop entry", "Linux launcher"},
	}},
	{Name: "Fonts", Extensions: []Extension{
		{".ttf", "TrueType", "font"},
		{".otf", "OpenType", "font"},
		{".woff", "WOFF", "web font"},
		{".woff2", "WOFF2", "web font"},
		{".eot", "Embedded OpenType", "legacy web font"},
		{".fon", "Bitmap font", "legacy Windows font"},
	}},
	{Name: "Certificates & keys", Extensions: []Extension{
		{".pem", "PEM", "certificate or key"},
		{".crt", "Certificate", "X.509 certificate"},
		{".cer", "Certificate (cer)", "X.509 certificate"},
		{".der", "DER", "binary certificate or key"},
		{".pub", "Public key", "public key material"},
		{".csr", "Certificate request", "signing request"},
		{".gpg", "GPG", "encrypted or signed data"},
		{".asc", "ASCII-armored", "armoured PGP data"},
		{".sig", "Signature", "detached signature"},
	}},
	{Name: "Other", Extensions: []Extension{
		{".bin", "Binary", "opaque data"},
		{".dat", "Data", "generic data"},
		{".tmp", "Temporary", "scratch file"},
		{".bak", "Backup", "backup copy"},
		{".old", "Old", "previous version"},
		{".swp", "Swap", "editor swap file"},
		{".torrent", "Torrent", "BitTorrent metadata"},
		{".ics", "iCalendar", "calendar events"},
		{".vcf", "vCard", "contact card"},
		{".sha256", "Checksum", "SHA-256 sum"},
		{".md5", "Checksum (MD5)", "MD5 sum"},
		{".diff", "Diff", "patch fragment"},
		{".patch", "Patch", "patch file"},
	}},
}

// Categories returns the whole catalogue. Callers must not modify it.
func Categories() []Category { return catalog }

// All returns every predefined extension in display order.
func All() []Extension {
	var out []Extension
	for _, c := range catalog {
		out = append(out, c.Extensions...)
	}
	return out
}

// Count returns the number of predefined extensions.
func Count() int {
	n := 0
	for _, c := range catalog {
		n += len(c.Extensions)
	}
	return n
}

// Find returns the predefined extension with this extension (case-insensitive,
// with or without the leading dot).
func Find(ext string) (Extension, bool) {
	want := "." + strings.TrimLeft(strings.TrimSpace(ext), ".")
	for _, e := range All() {
		if strings.EqualFold(e.Ext, want) {
			return e, true
		}
	}
	return Extension{}, false
}

// Match reports whether the extension matches a filter query. The query may be
// typed with or without the leading dot; an empty query matches everything.
func Match(e Extension, query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	q = strings.TrimLeft(q, ".")
	if q == "" {
		return true
	}
	return strings.Contains(strings.ToLower(e.Ext), q) ||
		strings.Contains(strings.ToLower(e.Name), q) ||
		strings.Contains(strings.ToLower(e.Desc), q)
}

func matchCategory(c Category, query string) bool {
	q := strings.ToLower(strings.TrimLeft(strings.TrimSpace(query), "."))
	return q != "" && strings.Contains(strings.ToLower(c.Name), q)
}

// Search filters the catalogue: it keeps only matching extensions, drops empty
// categories and orders the matches inside each category by relevance (exact
// extension first). A query that names a category ("images", "config") keeps
// that whole category.
func Search(query string) []Category {
	q := strings.TrimLeft(strings.ToLower(strings.TrimSpace(query)), ".")
	var out []Category
	for _, c := range catalog {
		var matches []Extension
		whole := matchCategory(c, q)
		for _, e := range c.Extensions {
			if whole || Match(e, q) {
				matches = append(matches, e)
			}
		}
		if len(matches) == 0 {
			continue
		}
		if q != "" && !whole {
			sort.SliceStable(matches, func(i, j int) bool {
				return rank(matches[i], q) < rank(matches[j], q)
			})
		}
		out = append(out, Category{Name: c.Name, Extensions: matches})
	}
	return out
}

// rank orders search hits: an exact extension match first, then extensions
// starting with the query, then those containing it, then name/description
// matches.
func rank(e Extension, q string) int {
	ext := strings.ToLower(strings.TrimPrefix(e.Ext, "."))
	name := strings.ToLower(e.Name)
	switch {
	case ext == q:
		return 0
	case strings.HasPrefix(ext, q):
		return 1
	case strings.Contains(ext, q):
		return 2
	case strings.HasPrefix(name, q):
		return 3
	default:
		return 4
	}
}

// SplitExtension splits a file name into its stem and extension. A leading dot
// (a dotfile such as .bashrc) is part of the name, not an extension, and so is
// a dot at the very end of the name.
func SplitExtension(name string) (stem, ext string) {
	i := strings.LastIndexByte(name, '.')
	if i <= 0 || i == len(name)-1 {
		return name, ""
	}
	return name[:i], name[i:]
}

// FinalName composes the name of the file to create from what the user typed
// and the chosen extension ("" = no extension).
//
// The typed name wins when it already ends with that extension, so "notes.md"
// plus .md stays "notes.md". A different extension is replaced ("notes.txt"
// plus .md gives "notes.md") instead of stacking a second one, and a name
// without an extension gets one ("notes" plus .md gives "notes.md").
func FinalName(name, ext string) string {
	name = strings.TrimSpace(name)
	if ext == "" {
		return name
	}
	name = strings.TrimRight(name, ".")
	if name == "" {
		return ext
	}
	if strings.EqualFold(name, ext) {
		return name // the typed name already is this extension (e.g. ".env")
	}
	stem, have := SplitExtension(name)
	if have != "" && strings.EqualFold(have, ext) {
		return name
	}
	return stem + ext
}

// NormalizeExtension accepts what the user typed as a custom extension
// (".md", "md", " tar.gz ") and returns it in the canonical form (".md",
// ".tar.gz"). An empty value or only dots means "no extension".
func NormalizeExtension(s string) (string, error) {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, ".")
	if s == "" {
		return "", nil
	}
	if len(s) > maxExtLen {
		return "", fmt.Errorf("extension is longer than %d characters", maxExtLen)
	}
	if strings.Contains(s, "..") {
		return "", fmt.Errorf("extension %q has an empty part", s)
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '+' || r == '-' || r == '_' || r == '.' {
			continue
		}
		return "", fmt.Errorf("extension %q: use letters, digits and . + - _ only", s)
	}
	return "." + s, nil
}
