package ui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
)

func TestHiddenFilesPreferencePersistsAcrossRestarts(t *testing.T) {
	root := t.TempDir()
	startDir := filepath.Join(root, "files")
	if err := os.MkdirAll(startDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(startDir, ".secret"), []byte("hidden"), 0o600); err != nil {
		t.Fatal(err)
	}
	options := Options{
		Version:      "test",
		StartDir:     startDir,
		SettingsPath: filepath.Join(root, "config", "settings.json"),
	}

	first := New(options)
	if first.ShowHidden() {
		t.Fatal("hidden files should be off before the first toggle")
	}
	drain(first, first.Init())
	press(first, "H")
	if !first.ShowHidden() {
		t.Fatal("H should enable hidden files")
	}
	for i := 0; i < 2; i++ {
		if !first.Panel(i).Options().ShowHidden || !hasPanelEntry(first.Panel(i), ".secret") {
			t.Fatalf("first app panel %d did not show the hidden file", i)
		}
	}

	// A fresh App reads the saved preference before either panel's initial
	// directory listing starts.
	second := New(options)
	if !second.ShowHidden() {
		t.Fatal("hidden-file preference was not loaded by the next app instance")
	}
	for i := 0; i < 2; i++ {
		if !second.Panel(i).Options().ShowHidden {
			t.Fatalf("second app panel %d did not receive the loaded setting", i)
		}
	}
	drain(second, second.Init())
	for i := 0; i < 2; i++ {
		if !hasPanelEntry(second.Panel(i), ".secret") {
			t.Fatalf("second app panel %d did not list the hidden file", i)
		}
	}

	press(second, "h")
	third := New(options)
	if third.ShowHidden() {
		t.Fatal("turning hidden files off should also persist across restarts")
	}
	for i := 0; i < 2; i++ {
		if third.Panel(i).Options().ShowHidden {
			t.Fatalf("third app panel %d should start with hidden files off", i)
		}
	}
}

func hasPanelEntry(p interface{ Entries() []fsys.Entry }, name string) bool {
	for _, entry := range p.Entries() {
		if entry.Name == name {
			return true
		}
	}
	return false
}
