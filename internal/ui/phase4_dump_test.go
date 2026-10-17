package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestDumpScreens prints representative screens for visual review; it only
// runs with TP_DUMP=1 (go test -run TestDumpScreens ./internal/ui -v).
func TestDumpScreens(t *testing.T) {
	if os.Getenv("TP_DUMP") == "" {
		t.Skip("set TP_DUMP=1 to print screens")
	}
	for _, sz := range [][2]int{{80, 24}, {50, 30}} {
		h, env := newOpsApp(t, nil, nil)
		for i := 0; i < 40; i++ {
			os.WriteFile(filepath.Join(env.src, fmt.Sprintf("file%02d.txt", i)), []byte("x"), 0o644)
		}
		os.WriteFile(filepath.Join(env.src, ".hidden"), nil, 0o644)
		h.exec(h.a.panels[0].Refresh())
		h.settle()
		h.send(tea.WindowSizeMsg{Width: sz[0], Height: sz[1]})
		h.focus("file05.txt")
		fmt.Printf("===== %dx%d browse\n%s\n", sz[0], sz[1], h.view())
		h.press("?")
		fmt.Printf("===== help\n%s\n", h.view())
		h.press("esc")
		h.press("d")
		h.waitDialog(dlgDelete)
		fmt.Printf("===== delete dialog\n%s\n", h.view())
		h.press("esc")
		h.press("/")
		h.searchDone()
		h.typeText("file", false)
		h.searchDone()
		fmt.Printf("===== search\n%s\n", h.view())
		h.press("esc")
		h.press("a")
		h.press("r")
		fmt.Printf("===== bulk\n%s\n", h.view())
		h.press("esc")
	}
}
