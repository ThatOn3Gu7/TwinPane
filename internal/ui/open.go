package ui

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/archive"
	"github.com/ThatOn3Gu7/TwinPane/internal/editor"
	"github.com/ThatOn3Gu7/TwinPane/internal/fsys"
	"github.com/ThatOn3Gu7/TwinPane/internal/opener"
	"github.com/ThatOn3Gu7/TwinPane/internal/panel"
)

// Opening files in other apps (o, O, Enter on media files, Share).
//
// Package opener decides the file type and the platform's opener
// (termux-open in Termux, xdg-open in a graphical Linux session, open on
// macOS). External openers are started detached (opener.Launch, through
// App.launch) in a tea.Cmd, so the UI never waits for them; the file is
// passed as one argument, never through a shell. "Run a program…" runs a
// terminal program in the foreground like the editor (editor.Process).

// openDoneMsg reports a finished external launch.
type openDoneMsg struct {
	name  string // file name
	how   string // "android", "share", or the opener's name
	err   error
	plain bool // a desktop opener (the result is known)
}

// runDoneMsg reports the end of a "Run a program…" command.
type runDoneMsg struct {
	prog, name string
	err        error
}

func (a *App) platform() opener.Platform {
	return opener.DetectPlatform(opener.Env{Getenv: a.getenv, LookPath: a.lookPath, GOOS: a.goos})
}

// platCache keeps the platform for the hint bar, which is rendered on
// every frame: detection looks programs up on $PATH. Actions always detect
// afresh (a.platform).
type platCache struct {
	key  string
	at   time.Time
	plat opener.Platform
}

const platCacheTTL = 5 * time.Second

func (a *App) cachedPlatform() opener.Platform {
	key := a.goos + "\x00" + a.getenv("TERMUX_VERSION") + "\x00" + a.getenv("PREFIX") + "\x00" +
		a.getenv("DISPLAY") + "\x00" + a.getenv("WAYLAND_DISPLAY") + "\x00" + a.getenv("PATH")
	now := time.Now()
	if c := a.platCache; c != nil && c.key == key && now.Sub(c.at) < platCacheTTL {
		return c.plat
	}
	a.platCache = &platCache{key: key, at: now, plat: a.platform()}
	return a.platCache.plat
}

// fileUnderCursor returns the file the open actions apply to: the entry
// under the cursor, which must be a file on the real filesystem.
func (a *App) fileUnderCursor(what string) (*panel.Panel, fsys.Entry, bool) {
	p := a.ActivePanel()
	if p.InArchive() {
		a.setStatus(what+" works on real files: extract it first (x), or view it with v", true)
		return p, fsys.Entry{}, false
	}
	e, ok := p.Current()
	if !ok || e.IsParent {
		a.setStatus(what+": put the cursor on a file", true)
		return p, e, false
	}
	if e.IsDir {
		a.setStatus(what+" is for files — ⏎ opens a folder", true)
		return p, e, false
	}
	return p, e, true
}

// openExternal opens the file under the cursor with the default app (o).
func (a *App) openExternal() tea.Cmd {
	_, e, ok := a.fileUnderCursor("Opening in another app")
	if !ok {
		return nil
	}
	plat := a.platform()
	argv, ok := plat.DefaultArgv(e.Path)
	if !ok {
		a.setStatus("cannot open "+fsys.SanitizeName(e.Name)+" in another app: "+plat.Why+" — v views it here, O shows other ways", true)
		return nil
	}
	return a.launchCmd(argv, e.Name, plat, "")
}

// launchCmd runs argv in the background and reports with openDoneMsg.
func (a *App) launchCmd(argv []string, name string, plat opener.Platform, how string) tea.Cmd {
	if how == "" {
		how = plat.Name
		if plat.Termux {
			how = "android"
		}
	}
	a.setStatus(fmt.Sprintf("opening %s with %s…", fsys.SanitizeName(name), plat.Name), false)
	launch := a.launch
	argv = append([]string(nil), argv...)
	return func() tea.Msg {
		return openDoneMsg{name: name, how: how, err: launch(argv), plain: !plat.Termux}
	}
}

func (a *App) openDone(m openDoneMsg) {
	name := fsys.SanitizeName(m.name)
	switch {
	case m.err != nil:
		a.setStatus("could not open "+name+": "+errText(m.err), true)
	case m.how == "share":
		a.setStatusLevel("sent "+name+" to Android's share sheet (termux-open --send)", lvlOK)
	case m.how == "android":
		// termux-open only hands the file to Android: if no app handles
		// it, Android shows nothing and nothing is reported back
		a.setStatusLevel("handed "+name+" to Android (termux-open) — if nothing opens, no app handles this type: O → choose app", lvlOK)
	default:
		a.setStatusLevel("opened "+name+" with "+m.how, lvlOK)
	}
}

// preferExternal reports whether Enter / double tap should open the file
// with the default app: media and documents (not the ZIP family TwinPane
// browses itself), when the platform has an opener.
func (a *App) preferExternal(e fsys.Entry) (opener.Platform, bool) {
	t := opener.ByName(e.Name)
	if !t.Kind.Media() || looksLikeZip(e.Name) {
		return opener.Platform{}, false
	}
	plat := a.platform()
	return plat, plat.Prefers(t)
}

// hintPrefersExternal is preferExternal for the hint bar (cached platform).
func (a *App) hintPrefersExternal(e fsys.Entry) bool {
	t := opener.ByName(e.Name)
	if !t.Kind.Media() || looksLikeZip(e.Name) {
		return false
	}
	return a.cachedPlatform().Prefers(t)
}

