package opener

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestByName(t *testing.T) {
	cases := map[string]Kind{
		"a.JPG": Image, "b.png": Image, "c.heic": Image, "song.mp3": Audio, "v.opus": Audio, "clip.MKV": Video,
		"doc.pdf": PDF, "x.docx": Document, "book.epub": Document, "app.apk": APK, "a.tar.gz": Archive, "z.zip": Archive,
		"main.go": Text, "notes.txt": Text, "Makefile": Text, "page.html": Text, "noext": Unknown, "weird.qqq": Unknown,
		".hidden": Unknown, ".bashrc": Text,
	}
	for name, want := range cases {
		if got := ByName(name).Kind; got != want {
			t.Errorf("ByName(%q) = %v, want %v", name, got, want)
		}
	}
	if ByName("a.mp4").MIME != "video/mp4" {
		t.Error("mime")
	}
}

func TestSniff(t *testing.T) {
	ftyp := func(brand string) []byte { return []byte("\x00\x00\x00\x18ftyp" + brand + "\x00\x00\x00\x00") }
	tarHead := make([]byte, 512)
	copy(tarHead[257:], "ustar")
	cases := []struct {
		head []byte
		kind Kind
		mime string
	}{
		{[]byte("%PDF-1.7\n"), PDF, "application/pdf"},
		{[]byte("\x89PNG\r\n\x1a\n...."), Image, "image/png"},
		{[]byte("\xff\xd8\xff\xe0JFIF"), Image, "image/jpeg"},
		{[]byte("GIF89a.."), Image, "image/gif"},
		{[]byte("RIFF\x00\x00\x00\x00WEBPVP8 "), Image, "image/webp"},
		{[]byte("RIFF\x00\x00\x00\x00WAVEfmt "), Audio, "audio/wav"},
		{ftyp("isom"), Video, "video/mp4"},
		{ftyp("M4A "), Audio, "audio/mp4"},
		{ftyp("heic"), Image, "image/heic"},
		{[]byte("\x1a\x45\xdf\xa3...."), Video, "video/x-matroska"},
		{[]byte("OggS\x00\x02"), Audio, "audio/ogg"},
		{[]byte("ID3\x04\x00"), Audio, "audio/mpeg"},
		{[]byte("PK\x03\x04\x14\x00"), Archive, "application/zip"},
		{[]byte("\x1f\x8b\x08\x00"), Archive, "application/gzip"},
		{[]byte("\xfd7zXZ\x00\x00"), Archive, "application/x-xz"},
		{[]byte("\x28\xb5\x2f\xfd\x00"), Archive, "application/zstd"},
		{[]byte("BZh91AY&SY"), Archive, "application/x-bzip2"},
		{tarHead, Archive, "application/x-tar"},
		{[]byte("\x7fELF\x02\x01\x01"), Program, "application/x-executable"},
		{[]byte("<?xml version=\"1.0\"?><svg xmlns="), Image, "image/svg+xml"},
		{[]byte("#!/bin/sh\necho hi\n"), Text, "text/plain"},
		{[]byte("ünïcødé text\n"), Text, "text/plain"},
		{[]byte{}, Text, "text/plain"},
		{[]byte("\x00\x01\x02\x03\x04binary"), Unknown, ""},
	}
	for _, c := range cases {
		got := Sniff(c.head)
		if got.Kind != c.kind || got.MIME != c.mime || !got.Sniffed {
			t.Errorf("Sniff(%q) = %+v, want %v %s", c.head[:min(len(c.head), 12)], got, c.kind, c.mime)
		}
	}
}

func TestDetectFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, data string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(data), 0o644)
		return p
	}
	if k := Detect(write("photo", "\x89PNG\r\n\x1a\nxxxx")).Kind; k != Image {
		t.Errorf("extensionless PNG: %v", k)
	}
	if k := Detect(write("script", "#!/bin/sh\n")).Kind; k != Text {
		t.Errorf("extensionless script: %v", k)
	}
	if k := Detect(write("app.ts", "export const x = 1;\n")).Kind; k != Text {
		t.Errorf("TypeScript: %v", k)
	}
	if k := Detect(write("rec.ts", "\x47\x00\x11\x00\x00\x00\x00\x00\x00\x01\x02")).Kind; k != Video {
		t.Errorf("MPEG-TS: %v", k)
	}
	// the name wins when it is telling (no I/O): a mislabelled file keeps its name's type
	if k := Detect(write("fake.jpg", "plain text")).Kind; k != Image {
		t.Errorf("by name: %v", k)
	}
	if k := Detect(filepath.Join(dir, "missing")).Kind; k != Unknown {
		t.Errorf("missing file: %v", k)
	}
}

func env(vars map[string]string, have ...string) Env {
	return Env{
		Getenv: func(k string) string { return vars[k] },
		LookPath: func(name string) (string, error) {
			for _, h := range have {
				if h == name {
					return "/usr/bin/" + name, nil
				}
			}
			return "", errors.New("not found")
		},
		GOOS: "linux",
	}
}

