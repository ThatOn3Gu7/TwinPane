// Package editor selects an external text editor and checks whether a file
// is suitable for editing. It has no UI dependencies: the TUI runs the
// returned command through Process (tea.Exec).
package editor

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf8"

	"github.com/ThatOn3Gu7/TwinPane/internal/proc"
)

// Env abstracts the environment so resolution can be tested.
type Env struct {
	Getenv   func(string) string
	LookPath func(string) (string, error)
	GOOS     string
	// EvalSymlinks resolves links when detected editors are de-duplicated;
	// nil = no resolution.
	EvalSymlinks func(string) (string, error)
}

// SystemEnv returns the real process environment. LookPath is proc.LookPath,
// not os/exec.LookPath: the latter uses the faccessat2 system call, which
// Android's seccomp filter answers with SIGSYS (TwinPane crashed on Termux
// when e was pressed).
func SystemEnv() Env {
	return Env{Getenv: os.Getenv, LookPath: proc.LookPath, GOOS: runtime.GOOS, EvalSymlinks: filepath.EvalSymlinks}
}

// Editor is a resolved editor command.
type Editor struct {
	Path   string   // absolute path of the executable
	Args   []string // extra arguments placed before the file names
	Source string   // "$VISUAL", "$EDITOR", "detected" or "chosen"
	Name   string   // command as configured, for messages
	Label  string   // human-readable name for the chooser ("Neovim")
	Note   string   // set when $VISUAL/$EDITOR could not be used
	Single bool     // opens only one file per invocation
}

// Known describes an editor TwinPane can detect.
type Known struct {
	Cmd    string
	Label  string
	Args   []string
	GUI    bool // needs a graphical session (listed only when one exists)
	Single bool // accepts one file only
}

// KnownEditors lists editors in the order they are offered and in which the
// first available one is used when neither $VISUAL nor $EDITOR is usable.
func KnownEditors(goos string) []Known {
	term := []Known{
		{Cmd: "nvim", Label: "Neovim"},
		{Cmd: "vim", Label: "Vim"},
		{Cmd: "nano", Label: "GNU nano"},
		{Cmd: "micro", Label: "micro"},
		{Cmd: "hx", Label: "Helix"},
		{Cmd: "helix", Label: "Helix"},
		{Cmd: "emacs", Label: "Emacs (terminal)", Args: []string{"-nw"}},
		{Cmd: "kak", Label: "Kakoune"},
		{Cmd: "ne", Label: "ne"},
		{Cmd: "joe", Label: "Joe"},
		{Cmd: "jed", Label: "JED"},
		{Cmd: "mg", Label: "mg"},
		{Cmd: "mcedit", Label: "mcedit", Single: true},
		{Cmd: "vi", Label: "vi"},
		{Cmd: "editor", Label: "system default (editor)"},
	}
	gui := []Known{
		{Cmd: "code", Label: "VS Code", Args: []string{"--wait"}, GUI: true},
		{Cmd: "codium", Label: "VSCodium", Args: []string{"--wait"}, GUI: true},
		{Cmd: "subl", Label: "Sublime Text", Args: []string{"--wait"}, GUI: true},
		{Cmd: "gedit", Label: "gedit", Args: []string{"--wait"}, GUI: true},
		{Cmd: "kate", Label: "Kate", Args: []string{"--block"}, GUI: true},
	}
	if goos == "windows" {
		term = []Known{{Cmd: "nvim", Label: "Neovim"}, {Cmd: "vim", Label: "Vim"},
			{Cmd: "nano", Label: "GNU nano"}, {Cmd: "micro", Label: "micro"}, {Cmd: "hx", Label: "Helix"},
			{Cmd: "notepad", Label: "Notepad", Single: true}}
	}
	return append(term, gui...)
}

// Fallbacks lists the commands tried when neither $VISUAL nor $EDITOR is
// usable (for messages).
func Fallbacks(goos string) []string {
	var out []string
	for _, k := range KnownEditors(goos) {
		if !k.GUI {
			out = append(out, k.Cmd)
		}
	}
	return out
}

func hasGUI(env Env) bool {
	switch env.GOOS {
	case "windows", "darwin":
		return true
	case "android":
		return false
	}
	return env.Getenv("DISPLAY") != "" || env.Getenv("WAYLAND_DISPLAY") != ""
}

