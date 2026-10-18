//go:build unix

package archive

import (
	"context"
	"encoding/base64"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ThatOn3Gu7/TwinPane/internal/ops"
)

// ---- links in 7Z and RAR archives, REAL 7-Zip (skipped when not installed) --------

// noLinksIn fails if dest holds any symbolic link.
func noLinksIn(t *testing.T, dest string) {
	t.Helper()
	filepath.WalkDir(dest, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.Type()&fs.ModeSymlink != 0 {
			t.Errorf("a link was created: %s", p)
		}
		return nil
	})
}

func emptyDir(t *testing.T, dir, what string) {
	t.Helper()
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		names := []string{}
		for _, e := range ents {
			names = append(names, e.Name())
		}
		t.Fatalf("%s: %q", what, names)
	}
}

// An archive made to escape: links out of the destination (absolute,
// "../"), a link to a folder outside with a file stored "below" it, link
// names with spaces and wildcard characters next to regular files with
// similar names. Nothing may be written outside, no link may be created,
// and every regular file must arrive (7-Zip 21+), or the archive is
// refused before anything is written (older 7-Zip).
func TestExtract7zHostileLinksReal(t *testing.T) {
	tool := find7z(t)
	modern := version7zOf(t, tool) >= min7zipForLinks
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	os.Mkdir(outside, 0o755)
	src := filepath.Join(root, "src")
	for _, d := range []string{"d", "sub", "tmp"} {
		os.MkdirAll(filepath.Join(src, d), 0o755)
	}
	files := map[string]string{"d/a.txt": "a", " lead.txt": "lead", "[a]b": "ab", "sub/abs": "regular", "tmp/x": "pwn"}
	for name, body := range files {
		os.WriteFile(filepath.Join(src, name), []byte(body), 0o644)
	}
	links := map[string]string{"d/rel": "a.txt", "d/abs": "/etc/passwd", "d/up": "../../x", "esc": outside, " lead": "/etc/passwd", "[a]*": "x", "abs": "/etc/passwd"}
	for name, target := range links {
		os.Symlink(target, filepath.Join(src, name))
	}
	arc := filepath.Join(root, "hostile.7z")
	add7zLinks(t, tool, src, arc, "d", " lead.txt", "[a]b", "sub", "esc", " lead", "[a]*", "abs", "tmp/x")
	// store tmp/x as esc/x: below the link to the outside folder
	run7zCreate(t, tool, src, "rn", "-bso0", "-bse0", arc, "tmp/x", "esc/x")
	run7zCreate(t, tool, src, "d", "-bso0", "-bse0", arc, "tmp")

	dest := filepath.Join(root, "out")
	res := ExtractAny(context.Background(), ExtractRequest{Archive: arc, DestDir: dest, SevenZip: tool}, ops.Hooks{})
	emptyDir(t, outside, "written outside the destination")
	noLinksIn(t, dest)
	noTemps(t, dest)
	if !modern {
		if res.Fatal == nil || !strings.Contains(res.Fatal.Error(), "too old") || !strings.Contains(res.Fatal.Error(), "7 links") {
			t.Fatalf("old 7-Zip: %v", res.Fatal)
		}
		emptyDir(t, dest, "nothing may be extracted")
		return
	}
	if !res.OK() || res.Files != 4 || res.Links != len(links) || res.Unsafe != 1 {
		t.Fatalf("hostile 7z: %s links %d unsafe %d %+v %v", res.Summary(), res.Links, res.Unsafe, res.Errors, res.Fatal)
	}
	for name, body := range files {
		if name == "tmp/x" {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(dest, name)); err != nil || string(b) != body {
			t.Errorf("%q: %q %v", name, b, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(dest, "esc")); err == nil {
		t.Error("nothing may be stored below a link")
	}
}

// fixtureRar was made with RAR 7.12 (rar a -ol, then rn): d/a.txt
// ("rar-ok"), links d/rel -> a.txt, d/abs -> /etc/passwd, d/up -> ../../x,
// esc -> /tmp/twinpane-rar-escape, and a file esc/x stored below that link.
const fixtureRar = "UmFyIRoHAQAzkrXlCgEFBgAFAQGAgABCmp+dIAIDCgAEBf/DAgAAAACAAAEFZC9yZWwJBQEABWEudHh0VXYyzCYCAxAABAv/wwIAAAAAgAABBWQvYWJzDwUBAAsvZXRjL3Bhc3N3ZDLSCR0XAgIGBAakgwKIIdyagAABB2QvYS50eHRyYXItb2tRa/aHIQIDDAAEB//DAgAAAACAAAEEZC91cAsFAQAHLi4vLi4vePGVOtMxAgMdAAQY/8MCAAAAAIAAAQNlc2McBQEAGC90bXAvdHdpbnBhbmUtcmFyLWVzY2FwZRadCOYVAgIDBAOkgwIsEsRhgAABBWVzYy94cHduqRRzjA0CAgABAO2DAYAAAQFkHXdWUQMFBAA="

func TestExtractRarLinksReal(t *testing.T) {
	tool := find7z(t)
	const escape = "/tmp/twinpane-rar-escape"
	if _, err := os.Lstat(escape); err == nil {
		t.Skipf("%s exists already; cannot tell whether extraction wrote it", escape)
	}
	b, err := base64.StdEncoding.DecodeString(fixtureRar)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	arc := writeFile(t, filepath.Join(root, "links.rar"), b)
	dest := filepath.Join(root, "out")
	res := ExtractAny(context.Background(), ExtractRequest{Archive: arc, DestDir: dest, SevenZip: tool}, ops.Hooks{})
	defer os.RemoveAll(escape)
	if _, err := os.Lstat(escape); err == nil {
		t.Fatal("extraction wrote outside the destination")
	}
	noLinksIn(t, dest)
	noTemps(t, dest)
	if res.Fatal != nil && strings.Contains(res.Fatal.Error(), "cannot read") {
		t.Skipf("this 7-Zip has no RAR support: %v", res.Fatal)
	}
	if version7zOf(t, tool) < min7zipForLinks {
		if res.Fatal == nil || !strings.Contains(res.Fatal.Error(), "too old") {
			t.Fatalf("old 7-Zip: %v", res.Fatal)
		}
		emptyDir(t, dest, "nothing may be extracted")
		return
	}
	if !res.OK() || res.Files != 1 || res.Links != 4 || res.Unsafe != 1 {
		t.Fatalf("rar: %s links %d unsafe %d %+v %v", res.Summary(), res.Links, res.Unsafe, res.Errors, res.Fatal)
	}
	if got, _ := os.ReadFile(filepath.Join(dest, "d", "a.txt")); string(got) != "rar-ok" {
		t.Fatalf("d/a.txt: %q", got)
	}
}

// ---- a simulated 7-Zip: the paths CI cannot reach with the installed one ----------

const fake7zScript = `#!/bin/sh
printf '%s\n' "--call--" "$@" >> "$TP_FAKE7Z_LOG"
case "$1" in
l)
	cat "$TP_FAKE7Z_LIST"
	if [ -n "$TP_FAKE7Z_LIST_ERR" ]; then echo "$TP_FAKE7Z_LIST_ERR" >&2; exit 2; fi
	exit 0 ;;
x)
	for a; do case "$a" in -o*) out="${a#-o}" ;; esac; done
	mkdir -p "$out/d" && echo data > "$out/d/file.txt"
	if [ -n "$TP_FAKE7Z_X_ERR" ]; then echo "$TP_FAKE7Z_X_ERR" >&2; exit 2; fi
	exit 0 ;;
esac
exit 7
`

type fake7z struct {
	tool, log, list, arc, dest string
}

func newFake7z(t *testing.T, listing string) fake7z {
	t.Helper()
	dir := t.TempDir()
	f := fake7z{
		tool: filepath.Join(dir, "7zz"),
		log:  filepath.Join(dir, "log"),
		list: filepath.Join(dir, "list"),
		arc:  filepath.Join(dir, "t.7z"),
		dest: filepath.Join(dir, "out"),
	}
	if err := os.WriteFile(f.tool, []byte(fake7zScript), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(f.list, []byte(listing), 0o644)
	os.WriteFile(f.arc, append(append([]byte{}, magic7z...), make([]byte, 32)...), 0o644)
	t.Setenv("TP_FAKE7Z_LOG", f.log)
	t.Setenv("TP_FAKE7Z_LIST", f.list)
	t.Setenv("TP_FAKE7Z_LIST_ERR", "")
	t.Setenv("TP_FAKE7Z_X_ERR", "")
	return f
}

func (f fake7z) run() ExtractResult {
	return ExtractAny(context.Background(), ExtractRequest{Archive: f.arc, DestDir: f.dest, SevenZip: f.tool}, ops.Hooks{})
}

// calls returns the argument lists 7-Zip was started with.
func (f fake7z) calls(t *testing.T) [][]string {
	t.Helper()
	b, _ := os.ReadFile(f.log)
	var out [][]string
	for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		if line == "--call--" {
			out = append(out, nil)
		} else if len(out) > 0 {
			out[len(out)-1] = append(out[len(out)-1], line)
		}
	}
	return out
}

func listing(banner string, entries ...string) string {
	return banner + "\n\n--\nPath = t.7z\nType = 7z\n\n----------\n" + strings.Join(entries, "\n\n") + "\n"
}

const (
	entryFile = "Path = d/file.txt\nSize = 5\nAttributes = A -rw-r--r--"
	entryLink = "Path =  my link \nSize = 11\nAttributes = A lrwxrwxrwx"
)

func TestExtract7zPassesExactExclusions(t *testing.T) {
	f := newFake7z(t, listing("7-Zip (z) 23.01 (x64) : Copyright", entryFile, entryLink))
	res := f.run()
	if !res.OK() || res.Files != 1 || res.Links != 1 {
		t.Fatalf("%s %+v %v", res.Summary(), res.Errors, res.Fatal)
	}
	calls := f.calls(t)
	if len(calls) != 2 || calls[0][0] != "l" || calls[1][0] != "x" {
		t.Fatalf("calls: %q", calls)
	}
	x := strings.Join(calls[1], "\x00")
	if !strings.Contains(x, "\x00-spd\x00-x! my link \x00--\x00") {
		t.Fatalf("the link must be excluded by its exact name, before --: %q", calls[1])
	}
}

func TestExtract7zNoLinksNoSwitches(t *testing.T) {
	f := newFake7z(t, listing("7-Zip 25.01 (x64) : Copyright", entryFile))
	if res := f.run(); !res.OK() || res.Files != 1 {
		t.Fatalf("%s %v", res.Summary(), res.Fatal)
	}
	for _, a := range f.calls(t)[1] {
		if a == "-spd" || strings.HasPrefix(a, "-x") {
			t.Fatalf("no exclusions without links: %q", f.calls(t)[1])
		}
	}
}

func TestExtract7zOldWithLinksRefused(t *testing.T) {
	for name, banner := range map[string]string{
		"p7zip 16.02":     "7-Zip (a) [64] 16.02 : Copyright (c) 1999-2016 Igor Pavlov : 2016-05-21",
		"unknown version": "Some archiver",
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake7z(t, listing(banner, entryFile, entryLink))
			res := f.run()
			if res.Fatal == nil || !strings.Contains(res.Fatal.Error(), "too old") || !strings.Contains(res.Fatal.Error(), "pkg install 7zip") {
				t.Fatalf("refusal: %v", res.Fatal)
			}
			if calls := f.calls(t); len(calls) != 1 {
				t.Fatalf("7-Zip may only list, never extract: %q", calls)
			}
			emptyDir(t, f.dest, "nothing may be extracted")
		})
	}
	// an old 7-Zip is fine for archives without links
	f := newFake7z(t, listing("7-Zip (a) [64] 16.02 : Copyright", entryFile))
	if res := f.run(); !res.OK() || res.Files != 1 {
		t.Fatalf("old 7-Zip, no links: %s %v", res.Summary(), res.Fatal)
	}
}

