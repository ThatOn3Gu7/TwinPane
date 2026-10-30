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

	// Notifications (phase 12): the border and heading of the small pop-up that
	// reports a result. One style per kind, so success, warnings, errors and
	// plain information are distinguishable by colour and by their icon.
	NoticeOK   lipgloss.Style
	NoticeWarn lipgloss.Style
	NoticeErr  lipgloss.Style
	NoticeInfo lipgloss.Style

	// Scroll indicators (phase 4): the thumb drawn on a panel's right border
	// or in the right padding column of the search / bulk-rename lists.
	ScrollThumb    lipgloss.Style // active panel, search, bulk list
	ScrollThumbOff lipgloss.Style // inactive panel

	// Phase 5
	ArchiveBadge lipgloss.Style // archive format badge on the path line of a browsing panel
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

	// scheme is the neutral half the theme was built from, kept so
	// SelectedRow can tell a muted foreground from an ordinary one. build
	// fills it in; a Theme built as a literal falls back to darkScheme.
	scheme scheme
}

// FooterKeyPad is the horizontal padding (cells on each side) of a hint-bar
// shortcut chip. Lip Gloss renders the padding with the chip's own
// background, so "Esc" appears as a coloured "  Esc  " block.
const FooterKeyPad = 2

// scheme is the neutral half of a theme: the greys everything that is not
// accented or semantic is drawn with, plus the outcome colours, chosen so
// they stay readable on that background. Two schemes exist — a dark one (the
// historic look) and a light one — and every preset picks one and adds its
// own accent and cursor.
type scheme struct {
	light bool

	text     lipgloss.Color // ordinary text
	soft     lipgloss.Color // hint-bar descriptions, status line
	muted    lipgloss.Color // secondary text: labels, summaries, hints
	dim      lipgloss.Color // hidden files, empty states
	faint    lipgloss.Color // dividers, progress background
	edge     lipgloss.Color // border of the panel that is not focused
	lineNo   lipgloss.Color // viewer line numbers
	thumbOff lipgloss.Color // scroll thumb of an inactive panel

	selectedBg lipgloss.Color // row background of a marked entry
	selectedFg lipgloss.Color // muted text on that background

	cursorOffFg lipgloss.Color // the row the cursor was on, panel not focused
	cursorOffBg lipgloss.Color
	pathOffFg   lipgloss.Color // path bar of the inactive panel
	pathOffBg   lipgloss.Color
	inputFg     lipgloss.Color // text field
	inputBg     lipgloss.Color
	inputCursor lipgloss.Color // the caret in a text field
	buttonFg    lipgloss.Color // button that is not focused
	buttonBg    lipgloss.Color
	chipFg      lipgloss.Color // hint-bar shortcut chip
	chipBg      lipgloss.Color
	backdrop    lipgloss.Color // the panes behind a dialog
	badgeFg     lipgloss.Color // text on the neutral badges (LOCAL)

	onAccent lipgloss.Color // text on an accent background (badge, focus button)
	onCursor lipgloss.Color // text on the cursor row
	onWarn   lipgloss.Color // text on a warning-coloured highlight (viewer match)

	ok   lipgloss.Color // success
	warn lipgloss.Color // warnings
	err  lipgloss.Color // errors, broken links, dangerous buttons
	link lipgloss.Color // symbolic links
	exec lipgloss.Color // executable files
}

// darkScheme is the original set of greys: light text on the terminal's dark
// background, pastel accents and pastel outcome colours.
var darkScheme = scheme{
	text:        "252",
	soft:        "250",
	muted:       "244",
	dim:         "243",
	faint:       "238",
	edge:        "239",
	lineNo:      "240",
	thumbOff:    "247",
	selectedBg:  "240",
	selectedFg:  "250",
	cursorOffFg: "252",
	cursorOffBg: "237",
	pathOffFg:   "250",
	pathOffBg:   "236",
	inputFg:     "231",
	inputBg:     "236",
	inputCursor: "252",
	buttonFg:    "252",
	buttonBg:    "238",
	chipFg:      "16",
	chipBg:      "249",
	backdrop:    "240",
	badgeFg:     "16",
	onAccent:    "16",
	onCursor:    "16",
	onWarn:      "16",
	ok:          "114",
	warn:        "221",
	err:         "203",
	link:        "80",
	exec:        "114",
}

// lightScheme is the same layout with dark text for a light terminal: the
// greys sit below the accent's brightness and the outcome colours are the
// deeper variants, so success and errors stay readable on white.
var lightScheme = scheme{
	light:       true,
	text:        "235",
	soft:        "238",
	muted:       "242",
	dim:         "245",
	faint:       "251",
	edge:        "251",
	lineNo:      "245",
	thumbOff:    "249",
	selectedBg:  "253",
	selectedFg:  "238",
	cursorOffFg: "238",
	cursorOffBg: "252",
	pathOffFg:   "238",
	pathOffBg:   "252",
	inputFg:     "232",
	inputBg:     "255",
	inputCursor: "255",
	buttonFg:    "236",
	buttonBg:    "252",
	chipFg:      "255",
	chipBg:      "240",
	backdrop:    "248",
	badgeFg:     "16",
	onAccent:    "231",
	onCursor:    "16",
	onWarn:      "231",
	ok:          "28",
	warn:        "130",
	err:         "160",
	link:        "25",
	exec:        "28",
}

