// Package activity keeps the User Activity Log: a persistent, structured
// timeline of what TwinPane did, for debugging and troubleshooting.
//
// It complements internal/history and does not duplicate it. The operation
// history answers "what happened to my files" and is a browsable record of
// file operations in the Tasks screen. This log answers "what was the program
// doing": navigation, dialogs, menus, keyboard shortcuts, screen switches,
// external commands (rclone, 7-Zip, the editor) with their exit codes, task
// lifecycle, cloud failures, settings changes and recovery events — each one
// timestamped, levelled and tagged with a category.
//
// Design rules, all enforced here rather than at the call sites:
//
//   - Append-only NDJSON. One entry is one write(2) to a file opened with
//     O_APPEND, so lines stay intact even when two TwinPane instances log at
//     once and a crash can lose at most the entry being written. There is no
//     fsync per entry: this is diagnostics, not a record of truth, and paying
//     for durability would slow real work down.
//   - Bounded: the file rotates at MaxBytes into activity.1.jsonl … keeping a
//     few files, and rotated files older than KeepDays are removed at start.
//   - Malformed lines are skipped and counted, never fatal.
//   - Everything written is passed through Redact first: passwords, tokens,
//     keys, URL credentials, Authorization headers and secret command-line
//     flags are replaced before they can reach the file.
//   - High-frequency events are not logged at all (progress updates, cursor
//     movement, per-frame work). Levels decide the rest: verbose is opt-in.
package activity

import (
	"fmt"
	"strings"
	"time"
)

const (
	// FileName is the log file in TwinPane's configuration directory.
	FileName = "activity.jsonl"

	// formatVersion is the per-line format. A line from a newer TwinPane is
	// still readable (unknown fields are ignored), so there is no read-only
	// mode as with history.json.
	formatVersion = 1

	// DefaultMaxBytes is when the current file is rotated.
	DefaultMaxBytes = 1 << 20 // 1 MiB
	// MinMaxBytes and MaxMaxBytes bound the configurable size.
	MinMaxBytes = 64 << 10 // 64 KiB
	MaxMaxBytes = 16 << 20 // 16 MiB

	// DefaultKeep is how many rotated files are kept beside the current one.
	DefaultKeep = 3
	// KeepLimit is the hard upper bound for Keep.
	KeepLimit = 9

	// DefaultKeepDays removes rotated files older than this at start.
	DefaultKeepDays = 30

	// MaxEntryBytes caps one encoded line; longer messages are truncated.
	MaxEntryBytes = 2 << 10

	// Caps that keep one entry small.
	maxMsg    = 300
	maxErr    = 500
	maxData   = 8
	maxKey    = 32
	maxVal    = 200
	maxEvents = 8000 // entries the viewer loads
)

// Level is the severity of an entry.
type Level int

const (
	// LevelDebug is verbose diagnostics (individual shortcuts and other
	// high-volume detail); only written in verbose mode.
	LevelDebug Level = iota
	// LevelInfo is a normal action (navigation, a dialog, a command).
	LevelInfo
	// LevelWarn is something the user was warned about (a refusal, a
	// fallback, an operation that only partly worked).
	LevelWarn
	// LevelError is a failure the user was told about.
	LevelError
)

// String is the short name stored in the file.
func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "debug"
	case LevelInfo:
		return "info"
	case LevelWarn:
		return "warn"
	case LevelError:
		return "error"
	}
	return "info"
}

// Label is the upper-case name shown in the viewer.
func (l Level) Label() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	}
	return "INFO"
}

// ParseLevel reads a stored level name ("info" when unknown).
func ParseLevel(s string) Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LevelDebug
	case "warn", "warning":
		return LevelWarn
	case "error":
		return LevelError
	}
	return LevelInfo
}

// Mode is how much is recorded.
type Mode int

const (
	// ModeOff disables the log completely: nothing is written.
	ModeOff Mode = iota
	// ModeWarnings keeps warnings and errors only.
	ModeWarnings
	// ModeActions keeps every action and all warnings (the default).
	ModeActions
	// ModeVerbose adds diagnostics: shortcuts, command arguments in full,
	// tool output tails.
	ModeVerbose
)

// String is the settings value.
func (m Mode) String() string {
	switch m {
	case ModeOff:
		return "off"
	case ModeWarnings:
		return "warnings"
	case ModeVerbose:
		return "verbose"
	}
	return "actions"
}

// Label is the human name shown in Settings and the viewer.
func (m Mode) Label() string {
	switch m {
	case ModeOff:
		return "off"
	case ModeWarnings:
		return "warnings only"
	case ModeVerbose:
		return "verbose"
	}
	return "actions"
}

// ParseMode reads a settings value; anything unknown means the default, so a
// hand-edited file can never switch logging off (or to verbose) by accident.
func ParseMode(s string) Mode {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "off":
		return ModeOff
	case "warnings":
		return ModeWarnings
	case "verbose":
		return ModeVerbose
	}
	return ModeActions
}

// Next returns the next mode, for the Settings row.
func (m Mode) Next(delta int) Mode {
	order := []Mode{ModeOff, ModeWarnings, ModeActions, ModeVerbose}
	i := 0
	for j, o := range order {
		if o == m {
			i = j
		}
	}
	i = (i + delta + len(order)) % len(order)
	return order[i]
}

// minLevel is the lowest level this mode writes.
func (m Mode) minLevel() (Level, bool) {
	switch m {
	case ModeOff:
		return LevelError, false
	case ModeWarnings:
		return LevelWarn, true
	case ModeVerbose:
		return LevelDebug, true
	}
	return LevelInfo, true
}

// Category groups entries for the viewer's filters. The categories are filters
// over the same log, never separate logs.
type Category string

