// Package settings persists user preferences shared by the TwinPane UI.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ThatOn3Gu7/TwinPane/internal/bookmarks"
	"github.com/ThatOn3Gu7/TwinPane/internal/history"
)

const (
	// FileName is the preferences file stored in TwinPane's configuration directory.
	FileName = "settings.json"
	version  = 1
)

// Preferences contains settings that apply across app restarts.
type Preferences struct {
	ShowHidden bool `json:"showHidden"`
	// HistorySessions is how many application runs (sessions) of operation
	// history are kept on disk. Zero means the default.
	HistorySessions int `json:"historySessions,omitempty"`
}

// DefaultHistorySessions is the retention limit used when the user has not
// chosen one.
const DefaultHistorySessions = history.DefaultSessions

type file struct {
	Version         int  `json:"version"`
	ShowHidden      bool `json:"showHidden"`
	HistorySessions int  `json:"historySessions,omitempty"`
}

// Normalize fills in defaults and clamps values into the supported range.
func (p Preferences) Normalize() Preferences {
	p.HistorySessions = history.ClampSessions(p.HistorySessions)
	return p
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
// TWINPANE_CONFIG_DIR, then the OS user config directory, then ~/.twinpane.
func DefaultPath() (string, error) {
	dir, err := bookmarks.ConfigDir(os.Getenv, os.UserConfigDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, FileName), nil
}

// Load reads preferences. A missing or empty file means default preferences.
func Load(path string) (Preferences, error) {
	if path == "" {
		return Preferences{}, errors.New("settings path is empty")
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Preferences{}.Normalize(), nil // defaults
	}
	if err != nil {
		return Preferences{}, fmt.Errorf("read settings %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return Preferences{}.Normalize(), nil // defaults
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
	return Preferences{
		ShowHidden:      saved.ShowHidden,
		HistorySessions: history.ClampSessions(saved.HistorySessions),
	}, nil
}

// Save writes preferences atomically with private file permissions. It first
// validates any existing file so damaged or newer settings are never replaced.
func Save(path string, preferences Preferences) error {
	if path == "" {
		return errors.New("settings path is empty")
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("settings path must be absolute: %s", path)
	}
	if _, err := Load(path); err != nil {
		return fmt.Errorf("cannot save settings: %w", err)
	}
	preferences = preferences.Normalize()
	data, err := json.MarshalIndent(file{
		Version:         version,
		ShowHidden:      preferences.ShowHidden,
		HistorySessions: preferences.HistorySessions,
	}, "", "  ")
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
