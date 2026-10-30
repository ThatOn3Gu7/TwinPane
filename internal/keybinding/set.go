package keybinding

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Binding is one resolved action: the registry entry plus the keys this
// session actually uses (after the file's overrides and conflicts).
type Binding struct {
	Name    string   // "browse.copy"
	Section string   // "browse"
	Desc    string   // "copy to the other panel"
	Keys    []string // canonical key strings, in display order
	Default bool     // the keys are the registry defaults
	// Fixed is the compact help label used while the action is unchanged. A
	// customized action is labelled from its real keys instead.
	Fixed string
}

// Label renders the action's keys, e.g. "c/F5". While the action still has
// its defaults the registry's compact label is used, so the help screen keeps
// its familiar shorthand; once a key is changed the label follows the keys.
func (b Binding) Label() string {
	if b.Fixed != "" {
		return b.Fixed
	}
	return JoinPretty(b.Keys)
}

// Primary is the first key: the one the hint bar and the action menu send when
// they are tapped.
func (b Binding) Primary() string {
	if len(b.Keys) == 0 {
		return ""
	}
	return b.Keys[0]
}

// PrimaryLabel renders the first key, e.g. "^L".
func (b Binding) PrimaryLabel() string { return Pretty(b.Primary()) }

// Enabled reports whether the action has at least one key.
func (b Binding) Enabled() bool { return len(b.Keys) > 0 }

// Msg returns the message the primary key produces. It is how a tapped hint
// re-sends a shortcut through the ordinary dispatch path.
func (b Binding) Msg() tea.KeyMsg {
	if !b.Enabled() {
		return tea.KeyMsg{}
	}
	msg, _, err := Parse(b.Primary())
	if err != nil {
		return tea.KeyMsg{}
	}
	return msg
}

// Set is the resolved key map of one session.
type Set struct {
	bindings map[string]Binding
	order    []string // resolved actions in registry order
	custom   map[string]bool
	warnings []string
}

// New builds a set from explicit keys per action. Missing actions keep their
// defaults; an empty list disables an action; unknown names and unusable keys
// are reported as warnings. It is what the loader uses and what tests use
// instead of a file.
func New(custom map[string][]string) *Set {
	s := &Set{bindings: map[string]Binding{}, custom: map[string]bool{}}
	warn := func(format string, args ...any) {
		s.warnings = append(s.warnings, fmt.Sprintf(format, args...))
	}

	// Unknown names first, in a stable order.
	names := make([]string, 0, len(custom))
	for name := range custom {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, ok := Lookup(name); !ok {
			warn("%q is not an action (ignored)", name)
		}
	}

	for _, a := range Actions() {
		keys := make([]string, 0, len(a.Keys))
		seen := map[string]bool{}
		for _, k := range a.Keys {
			_, c, err := Parse(k)
			if err != nil {
				continue // the registry itself is tested to contain valid keys
			}
			if seen[c] {
				continue
			}
			seen[c] = true
			keys = append(keys, c)
		}
		raw, ok := custom[a.Name]
		if ok {
			resolved := make([]string, 0, len(raw))
			seen = map[string]bool{}
			for _, given := range raw {
				_, c, err := Parse(given)
				if err != nil {
					warn("%s: %s", a.Name, err.Error())
					continue
				}
				if seen[c] {
					continue
				}
				seen[c] = true
				resolved = append(resolved, c)
			}
			switch {
			case len(resolved) == 0 && len(raw) == 0:
				// An empty list is the deliberate way to disable an action.
				keys = nil
			case len(resolved) == 0:
				warn("%s: no usable key, the defaults are used", a.Name)
			default:
				keys = resolved
			}
			s.custom[a.Name] = !sameList(keys, canonicalDefaults(a))
		}
		s.bindings[a.Name] = Binding{Name: a.Name, Section: a.Section, Desc: a.Desc, Keys: keys,
			Default: !s.custom[a.Name], Fixed: fixedLabel(a, s.custom[a.Name])}
		s.order = append(s.order, a.Name)
	}

	// Conflicts: one key, one action per section. A shadowing action opts out:
	// its keys are only consulted in a context the other action returns from
	// earlier (compare.retry).
	used := map[string]map[string]string{}
	for _, name := range s.order {
		b := s.bindings[name]
		action, _ := Lookup(name)
		keys := b.Keys[:0:0]
		seen := map[string]bool{}
		for _, k := range b.Keys {
			if seen[k] {
				continue
			}
			seen[k] = true
			if used[b.Section] == nil {
				used[b.Section] = map[string]string{}
			}
			if other, taken := used[b.Section][k]; taken && other != name {
				otherAction, _ := Lookup(other)
				if action.Shadow || otherAction.Shadow {
					keys = append(keys, k) // both keep it; context decides
					continue
				}
				warn("%s: %q is already used by %s, so it was ignored", name, Pretty(k), other)
				continue
			}
			used[b.Section][k] = name
			keys = append(keys, k)
		}
		if len(keys) == 0 && len(b.Keys) > 0 {
			warn("%s: every key it asked for is used elsewhere, so it is off", name)
		}
		b.Keys = keys
		b.Default = !s.custom[name]
		b.Fixed = fixedLabel(action, s.custom[name])
		s.bindings[name] = b
	}

	// Essential actions must stay reachable.
	for _, name := range s.order {
		b := s.bindings[name]
		action, _ := Lookup(name)
		if !b.Enabled() && action.Essential {
			b.Keys = canonicalDefaults(action)
			b.Default = true
			b.Fixed = fixedLabel(action, false)
			s.bindings[name] = b
			warn("%s cannot be left without a key; its defaults are used", name)
		}
	}
	return s
}

