package keybinding

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestRegistryDefaultsAreValid guards the table itself: every default key must
// be a key this terminal can report, and no section may bind one key twice by
// accident.
func TestRegistryDefaultsAreValid(t *testing.T) {
	used := map[string]map[string]string{}
	for _, a := range Actions() {
		if a.Name == "" || a.Section == "" || a.Desc == "" {
			t.Fatalf("action %+v is incomplete", a)
		}
		if !strings.HasPrefix(a.Name, a.Section+".") {
			t.Fatalf("%s does not start with its section %q", a.Name, a.Section)
		}
		if len(a.Keys) == 0 {
			t.Fatalf("%s has no default key", a.Name)
		}
		if used[a.Section] == nil {
			used[a.Section] = map[string]string{}
		}
		for _, k := range a.Keys {
			_, canonical, err := Parse(k)
			if err != nil {
				t.Fatalf("%s: %s", a.Name, err)
			}
			if prev, ok := used[a.Section][canonical]; ok {
				prevAction, _ := Lookup(prev)
				if prevAction.Shadow || a.Shadow {
					continue
				}
				t.Fatalf("%s and %s both bind %q in section %s", prev, a.Name, canonical, a.Section)
			}
			used[a.Section][canonical] = a.Name
		}
	}
}

func TestRegistryNamesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range Actions() {
		if seen[a.Name] {
			t.Fatalf("duplicate action %s", a.Name)
		}
		seen[a.Name] = true
	}
}

func TestParseAliases(t *testing.T) {
	cases := map[string]string{
		"Space":      " ",
		"space":      " ",
		"ESC":        "esc",
		"escape":     "esc",
		"Return":     "enter",
		"pageup":     "pgup",
		"PageDown":   "pgdown",
		"del":        "delete",
		"ins":        "insert",
		"^L":         "ctrl+l",
		"Ctrl+S":     "ctrl+s",
		"control+s":  "ctrl+s",
		"shift+tab":  "shift+tab",
		"backtab":    "shift+tab",
		"alt+left":   "alt+left",
		"Option+Up":  "alt+up",
		"f12":        "f12",
		"7":          "7",
		"A":          "A",
		"#":          "#",
		"alt+?":      "alt+?",
		"ctrl+space": "",
	}
	for in, want := range cases {
		_, got, err := Parse(in)
		if want == "" {
			if err == nil {
				t.Errorf("Parse(%q) should fail, got %q", in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("Parse(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("Parse(%q) = %q, want %q", in, got, want)
		}
	}
	for _, bad := range []string{"", "ctrl+shift+a", "meta+x", "super+k", "runes", "ctrl+", "alt+"} {
		if _, _, err := Parse(bad); err == nil {
			t.Errorf("Parse(%q) should fail", bad)
		}
	}
}

// TestParseRoundTrips checks that a parsed key produces the same message the
// terminal would deliver.
func TestParseRoundTrips(t *testing.T) {
	for _, s := range []string{"a", "A", "?", "#", " ", "ctrl+a", "alt+a", "alt+left", "shift+tab", "f5", "pgup"} {
		msg, canonical, err := Parse(s)
		if err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		if msg.String() != canonical {
			t.Fatalf("Parse(%q) message %q != canonical %q", s, msg.String(), canonical)
		}
	}
}

func TestPretty(t *testing.T) {
	cases := map[string]string{
		"up": "↑", "down": "↓", "left": "←", "right": "→",
		"enter": "⏎", "backspace": "⌫", " ": "Space", "pgup": "PgUp",
		"ctrl+l": "^L", "ctrl+s": "^S", "alt+left": "Alt+←", "f5": "F5", "c": "c",
	}
	for in, want := range cases {
		if got := Pretty(in); got != want {
			t.Errorf("Pretty(%q) = %q, want %q", in, got, want)
		}
	}
	if got := JoinPretty([]string{"c", "f5"}); got != "c/F5" {
		t.Fatalf("JoinPretty = %q", got)
	}
	if got := JoinPretty([]string{"up", "k"}); got != "↑/k" {
		t.Fatalf("JoinPretty = %q", got)
	}
}

func TestDefaultSet(t *testing.T) {
	s := Default()
	if len(s.Names()) == 0 {
		t.Fatal("no actions")
	}
	if len(s.Warnings()) != 0 {
		t.Fatalf("defaults warn: %v", s.Warnings())
	}
	if got := s.Label("browse.copy"); got != "c/F5" {
		t.Fatalf("copy label = %q", got)
	}
	if got := s.PrimaryLabel("browse.refresh"); got != "^L" {
		t.Fatalf("refresh label = %q", got)
	}
	if !s.Matches("browse.copy", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}}) {
		t.Fatal("c should copy")
	}
	if s.Matches("browse.copy", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}) {
		t.Fatal("y should not copy")
	}
	for _, name := range s.Names() {
		if !s.Enabled(name) {
			t.Errorf("%s is off by default", name)
		}
	}
	if b, _ := s.Binding("browse.copy"); !b.Default || b.Primary() != "c" {
		t.Fatalf("copy binding = %+v", b)
	}
}

