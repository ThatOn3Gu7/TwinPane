package textview

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadTextLinesAndEndings(t *testing.T) {
	d, err := Load(strings.NewReader("one\r\ntwo\r\n\r\nfour"), 18, Defaults)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(d.Lines, "|") != "one|two||four" || !d.CRLF || d.Binary || d.Truncated || d.Encoding != "ASCII" {
		t.Fatalf("%+v", d)
	}
	d, _ = Load(strings.NewReader("a\n"), 2, Defaults)
	if len(d.Lines) != 1 { // a final newline does not add an empty line
		t.Fatalf("lines %q", d.Lines)
	}
	d, _ = Load(strings.NewReader("héllo wörld ✓\n"), -1, Defaults)
	if d.Encoding != "UTF-8" || d.Lines[0] != "héllo wörld ✓" {
		t.Fatalf("%+v", d)
	}
}

func TestEmptyFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "empty")
	os.WriteFile(p, nil, 0o644)
	d, err := LoadFile(p, Defaults)
	if err != nil || !d.Empty() || len(d.Lines) != 0 || d.Binary {
		t.Fatalf("%+v %v", d, err)
	}
}

func TestBinaryDetection(t *testing.T) {
	for name, data := range map[string][]byte{
		"nul":     append([]byte("ELF"), 0, 1, 2, 3),
		"control": bytes.Repeat([]byte{1, 2, 3, 4, 'a'}, 100),
		"latin":   bytes.Repeat([]byte{0xe9, 0xff, 0xfe, 'a'}, 100),
	} {
		d, err := Load(bytes.NewReader(data), int64(len(data)), Defaults)
		if err != nil || !d.Binary || d.BinaryReason == "" || len(d.Lines) != 0 {
			t.Errorf("%s: %+v", name, d)
		}
	}
	// a few invalid bytes in text: shown with replacement characters
	d, _ := Load(strings.NewReader("caf\xe9 au lait, the menu says, is good\n"), -1, Defaults)
	if d.Binary || d.Invalid != 1 || !strings.Contains(d.Lines[0], "\uFFFD") {
		t.Fatalf("mostly text: %+v", d)
	}
	// escape sequences (coloured logs) are text
	d, _ = Load(strings.NewReader("\x1b[31mred\x1b[0m line\n"), -1, Defaults)
	if d.Binary {
		t.Fatal("ANSI log treated as binary")
	}
}

func TestUTF16AndBOM(t *testing.T) {
	le := []byte{0xFF, 0xFE, 'h', 0, 'i', 0, '\n', 0, 0xAC, 0x20} // "hi\n€"
	d, _ := Load(bytes.NewReader(le), int64(len(le)), Defaults)
	if d.Binary || d.Encoding != "UTF-16LE" || strings.Join(d.Lines, "|") != "hi|€" {
		t.Fatalf("utf16: %+v", d)
	}
	d, _ = Load(strings.NewReader("\xEF\xBB\xBFbom"), -1, Defaults)
	if d.Encoding != "UTF-8 with BOM" || d.Lines[0] != "bom" {
		t.Fatalf("bom: %+v", d)
	}
}

func TestBoundsLargeFilesAndLongLines(t *testing.T) {
	p := filepath.Join(t.TempDir(), "big.log")
	var b strings.Builder
	for b.Len() < 3<<20 {
		b.WriteString("line of a large log file\n")
	}
	os.WriteFile(p, []byte(b.String()), 0o644)
	opt := Options{MaxBytes: 1 << 20, MaxLines: 1000000, MaxLineBytes: 64}
	d, err := LoadFile(p, opt)
	if err != nil || !d.Truncated || d.Read != 1<<20 || d.Size != int64(b.Len()) {
		t.Fatalf("%+v %v", d, err)
	}
	opt.MaxLines = 10
	d, _ = LoadFile(p, opt)
	if !d.Truncated || len(d.Lines) != 10 {
		t.Fatalf("line cap: %d", len(d.Lines))
	}
	long := strings.Repeat("é", 100) // 200 bytes
	d, _ = Load(strings.NewReader(long), -1, Options{MaxBytes: 1 << 20, MaxLines: 10, MaxLineBytes: 51})
	if d.LongLines != 1 || len(d.Lines[0]) != 50 || d.Invalid != 0 {
		t.Fatalf("long line cut %d bytes, invalid %d", len(d.Lines[0]), d.Invalid)
	}
}

func TestLoadFileRefusesSpecialFiles(t *testing.T) {
	dir := t.TempDir()
	if _, err := LoadFile(dir, Defaults); err == nil {
		t.Fatal("folder")
	}
	if _, err := LoadFile(filepath.Join(dir, "missing"), Defaults); err == nil {
		t.Fatal("missing")
	}
}

func TestExpandAndChunks(t *testing.T) {
	if got := Expand("a\tb\x1b[1m\x07"); got != "a   b␛[1m·" {
		t.Fatalf("expand %q", got)
	}
	if got := Expand("plain"); got != "plain" {
		t.Fatal(got)
	}
	if c := Chunks("abcdefgh", 3); strings.Join(c, "|") != "abc|def|gh" || ChunkCount("abcdefgh", 3) != 3 {
		t.Fatalf("chunks %q", c)
	}
	if c := Chunks("日本語テキスト", 5); strings.Join(c, "|") != "日本|語テ|キス|ト" || ChunkCount("日本語テキスト", 5) != 4 {
		t.Fatalf("wide chunks %q %d", c, ChunkCount("日本語テキスト", 5))
	}
	if c := Chunks("", 5); len(c) != 1 || ChunkCount("", 5) != 1 {
		t.Fatal("empty chunk")
	}
	if got := Cut("0123456789", 3, 4); got != "3456" {
		t.Fatalf("cut %q", got)
	}
	if got := Cut("日本語", 1, 3); got != " 本" {
		t.Fatalf("wide cut %q", got)
	}
}

func TestFindSmartCase(t *testing.T) {
	lines := []string{"Alpha", "beta", "ALPHABET", "gamma alpha"}
	if got := Find(lines, "alpha", 0); len(got) != 3 {
		t.Fatalf("fold %v", got)
	}
	if got := Find(lines, "Alpha", 0); len(got) != 1 || got[0] != 0 {
		t.Fatalf("exact %v", got)
	}
	if got := Find(lines, "a", 2); len(got) != 2 {
		t.Fatalf("limit %v", got)
	}
	if r := MatchRanges("gamma alpha ALPHA", "alpha"); len(r) != 2 || r[0] != [2]int{6, 11} {
		t.Fatalf("ranges %v", r)
	}
}
