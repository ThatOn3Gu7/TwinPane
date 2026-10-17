package editor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHintPerEditor(t *testing.T) {
	cases := []struct {
		path, label, name string
		want, reject      []string
	}{
		{"/usr/bin/nvim", "Neovim", "Neovim", []string{"Esc", ":w", ":q", ":wq"}, nil},
		{"/data/data/com.termux/files/usr/bin/nvim", "", "Neovim", []string{":wq"}, nil},
		{"/usr/bin/vim.basic", "", "Vim", []string{":wq"}, nil},
		{"/usr/bin/vi", "vi", "vi", []string{":wq"}, nil},
		{"/usr/bin/hx", "Helix", "Helix", []string{":wq"}, nil},
		{"/usr/bin/nano", "GNU nano", "nano", []string{"Ctrl+O", "Ctrl+X"}, []string{":w", ":q"}},
		{"/usr/bin/micro", "micro", "micro", []string{"Ctrl+S", "Ctrl+Q"}, []string{":w"}},
		{"/usr/bin/emacs", "Emacs (terminal)", "Emacs", []string{"Ctrl+X Ctrl+S"}, []string{":w"}},
		{"/usr/bin/mcedit", "mcedit", "mcedit", []string{"F2", "F10"}, []string{":w"}},
		{"/usr/bin/code", "VS Code", "VS Code", []string{"close"}, []string{":w"}},
		{`C:\Tools\nvim.exe`, "Neovim", "Neovim", []string{":wq"}, nil},
	}
	for _, c := range cases {
		h := Editor{Path: c.path, Label: c.label}.Hint()
		if !h.Known || h.Name != c.name {
			t.Errorf("%s: known=%v name=%q, want %q", c.path, h.Known, h.Name, c.name)
		}
		for _, w := range c.want {
			if !strings.Contains(h.Short, w) || h.Save == "" {
				t.Errorf("%s: %q lacks %q", c.path, h.Short, w)
			}
		}
		for _, r := range c.reject {
			if strings.Contains(h.Short, r) {
				t.Errorf("%s: %q must not mention %q (Neovim-only keys)", c.path, h.Short, r)
			}
		}
	}
}

func TestHintUnknownEditorIsGeneric(t *testing.T) {
	h := Editor{Path: "/opt/bin/myed", Name: "myed", Source: "$EDITOR"}.Hint()
	if h.Known || strings.Contains(h.Short, ":w") || !strings.Contains(h.Short, "myed's own help") {
		t.Fatalf("%+v", h)
	}
}

func TestHintFollowsSymlinks(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "nano")
	os.WriteFile(real, []byte("#!/bin/sh\n"), 0o755)
	link := filepath.Join(dir, "editor")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	h := Editor{Path: link, Label: "system default (editor)"}.Hint()
	if !h.Known || !strings.Contains(h.Short, "Ctrl+O") {
		t.Fatalf("editor -> nano not identified: %+v", h)
	}
}
