package ui

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/panel"
)

// Back / forward in a panel's own navigation history ([ and ], or
// Alt+←/→). Each panel keeps its own session-local stacks (panel/history.go);
// nothing here touches the other panel or the bookmarks, and the keys are
// plain panel keys, so dialogs, the viewer, searches and editors never see
// them.

// navMove runs one history step and reports it in the status line.
func (a *App) navMove(p *panel.Panel, back bool) tea.Cmd {
	move := p.Forward
	dir := "forward"
	if back {
		move, dir = p.Back, "back"
	}
	r := move()
	if !r.OK {
		a.setStatusLevel(r.Note, lvlWarn)
		return nil
	}
	if r.Note != "" {
		// some dead entries were skipped: say so instead of pretending
		a.setStatusLevel(r.Note, lvlWarn)
	} else {
		a.setStatusLevel(dir+" to "+r.Label, lvlOK)
	}
	return r.Cmd
}

// navHints returns the history chips for the hint bar (only when a step is
// available, so panels without history look exactly as before).
func (a *App) navHints(p *panel.Panel) []barItem {
	back, forward := p.NavDepth()
	out := make([]barItem, 0, 2)
	if back > 0 {
		out = append(out, item("[", "back", "["))
	}
	if forward > 0 {
		out = append(out, item("]", "forward", "]"))
	}
	return out
}

// navMenuEntries adds Back / Forward to the action menu (M), with how many
// steps are available on each side.
func (a *App) navMenuEntries(p *panel.Panel) []menuEntry {
	back, forward := p.NavDepth()
	var out []menuEntry
	if back > 0 {
		out = append(out, menuEntry{label: "Back", detail: plural(back, "step"), key: "[",
			run: func() tea.Cmd { return a.navMove(p, true) }})
	}
	if forward > 0 {
		out = append(out, menuEntry{label: "Forward", detail: plural(forward, "step"), key: "]",
			run: func() tea.Cmd { return a.navMove(p, false) }})
	}
	return out
}
