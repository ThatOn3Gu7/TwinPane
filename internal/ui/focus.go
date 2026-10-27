package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ThatOn3Gu7/TwinPane/internal/cloud"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/panel"
	"github.com/ThatOn3Gu7/TwinPane/internal/textview"
)

// Focus mode: one panel at the full width of the terminal.
//
// It is a presentation switch, not a second file manager. The same two
// panels stay loaded, keep their paths, cursors, selections, scroll
// positions, sort order, hidden-file setting, navigation history and source,
// and the same keys reach the same handlers; only the drawing changes (the
// active panel gets every column, the other panel is not drawn) plus a few
// additions that the extra room makes possible:
//
//   - a breadcrumb path line with a source badge and a type column
//     (panel.SetWide / panel.View);
//   - an optional preview/details pane on the right (wide terminals only —
//     narrow ones keep the file list and use v for the full-width viewer);
//   - a destination chooser for copy/move instead of the hidden panel
//     (focus_dest.go);
//   - Ctrl+G to type a local path or a remote location.
//
// The hidden panel is never resized while focus mode is active, so nothing
// about it can be clamped away; leaving focus mode re-runs the normal
// two-pane layout with everything intact.

const (
	// The preview pane needs a wide, tall enough terminal; below that the
	// file list keeps the width (v still opens the full-width viewer).
	focusPreviewMinWidth  = 100
	focusPreviewMinHeight = 18
	focusPreviewMinPaneW  = 34
	focusPreviewMaxPaneW  = 64
	// focusPreviewMinListW is the narrowest file list the preview may leave.
	focusPreviewMinListW = 50
	// focusPreviewMaxBytes bounds the text a preview pane reads (the viewer
	// stays the place for whole files).
	focusPreviewMaxBytes = 64 << 10
	focusPreviewMaxLines = 400
)

// focusKey is the shortcut, as tea reports it: a plain Ctrl-only combination
// that Termux and on-screen keyboards (Gboard) send as an ordinary key
// event, with no enhanced keyboard protocol. Ctrl+F stays Page Down.
const focusKey = "ctrl+o"

// keyFocusMode is a synthetic key used by the hint-bar chip and the action
// menu to run the same toggle, exactly like the other menu entries re-send
// their shortcut, so the mode stays reachable by touch and by mouse anywhere.
const keyFocusMode tea.KeyType = 200

// FocusMode reports whether focus mode (one panel, full width) is active.
func (a *App) FocusMode() bool { return a.focus }

// FocusPreview reports whether the focus-mode preview pane is switched on.
func (a *App) FocusPreview() bool { return a.focusPreview }

// ---- layout ------------------------------------------------------------------

// focusPreviewAvail reports whether there is room for a preview pane.
func (a *App) focusPreviewAvail() bool {
	if !a.focus || a.width < focusPreviewMinWidth || a.height < focusPreviewMinHeight {
		return false
	}
	return a.width-a.focusPreviewPaneWidth() >= focusPreviewMinListW
}

// focusPreviewPaneWidth is the width the preview pane would take.
func (a *App) focusPreviewPaneWidth() int {
	w := a.width * 38 / 100
	if w < focusPreviewMinPaneW {
		w = focusPreviewMinPaneW
	}
	if w > focusPreviewMaxPaneW {
		w = focusPreviewMaxPaneW
	}
	return w
}

// focusPreviewWidth is the width the preview pane actually takes (0 when it
// is off or there is no room for it).
func (a *App) focusPreviewWidth() int {
	if !a.focusPreview || !a.focusPreviewAvail() {
		return 0
	}
	return a.focusPreviewPaneWidth()
}

// focusPanelWidth is the width of the visible panel in focus mode.
func (a *App) focusPanelWidth() int {
	if pw := a.focusPreviewWidth(); pw > 0 {
		return a.width - pw
	}
	return a.width
}

