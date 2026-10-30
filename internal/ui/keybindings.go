package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ThatOn3Gu7/TwinPane/internal/activity"
	"github.com/ThatOn3Gu7/TwinPane/internal/keybinding"
)

// Key bindings (keybinding.json, internal/keybinding).
//
// The file holds every binding TwinPane uses. The interface never compares a
// key string it wrote itself: it asks this set by action name, and every label
// it draws (help screen, hint bar, action menu, dialog buttons that re-send a
// shortcut) comes from the same set, so a rebinding shows up everywhere at
// once. Taps synthesise the configured key, not the one that used to be there.
//
// Loading is quiet by design: a missing file is created with the defaults, bad
// entries are skipped with a warning in the status line (and the activity log),
// and a damaged or newer file is never overwritten.

// loadBindings resolves the session's key map. It runs once, before the
// panels load, and again when the Settings screen reloads the file.
func (a *App) loadBindings() {
	if a.opts.Bindings != nil {
		a.binds = a.opts.Bindings
		a.keybindPath = ""
		a.keybindErr, a.keybindWarnings = nil, nil
		a.keys = keyMapFrom(a.binds)
		return
	}
	if a.opts.KeybindingPathError != "" {
		a.keybindErr = fmt.Errorf("%s", a.opts.KeybindingPathError)
	}
	loaded := keybinding.Load(a.opts.KeybindingPath)
	a.binds = loaded.Set
	a.keybindPath = loaded.Path
	a.keybindCreated, a.keybindUpdated = loaded.Created, loaded.Updated
	a.keybindWarnings = loaded.Set.Warnings()
	if loaded.Err != nil {
		a.keybindErr = loaded.Err
	} else if a.opts.KeybindingPathError != "" {
		a.keybindErr = fmt.Errorf("%s", a.opts.KeybindingPathError)
	}
	if loaded.WriteErr != nil {
		a.keybindErr = fmt.Errorf("key bindings not saved: %w", loaded.WriteErr)
	}
	a.keys = keyMapFrom(a.binds)
}

// keybindNotices is the start-up status line text about the file.
func (a *App) keybindNotices() []string {
	var out []string
	if a.keybindErr != nil {
		out = append(out, "key bindings: "+a.keybindErr.Error())
	}
	if len(a.keybindWarnings) > 0 {
		out = append(out, "key bindings: "+strings.Join(a.keybindWarnings, " · "))
	}
	return out
}

// reloadBindings re-reads keybinding.json, for the Settings row. It reports
// the result on the status line and in the activity log.
func (a *App) reloadBindings() tea.Cmd {
	if a.opts.Bindings != nil || a.opts.KeybindingPath == "" {
		a.setStatusLevel("key bindings are not loaded from a file in this session", lvlWarn)
		return nil
	}
	loaded := keybinding.Load(a.opts.KeybindingPath)
	a.binds = loaded.Set
	a.keybindPath = loaded.Path
	a.keybindCreated, a.keybindUpdated = loaded.Created, loaded.Updated
	a.keybindWarnings = loaded.Set.Warnings()
	a.keybindErr = loaded.Err
	if loaded.WriteErr != nil {
		a.keybindErr = fmt.Errorf("key bindings not saved: %w", loaded.WriteErr)
	}
	a.keys = keyMapFrom(a.binds)
	if a.actLog != nil {
		a.logf(activity.LevelInfo, activity.CatSettings, "keybindings.reload", "%s: %d customised", a.keybindPath, len(a.binds.Diff()))
	}
	// The outcome goes to the notification, not to the row as well: it is a
	// result, and the row would print the same sentence (cut short) underneath
	// the pop-up that shows it whole. The activity log keeps the line.
	switch {
	case a.keybindErr != nil:
		msg := a.keybindErr.Error()
		a.noteResult(lvlErr, "key bindings: "+msg, "Key bindings not reloaded", msg)
	case len(a.keybindWarnings) > 0:
		joined := strings.Join(a.keybindWarnings, " · ")
		a.noteResult(lvlWarn, "key bindings reloaded with warnings: "+joined,
			"Key bindings reloaded with problems", a.keybindWarnings[0], a.keybindWarnings[1:]...)
	case len(a.binds.Diff()) == 0:
		a.noteResult(lvlInfo, "key bindings reloaded: every default in place",
			"Key bindings reloaded", "Every default in place")
	default:
		n := len(a.binds.Diff())
		a.noteResult(lvlInfo, fmt.Sprintf("key bindings reloaded: %d changed", n),
			"Key bindings reloaded", fmt.Sprintf("%d of the defaults changed", n))
	}
	return nil
}

// bindingsSummary describes the state of the file for the Settings row.
func (a *App) bindingsSummary() string {
	switch {
	case a.opts.Bindings != nil:
		return "defaults (not from a file)"
	case a.keybindPath == "":
		return "defaults"
	case a.keybindErr != nil:
		return "defaults — the file could not be used"
	case len(a.keybindWarnings) > 0:
		n := len(a.binds.Diff())
		return fmt.Sprintf("%d changed · %d warnings", n, len(a.keybindWarnings))
	default:
		n := len(a.binds.Diff())
		if n == 0 {
			return "all defaults"
		}
		return fmt.Sprintf("%d changed", n)
	}
}

// bindingsDetail is the Settings row's note: the file path, shortened.
func (a *App) bindingsDetail() string {
	if a.opts.Bindings != nil || a.keybindPath == "" {
		return "keybinding.json"
	}
	return shortPath(a.keybindPath)
}

// --- helpers used by the dispatch code -------------------------------------

// is reports whether msg means the named action. Unknown or disabled actions
// never match.
func (a *App) is(action string, msg tea.KeyMsg) bool {
	return a.binds != nil && a.binds.Matches(action, msg)
}

// isAny reports whether msg means one of the named actions.
func (a *App) isAny(msg tea.KeyMsg, actions ...string) bool {
	for _, action := range actions {
		if a.is(action, msg) {
			return true
		}
	}
	return false
}

// label is the display label of an action ("c/F5").
func (a *App) label(action string) string {
	if a.binds == nil {
		return ""
	}
	return a.binds.Label(action)
}

// primaryLabel is the display label of an action's first key ("^L").
func (a *App) primaryLabel(action string) string {
	if a.binds == nil {
		return ""
	}
	return a.binds.PrimaryLabel(action)
}

// actionMsg is the message a synthetic press of the action sends.
func (a *App) actionMsg(action string) tea.KeyMsg {
	if a.binds == nil {
		return tea.KeyMsg{}
	}
	return a.binds.Msg(action)
}

// actionEnabled reports whether the action has a key.
func (a *App) actionEnabled(action string) bool {
	return a.binds != nil && a.binds.Enabled(action)
}

// burstSafe reports whether a rune may continue a type-ahead burst. Only the
// movement and selection keys do, and only when they are single-rune keys, so
// a burst never starts an operation (see the burst handling in Update).
func (a *App) burstSafe(r rune) bool {
	for _, action := range []string{"browse.up", "browse.down", "browse.select"} {
		if a.binds == nil {
			break
		}
		for _, k := range a.binds.Keys(action) {
			if k == string(r) {
				return true
			}
		}
	}
	return r == ' '
}