// fromVar resolves $VISUAL or $EDITOR. ok is false when the variable is
// unset; err is set when it is set but unusable.
func fromVar(env Env, v string) (ed Editor, ok bool, err error) {
	val := strings.TrimSpace(env.Getenv(v))
	if val == "" {
		return Editor{}, false, nil
	}
	fields, err := SplitArgs(val)
	if err != nil || len(fields) == 0 {
		return Editor{}, true, fmt.Errorf("$%s=%q cannot be parsed", v, val)
	}
	p, err := env.LookPath(fields[0])
	if err != nil {
		return Editor{}, true, fmt.Errorf("editor %q from $%s was not found", fields[0], v)
	}
	p = absPath(p)
	return Editor{Path: p, Args: fields[1:], Source: "$" + v, Name: val, Label: labelFor(filepath.Base(p), env.GOOS)}, true, nil
}

func labelFor(cmd string, goos string) string {
	cmd = strings.TrimSuffix(strings.ToLower(cmd), ".exe")
	for _, k := range KnownEditors(goos) {
		if k.Cmd == cmd {
			return k.Label
		}
	}
	return cmd
}

func absPath(p string) string {
	if a, err := filepath.Abs(p); err == nil {
		return a
	}
	return p
}

// Detect lists the usable editors: $VISUAL and $EDITOR first (if set and
// found), then the known editors found in $PATH (graphical ones only in a
// graphical session). Entries that resolve to the same program with the
// same arguments are listed once, under the first (highest-priority) name.
func Detect(env Env) []Editor {
	var out []Editor
	seen := map[string]bool{}
	add := func(ed Editor) {
		real := ed.Path
		if env.EvalSymlinks != nil {
			if r, err := env.EvalSymlinks(ed.Path); err == nil {
				real = r
			}
		}
		key := real + "\x00" + strings.Join(ed.Args, "\x00")
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, ed)
	}
	for _, v := range []string{"VISUAL", "EDITOR"} {
		if ed, ok, err := fromVar(env, v); ok && err == nil {
			add(ed)
		}
	}
	gui := hasGUI(env)
	for _, k := range KnownEditors(env.GOOS) {
		if k.GUI && !gui {
			continue
		}
		p, err := env.LookPath(k.Cmd)
		if err != nil {
			continue
		}
		add(Editor{Path: absPath(p), Args: append([]string(nil), k.Args...), Source: "detected",
			Name: k.Cmd, Label: k.Label, Single: k.Single})
	}
	return out
}

// Resolve picks the editor for the e key: $VISUAL, then $EDITOR, then the
// first detected editor. If a variable is set but unusable, the next
// candidate is used and Editor.Note says why, so the user learns about the
// broken setting without being blocked.
func Resolve(env Env) (Editor, error) {
	var notes []string
	for _, v := range []string{"VISUAL", "EDITOR"} {
		ed, ok, err := fromVar(env, v)
		if !ok {
			continue
		}
		if err == nil {
			ed.Note = strings.Join(notes, "; ")
			return ed, nil
		}
		notes = append(notes, err.Error())
	}
	for _, ed := range Detect(env) {
		if ed.Source == "detected" && !isGUIEditor(ed, env.GOOS) {
			ed.Note = strings.Join(notes, "; ")
			return ed, nil
		}
	}
	if len(notes) > 0 {
		return Editor{}, fmt.Errorf("%s, and no other editor was found (tried %s)", strings.Join(notes, "; "), strings.Join(Fallbacks(env.GOOS), ", "))
	}
	return Editor{}, fmt.Errorf("no editor found: set $VISUAL or $EDITOR (tried %s)", strings.Join(Fallbacks(env.GOOS), ", "))
}

func isGUIEditor(ed Editor, goos string) bool {
	for _, k := range KnownEditors(goos) {
		if k.Cmd == ed.Name {
			return k.GUI
		}
	}
	return false
}

// Display is the editor's name for messages and the chooser ("nvim").
func (e Editor) Display() string {
	if e.Name != "" && e.Source == "detected" {
		return e.Name
	}
	return filepath.Base(e.Path)
}