// syncMode applies the current mode to both panels. It runs after every
// message, so Tab (which changes the active panel) and a resize both keep
// the wide layout in step. The panel that is not shown keeps the size it had
// in two-pane mode: resizing it would clamp its scroll offset and cursor.
func (a *App) syncMode() {
	ph := a.height - 3
	if ph < 0 {
		ph = 0
	}
	if a.focus {
		a.panels[a.active].SetWide(true)
		a.panels[1-a.active].SetWide(false)
		a.panels[a.active].SetSize(a.focusPanelWidth(), ph)
		return
	}
	for _, p := range a.panels {
		p.SetWide(false)
	}
}

// toggleFocus switches between one panel and two panels.
func (a *App) toggleFocus() tea.Cmd {
	a.focus = !a.focus
	a.focusPrevScroll = 0
	a.focusPrev = focusPreview{}
	a.layout()
	a.syncMode()
	if a.focus {
		// The title bar already names the panel ("focus · left panel"), so
		// the status line spends its room on the ways out.
		a.setStatusLevel("focus mode — Tab switches panels, Ctrl+O or M → Two panes shows both again", lvlOK)
	} else {
		a.setStatusLevel("two panes — Ctrl+O or M → Focus mode shows one panel at full width", lvlOK)
	}
	return nil
}

// togglePreview switches the focus-mode preview/details pane.
func (a *App) togglePreview() tea.Cmd {
	if !a.focus {
		return nil
	}
	if !a.focusPreviewAvail() {
		a.setStatusLevel(fmt.Sprintf("the preview pane needs at least %d×%d — v opens the read-only viewer full width instead",
			focusPreviewMinWidth, focusPreviewMinHeight), lvlWarn)
		return nil
	}
	a.focusPreview = !a.focusPreview
	a.focusPrevScroll = 0
	a.layout()
	a.syncMode()
	if a.focusPreview {
		a.setStatusLevel("preview pane on (p hides it)", lvlOK)
	} else {
		a.setStatusLevel("preview pane off (p shows it, v opens the viewer)", lvlOK)
	}
	return nil
}

// ---- rendering ---------------------------------------------------------------

// focusView draws the active panel full width, with the preview pane beside
// it when there is room.
func (a *App) focusView(ph int) string {
	p := a.ActivePanel()
	left := p.View(a.theme, true)
	pw := a.focusPreviewWidth()
	if pw <= 0 {
		return left
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, left, a.focusPreviewPane(ph, pw))
}

// focusPreviewPane renders the preview/details pane for the entry under the
// cursor. It reuses the read-only text pipeline (textview) for local files,
// reports remote and archive entries with their metadata and points at the
// viewer, and never offers an action that the source does not support.
func (a *App) focusPreviewPane(ph, pw int) string {
	p := a.ActivePanel()
	e, ok := p.Current()
	title, right := "Preview", ""
	var body []string
	if !ok {
		title = "Preview"
		body = []string{"", "  nothing selected"}
	} else {
		pv := a.previewFor(p, e)
		title, right, body = pv.title, pv.right, pv.lines
		if len(body) == 0 {
			body = []string{"", "  (no details)"}
		}
		// scrolling (wheel / drag) over the pane
		rows := max(1, ph-2)
		maxOff := max(0, len(body)-rows)
		if a.focusPrevScroll > maxOff {
			a.focusPrevScroll = maxOff
		}
		if a.focusPrevScroll < 0 {
			a.focusPrevScroll = 0
		}
		body = body[a.focusPrevScroll:]
		var thumb []bool
		if len(body) > rows {
			thumb = make([]bool, rows)
			// a simple proportional thumb, drawn by sectionBox
			pos := a.focusPrevScroll * rows / max(1, a.focusPrevScroll+len(body))
			if pos < rows {
				thumb[pos] = true
			}
		}
		lines := sectionBox(a.theme, title, right, body, pw, ph, true, thumb)
		return strings.Join(lines, "\n")
	}
	return strings.Join(sectionBox(a.theme, title, right, body, pw, ph, true, nil), "\n")
}

