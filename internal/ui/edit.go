package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/editor"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
)

// ExecFunc runs an external program while the TUI is suspended. The default
// is tea.Exec; tests substitute a fake.
type ExecFunc func(c tea.ExecCommand, fn tea.ExecCallback) tea.Cmd

// quietAfterEdit is how long keys are ignored after the editor returns:
// replies to the editor's terminal queries can arrive late and must not be
// interpreted as TwinPane commands. Variable for tests.
var quietAfterEdit = 250 * time.Millisecond

// editorDoneMsg is delivered when the external editor exits.
type editorDoneMsg struct {
	paths    []string
	editor   string
	err      error
	resumed  int             // times the editor suspended itself and was resumed
	bulkFile string          // set when editing the bulk rename list
	note     string          // why $VISUAL/$EDITOR was not used (editor.Editor.Note)
	before   []fileStamp     // the files before editing (change detection)
	hint     editor.SaveHint // the editor's save/quit keys
	tipKey   string
}

// fileStamp is what TwinPane compares to tell whether an edited file was
// saved: size and modification time (nanoseconds on Linux/Android).
type fileStamp struct {
	size int64
	mod  time.Time
	ok   bool
}

func stampOf(path string) fileStamp {
	fi, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{size: fi.Size(), mod: fi.ModTime(), ok: true}
}

// beginEdit opens the edit targets (the selection, or the focused entry) in
// the default editor: $VISUAL, $EDITOR, else the first detected editor
// (editor.Resolve), without asking. Folders are refused; binary-looking files
// need confirmation.
func (a *App) beginEdit() tea.Cmd { return a.editPanelTargets(false) }

// beginEditWith is beginEdit with the editor chooser (Shift+E).
func (a *App) beginEditWith() tea.Cmd { return a.editPanelTargets(true) }

func (a *App) editPanelTargets(choose bool) tea.Cmd {
	if a.refuseInArchive("Editing") {
		return nil
	}
	targets := a.ActivePanel().Targets()
	if len(targets) == 0 {
		a.setStatus("nothing to edit: put the cursor on a file", true)
		return nil
	}
	paths := make([]string, len(targets))
	for i, e := range targets {
		paths[i] = e.Path
	}
	return a.editPaths(paths, choose)
}

// editPaths validates paths and opens them all in one editor invocation,
// with the default editor or, if choose is set, the one the user picks.
// Used by the panels and the search box.
func (a *App) editPaths(paths []string, choose bool) tea.Cmd {
	return a.editPathsAsk(paths, choose, false)
}

// editPathsAsk is editPaths; with ask set, the editor confirmation card
// is shown every time and the editor starts only when the user chooses
// Open on it (a double tap on a file: a touch can be accidental, and
// nothing may start without an explicit decision).
func (a *App) editPathsAsk(paths []string, choose, ask bool) tea.Cmd {
	var binary []string
	for _, p := range paths {
		name := fsys.SanitizeName(filepath.Base(p))
		info, err := editor.Check(p)
		if errors.Is(err, editor.ErrIsDir) {
			if len(paths) == 1 {
				a.setStatus(fmt.Sprintf("%s is a folder: only files can be edited", name), true)
			} else {
				a.setStatus(fmt.Sprintf("the selection contains the folder %s: only files can be edited", name), true)
			}
			return nil
		}
		if err != nil {
			a.setStatus("cannot edit: "+errText(err), true)
			return nil
		}
		if info.Binary {
			binary = append(binary, p)
		}
	}
	if choose {
		return a.openEditorChooser(paths, binary)
	}
	ed, err := a.resolveEditor()
	if err != nil {
		a.setStatus("cannot edit: "+err.Error(), true)
		return nil
	}
	return a.confirmBinaryThenEdit(ed, paths, binary, ask)
}

