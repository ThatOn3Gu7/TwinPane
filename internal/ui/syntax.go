package ui

import (
	"bytes"
	"strings"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/formatters"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"

	"github.com/ThatOn3Gu7/TwinPane/internal/textview"
)

// syntaxHighlightLines returns terminal-coloured lines for file types Chroma
// recognizes. The input is already bounded by textview.Defaults; controls and
// tabs are normalized before tokenization so rendered columns stay aligned
// with the viewer's wrapping and match-highlighting logic. Unknown formats,
// lexer failures, and formatting failures intentionally fall back to plain
// text.
func syntaxHighlightLines(filename string, lines []string) []string {
	if len(lines) == 0 {
		return nil
	}
	lexer := lexers.Match(filename)
	if lexer == nil || lexer.Config() == nil {
		return nil
	}
	name := strings.ToLower(lexer.Config().Name)
	if name == "plain text" || name == "plaintext" || name == "text only" {
		return nil
	}

	displayLines := make([]string, len(lines))
	for i, line := range lines {
		displayLines[i] = textview.Expand(line)
	}
	iterator, err := chroma.Coalesce(lexer).Tokenise(nil, strings.Join(displayLines, "\n"))
	if err != nil {
		return nil
	}
	formatter := formatters.TTY256
	style := styles.Get("monokai")
	if formatter == nil || style == nil {
		return nil
	}
	var out bytes.Buffer
	if err := formatter.Format(&out, style, iterator); err != nil {
		return nil
	}

	colored := strings.Split(out.String(), "\n")
	if len(colored) > len(displayLines) {
		colored = colored[:len(displayLines)] // Chroma may append a final LF.
	}
	for len(colored) < len(displayLines) {
		colored = append(colored, displayLines[len(colored)])
	}
	return colored
}