// focusPreview is the cached rendering of the preview pane.
type focusPreview struct {
	key         string
	title       string
	right       string
	lines       []string
	scrollReset bool
}

// previewFor returns the preview for e, reloading it only when the entry (or
// its size/time) changed: View runs on every frame, so the text is read once
// per selection instead of once per repaint.
func (a *App) previewFor(p *panel.Panel, e fsys.Entry) focusPreview {
	key := fmt.Sprintf("%s\x00%d\x00%d\x00%t\x00%t", e.Path, e.Size, e.ModTime.UnixNano(), p.InRemote(), p.InArchive())
	if a.focusPrev.key == key {
		return a.focusPrev
	}
	a.focusPrev = a.buildPreview(p, e)
	a.focusPrev.key = key
	a.focusPrevScroll = 0
	return a.focusPrev
}

func (a *App) buildPreview(p *panel.Panel, e fsys.Entry) focusPreview {
	name := fsys.SanitizeName(e.Name)
	pv := focusPreview{title: name}
	kind := "file"
	switch {
	case e.IsParent:
		kind = "parent folder"
	case e.IsLink && e.IsDir, e.IsLink:
		kind = "link"
	case e.IsDir:
		kind = "folder"
	case fsys.TypeOf(e.Name) != "":
		kind = fsys.TypeOf(e.Name) + " file"
	}
	pv.right = kind
	if e.IsParent {
		pv.lines = []string{"", "  Enter (or ←) goes to the parent folder."}
		return pv
	}

	var lines []string
	add := func(label, value string) {
		if value == "" {
			return
		}
		lines = append(lines, " "+a.theme.PropLabel.Render(fmt.Sprintf("%-9s", label))+" "+value)
	}
	add("Name", name)
	if p.InRemote() {
		src := p.Source()
		kind := ""
		if src != nil {
			kind = " (" + src.Remote.Type + ")"
		}
		add("Remote", fsys.SanitizeName(p.Location().Remote)+":"+kind)
		add("Path", fsys.SanitizeName(p.Location().Display()))
	} else if p.InArchive() {
		add("Archive", fsys.SanitizeName(p.Archive().Format.Name()))
		add("Entry", fsys.SanitizeName(p.Archive().Display(p.ArchiveInner())))
	} else {
		add("Folder", fsys.SanitizeName(filepath.Dir(e.Path)))
	}
	if !e.ModTime.IsZero() {
		add("Modified", e.ModTime.Format("2006-01-02 15:04:05"))
	}
	switch {
	case e.IsDir:
		add("Size", "—")
	case e.StatErr != nil:
		add("Size", "?")
	default:
		add("Size", fmt.Sprintf("%s (%d bytes)", fsys.HumanSize(e.Size), e.Size))
	}
	if p.InArchive() {
		// archive entries have no real permissions on disk
	} else if e.Mode != 0 {
		add("Mode", e.Mode.String())
	}
	if e.IsLink && e.LinkTarget != "" {
		add("Link", fsys.SanitizeName(e.LinkTarget))
	}
	if e.StatErr != nil {
		add("Problem", fsys.SanitizeName(e.StatErr.Error()))
	}
	lines = append(lines, "")

	switch {
	case p.InRemote():
		lines = append(lines, " "+a.theme.Summary.Render("Remote files are read-only here:"), " "+
			a.theme.Summary.Render("Enter or v previews text (up to 8 MiB),"), " "+
			a.theme.Summary.Render("c copies it to a local panel."))
		if e.IsDir {
			lines = append(lines, "", " "+a.theme.Summary.Render("Enter opens the folder."))
		}
	case p.InArchive():
		lines = append(lines, " "+a.theme.Summary.Render("Read-only archive entry."))
		if !e.IsDir {
			lines = append(lines, " "+a.theme.Summary.Render("Enter or v views it; x extracts."))
		}
	case e.IsDir:
		lines = append(lines, " "+a.theme.Summary.Render("Enter opens the folder."))
	default:
		lines = append(lines, a.previewTextLines(e)...)
	}
	pv.lines = lines
	return pv
}