// openWith shows the Open-with list for the file under the cursor (O).
func (a *App) openWith() tea.Cmd {
	p, e, ok := a.fileUnderCursor("Open with")
	if !ok {
		return nil
	}
	plat := a.platform()
	t := opener.Detect(e.Path)
	zip := archive.Sniff(e.Path)
	choices := plat.Choices(t, e.Path, zip)
	d := &dialog{kind: dlgOpenWith, title: "Open " + fsys.SanitizeName(e.Name) + " with…", width: 60}
	kind := t.Kind.String()
	if t.MIME != "" {
		kind += " (" + t.MIME + ")"
	}
	d.body = []dline{text("Type: " + kind)}
	if t.Kind == opener.APK && plat.Termux {
		d.body = append(d.body, hint("Installing an APK needs Termux to be allowed to install unknown apps (Android settings)."))
	}
	for _, c := range choices {
		it := listItem{label: c.Label, detail: c.Detail, disabled: c.Disabled}
		if c.Disabled {
			it.detail = "unavailable"
			it.info = c.Why
		}
		d.list = append(d.list, it)
	}
	for i, c := range choices {
		if !c.Disabled {
			d.listCursor = i
			break
		}
	}
	if choices[0].Disabled {
		d.errLine = "No app can be opened here: " + choices[0].Why
	}
	d.onPick = func(a *App, i int) tea.Cmd {
		c := choices[i]
		a.dialog = nil
		switch c.Action {
		case opener.ActExternal:
			return a.launchCmd(c.Argv, e.Name, plat, "")
		case opener.ActShare:
			return a.launchCmd(c.Argv, e.Name, plat, "share")
		case opener.ActView:
			return a.openViewer(p, e)
		case opener.ActEdit:
			return a.editPaths([]string{e.Path}, false)
		case opener.ActBrowse:
			a.setStatus("opening "+fsys.SanitizeName(e.Name)+"…", false)
			return p.OpenArchive(e.Path)
		case opener.ActCustom:
			return a.runWithDialog(e)
		}
		return nil
	}
	a.dialog = d
	return nil
}

// shareFile shares the file under the cursor (Termux only; menu).
func (a *App) shareFile() tea.Cmd {
	_, e, ok := a.fileUnderCursor("Share")
	if !ok {
		return nil
	}
	plat := a.platform()
	argv, ok := plat.ShareArgv(e.Path)
	if !ok {
		why := "sharing needs Termux (termux-open --send)"
		if plat.Termux {
			why = plat.Why
		}
		a.setStatus("cannot share: "+why, true)
		return nil
	}
	return a.launchCmd(argv, e.Name, plat, "share")
}

// lastRunWith remembers the last "Run a program…" command (session only).
var lastRunWith = ""

// runWithDialog asks for a program to run with the file as its last
// argument, in this terminal (e.g. "mpv", "less -R", "hexdump -C").
func (a *App) runWithDialog(e fsys.Entry) tea.Cmd {
	d := &dialog{kind: dlgRunWith, title: "Run a program with " + fsys.SanitizeName(e.Name)}
	d.body = []dline{path(e.Path), blank(),
		hint("The program runs in this terminal with the file as its last argument; TwinPane returns when it exits. No shell is used: write the program and its options (quotes group words), not a shell command.")}
	d.input = newLineInput(lastRunWith)
	d.inputLabel = "Program and options:"
	d.footer = "Enter run · Esc cancel"
	d.onSubmit = func(a *App, v string) tea.Cmd {
		args, err := editor.SplitArgs(v)
		if err != nil {
			a.dialog.errLine = err.Error()
			return nil
		}
		if len(args) == 0 {
			a.dialog.errLine = "type the name of a program, e.g. less"
			return nil
		}
		prog := args[0]
		if strings.HasPrefix(prog, "~/") {
			prog = filepath.Join(fsys.HomeDir(), prog[2:])
		}
		path, err := a.lookPath(prog)
		if err != nil {
			a.dialog.errLine = fmt.Sprintf("%s was not found (not on $PATH, or not executable)", args[0])
			return nil
		}
		lastRunWith = v
		a.dialog = nil
		cmd := &exec.Cmd{Path: path, Args: append(append([]string{path}, args[1:]...), e.Path), Dir: filepath.Dir(e.Path)}
		a.editing = true
		a.setStatus("running "+args[0]+"…", false)
		pr := editor.NewProcess(cmd)
		name := e.Name
		return a.execFn(pr, func(err error) tea.Msg { return runDoneMsg{prog: args[0], name: name, err: err} })
	}
	a.dialog = d
	return nil
}

func (a *App) runDone(m runDoneMsg) tea.Cmd {
	a.editing = false
	a.quietUntil = a.now().Add(quietAfterEdit)
	var mouse tea.Cmd
	if a.opts.Mouse {
		mouse = tea.EnableMouseCellMotion
	}
	var xe *editor.ExitError
	switch {
	case m.err == nil:
		a.setStatusLevel(m.prog+" finished ("+fsys.SanitizeName(m.name)+")", lvlOK)
	case errors.As(m.err, &xe) && xe.Signal != 0:
		a.setStatusLevel(fmt.Sprintf("%s was terminated (%s)", m.prog, xe.Signal), lvlWarn)
	case errors.As(m.err, &xe):
		a.setStatusLevel(fmt.Sprintf("%s exited with status %d", m.prog, xe.Code), lvlWarn)
	default:
		a.setStatus("could not run "+m.prog+": "+errText(m.err), true)
	}
	// the program may have changed files
	return tea.Batch(tea.WindowSize(), mouse, a.panels[0].Refresh(), a.panels[1].Refresh())
}
