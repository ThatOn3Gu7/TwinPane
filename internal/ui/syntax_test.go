package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/ThatOn3Gu7/TwinPane/internal/textview"
	"github.com/ThatOn3Gu7/TwinPane/internal/theme"
)

func TestSyntaxHighlightSupportedAndPlainFallback(t *testing.T) {
	for _, tc := range []struct {
		name  string
		lines []string
	}{
		{"sample.go", []string{"package main", "func main() { println(\"hello\") }"}},
		{"settings.json", []string{"{\"enabled\": true, \"count\": 3}"}},
		{"config.yaml", []string{"enabled: true", "name: twinpane"}},
	} {
		colored := syntaxHighlightLines(tc.name, tc.lines)
		if len(colored) != len(tc.lines) {
			t.Errorf("%s: got %d colored lines, want %d", tc.name, len(colored), len(tc.lines))
			continue
		}
		for i, line := range colored {
			if got := ansi.Strip(line); got != tc.lines[i] {
				t.Errorf("%s line %d: stripped text %q, want %q", tc.name, i, got, tc.lines[i])
			}
		}
		if !strings.Contains(colored[0], "\x1b[") {
			t.Errorf("%s was not syntax coloured", tc.name)
		}
	}
	if got := syntaxHighlightLines("unknown.extension", []string{"plain text"}); got != nil {
		t.Fatalf("unknown extension should use plain-text fallback, got %q", got)
	}
}

func TestViewerMatchHighlightOverridesSyntaxColor(t *testing.T) {
	a := &App{theme: theme.Default()}
	a.viewer = &viewer{
		doc:         &textview.Doc{Lines: []string{"needle plain"}},
		disp:        []string{"needle plain"},
		dispOK:      []bool{true},
		query:       "needle",
		syntaxLines: []string{"\x1b[38;5;100mneedle plain\x1b[0m"},
	}
	got := a.viewerText(0, "needle plain", 0)
	if !strings.Contains(got, a.theme.Match.Render("needle")) || !strings.Contains(ansi.Strip(got), "needle plain") {
		t.Fatalf("match highlight was not composed over syntax: %q", got)
	}
}

func TestViewerMatchHighlightSurvivesWrappedChunkBoundary(t *testing.T) {
	full := strings.Repeat("x", 38) + "needle" + " tail"
	chunk := full[40:48]
	a := &App{theme: theme.Default()}
	a.viewer = &viewer{
		doc:         &textview.Doc{Lines: []string{full}},
		disp:        []string{full},
		dispOK:      []bool{true},
		query:       "needle",
		syntaxLines: []string{"\x1b[38;5;100m" + full + "\x1b[0m"},
	}
	got := a.viewerText(0, chunk, 40)
	if ansi.Strip(got) != "edle tai" {
		t.Fatalf("visible wrapped chunk changed: %q", ansi.Strip(got))
	}
	if !strings.Contains(got, a.theme.Match.Render("edle")) {
		t.Fatalf("match intersecting the wrapped boundary was not highlighted: %q", got)
	}
}