// Command builds the command that opens the files in the editor. The
// executable path is used as is (no $PATH lookup happens here).
func (e Editor) Command(files ...string) *exec.Cmd {
	args := append(append([]string(nil), e.Args...), files...)
	return &exec.Cmd{Path: e.Path, Args: append([]string{e.Path}, args...)}
}

// VimLike reports editors that accept -p (tabs) and -O (side by side).
func (e Editor) VimLike() bool {
	switch strings.TrimSuffix(strings.ToLower(filepath.Base(e.Path)), ".exe") {
	case "nvim", "vim", "gvim", "mvim":
		return true
	}
	return false
}

// SplitArgs splits a command line into fields, honouring single quotes,
// double quotes and backslash escapes (e.g. `code --wait`, `"my editor" -n`).
func SplitArgs(s string) ([]string, error) {
	var out []string
	var cur strings.Builder
	inField := false
	var quote rune
	escaped := false
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'' && runtime.GOOS != "windows":
			escaped = true
			inField = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
			inField = true
		case r == ' ' || r == '\t':
			if inField {
				out = append(out, cur.String())
				cur.Reset()
				inField = false
			}
		default:
			cur.WriteRune(r)
			inField = true
		}
	}
	if quote != 0 || escaped {
		return nil, errors.New("unterminated quote or escape")
	}
	if inField {
		out = append(out, cur.String())
	}
	return out, nil
}

// ErrIsDir is returned by Check for directories.
var ErrIsDir = errors.New("is a folder: only files can be edited")

// FileInfo is the result of Check.
type FileInfo struct {
	Path   string
	Size   int64
	Binary bool // content looks binary: the UI asks before opening
}

// Check verifies that path can be opened in a text editor. Directories,
// broken links and special files are refused; unreadable files are
// reported; likely-binary content is flagged.
func Check(path string) (FileInfo, error) {
	info := FileInfo{Path: path}
	fi, err := os.Stat(path)
	if err != nil {
		if lfi, lerr := os.Lstat(path); lerr == nil && lfi.Mode()&os.ModeSymlink != 0 {
			return info, fmt.Errorf("%s is a broken link", filepath.Base(path))
		}
		return info, err
	}
	if fi.IsDir() {
		return info, fmt.Errorf("%s %w", filepath.Base(path), ErrIsDir)
	}
	if !fi.Mode().IsRegular() {
		return info, fmt.Errorf("%s is not a regular file", filepath.Base(path))
	}
	info.Size = fi.Size()
	f, err := os.Open(path)
	if err != nil {
		return info, err
	}
	defer f.Close()
	buf := make([]byte, 8192)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return info, err
	}
	info.Binary = LooksBinary(buf[:n])
	return info, nil
}

// LooksBinary reports whether data (a file prefix) is probably not text: it
// contains a NUL byte, or more than 10% of it is invalid UTF-8 or control
// characters other than common whitespace/escape.
func LooksBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	bad := 0
	for i := 0; i < len(data); {
		r, size := utf8.DecodeRune(data[i:])
		switch {
		case r == 0:
			return true
		case r == utf8.RuneError && size == 1:
			// a multi-byte sequence cut off by the 8KB limit is not "bad"
			if len(data)-i >= utf8.UTFMax {
				bad++
			}
		case r < 0x20 && r != '\n' && r != '\r' && r != '\t' && r != '\f' && r != '\b' && r != 0x1b:
			bad++
		case r == 0x7f:
			bad++
		}
		i += size
	}
	return bad*10 > len(data)
}

// DescribeExit turns an editor exit error into a short message.
func DescribeExit(name string, err error) string {
	if err == nil {
		return ""
	}
	var xe *ExitError
	if errors.As(err, &xe) {
		if xe.Signal != 0 {
			return fmt.Sprintf("editor %s was terminated (%s): changes may not have been saved", name, xe.Signal)
		}
		return fmt.Sprintf("editor %s exited with status %d: changes may not have been saved", name, xe.Code)
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return fmt.Sprintf("editor %s exited with status %d: changes may not have been saved", name, ee.ExitCode())
	}
	return fmt.Sprintf("could not run editor %s: %v", name, err)
}