// previewTextLines reads the first part of a local text file for the preview
// pane. Reading is bounded (focusPreviewMaxBytes) and only happens once per
// selected entry (see previewFor).
func (a *App) previewTextLines(e fsys.Entry) []string {
	muted := func(s string) string { return " " + a.theme.Summary.Render(s) }
	if e.Size > 8<<20 {
		return []string{muted("Too large to preview here —"), muted("v opens the viewer (first 8 MiB).")}
	}
	doc, err := textview.LoadFile(e.Path, textview.Options{
		MaxBytes: focusPreviewMaxBytes, MaxLines: focusPreviewMaxLines, MaxLineBytes: 4096,
	})
	if err != nil {
		return []string{muted("Cannot read this file: " + errText(err)), muted("v shows more.")}
	}
	if doc.Binary {
		return []string{muted("Binary file (" + doc.BinaryReason + ") — not shown."),
			muted("o opens it in its app, v the read-only viewer.")}
	}
	if len(doc.Lines) == 0 {
		return []string{muted("(empty file)")}
	}
	out := make([]string, 0, len(doc.Lines)+1)
	for i, ln := range doc.Lines {
		out = append(out, a.theme.LineNumber.Render(fmt.Sprintf("%4d ", i+1))+textview.Expand(ln))
	}
	if doc.Truncated || doc.Read < doc.Size {
		out = append(out, muted(fmt.Sprintf("… first %s shown — v opens the viewer", fsys.HumanSize(doc.Read))))
	}
	return out
}

// ---- hints and menu -------------------------------------------------------------

// chipItem is a hint whose shortcut stays visible even when the bar has no
// room left for its description.
func chipItem(key, desc, send string) barItem {
	it := item(key, desc, send)
	it.keepChip = true
	return it
}

// focusHints returns the focus-mode chips (the toggle, the preview pane and
// go to path). They are contextual, so they come right after Tab.
func (a *App) focusHints() []barItem {
	if !a.focus {
		return nil
	}
	out := []barItem{chipItem("^O", "two panes", "focus")}
	if a.focusPreviewAvail() {
		label := "preview"
		if a.focusPreview {
			label = "preview on"
		}
		out = append(out, item("p", label, "p"))
	}
	return append(out, item("^G", "go to path", "ctrl+g"))
}

// focusToggleHint is the two-pane counterpart of the focus chip. It is the
// lowest-priority hint: it goes last so it never pushes a working key off a
// narrow bar (the action menu and the help screen always offer it).
func (a *App) focusToggleHint() []barItem {
	if a.focus {
		return nil
	}
	return []barItem{chipItem("^O", "focus", "focus")}
}

// focusMenuEntries adds the mode entries to the action menu, which is the
// discoverable route to every focus-mode feature (and works by touch: tap
// the TwinPane badge).
func (a *App) focusMenuEntries() []menuEntry {
	if !a.focus {
		return []menuEntry{{label: "Focus mode", detail: "one panel, full width", key: "^O",
			run: func() tea.Cmd { return a.toggleFocus() }}}
	}
	preview := "on"
	detail := "details beside the list"
	if !a.focusPreview {
		preview = "off"
		detail = "show details beside the list"
	}
	out := []menuEntry{
		{label: "Two panes", detail: "show both panels again", key: "^O", run: func() tea.Cmd { return a.toggleFocus() }},
	}
	if a.focusPreviewAvail() {
		out = append(out, menuEntry{label: "Preview pane: " + preview, detail: detail, key: "p",
			run: func() tea.Cmd { return a.togglePreview() }})
	}
	return append(out, menuEntry{label: "Go to path…", detail: "local folder or remote:", key: "^G", send: "ctrl+g"})
}

