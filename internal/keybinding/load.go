package keybinding

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ThatOn3Gu7/TwinPane/internal/bookmarks"
)

const (
	// FileName is the key bindings file inside TwinPane's configuration
	// folder, next to settings.json.
	FileName = "keybinding.json"
	// Version is the file format version written.
	Version = 1
)

const about = "TwinPane key bindings. Every action is listed with its current keys under a " +
	"comment that says where it is used and what it does: change a key here, then reopen TwinPane " +
	"or choose Settings (F9) and press Enter on \u201cKey bindings\u201d to reload. Several keys per " +
	"action are allowed and the first one is the one the hint bar shows; [ ] disables an action. " +
	"The // comments are for reading only. Key names: letters as written (A and a differ), ctrl+x, " +
	"alt+x, f1-f20, up, down, left, right, home, end, pgup, pgdown, enter, tab, shift+tab, esc, " +
	"space, backspace, delete, insert."

// Loaded is the outcome of reading keybinding.json. Set is always usable: a
// missing or damaged file yields the defaults, and the warnings explain what
// was ignored.
type Loaded struct {
	Path     string
	Set      *Set
	Err      error // the file could not be used at all (damaged, newer, unreadable)
	Created  bool  // the file did not exist and was written with the defaults
	Updated  bool  // the file was completed: missing actions or the hints were added
	WriteErr error // the file could not be written
	Missing  bool  // no path: persistence is off for this session
}

// DefaultPath returns keybinding.json in the shared TwinPane configuration
// folder (TWINPANE_CONFIG_DIR, then the OS user config directory, then
// ~/.twinpane — the same policy as settings.json and bookmarks.json).
func DefaultPath() (string, error) {
	dir, err := bookmarks.ConfigDir(os.Getenv, os.UserConfigDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// fileShape is the on-disk shape. Bindings stays raw so one bad entry can be
// reported and skipped instead of failing the whole file.
type fileShape struct {
	Version  int                        `json:"version"`
	About    string                     `json:"about,omitempty"`
	Bindings map[string]json.RawMessage `json:"bindings"`
}

// Load reads path and returns a usable set. It never overwrites a damaged or
// newer file; it writes the file when it is missing, and adds actions that the
// file does not mention (a new TwinPane version) while keeping every value the
// user set.
func Load(path string) Loaded {
	out := Loaded{Path: path}
	if path == "" {
		out.Missing = true
		out.Set = Default()
		return out
	}
	out.Set = Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		out.Created = true
		if werr := write(path, out.Set.FileBindings(), false); werr != nil {
			out.WriteErr = werr
		}
		return out
	}
	if err != nil {
		out.Err = fmt.Errorf("read key bindings %s: %w", path, err)
		out.Set.warnings = append(out.Set.warnings, out.Err.Error())
		return out
	}
	if len(strings.TrimSpace(string(stripComments(data)))) == 0 {
		out.Created = true
		if werr := write(path, out.Set.FileBindings(), false); werr != nil {
			out.WriteErr = werr
		}
		return out
	}
	var shape fileShape
	if err := json.Unmarshal(stripComments(data), &shape); err != nil {
		out.Err = &CorruptError{Path: path, Err: err}
		out.Set.warnings = append(out.Set.warnings, out.Err.Error())
		return out
	}
	if shape.Version > Version {
		out.Err = &CorruptError{Path: path, Err: fmt.Errorf("written by a newer TwinPane (format %d)", shape.Version)}
		out.Set.warnings = append(out.Set.warnings, out.Err.Error())
		return out
	}
	custom := map[string][]string{}
	var extra []string
	unknown := false
	for name, raw := range shape.Bindings {
		keys, err := decodeKeys(raw)
		if err != nil {
			extra = append(extra, fmt.Sprintf("%q: %s", name, err.Error()))
			continue
		}
		if _, ok := Lookup(name); !ok {
			unknown = true // the set warns about it; the line stays in the file
		}
		custom[name] = keys
	}
	out.Set = New(custom)
	out.Set.warnings = append(extra, out.Set.warnings...)
	// A file that does not mention every action still works (missing means the
	// default), but the promise of the file is that you can see every binding.
	var missing []Action
	for _, a := range Actions() {
		if _, ok := shape.Bindings[a.Name]; !ok {
			missing = append(missing, a)
		}
	}
	// A malformed entry is never replaced automatically: the file may hold a
	// value the user is still writing. It is reported instead.
	if len(extra) > 0 {
		return out
	}
	if !hasComments(data) && !unknown {
		// No comments at all: a file from an older build or written by hand.
		// Rewriting it in the documented form (every action, every hint, the
		// values the file holds) is lossless and gives the reader the hints.
		// The values are the ones the file asked for, not the resolved set:
		// a key that lost a conflict must not come back as a disabled action.
		out.Updated = true
		if werr := write(path, fileValues(custom), true); werr != nil {
			out.WriteErr = werr
			out.Set.warnings = append(out.Set.warnings, "could not add the comments to the file: "+werr.Error())
		}
		return out
	}
	if len(missing) > 0 {
		out.Updated = true
		if werr := addMissing(path, missing); werr != nil {
			out.WriteErr = werr
			out.Set.warnings = append(out.Set.warnings, "could not add the missing actions to the file: "+werr.Error())
		}
	}
	return out
}

// decodeKeys accepts a list of keys or a single string, so both
// ["c", "f5"] and "c" work.
func decodeKeys(raw json.RawMessage) ([]string, error) {
	var list []string
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, nil
	}
	var one string
	if err := json.Unmarshal(raw, &one); err == nil {
		return []string{one}, nil
	}
	return nil, errors.New("expected a key or a list of keys")
}

