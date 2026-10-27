// Package settings persists user preferences shared by the TwinPane UI.
//
// One versioned JSON file holds every preference (see FileName). The format
// grows additively: version 1 stays 1 as fields are added, so a file written
// by a newer TwinPane is still readable (unknown keys are ignored) and a file
// written by an older one still carries its smaller field set. Fields that
// default to true are stored as pointers so a missing key means "default",
// not "off".
//
// Save never touches a file that could not be read back first: a damaged or
// newer-version file yields a *CorruptError, Save then refuses, and the UI
// offers Reset as the intentional recovery path that overwrites on purpose.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ThatOn3Gu7/TwinPane/internal/bookmarks"
	"github.com/ThatOn3Gu7/TwinPane/internal/history"
)

const (
	// FileName is the preferences file stored in TwinPane's configuration directory.
	FileName = "settings.json"
	version  = 1
)

// Bounds for preferences whose values must stay inside a sane range. Load
// normalizes anything outside them back to the default instead of failing
// the whole file.
const (
	NoticeDurationMin = 2
	NoticeDurationMax = 10
	NoticeDurationDef = 4
)

// DefaultHistorySessions is the retention limit used when the user has not
// chosen one.
const DefaultHistorySessions = history.DefaultSessions

// Preferences contains settings that apply across app restarts.
type Preferences struct {
	ShowHidden   bool
	Startup      StartupPrefs
	Sort         SortPrefs
	FocusPreview bool
	Notice       NoticePrefs
	Theme        string // theme preset name ("" = the default preset)
	Editor       EditorPrefs
	Mouse        bool
	Viewer       ViewerPrefs
	Compress     CompressPrefs
	// HistorySessions is how many application runs of operation history are
	// kept on disk (0 = DefaultHistorySessions).
	HistorySessions int
	// Activity configures the User Activity Log (see internal/activity).
	Activity ActivityPrefs
}

// ActivityPrefs configures the activity log. Mode is how much is recorded:
// "off", "warnings", "actions" (the default) or "verbose". MaxKiB is the size
// at which the current file is rotated (0 = the default, 1 MiB) and KeepDays
// removes rotated files older than that (0 = the default, 30 days).
type ActivityPrefs struct {
	Mode     string
	MaxKiB   int
	KeepDays int
}

// Bounds for the activity log's retention, mirrored by the settings screen.
const (
	ActivityMaxKiBMin = 64
	ActivityMaxKiBDef = 1024
	ActivityMaxKiBMax = 16384
	ActivityDaysMin   = 1
	ActivityDaysDef   = 30
	ActivityDaysMax   = 365
)

// StartupPrefs selects where the two panels open. Left/Right empty means the
// user's home directory; Pane is "left" or "right" (the panel focused first).
type StartupPrefs struct {
	Left, Right, Pane string
}

// SortPrefs is the order both panels start with. Key is a fsys sort key
// name ("name", "size", "time", "type"); s/S keep changing it for the
// session without touching this default.
type SortPrefs struct {
	Key       string
	Desc      bool
	DirsFirst bool
}

// NoticePrefs places the result pop-up and sets how long it stays. Corner is
// "br", "bl", "tr" or "tl"; Duration is seconds.
type NoticePrefs struct {
	Corner   string
	Duration int
}

// EditorPrefs: Command is a full command line ("code --wait"); empty keeps
// the historic chain ($VISUAL, $EDITOR, first detected editor). Tips is the
// "save/quit keys" card shown before an editor starts.
type EditorPrefs struct {
	Command string
	Tips    bool
}

// ViewerPrefs are the defaults for a newly opened file viewer.
type ViewerPrefs struct {
	Wrap    bool
	Numbers bool
}

// CompressPrefs remembers the last compress format ("" = let the dialog pick
// the first capable one) and the chosen level index per format ID.
type CompressPrefs struct {
	Format string
	Levels map[string]int
}