// ---- go to path (Ctrl+G) ---------------------------------------------------------

// openGoTo asks for a local folder or a remote location and navigates there.
// It accepts what the panels themselves use: an absolute local path, ~ (the
// home folder) or a typed remote location such as box:photos, which is the
// same cloud.Path the Sources picker and the panels use.
func (a *App) openGoTo() tea.Cmd {
	p := a.ActivePanel()
	cur := p.RealDir()
	switch {
	case p.InRemote():
		cur = p.Location().Display()
	case cur == "":
		cur = p.Path()
	}
	d := &dialog{kind: dlgGoTo, title: "Go to path", width: 64}
	d.body = []dline{
		text("A local folder (/home/me or ~/notes) or a remote location (box:photos)."),
		path(cur),
	}
	d.input, d.inputLabel = newLineInput(cur), "Path:"
	d.footer = "Enter go · Esc cancel"
	d.onSubmit = func(a *App, v string) tea.Cmd { return a.goToPath(v) }
	a.dialog = d
	return nil
}

// goToPath navigates the active panel to what the user typed.
func (a *App) goToPath(raw string) tea.Cmd {
	s := strings.TrimSpace(raw)
	if s == "" {
		a.dialog.errLine = "type a folder or a remote location"
		return nil
	}
	p := a.ActivePanel()
	// A remote location is what rclone would accept; a local path is
	// everything else (absolute, ~, or relative to the panel's folder).
	if !strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "~") && !strings.HasPrefix(s, ".") && !filepath.IsAbs(s) {
		if loc, ok := cloud.Parse(s); ok && loc.Remote != "" {
			return a.goToRemote(loc)
		}
	}
	if s == "~" {
		s = fsys.HomeDir()
	} else if strings.HasPrefix(s, "~"+string(filepath.Separator)) {
		s = filepath.Join(fsys.HomeDir(), strings.TrimPrefix(s, "~"+string(filepath.Separator)))
	}
	if !filepath.IsAbs(s) {
		s = filepath.Join(p.RealDir(), s)
	}
	s = filepath.Clean(s)
	fi, err := os.Stat(s)
	if err != nil {
		a.dialog.errLine = errText(err)
		return nil
	}
	focus := ""
	if !fi.IsDir() {
		// a file: show its folder with the cursor on it
		focus, s = filepath.Base(s), filepath.Dir(s)
	}
	a.dialog = nil
	a.setStatus("", false)
	return p.Load(s, focus, panel.LoadNavigate)
}

// goToRemote opens a typed remote location, reusing the client of the remote
// the panel already shows or starting rclone for a new one.
func (a *App) goToRemote(loc cloud.Path) tea.Cmd {
	p := a.ActivePanel()
	src := p.Source()
	if src == nil || src.Remote.Name != loc.Remote {
		c, err := cloud.Detect(a.lookPath)
		if err != nil {
			a.dialog.errLine = "cloud remotes need rclone: " + cloud.InstallHint(a.platform().Termux, a.goos)
			return nil
		}
		src = &panel.RemoteSource{Client: c, Remote: cloud.Remote{Name: loc.Remote}}
	}
	a.dialog = nil
	a.setStatus("opening "+fsys.SanitizeName(loc.Display())+"… (Esc cancels)", false)
	return p.OpenRemoteAt(src, loc, "")
}

// ---- mouse ---------------------------------------------------------------------

// focusPreviewAt reports whether screen position (x, y) is over the preview
// pane.
func (a *App) focusPreviewAt(x, y int) bool {
	pw := a.focusPreviewWidth()
	if pw <= 0 || y < 1 || y >= a.height-2 {
		return false
	}
	return x >= a.width-pw
}

// scrollFocusPreview scrolls the preview pane by delta rows.
func (a *App) scrollFocusPreview(delta int) {
	a.focusPrevScroll += delta
	if a.focusPrevScroll < 0 {
		a.focusPrevScroll = 0
	}
}