// palette is what a preset adds to a scheme: the accent (borders, badge,
// directories, focus) and the cursor colour. Everything else is derived, so
// the outcome colours mean the same in every theme.
type palette struct {
	accent lipgloss.Color
	cursor lipgloss.Color
	light  bool // use lightScheme: for a light terminal background
}

// presets are the theme names offered by the settings screen, in the order
// they cycle. "default" is the historic blue look and must stay first.
var presets = []struct {
	name string
	pal  palette
}{
	{"default", palette{accent: "75", cursor: "153"}},
	{"green", palette{accent: "78", cursor: "114"}},
	{"amber", palette{accent: "179", cursor: "214"}},
	{"violet", palette{accent: "141", cursor: "177"}},
	{"cyan", palette{accent: "45", cursor: "123"}},
	{"rose", palette{accent: "205", cursor: "218"}},
	{"sand", palette{accent: "180", cursor: "223"}},
	{"mono", palette{accent: "250", cursor: "255"}},
	{"light", palette{accent: "26", cursor: "111", light: true}},
	{"daylight", palette{accent: "29", cursor: "116", light: true}},
	{"plum", palette{accent: "90", cursor: "140", light: true}},
}

// PresetNames lists the available theme presets in cycle order.
func PresetNames() []string {
	out := make([]string, len(presets))
	for i, p := range presets {
		out[i] = p.name
	}
	return out
}

// Preset returns the named theme preset. ok is false for unknown names; the
// caller is expected to fall back to Default().
func Preset(name string) (Theme, bool) {
	for _, p := range presets {
		if p.name == name {
			return build(p.pal), true
		}
	}
	return Theme{}, false
}

// Default returns the built-in dark-friendly theme using ANSI-256 colours,
// which degrade gracefully on 16-colour terminals. It is the "default"
// preset.
func Default() Theme {
	return build(presets[0].pal)
}

