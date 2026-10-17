package ui

import (
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/mattn/go-runewidth"

	"github.com/ThatOn3Gu7/TwinPane/internal/theme"
)

// lineInput is a minimal single-line text field used by dialogs (rename,
// typed delete confirmation). It accepts pasted text but drops control
// characters and newlines.
type lineInput struct {
	runes []rune
	pos   int // cursor position in runes
}

func newLineInput(s string) *lineInput {
	r := []rune(s)
	return &lineInput{runes: r, pos: len(r)}
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
	switch msg.String() {
	case "backspace", "ctrl+h":
		if in.pos > 0 {
			in.runes = append(in.runes[:in.pos-1], in.runes[in.pos:]...)
			in.pos--
		}
	case "delete", "ctrl+d":
		if in.pos < len(in.runes) {
			in.runes = append(in.runes[:in.pos], in.runes[in.pos+1:]...)
		}
	case "left", "ctrl+b":
		in.SetCursor(in.pos - 1)
	case "right", "ctrl+f":
		in.SetCursor(in.pos + 1)
	case "home", "ctrl+a":
		in.pos = 0
	case "end", "ctrl+e":
		in.pos = len(in.runes)
	case "ctrl+u":
		in.runes = append([]rune(nil), in.runes[in.pos:]...)
		in.pos = 0
	case "ctrl+k":
		in.runes = in.runes[:in.pos]
	case "ctrl+w", "alt+backspace":
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
