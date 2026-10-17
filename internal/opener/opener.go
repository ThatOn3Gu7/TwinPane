package opener

import (
	"fmt"
	"strings"
)

// Env is what platform detection looks at (os.Getenv, proc.LookPath and
// runtime.GOOS in production; fakes in tests).
type Env struct {
	Getenv   func(string) string
	LookPath func(string) (string, error)
	GOOS     string
}

// Platform describes the external openers available.
type Platform struct {
	Termux bool
	// Opener is the absolute path of the program that opens files with
	// the default app (termux-open, xdg-open or open); "" when there is
	// none, with the reason in Why.
	Opener string
	Name   string // its name: "termux-open", "xdg-open", "open"
	Why    string
}

// HasOpener reports whether files can be opened externally.
func (p Platform) HasOpener() bool { return p.Opener != "" }

// DetectPlatform finds the external opener.
//
//   - Termux ($TERMUX_VERSION, a $PREFIX inside com.termux, or an Android
//     build): termux-open, a script of the termux-tools package (installed
//     with Termux; it needs no Termux:API app). It hands the file to
//     Android, which shows it with the default app or a chooser.
//   - Linux and BSD: xdg-open, only in a graphical session ($DISPLAY or
//     $WAYLAND_DISPLAY set) — without one it may start a text-mode browser
//     inside the terminal TwinPane is using.
//   - macOS: open.
func DetectPlatform(env Env) Platform {
	get := env.Getenv
	if get == nil {
		get = func(string) string { return "" }
	}
	look := env.LookPath
	if look == nil {
		look = func(string) (string, error) { return "", fmt.Errorf("not found") }
	}
	var p Platform
	p.Termux = get("TERMUX_VERSION") != "" || strings.Contains(get("PREFIX"), "/com.termux/") || env.GOOS == "android"
	switch {
	case p.Termux:
		p.Name = "termux-open"
		if path, err := look("termux-open"); err == nil {
			p.Opener = path
		} else {
			p.Why = "termux-open was not found (it is part of termux-tools: pkg install termux-tools)"
		}
	case env.GOOS == "darwin":
		p.Name = "open"
		if path, err := look("open"); err == nil {
			p.Opener = path
		} else {
			p.Why = "the open command was not found"
		}
	case env.GOOS == "windows":
		p.Why = "opening files in other apps is not supported on Windows"
	default:
		p.Name = "xdg-open"
		if get("DISPLAY") == "" && get("WAYLAND_DISPLAY") == "" {
			p.Why = "no graphical session ($DISPLAY and $WAYLAND_DISPLAY are not set), so other apps cannot be shown"
		} else if path, err := look("xdg-open"); err == nil {
			p.Opener = path
		} else {
			p.Why = "xdg-open was not found (install xdg-utils)"
		}
	}
	return p
}

// Action is what an Open-with choice does.
type Action int

const (
	ActExternal Action = iota // run Argv (default app, chooser, open as type)
	ActShare                  // run Argv: Android share sheet
	ActView                   // TwinPane's text viewer
	ActEdit                   // the text editor
	ActBrowse                 // browse as a ZIP archive
	ActCustom                 // ask for a program to run with the file
)

// Choice is one entry of the Open-with list.
type Choice struct {
	Action Action
	Label  string
	Detail string
	Argv   []string // for ActExternal / ActShare
	// Disabled choices are listed with Why, so the user learns what is
	// missing.
	Disabled bool
	Why      string
}

// DefaultArgv returns the command that opens path with the default app.
// path must be absolute.
func (p Platform) DefaultArgv(path string) ([]string, bool) {
	if !p.HasOpener() {
		return nil, false
	}
	if p.Termux {
		// "--" ends termux-open's options, so no file name is an option
		return []string{p.Opener, "--", path}, true
	}
	return []string{p.Opener, path}, true
}

// ShareArgv returns the command that shares path (Termux only).
func (p Platform) ShareArgv(path string) ([]string, bool) {
	if !p.Termux || !p.HasOpener() {
		return nil, false
	}
	return []string{p.Opener, "--send", "--", path}, true
}

// asTypes are the content types offered by "open as" for files whose type
// is unknown. They contain no wildcards or spaces: termux-open passes the
// value to `am` unquoted.
var asTypes = []struct{ label, mime string }{
	{"text", "text/plain"},
	{"image", "image/jpeg"},
	{"audio", "audio/mpeg"},
	{"video", "video/mp4"},
}

// Choices returns the Open-with list for a file of type t at the absolute
// path. zip reports that the file can be browsed as a ZIP archive.
func (p Platform) Choices(t Type, path string, zip bool) []Choice {
	var out []Choice
	defLabel := "Default app"
	if p.Termux {
		defLabel = "Android: default app"
	} else if p.Name != "" {
		defLabel = "System default (" + p.Name + ")"
	}
	if argv, ok := p.DefaultArgv(path); ok {
		out = append(out, Choice{Action: ActExternal, Label: defLabel, Detail: "opens it in another app", Argv: argv})
	} else {
		out = append(out, Choice{Action: ActExternal, Label: defLabel, Disabled: true, Why: p.Why})
	}
	if p.Termux && p.HasOpener() {
		if (t.Kind == Unknown || t.Kind == Program) && !zip {
			// Android cannot tell what the file is: let the user say,
			// then choose an app for that type
			for _, at := range asTypes {
				out = append(out, Choice{Action: ActExternal, Label: "Android: open as " + at.label, Detail: at.mime,
					Argv: []string{p.Opener, "--chooser", "--content-type", at.mime, "--", path}})
			}
		} else {
			out = append(out, Choice{Action: ActExternal, Label: "Android: choose app…", Detail: "always asks which app",
				Argv: []string{p.Opener, "--chooser", "--", path}})
		}
	}
	out = append(out, Choice{Action: ActView, Label: "Built-in viewer", Detail: "text, read-only"})
	out = append(out, Choice{Action: ActEdit, Label: "Text editor", Detail: "$VISUAL / $EDITOR"})
	if zip {
		out = append(out, Choice{Action: ActBrowse, Label: "Browse as ZIP archive", Detail: "inside TwinPane"})
	}
	if argv, ok := p.ShareArgv(path); ok {
		out = append(out, Choice{Action: ActShare, Label: "Share…", Detail: "send to another app (Android share sheet)", Argv: argv})
	}
	out = append(out, Choice{Action: ActCustom, Label: "Run a program…", Detail: "in this terminal, with the file as argument"})
	return out
}

// Prefers reports whether Enter should open a file of type t with the
// default app instead of the built-in viewer: media and documents, when an
// external opener exists. Text, unknown files, archives and Android
// packages (browsed as ZIP) keep their built-in handling.
func (p Platform) Prefers(t Type) bool {
	return p.HasOpener() && t.Kind.Media()
}
