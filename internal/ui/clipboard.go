package ui

import (
	"encoding/base64"
	"errors"
	"io"
	"os"
)

// TwinPane sets the terminal clipboard with OSC 52 instead of shelling out to
// xclip/wl-copy or requiring a clipboard server: it works over SSH, inside
// Termux and in most modern terminals, and needs no dependency or extra
// process. Terminals that do not implement OSC 52 ignore the sequence, so the
// caller reports the path in the status line as well.
//
// clipboardOut is a variable so tests can capture the sequence instead of
// writing escape bytes into the test log.
var clipboardOut io.Writer = os.Stdout

// osc52Sequence is the escape sequence that puts text on the clipboard.
func osc52Sequence(text string) string {
	return "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte(text)) + "\x07"
}

// copyToClipboard asks the terminal to copy text. It never writes to the
// screen, so it can run while TwinPane owns the terminal.
func copyToClipboard(text string) error {
	if text == "" {
		return errors.New("nothing to copy")
	}
	if clipboardOut == nil {
		return errors.New("no terminal to copy to")
	}
	if _, err := io.WriteString(clipboardOut, osc52Sequence(text)); err != nil {
		return err
	}
	return nil
}

// clipboardNote describes the mechanism for status messages and help.
const clipboardNote = "terminal clipboard"

// trimClipboardPath keeps status messages readable for very long paths.
func trimClipboardPath(p string) string {
	const limit = 60
	if len(p) <= limit {
		return p
	}
	head := p[:limit/3]
	return head + "…" + p[len(p)-(limit-len(head)-1):]
}
