// Package theme centralises all Lip Gloss styles so panels, dialogs and
// future components share one consistent look and can be re-themed in one
// place.
package theme

import "github.com/charmbracelet/lipgloss"

// Theme holds every style used by the interface.
type Theme struct {
	Accent    lipgloss.Color
	Muted     lipgloss.Color
	BorderOn  lipgloss.Color
	BorderOff lipgloss.Color

	// Panel chrome
	PathActive   lipgloss.Style
	PathInactive lipgloss.Style
	ColumnHeader lipgloss.Style
	Divider      lipgloss.Style
	Info         lipgloss.Style
	Summary      lipgloss.Style
	Error        lipgloss.Style
	Empty        lipgloss.Style

	// Entry kinds
	File     lipgloss.Style
	Dir      lipgloss.Style
	Link     lipgloss.Style
	Broken   lipgloss.Style
	Exec     lipgloss.Style
	Hidden   lipgloss.Style
	Selected lipgloss.Style // background of selected rows (see SelectedRow)

	// Cursor row
	CursorActive   lipgloss.Style
	CursorInactive lipgloss.Style

	// App chrome
	TitleBadge lipgloss.Style
	TitleText  lipgloss.Style
	StatusInfo lipgloss.Style
	StatusErr  lipgloss.Style
	FooterKey  lipgloss.Style // shortcut chip of the hint bar (padding included: see FooterKeyPad)
	FooterDesc lipgloss.Style
	HelpBox    lipgloss.Style
	HelpTitle  lipgloss.Style
	StatusOK   lipgloss.Style
	StatusWarn lipgloss.Style

	// Dialogs (phase 2)
	Backdrop          lipgloss.Style // dimmed screen behind a dialog
	DialogBox         lipgloss.Style
	DialogDangerBox   lipgloss.Style
	DialogTitle       lipgloss.Style
	DialogDangerTitle lipgloss.Style
	DialogText        lipgloss.Style
	DialogPath        lipgloss.Style
	DialogWarn        lipgloss.Style
	DialogHint        lipgloss.Style
	Button            lipgloss.Style
	ButtonFocus       lipgloss.Style
	ButtonDangerFocus lipgloss.Style
	Input             lipgloss.Style
	InputCursor       lipgloss.Style
	ProgressFill      lipgloss.Style
	ProgressEmpty     lipgloss.Style

	// Scroll indicators (phase 4): the thumb drawn on a panel's right border
	// or in the right padding column of the search / bulk-rename lists.
	ScrollThumb    lipgloss.Style // active panel, search, bulk list
	ScrollThumbOff lipgloss.Style // inactive panel

	// Phase 5
	ArchiveBadge lipgloss.Style // "ZIP" badge on the path line of a panel browsing an archive
	CloudBadge   lipgloss.Style // remote name badge ("drive:") on the path line of a panel showing an rclone remote
	LocalBadge   lipgloss.Style // "LOCAL" badge while the other panel shows a remote
	ViewerBadge  lipgloss.Style // "VIEW" / "READ-ONLY" badges of the file viewer
	LineNumber   lipgloss.Style // viewer line numbers
	Match        lipgloss.Style // viewer search matches
	MatchCurrent lipgloss.Style // the current search match line marker
	Control      lipgloss.Style // viewer: replaced control characters
	PropLabel    lipgloss.Style // properties: field labels
	CheckOn      lipgloss.Style // properties: permission box that is set
	CheckFocus   lipgloss.Style // properties: focused permission box
}

// FooterKeyPad is the horizontal padding (cells on each side) of a hint-bar
// shortcut chip. Lip Gloss renders the padding with the chip's own
// background, so "Esc" appears as a coloured "  Esc  " block.
const FooterKeyPad = 2