// openEditorChooser lists the detected editors (editor.Detect) for the user
// to pick from. Vim and Neovim get extra rows for opening several files in
// tabs or side by side; editors that take one file only are disabled when
// several files are to be opened.
func (a *App) openEditorChooser(paths, binary []string) tea.Cmd {
	eds := editor.Detect(a.edEnv)
	if len(eds) == 0 {
		a.setStatus("no editor found in $PATH (looked for "+strings.Join(editor.Fallbacks(a.edEnv.GOOS), ", ")+"): set $EDITOR or install one", true)
		return nil
	}
	def, defErr := a.resolveEditor()
	var items []listItem
	var choices []editor.Editor
	cursor := -1
	for _, ed := range eds {
		var tags []string
		tags = append(tags, ed.Display())
		if ed.Source != "detected" {
			tags = append(tags, ed.Source)
		}
		isDefault := defErr == nil && def.Path == ed.Path && strings.Join(def.Args, " ") == strings.Join(ed.Args, " ")
		if isDefault {
			tags = append(tags, "default")
		}
		info := ed.Path
		if len(ed.Args) > 0 {
			info += " " + strings.Join(ed.Args, " ")
		}
		it := listItem{label: ed.Label, detail: strings.Join(tags, " · "), info: info, tip: "Save & quit: " + ed.Hint().Short}
		if ed.Single && len(paths) > 1 {
			it.disabled = true
			it.detail += " · one file only"
		}
		if isDefault && cursor < 0 && !it.disabled {
			cursor = len(items)
		}
		items = append(items, it)
		choices = append(choices, ed)
		if ed.VimLike() && len(paths) > 1 {
			for _, v := range []struct{ flag, what string }{{"-p", "tabs"}, {"-O", "side by side"}} {
				ved := ed
				ved.Args = append(append([]string(nil), ed.Args...), v.flag)
				items = append(items, listItem{label: ed.Label + " – " + v.what, detail: ed.Display() + " " + v.flag, info: info + " " + v.flag, tip: "Save & quit: " + ed.Hint().Short})
				choices = append(choices, ved)
			}
		}
	}
	if cursor < 0 {
		for i, it := range items {
			if !it.disabled {
				cursor = i
				break
			}
		}
	}
	if cursor < 0 {
		a.setStatus("none of the detected editors can open several files at once", true)
		return nil
	}
	title := fmt.Sprintf("Open %s with…", fsys.SanitizeName(filepath.Base(paths[0])))
	if len(paths) > 1 {
		title = fmt.Sprintf("Open %d files with…", len(paths))
	}
	d := &dialog{kind: dlgChooser, title: title, list: items, listCursor: cursor, width: 60}
	if defErr == nil && def.Note != "" {
		d.body = append(d.body, warn(def.Note), blank())
	}
	d.onPick = func(a *App, i int) tea.Cmd {
		a.dialog = nil
		ed := choices[i]
		ed.Source, ed.Note = "chosen", ""
		return a.confirmBinaryThenEdit(ed, paths, binary, false) // picking is the decision
	}
	d.footer = "↑↓ choose · 1-9 pick · Enter open · Esc cancel"
	a.dialog = d
	return nil
}

// confirmBinaryThenEdit runs the editor, asking first if some files look
// binary. ask: see editPathsAsk (the binary warning is already a
// confirmation, so it is not followed by a second card).
func (a *App) confirmBinaryThenEdit(ed editor.Editor, paths, binary []string, ask bool) tea.Cmd {
	if len(binary) == 0 {
		return a.runEditor(ed, paths, "", ask)
	}
	title := fmt.Sprintf("%q looks like a binary file", filepath.Base(binary[0]))
	if len(binary) > 1 {
		title = fmt.Sprintf("%d files look like binary files", len(binary))
	}
	d := &dialog{kind: dlgBinary, title: title, danger: true}
	for i, b := range binary {
		if i == 5 {
			d.body = append(d.body, text(fmt.Sprintf("… and %d more", len(binary)-5)))
			break
		}
		d.body = append(d.body, path(b))
	}
	d.body = append(d.body, blank(),
		warn("Text editors can corrupt binary files when saving. Open anyway?"))
	d.buttons = []button{
		{label: "Cancel (Esc)", run: func(a *App) tea.Cmd { a.dialog = nil; return nil }},
		{label: "Open anyway (o)", key: "o", danger: true, run: func(a *App) tea.Cmd {
			a.dialog = nil
			return a.runEditor(ed, paths, "", false)
		}},
	}
	d.footer = "Enter = highlighted button · o open anyway · Esc cancel"
	a.dialog = d
	return nil
}