func TestDetectPlatform(t *testing.T) {
	termux := map[string]string{"TERMUX_VERSION": "0.118.1", "PREFIX": "/data/data/com.termux/files/usr"}
	p := DetectPlatform(env(termux, "termux-open", "xdg-open"))
	if !p.Termux || p.Name != "termux-open" || p.Opener != "/usr/bin/termux-open" {
		t.Errorf("termux: %+v", p)
	}
	// Termux detected from PREFIX alone, and from an Android build
	if p := DetectPlatform(env(map[string]string{"PREFIX": "/data/data/com.termux/files/usr"}, "termux-open")); !p.Termux {
		t.Error("PREFIX")
	}
	e := env(nil, "termux-open")
	e.GOOS = "android"
	if p := DetectPlatform(e); !p.Termux || !p.HasOpener() {
		t.Error("GOOS android")
	}
	p = DetectPlatform(env(termux))
	if p.HasOpener() || !strings.Contains(p.Why, "termux-tools") {
		t.Errorf("termux without termux-open: %+v", p)
	}
	// desktop needs a graphical session
	p = DetectPlatform(env(nil, "xdg-open"))
	if p.HasOpener() || !strings.Contains(p.Why, "DISPLAY") {
		t.Errorf("no display: %+v", p)
	}
	p = DetectPlatform(env(map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, "xdg-open"))
	if !p.HasOpener() || p.Termux {
		t.Errorf("wayland: %+v", p)
	}
	p = DetectPlatform(env(map[string]string{"DISPLAY": ":0"}))
	if p.HasOpener() || !strings.Contains(p.Why, "xdg-utils") {
		t.Errorf("no xdg-open: %+v", p)
	}
	e = env(nil, "open")
	e.GOOS = "darwin"
	if p := DetectPlatform(e); p.Opener != "/usr/bin/open" {
		t.Errorf("darwin: %+v", p)
	}
	e.GOOS = "windows"
	if p := DetectPlatform(e); p.HasOpener() {
		t.Error("windows")
	}
}

func TestArgvKeepsNamesIntact(t *testing.T) {
	termux := Platform{Termux: true, Opener: "/t/termux-open", Name: "termux-open"}
	desk := Platform{Opener: "/usr/bin/xdg-open", Name: "xdg-open"}
	for _, name := range []string{"/s/with space.jpg", `/s/q'"uote.png`, "/s/$(rm -rf ~);`id`.pdf", "/s/-n --help.mp4", "/s/ünï#?.mp3"} {
		argv, ok := termux.DefaultArgv(name)
		if !ok || len(argv) != 3 || argv[1] != "--" || argv[2] != name {
			t.Errorf("termux argv %q", argv)
		}
		argv, _ = termux.ShareArgv(name)
		if strings.Join(argv, "|") != "/t/termux-open|--send|--|"+name {
			t.Errorf("share argv %q", argv)
		}
		argv, _ = desk.DefaultArgv(name)
		if len(argv) != 2 || argv[1] != name {
			t.Errorf("desktop argv %q", argv)
		}
	}
	if _, ok := desk.ShareArgv("/x"); ok {
		t.Error("share outside Termux")
	}
	if _, ok := (Platform{}).DefaultArgv("/x"); ok {
		t.Error("argv without opener")
	}
}

func labels(cs []Choice) string {
	var l []string
	for _, c := range cs {
		s := c.Label
		if c.Disabled {
			s = "(" + s + ")"
		}
		l = append(l, s)
	}
	return strings.Join(l, ", ")
}

func TestChoices(t *testing.T) {
	termux := Platform{Termux: true, Opener: "/t/termux-open", Name: "termux-open"}
	got := labels(termux.Choices(Type{Kind: Image}, "/s/a.jpg", false))
	if got != "Android: default app, Android: choose app…, Built-in viewer, Text editor, Share…, Run a program…" {
		t.Errorf("termux image: %s", got)
	}
	got = labels(termux.Choices(Type{Kind: Unknown}, "/s/blob", false))
	if got != "Android: default app, Android: open as text, Android: open as image, Android: open as audio, Android: open as video, Built-in viewer, Text editor, Share…, Run a program…" {
		t.Errorf("termux unknown: %s", got)
	}
	got = labels(termux.Choices(Type{Kind: APK}, "/s/a.apk", true))
	if got != "Android: default app, Android: choose app…, Built-in viewer, Text editor, Browse as ZIP archive, Share…, Run a program…" {
		t.Errorf("termux apk: %s", got)
	}
	for _, c := range termux.Choices(Type{Kind: Unknown}, "/s/blob", false) {
		for _, a := range c.Argv {
			if strings.ContainsAny(a, "* ") && a != "/s/blob" {
				t.Errorf("argument %q could be split or globbed by termux-open", a)
			}
		}
	}
	none := Platform{Name: "xdg-open", Why: "no graphical session"}
	cs := none.Choices(Type{Kind: PDF}, "/s/a.pdf", false)
	if !cs[0].Disabled || cs[0].Why != "no graphical session" {
		t.Errorf("disabled default: %+v", cs[0])
	}
	if got := labels(cs); strings.Contains(got, "Share") || strings.Contains(got, "Android") {
		t.Errorf("desktop: %s", got)
	}
	for k := Unknown; k <= Program; k++ {
		for _, zip := range []bool{false, true} {
			if n := len(termux.Choices(Type{Kind: k}, "/x", zip)); n > 9 {
				t.Errorf("%v (zip %v): %d choices — more than the 9 number shortcuts", k, zip, n)
			}
		}
	}
}

func TestPrefers(t *testing.T) {
	p := Platform{Opener: "/x/xdg-open"}
	for k, want := range map[Kind]bool{Image: true, Audio: true, Video: true, PDF: true, Document: true,
		Text: false, Unknown: false, APK: false, Archive: false, Program: false} {
		if p.Prefers(Type{Kind: k}) != want {
			t.Errorf("Prefers(%v) != %v", k, want)
		}
	}
	if (Platform{}).Prefers(Type{Kind: Image}) {
		t.Error("prefers external without an opener")
	}
}
