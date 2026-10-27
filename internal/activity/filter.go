package activity

import (
	"strings"
)

// Viewing the log: the viewer has one log and several ways to narrow it. A
// category is a filter, never a separate log — the entries are all in the same
// file, written by the same calls, and switching filter only changes what is
// listed.

// CatFilter is a category filter offered by the viewer.
type CatFilter int

const (
	FilterAll CatFilter = iota
	FilterCommands
	FilterFileOps
	FilterNavigation
	FilterUI
	FilterTasks
	FilterCloud
	FilterWarnings
	FilterSettings
	FilterRecovery
)

// CatFilters lists the filters in the viewer's order.
var CatFilters = []CatFilter{
	FilterAll, FilterCommands, FilterFileOps, FilterNavigation, FilterUI,
	FilterTasks, FilterCloud, FilterWarnings, FilterSettings, FilterRecovery,
}

// Label is the name shown in the viewer's header.
func (c CatFilter) Label() string {
	switch c {
	case FilterAll:
		return "All Activity"
	case FilterCommands:
		return "Commands"
	case FilterFileOps:
		return "File Operations"
	case FilterNavigation:
		return "Navigation"
	case FilterUI:
		return "UI Actions"
	case FilterTasks:
		return "Tasks"
	case FilterCloud:
		return "Cloud"
	case FilterWarnings:
		return "Warnings & Errors"
	case FilterSettings:
		return "Settings"
	case FilterRecovery:
		return "Recovery"
	}
	return "All Activity"
}

// Key is a stable identifier (used by tests and by the status line).
func (c CatFilter) Key() string {
	switch c {
	case FilterAll:
		return "all"
	case FilterCommands:
		return "cmd"
	case FilterFileOps:
		return "op"
	case FilterNavigation:
		return "nav"
	case FilterUI:
		return "ui"
	case FilterTasks:
		return "task"
	case FilterCloud:
		return "cloud"
	case FilterWarnings:
		return "warn"
	case FilterSettings:
		return "settings"
	case FilterRecovery:
		return "recovery"
	}
	return "all"
}

// Next cycles through the filters, wrapping in both directions.
func (c CatFilter) Next(delta int) CatFilter {
	i := 0
	for j, f := range CatFilters {
		if f == c {
			i = j
		}
	}
	n := len(CatFilters)
	i = ((i+delta)%n + n) % n
	return CatFilters[i]
}

// Category is the entry category this filter selects, and whether it selects
// one at all.
func (c CatFilter) Category() (Category, bool) {
	switch c {
	case FilterCommands:
		return CatCommand, true
	case FilterFileOps:
		return CatOperation, true
	case FilterNavigation:
		return CatNavigation, true
	case FilterUI:
		return CatUI, true
	case FilterTasks:
		return CatTask, true
	case FilterCloud:
		return CatCloud, true
	case FilterSettings:
		return CatSettings, true
	case FilterRecovery:
		return CatRecovery, true
	}
	return "", false
}

// Match reports whether an entry belongs to this filter. "Warnings & Errors"
// cuts across categories, which is the point of having it.
func (c CatFilter) Match(e Entry) bool {
	if c == FilterAll {
		return true
	}
	if c == FilterWarnings {
		l := e.LevelOf()
		return l == LevelWarn || l == LevelError
	}
	cat, ok := c.Category()
	return ok && e.Cat == cat
}

// LevelFilter narrows by severity (the `l` key).
type LevelFilter int

const (
	LevelsAll LevelFilter = iota
	LevelsInfo
	LevelsWarn
	LevelsError
)

// LevelFilters is the cycle for the `l` key.
var LevelFilters = []LevelFilter{LevelsAll, LevelsInfo, LevelsWarn, LevelsError}

// Label names the filter.
func (l LevelFilter) Label() string {
	switch l {
	case LevelsAll:
		return "all levels"
	case LevelsInfo:
		return "info and up"
	case LevelsWarn:
		return "warnings and up"
	case LevelsError:
		return "errors only"
	}
	return "all levels"
}

// Min is the lowest level this filter keeps.
func (l LevelFilter) Min() Level {
	switch l {
	case LevelsInfo:
		return LevelInfo
	case LevelsWarn:
		return LevelWarn
	case LevelsError:
		return LevelError
	}
	return LevelDebug
}

// Next cycles the level filter.
func (l LevelFilter) Next(delta int) LevelFilter {
	i := 0
	for j, f := range LevelFilters {
		if f == l {
			i = j
		}
	}
	n := len(LevelFilters)
	i = ((i+delta)%n + n) % n
	return LevelFilters[i]
}

