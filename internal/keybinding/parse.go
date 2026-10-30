package keybinding

import (
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

// keys maps a canonical key string (exactly what tea.KeyMsg.String() returns)
// to the message that produces it. Bubble Tea can only report the key names in
// this table: a terminal without an enhanced keyboard protocol cannot tell
// Ctrl+Shift+A from Ctrl+A, so such a combination is rejected rather than
// silently accepted and never fired.
var keys = buildKeys()

// aliases make the file friendlier than Bubble Tea's own names.
var aliases = map[string]string{
	"escape":   "esc",
	"return":   "enter",
	"pageup":   "pgup",
	"pagedown": "pgdown",
	"del":      "delete",
	"ins":      "insert",
	"bs":       "backspace",
	"backtab":  "shift+tab",
	"spacebar": " ",
	"space":    " ",
}

func buildKeys() map[string]tea.KeyMsg {
	out := map[string]tea.KeyMsg{}
	add := func(name string, msg tea.KeyMsg) {
		if name == "" || name == "runes" {
			return
		}
		out[name] = msg
		out["alt+"+name] = tea.KeyMsg{Type: msg.Type, Runes: msg.Runes, Alt: true}
	}
	// Special keys: every KeyType Bubble Tea has a name for. The navigation
	// and function keys are negative values, the control keys 0–127.
	for t := tea.KeyType(-64); t <= tea.KeyType(300); t++ {
		name := t.String()
		if name == "" || name == "runes" {
			continue
		}
		if name == " " {
			add(" ", tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
			continue
		}
		add(name, tea.KeyMsg{Type: t})
	}
	return out
}

// runeMsg builds the message for a single printable rune (space included).
func runeMsg(r rune, alt bool) (tea.KeyMsg, bool) {
	switch {
	case r == ' ':
		return tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}, Alt: alt}, true
	case unicode.IsPrint(r) && r != unicode.ReplacementChar:
		return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: alt}, true
	}
	return tea.KeyMsg{}, false
}

// Parse turns a key written in keybinding.json into the message the terminal
// would produce, and returns its canonical spelling. A single rune is
// case-sensitive ("A" and "a" are different keys); named keys are
// case-insensitive ("ESC", "Esc" and "esc" are the same key).
func Parse(s string) (tea.KeyMsg, string, error) {
	if s == "" {
		return tea.KeyMsg{}, "", fmt.Errorf("the key is empty")
	}
	runes := []rune(s)
	if len(runes) == 1 {
		if msg, ok := runeMsg(runes[0], false); ok {
			return msg, msg.String(), nil
		}
		return tea.KeyMsg{}, "", fmt.Errorf("%q is not a printable key", s)
	}
	// Alt plus a single rune: alt+a, alt+1, alt+?.
	if low := strings.ToLower(strings.TrimSpace(s)); strings.HasPrefix(low, "alt+") || strings.HasPrefix(low, "option+") {
		rest := strings.SplitN(s, "+", 2)
		if len(rest) == 2 {
			if rs := []rune(rest[1]); len(rs) == 1 {
				if msg, ok := runeMsg(rs[0], true); ok {
					return msg, msg.String(), nil
				}
			}
		}
	}
	if strings.TrimSpace(s) == "" {
		if msg, ok := keys[" "]; ok {
			return msg, " ", nil
		}
	}
	name := strings.ToLower(strings.TrimSpace(s))
	name = strings.ReplaceAll(name, "control+", "ctrl+")
	name = strings.ReplaceAll(name, "option+", "alt+")
	// ^X is the label the interface shows for Ctrl+X.
	if strings.HasPrefix(name, "^") && len([]rune(name)) == 2 {
		name = "ctrl+" + strings.ToLower(string([]rune(name)[1]))
	}
	if a, ok := aliases[name]; ok {
		name = a
	}
	if msg, ok := keys[name]; ok {
		return msg, msg.String(), nil
	}
	return tea.KeyMsg{}, "", fmt.Errorf("%q is not a key this terminal can report (try a letter, ctrl+letter, alt+letter, f1–f20, up/down/left/right, home/end, pgup/pgdown, enter, tab, esc, space, backspace, delete, insert)", s)
}

// Valid reports whether s can be used as a binding.
func Valid(s string) bool {
	_, _, err := Parse(s)
	return err == nil
}

// Canonical returns the normalized spelling of a valid key, or s unchanged.
func Canonical(s string) string {
	if _, c, err := Parse(s); err == nil {
		return c
	}
	return s
}

// Match reports whether msg is exactly the key described by s.
func Match(s string, msg tea.KeyMsg) bool {
	_, c, err := Parse(s)
	if err != nil {
		return false
	}
	return msg.String() == c
}

// prettyKeys are how the interface prints special keys in the hint bar and the
// help screen.
var prettyKeys = map[string]string{
	"up":        "↑",
	"down":      "↓",
	"left":      "←",
	"right":     "→",
	"enter":     "⏎",
	"backspace": "⌫",
	"tab":       "Tab",
	"shift+tab": "Shift+Tab",
	"esc":       "Esc",
	" ":         "Space",
	"insert":    "Ins",
	"delete":    "Del",
	"pgup":      "PgUp",
	"pgdown":    "PgDn",
	"home":      "Home",
	"end":       "End",
}

// Pretty renders one canonical key the way TwinPane shows it.
func Pretty(key string) string {
	if p, ok := prettyKeys[key]; ok {
		return p
	}
	if strings.HasPrefix(key, "ctrl+") && len(key) == 6 {
		return "^" + strings.ToUpper(key[5:])
	}
	if strings.HasPrefix(key, "alt+") {
		return "Alt+" + Pretty(strings.TrimPrefix(key, "alt+"))
	}
	if len(key) > 1 && len(key) >= 2 && key[0] == 'f' {
		return "F" + key[1:]
	}
	return key
}

// JoinPretty renders a list of keys as one label ("c/F5").
func JoinPretty(list []string) string {
	parts := make([]string, 0, len(list))
	for _, k := range list {
		parts = append(parts, Pretty(k))
	}
	return strings.Join(parts, "/")
}
