package settings

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultPathUsesTwinPaneConfigDirectory(t *testing.T) {
	configDir := filepath.Join(t.TempDir(), "twinpane-config")
	t.Setenv("TWINPANE_CONFIG_DIR", configDir)

	got, err := DefaultPath()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(configDir, FileName); got != want {
		t.Fatalf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestLoadAndSavePreferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", FileName)

	prefs, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if prefs.ShowHidden {
		t.Fatal("missing settings should default to hidden files off")
	}

	if err := Save(path, Preferences{ShowHidden: true}); err != nil {
		t.Fatal(err)
	}
	prefs, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !prefs.ShowHidden {
		t.Fatal("saved hidden-file preference was not loaded")
	}

	if err := Save(path, Preferences{ShowHidden: false}); err != nil {
		t.Fatal(err)
	}
	prefs, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if prefs.ShowHidden {
		t.Fatal("saved false preference should disable hidden files")
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("settings file mode = %04o, want 0600", got)
		}
	}
}

func TestCorruptSettingsAreNotOverwritten(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	original := []byte("{broken json\n")
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path)
	var corrupt *CorruptError
	if !errors.As(err, &corrupt) {
		t.Fatalf("Load() error = %v, want *CorruptError", err)
	}
	if err := Save(path, Preferences{ShowHidden: true}); err == nil {
		t.Fatal("Save() should refuse to replace a damaged settings file")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(original) {
		t.Fatalf("damaged settings file changed: %q", got)
	}
}

func TestLoadRejectsNewerSettingsVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	if err := os.WriteFile(path, []byte(`{"version":99,"showHidden":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	var corrupt *CorruptError
	if !errors.As(err, &corrupt) || !strings.Contains(err.Error(), "newer TwinPane") {
		t.Fatalf("Load() error = %v, want a newer-version CorruptError", err)
	}
}

func TestHistoryRetentionDefaultsAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config", FileName)
	prefs, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if prefs.HistorySessions != DefaultHistorySessions || DefaultHistorySessions != 20 {
		t.Fatalf("default retention = %d, want %d", prefs.HistorySessions, DefaultHistorySessions)
	}

	// A settings file from before the option existed keeps the default.
	os.MkdirAll(filepath.Dir(path), 0o700)
	if err := os.WriteFile(path, []byte(`{"version":1,"showHidden":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	prefs, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !prefs.ShowHidden || prefs.HistorySessions != DefaultHistorySessions {
		t.Fatalf("old file: %+v", prefs)
	}

	if err := Save(path, Preferences{ShowHidden: true, HistorySessions: 5}); err != nil {
		t.Fatal(err)
	}
	prefs, err = Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if prefs.HistorySessions != 5 || !prefs.ShowHidden {
		t.Fatalf("saved preferences = %+v", prefs)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"historySessions": 5`) {
		t.Fatalf("settings file lacks the retention limit: %s", data)
	}

	// Out-of-range values are clamped instead of rejected.
	if err := Save(path, Preferences{HistorySessions: 100000}); err != nil {
		t.Fatal(err)
	}
	if prefs, _ = Load(path); prefs.HistorySessions != 200 {
		t.Fatalf("clamped retention = %d, want 200", prefs.HistorySessions)
	}
	// Zero means "default", so the option is omitted rather than written as 0.
	if err := Save(path, Preferences{ShowHidden: false}); err != nil {
		t.Fatal(err)
	}
	if prefs, _ = Load(path); prefs.HistorySessions != DefaultHistorySessions {
		t.Fatalf("retention after zero = %d", prefs.HistorySessions)
	}
}

// TestResetRewritesDefaultsAndRecoversCorruptFile covers the intentional
// recovery path: Reset force-writes the given preferences over a damaged
// (or newer-version) file, while plain Save keeps refusing to clobber it.
func TestResetRewritesDefaultsAndRecoversCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)

	// Customised file → Reset restores the defaults on disk.
	custom := Default()
	custom.ShowHidden = true
	custom.Theme = "green"
	custom.Notice.Duration = 7
	custom.Mouse = false
	if err := Save(path, custom); err != nil {
		t.Fatal(err)
	}
	if err := Reset(path, Default()); err != nil {
		t.Fatalf("Reset(): %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load after Reset: %v", err)
	}
	if got.ShowHidden || got.Theme != "default" || got.Notice.Duration != NoticeDurationDef || !got.Mouse {
		t.Fatalf("Reset did not restore defaults: %+v", got)
	}

	// A damaged file: Save still refuses, Reset recovers it.
	broken := []byte("{broken json\n")
	if err := os.WriteFile(path, broken, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, Default()); err == nil {
		t.Fatal("Save must still refuse a damaged file")
	}
	if err := Reset(path, Default()); err != nil {
		t.Fatalf("Reset must recover a damaged file: %v", err)
	}
	if got, err = Load(path); err != nil {
		t.Fatalf("Load after recovery: %v", err)
	}

	// Newer-version files recover the same way.
	if err := os.WriteFile(path, []byte(`{"version":99,"showHidden":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Reset(path, Default()); err != nil {
		t.Fatalf("Reset must recover a newer-version file: %v", err)
	}
	if got, err = Load(path); err != nil {
		t.Fatalf("Load after version recovery: %v", err)
	}
	if got.ShowHidden {
		t.Fatalf("recovered file kept foreign values: %+v", got)
	}
}