// A listing that cannot be parsed unambiguously must fail closed: a crafted
// name can hide a link from the exclusion, so nothing may be extracted and
// 7-Zip may only be asked for the listing, never for extraction.
func TestExtract7zAmbiguousListingRefused(t *testing.T) {
	for name, entries := range map[string][]string{
		"blocks with different keys": {
			"Path = a\nSize = 5\nAttributes = A -rw-r--r--",
			"Path = b\nSize = 6",
		},
		"repeated key hides the entry's shape": {
			"Path = safe.txt\nSize = 5\nAttributes = A -rw-r--r--\nPath = mylink",
		},
		"line without Key = Value": {
			"Path = a\nSize = 5\ninjected by a name\nAttributes = A -rw-r--r--",
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFake7z(t, listing("7-Zip (z) 23.01 (x64) : Copyright", entries...))
			res := f.run()
			if res.Fatal == nil || !strings.Contains(res.Fatal.Error(), "could not be read safely") ||
				!strings.Contains(res.Fatal.Error(), "nothing was extracted") {
				t.Fatalf("an ambiguous listing must refuse extraction: %v", res.Fatal)
			}
			if calls := f.calls(t); len(calls) != 1 {
				t.Fatalf("7-Zip may only list, never extract: %q", calls)
			}
			emptyDir(t, f.dest, "nothing may be extracted")
		})
	}
}

