package archive

import (
	"reflect"
	"strings"
	"testing"
)

// Listings as printed by "7z l -slt" (trimmed to the parts that matter;
// the real output has more keys per entry, the same for every entry).

const listModern = `
7-Zip (z) 23.01 (x64) : Copyright (c) 1999-2023 Igor Pavlov : 2023-06-20

Scanning the drive for archives:
1 file, 300 bytes (1 KiB)

Listing archive: t.7z

--
Path = t.7z
Type = 7z
Physical Size = 300

----------
Path = d/x
Size = 3
Attributes = A -rw-r--r--
CRC = 1234

Path = esc
Size = 9
Attributes = A lrwxrwxrwx
CRC = 

Path =  lead
Size = 9
Attributes = A lrwxrwxrwx
CRC = 

Path = esc/x
Size = 3
Attributes = A -rw-r--r--
CRC = 99

Path = escape/y
Size = 3
Attributes = A -rw-r--r--
CRC = 98

Path = d
Size = 0
Attributes = D drwxr-xr-x
CRC = 
`

func TestParseList7zModern(t *testing.T) {
	l, err := parseList7z(strings.NewReader(listModern))
	if err != nil {
		t.Fatal(err)
	}
	if l.version != 23 || l.entries != 6 || l.ambiguous {
		t.Fatalf("listing: %+v", l)
	}
	// exact names, leading space kept; the archive's own "Path = t.7z" is not an entry
	if !reflect.DeepEqual(l.links, []string{"esc", " lead"}) {
		t.Fatalf("links: %q", l.links)
	}
	// esc/x is below the link esc; escape/y is not
	if l.below != 1 {
		t.Fatalf("below: %d", l.below)
	}
	if got := l.exclusions(); !reflect.DeepEqual(got, []string{"-spd", "-x!esc", "-x! lead"}) {
		t.Fatalf("exclusions: %q", got)
	}
}

// p7zip 16.02 prints names raw: a name with a line break can fake a line.
// The link is still found (its real Attributes line follows), and the
// broken pattern marks the listing ambiguous.
const listOldInjected = "7-Zip (a) [64] 16.02 : Copyright (c) 1999-2016 Igor Pavlov : 2016-05-21\n" +
	"p7zip Version 16.02 (locale=C.UTF-8,Utf16=on,HugeFiles=on,64 bits)\n\n--\nPath = e.7z\nType = 7z\n\n----------\n" +
	"Path = x\nAttributes = A_ -rw-r--r--\nSize = 9\nAttributes = A_ lrwxrwxrwx\n\n" +
	"Path = n\nl.txt\nSize = 2\nAttributes = A_ -rw-r--r--\n\n" +
	"Path = ok\nSize = 2\nAttributes = A_ -rw-r--r--\n"

func TestParseList7zOldAmbiguous(t *testing.T) {
	l, err := parseList7z(strings.NewReader(listOldInjected))
	if err != nil {
		t.Fatal(err)
	}
	if l.version != 16 || !l.ambiguous || len(l.links) != 1 || l.entries != 3 {
		t.Fatalf("listing: %+v", l)
	}
}

