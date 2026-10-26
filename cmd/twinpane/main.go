// Command twinpane is a keyboard-driven, dual-pane terminal file manager.
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/editor"
	"github.com/ThatOn3Gu7/TwinPane/internal/settings"
	"github.com/ThatOn3Gu7/TwinPane/internal/ui"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	noMouse := flag.Bool("no-mouse", false, "do not use mouse/touch input (lets the terminal select text normally)")
	noTips := flag.Bool("no-editor-tips", false, "do not show the editor's save/quit keys before it starts")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "TwinPane %s — dual-pane terminal file manager\n\n", version)
		fmt.Fprintf(os.Stderr, "Usage: twinpane [--version] [--no-mouse] [--no-editor-tips]\n\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nBoth panels open in your home directory. Press ? inside the app for keys.\n")
		fmt.Fprintf(os.Stderr, "TWINPANE_NO_MOUSE=1 has the same effect as --no-mouse.\n")
	}
	flag.Parse()
	if *showVersion {
		fmt.Printf("twinpane %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
		return
	}

	restore := editor.SaveTerminal(os.Stdin)
	// Mouse: the durable default lives in settings.json; the flag and
	// TWINPANE_NO_MOUSE still override it for the session (they only ever
	// turn mouse reporting off, as before). The program option has to be
	// decided here — Bubble Tea takes it at startup — so the preferences are
	// loaded once for it; ui.New loads them again for everything else.
	prefsPath, prefsErr := settings.DefaultPath()
	mouse := !*noMouse && os.Getenv("TWINPANE_NO_MOUSE") == ""
	if prefsErr == nil {
		if prefs, err := settings.Load(prefsPath); err == nil {
			mouse = prefs.Mouse
		}
	}
	if *noMouse || os.Getenv("TWINPANE_NO_MOUSE") != "" {
		mouse = false
	}
	opts := ui.Options{Version: version, Mouse: mouse, EditorTips: !*noTips}
	if prefsErr != nil {
		opts.SettingsPathError = prefsErr.Error()
	} else {
		opts.SettingsPath = prefsPath
	}
	if runtime.GOOS == "linux" && onAndroid() {
		opts.Notice = "this is the Linux build running on Android: for Termux install the android-arm64 build"
	}
	app := ui.New(opts)
	progOpts := []tea.ProgramOption{tea.WithAltScreen()}
	if mouse {
		// Button-event tracking (press, release, drag, wheel) with SGR
		// coordinates; in Termux taps and swipes arrive as these events.
		progOpts = append(progOpts, tea.WithMouseCellMotion())
	}
	prog := tea.NewProgram(app, progOpts...)
	if _, err := prog.Run(); err != nil {
		// Bubble Tea restores the terminal itself; this is a safety net so
		// an abnormal exit never leaves the shell in raw mode, on the
		// alternate screen or with mouse/keyboard modes switched on.
		editor.RescueTerminal(os.Stdout)
		restore()
		fmt.Fprintf(os.Stderr, "twinpane: %v\n", err)
		os.Exit(1)
	}
}

// onAndroid reports whether the process runs on Android (e.g. in Termux).
func onAndroid() bool {
	if os.Getenv("ANDROID_ROOT") != "" || os.Getenv("TERMUX_VERSION") != "" {
		return true
	}
	_, err := os.Stat("/system/bin/linker64")
	return err == nil
}
