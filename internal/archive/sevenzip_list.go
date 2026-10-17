package archive

import (
	"bufio"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// Links in 7Z and RAR archives
//
// TwinPane never creates links, and 7-Zip extracts into a staging folder
// first, so a link must not reach the disk even there: a link "d" pointing
// to /etc followed by a file "d/passwd" would make 7-Zip write outside
// the destination. Before extracting, the archive is listed (7z l -slt)
// and every link entry is excluded by its exact name (-spd -x!NAME, an
// argument each: no wildcards, no list file, which would trim spaces), so
// 7-Zip never handles one. Files stored below a link are skipped with it.
//
// That relies on the listed names being exact. 7-Zip 21 and newer print
// control characters in names as "_" (so such a name may not match), but
// they also refuse every link that points outside the output folder on
// their own, so a mismatch fails closed. p7zip 16.02 and other old builds
// have no such check and print names raw, so a crafted name could hide a
// link from the exclusion: with them an archive that contains any link is
// refused before anything is extracted.

// min7zipForLinks is the first 7-Zip that refuses dangerous links itself.
const min7zipForLinks = 21

// maxExcludeBytes caps the exclusion arguments (well below ARG_MAX on
// Linux and Android). Beyond it no exclusions are passed and 7-Zip's own
// link checks decide (any unsafe link then stops the extraction).
const maxExcludeBytes = 128 << 10

// listing7z is what extraction needs to know about a 7Z or RAR archive.
type listing7z struct {
	version   int      // 7-Zip major version from its banner, 0 if unknown
	links     []string // exact archive paths of link entries
	below     int      // entries stored below a link (skipped with it)
	entries   int      // entries listed
	ambiguous bool     // the listing cannot be parsed unambiguously
}

var version7z = regexp.MustCompile(`\b(\d{1,3})\.(\d{2})\b`)

// parseList7z reads the output of "7z l -slt -- ARCHIVE".
//
// The output is a banner, the archive's own properties, a line of ten
// dashes, then one block of "Key = Value" lines per entry, separated by
// blank lines. Every block of an archive has the same keys in the same
// order; a block that breaks that pattern (a name with a line break,
// printed raw by old 7-Zip builds) marks the listing ambiguous.
func parseList7z(r io.Reader) (listing7z, error) {
	var (
		l       listing7z
		inItems bool
		pattern string // key sequence of the first entry
		cur     entry7z
		others  []string // paths of the entries that are not links
	)
	finish := func() {
		if !cur.started {
			return
		}
		keys := strings.Join(cur.keys, "\x00")
		switch {
		case cur.bad || !cur.hasPath:
			l.ambiguous = true
		case pattern == "":
			pattern = keys
		case keys != pattern:
			l.ambiguous = true
		}
		l.entries++
		if cur.link {
			l.links = append(l.links, cur.path)
		} else {
			others = append(others, cur.path)
		}
		cur = entry7z{}
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !inItems {
			if l.version == 0 && strings.Contains(line, "7-Zip") {
				if m := version7z.FindStringSubmatch(line); m != nil {
					l.version, _ = strconv.Atoi(m[1])
				}
			}
			if line == "----------" {
				inItems = true
			}
			continue
		}
		if line == "" {
			finish()
			continue
		}
		cur.add(line)
	}
	finish()
	if err := sc.Err(); err != nil {
		return l, err
	}
	l.below = countBelow(l.links, others)
	return l, nil
}

// entry7z collects the lines of one entry block.
type entry7z struct {
	started, hasPath, bad, link bool
	path                        string
	keys                        []string
	seen                        map[string]bool
}

func (e *entry7z) add(line string) {
	if !e.started {
		e.started, e.seen = true, map[string]bool{}
	}
	key, val, ok := strings.Cut(line, " = ")
	if !ok {
		if k, found := strings.CutSuffix(line, " ="); found {
			key, ok = k, true
		}
	}
	if !ok || key == "" {
		e.bad = true // not "Key = Value"
		return
	}
	if e.seen[key] {
		// A repeated key: a name faked a line. Still look at it, since the
		// real one may be the second (a link must never go unnoticed).
		e.bad = true
	} else {
		e.seen[key] = true
		e.keys = append(e.keys, key)
		if key == "Path" {
			e.path, e.hasPath = val, true
		}
	}
	switch key {
	case "Attributes":
		if attrIsLink(val) {
			e.link = true
		}
	case "Symbolic Link", "Hard Link", "Link", "Copy Link":
		if val != "" {
			e.link = true
		}
	}
}

// attrIsLink reports whether a 7-Zip attribute string ("A lrwxrwxrwx",
// "A_ -rw-r--r--", "DL") marks a symbolic link: a Unix mode of type l, or
// a Windows reparse point (L among the attribute letters).
func attrIsLink(v string) bool {
	for _, tok := range strings.Fields(v) {
		if len(tok) == 10 && strings.IndexByte("-dlcbps", tok[0]) >= 0 && unixMode(tok[1:]) {
			if tok[0] == 'l' {
				return true
			}
			continue
		}
		if strings.ContainsRune(tok, 'L') {
			return true
		}
	}
	return false
}

func unixMode(s string) bool {
	for i := 0; i < len(s); i++ {
		if strings.IndexByte("-rwxsStT", s[i]) < 0 {
			return false
		}
	}
	return true
}

// countBelow counts the entries stored below a link entry ("d/x" when "d"
// is a link): 7-Zip skips them together with the excluded link. Links may
// be listed after the entries below them, so this runs after the listing.
func countBelow(links, others []string) int {
	if len(links) == 0 {
		return 0
	}
	set := make(map[string]bool, len(links))
	for _, p := range links {
		set[strings.TrimSuffix(p, "/")] = true
	}
	n := 0
	for _, p := range others {
		for i := strings.IndexByte(p, '/'); i > 0; i = nextSlash(p, i) {
			if set[p[:i]] {
				n++
				break
			}
		}
	}
	return n
}

func nextSlash(p string, i int) int {
	if j := strings.IndexByte(p[i+1:], '/'); j >= 0 {
		return i + 1 + j
	}
	return -1
}

// exclusions returns the 7-Zip arguments that leave the links out, or
// nil when there are none or they would not fit (see maxExcludeBytes).
func (l listing7z) exclusions() []string {
	if len(l.links) == 0 {
		return nil
	}
	args := []string{"-spd"}
	total := 0
	for _, p := range l.links {
		total += len(p) + 3
		if total > maxExcludeBytes {
			return nil
		}
		args = append(args, "-x!"+p)
	}
	return args
}
