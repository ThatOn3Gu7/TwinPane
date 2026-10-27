// Command twinpane is a keyboard-driven, dual-pane terminal file manager.
package main

import (
	"flag"
	"fmt"
	"os"
	"runtime"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/activity"
	"github.com/ThatOn3Gu7/TwinPane/internal/editor"
	"github.com/ThatOn3Gu7/TwinPane/internal/history"
	"github.com/ThatOn3Gu7/TwinPane/internal/settings"
	"github.com/ThatOn3Gu7/TwinPane/internal/ui"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	noMouse := flag.Bool("no-mouse", false, "do not use mouse/touch input (lets the terminal select text normally)")
	noTips := flag.Bool("no-editor-tips", false, "do not show the editor's save/quit keys before it starts")
	logPath := flag.String("log", "", "activity log file (default: next to settings.json)")
	logMode := flag.String("log-mode", "", "what the activity log records: off, warnings, actions (default) or verbose")
	logSize := flag.Int("log-size", 0, "rotate the activity log at this many KiB (64–16384)")
	logDays := flag.Int("log-days", 0, "delete rotated activity logs older than this many days (1–365)")
	noLog := flag.Bool("no-log", false, "write no activity log for this run")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "TwinPane %s — dual-pane terminal file manager\n\n", version)
		fmt.Fprintf(os.Stderr, "Usage: twinpane [--version] [--no-mouse] [--no-editor-tips] [--log FILE]\n\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nBoth panels open in your home directory. Press ? inside the app for keys.\n")
		fmt.Fprintf(os.Stderr, "TWINPANE_NO_MOUSE=1 has the same effect as --no-mouse.\n")
		fmt.Fprintf(os.Stderr, "The activity log (L) records what TwinPane did, for troubleshooting;\n")
		fmt.Fprintf(os.Stderr, "it never records passwords, text-field contents or search queries.\n")
		fmt.Fprintf(os.Stderr, "TWINPANE_LOG, TWINPANE_LOG_MODE, TWINPANE_LOG_SIZE and TWINPANE_LOG_DAYS\n")
		fmt.Fprintf(os.Stderr, "have the same effect as the --log* flags.\n")
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
	histPath, histErr := history.DefaultPath()
	if histErr != nil {
		opts.HistoryPathError = histErr.Error()
	} else {
		opts.HistoryPath = histPath
	}
	// The activity log: a file next to settings.json unless --log/TWINPANE_LOG
	// says otherwise. --no-log and --log-mode off both mean "write nothing".
	// Every other --log* value is only a per-run override; the durable values
	// are the ones in the Settings screen.
	logModeValue := firstNonEmpty(*logMode, os.Getenv("TWINPANE_LOG_MODE"))
	if *noLog {
		logModeValue = "off"
	}
	opts.ActivityMode = logModeValue
	logFile := firstNonEmpty(*logPath, os.Getenv("TWINPANE_LOG"))
	if logFile == "" {
		p, err := activity.DefaultPath()
		if err != nil {
			opts.ActivityPathError = err.Error()
		} else {
			logFile = p
		}
	}
	opts.ActivityPath = logFile
	if n := firstNonZero(*logSize, envInt("TWINPANE_LOG_SIZE")); n > 0 {
		opts.ActivityMaxKiB = settings.ClampActivityMaxKiB(n)
	}
	if n := firstNonZero(*logDays, envInt("TWINPANE_LOG_DAYS")); n > 0 {
		opts.ActivityKeepDays = settings.ClampActivityKeepDays(n)
	}

	// A log path that cannot be resolved is reported by the interface, like
	// the settings and history paths.
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

// firstNonEmpty returns the first value that is not empty.
func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// firstNonZero returns the first value that is not zero.
func firstNonZero(values ...int) int {
	for _, v := range values {
		if v != 0 {
			return v
		}
	}
	return 0
}

// envInt reads a positive integer from the environment (0 when unset or not a
// number, which leaves the default in place).
func envInt(name string) int {
	v := os.Getenv(name)
	if v == "" {
		return 0
	}
	n := 0
	for _, r := range v {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int(r-'0')
		if n > 1<<20 {
			return 0
		}
	}
	return n
}