// Default returns the registry defaults.
func Default() *Set { return New(nil) }

func fixedLabel(a Action, customized bool) string {
	if customized {
		return ""
	}
	return a.Label
}

func canonicalDefaults(a Action) []string {
	out := make([]string, 0, len(a.Keys))
	for _, k := range a.Keys {
		if _, c, err := Parse(k); err == nil {
			out = append(out, c)
		}
	}
	return out
}

func sameList(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// --- accessors -------------------------------------------------------------

// Names lists every action in registry order.
func (s *Set) Names() []string { return append([]string(nil), s.order...) }

// Binding returns one action's resolved binding.
func (s *Set) Binding(name string) (Binding, bool) {
	b, ok := s.bindings[name]
	return b, ok
}

// Keys returns an action's canonical keys.
func (s *Set) Keys(name string) []string {
	b, ok := s.bindings[name]
	if !ok {
		return nil
	}
	return append([]string(nil), b.Keys...)
}

// Label renders an action's keys ("c/F5").
func (s *Set) Label(name string) string {
	b, ok := s.bindings[name]
	if !ok {
		return ""
	}
	return b.Label()
}

// PrimaryLabel renders an action's first key ("c").
func (s *Set) PrimaryLabel(name string) string {
	b, ok := s.bindings[name]
	if !ok {
		return ""
	}
	return b.PrimaryLabel()
}

// Primary returns an action's first key as a canonical string.
func (s *Set) Primary(name string) string {
	b, ok := s.bindings[name]
	if !ok {
		return ""
	}
	return b.Primary()
}

// Msg returns the message of an action's first key (a zero message when the
// action is disabled or unknown).
func (s *Set) Msg(name string) tea.KeyMsg {
	b, ok := s.bindings[name]
	if !ok {
		return tea.KeyMsg{}
	}
	return b.Msg()
}

// Matches reports whether msg is one of the action's keys.
func (s *Set) Matches(name string, msg tea.KeyMsg) bool {
	b, ok := s.bindings[name]
	if !ok || len(b.Keys) == 0 {
		return false
	}
	str := msg.String()
	for _, k := range b.Keys {
		if k == str {
			return true
		}
	}
	return false
}

// Enabled reports whether the action has a key.
func (s *Set) Enabled(name string) bool {
	b, ok := s.bindings[name]
	return ok && b.Enabled()
}

// Custom reports whether the user's file changed this action.
func (s *Set) Custom(name string) bool { return s.custom[name] }

// Warnings lists everything wrong with the file that was loaded, in the order
// it was found. No warning is fatal: the good entries always apply.
func (s *Set) Warnings() []string { return append([]string(nil), s.warnings...) }

// FileBindings returns every action's keys keyed by action name, in registry
// order, for writing keybinding.json.
func (s *Set) FileBindings() map[string][]string {
	out := make(map[string][]string, len(s.order))
	for _, name := range s.order {
		b := s.bindings[name]
		out[name] = append([]string(nil), b.Keys...)
	}
	return out
}

// Diff returns the actions whose keys differ from the defaults, for the
// Settings screen and the activity log.
func (s *Set) Diff() []string {
	var out []string
	for _, name := range s.order {
		if s.custom[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// Describe renders a binding for the Settings screen ("browse.copy: y").
func (s *Set) Describe(name string) string {
	b, ok := s.bindings[name]
	if !ok {
		return name
	}
	if !b.Enabled() {
		return name + ": off"
	}
	return name + ": " + b.Label()
}

// String lists every binding, used by tests and the activity log.
func (s *Set) String() string {
	parts := make([]string, 0, len(s.order))
	for _, name := range s.order {
		parts = append(parts, s.Describe(name))
	}
	return strings.Join(parts, ", ")
}