// runEditor suspends the TUI and runs the editor on paths (see
// editor.Process for the terminal handling). bulkFile marks the bulk rename
// list. With ask set, or when the editor's keys have not been shown yet
// this session, the confirmation card comes first.
func (a *App) runEditor(ed editor.Editor, paths []string, bulkFile string, ask bool) tea.Cmd {
	if ask || (a.opts.EditorTips && a.tipsPref && !a.tipsShown[tipKey(ed)]) {
		return a.showEditTip(ed, paths, bulkFile)
	}
	return a.launchEditor(ed, paths, bulkFile)
}

func tipKey(ed editor.Editor) string { return ed.Hint().Name + "\x00" + ed.Path }

// showEditTip shows the editor confirmation card: which file opens in
// which editor and (with editor tips on) the editor's save/quit keys,
// because once the editor owns the terminal TwinPane cannot show anything.
// The card stays until the user decides: Open (Enter, or a tap on it)
// starts the editor, Cancel (Esc) does not. There is deliberately no timer:
// idle time, scrolling or unrelated input never starts the editor.
// It appears for the first launch of each editor in a session, again
// after an edit that saved nothing, and for every double tap on a file.
func (a *App) showEditTip(ed editor.Editor, paths []string, bulkFile string) tea.Cmd {
	hint := ed.Hint()
	what := "the rename list"
	if bulkFile == "" {
		what = fsys.SanitizeName(filepath.Base(paths[0]))
		if len(paths) > 1 {
			what = fmt.Sprintf("%d files", len(paths))
		}
	}
	d := &dialog{kind: dlgEditTip, title: fmt.Sprintf("Open %s in %s?", what, hint.Name), width: 60}
	if a.opts.EditorTips {
		if hint.Known {
			d.body = append(d.body, text("How to save and quit "+hint.Name+":"))
			for _, part := range strings.Split(hint.Short, " · ") {
				d.body = append(d.body, text("  "+part))
			}
		} else {
			d.body = append(d.body, text("TwinPane does not know "+hint.Name+"'s keys: "+hint.Short+"."))
		}
		if a.edEnv.GOOS == "android" {
			d.body = append(d.body, blank(), tipLine("Termux: Esc and Ctrl are on the extra-keys row above the keyboard."))
		}
		d.body = append(d.body, blank())
	}
	d.body = append(d.body, tipLine("The editor starts when you choose Open; TwinPane comes back when you quit it."))
	// The buttons act only while this card is the open dialog (a stale
	// reference can never start the editor).
	launch := func(a *App) tea.Cmd {
		if a.dialog != d {
			return nil
		}
		a.dialog = nil
		return a.launchEditor(ed, paths, bulkFile)
	}
	cancel := func(a *App) tea.Cmd {
		if a.dialog == d {
			a.dialog = nil
		}
		a.setStatus("editing canceled: the editor was not started", false)
		return nil
	}
	d.buttons = []button{
		{label: "Open (Enter)", run: launch},
		{label: "Cancel (Esc)", run: cancel},
	}
	d.onEsc = cancel
	d.footer = "Enter open · Esc cancel"
	a.dialog = d
	return nil
}

// tipLine is a muted dialog line (hint() is shadowed in showEditTip).
func tipLine(s string) dline { return hint(s) }

// launchEditor suspends the TUI and runs the editor.
func (a *App) launchEditor(ed editor.Editor, paths []string, bulkFile string) tea.Cmd {
	a.tipsShown[tipKey(ed)] = true
	name := ed.Display()
	a.setStatus(fmt.Sprintf("editing with %s (from %s)…", name, ed.Source), false)
	// Keys that arrive before Bubble Tea has fully handed over the terminal,
	// or after it took it back but before editorDoneMsg, are dropped.
	a.editing = true
	proc := editor.NewProcess(ed.Command(paths...))
	note := ed.Note
	before := make([]fileStamp, len(paths))
	for i, p := range paths {
		before[i] = stampOf(p)
	}
	hint := ed.Hint()
	key := tipKey(ed)
	return a.execFn(proc, func(err error) tea.Msg {
		return editorDoneMsg{paths: paths, editor: name, err: err, resumed: proc.Resumed, bulkFile: bulkFile,
			note: note, before: before, hint: hint, tipKey: key}
	})
}