func TestOverrides(t *testing.T) {
	s := New(map[string][]string{
		"browse.copy":   {"y"},
		"browse.select": {"space", "ins"},
		"browse.hidden": {},
	})
	if got := s.Label("browse.copy"); got != "y" {
		t.Fatalf("label = %q", got)
	}
	if !s.Custom("browse.copy") || s.Custom("browse.move") {
		t.Fatal("custom flags are wrong")
	}
	if !s.Matches("browse.copy", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}) {
		t.Fatal("y must copy after the override")
	}
	if s.Matches("browse.copy", tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}}) {
		t.Fatal("c must not copy after the override")
	}
	if got := s.Label("browse.select"); got != "Space/Ins" {
		t.Fatalf("select label = %q", got)
	}
	if s.Enabled("browse.hidden") {
		t.Fatal("an empty list disables the action")
	}
	if got := s.Describe("browse.hidden"); got != "browse.hidden: off" {
		t.Fatalf("describe = %q", got)
	}
}

func TestWarnings(t *testing.T) {
	s := New(map[string][]string{
		"nope.copy":   {"y"},
		"browse.copy": {"ctrl+shift+a", "y"},
		"browse.move": {"y"},
	})
	warnings := strings.Join(s.Warnings(), "\n")
	for _, want := range []string{"not an action", "ctrl+shift+a", "already used by browse.copy"} {
		if !strings.Contains(warnings, want) {
			t.Errorf("warnings lack %q:\n%s", want, warnings)
		}
	}
	if got := s.Label("browse.copy"); got != "y" {
		t.Fatalf("the good key must still apply, label = %q", got)
	}
	if s.Enabled("browse.move") {
		t.Fatal("a conflicting key must be dropped")
	}
}

func TestInvalidOnlyFallsBackToDefaults(t *testing.T) {
	s := New(map[string][]string{"browse.copy": {"ctrl+shift+a"}})
	if got := s.Label("browse.copy"); got != "c/F5" {
		t.Fatalf("label = %q, want the defaults", got)
	}
	if !strings.Contains(strings.Join(s.Warnings(), "\n"), "defaults are used") {
		t.Fatalf("missing fallback warning: %v", s.Warnings())
	}
}

func TestEssentialCannotBeUnbound(t *testing.T) {
	s := New(map[string][]string{"browse.quit": {}, "dialog.cancel": {"ctrl+shift+a"}})
	if !s.Enabled("browse.quit") || !s.Enabled("dialog.cancel") {
		t.Fatal("essential actions must stay reachable")
	}
	if got := s.Label("browse.quit"); got != "q" {
		t.Fatalf("quit label = %q", got)
	}
	if !strings.Contains(strings.Join(s.Warnings(), "\n"), "cannot be left without a key") {
		t.Fatalf("missing essential warning: %v", s.Warnings())
	}
}

func TestShadowedSectionKeys(t *testing.T) {
	// compare.retry shares r with compare.rightToLeft on purpose.
	s := Default()
	if got := s.Label("compare.retry"); got != "r" {
		t.Fatalf("retry label = %q", got)
	}
	if got := s.Label("compare.rightToLeft"); got != "r" {
		t.Fatalf("right-to-left label = %q", got)
	}
	if len(s.Warnings()) != 0 {
		t.Fatalf("shadowing must not warn: %v", s.Warnings())
	}
}

func TestSameListNotCustom(t *testing.T) {
	s := New(map[string][]string{"browse.copy": {"c", "f5"}})
	if s.Custom("browse.copy") {
		t.Fatal("writing the defaults back must not count as a customization")
	}
	if len(s.Diff()) != 0 {
		t.Fatalf("diff = %v", s.Diff())
	}
}

// --- the file --------------------------------------------------------------

func TestLoadMissingWritesEveryAction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "twinpane", FileName)
	l := Load(path)
	if l.Err != nil || !l.Created || l.WriteErr != nil {
		t.Fatalf("load = %+v", l)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var shape fileShape
	if err := json.Unmarshal(data, &shape); err != nil {
		t.Fatalf("generated file is not valid JSON: %v\n%s", err, data)
	}
	if shape.Version != Version {
		t.Fatalf("version = %d", shape.Version)
	}
	if len(shape.Bindings) != len(Actions()) {
		t.Fatalf("file has %d actions, registry has %d", len(shape.Bindings), len(Actions()))
	}
	if !strings.Contains(string(data), `"browse.copy": ["c", "f5"]`) {
		t.Fatalf("file lacks the copy binding:\n%s", data)
	}
	if !strings.Contains(string(data), `"viewer.wrap": ["w"]`) {
		t.Fatalf("file lacks a viewer binding:\n%s", data)
	}
	// The registry order gives the file one blank line per section.
	if !strings.Contains(string(data), "\n\n    \"dialog.cancel\"") {
		t.Fatalf("file has no section breaks:\n%s", data)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
}

func TestLoadOverridesAndReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if l := Load(path); l.Err != nil {
		t.Fatalf("first load: %v", l.Err)
	}
	data, _ := os.ReadFile(path)
	data = []byte(strings.Replace(string(data), `"browse.copy": ["c", "f5"]`, `"browse.copy": ["y"]`, 1))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	l := Load(path)
	if l.Err != nil || l.Created || l.Updated || l.WriteErr != nil {
		t.Fatalf("load = %+v", l)
	}
	if got := l.Set.Label("browse.copy"); got != "y" {
		t.Fatalf("label = %q", got)
	}
	if !l.Set.Custom("browse.copy") {
		t.Fatal("copy should count as customized")
	}
}

