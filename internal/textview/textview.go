// Package textview loads text for TwinPane's built-in read-only viewer. It
// reads at most a bounded amount of data (Options.MaxBytes / MaxLines), so
// arbitrarily large files never have to fit in memory, detects binary data
// so it is never dumped into the terminal, decodes UTF-16 (with BOM),
// replaces invalid UTF-8, and prepares lines for display (tabs, control
// characters, wrapping by terminal cells). It has no UI dependencies.
package textview

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

// Options bounds what is loaded.
type Options struct {
	MaxBytes     int64 // bytes read at most (the rest is reported as truncated)
	MaxLines     int   // lines kept at most
	MaxLineBytes int   // longer lines are cut (and counted in Doc.LongLines)
}

// Defaults are the viewer's limits: 8 MiB, 500 000 lines, 16 KiB per line.
var Defaults = Options{MaxBytes: 8 << 20, MaxLines: 500_000, MaxLineBytes: 16 << 10}

// SampleSize is how much of the start is examined for binary content.
const SampleSize = 8192

// Doc is a loaded text (or the verdict that it is not text).
type Doc struct {
	Lines        []string // valid UTF-8, without line terminators
	Size         int64    // total size if known, else -1
	Read         int64    // bytes read
	Truncated    bool     // only the first part is shown (MaxBytes / MaxLines)
	Binary       bool     // not shown: see BinaryReason
	BinaryReason string
	Encoding     string // "UTF-8", "ASCII", "UTF-8 with BOM", "UTF-16LE", "UTF-16BE"
	Invalid      int    // invalid UTF-8 sequences, shown as U+FFFD
	CRLF         bool   // Windows line endings (the CRs are not shown)
	LongLines    int    // lines cut at MaxLineBytes
}

// Empty reports an empty file.
func (d *Doc) Empty() bool { return d.Read == 0 && !d.Binary }

// ErrNotRegular is returned for folders, devices, pipes and sockets.
var ErrNotRegular = errors.New("not a regular file")

// LoadFile opens path (following symlinks) and loads it. Only regular files
// are opened, so reading can never block on a FIFO or a device.
func LoadFile(path string, opt Options) (*Doc, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if fi.IsDir() {
		return nil, errors.New("it is a folder")
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%w (%s)", ErrNotRegular, fi.Mode().Type())
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return Load(f, fi.Size(), opt)
}

// Load reads at most opt.MaxBytes from r (size = total size if known, else
// -1) and splits it into lines.
func Load(r io.Reader, size int64, opt Options) (*Doc, error) {
	if opt.MaxBytes <= 0 {
		opt = Defaults
	}
	d := &Doc{Size: size, Encoding: "UTF-8"}
	data, err := io.ReadAll(io.LimitReader(r, opt.MaxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > opt.MaxBytes {
		data = data[:opt.MaxBytes]
		d.Truncated = true
	}
	d.Read = int64(len(data))
	if len(data) == 0 {
		return d, nil
	}

	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}):
		data, d.Encoding = decodeUTF16(data[2:], false), "UTF-16LE"
	case bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		data, d.Encoding = decodeUTF16(data[2:], true), "UTF-16BE"
	case bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}):
		data, d.Encoding = data[3:], "UTF-8 with BOM"
	default:
		if bin, why := Classify(data[:min(len(data), SampleSize)], len(data) > SampleSize); bin {
			d.Binary, d.BinaryReason = true, why
			return d, nil
		}
		if isASCII(data) {
			d.Encoding = "ASCII"
		}
	}

	s := string(data)
	for len(s) > 0 {
		if len(d.Lines) >= opt.MaxLines {
			d.Truncated = true
			break
		}
		line := s
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			line, s = s[:i], s[i+1:]
		} else {
			s = ""
		}
		if strings.HasSuffix(line, "\r") {
			line = line[:len(line)-1]
			d.CRLF = true
		}
		if opt.MaxLineBytes > 0 && len(line) > opt.MaxLineBytes {
			cut := opt.MaxLineBytes
			for cut > 0 && !utf8.RuneStart(line[cut]) {
				cut--
			}
			line = line[:cut]
			d.LongLines++
		}
		if !utf8.ValidString(line) {
			d.Invalid += countInvalid(line)
			line = strings.ToValidUTF8(line, "\uFFFD")
		}
		d.Lines = append(d.Lines, line)
	}
	return d, nil
}

// Classify decides from a sample whether data is binary. cut says the
// sample may end in the middle of a character.
func Classify(sample []byte, cut bool) (bool, string) {
	if bytes.IndexByte(sample, 0) >= 0 {
		return true, "it contains NUL bytes (binary data)"
	}
	ctrl, invalid, runes := 0, 0, 0
	for i := 0; i < len(sample); {
		r, n := utf8.DecodeRune(sample[i:])
		if r == utf8.RuneError && n <= 1 {
			if cut && len(sample)-i < utf8.UTFMax {
				break // a character cut at the end of the sample
			}
			invalid++
		} else if r < 0x20 && !strings.ContainsRune("\t\n\r\f\v\b\x1b", r) {
			ctrl++
		}
		runes++
		i += max(n, 1)
	}
	if runes == 0 {
		return false, ""
	}
	if ctrl*10 > runes {
		return true, "it is mostly control characters (binary data)"
	}
	if invalid*10 > runes*3 {
		return true, "it is not UTF-8 text (unsupported encoding or binary data)"
	}
	return false, ""
}

