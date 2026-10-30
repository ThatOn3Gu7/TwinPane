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

const about = "TwinPane key bindings. Every action is listed with its current keys: " +
	"change a key here, then reopen TwinPane or choose Settings (F9) and press Enter on " +
	"\u201cKey bindings\u201d to reload. Several keys per action are allowed; [ ] disables an action. " +
	"Key names: letters as written (A and a differ), ctrl+x, alt+x, f1-f20, up, down, left, " +
	"right, home, end, pgup, pgdown, enter, tab, shift+tab, esc, space, backspace, delete, insert."

// Loaded is the outcome of reading keybinding.json. Set is always usable: a
// missing or damaged file yields the defaults, and the warnings explain what
// was ignored.
type Loaded struct {
	Path     string
	Set      *Set
	Err      error // the file could not be used at all (damaged, newer, unreadable)
	Created  bool  // the file did not exist and was written with the defaults
	Updated  bool  // actions missing from the file were added to it
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
	if len(strings.TrimSpace(string(data))) == 0 {
		out.Created = true
		if werr := write(path, out.Set.FileBindings(), false); werr != nil {
			out.WriteErr = werr
		}
		return out
	}
	var shape fileShape
	if err := json.Unmarshal(data, &shape); err != nil {
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
	for name, raw := range shape.Bindings {
		keys, err := decodeKeys(raw)
		if err != nil {
			extra = append(extra, fmt.Sprintf("%q: %s", name, err.Error()))
			continue
		}
		custom[name] = keys
	}
	out.Set = New(custom)
	out.Set.warnings = append(extra, out.Set.warnings...)
	// A file that does not mention every action still works (missing means the
	// default), but the promise of the file is that you can see every binding.
	missing := false
	for _, name := range out.Set.Names() {
		if _, ok := shape.Bindings[name]; !ok {
			missing = true
			break
		}
	}
	// A malformed entry is never replaced automatically: the file may hold a
	// value the user is still writing. It is reported instead.
	if missing && len(extra) == 0 {
		out.Updated = true
		if werr := write(path, out.Set.FileBindings(), true); werr != nil {
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

// write replaces path atomically with the bindings, in registry order. When
// checkExisting is true a damaged or newer file is left alone instead.
func write(path string, bindings map[string][]string, checkExisting bool) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("key bindings path must be absolute: %s", path)
	}
	if checkExisting {
		data, err := os.ReadFile(path)
		if err == nil {
			var shape fileShape
			if jerr := json.Unmarshal(data, &shape); jerr != nil {
				return fmt.Errorf("not updating a damaged file: %w", jerr)
			}
			if shape.Version > Version {
				return errors.New("not updating a file written by a newer TwinPane")
			}
		}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("cannot create the configuration folder %s: %w", dir, err)
	}
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
	if _, err := tmp.Write(marshal(bindings)); err != nil {
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

// writeKey is one line of the generated file.
type writeKey struct {
	name string
	keys []string
}

// marshal renders the file: one action per line, a blank line between
// sections, in registry order. Writing it by hand keeps the order stable and
// readable, which encoding/json cannot promise for a map.
func marshal(bindings map[string][]string) []byte {
	var b strings.Builder
	b.WriteString("{\n")
	fmt.Fprintf(&b, "  \"version\": %d,\n", Version)
	fmt.Fprintf(&b, "  \"about\": %s,\n", quote(about))
	b.WriteString("  \"bindings\": {\n")
	var lines []writeKey
	section := ""
	for _, a := range Actions() {
		if a.Section != section {
			if section != "" {
				lines = append(lines, writeKey{})
			}
			section = a.Section
		}
		keys, ok := bindings[a.Name]
		if !ok {
			keys = canonicalDefaults(a)
		}
		lines = append(lines, writeKey{name: a.Name, keys: keys})
	}
	for i, line := range lines {
		if line.name == "" {
			b.WriteString("\n")
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
