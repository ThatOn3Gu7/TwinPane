package ui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/theme"
)

// TestEveryThemeRenders keeps the preset list honest at the interface level:
// each theme draws the same screen inside the window and keeps every row a
// keybinding label, a hint chip or a dialog box needs to fit.
func TestEveryThemeRenders(t *testing.T) {
	h, _ := newOpsApp(t, nil, nil)
	defer func() { h.a.theme = theme.Default() }()
	for _, name := range theme.PresetNames() {
		th, ok := theme.Preset(name)
		if !ok {
			t.Fatalf("%s does not build", name)
		}
		h.a.theme, h.a.themeName = th, name
		for _, size := range [][2]int{{100, 30}, {80, 24}, {60, 18}} {
			h.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			checkScreen(t, h.view(), size[0], size[1])
		}
		// A dialog and the help screen use the theme too.
		h.send(tea.WindowSizeMsg{Width: 100, Height: 30})
		h.press("M")
		if h.a.dialog == nil {
			t.Fatalf("%s: the action menu did not open", name)
		}
		checkScreen(t, h.view(), 100, 30)
		h.press("esc")
		h.press("?")
		if !h.a.showHelp {
			t.Fatalf("%s: the help screen did not open", name)
		}
		checkScreen(t, h.view(), 100, 30)
		h.press("esc")
	}
}