func TestParseList7zSameInjectionStillFindsLinks(t *testing.T) {
	// Two crafted names injecting the same line keep the pattern equal,
	// but the link's own Attributes line cannot be hidden.
	out := "7-Zip (a) [64] 16.02 : Copyright\n----------\n" +
		"Path = x\nFoo = 1\nSize = 9\nAttributes = A_ lrwxrwxrwx\n\n" +
		"Path = x\nFoo = 1/y\nSize = 3\nAttributes = A_ -rw-r--r--\n"
	l, err := parseList7z(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(l.links) != 1 || l.version >= min7zipForLinks {
		t.Fatalf("an old 7-Zip with a link must be detected: %+v", l)
	}
}

func TestParseList7zLinkKinds(t *testing.T) {
	out := "7-Zip 25.01 (x64) : Copyright\n----------\n" +
		"Path = unix\nAttributes =  lrwxrwxrwx\nSymbolic Link = /etc/passwd\nHard Link = \n\n" +
		"Path = rarlink\nAttributes = A\nSymbolic Link = ../../x\nHard Link = \n\n" +
		"Path = hard\nAttributes = A -rw-r--r--\nSymbolic Link = \nHard Link = file\n\n" +
		"Path = reparse\nAttributes = AL\nSymbolic Link = \nHard Link = \n\n" +
		"Path = plain\nAttributes = RHSA -rwxr-xr-x\nSymbolic Link = \nHard Link = \n\n" +
		"Path = dir\nAttributes = D drwxr-xr-x\nSymbolic Link = \nHard Link = \n"
	l, err := parseList7z(strings.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"unix", "rarlink", "hard", "reparse"}; !reflect.DeepEqual(l.links, want) || l.version != 25 || l.ambiguous {
		t.Fatalf("links %q, %+v", l.links, l)
	}
}

func TestParseList7zMalformed(t *testing.T) {
	for name, out := range map[string]string{
		"repeated key": "7-Zip 23.01\n----------\nPath = a\nSize = 1\nSize = 2\n",
		"no key":       "7-Zip 23.01\n----------\nPath = a\njunk\n",
		"no path":      "7-Zip 23.01\n----------\nSize = 1\n",
		"new pattern":  "7-Zip 23.01\n----------\nPath = a\nSize = 1\n\nPath = b\nCRC = 1\n",
	} {
		if l, _ := parseList7z(strings.NewReader(out)); !l.ambiguous {
			t.Errorf("%s: not marked ambiguous: %+v", name, l)
		}
	}
	// "Key =" with nothing after it is a valid, empty value
	if l, _ := parseList7z(strings.NewReader("7-Zip 23.01\n----------\nPath = a\nCRC =\n\nPath = b\nCRC = 5\n")); l.ambiguous || l.entries != 2 {
		t.Fatalf("empty value: %+v", l)
	}
}

func TestParseList7zVersions(t *testing.T) {
	for banner, want := range map[string]int{
		"7-Zip (a) [64] 16.02 : Copyright (c) 1999-2016 Igor Pavlov : 2016-05-21": 16,
		"7-Zip 25.01 (x64) : Copyright (c) 1999-2025 Igor Pavlov : 2025-08-03":    25,
		"7-Zip (z) 26.04 (x64) : Copyright (c) 1999-2026 Igor Pavlov":             26,
		"7-Zip (r) 21.07 (arm64) : Copyright":                                     21,
		"Something else entirely":                                                 0,
	} {
		l, _ := parseList7z(strings.NewReader(banner + "\n----------\nPath = a\n"))
		if l.version != want {
			t.Errorf("%q: version %d, want %d", banner, l.version, want)
		}
	}
}

func TestCountBelowAnyOrder(t *testing.T) {
	links := []string{"a", "x/y"}
	others := []string{"a/1", "a/b/2", "ab/3", "x/y/z", "x/1", "b"}
	if n := countBelow(links, others); n != 3 {
		t.Fatalf("below: %d", n)
	}
	if countBelow(nil, others) != 0 {
		t.Fatal("no links")
	}
}

func TestExclusionsTooLong(t *testing.T) {
	l := listing7z{links: []string{strings.Repeat("n", 200)}}
	if got := l.exclusions(); len(got) != 2 || got[0] != "-spd" {
		t.Fatalf("short: %q", got)
	}
	for len(l.links)*200 < maxExcludeBytes {
		l.links = append(l.links, strings.Repeat("n", 200))
	}
	if got := l.exclusions(); got != nil {
		t.Fatal("too many to pass: none, 7-Zip's own checks decide")
	}
	if (listing7z{}).exclusions() != nil {
		t.Fatal("no links, no switches")
	}
}