func build(p palette) Theme {
	sc := darkScheme
	if p.light {
		sc = lightScheme
	}
	accent := p.accent
	cursor := p.cursor
	muted := sc.muted
	// A dark theme draws the path in a dialog and the scroll thumb in the
	// cursor colour, which is one of its bright colours. On a light background
	// those two are too pale to read, so the darker accent stands in.
	pathIn, thumb := cursor, cursor
	if sc.light {
		pathIn, thumb = accent, accent
	}
	return Theme{
		Accent:    accent,
		Muted:     muted,
		BorderOn:  accent,
		BorderOff: sc.edge,

		PathActive:   lipgloss.NewStyle().Bold(true).Foreground(sc.onAccent).Background(accent),
		PathInactive: lipgloss.NewStyle().Foreground(sc.pathOffFg).Background(sc.pathOffBg),
		ColumnHeader: lipgloss.NewStyle().Foreground(muted).Bold(true),
		Divider:      lipgloss.NewStyle().Foreground(sc.faint),
		Info:         lipgloss.NewStyle().Foreground(sc.text),
		Summary:      lipgloss.NewStyle().Foreground(muted),
		Error:        lipgloss.NewStyle().Foreground(sc.err).Bold(true),
		Empty:        lipgloss.NewStyle().Foreground(muted).Italic(true),

		File:     lipgloss.NewStyle().Foreground(sc.text),
		Dir:      lipgloss.NewStyle().Foreground(accent).Bold(true),
		Link:     lipgloss.NewStyle().Foreground(sc.link),
		Broken:   lipgloss.NewStyle().Foreground(sc.err),
		Exec:     lipgloss.NewStyle().Foreground(sc.exec),
		Hidden:   lipgloss.NewStyle().Foreground(sc.dim),
		Selected: lipgloss.NewStyle().Background(sc.selectedBg),

		CursorActive:   lipgloss.NewStyle().Foreground(sc.onCursor).Background(cursor).Bold(true),
		CursorInactive: lipgloss.NewStyle().Foreground(sc.cursorOffFg).Background(sc.cursorOffBg),

		TitleBadge: lipgloss.NewStyle().Bold(true).Foreground(sc.onAccent).Background(accent).Padding(0, 1),
		TitleText:  lipgloss.NewStyle().Foreground(muted),
		StatusInfo: lipgloss.NewStyle().Foreground(sc.soft),
		StatusErr:  lipgloss.NewStyle().Foreground(sc.err).Bold(true),
		FooterKey:  lipgloss.NewStyle().Foreground(sc.chipFg).Background(sc.chipBg).Bold(true).Padding(0, FooterKeyPad),
		FooterDesc: lipgloss.NewStyle().Foreground(sc.soft),
		HelpBox:    lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(1, 3),
		HelpTitle:  lipgloss.NewStyle().Bold(true).Foreground(accent),
		StatusOK:   lipgloss.NewStyle().Foreground(sc.ok),
		StatusWarn: lipgloss.NewStyle().Foreground(sc.warn).Bold(true),

		Backdrop:          lipgloss.NewStyle().Foreground(sc.backdrop),
		DialogBox:         lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(accent).Padding(0, 2),
		DialogDangerBox:   lipgloss.NewStyle().Border(lipgloss.ThickBorder()).BorderForeground(sc.err).Padding(0, 2),
		DialogTitle:       lipgloss.NewStyle().Bold(true).Foreground(accent),
		DialogDangerTitle: lipgloss.NewStyle().Bold(true).Foreground(sc.err),
		DialogText:        lipgloss.NewStyle().Foreground(sc.text),
		DialogPath:        lipgloss.NewStyle().Foreground(pathIn),
		DialogWarn:        lipgloss.NewStyle().Foreground(sc.warn).Bold(true),
		DialogHint:        lipgloss.NewStyle().Foreground(muted),
		Button:            lipgloss.NewStyle().Foreground(sc.buttonFg).Background(sc.buttonBg).Padding(0, 1),
		ButtonFocus:       lipgloss.NewStyle().Foreground(sc.onAccent).Background(accent).Bold(true).Padding(0, 1),
		ButtonDangerFocus: lipgloss.NewStyle().Foreground(lipgloss.Color("231")).Background(lipgloss.Color("160")).Bold(true).Padding(0, 1),
		Input:             lipgloss.NewStyle().Foreground(sc.inputFg).Background(sc.inputBg),
		InputCursor:       lipgloss.NewStyle().Foreground(sc.onCursor).Background(sc.inputCursor),
		ProgressFill:      lipgloss.NewStyle().Foreground(accent),
		ProgressEmpty:     lipgloss.NewStyle().Foreground(sc.faint),
		NoticeOK:          lipgloss.NewStyle().Foreground(sc.ok),
		NoticeWarn:        lipgloss.NewStyle().Foreground(sc.warn),
		NoticeErr:         lipgloss.NewStyle().Foreground(sc.err),
		NoticeInfo:        lipgloss.NewStyle().Foreground(accent),
		ScrollThumb:       lipgloss.NewStyle().Foreground(thumb).Bold(true),
		ScrollThumbOff:    lipgloss.NewStyle().Foreground(sc.thumbOff),

		ArchiveBadge: lipgloss.NewStyle().Bold(true).Foreground(sc.badgeFg).Background(lipgloss.Color("179")).Padding(0, 1),
		CloudBadge:   lipgloss.NewStyle().Bold(true).Foreground(sc.badgeFg).Background(lipgloss.Color("117")).Padding(0, 1),
		LocalBadge:   lipgloss.NewStyle().Bold(true).Foreground(sc.badgeFg).Background(lipgloss.Color("250")).Padding(0, 1),
		ViewerBadge:  lipgloss.NewStyle().Bold(true).Foreground(sc.badgeFg).Background(lipgloss.Color("179")).Padding(0, 1),
		LineNumber:   lipgloss.NewStyle().Foreground(sc.lineNo),
		Match:        lipgloss.NewStyle().Foreground(sc.onWarn).Background(sc.warn),
		MatchCurrent: lipgloss.NewStyle().Foreground(sc.warn).Bold(true),
		Control:      lipgloss.NewStyle().Foreground(sc.err),
		PropLabel:    lipgloss.NewStyle().Foreground(muted),
		CheckOn:      lipgloss.NewStyle().Foreground(sc.ok).Bold(true),
		CheckFocus:   lipgloss.NewStyle().Foreground(sc.onAccent).Background(accent).Bold(true),

		scheme: sc,
	}
}

// SelectedRow returns the style for a selected row whose normal style is
// st: the same text colour on the Selected background. Muted text (hidden
// files, empty-state messages, line numbers) is moved to the scheme's
// selected-text colour, so it stays readable whether the theme is dark or
// light.
func (t Theme) SelectedRow(st lipgloss.Style) lipgloss.Style {
	sc := t.scheme
	if sc.muted == "" {
		sc = darkScheme // a Theme built literal (tests, embedders) has no scheme
	}
	if fg, ok := st.GetForeground().(lipgloss.Color); ok {
		if fg == sc.muted || fg == sc.dim || fg == sc.faint || fg == sc.thumbOff || fg == sc.lineNo {
			st = st.Foreground(sc.selectedFg)
		}
	}
	return st.Background(t.Selected.GetBackground())
}