// Default returns the preferences that match TwinPane's historic behaviour:
// hidden files off, both panels in the home directory starting on the left,
// name sort ascending with directories first, focus preview on, notifications
// bottom-right for four seconds, the default theme, the environment's editor
// with tips, mouse on, viewer wrap and line numbers on, automatic compress
// defaults.
func Default() Preferences {
	return Preferences{
		Startup:         StartupPrefs{Pane: "left"},
		Sort:            SortPrefs{Key: "name", DirsFirst: true},
		FocusPreview:    true,
		Notice:          NoticePrefs{Corner: "br", Duration: NoticeDurationDef},
		Theme:           "default",
		Editor:          EditorPrefs{Tips: true},
		Mouse:           true,
		Viewer:          ViewerPrefs{Wrap: true, Numbers: true},
		Compress:        CompressPrefs{Levels: map[string]int{}},
		HistorySessions: DefaultHistorySessions,
		// The activity log records actions by default: it is there to help
		// when something went wrong, which is after the fact.
		Activity: ActivityPrefs{Mode: "actions", MaxKiB: ActivityMaxKiBDef, KeepDays: ActivityDaysDef},
	}
}

// Normalized returns p with every field forced back into its valid range:
// unknown enum values, out-of-range durations and stray whitespace become
// the defaults instead of an error, so a hand-edited file with one odd value
// still loads. The next save writes the cleaned set back.
func (p Preferences) Normalized() Preferences {
	out := p
	out.Startup.Left = strings.TrimSpace(out.Startup.Left)
	out.Startup.Right = strings.TrimSpace(out.Startup.Right)
	if out.Startup.Pane != "right" {
		out.Startup.Pane = "left"
	}
	switch out.Sort.Key {
	case "name", "size", "time", "type":
	default:
		out.Sort.Key = "name"
	}
	switch out.Notice.Corner {
	case "br", "bl", "tr", "tl":
	default:
		out.Notice.Corner = "br"
	}
	if out.Notice.Duration < NoticeDurationMin || out.Notice.Duration > NoticeDurationMax {
		out.Notice.Duration = NoticeDurationDef
	}
	out.Theme = strings.TrimSpace(out.Theme)
	if out.Theme == "" {
		out.Theme = "default"
	}
	out.Editor.Command = strings.TrimSpace(out.Editor.Command)
	if out.Compress.Levels == nil {
		out.Compress.Levels = map[string]int{}
	} else {
		levels := make(map[string]int, len(out.Compress.Levels))
		for k, v := range out.Compress.Levels {
			if k != "" {
				levels[k] = v
			}
		}
		out.Compress.Levels = levels
	}
	out.HistorySessions = history.ClampSessions(out.HistorySessions)
	out.Activity = out.Activity.normalized()
	return out
}

// normalized forces the activity log's values back into their valid ranges. An
// unknown mode falls back to the default rather than to "off" or "verbose".
func (p ActivityPrefs) normalized() ActivityPrefs {
	switch p.Mode {
	case "off", "warnings", "actions", "verbose":
	default:
		p.Mode = "actions"
	}
	p.MaxKiB = ClampActivityMaxKiB(p.MaxKiB)
	p.KeepDays = ClampActivityKeepDays(p.KeepDays)
	return p
}

// ClampActivityMaxKiB forces a rotation size into its range (0 or an odd value
// becomes the default).
func ClampActivityMaxKiB(kib int) int {
	if kib < ActivityMaxKiBMin || kib > ActivityMaxKiBMax {
		return ActivityMaxKiBDef
	}
	return kib
}

// ClampActivityKeepDays forces the age limit into its range.
func ClampActivityKeepDays(days int) int {
	if days < ActivityDaysMin || days > ActivityDaysMax {
		return ActivityDaysDef
	}
	return days
}

// Duration returns how long a notification stays, clamped to the valid
// range no matter what the file said.
func (p Preferences) Duration() time.Duration {
	d := p.Notice.Duration
	if d < NoticeDurationMin || d > NoticeDurationMax {
		d = NoticeDurationDef
	}
	return time.Duration(d) * time.Second
}