func TestExtract7zListingFails(t *testing.T) {
	f := newFake7z(t, "7-Zip (z) 23.01\n")
	t.Setenv("TP_FAKE7Z_LIST_ERR", "ERROR: t.7z : Can not open encrypted archive. Wrong password?")
	res := f.run()
	if res.Fatal == nil || !strings.Contains(res.Fatal.Error(), "password-protected") {
		t.Fatalf("listing error: %v", res.Fatal)
	}
	if calls := f.calls(t); len(calls) != 1 {
		t.Fatalf("no extraction after a failed listing: %q", calls)
	}
	emptyDir(t, f.dest, "nothing may be extracted")
}

func TestExtract7zRefusedLinkMovesNothing(t *testing.T) {
	// 7-Zip refusing a link (a name the exclusion could not match) stops
	// the extraction; what it already unpacked is never moved into place
	f := newFake7z(t, listing("7-Zip (z) 23.01", entryFile))
	t.Setenv("TP_FAKE7Z_X_ERR", "ERROR: Dangerous link path was ignored : odd_name : /etc")
	res := f.run()
	if res.Fatal == nil || !strings.Contains(res.Fatal.Error(), "link pointing outside") || !strings.Contains(res.Fatal.Error(), "nothing was extracted") {
		t.Fatalf("dangerous link: %v", res.Fatal)
	}
	emptyDir(t, f.dest, "nothing may be moved into place")
}