func (a *App) editorDone(m editorDoneMsg) tea.Cmd {
	a.editing = false
	a.quietUntil = a.now().Add(quietAfterEdit)
	// Bubble Tea switches mouse reporting off when it hands the terminal
	// to the editor and does not switch it back on: do it here.
	var mouse tea.Cmd
	if a.opts.Mouse {
		mouse = tea.EnableMouseCellMotion
	}
	if m.bulkFile != "" {
		a.bulkEditorDone(m)
		if m.resumed > 0 && m.err == nil {
			a.setStatusLevel(a.status+" (the editor was suspended and has been resumed)", lvlWarn)
		}
		return tea.Batch(tea.WindowSize(), mouse)
	}
	var what string
	if len(m.paths) == 1 {
		what = fsys.SanitizeName(filepath.Base(m.paths[0]))
	} else {
		what = fmt.Sprintf("%d files", len(m.paths))
	}
	var note string
	if m.resumed > 0 {
		note = " (the editor was suspended and has been resumed: TwinPane cannot be suspended while editing)"
	}
	if m.note != "" {
		note += " — " + m.note + ", used " + m.editor
	}
	// Was anything saved? Compare size and modification time.
	saved := 0
	for i, p := range m.paths {
		if i < len(m.before) {
			if now := stampOf(p); now != m.before[i] {
				saved++
			}
		}
	}
	result := ""
	switch {
	case len(m.before) == 0:
	case saved == 0:
		result = " — nothing saved (unchanged)"
		if m.hint.Known {
			result += ". To save in " + m.hint.Name + ": " + m.hint.Save
		} else if m.hint.Name != "" {
			result += "; " + m.hint.Save
		}
		// show the save/quit card again next time
		delete(a.tipsShown, m.tipKey)
	case len(m.paths) == 1:
		result = " — saved"
	case saved == len(m.paths):
		result = " — all saved"
	default:
		result = fmt.Sprintf(" — %d of %d saved", saved, len(m.paths))
	}
	switch {
	case m.err != nil:
		a.setStatus(editor.DescribeExit(m.editor, m.err)+note, true)
	case note != "":
		// the note (e.g. "$EDITOR was not found") goes first: the save
		// hint after it is long and may be cut off at the screen edge
		a.setStatusLevel("Finished editing "+what+note+result, lvlWarn)
	case saved == 0 && len(m.before) > 0:
		a.setStatusLevel("Finished editing "+what+result, lvlInfo)
	default:
		a.result(lvlOK, "Edit completed", "Finished editing "+fsys.SanitizeName(what)+result)
		a.setStatus("", false)
	}
	// The files may have changed size/time (or been renamed/deleted by the
	// editor): refresh both panels, keeping cursor and selection. The window
	// may have been resized while the editor owned the terminal (TwinPane,
	// in the background, gets no SIGWINCH then), so re-query the size.
	return tea.Batch(a.panels[0].Refresh(), a.panels[1].Refresh(), tea.WindowSize(), mouse)
}

// resolveEditor picks the editor for e / E / help: the preferred editor from
// the settings first, then the historic chain ($VISUAL, $EDITOR, detected
// editors). A broken preference is reported in Editor.Note or the error
// instead of blocking editing.
func (a *App) resolveEditor() (editor.Editor, error) {
	return editor.ResolvePreferred(a.edEnv, a.editorCmd)
}

// editorHelp is the help screen's "External editor" section: the default
// editor and how to save and quit it.
func (a *App) editorHelp() helpSection {
	ed, err := a.resolveEditor()
	if err != nil {
		return helpSection{"External editor", []Binding{{Help: "none", Desc: "set $EDITOR or install one"}}}
	}
	hint := ed.Hint()
	rows := []Binding{{Help: "e uses", Desc: hint.Name + " (" + ed.Source + ")"}}
	first := true
	for _, part := range strings.Split(hint.Short, " · ") {
		for _, wl := range wrapText(part, 28) {
			label := ""
			if first {
				label, first = "save/quit", false
			}
			rows = append(rows, Binding{Help: label, Desc: wl})
		}
	}
	return helpSection{"External editor", rows}
}

// keyBlocked reports whether keyboard input must be ignored right now:
// while the editor runs and during the short quiet period afterwards.
func (a *App) keyBlocked() bool {
	return a.editing || a.now().Before(a.quietUntil)
}

// burstSafe reports runes that may be repeated within one input burst:
// auto-repeated movement and selection keys. Everything else in a burst
// after the first key is dropped (see Update).
func burstSafe(r rune) bool { return strings.ContainsRune("jk ", r) }
