package theme

import (
	"math"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// rgb approximates an ANSI-256 colour: the 6×6×6 cube (16–231), the grey ramp
// (232–255) and the two ends of the basic range. The test only needs relative
// brightness, so the approximations are fine.
func rgb(c lipgloss.Color) (r, g, b float64) {
	n, err := strconv.Atoi(string(c))
	if err != nil { // a colour name or an empty value: treat as black
		return 0, 0, 0
	}
	switch {
	case n < 16:
		// The basic 16 are the terminal's own palette; only the black end is
		// used by the interface, so everything else counts as white.
		if n == 0 {
			return 0, 0, 0
		}
		return 1, 1, 1
	case n < 232:
		n -= 16
		step := func(v int) float64 {
			if v == 0 {
				return 0
			}
			return float64(55+40*v) / 255
		}
		return step(n / 36), step((n / 6) % 6), step(n % 6)
	default:
		v := float64(8+10*(n-232)) / 255
		return v, v, v
	}
}

// luminance is the WCAG relative luminance of a colour.
func luminance(c lipgloss.Color) float64 {
	r, g, b := rgb(c)
	lin := func(v float64) float64 {
		if v <= 0.03928 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// contrast is the WCAG contrast ratio between two colours.
func contrast(a, b lipgloss.Color) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// The background each kind of theme assumes: the interface draws ordinary
// text straight on the terminal's own background.
var (
	darkBackground  lipgloss.Color = "234"
	lightBackground lipgloss.Color = "231"
)

func backgroundOf(th Theme) lipgloss.Color {
	if th.scheme.light {
		return lightBackground
	}
	return darkBackground
}

// TestPresetNames checks the list the settings screen cycles through.
func TestPresetNames(t *testing.T) {
	names := PresetNames()
	if len(names) < 6 {
		t.Fatalf("only %d presets", len(names))
	}
	if names[0] != "default" {
		t.Fatalf("default must stay first, got %q", names[0])
	}
	seen := map[string]bool{}
	for _, n := range names {
		if n == "" || n != strings.ToLower(n) || strings.ContainsAny(n, " \t") {
			t.Fatalf("preset name %q is not a simple lower-case word", n)
		}
		if seen[n] {
			t.Fatalf("preset %q is listed twice", n)
		}
		seen[n] = true
	}
}

// TestPresetsBuild checks every listed preset resolves, and that an unknown
// name is reported instead of silently becoming something else.
func TestPresetsBuild(t *testing.T) {
	for _, name := range PresetNames() {
		th, ok := Preset(name)
		if !ok {
			t.Fatalf("%s does not build", name)
		}
		if th.Accent == "" || th.BorderOn != th.Accent || th.HelpTitle.GetForeground() == nil {
			t.Fatalf("%s has no accent", name)
		}
		if th.scheme.text == "" || th.scheme.ok == "" {
			t.Fatalf("%s has no scheme", name)
		}
	}
	if _, ok := Preset("nonsense"); ok {
		t.Fatal("an unknown preset must not resolve")
	}
	if _, ok := Preset(""); ok {
		t.Fatal("an empty preset must not resolve")
	}
}

// TestDefaultIsTheHistoricLook pins the default preset's identity: the blue
// accent and the greys of the original theme.
func TestDefaultIsTheHistoricLook(t *testing.T) {
	th := Default()
	if th.Accent != "75" || th.Muted != "244" || th.BorderOff != "239" {
		t.Fatalf("default accent/muted/border = %s/%s/%s", th.Accent, th.Muted, th.BorderOff)
	}
	if th.CursorActive.GetBackground() == lipgloss.Color("") {
		t.Fatal("default has no cursor background")
	}
	if th.scheme.light {
		t.Fatal("the default look is a dark theme")
	}
}

// TestPresetsAreReadable is the reason the light scheme exists: on the
// background each preset assumes, text, muted text, the accent and the
// coloured surfaces must all stay legible.
func TestPresetsAreReadable(t *testing.T) {
	for _, name := range PresetNames() {
		th, _ := Preset(name)
		bg := backgroundOf(th)
		check := func(what string, fg, on lipgloss.Color, want float64) {
			t.Helper()
			if got := contrast(fg, on); got < want {
				t.Errorf("%s: %s contrast %.2f (fg %s on %s), want %.1f", name, what, got, fg, on, want)
			}
		}
		cursorBg := th.CursorActive.GetBackground().(lipgloss.Color)
		check("text", th.scheme.text, bg, 4.5)
		check("muted", th.scheme.muted, bg, 3)
		check("accent", th.Accent, bg, 3)
		check("hint-bar description", th.scheme.soft, bg, 4.5)
		check("hint-bar chip", th.scheme.chipFg, th.scheme.chipBg, 4.5)
		check("cursor row", th.scheme.onCursor, cursorBg, 4.5)
		check("path bar", th.scheme.onAccent, th.Accent, 4.5)
		check("unfocused path bar", th.scheme.pathOffFg, th.scheme.pathOffBg, 4.5)
		check("text field", th.scheme.inputFg, th.scheme.inputBg, 4.5)
		check("button", th.scheme.buttonFg, th.scheme.buttonBg, 4.5)
		check("selected row", th.scheme.text, th.scheme.selectedBg, 4.5)
		check("muted text on a selected row", th.scheme.selectedFg, th.scheme.selectedBg, 3)
		check("success", th.scheme.ok, bg, 3)
		check("warning", th.scheme.warn, bg, 3)
		check("error", th.scheme.err, bg, 3)
		check("viewer match", th.scheme.onWarn, th.scheme.warn, 4.5)
		check("path in a dialog", th.DialogPath.GetForeground().(lipgloss.Color), bg, 3)
		check("scroll thumb", th.ScrollThumb.GetForeground().(lipgloss.Color), bg, 3)
		check("progress bar", th.ProgressFill.GetForeground().(lipgloss.Color), bg, 3)
	}
}

// TestLightPresetsUseTheLightScheme keeps the two halves of the list honest:
// a preset marked light must be built from the light greys.
func TestLightPresetsUseTheLightScheme(t *testing.T) {
	light := 0
	for _, name := range PresetNames() {
		th, _ := Preset(name)
		if !th.scheme.light {
			continue
		}
		light++
		if th.scheme.text != lightScheme.text || th.scheme.muted != lightScheme.muted {
			t.Fatalf("%s is marked light but uses the dark greys", name)
		}
		if luminance(th.scheme.text) > luminance(lightBackground) {
			t.Fatalf("%s: light theme text is lighter than its background", name)
		}
	}
	if light == 0 {
		t.Fatal("no light presets")
	}
}

// TestSelectedRow checks the helper that keeps marked rows readable: muted
// text is switched to the scheme's selected colour, ordinary text keeps its
// own, and the selection background is applied either way.
func TestSelectedRow(t *testing.T) {
	for _, name := range PresetNames() {
		th, _ := Preset(name)
		selectedBg := th.Selected.GetBackground().(lipgloss.Color)

		muted := th.SelectedRow(lipgloss.NewStyle().Foreground(th.scheme.dim))
		if got := muted.GetForeground().(lipgloss.Color); got != th.scheme.selectedFg {
			t.Errorf("%s: muted text on a selected row = %s, want %s", name, got, th.scheme.selectedFg)
		}
		if got := muted.GetBackground().(lipgloss.Color); got != selectedBg {
			t.Errorf("%s: selection background missing", name)
		}

		plain := th.SelectedRow(lipgloss.NewStyle().Foreground(th.scheme.text))
		if got := plain.GetForeground().(lipgloss.Color); got != th.scheme.text {
			t.Errorf("%s: ordinary text changed colour on selection: %s", name, got)
		}
	}

	// A Theme built as a literal (tests, embedders) still behaves as before.
	literal := Theme{Selected: lipgloss.NewStyle().Background(lipgloss.Color("240"))}
	got := literal.SelectedRow(lipgloss.NewStyle().Foreground(lipgloss.Color("243")))
	if fg := got.GetForeground().(lipgloss.Color); fg != darkScheme.selectedFg {
		t.Fatalf("literal theme muted text = %s, want %s", fg, darkScheme.selectedFg)
	}
}
