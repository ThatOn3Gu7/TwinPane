package ui

import (
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/ThatOn3Gu7/TwinPane/internal/keybinding"
	"github.com/ThatOn3Gu7/TwinPane/internal/theme"
)

// lineInput is a minimal single-line text field used by dialogs (rename,
// typed delete confirmation). It accepts pasted text but drops control
// characters and newlines.
type lineInput struct {
	runes []rune
	pos   int // cursor position in runes
	// binds is the active key binding set (nil keeps the defaults). The
	// editing keys (the input section of keybinding.json) are looked up
	// here, so a rebound cursor key works in every text field.
	binds *keybinding.Set
}

func newLineInput(s string) *lineInput {
	r := []rune(s)
	return &lineInput{runes: r, pos: len(r)}
}

// newInput is the App-bound constructor: the field follows keybinding.json.
func (a *App) newInput(s string) *lineInput {
	in := newLineInput(s)
	in.binds = a.binds
	return in
}

// matches reports whether msg means the named input action.
func (in *lineInput) matches(action string, msg tea.KeyMsg) bool {
	if in.binds != nil {
		return in.binds.Matches(action, msg)
	}
	return keybinding.Default().Matches(action, msg)
}

func (in *lineInput) Value() string { return string(in.runes) }

// SetCursor places the cursor (clamped) at rune index i.
func (in *lineInput) SetCursor(i int) {
	if i < 0 {
		i = 0
	}
	if i > len(in.runes) {
		i = len(in.runes)
	}
	in.pos = i
}

func (in *lineInput) insert(rs []rune) {
	var clean []rune
	for _, r := range rs {
		if r == '\n' || r == '\r' || r == '\t' {
			r = ' '
		}
		if unicode.IsControl(r) {
			continue
		}
		clean = append(clean, r)
	}
	out := make([]rune, 0, len(in.runes)+len(clean))
	out = append(out, in.runes[:in.pos]...)
	out = append(out, clean...)
	out = append(out, in.runes[in.pos:]...)
	in.runes = out
	in.pos += len(clean)
}

// Update applies an editing key. It returns false if the key was not used.
func (in *lineInput) Update(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyRunes, tea.KeySpace:
		in.insert(msg.Runes)
		return true
	}
	switch {
	case in.matches("input.backspace", msg):
		if in.pos > 0 {
			in.runes = append(in.runes[:in.pos-1], in.runes[in.pos:]...)
			in.pos--
		}
	case in.matches("input.delete", msg):
		if in.pos < len(in.runes) {
			in.runes = append(in.runes[:in.pos], in.runes[in.pos+1:]...)
		}
	case in.matches("input.left", msg):
		in.SetCursor(in.pos - 1)
	case in.matches("input.right", msg):
		in.SetCursor(in.pos + 1)
	case in.matches("input.home", msg):
		in.pos = 0
	case in.matches("input.end", msg):
		in.pos = len(in.runes)
	case in.matches("input.deleteToStart", msg):
		in.runes = append([]rune(nil), in.runes[in.pos:]...)
		in.pos = 0
	case in.matches("input.deleteToEnd", msg):
		in.runes = in.runes[:in.pos]
	case in.matches("input.deleteWord", msg):
		i := in.pos
		for i > 0 && in.runes[i-1] == ' ' {
			i--
		}
		for i > 0 && in.runes[i-1] != ' ' {
			i--
		}
		in.runes = append(in.runes[:i], in.runes[in.pos:]...)
		in.pos = i
	default:
		return false
	}
	return true
}

// View renders the field exactly w cells wide, scrolling horizontally so the
// cursor stays visible.
func (in *lineInput) View(th theme.Theme, w int) string {
	if w < 2 {
		w = 2
	}
	// choose the first visible rune so that the cursor fits
	start := 0
	width := func(rs []rune) int {
		n := 0
		for _, r := range rs {
			n += runewidth.RuneWidth(r)
		}
		return n
	}
	for width(in.runes[start:in.pos])+1 > w {
		start++
	}
	var b strings.Builder
	used := 0
	for i := start; i < len(in.runes); i++ {
		rw := runewidth.RuneWidth(in.runes[i])
		if used+rw > w {
			break
		}
		ch := string(in.runes[i])
		if i == in.pos {
			b.WriteString(th.InputCursor.Render(ch))
		} else {
			b.WriteString(th.Input.Render(ch))
		}
		used += rw
	}
	if in.pos >= len(in.runes) && used < w {
		b.WriteString(th.InputCursor.Render(" "))
		used++
	}
	if used < w {
		b.WriteString(th.Input.Render(strings.Repeat(" ", w-used)))
	}
	return b.String()
}
