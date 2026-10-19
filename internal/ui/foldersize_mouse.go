package ui

import tea "github.com/charmbracelet/bubbletea"

// folderSizeMouse supports list scrolling and double-tap drill-down. Taps
// never change either file panel or execute a filesystem operation.
func (a *App) folderSizeMouse(ev tea.MouseEvent) tea.Cmd {
	v := a.folderSize
	if v == nil || v.loading || v.result == nil || v.root == nil {
		return nil
	}
	rows := folderSizeRows(v)
	if len(rows) == 0 {
		return nil
	}
	if ev.IsWheel() {
		v.cursor = max(0, min(len(rows)-1, v.cursor+wheelDelta(ev)*wheelStep))
		a.keepFolderSizeCursorVisible()
		return nil
	}
	if ev.Button != tea.MouseButtonLeft || ev.Action != tea.MouseActionRelease {
		return nil
	}
	bodyHeight := a.height - 3
	headerLines := a.folderSizeHeaderLines(bodyHeight)
	row := ev.Y - 1 - headerLines
	if row < 0 || row >= a.folderSizeListRows(bodyHeight) {
		return nil
	}
	v.cursor = min(len(rows)-1, v.offset+row)
	a.keepFolderSizeCursorVisible()
	if a.doubleTap(areaFolderSize, v.cursor) {
		a.openFolderSizeRow(rows)
	}
	return nil
}

func (a *App) folderSizeHeaderLines(height int) int {
	v := a.folderSize
	if v == nil {
		return 0
	}
	if v.loading {
		if height <= 6 {
			return 3
		}
		return 5
	}
	if v.result == nil {
		return 2
	}
	if v.root == nil {
		return 3
	}
	rows := folderSizeRows(v)
	if height <= 6 {
		lines := 3 // title, current path, compact subtree summary
		if len(rows) > 0 {
			if len(folderSizeLeafEntries(v)) < int(v.current.DirectLeaves) || v.current.TopEntriesLimited {
				lines++
			}
			lines++ // size/name column labels
		}
		return lines
	}
	lines := 5 // title, target, current path, subtree totals, scan-state detail
	if v.current.FirstError != "" && (v.result.Error == "" || v.current.FirstError != v.result.Error) {
		lines++
	}
	if len(rows) > 0 {
		if len(folderSizeLeafEntries(v)) < int(v.current.DirectLeaves) || v.current.TopEntriesLimited {
			lines++
		}
		lines++ // size/name column labels
	}
	return lines
}