// Match reports whether an entry passes this level filter.
func (l LevelFilter) Match(e Entry) bool { return e.LevelOf() >= l.Min() }

// Filter is the viewer's whole selection: the category filter, the level
// filter and the free-text query.
type Filter struct {
	Cat   CatFilter
	Level LevelFilter
	Text  string
	// EvPrefix restricts by event name, from `ev:` in a query.
	EvPrefix string
}

// Match reports whether e is shown.
func (f Filter) Match(e Entry) bool {
	if !f.Cat.Match(e) || !f.Level.Match(e) {
		return false
	}
	if f.EvPrefix != "" && !strings.HasPrefix(strings.ToLower(e.Ev), f.EvPrefix) {
		return false
	}
	if f.Text == "" {
		return true
	}
	hay := strings.ToLower(strings.Join([]string{e.Ev, e.Msg, e.Err, string(e.Cat), e.Op, e.Kind, e.Status}, " "))
	return strings.Contains(hay, f.Text)
}

// Empty reports whether the filter shows everything.
func (f Filter) Empty() bool {
	return f.Cat == FilterAll && f.Level == LevelsAll && f.Text == "" && f.EvPrefix == ""
}

// Describe is the one-line summary shown under the header. It is empty when
// nothing is filtered.
func (f Filter) Describe() string {
	var parts []string
	if f.Cat != FilterAll {
		parts = append(parts, f.Cat.Label())
	}
	if f.Level != LevelsAll {
		parts = append(parts, f.Level.Label())
	}
	if f.EvPrefix != "" {
		parts = append(parts, "ev:"+f.EvPrefix)
	}
	if f.Text != "" {
		parts = append(parts, `"`+f.Text+`"`)
	}
	return strings.Join(parts, " · ")
}

// ApplyQuery parses a typed query on top of the current filter and returns it.
// Supported tokens, in any order, all of them optional:
//
//	cat:cmd | cat:cloud | cat:all      restrict to one category
//	lvl:warn | lvl:error | lvl:info    restrict to a level and above
//	ev:command.done                    restrict by event name (prefix allowed)
//	anything else                      a case-insensitive substring
//
// A token whose value is not recognised becomes part of the text, so a typo
// narrows the list instead of silently showing everything. Text is capped:
// this is a filter for the eye, not a search language.
func (f Filter) ApplyQuery(q string) Filter {
	out := Filter{}
	for _, tok := range strings.Fields(q) {
		lower := strings.ToLower(tok)
		switch {
		case strings.HasPrefix(lower, "cat:"):
			if c, ok := parseCat(lower[4:]); ok {
				out.Cat = c
				continue
			}
		case strings.HasPrefix(lower, "lvl:"), strings.HasPrefix(lower, "level:"):
			v := lower[strings.IndexByte(lower, ':')+1:]
			if l, ok := parseLevelFilter(v); ok {
				out.Level = l
				continue
			}
		case strings.HasPrefix(lower, "ev:"):
			if v := lower[3:]; v != "" {
				out.EvPrefix = v
				continue
			}
		}
		if out.Text != "" {
			out.Text += " "
		}
		out.Text += lower
	}
	if len(out.Text) > 80 {
		out.Text = out.Text[:80]
	}
	return out
}

// parseCat accepts a category key, its short name or its label.
func parseCat(v string) (CatFilter, bool) {
	switch v {
	case "all", "any", "":
		return FilterAll, true
	case "warn", "warnings", "errors", "error", "problem", "problems":
		return FilterWarnings, true
	}
	for _, f := range CatFilters {
		if v == f.Key() || strings.EqualFold(v, f.Label()) {
			return f, true
		}
	}
	for _, c := range Categories {
		if v == string(c) || v == c.Short() {
			for _, f := range CatFilters {
				if cat, ok := f.Category(); ok && cat == c {
					return f, true
				}
			}
		}
	}
	return FilterAll, false
}

// parseLevelFilter accepts a level name, meaning "this level and above".
func parseLevelFilter(v string) (LevelFilter, bool) {
	switch strings.TrimSuffix(v, "+") {
	case "all", "debug", "any":
		return LevelsAll, true
	case "info":
		return LevelsInfo, true
	case "warn", "warning", "warnings":
		return LevelsWarn, true
	case "error", "errors":
		return LevelsError, true
	}
	return LevelsAll, false
}
