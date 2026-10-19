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