// file is the on-disk shape. Bool fields that default to true are pointers:
// absent (older file) means the default, not false.
type file struct {
	Version      int          `json:"version"`
	ShowHidden   bool         `json:"showHidden"`
	Startup      startupFile  `json:"startup"`
	Sort         sortFile     `json:"sort"`
	FocusPreview *bool        `json:"focusPreview,omitempty"`
	Notice       noticeFile   `json:"notice"`
	Theme        string       `json:"theme"`
	Editor       editorFile   `json:"editor"`
	Mouse        *bool        `json:"mouse,omitempty"`
	Viewer       viewerFile   `json:"viewer"`
	Compress     compressFile `json:"compress"`
	// HistorySessions is omitted when zero (an older file keeps the default).
	HistorySessions int          `json:"historySessions,omitempty"`
	Activity        activityFile `json:"activity,omitempty"`
}

type activityFile struct {
	Mode     string `json:"mode,omitempty"`
	MaxKiB   int    `json:"maxKiB,omitempty"`
	KeepDays int    `json:"keepDays,omitempty"`
}

type startupFile struct {
	Left  string `json:"left"`
	Right string `json:"right"`
	Pane  string `json:"pane"`
}

type sortFile struct {
	Key       string `json:"key"`
	Desc      bool   `json:"desc"`
	DirsFirst *bool  `json:"dirsFirst,omitempty"`
}

type noticeFile struct {
	Corner   string `json:"corner"`
	Duration int    `json:"duration"`
}

type editorFile struct {
	Command string `json:"command"`
	Tips    *bool  `json:"tips,omitempty"`
}

type viewerFile struct {
	Wrap    *bool `json:"wrap,omitempty"`
	Numbers *bool `json:"numbers,omitempty"`
}

type compressFile struct {
	Format string         `json:"format"`
	Levels map[string]int `json:"levels"`
}

func boolPtr(b bool) *bool { return &b }

// decode maps the on-disk shape to Preferences using defaults for anything
// absent, then normalizes the result.
func (f file) decode() Preferences {
	p := Default()
	p.ShowHidden = f.ShowHidden
	p.Startup = StartupPrefs{Left: f.Startup.Left, Right: f.Startup.Right, Pane: f.Startup.Pane}
	p.Sort = SortPrefs{Key: f.Sort.Key, Desc: f.Sort.Desc, DirsFirst: true}
	if f.Sort.DirsFirst != nil {
		p.Sort.DirsFirst = *f.Sort.DirsFirst
	}
	if f.FocusPreview != nil {
		p.FocusPreview = *f.FocusPreview
	}
	p.Notice = NoticePrefs{Corner: f.Notice.Corner, Duration: f.Notice.Duration}
	p.Theme = f.Theme
	p.Editor = EditorPrefs{Command: f.Editor.Command, Tips: true}
	if f.Editor.Tips != nil {
		p.Editor.Tips = *f.Editor.Tips
	}
	p.Mouse = true
	if f.Mouse != nil {
		p.Mouse = *f.Mouse
	}
	p.Viewer = ViewerPrefs{Wrap: true, Numbers: true}
	if f.Viewer.Wrap != nil {
		p.Viewer.Wrap = *f.Viewer.Wrap
	}
	if f.Viewer.Numbers != nil {
		p.Viewer.Numbers = *f.Viewer.Numbers
	}
	p.Compress = CompressPrefs{Format: f.Compress.Format, Levels: f.Compress.Levels}
	p.HistorySessions = f.HistorySessions
	p.Activity = ActivityPrefs{Mode: f.Activity.Mode, MaxKiB: f.Activity.MaxKiB, KeepDays: f.Activity.KeepDays}
	return p.Normalized()
}