// CorruptError reports a key bindings file that cannot be used safely. Like
// the settings file, it is never overwritten by an ordinary save: the next
// start reports it again until it is fixed or removed.
type CorruptError struct {
	Path string
	Err  error
}

func (e *CorruptError) Error() string {
	return fmt.Sprintf("key bindings file %s is damaged or from a newer version: %v — using the defaults", e.Path, e.Err)
}

func (e *CorruptError) Unwrap() error { return e.Err }

// fileValues is what a rewritten file holds: every action's requested keys,
// the defaults for the ones the file leaves out.
func fileValues(custom map[string][]string) map[string][]string {
	out := make(map[string][]string, len(actions))
	for _, a := range Actions() {
		if keys, ok := custom[a.Name]; ok {
			out[a.Name] = keys
			continue
		}
		out[a.Name] = canonicalDefaults(a)
	}
	return out
}

// write replaces path atomically with a complete file for bindings. When
// checkExisting is true a damaged or newer file is left alone instead.
func write(path string, bindings map[string][]string, checkExisting bool) error {
	if checkExisting {
		data, err := os.ReadFile(path)
		if err == nil {
			var shape fileShape
			if jerr := json.Unmarshal(stripComments(data), &shape); jerr != nil {
				return fmt.Errorf("not updating a damaged file: %w", jerr)
			}
			if shape.Version > Version {
				return errors.New("not updating a file written by a newer TwinPane")
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("cannot create the configuration folder %s: %w", filepath.Dir(path), err)
	}
	return replace(path, marshalFile(bindings))
}

// writeKey is one line of the generated file: a blank line, a comment, or a
// binding with the comment above it.
type writeKey struct {
	name    string
	keys    []string
	comment string
}

// marshalFile renders a complete file: the header, then every action under a
// comment that says where the key is used and what it does, with a blank line
// between the groups. Writing it by hand keeps the order stable and readable,
// which encoding/json cannot promise for a map.
func marshalFile(bindings map[string][]string) []byte {
	var lines []writeKey
	section := ""
	for _, a := range Actions() {
		if a.Section != section {
			if section != "" {
				lines = append(lines, writeKey{}) // a blank line between groups
			}
			section = a.Section
		}
		keys, ok := bindings[a.Name]
		if !ok {
			keys = canonicalDefaults(a)
		}
		lines = append(lines, writeKey{name: a.Name, keys: keys, comment: entryComment(a)})
	}
	return assemble(lines)
}

// entryComment is the hint above one binding: where the key is used, then
// what it does.
func entryComment(a Action) string {
	text := strings.TrimSpace(a.hint())
	if text == "" {
		return upperFirst(SectionTitle(a.Section))
	}
	if !strings.HasSuffix(text, ".") && !strings.HasSuffix(text, "…") && !strings.HasSuffix(text, "?") {
		text += "."
	}
	return upperFirst(SectionTitle(a.Section)) + " — " + text
}

// assemble renders the header, the comments and the bindings object.
func assemble(lines []writeKey) []byte {
	var b strings.Builder
	b.WriteString("{\n")
	fmt.Fprintf(&b, "  \"version\": %d,\n", Version)
	fmt.Fprintf(&b, "  \"about\": %s,\n", quote(about))
	b.WriteString("  \"bindings\": {\n")
	for i, line := range lines {
		if line.comment != "" {
			b.WriteString("    // " + line.comment + "\n")
		}
		if line.name == "" {
			if line.comment == "" {
				b.WriteString("\n")
			}
			continue
		}
		fmt.Fprintf(&b, "    %s: %s", quote(line.name), quoteList(line.keys))
		if i < len(lines)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("  }\n}\n")
	return []byte(b.String())
}

// hasComments reports whether the file already carries comments. A file
// without any is refreshed with the documented form; one with comments (the
// generated hints or the user's own) is only ever appended to.
func hasComments(data []byte) bool { return len(stripComments(data)) != len(data) }

// addMissing appends the actions a file does not mention to its bindings
// object, comments included, leaving every existing line untouched.
func addMissing(path string, actions []Action) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(data)
	open, close, ok := bindingsBounds(text)
	if !ok {
		return errors.New("no bindings object found")
	}
	head, body, tail := text[:open+1], strings.TrimRight(text[open+1:close], " \t\r\n"), text[close:]
	// The last binding line may end with a comment: the comma that separates
	// it from the new entries has to go before that comment.
	if at := trailingComment(body); at >= 0 {
		body = strings.TrimRight(body[:at], " \t") + ", " + body[at:]
	} else if body != "" && !strings.HasSuffix(body, ",") {
		body += ","
	}
	var add strings.Builder
	for i, a := range actions {
		add.WriteString("\n    // " + entryComment(a) + "\n")
		fmt.Fprintf(&add, "    %s: %s", quote(a.Name), quoteList(canonicalDefaults(a)))
		if i < len(actions)-1 {
			add.WriteString(",")
		}
	}
	add.WriteString("\n")
	return replace(path, []byte(head+body+add.String()+tail))
}

// bindingsBounds finds the braces of the "bindings" object, skipping string
// literals and comments, so a missing action can be appended to its end.
func bindingsBounds(text string) (open, close int, ok bool) {
	key := strings.Index(text, `"bindings"`)
	if key < 0 {
		return 0, 0, false
	}
	open = strings.Index(text[key:], "{")
	if open < 0 {
		return 0, 0, false
	}
	open += key
	depth, inString, escaped := 0, false, false
	for i := open; i < len(text); i++ {
		c := text[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		switch {
		case c == '"':
			inString = true
		case c == '/' && i+1 < len(text) && text[i+1] == '/':
			for i < len(text) && text[i] != '\n' {
				i++
			}
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return open, i, true
			}
		}
	}
	return 0, 0, false
}

// trailingComment is the index of a // comment on the last line of body when
// that line also holds a binding, or -1 when the last line is a comment of its
// own (a comma can then simply follow it) or has no comment at all.
func trailingComment(body string) int {
	nl := strings.LastIndex(body, "\n")
	line := body[nl+1:]
	if strings.HasPrefix(strings.TrimSpace(line), "//") {
		return -1
	}
	c := strings.Index(line, "//")
	if c < 0 {
		return -1
	}
	return nl + 1 + c
}

// replace writes data over path atomically (temporary file in the same
// folder, 0600, fsync, rename).
func replace(path string, data []byte) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("key bindings path must be absolute: %s", path)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".keybinding-*.tmp")
	if err != nil {
		return fmt.Errorf("cannot write key bindings in %s: %w", dir, err)
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(name)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

// stripComments removes whole-line // comments and trailing // comments
// outside string literals, so the file can be read with the comments the
// generated file carries and a user can add their own.
func stripComments(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString, escaped := false, false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			out = append(out, c)
			switch {
			case escaped:
				escaped = false
			case c == '\\':
				escaped = true
			case c == '"':
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			out = append(out, c)
			continue
		}
		if c == '/' && i+1 < len(data) && data[i+1] == '/' {
			// Skip to the end of the line; the newline is kept below.
			for i < len(data) && data[i] != '\n' {
				i++
			}
			if i >= len(data) {
				break
			}
			out = append(out, '\n')
			continue
		}
		out = append(out, c)
	}
	return out
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func quote(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

func quoteList(list []string) string {
	parts := make([]string, 0, len(list))
	for _, k := range list {
		parts = append(parts, quote(k))
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
