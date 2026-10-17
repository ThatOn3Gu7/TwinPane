package panel

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/theme"
)

func viewLines(p *Panel) []string {
	return strings.Split(ansi.Strip(p.View(theme.Default(), true)), "\n")
}

func hasSpinner(s string) bool {
	for _, f := range spinnerFrames {
		if strings.Contains(s, f) {
			return true
		}
	}
	return false
}

// While a folder loads, the path line shows an animated spinner and (after
// the first frame) the info line says what is loading and for how long;
// both disappear the moment the listing arrives.
func TestSpinnerWhileLoading(t *testing.T) {
	dir := tree(t, 3)
	p := newLoaded(t, dir)
	target := filepath.Join(dir, "beta")
	cmd := p.Load(target, "", LoadNavigate)

	lines := viewLines(p)
	if !strings.Contains(lines[1], spinnerFrames[0]) {
		t.Fatalf("path line has no spinner:\n%s", lines[1])
	}
	view := strings.Join(lines, "\n")
	if strings.Contains(view, " Loading ") { // (the temp folder's name contains "Loading")
		t.Fatalf("a load that may finish at once must not flash the info line:\n%s", view)
	}

	p.SpinnerTick()
	view = strings.Join(viewLines(p), "\n")
	if !strings.Contains(viewLines(p)[1], spinnerFrames[1]) {
		t.Fatalf("spinner did not advance:\n%s", view)
	}
	if !strings.Contains(view, spinnerFrames[1]+" Loading ") || !strings.Contains(view, "beta …") {
		t.Fatalf("info line should name the folder being loaded:\n%s", view)
	}
	if strings.Contains(view, "Esc cancels") || strings.Contains(view, "1s") {
		t.Fatalf("local load: no Esc hint, no seconds yet:\n%s", view)
	}

	// after a while: seconds, and the Esc hint when the load can be cancelled
	p.loadSince = time.Now().Add(-2500 * time.Millisecond)
	p.loadCancel = func() {}
	p.SpinnerTick()
	view = strings.Join(viewLines(p), "\n")
	if !strings.Contains(view, "beta … 2s · Esc cancels") {
		t.Fatalf("seconds / Esc hint missing:\n%s", view)
	}
	p.loadCancel = nil

	if err := apply(t, p, cmd); err != nil {
		t.Fatal(err)
	}
	view = strings.Join(viewLines(p), "\n")
	if hasSpinner(view) || strings.Contains(view, " Loading ") || p.pending != "" {
		t.Fatalf("spinner must disappear when the folder is loaded:\n%s", view)
	}
	if p.Path() != target {
		t.Fatalf("path %s", p.Path())
	}

	// ticks while idle change nothing
	p.SpinnerTick()
	if p.spinFrame != 1+1 || hasSpinner(strings.Join(viewLines(p), "\n")) {
		t.Fatalf("idle tick changed the spinner (frame %d)", p.spinFrame)
	}
}

// A new load restarts the spinner and its seconds, and its label replaces
// the previous one.
func TestSpinnerRestartsPerLoad(t *testing.T) {
	dir := tree(t, 1)
	p := newLoaded(t, dir)
	p.Load(filepath.Join(dir, "alpha"), "", LoadNavigate)
	p.loadSince = time.Now().Add(-5 * time.Second)
	p.SpinnerTick()
	p.SpinnerTick()
	cmd := p.Load(filepath.Join(dir, "gamma"), "", LoadNavigate)
	if p.spinFrame != 0 || p.spinFor != 0 {
		t.Fatalf("spinner not restarted: frame %d, %v", p.spinFrame, p.spinFor)
	}
	p.SpinnerTick()
	view := strings.Join(viewLines(p), "\n")
	if !strings.Contains(view, "gamma …") || strings.Contains(view, "alpha …") || strings.Contains(view, "5s") {
		t.Fatalf("label/seconds of the old load shown:\n%s", view)
	}
	if err := apply(t, p, cmd); err != nil {
		t.Fatal(err)
	}
}

// Every line keeps the exact panel width while loading, at every size and
// with a long folder name (the name is shortened from the left).
func TestSpinnerLinesKeepWidth(t *testing.T) {
	dir := tree(t, 2)
	long := filepath.Join(dir, strings.Repeat("very-long-folder-name-", 4))
	for w := 8; w <= 90; w++ {
		for _, h := range []int{5, 8, 14} {
			p := newLoaded(t, dir)
			p.SetSize(w, h)
			p.Load(long, "", LoadNavigate)
			p.loadSince = time.Now().Add(-3 * time.Second)
			p.loadCancel = func() {}
			for i := 0; i < 3; i++ {
				p.SpinnerTick()
				out := p.View(theme.Default(), true)
				for n, l := range strings.Split(out, "\n") {
					if lw := ansi.StringWidth(l); lw != w {
						t.Fatalf("w=%d h=%d line %d has width %d: %q", w, h, n, lw, ansi.Strip(l))
					}
				}
			}
			if w >= 60 && h >= 8 {
				view := ansi.Strip(p.View(theme.Default(), true))
				if !strings.Contains(view, "very-long-folder-name- … 3s · Esc cancels") {
					t.Fatalf("w=%d: the end of the name and the hints should stay visible:\n%s", w, view)
				}
			}
		}
	}
}

// In a narrow panel (a phone in portrait) the Esc hint is dropped before
// the seconds, and the spinner and the end of the name always remain.
func TestSpinnerNarrowPanelKeepsSeconds(t *testing.T) {
	dir := tree(t, 1)
	p := newLoaded(t, dir)
	p.beginLoad("drive: /Backups")
	p.loadSince = time.Now().Add(-2 * time.Second)
	p.loadCancel = func() {}
	p.SpinnerTick()
	for _, c := range []struct {
		w    int
		want string
	}{
		{80, "⠙ Loading drive: /Backups … 2s · Esc cancels"},
		{34, "⠙ Loading drive: /Backups … 2s"}, // innerW 32: no room for the Esc hint
		{24, "⠙ Loading …/Backups … 2s"},
		{20, "⠙ Loading …Backups …"},
	} {
		got := p.loadingText(c.w)
		if got != c.want {
			t.Errorf("w=%d: %q, want %q", c.w, got, c.want)
		}
		if ansi.StringWidth(got) > c.w {
			t.Errorf("w=%d: %q is too wide", c.w, got)
		}
	}
}

// An empty panel (the first listing of a remote) shows the loading line at
// once, without waiting for a tick.
func TestSpinnerEmptyPanelShowsAtOnce(t *testing.T) {
	p := New(0, nil)
	p.SetSize(60, 12)
	p.beginLoad("drive: /Backups")
	view := strings.Join(viewLines(p), "\n")
	if !strings.Contains(view, spinnerFrames[0]+" Loading drive: /Backups …") {
		t.Fatalf("empty panel should say what is loading:\n%s", view)
	}
}