// encode maps Preferences to the on-disk shape with every key explicit, so
// a saved file documents the whole configuration.
func encode(p Preferences) file {
	p = p.Normalized()
	return file{
		Version:         version,
		ShowHidden:      p.ShowHidden,
		Startup:         startupFile{Left: p.Startup.Left, Right: p.Startup.Right, Pane: p.Startup.Pane},
		Sort:            sortFile{Key: p.Sort.Key, Desc: p.Sort.Desc, DirsFirst: boolPtr(p.Sort.DirsFirst)},
		FocusPreview:    boolPtr(p.FocusPreview),
		Notice:          noticeFile{Corner: p.Notice.Corner, Duration: p.Notice.Duration},
		Theme:           p.Theme,
		Editor:          editorFile{Command: p.Editor.Command, Tips: boolPtr(p.Editor.Tips)},
		Mouse:           boolPtr(p.Mouse),
		Viewer:          viewerFile{Wrap: boolPtr(p.Viewer.Wrap), Numbers: boolPtr(p.Viewer.Numbers)},
		Compress:        compressFile{Format: p.Compress.Format, Levels: p.Compress.Levels},
		HistorySessions: p.HistorySessions,
		Activity:        activityFile{Mode: p.Activity.Mode, MaxKiB: p.Activity.MaxKiB, KeepDays: p.Activity.KeepDays},
	}
}

// CorruptError indicates that a settings file could not be read safely. Files
// written by a newer TwinPane version are also treated as corrupt, so an older
// version won't overwrite settings it does not understand.
type CorruptError struct {
	Path string
	Err  error
}

func (e *CorruptError) Error() string {
	return fmt.Sprintf("settings file %s is damaged or from a newer version: %v", e.Path, e.Err)
}

func (e *CorruptError) Unwrap() error { return e.Err }

// DefaultPath returns the settings file path in the shared TwinPane
// configuration directory. It follows the same policy as bookmarks:
// TWINPANE_CONFIG_DIR, then the OS user config directory (~/.config/twinpane
// on Linux), then ~/.twinpane.
func DefaultPath() (string, error) {
	dir, err := bookmarks.ConfigDir(os.Getenv, os.UserConfigDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// Load reads preferences. A missing or empty file means default preferences;
// a damaged or newer-version file returns a *CorruptError; a file with odd
// individual values loads with those values reset to their defaults.
func Load(path string) (Preferences, error) {
	if path == "" {
		return Preferences{}, errors.New("settings path is empty")
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Preferences{}, fmt.Errorf("read settings %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return Default(), nil
	}
	var saved file
	if err := json.Unmarshal(data, &saved); err != nil {
		return Preferences{}, &CorruptError{Path: path, Err: err}
	}
	if saved.Version > version {
		return Preferences{}, &CorruptError{
			Path: path,
			Err:  fmt.Errorf("written by a newer TwinPane (format %d)", saved.Version),
		}
	}
	return saved.decode(), nil
}

// Save writes preferences atomically with private file permissions. It first
// validates any existing file so damaged or newer settings are never replaced.
func Save(path string, preferences Preferences) error {
	return write(path, preferences, true)
}

// Reset writes preferences over the file on purpose, skipping the check that
// makes Save refuse a damaged or newer-version file. It is the recovery path
// behind "reset to defaults" in the Settings screen: the user asked for the
// file to be replaced, so the old contents are not worth preserving.
func Reset(path string, preferences Preferences) error {
	return write(path, preferences, false)
}

func write(path string, preferences Preferences, checkExisting bool) error {
	if path == "" {
		return errors.New("settings path is empty")
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("settings path must be absolute: %s", path)
	}
	if checkExisting {
		if _, err := Load(path); err != nil {
			return fmt.Errorf("cannot save settings: %w", err)
		}
	}
	data, err := json.MarshalIndent(encode(preferences), "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("cannot create settings directory %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".settings-*.tmp")
	if err != nil {
		return fmt.Errorf("cannot write settings in %s: %w", dir, err)
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
		d.Sync() // make the rename durable where supported
		d.Close()
	}
	return nil
}