func isASCII(b []byte) bool {
	for _, c := range b {
		if c >= 0x80 {
			return false
		}
	}
	return true
}

func countInvalid(s string) int {
	n := 0
	for i := 0; i < len(s); {
		r, w := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && w <= 1 {
			n++
		}
		i += max(w, 1)
	}
	return n
}

func decodeUTF16(b []byte, bigEndian bool) []byte {
	u := make([]uint16, len(b)/2)
	for i := range u {
		if bigEndian {
			u[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
		} else {
			u[i] = uint16(b[2*i+1])<<8 | uint16(b[2*i])
		}
	}
	return []byte(string(utf16.Decode(u)))
}

// ---- display ---------------------------------------------------------------------

// TabWidth is the tab stop distance used by the viewer.
const TabWidth = 4

// Expand prepares a line for the terminal: tabs become spaces up to the next
// tab stop, and control characters (which could move the cursor or change
// colours) become "·" (or "␛" for ESC). The result contains only printable
// characters.
func Expand(line string) string {
	clean := true
	for i := 0; i < len(line); i++ {
		if c := line[i]; c < 0x20 || c == 0x7f {
			clean = false
			break
		}
	}
	if clean && !hasC1(line) {
		return line
	}
	var b strings.Builder
	col := 0
	for _, r := range line {
		switch {
		case r == '\t':
			n := TabWidth - col%TabWidth
			b.WriteString(strings.Repeat(" ", n))
			col += n
			continue
		case r == 0x1b:
			r = '␛'
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0):
			r = '·'
		}
		b.WriteRune(r)
		col += runewidth.RuneWidth(r)
	}
	return b.String()
}

func hasC1(s string) bool {
	for _, r := range s {
		if r >= 0x80 && r < 0xa0 {
			return true
		}
	}
	return false
}

// Chunks splits an expanded line into pieces of at most width cells (wide
// characters are never split). An empty line yields one empty chunk.
func Chunks(s string, width int) []string {
	if width < 1 {
		width = 1
	}
	if s == "" {
		return []string{""}
	}
	if len(s) <= width && isASCIIString(s) {
		return []string{s}
	}
	var out []string
	start, w := 0, 0
	for i, r := range s {
		rw := runewidth.RuneWidth(r)
		if w+rw > width && i > start {
			out = append(out, s[start:i])
			start, w = i, 0
		}
		w += rw
	}
	return append(out, s[start:])
}

// ChunkCount is len(Chunks(s, width)) without building the pieces.
func ChunkCount(s string, width int) int {
	if width < 1 {
		width = 1
	}
	if s == "" {
		return 1
	}
	if isASCIIString(s) {
		return (len(s) + width - 1) / width
	}
	n, w, first := 1, 0, true
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if w+rw > width && !first {
			n++
			w = 0
		}
		w += rw
		first = false
	}
	return n
}

// Cut returns the cells [from, from+width) of an expanded line (for
// horizontal scrolling without wrapping); wide characters crossing an edge
// become spaces.
func Cut(s string, from, width int) string {
	var b strings.Builder
	col := 0
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if col >= from+width {
			break
		}
		switch {
		case col >= from && col+rw <= from+width:
			b.WriteRune(r)
		case col+rw > from && col < from+width:
			for c := col; c < col+rw; c++ {
				if c >= from && c < from+width {
					b.WriteByte(' ')
				}
			}
		}
		col += rw
	}
	return b.String()
}

func isASCIIString(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// Find returns the indices of the lines containing q. Matching ignores case
// unless q contains an upper-case letter ("smart case"). At most limit
// lines are returned (0 = no limit).
func Find(lines []string, q string, limit int) []int {
	if q == "" {
		return nil
	}
	fold := strings.ToLower(q) == q
	var out []int
	for i, l := range lines {
		if fold {
			if !containsFold(l, q) {
				continue
			}
		} else if !strings.Contains(l, q) {
			continue
		}
		out = append(out, i)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

func containsFold(s, lowerQ string) bool {
	if isASCIIString(s) && isASCIIString(lowerQ) {
		n := len(lowerQ)
		for i := 0; i+n <= len(s); i++ {
			if strings.EqualFold(s[i:i+n], lowerQ) {
				return true
			}
		}
		return false
	}
	return strings.Contains(strings.ToLower(s), lowerQ)
}

// MatchRanges returns the byte ranges of q in s (same case rule as Find).
func MatchRanges(s, q string) [][2]int {
	if q == "" {
		return nil
	}
	hay, needle := s, q
	if strings.ToLower(q) == q {
		hay = strings.ToLower(s)
		if len(hay) != len(s) {
			return nil // case folding changed byte lengths: no highlight
		}
	}
	var out [][2]int
	for i := 0; i <= len(hay)-len(needle); {
		j := strings.Index(hay[i:], needle)
		if j < 0 {
			break
		}
		out = append(out, [2]int{i + j, i + j + len(needle)})
		i += j + max(len(needle), 1)
	}
	return out
}