const (
	// CatCommand is an external program TwinPane ran (rclone, 7-Zip, the
	// editor, an opener, a search tool) — not a keyboard shortcut.
	CatCommand Category = "cmd"
	// CatOperation is file work: create, copy, move, rename, delete.
	CatOperation Category = "op"
	// CatNavigation is a directory change, a remote or archive opening, or
	// back/forward.
	CatNavigation Category = "nav"
	// CatUI is an action in the interface: menus, dialogs, screens, keys.
	CatUI Category = "ui"
	// CatTask is a background/foreground task's lifecycle and prompts.
	CatTask Category = "task"
	// CatCloud is rclone-backed navigation and remote operations.
	CatCloud Category = "cloud"
	// CatSettings is a preference change.
	CatSettings Category = "settings"
	// CatRecovery is application lifecycle, damaged files, fallbacks and
	// anything the program recovered from.
	CatRecovery Category = "recovery"
)

// Categories lists them in viewer order.
var Categories = []Category{
	CatCommand, CatOperation, CatNavigation, CatUI, CatTask, CatCloud, CatSettings, CatRecovery,
}

// Label is the name shown in the viewer's filter.
func (c Category) Label() string {
	switch c {
	case CatCommand:
		return "Commands"
	case CatOperation:
		return "File Operations"
	case CatNavigation:
		return "Navigation"
	case CatUI:
		return "UI Actions"
	case CatTask:
		return "Tasks"
	case CatCloud:
		return "Cloud"
	case CatSettings:
		return "Settings"
	case CatRecovery:
		return "Recovery"
	}
	return "Other"
}

// Valid reports whether c is a known category.
func (c Category) Valid() bool {
	for _, k := range Categories {
		if k == c {
			return true
		}
	}
	return false
}

// Short is the fixed-width abbreviation used in a table row.
func (c Category) Short() string {
	switch c {
	case CatCommand:
		return "cmd"
	case CatOperation:
		return "file"
	case CatNavigation:
		return "nav"
	case CatUI:
		return "ui"
	case CatTask:
		return "task"
	case CatCloud:
		return "cloud"
	case CatSettings:
		return "config"
	case CatRecovery:
		return "recov"
	}
	return string(c)
}

// Entry is one line of the log.
type Entry struct {
	V      int               `json:"v"`
	Time   time.Time         `json:"ts"`
	Run    string            `json:"run,omitempty"`
	Seq    uint64            `json:"seq,omitempty"`
	Level  string            `json:"lvl"`
	Cat    Category          `json:"cat"`
	Ev     string            `json:"ev"`
	Msg    string            `json:"msg,omitempty"`
	Panel  string            `json:"panel,omitempty"`
	Op     string            `json:"op,omitempty"`
	Status string            `json:"status,omitempty"`
	DurMS  int64             `json:"dur_ms,omitempty"`
	Code   int               `json:"code,omitempty"`
	Kind   string            `json:"kind,omitempty"`
	Err    string            `json:"err,omitempty"`
	Data   map[string]string `json:"data,omitempty"`
}

// LevelOf returns the entry's parsed level.
func (e Entry) LevelOf() Level { return ParseLevel(e.Level) }

// Failed reports whether the entry records a failure.
func (e Entry) Failed() bool { return e.LevelOf() == LevelError }

// Summary is the one-line text the viewer and lists show.
func (e Entry) Summary() string {
	if e.Msg != "" {
		return e.Msg
	}
	if e.Err != "" {
		return e.Err
	}
	return e.Ev
}

// sanitizeEntry applies every cap and the redactor. It runs inside Append, so
// no path into the file can skip it.
func sanitizeEntry(e Entry) Entry {
	e.V = formatVersion
	if e.Level == "" {
		e.Level = LevelInfo.String()
	}
	if !e.Cat.Valid() {
		e.Cat = CatUI
	}
	e.Ev = clip(Redact(e.Ev), 60)
	e.Msg = clip(Redact(e.Msg), maxMsg)
	e.Err = clip(Redact(e.Err), maxErr)
	e.Panel = clip(Redact(e.Panel), 8)
	e.Op = clip(Redact(e.Op), 40)
	e.Status = clip(Redact(e.Status), 24)
	e.Kind = clip(Redact(e.Kind), 32)
	if len(e.Data) > 0 {
		out := make(map[string]string, min(len(e.Data), maxData))
		n := 0
		for k, v := range e.Data {
			if n == maxData {
				break
			}
			k = clip(Redact(k), maxKey)
			if k == "" {
				continue
			}
			if IsSecretKeyName(k) {
				out[k] = redacted
			} else {
				out[k] = clip(Redact(v), maxVal)
			}
			n++
		}
		if len(out) == 0 {
			out = nil
		}
		e.Data = out
	}
	return e
}

// clip shortens s to at most n bytes on a rune boundary.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return ""
	}
	for n > 0 && !utf8Start(s[n]) {
		n--
	}
	return s[:n] + "…"
}

// utf8Start reports whether b starts a rune.
func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// Stat is a snapshot's shape, shown in the viewer's header.
type Stat struct {
	Entries int
	Errors  int
	Warns   int
	Runs    int
	Skipped int  // malformed lines that were ignored
	Missing bool // no file yet
}

// String describes the stat for a status line.
func (s Stat) String() string {
	if s.Missing {
		return "no activity log yet"
	}
	parts := []string{fmt.Sprintf("%d entries", s.Entries)}
	if s.Warns > 0 {
		parts = append(parts, fmt.Sprintf("%d warnings", s.Warns))
	}
	if s.Errors > 0 {
		parts = append(parts, fmt.Sprintf("%d errors", s.Errors))
	}
	return strings.Join(parts, " · ")
}
