package ui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// comparisonMouse keeps touch/desktop input inside the comparison overlay:
// wheels scroll its list and taps move the cursor, but never execute an
// operation. Copy proposals still require the explicit l/r or footer actions.
func (a *App) comparisonMouse(ev tea.MouseEvent) tea.Cmd {
	c := a.comparison
	if c == nil {
		return nil
	}
	if ev.IsWheel() {
		delta := wheelDelta(ev)
		if c.reviewing {
			actions := c.plan.Actions()
			if len(actions) == 0 {
				return nil
			}
			c.planCursor = max(0, min(len(actions)-1, c.planCursor+delta*wheelStep))
			a.keepPlanCursorVisible()
		} else {
			entries := visibleComparisonEntries(c)
			if len(entries) > 0 {
				c.cursor = max(0, min(len(entries)-1, c.cursor+delta*wheelStep))
				a.keepComparisonCursorVisible()
			}
		}
		return nil
	}
	if ev.Button != tea.MouseButtonLeft || ev.Action != tea.MouseActionRelease {
		return nil
	}
	bodyHeight := a.height - 3
	if c.reviewing {
		actions := c.plan.Actions()
		if len(actions) == 0 {
			return nil
		}
		header := 4
		if bodyHeight <= 5 {
			header = 3
		}
		row := ev.Y - 1 - header
		if row >= 0 && row < a.comparisonPlanRows(bodyHeight) {
			c.planCursor = min(len(actions)-1, c.planOffset+row)
			a.keepPlanCursorVisible()
		}
		return nil
	}
	entries := visibleComparisonEntries(c)
	if len(entries) == 0 {
		return nil
	}
	header := 6
	if bodyHeight <= 6 {
		header = 3
	}
	row := ev.Y - 1 - header
	if row >= 0 && row < a.comparisonListRows(bodyHeight) {
		c.cursor = min(len(entries)-1, c.offset+row)
		a.keepComparisonCursorVisible()
	}
	return nil
}