// Default returns the built-in dark-friendly theme using ANSI-256 colours,
// which degrade gracefully on 16-colour terminals.
func Default() Theme {
	accent := lipgloss.Color("75")
	muted := lipgloss.Color("244")
	return Theme{
		Accent:    accent,
		Muted:     muted,
		BorderOn:  accent,
		BorderOff: lipgloss.Color("239"),

		PathActive:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(accent),
		PathInactive: lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Background(lipgloss.Color("236")),
		ColumnHeader: lipgloss.NewStyle().Foreground(muted).Bold(true),
		Divider:      lipgloss.NewStyle().Foreground(lipgloss.Color("238")),
		Info:         lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		Summary:      lipgloss.NewStyle().Foreground(muted),
		Error:        lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true),
		Empty:        lipgloss.NewStyle().Foreground(muted).Italic(true),

		File:     lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		Dir:      lipgloss.NewStyle().Foreground(accent).Bold(true),
		Link:     lipgloss.NewStyle().Foreground(lipgloss.Color("80")),
		Broken:   lipgloss.NewStyle().Foreground(lipgloss.Color("203")),
		Exec:     lipgloss.NewStyle().Foreground(lipgloss.Color("114")),
		Hidden:   lipgloss.NewStyle().Foreground(lipgloss.Color("243")),
		Selected: lipgloss.NewStyle().Background(lipgloss.Color("240")),

		CursorActive:   lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("153")).Bold(true),
		CursorInactive: lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("237")),

		TitleBadge: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(accent).Padding(0, 1),
		TitleText:  lipgloss.NewStyle().Foreground(muted),
		StatusInfo: lipgloss.NewStyle().Foreground(lipgloss.Color("250")),
		StatusErr:  lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true),
		FooterKey:  lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("249")).Bold(true).Padding(0, FooterKeyPad),
		FooterDesc: lipgloss.NewStyle().Foreground(lipgloss.Color("250")),
		HelpBox:    lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(1, 3),
		HelpTitle:  lipgloss.NewStyle().Bold(true).Foreground(accent),
		StatusOK:   lipgloss.NewStyle().Foreground(lipgloss.Color("114")),
		StatusWarn: lipgloss.NewStyle().Foreground(lipgloss.Color("221")).Bold(true),

		Backdrop:          lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		DialogBox:         lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(0, 2),
		DialogDangerBox:   lipgloss.NewStyle().Border(lipgloss.ThickBorder()).BorderForeground(lipgloss.Color("203")).Padding(0, 2),
		DialogTitle:       lipgloss.NewStyle().Bold(true).Foreground(accent),
		DialogDangerTitle: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("203")),
		DialogText:        lipgloss.NewStyle().Foreground(lipgloss.Color("252")),
		DialogPath:        lipgloss.NewStyle().Foreground(lipgloss.Color("153")),
		DialogWarn:        lipgloss.NewStyle().Foreground(lipgloss.Color("221")).Bold(true),
		DialogHint:        lipgloss.NewStyle().Foreground(muted),
		Button:            lipgloss.NewStyle().Foreground(lipgloss.Color("252")).Background(lipgloss.Color("238")).Padding(0, 1),
		ButtonFocus:       lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(accent).Bold(true).Padding(0, 1),
		ButtonDangerFocus: lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(lipgloss.Color("160")).Bold(true).Padding(0, 1),
		Input:             lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(lipgloss.Color("236")),
		InputCursor:       lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("252")),
		ProgressFill:      lipgloss.NewStyle().Foreground(accent),
		ProgressEmpty:     lipgloss.NewStyle().Foreground(lipgloss.Color("238")),
		ScrollThumb:       lipgloss.NewStyle().Foreground(lipgloss.Color("153")).Bold(true),
		ScrollThumbOff:    lipgloss.NewStyle().Foreground(lipgloss.Color("247")),

		ArchiveBadge: lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(lipgloss.Color("179")).Padding(0, 1),
		CloudBadge:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(lipgloss.Color("117")).Padding(0, 1),
		LocalBadge:   lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(lipgloss.Color("250")).Padding(0, 1),
		ViewerBadge:  lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("16")).Background(lipgloss.Color("179")).Padding(0, 1),
		LineNumber:   lipgloss.NewStyle().Foreground(lipgloss.Color("240")),
		Match:        lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(lipgloss.Color("221")),
		MatchCurrent: lipgloss.NewStyle().Foreground(lipgloss.Color("221")).Bold(true),
		Control:      lipgloss.NewStyle().Foreground(lipgloss.Color("203")),
		PropLabel:    lipgloss.NewStyle().Foreground(muted),
		CheckOn:      lipgloss.NewStyle().Foreground(lipgloss.Color("114")).Bold(true),
		CheckFocus:   lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(accent).Bold(true),
	}
}

// SelectedRow returns the style for a selected row whose normal style is
// st: the same text colour on the Selected background (gray). Muted text
// (hidden files, empty-state messages) is lightened to stay readable on it.
func (t Theme) SelectedRow(st lipgloss.Style) lipgloss.Style {
	if fg, ok := st.GetForeground().(lipgloss.Color); ok && (fg == "243" || fg == "240" || fg == "241" || fg == "242" || fg == "244") {
		st = st.Foreground(lipgloss.Color("250"))
	}
	return st.Background(t.Selected.GetBackground())
}