func TestLoadPartialFileAddsMissingActions(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	partial := `{"version":1,"bindings":{"browse.copy":["y"]}}`
	if err := os.WriteFile(path, []byte(partial), 0o600); err != nil {
		t.Fatal(err)
	}
	l := Load(path)
	if l.Err != nil || !l.Updated || l.WriteErr != nil {
		t.Fatalf("load = %+v", l)
	}
	if got := l.Set.Label("browse.copy"); got != "y" {
		t.Fatalf("label = %q", got)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"browse.copy": ["y"]`) {
		t.Fatalf("the user's value was lost:\n%s", data)
	}
	if !strings.Contains(string(data), `"viewer.wrap"`) {
		t.Fatalf("missing actions were not added:\n%s", data)
	}
	if l := Load(path); l.Updated {
		t.Fatal("a complete file must not be rewritten")
	}
}

func TestLoadSingleStringKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	one := `{"version":1,"bindings":{"browse.copy":"y"}}`
	if err := os.WriteFile(path, []byte(one), 0o600); err != nil {
		t.Fatal(err)
	}
	if l := Load(path); l.Err != nil || l.Set.Label("browse.copy") != "y" {
		t.Fatalf("load = %+v, label %q", l, l.Set.Label("browse.copy"))
	}
}

func TestLoadDamagedKeepsDefaultsAndNeverOverwrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	broken := `{"version":1,"bindings":{"browse.copy":`
	if err := os.WriteFile(path, []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	l := Load(path)
	if l.Err == nil || l.Created || l.Updated || l.WriteErr != nil {
		t.Fatalf("load = %+v", l)
	}
	if got := l.Set.Label("browse.copy"); got != "c/F5" {
		t.Fatalf("label = %q, want the defaults", got)
	}
	if !strings.Contains(l.Err.Error(), "damaged") {
		t.Fatalf("error = %v", l.Err)
	}
	data, _ := os.ReadFile(path)
	if string(data) != broken {
		t.Fatalf("damaged file was rewritten:\n%s", data)
	}
}

func TestLoadNewerVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	newer := `{"version":99,"bindings":{}}`
	if err := os.WriteFile(path, []byte(newer), 0o600); err != nil {
		t.Fatal(err)
	}
	l := Load(path)
	if l.Err == nil || !strings.Contains(l.Err.Error(), "newer") {
		t.Fatalf("load = %+v", l)
	}
	data, _ := os.ReadFile(path)
	if string(data) != newer {
		t.Fatal("newer file was rewritten")
	}
}

func TestLoadWrongTypeIsReported(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	bad := `{"version":1,"bindings":{"browse.copy":42,"browse.move":["m"]}}`
	if err := os.WriteFile(path, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	l := Load(path)
	if l.Err != nil {
		t.Fatalf("one bad entry must not fail the file: %v", l.Err)
	}
	if l.Set.Label("browse.copy") != "c/F5" || l.Set.Label("browse.move") != "m" {
		t.Fatalf("labels: copy %q move %q", l.Set.Label("browse.copy"), l.Set.Label("browse.move"))
	}
	if !strings.Contains(strings.Join(l.Set.Warnings(), "\n"), "expected a key") {
		t.Fatalf("missing warning: %v", l.Set.Warnings())
	}
}

func TestLoadEmptyFileWritesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	l := Load(path)
	if l.Err != nil || !l.Created || l.WriteErr != nil {
		t.Fatalf("load = %+v", l)
	}
	data, _ := os.ReadFile(path)
	if !strings.Contains(string(data), `"browse.quit"`) {
		t.Fatalf("file was not filled:\n%s", data)
	}
}

func TestLoadNoPath(t *testing.T) {
	l := Load("")
	if !l.Missing || l.Err != nil || l.Set == nil || !l.Set.Enabled("browse.copy") {
		t.Fatalf("load = %+v", l)
	}
}

func TestLoadRelativePath(t *testing.T) {
	l := Load("keybinding.json")
	if l.Err != nil || l.WriteErr == nil {
		t.Fatalf("a relative path must not be written: %+v", l)
	}
}
